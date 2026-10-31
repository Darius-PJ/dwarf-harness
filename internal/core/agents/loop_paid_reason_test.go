package agents

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

// TestTurnLoopPaidResultReason drives one real loopback turn per F3-13 result
// kind and checks the turn reason it gives: an unknown ending is an unresolved
// result, not invalid content, and keeps its hold; a no-charge terminal is a
// provider error; a billed result with a content error stays content_invalid.
func TestTurnLoopPaidResultReason(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, reason, state string
		profile             func(*providers.Profile)
		respond             func(t *testing.T, w http.ResponseWriter, model string)
	}{
		{"unknown_reset", "unresolved_result", "unknown", nil, func(t *testing.T, w http.ResponseWriter, _ string) {
			// The connection closes after the request arrived: delivery and
			// billing stay unknown.
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
		}},
		{"no_charge_refusal", "provider_error", "no_charge", func(p *providers.Profile) {
			p.ModelID, p.RequestedTier = providers.Sonnet, "auto"
			p.NoChargeRules = []string{"anthropic_refusal_before_output_2026_09_24"}
		}, func(_ *testing.T, w http.ResponseWriter, model string) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, paidSSE(model, `{"input_tokens":7,"output_tokens":0}`, `{"input_tokens":7,"output_tokens":0}`, "", "refusal", `{"type":"refusal","category":null}`, true))
		}},
		{"usage_content_error", "content_invalid", "settled", nil, func(_ *testing.T, w http.ResponseWriter, model string) {
			// Complete and billed, but tool_use without a tool call is a
			// content error.
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, paidSSE(model, `{"input_tokens":100,"output_tokens":1}`, `{"input_tokens":100,"output_tokens":20}`, "Text where a tool call was promised.", "tool_use", "", true))
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			var posts atomic.Int32
			f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
				model, _ := turnRequest(t, r)
				posts.Add(1)
				row.respond(t, w, model)
			}, nil)
			if row.profile != nil {
				f.liveProfile(t, row.profile)
				f.setting.ExpectedRouteVersion = f.route.Version
			}
			f.enable(t)
			_, start := f.send(t)
			result, events := f.finished(t, start.TurnID)
			if result.Outcome != "failed" || result.Reason != row.reason || result.ModelCalls != 1 || result.ToolDispatches != 0 || result.FinalMessageObject != nil || posts.Load() != 1 {
				t.Fatalf("paid %s ended the turn %s/%s after %d model calls, %d tool dispatches and %d POSTs", row.state, result.Outcome, result.Reason, result.ModelCalls, result.ToolDispatches, posts.Load())
			}
			for _, event := range events {
				if event.Type == "tool.started" {
					t.Fatal("a tool started after a paid result without usable output")
				}
			}
			admissions := loopPaidAdmissions(t, f.service)
			if len(admissions) != 1 {
				t.Fatal("the stopped turn admitted a replacement attempt")
			}
			attempt := admissions[0].AttemptID
			held := "0"
			if row.state == "unknown" {
				held = strconv.FormatInt(admissions[0].Quote.MaximumMicros, 10)
			}
			view, err := f.service.PaidAttemptStatus(ctx, PaidAttemptLookup{AttemptID: attempt})
			if err != nil || view.Intent == nil || !view.Ended || view.State != row.state || view.HeldMicros != held {
				t.Fatalf("attempt ended %s holding %s, want %s holding %s", view.State, view.HeldMicros, row.state, held)
			}
			budget, err := f.service.PaidBudgetStatus(ctx, PaidBudgetLookup{Scope: ledger.PaidScope{Kind: "colony"}})
			if err != nil || budget.HeldMicros != held || budget.SettledMicros != view.SettledMicros {
				t.Fatal("the colony budget disagrees with the attempt's ending")
			}
			reopened, report, err := Open(ctx, f.store, f.root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := reopened.Close(); err != nil {
					t.Error(err)
				}
			})
			if len(report.Issues) != 0 || len(report.PendingLoops) != 0 {
				t.Fatal("a fresh Open refused or reopened the failed turn's history")
			}
			if row.state == "unknown" {
				if len(report.PendingPaid) != 1 || report.PendingPaid[0].Admission.AttemptID != attempt || report.PendingPaid[0].HeldMicros != held {
					t.Fatal("a fresh Open lost the unknown attempt's hold")
				}
			} else if len(report.PendingPaid) != 0 {
				t.Fatal("a fresh Open reported a hold for an ended attempt")
			}
		})
	}
}
