package setup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/agents"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

type setupFixture struct {
	dir      string
	services Services
}

func inputBytes(t testing.TB, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func reviewFor(t testing.TB, f *setupFixture, operation string, value any) ReviewDocument {
	t.Helper()
	doc, err := Review(context.Background(), f.services, f.dir, operation, inputBytes(t, value))
	if err != nil {
		t.Fatalf("review %s: %v; cause=%v", operation, err, errors.Unwrap(err))
	}
	return doc
}
func applyFor(t testing.TB, f *setupFixture, doc ReviewDocument) []Receipt {
	t.Helper()
	out, err := Apply(context.Background(), f.services, f.dir, doc, doc.ReviewSHA256)
	if err != nil {
		t.Fatalf("apply %s: %v; cause=%v", doc.Operation, err, errors.Unwrap(err))
	}
	return out
}
func newSetupFixture(t testing.TB) *setupFixture {
	t.Helper()
	f := &setupFixture{dir: filepath.Join(t.TempDir(), "colony")}
	applyFor(t, f, reviewFor(t, f, "colony create", struct{}{}))
	f.open(t)
	t.Cleanup(func() { f.close(t) })
	return f
}
func (f *setupFixture) open(t testing.TB) {
	t.Helper()
	var err error
	f.services.Store, _, err = ledgerstore.Open(f.dir, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	f.services.Agents, _, err = agents.OpenPaid(context.Background(), f.services.Store, f.dir, agents.DefaultLimits(), agents.PaidOptions{Limits: agents.DefaultPaidLimits()})
	if err != nil {
		t.Fatal(err)
	}
	f.services.Workspace, _, err = workspace.Open(context.Background(), f.services.Store, f.dir, workspace.Config{Capacity: &workspace.CapacityProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 2 << 20, CheckpointsPerPair: 4, GenerationWindow: 4}})
	if err != nil {
		t.Fatal(err)
	}
}
func (f *setupFixture) close(t testing.TB) {
	t.Helper()
	for _, close := range []func() error{f.services.Workspace.Close, f.services.Agents.Close, f.services.Store.Close} {
		if err := close(); err != nil {
			t.Error(err)
		}
	}
}
func contextInput() ContextInput {
	return ContextInput{RouteID: uuid.NewV7(), ExpectedRouteVersion: 1, CredentialScopeID: uuid.NewV7(), MaxOutputTokens: 4096}
}
func addAgent(t testing.TB, f *setupFixture) (ledger.Binding, ReviewDocument) {
	t.Helper()
	doc := reviewFor(t, f, "agent create", AgentInput{Name: "Scout", Instructions: []byte("# Exact instructions\r\nNo changes.\x00"), Settings: contextInput()})
	applyFor(t, f, doc)
	return ledger.Binding{AgentID: doc.Steps[0].Target.ID, ContextID: doc.Steps[1].Target.ID}, doc
}
func requireCode(t testing.TB, err error, code string) {
	t.Helper()
	var failure *Failure
	if !errors.As(err, &failure) || failure.Code != code || failure.FixCommand == "" {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestSetupColonyCreation(t *testing.T) {
	t.Parallel()
	f := &setupFixture{dir: filepath.Join(t.TempDir(), "colony")}
	doc := reviewFor(t, f, "colony create", struct{}{})
	if _, err := Apply(context.Background(), f.services, f.dir, doc, ledger.Digest{}); err == nil {
		t.Fatal("apply without approval")
	} else {
		requireCode(t, err, "approval_required")
	}
	if _, err := os.Stat(f.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("review/approval refusal created colony: %v", err)
	}
	first := applyFor(t, f, doc)
	if got := applyFor(t, f, doc); !reflect.DeepEqual(got, first) {
		t.Fatal("duplicate changed receipt")
	}
	other := reviewFor(t, f, "colony create", struct{}{})
	_, err := Apply(context.Background(), f.services, f.dir, other, other.ReviewSHA256)
	requireCode(t, err, "colony_exists")
	_, err = Apply(context.Background(), f.services, filepath.Join(t.TempDir(), "elsewhere"), doc, doc.ReviewSHA256)
	requireCode(t, err, "review_changed")
	store, _, err := ledgerstore.Open(f.dir, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state, err := Load(context.Background(), store)
	if err != nil || !state.Managed || state.ColonyID != doc.ColonyID || state.ThroughSequence != first[0].Sequence {
		t.Fatalf("creation replay: %v", err)
	}
	_, err = LoadConfiguration(context.Background(), store, nil)
	requireCode(t, err, "setup_required")
	_, err = LoadConfiguration(context.Background(), store, []byte{})
	requireCode(t, err, "mixed_configuration")
}

func TestSetupWorkspaceRegistration(t *testing.T) {
	// Inspect intentionally consumes the host Git environment. This test owns
	// one isolated process environment; it therefore must not run in parallel.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "missing-config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_ATTR_NOSYSTEM", "1")
	f := newSetupFixture(t)
	repo := filepath.Join(t.TempDir(), "repository")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
	}
	git("init", "--initial-branch=main", repo)
	git("-C", repo, "config", "core.autocrlf", "false")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("-C", repo, "add", "--", "base.txt")
	git("-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgSign=false", "commit", "-m", "base")
	git("-C", repo, "checkout", "--detach")
	doc := reviewFor(t, f, "workspace register", WorkspaceInput{Repository: repo})
	git("-C", repo, "config", "core.autocrlf", "true")
	before := f.services.Store.Durable()
	_, err := Apply(context.Background(), f.services, f.dir, doc, doc.ReviewSHA256)
	requireCode(t, err, "stale_proposal")
	if f.services.Store.Durable() != before {
		t.Fatal("stale proposal mutated ledger")
	}
	doc = reviewFor(t, f, "workspace register", WorkspaceInput{Repository: repo})
	first := applyFor(t, f, doc)
	id := first[0].Target.ID
	git("-C", repo, "config", "core.autocrlf", "false")
	update := reviewFor(t, f, "workspace inputs", WorkspaceInput{Repository: repo, WorkspaceID: id})
	applyFor(t, f, update)
	before = f.services.Store.Durable()
	if got := applyFor(t, f, doc); !reflect.DeepEqual(got, first) || before != f.services.Store.Durable() {
		t.Fatal("old registration retry did not return original receipt after input revision")
	}
	list, err := f.services.Workspace.ListWorkspaces()
	if err != nil || len(list) != 1 || list[0].ID != id || list[0].Revision != 2 {
		t.Fatalf("workspace identity/revision: %v", err)
	}
	stale := update
	stale.Steps = append([]Step(nil), update.Steps...)
	stale.Steps[0].CommandID = uuid.NewV7()
	stale.Steps[0].ExpectedRevision = 2
	stale.ReviewSHA256, err = reviewDigest(stale)
	if err != nil {
		t.Fatal(err)
	}
	applyFor(t, f, stale)
	stale.Steps[0].CommandID = uuid.NewV7()
	stale.Steps[0].ExpectedRevision = 1
	stale.ReviewSHA256, _ = reviewDigest(stale)
	_, err = Apply(context.Background(), f.services, f.dir, stale, stale.ReviewSHA256)
	requireCode(t, err, "stale_revision")
}

func TestSetupExactAgentAndContext(t *testing.T) {
	t.Parallel()
	f := newSetupFixture(t)
	binding, doc := addAgent(t, f)
	page, err := f.services.Agents.ReadPrivate(context.Background(), agents.PrivateRead{ReadBinding: agents.ReadBinding{Binding: agents.Binding(binding), Scope: "agent"}, Slot: "instructions", Revision: 1, MaxBytes: 1 << 20})
	if err != nil || !bytes.Equal(page.Bytes, doc.Artifacts[0].Data) {
		t.Fatalf("instructions changed: %v", err)
	}
	bound, err := f.services.Agents.BindAgent(context.Background(), agents.Binding(binding))
	if err != nil {
		t.Fatal(err)
	}
	grantID := uuid.NewV7()
	read := agents.ScopedPrivateRead{CommandID: uuid.NewV7(), GrantID: grantID, Scope: "agent", Slot: "instructions", Revision: 1, MaxBytes: 1024}
	if _, err := bound.ReadPrivate(context.Background(), read); !errors.Is(err, agents.ErrDenied) {
		t.Fatalf("setup invented authority: %v", err)
	}
	limits := ledger.Capacity{TurnSlots: 1, LocalSlots: 1, BufferBytes: 1 << 20, LocalUnits: 100, MaxOperationBytes: 1 << 20}
	for _, scope := range []ledger.ScopeKey{{Kind: "colony"}, {Kind: "agent", ID: binding.AgentID}} {
		applyFor(t, f, reviewFor(t, f, "capacity configure", CapacityInput{Origin: binding, Scope: scope, Limits: limits}))
	}
	grant := GrantInput{Origin: binding, Spec: ledger.GrantIssueArguments{GrantID: grantID, Grantee: binding, Operations: []string{"private.read"}, Resources: []ledger.Resource{{Kind: "context", Binding: binding, Scope: "agent", Slots: []string{"instructions"}}}, DataClass: "private", Recipients: []ledger.Binding{binding}, Limits: limits, Lifetime: "until_revoked"}}
	tamperedGrant := bytes.Replace(inputBytes(t, grant), []byte(`"spec":{`), []byte(`"spec":{"authority_version":1,`), 1)
	beforeGrant := f.services.Store.Durable()
	_, err = Review(context.Background(), f.services, f.dir, "grant issue", tamperedGrant)
	requireCode(t, err, "review_invalid")
	if f.services.Store.Durable() != beforeGrant {
		t.Fatal("invalid grant review changed authority")
	}
	applyFor(t, f, reviewFor(t, f, "grant issue", grant))
	authorized, err := bound.ReadPrivate(context.Background(), read)
	if err != nil || !bytes.Equal(authorized.Page.Bytes, doc.Artifacts[0].Data) {
		t.Fatalf("explicit reviewed authority did not permit the exact read: %v", err)
	}
	f.close(t)
	f.open(t)
	configuration, err := LoadConfiguration(context.Background(), f.services.Store, nil)
	if err != nil || len(configuration.Contexts) != 1 || configuration.Contexts[0].ContextID != binding.ContextID {
		t.Fatalf("managed replay: %v", err)
	}
	before := f.services.Store.Durable()
	first := applyFor(t, f, doc)
	if before != f.services.Store.Durable() || first[1].Revision != 1 {
		t.Fatal("agent duplicate created another context")
	}
	changed := doc
	changed.Artifacts = append([]Artifact(nil), doc.Artifacts...)
	changed.Artifacts[0].Data = []byte("unreviewed")
	_, err = Apply(context.Background(), f.services, f.dir, changed, changed.ReviewSHA256)
	requireCode(t, err, "review_changed")
	if f.services.Store.Durable() != before {
		t.Fatal("changed instructions wrote native effect")
	}
	_, err = Review(context.Background(), f.services, f.dir, "agent create", []byte(`{"name":"unsafe","name":"other"}`))
	requireCode(t, err, "review_invalid")
	_, err = Review(context.Background(), f.services, f.dir, "agent create", append(inputBytes(t, AgentInput{Name: "Other", Instructions: []byte("x"), Settings: contextInput()}), []byte(` null`)...))
	requireCode(t, err, "review_invalid")
	work := contextInput()
	work.DisplayTitle = "Work on the declared assignment"
	task, assignment := uuid.NewV7(), uuid.NewV7()
	work.TaskBinding = &ledger.TaskBinding{TaskID: task, AssignmentID: assignment, Generation: 1}
	workDoc := reviewFor(t, f, "context create", ContextCreateInput{AgentID: binding.AgentID, Settings: work})
	applyFor(t, f, workDoc)
	taskDoc := reviewFor(t, f, "task create", TaskInput{ContextID: workDoc.Steps[0].Target.ID, Description: []byte("Deliver the named result."), AcceptanceSHA256: ledger.Digest(sha256.Sum256([]byte("accepted result"))), Dependencies: []uuid.UUID{}, Audience: []uuid.UUID{binding.AgentID}})
	applyFor(t, f, taskDoc)
	taskView, err := f.services.Agents.TaskStatus(context.Background(), agents.TaskLookup{TaskID: task})
	if err != nil || taskView.Task.TaskID != task || taskView.Task.Assignment.AssignmentID != assignment {
		t.Fatalf("native task assignment: %v", err)
	}
	configured := reviewFor(t, f, "context configure", ContextConfigureInput{ContextID: workDoc.Steps[1].Target.ID, Settings: work})
	applyFor(t, f, configured)
	stale := configured
	stale.Steps = append([]Step(nil), configured.Steps...)
	stale.Steps[0].CommandID = uuid.NewV7()
	stale.ReviewSHA256, _ = reviewDigest(stale)
	_, err = Apply(context.Background(), f.services, f.dir, stale, stale.ReviewSHA256)
	requireCode(t, err, "stale_revision")
	path := filepath.Join(t.TempDir(), "review.json")
	if err := WriteReview(path, doc); err != nil {
		t.Fatal(err)
	}
	if err := WriteReview(path, doc); err == nil {
		t.Fatal("review overwritten")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeReview(encoded)
	if err != nil || decoded.ReviewSHA256 != doc.ReviewSHA256 {
		t.Fatalf("review file: %v", err)
	}
}

func syntheticProfile(t testing.TB, expires time.Time) []byte {
	t.Helper()
	now := time.Now().Add(-time.Minute).UTC().UnixNano()
	p := providers.Profile{Schema: 1, CapabilityVersion: "synthetic-capability", TariffVersion: "synthetic-tariff", Provider: "anthropic", Authority: "api.anthropic.com", Method: "POST", Path: "/v1/messages", APIVersion: providers.APIVersion, ModelID: providers.Haiku, Region: "global", RequestedTier: "standard_only", AllowedServedTiers: []string{"standard"}, Speed: "standard", ContextMax: 200000, InputMax: 196000, OutputMax: 8192, UsageSchema: "anthropic-messages-text/v1", CacheClasses: []string{providers.CacheRead, providers.CacheWrite1h, providers.CacheWrite5m}, ToolChoices: []string{"auto", "none"}, NoChargeRules: []string{}, Tariff: providers.Tariff{Currency: "USD"}, VerifiedUTCNS: now, ExpiresUTCNS: expires.UTC().UnixNano()}
	for _, class := range []string{providers.CacheRead, providers.CacheWrite1h, providers.CacheWrite5m, providers.Uncached, providers.Generated} {
		p.Tariff.Rates = append(p.Tariff.Rates, providers.Rate{Class: class, Numerator: "1", Denominator: "1"})
	}
	for _, topic := range []string{"endpoint_api_version", "inclusive_usage", "lifecycle", "local_tool_billing", "model_limits", "no_charge_billing", "tariff", "tier_region_speed"} {
		p.Sources = append(p.Sources, providers.SourceQuote{Topic: topic, URL: "https://platform.claude.com/synthetic/" + topic, RetrievedUTCNS: now, Quote: "Synthetic test facts, not provider pricing evidence."})
	}
	raw, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSetupSpendAndRoutes(t *testing.T) {
	// This row owns an environment credential source; no parallel process state.
	sourceName := "DWARF_SETUP_SYNTHETIC_" + strings.ReplaceAll(uuid.NewV7().String(), "-", "_")
	t.Setenv(sourceName, "synthetic-credential-value")
	f := newSetupFixture(t)
	origin, agentDoc := addAgent(t, f)
	var settings ledger.SetupContextArguments
	if err := json.Unmarshal(agentDoc.Steps[1].Arguments, &settings); err != nil {
		t.Fatal(err)
	}
	routeInput := RouteInput{ContextID: origin.ContextID, Enabled: true, Profile: syntheticProfile(t, time.Now().Add(24*time.Hour))}
	route := reviewFor(t, f, "route set", routeInput)
	applyFor(t, f, route)
	snapshot, err := f.services.Agents.OwnerSetupSnapshot(context.Background())
	if err != nil || snapshot.Routes[settings.RouteID].Profile.Authority != "api.anthropic.com" || snapshot.Routes[settings.RouteID].CredentialScopeID != settings.CredentialScopeID {
		t.Fatalf("route: %v", err)
	}
	for _, scope := range []ledger.PaidScope{{Kind: "colony"}, {Kind: "agent", ID: origin.AgentID}} {
		spend := reviewFor(t, f, "spend set", SpendInput{Origin: origin, Scope: scope, Enabled: true, Mode: "finite", USD: "0.000000", PeriodKind: "lifetime"})
		first := applyFor(t, f, spend)
		if got := applyFor(t, f, spend); !reflect.DeepEqual(got, first) {
			t.Fatal("spend duplicate changed receipt")
		}
	}
	wrongPeriod := reviewFor(t, f, "spend set", SpendInput{Origin: origin, Scope: ledger.PaidScope{Kind: "agent", ID: origin.AgentID}, Enabled: true, Mode: "unlimited", USD: "", PeriodKind: "utc_day"})
	_, err = Apply(context.Background(), f.services, f.dir, wrongPeriod, wrongPeriod.ReviewSHA256)
	requireCode(t, err, "spend_period_mismatch")
	badProfile := routeInput
	badProfile.Profile = syntheticProfile(t, time.Now().Add(-time.Second))
	_, err = Review(context.Background(), f.services, f.dir, "route set", inputBytes(t, badProfile))
	requireCode(t, err, "profile_expired")
	withoutMode := bytes.Replace(inputBytes(t, SpendInput{Origin: origin, Scope: ledger.PaidScope{Kind: "colony"}, Mode: "finite", PeriodKind: "lifetime", USD: "1"}), []byte(`"mode":"finite",`), nil, 1)
	_, err = Review(context.Background(), f.services, f.dir, "spend set", withoutMode)
	requireCode(t, err, "review_invalid")
	before := f.services.Store.Durable()
	credential := reviewFor(t, f, "credential bind", CredentialInput{ContextID: origin.ContextID, Provider: "anthropic", SourceKind: "environment", SourceLocator: sourceName + "_MISSING"})
	_, err = Apply(context.Background(), f.services, f.dir, credential, credential.ReviewSHA256)
	requireCode(t, err, "credential_missing")
	if f.services.Store.Durable() != before {
		t.Fatal("missing credential wrote binding")
	}
	credential = reviewFor(t, f, "credential bind", CredentialInput{ContextID: origin.ContextID, Provider: "anthropic", SourceKind: "environment", SourceLocator: sourceName})
	if bytes.Contains(inputBytes(t, credential), []byte("synthetic-credential-value")) {
		t.Fatal("review contains credential bytes")
	}
	bound := applyFor(t, f, credential)
	if got := applyFor(t, f, credential); !reflect.DeepEqual(got, bound) {
		t.Fatal("binding retry rotated the credential")
	}
	scope, err := f.services.Agents.CredentialScopeStatus(context.Background(), agents.CredentialScopeLookup{ScopeID: settings.CredentialScopeID})
	if err != nil || len(scope.ActiveVersions) != 1 {
		t.Fatalf("credential binding: %v", err)
	}
	revoke := reviewFor(t, f, "credential revoke", CredentialRevokeInput{Origin: origin, ScopeID: settings.CredentialScopeID, CredentialVersion: scope.ActiveVersions[0]})
	revoked := applyFor(t, f, revoke)
	if got := applyFor(t, f, revoke); !reflect.DeepEqual(got, revoked) {
		t.Fatal("revocation retry changed receipt")
	}
	scope, err = f.services.Agents.CredentialScopeStatus(context.Background(), agents.CredentialScopeLookup{ScopeID: settings.CredentialScopeID})
	if err != nil || len(scope.ActiveVersions) != 0 {
		t.Fatalf("credential was not revoked: %v", err)
	}
}

func TestSetupReceiptRecovery(t *testing.T) {
	// This deterministic crash child exits after the acknowledged native step.
	// The parent waits for its exit, then opens the real colony and reapplies.
	if reviewPath := os.Getenv("DWARF_SETUP_CRASH_REVIEW"); reviewPath != "" {
		raw, err := os.ReadFile(reviewPath)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := DecodeReview(raw)
		if err != nil {
			t.Fatal(err)
		}
		f := &setupFixture{dir: doc.Directory}
		f.open(t)
		f.services.afterNative = func(op string) {
			if op == "agent.register" {
				os.Exit(23)
			}
		}
		applyFor(t, f, doc)
		t.Fatal("crash boundary was not reached")
	}
	t.Parallel()
	f := newSetupFixture(t)
	doc := reviewFor(t, f, "agent create", AgentInput{Name: "Recover", Instructions: []byte("Exact crash-recovery instructions"), Settings: contextInput()})
	path := filepath.Join(t.TempDir(), "review.json")
	if err := WriteReview(path, doc); err != nil {
		t.Fatal(err)
	}
	f.close(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestSetupReceiptRecovery$")
	cmd.Env = append(os.Environ(), "DWARF_SETUP_CRASH_REVIEW="+path)
	out, err := cmd.CombinedOutput()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 23 {
		t.Fatalf("crash child: %v: %s", err, out)
	}
	f.open(t)
	state, err := Load(context.Background(), f.services.Store)
	if err != nil || len(state.Contexts) != 0 || len(state.nativeContexts) != 1 {
		t.Fatalf("partial native registration: %v", err)
	}
	first := applyFor(t, f, doc)
	before := f.services.Store.Durable()
	if got := applyFor(t, f, doc); !reflect.DeepEqual(got, first) || f.services.Store.Durable() != before {
		t.Fatal("recovery repeated native effect")
	}
	changed := doc
	changed.Steps = append([]Step(nil), doc.Steps...)
	var settings ledger.SetupContextArguments
	if err := json.Unmarshal(changed.Steps[1].Arguments, &settings); err != nil {
		t.Fatal(err)
	}
	settings.MaxOutputTokens++
	changed.Steps[1].Arguments = inputBytes(t, settings)
	changed.ReviewSHA256, _ = reviewDigest(changed)
	_, err = Apply(context.Background(), f.services, f.dir, changed, changed.ReviewSHA256)
	requireCode(t, err, "command_mismatch")
	if !errors.Is(err, ledgerstore.ErrCommandMismatch) || f.services.Store.Durable() != before {
		t.Fatal("mismatch classification or ledger changed")
	}
	goldenID, err := uuid.Parse("019a1500-0000-7000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	golden, err := load(context.Background(), filepath.Join("..", "..", "..", "contract", "ledger", "testdata", "f3", "150", "log"), goldenID, 42)
	if err != nil || !golden.Managed || len(golden.Contexts) != 2 || golden.StartupRevision != 1 || golden.ChecksRevision != 1 {
		t.Fatalf("frozen history, including historical policy bounds: %v", err)
	}
	state, err = Load(context.Background(), f.services.Store)
	if err != nil {
		t.Fatal(err)
	}
	host := []byte(`{"schema":1}`)
	hostDigest := ledger.Digest(sha256.Sum256(host))
	startup := ledger.SetupSubmission{Schema: 12, Actor: "owner", Operation: "startup.set", Target: ledger.SetupTarget{Kind: "colony", ID: state.ColonyID}, Arguments: ledger.SetupStartupArguments{SettingsSHA256: hostDigest, SettingsBytes: int64(len(host))}}
	if _, err := submitSetup(context.Background(), f.services.Store, state, uuid.NewV7(), startup, map[ledger.Digest][]byte{hostDigest: host}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfiguration(context.Background(), f.services.Store, nil)
	if err != nil || !bytes.Equal(loaded.HostSettings, host) {
		t.Fatalf("pinned host-settings identity: %v", err)
	}
	state, err = Load(context.Background(), f.services.Store)
	if err != nil {
		t.Fatal(err)
	}
	checks := []byte("synthetic check body")
	checkDigest := ledger.Digest(sha256.Sum256(checks))
	checkSub := ledger.SetupSubmission{Schema: 12, Actor: "owner", Operation: "checks.publish", Target: ledger.SetupTarget{Kind: "colony", ID: state.ColonyID}, Arguments: ledger.SetupChecksArguments{BodySetSHA256: bodySetDigest([]ledger.Digest{checkDigest}), BodyCount: 1}}
	if _, err := submitSetup(context.Background(), f.services.Store, state, uuid.NewV7(), checkSub, map[ledger.Digest][]byte{checkDigest: checks}); err != nil {
		t.Fatal(err)
	}
	state, err = Load(context.Background(), f.services.Store)
	if err != nil || state.ChecksRevision != 1 {
		t.Fatalf("check body-set replay: %v", err)
	}
	badID := uuid.NewV7()
	sub := setupSubmission(doc.Steps[1], settings)
	sub.ExpectedRevision = 1
	encoded, err := sub.Encode()
	if err != nil {
		t.Fatal(err)
	}
	digest := ledger.Digest(sha256.Sum256(encoded))
	command, _ := (ledger.Command{CommandID: badID, SubmissionSHA256: digest}).Encode()
	applied, _ := (ledger.RuntimeSetupApplied{Submission: sub}).Encode()
	refused, _ := (ledger.RuntimeSetupReceipt{CommandID: badID, SubmissionSHA256: digest, Outcome: "future_outcome", Revision: 2}).Encode()
	draft := func(kind string, payload json.RawMessage) ledger.Draft {
		return ledger.Draft{Type: kind, TypeVersion: 1, Actor: ledger.ActorOwner, OperationID: badID, Payload: payload}
	}
	if _, err := f.services.Store.Submit(context.Background(), draft(ledger.TypeCommand, command), []ledger.Draft{draft(ledger.TypeRuntimeSetupApplied, applied), draft(ledger.TypeRuntimeSetupReceipt, refused)}); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), f.services.Store); !errors.Is(err, ErrInconsistent) {
		t.Fatalf("unsupported receipt outcome was applied: %v", err)
	}
}
