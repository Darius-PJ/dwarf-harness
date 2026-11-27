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
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/agents"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

// Apply executes only the reviewed commands, with their original IDs and bytes.
// A partial apply is retried with this same review, never with a fresh review.
func Apply(ctx context.Context, services Services, directory string, doc ReviewDocument, approval ledger.Digest) ([]Receipt, error) {
	if approval == (ledger.Digest{}) {
		return nil, fail("approval_required", doc.Operation, Target{}, nil)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, classify(err, doc.Operation, Target{})
	}
	doc, err = DecodeReview(raw)
	if err != nil {
		return nil, err
	}
	if approval != doc.ReviewSHA256 {
		return nil, fail("review_changed", doc.Operation, Target{}, nil)
	}
	directory, err = canonicalDirectory(directory)
	if err != nil {
		return nil, classify(err, doc.Operation, Target{})
	}
	if directory != doc.Directory {
		return nil, fail("review_changed", doc.Operation, Target{}, nil)
	}
	artifacts, err := reviewedArtifacts(doc)
	if err != nil {
		return nil, err
	}
	if doc.Operation == "colony create" {
		return applyColony(ctx, directory, doc, services.afterNative)
	}
	state, err := Load(ctx, services.Store)
	if err == nil {
		err = state.requireManaged()
	}
	if err != nil {
		return nil, classify(err, doc.Operation, Target{})
	}
	if state.ColonyID != doc.ColonyID || services.Agents == nil || filepath.Clean(filepath.Dir(services.Store.LogDir())) != directory {
		return nil, fail("review_changed", doc.Operation, Target{}, nil)
	}
	out := make([]Receipt, 0, len(doc.Steps))
	for _, step := range doc.Steps {
		state, err = Load(ctx, services.Store)
		if err != nil {
			return out, classify(err, doc.Operation, step.Target)
		}
		value, err := stepValue(step)
		if err != nil {
			return out, err
		}
		var receipt Receipt
		switch args := value.(type) {
		case *workspaceArguments:
			receipt, err = applyWorkspace(ctx, services, state, step, *args)
		case *ledger.SetupContextArguments:
			sub := setupSubmission(step, *args)
			var duplicate bool
			receipt, duplicate, err = existingReceipt(state, step.CommandID, sub)
			if err == nil && !duplicate {
				if state.contexts[step.Target.ID].Revision != step.ExpectedRevision {
					err = fail("stale_revision", doc.Operation, step.Target, agents.ErrRevision)
				}
				if err == nil {
					err = state.contextChange(step.Target, *args)
				}
				if err == nil {
					var snapshot agents.SetupSnapshot
					snapshot, err = services.Agents.OwnerSetupSnapshot(ctx)
					if err == nil && slices.ContainsFunc(snapshot.Contexts, func(c agents.SetupIdentity) bool { return c.ContextID == step.Target.ID && c.Busy }) {
						err = fail("context_busy", doc.Operation, step.Target, nil)
					}
				}
				if err == nil {
					receipt, err = submitSetup(ctx, services.Store, state, step.CommandID, sub, nil)
				}
			}
		default:
			receipt, err = applyNative(ctx, services, step, value, artifacts)
			if err == nil && services.afterNative != nil {
				services.afterNative(step.Operation)
			}
		}
		if err != nil {
			return out, classify(err, doc.Operation, step.Target)
		}
		out = append(out, receipt)
	}
	return out, nil
}

