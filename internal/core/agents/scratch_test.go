package agents

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

func privateWorkspace(t *testing.T, f *serviceFixture) (*workspace.Service, workspace.Workspace) {
	t.Helper()
	root := testRoot(t)
	for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=Private Test", "-c", "user.email=private@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "base"}, {"checkout", "--detach"}} {
		cmd := exec.CommandContext(context.Background(), "git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git %v: %v %s", args, err, out)
		}
	}
	proposal, err := workspace.Inspect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	config := workspace.Config{Capacity: &workspace.CapacityProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 2 << 20, CheckpointsPerPair: 4, GenerationWindow: 4}}
	service, _, err := workspace.Open(context.Background(), f.store, f.root, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	registered, err := service.Register(context.Background(), proposal)
	if err != nil {
		t.Fatal(err)
	}
	return service, registered
}
func TestPrivateScratchBindingsAreDistinct(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t, serviceDeps{})
	a := registerPrivate(t, f.service, 10)
	b := createPrivate(t, f.service, a, 20)
	ws, w := privateWorkspace(t, f)
	ctx := context.Background()
	for _, binding := range []Binding{a, b} {
		before := f.store.Durable()
		view, err := f.service.ScratchBinding(ctx, ScratchLookup{Binding: binding, WorkspaceID: w.ID})
		if err != nil || view.State != "absent" || f.store.Durable() != before {
			t.Fatalf("lookup allocated scratch: %+v %v", view, err)
		}
	}
	sa, err := ws.Scratch(ctx, a.ContextID, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := ws.Scratch(ctx, b.ContextID, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sa.ID == sb.ID || sa.Dir == sb.Dir {
		t.Fatal("real workspace fixture aliased contexts")
	}
	writer, err := ws.BeginWrite(ctx, workspace.WriteBinding{WriterID: privateID(40), ContextID: b.ContextID, AgentID: a.AgentID, TaskAgentID: a.AgentID, TaskScratchID: sa.ID})
	if err != nil {
		t.Fatal(err)
	}
	temporary := writer.Scratch()
	writer.Close()
	view, err := f.service.ScratchBinding(ctx, ScratchLookup{Binding: b, WorkspaceID: w.ID})
	if err != nil || view.ScratchID != sb.ID || view.Kind != "dedicated" {
		t.Fatalf("temporary substituted for dedicated: %+v %v", view, err)
	}
	view, err = f.service.ScratchBinding(ctx, ScratchLookup{Binding: b, WorkspaceID: w.ID, ExpectedScratchID: temporary.ID})
	if err != nil || view.Kind != "temporary" || view.ParentScratchID != sa.ID {
		t.Fatalf("temporary not identified: %+v %v", view, err)
	}
	view, err = f.service.ScratchBinding(ctx, ScratchLookup{Binding: a, WorkspaceID: w.ID, ExpectedScratchID: sb.ID})
	if err != nil || view.State != "mismatch" {
		t.Fatalf("foreign pair accepted: %+v %v", view, err)
	}
	before := f.store.Durable()
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	f.service, _, err = Open(ctx, f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	view, err = f.service.ScratchBinding(ctx, ScratchLookup{Binding: b, WorkspaceID: w.ID})
	if err != nil || view.ScratchID != sb.ID || f.store.Durable() != before {
		t.Fatalf("restart changed scratch: %+v %v", view, err)
	}
}
func TestPrivateScratchRefreshAcrossOtherCommits(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t, serviceDeps{})
	b := registerPrivate(t, f.service, 10)
	ws, w := privateWorkspace(t, f)
	ctx := context.Background()
	scratch, err := ws.Scratch(ctx, b.ContextID, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	writePrivate(t, f.service, b, 20, 1, Change{"memory", "replace", []byte("interleaved")})
	view, err := f.service.ScratchBinding(ctx, ScratchLookup{Binding: b, WorkspaceID: w.ID})
	if err != nil || view.ScratchID != scratch.ID || view.State != "present" {
		t.Fatalf("own commit skipped scratch creation: %+v %v", view, err)
	}
	if _, err := ws.CloseScratch(ctx, workspace.Closure{ScratchID: scratch.ID, Actor: ledger.ActorOwner, Reason: "abandoned"}); err != nil {
		t.Fatal(err)
	}
	writePrivate(t, f.service, b, 21, 2, Change{"memory", "replace", []byte("after closure")})
	before := f.store.Durable()
	view, err = f.service.ScratchBinding(ctx, ScratchLookup{Binding: b, WorkspaceID: w.ID})
	if err != nil || view.State != "closed" || view.ScratchID != scratch.ID || f.store.Durable() != before {
		t.Fatalf("own commit skipped closure: %+v %v", view, err)
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	f.service, _, err = Open(ctx, f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	view, err = f.service.ScratchBinding(ctx, ScratchLookup{Binding: b, WorkspaceID: w.ID})
	if err != nil || view.State != "closed" || view.ScratchID != scratch.ID || f.store.Durable() != before {
		t.Fatalf("restart created replacement: %+v %v", view, err)
	}
}

func TestPrivateScratchClosureActorBinding(t *testing.T) {
	t.Parallel()
	for _, actor := range []ledger.Actor{ledger.ActorOwner, ledger.ActorAgent} {
		t.Run(string(actor), func(t *testing.T) {
			t.Parallel()
			f := newServiceFixture(t, serviceDeps{})
			b := registerPrivate(t, f.service, 10)
			ws, w := privateWorkspace(t, f)
			ctx := context.Background()
			scratch, err := ws.Scratch(ctx, b.ContextID, w.ID)
			if err != nil {
				t.Fatal(err)
			}
			lookup := ScratchLookup{Binding: b, WorkspaceID: w.ID}
			view, err := f.service.ScratchBinding(ctx, lookup)
			if err != nil || view.State != "present" || view.ScratchID != scratch.ID {
				t.Fatalf("initial scratch binding: %+v %v", view, err)
			}
			// CloseScratch always supplies an envelope context. Append the frozen
			// F2 variant directly through the real store, not a replacement writer.
			payload, err := (ledger.ScratchClosed{ScratchID: scratch.ID, ContextID: b.ContextID, Reason: "abandoned"}).Encode()
			if err != nil {
				t.Fatal(err)
			}
			draft := ledger.Draft{Type: ledger.TypeScratchClosed, TypeVersion: 1, Actor: actor, Payload: payload}
			if actor == ledger.ActorAgent {
				draft.ContextID = privateID(99)
			}
			if _, err := f.store.Append(ctx, []ledger.Draft{draft}); err != nil {
				t.Fatal(err)
			}
			before := f.store.Durable()
			view, err = f.service.ScratchBinding(ctx, lookup)
			if actor == ledger.ActorAgent {
				if !errors.Is(err, ErrInconsistent) {
					t.Fatalf("live refresh accepted another agent context: %v", err)
				}
			} else if err != nil || view.State != "closed" || view.ScratchID != scratch.ID || f.store.Durable() != before {
				t.Fatalf("owner closure without envelope context: %+v %v", view, err)
			}
			if err := f.service.Close(); err != nil {
				t.Fatal(err)
			}
			opened, _, err := Open(ctx, f.store, f.root, DefaultLimits())
			if actor == ledger.ActorAgent {
				if opened != nil {
					_ = opened.Close()
				}
				if !errors.Is(err, ErrInconsistent) {
					t.Fatalf("Open accepted another agent context: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			f.service = opened
			view, err = f.service.ScratchBinding(ctx, lookup)
			if err != nil || view.State != "closed" || view.ScratchID != scratch.ID || f.store.Durable() != before {
				t.Fatalf("restart lost owner closure: %+v %v", view, err)
			}
		})
	}
}
