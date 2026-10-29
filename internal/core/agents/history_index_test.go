package agents

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// Faults affect acceleration only; every assertion below observes the service,
// its durable frontier or its exposure rather than the representation of a hint.
func damageHistoryProofs(s *Service, mode string) {
	if mode == "evicted" || mode == "disabled" {
		damageHistoryCache(s, mode)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch mode {
	case "missing":
		s.p.identities = identityMaxima{}
	case "damaged":
		s.p.identities.task = uuid.UUID{}
	case "stale":
		s.p.identities.through--
	}
}

func damageContextAssociations(s *Service, mode string) {
	if mode == "evicted" || mode == "disabled" {
		damageHistoryCache(s, mode)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, state := range s.p.scopes {
		switch mode {
		case "missing":
			state.association = contextAssociation{}
		case "damaged":
			state.association.task = uuid.UUID{}
		case "stale":
			state.association.accepted = s.p.through + 1
		}
	}
}

func TestHistoryIndexContextBinding(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"intact", "missing", "damaged", "stale", "evicted", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := newServiceFixture(t, serviceDeps{})
			owner := registerPrivate(t, f.service, 100)
			old := scopedWorkContext(t, f.service, owner, 110, 111)
			next := scopedWorkContext(t, f.service, owner, 120, 121)
			unused := scopedWorkContext(t, f.service, owner, 130, 131)
			scopedEnrol(t, f.service, owner, scopedCapacity(), 300, owner.AgentID)
			grant := privateScopedGrant(old, 500, scopedCapacity(), "private.write", "turn.start")
			issueScopedRoot(t, f.service, owner, 501, grant)
			freshGrant := privateScopedGrant(unused, 510, scopedCapacity(), "turn.start")
			issueScopedRoot(t, f.service, owner, 511, freshGrant)
			creation := scopedNewTask(401, 400, uuid.UUID{}, owner.AgentID)
			creation.Assignment = &ledger.AssignmentInput{AssignmentID: privateID(600), Assignee: ledger.Binding(old)}
			if _, err := f.service.CreateTask(ctx, owner, creation); err != nil {
				t.Fatal(err)
			}
			// Both revision families replace scope state without unbinding it.
			if _, err := f.service.WritePrivate(ctx, memoryWrite(old, 402, 1, "retained context")); err != nil {
				t.Fatal(err)
			}
			if _, err := scopedBound(t, f.service, old).WritePrivate(ctx, ScopedPrivateWrite{CommandID: privateID(405), GrantID: grant.GrantID, Scope: "context", ExpectedVersion: 2, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("scoped context")}}}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.ReassignTask(ctx, owner, TaskAssignment{CommandID: privateID(403), TaskID: creation.TaskID, ExpectedVersion: 1, ExpectedAssignmentID: privateID(600), ExpectedGeneration: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(601), Assignee: ledger.Binding(next)}}); err != nil {
				t.Fatal(err)
			}
			scopedCreate(t, f.service, owner, 410, 411, owner.AgentID)
			for phase := range 3 {
				if phase == 1 {
					if _, err := f.service.TransitionTask(ctx, owner, TaskMutation{CommandID: privateID(404), TaskID: creation.TaskID, ExpectedVersion: 2, ExpectedAssignmentID: privateID(601), ExpectedGeneration: 2, Action: "cancel"}); err != nil {
						t.Fatal(err)
					}
				}
				if phase == 2 {
					reopenScoped(t, f, DefaultLimits())
				}
				damageContextAssociations(f.service, mode)
				agent := scopedBound(t, f.service, old)
				assertReviewRefusal(t, f, func() error {
					_, err := agent.StartTurn(ctx, TurnAdmission{CommandID: privateID(700), TurnID: privateID(701), GrantID: grant.GrantID})
					return err
				}, ErrDenied)
				assertReviewRefusal(t, f, func() error {
					_, err := f.service.AssignTask(ctx, owner, TaskAssignment{CommandID: privateID(702), TaskID: privateID(410), ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(703), Assignee: ledger.Binding(old)}})
					return err
				}, ErrInvalid)
				view, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: creation.TaskID})
				state, version := "waiting", int64(2)
				if phase != 0 {
					state, version = "cancelled", 3
				}
				if err != nil || view.Task.State != state || view.Task.Version != version || view.Task.Assignment == nil || view.Task.Assignment.Assignee != ledger.Binding(next) || view.Task.Assignment.Generation != 2 {
					t.Fatalf("historical binding changed current task: %+v %v", view, err)
				}
			}
			// An actually never-assigned context still admits unbound work after
			// the same faults, and may subsequently receive an assignment.
			fresh := scopedBound(t, f.service, unused)
			start := TurnAdmission{CommandID: privateID(710), TurnID: privateID(711), GrantID: freshGrant.GrantID}
			if _, err := fresh.StartTurn(ctx, start); err != nil {
				t.Fatalf("never-assigned context refused: %v", err)
			}
			if _, err := f.service.EndTurn(ctx, TurnEnding{CommandID: privateID(712), TurnID: start.TurnID, ExpectedVersion: 1, Outcome: "released"}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.AssignTask(ctx, owner, TaskAssignment{CommandID: privateID(713), TaskID: privateID(410), ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(550), Assignee: ledger.Binding(unused)}}); err != nil {
				t.Fatalf("fresh out-of-order assignment refused: %v", err)
			}
		})
	}
}

