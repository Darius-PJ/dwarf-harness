package agents

import (
	"context"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func TestEligibilitySharedSessionBoundary(t *testing.T) {
	t.Parallel()
	t.Run("two_views_before_native_commit", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		claimed, resume := make(chan struct{}), make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(resume) })
		var armed atomic.Bool
		var posts atomic.Int32
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			posts.Add(1)
			model, _ := turnRequest(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "first completed"))
		}, func(deps *serviceDeps) {
			deps.afterScopedClaim = func(sub ledger.ScopedSubmission) {
				if sub.Operation == "turn.start" && armed.CompareAndSwap(true, false) {
					close(claimed)
					<-resume
				}
			}
		})
		f.enable(t)
		other, err := f.service.OpenSession(ctx, bindingOf(f.origin))
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		command := turnMessage(f.origin.ContextID, "overlapping first send")
		request := firstActionRequest(f.origin, command, nil)
		requireFirstAction(t, f.service, request)
		before := f.store.Durable()
		armed.Store(true)
		result := make(chan session.CommandResult, 1)
		failure := make(chan error, 1)
		go func() { value, err := f.view.Do(ctx, command); result <- value; failure <- err }()
		<-claimed
		winner, err := other.Do(ctx, command)
		if err != nil {
			t.Fatal("second pre-native request did not resolve", err)
		}
		f.finished(t, request.FirstActionTurn.TurnID)
		once.Do(func() { close(resume) })
		retry := <-result
		if err := <-failure; err != nil || retry.ReceiptSequence != winner.ReceiptSequence {
			t.Fatal("pre-native duplicate failed to reuse winning binding", retry, winner, err)
		}
		if posts.Load() != 1 || eligibilityCount(t, f.service, before, ledger.TypeRuntimeWorkAdmitted) != 1 || eligibilityCount(t, f.service, before, ledger.TypeRuntimeTurnBound) != 1 {
			t.Fatal("pre-native overlap repeated first work")
		}
	})
	t.Run("two_views_before_native_unbound", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		claimed, published := make(chan struct{}), make(chan struct{})
		resumeNative, resumeBind := make(chan struct{}), make(chan struct{})
		var nativeOnce, bindOnce sync.Once
		defer nativeOnce.Do(func() { close(resumeNative) })
		defer bindOnce.Do(func() { close(resumeBind) })
		var armed atomic.Bool
		var objects, posts atomic.Int32
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			posts.Add(1)
			model, _ := turnRequest(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "first completed"))
		}, func(deps *serviceDeps) {
			deps.afterScopedClaim = func(sub ledger.ScopedSubmission) {
				if sub.Operation == "turn.start" && armed.CompareAndSwap(true, false) {
					close(claimed)
					<-resumeNative
				}
			}
			deps.afterPublish = func(_ Binding, slot string) {
				if slot == "loop_object" && objects.Add(1) == 1 {
					close(published)
					<-resumeBind
				}
			}
		})
		f.enable(t)
		other, err := f.service.OpenSession(ctx, bindingOf(f.origin))
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		command := turnMessage(f.origin.ContextID, "overlapping unbound first send")
		request := firstActionRequest(f.origin, command, nil)
		requireFirstAction(t, f.service, request)
		before := f.store.Durable()
		armed.Store(true)
		first, second := make(chan error, 1), make(chan error, 1)
		go func() { _, err := f.view.Do(ctx, command); first <- err }()
		<-claimed
		go func() { _, err := other.Do(ctx, command); second <- err }()
		<-published
		nativeOnce.Do(func() { close(resumeNative) })
		if err := <-first; err != ErrUnavailable {
			t.Fatal("pre-native loser did not report admitted-unbound", err)
		}
		status := firstActionStatus(t, f.service, f.origin.AgentID)
		if status.FirstAction.Disposition != "admitted_unbound" || status.FirstAction.NativeEndingSequence != 0 || posts.Load() != 0 {
			t.Fatal("duplicate ended another caller's native admission", status)
		}
		bindOnce.Do(func() { close(resumeBind) })
		if err := <-second; err != nil {
			t.Fatal("winning native admission could not bind", err)
		}
		f.finished(t, request.FirstActionTurn.TurnID)
		if posts.Load() != 1 || eligibilityCount(t, f.service, before, ledger.TypeRuntimeWorkAdmitted) != 1 || eligibilityCount(t, f.service, before, ledger.TypeRuntimeTurnBound) != 1 {
			t.Fatal("pre-native unbound overlap repeated first work")
		}
	})
	t.Run("two_views_same_first_send", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		published, resume := make(chan struct{}), make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(resume) })
		var posts, objects atomic.Int32
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			posts.Add(1)
			model, _ := turnRequest(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "first completed"))
		}, func(deps *serviceDeps) {
			deps.afterPublish = func(_ Binding, slot string) {
				if slot == "loop_object" && objects.Add(1) == 1 {
					close(published)
					<-resume
				}
			}
		})
		f.enable(t)
		other, err := f.service.OpenSession(ctx, bindingOf(f.origin))
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		command := turnMessage(f.origin.ContextID, "exact first input")
		request := firstActionRequest(f.origin, command, nil)
		requireFirstAction(t, f.service, request)
		before := f.store.Durable()
		result := make(chan session.CommandResult, 1)
		failure := make(chan error, 1)
		go func() { value, err := f.view.Do(ctx, command); result <- value; failure <- err }()
		<-published
		interim := firstActionStatus(t, f.service, f.origin.AgentID)
		if interim.FirstAction.Disposition != "admitted_unbound" || interim.FirstAction.NativeSequence == 0 || interim.FirstAction.BoundSequence != 0 {
			t.Fatal("native-to-bind window missing", interim)
		}
		follower, err := other.Do(ctx, command)
		if err != ErrUnavailable || follower.Outcome != "refused" || posts.Load() != 0 {
			t.Fatal("interim duplicate neither returned unavailable nor stayed inert", follower, err)
		}
		once.Do(func() { close(resume) })
		original := <-result
		if err := <-failure; err != nil {
			t.Fatal(err)
		}
		input := command.Arguments.(session.MessageSend)
		f.finished(t, input.TurnID)
		input.Artifacts = []ledger.ArtifactRef{}
		command.Arguments = input
		retry, err := other.Do(ctx, command)
		if err != nil || retry.ReceiptSequence != original.ReceiptSequence || retry.EffectSequence != original.EffectSequence || retry.TurnID != original.TurnID {
			t.Fatal("bound same-ID retry did not return original receipt", retry, original, err)
		}
		if posts.Load() != 1 || eligibilityCount(t, f.service, before, ledger.TypeRuntimeWorkAdmitted) != 1 || eligibilityCount(t, f.service, before, ledger.TypeRuntimeTurnBound) != 1 {
			t.Fatal("duplicate Session request admitted, bound or posted twice", posts.Load())
		}
		input.Text += " changed"
		command.Arguments = input
		if _, err := other.Do(ctx, command); err != ErrCommandMismatch {
			t.Fatal("bound receipt accepted changed input", err)
		}
		revokeFirstGrant(t, f, f.privateGrant)
		input.Text = "exact first input"
		command.Arguments = input
		if _, err := other.Do(ctx, command); err != ErrDenied {
			t.Fatal("same-ID receipt bypassed current disclosure authority", err)
		}
	})
	t.Run("context_redaction_and_current_inspect", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newTurnFixture(t, "anthropic", func(http.ResponseWriter, *http.Request) { t.Error("status posted") }, nil)
		other := f.workSetting(t, 52000, "Other context")
		f.enable(t, other)
		request := firstActionRequest(f.origin, turnMessage(f.origin.ContextID, "private first input"), nil)
		requireFirstAction(t, f.service, request)
		status := func(contextID uuid.UUID) session.ContextView {
			t.Helper()
			result, err := f.view.Do(ctx, session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: contextID, Operation: "context.status", Arguments: session.ContextStatus{}})
			if err != nil {
				t.Fatal(err)
			}
			return result.Data.(session.ContextView)
		}
		declared := status(f.origin.ContextID)
		if declared.Eligibility.FirstAction == nil || declared.Eligibility.State != "first_action_required" || declared.Eligibility.FirstAction.Declaration.ActionID != request.ActionID {
			t.Fatal("current inspect authority did not disclose declaration", declared)
		}
		unrelated := status(other.Binding.ContextID)
		if unrelated.Eligibility.FirstAction != nil || unrelated.Eligibility.State != "first_action_required" {
			t.Fatal("other context learned declaration or lost gate", unrelated)
		}
		revokeFirstGrant(t, f, f.privateGrant)
		redacted := status(f.origin.ContextID)
		if redacted.Eligibility.FirstAction != nil || redacted.Eligibility.State != "first_action_required" {
			t.Fatal("no-authority empty context lost held state or leaked declaration", redacted)
		}
	})
}
