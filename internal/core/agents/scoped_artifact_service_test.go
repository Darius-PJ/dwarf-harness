package agents

import (
	"context"
	"crypto/sha256"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func scopedArtifactFixture(t *testing.T, deps serviceDeps) (*serviceFixture, Binding, *Agent, ArtifactRead, []byte) {
	t.Helper()
	ctx := context.Background()
	f := newServiceFixture(t, deps)
	binding := registerPrivate(t, f.service, 10)
	capacity := scopedCapacity()
	capacity.BufferBytes = artifactReadBufferBytes
	scopedEnrol(t, f.service, binding, capacity, 100, binding.AgentID)
	body := []byte("verified artifact\n")
	if _, err := f.service.CreateTask(ctx, binding, TaskCreation{CommandID: privateID(200), TaskID: privateID(201), Description: body, AcceptanceSHA256: ledger.Digest(sha256.Sum256([]byte("acceptance"))), Audience: []uuid.UUID{binding.AgentID}}); err != nil {
		t.Fatal(err)
	}
	view, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: privateID(201)})
	if err != nil {
		t.Fatal(err)
	}
	spec := GrantSpec{GrantID: privateID(300), Grantee: binding, Operations: []string{"artifact.read"}, Resources: []ledger.Resource{{Kind: "artifact", Artifact: view.DescriptionRef}}, DataClass: "artifact", Recipients: []Binding{binding}, Limits: capacity, Lifetime: "until_revoked"}
	issueScopedRoot(t, f.service, binding, 301, spec)
	request := ArtifactRead{CommandID: privateID(400), GrantID: spec.GrantID, Artifact: view.DescriptionRef, MaxBytes: int64(len(body))}
	return f, binding, scopedBound(t, f.service, binding), request, body
}