func TestHistoryIndexIdentityReuse(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"intact", "missing", "damaged", "stale", "evicted", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := newServiceFixture(t, serviceDeps{})
			owner := registerPrivate(t, f.service, 100)
			scopedEnrol(t, f.service, owner, scopedCapacity(), 300, owner.AgentID)
			turnGrant := privateScopedGrant(owner, 800, scopedCapacity(), "turn.start")
			issueScopedRoot(t, f.service, owner, 801, turnGrant)
			// The same bytes are valid in all four independent namespaces. The
			// second generation is bytewise lower, not a UUID clock restriction.
			for i, id := range []int{900, 400} {
				if i == 1 {
					damageHistoryProofs(f.service, mode)
				}
				work := scopedWorkContext(t, f.service, owner, 110+i*10, 111+i*10)
				task := scopedNewTask(1000+i*20, id, uuid.UUID{}, owner.AgentID)
				task.Assignment = &ledger.AssignmentInput{AssignmentID: privateID(id), Assignee: ledger.Binding(work)}
				if _, err := f.service.CreateTask(ctx, owner, task); err != nil {
					t.Fatalf("fresh task/assignment %d: %v", id, err)
				}
				if _, err := f.service.TransitionTask(ctx, owner, TaskMutation{CommandID: privateID(1001 + i*20), TaskID: task.TaskID, ExpectedVersion: 1, ExpectedAssignmentID: privateID(id), ExpectedGeneration: 1, Action: "cancel"}); err != nil {
					t.Fatal(err)
				}
				grant := privateScopedGrant(owner, id, scopedCapacity(), "context.inspect")
				issued := issueScopedRoot(t, f.service, owner, 1002+i*20, grant)
				if _, err := f.service.RevokeGrant(ctx, owner, GrantRevocation{CommandID: privateID(1003 + i*20), RevokeGrantID: grant.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(issued.Version)}); err != nil {
					t.Fatal(err)
				}
				agent := scopedBound(t, f.service, owner)
				if _, err := agent.StartTurn(ctx, TurnAdmission{CommandID: privateID(1004 + i*20), TurnID: privateID(id), GrantID: turnGrant.GrantID}); err != nil {
					t.Fatalf("fresh work %d: %v", id, err)
				}
				if _, err := f.service.EndTurn(ctx, TurnEnding{CommandID: privateID(1005 + i*20), TurnID: privateID(id), ExpectedVersion: 1, Outcome: "released"}); err != nil {
					t.Fatal(err)
				}
			}
			unused := scopedWorkContext(t, f.service, owner, 140, 141)
			scopedCreate(t, f.service, owner, 450, 451, owner.AgentID)
			for phase := range 2 {
				if phase == 1 {
					reopenScoped(t, f, DefaultLimits())
					damageHistoryProofs(f.service, mode)
				}
				// A suffix after loss of proof must not become a complete-history
				// maximum: the higher, pruned identities still exist.
				assertReviewRefusal(t, f, func() error {
					_, err := f.service.CreateTask(ctx, owner, scopedNewTask(1100, 900, uuid.UUID{}, owner.AgentID))
					return err
				}, ErrRevision)
				assertReviewRefusal(t, f, func() error {
					_, err := f.service.IssueGrant(ctx, owner, GrantIssue{CommandID: privateID(1101), ExpectedAuthorityVersion: 5, Spec: privateScopedGrant(owner, 900, scopedCapacity(), "context.inspect")})
					return err
				}, ErrInvalid)
				agent := scopedBound(t, f.service, owner)
				assertReviewRefusal(t, f, func() error {
					_, err := agent.StartTurn(ctx, TurnAdmission{CommandID: privateID(1102), TurnID: privateID(900), GrantID: turnGrant.GrantID})
					return err
				}, ErrInvalid)
				assertReviewRefusal(t, f, func() error {
					_, err := f.service.AssignTask(ctx, owner, TaskAssignment{CommandID: privateID(1103), TaskID: privateID(450), ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(900), Assignee: ledger.Binding(unused)}})
					return err
				}, ErrAssignment)
				view, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: privateID(450)})
				if err != nil || view.Task.Version != 1 || view.Task.Assignment != nil {
					t.Fatalf("reused assignment changed task: %+v %v", view, err)
				}
				work, err := f.service.TurnStatus(ctx, WorkLookup{WorkID: privateID(900)})
				if err != nil || !work.Ended || work.Held != (Exposure{}) {
					t.Fatalf("reused work revived exposure: %+v %v", work, err)
				}
			}
		})
	}
}

func TestHistoryIndexFamilyTransitions(t *testing.T) {
	t.Parallel()
	for _, verdict := range []string{"approve", "fail"} {
		t.Run(verdict, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, q := liveReviewFixture(t)
			q.Binding.Reviewer = reviewOwnerPrincipal()
			old := Binding(q.Binding.Assignee)
			grant := privateScopedGrant(old, 1500, scopedCapacity(), "turn.start")
			q.Binding.AuthorizationRevision = issueScopedRoot(t, f.service, reviewOwner(), 1501, grant).Version
			var receipt ReviewReceipt
			var retry func() (ReviewReceipt, error)
			state := "completed"
			if verdict == "approve" {
				_, agent, result := requestLiveReview(t, f, q)
				retry = func() (ReviewReceipt, error) { return resolveLiveReview(f, agent, result) }
			} else {
				state = "failed"
				request := FailureRequest{CommandID: privateID(1510), RequestID: privateID(1511), Binding: q.Binding, Cause: "semantic_failure", FailingEvidence: q.Binding.Subject}
				r, err := f.service.RequestFailure(ctx, reviewOwner(), request)
				if err != nil {
					t.Fatal(err)
				}
				result := FailureResult{CommandID: privateID(1512), RequestID: request.RequestID, RequestSequence: int64(r.EffectSequence), Binding: q.Binding, Cause: request.Cause, FailingEvidence: request.FailingEvidence}
				retry = func() (ReviewReceipt, error) { return f.service.ResolveFailure(ctx, reviewOwner(), result) }
			}
			var err error
			receipt, err = retry()
			if err != nil {
				t.Fatal(err)
			}
			scopedCreate(t, f.service, reviewOwner(), 1520, 1521, old.AgentID)
			for phase := range 2 {
				if phase == 1 {
					reopenScoped(t, f, DefaultLimits())
				}
				for _, mode := range []string{"intact", "damaged", "missing", "evicted", "disabled"} {
					damageContextAssociations(f.service, mode)
					view, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: q.Binding.TaskID})
					if err != nil || view.Task.State != state || view.Task.Version != q.Binding.TaskVersion+1 || view.Task.Assignment == nil || view.Task.Assignment.Assignee != q.Binding.Assignee {
						t.Fatalf("review terminal state lost: %+v %v", view, err)
					}
					agent := scopedBound(t, f.service, old)
					assertReviewRefusal(t, f, func() error {
						_, err := agent.StartTurn(ctx, TurnAdmission{CommandID: privateID(1522), TurnID: privateID(1523), GrantID: grant.GrantID})
						return err
					}, ErrDenied)
					assertReviewRefusal(t, f, func() error {
						_, err := f.service.AssignTask(ctx, reviewOwner(), TaskAssignment{CommandID: privateID(1524), TaskID: privateID(1520), ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(1525), Assignee: ledger.Binding(old)}})
						return err
					}, ErrInvalid)
					before := f.store.Durable()
					again, err := retry()
					if err != nil || !reflect.DeepEqual(again, receipt) || f.store.Durable() != before {
						t.Fatalf("terminal receipt changed or appended: %+v %v", again, err)
					}
				}
			}
		})
	}
}

