package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/agents"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
	"github.com/Darius-PJ/dwarf-harness/internal/core/setup"
)

type setupCLI struct {
	root, colony string
	next         int
	output       bytes.Buffer
}

func newSetupCLI(t *testing.T) *setupCLI {
	t.Helper()
	root := t.TempDir()
	h := &setupCLI{root: root, colony: filepath.Join(root, "colony")}
	h.apply(t, h.review(t, "colony create", map[string]any{}))
	return h
}

func setupJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func setupWrite(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (h *setupCLI) command(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := run(append([]string{"--colony-dir", h.colony}, args...), nil, &out, nil)
	h.output.Write(out.Bytes())
	return code, out.String()
}

func (h *setupCLI) review(t *testing.T, operation string, input any, flags ...string) string {
	t.Helper()
	h.next++
	inputPath := filepath.Join(h.root, fmt.Sprintf("input-%d.json", h.next))
	review := filepath.Join(h.root, fmt.Sprintf("review-%d.json", h.next))
	setupWrite(t, inputPath, setupJSON(t, input))
	args := append(strings.Fields(operation), "--input", inputPath, "--review-out", review)
	args = append(args, flags...)
	if code, output := h.command(t, args...); code != 0 {
		t.Fatalf("review %s: exit=%d %s", operation, code, output)
	}
	return review
}

func setupDocument(t *testing.T, path string) setup.ReviewDocument {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := setup.DecodeReview(raw)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func (h *setupCLI) apply(t *testing.T, path string) setup.ReviewDocument {
	t.Helper()
	doc := setupDocument(t, path)
	op := doc.Operation
	if op == "capacity configure" {
		op = "capacity set"
	}
	args := append(strings.Fields(op), "--apply", path, "--approve", doc.ReviewSHA256.String())
	if code, output := h.command(t, args...); code != 0 {
		t.Fatalf("apply %s: exit=%d %s", op, code, output)
	}
	return doc
}

func (h *setupCLI) agent(t *testing.T, name string) (ledger.Binding, ledger.SetupContextArguments) {
	t.Helper()
	setupWrite(t, filepath.Join(h.root, "instructions.txt"), []byte("Exact instructions\r\nNo inherited memory.\x00"))
	doc := h.apply(t, h.review(t, "agent create", map[string]any{"name": name, "instructions_file": "instructions.txt", "general": map[string]any{"max_output_tokens": 64}}))
	var settings ledger.SetupContextArguments
	if err := json.Unmarshal(doc.Steps[1].Arguments, &settings); err != nil {
		t.Fatal(err)
	}
	return ledger.Binding{AgentID: doc.Steps[0].Target.ID, ContextID: doc.Steps[1].Target.ID}, settings
}

func (h *setupCLI) snapshot(t *testing.T) (setup.State, agents.SetupSnapshot) {
	t.Helper()
	ctx := context.Background()
	store, _, err := ledgerstore.InspectColony(h.colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state, err := setup.Load(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	view, _, err := agents.Inspect(ctx, store, h.colony, agents.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	snapshot, err := view.OwnerSetupSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return state, snapshot
}

func (h *setupCLI) start(t *testing.T, flags ...string) (int, string) {
	t.Helper()
	input, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	var out bytes.Buffer
	code := run(append([]string{"--colony-dir", h.colony}, flags...), input, &out, nil)
	h.output.Write(out.Bytes())
	return code, out.String()
}

func requireSetupFailure(t *testing.T, code int, output, cause, fix string) {
	t.Helper()
	if code == 0 || !strings.Contains(output, "code="+cause) || !strings.Contains(output, "target=") || !strings.Contains(output, "member=") || !strings.Contains(output, "fix="+fmt.Sprintf("%q", fix)) {
		t.Fatalf("refusal: exit=%d %s", code, output)
	}
}

func TestSetupManagedStart(t *testing.T) {
	// Real Git inspection consumes the host configuration; isolate it, without
	// parallel environment mutation or a turn-starting message.
	h := newSetupCLI(t)
	t.Setenv("HOME", h.root)
	t.Setenv("XDG_CONFIG_HOME", h.root)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(h.root, "absent-gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_ATTR_NOSYSTEM", "1")
	exact := []byte("Approved instructions\r\nNo inheritance.\x00")
	setupWrite(t, filepath.Join(h.root, "instructions.txt"), exact)
	review := h.review(t, "agent create", map[string]any{"name": "Scout", "instructions_file": "instructions.txt", "general": map[string]any{"max_output_tokens": 64}})
	state, _ := h.snapshot(t)
	if len(state.Contexts) != 0 {
		t.Fatal("review created a context")
	}
	if !strings.Contains(h.output.String(), fmt.Sprintf("%q", exact)) {
		t.Fatal("review omitted exact instruction text")
	}
	setupWrite(t, filepath.Join(h.root, "instructions.txt"), []byte("changed after approval"))
	doc := h.apply(t, review)
	h.apply(t, review)
	origin := ledger.Binding{AgentID: doc.Steps[0].Target.ID, ContextID: doc.Steps[1].Target.ID}
	ctx := context.Background()
	s, closeAll, err := openSetupServices(ctx, h.colony)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Agents.ReadPrivate(ctx, agents.PrivateRead{ReadBinding: agents.ReadBinding{Binding: agents.Binding(origin), Scope: "agent"}, Revision: 1, Slot: "instructions", MaxBytes: 1024})
	if err != nil || !bytes.Equal(page.Bytes, exact) {
		t.Fatalf("apply reread instruction path: %v", err)
	}
	if err := closeAll(); err != nil {
		t.Fatal(err)
	}
	state, snapshot := h.snapshot(t)
	if len(state.Contexts) != 1 || len(snapshot.Contexts) != 1 || len(snapshot.Routes) != 0 || len(snapshot.Credentials) != 0 || snapshot.AuthorityVersion != 0 {
		t.Fatal("creation fabricated prerequisites or repeated native effect")
	}
	if code, output := h.start(t); code != 0 {
		t.Fatalf("incomplete paid prerequisites blocked terminal: exit=%d %s", code, output)
	}
	workDoc := h.apply(t, h.review(t, "context create", map[string]any{"title": "Research", "max_output_tokens": 32}, "--agent", "Scout"))
	var work ledger.SetupContextArguments
	if err := json.Unmarshal(workDoc.Steps[1].Arguments, &work); err != nil {
		t.Fatal(err)
	}
	if work.TaskBinding == nil || work.TaskBinding.Generation != 1 {
		t.Fatal("work review omitted exact task triple")
	}
	h.apply(t, h.review(t, "task create", map[string]any{"description_base64": []byte("Review the repository"), "acceptance_sha256": ledger.Digest(sha256.Sum256([]byte("accepted"))), "dependencies": []uuid.UUID{}, "audience": []uuid.UUID{origin.AgentID}}, "--context", "Scout/Research"))
	configure := map[string]any{"title": "Research", "task_binding": work.TaskBinding, "route_id": work.RouteID, "expected_route_version": work.ExpectedRouteVersion, "credential_scope_id": work.CredentialScopeID, "max_output_tokens": 48}
	h.apply(t, h.review(t, "context configure", configure, "--context", "Scout/Research"))
	h.apply(t, h.review(t, "capacity set", setup.CapacityInput{Origin: origin, Scope: ledger.ScopeKey{Kind: "colony"}, Limits: ledger.Capacity{TurnSlots: 1, LocalSlots: 1, BufferBytes: 1 << 20, LocalUnits: 100, MaxOperationBytes: 1 << 20}}))
	h.apply(t, h.review(t, "spend set", setup.SpendInput{Origin: origin, Scope: ledger.PaidScope{Kind: "colony"}, Enabled: true, Mode: "finite", USD: "0.000000", PeriodKind: "lifetime"}))
	h.apply(t, h.review(t, "grant issue", map[string]any{"origin": origin, "spec": map[string]any{"grant_id": uuid.NewV7(), "grantee": origin, "recipients": []ledger.Binding{origin}, "operations": []string{"turn.start"}, "resources": []any{map[string]any{"kind": "context", "binding": origin, "scope": "context", "slots": []string{}}}, "data_class": "private", "limits": ledger.Capacity{TurnSlots: 1}, "delegation_depth": 0, "lifetime": "until_revoked"}}))
	repo := filepath.Join(h.root, "repository")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Git: %v %s", err, out)
		}
	}
	git("init", "--initial-branch=main", repo)
	git("-C", repo, "config", "core.autocrlf", "false")
	setupWrite(t, filepath.Join(repo, "base.txt"), []byte("retained\n"))
	git("-C", repo, "add", "--", "base.txt")
	git("-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgSign=false", "commit", "-m", "base")
	git("-C", repo, "checkout", "--detach")
	h.apply(t, h.review(t, "workspace register", map[string]any{"repository": "repository"}))
	git("-C", repo, "config", "core.autocrlf", "true")
	inputsDoc := h.apply(t, h.review(t, "workspace inputs", map[string]any{"repository": "repository"}))
	workspaceID := inputsDoc.Steps[0].Target.ID
	h.apply(t, h.review(t, "workspace inputs", map[string]any{"repository": "repository"}, "--workspace", workspaceID.String()))
	unknownInput := filepath.Join(h.root, "unknown-workspace.json")
	setupWrite(t, unknownInput, setupJSON(t, map[string]any{"repository": "repository"}))
	code, output := h.command(t, "workspace", "inputs", "--workspace", uuid.NewV7().String(), "--input", unknownInput, "--review-out", filepath.Join(h.root, "unknown-workspace-review.json"))
	requireSetupFailure(t, code, output, "review_invalid", "workspace register --input --review-out")
	if code, output := h.start(t); code != 0 {
		t.Fatalf("managed reopen: exit=%d %s", code, output)
	}
	state, snapshot = h.snapshot(t)
	if len(state.Contexts) != 2 || snapshot.AuthorityVersion != 1 || snapshot.CapacityVersions[ledger.ScopeKey{Kind: "colony"}] != 1 || snapshot.SpendVersions[ledger.PaidScope{Kind: "colony"}] != 1 {
		t.Fatalf("configured owner state lost: %+v", snapshot)
	}
	before := state.ThroughSequence
	bad := append(strings.Fields(doc.Operation), "--apply", review, "--approve", strings.Repeat("f", 64))
	code, output = h.command(t, bad...)
	requireSetupFailure(t, code, output, "review_changed", "agent create --input --review-out")
	state, _ = h.snapshot(t)
	if state.ThroughSequence != before {
		t.Fatal("wrong approval changed ledger")
	}
}

func TestSetupConfigurationMode(t *testing.T) {
	h := newSetupCLI(t)
	code, output := h.start(t)
	requireSetupFailure(t, code, output, "setup_required", "agent create --input --review-out")
	empty := filepath.Join(h.root, "empty.json")
	setupWrite(t, empty, nil)
	code, output = h.start(t, "--settings", empty)
	requireSetupFailure(t, code, output, "mixed_configuration", "--colony-dir")
	h.agent(t, "Scout")
	input := filepath.Join(h.root, "invalid-input.json")
	setupWrite(t, input, setupJSON(t, map[string]any{"name": "Scout", "instructions_file": "instructions.txt", "general": map[string]any{"max_output_tokens": 64}}))
	code, output = h.command(t, "agent", "create", "--input", input, "--review-out", filepath.Join(h.root, "duplicate-agent-review.json"))
	requireSetupFailure(t, code, output, "agent_exists", "agent create --input --review-out")
	setupWrite(t, input, setupJSON(t, map[string]any{"name": "Other", "instructions_file": "private-missing-file", "general": map[string]any{"max_output_tokens": 64}}))
	code, output = h.command(t, "agent", "create", "--input", input, "--review-out", filepath.Join(h.root, "absent-review.json"))
	requireSetupFailure(t, code, output, "review_invalid", "agent create --input --review-out")
	if !strings.Contains(output, "member=instructions_file") || strings.Contains(output, "private-missing-file") {
		t.Fatal("artifact refusal exposed path or omitted member")
	}
	setupWrite(t, filepath.Join(h.root, "large-instructions"), make([]byte, agents.DefaultLimits().MaxObjectBytes+1))
	setupWrite(t, input, setupJSON(t, map[string]any{"name": "Other", "instructions_file": "large-instructions", "general": map[string]any{"max_output_tokens": 64}}))
	code, output = h.command(t, "agent", "create", "--input", input, "--review-out", filepath.Join(h.root, "oversize-review.json"))
	requireSetupFailure(t, code, output, "review_invalid", "agent create --input --review-out")
	if !strings.Contains(output, "member=instructions_file") || strings.Contains(output, "large-instructions") {
		t.Fatal("oversize artifact refusal leaked path")
	}
	setupWrite(t, input, setupJSON(t, map[string]any{"title": "Research", "max_output_tokens": 32, "agent_id": uuid.NewV7()}))
	code, output = h.command(t, "context", "create", "--agent", "Scout", "--input", input, "--review-out", filepath.Join(h.root, "conflict-review.json"))
	requireSetupFailure(t, code, output, "review_invalid", "context create --input --review-out")
	if !strings.Contains(output, "member=agent_id") {
		t.Fatal("agent disagreement omitted member")
	}
	code, output = h.command(t, "context", "create", "--agent", "Unknown", "--input", input, "--review-out", filepath.Join(h.root, "unknown-review.json"))
	requireSetupFailure(t, code, output, "review_invalid", "agent create --input --review-out")
	for range 2 {
		h.apply(t, h.review(t, "context create", map[string]any{"title": "Repeated", "max_output_tokens": 32}, "--agent", "Scout"))
	}
	setupWrite(t, input, []byte(`{"title":"Renamed","max_output_tokens":32}`))
	code, output = h.command(t, "context", "configure", "--context", "Scout/Repeated", "--input", input, "--review-out", filepath.Join(h.root, "ambiguous-review.json"))
	requireSetupFailure(t, code, output, "review_invalid", "context configure --input --review-out")
	store, _, err := ledgerstore.Open(h.colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	code, output = h.start(t)
	requireSetupFailure(t, code, output, "colony_in_use", "/quit")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	legacy := &setupCLI{root: t.TempDir()}
	legacy.colony = filepath.Join(legacy.root, "colony")
	store, err = ledgerstore.Create(legacy.colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	service, _, err := agents.Open(context.Background(), store, legacy.colony, agents.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	origin := ledger.Binding{AgentID: uuid.NewV7(), ContextID: uuid.NewV7()}
	_, err = service.RegisterAgent(context.Background(), agents.AgentRegistration{CommandID: uuid.NewV7(), AgentID: origin.AgentID, SessionID: uuid.NewV7(), ContextID: origin.ContextID, Directory: "Legacy", AgentMarkdown: []byte("Legacy instructions")})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	code, output = legacy.start(t)
	requireSetupFailure(t, code, output, "settings_required", "--settings")
	setupWrite(t, input, []byte(`{}`))
	code, output = legacy.command(t, "agent", "create", "--input", input, "--review-out", filepath.Join(legacy.root, "review.json"))
	requireSetupFailure(t, code, output, "legacy_configuration", "--settings")
	name := "DWARF_SETUP_LEGACY_" + strings.ReplaceAll(uuid.NewV7().String(), "-", "_")
	t.Setenv(name, "synthetic-legacy-value")
	scope := uuid.NewV7()
	settings := map[string]any{"schema": 1, "contexts": []any{map[string]any{"agent_id": origin.AgentID, "context_id": origin.ContextID, "route_id": uuid.NewV7(), "expected_route_version": 1, "credential_scope_id": scope, "max_output_tokens": 32, "display_title": "General work", "turn_policy": map[string]any{}}}, "credential_bindings": []any{map[string]any{"scope_id": scope, "provider": "anthropic", "source_kind": "environment", "source_locator": name}}}
	legacyFile := filepath.Join(legacy.root, "settings.json")
	setupWrite(t, legacyFile, setupJSON(t, settings))
	code, output = legacy.start(t, "--settings", legacyFile)
	if code != 0 {
		t.Fatalf("explicit legacy startup: exit=%d %s", code, output)
	}
}

func setupProfile(t *testing.T) []byte {
	t.Helper()
	now := time.Now().Add(-time.Minute).UTC().UnixNano()
	p := providers.Profile{Schema: 1, CapabilityVersion: "synthetic-capability", TariffVersion: "synthetic-tariff", Provider: "anthropic", Authority: "api.anthropic.com", Method: "POST", Path: "/v1/messages", APIVersion: providers.APIVersion, ModelID: providers.Haiku, Region: "global", RequestedTier: "standard_only", AllowedServedTiers: []string{"standard"}, Speed: "standard", ContextMax: 200000, InputMax: 196000, OutputMax: 8192, UsageSchema: "anthropic-messages-text/v1", CacheClasses: []string{providers.CacheRead, providers.CacheWrite1h, providers.CacheWrite5m}, ToolChoices: []string{"auto", "none"}, NoChargeRules: []string{}, Tariff: providers.Tariff{Currency: "USD"}, VerifiedUTCNS: now, ExpiresUTCNS: time.Now().Add(24 * time.Hour).UTC().UnixNano()}
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

func TestSetupCredentialSources(t *testing.T) {
	h := newSetupCLI(t)
	origin, settings := h.agent(t, "Scout")
	// Two contexts use one scope. Start must rotate it once, not once per view.
	h.apply(t, h.review(t, "context create", map[string]any{"title": "Research", "max_output_tokens": 32, "credential_scope_id": settings.CredentialScopeID}, "--agent", origin.AgentID.String()))
	profile := setupProfile(t)
	setupWrite(t, filepath.Join(h.root, "profile.json"), profile)
	h.apply(t, h.review(t, "route set", map[string]any{"enabled": true, "profile_file": "profile.json"}, "--context", "Scout/General work"))
	sentinel := "synthetic-secret-" + uuid.NewV7().String()
	name := "DWARF_SETUP_SOURCE_" + strings.ReplaceAll(uuid.NewV7().String(), "-", "_")
	oldName := name + "_OLD"
	t.Setenv(name, sentinel)
	t.Setenv(oldName, sentinel)
	bind := func(source string, jsonOutput bool) {
		t.Helper()
		flags := []string{"--context", "Scout/General work"}
		if jsonOutput {
			flags = append(flags, "--json")
		}
		before := h.output.Len()
		path := h.review(t, "credential bind", map[string]any{"provider": "anthropic", "source_kind": "environment", "source_locator": source}, flags...)
		if jsonOutput && strings.Contains(h.output.String()[before:], source) {
			t.Fatal("JSON review leaked source locator")
		}
		h.apply(t, path)
	}
	bind(oldName, false)
	_, before := h.snapshot(t)
	oldVersion := before.Credentials[0].CredentialVersion
	bind(name, true)
	_, before = h.snapshot(t)
	selected := before.Credentials[1].CredentialVersion
	code, output := h.start(t)
	if code != 0 || strings.Contains(output, name) {
		t.Fatalf("managed credential startup: exit=%d %s", code, output)
	}
	_, after := h.snapshot(t)
	if len(after.Credentials) != 2 || after.Credentials[0].CredentialVersion != oldVersion || after.Credentials[1].CredentialVersion == selected || after.CredentialVersions[settings.CredentialScopeID] != before.CredentialVersions[settings.CredentialScopeID]+1 {
		t.Fatal("startup did not replace exactly the latest selection once")
	}
	t.Setenv(name, "")
	sequence := after.ThroughSequence
	code, output = h.start(t)
	requireSetupFailure(t, code, output, "credential_invalid", "credential bind --input --review-out")
	if strings.Contains(output, name) || !strings.Contains(output, settings.CredentialScopeID.String()) {
		t.Fatal("unusable source exposed locator or omitted scope")
	}
	_, failed := h.snapshot(t)
	if failed.ThroughSequence != sequence || failed.CredentialVersions[settings.CredentialScopeID] != after.CredentialVersions[settings.CredentialScopeID] {
		t.Fatal("unusable latest source fell back or wrote a binding")
	}
	h.apply(t, h.review(t, "credential revoke", setup.CredentialRevokeInput{Origin: origin, ScopeID: settings.CredentialScopeID, CredentialVersion: after.Credentials[1].CredentialVersion}))
	_, revoked := h.snapshot(t)
	if len(revoked.Credentials) != 1 || revoked.Credentials[0].CredentialVersion != oldVersion {
		t.Fatal("explicit revoke removed the wrong active version")
	}
	if strings.Contains(h.output.String(), sentinel) {
		t.Fatal("credential bytes reached output")
	}
	if err := filepath.WalkDir(h.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() == "lock" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err == nil && bytes.Contains(raw, []byte(sentinel)) {
			t.Error("credential bytes persisted in ledger, bodies, objects, review or input")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
