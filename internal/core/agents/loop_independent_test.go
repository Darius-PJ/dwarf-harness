package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

func reopenIntegratedFixture(t *testing.T, f *turnFixture, settings ...LoopContextSettings) {
	t.Helper()
	deps := f.service.deps
	deps.afterLoopCommit, deps.beforeSubmit = nil, nil
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.workspace.Close(); err != nil {
		t.Fatal(err)
	}
	before := f.store.Durable()
	service, report, err := openPaidService(context.Background(), f.store, f.root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Issues) != 0 || len(report.PendingLoops) != 0 || f.store.Durable() != before {
		t.Fatalf("reopen executed or damaged completed work: %+v", report)
	}
	f.service = service
	ws, _, err := workspace.Open(context.Background(), f.store, f.root, workspace.Config{})
	if err != nil {
		t.Fatal(err)
	}
	f.workspace = ws
	t.Cleanup(func() { _ = f.service.Close(); _ = ws.Close() })
	version := uuid.NewV7()
	_, err = f.service.BindCredential(context.Background(), bindingOf(f.origin), CredentialBinding{CommandID: uuid.NewV7(), ScopeID: f.credential.ScopeID, ExpectedVersion: f.credential.Version, Provider: f.route.Profile.Provider, CredentialVersion: version, SourceKind: "environment", SourceLocator: paidTestEnvironment, RevokePreviousVersion: f.credential.CredentialVersion})
	if err != nil {
		t.Fatal(err)
	}
	f.credential = f.service.p.paidCredentials[version]
	f.enable(t, settings...)
}

func integratedHasReplay(provider string, request, replay []byte) bool {
	if provider == "anthropic" {
		var body struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if json.Unmarshal(request, &body) != nil {
			return false
		}
		matches := 0
		for _, message := range body.Messages {
			if message.Role == "assistant" && bytes.Equal(message.Content, replay) {
				matches++
			}
		}
		return matches == 1
	}
	var body struct {
		Input []json.RawMessage `json:"input"`
	}
	var items []json.RawMessage
	if json.Unmarshal(request, &body) != nil || json.Unmarshal(replay, &items) != nil || len(items) == 0 {
		return false
	}
	for _, item := range items {
		matches := 0
		for _, input := range body.Input {
			if bytes.Equal(input, item) {
				matches++
			}
		}
		if matches != 1 {
			return false
		}
	}
	return true
}

