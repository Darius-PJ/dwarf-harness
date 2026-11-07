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
	if _, found, err := f.service.contextTask(ctx, nil, origin, unassigned); err != nil || found {
		t.Fatalf("later association borrowed by earlier prefix: found=%v %v", found, err)
	}
	task, found, err := f.service.contextTask(ctx, nil, origin, assigned)
	if err != nil || !found || task.record.Version != 1 || task.record.Assignment == nil || task.record.Assignment.Assignee != ledger.Binding(old) {
		t.Fatalf("later assignment borrowed by earlier task: %+v %v", task.record, err)
	}
	if _, found, err := f.service.loadTask(ctx, nil, creation.TaskID, unassigned); err != nil || found {
		t.Fatalf("future task existed before creation: found=%v %v", found, err)
	}
	if _, found, err := f.service.loadGrant(ctx, nil, grant.GrantID, assigned); err != nil || found {
		t.Fatalf("future grant existed before issue: found=%v %v", found, err)
	}
	if _, found, err := f.service.loadWork(ctx, nil, privateID(701), assigned); err != nil || found {
		t.Fatalf("future admission existed before work: found=%v %v", found, err)
	}
	idUnused, contextUnused, err := f.service.assignmentUnused(ctx, nil, *creation.Assignment, unassigned)
	if err != nil || !idUnused || !contextUnused {
		t.Fatalf("future assignment consumed earlier identity: id=%v context=%v %v", idUnused, contextUnused, err)
	}
}