func TestHistoryIndexHistoricalPrefix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	owner := registerPrivate(t, f.service, 100)
	old := scopedWorkContext(t, f.service, owner, 110, 111)
	next := scopedWorkContext(t, f.service, owner, 120, 121)
	scopedEnrol(t, f.service, owner, scopedCapacity(), 300, owner.AgentID)
	unassigned := f.store.Durable()
	creation := scopedNewTask(401, 400, uuid.UUID{}, owner.AgentID)
	creation.Assignment = &ledger.AssignmentInput{AssignmentID: privateID(600), Assignee: ledger.Binding(old)}
	if _, err := f.service.CreateTask(ctx, owner, creation); err != nil {
		t.Fatal(err)
	}
	assigned := f.store.Durable()
	if _, err := f.service.ReassignTask(ctx, owner, TaskAssignment{CommandID: privateID(402), TaskID: creation.TaskID, ExpectedVersion: 1, ExpectedAssignmentID: privateID(600), ExpectedGeneration: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(601), Assignee: ledger.Binding(next)}}); err != nil {
		t.Fatal(err)
	}
	grant := privateScopedGrant(owner, 500, scopedCapacity(), "turn.start")
	issueScopedRoot(t, f.service, owner, 501, grant)
	if _, err := scopedBound(t, f.service, owner).StartTurn(ctx, TurnAdmission{CommandID: privateID(700), TurnID: privateID(701), GrantID: grant.GrantID}); err != nil {
		t.Fatal(err)
	}
	f.service.mu.Lock()
	defer f.service.mu.Unlock()
	origin := ledger.Origin{AgentID: owner.AgentID, SessionID: privateID(101), ContextID: old.ContextID}
	if _, found, err := f.service.contextTask(ctx, origin, unassigned); err != nil || found {
		t.Fatalf("later association borrowed by earlier prefix: found=%v %v", found, err)
	}
	task, found, err := f.service.contextTask(ctx, origin, assigned)
	if err != nil || !found || task.record.Version != 1 || task.record.Assignment == nil || task.record.Assignment.Assignee != ledger.Binding(old) {
		t.Fatalf("later assignment borrowed by earlier task: %+v %v", task.record, err)
	}
	if _, found, err := f.service.loadTask(ctx, creation.TaskID, unassigned); err != nil || found {
		t.Fatalf("future task existed before creation: found=%v %v", found, err)
	}
	if _, found, err := f.service.loadGrant(ctx, grant.GrantID, assigned); err != nil || found {
		t.Fatalf("future grant existed before issue: found=%v %v", found, err)
	}
	if _, found, err := f.service.loadWork(ctx, privateID(701), assigned); err != nil || found {
		t.Fatalf("future admission existed before work: found=%v %v", found, err)
	}
	idUnused, contextUnused, err := f.service.assignmentUnused(ctx, *creation.Assignment, unassigned)
	if err != nil || !idUnused || !contextUnused {
		t.Fatalf("future assignment consumed earlier identity: id=%v context=%v %v", idUnused, contextUnused, err)
	}
}