func applyColony(ctx context.Context, dir string, doc ReviewDocument, after func(string)) (_ []Receipt, err error) {
	step := doc.Steps[0]
	if step.Target.Kind != "colony" || step.Target.ID != doc.ColonyID || step.ExpectedRevision != 0 {
		return nil, fail("review_invalid", doc.Operation, step.Target, nil)
	}
	store, _, openErr := ledgerstore.Open(dir, ledger.Defined())
	if openErr != nil {
		if !errors.Is(openErr, os.ErrNotExist) {
			return nil, classify(openErr, doc.Operation, step.Target)
		}
		entries, e := os.ReadDir(dir)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return nil, classify(e, doc.Operation, step.Target)
		}
		if len(entries) != 0 {
			return nil, fail("colony_exists", doc.Operation, step.Target, nil)
		}
		store, err = ledgerstore.CreateWithID(dir, ledger.Defined(), doc.ColonyID)
		if err != nil {
			return nil, classify(err, doc.Operation, step.Target)
		}
		if after != nil {
			after("colony.create")
		}
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	if store.Colony() != doc.ColonyID {
		return nil, fail("colony_exists", doc.Operation, step.Target, nil)
	}
	state, err := Load(ctx, store)
	if err != nil {
		return nil, classify(err, doc.Operation, step.Target)
	}
	sub := setupSubmission(step, ledger.SetupColonyCreateArguments{})
	if receipt, found, e := existingReceipt(state, step.CommandID, sub); e != nil || found {
		return []Receipt{receipt}, e
	}
	if state.ThroughSequence != 0 {
		return nil, fail("colony_exists", doc.Operation, step.Target, nil)
	}
	receipt, err := submitSetup(ctx, store, state, step.CommandID, sub, nil)
	if err != nil {
		return nil, classify(err, doc.Operation, step.Target)
	}
	return []Receipt{receipt}, nil
}

func setupSubmission(step Step, args any) ledger.SetupSubmission {
	return ledger.SetupSubmission{Schema: 12, Actor: "owner", Operation: step.Operation, Target: ledger.SetupTarget{Kind: step.Target.Kind, ID: step.Target.ID, AgentID: step.Target.AgentID}, ExpectedRevision: step.ExpectedRevision, Arguments: args}
}

func existingReceipt(state State, id uuid.UUID, sub ledger.SetupSubmission) (Receipt, bool, error) {
	raw, err := sub.Encode()
	if err != nil {
		return Receipt{}, false, err
	}
	digest := ledger.Digest(sha256.Sum256(raw))
	old, exists := state.commands[id]
	if !exists {
		return Receipt{}, false, nil
	}
	if old != digest {
		return Receipt{}, false, fail("command_mismatch", sub.Operation, Target{sub.Target.Kind, sub.Target.ID, sub.Target.AgentID}, ledgerstore.ErrCommandMismatch)
	}
	receipt, exists := state.receipts[id]
	if !exists {
		return Receipt{}, false, ErrInconsistent
	}
	return receipt, true, nil
}

func applyWorkspace(ctx context.Context, services Services, state State, step Step, args workspaceArguments) (Receipt, error) {
	if receipt, exists := state.receipts[step.CommandID]; exists {
		native := state.workspaceRegistrations[receipt.Target.ID]
		if native.CommonDir != args.Proposal.CommonDir || native.ObjectFormat != args.Proposal.ObjectFormat {
			return Receipt{}, ledgerstore.ErrCommandMismatch
		}
		bound := step
		if step.Operation == "workspace.register" {
			bound.Target.ID = receipt.Target.ID
		}
		r, _, err := existingReceipt(state, step.CommandID, setupSubmission(bound, ledger.SetupWorkspaceArguments{CaptureInputsSHA256: args.CaptureInputsSHA256}))
		return r, err
	}
	if _, used := state.commands[step.CommandID]; used {
		return Receipt{}, ledgerstore.ErrCommandMismatch
	}
	if services.Workspace == nil {
		return Receipt{}, workspace.ErrUnavailable
	}
	digest, err := workspace.CaptureInputsDigest(args.Proposal)
	if err != nil {
		return Receipt{}, err
	}
	if digest != args.CaptureInputsSHA256 {
		return Receipt{}, fail("review_changed", step.Operation, step.Target, nil)
	}
	list, err := services.Workspace.ListWorkspaces()
	if err != nil {
		return Receipt{}, err
	}
	var found workspace.Workspace
	if step.Operation == "workspace.register" {
		for _, w := range list {
			if sameRepository(w.CommonDir, args.Proposal.CommonDir) {
				found = w
				break
			}
		}
		if found.ID != (uuid.UUID{}) {
			if found.Revision != 1 || found.InputsSHA256 != digest {
				return Receipt{}, workspace.ErrExists
			}
		} else {
			found, err = services.Workspace.Register(ctx, args.Proposal)
		}
	} else {
		for _, w := range list {
			if w.ID == step.Target.ID {
				found = w
				break
			}
		}
		if found.ID == (uuid.UUID{}) || !sameRepository(found.CommonDir, args.Proposal.CommonDir) {
			return Receipt{}, workspace.ErrUnknownWorkspace
		}
		if found.Revision == step.ExpectedRevision+1 && found.InputsSHA256 == digest {
			// Complete only this exact native effect, never a later revision.
		} else if found.Revision != step.ExpectedRevision {
			return Receipt{}, workspace.ErrRevision
		} else {
			found, err = services.Workspace.UpdateInputs(ctx, found.ID, step.ExpectedRevision, args.Proposal)
		}
	}
	if err != nil {
		return Receipt{}, err
	}
	if services.afterNative != nil {
		services.afterNative(step.Operation)
	}
	step.Target.ID = found.ID
	state, err = Load(ctx, services.Store)
	if err != nil {
		return Receipt{}, err
	}
	return submitSetup(ctx, services.Store, state, step.CommandID, setupSubmission(step, ledger.SetupWorkspaceArguments{CaptureInputsSHA256: digest}), nil)
}

