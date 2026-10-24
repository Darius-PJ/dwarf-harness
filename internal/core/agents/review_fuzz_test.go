package agents

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func FuzzReviewGroupReplay(f *testing.F) {
	f.Add(byte(0), uint64(0), []byte(nil))
	f.Add(byte(0), uint64(1), []byte(nil))
	f.Add(byte(1), uint64(0), []byte(`{}`))
	f.Add(byte(2), uint64(0), []byte(`{}`))
	f.Add(byte(3), uint64(1), []byte(nil))
	f.Add(byte(4), uint64(0), []byte(nil))
	f.Add(byte(5), uint64(1), []byte(nil))
	fixture, commits := openReviewFixture(f, 18)
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
		if len(input) > ledger.MaxReviewPayloadBytes {
			return
		}
		d := scopedGoldenDrafts(commits[18])
		valid := false
		switch mode % 6 {
		case 0:
			v := scopedDecode(t, ledger.DecodeRuntimeReviewResult, d[1].Payload)
			v.ObservedUTCNS = fixture.service.p.reviewFloor - int64(value%256)
			d[1].Payload = testPayload(t, v)
			valid = value%256 == 0
		case 1:
			d[1].Payload = append([]byte(nil), input...)
		case 2:
			d[2].Payload = append([]byte(nil), input...)
		case 3:
			d[1].TypeVersion = int(value%256) + 1
			valid = d[1].TypeVersion == 1
		case 4:
			v := scopedDecode(t, ledger.DecodeRuntimeReviewResult, d[1].Payload)
			v.RequestSequence += int64(value % 256)
			d[1].Payload = testPayload(t, v)
			rewriteReviewEffect(t, d)
			valid = value%256 == 0
		case 5:
			d[1].ContextID[15] ^= byte(value)
			valid = byte(value) == 0
		}
		fixture.service.mu.Lock()
		_, _, g, err := decodeFamily(d, before+1)
		if err == nil {
			_, err = fixture.service.planReview(context.Background(), g, before+1)
		}
		unchanged := reflect.DeepEqual(fixture.service.p, baseline.p)
		fixture.service.mu.Unlock()
		if !unchanged || fixture.store.Durable() != before {
			t.Fatal("fuzz preflight installed state")
		}
		if err != nil {
			if valid {
				t.Fatalf("valid bounded group refused: %v", err)
			}
			return
		}
		original := scopedDecode(t, ledger.DecodeRuntimeReviewResult, commits[18].Records[1].Payload)
		actual := *g.result
		original.ObservedUTCNS = actual.ObservedUTCNS
		if !reflect.DeepEqual(original, actual) {
			t.Fatal("accepted a different result for the fixed request")
		}
		d[0].Payload = testPayload(t, g.command)
		d[1].Payload = testPayload(t, actual)
		d[2].Payload = testPayload(t, g.receipt)
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
		service, _, err := Open(context.Background(), store, root, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		view, err := service.ReviewStatus(context.Background(), ReviewLookup{RequestID: actual.RequestID})
		if err != nil || view.State != "rejected" || !view.Current || !reflect.DeepEqual(*view.Result, actual) {
			t.Fatalf("canonical replay lost reject: %+v %v", view, err)
		}
		book, err := service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
		if err != nil || book.Used.LocalUnits != 2 {
			t.Fatalf("canonical replay lost charge: %+v %v", book, err)
		}
	})
}
