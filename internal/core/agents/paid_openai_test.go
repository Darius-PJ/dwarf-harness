package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func paidOpenAIProfile(p providers.Profile) providers.Profile {
	p.Provider, p.Path, p.APIVersion, p.ModelID = "openai", "/v1/responses", "v1", "gpt-6-luna"
	p.CapabilityVersion += "-openai"
	p.TariffVersion += "-openai"
	p.Region, p.Speed, p.RequestedTier = "global", "standard", "default"
	p.AllowedServedTiers = []string{"default"}
	p.ContextMax, p.InputMax, p.OutputMax = 1050000, 922000, 128000
	p.UsageSchema = "openai-responses-text-30m-kguard/v1"
	p.CacheClasses = []string{"input.cached.long", "input.cached.short", "input.write.long", "input.write.short"}
	p.NoChargeRules = []string{}
	p.Tariff.Rates = []providers.Rate{
		{Class: "input.cached.long", Numerator: "1", Denominator: "50"}, {Class: "input.cached.short", Numerator: "1", Denominator: "100"},
		{Class: "input.uncached.long", Numerator: "1", Denominator: "5"}, {Class: "input.uncached.short", Numerator: "1", Denominator: "10"},
		{Class: "input.write.long", Numerator: "1", Denominator: "4"}, {Class: "input.write.short", Numerator: "1", Denominator: "8"},
		{Class: "output.generated.long", Numerator: "3", Denominator: "4"}, {Class: "output.generated.short", Numerator: "1", Denominator: "2"},
	}
	return p
}

func newOpenAIPaidFixture(t testing.TB, deps serviceDeps) *paidFixture {
	t.Helper()
	f := newLivePaidFixture(t, deps)
	binding := CredentialBinding{CommandID: f.id(), ScopeID: f.id(), Provider: "openai", CredentialVersion: f.id(), SourceKind: "environment", SourceLocator: paidTestEnvironment}
	if _, err := f.service.BindCredential(context.Background(), bindingOf(f.origin), binding); err != nil {
		t.Fatal(err)
	}
	f.credential = f.service.p.paidCredentials[binding.CredentialVersion]
	p := paidOpenAIProfile(providerProfile(f.route.Profile))
	raw, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.ConfigurePaidRoute(context.Background(), bindingOf(f.origin), PaidRouteConfiguration{CommandID: f.id(), RouteID: f.route.RouteID, ExpectedVersion: f.route.Version, Enabled: true, CredentialScopeID: binding.ScopeID, Profile: raw}); err != nil {
		t.Fatal(err)
	}
	f.route = f.service.p.paidRoutes[f.route.RouteID]
	for _, scope := range []ledger.PaidScope{{Kind: "colony"}, {Kind: "agent", ID: f.origin.AgentID}, {Kind: "credential", ID: binding.ScopeID}, {Kind: "grant", ID: f.grant}} {
		f.liveSpend(t, scope, SpendTermsInput{Enabled: true, Mode: "finite", USD: "100", PeriodKind: "utc_day"})
	}
	return f
}

func paidOpenAIPrepare(t testing.TB, f *paidFixture, output int64) PaidAdmission {
	t.Helper()
	input := OpenAIInput(f.input())
	input.Request.MaxTokens = output
	p, err := f.service.PrepareOpenAI(context.Background(), bindingOf(f.origin), input)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return PaidAdmission{CommandID: f.id(), AttemptID: input.AttemptID, GrantID: input.GrantID, ParentTurnID: input.ParentTurnID, TaskBinding: input.TaskBinding, Prepared: p}
}

func paidOpenAIRun(t *testing.T, f *paidFixture, output int64) (ledger.RuntimePaidReserved, PaidDispatch, PaidDispatchOutcome) {
	t.Helper()
	r := paidOpenAIPrepare(t, f, output)
	_, bound := f.admit(t, r)
	a, err := bound.Admission()
	if err != nil {
		t.Fatal(err)
	}
	d := PaidDispatch{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1}
	out, err := f.service.DispatchPaid(context.Background(), d)
	if err != nil || out.ObservedPaidCall == nil || out.IntentReceipt.Outcome != "sent" {
		t.Fatalf("OpenAI dispatch: %v", err)
	}
	return a, d, out
}

func paidOpenAIUsage(input, cached, write, output int64) string {
	return fmt.Sprintf(`{"input_tokens":%d,"input_tokens_details":{"cached_tokens":%d,"cache_write_tokens":%d},"output_tokens":%d}`, input, cached, write, output)
}

const paidOpenAIText = `[{"type":"message","id":"message-one","status":"completed","role":"assistant","content":[{"type":"output_text","text":"answer","annotations":[],"logprobs":[]}]}]`
const paidOpenAIReasoning = `[{"type":"reasoning","id":"reasoning-one","summary":[{"type":"summary_text","text":"opaque summary"}],"encrypted_content":"synthetic-opaque","extension":{"kept":true}}]`

