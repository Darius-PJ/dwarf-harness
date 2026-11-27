package setup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/agents"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
	"github.com/Darius-PJ/dwarf-harness/internal/core/terminal"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

const MaxReviewBytes = 8 << 20
const reviewFormat = "dwarf-harness.setup-review"

type ContextInput struct {
	DisplayTitle         string                  `json:"display_title,omitempty"`
	TaskBinding          *ledger.TaskBinding     `json:"task_binding,omitempty"`
	RouteID              uuid.UUID               `json:"route_id"`
	ExpectedRouteVersion int64                   `json:"expected_route_version"`
	CredentialScopeID    uuid.UUID               `json:"credential_scope_id"`
	MaxOutputTokens      int64                   `json:"max_output_tokens"`
	TurnPolicy           *ledger.SetupTurnPolicy `json:"turn_policy,omitempty"`
}
type AgentInput struct {
	Name         string       `json:"name"`
	Instructions []byte       `json:"instructions_base64"`
	Settings     ContextInput `json:"settings"`
}
type ContextCreateInput struct {
	AgentID  uuid.UUID    `json:"agent_id"`
	Settings ContextInput `json:"settings"`
}
type ContextConfigureInput struct {
	ContextID uuid.UUID    `json:"context_id"`
	Settings  ContextInput `json:"settings"`
}
type WorkspaceInput struct {
	Repository  string    `json:"repository"`
	WorkspaceID uuid.UUID `json:"workspace_id,omitzero"`
}
type TaskInput struct {
	TaskID           uuid.UUID               `json:"task_id,omitzero"`
	ContextID        uuid.UUID               `json:"context_id,omitzero"`
	Origin           ledger.Binding          `json:"origin,omitzero"`
	Description      []byte                  `json:"description_base64"`
	AcceptanceSHA256 ledger.Digest           `json:"acceptance_sha256"`
	Dependencies     []uuid.UUID             `json:"dependencies"`
	Audience         []uuid.UUID             `json:"audience"`
	Assignment       *ledger.AssignmentInput `json:"assignment,omitempty"`
}
type GrantInput struct {
	Origin ledger.Binding             `json:"origin"`
	Spec   ledger.GrantIssueArguments `json:"spec"`
}
type CapacityInput struct {
	Origin ledger.Binding  `json:"origin"`
	Scope  ledger.ScopeKey `json:"scope"`
	Limits ledger.Capacity `json:"limits"`
}
type RouteInput struct {
	Origin            ledger.Binding `json:"origin,omitzero"`
	RouteID           uuid.UUID      `json:"route_id,omitzero"`
	ContextID         uuid.UUID      `json:"context_id,omitzero"`
	CredentialScopeID uuid.UUID      `json:"credential_scope_id,omitzero"`
	Enabled           bool           `json:"enabled"`
	Profile           []byte         `json:"profile_base64"`
}
type CredentialInput struct {
	Origin                ledger.Binding `json:"origin,omitzero"`
	ContextID             uuid.UUID      `json:"context_id,omitzero"`
	ScopeID               uuid.UUID      `json:"scope_id,omitzero"`
	Provider              string         `json:"provider"`
	SourceKind            string         `json:"source_kind"`
	SourceLocator         string         `json:"source_locator"`
	RevokePreviousVersion uuid.UUID      `json:"revoke_previous_version,omitzero"`
}
type CredentialRevokeInput struct {
	Origin            ledger.Binding `json:"origin"`
	ScopeID           uuid.UUID      `json:"scope_id"`
	CredentialVersion uuid.UUID      `json:"credential_version"`
}
type SpendInput struct {
	Origin     ledger.Binding   `json:"origin"`
	Scope      ledger.PaidScope `json:"scope"`
	Enabled    bool             `json:"enabled"`
	Mode       string           `json:"mode"`
	USD        string           `json:"usd"`
	PeriodKind string           `json:"period_kind"`
	WarningPPM *int64           `json:"warning_ppm,omitempty"`
}

