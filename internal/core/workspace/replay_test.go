package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func encoded(t *testing.T, p interface {
	Encode() (json.RawMessage, error)
}) json.RawMessage {
	t.Helper()
	b, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func publishTestBody(t *testing.T, f *serviceFixture, b []byte) ledger.Digest {
	t.Helper()
	d, _, err := f.store.PublishBody(context.Background(), bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func revisionDrafts(t *testing.T, f *serviceFixture, revision int64, document ledger.Digest, refs []ledger.Digest) []ledger.Draft {
	t.Helper()
	id := uuid.NewV7()
	return []ledger.Draft{
		{Type: ledger.TypeCaptureInputsPinned, TypeVersion: 1, Actor: ledger.ActorOwner, BodyRefs: refs, Payload: encoded(t, ledger.CaptureInputsPinned{WorkspaceID: f.w.ID, Revision: revision, CaptureInputsSHA256: document, PinID: id})},
		{Type: ledger.TypeBodyPin, TypeVersion: 1, Actor: ledger.ActorOwner, BodyRefs: refs, Payload: encoded(t, ledger.BodyPin{PinID: id})},
		{Type: ledger.TypeBodyUnpin, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: encoded(t, ledger.BodyUnpin{PinID: f.w.PinID})},
	}
}

func TestOpenFailsClosedOnRuleBreak(t *testing.T) {
	rows := []string{
		"registered_actor", "registered_body_refs", "registered_index", "registered_commit_count", "registration_without_pin", "duplicate_workspace", "duplicate_common_dir", "registration_revision", "registration_workspace",
		"inputs_actor", "inputs_workspace", "inputs_body_refs_absent", "inputs_document_omitted", "inputs_pin_id", "inputs_pin_refs", "inputs_pin_index", "revision_one_outside_registration", "revision_gap", "update_index", "update_without_unpin", "update_wrong_unpin", "current_document_exact_set", "current_document_malformed",
		"scratch_actor", "scratch_context_envelope", "scratch_body_refs", "duplicate_scratch_id", "second_open_pair", "scratch_unknown_workspace", "reused_dir", "reused_branch", "branch_directory_scheme", "sha1_base_length", "sha1_anchor_length",
		"closure_actor", "closure_agent_context", "closure_body_refs", "closure_unknown", "closure_twice", "closure_payload_context",
		"workspace_suffix_collision",
	}
	for _, name := range rows {
		t.Run(name, func(t *testing.T) {
			f := newServiceFixture(t)
			scratch := mustScratch(t, f, uuid.NewV7())
			refs := f.svc.workspaces[f.w.ID].refs
			otherBody := publishTestBody(t, f, []byte("unrelated published body"))
			update := revisionDrafts(t, f, 2, f.w.InputsSHA256, refs)
			registered := ledger.WorkspaceRegistered{WorkspaceID: uuid.NewV7(), CommonDir: f.p.CommonDir + "-other", ObjectFormat: "sha1"}
			pin, _ := ledger.DecodeCaptureInputsPinned(update[0].Payload)
			pin.WorkspaceID, pin.Revision = registered.WorkspaceID, 1
			registration := []ledger.Draft{{Type: ledger.TypeWorkspaceRegistered, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: encoded(t, registered)}, update[0], update[1]}
			registration[1].Payload = encoded(t, pin)
			created := ledger.ScratchCreated{ScratchID: uuid.NewV7(), WorkspaceID: f.w.ID, ContextID: uuid.NewV7(), Dir: "01234567", Branch: "refs/heads/dwarf-harness/01234567", BaseCommit: scratch.BaseCommit, AnchorTree: scratch.AnchorTree}
			creation := ledger.Draft{Type: ledger.TypeScratchCreated, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: created.ContextID}
			closed := ledger.ScratchClosed{ScratchID: scratch.ID, ContextID: scratch.ContextID, Reason: "abandoned"}
			closure := ledger.Draft{Type: ledger.TypeScratchClosed, TypeVersion: 1, Actor: ledger.ActorOwner, ContextID: scratch.ContextID}
			var drafts []ledger.Draft
			badIndex := 0
			switch name {
			case "registered_actor":
				registration[0].Actor = ledger.ActorCore
				drafts = registration
			case "registered_body_refs":
				registration[0].BodyRefs = refs
				drafts = registration
			case "registered_index":
				drafts = []ledger.Draft{registration[2], registration[0], registration[1]}
				badIndex = 1
			case "registered_commit_count":
				drafts = append(registration, ledger.Draft{Type: ledger.TypeScratchClosed, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: encoded(t, closed)})
			case "registration_without_pin":
				drafts = registration[:2]
			case "duplicate_workspace":
				registered.WorkspaceID = f.w.ID
				pin.WorkspaceID = f.w.ID
				registration[0].Payload = encoded(t, registered)
				registration[1].Payload = encoded(t, pin)
				drafts = registration
			case "duplicate_common_dir":
				registered.CommonDir = f.p.CommonDir
				registration[0].Payload = encoded(t, registered)
				drafts = registration
			case "workspace_suffix_collision":
				copy(registered.WorkspaceID[12:], f.w.ID[12:])
				pin.WorkspaceID = registered.WorkspaceID
				registration[0].Payload = encoded(t, registered)
				registration[1].Payload = encoded(t, pin)
				drafts = registration
			case "registration_revision":
				pin.Revision = 2
				registration[1].Payload = encoded(t, pin)
				drafts = registration
			case "registration_workspace":
				pin.WorkspaceID = f.w.ID
				registration[1].Payload = encoded(t, pin)
				drafts = registration
			case "inputs_actor":
				update[0].Actor = ledger.ActorCore
				drafts = update
			case "inputs_workspace":
				p, _ := ledger.DecodeCaptureInputsPinned(update[0].Payload)
				p.WorkspaceID = uuid.NewV7()
				update[0].Payload = encoded(t, p)
				drafts = update
			case "inputs_body_refs_absent":
				update[0].BodyRefs = nil
				drafts = update
			case "inputs_document_omitted":
				update[0].BodyRefs = []ledger.Digest{otherBody}
				update[1].BodyRefs = update[0].BodyRefs
				drafts = update
			case "inputs_pin_id":
				update[1].Payload = encoded(t, ledger.BodyPin{PinID: uuid.NewV7()})
				drafts = update
			case "inputs_pin_refs":
				update[1].BodyRefs = []ledger.Digest{otherBody}
				drafts = update
			case "inputs_pin_index":
				drafts = []ledger.Draft{update[0], update[2], update[1]}
			case "revision_one_outside_registration":
				p, _ := ledger.DecodeCaptureInputsPinned(update[0].Payload)
				p.Revision = 1
				update[0].Payload = encoded(t, p)
				drafts = update
			case "revision_gap":
				p, _ := ledger.DecodeCaptureInputsPinned(update[0].Payload)
				p.Revision = 3
				update[0].Payload = encoded(t, p)
				drafts = update
			case "update_index":
				drafts = []ledger.Draft{update[1], update[0], update[2]}
				badIndex = 1
			case "update_without_unpin":
				drafts = update[:2]
			case "update_wrong_unpin":
				extra := uuid.NewV7()
				if _, err := f.store.Append(context.Background(), []ledger.Draft{{Type: ledger.TypeBodyPin, TypeVersion: 1, Actor: ledger.ActorOwner, BodyRefs: refs, Payload: encoded(t, ledger.BodyPin{PinID: extra})}}); err != nil {
					t.Fatal(err)
				}
				update[2].Payload = encoded(t, ledger.BodyUnpin{PinID: extra})
				drafts = update
			case "current_document_exact_set":
				update[0].BodyRefs = []ledger.Digest{f.w.InputsSHA256}
				update[1].BodyRefs = update[0].BodyRefs
				drafts = update
			case "current_document_malformed":
				d := publishTestBody(t, f, []byte(`{"format":"dwarf-harness/capture-inputs","version":1}`))
				drafts = revisionDrafts(t, f, 2, d, []ledger.Digest{d})
			case "scratch_actor":
				creation.Actor = ledger.ActorOwner
			case "scratch_context_envelope":
				creation.ContextID = uuid.NewV7()
			case "scratch_body_refs":
				creation.BodyRefs = refs
			case "duplicate_scratch_id":
				created.ScratchID = scratch.ID
			case "second_open_pair":
				created.ContextID = scratch.ContextID
				creation.ContextID = scratch.ContextID
			case "scratch_unknown_workspace":
				created.WorkspaceID = uuid.NewV7()
			case "reused_dir", "reused_branch":
				if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(scratch)); err != nil {
					t.Fatal(err)
				}
				if name == "reused_dir" {
					created.Dir = filepath.Base(scratch.Dir)
					created.Branch = "refs/heads/dwarf-harness/" + created.Dir
				} else {
					created.Branch = scratch.Branch
				}
			case "branch_directory_scheme":
				created.Branch = "refs/heads/some-other-name"
			case "sha1_base_length":
				created.BaseCommit = strings.Repeat("a", 64)
			case "sha1_anchor_length":
				created.AnchorTree = strings.Repeat("b", 64)
			case "closure_actor":
				closure.Actor = ledger.ActorCore
			case "closure_agent_context":
				closure.Actor = ledger.ActorAgent
				closure.ContextID = uuid.NewV7()
			case "closure_body_refs":
				closure.BodyRefs = refs
			case "closure_unknown":
				closed.ScratchID = uuid.NewV7()
			case "closure_twice":
				if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(scratch)); err != nil {
					t.Fatal(err)
				}
			case "closure_payload_context":
				closed.ContextID = uuid.NewV7()
			}
			if drafts == nil {
				if strings.HasPrefix(name, "closure_") {
					closure.Payload = encoded(t, closed)
					drafts = []ledger.Draft{closure}
				} else {
					creation.Payload = encoded(t, created)
					drafts = []ledger.Draft{creation}
				}
			}
			ack, err := f.store.Append(context.Background(), drafts)
			if err != nil {
				t.Fatalf("invalid-history fixture was refused by store: %v", err)
			}
			if err := f.svc.Close(); err != nil {
				t.Fatal(err)
			}
			_, _, err = openService(context.Background(), f.store, f.colony, serviceConfig(), serviceDeps{environ: func() []string { return f.env }})
			var inconsistent *InconsistentError
			if !errors.Is(err, ErrInconsistent) || !errors.As(err, &inconsistent) || inconsistent.Seq != ack.First+uint64(badIndex) {
				t.Fatalf("want inconsistent sequence %d, got %v", ack.First+uint64(badIndex), err)
			}
		})
	}
}

func TestReplayUnknownClosureReason(t *testing.T) {
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	payload := encoded(t, ledger.ScratchClosed{ScratchID: s.ID, ContextID: s.ContextID, Reason: "future_reason"})
	if _, err := f.store.Append(context.Background(), []ledger.Draft{{Type: ledger.TypeScratchClosed, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	f.reopen(t, serviceConfig())
	if _, err := f.svc.BeginWrite(context.Background(), s.ID); !errors.Is(err, ErrClosed) {
		t.Fatalf("unknown reason not treated as closure: %v", err)
	}
}

func TestReplayNeverReadsSupersededInputs(t *testing.T) {
	f := newServiceFixture(t)
	malformed := publishTestBody(t, f, []byte(`{"format":"dwarf-harness/capture-inputs","version":1}`))
	second := revisionDrafts(t, f, 2, malformed, []ledger.Digest{malformed})
	if _, err := f.store.Append(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	pin, _ := ledger.DecodeCaptureInputsPinned(second[0].Payload)
	f.w.PinID = pin.PinID
	third := revisionDrafts(t, f, 3, f.w.InputsSHA256, f.svc.workspaces[f.w.ID].refs)
	if _, err := f.store.Append(context.Background(), third); err != nil {
		t.Fatal(err)
	}
	report := f.reopen(t, serviceConfig())
	if len(report.Pending) != 0 || f.svc.workspaces[f.w.ID].Revision != 3 {
		t.Fatalf("historical bytes affected replay: %#v", report)
	}
}
