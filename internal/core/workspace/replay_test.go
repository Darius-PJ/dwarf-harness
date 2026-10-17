package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
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
	t.Parallel()
	rows := []string{
		"registered_actor", "registered_body_refs", "registered_index", "registered_commit_count", "registration_without_pin", "duplicate_workspace", "duplicate_common_dir", "registration_revision", "registration_workspace",
		"inputs_actor", "inputs_workspace", "inputs_body_refs_absent", "inputs_document_omitted", "inputs_pin_id", "inputs_pin_refs", "inputs_pin_index", "revision_one_outside_registration", "revision_gap", "update_index", "update_without_unpin", "update_wrong_unpin", "current_document_exact_set", "current_document_malformed",
		"scratch_actor", "scratch_context_envelope", "scratch_body_refs", "duplicate_scratch_id", "second_open_pair", "scratch_unknown_workspace", "reused_dir", "reused_branch", "branch_directory_scheme", "sha1_base_length", "sha1_anchor_length",
		"closure_actor", "closure_agent_context", "closure_body_refs", "closure_unknown", "closure_twice", "closure_payload_context",
		"workspace_suffix_collision",
	}
	for _, name := range rows {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
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
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	payload := encoded(t, ledger.ScratchClosed{ScratchID: s.ID, ContextID: s.ContextID, Reason: "future_reason"})
	if _, err := f.store.Append(context.Background(), []ledger.Draft{{Type: ledger.TypeScratchClosed, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	f.reopen(t, serviceConfig())
	if _, err := f.svc.BeginWrite(context.Background(), testWriteBinding(s)); !errors.Is(err, ErrNoScratch) {
		t.Fatalf("unknown reason not treated as closure: %v", err)
	}
}

func TestReplayNeverReadsSupersededInputs(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	h := buildPromotionHistory(t)
	// Every row has a different colony ID, so the shared repository's marker
	// makes it unavailable for reconciliation. Only its immutable history and
	// bodies are shared; check that the reference fixture stays untouched.
	before := snapshotRequestEffects(t, h.f)
	t.Cleanup(func() { assertRequestEffects(t, h.f, before) })
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
		"manifest_unpin_without_ending",
	}
	rows = append(rows, "generation_ended_id_reused")
	for _, name := range rows {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
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
			} else if name == "manifest_unpin_without_ending" {
				drafts = []ledger.Draft{{Type: ledger.TypeBodyUnpin, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: a1.ContextID, Payload: encoded(t, ledger.BodyUnpin{PinID: a1.PinID})}}
				badAt = 0
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
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	first, next := rootRequest(s), rootRequest(s)
	next.Head = first.RequestID
	mustAdmit(t, f, first)
	mustAdmit(t, f, next)
	drafts := []ledger.Draft{
		{Type: ledger.TypePromotionFailed, TypeVersion: 1, Actor: ledger.ActorAgent, ContextID: s.ContextID, Payload: encoded(t, ledger.PromotionFailed{RequestID: first.RequestID, Outcome: "accepted", GenerationID: uuid.NewV7(), FailedRequestID: uuid.NewV7()})},
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
	if err != nil || got.State != RequestUnknownFailure || got.FailureOutcome == "" {
		t.Fatalf("unknown outcome rejected or misclassified: %+v %v", got, err)
	}
	got, err = f.svc.RequestPromotion(context.Background(), next)
	if err != nil || got.State != RequestDependencyFailed || got.FailedRequest != first.RequestID {
		t.Fatalf("unknown outcome failed to end its lineage: %+v %v", got, err)
	}
	successor := rootRequest(s)
	successor.Head = next.RequestID
	if err := promotionError(context.Background(), f.svc, successor); !errors.Is(err, ErrStaleHead) {
		t.Fatalf("unknown failure allowed a successor: %v", err)
	}
	restart := rootRequest(s)
	restart.RestartFrom = next.RequestID
	assertUnavailable(t, promotionError(context.Background(), f.svc, restart), "no_validator")
}

func TestTipRepairAtOpenAndBeforeUse(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, mode := range []string{"old_open", "absent_open", "other_open", "old_observe", "old_fork", "other_writer", "other_lease", "other_admit", "other_close", "after_ack"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			ctx := context.Background()
			task := mustScratch(t, f, uuid.NewV7())
			first := mustObserve(t, mustWriter(t, f, testWriteBinding(task)))
			var writer *Writer
			if mode == "old_observe" || mode == "after_ack" || mode == "other_lease" {
				writer = mustWriter(t, f, testWriteBinding(task))
			}
			thirdTree := integrationTree(t, f.gitFixture, map[string]string{"foreign": "do not overwrite\n"})
			third := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "commit-tree", "-m", "foreign", thirdTree)))
			if mode == "after_ack" {
				f.svc.deps.beforeAppend = func() {
					gitCommand(t, f.env, f.root, nil, "update-ref", task.Branch, third)
				}
				acknowledged, err := writer.CommitObserved(ctx)
				assertUnavailable(t, err, "scratch_tip_mismatch")
				if acknowledged.ID == (uuid.UUID{}) {
					t.Fatal("post-ack failure lost the durable observation")
				}
				writer.Close()
				f.svc.deps.beforeAppend = nil
				before := f.store.Durable()
				report := f.reopen(t, serviceConfig())
				if len(report.Pending) == 0 || f.store.Durable() != before {
					t.Fatalf("Open did not retain the blocked acknowledgment: %+v", report)
				}
				gitCommand(t, f.env, f.root, nil, "update-ref", task.Branch, first.Commit, third)
				repaired := mustWriter(t, f, testWriteBinding(task))
				if repaired.Scratch().TipCommit != acknowledged.Commit {
					t.Fatal("pre-use repair did not recover acknowledged transition")
				}
				return
			}
			if mode == "other_lease" {
				gitCommand(t, f.env, f.root, nil, "update-ref", task.Branch, third, first.Commit)
				b := testWriteBinding(task)
				b.ContextID = uuid.NewV7()
				_, err := f.svc.BeginWrite(ctx, b)
				assertUnavailable(t, err, "scratch_tip_mismatch")
				release, err := writer.Lease(ctx)
				if release != nil {
					release()
				}
				assertUnavailable(t, err, "scratch_tip_mismatch")
				gitCommand(t, f.env, f.root, nil, "update-ref", task.Branch, task.BaseCommit, third)
				mustWriter(t, f, b)
				// Leasing must use the repaired projection, even without Git.
				gitPath := f.svc.git.path
				f.svc.git.path = filepath.Join(t.TempDir(), "missing-git")
				release, err = writer.Lease(ctx)
				f.svc.git.path = gitPath
				if err != nil {
					t.Fatalf("lease after successful repair: %v", err)
				}
				release()
				return
			}
			switch {
			case mode == "absent_open":
				gitCommand(t, f.env, f.root, nil, "update-ref", "-d", task.Branch, first.Commit)
			case strings.HasPrefix(mode, "other_"):
				gitCommand(t, f.env, f.root, nil, "update-ref", task.Branch, third, first.Commit)
			default:
				gitCommand(t, f.env, f.root, nil, "update-ref", task.Branch, task.BaseCommit, first.Commit)
			}
			before := f.store.Durable()
			switch mode {
			case "old_open", "absent_open", "other_open":
				report := f.reopen(t, serviceConfig())
				if (len(report.Pending) != 0) != (mode == "other_open") || f.store.Durable() != before {
					t.Fatalf("tip reconciliation: %+v", report)
				}
			case "old_observe":
				next := mustObserve(t, writer)
				if parent := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "rev-parse", next.Commit+"^"))); parent != first.Commit {
					t.Fatal("observation read stale branch rather than recorded tip")
				}
				first = next
			case "old_fork":
				b := testWriteBinding(task)
				b.ContextID = uuid.NewV7()
				temporary := mustWriter(t, f, b)
				if temporary.Scratch().BaseCommit != first.Commit {
					t.Fatal("temporary fork ignored repaired tip")
				}
			case "other_writer":
				_, err := f.svc.BeginWrite(ctx, testWriteBinding(task))
				assertUnavailable(t, err, "scratch_tip_mismatch")
			case "other_admit":
				assertUnavailable(t, promotionError(ctx, f.svc, rootRequest(task)), "scratch_tip_mismatch")
			case "other_close":
				_, err := f.svc.CloseScratch(ctx, ownerClosure(task))
				assertUnavailable(t, err, "scratch_tip_mismatch")
			}
			want := first.Commit
			if strings.HasPrefix(mode, "other_") {
				want = third
			}
			got, err := f.git.readRef(ctx, f.colony, f.p.CommonDir, task.Branch, 40)
			if err != nil || got != want {
				t.Fatalf("tip was not repaired safely: %s want %s: %v", got, want, err)
			}
		})
	}
}