type agentArguments struct {
	SessionID    uuid.UUID        `json:"session_id"`
	ContextID    uuid.UUID        `json:"context_id"`
	Name         string           `json:"name"`
	Instructions ledger.ObjectRef `json:"instructions"`
}
type contextArguments struct {
	Kind string `json:"kind"`
}
type workspaceArguments struct {
	Proposal            workspace.Proposal `json:"proposal"`
	CaptureInputsSHA256 ledger.Digest      `json:"capture_inputs_sha256"`
}
type routeArguments struct {
	Enabled           bool             `json:"enabled"`
	CredentialScopeID uuid.UUID        `json:"credential_scope_id"`
	Profile           ledger.ObjectRef `json:"profile"`
}
type credentialArguments struct {
	CredentialVersion      uuid.UUID `json:"credential_version"`
	Provider               string    `json:"provider"`
	SourceKind             string    `json:"source_kind"`
	SourceLocator          string    `json:"source_locator"`
	RevokePreviousVersion  uuid.UUID `json:"revoke_previous_version,omitzero"`
	ReadSourceAtEveryStart bool      `json:"read_source_at_every_start"`
}
type revokeArguments struct {
	CredentialVersion uuid.UUID `json:"credential_version"`
	Provider          string    `json:"provider"`
}
type spendArguments struct {
	Scope      ledger.PaidScope `json:"scope"`
	Enabled    bool             `json:"enabled"`
	Mode       string           `json:"mode"`
	USD        string           `json:"usd"`
	PeriodKind string           `json:"period_kind"`
	WarningPPM *int64           `json:"warning_ppm,omitempty"`
}

func decode(raw []byte, value any) error {
	if err := terminal.DecodeObject(raw, MaxReviewBytes, value); err != nil {
		f := fail("review_invalid", "", Target{}, err).(*Failure)
		var refusal *terminal.RefusalError
		if errors.As(err, &refusal) {
			f.Path = refusal.Path
		}
		return f
	}
	return nil
}

func expand(input ContextInput, general bool) ledger.SetupContextArguments {
	if general && input.DisplayTitle == "" {
		input.DisplayTitle = "General work"
	}
	if input.TurnPolicy == nil {
		p := terminal.DefaultPolicy()
		input.TurnPolicy = &ledger.SetupTurnPolicy{MaxModelCalls: p.MaxModelCalls, MaxToolCalls: p.MaxToolCalls, TimeoutMS: p.TimeoutMS, MaxArgumentBytes: p.MaxArgumentBytes, MaxResultBytes: p.MaxResultBytes, MaxDiscoveryEntries: p.MaxDiscoveryEntries, MaxSearchEntries: p.MaxSearchEntries, MaxSearchMatches: p.MaxSearchMatches, MaxLiveBytes: p.MaxLiveBytes}
	}
	return ledger.SetupContextArguments{DisplayTitle: input.DisplayTitle, TaskBinding: input.TaskBinding, RouteID: input.RouteID, ExpectedRouteVersion: input.ExpectedRouteVersion, CredentialScopeID: input.CredentialScopeID, MaxOutputTokens: input.MaxOutputTokens, TurnPolicy: *input.TurnPolicy}
}

func loopSetting(a ledger.SetupContextArguments) agents.LoopContextSettings {
	p := a.TurnPolicy
	return agents.LoopContextSettings{RouteID: a.RouteID, ExpectedRouteVersion: a.ExpectedRouteVersion, CredentialScopeID: a.CredentialScopeID, DisplayTitle: a.DisplayTitle, TaskBinding: a.TaskBinding, MaxOutputTokens: a.MaxOutputTokens, TimeoutMS: p.TimeoutMS, Policy: agents.TurnPolicy{Schema: 1, MaxModelCalls: p.MaxModelCalls, MaxToolCalls: p.MaxToolCalls, MaxArgumentBytes: p.MaxArgumentBytes, MaxResultBytes: p.MaxResultBytes, MaxDiscoveryEntries: p.MaxDiscoveryEntries, MaxSearchEntries: p.MaxSearchEntries, MaxSearchMatches: p.MaxSearchMatches, MaxLiveBytes: p.MaxLiveBytes}}
}