func paidOpenAIEvent(model, status, usage, output, extra string, sequence int) string {
	return fmt.Sprintf("event: response.%s\ndata: {\"type\":\"response.%s\",\"sequence_number\":%d,\"response\":{\"id\":\"response-one\",\"model\":%q,\"status\":%q,\"service_tier\":\"default\",\"usage\":%s,\"output\":%s%s}}\n\n", status, status, sequence, model, status, usage, output, extra)
}

func paidOpenAIReply(t *testing.T, w http.ResponseWriter, r *http.Request, usage string) {
	t.Helper()
	model := paidRequestModel(t, r)
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, paidOpenAIEvent(model, "completed", usage, paidOpenAIText, "", 1))
}

func paidOpenAIObservation(t *testing.T, f *paidFixture, id PaidDispatch) paidObservationObject {
	t.Helper()
	v := f.view(t, id.AttemptID)
	raw, err := f.service.ReadPaidObject(context.Background(), PaidObjectLookup{AttemptID: id.AttemptID, Object: *v.Result.EvidenceObject, MaxBytes: maxObjectBytes})
	if err != nil {
		t.Fatal(err)
	}
	var o paidObservationObject
	if err = json.Unmarshal(raw, &o); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestOpenAIBoundAndSettlement(t *testing.T) {
	t.Parallel()
	// Wrong band selection, cache/reasoning double charges, per-component
	// rounding, clamped actuals or current-tariff pricing change these amounts.
	for _, row := range []struct {
		name                                           string
		input, cached, write, output, cap, bound, cost int64
		held, fenced                                   bool
	}{
		{"inclusive", 20, 4, 8, 16, 16, 230512, 10, false, false},
		{"short-edge", 272000, 0, 0, 16, 16, 230512, 27208, false, false},
		{"ambiguous-low", 272001, 0, 0, 16, 16, 230512, 0, true, false},
		{"ambiguous-high", 278528, 0, 0, 16, 16, 230512, 0, true, false},
		{"long-edge", 278529, 0, 0, 16, 16, 230512, 55718, false, false},
		{"one-rounding", 1, 1, 0, 17, 17, 230513, 9, false, false},
		{"input-over-bound", 922001, 0, 0, 16, 16, 230512, 184413, false, true},
		{"cost-over-bound", 922000, 0, 922000, 17, 16, 230512, 230513, false, true},
		{"pinned-tariff", 20, 4, 8, 16, 16, 230512, 10, false, false},
		{"zero", 0, 0, 0, 0, 16, 230512, 0, false, false},
		{"region-rational", 20, 4, 8, 16, 16, 253564, 11, false, false},
		{"sol", 20, 4, 8, 16, 16, 4610240, 197, false, false},
		{"astra", 20, 4, 8, 16, 16, 23051200, 984, false, false},
		{"overflow", 9223372036854775807, 0, 0, 16, 16, 23051200, 0, true, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
				model := paidRequestModel(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, paidOpenAIEvent(model, "in_progress", paidOpenAIUsage(10, 2, 3, 4), "[]", "", 1))
				usage := paidOpenAIUsage(row.input, row.cached, row.write, row.output)
				if row.name == "inclusive" {
					usage = strings.TrimSuffix(usage, "}") + `,"output_tokens_details":{"reasoning_tokens":12},"total_tokens":36}`
				}
				output := paidOpenAIText
				if row.output == 0 {
					output = "[]"
				}
				_, _ = io.WriteString(w, paidOpenAIEvent(model, "completed", usage, output, "", 2))
			})
			f := newOpenAIPaidFixture(t, paidServerDeps(server, nil))
			if row.name == "sol" || row.name == "astra" || row.name == "overflow" {
				f.liveProfile(t, func(p *providers.Profile) {
					factor := int64(20)
					p.ModelID = "gpt-6-sol"
					if row.name != "sol" {
						factor = 100
						p.ModelID = "gpt-6-astra"
					}
					p.TariffVersion += "-" + row.name
					for i := range p.Tariff.Rates {
						r := &p.Tariff.Rates[i]
						n, _ := new(big.Int).SetString(r.Numerator, 10)
						r.Numerator = n.Mul(n, big.NewInt(factor)).String()
					}
				})
			}
			if row.name == "region-rational" {
				f.liveProfile(t, func(p *providers.Profile) {
					p.TariffVersion += "-uplift"
					for i := range p.Tariff.Rates {
						r := &p.Tariff.Rates[i]
						n, _ := new(big.Int).SetString(r.Numerator, 10)
						d, _ := new(big.Int).SetString(r.Denominator, 10)
						r.Numerator, r.Denominator = n.Mul(n, big.NewInt(11)).String(), d.Mul(d, big.NewInt(10)).String()
					}
				})
			}
			a, d, out := paidOpenAIRun(t, f, row.cap)
			if a.Quote.MaximumMicros != row.bound || f.view(t, d.AttemptID).HeldMicros != strconv.FormatInt(row.bound, 10) {
				t.Fatal("incorrect rectangular admission hold")
			}
			if row.name == "pinned-tariff" {
				f.liveProfile(t, func(p *providers.Profile) {
					p.TariffVersion += "-later"
					for i := range p.Tariff.Rates {
						p.Tariff.Rates[i].Numerator = "100"
					}
				})
			}
			paidRecord(t, f, d, out)
			v := f.view(t, d.AttemptID)
			if v.SettledMicros != strconv.FormatInt(row.cost, 10) || (v.HeldMicros != "0") != row.held || v.Fenced != row.fenced {
				t.Fatalf("money: state=%s cost=%s held=%s fenced=%v", v.State, v.SettledMicros, v.HeldMicros, v.Fenced)
			}
			if row.held {
				if v.HeldMicros != strconv.FormatInt(row.bound, 10) {
					t.Fatal("unpriceable result released hold")
				}
				if row.name == "overflow" {
					if v.State != "unknown" || v.Result.ErrorClass != "protocol_error" {
						t.Fatal("overflow became a price")
					}
				} else {
					if v.Result.ErrorClass != "tariff_ambiguous" {
						t.Fatal("ambiguous tariff misclassified")
					}
					o := paidOpenAIObservation(t, f, d)
					if o.Error == nil || o.Error.Type != "threshold_ambiguous" || string(o.ServedTier) != `"default"` || !bytes.Contains(o.RawUsageFinal, []byte(strconv.FormatInt(row.input, 10))) {
						t.Fatal("ambiguity evidence lost")
					}
				}
			} else {
				band := "short"
				if row.input >= 278529 {
					band = "long"
				}
				want := map[string]int64{"input.cached." + band: row.cached, "input.uncached." + band: row.input - row.cached - row.write, "input.write." + band: row.write, "output.generated." + band: row.output}
				if v.Result.Usage == nil || len(v.Result.Usage.Classes) != 8 || v.Result.Usage.PricingTier != "default_"+band || v.Result.Usage.ServedTier != "default" {
					t.Fatal("usage class universe or tier changed")
				}
				for _, u := range v.Result.Usage.Classes {
					if u.Units != want[u.Class] {
						t.Fatalf("exclusive class %s=%d", u.Class, u.Units)
					}
				}
			}
			reopenScoped(t, f.serviceFixture, DefaultLimits())
			replayed := f.view(t, d.AttemptID)
			if replayed.State != v.State || replayed.SettledMicros != v.SettledMicros || replayed.HeldMicros != v.HeldMicros || replayed.Fenced != v.Fenced {
				t.Fatal("replay changed money")
			}
		})
	}
}

