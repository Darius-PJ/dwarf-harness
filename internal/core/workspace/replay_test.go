package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
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

type promotionHistory struct {
	f              *serviceFixture
	commits        []ledger.Commit
	a1, a2, a3, b1 Request
}

func buildPromotionHistory(t *testing.T) promotionHistory {
	t.Helper()
	f := newServiceFixture(t)
	a, b := mustScratch(t, f, uuid.NewV7()), mustScratch(t, f, uuid.NewV7())
	h := promotionHistory{f: f, a1: rootRequest(a), a2: rootRequest(a), a3: rootRequest(a), b1: rootRequest(b)}
	h.a2.Head = h.a1.RequestID
	h.a3.RestartFrom = h.a2.RequestID
	writeTestFile(t, filepath.Join(a.Dir, "base.txt"), []byte("A\ntwo\n"))
	mustAdmit(t, f, h.a1)
	writeTestFile(t, filepath.Join(a.Dir, "a.txt"), []byte("local successor\n"))
	mustAdmit(t, f, h.a2)
	writeTestFile(t, filepath.Join(b.Dir, "base.txt"), []byte("B\ntwo\n"))
	mustAdmit(t, f, h.b1)
	mustIntegrate(t, f)
	advanceMain(t, f, map[string]string{"base.txt": "B\ntwo\n"})
	mustIntegrate(t, f)
	mustAdmit(t, f, h.a3)
	mustIntegrate(t, f)
	advanceMain(t, f, map[string]string{"base.txt": "C\ntwo\n"})
	mustIntegrate(t, f)
	writeTestFile(t, filepath.Join(a.Dir, "base.txt"), []byte("one\ntwo\n"))
	if err := os.Remove(filepath.Join(a.Dir, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(a)); err != nil {
		t.Fatal(err)
	}
	h.commits = serviceCommits(t, f.store)
	return h
}

func (h promotionHistory) locate(t *testing.T, kind string, id uuid.UUID, outcome string, last bool) (int, int) {
	t.Helper()
	ci, ri := -1, -1
	for i, commit := range h.commits {
		for j, record := range commit.Records {
			if record.Type != kind {
				continue
			}
			match := false
			switch kind {
			case ledger.TypePromotionAdmitted:
				p, _ := ledger.DecodePromotionAdmitted(record.Payload)
				match = p.RequestID == id
			case ledger.TypeGenerationDerived:
				p, _ := ledger.DecodeGenerationDerived(record.Payload)
				match = p.RequestID == id && (outcome == "" || p.Outcome == outcome)
			case ledger.TypePromotionFailed:
				p, _ := ledger.DecodePromotionFailed(record.Payload)
				match = p.RequestID == id
			case ledger.TypeScratchClosed:
				match = true
			}
			if match {
				ci, ri = i, j
				if !last {
					return ci, ri
				}
			}
		}
	}
	if ci < 0 {
		t.Fatalf("missing history target %s %s %s", kind, id, outcome)
	}
	return ci, ri
}

func TestOpenFailsClosedOnF2_06RuleBreak(t *testing.T) {
	h := buildPromotionHistory(t)
	rows := []string{
		"admission_actor", "admission_context", "admission_body_refs", "admission_pin_missing", "admission_pin_id", "admission_pin_refs", "admission_pin_not_immediate",
		"admission_request_reused", "admission_capture_reused", "admission_root_lineage_reused", "admission_successor_lineage",
		"admission_unknown_workspace", "admission_unknown_scratch", "admission_wrong_pair", "admission_scratch_base", "admission_closed_scratch",
		"admission_stale_parent", "admission_checkpoint", "admission_root_while_live", "admission_restart_head", "admission_restart_lineage", "admission_restart_baseline", "admission_restart_delta",
		"admission_baseline", "admission_delta", "admission_root_baseline", "admission_root_delta", "admission_revision_unrecorded", "admission_revision_digest", "admission_oid_length",
		"generation_actor", "generation_context", "generation_clean_refs", "generation_conflict_refs", "generation_conflict_pin_missing", "generation_conflict_pin_id", "generation_conflict_pin_refs", "generation_conflict_pin_not_immediate",
		"generation_id_reused", "generation_unknown_request", "generation_ended_request", "generation_missing_supersedes", "generation_wrong_supersedes", "generation_unexpected_supersedes", "generation_unchanged_predecessor", "generation_superseded_report_unpin_missing", "generation_superseded_report_unpin_before",
		"generation_not_nearest", "generation_predecessor_generation", "generation_predecessor_commit", "generation_predecessor_tree", "generation_predecessor_conflict", "generation_missing_predecessor", "generation_unexpected_predecessor", "generation_revision", "generation_oid_length",
		"ending_actor", "ending_context", "ending_refs", "ending_unknown_request", "ending_ended_request", "ending_conflict_generation", "ending_clean_generation", "ending_live_predecessor",
		"ending_dependency_actor", "ending_dependency_live", "ending_dependency_other_lineage", "ending_dependency_later", "ending_suffix_live", "ending_unpin_missing", "ending_unpin_order", "ending_unpin_before", "ending_report_unpin",
		"closure_live", "closure_report_unpin_missing", "closure_report_unpin_order", "closure_report_unpin_before", "report_unpin_without_owner",
	}
	rows = append(rows, "generation_ended_id_reused")
	for _, name := range rows {
		t.Run(name, func(t *testing.T) {
			ci, at := h.locate(t, ledger.TypePromotionAdmitted, h.a2.RequestID, "", false)
			switch {
			case strings.HasPrefix(name, "generation_"):
				ci, at = h.locate(t, ledger.TypeGenerationDerived, h.a2.RequestID, ledger.GenerationClean, false)
				if strings.Contains(name, "conflict_") || name == "generation_predecessor_conflict" {
					ci, at = h.locate(t, ledger.TypeGenerationDerived, h.b1.RequestID, ledger.GenerationConflict, false)
				}
				if strings.Contains(name, "supersed") || name == "generation_unchanged_predecessor" {
					ci, at = h.locate(t, ledger.TypeGenerationDerived, h.b1.RequestID, ledger.GenerationClean, true)
				}
				if name == "generation_unexpected_supersedes" || name == "generation_unexpected_predecessor" {
					ci, at = h.locate(t, ledger.TypeGenerationDerived, h.a1.RequestID, ledger.GenerationClean, false)
				}
				if name == "generation_predecessor_conflict" {
					ci++
				}
				if name == "generation_ended_request" || name == "generation_ended_id_reused" {
					ci, at = h.locate(t, ledger.TypeGenerationDerived, h.a3.RequestID, ledger.GenerationConflict, false)
				}
			case strings.HasPrefix(name, "ending_"):
				ci, at = h.locate(t, ledger.TypePromotionFailed, h.a1.RequestID, "", false)
				if strings.HasPrefix(name, "ending_dependency_") {
					ci, at = h.locate(t, ledger.TypePromotionFailed, h.a2.RequestID, "", false)
				}
				if name == "ending_ended_request" || name == "ending_dependency_other_lineage" {
					ci, at = h.locate(t, ledger.TypePromotionFailed, h.b1.RequestID, "", false)
				}
				if name == "ending_live_predecessor" {
					ci, at = h.locate(t, ledger.TypeGenerationDerived, h.b1.RequestID, ledger.GenerationConflict, false)
				}
				if name == "ending_clean_generation" {
					ci, at = h.locate(t, ledger.TypeGenerationDerived, h.a2.RequestID, ledger.GenerationClean, false)
				}
			case strings.HasPrefix(name, "closure_"):
				ci, at = h.locate(t, ledger.TypeScratchClosed, uuid.UUID{}, "", false)
				if name == "closure_live" {
					ci, at = h.locate(t, ledger.TypeGenerationDerived, h.a1.RequestID, ledger.GenerationClean, false)
				}
			case name == "report_unpin_without_owner":
				ci, at = h.locate(t, ledger.TypeGenerationDerived, h.b1.RequestID, ledger.GenerationClean, true)
			case strings.HasPrefix(name, "admission_restart_"):
				ci, at = h.locate(t, ledger.TypePromotionAdmitted, h.a3.RequestID, "", false)
			case strings.HasPrefix(name, "admission_root_") && name != "admission_root_while_live":
				ci, at = h.locate(t, ledger.TypePromotionAdmitted, h.b1.RequestID, "", false)
			case name == "admission_closed_scratch":
				ci, _ = h.locate(t, ledger.TypeScratchClosed, uuid.UUID{}, "", false)
				ci++
				at = 0
			}
			var drafts []ledger.Draft
			if ci < len(h.commits) {
				for _, record := range h.commits[ci].Records {
					d := record.Draft
					d.BodyRefs = slices.Clone(d.BodyRefs)
					drafts = append(drafts, d)
				}
			}
			a1 := admittedRecord(t, h.f, h.a1.RequestID)
			a2 := admittedRecord(t, h.f, h.a2.RequestID)
			b1 := admittedRecord(t, h.f, h.b1.RequestID)
			badAt := at
			if strings.HasPrefix(name, "admission_") {
				if name == "admission_closed_scratch" {
					p := a1
					p.RequestID, p.CaptureID, p.LineageID, p.PinID = uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
					drafts = []ledger.Draft{{Type: ledger.TypePromotionAdmitted, TypeVersion: 1, Actor: ledger.ActorAgent, ContextID: p.ContextID, BodyRefs: []ledger.Digest{p.ManifestSHA256}, Payload: encoded(t, p)}, {Type: ledger.TypeBodyPin, TypeVersion: 1, Actor: ledger.ActorAgent, BodyRefs: []ledger.Digest{p.ManifestSHA256}, Payload: encoded(t, ledger.BodyPin{PinID: p.PinID})}}
				}
				p, _ := ledger.DecodePromotionAdmitted(drafts[at].Payload)
				switch name {
				case "admission_actor":
					drafts[at].Actor = ledger.ActorCore
				case "admission_context":
					drafts[at].ContextID = uuid.NewV7()
				case "admission_body_refs":
					drafts[at].BodyRefs = nil
				case "admission_pin_missing":
					drafts = drafts[:at+1]
				case "admission_pin_id":
					drafts[at+1].Payload = encoded(t, ledger.BodyPin{PinID: uuid.NewV7()})
				case "admission_pin_refs":
					drafts[at+1].BodyRefs = []ledger.Digest{h.f.w.InputsSHA256}
				case "admission_pin_not_immediate":
					drafts = append(drafts[:at+1], append([]ledger.Draft{{Type: ledger.TypeBodyPin, TypeVersion: 1, Actor: ledger.ActorCore, BodyRefs: []ledger.Digest{h.f.w.InputsSHA256}, Payload: encoded(t, ledger.BodyPin{PinID: uuid.NewV7()})}}, drafts[at+1:]...)...)
				case "admission_request_reused":
					p.RequestID = h.a1.RequestID
				case "admission_capture_reused":
					p.CaptureID = a1.CaptureID
				case "admission_root_lineage_reused", "admission_restart_lineage":
					p.LineageID = a1.LineageID
				case "admission_successor_lineage":
					p.LineageID = uuid.NewV7()
				case "admission_unknown_workspace":
					p.WorkspaceID = uuid.NewV7()
				case "admission_unknown_scratch":
					p.ScratchID = uuid.NewV7()
				case "admission_wrong_pair":
					p.ScratchID = b1.ScratchID
				case "admission_scratch_base":
					p.ScratchBase = p.SealedTree
				case "admission_stale_parent":
					p.ParentRequestID = uuid.NewV7()
				case "admission_checkpoint":
					p.Checkpoint++
				case "admission_root_while_live":
					p.ParentRequestID, p.Checkpoint, p.LineageID = uuid.UUID{}, 1, uuid.NewV7()
				case "admission_restart_head":
					p.RestartFrom = h.a1.RequestID
				case "admission_restart_baseline", "admission_restart_delta":
					if name == "admission_restart_baseline" {
						p.BaselineTree = a2.SealedTree
					} else {
						p.DeltaBaseTree = a2.SealedTree
					}
				case "admission_baseline", "admission_root_baseline":
					p.BaselineTree = p.ScratchBase
				case "admission_delta", "admission_root_delta":
					p.DeltaBaseTree = p.ScratchBase
				case "admission_revision_unrecorded":
					p.InputsRevision++
				case "admission_revision_digest":
					p.CaptureInputsSHA256 = p.ManifestSHA256
				case "admission_oid_length":
					p.SealedTree = strings.Repeat("a", 64)
				}
				drafts[at].Payload = encoded(t, p)
			} else if strings.HasPrefix(name, "generation_") {
				if name == "generation_predecessor_conflict" {
					a := b1
					a.RequestID, a.CaptureID, a.PinID = uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
					a.ParentRequestID, a.Checkpoint = b1.RequestID, 2
					a.BaselineTree, a.DeltaBaseTree = b1.SealedTree, b1.SealedTree
					g := generations(t, h.f, h.a1.RequestID)[0]
					g.RequestID, g.GenerationID = a.RequestID, uuid.NewV7()
					g.PredecessorRequestID = b1.RequestID
					g.PredecessorGenerationID = generations(t, h.f, b1.RequestID)[0].GenerationID
					pin, err := pinDraft(a.PinID, a.ManifestSHA256, a.ContextID)
					if err != nil {
						t.Fatal(err)
					}
					drafts = []ledger.Draft{
						{Type: ledger.TypePromotionAdmitted, TypeVersion: 1, Actor: ledger.ActorAgent, ContextID: a.ContextID, BodyRefs: []ledger.Digest{a.ManifestSHA256}, Payload: encoded(t, a)},
						pin,
						{Type: ledger.TypeGenerationDerived, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: a.ContextID, Payload: encoded(t, g)},
					}
					at, badAt = 2, 2
				}
				p, _ := ledger.DecodeGenerationDerived(drafts[at].Payload)
				first := generations(t, h.f, h.a1.RequestID)[0]
				oldB := generations(t, h.f, h.b1.RequestID)[0]
				switch name {
				case "generation_actor":
					drafts[at].Actor = ledger.ActorOwner
				case "generation_context":
					drafts[at].ContextID = uuid.NewV7()
				case "generation_clean_refs":
					drafts[at].BodyRefs = []ledger.Digest{h.f.w.InputsSHA256}
				case "generation_conflict_refs":
					drafts[at].BodyRefs = nil
				case "generation_conflict_pin_missing":
					drafts = drafts[:at+1]
				case "generation_conflict_pin_id":
					drafts[at+1].Payload = encoded(t, ledger.BodyPin{PinID: uuid.NewV7()})
				case "generation_conflict_pin_refs":
					drafts[at+1].BodyRefs = []ledger.Digest{h.f.w.InputsSHA256}
				case "generation_conflict_pin_not_immediate":
					drafts = append(drafts[:at+1], append([]ledger.Draft{{Type: ledger.TypeBodyPin, TypeVersion: 1, Actor: ledger.ActorCore, BodyRefs: []ledger.Digest{h.f.w.InputsSHA256}, Payload: encoded(t, ledger.BodyPin{PinID: uuid.NewV7()})}}, drafts[at+1:]...)...)
				case "generation_id_reused", "generation_ended_id_reused":
					p.GenerationID = first.GenerationID
				case "generation_unknown_request":
					p.RequestID = uuid.NewV7()
				case "generation_ended_request":
					p.RequestID = h.a1.RequestID
				case "generation_missing_supersedes":
					p.Supersedes = uuid.UUID{}
				case "generation_wrong_supersedes", "generation_unexpected_supersedes":
					p.Supersedes = uuid.NewV7()
				case "generation_unchanged_predecessor":
					p.PredecessorCommit = oldB.PredecessorCommit
				case "generation_superseded_report_unpin_missing":
					drafts = drafts[:at+1]
				case "generation_superseded_report_unpin_before":
					drafts[0], drafts[1] = drafts[1], drafts[0]
					at, badAt = 1, 0
				case "generation_not_nearest":
					p.PredecessorRequestID = h.b1.RequestID
				case "generation_predecessor_generation":
					p.PredecessorGenerationID = uuid.NewV7()
				case "generation_predecessor_commit":
					p.PredecessorCommit = a1.ScratchBase
				case "generation_predecessor_tree":
					p.PredecessorTree = a1.BaselineTree
				case "generation_missing_predecessor":
					p.PredecessorRequestID, p.PredecessorGenerationID = uuid.UUID{}, uuid.UUID{}
				case "generation_unexpected_predecessor":
					p.PredecessorRequestID, p.PredecessorGenerationID = h.a2.RequestID, uuid.NewV7()
				case "generation_revision":
					p.InputsRevision++
				case "generation_oid_length":
					p.DerivedTree = strings.Repeat("a", 64)
				}
				drafts[at].Payload = encoded(t, p)
			} else if strings.HasPrefix(name, "ending_") {
				if name == "ending_clean_generation" || name == "ending_live_predecessor" {
					g, _ := ledger.DecodeGenerationDerived(drafts[at].Payload)
					p := ledger.PromotionFailed{RequestID: g.RequestID, Outcome: ledger.FailedConflict, GenerationID: g.GenerationID}
					a := admittedRecord(t, h.f, g.RequestID)
					drafts = append(drafts, ledger.Draft{Type: ledger.TypePromotionFailed, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: a.ContextID, Payload: encoded(t, p)}, ledger.Draft{Type: ledger.TypeBodyUnpin, TypeVersion: 1, Actor: ledger.ActorCore, Payload: encoded(t, ledger.BodyUnpin{PinID: a.PinID})})
					badAt = len(drafts) - 2
				} else {
					p, _ := ledger.DecodePromotionFailed(drafts[at].Payload)
					switch name {
					case "ending_actor", "ending_dependency_actor":
						drafts[at].Actor = ledger.ActorOwner
					case "ending_context":
						drafts[at].ContextID = uuid.NewV7()
					case "ending_refs":
						drafts[at].BodyRefs = []ledger.Digest{h.f.w.InputsSHA256}
					case "ending_unknown_request":
						p.RequestID = uuid.NewV7()
					case "ending_ended_request":
						p.RequestID = h.a1.RequestID
					case "ending_conflict_generation":
						p.GenerationID = uuid.NewV7()
					case "ending_dependency_live":
						p.FailedRequestID = h.b1.RequestID
					case "ending_dependency_other_lineage":
						p.Outcome, p.GenerationID, p.FailedRequestID = ledger.FailedDependencyFailed, uuid.UUID{}, h.a1.RequestID
					case "ending_dependency_later":
						p.FailedRequestID = h.a2.RequestID
					case "ending_suffix_live":
						drafts = append(drafts[:at+1], drafts[at+2:len(drafts)-1]...)
					case "ending_unpin_missing":
						drafts = drafts[:len(drafts)-1]
					case "ending_unpin_order":
						n := len(drafts)
						drafts[n-1], drafts[n-2] = drafts[n-2], drafts[n-1]
					case "ending_unpin_before":
						n := len(drafts)
						drafts[at], drafts[n-2] = drafts[n-2], drafts[at]
						badAt = at
					case "ending_report_unpin":
						g, _ := ledger.DecodeGenerationDerived(drafts[0].Payload)
						drafts = append(drafts, ledger.Draft{Type: ledger.TypeBodyUnpin, TypeVersion: 1, Actor: ledger.ActorCore, Payload: encoded(t, ledger.BodyUnpin{PinID: g.ReportPinID})})
						badAt = len(drafts) - 1
					}
					if name != "ending_unpin_before" {
						drafts[at].Payload = encoded(t, p)
					}
				}
			} else if strings.HasPrefix(name, "closure_") {
				switch name {
				case "closure_live":
					drafts = []ledger.Draft{{Type: ledger.TypeScratchClosed, TypeVersion: 1, Actor: ledger.ActorOwner, ContextID: a1.ContextID, Payload: encoded(t, ledger.ScratchClosed{ScratchID: a1.ScratchID, ContextID: a1.ContextID, Reason: "abandoned"})}}
					badAt = 0
				case "closure_report_unpin_missing":
					drafts = drafts[:len(drafts)-1]
				case "closure_report_unpin_order":
					drafts[1], drafts[2] = drafts[2], drafts[1]
				case "closure_report_unpin_before":
					drafts[0], drafts[1] = drafts[1], drafts[0]
					badAt = 0
				}
			} else {
				drafts = drafts[len(drafts)-1:]
				badAt = 0
			}
			colony := t.TempDir()
			store, err := ledgerstore.Create(colony, ledger.Defined())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			publish := func(ds []ledger.Draft) {
				for _, d := range ds {
					for _, digest := range d.BodyRefs {
						b, err := h.f.store.ReadBody(context.Background(), digest)
						if err != nil {
							t.Fatal(err)
						}
						if _, _, err := store.PublishBody(context.Background(), bytes.NewReader(b)); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			for _, commit := range h.commits[:ci] {
				var ds []ledger.Draft
				for _, r := range commit.Records {
					ds = append(ds, r.Draft)
				}
				publish(ds)
				if _, err := store.Append(context.Background(), ds); err != nil {
					t.Fatal(err)
				}
			}
			publish(drafts)
			svc, _, err := openService(context.Background(), store, colony, serviceConfig(), serviceDeps{environ: func() []string { return h.f.env }})
			if err != nil {
				t.Fatalf("valid prefix failed: %v", err)
			}
			before := store.Durable()
			if err := svc.append(context.Background(), drafts); !errors.Is(err, ErrInvalid) {
				t.Fatalf("append admitted rule break: %v", err)
			}
			if store.Durable() != before {
				t.Fatal("invalid draft wrote records")
			}
			if err := svc.Close(); err != nil {
				t.Fatal(err)
			}
			ack, err := store.Append(context.Background(), drafts)
			if err != nil {
				t.Fatalf("invalid-history fixture refused by store: %v", err)
			}
			_, _, err = openService(context.Background(), store, colony, serviceConfig(), serviceDeps{environ: func() []string { return h.f.env }})
			var inconsistent *InconsistentError
			if !errors.As(err, &inconsistent) || !errors.Is(err, ErrInconsistent) || inconsistent.Seq != ack.First+uint64(badAt) {
				t.Fatalf("want inconsistent at %d, got %v", ack.First+uint64(badAt), err)
			}
		})
	}
}

func TestReplayUnknownPromotionFailure(t *testing.T) {
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	first, next := rootRequest(s), rootRequest(s)
	next.Head = first.RequestID
	mustAdmit(t, f, first)
	mustAdmit(t, f, next)
	drafts := []ledger.Draft{
		{Type: ledger.TypePromotionFailed, TypeVersion: 1, Actor: ledger.ActorAgent, ContextID: s.ContextID, Payload: encoded(t, ledger.PromotionFailed{RequestID: first.RequestID, Outcome: "future_failure", GenerationID: uuid.NewV7(), FailedRequestID: uuid.NewV7()})},
		{Type: ledger.TypePromotionFailed, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: s.ContextID, Payload: encoded(t, ledger.PromotionFailed{RequestID: next.RequestID, Outcome: ledger.FailedDependencyFailed, FailedRequestID: first.RequestID})},
	}
	for _, r := range []Request{first, next} {
		a := admittedRecord(t, f, r.RequestID)
		unpin, err := unpinDraft(a.PinID, s.ContextID)
		if err != nil {
			t.Fatal(err)
		}
		drafts = append(drafts, unpin)
	}
	before := snapshotRequestEffects(t, f)
	if err := f.svc.append(context.Background(), drafts); !errors.Is(err, ErrInvalid) {
		t.Fatalf("producer wrote an undefined outcome: %v", err)
	}
	assertRequestEffects(t, f, before)
	if _, err := f.store.Append(context.Background(), drafts); err != nil {
		t.Fatal(err)
	}
	if report := f.reopen(t, serviceConfig()); len(report.Pending) != 0 {
		t.Fatalf("future failure replay: %+v", report)
	}
	got, err := f.svc.RequestPromotion(context.Background(), first)
	if err != nil || got.State != "future_failure" {
		t.Fatalf("unknown outcome rejected or misclassified: %+v %v", got, err)
	}
	got, err = f.svc.RequestPromotion(context.Background(), next)
	if err != nil || got.State != RequestDependencyFailed || got.FailedRequest != first.RequestID {
		t.Fatalf("unknown outcome failed to end its lineage: %+v %v", got, err)
	}
	restart := rootRequest(s)
	restart.RestartFrom = next.RequestID
	assertUnavailable(t, promotionError(context.Background(), f.svc, restart), "no_validator")
}