// Review reads inputs and current revisions, allocates every command identity
// and expands finite context defaults. It never changes colony state.
func Review(ctx context.Context, services Services, directory, operation string, raw []byte) (ReviewDocument, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return ReviewDocument{}, classify(err, operation, Target{})
	}
	doc := ReviewDocument{Format: reviewFormat, Schema: 1, Directory: filepath.Clean(directory), Operation: operation, Steps: []Step{}, Artifacts: []Artifact{}}
	var state State
	var snapshot agents.SetupSnapshot
	if operation == "colony create" {
		if err := decode(raw, &struct{}{}); err != nil {
			return ReviewDocument{}, err
		}
		doc.ColonyID = uuid.NewV7()
	} else {
		state, err = Load(ctx, services.Store)
		if err == nil {
			err = state.requireManaged()
		}
		if err != nil {
			return ReviewDocument{}, classify(err, operation, Target{})
		}
		doc.ColonyID = state.ColonyID
		if services.Agents == nil {
			return ReviewDocument{}, fail("storage_unavailable", operation, Target{}, nil)
		}
		snapshot, err = services.Agents.OwnerSetupSnapshot(ctx)
		if err != nil {
			return ReviewDocument{}, classify(err, operation, Target{})
		}
	}
	add := func(op string, target Target, revision int64, origin *ledger.Binding, args any) {
		data, e := json.Marshal(args)
		if e != nil {
			err = e
			return
		}
		doc.Steps = append(doc.Steps, Step{CommandID: uuid.NewV7(), Operation: op, Target: target, ExpectedRevision: revision, Origin: origin, Arguments: data})
	}
	artifact := func(data []byte) ledger.ObjectRef {
		digest := ledger.Digest(sha256.Sum256(data))
		if !slices.ContainsFunc(doc.Artifacts, func(a Artifact) bool { return a.SHA256 == digest }) {
			doc.Artifacts = append(doc.Artifacts, Artifact{SHA256: digest, Bytes: int64(len(data)), Data: slices.Clone(data)})
		}
		return ledger.ObjectRef{SHA256: digest, Bytes: int64(len(data))}
	}
	switch operation {
	case "colony create":
		add("colony.create", Target{Kind: "colony", ID: doc.ColonyID}, 0, nil, ledger.SetupColonyCreateArguments{})
	case "workspace register", "workspace inputs":
		var input WorkspaceInput
		if err = decode(raw, &input); err != nil {
			break
		}
		var proposal workspace.Proposal
		proposal, err = workspace.Inspect(ctx, input.Repository)
		if err != nil {
			break
		}
		// Absent host files have empty bytes, not JSON null. Their native
		// capture-input identity remains exactly the inspected proposal's.
		for _, file := range []*workspace.HostFile{&proposal.LocalExcludes, &proposal.GlobalExcludes, &proposal.LocalAttributes, &proposal.GlobalAttributes, &proposal.SystemAttributes} {
			if file.Bytes == nil {
				file.Bytes = []byte{}
			}
		}
		var digest ledger.Digest
		digest, err = workspace.CaptureInputsDigest(proposal)
		if err != nil {
			break
		}
		if operation == "workspace register" {
			if input.WorkspaceID != (uuid.UUID{}) {
				err = fail("review_invalid", operation, Target{}, nil)
				break
			}
			add("workspace.register", Target{Kind: "workspace"}, 0, nil, workspaceArguments{proposal, digest})
		} else {
			w, exists := state.workspaces[input.WorkspaceID]
			if !exists {
				err = fail("review_invalid", operation, Target{}, nil)
				break
			}
			add("workspace.inputs", Target{Kind: "workspace", ID: input.WorkspaceID}, w.Revision, nil, workspaceArguments{proposal, digest})
		}
	case "agent create":
		var input AgentInput
		if err = decode(raw, &input); err != nil {
			break
		}
		if !safeName(input.Name) {
			err = fail("review_invalid", operation, Target{}, nil)
			break
		}
		for _, identity := range snapshot.Contexts {
			if identity.Directory == input.Name {
				err = fail("agent_exists", operation, Target{Kind: "agent", ID: identity.AgentID}, nil)
				break
			}
		}
		if err != nil {
			break
		}
		agent, session, contextID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
		add("agent.register", Target{Kind: "agent", ID: agent}, 0, nil, agentArguments{session, contextID, input.Name, artifact(input.Instructions)})
		add("context.set", Target{Kind: "context", ID: contextID, AgentID: agent}, 0, nil, expand(input.Settings, true))
	case "context create":
		var input ContextCreateInput
		if err = decode(raw, &input); err != nil {
			break
		}
		if !slices.ContainsFunc(snapshot.Contexts, func(c agents.SetupIdentity) bool { return c.AgentID == input.AgentID }) {
			err = fail("review_invalid", operation, Target{}, nil)
			break
		}
		target := Target{Kind: "context", ID: uuid.NewV7(), AgentID: input.AgentID}
		add("context.create", target, 0, nil, contextArguments{"work"})
		add("context.set", target, 0, nil, expand(input.Settings, false))
	case "context configure":
		var input ContextConfigureInput
		if err = decode(raw, &input); err != nil {
			break
		}
		n, exists := state.nativeContexts[input.ContextID]
		if !exists {
			err = fail("review_invalid", operation, Target{}, nil)
			break
		}
		target := Target{Kind: "context", ID: input.ContextID, AgentID: n.agent}
		setting := expand(input.Settings, n.kind == "general")
		if err = state.contextChange(target, setting); err != nil {
			break
		}
		add("context.set", target, state.contexts[input.ContextID].Revision, nil, setting)
	case "task create":
		var input TaskInput
		if err = decode(raw, &input); err != nil {
			break
		}
		if input.ContextID != (uuid.UUID{}) {
			c, exists := state.contexts[input.ContextID]
			if !exists || c.Settings.TaskBinding == nil || c.Settings.TaskBinding.Generation != 1 || input.TaskID != (uuid.UUID{}) || input.Assignment != nil {
				err = fail("review_invalid", operation, Target{}, nil)
				break
			}
			input.TaskID = c.Settings.TaskBinding.TaskID
			input.Assignment = &ledger.AssignmentInput{AssignmentID: c.Settings.TaskBinding.AssignmentID, Assignee: ledger.Binding{AgentID: c.AgentID, ContextID: c.ContextID}}
			if input.Origin == (ledger.Binding{}) {
				input.Origin = input.Assignment.Assignee
			}
		}
		id := input.TaskID
		if id == (uuid.UUID{}) {
			id = uuid.NewV7()
		}
		add("task.create", Target{Kind: "task", ID: id}, 0, &input.Origin, ledger.TaskCreateArguments{TaskID: id, Description: artifact(input.Description), AcceptanceSHA256: input.AcceptanceSHA256, Dependencies: input.Dependencies, Audience: input.Audience, Assignment: input.Assignment})
	case "grant issue":
		var input struct {
			Origin ledger.Binding  `json:"origin"`
			Spec   json.RawMessage `json:"spec"`
		}
		if err = decode(raw, &input); err != nil {
			break
		}
		var args *ledger.GrantIssueArguments
		args, err = decodeGrantArguments(input.Spec, snapshot.AuthorityVersion)
		if err == nil {
			add("grant.issue", Target{Kind: "grant", ID: args.GrantID}, snapshot.AuthorityVersion, &input.Origin, *args)
		}
	case "capacity configure":
		var input CapacityInput
		if err = decode(raw, &input); err == nil {
			add("capacity.configure", Target{Kind: input.Scope.Kind, ID: input.Scope.ID}, snapshot.CapacityVersions[input.Scope], &input.Origin, ledger.CapacityArguments{Scope: input.Scope, Limits: input.Limits})
		}
	case "route set":
		var input RouteInput
		if err = decode(raw, &input); err != nil {
			break
		}
		if input.ContextID != (uuid.UUID{}) {
			c, exists := state.contexts[input.ContextID]
			if !exists || input.RouteID != (uuid.UUID{}) {
				err = fail("review_invalid", operation, Target{}, nil)
				break
			}
			input.RouteID = c.Settings.RouteID
			if input.CredentialScopeID == (uuid.UUID{}) {
				input.CredentialScopeID = c.Settings.CredentialScopeID
			}
			if input.Origin == (ledger.Binding{}) {
				input.Origin = ledger.Binding{AgentID: c.AgentID, ContextID: c.ContextID}
			}
		}
		if input.RouteID == (uuid.UUID{}) {
			input.RouteID = uuid.NewV7()
		}
		if input.CredentialScopeID == (uuid.UUID{}) {
			input.CredentialScopeID = uuid.NewV7()
		}
		var profile providers.Profile
		profile, err = providers.ParseProfile(input.Profile)
		if err == nil && input.Enabled {
			err = profile.Current(time.Now().UTC().UnixNano())
		}
		if err == nil {
			add("route.set", Target{Kind: "route", ID: input.RouteID}, snapshot.Routes[input.RouteID].Version, &input.Origin, routeArguments{input.Enabled, input.CredentialScopeID, artifact(input.Profile)})
		}
	case "credential bind":
		var input CredentialInput
		if err = decode(raw, &input); err != nil {
			break
		}
		if input.ContextID != (uuid.UUID{}) {
			c, exists := state.contexts[input.ContextID]
			if !exists || input.ScopeID != (uuid.UUID{}) {
				err = fail("review_invalid", operation, Target{}, nil)
				break
			}
			input.ScopeID = c.Settings.CredentialScopeID
			if input.Origin == (ledger.Binding{}) {
				input.Origin = ledger.Binding{AgentID: c.AgentID, ContextID: c.ContextID}
			}
		}
		add("credential.bind", Target{Kind: "credential", ID: input.ScopeID}, snapshot.CredentialVersions[input.ScopeID], &input.Origin, credentialArguments{uuid.NewV7(), input.Provider, input.SourceKind, input.SourceLocator, input.RevokePreviousVersion, true})
	case "credential revoke":
		var input CredentialRevokeInput
		if err = decode(raw, &input); err != nil {
			break
		}
		var provider string
		for _, c := range snapshot.Credentials {
			if c.ScopeID == input.ScopeID && c.CredentialVersion == input.CredentialVersion {
				provider = c.Provider
				break
			}
		}
		if provider == "" {
			err = fail("credential_revoked", operation, Target{Kind: "credential", ID: input.ScopeID}, nil)
			break
		}
		add("credential.revoke", Target{Kind: "credential", ID: input.ScopeID}, snapshot.CredentialVersions[input.ScopeID], &input.Origin, revokeArguments{input.CredentialVersion, provider})
	case "spend set":
		var input SpendInput
		if err = decode(raw, &input); err == nil {
			add("spend.set", Target{Kind: input.Scope.Kind, ID: input.Scope.ID}, snapshot.SpendVersions[input.Scope], &input.Origin, spendArguments{input.Scope, input.Enabled, input.Mode, input.USD, input.PeriodKind, input.WarningPPM})
		}
	default:
		err = fail("review_invalid", operation, Target{}, nil)
	}
	if err == nil {
		err = validateDocument(doc)
	}
	if err != nil {
		return ReviewDocument{}, classify(err, operation, Target{})
	}
	doc.ReviewSHA256, err = reviewDigest(doc)
	return doc, err
}