func TestScratchCommitAndMergeRefsRetained(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	ctx := context.Background()
	task := mustScratch(t, f, uuid.NewV7())
	first := mustObserve(t, mustWriter(t, f, testWriteBinding(task)))
	b := testWriteBinding(task)
	b.ContextID = uuid.NewV7()
	writer := mustWriter(t, f, b)
	temporary := writer.Scratch()
	writeTestFile(t, filepath.Join(temporary.Dir, "private.txt"), []byte("retained branch\n"))
	source := mustObserve(t, writer)
	second := mustObserve(t, mustWriter(t, f, testWriteBinding(task)))
	firstRef := "refs/dwarf-harness/scratch-commits/" + first.ID.String()
	sourceRef := "refs/dwarf-harness/scratch-commits/" + source.ID.String()
	gitCommand(t, f.env, f.root, nil, "update-ref", "-d", firstRef, first.Commit)
	stray := "refs/dwarf-harness/scratch-commits/" + uuid.NewV7().String()
	gitCommand(t, f.env, f.root, nil, "update-ref", stray, task.BaseCommit)
	before := f.store.Durable()
	if report := f.reopen(t, serviceConfig()); len(report.Pending) != 0 || f.store.Durable() != before {
		t.Fatalf("retention replay: %+v", report)
	}
	refs, err := f.svc.listRefs(ctx, f.svc.workspaces[f.w.ID], "refs/dwarf-harness/scratch-commits/")
	if err != nil || refs[firstRef] != first.Commit || refs[stray] != "" || len(refs) != 3 {
		t.Fatalf("open observation/fork refs not retained: %v %v", refs, err)
	}
	if _, err := f.svc.CloseScratch(ctx, ownerClosure(temporary)); err != nil {
		t.Fatal(err)
	}
	got, err := f.git.readRef(ctx, f.colony, f.p.CommonDir, sourceRef, 40)
	if err != nil || got != "" {
		t.Fatalf("closed observation ref leaked: %s %v", got, err)
	}
	gitCommand(t, f.env, f.root, nil, "gc", "--prune=now")
	if got := string(gitCommand(t, f.env, f.root, nil, "show", source.Commit+":private.txt")); got != "retained branch\n" {
		t.Fatal("removal lost temporary private history")
	}
	gitCommand(t, f.env, f.root, nil, "update-ref", firstRef, task.BaseCommit, first.Commit)
	report := f.reopen(t, serviceConfig())
	if len(report.Pending) == 0 {
		t.Fatal("foreign retention ref silently accepted")
	}
	if got, _ := f.git.readRef(ctx, f.colony, f.p.CommonDir, firstRef, 40); got != task.BaseCommit {
		t.Fatal("foreign retention ref overwritten")
	}
	gitCommand(t, f.env, f.root, nil, "update-ref", firstRef, first.Commit, task.BaseCommit)
	if _, err := f.svc.CloseScratch(ctx, ownerClosure(task)); err != nil {
		t.Fatal(err)
	}
	refs, err = f.svc.listRefs(ctx, f.svc.workspaces[f.w.ID], "refs/dwarf-harness/scratch-commits/")
	if err != nil || len(refs) != 0 {
		t.Fatalf("closed parent refs leaked: %v %v", refs, err)
	}
	gitCommand(t, f.env, f.root, nil, "cat-file", "-e", second.Commit)
	for _, admitted := range []bool{false, true} {
		t.Run(fmt.Sprintf("merge_provenance_%t", admitted), func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, f.gitFixture)
			task := mustScratch(t, f, uuid.NewV7())
			source, sourceObservation := observedTemporary(t, f, task, map[string]string{"base.txt": "source\n"})
			root := rootRequest(task)
			if admitted {
				f.svc.deps.afterMergeBackApplied = func() { mustAdmit(t, f, root) }
			}
			merged, err := f.svc.MergeBack(ctx, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			mustObserve(t, mustWriter(t, f, testWriteBinding(task)))
			mergeRef := "refs/dwarf-harness/scratch-commits/" + merged.ID.String()
			sourceRef := "refs/dwarf-harness/scratch-commits/" + sourceObservation.ID.String()
			if admitted {
				p := admittedRecord(t, f, root.RequestID)
				if !slices.Equal(p.MergeBackIDs, []uuid.UUID{merged.ID}) || f.svc.liveCount(task.ID) != 1 || !f.svc.scratches[source.ID].closed {
					t.Fatal("source closure lost live admitted provenance")
				}
				if got, err := f.git.readRef(ctx, f.colony, f.p.CommonDir, mergeRef, 40); err != nil || got != merged.TargetCommit {
					t.Fatalf("live provenance ref lost at source closure: %s %v", got, err)
				}
				advanceMain(t, f, map[string]string{"base.txt": "foreign\n"})
				mustIntegrate(t, f)
			}
			if got, err := f.git.readRef(ctx, f.colony, f.p.CommonDir, sourceRef, 40); err != nil || got != "" {
				t.Fatalf("admission restored released source observation: %s %v", got, err)
			}
			gitCommand(t, f.env, f.root, nil, "update-ref", "-d", mergeRef, merged.TargetCommit)
			before := f.store.Durable()
			if report := f.reopen(t, serviceConfig()); len(report.Pending) != 0 || f.store.Durable() != before {
				t.Fatalf("open-parent merge ref recovery: %+v", report)
			}
			if got, err := f.git.readRef(ctx, f.colony, f.p.CommonDir, mergeRef, 40); err != nil || got != merged.TargetCommit {
				t.Fatalf("retained target not restored: %s %v", got, err)
			}
			gitCommand(t, f.env, f.root, nil, "update-ref", mergeRef, task.BaseCommit, merged.TargetCommit)
			if report := f.reopen(t, serviceConfig()); len(report.Pending) == 0 {
				t.Fatal("foreign merge retention ref accepted")
			}
			if got, _ := f.git.readRef(ctx, f.colony, f.p.CommonDir, mergeRef, 40); got != task.BaseCommit {
				t.Fatal("foreign merge retention ref overwritten")
			}
			gitCommand(t, f.env, f.root, nil, "update-ref", mergeRef, merged.TargetCommit, task.BaseCommit)
			writer := mustWriter(t, f, testWriteBinding(task))
			writeTestFile(t, filepath.Join(task.Dir, "base.txt"), []byte("one\ntwo\n"))
			mustObserve(t, writer)
			if _, err := f.svc.CloseScratch(ctx, ownerClosure(task)); err != nil {
				t.Fatal(err)
			}
			if report := f.reopen(t, serviceConfig()); len(report.Pending) != 0 {
				t.Fatalf("closed retention: %+v", report)
			}
			refs, err := f.svc.listRefs(ctx, f.svc.workspaces[f.w.ID], "refs/dwarf-harness/scratch-commits/")
			if err != nil {
				t.Fatal(err)
			}
			if len(refs) != 0 {
				t.Fatalf("ended or unadmitted closed merge refs leaked: %v", refs)
			}
			gitCommand(t, f.env, f.root, nil, "gc", "--prune=now")
			gitCommand(t, f.env, f.root, nil, "cat-file", "-e", merged.TargetCommit)
			if got := string(gitCommand(t, f.env, f.root, nil, "show", sourceObservation.Commit+":base.txt")); got != "source\n" {
				t.Fatal("released observation ref lost moved source history")
			}
		})
	}
}

