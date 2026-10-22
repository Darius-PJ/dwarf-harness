package agents

import (
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// FuzzPrivateGroupReplay exercises the same canonicalization, full-group
// decoder and transition planner used before Submit and during Open. No fake
// store is involved: real-store rejection and restart rows live in replay_test.
func FuzzPrivateGroupReplay(f *testing.F) {
	for mode := byte(0); mode < 10; mode++ {
		f.Add(mode, []byte(nil))
		f.Add(mode, []byte("private\x00\r\n\xff"))
	}
	f.Fuzz(func(t *testing.T, mode byte, input []byte) {
		if len(input) > 2048 {
			return
		}
		s := &Service{p: newProjection()}
		ids := submissionIDs{privateID(10), privateID(11), privateID(12)}
		_, digest, err := encodeRegistration(ids, "Fuzz-agent", input)
		if err != nil {
			t.Fatal(err)
		}
		registration := privateOperation{ids: ids, id: privateID(13), digest: digest, operation: "agent_register", scope: "agent", directory: "Fuzz-agent", changes: []privateChange{{"instructions", "replace", input}, {"memory", "replace", nil}}}
		install := func(op privateOperation, first uint64) {
			t.Helper()
			d, err := s.operationDrafts(op)
			if err != nil {
				t.Fatal(err)
			}
			g, err := decodeGroup(d, first)
			if err != nil || g == nil {
				t.Fatalf("valid initialization refused: %v", err)
			}
			next, err := s.p.planGroup(g, first)
			if err != nil {
				t.Fatal(err)
			}
			s.p.install(g, next)
		}
		install(registration, 1)
		ids.context = privateID(14)
		_, digest, err = encodeCreation(ids, "work")
		if err != nil {
			t.Fatal(err)
		}
		install(privateOperation{ids: ids, id: privateID(15), digest: digest, operation: "context_create", scope: "context", kind: "work", changes: []privateChange{{"handoff", "replace", nil}, {"memory", "replace", nil}}}, 7)

		data := append([]byte{0}, input...) // transcript chunks are nonempty
		expected := uint64(1)
		mode %= 10
		if mode == 9 {
			expected = 2 // Canonically valid, but not the projected predecessor.
		}
		changes := []privateChange{{"memory", "replace", data}, {"transcript", "append", data}}
		_, digest, err = encodePrivateWrite(ids, "context", expected, changes)
		if err != nil {
			t.Fatal(err)
		}
		d, err := s.operationDrafts(privateOperation{ids: ids, id: privateID(20), digest: digest, operation: "private_write", scope: "context", expected: expected, changes: changes})
		if err != nil {
			t.Fatal(err)
		}
		switch mode {
		case 1:
			d[1].Actor = ledger.ActorOwner
		case 2:
			d[1].ContextID = privateID(90)
		case 3:
			d[0].OperationID = privateID(90)
		case 4, 5:
			r, err := ledger.DecodeRuntimeCommandReceipt(d[2].Payload)
			if err != nil {
				t.Fatal(err)
			}
			if mode == 4 {
				r.SubmissionSHA256[0] ^= 1
			} else {
				r.Target.Slots = []string{"handoff"}
			}
			d[2].Payload = testPayload(t, r)
		case 6:
			r, err := ledger.DecodePrivateRevision(d[1].Payload)
			if err != nil {
				t.Fatal(err)
			}
			r.Changes[1].PreviousSHA256 = ledger.Digest(sha256.Sum256(data))
			d[1].Payload = testPayload(t, r)
		case 7:
			d = append(d, d[1]) // A foreign subsequence must not be filtered out.
		case 8:
			// Duplicate keys are forbidden regardless of the supplied bytes.
			d[1].Payload = append([]byte(`{"scope":"context",`), d[1].Payload[1:]...)
		}
		g, err := decodeGroup(d, 11)
		var next map[scopeKey]*scopeState
		if err == nil && g != nil {
			next, err = s.p.planGroup(g, 11)
		}
		if mode != 0 {
			if err == nil || !errors.Is(err, ErrInconsistent) && !errors.Is(err, ErrRevision) {
				t.Fatalf("invalid group mode %d accepted: %v", mode, err)
			}
			if s.p.scopes[keyFor(ids, "context")].revision != 1 {
				t.Fatal("refused group changed visible revision")
			}
			return
		}
		if err != nil || g == nil {
			t.Fatalf("valid group refused: %v", err)
		}
		s.p.install(g, next)
		state := s.p.scopes[keyFor(ids, "context")]
		want := ledger.Digest(sha256.Sum256(data))
		if state.revision != 2 || state.heads["memory"].SHA256 != want || state.heads["transcript"].SHA256 != want || state.heads["transcript"].Bytes != int64(len(data)) {
			t.Fatal("valid replay lost its selected bytes or revision")
		}
		if _, err := s.p.planGroup(g, 14); !errors.Is(err, ErrRevision) {
			t.Fatalf("same transition applied a second time: %v", err)
		}
	})
}