func safeName(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	for _, b := range []byte(name) {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-') {
			return false
		}
	}
	return true
}

func stepValue(step Step) (any, error) {
	var value any
	switch step.Operation {
	case "colony.create":
		value = &ledger.SetupColonyCreateArguments{}
	case "workspace.register", "workspace.inputs":
		value = &workspaceArguments{}
	case "agent.register":
		value = &agentArguments{}
	case "context.create":
		value = &contextArguments{}
	case "context.set":
		value = &ledger.SetupContextArguments{}
	case "task.create":
		value = &ledger.TaskCreateArguments{}
	case "grant.issue":
		return decodeGrantArguments(step.Arguments, step.ExpectedRevision)
	case "capacity.configure":
		value = &ledger.CapacityArguments{}
	case "route.set":
		value = &routeArguments{}
	case "credential.bind":
		value = &credentialArguments{}
	case "credential.revoke":
		value = &revokeArguments{}
	case "spend.set":
		value = &spendArguments{}
	default:
		return nil, fail("review_invalid", step.Operation, step.Target, nil)
	}
	if err := decode(step.Arguments, value); err != nil {
		return nil, err
	}
	return value, nil
}

// Grant resources have a closed, kind-dependent JSON codec, not struct tags.
// Project the reviewed arguments into their prospective native record so the
// frozen decoder, rather than a second resource schema, validates every kind.
func decodeGrantArguments(raw []byte, expected int64) (*ledger.GrantIssueArguments, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '{' || raw[len(raw)-1] != '}' {
		return nil, fail("review_invalid", "grant.issue", Target{}, nil)
	}
	header, err := json.Marshal(struct {
		ExpectedAuthorityVersion int64 `json:"expected_authority_version"`
		AuthorityVersion         int64 `json:"authority_version"`
	}{expected, expected + 1})
	if err != nil {
		return nil, fail("review_invalid", "grant.issue", Target{}, err)
	}
	// Retain the original members, including duplicates: the native decoder
	// must reject any attempt to supply these derived revision fields.
	projected := append(header[:len(header)-1], ',')
	projected = append(projected, raw[1:]...)
	grant, err := ledger.DecodeRuntimeGrantIssued(projected)
	if err != nil {
		return nil, fail("review_invalid", "grant.issue", Target{}, err)
	}
	return &ledger.GrantIssueArguments{
		GrantID: grant.GrantID, Grantee: grant.Grantee, ParentGrantID: grant.ParentGrantID,
		Operations: grant.Operations, Resources: grant.Resources, DataClass: grant.DataClass,
		Recipients: grant.Recipients, Limits: grant.Limits, DelegationDepth: grant.DelegationDepth,
		Lifetime: grant.Lifetime, TaskBinding: grant.TaskBinding,
	}, nil
}