func testScopedArtifactReads(t *testing.T) {
	t.Parallel()
	t.Run("authorized_body_failures", testScopedBodyFailures)
	t.Run("exact_size_and_real_body_allocation", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f, binding, agent, request, body := scopedArtifactFixture(t, serviceDeps{})
		first, err := agent.ReadArtifact(ctx, request)
		if err != nil || string(first.Bytes) != string(body) {
			t.Fatalf("exact owner-rooted description artifact: %+v %v", first, err)
		}
		wrong := request
		wrong.CommandID, wrong.GrantID = privateID(401), privateID(310)
		wrong.Artifact.Bytes++
		wrong.MaxBytes++
		capacity := scopedCapacity()
		capacity.BufferBytes = artifactReadBufferBytes
		issueScopedRoot(t, f.service, binding, 311, GrantSpec{GrantID: wrong.GrantID, Grantee: binding, Operations: []string{"artifact.read"}, Resources: []ledger.Resource{{Kind: "artifact", Artifact: wrong.Artifact}}, DataClass: "artifact", Recipients: []Binding{binding}, Limits: capacity, Lifetime: "until_revoked"})
		before := f.store.Durable()
		if page, err := agent.ReadArtifact(ctx, wrong); !errors.Is(err, ledgerstore.ErrBodyCorrupt) || !reflect.DeepEqual(page, ArtifactPage{}) || f.store.Durable() != before {
			t.Fatalf("source and digest alone authorized a false body size: %+v %v", page, err)
		}
		capacity.BufferBytes, capacity.LocalUnits, capacity.MaxOperationBytes = int64(len(body)), 1, 0
		if _, err := f.service.ConfigureCapacity(ctx, binding, CapacitySpec{CommandID: privateID(402), ExpectedVersion: 1, Scope: ledger.ScopeKey{Kind: "colony"}, Limits: capacity}); err != nil {
			t.Fatal(err)
		}
		before = f.store.Durable()
		var busy *BusyError
		if page, err := agent.ReadArtifact(ctx, request); !errors.As(err, &busy) || !reflect.DeepEqual(page, ArtifactPage{}) || f.store.Durable() != before {
			t.Fatalf("artifact retry reserved only caller MaxBytes: %+v %v", page, err)
		}
		status, err := agent.ScopedCommandStatus(ctx, ScopedCommandLookup{CommandID: request.CommandID, GrantID: request.GrantID, SubmissionSHA256: first.Receipt.SubmissionSHA256})
		if err != nil || !reflect.DeepEqual(status, first.Receipt) {
			t.Fatalf("busy body hid passive receipt: %+v %v", status, err)
		}
		owner, err := f.service.ReadArtifact(ctx, binding, ArtifactLookup{Artifact: request.Artifact, MaxBytes: request.MaxBytes})
		if err != nil || string(owner) != string(body) || f.store.Durable() != before {
			t.Fatalf("owner read charged a unit or depended on shared enrolment: %q %v", owner, err)
		}
		view, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
		if err != nil || view.Held != (Exposure{}) || view.Used.LocalUnits != 1 {
			t.Fatalf("artifact refusal leaked or consumed capacity: %+v %v", view, err)
		}
	})
	t.Run("artifact_revocation_before_disclosure", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		var armed atomic.Bool
		f, binding, agent, request, _ := scopedArtifactFixture(t, serviceDeps{afterRead: func(_ Binding, scope string) {
			if scope == "artifact" && armed.CompareAndSwap(true, false) {
				close(entered)
				<-release
			}
		}})
		armed.Store(true)
		type result struct {
			page ArtifactPage
			err  error
		}
		pending := make(chan result, 1)
		go func() { page, err := agent.ReadArtifact(ctx, request); pending <- result{page, err} }()
		select {
		case <-entered:
		case got := <-pending:
			t.Fatalf("artifact failed before verification barrier: %+v %v", got.page, got.err)
		}
		if _, err := f.service.RevokeGrant(ctx, binding, GrantRevocation{CommandID: privateID(410), RevokeGrantID: request.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: 1}); err != nil {
			t.Fatal(err)
		}
		before := f.store.Durable()
		once.Do(func() { close(release) })
		got := <-pending
		if !errors.Is(got.err, ErrDenied) || !reflect.DeepEqual(got.page, ArtifactPage{}) || f.store.Durable() != before {
			t.Fatalf("artifact escaped final permission check: %+v %v", got.page, got.err)
		}
		missing := request
		missing.Artifact.SourceSequence += 1000
		missing.Artifact.SHA256 = ledger.Digest(sha256.Sum256([]byte("missing")))
		for _, denied := range []ArtifactRead{request, missing} {
			if page, err := agent.ReadArtifact(ctx, denied); err != ErrDenied || !reflect.DeepEqual(page, ArtifactPage{}) || f.store.Durable() != before {
				t.Fatalf("denial disclosed artifact existence: %+v %v", page, err)
			}
		}
	})
	t.Run("owner_unenrolled_and_shared_preparation", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		var armed atomic.Bool
		limits := DefaultLimits()
		limits.MaxConcurrentWrites = 1
		f := newServiceFixtureWithLimits(t, serviceDeps{beforePublish: func(_ Binding, scope string) error {
			if scope == "context" && armed.CompareAndSwap(true, false) {
				close(entered)
				<-release
			}
			return nil
		}}, limits)
		binding := registerPrivate(t, f.service, 10)
		other := scopedWorkContext(t, f.service, binding, 20, 21)
		scopedCreate(t, f.service, binding, 200, 201, binding.AgentID)
		task, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: privateID(200)})
		if err != nil {
			t.Fatal(err)
		}
		lookup := ArtifactLookup{Artifact: task.DescriptionRef, MaxBytes: task.DescriptionRef.Bytes}
		before := f.store.Durable()
		body, err := f.service.ReadArtifact(ctx, binding, lookup)
		if err != nil || string(body) != "A bounded task description\n" || f.store.Durable() != before {
			t.Fatalf("unenrolled owner read: %q %v", body, err)
		}
		scopedEnrol(t, f.service, binding, scopedCapacity(), 300, binding.AgentID)
		grant := privateScopedGrant(binding, 310, scopedCapacity(), "private.write")
		issueScopedRoot(t, f.service, binding, 311, grant)
		agent := scopedBound(t, f.service, binding)
		armed.Store(true)
		pending := make(chan error, 1)
		go func() {
			_, err := agent.WritePrivate(ctx, ScopedPrivateWrite{CommandID: privateID(400), GrantID: grant.GrantID, Scope: "context", ExpectedVersion: 1, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("held")}}})
			pending <- err
		}()
		select {
		case <-entered:
		case err := <-pending:
			t.Fatalf("write failed before preparation barrier: %v", err)
		}
		before = f.store.Durable()
		var busy *BusyError
		if data, err := f.service.ReadArtifact(ctx, binding, lookup); !errors.As(err, &busy) || data != nil || f.store.Durable() != before {
			t.Fatalf("owner body read bypassed the shared process slot: %q %v", data, err)
		}
		ownerWrite := PrivateWrite{Binding: other, CommandID: privateID(401), Scope: "context", ExpectedVersion: 1, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("owner")}}}
		if _, err := f.service.WritePrivate(ctx, ownerWrite); !errors.As(err, &busy) || f.store.Durable() != before {
			t.Fatalf("owner write used a separate preparation counter: %v", err)
		}
		once.Do(func() { close(release) })
		if err := <-pending; err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.WritePrivate(ctx, ownerWrite); err != nil {
			t.Fatalf("busy consumed an owner write ID: %v", err)
		}
		before = f.store.Durable()
		if data, err := f.service.ReadArtifact(ctx, binding, lookup); err != nil || string(data) != string(body) || f.store.Durable() != before {
			t.Fatalf("released shared preparation remained busy: %q %v", data, err)
		}
	})
}

