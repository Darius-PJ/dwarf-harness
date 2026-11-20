package agents

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func TestEligibilityInputBinding(t *testing.T) {
	t.Parallel()
	t.Run("canonical_owner_request", func(t *testing.T) {
		t.Parallel()
		f := newServiceFixture(t, serviceDeps{})
		binding := registerPrivate(t, f.service, 10)
		origin := ledger.Origin{AgentID: binding.AgentID, SessionID: f.service.p.agents[binding.AgentID].session, ContextID: binding.ContextID}
		command := turnMessage(binding.ContextID, "<é>&\n\"\\\u2028\u2029")
		input := command.Arguments.(session.MessageSend)
		input.Artifacts = []ledger.ArtifactRef{{SourceSequence: 7, SHA256: sha256.Sum256([]byte("first")), Bytes: 11}, {SourceSequence: 9, SHA256: sha256.Sum256([]byte("second")), Bytes: 3}}
		command.Arguments = input
		request := firstActionRequest(origin, command, nil)
		requireFirstAction(t, f.service, request)
		preimage := fmt.Sprintf(`{"schema":1,"origin":{"agent_id":"%s","session_id":"%s","context_id":"%s"},"command_id":"%s","turn_id":"%s","text":"<é>&\n\"\\\u2028\u2029","artifacts":[{"source_sequence":7,"sha256":"%s","bytes":11},{"source_sequence":9,"sha256":"%s","bytes":3}]}`, origin.AgentID, origin.SessionID, origin.ContextID, command.CommandID, input.TurnID, input.Artifacts[0].SHA256, input.Artifacts[1].SHA256)
		view := firstActionStatus(t, f.service, origin.AgentID)
		if view.FirstAction.Declaration.InitialTurn.InputSHA256 != ledger.Digest(sha256.Sum256([]byte(preimage))) {
			t.Fatal("owner digest changed field order, artifact encoding or JSON escaping")
		}
		before := f.store.Durable()
		input.Artifacts[0], input.Artifacts[1] = input.Artifacts[1], input.Artifacts[0]
		request.FirstActionTurn.Input = input
		if _, err := f.service.RequireFirstAction(context.Background(), request); err != ErrCommandMismatch || f.store.Durable() != before {
			t.Fatal("artifact order was normalized or changed meaning reused receipt", err)
		}
	})
	t.Run("session_mismatch", func(t *testing.T) {
		t.Parallel()
		f := newTurnFixture(t, "anthropic", func(http.ResponseWriter, *http.Request) { t.Error("mismatched request posted") }, nil)
		work := f.workSetting(t, 51000, "Other work")
		f.enable(t, work)
		command := turnMessage(f.origin.ContextID, "exact first input")
		request := firstActionRequest(f.origin, command, nil)
		requireFirstAction(t, f.service, request)
		before := f.store.Durable()
		for _, field := range []string{"text", "turn", "command", "context", "artifacts"} {
			wrong := command
			input := command.Arguments.(session.MessageSend)
			switch field {
			case "text":
				input.Text += " changed"
			case "turn":
				input.TurnID = uuid.NewV7()
			case "command":
				wrong.CommandID = uuid.NewV7()
			case "context":
				wrong.ContextID = work.Binding.ContextID
			case "artifacts":
				input.Artifacts = []ledger.ArtifactRef{{SourceSequence: 7, SHA256: sha256.Sum256([]byte("extra")), Bytes: 5}}
			}
			wrong.Arguments = input
			_, err := f.view.Do(context.Background(), wrong)
			if eligibilityCount(t, f.service, before, ledger.TypeRuntimeWorkAdmitted) != 0 {
				t.Fatal("mismatched Session input created native admission", field)
			}
			requireEligibilityHeld(t, err)
			if f.store.Durable() != before {
				t.Fatal("mismatch consumed a command or recorded a refusal", field)
			}
		}
	})
	t.Run("native_bind_mismatch", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newTurnFixture(t, "anthropic", func(http.ResponseWriter, *http.Request) { t.Error("manual binding dispatched") }, nil)
		agent := scopedBound(t, f.service, bindingOf(f.origin))
		ordinary, admission := uuid.NewV7(), uuid.NewV7()
		native, err := agent.StartTurn(ctx, TurnAdmission{CommandID: admission, TurnID: ordinary, GrantID: f.privateGrant})
		if err != nil {
			t.Fatal(err)
		}
		ref, err := f.service.publishLoopObject(ctx, f.origin, f.credential.CredentialVersion, []byte(`{"schema":1}`))
		if err != nil {
			t.Fatal(err)
		}
		request := firstActionRequest(f.origin, turnMessage(f.origin.ContextID, "different declared turn"), nil)
		requireFirstAction(t, f.service, request)
		sub := ledger.LoopSubmission{Schema: 6, Actor: "owner", Origin: f.origin, TurnID: ordinary, Operation: "loop.bind", Arguments: ledger.LoopBindArguments{TurnID: ordinary, AdmissionCommandID: admission, InputObject: ref, PolicyObject: ref, RenderObject: ref}}
		before := f.store.Durable()
		_, err = f.service.commitLoop(ctx, uuid.NewV7(), sub, nil)
		if err == nil || eligibilityCount(t, f.service, before, ledger.TypeRuntimeTurnBound) != 0 {
			t.Fatal("first-required accepted another ordinary turn binding")
		}
		requireEligibilityHeld(t, err)
		// Even a declared TurnID cannot substitute an admission for another turn.
		wrong := sub
		wrong.TurnID = request.FirstActionTurn.TurnID
		args := sub.Arguments.(ledger.LoopBindArguments)
		args.TurnID = wrong.TurnID
		wrong.Arguments = args
		if _, err := f.service.commitLoop(ctx, uuid.NewV7(), wrong, nil); err == nil || f.store.Durable() != before {
			t.Fatal("binding accepted another AdmissionCommandID")
		}
		// Codec-valid forbidden binding must also fail Open, without object reads.
		bound := ledger.RuntimeTurnBound{TurnID: ordinary, AdmissionSequence: int64(native.Sequence - 1), InputObject: ref, PolicyObject: ref, RenderObject: ref}
		drafts, err := loopDrafts(uuid.NewV7(), sub, before+1, []paidEncoding{bound}, []string{ledger.TypeRuntimeTurnBound})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.Submit(ctx, drafts[0], drafts[1:]); err != nil {
			t.Fatal(err)
		}
		opened, _, err := Open(ctx, f.store, f.root, DefaultLimits())
		if opened != nil {
			_ = opened.Close()
		}
		if err == nil {
			t.Fatal("Open accepted ordinary turn binding while first-required")
		}
	})
}