func damageHistoryCache(s *Service, mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.history
	if mode == "evicted" {
		for i := range historyEntryLimit + 1 {
			h.put(historyEntry{key: historyKey{kind: historyCommand, id: privateID(10000 + i)}, from: s.p.through})
		}
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	switch mode {
	case "empty", "discarded":
		h.clearLocked()
	case "disabled":
		h.clearLocked()
		h.disabled = true
	case "coverage":
		h.checksum ^= 1
	default:
		for _, node := range h.entries {
			e := node.Value.(*historyEntry)
			oldBytes := len(e.data)
			switch mode {
			case "damaged":
				e.checksum ^= 1
			case "key":
				e.key.id = privateID(19999)
				e.checksum = e.sum()
			case "range":
				e.from = s.p.through + 1
				e.checksum = e.sum()
			case "decoder":
				e.found, e.data = true, []byte("{")
				e.checksum = e.sum()
			case "provenance":
				e.task = uuid.UUID{}
				e.first, e.last = s.p.through+1, s.p.through+2
				e.checksum = e.sum()
			}
			h.bytes += len(e.data) - oldBytes
		}
	}
}

func TestHistoryIndexPrefixAndDiscard(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"empty", "warm", "evicted", "discarded", "disabled", "damaged", "key", "range", "decoder", "provenance", "coverage"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, commits := openReviewFixture(t, -1)
			request := scopedDecode(t, ledger.DecodeRuntimeReviewRequested, commits[17].Records[1].Payload)
			before := f.store.Durable()
			wantTask, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: request.Binding.TaskID})
			if err != nil {
				t.Fatal(err)
			}
			wantReview, err := f.service.ReviewStatus(ctx, ReviewLookup{RequestID: request.RequestID})
			if err != nil {
				t.Fatal(err)
			}
			wantSources, err := f.service.reviewWitnesses(ctx, request.Binding, before)
			if err != nil {
				t.Fatal(err)
			}
			command := scopedDecode(t, ledger.DecodeRuntimeReviewReceipt, commits[17].Records[2].Payload)
			lookup := ReviewCommandLookup{CommandID: command.CommandID, SubmissionSHA256: command.SubmissionSHA256}
			wantReceipt, err := f.service.ReviewCommandStatus(ctx, lookup)
			if err != nil {
				t.Fatal(err)
			}
			wantBook, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil {
				t.Fatal(err)
			}
			damageHistoryCache(f.service, mode)
			for range 2 {
				task, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: request.Binding.TaskID})
				if err != nil || !reflect.DeepEqual(task, wantTask) {
					t.Fatalf("historical task changed: %+v %v", task, err)
				}
				review, err := f.service.ReviewStatus(ctx, ReviewLookup{RequestID: request.RequestID})
				if err != nil || !reflect.DeepEqual(review, wantReview) {
					t.Fatalf("resolved/replaced request changed: %+v %v", review, err)
				}
				receipt, err := f.service.ReviewCommandStatus(ctx, lookup)
				if err != nil || !reflect.DeepEqual(receipt, wantReceipt) {
					t.Fatalf("immutable receipt changed: %+v %v", receipt, err)
				}
				sources, err := f.service.reviewWitnesses(ctx, request.Binding, before)
				if err != nil || !reflect.DeepEqual(sources, wantSources) {
					t.Fatalf("source metadata changed: %v", err)
				}
				if err := f.service.reviewSources(request.Binding, sources); err != nil {
					t.Fatal(err)
				}
				// Neither decoded cache values nor their buffers are lent out.
				for seq, record := range sources {
					if len(record.BodyRefs) != 0 {
						record.BodyRefs[0] = ledger.Digest{}
					}
					record.Payload[0] = '!'
					record.JSON[0] = '!'
					sources[seq] = record
				}
				task.Task.Audience[0] = uuid.UUID{}
			}
			s := f.service
			s.mu.Lock()
			old, found, err := s.loadReview(ctx, request.RequestID, commits[17].Last)
			if err != nil || !found || old.state != "pending" || old.result != nil || old.supersededBy != (uuid.UUID{}) {
				t.Errorf("future review borrowed by pending prefix: %+v %v", old, err)
			}
			if _, found, err := s.loadReview(ctx, request.RequestID, commits[17].First-1); err != nil || found {
				t.Errorf("future request borrowed by earlier prefix: %v %v", found, err)
			}
			earlier, found, err := s.loadTask(ctx, request.Binding.TaskID, commits[17].Last)
			if err != nil || !found || earlier.record.State != "submitted" || earlier.record.Version != request.Binding.TaskVersion || earlier.descriptionSequence != uint64(wantTask.DescriptionRef.SourceSequence) {
				t.Errorf("future terminal task borrowed by earlier prefix: %+v %v", earlier, err)
			}
			tail, err := s.findCommand(ctx, command.CommandID, commits[17].Last, before)
			if err != nil || tail.found {
				t.Errorf("negative tail changed interval: %+v %v", tail, err)
			}
			broad, err := s.findCommand(ctx, command.CommandID, 0, before)
			if err != nil || broad.review == nil || broad.command != (ledger.Command{CommandID: command.CommandID, SubmissionSHA256: command.SubmissionSHA256}) {
				t.Errorf("tail absence erased broader receipt: %+v %v", broad, err)
			}
			if _, err := s.findCommand(ctx, command.CommandID, commits[17].First, before); !errors.Is(err, ledger.ErrCursor) {
				t.Errorf("partial lower commit accepted: %v", err)
			}
			if _, err := s.findCommand(ctx, command.CommandID, 0, commits[17].First+1); !errors.Is(err, ledger.ErrCursor) {
				t.Errorf("partial upper commit accepted: %v", err)
			}
			s.mu.Unlock()
			book, err := s.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || !reflect.DeepEqual(book, wantBook) || f.store.Durable() != before {
				t.Fatalf("lookup changed durable effects or charges: %+v %v", book, err)
			}
		})
	}
}

// Publish already-preflighted real records without installing their plan, so
// the next public query exercises refreshLocked instead of the live shortcut.
func historyReviewAppend(t *testing.T, f *serviceFixture, actor string, binding Binding, operation string, request any) ReviewReceipt {
	t.Helper()
	s := f.service
	ctx := context.Background()
	s.mu.Lock()
	ids, err := s.bindingLocked(binding, false)
	if err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	input, err := makeReviewInput(actor, ids, operation, request)
	var drafts []ledger.Draft
	if err == nil {
		var sources map[int64]ledger.Record
		sources, err = s.reviewWitnesses(ctx, input.binding, s.p.through)
		if err == nil {
			var sample int64
			sample, err = s.deps.reviewClock()
			if err == nil {
				drafts, _, _, err = s.prepareReviewLocked(ctx, input, sources, sample)
			}
		}
	}
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Submit(ctx, drafts[0], drafts[1:]); err != nil {
		t.Fatal(err)
	}
	command := scopedDecode(t, ledger.DecodeCommand, drafts[0].Payload)
	receipt, err := s.ReviewCommandStatus(ctx, ReviewCommandLookup{CommandID: command.CommandID, SubmissionSHA256: command.SubmissionSHA256})
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestHistoryIndexReviewHistory(t *testing.T) {
	t.Parallel()
	for _, publication := range []string{"live", "refresh"} {
		t.Run(publication, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, q := liveReviewFixture(t)
			request := func(q ReviewRequest) ReviewReceipt {
				if publication == "refresh" {
					return historyReviewAppend(t, f, "owner", reviewOwner(), "review.request", q)
				}
				r, err := f.service.RequestReview(ctx, reviewOwner(), q)
				if err != nil {
					t.Fatal(err)
				}
				return r
			}
			reviewer := scopedBound(t, f.service, bindingOf(q.Binding.Reviewer.Origin))
			resolve := func(r ReviewResult) ReviewReceipt {
				if publication == "refresh" {
					return historyReviewAppend(t, f, "agent", bindingOf(q.Binding.Reviewer.Origin), "review.resolve", r)
				}
				receipt, err := reviewer.ResolveReview(ctx, r)
				if err != nil {
					t.Fatal(err)
				}
				return receipt
			}
			warm := func() {
				s := f.service
				s.mu.Lock()
				defer s.mu.Unlock()
				// Seed valid ledger-derived answers to exercise invalidation
				// even when the live map currently has precedence.
				slot := s.p.reviews[q.Binding.TaskID]
				s.history.putReview(slot.id, s.p.through, slot, true)
				s.history.putTask(q.Binding.TaskID, s.p.through, s.p.tasks[q.Binding.TaskID], true)
			}
			first := request(q)
			warm()
			pendingThrough := f.store.Durable()
			result := ReviewResult{CommandID: privateID(2100), GrantID: q.Binding.Reviewer.GrantID, RequestID: q.RequestID, RequestSequence: int64(first.EffectSequence), Binding: q.Binding, Verdict: "reject"}
			rejected := resolve(result)
			original, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: q.Binding.TaskID})
			if err != nil || original.Task.State != "submitted" {
				t.Fatalf("reject terminalized task: %+v %v", original, err)
			}
			warm()
			oldID := q.RequestID
			q.CommandID, q.RequestID, q.ReplacesRequestID = privateID(2101), privateID(2102), oldID
			second := request(q)
			view, err := f.service.ReviewStatus(ctx, ReviewLookup{RequestID: oldID})
			if err != nil || view.State != "rejected" || view.Result == nil || view.Result.Verdict != "reject" || view.Current || view.SupersededByRequestID != q.RequestID || view.ResultSequence != rejected.EffectSequence {
				t.Fatalf("replacement erased resolved result: %+v %v", view, err)
			}
			warm()
			next := result
			next.CommandID, next.RequestID, next.RequestSequence, next.Verdict = privateID(2103), q.RequestID, int64(second.EffectSequence), "approve"
			approved := resolve(next)
			for _, mode := range []string{"warm", "evicted", "disabled"} {
				damageHistoryCache(f.service, mode)
				task, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: q.Binding.TaskID})
				if err != nil || task.Task.State != "completed" || task.Task.Version != q.Binding.TaskVersion+1 || task.DescriptionRef != original.DescriptionRef || task.Sequence != approved.EffectSequence {
					t.Fatalf("review task/source transition lost: %+v %v", task, err)
				}
				before := f.store.Durable()
				retry, err := reviewer.ResolveReview(ctx, result)
				if err != nil || !reflect.DeepEqual(retry, rejected) || f.store.Durable() != before {
					t.Fatalf("replaced result retry changed: %+v %v", retry, err)
				}
			}
			f.service.mu.Lock()
			old, found, err := f.service.loadReview(ctx, oldID, pendingThrough)
			f.service.mu.Unlock()
			if err != nil || !found || old.state != "pending" || old.result != nil {
				t.Fatalf("later result used at old prefix: %+v %v", old, err)
			}
			book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || book.Used.LocalUnits != 2 || book.Held != (Exposure{}) {
				t.Fatalf("review charged more than once: %+v %v", book, err)
			}
			reopenScoped(t, f, DefaultLimits())
			view, err = f.service.ReviewStatus(ctx, ReviewLookup{RequestID: oldID})
			if err != nil || view.State != "rejected" || view.ResultSequence != rejected.EffectSequence || view.SupersededByRequestID != q.RequestID {
				t.Fatalf("reopen changed original result: %+v %v", view, err)
			}
		})
	}
}