func TestOpenAIUsageV2SettlementAndReplay(t *testing.T) {
	t.Parallel()
	// A real loopback terminal must release the hold, and a plain reopen must
	// keep the v2 quote supported and the exact four-micro settlement intact.
	const usage = `{"input_tokens":9,"input_tokens_details":{"cache_write_tokens":0,"cache_write_tokens_details":{"ttl_12h_tokens":0,"ttl_30m_tokens":0},"cached_tokens":0},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":14}`
	const tools = `,"tool_usage":{"image_gen":{"input_tokens":0,"input_tokens_details":{"image_tokens":0,"text_tokens":0},"output_tokens":0,"output_tokens_details":{"image_tokens":0,"text_tokens":0},"total_tokens":0},"web_search":{"num_requests":0}}`
	server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
		model := paidRequestModel(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, paidOpenAIEvent(model, "completed", usage, paidOpenAIText, tools, 1))
	})
	f := newOpenAIPaidFixture(t, paidServerDeps(server, nil))
	f.liveProfile(t, func(p *providers.Profile) {
		p.UsageSchema = "openai-responses-text-30m-kguard/v2"
		p.CapabilityVersion += "-v2"
	})
	_, d, out := paidOpenAIRun(t, f, 16)
	paidRecord(t, f, d, out)
	v := f.view(t, d.AttemptID)
	if v.State != "settled" || v.SettledMicros != "4" || v.HeldMicros != "0" || v.Fenced {
		t.Fatalf("v2 settlement: state=%s cost=%s held=%s fenced=%v", v.State, v.SettledMicros, v.HeldMicros, v.Fenced)
	}
	if v.Result.Usage == nil || v.Result.Usage.PricingTier != "default_short" || len(v.Result.Usage.Classes) != 8 {
		t.Fatal("v2 settlement lost short-band usage")
	}
	want := map[string]int64{"input.uncached.short": 9, "output.generated.short": 5}
	for _, u := range v.Result.Usage.Classes {
		if u.Units != want[u.Class] {
			t.Fatalf("v2 settlement units %s=%d", u.Class, u.Units)
		}
	}
	reopenScoped(t, f.serviceFixture, DefaultLimits())
	replayed := f.view(t, d.AttemptID)
	if replayed.State != "settled" || replayed.SettledMicros != "4" || replayed.HeldMicros != "0" || replayed.Fenced {
		t.Fatalf("v2 replay: state=%s cost=%s held=%s fenced=%v", replayed.State, replayed.SettledMicros, replayed.HeldMicros, replayed.Fenced)
	}
}