func sameRepository(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	x, e1 := os.Stat(a)
	y, e2 := os.Stat(b)
	return e1 == nil && e2 == nil && os.SameFile(x, y)
}

func applyNative(ctx context.Context, services Services, step Step, value any, artifacts map[ledger.Digest][]byte) (Receipt, error) {
	var origin agents.Binding
	if step.Origin != nil {
		origin = agents.Binding{AgentID: step.Origin.AgentID, ContextID: step.Origin.ContextID}
	}
	object := func(ref ledger.ObjectRef) ([]byte, error) {
		b, exists := artifacts[ref.SHA256]
		if !exists || int64(len(b)) != ref.Bytes {
			return nil, fail("review_changed", step.Operation, step.Target, nil)
		}
		return b, nil
	}
	out := Receipt{CommandID: step.CommandID, Operation: step.Operation, Target: step.Target}
	var err error
	var private agents.Receipt
	var scoped agents.ScopedReceipt
	var paid agents.PaidReceipt
	switch a := value.(type) {
	case *agentArguments:
		var instructions []byte
		instructions, err = object(a.Instructions)
		if err == nil {
			private, err = services.Agents.RegisterAgent(ctx, agents.AgentRegistration{CommandID: step.CommandID, AgentID: step.Target.ID, SessionID: a.SessionID, ContextID: a.ContextID, Directory: a.Name, AgentMarkdown: instructions, ExpectedVersion: uint64(step.ExpectedRevision)})
		}
		out.Revision, out.Sequence, out.SubmissionSHA256 = int64(private.Revision), private.Sequence, private.SubmissionSHA256
	case *contextArguments:
		private, err = services.Agents.CreateContext(ctx, agents.ContextCreation{Binding: agents.Binding{AgentID: step.Target.AgentID, ContextID: step.Target.ID}, CommandID: step.CommandID, Kind: a.Kind, ExpectedVersion: uint64(step.ExpectedRevision)})
		out.Revision, out.Sequence, out.SubmissionSHA256 = int64(private.Revision), private.Sequence, private.SubmissionSHA256
	case *ledger.TaskCreateArguments:
		var description []byte
		description, err = object(a.Description)
		if err == nil {
			scoped, err = services.Agents.CreateTask(ctx, origin, agents.TaskCreation{CommandID: step.CommandID, TaskID: a.TaskID, ExpectedVersion: uint64(step.ExpectedRevision), Description: description, AcceptanceSHA256: a.AcceptanceSHA256, Dependencies: a.Dependencies, Audience: a.Audience, Assignment: a.Assignment})
		}
	case *ledger.GrantIssueArguments:
		recipients := make([]agents.Binding, len(a.Recipients))
		for i, b := range a.Recipients {
			recipients[i] = agents.Binding{AgentID: b.AgentID, ContextID: b.ContextID}
		}
		spec := agents.GrantSpec{GrantID: a.GrantID, Grantee: agents.Binding{AgentID: a.Grantee.AgentID, ContextID: a.Grantee.ContextID}, ParentGrantID: a.ParentGrantID, Operations: a.Operations, Resources: a.Resources, DataClass: a.DataClass, Recipients: recipients, Limits: a.Limits, DelegationDepth: a.DelegationDepth, Lifetime: a.Lifetime, TaskBinding: a.TaskBinding}
		scoped, err = services.Agents.IssueGrant(ctx, origin, agents.GrantIssue{CommandID: step.CommandID, ExpectedAuthorityVersion: uint64(step.ExpectedRevision), Spec: spec})
	case *ledger.CapacityArguments:
		scoped, err = services.Agents.ConfigureCapacity(ctx, origin, agents.CapacitySpec{CommandID: step.CommandID, ExpectedVersion: uint64(step.ExpectedRevision), Scope: a.Scope, Limits: a.Limits})
	case *routeArguments:
		var profile []byte
		profile, err = object(a.Profile)
		if err == nil {
			paid, err = services.Agents.ConfigurePaidRoute(ctx, origin, agents.PaidRouteConfiguration{CommandID: step.CommandID, RouteID: step.Target.ID, ExpectedVersion: step.ExpectedRevision, Enabled: a.Enabled, CredentialScopeID: a.CredentialScopeID, Profile: profile})
		}
	case *credentialArguments:
		paid, err = services.Agents.BindCredential(ctx, origin, agents.CredentialBinding{CommandID: step.CommandID, ScopeID: step.Target.ID, CredentialVersion: a.CredentialVersion, ExpectedVersion: step.ExpectedRevision, Provider: a.Provider, SourceKind: a.SourceKind, SourceLocator: a.SourceLocator, RevokePreviousVersion: a.RevokePreviousVersion})
	case *revokeArguments:
		paid, err = services.Agents.RevokeCredential(ctx, origin, agents.CredentialRevocation{CommandID: step.CommandID, ScopeID: step.Target.ID, CredentialVersion: a.CredentialVersion, ExpectedVersion: step.ExpectedRevision, Provider: a.Provider})
	case *spendArguments:
		paid, err = services.Agents.ConfigureSpend(ctx, origin, agents.SpendConfiguration{CommandID: step.CommandID, Scope: a.Scope, ExpectedVersion: step.ExpectedRevision, Terms: agents.SpendTermsInput{Enabled: a.Enabled, Mode: a.Mode, USD: a.USD, PeriodKind: a.PeriodKind, WarningPPM: a.WarningPPM}})
	default:
		err = fail("review_invalid", step.Operation, step.Target, nil)
	}
	if scoped.Sequence != 0 {
		out.Revision, out.Sequence, out.SubmissionSHA256 = scoped.Version, scoped.Sequence, scoped.SubmissionSHA256
	}
	if paid.Sequence != 0 {
		out.Revision, out.Sequence, out.SubmissionSHA256 = paid.Version, paid.Sequence, paid.SubmissionSHA256
	}
	return out, err
}

