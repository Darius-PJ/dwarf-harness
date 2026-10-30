package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func TestAnthropicZeroBlockSettlement(t *testing.T) {
	t.Parallel()
	const usage = `{"input_tokens":7,"output_tokens":1}`
	for _, mode := range []string{"live", "late"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
				model := paidRequestModel(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Request-Id", "request-one")
				final := usage
				if mode == "late" {
					final = `{}`
				}
				_, _ = io.WriteString(w, paidSSE(model, usage, final, "", "end_turn", "", mode == "live"))
			})
			f := newLivePaidFixture(t, paidServerDeps(server, nil))
			if book := f.budget(t, ""); book.HeldMicros != "0" || book.SettledMicros != "0" {
				t.Fatal("fixture already has paid exposure")
			}
			admission, d, out := paidRun(t, f)
			held := strconv.FormatInt(admission.Quote.MaximumMicros, 10)
			if book := f.budget(t, ""); book.HeldMicros != held || book.SettledMicros != "0" {
				t.Fatalf("dispatch did not retain the full hold: %+v", book)
			}
			receipt := paidRecord(t, f, d, out)
			request := PaidObservation{CommandID: receipt.CommandID, AttemptID: d.AttemptID, ExpectedVersion: 2, Observed: out.ObservedPaidCall}
			if mode == "late" {
				v := f.view(t, d.AttemptID)
				if v.State != "unknown" || v.Version != 3 || v.HeldMicros != held || v.SettledMicros != "0" {
					t.Fatalf("incomplete exchange released money: %+v", v)
				}
				if book := f.budget(t, ""); book.HeldMicros != held || book.SettledMicros != "0" {
					t.Fatalf("unknown result changed budget exposure: %+v", book)
				}
				late := paidLateEvidence(t, f, out, func(o *paidObservationObject) {
					o.Complete = true
					o.Error = nil
					o.ErrorClass = ""
					o.StopReason = "end_turn"
					o.ContentBlocks = 0
					o.OutputStarted = false
					o.RawUsageFinal = json.RawMessage(usage)
					o.RawUsageMerged = providers.Usage{"input_tokens": json.RawMessage(`7`), "output_tokens": json.RawMessage(`1`)}
				})
				request = PaidObservation{CommandID: f.id(), AttemptID: d.AttemptID, ExpectedVersion: 3, TrustedEvidence: &late}
				var err error
				receipt, err = f.service.RecordPaidObservation(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
			}
			assertSettled := func(t *testing.T) PaidAttemptView {
				t.Helper()
				v := f.view(t, d.AttemptID)
				// The fixture tariff is one micro per input or output token: 7 + 1.
				if v.State != "settled" || v.SettledMicros != "8" || v.HeldMicros != "0" {
					t.Fatalf("zero-block usage did not settle exactly once: %+v", v)
				}
				if v.Result == nil || v.Result.Kind != "usage" || v.Result.CostMicros == nil || *v.Result.CostMicros != 8 {
					t.Fatalf("missing eight-micro usage result: %+v", v.Result)
				}
				if v.Result.MessageObjects != nil || v.Result.OutputStarted == nil || *v.Result.OutputStarted {
					t.Fatal("zero-block result invented output or retained an empty object list")
				}
				if book := f.budget(t, ""); book.HeldMicros != "0" || book.SettledMicros != "8" {
					t.Fatalf("settlement did not charge eight micros and release the hold: %+v", book)
				}
				return v
			}
			settled := assertSettled(t)
			if bytes.Contains(paidEncode(t, receipt.Submission), []byte(`"message_objects"`)) {
				t.Fatal("zero-block submission contains message_objects")
			}
			cost, err := providers.PriceHistoricalUsage(providerProfile(f.route.Profile), []providers.Units{
				{Class: providers.CacheRead}, {Class: providers.CacheWrite1h}, {Class: providers.CacheWrite5m},
				{Class: providers.Uncached, Count: 7}, {Class: providers.Generated, Count: 1},
			})
			if err != nil || cost != 8 {
				t.Fatalf("historical tariff cross-check: cost=%d err=%v", cost, err)
			}
			// These rows share the settled fixture and must run in order.
			t.Run("duplicate", func(t *testing.T) {
				before := f.store.Durable()
				book := f.budget(t, "")
				again, err := f.service.RecordPaidObservation(context.Background(), request)
				if err != nil || again.Sequence != receipt.Sequence || f.store.Durable() != before {
					t.Fatalf("duplicate result appended another settlement: receipt=%+v err=%v", again, err)
				}
				if after := f.budget(t, ""); after.HeldMicros != book.HeldMicros || after.SettledMicros != book.SettledMicros {
					t.Fatalf("duplicate result moved money: before=%+v after=%+v", book, after)
				}
				assertSettled(t)
			})
			if mode == "live" {
				t.Run("reopen", func(t *testing.T) {
					reopenScoped(t, f.serviceFixture, DefaultLimits())
					replayed := assertSettled(t)
					if replayed.ResultSequence != settled.ResultSequence || !reflect.DeepEqual(replayed.Result, settled.Result) {
						t.Fatal("replay changed the committed zero-block result")
					}
				})
			}
		})
	}
}