func TestOpenAIPaidEvidenceStates(t *testing.T) {
	t.Parallel()
	// Terminal nulls cannot borrow snapshot counters; non-success is not free,
	// unknown billing must hold, and explicit-zero extensions must not hold.
	for _, row := range []struct {
		name, state, cost, class string
		outputs                  int
		fence                    bool
	}{
		{"completed", "settled", "10", "", 1, false},
		{"incomplete-reasoning", "settled", "10", "", 1, false},
		{"incomplete-missing", "unknown", "0", "protocol_error", 0, false},
		{"cached-null", "unknown", "0", "protocol_error", 0, false},
		{"write-null", "unknown", "0", "protocol_error", 0, false},
		{"cached-missing", "unknown", "0", "protocol_error", 0, false},
		{"write-missing", "unknown", "0", "protocol_error", 0, false},
		{"zero-extensions", "settled", "10", "", 1, false},
		{"null-extensions", "settled", "10", "", 1, false},
		{"unknown-positive", "unknown", "0", "protocol_error", 0, true},
		{"tools-positive", "unknown", "0", "protocol_error", 0, true},
		{"tier", "unknown", "0", "protocol_error", 0, true},
		{"tier-missing", "unknown", "0", "protocol_error", 0, false},
		{"inconsistent-cache", "unknown", "0", "protocol_error", 0, true},
		{"eof", "unknown", "0", "protocol_error", 0, false},
		{"unknown-event", "unknown", "0", "protocol_error", 0, true},
		{"refused", "settled", "10", "refused", 0, false},
		{"failed", "settled", "10", "provider_error", 0, false},
		{"denied", "unknown", "0", "denied", 0, false},
		{"quota", "unknown", "0", "exhausted", 0, false},
		{"rate", "unknown", "0", "rate_limited", 0, false},
		{"overload", "unknown", "0", "overloaded", 0, false},
		{"malformed-content", "settled", "10", "protocol_error", 0, false},
		{"wrong-model", "unknown", "0", "protocol_error", 0, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
				model := paidRequestModel(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				if status := map[string]int{"denied": 401, "quota": 429, "rate": 429, "overload": 503}[row.name]; status != 0 {
					w.WriteHeader(status)
					code := map[string]string{"quota": "credit_balance_exhausted", "rate": "slow_down", "overload": "server_is_overloaded"}[row.name]
					_, _ = fmt.Fprintf(w, `{"error":{"type":"synthetic_error","message":null,"code":%q,"param":null}}`, code)
					return
				}
				_, _ = io.WriteString(w, paidOpenAIEvent(model, "in_progress", paidOpenAIUsage(10, 2, 3, 4), "[]", "", 1))
				if row.name == "eof" {
					return
				}
				usage, output, status, extra := paidOpenAIUsage(20, 4, 8, 16), paidOpenAIText, "completed", ""
				switch row.name {
				case "incomplete-reasoning", "incomplete-missing":
					status, output, extra = "incomplete", paidOpenAIReasoning, `,"incomplete_details":{"reason":"max_output_tokens"}`
					if row.name == "incomplete-missing" {
						usage = "null"
					}
				case "cached-null":
					usage = strings.Replace(usage, `"cached_tokens":4`, `"cached_tokens":null`, 1)
				case "write-null":
					usage = strings.Replace(usage, `"cache_write_tokens":8`, `"cache_write_tokens":null`, 1)
				case "cached-missing":
					usage = strings.Replace(usage, `"cached_tokens":4,`, ``, 1)
				case "write-missing":
					usage = strings.Replace(usage, `,"cache_write_tokens":8`, ``, 1)
				case "zero-extensions":
					usage = strings.Replace(usage, `"cached_tokens":4`, `"future":0,"cached_tokens":4`, 1)
					usage = strings.TrimSuffix(usage, "}") + `,"future":0,"output_tokens_details":{"future":0}}`
					extra = `,"tool_usage":{"future":{"requests":0}}`
				case "null-extensions":
					usage = strings.TrimSuffix(usage, "}") + `,"future":null}`
					extra = `,"tool_usage":null`
				case "unknown-positive":
					usage = strings.TrimSuffix(usage, "}") + `,"future":1}`
				case "tools-positive":
					extra = `,"tool_usage":{"web_search":{"requests":1}}`
				case "inconsistent-cache":
					usage = paidOpenAIUsage(20, 14, 8, 16)
				case "unknown-event":
					_, _ = io.WriteString(w, "event: response.future\ndata: {\"type\":\"response.future\",\"sequence_number\":2}\n\n")
				case "refused":
					output = `[{"type":"message","id":"message-one","status":"completed","role":"assistant","content":[{"type":"refusal","refusal":"no"}]}]`
				case "failed":
					status, output = "failed", "[]"
				case "malformed-content":
					output = strings.Replace(output, `"annotations":[]`, `"annotations":[{"type":"unsupported"}]`, 1)
				case "wrong-model":
					model = "different-model"
				}
				body := paidOpenAIEvent(model, status, usage, output, extra, 3)
				if row.name == "tier" {
					body = strings.Replace(body, `"service_tier":"default"`, `"service_tier":"priority"`, 1)
				}
				if row.name == "tier-missing" {
					body = strings.Replace(body, `"service_tier":"default",`, ``, 1)
				}
				_, _ = io.WriteString(w, body)
			})
			f := newOpenAIPaidFixture(t, paidServerDeps(server, nil))
			_, d, out := paidOpenAIRun(t, f, 16)
			paidRecord(t, f, d, out)
			v := f.view(t, d.AttemptID)
			if v.State != row.state || v.SettledMicros != row.cost || v.Result.ErrorClass != row.class || len(v.Result.MessageObjects) != row.outputs || v.Fenced != row.fence || (v.HeldMicros == "230512") != (row.state == "unknown") {
				t.Fatalf("evidence result: state=%s cost=%s held=%s class=%s objects=%d fence=%v", v.State, v.SettledMicros, v.HeldMicros, v.Result.ErrorClass, len(v.Result.MessageObjects), v.Fenced)
			}
			if row.name == "incomplete-reasoning" {
				raw, err := f.service.ReadPaidObject(context.Background(), PaidObjectLookup{AttemptID: d.AttemptID, Object: v.Result.MessageObjects[0], MaxBytes: maxObjectBytes})
				if err != nil || !bytes.Contains(raw, []byte(`"artifact_kind":"responses_reasoning"`)) || !bytes.Contains(raw, []byte(`"extension":{"kept":true}`)) {
					t.Fatal("incomplete reasoning was lost or made text")
				}
			}
			if row.name == "quota" {
				o := paidOpenAIObservation(t, f, d)
				if o.Error == nil || string(o.Error.Code) != `"credit_balance_exhausted"` || string(o.Error.Param) != "null" {
					t.Fatal("structured error code/null lost")
				}
			}
		})
	}
}

