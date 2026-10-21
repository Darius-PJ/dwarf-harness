package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func privateID(n int) uuid.UUID {
	id, err := uuid.Parse(fmt.Sprintf("019a0700-0000-7000-8000-%012x", n))
	if err != nil {
		panic(err)
	}
	return id
}

type serviceFixture struct {
	root    string
	store   *ledgerstore.Store
	service *Service
}

func newServiceFixture(t *testing.T, deps serviceDeps) *serviceFixture {
	t.Helper()
	return newServiceFixtureWithLimits(t, deps, DefaultLimits())
}
func newServiceFixtureWithLimits(t *testing.T, deps serviceDeps, limits Limits) *serviceFixture {
	t.Helper()
	root := testRoot(t)
	store, err := ledgerstore.Create(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	f := &serviceFixture{root: root, store: store}
	t.Cleanup(func() {
		if f.service != nil {
			_ = f.service.Close()
		}
		if f.store != nil {
			_ = f.store.Close()
		}
	})
	f.service, _, err = openService(context.Background(), store, root, limits, deps)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func registerPrivate(t *testing.T, s *Service, n int) Binding {
	t.Helper()
	b := Binding{AgentID: privateID(n), ContextID: privateID(n + 2)}
	_, err := s.RegisterAgent(context.Background(), AgentRegistration{CommandID: privateID(n + 3), AgentID: b.AgentID, SessionID: privateID(n + 1), ContextID: b.ContextID, Directory: fmt.Sprintf("Scout-%d", n), AgentMarkdown: []byte("# Instructions\r\n")})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func writePrivate(t *testing.T, s *Service, b Binding, id int, revision uint64, changes ...Change) Receipt {
	t.Helper()
	r, err := s.WritePrivate(context.Background(), PrivateWrite{Binding: b, CommandID: privateID(id), Scope: "context", ExpectedVersion: revision, Changes: changes})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func readPrivate(t *testing.T, s *Service, b Binding, revision uint64, slot string) PrivatePage {
	t.Helper()
	p, err := s.ReadPrivate(context.Background(), PrivateRead{ReadBinding: ReadBinding{Binding: b, Scope: "context"}, Revision: revision, Slot: slot, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A lost acknowledgement must not become a second effect after a real restart.
func TestPrivateRestartLostAcknowledgement(t *testing.T) {
	t.Parallel()
	fail := false
	f := newServiceFixture(t, serviceDeps{beforeMaterialize: func(_ Binding, scope, slot string) error {
		if fail && scope == "context" && slot == "memory" {
			return os.ErrPermission
		}
		return nil
	}})
	b := registerPrivate(t, f.service, 10)
	request := PrivateWrite{Binding: b, CommandID: privateID(20), Scope: "context", ExpectedVersion: 1, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("private\x00memory\r\n")}}}
	fail = true
	if _, err := f.service.WritePrivate(context.Background(), request); !errors.Is(err, ErrMaterialization) {
		t.Fatalf("expected withheld acknowledgement after commit: %v", err)
	}
	receipt, err := f.service.WritePrivate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	before := f.store.Durable()
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.store, _, err = ledgerstore.Open(f.root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	var report Reconciliation
	f.service, report, err = Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil || len(report.Completed) != 1 {
		t.Fatalf("restart did not complete the unacknowledged head: %+v %v", report, err)
	}
	replay, err := f.service.WritePrivate(context.Background(), request)
	if err != nil || replay.Sequence != receipt.Sequence || replay.Revision != 2 || f.store.Durable() != before {
		t.Fatalf("lost-ack replay: %+v, %v; durable %d/%d", replay, err, f.store.Durable(), before)
	}
	if p := readPrivate(t, f.service, b, 2, "memory"); string(p.Bytes) != "private\x00memory\r\n" {
		t.Fatalf("restart bytes %q", p.Bytes)
	}
	request.Changes[0].Bytes = []byte("different")
	if _, err := f.service.WritePrivate(context.Background(), request); !errors.Is(err, ErrCommandMismatch) {
		t.Fatalf("same ID different digest: %v", err)
	}
}

func TestPrivateIdentityReplayAndUniqueness(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t, serviceDeps{})
	request := AgentRegistration{CommandID: privateID(13), AgentID: privateID(10), SessionID: privateID(11), ContextID: privateID(12), Directory: "Scout-10", AgentMarkdown: []byte("instructions")}
	first, err := f.service.RegisterAgent(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.service.RegisterAgent(context.Background(), request)
	if err != nil || replay.Sequence != first.Sequence {
		t.Fatalf("registration retry: %+v %v", replay, err)
	}
	request.Directory = "renamed"
	if _, err := f.service.RegisterAgent(context.Background(), request); !errors.Is(err, ErrCommandMismatch) {
		t.Fatalf("registration argument bypassed digest mismatch: %v", err)
	}
	create := ContextCreation{Binding: Binding{request.AgentID, privateID(20)}, CommandID: privateID(21), Kind: "work"}
	first, err = f.service.CreateContext(context.Background(), create)
	if err != nil {
		t.Fatal(err)
	}
	replay, err = f.service.CreateContext(context.Background(), create)
	if err != nil || replay.Sequence != first.Sequence {
		t.Fatalf("creation retry: %+v %v", replay, err)
	}
	before := f.store.Durable()
	collision := AgentRegistration{CommandID: privateID(33), AgentID: privateID(30), SessionID: privateID(31), ContextID: privateID(32), Directory: "sCoUt-10"}
	if _, err := f.service.RegisterAgent(context.Background(), collision); err == nil || f.store.Durable() != before {
		t.Fatalf("case-folded directory rebound: %v", err)
	}
	collision.Directory = "different"
	collision.SessionID = request.SessionID
	if _, err := f.service.RegisterAgent(context.Background(), collision); err == nil || f.store.Durable() != before {
		t.Fatalf("session rebound: %v", err)
	}
}