// submitSetup is deliberately private: callers cannot submit arbitrary drafts
// or smuggle actor, pin or body-ref fields through a review.
func submitSetup(ctx context.Context, store *ledgerstore.Store, state State, id uuid.UUID, sub ledger.SetupSubmission, bodies map[ledger.Digest][]byte) (Receipt, error) {
	if receipt, found, err := existingReceipt(state, id, sub); err != nil || found {
		return receipt, err
	}
	encoded, err := sub.Encode()
	if err != nil {
		return Receipt{}, err
	}
	if sub.Target.Kind == "colony" && sub.Target.ID != state.ColonyID {
		return Receipt{}, ledger.ErrInvalid
	}
	if sub.Operation == "colony.create" {
		if state.ThroughSequence != 0 {
			return Receipt{}, ledgerstore.ErrExists
		}
	} else if err := state.requireManaged(); err != nil {
		return Receipt{}, err
	}
	switch args := sub.Arguments.(type) {
	case ledger.SetupWorkspaceArguments:
		native := state.workspaces[sub.Target.ID]
		if native.Revision != sub.ExpectedRevision+1 || native.CaptureInputsSHA256 != args.CaptureInputsSHA256 {
			return Receipt{}, workspace.ErrRevision
		}
	case ledger.SetupContextArguments:
		if state.contexts[sub.Target.ID].Revision != sub.ExpectedRevision {
			return Receipt{}, agents.ErrRevision
		}
	case ledger.SetupStartupArguments:
		if state.StartupRevision != sub.ExpectedRevision {
			return Receipt{}, agents.ErrRevision
		}
	case ledger.SetupChecksArguments:
		if state.ChecksRevision != sub.ExpectedRevision {
			return Receipt{}, agents.ErrRevision
		}
	}
	digest := ledger.Digest(sha256.Sum256(encoded))
	var refs []ledger.Digest
	switch args := sub.Arguments.(type) {
	case ledger.SetupStartupArguments:
		data, ok := bodies[args.SettingsSHA256]
		if !ok || int64(len(data)) != args.SettingsBytes || args.SettingsBytes <= 0 {
			return Receipt{}, ledger.ErrInvalid
		}
		refs = []ledger.Digest{args.SettingsSHA256}
	case ledger.SetupChecksArguments:
		for d := range bodies {
			refs = append(refs, d)
		}
		slices.SortFunc(refs, func(a, b ledger.Digest) int { return bytes.Compare(a[:], b[:]) })
		if len(refs) == 0 || int64(len(refs)) != args.BodyCount || bodySetDigest(refs) != args.BodySetSHA256 {
			return Receipt{}, ledger.ErrInvalid
		}
	default:
		if len(bodies) != 0 {
			return Receipt{}, ledger.ErrInvalid
		}
	}
	for _, d := range refs {
		data := bodies[d]
		if ledger.Digest(sha256.Sum256(data)) != d {
			return Receipt{}, ledger.ErrInvalid
		}
	}
	for _, d := range refs {
		if _, _, err := store.PublishBody(ctx, bytes.NewReader(bodies[d])); err != nil {
			return Receipt{}, err
		}
	}
	command, err := (ledger.Command{CommandID: id, SubmissionSHA256: digest}).Encode()
	if err != nil {
		return Receipt{}, err
	}
	applied, err := (ledger.RuntimeSetupApplied{Submission: sub}).Encode()
	if err != nil {
		return Receipt{}, err
	}
	receipt, err := (ledger.RuntimeSetupReceipt{CommandID: id, SubmissionSHA256: digest, Outcome: "applied", Revision: sub.ExpectedRevision + 1}).Encode()
	if err != nil {
		return Receipt{}, err
	}
	draft := func(kind string, payload json.RawMessage, refs []ledger.Digest) ledger.Draft {
		return ledger.Draft{Type: kind, TypeVersion: 1, Actor: ledger.ActorOwner, OperationID: id, Payload: payload, BodyRefs: refs}
	}
	drafts := []ledger.Draft{draft(ledger.TypeRuntimeSetupApplied, applied, refs)}
	if len(refs) != 0 {
		pin, err := (ledger.BodyPin{PinID: id}).Encode()
		if err != nil {
			return Receipt{}, err
		}
		drafts = append(drafts, draft(ledger.TypeBodyPin, pin, refs))
	}
	drafts = append(drafts, draft(ledger.TypeRuntimeSetupReceipt, receipt, nil))
	written, err := store.SubmitAt(ctx, state.ThroughSequence, draft(ledger.TypeCommand, command, nil), drafts)
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{CommandID: id, Operation: sub.Operation, Target: Target{sub.Target.Kind, sub.Target.ID, sub.Target.AgentID}, SubmissionSHA256: digest, Revision: sub.ExpectedRevision + 1, Sequence: written.Last}, nil
}