func TestOpenAIPaidBoundResults(t *testing.T) {
	t.Parallel()
	// A late capture following a different bound view must never select that
	// view's context; the durable hold and intent must precede any socket byte.
	entered, release := make(chan struct{}), make(chan struct{})
	var writes atomic.Int64
	var minimum atomic.Uint64
	var f *paidFixture
	server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
		model := paidRequestModel(t, r)
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-Id", "synthetic-openai-request")
		w.Header().Set("Request-Id", "excluded-anthropic-header")
		w.Header().Set("Set-Cookie", "excluded")
		output := strings.TrimSuffix(paidOpenAIText, "]") + "," + strings.TrimPrefix(paidOpenAIReasoning, "[")
		_, _ = io.WriteString(w, paidOpenAIEvent(model, "completed", paidOpenAIUsage(20, 4, 8, 16), output, "", 1))
	})
	f = newOpenAIPaidFixture(t, paidServerDeps(server, func(c net.Conn) net.Conn {
		return &paidObservedConn{c, func() {
			writes.Add(1)
			if f.store.Durable() < minimum.Load() {
				t.Error("socket bytes before durable intent")
			}
		}}
	}))
	r := paidOpenAIPrepare(t, f, 16)
	_, bound := f.admit(t, r)
	a, _ := bound.Admission()
	if f.budget(t, "").HeldMicros != "230512" {
		t.Fatal("missing pre-send hold")
	}
	minimum.Store(f.store.Durable() + 3)
	d := PaidDispatch{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1}
	type result struct {
		out PaidDispatchOutcome
		err error
	}
	done := make(chan result, 1)
	go func() { out, err := f.service.DispatchPaid(context.Background(), d); done <- result{out, err} }()
	select {
	case <-entered:
	case got := <-done:
		t.Fatalf("dispatch did not enter server: %v", got.err)
	}
	other := scopedWorkContext(t, f.service, bindingOf(f.origin), 0xf701, 0xf702)
	reader := privateScopedGrant(other, 0xf703, paidPhysicalCapacity(), "private.read")
	issueScopedRoot(t, f.service, bindingOf(f.origin), 0xf704, reader)
	otherAgent := scopedBound(t, f.service, other)
	close(release)
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	paidRecord(t, f, d, got.out)
	v := f.view(t, d.AttemptID)
	if v.SettledMicros != "10" || v.HeldMicros != "0" || len(v.Result.MessageObjects) != 2 || writes.Load() == 0 {
		t.Fatal("bound result did not settle")
	}
	ownerReader := privateScopedGrant(bindingOf(f.origin), 0xf705, paidPhysicalCapacity(), "private.read")
	issueScopedRoot(t, f.service, bindingOf(f.origin), 0xf706, ownerReader)
	owner := scopedBound(t, f.service, bindingOf(f.origin))
	refs := append([]ledger.ObjectRef{a.InputObject, *v.Result.EvidenceObject}, v.Result.MessageObjects...)
	for _, ref := range refs {
		request := PaidObjectLookup{AttemptID: d.AttemptID, GrantID: ownerReader.GrantID, Object: ref, MaxBytes: maxObjectBytes}
		raw, err := owner.ReadPaidObject(context.Background(), request)
		if err != nil || ledger.Digest(sha256.Sum256(raw)) != ref.SHA256 {
			t.Fatal("origin cannot read its bound artifact")
		}
		request.GrantID = reader.GrantID
		if raw, err = otherAgent.ReadPaidObject(context.Background(), request); !errors.Is(err, ErrDenied) || raw != nil {
			t.Fatal("other context read bound artifact")
		}
	}
	o := paidOpenAIObservation(t, f, d)
	if o.ProviderRequestID != "synthetic-openai-request" {
		t.Fatal("wrong request-id header selected")
	}
	for _, h := range o.Headers {
		if h.Name == "request-id" || h.Name == "set-cookie" {
			t.Fatal("unlisted header retained")
		}
	}
}