func damageHistoryCache(s *Service, mode string) {
	if directoryMode := map[string]string{"directory-missing": "missing", "directory-stale": "stale", "directory-generation": "generation", "directory-row": "row", "directory-boundary": "boundary"}[mode]; directoryMode != "" {
		damageCommandDirectory(s, directoryMode)
		return
	}
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
	for _, mode := range []string{"empty", "warm", "evicted", "discarded", "disabled", "damaged", "key", "range", "decoder", "provenance", "coverage", "directory-missing", "directory-stale", "directory-generation", "directory-row", "directory-boundary"} {
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
			wantSources, err := f.service.reviewWitnesses(ctx, nil, request.Binding, before)
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
				sources, err := f.service.reviewWitnesses(ctx, nil, request.Binding, before)
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
			old, found, err := s.loadReview(ctx, nil, request.RequestID, commits[17].Last)
			if err != nil || !found || old.state != "pending" || old.result != nil || old.supersededBy != (uuid.UUID{}) {
				t.Errorf("future review borrowed by pending prefix: %+v %v", old, err)
			}
			if _, found, err := s.loadReview(ctx, nil, request.RequestID, commits[17].First-1); err != nil || found {
				t.Errorf("future request borrowed by earlier prefix: %v %v", found, err)
			}
			earlier, found, err := s.loadTask(ctx, nil, request.Binding.TaskID, commits[17].Last)
			if err != nil || !found || earlier.record.State != "submitted" || earlier.record.Version != request.Binding.TaskVersion || earlier.descriptionSequence != uint64(wantTask.DescriptionRef.SourceSequence) {
				t.Errorf("future terminal task borrowed by earlier prefix: %+v %v", earlier, err)
			}
			tail, err := s.findCommand(ctx, nil, command.CommandID, commits[17].Last, before)
			if err != nil || tail.found {
				t.Errorf("negative tail changed interval: %+v %v", tail, err)
			}
			broad, err := s.findCommand(ctx, nil, command.CommandID, 0, before)
			if err != nil || broad.review == nil || broad.command != (ledger.Command{CommandID: command.CommandID, SubmissionSHA256: command.SubmissionSHA256}) {
				t.Errorf("tail absence erased broader receipt: %+v %v", broad, err)
			}
			if _, err := s.findCommand(ctx, nil, command.CommandID, commits[17].First, before); !errors.Is(err, ledger.ErrCursor) {
				t.Errorf("partial lower commit accepted: %v", err)
			}
			if _, err := s.findCommand(ctx, nil, command.CommandID, 0, commits[17].First+1); !errors.Is(err, ledger.ErrCursor) {
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

func TestHistoryIndexAcknowledgementFallback(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"discarded", "evicted", "disabled", "damaged", "decoder", "byte-pressure"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, binding, agent, grant := scopedPrivateFixture(t, serviceDeps{}, scopedCapacity())
			warmed := warmScopedPrivateAcknowledgement(t, f, agent, grant)
			beforeBook, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "byte-pressure" {
				h := f.service.history
				for i := range 2 {
					h.put(historyEntry{key: historyKey{kind: historyCommand, id: privateID(29998 + i)}, from: warmed.Sequence, found: true, data: make([]byte, historyPayloadByteLimit-128)})
				}
			} else {
				damageHistoryCache(f.service, mode)
			}
			token := f.service.history.checkpoint(warmed.Sequence)
			retained := mode == "evicted" || mode == "byte-pressure"
			if (token != nil) != retained {
				t.Fatalf("checkpoint precondition for %s: present=%v", mode, token != nil)
			}
			request := ScopedPrivateWrite{CommandID: privateID(2500), GrantID: grant.GrantID, Scope: "context", ExpectedVersion: uint64(warmed.Version), Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("after checkpoint loss")}}}
			receipt, err := agent.WritePrivate(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			through := f.store.Durable()
			var durable ScopedReceipt
			if err := f.service.scan(ctx, warmed.Sequence, through, func(c ledger.Commit) error {
				last := c.Records[len(c.Records)-1]
				durable = scopedReceiptAt(scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, last.Payload), last.Seq)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(receipt, durable) || receipt.Version != warmed.Version+1 {
				t.Fatalf("fallback changed the authoritative receipt: %+v %+v", receipt, durable)
			}
			for phase := range 2 {
				if phase == 1 {
					reopenScoped(t, f, DefaultLimits())
					agent = scopedBound(t, f.service, binding)
				}
				retry, err := agent.WritePrivate(ctx, request)
				if err != nil || !reflect.DeepEqual(retry, durable) || f.store.Durable() != through {
					t.Fatalf("checkpoint loss repeated the durable write: %+v %v", retry, err)
				}
				view, err := agent.Snapshot(ctx, ScopedSnapshotLookup{GrantID: grant.GrantID, Scope: "context"})
				if err != nil || view.Revision != uint64(warmed.Version+1) {
					t.Fatalf("fallback lost the accepted private head: %+v %v", view, err)
				}
				if page := readPrivate(t, f.service, binding, view.Revision, "memory"); string(page.Bytes) != "after checkpoint loss" {
					t.Fatalf("fallback materialized a different write: %q", page.Bytes)
				}
				book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
				used := beforeBook.Used
				used.LocalUnits++
				if err != nil || book.Used != used || book.Held != beforeBook.Held {
					t.Fatalf("fallback repeated a charge or leaked a hold: %+v %v", book, err)
				}
				changed := request
				changed.Changes = []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("changed request")}}
				assertReviewRefusal(t, f, func() error { _, err := agent.WritePrivate(ctx, changed); return err }, ErrCommandMismatch)
			}
		})
	}
	for _, publication := range []string{"foreign-review", "direct-review"} {
		t.Run(publication, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, request := liveReviewFixture(t)
			creation := scopedNewTask(2600, 2601, uuid.UUID{}, reviewOwner().AgentID)
			_, err := f.service.CreateTask(ctx, reviewOwner(), creation)
			if err != nil {
				t.Fatal(err)
			}
			var reviewed ReviewReceipt
			if publication == "foreign-review" {
				reviewed = historyReviewAppend(t, f, "owner", reviewOwner(), "review.request", request)
			} else {
				reviewed, err = f.service.RequestReview(ctx, reviewOwner(), request)
				if err != nil {
					t.Fatal(err)
				}
			}
			view, err := f.service.ReviewStatus(ctx, ReviewLookup{RequestID: request.RequestID})
			if err != nil || view.State != "pending" || view.RequestSequence != reviewed.EffectSequence {
				t.Fatalf("accepted review advance lost its request: %+v %v", view, err)
			}
			advanced := f.store.Durable()
			cancel := TaskMutation{CommandID: privateID(2602), TaskID: creation.TaskID, ExpectedVersion: 1, Action: "cancel"}
			receipt, err := f.service.TransitionTask(ctx, reviewOwner(), cancel)
			if err != nil {
				t.Fatal(err)
			}
			through := f.store.Durable()
			var durable ScopedReceipt
			if err := f.service.scan(ctx, advanced, through, func(c ledger.Commit) error {
				last := c.Records[len(c.Records)-1]
				durable = scopedReceiptAt(scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, last.Payload), last.Seq)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(receipt, durable) {
				t.Fatalf("post-review acknowledgement differs from durable receipt: %+v %+v", receipt, durable)
			}
			beforeBook, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil {
				t.Fatal(err)
			}
			for phase := range 2 {
				if phase == 1 {
					reopenScoped(t, f, DefaultLimits())
				}
				retry, err := f.service.TransitionTask(ctx, reviewOwner(), cancel)
				if err != nil || !reflect.DeepEqual(retry, durable) || f.store.Durable() != through {
					t.Fatalf("post-review retry repeated acceptance: %+v %v", retry, err)
				}
				task, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: creation.TaskID})
				if err != nil || task.Task.State != "cancelled" || task.Task.Version != 2 {
					t.Fatalf("post-review effect changed: %+v %v", task, err)
				}
				book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
				if err != nil || !reflect.DeepEqual(book, beforeBook) {
					t.Fatalf("post-review retry changed exposure: %+v %v", book, err)
				}
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
		sources, err = s.reviewWitnesses(ctx, nil, input.binding, s.p.through)
		if err == nil {
			var sample int64
			sample, err = s.deps.reviewClock()
			if err == nil {
				drafts, _, _, err = s.prepareReviewLocked(ctx, nil, input, sources, sample)
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
			old, found, err := f.service.loadReview(ctx, nil, oldID, pendingThrough)
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
				current, found, err := s.loadGrant(ctx, nil, result.GrantID, s.p.through)
				old, oldFound, oldErr := s.loadGrant(ctx, nil, result.GrantID, through)
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
	if _, err := f.service.reviewWitnesses(ctx, nil, q.Binding, f.store.Durable()); err != nil {
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
	if _, found, err := f.service.sourceRecord(ctx, nil, other.DescriptionRef.SourceSequence, f.store.Durable()); err != nil || !found {
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
	if match, err := f.service.findCommand(ctx, nil, privateID(20), 0, through); err != nil || !match.found {
		t.Fatalf("original command: %+v %v", match, err)
	}
	if match, err := f.service.findCommand(ctx, nil, foreign.CommandID, 0, through); err != nil || match.found {
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
	err = f.service.refreshLocked(ctx, &historyReadSet{}, chain.Seq)
	f.service.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if match, err := f.service.findCommand(ctx, nil, foreign.CommandID, 0, chain.Seq); err != nil || !match.found || match.command != foreign {
		t.Fatalf("accepted F0 occurrence retained negative: %+v %v", match, err)
	}
	if _, err := f.service.findCommand(ctx, nil, privateID(20), 0, chain.Seq); !errors.Is(err, ErrInconsistent) {
		t.Fatalf("cached receipt concealed duplicate command: %v", err)
	}
	if match, err := f.service.findCommand(ctx, nil, privateID(20), through, chain.Seq); err != nil || !match.found || match.group != nil || match.command.SubmissionSHA256 != receipt.SubmissionSHA256 {
		t.Fatalf("unique foreign tail occurrence changed: %+v %v", match, err)
	}
	if match, err := f.service.findCommand(ctx, nil, privateID(20), 0, through); err != nil || match.group == nil || !reflect.DeepEqual(match.group.result, receipt) {
		t.Fatalf("future duplicate changed prior receipt: %+v %v", match, err)
	}
	if _, err := f.service.findCommand(ctx, nil, foreign.CommandID, 0, chain.Seq-1); !errors.Is(err, ledger.ErrCursor) {
		t.Fatalf("warm command accepted incomplete commit: %v", err)
	}
}

func TestAgentTailCommandIndex(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"reuse", "foreign_reuse", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			var captured []ledger.Draft
			f := newServiceFixture(t, serviceDeps{beforeSubmit: func(d []ledger.Draft) []ledger.Draft {
				captured = d
				return d
			}})
			b := registerPrivate(t, f.service, 100)
			first := writePrivate(t, f.service, b, 20, 1, Change{Slot: "memory", Mode: "replace", Bytes: []byte("first")})
			before := f.store.Durable()
			var last ledger.Record
			if err := f.service.scan(ctx, 0, before, func(c ledger.Commit) error {
				last = c.Records[len(c.Records)-1]
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.store.LogDir(), ledger.SegmentName(1))
			prefix := readTestFile(t, path)
			writePrivate(t, f.service, b, 30, 2, Change{Slot: "memory", Mode: "replace", Bytes: []byte("second")})
			drafts := captured
			previous := ledger.Chain{Seq: last.Seq, Hash: last.Hash, EventTimeNS: last.EventTimeNS}
			reused := privateID(20)
			if kind == "foreign_reuse" {
				reused = privateID(40)
				foreign := []ledger.Draft{{Type: ledger.TypeCommand, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: testPayload(t, ledger.Command{CommandID: reused, SubmissionSHA256: ledger.Digest{1}})}}
				raw, chain, err := ledger.EncodeCommit(previous, ledger.CommitHeader{ColonyID: f.store.Colony(), CommitID: privateID(89), EventTimeNS: previous.EventTimeNS + 1}, foreign)
				if err != nil {
					t.Fatal(err)
				}
				prefix, previous, before = append(prefix, raw...), chain, chain.Seq
			}
			if kind != "duplicate" {
				cmd := scopedDecode(t, ledger.DecodeCommand, drafts[0].Payload)
				cmd.CommandID = reused
				drafts[0].Payload = testPayload(t, cmd)
				receipt := scopedDecode(t, ledger.DecodeRuntimeCommandReceipt, drafts[len(drafts)-1].Payload)
				receipt.CommandID = cmd.CommandID
				drafts[len(drafts)-1].Payload = testPayload(t, receipt)
				for i := range drafts {
					drafts[i].OperationID = cmd.CommandID
				}
			} else {
				drafts = []ledger.Draft{
					{Type: ledger.TypeCommand, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: testPayload(t, ledger.Command{CommandID: privateID(20), SubmissionSHA256: first.SubmissionSHA256})},
					{Type: ledger.TypeCommand, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: testPayload(t, ledger.Command{CommandID: privateID(40), SubmissionSHA256: ledger.Digest{1}})},
				}
			}
			if err := f.service.Close(); err != nil {
				t.Fatal(err)
			}
			if err := f.store.Close(); err != nil {
				t.Fatal(err)
			}
			extra, chain, err := ledger.EncodeCommit(previous, ledger.CommitHeader{ColonyID: f.store.Colony(), CommitID: privateID(90), EventTimeNS: previous.EventTimeNS + 1}, drafts)
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, path, append(prefix, extra...))
			// Replay uses the real Reader over correctly framed commits, rather
			// than the store's duplicate-admission guard.
			s := &Service{store: f.store, p: newProjection(), history: newHistoryIndex()}
			s.mu.Lock()
			err = s.refreshLocked(ctx, &historyReadSet{}, chain.Seq)
			s.mu.Unlock()
			if kind != "duplicate" {
				if !errors.Is(err, ErrInconsistent) || s.p.through != before {
					t.Fatalf("reused command published: through=%d error=%v", s.p.through, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.findCommand(ctx, nil, privateID(20), 0, chain.Seq); !errors.Is(err, ErrInconsistent) {
				t.Fatalf("duplicate command returned a receipt: %v", err)
			}
			if m, err := s.findCommand(ctx, nil, privateID(20), 0, before); err != nil || m.group == nil || !reflect.DeepEqual(m.group.result, first) {
				t.Fatalf("later duplicate changed historical receipt: %+v %v", m, err)
			}
			if _, err := s.findCommand(ctx, nil, privateID(40), before+1, chain.Seq); !errors.Is(err, ledger.ErrCursor) {
				t.Fatalf("partial lower endpoint accepted: %v", err)
			}
		})
	}
}

func damageCommandDirectory(s *Service, mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.history
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clearLocked()
	d := &h.directory
	switch mode {
	case "missing":
		*d = commandDirectory{}
	case "stale":
		d.boundaries = d.boundaries[:len(d.boundaries)-1]
		d.boundaryCount = len(d.boundaries)
		d.through = d.boundaries[len(d.boundaries)-1].last
		for key, bucket := range d.rows {
			for len(bucket.rows) != 0 && bucket.rows[len(bucket.rows)-1].last > d.through {
				bucket.rows = bucket.rows[:len(bucket.rows)-1]
				d.rowCount--
			}
			if len(bucket.rows) == 0 {
				delete(d.rows, key)
				d.keyCount--
			} else {
				bucket.count = len(bucket.rows)
				d.rows[key] = bucket
			}
		}
		d.chargedBytes = 96*d.rowCount + 24*d.boundaryCount
		d.checksum = d.sum()
		h.through, h.checksum = d.through, historyCoverageSum(d.through)
	case "generation":
		h.generation++
	case "row":
		for key, bucket := range d.rows {
			bucket.rows[0].checksum ^= 1
			d.rows[key] = bucket
		}
	case "boundary":
		d.boundaries[0].checksum ^= 1
	case "count":
		d.rowCount++
	case "disabled":
		h.disabled = true
	}
}

func TestAgentTailDiscardRecovery(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"cold", "missing", "stale", "generation", "row", "boundary", "count", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := newServiceFixture(t, serviceDeps{})
			b := registerPrivate(t, f.service, 100)
			request := memoryWrite(b, 20, 1, "retained")
			receipt, err := f.service.WritePrivate(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			before := f.store.Durable()
			for phase := range 2 {
				if phase == 1 {
					reopenScoped(t, f, DefaultLimits())
				}
				damageCommandDirectory(f.service, mode)
				got, err := f.service.WritePrivate(ctx, request)
				if err != nil || !reflect.DeepEqual(got, receipt) {
					t.Fatalf("retry lost receipt: %+v %v", got, err)
				}
				mismatch := memoryWrite(b, 20, 2, "changed")
				if _, err := f.service.WritePrivate(ctx, mismatch); !errors.Is(err, ErrCommandMismatch) {
					t.Fatalf("reused ID accepted after directory loss: %v", err)
				}
				if _, err := f.service.CommandStatus(ctx, CommandLookup{Binding: b, CommandID: privateID(19), SubmissionSHA256: receipt.SubmissionSHA256}); !errors.Is(err, ErrUnavailable) {
					t.Fatalf("absent ID gained a receipt: %v", err)
				}
				if f.store.Durable() != before {
					t.Fatal("directory recovery appended")
				}
			}
		})
	}
	t.Run("cold_load", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		p := f.reserve(t)
		f.dispatch(t, p)
		result := f.result(t, p, 10, 2)
		f.append(t, result)
		correction := paidCorrection(f, t, p, result, 19, 2)
		f.append(t, correction)
		pending := f.reserve(t)
		before := f.store.Durable()
		for _, mode := range []string{"cold", "missing", "reopen"} {
			if mode == "reopen" {
				reopenScoped(t, f.serviceFixture, DefaultLimits())
			}
			f.service.mu.Lock()
			f.service.p.paidHotLimit = 0
			f.service.p.prunePaid()
			f.service.mu.Unlock()
			damageCommandDirectory(f.service, mode)
			if v := f.view(t, p.AttemptID); v.SettledMicros != "21" || v.HeldMicros != "0" || v.Version != 4 || v.Fenced {
				t.Fatalf("cold corrected amount changed: %+v", v)
			}
			if v := f.view(t, pending.AttemptID); v.HeldMicros != "120" || v.SettledMicros != "0" || v.State != "reserved" {
				t.Fatalf("pending hold changed: %+v", v)
			}
			if v := f.budget(t, p.AdmissionPeriod); v.SettledMicros != "21" || v.HeldMicros != "120" {
				t.Fatalf("directory recovery changed exposure: %+v", v)
			}
			if f.store.Durable() != before {
				t.Fatal("cold paid recovery appended")
			}
		}
	})
}

func TestAgentTailColdReadRace(t *testing.T) {
	t.Parallel()
	t.Run("command_commit", testColdCommandCommit)
	t.Run("late_command_negative", testColdCommandNegative)
	t.Run("late_negative_scope_conflict", testColdNegativeScopeConflict)
	t.Run("larger_refresh_waiter", testColdLargerRefreshWaiter)
	t.Run("foreign_usage_recovery", testColdForeignUsageRecovery)
	for _, fence := range []string{"cancel", "close"} {
		t.Run("cold_read_"+fence, func(t *testing.T) { testColdReadFence(t, fence) })
	}
	t.Run("revoked_grant_history", testColdGrantHistory)
	t.Run("terminal_task_history", testColdTaskHistory)
	t.Run("ended_work_history", testColdWorkHistory)
	t.Run("review_clock_backwards", testColdReviewClock)
	t.Run("loop_cancel_during_authority_read", testColdLoopCancel)
	t.Run("loop_authority_read_error", testColdLoopAuthorityError)
	t.Run("source_revocation", testColdSourceRevocation)
	t.Run("paid_read_error_identity", testColdPaidReadError)
	t.Run("paid_totals_correction", testColdPaidTotals)
	t.Run("assignment_negative_move", testColdAssignment)
	t.Run("workspace_readiness", testColdWorkspace)
	t.Run("context_association", testColdContextTask)
	t.Run("transcript_selection", testColdScope)
	t.Run("registration_version", testColdRegistration)
	t.Run("originating_budget", testColdBudget)
	t.Run("credential_version", testColdCredential)
	for _, mutation := range []string{"advance", "generation"} {
		t.Run("directory_"+mutation, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := newServiceFixture(t, serviceDeps{})
			b := registerPrivate(t, f.service, 100)
			first := writePrivate(t, f.service, b, 20, 1, Change{Slot: "memory", Mode: "replace", Bytes: []byte("first")})
			damageCommandDirectory(f.service, "missing")
			arrived, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			var armed atomic.Bool
			armed.Store(true)
			f.service.deps.beforeRangeRead = func(after, through uint64) {
				if after == 0 && through == first.Sequence && armed.CompareAndSwap(true, false) {
					close(arrived)
					<-release
				}
			}
			type answer struct {
				receipt Receipt
				err     error
			}
			done := make(chan answer, 1)
			go func() {
				receipt, err := f.service.CommandStatus(ctx, CommandLookup{Binding: b, CommandID: privateID(20), SubmissionSHA256: first.SubmissionSHA256})
				done <- answer{receipt, err}
			}()
			<-arrived
			if mutation == "generation" {
				damageCommandDirectory(f.service, "generation")
			}
			// Completion, not elapsed time, proves the rebuild owns no
			// Service.mu and cannot overwrite a newer accepted frontier.
			second := writePrivate(t, f.service, b, 21, 2, Change{Slot: "memory", Mode: "replace", Bytes: []byte("winner")})
			once.Do(func() { close(release) })
			got := <-done
			if got.err != nil || !reflect.DeepEqual(got.receipt, first) {
				t.Fatalf("rebuild changed original receipt: %+v %v", got.receipt, got.err)
			}
			for phase := range 2 {
				if phase == 1 {
					reopenScoped(t, f, DefaultLimits())
				}
				if page := readPrivate(t, f.service, b, second.Revision, "memory"); string(page.Bytes) != "winner" {
					t.Fatalf("late rebuild replaced winner: %q", page.Bytes)
				}
				if _, err := f.service.WritePrivate(ctx, memoryWrite(b, 20, second.Revision, "reuse")); !errors.Is(err, ErrCommandMismatch) {
					t.Fatalf("late rebuild concealed reused identity: %v", err)
				}
				if _, err := f.service.CommandStatus(ctx, CommandLookup{Binding: b, CommandID: privateID(19), SubmissionSHA256: first.SubmissionSHA256}); !errors.Is(err, ErrUnavailable) {
					t.Fatalf("rebuild manufactured an absent receipt: %v", err)
				}
				if f.store.Durable() != second.Sequence {
					t.Fatal("rebuild or retry appended an effect")
				}
			}
		})
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
	if _, _, err := f.service.sourceRecord(ctx, nil, int64(receipt.Sequence-1), f.store.Durable()); !errors.Is(err, ledger.ErrCorrupt) {
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
				return f.service.registeredResource(ctx, nil, resource, through)
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
			found, err := f.service.registeredResource(cancelled, nil, resource, registered.Last)
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
			found, err := f.service.registeredResource(ctx, nil, admit.Target, f.service.p.through)
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
					got, found, err := f.service.sourceRecord(ctx, nil, seq, through)
					if err != nil || !found || !reflect.DeepEqual(got, want) {
						t.Fatalf("accepted source %d changed: %+v %v", seq, got, err)
					}
					if len(got.BodyRefs) != 0 {
						got.BodyRefs[0] = ledger.Digest{}
					}
					got.Payload[0], got.JSON[0] = '!', '!'
					again, found, err := f.service.sourceRecord(ctx, nil, seq, through)
					if err != nil || !found || !reflect.DeepEqual(again, want) {
						t.Fatalf("source %d lent mutable storage: %+v %v", seq, again, err)
					}
					bound := bounds[want.Seq]
					if _, found, err := f.service.sourceRecord(ctx, nil, seq, bound[0]-1); err != nil || found {
						t.Fatalf("source %d leaked before its commit: %v %v", seq, found, err)
					}
					if want.Seq < bound[1] {
						if _, _, err := f.service.sourceRecord(ctx, nil, seq, bound[1]-1); !errors.Is(err, ledger.ErrCursor) {
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
			if _, _, err := f.service.sourceRecord(ctx, nil, int64(source.Seq), through); !errors.Is(err, ledger.ErrCorrupt) {
				t.Fatalf("ineligible source bypassed authoritative reader: %v", err)
			}
		})
	}
}

// The kind selects the miss; the stop itself is immediately before its real
// numeric read. Producers on the other side of the barrier must complete.
func stopColdHistory(s *Service, kind historyNeedKind) (<-chan struct{}, func()) {
	arrived, release := make(chan struct{}), make(chan struct{})
	var armed, pending atomic.Bool
	var once sync.Once
	armed.Store(true)
	s.deps.historyResolveError = func(got historyNeedKind) error {
		if got == kind && armed.CompareAndSwap(true, false) {
			pending.Store(true)
		}
		return nil
	}
	s.deps.beforeRangeRead = func(_, _ uint64) {
		if pending.CompareAndSwap(true, false) {
			close(arrived)
			<-release
		}
	}
	return arrived, func() { once.Do(func() { close(release) }) }
}

func testColdCommandCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	b := registerPrivate(t, f.service, 100)
	first := writePrivate(t, f.service, b, 20, 1, Change{Slot: "memory", Mode: "replace", Bytes: []byte("first")})
	damageHistoryCache(f.service, "evicted")
	arrived, release := stopColdHistory(f.service, needCommit)
	defer release()
	type answer struct {
		receipt Receipt
		err     error
	}
	done := make(chan answer, 1)
	go func() {
		r, err := f.service.CommandStatus(ctx, CommandLookup{Binding: b, CommandID: privateID(20), SubmissionSHA256: first.SubmissionSHA256})
		done <- answer{r, err}
	}()
	select {
	case <-arrived:
	case got := <-done:
		t.Fatalf("cold commit did not suspend: %+v %v", got.receipt, got.err)
	}
	second := writePrivate(t, f.service, b, 21, 2, Change{Slot: "memory", Mode: "replace", Bytes: []byte("winner")})
	release()
	got := <-done
	if got.err != nil || !reflect.DeepEqual(got.receipt, first) {
		t.Fatalf("containing commit changed original receipt: %+v %v", got.receipt, got.err)
	}
	if page := readPrivate(t, f.service, b, second.Revision, "memory"); string(page.Bytes) != "winner" || f.store.Durable() != second.Sequence {
		t.Fatalf("late commit read changed the winning head: %q", page.Bytes)
	}
}

func testColdCommandNegative(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	b := registerPrivate(t, f.service, 100)
	writePrivate(t, f.service, b, 20, 1, Change{Slot: "memory", Mode: "replace", Bytes: []byte("before")})
	request := memoryWrite(b, 19, 2, "winner")
	damageHistoryCache(f.service, "disabled")
	arrived, release := stopColdHistory(f.service, needCommand)
	defer release()
	type answer struct {
		receipt Receipt
		err     error
	}
	done := make(chan answer, 1)
	go func() { r, err := f.service.WritePrivate(ctx, request); done <- answer{r, err} }()
	select {
	case <-arrived:
	case got := <-done:
		t.Fatalf("cold negative did not suspend: %+v %v", got.receipt, got.err)
	}
	winner, err := f.service.WritePrivate(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	release()
	got := <-done
	if got.err != nil || !reflect.DeepEqual(got.receipt, winner) {
		t.Fatalf("late negative lost the winning receipt: %+v %v", got.receipt, got.err)
	}
	reopenScoped(t, f, DefaultLimits())
	retry, err := f.service.WritePrivate(ctx, request)
	if err != nil || !reflect.DeepEqual(retry, winner) || f.store.Durable() != winner.Sequence {
		t.Fatalf("late negative changed recovery or repeated the effect: %+v %v", retry, err)
	}
	if page := readPrivate(t, f.service, b, winner.Revision, "memory"); string(page.Bytes) != "winner" {
		t.Fatalf("recovery lost winning bytes: %q", page.Bytes)
	}
}

func testColdGrantHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	b := registerPrivate(t, f.service, 100)
	grant := privateScopedGrant(b, 500, scopedCapacity(), "private.read")
	issueScopedRoot(t, f.service, b, 501, grant)
	if _, err := f.service.RevokeGrant(ctx, b, GrantRevocation{CommandID: privateID(502), RevokeGrantID: grant.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(f.service.p.authority)}); err != nil {
		t.Fatal(err)
	}
	damageHistoryCache(f.service, "evicted")
	arrived, release := stopColdHistory(f.service, needGrant)
	defer release()
	type answer struct {
		view GrantView
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		v, err := f.service.GrantStatus(ctx, GrantLookup{GrantID: grant.GrantID})
		done <- answer{v, err}
	}()
	select {
	case <-arrived:
	case got := <-done:
		t.Fatalf("revoked history did not suspend: %+v %v", got.view, got.err)
	}
	winner := writePrivate(t, f.service, b, 503, 1, Change{Slot: "memory", Mode: "replace", Bytes: []byte("winner")})
	release()
	got := <-done
	if got.err != nil || !got.view.Revoked || got.view.Grant.GrantID != grant.GrantID || got.view.ThroughSequence != winner.Sequence || f.store.Durable() != winner.Sequence {
		t.Fatalf("old grant fold restored authority: %+v %v", got.view, got.err)
	}
}

func testColdTaskHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, request := liveReviewFixture(t)
	if _, err := f.service.TransitionTask(ctx, reviewOwner(), reviewTaskMutation(t, f.service, "cancel")); err != nil {
		t.Fatal(err)
	}
	damageHistoryCache(f.service, "evicted")
	arrived, release := stopColdHistory(f.service, needTask)
	defer release()
	type answer struct {
		view TaskView
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		v, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: request.Binding.TaskID})
		done <- answer{v, err}
	}()
	select {
	case <-arrived:
	case got := <-done:
		t.Fatalf("terminal task history did not suspend: %+v %v", got.view, got.err)
	}
	scopedCreate(t, f.service, reviewOwner(), 2600, 2601, reviewOwner().AgentID)
	through := f.store.Durable()
	release()
	got := <-done
	if got.err != nil || got.view.Task.State != "cancelled" || got.view.Task.Version != request.Binding.TaskVersion+1 || f.store.Durable() != through {
		t.Fatalf("old task fold undid cancellation: %+v %v", got.view, got.err)
	}
}