func TestHistoryIndexLookupRace(t *testing.T) {
	t.Parallel()
	for _, winner := range []string{"duplicate", "revocation", "replacement"} {
		t.Run(winner, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, q := liveReviewFixture(t)
			_, reviewer, result := requestLiveReview(t, f, q)
			var stop atomic.Bool
			stop.Store(true)
			arrived, release := make(chan struct{}), make(chan struct{})
			var released sync.Once
			defer released.Do(func() { close(release) })
			f.service.deps.beforeHistoryFill = func(key historyKey) {
				if key.kind == historyCommand && key.id == result.CommandID && stop.CompareAndSwap(true, false) {
					close(arrived)
					<-release
				}
			}
			type outcome struct {
				receipt ReviewReceipt
				err     error
			}
			done := make(chan outcome, 1)
			go func() {
				r, err := resolveLiveReview(f, reviewer, result)
				done <- outcome{r, err}
			}()
			<-arrived
			var committed ReviewReceipt
			var err error
			switch winner {
			case "duplicate":
				committed, err = resolveLiveReview(f, reviewer, result)
			case "revocation":
				_, err = f.service.RevokeGrant(ctx, reviewOwner(), GrantRevocation{CommandID: privateID(2200), RevokeGrantID: result.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(f.service.p.authority)})
			case "replacement":
				q.CommandID, q.ReplacesRequestID, q.RequestID = privateID(2201), q.RequestID, privateID(2202)
				_, err = f.service.RequestReview(ctx, reviewOwner(), q)
			}
			before := f.store.Durable()
			released.Do(func() { close(release) })
			late := <-done
			if err != nil {
				t.Fatal(err)
			}
			units := int64(0)
			switch winner {
			case "duplicate":
				units = 1
				if late.err != nil || !reflect.DeepEqual(late.receipt, committed) {
					t.Fatalf("late negative hid winning receipt: %+v %v", late.receipt, late.err)
				}
				retry, err := resolveLiveReview(f, reviewer, result)
				if err != nil || !reflect.DeepEqual(retry, committed) {
					t.Fatalf("late fill poisoned subsequent retry: %+v %v", retry, err)
				}
			case "revocation":
				if !errors.Is(late.err, ErrDenied) {
					t.Fatalf("late lookup bypassed revocation: %v", late.err)
				}
			case "replacement":
				if !errors.Is(late.err, ErrReviewStale) {
					t.Fatalf("late lookup bypassed replacement: %v", late.err)
				}
			}
			book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || book.Used.LocalUnits != units || book.Held != (Exposure{}) || f.store.Durable() != before {
				t.Fatalf("late lookup added effect/charge or leaked hold: %+v %v", book, err)
			}
		})
	}
}

func TestHistoryIndexMutationInvalidation(t *testing.T) {
	t.Parallel()
	for _, mutation := range []string{"revoke", "cancel"} {
		t.Run(mutation, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, q := liveReviewFixture(t)
			_, reviewer, result := requestLiveReview(t, f, q)
			through := f.store.Durable()
			s := f.service
			s.mu.Lock()
			s.history.putGrant(result.GrantID, through, s.p.grants[result.GrantID], true)
			s.history.putTask(q.Binding.TaskID, through, s.p.tasks[q.Binding.TaskID], true)
			s.history.putReview(q.RequestID, through, s.p.reviews[q.Binding.TaskID], true)
			s.mu.Unlock()
			if mutation == "revoke" {
				_, err := s.RevokeGrant(ctx, reviewOwner(), GrantRevocation{CommandID: privateID(2400), RevokeGrantID: result.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(s.p.authority)})
				if err != nil {
					t.Fatal(err)
				}
				assertReviewRefusal(t, f, func() error { _, err := resolveLiveReview(f, reviewer, result); return err }, ErrDenied)
				s.mu.Lock()
				current, found, err := s.loadGrant(ctx, result.GrantID, s.p.through)
				old, oldFound, oldErr := s.loadGrant(ctx, result.GrantID, through)
				s.mu.Unlock()
				if err != nil || !found || !current.revoked || oldErr != nil || !oldFound || old.revoked {
					t.Fatalf("grant revocation crossed historical prefix: current=%+v old=%+v %v %v", current, old, err, oldErr)
				}
			} else {
				if _, err := s.TransitionTask(ctx, reviewOwner(), reviewTaskMutation(t, s, "cancel")); err != nil {
					t.Fatal(err)
				}
				view, err := s.ReviewStatus(ctx, ReviewLookup{RequestID: q.RequestID})
				if err != nil || view.State != "stale" || view.Current || view.Result != nil {
					t.Fatalf("task mutation retained pending request: %+v %v", view, err)
				}
				task, err := s.TaskStatus(ctx, TaskLookup{TaskID: q.Binding.TaskID})
				if err != nil || task.Task.State != "cancelled" || task.Task.Version != q.Binding.TaskVersion+1 {
					t.Fatalf("cached task survived cancellation: %+v %v", task, err)
				}
				assertReviewRefusal(t, f, func() error { _, err := resolveLiveReview(f, reviewer, result); return err }, ErrReviewStale)
			}
		})
	}
}