func testIndependentContinuations(t *testing.T, provider string) {
	t.Helper()
	type continuation struct {
		index int
		raw   []byte
	}
	entered := make(chan continuation, 3)
	interrupted := make(chan struct{})
	var gates [3]chan struct{}
	var releases [3]sync.Once
	for i := range gates {
		gates[i] = make(chan struct{})
	}
	free := func(i int) { releases[i].Do(func() { close(gates[i]) }) }
	defer func() {
		for i := range gates {
			free(i)
		}
	}()
	var posts [3]atomic.Int32
	var revisions [3]uint64
	var artifactRefs [3]ledger.ArtifactRef
	f := newTurnFixture(t, provider, func(w http.ResponseWriter, r *http.Request) {
		model, raw := turnRequest(t, r)
		index, natural, stopping := -1, false, false
		for i := range 3 {
			if bytes.Contains(raw, []byte(fmt.Sprintf("independent-next-%d", i))) {
				index, natural = i, true
				break
			}
			if bytes.Contains(raw, []byte(fmt.Sprintf("independent-interrupted-%d", i))) {
				index, stopping = i, true
				break
			}
			if bytes.Contains(raw, []byte(fmt.Sprintf("independent-input-%d", i))) {
				index = i
				break
			}
		}
		if index < 0 {
			t.Error("unexpected independent request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		call := posts[index].Add(1)
		for j := range 3 {
			if j == index {
				continue
			}
			for _, marker := range []string{fmt.Sprintf("independent-input-%d", j), fmt.Sprintf("independent-memory-%d", j), fmt.Sprintf("independent-artifact-%d", j), fmt.Sprintf("independent-history-%d", j), fmt.Sprintf("independent-final-%d", j), fmt.Sprintf("independent-call-%d", j), artifactRefs[j].SHA256.String()} {
				if bytes.Contains(raw, []byte(marker)) {
					t.Error("continuation disclosed a sibling input, memory, artifact, result or history")
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		if stopping {
			close(interrupted)
			<-r.Context().Done()
			return
		}
		if natural {
			for _, marker := range []string{fmt.Sprintf("independent-input-%d", index), fmt.Sprintf("independent-memory-%d", index), fmt.Sprintf("independent-history-%d", index), fmt.Sprintf("independent-final-%d", index)} {
				if !bytes.Contains(raw, []byte(marker)) {
					t.Error("reopened natural turn omitted its committed private transcript")
				}
			}
			if bytes.Contains(raw, []byte(fmt.Sprintf("independent-call-%d", index))) {
				t.Error("reopen resumed provider replay instead of rendering the committed transcript")
			}
			_, _ = io.WriteString(w, integratedSSE(provider, model, "", "", "", fmt.Sprintf("independent-next-answer-%d", index)))
			return
		}
		for _, marker := range []string{fmt.Sprintf("independent-memory-%d", index), fmt.Sprintf("independent-artifact-%d", index), fmt.Sprintf("independent-history-%d", index)} {
			if !bytes.Contains(raw, []byte(marker)) {
				t.Error("own private input source was omitted")
			}
		}
		if call == 1 {
			args := fmt.Sprintf(`{"schema":1,"action":"read","scope":"context","slot":"memory","revision":%d,"max_bytes":1024}`, revisions[index])
			_, _ = io.WriteString(w, integratedSSE(provider, model, fmt.Sprintf("independent-call-%d", index), "core_memory_v1", args, ""))
			return
		}
		if call != 2 {
			t.Error("independent turn repeated a model call")
			return
		}
		envelope := integratedLastTool(t, provider, raw)
		var memory integratedPrivateData
		if envelope.Error != nil || json.Unmarshal(modelJSON(envelope.Data), &memory) != nil || memory.Text == nil || *memory.Text != fmt.Sprintf("independent-memory-%d", index) {
			t.Error("continuation did not contain its actual native tool result")
		}
		entered <- continuation{index, bytes.Clone(raw)}
		select {
		case <-gates[index]:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, integratedSSE(provider, model, "", "", "", fmt.Sprintf("independent-final-%d", index)))
	}, nil)
	first, second := f.workSetting(t, 85000, "First independent task"), f.workSetting(t, 85010, "Second independent task")
	bindings := []Binding{bindingOf(f.origin), first.Binding, second.Binding}
	var grants [3]uuid.UUID
	for i, binding := range bindings {
		grants[i] = uuid.NewV7()
		capacity := paidPhysicalCapacity()
		capacity.BufferBytes, capacity.MaxOperationBytes = 512<<20, 4<<20
		_, err := f.service.IssueGrant(context.Background(), bindings[0], GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: grants[i], Grantee: binding, Operations: []string{"private.read", "private.write"}, Resources: []ledger.Resource{{Kind: "context", Binding: ledger.Binding(binding), Scope: "context", Slots: []string{"continuation", "handoff", "memory", "transcript"}}}, DataClass: "private", Recipients: []Binding{binding}, Limits: capacity, Lifetime: "until_revoked"}})
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := f.service.Snapshot(context.Background(), ReadBinding{Binding: binding, Scope: "context"})
		if err != nil {
			t.Fatal(err)
		}
		written, err := scopedBound(t, f.service, binding).WritePrivate(context.Background(), ScopedPrivateWrite{CommandID: uuid.NewV7(), GrantID: grants[i], Scope: "context", ExpectedVersion: snapshot.Revision, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte(fmt.Sprintf("independent-memory-%d", i))}, {Slot: "transcript", Mode: "append", Bytes: []byte(fmt.Sprintf(`{"schema":1,"entries":[],"legacy_marker":"independent-history-%d"}`, i))}}})
		if err != nil {
			t.Fatal(err)
		}
		revisions[i] = uint64(written.Version)
		task := uuid.NewV7()
		_, err = f.service.CreateTask(context.Background(), bindings[0], TaskCreation{CommandID: uuid.NewV7(), TaskID: task, Description: []byte(fmt.Sprintf("independent-artifact-%d", i)), AcceptanceSHA256: sha256.Sum256([]byte("synthetic acceptance")), Audience: []uuid.UUID{f.origin.AgentID}})
		if err != nil {
			t.Fatal(err)
		}
		status, err := f.service.TaskStatus(context.Background(), TaskLookup{TaskID: task})
		if err != nil {
			t.Fatal(err)
		}
		artifactRefs[i] = status.DescriptionRef
		_, err = f.service.IssueGrant(context.Background(), bindings[0], GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: uuid.NewV7(), Grantee: binding, Operations: []string{"artifact.read"}, Resources: []ledger.Resource{{Kind: "artifact", Artifact: artifactRefs[i]}}, DataClass: "artifact", Recipients: []Binding{binding}, Limits: capacity, Lifetime: "until_revoked"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.enable(t, first, second)
	views := make([]session.Session, 3)
	turns := make([]uuid.UUID, 3)
	for i, binding := range bindings {
		view, err := f.service.OpenSession(context.Background(), binding)
		if err != nil {
			t.Fatal(err)
		}
		defer view.Close()
		views[i] = view
		command := turnMessage(binding.ContextID, fmt.Sprintf("independent-input-%d", i))
		input := command.Arguments.(session.MessageSend)
		input.Artifacts = []ledger.ArtifactRef{artifactRefs[i]}
		command.Arguments = input
		started, err := view.Do(context.Background(), command)
		if err != nil {
			t.Fatal(err)
		}
		turns[i] = started.TurnID
	}
	var requests [3][]byte
	for range 3 {
		item := <-entered
		if requests[item.index] != nil {
			t.Fatal("duplicated continuation")
		}
		requests[item.index] = item.raw
	}
	capacity, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "agent", ID: f.origin.AgentID}})
	if err != nil || capacity.Held.TurnSlots != 3 {
		t.Fatal("three contexts did not actually overlap", err)
	}
	for _, i := range []int{2, 0, 1} {
		free(i)
		result, events := f.finishedFor(t, views[i], turns[i])
		if result.Outcome != "finished" || result.ModelCalls != 2 || result.ToolDispatches != 1 || posts[i].Load() != 2 {
			t.Fatal("independent turn did not finish its exact script", result)
		}
		var paid []ledger.RuntimePaidResult
		for _, event := range events {
			if value, ok := event.Data.(ledger.RuntimePaidResult); ok {
				paid = append(paid, value)
			}
		}
		if len(paid) != 2 || paid[0].EvidenceObject == nil {
			t.Fatal("missing provider observation")
		}
		agent := scopedBound(t, f.service, bindings[i])
		evidence, err := agent.ReadPaidObject(context.Background(), PaidObjectLookup{AttemptID: paid[0].AttemptID, GrantID: grants[i], Object: *paid[0].EvidenceObject, MaxBytes: maxObjectBytes})
		if err != nil {
			t.Fatal(err)
		}
		var observation paidObservationObject
		if json.Unmarshal(evidence, &observation) != nil || observation.ReplayObject == nil {
			t.Fatal("missing exact replay artifact")
		}
		replay, err := agent.ReadPaidObject(context.Background(), PaidObjectLookup{AttemptID: paid[0].AttemptID, GrantID: grants[i], Object: *observation.ReplayObject, MaxBytes: maxObjectBytes})
		if err != nil || !integratedHasReplay(provider, requests[i], replay) {
			t.Fatal("continuation did not carry its exact committed provider replay", err)
		}
		for j := range requests {
			if j != i && integratedHasReplay(provider, requests[j], replay) {
				t.Fatal("provider replay crossed contexts")
			}
		}
	}
	stop, err := views[2].Do(context.Background(), turnMessage(bindings[2].ContextID, "independent-interrupted-2"))
	if err != nil {
		t.Fatal(err)
	}
	<-interrupted
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	before := []int32{posts[0].Load(), posts[1].Load(), posts[2].Load()}
	reopenIntegratedFixture(t, f, first, second)
	for i := range posts {
		if posts[i].Load() != before[i] {
			t.Fatal("reopen resumed an interrupted or completed turn")
		}
	}
	for i, binding := range bindings {
		view, err := f.service.OpenSession(context.Background(), binding)
		if err != nil {
			t.Fatal(err)
		}
		defer view.Close()
		if i == 2 {
			result, _ := f.finishedFor(t, view, stop.TurnID)
			if result.Outcome != "interrupted" || result.ModelCalls != 1 {
				t.Fatal("shutdown did not retain one interrupted turn", result)
			}
		}
		started, err := view.Do(context.Background(), turnMessage(binding.ContextID, fmt.Sprintf("independent-next-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		result, _ := f.finishedFor(t, view, started.TurnID)
		if result.Outcome != "finished" || result.ModelCalls != 1 || posts[i].Load() != before[i]+1 {
			t.Fatal("new natural turn resumed old provider work", result)
		}
	}
}