func TestOpenFailsClosedOnF2_07RuleBreak(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	ctx := context.Background()
	otherRoot := filepath.Join(t.TempDir(), "repo")
	gitCommand(t, f.env, f.root, nil, "clone", "--no-hardlinks", "--no-checkout", "-b", "main", f.root, otherRoot)
	gitCommand(t, f.env, otherRoot, nil, "checkout", "--detach")
	otherProposal, err := f.git.inspect(ctx, otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	otherWorkspace, err := f.svc.Register(ctx, otherProposal)
	if err != nil {
		t.Fatal(err)
	}
	task, author := mustScratch(t, f, uuid.NewV7()), mustScratch(t, f, uuid.NewV7())
	closed := mustScratch(t, f, uuid.NewV7())
	if _, err := f.svc.CloseScratch(ctx, ownerClosure(closed)); err != nil {
		t.Fatal(err)
	}
	parentObservation := mustObserve(t, mustWriter(t, f, testWriteBinding(task)))
	b := testWriteBinding(task)
	b.ContextID = uuid.NewV7()
	siblingWriter := mustWriter(t, f, b)
	sibling := siblingWriter.Scratch()
	siblingWriter.Close()
	b.ContextID, b.WriterID = author.ContextID, uuid.NewV7()
	writer := mustWriter(t, f, b)
	temporary := writer.Scratch()
	writeTestFile(t, filepath.Join(temporary.Dir, "work.txt"), []byte("first\n"))
	first := mustObserve(t, writer)
	b.WriterID, b.ResumeScratchID = uuid.NewV7(), temporary.ID
	writer = mustWriter(t, f, b)
	writeTestFile(t, filepath.Join(temporary.Dir, "work.txt"), []byte("second\n"))
	second := mustObserve(t, writer)
	if _, err := f.svc.CloseScratch(ctx, ownerClosure(temporary)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CloseScratch(ctx, ownerClosure(sibling)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CloseScratch(ctx, ownerClosure(task)); err != nil {
		t.Fatal(err)
	}
	commits := serviceCommits(t, f.store)
	creationAt, observationAt, closureAt := -1, -1, -1
	var parent ledger.ScratchChanged
	for i, commit := range commits {
		for _, record := range commit.Records {
			switch record.Type {
			case ledger.TypeScratchCreated:
				p, _ := ledger.DecodeScratchCreated(record.Payload)
				if p.ScratchID == temporary.ID {
					creationAt = i
				}
			case ledger.TypeScratchChanged:
				p, _ := ledger.DecodeScratchChanged(record.Payload)
				if p.ObservationID == second.ID {
					observationAt = i
				}
				if p.ObservationID == parentObservation.ID {
					parent = p
				}
			case ledger.TypeScratchClosed:
				p, _ := ledger.DecodeScratchClosed(record.Payload)
				if p.ScratchID == temporary.ID {
					closureAt = i
				}
			}
		}
	}
	if creationAt < 0 || observationAt < 0 || closureAt < 0 {
		t.Fatal("history lacks temporary lifecycle")
	}
	rows := []string{
		"parent_unknown", "parent_closed", "parent_nested", "parent_same_context", "parent_workspace", "parent_fork_commit", "parent_fork_tree",
		"observation_actor", "observation_context", "observation_refs", "observation_pin_missing", "observation_pin_id", "observation_pin_refs", "observation_pin_not_immediate",
		"observation_id_reused", "observation_id_reused_closed", "observation_unknown", "observation_closed", "observation_workspace", "observation_author", "observation_parent",
		"observation_previous_commit", "observation_previous_tree", "observation_revision", "observation_digest", "observation_oid_length",
		"observation_changed_paths",
		"observation_unpin_missing", "observation_unpin_wrong", "observation_unpin_before", "observation_unpin_repeated", "observation_unpin_early",
		"closure_child_open", "closure_unpin_missing", "closure_unpin_before", "closure_unpin_repeated",
	}
	prefixes := snapshotReplayPrefixes(t, f, commits, []int{creationAt, observationAt, closureAt, closureAt + 1})
	// Apply every mutation in both modes on one journalled projection. Keep
	// real Opens for record routing, body-store and ordering obligations.
	fixture := f.gitFixture
	fixture.colony = t.TempDir()
	projection := openFixtureService(t, fixture).svc
	assertApply := func(t *testing.T, at int, drafts []ledger.Draft) {
		t.Helper()
		var undos []func()
		defer func() {
			for i := len(undos) - 1; i >= 0; i-- {
				undos[i]()
			}
		}()
		for _, commit := range commits[:at] {
			var prefix []ledger.Draft
			for _, record := range commit.Records {
				prefix = append(prefix, record.Draft)
			}
			undo, err := projection.apply(ctx, prefix, commit.First, false)
			if err != nil {
				t.Fatalf("valid prefix: %v", err)
			}
			undos = append(undos, undo)
		}
		first := commits[at].First
		for _, producing := range []bool{true, false} {
			undo, err := projection.apply(ctx, drafts, first, producing)
			if err == nil {
				undo()
				t.Errorf("rule break accepted producing=%t", producing)
				continue
			}
			var inconsistent *InconsistentError
			if !errors.As(err, &inconsistent) || inconsistent.Seq != first {
				t.Errorf("want inconsistent at %d producing=%t: %v", first, producing, err)
			}
		}
	}
	for _, name := range rows {
		t.Run(name, func(t *testing.T) {
			at := observationAt
			if strings.HasPrefix(name, "parent_") {
				at = creationAt
			}
			if strings.HasPrefix(name, "closure_") {
				at = closureAt
			}
			if name == "observation_closed" || name == "observation_id_reused_closed" {
				at = closureAt + 1
			}
			source := at
			if strings.HasPrefix(name, "observation_") {
				source = observationAt
			}
			drafts := make([]ledger.Draft, len(commits[source].Records))
			for i, record := range commits[source].Records {
				drafts[i] = record.Draft
			}
			if strings.HasPrefix(name, "parent_") {
				p, _ := ledger.DecodeScratchCreated(drafts[0].Payload)
				switch name {
				case "parent_unknown":
					p.ParentScratchID = uuid.NewV7()
				case "parent_closed":
					p.ParentScratchID = closed.ID
				case "parent_nested":
					p.ParentScratchID = sibling.ID
				case "parent_same_context":
					p.ContextID, drafts[0].ContextID = task.ContextID, task.ContextID
				case "parent_workspace":
					p.WorkspaceID = otherWorkspace.ID
				case "parent_fork_commit":
					p.BaseCommit = task.BaseCommit
				case "parent_fork_tree":
					p.AnchorTree = strings.Repeat("1", 40)
				}
				drafts[0].Payload = encoded(t, p)
			} else if strings.HasPrefix(name, "observation_") {
				p, _ := ledger.DecodeScratchChanged(drafts[0].Payload)
				switch name {
				case "observation_actor":
					drafts[0].Actor = ledger.ActorAgent
				case "observation_context":
					drafts[0].ContextID = uuid.NewV7()
				case "observation_refs":
					drafts[0].BodyRefs = nil
				case "observation_id_reused":
					p.ObservationID = first.ID
				case "observation_id_reused_closed":
					p = parent
					p.ObservationID, p.PinID = first.ID, uuid.NewV7()
					p.PreviousCommit, p.ScratchCommit = parent.ScratchCommit, strings.Repeat("2", 40)
					drafts, _ = observationDrafts(p, parent.PinID)
				case "observation_closed":
					p.ObservationID, p.PinID = uuid.NewV7(), uuid.NewV7()
					p.PreviousCommit, p.PreviousTree = second.Commit, second.Tree
					p.ScratchCommit, p.ScratchTree = strings.Repeat("2", 40), first.Tree
					drafts, _ = observationDrafts(p, uuid.UUID{})
				case "observation_unknown":
					p.ScratchID = uuid.NewV7()
				case "observation_workspace":
					p.WorkspaceID = uuid.NewV7()
				case "observation_author":
					p.ContextID, drafts[0].ContextID = task.ContextID, task.ContextID
				case "observation_parent":
					p.ParentScratchID = uuid.UUID{}
				case "observation_previous_commit":
					p.PreviousCommit = task.BaseCommit
				case "observation_previous_tree":
					p.PreviousTree = strings.Repeat("1", 40)
				case "observation_changed_paths":
					p.ChangedPaths = nil
				case "observation_revision":
					p.InputsRevision++
				case "observation_digest":
					p.CaptureInputsSHA256 = ledger.Digest{1}
				case "observation_oid_length":
					p.ScratchCommit = strings.Repeat("1", 64)
				}
				drafts[0].Payload = encoded(t, p)
				switch name {
				case "observation_pin_missing":
					drafts = append(drafts[:1], drafts[2:]...)
				case "observation_pin_id":
					drafts[1].Payload = encoded(t, ledger.BodyPin{PinID: uuid.NewV7()})
				case "observation_pin_refs":
					drafts[1].BodyRefs = []ledger.Digest{parent.ManifestSHA256}
				case "observation_pin_not_immediate":
					drafts[1], drafts[2] = drafts[2], drafts[1]
				case "observation_unpin_missing":
					drafts = drafts[:2]
				case "observation_unpin_wrong":
					drafts[2].Payload = encoded(t, ledger.BodyUnpin{PinID: parent.PinID})
				case "observation_unpin_before":
					drafts = []ledger.Draft{drafts[2], drafts[0], drafts[1]}
				case "observation_unpin_repeated":
					drafts = append(drafts, drafts[2])
				case "observation_unpin_early":
					drafts = drafts[2:]
				}
			} else {
				switch name {
				case "closure_child_open":
					drafts[0].Payload = encoded(t, ledger.ScratchClosed{ScratchID: task.ID, ContextID: task.ContextID, Reason: "abandoned"})
					drafts[0].ContextID = task.ContextID
					drafts[1], _ = unpinDraft(parent.PinID, task.ContextID)
				case "closure_unpin_missing":
					drafts = drafts[:1]
				case "closure_unpin_before":
					drafts[0], drafts[1] = drafts[1], drafts[0]
				case "closure_unpin_repeated":
					drafts = append(drafts, drafts[1])
				}
			}
			assertApply(t, at, drafts)
			switch name {
			case "parent_unknown", "observation_refs", "observation_pin_refs", "observation_pin_not_immediate",
				"observation_unpin_before", "observation_unpin_repeated", "closure_child_open", "closure_unpin_before", "closure_unpin_repeated":
				// assertApply has undone this row before it can run in parallel.
				t.Parallel()
			default:
				return
			}
			colony := t.TempDir()
			if err := os.CopyFS(colony, os.DirFS(prefixes[at])); err != nil {
				t.Fatal(err)
			}
			store, _, err := ledgerstore.Open(colony, ledger.Defined())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			svc, _, err := openService(ctx, store, colony, serviceConfig(), serviceDeps{environ: func() []string { return f.env }})
			if err != nil {
				t.Fatalf("valid prefix: %v", err)
			}
			before := store.Durable()
			if err := svc.append(ctx, drafts); !errors.Is(err, ErrInvalid) || store.Durable() != before {
				t.Fatalf("producer accepted rule break: %v", err)
			}
			if err := svc.Close(); err != nil {
				t.Fatal(err)
			}
			ack, err := store.Append(ctx, drafts)
			if name == "observation_unpin_repeated" || name == "closure_unpin_repeated" {
				if !errors.Is(err, appender.ErrRefused) || store.Durable() != before {
					t.Fatalf("body store accepted a repeated unpin: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("store refused envelope-valid history: %v", err)
			}
			reopened, _, err := openService(ctx, store, colony, serviceConfig(), serviceDeps{environ: func() []string { return f.env }})
			if reopened != nil {
				reopened.Close()
			}
			var inconsistent *InconsistentError
			if !errors.As(err, &inconsistent) || inconsistent.Seq != ack.First {
				t.Fatalf("want inconsistent at %d: %v", ack.First, err)
			}
		})
	}
}

func TestFrozenWorkspaceGoldensReplay(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, row := range []struct {
		name                                        string
		workspaces, scratches, open, requests, live int
	}{{"05", 2, 2, 1, 0, 0}, {"06", 1, 2, 2, 4, 2}, {"07", 1, 5, 2, 1, 1}, {"08", 1, 1, 1, 7, 0}} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			logDir := filepath.Join(t.TempDir(), "log")
			source := filepath.Join("..", "..", "..", "contract", "ledger", "testdata", "f2", row.name, "log")
			if err := os.CopyFS(logDir, os.DirFS(source)); err != nil {
				t.Fatal(err)
			}
			first, _, _ := bytes.Cut(readTestFile(t, filepath.Join(logDir, "00000000000000000001.jsonl")), []byte{'\n'})
			_, envelope, ok := bytes.Cut(first, []byte{'\t'})
			var header struct {
				Colony uuid.UUID `json:"colony_id"`
			}
			if !ok {
				t.Fatal("golden lacks a framed envelope")
			}
			if err := json.Unmarshal(envelope, &header); err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.Check(logDir, header.Colony); err != nil {
				t.Fatal(err)
			}
			reader, err := ledger.OpenReader(logDir, ledger.ReadOptions{Colony: header.Colony, Through: ledger.ToEnd, Consume: workspaceConsumption(), AllTypes: true})
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			fixture := history
			fixture.colony = filepath.Join(t.TempDir(), "colony")
			f := openFixtureService(t, fixture)
			for {
				commit, err := reader.Next()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				drafts := make([]ledger.Draft, len(commit.Records))
				for i, record := range commit.Records {
					drafts[i] = record.Draft
				}
				if _, err := f.svc.apply(context.Background(), drafts, commit.First, false); err != nil {
					t.Fatalf("frozen F2-%s no longer replays: %v", row.name, err)
				}
			}
			live := 0
			for _, request := range f.svc.requests {
				if request.ending == "" {
					live++
				}
			}
			if len(f.svc.workspaces) != row.workspaces || len(f.svc.scratches) != row.scratches || len(f.svc.openScratches) != row.open || len(f.svc.requests) != row.requests || live != row.live {
				t.Fatalf("F2-%s projection: workspaces=%d scratches=%d open=%d requests=%d live=%d", row.name, len(f.svc.workspaces), len(f.svc.scratches), len(f.svc.openScratches), len(f.svc.requests), live)
			}
		})
	}
}

func assertF207History(t *testing.T, f *serviceFixture, prefix string, drafts []ledger.Draft, producerOK, replayOK bool, offsets ...uint64) {
	t.Helper()
	ctx := context.Background()
	colony := t.TempDir()
	if err := os.CopyFS(colony, os.DirFS(prefix)); err != nil {
		t.Fatal(err)
	}
	store, _, err := ledgerstore.Open(colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	deps := serviceDeps{environ: func() []string { return f.env }}
	svc, _, err := openService(ctx, store, colony, serviceConfig(), deps)
	if err != nil {
		t.Fatalf("valid prefix: %v", err)
	}
	before := store.Durable()
	err = svc.append(ctx, drafts)
	if producerOK {
		if err != nil {
			t.Fatalf("producer rejected contract-valid records: %v", err)
		}
	} else if !errors.Is(err, ErrInvalid) || store.Durable() != before {
		t.Fatalf("producer accepted rule break: %v", err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	first := before + 1
	if !producerOK {
		ack, err := store.Append(ctx, drafts)
		if err != nil {
			t.Fatalf("store refused envelope-valid history: %v", err)
		}
		first = ack.First
	}
	reopened, _, err := openService(ctx, store, colony, serviceConfig(), deps)
	if reopened != nil {
		defer reopened.Close()
	}
	if replayOK {
		if err != nil {
			t.Fatalf("replay rejected frozen-contract-valid records: %v", err)
		}
		for _, d := range drafts {
			if d.Type == ledger.TypeScratchMergeBack {
				p, _ := ledger.DecodeScratchMergeBack(d.Payload)
				if p.Outcome == ledger.MergeBackMerged && reopened.scratches[p.ParentScratchID].TipCommit != p.TargetCommit {
					t.Fatal("valid repeated-source application did not project target")
				}
			}
		}
		return
	}
	if len(offsets) != 0 {
		first += offsets[0]
	}
	var inconsistent *InconsistentError
	if !errors.As(err, &inconsistent) || inconsistent.Seq != first {
		t.Fatalf("want inconsistent at %d: %v", first, err)
	}
}

func TestOpenFailsClosedOnF2_07MergeBackRuleBreak(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	ctx := context.Background()
	task := mustScratch(t, f, uuid.NewV7())
	taskObservation := mustObserve(t, mustWriter(t, f, testWriteBinding(task)))
	source, sourceObservation := observedTemporary(t, f, task, map[string]string{"one.txt": "one\n"})
	secondSource, secondObservation := observedTemporary(t, f, task, map[string]string{"two.txt": "two\n"})
	conflictSource, _ := observedTemporary(t, f, task, map[string]string{"one.txt": "conflict\n"})
	thirdSource, _ := observedTemporary(t, f, task, map[string]string{"three.txt": "three\n"})
	first, err := f.svc.MergeBack(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	root := rootRequest(task)
	mustAdmit(t, f, root)
	cancelCtx, cancel := context.WithCancel(ctx)
	f.svc.deps.afterMergeBackPrepared = cancel
	second, err := f.svc.MergeBack(cancelCtx, secondSource.ID)
	cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	f.svc.deps.afterMergeBackPrepared = nil
	if _, err := f.svc.RetryMergeBack(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	third, err := f.svc.MergeBack(ctx, thirdSource.ID)
	if err != nil {
		t.Fatal(err)
	}
	conflict, err := f.svc.MergeBack(ctx, conflictSource.ID)
	if err != nil || conflict.Outcome != ledger.MergeBackConflict {
		t.Fatalf("history lacks conflict: %+v %v", conflict, err)
	}
	writer := mustWriter(t, f, testWriteBinding(task))
	writeTestFile(t, filepath.Join(task.Dir, "task.txt"), []byte("task\n"))
	latest := mustObserve(t, writer)
	successor := rootRequest(task)
	successor.Head = root.RequestID
	mustAdmit(t, f, successor)
	if _, err := f.svc.CloseScratch(ctx, ownerClosure(conflictSource)); err != nil {
		t.Fatal(err)
	}
	commits := serviceCommits(t, f.store)
	indices := make(map[string]int)
	var taskPin, secondPin uuid.UUID
	for i, commit := range commits {
		for _, r := range commit.Records {
			switch r.Type {
			case ledger.TypeScratchMergeBack:
				p, _ := ledger.DecodeScratchMergeBack(r.Payload)
				key := p.Outcome
				if p.MergeBackID == first.ID {
					key += "_first"
				} else if p.MergeBackID == third.ID {
					key += "_third"
				}
				indices[key] = i
			case ledger.TypePromotionAdmitted:
				p, _ := ledger.DecodePromotionAdmitted(r.Payload)
				if p.RequestID == successor.RequestID {
					indices["admission"] = i
				}
			case ledger.TypeScratchClosed:
				p, _ := ledger.DecodeScratchClosed(r.Payload)
				if p.ScratchID == source.ID {
					indices["closure"] = i
				}
			case ledger.TypeScratchChanged:
				p, _ := ledger.DecodeScratchChanged(r.Payload)
				if p.ObservationID == taskObservation.ID {
					taskPin = p.PinID
				}
				if p.ObservationID == secondObservation.ID {
					secondPin = p.PinID
				}
				if p.ObservationID == latest.ID {
					indices["observation"] = i
				}
			}
		}
	}
	rows := []string{
		"actor", "envelope_context", "source_unknown", "source_dedicated", "source_author", "parent_unknown", "parent_wrong", "workspace",
		"source_observation", "source_commit", "source_tree", "fork_commit", "fork_tree", "parent_commit", "parent_tree", "revision", "digest", "oid_length",
		"prepared_refs", "prepared_pin_missing", "prepared_pin_wrong", "prepared_pin_refs", "prepared_reused", "prepared_parent_pending", "pending_observation", "pending_closure", "pending_admission",
		"failure_without_prepared", "failure_plan", "failure_refs", "failure_unpin", "unknown_failure_readable",
		"applied_plan", "applied_unpin_missing", "applied_unpin_wrong", "applied_unpin_before", "applied_refs", "applied_changed", "applied_id_reused",
		"fast_forward_refs",
		"conflict_refs", "conflict_pin_missing", "conflict_pin_wrong", "conflict_unpin_early",
		"closure_without_applied", "closure_after_observation", "closure_same_commit",
		"admission_observation_missing", "admission_observation_stale", "admission_merge_missing", "admission_outside_window", "admission_foreign", "admission_reversed",
		"applied_source_fresh_attempt",
	}
	var cuts []int
	for _, at := range indices {
		cuts = append(cuts, at, at+1)
	}
	prefixes := snapshotReplayPrefixes(t, f, commits, cuts)
	// Prefix journals are used only by serial apply checks. The representative
	// Open rows own private stores and pause only after the journal is undone.
	fixture := f.gitFixture
	fixture.colony = t.TempDir()
	projection := openFixtureService(t, fixture).svc
	assertApply := func(t *testing.T, at int, drafts []ledger.Draft, producerOK, replayOK bool, failedOffset uint64) {
		t.Helper()
		var undos []func()
		defer func() {
			for i := len(undos) - 1; i >= 0; i-- {
				undos[i]()
			}
		}()
		for _, commit := range commits[:at] {
			var prefix []ledger.Draft
			for _, record := range commit.Records {
				prefix = append(prefix, record.Draft)
			}
			undo, err := projection.apply(ctx, prefix, commit.First, false)
			if err != nil {
				t.Fatalf("valid prefix: %v", err)
			}
			undos = append(undos, undo)
		}
		first := commits[at].First
		for _, producing := range []bool{true, false} {
			undo, err := projection.apply(ctx, drafts, first, producing)
			wantOK := producing && producerOK || !producing && replayOK
			if err == nil {
				undo()
				if !wantOK {
					t.Errorf("rule break accepted producing=%t", producing)
				}
				continue
			}
			if wantOK {
				t.Errorf("contract-valid records refused producing=%t: %v", producing, err)
				continue
			}
			var inconsistent *InconsistentError
			if !errors.As(err, &inconsistent) || inconsistent.Seq != first+failedOffset {
				t.Errorf("want inconsistent at %d producing=%t: %v", first+failedOffset, producing, err)
			}
		}
	}
	for _, name := range rows {
		t.Run(name, func(t *testing.T) {
			check := func(at int, drafts []ledger.Draft, valid bool, failedOffset uint64) {
				t.Helper()
				replayOK := valid || name == "unknown_failure_readable"
				assertApply(t, at, drafts, valid, replayOK, failedOffset)
				switch name {
				case "prepared_refs", "prepared_pin_missing", "pending_observation", "pending_closure", "pending_admission",
					"failure_plan", "failure_unpin", "unknown_failure_readable", "applied_refs", "applied_unpin_before",
					"fast_forward_refs", "conflict_refs", "closure_after_observation", "closure_same_commit", "applied_source_fresh_attempt":
					t.Parallel()
					assertF207History(t, f, prefixes[at], drafts, valid, replayOK, failedOffset)
				}
			}
			at := indices[ledger.MergeBackPrepared]
			switch {
			case strings.HasPrefix(name, "failure_") || name == "unknown_failure_readable":
				at = indices[ledger.MergeBackWriteFailed]
			case strings.HasPrefix(name, "applied_"):
				at = indices[ledger.MergeBackMerged]
			case name == "fast_forward_refs":
				at = indices[ledger.MergeBackFastForward+"_first"]
			case strings.HasPrefix(name, "conflict_"):
				at = indices[ledger.MergeBackConflict]
			case strings.HasPrefix(name, "closure_"):
				at = indices["closure"]
			case strings.HasPrefix(name, "admission_"):
				at = indices["admission"]
			}
			drafts := make([]ledger.Draft, len(commits[at].Records))
			for i, r := range commits[at].Records {
				drafts[i] = r.Draft
			}
			valid := false
			var failedOffset uint64
			switch {
			case strings.HasPrefix(name, "admission_"):
				p, _ := ledger.DecodePromotionAdmitted(drafts[0].Payload)
				switch name {
				case "admission_observation_missing":
					p.ScratchObservationID = uuid.UUID{}
				case "admission_observation_stale":
					p.ScratchObservationID = taskObservation.ID
				case "admission_merge_missing":
					p.MergeBackIDs = nil
				case "admission_outside_window":
					p.MergeBackIDs = []uuid.UUID{first.ID, second.ID, third.ID}
				case "admission_foreign":
					p.MergeBackIDs = []uuid.UUID{conflict.ID}
				case "admission_reversed":
					p.MergeBackIDs = []uuid.UUID{third.ID, second.ID}
				}
				drafts[0].Payload = encoded(t, p)
			case strings.HasPrefix(name, "closure_"):
				if name == "closure_without_applied" {
					at = indices[ledger.MergeBackPrepared+"_first"]
				} else if name == "closure_same_commit" {
					at = indices[ledger.MergeBackFastForward+"_first"]
					failedOffset = uint64(len(commits[at].Records))
					var prefix []ledger.Draft
					for _, record := range commits[at].Records {
						prefix = append(prefix, record.Draft)
					}
					drafts = append(prefix, drafts...)
				} else {
					failedOffset = 3
					p := ledger.ScratchChanged{ObservationID: uuid.NewV7(), WorkspaceID: f.w.ID, ScratchID: source.ID, ContextID: source.ContextID, ParentScratchID: task.ID,
						PreviousCommit: sourceObservation.Commit, PreviousTree: sourceObservation.Tree, ScratchCommit: strings.Repeat("c", 40), ScratchTree: sourceObservation.Tree,
						InputsRevision: 1, CaptureInputsSHA256: f.w.InputsSHA256, ScratchRules: "scratch-rules/1",
						ManifestSHA256: ledger.Digest{}, PinID: uuid.NewV7(), WrittenPaths: []string{}, ChangedPaths: []string{"one.txt"}}
					// Reuse the source's real manifest, but acknowledge a later observation.
					for _, commit := range commits {
						for _, record := range commit.Records {
							if record.Type == ledger.TypeScratchChanged {
								old, _ := ledger.DecodeScratchChanged(record.Payload)
								if old.ObservationID == sourceObservation.ID {
									p.ManifestSHA256 = old.ManifestSHA256
									prefix, err := observationDrafts(p, old.PinID)
									if err != nil {
										t.Fatal(err)
									}
									drafts[1], _ = unpinDraft(p.PinID, source.ContextID)
									drafts = append(prefix, drafts...)
								}
							}
						}
					}
				}
			default:
				p, _ := ledger.DecodeScratchMergeBack(drafts[0].Payload)
				switch name {
				case "actor":
					drafts[0].Actor = ledger.ActorAgent
				case "envelope_context":
					drafts[0].ContextID = uuid.NewV7()
				case "source_unknown":
					p.ScratchID = uuid.NewV7()
				case "source_dedicated":
					p.ScratchID = task.ID
				case "source_author":
					p.AuthoringContextID = uuid.NewV7()
				case "parent_unknown":
					p.ParentScratchID = uuid.NewV7()
				case "parent_wrong":
					p.ParentScratchID = conflictSource.ID
				case "workspace":
					p.WorkspaceID = uuid.NewV7()
				case "source_observation":
					p.SourceObservationID = sourceObservation.ID
				case "source_commit":
					p.SourceCommit = strings.Repeat("c", 40)
				case "source_tree":
					p.SourceTree = strings.Repeat("c", 40)
				case "fork_commit":
					p.ForkCommit = strings.Repeat("c", 40)
				case "fork_tree":
					p.ForkTree = strings.Repeat("c", 40)
				case "parent_commit":
					p.ParentCommit = strings.Repeat("c", 40)
				case "parent_tree":
					p.ParentTree = strings.Repeat("c", 40)
				case "revision":
					p.InputsRevision++
				case "digest":
					p.CaptureInputsSHA256 = ledger.Digest{1}
				case "oid_length":
					p.TargetCommit = strings.Repeat("c", 64)
				case "prepared_refs":
					drafts[0].BodyRefs = []ledger.Digest{p.ManifestSHA256}
				case "prepared_pin_missing", "conflict_pin_missing", "applied_unpin_missing":
					drafts = drafts[:1]
				case "prepared_pin_wrong", "conflict_pin_wrong":
					drafts[1].Payload = encoded(t, ledger.BodyPin{PinID: uuid.NewV7()})
				case "prepared_pin_refs":
					drafts[1].BodyRefs = []ledger.Digest{p.ManifestSHA256}
				case "prepared_reused":
					p.MergeBackID = first.ID
				case "prepared_parent_pending":
					at++
					p.MergeBackID, p.PinID = uuid.NewV7(), uuid.NewV7()
					drafts[1].Payload = encoded(t, ledger.BodyPin{PinID: p.PinID})
				case "pending_observation":
					at++
					for _, r := range commits[indices["observation"]].Records {
						drafts = append(drafts, r.Draft)
					}
					drafts = drafts[len(commits[indices[ledger.MergeBackPrepared]].Records):]
					observation, _ := ledger.DecodeScratchChanged(drafts[0].Payload)
					observation.PreviousCommit, observation.PreviousTree = first.TargetCommit, first.TargetTree
					drafts[0].Payload = encoded(t, observation)
					check(at, drafts, false, 0)
					return
				case "pending_closure":
					at++
					drafts = []ledger.Draft{{Type: ledger.TypeScratchClosed, TypeVersion: 1, Actor: ledger.ActorOwner,
						Payload: encoded(t, ledger.ScratchClosed{ScratchID: secondSource.ID, ContextID: secondSource.ContextID, Reason: "abandoned"})}}
					unpin, _ := unpinDraft(secondPin, secondSource.ContextID)
					drafts = append(drafts, unpin)
					check(at, drafts, false, 0)
					return
				case "pending_admission":
					at++
					drafts = nil
					for _, r := range commits[indices["admission"]].Records {
						drafts = append(drafts, r.Draft)
					}
					admission, _ := ledger.DecodePromotionAdmitted(drafts[0].Payload)
					admission.ScratchObservationID, admission.MergeBackIDs = taskObservation.ID, nil
					drafts[0].Payload = encoded(t, admission)
					check(at, drafts, false, 0)
					return
				case "failure_without_prepared":
					at = indices[ledger.MergeBackPrepared]
				case "failure_plan", "applied_plan":
					p.TargetTree = strings.Repeat("c", 40)
				case "failure_refs", "applied_refs", "fast_forward_refs":
					drafts[0].BodyRefs = []ledger.Digest{f.w.InputsSHA256}
				case "failure_unpin":
					failedOffset = 1
					pin := mergeBackRecord(t, f, second.ID, ledger.MergeBackPrepared).PinID
					unpin, _ := unpinDraft(pin, task.ContextID)
					drafts = append(drafts, unpin)
				case "unknown_failure_readable":
					p.Failure = "future_failure"
				case "applied_unpin_wrong":
					drafts[1].Payload = encoded(t, ledger.BodyUnpin{PinID: taskPin})
				case "applied_unpin_before":
					drafts[0], drafts[1] = drafts[1], drafts[0]
					check(at, drafts, false, 0)
					return
				case "applied_changed":
					p.ChangedPaths = []string{}
				case "applied_id_reused":
					at++
					drafts = drafts[:1]
				case "conflict_refs":
					drafts[0].BodyRefs = nil
				case "conflict_unpin_early":
					at++
					d, _ := unpinDraft(p.ReportPinID, p.ContextID)
					drafts = []ledger.Draft{d}
					check(at, drafts, false, 0)
					return
				case "applied_source_fresh_attempt":
					at = indices[ledger.MergeBackFastForward+"_first"] + 1
					p = mergeBackRecord(t, f, first.ID, ledger.MergeBackPrepared)
					p.MergeBackID, p.PinID = uuid.NewV7(), uuid.NewV7()
					p.ParentCommit, p.ParentTree = p.TargetCommit, p.TargetTree
					p.TargetCommit = strings.Repeat("c", 40)
					refs := commits[indices[ledger.MergeBackPrepared+"_first"]].Records[0].BodyRefs
					drafts, err = mergeBackDrafts(p, refs, uuid.UUID{})
					if err != nil {
						t.Fatal(err)
					}
					applied := p
					applied.Outcome, applied.ManifestSHA256, applied.PinID = ledger.MergeBackMerged, ledger.Digest{}, uuid.UUID{}
					applied.ChangedPaths = []string{"one.txt"}
					result, err := mergeBackDrafts(applied, nil, p.PinID)
					if err != nil {
						t.Fatal(err)
					}
					drafts = append(drafts, result...)
					valid = true
				}
				if name != "applied_source_fresh_attempt" {
					drafts[0].Payload = encoded(t, p)
				}
			}
			check(at, drafts, valid, failedOffset)
		})
	}
}

// Snapshot only closed stores. Rows copy a stable prefix, then exercise their
// mutation through the real appender and Open; no repeated prefix fsyncs.
func snapshotReplayPrefixes(t *testing.T, f *serviceFixture, commits []ledger.Commit, cuts []int) map[int]string {
	t.Helper()
	cuts = slices.Clone(cuts)
	slices.Sort(cuts)
	cuts = slices.Compact(cuts)
	if len(cuts) == 0 || cuts[0] < 0 || cuts[len(cuts)-1] > len(commits) {
		t.Fatal("invalid replay prefix boundaries")
	}
	ctx := context.Background()
	base, snapshots := t.TempDir(), t.TempDir()
	store, err := ledgerstore.Create(base, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if store != nil {
			if err := store.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	// Publish the shared history's witnesses once, including bodies a row may
	// reference from a later record. A published body alone conveys no pin.
	published := make(map[ledger.Digest]bool)
	for _, commit := range commits {
		for _, record := range commit.Records {
			for _, digest := range record.BodyRefs {
				if published[digest] {
					continue
				}
				body, err := f.store.ReadBody(ctx, digest)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := store.PublishBody(ctx, bytes.NewReader(body)); err != nil {
					t.Fatal(err)
				}
				published[digest] = true
			}
		}
	}
	result := make(map[int]string)
	next := 0
	for at := 0; at <= cuts[len(cuts)-1]; at++ {
		if at == cuts[next] {
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store = nil
			path := filepath.Join(snapshots, fmt.Sprint(at))
			if err := os.CopyFS(path, os.DirFS(base)); err != nil {
				t.Fatal(err)
			}
			result[at] = path
			next++
			if next == len(cuts) {
				break
			}
			store, _, err = ledgerstore.Open(base, ledger.Defined())
			if err != nil {
				t.Fatal(err)
			}
		}
		drafts := make([]ledger.Draft, len(commits[at].Records))
		for i, record := range commits[at].Records {
			drafts[i] = record.Draft
		}
		if _, err := store.Append(ctx, drafts); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

// This real history has the frozen F2-08 golden's twenty-two commit shapes and
// sequence numbers, but binds actual Git objects and published manifest bodies.
func finalizationReplayHistory(t *testing.T) (*serviceFixture, []ledger.Commit) {
	t.Helper()
	f := newServiceFixture(t)
	scratch := mustScratch(t, f, uuid.NewV7())
	end := func(r Request, outcome string, actor ledger.Actor) {
		t.Helper()
		f.svc.mu.Lock()
		drafts, ended, err := f.svc.endingDrafts(f.svc.requests[r.RequestID], outcome, actor, r.ContextID, uuid.UUID{})
		f.svc.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.finishFinalization(context.Background(), f.w.ID, drafts, ended); err != nil {
			t.Fatal(err)
		}
	}
	r1 := rootRequest(scratch)
	writeTestFile(t, filepath.Join(scratch.Dir, "one.txt"), []byte("one\n"))
	mustAdmit(t, f, r1)
	mustIntegrate(t, f)
	mustFinalize(t, f, r1)
	r2 := rootRequest(scratch)
	r2.Head = r1.RequestID
	mustAdmit(t, f, r2)
	mustIntegrate(t, f)
	mustFinalize(t, f, r2)
	r3, r4 := rootRequest(scratch), rootRequest(scratch)
	r3.Head, r4.Head = r2.RequestID, r3.RequestID
	mustAdmit(t, f, r3)
	mustAdmit(t, f, r4)
	end(r3, ledger.FailedCancelled, ledger.ActorOwner)
	r5 := rootRequest(scratch)
	r5.RestartFrom = r4.RequestID
	writeTestFile(t, filepath.Join(scratch.Dir, "five.txt"), []byte("five\n"))
	mustAdmit(t, f, r5)
	mustIntegrate(t, f)
	g := currentGeneration(t, f, r5)
	witness := f.svc.witnesses[f.w.ID]
	intent := ledger.FinalizationIntended{IntentID: uuid.NewV7(), RequestID: r5.RequestID, GenerationID: g.GenerationID, PredecessorWitnessSeq: int64(witness.sequence), PredecessorCommit: g.PredecessorCommit, PredecessorTree: g.PredecessorTree, TargetCommit: g.DerivedCommit, TargetTree: g.DerivedTree, FinalizationRules: "finalization-rules/1"}
	if err := f.svc.append(context.Background(), []ledger.Draft{{Type: ledger.TypeFinalizationIntended, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: r5.ContextID, Payload: encoded(t, intent)}}); err != nil {
		t.Fatal(err)
	}
	end(r5, ledger.FailedInterrupted, ledger.ActorCore)
	r6 := rootRequest(scratch)
	r6.RestartFrom = r5.RequestID
	mustAdmit(t, f, r6)
	mustIntegrate(t, f)
	mustFinalize(t, f, r6)
	r7 := rootRequest(scratch)
	r7.Head = r6.RequestID
	mustAdmit(t, f, r7)
	end(r7, ledger.FailedInterrupted, ledger.ActorCore)
	commits := serviceCommits(t, f.store)
	if len(commits) != 22 || f.store.Durable() != 39 {
		t.Fatalf("F2-08 shape drift: commits=%d durable=%d", len(commits), f.store.Durable())
	}
	return f, commits
}

func TestOpenFailsClosedOnF2_08RuleBreak(t *testing.T) {
	t.Parallel()
	f, commits := finalizationReplayHistory(t)
	type row struct {
		name   string
		seq    uint64
		cut    uint64
		change func([]ledger.Draft) []ledger.Draft
		open   bool
	}
	var rows []row
	payload := func(key string, value any) func([]ledger.Draft) []ledger.Draft {
		return func(drafts []ledger.Draft) []ledger.Draft {
			var p map[string]any
			if err := json.Unmarshal(drafts[0].Payload, &p); err != nil {
				t.Fatal(err)
			}
			if value == nil {
				delete(p, key)
			} else {
				p[key] = value
			}
			b, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			drafts[0].Payload = b
			return drafts
		}
	}
	add := func(name string, seq uint64, change func([]ledger.Draft) []ledger.Draft) {
		rows = append(rows, row{name: name, seq: seq, change: change})
	}
	for _, seq := range []uint64{8, 9, 14, 20, 28, 38} {
		add(fmt.Sprintf("%d_context", seq), seq, func(d []ledger.Draft) []ledger.Draft { d[0].ContextID = uuid.NewV7(); return d })
		add(fmt.Sprintf("%d_body_refs", seq), seq, func(d []ledger.Draft) []ledger.Draft { d[0].BodyRefs = []ledger.Digest{f.w.InputsSHA256}; return d })
		add(fmt.Sprintf("%d_actor", seq), seq, func(d []ledger.Draft) []ledger.Draft {
			d[0].Actor = ledger.ActorOwner
			if seq == 20 {
				d[0].Actor = ledger.ActorCore
			}
			return d
		})
		add(fmt.Sprintf("%d_unadmitted", seq), seq, payload("request_id", uuid.NewV7()))
	}
	for _, seq := range []uint64{8, 9, 14} {
		add(fmt.Sprintf("%d_generation", seq), seq, payload("generation_id", uuid.NewV7()))
		add(fmt.Sprintf("%d_predecessor_commit", seq), seq, payload("predecessor_commit", strings.Repeat("a", 40)))
		add(fmt.Sprintf("%d_predecessor_tree", seq), seq, payload("predecessor_tree", strings.Repeat("b", 40)))
	}
	for _, seq := range []uint64{13, 14, 26, 27, 32, 33, 34} {
		add(fmt.Sprintf("%d_missing_witness", seq), seq, payload("predecessor_witness_seq", nil))
		add(fmt.Sprintf("%d_stale_witness", seq), seq, payload("predecessor_witness_seq", 8))
	}
	add("first_intent_unearned_witness", 8, payload("predecessor_witness_seq", 7))
	add("first_acceptance_unearned_witness", 9, payload("predecessor_witness_seq", 7))
	add("generation_witness_commit", 26, payload("predecessor_commit", strings.Repeat("a", 40)))
	add("generation_witness_tree", 26, payload("predecessor_tree", strings.Repeat("b", 40)))
	add("intent_target_commit", 8, payload("target_commit", strings.Repeat("c", 40)))
	add("intent_target_tree", 8, payload("target_tree", strings.Repeat("d", 40)))
	add("acceptance_target_commit", 9, payload("accepted_commit", strings.Repeat("c", 40)))
	add("acceptance_target_tree", 9, payload("accepted_tree", strings.Repeat("d", 40)))
	add("acceptance_intent", 9, payload("intent_id", uuid.NewV7()))
	add("reused_intent_identity", 27, func(d []ledger.Draft) []ledger.Draft {
		first, _ := ledger.DecodeFinalizationIntended(commits[4].Records[0].Payload)
		return payload("intent_id", first.IntentID)(d)
	})
	for _, seq := range []uint64{9, 14, 20, 28, 38} {
		add(fmt.Sprintf("%d_missing_unpin", seq), seq, func(d []ledger.Draft) []ledger.Draft { return d[:len(d)-1] })
		add(fmt.Sprintf("%d_early_unpin", seq), seq, func(d []ledger.Draft) []ledger.Draft { return append([]ledger.Draft{d[len(d)-1]}, d[:len(d)-1]...) })
		add(fmt.Sprintf("%d_duplicate_unpin", seq), seq, func(d []ledger.Draft) []ledger.Draft { return append(d, d[len(d)-1]) })
	}
	for _, seq := range []uint64{20, 28, 38} {
		add(fmt.Sprintf("%d_generation_key", seq), seq, payload("generation_id", uuid.NewV7()))
		add(fmt.Sprintf("%d_dependency_key", seq), seq, payload("failed_request_id", uuid.NewV7()))
	}
	add("cancelled_live_suffix", 20, func(d []ledger.Draft) []ledger.Draft { return []ledger.Draft{d[0], d[2]} })
	add("cancelled_suffix_not_dependency", 20, func(d []ledger.Draft) []ledger.Draft {
		p, _ := ledger.DecodePromotionFailed(d[1].Payload)
		p.Outcome, p.FailedRequestID = ledger.FailedInterrupted, uuid.UUID{}
		d[1].Payload = encoded(t, p)
		return d
	})
	add("new_root_anchor_not_representation", 24, payload("baseline_tree", f.svc.scratches[f.svc.requests[committedRequest(t, commits, 24)].admission.ScratchID].AnchorTree))
	add("new_root_delta_not_representation", 24, payload("delta_base_tree", f.svc.scratches[f.svc.requests[committedRequest(t, commits, 24)].admission.ScratchID].AnchorTree))
	// Cross-record obligations: use the ordinary record shape at another cut.
	rows = append(rows,
		row{name: "second_intent", seq: 8, cut: 9},
		row{name: "second_acceptance", seq: 9, cut: 11},
		row{name: "acceptance_after_interrupted", seq: 34, cut: 30, change: func(d []ledger.Draft) []ledger.Draft {
			i, _ := ledger.DecodeFinalizationIntended(commits[14].Records[0].Payload)
			p, _ := ledger.DecodePromotionAccepted(d[0].Payload)
			p.IntentID, p.RequestID, p.GenerationID = i.IntentID, i.RequestID, i.GenerationID
			p.AcceptedCommit, p.AcceptedTree = i.TargetCommit, i.TargetTree
			d[0].Payload = encoded(t, p)
			return d
		}},
		row{name: "cancelled_intent", seq: 28, change: payload("outcome", ledger.FailedCancelled)},
		row{name: "unknown_failure_intent", seq: 28, change: payload("outcome", "accepted")},
		row{name: "generation_supersedes_intent", seq: 26, cut: 28, change: func(d []ledger.Draft) []ledger.Draft {
			p, _ := ledger.DecodeGenerationDerived(d[0].Payload)
			p.Supersedes, p.GenerationID, p.PredecessorCommit = p.GenerationID, uuid.NewV7(), strings.Repeat("a", 40)
			d[0].Payload = encoded(t, p)
			return d
		}},
		row{name: "no_change_has_intent", seq: 14, cut: 28, change: func(d []ledger.Draft) []ledger.Draft {
			i, _ := ledger.DecodeFinalizationIntended(commits[14].Records[0].Payload)
			return payload("request_id", i.RequestID)(payload("generation_id", i.GenerationID)(d))
		}},
		row{name: "no_change_unequal_tree", seq: 14, cut: 8, change: func(d []ledger.Draft) []ledger.Draft {
			g, _ := ledger.DecodeGenerationDerived(commits[3].Records[0].Payload)
			return payload("request_id", g.RequestID)(payload("generation_id", g.GenerationID)(payload("predecessor_commit", g.PredecessorCommit)(payload("predecessor_tree", g.PredecessorTree)(payload("predecessor_witness_seq", nil)(d)))))
		}},
	)
	indices := map[uint64]int{}
	for i, c := range commits {
		indices[c.First] = i
	}
	var cuts []int
	for i := range rows {
		r := &rows[i]
		if r.cut == 0 {
			r.cut = r.seq
		}
		switch r.name {
		case "26_missing_witness", "27_missing_witness", "34_missing_witness", "14_missing_witness", "20_actor", "28_generation_key":
			r.open = true
			cuts = append(cuts, indices[r.cut])
		}
	}
	prefixes := snapshotReplayPrefixes(t, f, commits, cuts)
	// C-type cut: all rules exercise the exact append/replay apply path; one
	// genuine Open per new record type (and the conditional old types) proves
	// reader consumption and fail-closed routing without repeated Git sweeps.
	fixture := f.gitFixture
	fixture.colony = t.TempDir()
	projection := openFixtureService(t, fixture).svc
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			var undos []func()
			defer func() {
				for i := len(undos) - 1; i >= 0; i-- {
					undos[i]()
				}
			}()
			cut := indices[r.cut]
			for _, c := range commits[:cut] {
				var drafts []ledger.Draft
				for _, record := range c.Records {
					drafts = append(drafts, record.Draft)
				}
				undo, err := projection.apply(context.Background(), drafts, c.First, false)
				if err != nil {
					t.Fatalf("valid prefix: %v", err)
				}
				undos = append(undos, undo)
			}
			var drafts []ledger.Draft
			for _, record := range commits[indices[r.seq]].Records {
				drafts = append(drafts, record.Draft)
			}
			if r.change != nil {
				drafts = r.change(drafts)
			}
			for _, producing := range []bool{true, false} {
				undo, err := projection.apply(context.Background(), drafts, r.cut, producing)
				if err == nil {
					undo()
					t.Fatalf("rule break accepted producing=%t", producing)
				}
				var inconsistent *InconsistentError
				if !errors.As(err, &inconsistent) {
					t.Fatalf("wrong replay refusal: %v", err)
				}
			}
			if r.open {
				assertF207History(t, f, prefixes[cut], drafts, false, false)
			}
		})
	}
}

func committedRequest(t *testing.T, commits []ledger.Commit, seq uint64) uuid.UUID {
	t.Helper()
	for _, c := range commits {
		if c.First == seq {
			p, err := ledger.DecodePromotionAdmitted(c.Records[0].Payload)
			if err != nil {
				t.Fatal(err)
			}
			return p.RequestID
		}
	}
	t.Fatal("admission not found")
	return uuid.UUID{}
}
