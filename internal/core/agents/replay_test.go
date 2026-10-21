package agents

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func testPayload(t *testing.T, p interface {
	Encode() (json.RawMessage, error)
}) json.RawMessage {
	t.Helper()
	data, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestF3_10PayloadsAndRecordRules(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*testing.T, []ledger.Draft) []ledger.Draft
	}{
		{"missing_payload", func(_ *testing.T, d []ledger.Draft) []ledger.Draft { d[1].Payload = []byte(`{}`); return d }},
		{"actor", func(_ *testing.T, d []ledger.Draft) []ledger.Draft { d[1].Actor = ledger.ActorOwner; return d }},
		{"identity", func(_ *testing.T, d []ledger.Draft) []ledger.Draft { d[1].ContextID = privateID(90); return d }},
		{"operation_id", func(_ *testing.T, d []ledger.Draft) []ledger.Draft { d[0].OperationID = privateID(90); return d }},
		{"receipt_digest", func(t *testing.T, d []ledger.Draft) []ledger.Draft {
			p, err := ledger.DecodeRuntimeCommandReceipt(d[2].Payload)
			if err != nil {
				t.Fatal(err)
			}
			p.SubmissionSHA256[0] ^= 1
			d[2].Payload = testPayload(t, p)
			return d
		}},
		{"receipt_target", func(t *testing.T, d []ledger.Draft) []ledger.Draft {
			p, err := ledger.DecodeRuntimeCommandReceipt(d[2].Payload)
			if err != nil {
				t.Fatal(err)
			}
			p.Target.Slots = []string{"memory"}
			d[2].Payload = testPayload(t, p)
			return d
		}},
		{"predecessor", func(t *testing.T, d []ledger.Draft) []ledger.Draft {
			p, err := ledger.DecodePrivateRevision(d[1].Payload)
			if err != nil {
				t.Fatal(err)
			}
			p.Changes[0].PreviousSHA256[0] = 1
			d[1].Payload = testPayload(t, p)
			return d
		}},
		{"missing_record", func(_ *testing.T, d []ledger.Draft) []ledger.Draft { return []ledger.Draft{d[0], d[2]} }},
		{"extra_foreign", func(_ *testing.T, d []ledger.Draft) []ledger.Draft {
			return append(d, ledger.Draft{Type: ledger.TypeWorkspaceRegistered, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: []byte(`{}`)})
		}},
		{"reordered", func(_ *testing.T, d []ledger.Draft) []ledger.Draft { d[1], d[2] = d[2], d[1]; return d }},
		{"wrong_session", func(t *testing.T, d []ledger.Draft) []ledger.Draft {
			ids := submissionIDs{d[0].AgentID, privateID(90), d[0].ContextID}
			_, digest, err := encodePrivateWrite(ids, "context", 1, []privateChange{{"transcript", "append", []byte("chunk")}})
			if err != nil {
				t.Fatal(err)
			}
			cmd, err := ledger.DecodeCommand(d[0].Payload)
			if err != nil {
				t.Fatal(err)
			}
			cmd.SubmissionSHA256 = digest
			d[0].Payload = testPayload(t, cmd)
			receipt, err := ledger.DecodeRuntimeCommandReceipt(d[2].Payload)
			if err != nil {
				t.Fatal(err)
			}
			receipt.SubmissionSHA256 = digest
			d[2].Payload = testPayload(t, receipt)
			for i := range d {
				d[i].ExecutionSessionID = ids.session
			}
			return d
		}},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var armed atomic.Bool
			var poisoned []ledger.Draft
			f := newServiceFixture(t, serviceDeps{beforeSubmit: func(d []ledger.Draft) []ledger.Draft {
				if armed.Load() {
					d = row.mutate(t, d)
					poisoned = d
				}
				return d
			}})
			b := registerPrivate(t, f.service, 10)
			before := f.store.Durable()
			armed.Store(true)
			_, err := f.service.WritePrivate(context.Background(), PrivateWrite{Binding: b, CommandID: privateID(20), Scope: "context", ExpectedVersion: 1, Changes: []Change{{"transcript", "append", []byte("chunk")}}})
			if err == nil || f.store.Durable() != before {
				t.Fatalf("invalid group appended: %v through %d/%d", err, f.store.Durable(), before)
			}
			if len(poisoned) == 0 {
				t.Fatal("invalid-record fixture was not exercised")
			}
			if err := f.service.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.Submit(context.Background(), poisoned[0], poisoned[1:]); err != nil {
				t.Fatalf("real-store malformed group fixture: %v", err)
			}
			opened, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
			if opened != nil {
				_ = opened.Close()
			}
			if !errors.Is(err, ErrInconsistent) {
				t.Fatalf("Open accepted invalid record group: %v", err)
			}
		})
	}
	t.Run("nonempty_initial_document", func(t *testing.T) {
		t.Parallel()
		var poisoned []ledger.Draft
		f := newServiceFixture(t, serviceDeps{beforeSubmit: func(d []ledger.Draft) []ledger.Draft {
			p, err := ledger.DecodePrivateRevision(d[4].Payload)
			if err != nil {
				t.Fatal(err)
			}
			p.Changes[0].SHA256, _ = hashObject([]byte("imported"))
			p.Changes[0].Bytes = 8
			d[4].Payload = testPayload(t, p)
			poisoned = d
			return d
		}})
		_, err := f.service.RegisterAgent(context.Background(), AgentRegistration{AgentID: privateID(10), SessionID: privateID(11), ContextID: privateID(12), CommandID: privateID(13), Directory: "Scout-10"})
		if !errors.Is(err, ErrInconsistent) || f.store.Durable() != 0 {
			t.Fatalf("imported initial private bytes: %v", err)
		}
		if err := f.service.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.Submit(context.Background(), poisoned[0], poisoned[1:]); err != nil {
			t.Fatal(err)
		}
		opened, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
		if opened != nil {
			_ = opened.Close()
		}
		if !errors.Is(err, ErrInconsistent) {
			t.Fatalf("Open imported initial bytes: %v", err)
		}
	})
}
func TestPrivateReplayRefreshesRacingTail(t *testing.T) {
	t.Parallel()
	var hold atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	f := newServiceFixture(t, serviceDeps{beforeLookup: func() {
		if hold.CompareAndSwap(true, false) {
			close(entered)
			<-release
		}
	}})
	b := registerPrivate(t, f.service, 10)
	req := memoryWrite(b, 20, 1, "race")
	hold.Store(true)
	type result struct {
		r   Receipt
		err error
	}
	done := make(chan result, 1)
	go func() { r, err := f.service.WritePrivate(context.Background(), req); done <- result{r, err} }()
	<-entered
	winner, err := f.service.WritePrivate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	before := f.store.Durable()
	once.Do(func() { close(release) })
	late := <-done
	if late.err != nil || late.r.Sequence != winner.Sequence || f.store.Durable() != before {
		t.Fatalf("racing tail lost duplicate precedence: %+v", late)
	}
}
func TestPrivateForeignCommandAndHistoricalLimits(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t, serviceDeps{})
	b := registerPrivate(t, f.service, 10)
	r := writePrivate(t, f.service, b, 20, 1, Change{"memory", "replace", []byte("larger than lowered profile")})
	digest, _ := hashObject([]byte("foreign"))
	cmd := ledger.Command{CommandID: privateID(90), SubmissionSHA256: digest}
	if _, err := f.store.Submit(context.Background(), ledger.Draft{Type: ledger.TypeCommand, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: testPayload(t, cmd)}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CommandStatus(context.Background(), CommandLookup{Binding: b, CommandID: cmd.CommandID, SubmissionSHA256: digest}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("foreign tombstone became private receipt: %v", err)
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	limits := DefaultLimits()
	limits.MaxObjectBytes = 1
	limits.MaxOperationBytes = 1
	var err error
	f.service, _, err = Open(context.Background(), f.store, f.root, limits)
	if err != nil {
		t.Fatal(err)
	}
	if p := readPrivate(t, f.service, b, 2, "memory"); string(p.Bytes) != "larger than lowered profile" {
		t.Fatal("write profile corrupted history")
	}
	replay, err := f.service.WritePrivate(context.Background(), memoryWrite(b, 20, 1, "larger than lowered profile"))
	if err != nil || replay.Sequence != r.Sequence {
		t.Fatalf("lowered capacity before replay: %+v %v", replay, err)
	}
	if _, err := f.service.WritePrivate(context.Background(), memoryWrite(b, 90, 2, "too large")); !errors.Is(err, ErrCommandMismatch) {
		t.Fatalf("foreign command was omitted from the replay bound: %v", err)
	}
	before := f.store.Durable()
	if _, err := f.service.WritePrivate(context.Background(), memoryWrite(b, 30, 2, "too large")); !errors.Is(err, ErrTooLarge) || f.store.Durable() != before {
		t.Fatalf("lowered write limit: %v", err)
	}
	if _, err := f.service.WritePrivate(context.Background(), memoryWrite(b, 30, 2, "x")); err != nil {
		t.Fatalf("size refusal consumed ID: %v", err)
	}
}