func testScopedBodyFailures(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"missing", "corrupt", "collected"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, binding, agent, request, body := scopedArtifactFixture(t, serviceDeps{})
			first, err := agent.ReadArtifact(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			digest := request.Artifact.SHA256.String()
			name := filepath.Join(f.root, "bodies", digest[:2], digest)
			want := ledgerstore.ErrBodyMissing
			var collectionSequence uint64
			switch fault {
			case "missing":
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				corrupt := append([]byte{}, body...)
				corrupt[0] ^= 1
				writeTestFile(t, name, corrupt)
				want = ledgerstore.ErrBodyCorrupt
			case "collected":
				if _, err := f.service.TransitionTask(ctx, binding, TaskMutation{CommandID: privateID(410), TaskID: privateID(201), ExpectedVersion: 1, Action: "cancel"}); err != nil {
					t.Fatal(err)
				}
				report, err := f.store.Collect(ctx, ledgerstore.CollectPolicy{Actor: ledger.ActorOwner, BeforeNS: math.MaxInt64})
				if err != nil || len(report.Records) != 1 {
					t.Fatalf("real collection after terminal unpin: %+v %v", report, err)
				}
				collectionSequence = report.Records[0]
				want = ledgerstore.ErrBodyUnavailable
			}
			reopenScoped(t, f, DefaultLimits())
			agent = scopedBound(t, f.service, binding)
			before := f.store.Durable()
			page, err := agent.ReadArtifact(ctx, request)
			if !errors.Is(err, want) || !reflect.DeepEqual(page, ArtifactPage{}) || f.store.Durable() != before {
				t.Fatalf("authorized retry lost the distinct %s outcome: %+v %v", fault, page, err)
			}
			if fault == "collected" {
				var unavailable *ledgerstore.BodyUnavailableError
				if !errors.As(err, &unavailable) || unavailable.Digest != request.Artifact.SHA256 || unavailable.Seq != collectionSequence {
					t.Fatalf("collection provenance lost across reopen: %v", err)
				}
			}
			if data, err := f.service.ReadArtifact(ctx, binding, ArtifactLookup{Artifact: request.Artifact, MaxBytes: request.MaxBytes}); !errors.Is(err, want) || data != nil || f.store.Durable() != before {
				t.Fatalf("owner collapsed the %s outcome: %q %v", fault, data, err)
			}
			status, err := agent.ScopedCommandStatus(ctx, ScopedCommandLookup{CommandID: request.CommandID, GrantID: request.GrantID, SubmissionSHA256: first.Receipt.SubmissionSHA256})
			if err != nil || !reflect.DeepEqual(status, first.Receipt) {
				t.Fatalf("body availability erased a historical receipt: %+v %v", status, err)
			}
			book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || book.Used.LocalUnits != 1 || book.Held != (Exposure{}) {
				t.Fatalf("failed body retry leaked or recharged: %+v %v", book, err)
			}
		})
	}
}
