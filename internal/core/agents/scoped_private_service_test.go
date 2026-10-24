package agents

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func scopedPrivateFixture(t *testing.T, deps serviceDeps, capacity ledger.Capacity) (*serviceFixture, Binding, *Agent, GrantSpec) {
	t.Helper()
	f := newServiceFixture(t, deps)
	binding := registerPrivate(t, f.service, 10)
	scopedEnrol(t, f.service, binding, capacity, 100, binding.AgentID)
	grant := privateScopedGrant(binding, 200, capacity, "context.inspect", "private.read", "private.write", "turn.start", "turn.inspect", "turn.cancel")
	issueScopedRoot(t, f.service, binding, 201, grant)
	return f, binding, scopedBound(t, f.service, binding), grant
}

func TestScopedOwnerAndAgentPrivateCAS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, binding, agent, grant := scopedPrivateFixture(t, serviceDeps{}, scopedCapacity())
	owner := PrivateWrite{Binding: binding, CommandID: privateID(300), Scope: "context", ExpectedVersion: 1, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("owner memory")}}}
	request := ScopedPrivateWrite{CommandID: privateID(301), GrantID: grant.GrantID, Scope: "context", ExpectedVersion: 1, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("agent memory")}}}
	var ownerReceipt Receipt
	var agentReceipt ScopedReceipt
	var ownerErr, agentErr error
	var done sync.WaitGroup
	start := make(chan struct{})
	done.Go(func() { <-start; ownerReceipt, ownerErr = f.service.WritePrivate(ctx, owner) })
	done.Go(func() { <-start; agentReceipt, agentErr = agent.WritePrivate(ctx, request) })
	close(start)
	done.Wait()
	want := "owner memory"
	if ownerErr == nil && errors.Is(agentErr, ErrRevision) {
		request.ExpectedVersion = 2
		agentReceipt, agentErr = agent.WritePrivate(ctx, request)
		want = "agent memory"
	} else if agentErr == nil && errors.Is(ownerErr, ErrRevision) {
		owner.ExpectedVersion = 2
		ownerReceipt, ownerErr = f.service.WritePrivate(ctx, owner)
	} else {
		t.Fatalf("two entrances did not contend on one head: owner %v, agent %v", ownerErr, agentErr)
	}
	if ownerErr != nil || agentErr != nil || agentReceipt.Actor != "agent" {
		t.Fatalf("losing CAS consumed its command ID or emitted owner provenance: %+v %v %v", agentReceipt, ownerErr, agentErr)
	}
	ownerView, err := f.service.Snapshot(ctx, ReadBinding{Binding: binding, Scope: "context"})
	if err != nil {
		t.Fatal(err)
	}
	agentView, err := agent.Snapshot(ctx, ScopedSnapshotLookup{GrantID: grant.GrantID, Scope: "context"})
	if err != nil || ownerView.Revision != 3 || !reflect.DeepEqual(ownerView, agentView) {
		t.Fatalf("separate owner/agent heads: %+v %+v %v", ownerView, agentView, err)
	}
	if page := readPrivate(t, f.service, binding, 3, "memory"); string(page.Bytes) != want {
		t.Fatalf("CAS body: %q, want %q", page.Bytes, want)
	}
	status, err := agent.ScopedCommandStatus(ctx, ScopedCommandLookup{CommandID: request.CommandID, GrantID: request.GrantID, SubmissionSHA256: agentReceipt.SubmissionSHA256})
	if err != nil || !reflect.DeepEqual(status, agentReceipt) {
		t.Fatalf("agent receipt not queryable as its own: %+v %v", status, err)
	}
	// A matched historical write neither reserves capacity nor replays old bytes.
	if _, err := f.service.ConfigureCapacity(ctx, binding, CapacitySpec{CommandID: privateID(310), ExpectedVersion: 1, Scope: ledger.ScopeKey{Kind: "colony"}, Limits: ledger.Capacity{}}); err != nil {
		t.Fatal(err)
	}
	before := f.store.Durable()
	if replay, err := agent.WritePrivate(ctx, request); err != nil || !reflect.DeepEqual(replay, agentReceipt) {
		t.Fatalf("agent replay at full capacity: %+v %v", replay, err)
	}
	if replay, err := f.service.WritePrivate(ctx, owner); err != nil || !reflect.DeepEqual(replay, ownerReceipt) {
		t.Fatalf("owner replay: %+v %v", replay, err)
	}
	if f.store.Durable() != before {
		t.Fatal("historical write appended another group")
	}
	if page := readPrivate(t, f.service, binding, 3, "memory"); string(page.Bytes) != want {
		t.Fatalf("historical replay materialized old content: %q", page.Bytes)
	}
	reopenScoped(t, f, DefaultLimits())
	book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
	if err != nil || book.Used != (Exposure{LocalUnits: 1}) || book.Held != (Exposure{}) {
		t.Fatalf("shared-head replay changed charges: %+v %v", book, err)
	}
}

