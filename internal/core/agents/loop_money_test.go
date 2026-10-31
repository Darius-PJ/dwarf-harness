package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func loopPaidAdmissions(t *testing.T, s *Service) []ledger.RuntimePaidReserved {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []ledger.RuntimePaidReserved
	err := s.scan(context.Background(), 0, s.p.through, func(c ledger.Commit) error {
		for _, record := range c.Records {
			if record.Type == ledger.TypeRuntimePaidReserved {
				value, err := ledger.DecodeRuntimePaidReserved(record.Payload)
				if err != nil {
					return err
				}
				result = append(result, value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTurnLoopMoneyPerCall(t *testing.T) {
	t.Parallel()
	t.Run("released_unsent", func(t *testing.T) { testLoopPhysicalRelease(t, true) })
	for _, provider := range []string{"anthropic", "openai"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			first, second := make(chan struct{}), make(chan struct{})
			requests := make(chan []byte, 2)
			var posts atomic.Int32
			var arguments string
			f := newTurnFixture(t, provider, func(w http.ResponseWriter, r *http.Request) {
				model, raw := turnRequest(t, r)
				n := posts.Add(1)
				requests <- raw
				w.Header().Set("Content-Type", "text/event-stream")
				if n > 2 {
					t.Error("money continuation dispatched an unadmitted replacement")
					return
				}
				// Headers go out before the gate so a held exchange waits on its
				// body, never on the transport's response-header deadline.
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				if n == 1 {
					<-first
				} else {
					<-second
				}
				if provider == "anthropic" {
					var body string
					if n == 1 {
						body = turnAnthropic(model, turnCalls("core_memory_v1", arguments), "")
					} else {
						body = turnAnthropic(model, nil, "Final accounted answer.")
					}
					body = strings.ReplaceAll(body, `"output_tokens":20`, `"output_tokens":20,"output_tokens_details":{"thinking_tokens":7}`)
					_, _ = io.WriteString(w, body)
				} else {
					output := paidOpenAIText
					if n == 1 {
						output = fmt.Sprintf(`[{"type":"function_call","id":"money-function","call_id":"call-one","name":"core_memory_v1","arguments":%q,"status":"completed"}]`, arguments)
					}
					usage := `{"input_tokens":100,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens":20,"output_tokens_details":{"reasoning_tokens":7}}`
					_, _ = io.WriteString(w, paidOpenAIEvent(model, "completed", usage, output, "", 1))
				}
			}, nil)
			t.Cleanup(func() {
				for _, gate := range []chan struct{}{first, second} {
					select {
					case <-gate:
					default:
						close(gate)
					}
				}
			})
			// Fixture configuration precedes every admission and exchange.
			f.service.paid.limits.MaxActivePaid = 1
			const source = "Once-only retained source instructions."
			revision := f.seedMemory(t, source)
			arguments = fmt.Sprintf(`{"schema":1,"action":"write","scope":"context","slot":"memory","revision":%d,"text":"Native money-step result."}`, revision)
			peerBinding := registerPrivate(t, f.service, 63000)
			peer := f.contextSetting(t, peerBinding)
			f.enable(t, peer)
			peerView, err := f.service.OpenSession(context.Background(), peerBinding)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(peerView.Close)
			_, start := f.send(t)
			firstRaw := <-requests
			maximum, settled := int64(200096), int64(120)
			if provider == "openai" {
				maximum, settled = 52072, 20
			}
			checkBudget := func(held, charged int64) {
				t.Helper()
				view, err := f.service.PaidBudgetStatus(context.Background(), PaidBudgetLookup{Scope: ledger.PaidScope{Kind: "colony"}})
				if err != nil || view.HeldMicros != strconv.FormatInt(held, 10) || view.SettledMicros != strconv.FormatInt(charged, 10) {
					t.Fatal("inclusive per-call hold or settlement was omitted or counted twice")
				}
			}
			checkBudget(maximum, 0)
			peerCommand := turnMessage(peerBinding.ContextID, "Wait without acquiring a wallet hold.")
			waiting, err := peerView.Do(context.Background(), peerCommand)
			if err != nil {
				t.Fatal(err)
			}
			waitLoopState(t, peerView, peerBinding.ContextID, waiting.TurnID, "waiting_resource")
			for _, admission := range loopPaidAdmissions(t, f.service) {
				if admission.ParentTurnID == waiting.TurnID {
					t.Fatal("exchange saturation installed a paid hold before a send token")
				}
			}
			checkBudget(maximum, 0)
			_, err = peerView.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: peerBinding.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: waiting.TurnID}})
			if err != nil {
				t.Fatal(err)
			}
			cancelled, _ := f.finishedFor(t, peerView, waiting.TurnID)
			if cancelled.Outcome != "cancelled" || cancelled.ModelCalls != 0 {
				t.Fatal("waiting cancellation consumed a model dispatch")
			}
			close(first)
			secondRaw := <-requests
			checkBudget(maximum, settled)
			for index, raw := range [][]byte{firstRaw, secondRaw} {
				var request struct {
					Tools []struct {
						Name string `json:"name"`
					} `json:"tools"`
					Messages []struct {
						Role    string `json:"role"`
						Content []struct {
							Type string `json:"type"`
						} `json:"content"`
					} `json:"messages"`
					Input []struct {
						Type string `json:"type"`
					} `json:"input"`
				}
				if json.Unmarshal(raw, &request) != nil || len(request.Tools) != 9 || bytes.Count(raw, []byte(source)) != 1 {
					t.Fatal("tool definitions or retained source prompt were charged as duplicated input")
				}
				calls, results := 0, 0
				if provider == "anthropic" {
					for _, message := range request.Messages {
						for _, block := range message.Content {
							if block.Type == "tool_use" {
								calls++
							}
							if block.Type == "tool_result" {
								results++
							}
						}
					}
				} else {
					for _, item := range request.Input {
						if item.Type == "function_call" {
							calls++
						}
						if item.Type == "function_call_output" {
							results++
						}
					}
				}
				if calls != index || results != index {
					t.Fatal("continuation history did not count each prior call and result once")
				}
			}
			close(second)
			result, _ := f.finished(t, start.TurnID)
			if result.Outcome != "finished" || result.ModelCalls != 2 || result.ToolDispatches != 1 || posts.Load() != 2 {
				t.Fatal("tool continuation skipped a separately admitted model call")
			}
			checkBudget(0, 2*settled)
			admissions := loopPaidAdmissions(t, f.service)
			if len(admissions) != 2 || admissions[0].AttemptID == admissions[1].AttemptID {
				t.Fatal("continuation did not have two distinct native attempts")
			}
			for _, admission := range admissions {
				if admission.ParentTurnID != start.TurnID || admission.Quote.MaximumMicros != maximum || len(admission.Quote.Components) != 2 || admission.Quote.Components[0].MaxUnits != 196000 || admission.Quote.Components[1].MaxUnits != 4096 {
					t.Fatal("inclusive input/tool-prompt/history maximum or requested output was double-counted")
				}
				view, err := f.service.PaidAttemptStatus(context.Background(), PaidAttemptLookup{AttemptID: admission.AttemptID, VerifyInput: true})
				if err != nil || view.Intent == nil || view.State != "settled" || view.HeldMicros != "0" || view.SettledMicros != strconv.FormatInt(settled, 10) || view.Result == nil || view.Result.CostMicros == nil || *view.Result.CostMicros != settled {
					t.Fatal("attempt lacks its own send/evidence or adds reasoning to already-inclusive output")
				}
			}
		})
	}
}
