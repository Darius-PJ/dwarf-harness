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

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func TestPaidNetworkUnknownAndSlotDrain(t *testing.T) {
	t.Parallel()
	t.Run("concurrent-exchanges", func(t *testing.T) {
		t.Parallel()
		firstEntered, secondEntered := make(chan struct{}), make(chan struct{})
		var posts atomic.Int64
		server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
			model := paidRequestModel(t, r)
			switch posts.Add(1) {
			case 1:
				close(firstEntered)
				select {
				case <-secondEntered:
				case <-r.Context().Done():
					return
				}
			case 2:
				close(secondEntered)
			default:
				t.Error("exchange was resent")
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Request-Id", "request-one")
			_, _ = io.WriteString(w, paidSSE(model, `{"input_tokens":7,"output_tokens":1}`, `{"output_tokens":4}`, "done", "end_turn", "", true))
		})
		f := newLivePaidFixture(t, paidServerDeps(server, nil))
		ctx := context.Background()
		binding := bindingOf(f.origin)
		capacity := paidPhysicalCapacity()
		capacity.BufferBytes = 256 << 20
		for _, scope := range []ledger.ScopeKey{{Kind: "colony"}, {Kind: "agent", ID: f.origin.AgentID}} {
			if _, err := f.service.ConfigureCapacity(ctx, binding, CapacitySpec{CommandID: f.id(), ExpectedVersion: uint64(f.service.p.book.accounts[scope].version), Scope: scope, Limits: capacity}); err != nil {
				t.Fatal(err)
			}
		}
		f.grant = f.id()
		if _, err := f.service.IssueGrant(ctx, binding, GrantIssue{CommandID: f.id(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: f.grant, Grantee: binding, Operations: []string{"model.call"}, Resources: []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + f.route.RouteID.String()}}, DataClass: "external", Recipients: []Binding{binding}, Limits: capacity, Lifetime: "until_revoked"}}); err != nil {
			t.Fatal(err)
		}
		f.liveSpend(t, ledger.PaidScope{Kind: "grant", ID: f.grant}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.100000", PeriodKind: "utc_day"})
		f.service.paid.limits.MaxActivePaid = 2
		f.service.paid.limits.DispatchTimeout = 10 * time.Second
		first, second := f.prepare(t), f.prepare(t)
		f.admit(t, first)
		f.admit(t, second)
		requests := []PaidDispatch{
			{CommandID: f.id(), AttemptID: first.AttemptID, ExpectedVersion: 1},
			{CommandID: f.id(), AttemptID: second.AttemptID, ExpectedVersion: 1},
		}
		type result struct {
			request PaidDispatch
			out     PaidDispatchOutcome
			err     error
		}
		done := make(chan result, 2)
		for i, request := range requests {
			go func() {
				out, err := f.service.DispatchPaid(ctx, request)
				done <- result{request, out, err}
			}()
			if i == 0 {
				select {
				case <-firstEntered:
				case got := <-done:
					t.Fatalf("first dispatch returned before its request arrived: %v", got.err)
				}
			}
		}
		results := []result{<-done, <-done}
		for _, got := range results {
			if got.err != nil {
				t.Errorf("concurrent dispatch: %v", got.err)
				continue
			}
			paidRecord(t, f, got.request, got.out)
			view := f.view(t, got.request.AttemptID)
			if view.State != "settled" || view.HeldMicros != "0" || view.Result.CostMicros == nil || *view.Result.CostMicros != 11 {
				t.Errorf("same-credential exchange did not settle: state=%s held=%s", view.State, view.HeldMicros)
			}
		}
		if posts.Load() != 2 {
			t.Errorf("server received %d POSTs, want exactly two", posts.Load())
		}
	})
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