func TestScopedProjectionBeforeAckAndIndependentPreparation(t *testing.T) {
	t.Parallel()
	t.Run("independent_preparation_and_complete_projection", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		publishing, publish := make(chan struct{}), make(chan struct{})
		projecting, project := make(chan struct{}), make(chan struct{})
		var publishOnce, projectOnce sync.Once
		defer publishOnce.Do(func() { close(publish) })
		defer projectOnce.Do(func() { close(project) })
		var armed, projected atomic.Bool
		var origin Binding
		f, binding, agent, grant := scopedPrivateFixture(t, serviceDeps{
			beforePublish: func(b Binding, scope string) error {
				if armed.Load() && b == origin && scope == "context" {
					close(publishing)
					<-publish
				}
				return nil
			},
			beforeProjection: func(b Binding, scope string) {
				if armed.Load() && b == origin && scope == "context" {
					close(projecting)
					<-project
					projected.Store(true)
				}
			},
		}, scopedCapacity())
		origin = binding
		other := scopedWorkContext(t, f.service, binding, 20, 21)
		otherGrant := privateScopedGrant(other, 210, scopedCapacity(), "private.write", "context.inspect")
		issueScopedRoot(t, f.service, binding, 211, otherGrant)
		otherAgent := scopedBound(t, f.service, other)
		request := ScopedPrivateWrite{CommandID: privateID(300), GrantID: grant.GrantID, Scope: "context", ExpectedVersion: 1, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("new memory")}, {Slot: "handoff", Mode: "replace", Bytes: []byte("new handoff")}}}
		armed.Store(true)
		result := make(chan error, 1)
		go func() {
			_, err := agent.WritePrivate(ctx, request)
			if err == nil && !projected.Load() {
				err = errors.New("acknowledgement preceded complete projection")
			}
			result <- err
		}()
		<-publishing
		if _, err := otherAgent.WritePrivate(ctx, ScopedPrivateWrite{CommandID: privateID(301), GrantID: otherGrant.GrantID, Scope: "context", ExpectedVersion: 1, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("independent")}}}); err != nil {
			t.Fatalf("blocked I/O held the service lock or another scope: %v", err)
		}
		if page := readPrivate(t, f.service, other, 2, "memory"); string(page.Bytes) != "independent" {
			t.Fatalf("independent materialization: %q", page.Bytes)
		}
		publishOnce.Do(func() { close(publish) })
		<-projecting
		view, err := otherAgent.Snapshot(ctx, ScopedSnapshotLookup{GrantID: otherGrant.GrantID, Scope: "context"})
		if err != nil || view.Materializations["memory"] != "current" {
			t.Fatalf("projection held the global lock: %+v %v", view, err)
		}
		select {
		case err := <-result:
			t.Fatalf("acknowledged before projection: %v", err)
		default:
		}
		projectOnce.Do(func() { close(project) })
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		snapshot, err := agent.Snapshot(ctx, ScopedSnapshotLookup{GrantID: grant.GrantID, Scope: "context"})
		if err != nil || snapshot.Revision != 2 || snapshot.Materializations["memory"] != "current" || snapshot.Materializations["handoff"] != "current" {
			t.Fatalf("acknowledged mixed projection: %+v %v", snapshot, err)
		}
		base := filepath.Join(f.root, "agents", "Scout-10", "contexts", binding.ContextID.String())
		if string(readTestFile(t, filepath.Join(base, "Memory.md"))) != "new memory" || string(readTestFile(t, filepath.Join(base, "Handoff.md"))) != "new handoff" {
			t.Fatal("acknowledged an incomplete materialization")
		}
	})
	for _, owner := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			name := "agent_duplicate"
			if owner {
				name = "owner_duplicate"
			}
			if fail {
				name += "_failed_completion"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				entered, release := make(chan struct{}), make(chan struct{})
				waiting := make(chan struct{}, 2)
				var once sync.Once
				defer once.Do(func() { close(release) })
				var armed atomic.Bool
				var materializations atomic.Int32
				var digest ledger.Digest
				f, binding, agent, grant := scopedPrivateFixture(t, serviceDeps{
					afterCommit: func(receipt Receipt) {
						if armed.Load() {
							digest = receipt.SubmissionSHA256
						}
					},
					afterScopedCommit: func(receipt ScopedReceipt) error {
						if armed.Load() {
							digest = receipt.SubmissionSHA256
						}
						return nil
					},
					beforeMaterialize: func(_ Binding, scope, slot string) error {
						if armed.Load() && scope == "context" && slot == "memory" {
							materializations.Add(1)
							close(entered)
							<-release
							if fail {
								return errors.New("fixture materialization failure")
							}
						}
						return nil
					},
					beforeReplayWait: func() { waiting <- struct{}{} },
				}, scopedCapacity())
				request := ScopedPrivateWrite{CommandID: privateID(300), GrantID: grant.GrantID, Scope: "context", ExpectedVersion: 1, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("once")}}}
				ownerRequest := PrivateWrite{Binding: binding, CommandID: request.CommandID, Scope: request.Scope, ExpectedVersion: 1, Changes: request.Changes}
				write := func(ctx context.Context) error {
					if owner {
						_, err := f.service.WritePrivate(ctx, ownerRequest)
						return err
					}
					_, err := agent.WritePrivate(ctx, request)
					return err
				}
				armed.Store(true)
				original := make(chan error, 1)
				go func() { original <- write(ctx) }()
				<-entered
				before := f.store.Durable()
				duplicate := make(chan error, 1)
				go func() { duplicate <- write(ctx) }()
				select {
				case <-waiting:
				case err := <-duplicate:
					t.Fatalf("duplicate skipped the completion gate: %v", err)
				}
				cancelCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				cancelled := make(chan error, 1)
				go func() { cancelled <- write(cancelCtx) }()
				select {
				case <-waiting:
				case err := <-cancelled:
					t.Fatalf("duplicate skipped its cancellable wait: %v", err)
				}
				cancel()
				if err := <-cancelled; !errors.Is(err, context.Canceled) {
					t.Fatalf("receipt wait ignored cancellation: %v", err)
				}
				// Receipt-only status is available even though acknowledgement waits.
				if owner {
					if _, err := f.service.CommandStatus(ctx, CommandLookup{Binding: binding, CommandID: request.CommandID, SubmissionSHA256: digest}); err != nil {
						t.Fatalf("owner receipt hidden by completion: %v", err)
					}
				} else if _, err := agent.ScopedCommandStatus(ctx, ScopedCommandLookup{CommandID: request.CommandID, GrantID: grant.GrantID, SubmissionSHA256: digest}); err != nil {
					t.Fatalf("agent receipt hidden by completion: %v", err)
				}
				select {
				case err := <-duplicate:
					t.Fatalf("duplicate acknowledged before completion: %v", err)
				default:
				}
				once.Do(func() { close(release) })
				originalErr := <-original
				if fail && !errors.Is(originalErr, ErrMaterialization) || !fail && originalErr != nil {
					t.Fatalf("original completion: %v", originalErr)
				}
				if err := <-duplicate; err != nil {
					t.Fatalf("historical receipt revalidated completion availability: %v", err)
				}
				if f.store.Durable() != before || materializations.Load() != 1 {
					t.Fatal("duplicate wrote, charged, or materialized again")
				}
				book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
				used := int64(1)
				if owner {
					used = 0
				}
				if err != nil || book.Used.LocalUnits != used || book.Held != (Exposure{}) {
					t.Fatalf("duplicate preparation leaked or charged: %+v %v", book, err)
				}
			})
		}
	}
	t.Run("retained_copy_rechecks_the_semantic_digest", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		data := []byte("original")
		var mutate atomic.Bool
		f, binding, agent, grant := scopedPrivateFixture(t, serviceDeps{beforeLookup: func() {
			if mutate.CompareAndSwap(true, false) {
				data[0] ^= 1
			}
		}}, scopedCapacity())
		request := ScopedPrivateWrite{CommandID: privateID(300), GrantID: grant.GrantID, Scope: "context", ExpectedVersion: 1, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: data}}}
		before := f.store.Durable()
		mutate.Store(true)
		if _, err := agent.WritePrivate(ctx, request); err != ErrInvalid || f.store.Durable() != before {
			t.Fatalf("changed caller bytes escaped retained-copy verification: %v", err)
		}
		data[0] ^= 1
		if _, err := agent.WritePrivate(ctx, request); err != nil {
			t.Fatalf("pre-publication refusal consumed the command ID: %v", err)
		}
		if page := readPrivate(t, f.service, binding, 2, "memory"); string(page.Bytes) != "original" {
			t.Fatalf("published bytes differ from the semantic request: %q", page.Bytes)
		}
		book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
		if err != nil || book.Used.LocalUnits != 1 || book.Held != (Exposure{}) {
			t.Fatalf("failed preparation charged or leaked: %+v %v", book, err)
		}
	})
}
