package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func integratedSSE(provider, model, callID, name, arguments, answer string) string {
	if provider == "anthropic" {
		var calls []struct{ ID, Name, Arguments string }
		if name != "" {
			calls = append(calls, struct{ ID, Name, Arguments string }{callID, name, arguments})
		}
		return turnAnthropic(model, calls, answer)
	}
	output := fmt.Sprintf(`[{"type":"message","id":"message-one","role":"assistant","status":"completed","content":[{"type":"output_text","text":%q,"annotations":[]}]}]`, answer)
	if name != "" {
		output = fmt.Sprintf(`[{"type":"function_call","id":%q,"call_id":%q,"name":%q,"arguments":%q,"status":"completed"}]`, "function-"+callID, callID, name, arguments)
	}
	return paidOpenAIEvent(model, "completed", paidOpenAIUsage(100, 0, 0, 20), output, "", 1)
}

func integratedLastTool(t *testing.T, provider string, raw []byte) loopToolEnvelope {
	t.Helper()
	var wire struct {
		Messages []struct {
			Content []struct {
				Type    string `json:"type"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"content"`
		} `json:"messages"`
		Input []struct {
			Type   string `json:"type"`
			Output string `json:"output"`
		} `json:"input"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Error(err)
		return loopToolEnvelope{}
	}
	var encoded string
	if provider == "anthropic" {
		for _, message := range wire.Messages {
			for _, block := range message.Content {
				if block.Type == "tool_result" {
					for _, text := range block.Content {
						encoded = text.Text
					}
				}
			}
		}
	} else {
		for _, item := range wire.Input {
			if item.Type == "function_call_output" {
				encoded = item.Output
			}
		}
	}
	var envelope loopToolEnvelope
	if encoded == "" || json.Unmarshal([]byte(encoded), &envelope) != nil {
		t.Error("continuation lacks its actual native result")
	}
	return envelope
}

func integratedMemory(t *testing.T, s *Service, binding Binding, scope string) (uint64, string) {
	t.Helper()
	view, err := s.Snapshot(context.Background(), ReadBinding{Binding: binding, Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.ReadPrivate(context.Background(), PrivateRead{ReadBinding: ReadBinding{Binding: binding, Scope: scope}, Slot: "memory", Revision: view.Revision, MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	return view.Revision, string(page.Bytes)
}

// Read only the model-facing fields; native receipt digests have producer-only
// JSON marshaling and are independently checked through the ledger decoder.
type integratedPrivateData struct {
	Revision uint64  `json:"revision"`
	Text     *string `json:"text"`
}

func testConcurrentMemoryCAS(t *testing.T, provider string) {
	t.Helper()
	reads := make(chan integratedPrivateData, 2)
	releaseWrites, releaseThird := make(chan struct{}), make(chan struct{})
	var writesOnce, thirdOnce sync.Once
	freeWrites := func() { writesOnce.Do(func() { close(releaseWrites) }) }
	freeThird := func() { thirdOnce.Do(func() { close(releaseThird) }) }
	defer freeWrites()
	defer freeThird()
	thirdEntered := make(chan struct{})
	var posts [3]atomic.Int32
	var conflicts atomic.Int32
	var winner atomic.Int32
	winner.Store(-1)
	var initial uint64
	const initialText = "shared baseline\n"
	readArgs := ""
	f := newTurnFixture(t, provider, func(w http.ResponseWriter, r *http.Request) {
		model, raw := turnRequest(t, r)
		index := -1
		for i := range 3 {
			if bytes.Contains(raw, []byte(fmt.Sprintf("shared-cas-%d", i))) {
				index = i
				break
			}
		}
		if index < 0 {
			t.Error("unexpected CAS request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		call := posts[index].Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		if index == 2 {
			close(thirdEntered)
			select {
			case <-releaseThird:
			case <-r.Context().Done():
				return
			}
			_, _ = io.WriteString(w, integratedSSE(provider, model, "", "", "", "Third overlapping context."))
			return
		}
		name, arguments, answer := "core_memory_v1", readArgs, ""
		switch call {
		case 1:
		case 2:
			envelope := integratedLastTool(t, provider, raw)
			var data integratedPrivateData
			if envelope.Error != nil || json.Unmarshal(modelJSON(envelope.Data), &data) != nil {
				reads <- data
				t.Errorf("initial native read failed: revision=%d envelope=%s", initial, modelJSON(envelope))
				return
			}
			reads <- data
			select {
			case <-releaseWrites:
			case <-r.Context().Done():
				return
			}
			arguments = fmt.Sprintf(`{"schema":1,"action":"write","scope":"agent","slot":"memory","revision":%d,"text":%q}`, data.Revision, initialText+fmt.Sprintf("contribution-%d\n", index))
		case 3:
			envelope := integratedLastTool(t, provider, raw)
			if envelope.Error == nil {
				var data integratedPrivateData
				if json.Unmarshal(modelJSON(envelope.Data), &data) != nil || data.Revision != initial+1 || !winner.CompareAndSwap(-1, int32(index)) {
					t.Error("two first writes applied or wrong winner revision")
				}
				name, arguments, answer = "", "", "The first CAS applied."
			} else {
				// PM answer 3: the existing correction carries no version. The
				// next scripted read supplies it; no runtime repair is invented.
				if string(modelJSON(envelope.Error)) != `{"code":"conflict"}` || envelope.Data != nil {
					t.Error("stale native refusal changed shape")
				}
				conflicts.Add(1)
				arguments = fmt.Sprintf(`{"schema":1,"action":"read","scope":"agent","slot":"memory","revision":%d,"max_bytes":1024}`, initial+1)
			}
		case 4:
			envelope := integratedLastTool(t, provider, raw)
			var data integratedPrivateData
			if envelope.Error != nil || json.Unmarshal(modelJSON(envelope.Data), &data) != nil || data.Text == nil || data.Revision != initial+1 || *data.Text != initialText+fmt.Sprintf("contribution-%d\n", 1-index) {
				t.Error("explicit reread did not return the winner's applied revision/body")
				return
			}
			arguments = fmt.Sprintf(`{"schema":1,"action":"write","scope":"agent","slot":"memory","revision":%d,"text":%q}`, data.Revision, *data.Text+fmt.Sprintf("contribution-%d\n", index))
		case 5:
			envelope := integratedLastTool(t, provider, raw)
			var data integratedPrivateData
			if envelope.Error != nil || json.Unmarshal(modelJSON(envelope.Data), &data) != nil || data.Revision != initial+2 {
				t.Error("explicit loser submission did not apply once")
			}
			name, arguments, answer = "", "", "Explicit reread and CAS preserved both contributions."
		default:
			t.Error("runtime issued an undeclared repair POST")
			return
		}
		_, _ = io.WriteString(w, integratedSSE(provider, model, fmt.Sprintf("cas-%d-%d", index, call), name, arguments, answer))
	}, nil)
	first, second := f.workSetting(t, 84000, "First CAS task"), f.workSetting(t, 84010, "Second CAS task")
	f.setting.Policy.MaxModelCalls, first.Policy.MaxModelCalls, second.Policy.MaxModelCalls = 5, 5, 5
	initial, _ = integratedMemory(t, f.service, bindingOf(f.origin), "agent")
	seeded, err := scopedBound(t, f.service, bindingOf(f.origin)).WritePrivate(context.Background(), ScopedPrivateWrite{CommandID: uuid.NewV7(), GrantID: f.privateGrant, Scope: "agent", ExpectedVersion: initial, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte(initialText)}}})
	if err != nil {
		t.Fatal(err)
	}
	initial = uint64(seeded.Version)
	readArgs = fmt.Sprintf(`{"schema":1,"action":"read","scope":"agent","slot":"memory","revision":%d,"max_bytes":1024}`, initial)
	before := f.store.Durable()
	f.enable(t, first, second)
	bindings := []Binding{bindingOf(f.origin), first.Binding, second.Binding}
	views := []session.Session{}
	turns := []uuid.UUID{}
	for _, binding := range bindings {
		view, err := f.service.OpenSession(context.Background(), binding)
		if err != nil {
			t.Fatal(err)
		}
		defer view.Close()
		views = append(views, view)
	}
	for _, i := range []int{2, 0, 1} {
		start, err := views[i].Do(context.Background(), turnMessage(bindings[i].ContextID, fmt.Sprintf("shared-cas-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		turns = append(turns, start.TurnID)
	}
	<-thirdEntered
	for range 2 {
		read := <-reads
		if read.Revision != initial || read.Text == nil || *read.Text != initialText {
			t.Fatal("contenders did not read the same initial memory")
		}
	}
	capacity, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "agent", ID: f.origin.AgentID}})
	if err != nil || capacity.Held.TurnSlots != 3 {
		t.Fatal("fixture lacks three actual overlapping turns", err)
	}
	freeWrites()
	for i := range 2 {
		result, events := f.finishedFor(t, views[i], turns[i+1])
		calls, tools := int64(3), int64(2)
		if posts[i].Load() == 5 {
			calls, tools = 5, 4
		}
		if result.Outcome != "finished" || result.ModelCalls != calls || result.ToolDispatches != tools || int64(posts[i].Load()) != calls {
			t.Fatal("CAS script was silently repaired or repeated", result)
		}
		for _, event := range events {
			if value, ok := event.Data.(ledger.RuntimeToolResult); ok && value.Outcome == "conflict" && value.EffectReceiptSequence != 0 {
				t.Fatal("stale refusal gained an effect")
			}
		}
	}
	freeThird()
	third, _ := f.finishedFor(t, views[2], turns[0])
	if third.Outcome != "finished" || third.ModelCalls != 1 || conflicts.Load() != 1 || winner.Load() < 0 || posts[0].Load()+posts[1].Load() != 8 {
		t.Fatal("wrong applied/stale/POST counts")
	}
	want := initialText + fmt.Sprintf("contribution-%d\ncontribution-%d\n", winner.Load(), 1-winner.Load())
	if revision, text := integratedMemory(t, f.service, bindings[0], "agent"); revision != initial+2 || text != want {
		t.Fatal("shared memory was implicitly merged or lost bytes", revision, text)
	}
	var receipts []ledger.RuntimeScopedReceipt
	var firstReceipt ScopedReceipt
	if err := f.service.scan(context.Background(), before, f.store.Durable(), func(commit ledger.Commit) error {
		for _, record := range commit.Records {
			if record.Type != ledger.TypeRuntimeScopedReceipt {
				continue
			}
			v := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, record.Payload)
			if v.Submission.Operation != "private.write" || v.Submission.Target.Scope != "agent" {
				continue
			}
			receipts = append(receipts, v)
			charges, revisions := 0, 0
			for _, effect := range commit.Records {
				if effect.Type == ledger.TypeRuntimeLocalUsed {
					charge := scopedDecode(t, ledger.DecodeRuntimeLocalUsed, effect.Payload)
					charges++
					if charge.Units != 1 || charge.GrantID != v.Submission.GrantID {
						t.Fatal("write command charged the wrong unit/grant")
					}
				}
				if effect.Type == ledger.TypePrivateScopedRevision {
					revisions++
				}
			}
			if charges != 1 || revisions != 1 {
				t.Fatal("applied CAS was not charged/revised exactly once")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 2 || receipts[0].Version != int64(initial+1) || receipts[1].Version != int64(initial+2) {
		t.Fatal("wrong native applied revision sequence")
	}
	v := receipts[0]
	request := ScopedPrivateWrite{CommandID: v.CommandID, GrantID: v.Submission.GrantID, Scope: "agent", ExpectedVersion: uint64(v.Submission.Expected.Version), Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte(initialText + fmt.Sprintf("contribution-%d\n", winner.Load()))}}}
	for restart := range 2 {
		if restart == 1 {
			if err := f.service.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, report, err := Open(context.Background(), f.store, f.root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			f.service = reopened
			if len(report.Issues) != 0 || len(report.PendingLoops) != 0 {
				t.Fatal("completed CAS reopened as work", report)
			}
		}
		through := f.store.Durable()
		capacityBefore, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "agent", ID: f.origin.AgentID}})
		if err != nil {
			t.Fatal(err)
		}
		retry, err := scopedBound(t, f.service, bindingOf(v.Submission.Origin)).WritePrivate(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if restart == 0 {
			firstReceipt = retry
		} else if !reflect.DeepEqual(retry, firstReceipt) {
			t.Fatal("reopen changed the historical command receipt")
		}
		capacityAfter, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "agent", ID: f.origin.AgentID}})
		if err != nil || !reflect.DeepEqual(capacityAfter, capacityBefore) || f.store.Durable() != through || retry.Sequence == 0 || retry.Version != v.Version {
			t.Fatal("same-ID retry consumed revision or capacity", err)
		}
		if revision, text := integratedMemory(t, f.service, bindings[0], "agent"); revision != initial+2 || text != want {
			t.Fatal("same-ID retry replaced newer memory")
		}
	}
}

func TestTurnLoopConcurrentPrivateRuntime(t *testing.T) {
	for _, provider := range []string{"anthropic", "openai"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			t.Run("shared_memory_cas", func(t *testing.T) { t.Parallel(); testConcurrentMemoryCAS(t, provider) })
			t.Run("independent_continuations", func(t *testing.T) { t.Parallel(); testIndependentContinuations(t, provider) })
			t.Run("live_turn_exclusion", func(t *testing.T) { t.Parallel(); testLiveTurnExclusion(t, provider) })
		})
	}
}