func validateDocument(doc ReviewDocument) error {
	if doc.Format != reviewFormat || doc.Schema != 1 || !filepath.IsAbs(doc.Directory) || len(doc.Steps) == 0 || len(doc.Steps) > 2 {
		return fail("review_invalid", doc.Operation, Target{}, nil)
	}
	want := map[string][]string{"colony create": {"colony.create"}, "workspace register": {"workspace.register"}, "workspace inputs": {"workspace.inputs"}, "agent create": {"agent.register", "context.set"}, "context create": {"context.create", "context.set"}, "context configure": {"context.set"}, "task create": {"task.create"}, "grant issue": {"grant.issue"}, "capacity configure": {"capacity.configure"}, "route set": {"route.set"}, "credential bind": {"credential.bind"}, "credential revoke": {"credential.revoke"}, "spend set": {"spend.set"}}[doc.Operation]
	if len(want) != len(doc.Steps) {
		return fail("review_invalid", doc.Operation, Target{}, nil)
	}
	ids := map[uuid.UUID]bool{}
	for i, step := range doc.Steps {
		if ids[step.CommandID] || step.CommandID == (uuid.UUID{}) || step.Operation != want[i] || step.ExpectedRevision < 0 {
			return fail("review_invalid", doc.Operation, step.Target, nil)
		}
		if step.Origin != nil && (step.Origin.AgentID == (uuid.UUID{}) || step.Origin.ContextID == (uuid.UUID{})) {
			return fail("review_invalid", doc.Operation, step.Target, nil)
		}
		ids[step.CommandID] = true
		value, err := stepValue(step)
		if err != nil {
			return err
		}
		valid := step.Target.AgentID == (uuid.UUID{})
		switch args := value.(type) {
		case *ledger.SetupColonyCreateArguments:
			valid = valid && step.Origin == nil && step.Target.Kind == "colony" && step.Target.ID == doc.ColonyID && step.ExpectedRevision == 0
		case *workspaceArguments:
			digest, e := workspace.CaptureInputsDigest(args.Proposal)
			valid = valid && e == nil && digest == args.CaptureInputsSHA256 && step.Origin == nil && step.Target.Kind == "workspace"
			if step.Operation == "workspace.register" {
				valid = valid && step.Target.ID == (uuid.UUID{}) && step.ExpectedRevision == 0
			} else {
				valid = valid && step.Target.ID != (uuid.UUID{}) && step.ExpectedRevision > 0
			}
		case *agentArguments:
			valid = valid && step.Origin == nil && step.Target.Kind == "agent" && step.Target.ID != (uuid.UUID{}) && step.ExpectedRevision == 0 && safeName(args.Name)
		case *contextArguments:
			valid = step.Origin == nil && step.Target.Kind == "context" && step.Target.ID != (uuid.UUID{}) && step.Target.AgentID != (uuid.UUID{}) && step.ExpectedRevision == 0 && args.Kind == "work"
		case *ledger.SetupContextArguments:
			_, e := setupSubmission(step, *args).Encode()
			valid = step.Origin == nil && e == nil
		case *ledger.TaskCreateArguments:
			valid = valid && step.Origin != nil && step.Target.Kind == "task" && step.Target.ID == args.TaskID && step.ExpectedRevision == 0
		case *ledger.GrantIssueArguments:
			valid = valid && step.Origin != nil && step.Target.Kind == "grant" && step.Target.ID == args.GrantID
		case *ledger.CapacityArguments:
			valid = valid && step.Origin != nil && step.Target.Kind == args.Scope.Kind && step.Target.ID == args.Scope.ID
		case *routeArguments:
			valid = valid && step.Origin != nil && step.Target.Kind == "route" && step.Target.ID != (uuid.UUID{})
		case *credentialArguments:
			valid = valid && step.Origin != nil && step.Target.Kind == "credential" && step.Target.ID != (uuid.UUID{}) && args.ReadSourceAtEveryStart
		case *revokeArguments:
			valid = valid && step.Origin != nil && step.Target.Kind == "credential" && step.Target.ID != (uuid.UUID{})
		case *spendArguments:
			valid = valid && step.Origin != nil && step.Target.Kind == args.Scope.Kind && step.Target.ID == args.Scope.ID
		}
		if !valid {
			return fail("review_invalid", doc.Operation, step.Target, nil)
		}
		if settings, ok := value.(*ledger.SetupContextArguments); ok {
			if err := agents.ValidateSetupTemplate(loopSetting(*settings)); err != nil {
				return classify(err, doc.Operation, step.Target)
			}
			if doc.Operation == "agent create" && (settings.DisplayTitle != "General work" || settings.TaskBinding != nil) || doc.Operation == "context create" && settings.TaskBinding == nil {
				return fail("review_invalid", doc.Operation, step.Target, nil)
			}
		}
	}
	if len(doc.Steps) == 2 {
		a, b := doc.Steps[0], doc.Steps[1]
		if doc.Operation == "agent create" {
			value, _ := stepValue(a)
			args := value.(*agentArguments)
			if b.Target.AgentID != a.Target.ID || b.Target.ID != args.ContextID || !safeName(args.Name) {
				return fail("review_invalid", doc.Operation, b.Target, nil)
			}
		} else if a.Target != b.Target {
			return fail("review_invalid", doc.Operation, b.Target, nil)
		}
	}
	return nil
}