func TestOpenAIPaidRefusalBeforeSend(t *testing.T) {
	t.Parallel()
	// Provider isolation, authority changes and mutated seals must fail before
	// either an admission/intent or a physical POST can escape.
	for _, name := range []string{"authority", "path", "model", "region", "tier", "feature", "wrong-entrance", "credential-provider", "revoked", "route-cas", "expired", "task-generation", "parent-ended", "admit-body", "admit-valid-body", "admit-quote", "intent-body", "intent-valid-body", "intent-header"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var posts atomic.Int64
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				paidOpenAIReply(t, w, r, paidOpenAIUsage(20, 4, 8, 16))
			})
			f := newOpenAIPaidFixture(t, paidServerDeps(server, nil))
			ctx, origin := context.Background(), bindingOf(f.origin)
			input := OpenAIInput(f.input())
			input.Request.MaxTokens = 16
			before := f.store.Durable()
			var err error
			switch name {
			case "authority", "path", "model", "region", "tier":
				p := providerProfile(f.route.Profile)
				p.CapabilityVersion += "-invalid"
				switch name {
				case "authority":
					p.Authority = "api.anthropic.com"
				case "path":
					p.Path = "/v1/messages"
				case "model":
					p.ModelID = "unpriced-model"
				case "region":
					p.Region = "us"
				case "tier":
					p.RequestedTier = "auto"
				}
				raw, marshalErr := json.Marshal(p)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				_, err = f.service.ConfigurePaidRoute(ctx, origin, PaidRouteConfiguration{CommandID: f.id(), RouteID: f.route.RouteID, ExpectedVersion: f.route.Version, Enabled: true, CredentialScopeID: f.credential.ScopeID, Profile: raw})
			case "wrong-entrance":
				_, err = f.service.PrepareAnthropic(ctx, origin, AnthropicInput(input))
			case "feature":
				input.Request.Messages[0].Content[0].Type = "image"
				_, err = f.service.PrepareOpenAI(ctx, origin, input)
			case "credential-provider":
				b := CredentialBinding{CommandID: f.id(), ScopeID: f.id(), Provider: "anthropic", CredentialVersion: f.id(), SourceKind: "environment", SourceLocator: paidTestEnvironment}
				if _, err = f.service.BindCredential(ctx, origin, b); err != nil {
					t.Fatal(err)
				}
				raw, _ := f.route.Profile.Encode()
				_, err = f.service.ConfigurePaidRoute(ctx, origin, PaidRouteConfiguration{CommandID: f.id(), RouteID: f.route.RouteID, ExpectedVersion: f.route.Version, Enabled: true, CredentialScopeID: b.ScopeID, Profile: raw})
				if err != nil {
					if posts.Load() != 0 {
						t.Fatal("provider mismatch sent a request")
					}
					return
				}
				input.ExpectedRouteVersion++
				input.CredentialVersion = b.CredentialVersion
				before = f.store.Durable()
				_, err = f.service.PrepareOpenAI(ctx, origin, input)
			default:
				r := paidOpenAIPrepare(t, f, 16)
				intent := strings.HasPrefix(name, "intent-")
				if intent {
					f.admit(t, r)
				}
				switch name {
				case "revoked":
					if _, err = f.service.RevokeCredential(ctx, origin, CredentialRevocation{CommandID: f.id(), ScopeID: f.credential.ScopeID, ExpectedVersion: f.credential.Version, Provider: "openai", CredentialVersion: f.credential.CredentialVersion}); err != nil {
						t.Fatal(err)
					}
				case "route-cas":
					f.liveProfile(t, func(p *providers.Profile) { p.Sources[0].Quote += " New synthetic facts." })
				case "expired":
					f.service.deps.reviewClock = func() (int64, error) { return f.route.Profile.ExpiresUTCNS, nil }
				case "task-generation":
					r.TaskBinding = &ledger.TaskBinding{TaskID: f.id(), AssignmentID: f.id(), Generation: 2}
				case "parent-ended":
					if _, err = f.service.EndTurn(ctx, TurnEnding{CommandID: f.id(), TurnID: f.turn, ExpectedVersion: 1, Outcome: "interrupted"}); err != nil {
						t.Fatal(err)
					}
				case "admit-body", "intent-body":
					r.Prepared.state.body[0] = '['
				case "admit-valid-body", "intent-valid-body":
					// Invalid JSON also fails secret scanning; retain valid
					// JSON here so only the seal binds these changed bytes.
					index := bytes.Index(r.Prepared.state.body, []byte("Synthetic request."))
					if index < 0 {
						t.Fatal("missing synthetic input")
					}
					r.Prepared.state.body[index] = 's'
				case "admit-quote":
					r.Prepared.state.admission.Quote.MaximumMicros--
				case "intent-header":
					r.Prepared.state.admission.Request.Headers[0].Value = "application/json"
				}
				before = f.store.Durable()
				if intent {
					_, err = f.service.DispatchPaid(ctx, PaidDispatch{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1})
				} else {
					_, _, err = scopedBound(t, f.service, origin).AdmitPaid(ctx, r)
				}
			}
			if err == nil || f.store.Durable() != before || posts.Load() != 0 {
				t.Fatalf("invalid OpenAI request reached admission or intent: %v", err)
			}
		})
	}
}