func TestHistoryIndexWitnessIdentity(t *testing.T) {
	t.Parallel()
	f, commits := openReviewFixture(t, 21)
	ctx := context.Background()
	q := scopedDecode(t, ledger.DecodeRuntimeReviewRequested, commits[21].Records[1].Payload)
	sample := f.service.p.reviewFloor + 315360000000000000
	f.service.deps.reviewClock = func() (int64, error) { return sample, nil }
	q.Binding.DeadlineUTCNS = sample + 1000000000
	q.Binding.Reviewer = reviewOwnerPrincipal()
	if _, err := f.service.reviewWitnesses(ctx, q.Binding, f.store.Durable()); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"sequence", "revision", "slot", "scope", "size", "digest", "binding"} {
		t.Logf("wrong private witness %s", field)
		binding := q.Binding
		ref := *q.Binding.Subject.PrivateRevision
		switch field {
		case "sequence":
			ref.SourceSequence = int64(commits[0].Records[0].Seq)
		case "revision":
			ref.Revision++
		case "slot":
			// This occurrence has both memory and handoff with the same
			// empty digest/size; transcript is valid grammar but absent.
			ref.Slot = "transcript"
		case "scope":
			ref.Scope = "agent"
			if ref.Scope == q.Binding.Subject.PrivateRevision.Scope {
				ref.Scope = "context"
			}
		case "size":
			ref.Bytes++
		case "digest":
			ref.SHA256[0] ^= 1
		case "binding":
			ref.Binding.ContextID = privateID(2598)
		}
		binding.Subject = ledger.SubjectRef{Kind: "private_revision", PrivateRevision: &ref}
		binding.RequiredEvidence = []ledger.SubjectRef{binding.Subject}
		request := FailureRequest{CommandID: privateID(2500), RequestID: privateID(2501), Binding: binding, Cause: q.FailureCause, FailingEvidence: binding.Subject}
		assertReviewRefusal(t, f, func() error {
			_, err := f.service.RequestFailure(ctx, reviewOwner(), request)
			return err
		}, ErrInvalid)
	}
	valid := FailureRequest{CommandID: privateID(2500), RequestID: privateID(2501), Binding: q.Binding, Cause: q.FailureCause, FailingEvidence: *q.FailingEvidence}
	if _, err := f.service.RequestFailure(ctx, reviewOwner(), valid); err != nil {
		t.Fatalf("warm exact source refused after invalid witnesses: %v", err)
	}
}

func TestHistoryIndexDescriptionWitness(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, q := liveReviewFixture(t)
	q.Binding.Reviewer = reviewOwnerPrincipal()
	other, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: reviewGoldenID(0x201)})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := f.service.sourceRecord(ctx, other.DescriptionRef.SourceSequence, f.store.Durable()); err != nil || !found {
		t.Fatalf("description source: %v %v", found, err)
	}
	for _, wrong := range []string{"size", "body-reference"} {
		b := q.Binding
		ref := other.DescriptionRef
		want := ErrInvalid
		if wrong == "size" {
			ref.Bytes++
		} else {
			ref.SHA256[0] ^= 1
			want = ErrDenied
		}
		evidence := ledger.SubjectRef{Kind: "artifact", Artifact: ref}
		b.RequiredEvidence = []ledger.SubjectRef{q.Binding.Subject, evidence}
		request := FailureRequest{CommandID: privateID(2510), RequestID: privateID(2511), Binding: b, Cause: "semantic_failure", FailingEvidence: evidence}
		assertReviewRefusal(t, f, func() error {
			_, err := f.service.RequestFailure(ctx, reviewOwner(), request)
			return err
		}, want)
	}
}