func reviewDigest(doc ReviewDocument) (ledger.Digest, error) {
	steps := slices.Clone(doc.Steps)
	for i := range steps {
		value, err := stepValue(steps[i])
		if err != nil {
			return ledger.Digest{}, err
		}
		steps[i].Arguments, err = json.Marshal(value)
		if err != nil {
			return ledger.Digest{}, err
		}
	}
	artifacts := make([]ledger.ObjectRef, len(doc.Artifacts))
	for i, a := range doc.Artifacts {
		artifacts[i] = ledger.ObjectRef{SHA256: a.SHA256, Bytes: a.Bytes}
	}
	canonical := struct {
		Format    string             `json:"format"`
		Schema    int                `json:"schema"`
		ColonyID  uuid.UUID          `json:"colony_id"`
		Directory string             `json:"directory"`
		Operation string             `json:"operation"`
		Steps     []Step             `json:"steps"`
		Artifacts []ledger.ObjectRef `json:"artifacts"`
	}{doc.Format, doc.Schema, doc.ColonyID, doc.Directory, doc.Operation, steps, artifacts}
	raw, err := json.Marshal(canonical)
	return ledger.Digest(sha256.Sum256(raw)), err
}

func DecodeReview(raw []byte) (ReviewDocument, error) {
	var doc ReviewDocument
	if err := decode(raw, &doc); err != nil {
		return ReviewDocument{}, err
	}
	if err := validateDocument(doc); err != nil {
		return ReviewDocument{}, err
	}
	digest, err := reviewDigest(doc)
	if err != nil {
		return ReviewDocument{}, err
	}
	if digest != doc.ReviewSHA256 {
		return ReviewDocument{}, fail("review_changed", doc.Operation, Target{}, nil)
	}
	return doc, nil
}