func testColdReviewClock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, request := liveReviewFixture(t)
	first, err := f.service.RequestReview(ctx, reviewOwner(), request)
	if err != nil {
		t.Fatal(err)
	}
	f.service.mu.Lock()
	before := f.service.reviewSample
	f.service.mu.Unlock()
	var clock atomic.Int64
	clock.Store(before + 20)
	f.service.deps.reviewClock = func() (int64, error) { return clock.Load(), nil }
	request.CommandID, request.ReplacesRequestID, request.RequestID = privateID(2700), request.RequestID, privateID(1099)
	damageHistoryCache(f.service, "evicted")
	arrived, release := stopColdHistory(f.service, needReview)
	defer release()
	done := make(chan error, 1)
	go func() { _, err := f.service.RequestReview(ctx, reviewOwner(), request); done <- err }()
	select {
	case <-arrived:
	case err := <-done:
		t.Fatalf("review head did not suspend after sampling: %v", err)
	}
	clock.Store(before + 10)
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	view, err := f.service.ReviewStatus(ctx, ReviewLookup{RequestID: request.RequestID})
	f.service.mu.Lock()
	after := f.service.reviewSample
	f.service.mu.Unlock()
	if err != nil || view.Request.ObservedUTCNS != before+10 || view.Request.ObservedUTCNS < before || after != view.Request.ObservedUTCNS || view.RequestSequence <= first.Sequence {
		t.Fatalf("suspended clock sample escaped: %+v floor=%d before=%d err=%v", view, after, before, err)
	}
	clock.Store(after + 1)
	request.CommandID, request.ReplacesRequestID, request.RequestID = privateID(2701), request.RequestID, privateID(2702)
	if _, err := f.service.RequestReview(ctx, reviewOwner(), request); err != nil {
		t.Fatal(err)
	}
	later, err := f.service.ReviewStatus(ctx, ReviewLookup{RequestID: request.RequestID})
	if err != nil || later.Request.ObservedUTCNS < after {
		t.Fatalf("later review regressed completed floor: %+v %v", later, err)
	}
}