func TestHistoryIndexCommandOccurrences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	b := registerPrivate(t, f.service, 10)
	receipt := writePrivate(t, f.service, b, 20, 1, Change{Slot: "memory", Mode: "replace", Bytes: []byte("receipt")})
	through := f.store.Durable()
	foreign := ledger.Command{CommandID: privateID(90), SubmissionSHA256: ledger.Digest{1}}
	if match, err := f.service.findCommand(ctx, privateID(20), 0, through); err != nil || !match.found {
		t.Fatalf("original command: %+v %v", match, err)
	}
	if match, err := f.service.findCommand(ctx, foreign.CommandID, 0, through); err != nil || match.found {
		t.Fatalf("pre-occurrence absence: %+v %v", match, err)
	}
	var last ledger.Record
	if err := f.service.scan(ctx, 0, through, func(c ledger.Commit) error { last = c.Records[len(c.Records)-1]; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	drafts := []ledger.Draft{
		{Type: ledger.TypeCommand, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: testPayload(t, foreign)},
		{Type: ledger.TypeCommand, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: testPayload(t, ledger.Command{CommandID: privateID(20), SubmissionSHA256: receipt.SubmissionSHA256})},
	}
	extra, chain, err := ledger.EncodeCommit(ledger.Chain{Seq: last.Seq, Hash: last.Hash, EventTimeNS: last.EventTimeNS}, ledger.CommitHeader{ColonyID: f.store.Colony(), CommitID: privateID(91), EventTimeNS: last.EventTimeNS + 1}, drafts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.store.LogDir(), ledger.SegmentName(1))
	writeTestFile(t, path, append(readTestFile(t, path), extra...))
	f.service.mu.Lock()
	err = f.service.refreshLocked(ctx, chain.Seq)
	f.service.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if match, err := f.service.findCommand(ctx, foreign.CommandID, 0, chain.Seq); err != nil || !match.found || match.command != foreign {
		t.Fatalf("accepted F0 occurrence retained negative: %+v %v", match, err)
	}
	if _, err := f.service.findCommand(ctx, privateID(20), 0, chain.Seq); !errors.Is(err, ErrInconsistent) {
		t.Fatalf("cached receipt concealed duplicate command: %v", err)
	}
	if _, err := f.service.findCommand(ctx, foreign.CommandID, 0, chain.Seq-1); !errors.Is(err, ledger.ErrCursor) {
		t.Fatalf("warm command accepted incomplete commit: %v", err)
	}
}

func TestHistoryIndexAuthoritativeCorruption(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	b := registerPrivate(t, f.service, 10)
	receipt := writePrivate(t, f.service, b, 20, 1, Change{Slot: "memory", Mode: "replace", Bytes: []byte("private bytes")})
	lookup := CommandLookup{Binding: b, CommandID: privateID(20), SubmissionSHA256: receipt.SubmissionSHA256}
	if _, err := f.service.CommandStatus(ctx, lookup); err != nil {
		t.Fatal(err)
	}
	damageHistoryCache(f.service, "decoder")
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.store.LogDir(), ledger.SegmentName(1))
	data := readTestFile(t, path)
	at := bytes.LastIndex(data[:len(data)-1], []byte{'\n'}) + 1
	data[at] = 'x'
	writeTestFile(t, path, data)
	if _, err := f.service.CommandStatus(ctx, lookup); !errors.Is(err, ledger.ErrCorrupt) {
		t.Fatalf("damaged acceleration hid corrupt authoritative history: %v", err)
	}
	if _, _, err := f.service.sourceRecord(ctx, int64(receipt.Sequence-1), f.store.Durable()); !errors.Is(err, ledger.ErrCorrupt) {
		t.Fatalf("damaged source acceleration hid corrupt authoritative commit: %v", err)
	}
}

func TestHistoryIndexWorkspaceRegistration(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"warm", "discarded", "evicted", "damaged", "decoder"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, commits := scopedGoldenFixture(t, "11", 4)
			var err error
			f.service, _, err = Open(ctx, f.store, f.root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			registration := commits[4]
			workspace := scopedDecode(t, ledger.DecodeWorkspaceRegistered, registration.Records[0].Payload)
			resource := ledger.Resource{Kind: "workspace", WorkspaceID: workspace.WorkspaceID, Binding: ledger.Binding{AgentID: commits[0].Records[0].AgentID, ContextID: commits[0].Records[0].ContextID}, PathPrefix: "."}
			query := func(resource ledger.Resource, through uint64) (bool, error) {
				f.service.mu.Lock()
				defer f.service.mu.Unlock()
				return f.service.registeredResource(ctx, resource, through)
			}
			before := f.store.Durable()
			if found, err := query(resource, before); err != nil || found {
				t.Fatalf("workspace before registration: %v %v", found, err)
			}
			registered, err := f.store.Append(ctx, scopedGoldenDrafts(registration))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}}); err != nil {
				t.Fatal(err)
			}
			if found, err := query(resource, registered.Last); err != nil || !found {
				t.Fatalf("accepted registration retained absence: %v %v", found, err)
			}
			if mode == "damaged" {
				h := f.service.history
				h.mu.Lock()
				node := h.entries[historyKey{kind: historyWorkspace, id: resource.WorkspaceID}]
				if node != nil {
					// Change the answer without updating its checksum.
					node.Value.(*historyEntry).found = false
				}
				h.mu.Unlock()
			} else {
				damageHistoryCache(f.service, mode)
			}
			if found, err := query(resource, registered.Last); err != nil || !found {
				t.Fatalf("registration lost after %s: %v %v", mode, found, err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			f.service.mu.Lock()
			found, err := f.service.registeredResource(cancelled, resource, registered.Last)
			f.service.mu.Unlock()
			if found || !errors.Is(err, context.Canceled) {
				t.Fatalf("warm registration ignored cancellation: %v %v", found, err)
			}
			wrong := resource
			wrong.Binding.AgentID = privateID(29999)
			if found, err := query(wrong, registered.Last); err != nil || found {
				t.Fatalf("registration bypassed context binding: %v %v", found, err)
			}
			if found, err := query(resource, before); err != nil || found {
				t.Fatalf("later registration leaked into earlier prefix: %v %v", found, err)
			}
			if _, err := query(resource, registered.First); !errors.Is(err, ledger.ErrCursor) {
				t.Fatalf("partial registration commit accepted: %v", err)
			}
			if found, err := query(resource, registered.Last); err != nil || !found {
				t.Fatalf("historical absence replaced current registration: %v %v", found, err)
			}
			if _, err := f.store.Append(ctx, scopedGoldenDrafts(commits[5])); err != nil {
				t.Fatal(err)
			}
			drafts := scopedGoldenDrafts(commits[6])
			advanced, err := f.store.Submit(ctx, drafts[0], drafts[1:])
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}}); err != nil {
				t.Fatal(err)
			}
			if _, err := query(resource, advanced.First); !errors.Is(err, ledger.ErrCursor) {
				t.Fatalf("registration proof accepted an arbitrary intermediate prefix: %v", err)
			}
			// An unrelated malformed registration is still part of the full
			// authoritative search. Optional invalidation must lose the proof,
			// not turn this decoder error into a positive or negative answer.
			if _, err := f.store.Append(ctx, []ledger.Draft{{Type: ledger.TypeWorkspaceRegistered, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: []byte("{}")}}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}}); err != nil {
				t.Fatalf("optional invalidation added a refusal: %v", err)
			}
			through := f.store.Durable()
			if _, err := query(resource, through); !errors.Is(err, ledger.ErrInvalid) {
				t.Fatalf("registration proof concealed authoritative error: %v", err)
			}
			if _, err := query(resource, through); !errors.Is(err, ledger.ErrInvalid) || f.store.Durable() != through {
				t.Fatalf("failed full search published an answer or an effect: %v", err)
			}
		})
	}
}

