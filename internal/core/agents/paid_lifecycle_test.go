package agents

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func TestPaidNetworkUnknownAndSlotDrain(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"reset", "timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			entered, drained := make(chan struct{}), make(chan struct{})
			var posts atomic.Int64
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				close(entered)
				defer close(drained)
				if mode == "reset" {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					if tlsConn, ok := conn.(*tls.Conn); ok {
						if raw, ok := tlsConn.NetConn().(*net.TCPConn); ok {
							_ = raw.SetLinger(0)
						}
					}
					_ = conn.Close()
					return
				}
				<-r.Context().Done()
			})
			deps := paidServerDeps(server, nil)
			f := newLivePaidFixture(t, deps)
			f.service.paid.limits.MaxActivePaid = 1
			if mode == "timeout" {
				f.service.paid.transport.CloseIdleConnections()
				limits := providers.DefaultLimits()
				limits.ResponseHeaderTimeout = time.Second
				transport, err := providers.NewTransportWithRoots(limits, deps.paidTestRoots, nil)
				if err != nil {
					t.Fatal(err)
				}
				f.service.paid.transport = transport
			}
			r := f.prepare(t)
			f.admit(t, r)
			second := f.prepare(t)
			f.admit(t, second)
			d := PaidDispatch{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				o   PaidDispatchOutcome
				err error
			}
			done := make(chan result, 1)
			go func() { o, err := f.service.DispatchPaid(ctx, d); done <- result{o, err} }()
			<-entered
			if mode == "cancel" {
				before := f.store.Durable()
				_, err := f.service.DispatchPaid(context.Background(), PaidDispatch{CommandID: f.id(), AttemptID: second.AttemptID, ExpectedVersion: 1})
				var busy *BusyError
				if !errors.As(err, &busy) || f.store.Durable() != before || posts.Load() != 1 {
					t.Fatal("active slot limit failed before intent")
				}
				cancel()
			}
			got := <-done
			if got.err != nil {
				t.Fatal(got.err)
			}
			<-drained
			paidRecord(t, f, d, got.o)
			v := f.view(t, d.AttemptID)
			want := "unreachable"
			if mode == "timeout" {
				want = "timeout"
			}
			if v.State != "unknown" || v.HeldMicros == "0" || v.Result.ErrorClass != want || posts.Load() != 1 {
				t.Fatalf("fault result: state=%s class=%s", v.State, v.Result.ErrorClass)
			}
			if len(f.service.paid.exchanges) != 0 {
				t.Fatal("drained exchange retained active slot")
			}
			// Drained physical buffers no longer block an independent preparation.
			next, err := f.service.PrepareAnthropic(context.Background(), bindingOf(f.origin), f.input())
			if err != nil {
				t.Fatal(err)
			}
			_ = next.Close()
			if _, err := f.service.ReconcilePaid(context.Background(), PaidReconciliation{CommandID: f.id(), AttemptID: second.AttemptID, ExpectedVersion: 1}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func paidLateRefusalWitnessRow(t *testing.T) {
	t.Parallel()
	server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
		model := paidRequestModel(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Request-Id", "request-one")
		_, _ = io.WriteString(w, paidSSE(model, `{"input_tokens":7,"output_tokens":1}`, `{"output_tokens":1}`, "partial", "end_turn", "", false))
	})
	f := newLivePaidFixture(t, paidServerDeps(server, nil))
	f.liveProfile(t, func(p *providers.Profile) {
		p.ModelID = providers.Sonnet
		p.RequestedTier = "auto"
		p.NoChargeRules = []string{"anthropic_refusal_before_output_2026_09_24"}
	})
	_, d, out := paidRun(t, f)
	paidRecord(t, f, d, out)
	late := paidLateEvidence(t, f, out, func(o *paidObservationObject) {
		o.Complete = true
		o.OutputStarted = false
		o.ContentBlocks = 0
		o.Error = nil
		o.ErrorClass = "refused"
		o.StopReason = "refusal"
		o.StopDetails = json.RawMessage(`{"type":"refusal","category":null}`)
		o.RawUsageMerged["output_tokens"] = json.RawMessage(`0`)
		o.RawUsageFinal = json.RawMessage(`{"output_tokens":0}`)
	})
	if _, err := f.service.RecordPaidObservation(context.Background(), PaidObservation{CommandID: f.id(), AttemptID: d.AttemptID, ExpectedVersion: 3, TrustedEvidence: &late}); err != nil {
		t.Fatal(err)
	}
	v := f.view(t, d.AttemptID)
	if v.State != "unknown" || v.HeldMicros == "0" || v.Result.OutputStarted == nil || !*v.Result.OutputStarted {
		t.Fatal("later pre-output assertion erased an earlier output witness")
	}
}