// WriteReview creates a new review with 0o600 and never overwrites a file.
// On Windows, the directory's inherited ACL governs access, as for colony files.
func WriteReview(path string, doc ReviewDocument) (err error) {
	if err := validateDocument(doc); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return classify(err, "review", Target{})
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	if _, err = f.Write(append(raw, '\n')); err != nil {
		return classify(err, "review", Target{})
	}
	return classify(f.Sync(), "review", Target{})
}

func reviewedArtifacts(doc ReviewDocument) (map[ledger.Digest][]byte, error) {
	out := map[ledger.Digest][]byte{}
	for _, a := range doc.Artifacts {
		if a.Bytes < 0 || a.Bytes > MaxReviewBytes || a.SHA256 == (ledger.Digest{}) {
			return nil, fail("review_invalid", doc.Operation, Target{}, nil)
		}
		data := slices.Clone(a.Data)
		if int64(len(data)) != a.Bytes || ledger.Digest(sha256.Sum256(data)) != a.SHA256 {
			return nil, fail("review_changed", doc.Operation, Target{}, nil)
		}
		if _, exists := out[a.SHA256]; exists {
			return nil, fail("review_invalid", doc.Operation, Target{}, nil)
		}
		out[a.SHA256] = data
	}
	return out, nil
}

func canonicalDirectory(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(path), nil
}