func TestHistoryIndexWorkspaceReadiness(t *testing.T) {
	t.Parallel()
	for _, refusal := range []string{"grant", "origin", "revoked", "closed", "missing"} {
		t.Run(refusal, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, agent, start, admit := scopedLocalFixture(t, DefaultLimits())
			if _, err := agent.StartTurn(ctx, start); err != nil {
				t.Fatal(err)
			}
			f.service.mu.Lock()
			found, err := f.service.registeredResource(ctx, admit.Target, f.service.p.through)
			f.service.mu.Unlock()
			if err != nil || !found {
				t.Fatalf("registered workspace: %v %v", found, err)
			}
			want := ErrDenied
			switch refusal {
			case "grant":
				admit.GrantID = privateID(29999)
			case "origin":
				admit.Target.Binding.ContextID = privateID(29999)
			case "revoked":
				if _, err := f.service.RevokeGrant(ctx, Binding{AgentID: agent.ids.agent, ContextID: agent.ids.context}, GrantRevocation{CommandID: privateID(9011), RevokeGrantID: scopedGoldenID(0x300), ExpectedVersion: 1, ExpectedAuthorityVersion: 4}); err != nil {
					t.Fatal(err)
				}
			case "closed", "missing":
				binding := Binding{AgentID: agent.ids.agent, ContextID: agent.ids.context}
				scratch, err := f.service.ScratchBinding(ctx, ScratchLookup{Binding: binding, WorkspaceID: admit.Target.WorkspaceID})
				if err != nil || scratch.State != "present" {
					t.Fatalf("initial matching scratch: %+v %v", scratch, err)
				}
				if refusal == "closed" {
					payload := testPayload(t, ledger.ScratchClosed{ScratchID: scratch.ScratchID, ContextID: binding.ContextID, Reason: "abandoned"})
					if _, err := f.store.Append(ctx, []ledger.Draft{{Type: ledger.TypeScratchClosed, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: payload}}); err != nil {
						t.Fatal(err)
					}
				} else {
					// Losing the live scratch projection must not make cached
					// registration evidence a substitute for a matching root.
					f.service.mu.Lock()
					delete(f.service.p.scratches, scratch.ScratchID)
					f.service.mu.Unlock()
				}
				want = ErrUnavailable
			}
			assertReviewRefusal(t, f, func() error {
				_, err := agent.AdmitLocal(ctx, admit)
				return err
			}, want)
		})
	}
}

func TestHistoryIndexAcceptedSources(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"warm", "discarded", "evicted", "damaged"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, binding, agent, grant := scopedPrivateFixture(t, serviceDeps{}, scopedCapacity())
			writePrivate(t, f.service, binding, 2500, 1, Change{Slot: "memory", Mode: "replace", Bytes: []byte("owner source")})
			if _, err := agent.WritePrivate(ctx, ScopedPrivateWrite{CommandID: privateID(2501), GrantID: grant.GrantID, Scope: "context", ExpectedVersion: 2, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("scoped source")}}}); err != nil {
				t.Fatal(err)
			}
			task := scopedNewTask(2502, 2503, uuid.UUID{}, binding.AgentID)
			if _, err := f.service.CreateTask(ctx, binding, task); err != nil {
				t.Fatal(err)
			}
			var sources []ledger.Record
			bounds := map[uint64][2]uint64{}
			through := f.store.Durable()
			if err := f.service.scan(ctx, 0, through, func(c ledger.Commit) error {
				for _, record := range c.Records {
					if record.Type == ledger.TypePrivateRevision || record.Type == ledger.TypePrivateScopedRevision || len(record.BodyRefs) != 0 {
						sources = append(sources, record)
						bounds[record.Seq] = [2]uint64{c.First, c.Last}
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for phase := range 2 {
				if phase == 1 {
					reopenScoped(t, f, DefaultLimits())
				}
				// The first query follows real live acceptance or Open, not a
				// hand-inserted entry or a warming source lookup.
				damageHistoryCache(f.service, mode)
				for _, want := range sources {
					seq := int64(want.Seq)
					got, found, err := f.service.sourceRecord(ctx, seq, through)
					if err != nil || !found || !reflect.DeepEqual(got, want) {
						t.Fatalf("accepted source %d changed: %+v %v", seq, got, err)
					}
					if len(got.BodyRefs) != 0 {
						got.BodyRefs[0] = ledger.Digest{}
					}
					got.Payload[0], got.JSON[0] = '!', '!'
					again, found, err := f.service.sourceRecord(ctx, seq, through)
					if err != nil || !found || !reflect.DeepEqual(again, want) {
						t.Fatalf("source %d lent mutable storage: %+v %v", seq, again, err)
					}
					bound := bounds[want.Seq]
					if _, found, err := f.service.sourceRecord(ctx, seq, bound[0]-1); err != nil || found {
						t.Fatalf("source %d leaked before its commit: %v %v", seq, found, err)
					}
					if want.Seq < bound[1] {
						if _, _, err := f.service.sourceRecord(ctx, seq, bound[1]-1); !errors.Is(err, ledger.ErrCursor) {
							t.Fatalf("source %d escaped its complete commit: %v", seq, err)
						}
					}
				}
			}
		})
	}
}

func TestHistoryIndexSourceEligibility(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"unconsumed-body-reference", "command-without-body-reference"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, commits := scopedGoldenFixture(t, "11", 5)
			var err error
			f.service, _, err = Open(ctx, f.store, f.root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			// A body-bearing capture-input record is unconsumed; an F0
			// command is consumed but not eligible for source warming.
			// Neither may turn a later authoritative error into a hit.
			source := commits[4].Records[1]
			if kind == "command-without-body-reference" {
				source = commits[0].Records[0]
			}
			through := f.store.Durable()
			if err := f.store.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.store.LogDir(), ledger.SegmentName(1))
			lines := bytes.SplitAfter(readTestFile(t, path), []byte{'\n'})
			lines[source.Seq-1][0] = 'x'
			writeTestFile(t, path, bytes.Join(lines, nil))
			if _, _, err := f.service.sourceRecord(ctx, int64(source.Seq), through); !errors.Is(err, ledger.ErrCorrupt) {
				t.Fatalf("ineligible source bypassed authoritative reader: %v", err)
			}
		})
	}
}