func TestOpenAIPaidCredentialPinning(t *testing.T) {
	// Process-state rows intentionally remain sequential. A source reread at
	// dispatch would send the replacement instead of the admitted credential.
	const source = "DWARF_OPENAI_PIN_SYNTHETIC"
	const pinned = "synthetic-openai-pinned-value"
	var posts atomic.Int64
	server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+pinned {
			t.Error("dispatch reread credential source")
		}
		paidOpenAIReply(t, w, r, paidOpenAIUsage(20, 4, 8, 16))
	})
	f := newOpenAIPaidFixture(t, paidServerDeps(server, nil))
	t.Setenv(source, pinned)
	b := CredentialBinding{CommandID: f.id(), ScopeID: f.credential.ScopeID, ExpectedVersion: f.credential.Version, Provider: "openai", CredentialVersion: f.id(), SourceKind: "environment", SourceLocator: source, RevokePreviousVersion: f.credential.CredentialVersion}
	if _, err := f.service.BindCredential(context.Background(), bindingOf(f.origin), b); err != nil {
		t.Fatal(err)
	}
	f.credential = f.service.p.paidCredentials[b.CredentialVersion]
	r := paidOpenAIPrepare(t, f, 16)
	f.admit(t, r)
	t.Setenv(source, "synthetic-openai-replacement-value")
	d := PaidDispatch{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1}
	out, err := f.service.DispatchPaid(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	paidRecord(t, f, d, out)
	if posts.Load() != 1 || f.view(t, d.AttemptID).SettledMicros != "10" {
		t.Fatal("pinned exchange did not settle")
	}
	paidAssertNoSecret(t, f.root, pinned)
}