func testColdSourceRevocation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, binding, agent, request, _ := scopedArtifactFixture(t, serviceDeps{})
	damageHistoryCache(f.service, "evicted")
	arrived, release := stopColdHistory(f.service, needSources)
	defer release()
	type answer struct {
		page ArtifactPage
		err  error
	}
	done := make(chan answer, 1)
	go func() { page, err := agent.ReadArtifact(ctx, request); done <- answer{page, err} }()
	select {
	case <-arrived:
	case got := <-done:
		t.Fatalf("source selection did not suspend: %+v %v", got.page, got.err)
	}
	f.service.mu.Lock()
	authority := f.service.p.authority
	f.service.mu.Unlock()
	if _, err := f.service.RevokeGrant(ctx, binding, GrantRevocation{CommandID: privateID(401), RevokeGrantID: request.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(authority)}); err != nil {
		t.Fatal(err)
	}
	before := f.store.Durable()
	release()
	got := <-done
	if !errors.Is(got.err, ErrDenied) || !reflect.DeepEqual(got.page, ArtifactPage{}) || f.store.Durable() != before {
		t.Fatalf("old source authorized a revoked disclosure: %+v %v", got.page, got.err)
	}
	book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
	if err != nil || book.Held != (Exposure{}) || book.Used.LocalUnits != 0 {
		t.Fatalf("suspended source staged an effect or hold: %+v %v", book, err)
	}
}

