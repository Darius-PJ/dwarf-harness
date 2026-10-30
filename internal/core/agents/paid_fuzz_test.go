package agents

import (
	"context"
	"os"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func FuzzPaidGroupReplay(f *testing.F) {
	for mode := byte(0); mode < 6; mode++ {
		f.Add(mode, uint64(0), []byte(`{}`))
		f.Add(mode, uint64(1), []byte(nil))
	}
	// OpenAI intent/receipt seeds must not be accepted against an Anthropic
	// reservation merely because both use the unchanged schema-4 family.
	_, openAI := scopedGoldenFixture(f, "14", -1)
	for _, commit := range openAI {
		if len(commit.Records) != 3 || commit.Records[1].Type != ledger.TypeRuntimePaidDispatchIntent {
			continue
		}
		intent, err := ledger.DecodeRuntimePaidDispatchIntent(commit.Records[1].Payload)
		if err != nil {
			f.Fatal(err)
		}
		if intent.Credential.ScopeID != paidGoldenID(0x1401) {
			continue
		}
		f.Add(byte(1), uint64(0), []byte(commit.Records[1].Payload))
		f.Add(byte(2), uint64(0), []byte(commit.Records[2].Payload))
	}
	fixture := newPaidFixture(f)
	p := fixture.reserve(f)
	intent := ledger.RuntimePaidDispatchIntent{AttemptID: p.AttemptID, ExpectedVersion: 1, Version: 2, AuthorityVersion: fixture.service.p.authority, RequestSHA256: p.Quote.RequestSHA256, QuoteSHA256: p.Quote.QuoteSHA256, Credential: p.Request.Credential, ObservedUTCNS: fixture.service.p.reviewFloor}
	base := fixture.drafts(f, intent)
	if err := fixture.service.Close(); err != nil {
		f.Fatal(err)
	}
	fixture.service = nil
	if err := fixture.store.Close(); err != nil {
		f.Fatal(err)
	}
	fixture.store = nil
	prepared := testRoot(f)
	if err := os.CopyFS(prepared, os.DirFS(fixture.root)); err != nil {
		f.Fatal(err)
	}
	var err error
	fixture.store, _, err = ledgerstore.Open(fixture.root, ledger.Defined())
	if err != nil {
		f.Fatal(err)
	}
	fixture.service, _, err = Open(context.Background(), fixture.store, fixture.root, DefaultLimits())
	if err != nil {
		f.Fatal(err)
	}
	baseline, _, err := Open(context.Background(), fixture.store, fixture.root, DefaultLimits())
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { _ = baseline.Close() })
	before := fixture.store.Durable()
	f.Fuzz(func(t *testing.T, mode byte, value uint64, input []byte) {
		t.Parallel()
		if len(input) > ledger.MaxPaidPayloadBytes {
			return
		}
		d := append([]ledger.Draft(nil), base...)
		valid := false
		switch mode % 6 {
		case 0:
			v := intent
			v.ObservedUTCNS -= int64(value % 256)
			d[1].Payload = paidEncode(t, v)
			valid = value%256 == 0
		case 1:
			d[1].Payload = append([]byte(nil), input...)
		case 2:
			d[2].Payload = append([]byte(nil), input...)
		case 3:
			d[1].TypeVersion = int(value%256) + 1
			valid = value%256 == 0
		case 4:
			v := intent
			v.RequestSHA256[0] ^= byte(value)
			d[1].Payload = paidEncode(t, v)
			valid = byte(value) == 0
		case 5:
			d[1].ContextID[15] ^= byte(value)
			valid = byte(value) == 0
		}
		fixture.service.mu.Lock()
		_, _, _, g, _, err := decodeFamily(d, before+1)
		if err == nil {
			_, err = fixture.service.planPaid(context.Background(), g, before+1)
		}
		unchanged := sameProjectionState(fixture.service.p, baseline.p)
		fixture.service.mu.Unlock()
		if !unchanged || fixture.store.Durable() != before {
			t.Fatal("preflight installed a paid effect")
		}
		if err != nil {
			if valid {
				t.Fatalf("valid intent refused: %v", err)
			}
			return
		}
		if g.intent == nil || *g.intent != intent {
			t.Fatal("accepted an intent for a different reservation")
		}
		root := testRoot(t)
		if err := os.CopyFS(root, os.DirFS(prepared)); err != nil {
			t.Fatal(err)
		}
		store, _, err := ledgerstore.Open(root, ledger.Defined())
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if _, err := store.Submit(context.Background(), d[0], d[1:]); err != nil {
			t.Fatal(err)
		}
		s, _, err := Open(context.Background(), store, root, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		view, err := s.PaidAttemptStatus(context.Background(), PaidAttemptLookup{AttemptID: p.AttemptID})
		if err != nil || view.State != "sent" || view.HeldMicros != "120" || view.SettledMicros != "0" || view.Ended || view.Version != 2 {
			t.Fatalf("durable intent altered exposure: %+v %v", view, err)
		}
	})
}