func TestOpenAIPaidHistoricalEvidence(t *testing.T) {
	t.Parallel()
	// A trusted late capture is not permission to borrow start counters or to
	// ignore the served tier. Replaying a forged band must not release a hold.
	for _, name := range []string{"late-cached-null", "late-write-null", "late-missing-final", "late-tier", "replay-short-at-long", "replay-both-bands", "replay-pricing-tier"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
				paidOpenAIReply(t, w, r, paidOpenAIUsage(278529, 0, 0, 16))
			})
			f := newOpenAIPaidFixture(t, paidServerDeps(server, nil))
			_, d, out := paidOpenAIRun(t, f, 16)
			if strings.HasPrefix(name, "late-") {
				ref := paidLateEvidence(t, f, out, func(o *paidObservationObject) {
					o.RawUsageMerged = providers.Usage{"input_tokens": json.RawMessage(`20`), "output_tokens": json.RawMessage(`16`), "input_tokens_details": json.RawMessage(`{"cached_tokens":4,"cache_write_tokens":8}`)}
					switch name {
					case "late-cached-null":
						o.RawUsageFinal = json.RawMessage(`{"input_tokens":20,"input_tokens_details":{"cached_tokens":null,"cache_write_tokens":8},"output_tokens":16}`)
					case "late-write-null":
						o.RawUsageFinal = json.RawMessage(`{"input_tokens":20,"input_tokens_details":{"cached_tokens":4,"cache_write_tokens":null},"output_tokens":16}`)
					case "late-missing-final":
						o.RawUsageFinal = nil
					case "late-tier":
						o.ServedTier = json.RawMessage(`"flex"`)
					}
				})
				if _, err := f.service.RecordPaidObservation(context.Background(), PaidObservation{CommandID: f.id(), AttemptID: d.AttemptID, ExpectedVersion: 2, TrustedEvidence: &ref}); err != nil {
					t.Fatal(err)
				}
				v := f.view(t, d.AttemptID)
				if v.State != "unknown" || v.HeldMicros != "230512" || v.SettledMicros != "0" || v.Fenced != (name == "late-tier") {
					t.Fatal("late evidence inferred a price")
				}
				return
			}
			paidRecord(t, f, d, out)
			v := f.view(t, d.AttemptID)
			r := *v.Result
			r.Kind, r.ExpectedVersion, r.Version = "correction", 3, 4
			r.PreviousResultSequence, r.PreviousEvidenceSHA256 = int64(v.ResultSequence), v.Result.EvidenceSHA256
			r.EvidenceSHA256 = ledger.Digest(sha256.Sum256([]byte(name)))
			r.EvidenceObject = &ledger.ObjectRef{SHA256: r.EvidenceSHA256, Bytes: 17}
			switch name {
			case "replay-short-at-long":
				r.Usage.Classes[3].Units, r.Usage.Classes[2].Units = r.Usage.Classes[2].Units, 0
				r.Usage.Classes[7].Units, r.Usage.Classes[6].Units = r.Usage.Classes[6].Units, 0
				r.Usage.PricingTier = "default_short"
				r.CostMicros = new(int64(27861))
			case "replay-both-bands":
				r.Usage.Classes[3].Units = 1
			case "replay-pricing-tier":
				r.Usage.PricingTier = "default_short"
			}
			f.append(t, r)
			reopenScoped(t, f.serviceFixture, DefaultLimits())
			got := f.view(t, d.AttemptID)
			if got.State != "unknown" || got.SettledMicros != "55718" || got.HeldMicros != "0" {
				t.Fatal("unsupported historical band erased established money")
			}
		})
	}
}

func TestOpenAIPaidNewCaptureSecretSurfaces(t *testing.T) {
	t.Parallel()
	// Raw extension leaves and opaque reasoning are still subject to the
	// credential-disclosure boundary, including JSON-escaped occurrences.
	for _, field := range []string{"served-tier", "tool-usage", "error-code", "error-param", "reasoning"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			const syntheticValue = "synthetic-paid-integration-value"
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
				model := paidRequestModel(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				if strings.HasPrefix(field, "error-") {
					w.WriteHeader(429)
					_, _ = fmt.Fprintf(w, `{"error":{"type":"synthetic","message":"","%s":"synthetic-paid-integration-\u0076alue"}}`, strings.TrimPrefix(field, "error-"))
					return
				}
				output, extra := paidOpenAIReasoning, ""
				if field == "tool-usage" {
					extra = `,"tool_usage":{"synthetic-paid-integration-\u0076alue":1}`
				}
				if field == "reasoning" {
					output = strings.Replace(output, `"kept":true`, `"kept":"synthetic-paid-integration-\u0076alue"`, 1)
				}
				body := paidOpenAIEvent(model, "completed", paidOpenAIUsage(20, 4, 8, 16), output, extra, 1)
				if field == "served-tier" {
					body = strings.Replace(body, `"service_tier":"default"`, `"service_tier":"synthetic-paid-integration-\u0076alue"`, 1)
				}
				_, _ = io.WriteString(w, body)
			})
			f := newOpenAIPaidFixture(t, paidServerDeps(server, nil))
			_, d, out := paidOpenAIRun(t, f, 16)
			paidRecord(t, f, d, out)
			v := f.view(t, d.AttemptID)
			if v.State != "unknown" || v.HeldMicros != "230512" || len(v.Result.MessageObjects) != 0 {
				t.Fatal("credential-bearing evidence became a settled output")
			}
			paidAssertNoSecret(t, f.root, syntheticValue)
		})
	}
}