func testColdPaidReadError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPaidFixture(t)
	admission := f.reserve(t)
	f.dispatch(t, admission)
	f.append(t, f.result(t, admission, 10, 2))
	f.service.mu.Lock()
	f.service.p.paidHotLimit = 0
	f.service.p.prunePaid()
	f.service.mu.Unlock()
	damageHistoryCache(f.service, "disabled")
	path := filepath.Join(f.store.LogDir(), ledger.SegmentName(1))
	original := readTestFile(t, path)
	damaged := bytes.Clone(original)
	damaged[0] ^= 1
	writeTestFile(t, path, damaged)
	f.service.mu.Lock()
	_, _, lockedErr := f.service.loadPaid(ctx, nil, admission.AttemptID, f.service.p.through, ledger.Digest{})
	lockedPoison := f.service.poisoned
	f.service.mu.Unlock()
	writeTestFile(t, path, original)
	if !errors.Is(lockedErr, ledger.ErrCorrupt) {
		t.Fatalf("locked corruption oracle: %v", lockedErr)
	}
	f.service.deps.historyResolveError = func(kind historyNeedKind) error {
		if kind == needPaid {
			return lockedErr
		}
		return nil
	}
	before := f.store.Durable()
	view, err := f.service.PaidAttemptStatus(ctx, PaidAttemptLookup{AttemptID: admission.AttemptID})
	f.service.mu.Lock()
	poisoned := f.service.poisoned
	f.service.mu.Unlock()
	if err != lockedErr || !errors.Is(err, ledger.ErrCorrupt) || poisoned != lockedPoison || !reflect.DeepEqual(view, PaidAttemptView{}) || f.store.Durable() != before {
		t.Fatalf("unlocked failure changed original error or poison state: %v poison=%v locked=%v", err, poisoned, lockedPoison)
	}
}

func testColdWorkHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	binding := registerPrivate(t, f.service, 100)
	scopedEnrol(t, f.service, binding, scopedCapacity(), 300, binding.AgentID)
	grant := privateScopedGrant(binding, 500, scopedCapacity(), "turn.start")
	issueScopedRoot(t, f.service, binding, 501, grant)
	agent := scopedBound(t, f.service, binding)
	start := TurnAdmission{CommandID: privateID(502), TurnID: privateID(503), GrantID: grant.GrantID}
	if _, err := agent.StartTurn(ctx, start); err != nil {
		t.Fatal(err)
	}
	ending, err := f.service.EndTurn(ctx, TurnEnding{CommandID: privateID(504), TurnID: start.TurnID, ExpectedVersion: 1, Outcome: "released"})
	if err != nil {
		t.Fatal(err)
	}
	damageHistoryCache(f.service, "evicted")
	arrived, release := stopColdHistory(f.service, needWork)
	defer release()
	type answer struct {
		view WorkView
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		view, err := f.service.TurnStatus(ctx, WorkLookup{WorkID: start.TurnID})
		done <- answer{view, err}
	}()
	select {
	case <-arrived:
	case got := <-done:
		t.Fatalf("ended work did not suspend: %+v %v", got.view, got.err)
	}
	winner := writePrivate(t, f.service, binding, 505, 1, Change{Slot: "memory", Mode: "replace", Bytes: []byte("winner")})
	release()
	got := <-done
	if got.err != nil || !got.view.Ended || got.view.Outcome != "released" || got.view.Version != ending.Version || got.view.Admission.WorkID != start.TurnID || f.store.Durable() != winner.Sequence {
		t.Fatalf("cold work fold changed ended admission: %+v %v", got.view, got.err)
	}
	reopenScoped(t, f, DefaultLimits())
	retry, err := f.service.ReconcileAdmissions(ctx, EndingRequest{CommandID: privateID(506), WorkID: start.TurnID, ExpectedVersion: uint64(ending.Version)})
	if err != nil || !reflect.DeepEqual(retry, ending) || f.store.Durable() != winner.Sequence {
		t.Fatalf("historical work recovery repeated release: %+v %v", retry, err)
	}
}

func testColdAssignment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	owner := registerPrivate(t, f.service, 100)
	scopedEnrol(t, f.service, owner, scopedCapacity(), 300, owner.AgentID)
	hi := scopedWorkContext(t, f.service, owner, 110, 111)
	lo := scopedWorkContext(t, f.service, owner, 120, 121)
	other := scopedWorkContext(t, f.service, owner, 130, 131)
	first := scopedNewTask(1000, 900, uuid.UUID{}, owner.AgentID)
	first.Assignment = &ledger.AssignmentInput{AssignmentID: privateID(900), Assignee: ledger.Binding(hi)}
	if _, err := f.service.CreateTask(ctx, owner, first); err != nil {
		t.Fatal(err)
	}
	request := scopedNewTask(1020, 1010, uuid.UUID{}, owner.AgentID)
	request.Assignment = &ledger.AssignmentInput{AssignmentID: privateID(400), Assignee: ledger.Binding(lo)}
	arrived, release := stopColdHistory(f.service, needAssignment)
	defer release()
	done := make(chan error, 1)
	go func() { _, err := f.service.CreateTask(ctx, owner, request); done <- err }()
	select {
	case <-arrived:
	case err := <-done:
		t.Fatalf("assignment proof did not suspend: %v", err)
	}
	winner := scopedNewTask(1040, 1030, uuid.UUID{}, owner.AgentID)
	winner.Assignment = &ledger.AssignmentInput{AssignmentID: privateID(400), Assignee: ledger.Binding(other)}
	receipt, err := f.service.CreateTask(ctx, owner, winner)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-done; !errors.Is(err, ErrAssignment) || f.store.Durable() != receipt.Sequence {
		t.Fatalf("old negative reused an assignment: %v", err)
	}
	if _, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: request.TaskID}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("refused assignment installed a task: %v", err)
	}
}

func testColdWorkspace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, agent, start, request := scopedLocalFixture(t, DefaultLimits())
	if _, err := agent.StartTurn(ctx, start); err != nil {
		t.Fatal(err)
	}
	damageHistoryCache(f.service, "evicted")
	arrived, release := stopColdHistory(f.service, needWorkspace)
	defer release()
	type answer struct {
		receipt ScopedReceipt
		err     error
	}
	done := make(chan answer, 1)
	go func() { receipt, err := agent.AdmitLocal(ctx, request); done <- answer{receipt, err} }()
	select {
	case <-arrived:
	case got := <-done:
		t.Fatalf("workspace readiness did not suspend: %+v %v", got.receipt, got.err)
	}
	registerPrivate(t, f.service, 10000)
	before := f.store.Durable()
	release()
	got := <-done
	if got.err != nil || got.receipt.Sequence <= before || f.store.Durable() != got.receipt.Sequence {
		t.Fatalf("cold workspace admission failed or repeated: %+v %v", got.receipt, got.err)
	}
	view, err := f.service.TurnStatus(ctx, WorkLookup{WorkID: request.WorkID})
	if err != nil || view.Ended || view.Dispatched || view.Version != 1 || view.Admission.WorkID != request.WorkID {
		t.Fatalf("cold workspace selected a different admission: %+v %v", view, err)
	}
	book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
	if err != nil || book.Held.LocalSlots != 1 || book.Held.TurnSlots != 1 {
		t.Fatalf("cold workspace holds: %+v %v", book, err)
	}
	ending, err := f.service.EndLocal(ctx, LocalEnding{CommandID: privateID(10010), WorkID: request.WorkID, ExpectedVersion: 1, Outcome: "released_unsent"})
	if err != nil {
		t.Fatal(err)
	}
	damageHistoryCache(f.service, "evicted")
	recovered, err := f.service.ReconcileAdmissions(ctx, EndingRequest{CommandID: privateID(10011), WorkID: request.WorkID, ExpectedVersion: uint64(ending.Version)})
	if err != nil || !reflect.DeepEqual(recovered, ending) || f.store.Durable() != ending.Sequence {
		t.Fatalf("cold ended-work receipt lost its workspace target: %+v %v", recovered, err)
	}
}

func testColdContextTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	owner := registerPrivate(t, f.service, 100)
	work := scopedWorkContext(t, f.service, owner, 130, 131)
	scopedEnrol(t, f.service, owner, scopedCapacity(), 300, owner.AgentID)
	grant := privateScopedGrant(work, 510, scopedCapacity(), "turn.start")
	issueScopedRoot(t, f.service, owner, 511, grant)
	damageContextAssociations(f.service, "missing")
	agent := scopedBound(t, f.service, work)
	request := TurnAdmission{CommandID: privateID(710), TurnID: privateID(711), GrantID: grant.GrantID}
	arrived, release := stopColdHistory(f.service, needContextTask)
	defer release()
	type answer struct {
		receipt ScopedReceipt
		err     error
	}
	done := make(chan answer, 1)
	go func() { receipt, err := agent.StartTurn(ctx, request); done <- answer{receipt, err} }()
	select {
	case <-arrived:
	case got := <-done:
		t.Fatalf("context association did not suspend: %+v %v", got.receipt, got.err)
	}
	registerPrivate(t, f.service, 2000)
	release()
	got := <-done
	if got.err != nil || f.store.Durable() != got.receipt.Sequence {
		t.Fatalf("cold unassigned turn: %+v %v", got.receipt, got.err)
	}
	view, err := f.service.TurnStatus(ctx, WorkLookup{WorkID: request.TurnID})
	if err != nil || view.Ended || view.Version != 1 || view.Admission.TaskBinding != nil {
		t.Fatalf("unassigned context acquired a task: %+v %v", view, err)
	}
	before := f.store.Durable()
	retry, err := agent.StartTurn(ctx, request)
	if err != nil || !reflect.DeepEqual(retry, got.receipt) || f.store.Durable() != before {
		t.Fatalf("context rerun repeated its admission: %+v %v", retry, err)
	}
}

func testColdScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, binding, agent, grant := scopedPrivateFixture(t, serviceDeps{}, scopedCapacity())
	writePrivate(t, f.service, binding, 300, 1, Change{Slot: "transcript", Mode: "append", Bytes: []byte("first")})
	writePrivate(t, f.service, binding, 301, 2, Change{Slot: "transcript", Mode: "append", Bytes: []byte("second")})
	request := ScopedPrivateRead{CommandID: privateID(302), GrantID: grant.GrantID, Scope: "context", Slot: "transcript", Revision: 3, MaxBytes: 64}
	arrived, release := stopColdHistory(f.service, needScope)
	defer release()
	type answer struct {
		page ScopedPrivatePage
		err  error
	}
	done := make(chan answer, 1)
	go func() { page, err := agent.ReadPrivate(ctx, request); done <- answer{page, err} }()
	select {
	case <-arrived:
	case got := <-done:
		t.Fatalf("transcript validation did not suspend: %+v %v", got.page, got.err)
	}
	// The read owns its scope gate; the independent writer uses another scope.
	registerPrivate(t, f.service, 2000)
	release()
	got := <-done
	if got.err != nil || len(got.page.Page.Chunks) != 2 || string(got.page.Page.Chunks[0].Bytes) != "first" || string(got.page.Page.Chunks[1].Bytes) != "second" || got.page.Page.NextCursor != 0 {
		t.Fatalf("cold transcript selection changed: %+v %v", got.page, got.err)
	}
	before := f.store.Durable()
	retry, err := agent.ReadPrivate(ctx, request)
	if err != nil || !reflect.DeepEqual(retry, got.page) || f.store.Durable() != before {
		t.Fatalf("cold transcript receipt repeated selection: %+v %v", retry, err)
	}
	book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
	if err != nil || book.Used.LocalUnits != 1 || book.Held != (Exposure{}) {
		t.Fatalf("cold transcript accounting: %+v %v", book, err)
	}
}

func testColdRegistration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	owner := registerPrivate(t, f.service, 100)
	request := registrationRequest(owner, 300, 400, "cold")
	if _, err := f.service.SetResourceRegistration(ctx, owner, request); err != nil {
		t.Fatal(err)
	}
	lookup := RegistrationVersionLookup{Ref: ledger.RegistrationRef{Kind: "resource", ID: request.ResourceID, Version: 1}, ThroughSequence: ledger.ToEnd}
	want, err := f.service.RegistrationVersion(ctx, lookup)
	if err != nil {
		t.Fatal(err)
	}
	request.CommandID, request.ExpectedVersion = privateID(340), 1
	if _, err := f.service.SetResourceRegistration(ctx, owner, request); err != nil {
		t.Fatal(err)
	}
	arrived, release := stopColdHistory(f.service, needRegistration)
	defer release()
	type answer struct {
		view RegistrationEvidence
		err  error
	}
	done := make(chan answer, 1)
	go func() { view, err := f.service.RegistrationVersion(ctx, lookup); done <- answer{view, err} }()
	select {
	case <-arrived:
	case got := <-done:
		t.Fatalf("historical registration did not suspend: %+v %v", got.view, got.err)
	}
	request.CommandID, request.ExpectedVersion = privateID(341), 2
	winner, err := f.service.SetResourceRegistration(ctx, owner, request)
	if err != nil {
		t.Fatal(err)
	}
	release()
	got := <-done
	if got.err != nil || got.view.Resource == nil || got.view.Resource.ThroughSequence != winner.Sequence || !reflect.DeepEqual(got.view.Resource.Record, want.Resource.Record) || got.view.Resource.EffectSequence != want.Resource.EffectSequence || f.store.Durable() != winner.Sequence {
		t.Fatalf("historical version changed across current revision: %+v %v", got.view, got.err)
	}
}

func testColdBudget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _, agent, grant := scopedPrivateFixture(t, serviceDeps{}, scopedCapacity())
	arrived, release := stopColdHistory(f.service, needBudget)
	defer release()
	type answer struct {
		view CapacityView
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		view, err := agent.CapacityStatus(ctx, GrantLookup{GrantID: grant.GrantID})
		done <- answer{view, err}
	}()
	select {
	case <-arrived:
	case got := <-done:
		t.Fatalf("origin budget did not suspend: %+v %v", got.view, got.err)
	}
	winner, err := agent.WritePrivate(ctx, ScopedPrivateWrite{CommandID: privateID(300), GrantID: grant.GrantID, Scope: "context", ExpectedVersion: 1, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("winner")}}})
	if err != nil {
		t.Fatal(err)
	}
	release()
	got := <-done
	if got.err != nil || got.view.Used.LocalUnits != 1 || got.view.Held != (Exposure{}) || got.view.ThroughSequence != winner.Sequence || f.store.Durable() != winner.Sequence {
		t.Fatalf("origin fold reused pre-write accounting: %+v %v", got.view, got.err)
	}
}

func testColdReadFence(t *testing.T, fence string) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newServiceFixture(t, serviceDeps{})
	binding := registerPrivate(t, f.service, 100)
	receipt := writePrivate(t, f.service, binding, 200, 1, Change{Slot: "memory", Mode: "replace", Bytes: []byte("committed")})
	damageHistoryCache(f.service, "evicted")
	arrived, release := stopColdHistory(f.service, needCommit)
	defer release()
	type answer struct {
		receipt Receipt
		err     error
	}
	done := make(chan answer, 1)
	go func() {
		view, err := f.service.CommandStatus(ctx, CommandLookup{Binding: binding, CommandID: receipt.CommandID, SubmissionSHA256: receipt.SubmissionSHA256})
		done <- answer{view, err}
	}()
	select {
	case <-arrived:
	case got := <-done:
		t.Fatalf("cold fenced read did not suspend: %+v %v", got.receipt, got.err)
	}
	want := error(context.Canceled)
	closed := make(chan error, 1)
	if fence == "close" {
		want = ErrClosed
		go func() { closed <- f.service.Close() }()
		<-f.service.closeSignal
	} else {
		cancel()
	}
	release()
	got := <-done
	if !errors.Is(got.err, want) || !reflect.DeepEqual(got.receipt, Receipt{}) || f.store.Durable() != receipt.Sequence {
		t.Fatalf("cold read crossed its usability fence: %+v %v", got.receipt, got.err)
	}
	if fence == "close" {
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
	}
}

