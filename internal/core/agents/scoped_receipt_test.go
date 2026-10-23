package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func TestScopedReceiptsAndLostAck(t *testing.T) {
	t.Parallel()
	t.Run("restart-full-capacity-lower-limits-and-current-authority", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		limits := DefaultLimits()
		limits.MaxLiveTasks = 1
		f := newServiceFixtureWithLimits(t, serviceDeps{}, limits)
		origin, other := registerPrivate(t, f.service, 100), registerPrivate(t, f.service, 200)
		capacity := scopedCapacity()
		capacity.LocalUnits = 1
		scopedEnrol(t, f.service, origin, capacity, 300, origin.AgentID, other.AgentID)
		root := GrantSpec{GrantID: privateID(500), Grantee: origin, Operations: []string{"task.create"}, Resources: []ledger.Resource{{Kind: "task"}}, DataClass: "task", Recipients: []Binding{origin}, Limits: capacity, Lifetime: "until_revoked"}
		scopedRoot(t, f.service, origin, 0, 501, root)
		otherRoot := root
		otherRoot.GrantID, otherRoot.Grantee = privateID(510), other
		scopedRoot(t, f.service, origin, 1, 511, otherRoot)
		request := scopedNewTask(600, 400, root.GrantID, origin.AgentID)
		lost := errors.New("withheld scoped acknowledgement")
		var committed ScopedReceipt
		f.service.deps.afterScopedCommit = func(receipt ScopedReceipt) error {
			if receipt.CommandID == request.CommandID && committed.CommandID == (uuid.UUID{}) {
				committed = receipt
				return lost
			}
			return nil
		}
		agent := scopedBound(t, f.service, origin)
		if _, err := agent.CreateTask(ctx, request); !errors.Is(err, lost) {
			t.Fatalf("lost acknowledgement seam: %v", err)
		}
		if _, err := f.service.TransitionTask(ctx, origin, TaskMutation{CommandID: privateID(601), TaskID: request.TaskID, ExpectedVersion: 1, Action: "cancel"}); err != nil {
			t.Fatal(err)
		}
		filler := scopedNewTask(701, 700, uuid.UUID{}, origin.AgentID)
		if _, err := f.service.CreateTask(ctx, origin, filler); err != nil {
			t.Fatal(err)
		}
		limits.MaxTaskDescriptionBytes = 1
		reopenScoped(t, f, limits)
		agent = scopedBound(t, f.service, origin)
		otherAgent := scopedBound(t, f.service, other)
		before := f.store.Durable()
		replayed, err := agent.CreateTask(ctx, request)
		if err != nil || !reflect.DeepEqual(replayed, committed) || f.store.Durable() != before {
			t.Fatalf("retry checked current CAS/live/capacity/description limits before the ledger receipt: %+v %+v %v", replayed, committed, err)
		}
		view, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: request.TaskID})
		if err != nil || view.Task.State != "cancelled" || view.Task.Version != 2 {
			t.Fatalf("retry repeated the effect or returned current state as its receipt: %+v %v", view, err)
		}
		book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
		if err != nil || book.Used.LocalUnits != 1 || book.Held != (Exposure{}) {
			t.Fatalf("lost acknowledgement charged twice: %+v %v", book, err)
		}
		lookup := ScopedCommandLookup{CommandID: request.CommandID, GrantID: root.GrantID, SubmissionSHA256: committed.SubmissionSHA256}
		status, err := agent.ScopedCommandStatus(ctx, lookup)
		if err != nil || !reflect.DeepEqual(status, committed) || f.store.Durable() != before {
			t.Fatalf("passive own receipt at full capacity: %+v %v", status, err)
		}
		changedBody := request
		changedBody.Description = []byte("different description")
		changedAcceptance := request
		changedAcceptance.AcceptanceSHA256[0] ^= 1
		for _, changed := range []TaskCreation{changedBody, changedAcceptance} {
			if _, err := agent.CreateTask(ctx, changed); !errors.Is(err, ErrCommandMismatch) || f.store.Durable() != before {
				t.Fatalf("semantic input omitted from digest: %v", err)
			}
		}
		ownerAttempt := request
		ownerAttempt.GrantID = uuid.UUID{}
		if _, err := f.service.CreateTask(ctx, origin, ownerAttempt); err != ErrDenied {
			t.Fatalf("owner/agent receipt principal collision: %v", err)
		}
		foreignAttempt := request
		foreignAttempt.GrantID = otherRoot.GrantID
		if _, err := otherAgent.CreateTask(ctx, foreignAttempt); err != ErrDenied {
			t.Fatalf("different origin claimed receipt: %v", err)
		}
		for _, id := range []uuid.UUID{request.CommandID, privateID(999)} {
			foreignLookup := lookup
			foreignLookup.CommandID, foreignLookup.GrantID = id, otherRoot.GrantID
			if _, err := otherAgent.ScopedCommandStatus(ctx, foreignLookup); err != ErrDenied {
				t.Fatalf("foreign/absent receipt oracle: %v", err)
			}
		}
		privateCollision := request
		privateCollision.CommandID = privateID(103)
		if _, err := agent.CreateTask(ctx, privateCollision); !errors.Is(err, ErrCommandMismatch) {
			t.Fatalf("owner-private receipt matched scoped command: %v", err)
		}
		privateLookup := lookup
		privateLookup.CommandID = privateID(103)
		if _, err := agent.ScopedCommandStatus(ctx, privateLookup); err != ErrDenied {
			t.Fatalf("private receipt disclosed by scoped query: %v", err)
		}
		wrongDigest := lookup
		wrongDigest.SubmissionSHA256[0] ^= 1
		if _, err := agent.ScopedCommandStatus(ctx, wrongDigest); !errors.Is(err, ErrCommandMismatch) {
			t.Fatalf("receipt query ignored digest: %v", err)
		}
		if f.store.Durable() != before {
			t.Fatal("receipt refusals appended records")
		}
		if _, err := f.service.RevokeGrant(ctx, origin, GrantRevocation{CommandID: privateID(800), RevokeGrantID: root.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: 2}); err != nil {
			t.Fatal(err)
		}
		if _, err := agent.CreateTask(ctx, request); err != ErrDenied {
			t.Fatalf("prior receipt became permission after revocation: %v", err)
		}
		if _, err := agent.ScopedCommandStatus(ctx, lookup); err != ErrDenied {
			t.Fatalf("revoked authority disclosed receipt: %v", err)
		}
		replacement := root
		replacement.GrantID = privateID(520)
		scopedRoot(t, f.service, origin, 3, 521, replacement)
		lookup.GrantID = replacement.GrantID
		status, err = agent.ScopedCommandStatus(ctx, lookup)
		if err != nil || !reflect.DeepEqual(status, committed) {
			t.Fatalf("current replacement authority rewrote the original receipt: %+v %v", status, err)
		}
		request.GrantID = replacement.GrantID
		before = f.store.Durable()
		if _, err := agent.CreateTask(ctx, request); !errors.Is(err, ErrCommandMismatch) || f.store.Durable() != before {
			t.Fatalf("different grant omitted from command identity: %v", err)
		}
	})
	t.Run("concurrent-same-command-and-query-tail", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newServiceFixture(t, serviceDeps{})
		origin := registerPrivate(t, f.service, 100)
		scopedEnrol(t, f.service, origin, scopedCapacity(), 300, origin.AgentID)
		root := GrantSpec{GrantID: privateID(500), Grantee: origin, Operations: []string{"task.create"}, Resources: []ledger.Resource{{Kind: "task"}}, DataClass: "task", Recipients: []Binding{origin}, Limits: scopedCapacity(), Lifetime: "until_revoked"}
		scopedRoot(t, f.service, origin, 0, 501, root)
		agent := scopedBound(t, f.service, origin)
		request := scopedNewTask(600, 400, root.GrantID, origin.AgentID)
		entered, proceed := make(chan struct{}, 2), make(chan struct{})
		var published atomic.Int32
		var release sync.Once
		var writers sync.WaitGroup
		f.service.deps.beforePublish = func(_ Binding, scope string) error {
			if scope == "task" && published.Add(1) <= 2 {
				entered <- struct{}{}
				<-proceed
			}
			return nil
		}
		t.Cleanup(func() { release.Do(func() { close(proceed) }); writers.Wait() })
		type result struct {
			receipt ScopedReceipt
			err     error
		}
		results := make(chan result, 2)
		for range 2 {
			writers.Go(func() { receipt, err := agent.CreateTask(ctx, request); results <- result{receipt, err} })
		}
		<-entered
		<-entered
		prepared, err := agent.CapacityStatus(ctx, GrantLookup{GrantID: root.GrantID})
		if err != nil || prepared.Held.LocalSlots != 2 || prepared.Held.LocalUnits != 2 || prepared.Held.BufferBytes != 2*int64(len(request.Description)) {
			t.Fatalf("concurrent retained bodies escaped reservations: %+v %v", prepared, err)
		}
		release.Do(func() { close(proceed) })
		first, second := <-results, <-results
		writers.Wait()
		if first.err != nil || second.err != nil || !reflect.DeepEqual(first.receipt, second.receipt) {
			t.Fatalf("same-command race: %+v %+v", first, second)
		}
		book, err := agent.CapacityStatus(ctx, GrantLookup{GrantID: root.GrantID})
		if err != nil || book.Used.LocalUnits != 1 || book.Held != (Exposure{}) {
			t.Fatalf("concurrent duplicate charged twice or leaked preparation: %+v %v", book, err)
		}
		future := scopedNewTask(610, 410, root.GrantID, origin.AgentID)
		originRecord := ledger.Origin{AgentID: origin.AgentID, SessionID: privateID(101), ContextID: origin.ContextID}
		semantic := ledger.ScopedSubmission{Schema: 2, Actor: "agent", Origin: originRecord, Operation: "task.create", Target: ledger.Resource{Kind: "task", TaskID: future.TaskID}, GrantID: root.GrantID, Expected: ledger.ScopedExpected{Version: 0}, Arguments: ledger.TaskCreateArguments{TaskID: future.TaskID, Description: ledger.ObjectRef{SHA256: ledger.Digest(sha256.Sum256(future.Description)), Bytes: int64(len(future.Description))}, AcceptanceSHA256: future.AcceptanceSHA256, Dependencies: []uuid.UUID{}, Audience: future.Audience}}
		digest := ledger.Digest(sha256.Sum256(testPayload(t, semantic)))
		lookupEntered, lookupProceed := make(chan struct{}), make(chan struct{})
		var lookups atomic.Int32
		var releaseLookup sync.Once
		var queries sync.WaitGroup
		f.service.deps.beforeLookup = func() {
			if lookups.Add(1) == 1 {
				close(lookupEntered)
				<-lookupProceed
			}
		}
		t.Cleanup(func() { releaseLookup.Do(func() { close(lookupProceed) }); queries.Wait() })
		queryResult := make(chan result, 1)
		queries.Go(func() {
			receipt, err := agent.ScopedCommandStatus(ctx, ScopedCommandLookup{CommandID: future.CommandID, GrantID: root.GrantID, SubmissionSHA256: digest})
			queryResult <- result{receipt, err}
		})
		<-lookupEntered
		written, err := agent.CreateTask(ctx, future)
		if err != nil {
			t.Fatal(err)
		}
		before := f.store.Durable()
		releaseLookup.Do(func() { close(lookupProceed) })
		queried := <-queryResult
		queries.Wait()
		if queried.err != nil || !reflect.DeepEqual(queried.receipt, written) || f.store.Durable() != before {
			t.Fatalf("query trusted an authoritative-prefix miss without checking the new tail: %+v %v", queried, written)
		}
	})
	t.Run("borrowed-description-changes-before-reservation-copy", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newServiceFixture(t, serviceDeps{})
		origin := registerPrivate(t, f.service, 100)
		capacity := scopedCapacity()
		capacity.LocalUnits = 1
		scopedEnrol(t, f.service, origin, capacity, 300, origin.AgentID)
		root := GrantSpec{GrantID: privateID(500), Grantee: origin, Operations: []string{"task.create"}, Resources: []ledger.Resource{{Kind: "task"}}, DataClass: "task", Recipients: []Binding{origin}, Limits: capacity, Lifetime: "until_revoked"}
		scopedRoot(t, f.service, origin, 0, 501, root)
		agent := scopedBound(t, f.service, origin)
		request := scopedNewTask(600, 400, root.GrantID, origin.AgentID)
		original := ledger.Digest(sha256.Sum256(request.Description))
		var changed atomic.Bool
		f.service.deps.beforeLookup = func() {
			if changed.CompareAndSwap(false, true) {
				request.Description[0] ^= 1
			}
		}
		before := f.store.Durable()
		if _, err := agent.CreateTask(ctx, request); !errors.Is(err, ErrInvalid) || f.store.Durable() != before {
			t.Fatalf("changed borrowed bytes committed under the earlier semantic hash: %v", err)
		}
		mutated := ledger.Digest(sha256.Sum256(request.Description))
		for _, digest := range []ledger.Digest{original, mutated} {
			if _, err := f.store.ReadBody(ctx, digest); !errors.Is(err, ledgerstore.ErrBodyMissing) {
				t.Fatalf("unverified retained bytes were published: %v", err)
			}
		}
		book, err := agent.CapacityStatus(ctx, GrantLookup{GrantID: root.GrantID})
		if err != nil || book.Held != (Exposure{}) || book.Used != (Exposure{}) {
			t.Fatalf("refused copy leaked exposure: %+v %v", book, err)
		}
		if _, err := agent.CreateTask(ctx, request); err != nil {
			t.Fatalf("failed preparation retained command identity or allowance: %v", err)
		}
		body, err := f.store.ReadBody(ctx, mutated)
		if err != nil || !bytes.Equal(body, request.Description) {
			t.Fatalf("retry failed to publish verified new bytes: %q %v", body, err)
		}
	})
}
