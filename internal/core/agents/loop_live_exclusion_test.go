package agents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func testLiveTurnExclusion(t *testing.T, provider string) {
	t.Helper()
	entered := make(chan int, 4)
	var gates [4]chan struct{}
	var once [4]sync.Once
	var posts [4]atomic.Int32
	for i := range gates {
		gates[i] = make(chan struct{})
	}
	free := func(i int) { once[i].Do(func() { close(gates[i]) }) }
	defer func() {
		for i := range gates {
			free(i)
		}
	}()
	f := newTurnFixture(t, provider, func(w http.ResponseWriter, r *http.Request) {
		model, raw := turnRequest(t, r)
		index := -1
		for i := range 4 {
			if bytes.Contains(raw, []byte(fmt.Sprintf("live-exclusion-%d", i))) {
				index = i
				break
			}
		}
		if index < 0 {
			t.Error("a refused turn was queued or sent")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		posts[index].Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		entered <- index
		select {
		case <-gates[index]:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, integratedSSE(provider, model, "", "", "", fmt.Sprintf("Live exclusion answer %d.", index)))
	}, nil)
	settings := []LoopContextSettings{f.workSetting(t, 86000, "First live task"), f.workSetting(t, 86010, "Second live task"), f.workSetting(t, 86020, "Fourth live task")}
	bindings := []Binding{bindingOf(f.origin), settings[0].Binding, settings[1].Binding, settings[2].Binding}
	scopes := []ledger.ScopeKey{{Kind: "colony"}, {Kind: "agent", ID: f.origin.AgentID}}
	for _, binding := range bindings {
		scopes = append(scopes, ledger.ScopeKey{Kind: "context", ID: binding.ContextID})
		limits := paidPhysicalCapacity()
		limits.TurnSlots, limits.BufferBytes, limits.MaxOperationBytes = 1, 512<<20, 4<<20
		if _, err := f.service.ConfigureCapacity(context.Background(), bindingOf(f.origin), CapacitySpec{CommandID: uuid.NewV7(), Scope: scopes[len(scopes)-1], Limits: limits}); err != nil {
			t.Fatal(err)
		}
	}
	baseline := make(map[ledger.ScopeKey]Exposure)
	check := func() {
		for _, scope := range scopes {
			view, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: scope})
			if err != nil {
				t.Fatal(err)
			}
			if view.Available == nil || view.Held.TurnSlots+view.Used.TurnSlots > view.Limits.TurnSlots || view.Held.LocalSlots+view.Used.LocalSlots > view.Limits.LocalSlots || view.Held.BufferBytes+view.Used.BufferBytes > view.Limits.BufferBytes || view.Held.LocalUnits+view.Used.LocalUnits > view.Limits.LocalUnits || view.Available.TurnSlots < 0 || view.Available.LocalSlots < 0 || view.Available.BufferBytes < 0 || view.Available.LocalUnits < 0 {
				t.Fatal("native reservation exceeded an effective ceiling", scope.Kind)
			}
		}
	}
	for _, scope := range scopes {
		view, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: scope})
		if err != nil {
			t.Fatal(err)
		}
		baseline[scope] = view.Held
	}
	f.enable(t, settings...)
	views := make([]session.Session, 4)
	turns := make([]uuid.UUID, 4)
	for i, binding := range bindings {
		view, err := f.service.OpenSession(context.Background(), binding)
		if err != nil {
			t.Fatal(err)
		}
		defer view.Close()
		views[i] = view
	}
	for i := range 3 {
		start, err := views[i].Do(context.Background(), turnMessage(bindings[i].ContextID, fmt.Sprintf("live-exclusion-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		turns[i] = start.TurnID
		if got := <-entered; got != i {
			t.Fatal("wrong live context reached its barrier")
		}
	}
	check()
	capacity, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: scopes[1]})
	if err != nil || capacity.Held.TurnSlots != 3 || capacity.Limits.TurnSlots != 3 {
		t.Fatal("fixture did not reach the actual three-turn agent limit", err)
	}
	refused := []session.Command{turnMessage(bindings[0].ContextID, "rejected-same-live-context"), turnMessage(bindings[3].ContextID, "rejected-fourth-live-context")}
	for i, index := range []int{0, 3} {
		_, err := views[index].Do(context.Background(), refused[i])
		var busy *BusyError
		if !errors.As(err, &busy) {
			t.Fatal("one-live-context or three-agent-turn exclusion was bypassed", err)
		}
	}
	check()
	free(0)
	first, _ := f.finishedFor(t, views[0], turns[0])
	if first.Outcome != "finished" || first.ModelCalls != 1 {
		t.Fatal(first)
	}
	fourth, err := views[3].Do(context.Background(), turnMessage(bindings[3].ContextID, "live-exclusion-3"))
	if err != nil {
		t.Fatal("a positively released native turn slot was not reusable", err)
	}
	turns[3] = fourth.TurnID
	if got := <-entered; got != 3 {
		t.Fatal("released capacity admitted a refused request instead of the new one")
	}
	check()
	capacity, err = f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: scopes[1]})
	if err != nil || capacity.Held.TurnSlots != 3 {
		t.Fatal("slot reuse overlapped or lost a live turn", err)
	}
	for _, i := range []int{3, 1, 2} {
		free(i)
		result, _ := f.finishedFor(t, views[i], turns[i])
		if result.Outcome != "finished" || result.ModelCalls != 1 || result.ToolDispatches != 0 {
			t.Fatal(result)
		}
	}
	for i := range posts {
		if posts[i].Load() != 1 {
			t.Fatal("native exclusion retained or duplicated a request")
		}
	}
	check()
	for _, scope := range scopes {
		view, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: scope})
		if err != nil || view.Held != baseline[scope] {
			t.Fatal("finished turns leaked native reservations", scope.Kind, err)
		}
	}
	if err := f.service.scan(context.Background(), 0, f.store.Durable(), func(commit ledger.Commit) error {
		for _, record := range commit.Records {
			for _, command := range refused {
				if record.TurnID == command.Arguments.(session.MessageSend).TurnID {
					t.Error("busy request was retained in native history")
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