func testColdLargerRefreshWaiter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	arrived, resume, waiting := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var armed atomic.Bool
	var releaseOnce, waitOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resume) }) }
	defer release()
	f := newServiceFixture(t, serviceDeps{
		beforeRefreshInstall: func(ledger.Commit) {
			if armed.CompareAndSwap(true, false) {
				close(arrived)
				<-resume
			}
		},
		beforeRefreshWait: func() { waitOnce.Do(func() { close(waiting) }) },
	})
	binding := registerPrivate(t, f.service, 100)
	writer, _, err := Open(ctx, f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writePrivate(t, writer, binding, 200, 1, Change{Slot: "memory", Mode: "replace", Bytes: []byte("first")})
	type answer struct {
		view Snapshot
		err  error
	}
	read := func() <-chan answer {
		done := make(chan answer, 1)
		go func() {
			view, err := f.service.Snapshot(ctx, ReadBinding{Binding: binding, Scope: "context"})
			done <- answer{view, err}
		}()
		return done
	}
	armed.Store(true)
	first := read()
	select {
	case <-arrived:
	case got := <-first:
		t.Fatalf("first refresh did not stop: %+v %v", got.view, got.err)
	}
	winner := writePrivate(t, writer, binding, 201, 2, Change{Slot: "memory", Mode: "replace", Bytes: []byte("later")})
	later := read()
	select {
	case <-waiting:
	case got := <-later:
		t.Fatalf("larger-prefix reader did not join the flight: %+v %v", got.view, got.err)
	}
	// Unlike the acknowledgement-overlap row, no other caller installs the
	// later prefix. This waiter must become the next refresh performer.
	release()
	if got := <-first; got.err != nil {
		t.Fatal(got.err)
	}
	got := <-later
	if got.err != nil || got.view.Revision != 3 || got.view.ThroughSequence != winner.Sequence || f.store.Durable() != winner.Sequence {
		t.Fatalf("larger waiter stopped at the earlier flight's frontier: %+v %v", got.view, got.err)
	}
	if page := readPrivate(t, f.service, binding, 3, "memory"); string(page.Bytes) != "later" {
		t.Fatal("larger refresh waiter did not select the later committed bytes")
	}
}

func testColdNegativeScopeConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := func() (*serviceFixture, PrivateWrite) {
		t.Helper()
		f := newServiceFixture(t, serviceDeps{})
		binding := registerPrivate(t, f.service, 100)
		writePrivate(t, f.service, binding, 20, 1, Change{Slot: "memory", Mode: "replace", Bytes: []byte("before")})
		return f, memoryWrite(binding, 19, 2, "candidate")
	}
	sequential, prior := fixture()
	priorWinner := prior
	priorWinner.Scope, priorWinner.ExpectedVersion = "agent", 1
	if _, err := sequential.service.WritePrivate(ctx, priorWinner); err != nil {
		t.Fatal(err)
	}
	_, wantErr := sequential.service.WritePrivate(ctx, prior)
	if !errors.Is(wantErr, ErrCommandMismatch) {
		t.Fatalf("sequential conflict oracle: %v", wantErr)
	}
	f, request := fixture()
	damageHistoryCache(f.service, "disabled")
	arrived, release := stopColdHistory(f.service, needCommand)
	defer release()
	done := make(chan error, 1)
	go func() { _, err := f.service.WritePrivate(ctx, request); done <- err }()
	select {
	case <-arrived:
	case err := <-done:
		t.Fatalf("absent command did not suspend: %v", err)
	}
	winnerRequest := request
	winnerRequest.Scope, winnerRequest.ExpectedVersion = "agent", 1
	winner, err := f.service.WritePrivate(ctx, winnerRequest)
	if err != nil {
		t.Fatal(err)
	}
	release()
	err = <-done
	f.service.mu.Lock()
	poisoned := f.service.poisoned
	f.service.mu.Unlock()
	if err != wantErr || poisoned || f.store.Durable() != winner.Sequence {
		t.Fatalf("late negative changed the sequential conflict: %v poisoned=%v durable=%d want=%d", err, poisoned, f.store.Durable(), winner.Sequence)
	}
	reopenScoped(t, f, DefaultLimits())
	if _, err := f.service.WritePrivate(ctx, request); err != wantErr || f.store.Durable() != winner.Sequence {
		t.Fatalf("late negative changed conflict after recovery: %v", err)
	}
	if page := readPrivate(t, f.service, request.Binding, 2, "memory"); string(page.Bytes) != "before" {
		t.Fatal("refused context write changed private bytes")
	}
}

func testColdForeignUsageRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, binding, _, grant := scopedPrivateFixture(t, serviceDeps{}, scopedCapacity())
	writer, _, err := Open(ctx, f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	agent := scopedBound(t, writer, binding)
	request := ScopedPrivateWrite{CommandID: privateID(199), GrantID: grant.GrantID, Scope: "context", ExpectedVersion: 1, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("foreign")}}}
	receipt, err := agent.WritePrivate(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	damageHistoryCache(f.service, "disabled")
	arrived, release := stopColdHistory(f.service, needCommand)
	defer release()
	lookup := CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}}
	type answer struct {
		view CapacityView
		err  error
	}
	done := make(chan answer, 1)
	go func() { view, err := f.service.CapacityStatus(ctx, lookup); done <- answer{view, err} }()
	select {
	case <-arrived:
	case got := <-done:
		t.Fatalf("foreign acceptance did not suspend: %+v %v", got.view, got.err)
	}
	release()
	live := <-done
	if live.err != nil || live.view.Used.LocalUnits != 1 || live.view.Held != (Exposure{}) || live.view.ThroughSequence != receipt.Sequence || f.store.Durable() != receipt.Sequence {
		t.Fatalf("staged foreign usage was not installed exactly once: %+v %v", live.view, live.err)
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, reconciliation, err := Open(ctx, f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	f.service = reopened
	view, err := reopened.CapacityStatus(ctx, lookup)
	if err != nil || !reflect.DeepEqual(view, live.view) || len(reconciliation.Issues) != 0 || len(reconciliation.PendingWork) != 0 || len(reconciliation.PendingPaid) != 0 || len(reconciliation.PendingLoops) != 0 || f.store.Durable() != receipt.Sequence {
		t.Fatalf("fresh replay differs from staged live accounting: %+v %v reconciliation=%+v", view, err, reconciliation)
	}
	agent = scopedBound(t, reopened, binding)
	retry, err := agent.WritePrivate(ctx, request)
	if err != nil || !reflect.DeepEqual(retry, receipt) || f.store.Durable() != receipt.Sequence {
		t.Fatalf("recovery repeated the foreign effect: %+v %v", retry, err)
	}
	if page := readPrivate(t, reopened, binding, 2, "memory"); string(page.Bytes) != "foreign" {
		t.Fatal("staged installation changed the committed effect")
	}
}
