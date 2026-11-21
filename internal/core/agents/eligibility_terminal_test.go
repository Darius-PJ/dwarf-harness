package agents

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/terminal"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

// This is the executable's line terminal, real Sessions/stores and verified
// loopback TLS, not an IPC/GUI or live-provider claim. The provider seam is
// package-private; the executable's production startup intentionally has none.
func TestEligibilityTerminalSmoke(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var posts atomic.Int32
	entered, finishFirst := make(chan struct{}), make(chan struct{})
	var finishOnce sync.Once
	finish := func() { finishOnce.Do(func() { close(finishFirst) }) }
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		n := posts.Add(1)
		model, _ := turnRequest(t, r)
		if r.Method != http.MethodPost || r.TLS == nil {
			t.Error("provider request was not a real TLS POST")
		}
		answer := "Ordinary answer."
		if n == 1 {
			close(entered)
			select {
			case <-finishFirst:
			case <-r.Context().Done():
				return
			}
			answer = "Declared first answer."
		} else if n != 2 {
			t.Error("unexpected extra provider execution")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, turnAnthropic(model, nil, answer))
	}, nil)
	t.Cleanup(finish)

	// Give pre-first /status a real ended turn, without a preparation POST.
	f.liveSpend(t, ledger.PaidScope{Kind: "colony"}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000001", PeriodKind: "utc_day"})
	f.raiseDispatchBound(t)
	f.enable(t)
	prior, _ := f.send(t)
	priorTurn := prior.Arguments.(session.MessageSend).TurnID
	waitLoopState(t, f.view, f.origin.ContextID, priorTurn, "waiting_resource")
	if _, err := f.view.Do(ctx, session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: priorTurn}}); err != nil {
		t.Fatal(err)
	}
	f.finished(t, priorTurn)
	f.liveSpend(t, ledger.PaidScope{Kind: "colony"}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "100.000000", PeriodKind: "utc_day"})
	if posts.Load() != 0 {
		t.Fatal("status fixture dispatched paid work")
	}

	first := turnMessage(f.origin.ContextID, "Execute the predeclared first action.")
	request := firstActionRequest(f.origin, first, nil)
	requireFirstAction(t, f.service, request)
	baseline := f.store.Durable()
	firstTurn := request.FirstActionTurn.TurnID
	views := func(s *Service) []terminal.Agent {
		view, err := s.OpenSession(ctx, bindingOf(f.origin))
		if err != nil {
			t.Fatal(err)
		}
		return []terminal.Agent{{AgentID: f.origin.AgentID, Session: view, Contexts: []terminal.Context{{ContextID: f.origin.ContextID, Title: "General work"}}}}
	}
	send := func(h *terminalHarness, command session.Command) {
		t.Helper()
		if input, ok := command.Arguments.(session.MessageSend); ok && input.Artifacts == nil {
			input.Artifacts = []ledger.ArtifactRef{}
			command.Arguments = input
		}
		raw, err := json.Marshal(command)
		if err != nil {
			t.Fatal(err)
		}
		h.send(t, "/send "+string(raw))
	}
	contextStatus := func(h *terminalHarness) {
		t.Helper()
		send(h, session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "context.status", Arguments: session.ContextStatus{}})
	}
	identity := func(h *terminalHarness, from int) int {
		t.Helper()
		i := h.wait(t, from, "first-action action="+request.ActionID.String())
		return h.wait(t, i, "first-action-permitted", "context="+f.origin.ContextID.String(), "turn="+firstTurn.String(), "start="+first.CommandID.String(), "admission="+request.FirstActionTurn.AdmissionCommandID.String())
	}
	counts := func(stage string, s *Service, executions, results int) {
		t.Helper()
		got := map[string]int{
			"native":   eligibilityCount(t, s, baseline, ledger.TypeRuntimeWorkAdmitted),
			"bound":    eligibilityCount(t, s, baseline, ledger.TypeRuntimeTurnBound),
			"reserved": eligibilityCount(t, s, baseline, ledger.TypeRuntimePaidReserved),
			"intents":  eligibilityCount(t, s, baseline, ledger.TypeRuntimePaidDispatchIntent),
			"results":  eligibilityCount(t, s, baseline, ledger.TypeRuntimePaidResult),
			"posts":    int(posts.Load()),
		}
		t.Logf("COUNTS stage=%s baseline=%d through=%d native=%d bound=%d reserved=%d intents=%d results=%d posts=%d", stage, baseline, s.store.Durable(), got["native"], got["bound"], got["reserved"], got["intents"], got["results"], got["posts"])
		for kind, n := range got {
			want := executions
			if kind == "results" {
				want = results
			}
			if n != want {
				t.Fatalf("%s: %s=%d want=%d", stage, kind, n, want)
			}
		}
	}
	stop := func(name string, h *terminalHarness) {
		t.Helper()
		h.send(t, "/quit")
		if err := <-h.done; err != nil {
			t.Fatal(err)
		}
		for i, line := range h.all() {
			t.Logf("TRANSCRIPT view=%s line=%d %s", name, i+1, line)
		}
	}

	h := runTerminal(t, views(f.service), false)
	i := h.wait(t, 0, "ready")
	ordinary := turnMessage(f.origin.ContextID, "Ordinary input refused while held.")
	send(h, ordinary)
	i = h.wait(t, i+1, "refused:", "message.send", "eligibility held")
	i = h.wait(t, i+1, "state=first_action_required", "reason=first_action", "operation="+request.CommandID.String())
	i = identity(h, i)
	counts("ordinary-held", f.service, 0, 0)

	contextStatus(h)
	i = h.wait(t, i+1, "context revision=")
	i = identity(h, i)
	h.send(t, "/status "+priorTurn.String())
	i = identity(h, i+1)
	i = h.wait(t, i, terminalLabel(f.origin.AgentID, f.origin.ContextID, priorTurn), "status state=cancelled")
	counts("held-context-and-turn-status", f.service, 0, 0)
	if f.store.Durable() != baseline {
		t.Fatal("held sends or status appended work")
	}

	send(h, first)
	i = h.wait(t, i+1, "command message.send applied", "turn "+firstTurn.String())
	boundLine := h.line(i)
	<-entered
	counts("first-executing", f.service, 1, 0)
	second := runTerminal(t, views(f.service), false)
	j := second.wait(t, 0, "ready")
	send(second, first) // after the leader's acknowledgement, not admission-to-bind
	j = second.wait(t, j+1, "command message.send applied", "turn "+firstTurn.String())
	if second.line(j) != boundLine {
		t.Fatal("the second view returned another bound result", second.line(j), boundLine)
	}
	counts("second-view-repeat", f.service, 1, 0)
	finish()
	f.finished(t, firstTurn)
	i = h.wait(t, i+1, "final (durable): Declared first answer.")
	h.send(t, "/status "+firstTurn.String())
	i = h.wait(t, i+1, "first-action", "disposition=terminal")
	i = identity(h, i)
	i = h.wait(t, i, "status state=finished", "model_calls=1")
	gate := firstActionStatus(t, f.service, f.origin.AgentID)
	if gate.State != "first_action_required" || gate.FirstAction.FirstAdmittedTurn == nil || gate.FirstAction.FirstAdmittedTurn.TurnID != firstTurn || gate.FirstAction.Declaration.PermittedTurn.AdmissionCommandID != request.FirstActionTurn.AdmissionCommandID {
		t.Fatal("completion changed the declared or first-admitted identity")
	}
	counts("completed-inspection", f.service, 1, 1)

	releaseCursor := f.store.Durable()
	if _, err := f.service.ReleaseEligibility(ctx, EligibilityRelease{CommandID: uuid.NewV7(), AgentID: f.origin.AgentID, ExpectedVersion: gate.Version, ExpectedOperationID: gate.OperationID}); err != nil {
		t.Fatal(err)
	}
	t.Run("release_event_has_no_disposition", func(t *testing.T) {
		page, err := f.view.Since(ctx, releaseCursor, 128)
		if err != nil {
			t.Fatal(err)
		}
		var releases int
		for _, event := range page.Events {
			if event.Type != "eligibility.changed" {
				continue
			}
			releases++
			view, ok := event.Data.(session.EligibilityView)
			if !ok || view.State != "eligible" || view.Reason != "none" || view.Version != gate.Version+1 || view.OperationID != gate.OperationID || view.ThroughSequence != event.Sequence {
				t.Fatalf("release event lost its recorded gate: %+v", event)
			}
			first := view.FirstAction
			if first == nil || first.Declaration.ActionID != request.ActionID || first.Declaration.PermittedTurn.TurnID != firstTurn || first.Declaration.PermittedTurn.AdmissionCommandID != request.FirstActionTurn.AdmissionCommandID {
				t.Fatalf("release event lost its declaration: %+v", first)
			}
			if first.Disposition != "" || first.FirstAdmittedTurn != nil || first.FirstAdmittedSequence != 0 || first.NativeSequence != 0 || first.BoundSequence != 0 || first.NativeEndingSequence != 0 {
				t.Fatalf("release event fabricated derived first-action facts: %+v", first)
			}
		}
		if releases != 1 {
			t.Fatalf("release events=%d, want one", releases)
		}
		status := firstActionStatus(t, f.service, f.origin.AgentID)
		if status.State != "eligible" || status.FirstAction.Disposition != "terminal" || status.FirstAction.FirstAdmittedSequence == 0 || status.FirstAction.FirstAdmittedSequence != gate.FirstAction.FirstAdmittedSequence || status.FirstAction.FirstAdmittedTurn == nil || status.FirstAction.FirstAdmittedTurn.TurnID != firstTurn {
			t.Fatalf("status lost the completed first-action facts: %+v", status.FirstAction)
		}
		t.Logf("EVENT-VIEW disposition=empty derived_sequences=0 first_admitted_turn=nil; STATUS disposition=%s first_admitted_sequence=%d", status.FirstAction.Disposition, status.FirstAction.FirstAdmittedSequence)
	})
	eventTranscript := func(name string, h *terminalHarness, from int) int {
		t.Helper()
		start := h.wait(t, from, "event eligibility.changed")
		gateLine := h.wait(t, start+1, "eligibility state=eligible", "reason=none")
		end := identity(h, gateLine+1)
		for n := start; n <= end; n++ {
			line := h.line(n)
			t.Logf("EVENT-TRANSCRIPT view=%s line=%d %s", name, n+1, line)
			for _, derived := range []string{"disposition=", "_sequence=", "first-action-first-admitted"} {
				if strings.Contains(line, derived) {
					t.Fatalf("%s event display invented %q: %s", name, derived, line)
				}
			}
		}
		return end
	}
	i = eventTranscript("first", h, i+1)
	eventTranscript("second", second, j+1)
	contextStatus(h)
	i = h.wait(t, i+1, "context revision=")
	i = h.wait(t, i+1, "eligibility state=eligible", "reason=none")
	counts("released-without-send", f.service, 1, 1)
	fresh := turnMessage(f.origin.ContextID, "Explicit new ordinary input after release.")
	send(h, fresh)
	i = h.wait(t, i+1, "command message.send applied", "turn "+fresh.Arguments.(session.MessageSend).TurnID.String())
	f.finished(t, fresh.Arguments.(session.MessageSend).TurnID)
	h.wait(t, i+1, "final (durable): Ordinary answer.")
	counts("fresh-ordinary-completed", f.service, 2, 2)
	stop("first", h)
	stop("second", second)
	f.view.Close()
	deps := f.service.deps
	for _, close := range []func() error{f.service.Close, f.workspace.Close, f.store.Close} {
		if err := close(); err != nil {
			t.Fatal(err)
		}
	}

	store, _, err := ledgerstore.Open(f.root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	through := store.Durable()
	reopened, report, err := openPaidService(ctx, store, f.root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, deps)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(report.Issues) != 0 || len(report.PendingLoops) != 0 {
		t.Fatal("completed turns reopened as damaged or runnable work")
	}
	profile := workspace.ProvisionalCapacityProfile()
	ws, _, err := workspace.Open(ctx, store, f.root, workspace.Config{Capacity: &profile})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err := reopened.EnableTurnLoop(ctx, ws, LoopOptions{Contexts: []LoopContextSettings{f.setting}}); err != nil {
		t.Fatal(err)
	}
	attached := runTerminal(t, views(reopened), false)
	k := attached.wait(t, 0, "ready")
	contextStatus(attached)
	k = attached.wait(t, k+1, "context revision=")
	k = attached.wait(t, k+1, "eligibility state=eligible")
	attached.send(t, "/status "+firstTurn.String())
	k = identity(attached, k+1)
	attached.wait(t, k, "status state=finished", "model_calls=1")
	stop("reopened", attached)
	counts("reopen-attach-status", reopened, 2, 2)
	if store.Durable() != through {
		t.Fatal("reopen/attach/status wrote new records")
	}
	t.Logf("SMOKE first_turn=%s admission=%s action=%s reopened_new_posts=0", firstTurn, request.FirstActionTurn.AdmissionCommandID, request.ActionID)
}
