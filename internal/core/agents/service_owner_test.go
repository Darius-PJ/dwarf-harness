package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/terminal"
)

func TestServiceOwnerQueries(t *testing.T) {
	t.Parallel()
	t.Run("outage_refresh_and_filter_binding", func(t *testing.T) {
		t.Parallel()
		f, _ := serviceGolden(t, 223, serviceDeps{})
		all := serviceSnapshot(t, f.service)
		outage := all.Outages[0].OutageID
		q := ServiceOutageQuery{CallerID: uuid.NewV7(), OutageID: outage, Limit: 1}
		page, err := f.service.ServiceOutages(context.Background(), q)
		if err != nil || len(page.Rows) != 1 || page.Rows[0].OutageID != outage || page.Next == nil {
			t.Fatalf("incident refresh: %+v %v", page, err)
		}
		q.Next = page.Next
		q.OutageID = uuid.NewV7()
		if _, err := f.service.ServiceOutages(context.Background(), q); !errors.Is(err, ErrServicePageStale) {
			t.Fatalf("cursor moved to another incident: %v", err)
		}
		if f.store.Durable() != all.ThroughSequence {
			t.Fatal("passive query appended")
		}
	})
	t.Run("known_loss_wins_over_unknown_cause", func(t *testing.T) {
		t.Parallel()
		f, commits := serviceGolden(t, 150, serviceDeps{})
		e := serviceObservedAt(t, commits, 144)
		e.ObservationID, e.CauseID = uuid.NewV7(), uuid.NewV7()
		e.Source.ObservationID = e.ObservationID
		e.Source.Kind, e.Source.Reason = "future_failure", "future_denial"
		e.Source.ObservedThrough = int64(f.store.Durable())
		e.Cause = &ledger.ServiceCauseSpec{Family: "future", Code: "future", ScopeKind: "colony"}
		var err error
		e.CauseKey, err = e.Cause.Key()
		if err != nil {
			t.Fatal(err)
		}
		drafts, err := serviceDrafts(e.ObservationID, serviceRecordSubmission(e), servicePlan{next: f.service.p.service, observed: &e}, f.store.Durable()+1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
			t.Fatal(err)
		}
		serviceReopen(t, f, serviceDeps{})
		view, err := f.service.ServiceStatus(context.Background(), e.AgentID)
		if err != nil || view.State != "lost" || len(view.Active) != 1 || len(view.Unsupported) != 1 {
			t.Fatalf("unknown cause hid known loss: %+v %v", view, err)
		}
	})
}

func TestServiceSessionTerminal(t *testing.T) {
	t.Parallel()
	var healthy atomic.Bool
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		if healthy.Load() {
			paidProviderSuccess(t, "anthropic", w, r)
		} else {
			serviceProviderError("anthropic", http.StatusUnauthorized, "authentication_error", nil)(w, r)
		}
	}, nil)
	peer := registerPrivate(t, f.service, 916000)
	peerSetting := f.contextSetting(t, peer)
	f.raiseDispatchBound(t)
	f.enable(t, peerSetting)
	peerView := f.terminalView(t, peer)
	defer peerView.Close()
	cursor := f.store.Durable()
	h := runTerminal(t, []terminal.Agent{
		{AgentID: f.origin.AgentID, Session: f.terminalView(t, bindingOf(f.origin)), Contexts: []terminal.Context{{ContextID: f.origin.ContextID, Title: "General work"}}},
		{AgentID: peer.AgentID, Session: f.terminalView(t, peer), Contexts: []terminal.Context{{ContextID: peer.ContextID, Title: "General work"}}},
	}, false)
	h.wait(t, 0, "ready")
	call := func(view session.Session, contextID uuid.UUID) {
		t.Helper()
		command := turnMessage(contextID, "Bounded service observation.")
		if _, err := view.Do(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		f.finishedFor(t, view, command.Arguments.(session.MessageSend).TurnID)
	}
	call(f.view, f.origin.ContextID)
	h.wait(t, 0, "event outage.began")
	call(peerView, peer.ContextID)
	h.wait(t, 0, "event outage.updated", peer.AgentID.String())
	healthy.Store(true)
	call(f.view, f.origin.ContextID)
	call(peerView, peer.ContextID)
	i := h.wait(t, 0, "event outage.ended")
	before := f.store.Durable()
	h.send(t, "/service "+peer.AgentID.String())
	i = h.wait(t, i+1, "service agent="+peer.AgentID.String(), "state=available")
	h.send(t, "/outages")
	i = h.wait(t, i+1, "outage=", "agent="+peer.AgentID.String(), "phase=ended")
	query := session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "service.status", Arguments: session.ServiceStatus{}}
	raw, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	h.send(t, "/send "+string(raw))
	i = h.wait(t, i+1, "service agent="+f.origin.AgentID.String(), "state=available")
	h.send(t, fmt.Sprintf(`/send {"schema":1,"command_id":%q,"context_id":%q,"operation":"service.clear","arguments":{}}`, uuid.NewV7(), f.origin.ContextID))
	h.wait(t, i+1, "refused:")
	h.send(t, "/quit")
	if err := <-h.done; err != nil {
		t.Fatal(err)
	}
	text := strings.Join(h.all(), "\n")
	for _, kind := range []string{"outage.began", "outage.ended", "outage.updated"} {
		if strings.Count(text, "event "+kind) != 1 {
			t.Fatalf("two bound pumps repeated or lost %s: %s", kind, text)
		}
	}
	if f.store.Durable() != before {
		t.Fatal("owner queries or refused state-setting command appended")
	}
	for _, bound := range []struct {
		view  session.Session
		agent uuid.UUID
	}{{f.view, f.origin.AgentID}, {peerView, peer.AgentID}} {
		counts := map[string]int{}
		for next := cursor; next < before; {
			page, err := bound.view.Since(context.Background(), next, 128)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range page.Events {
				if strings.HasPrefix(event.Type, "outage.") || event.Type == "service.changed" {
					counts[event.Type]++
				}
				if event.Type == "service.changed" && event.AgentID != bound.agent || !strings.HasPrefix(event.Type, "outage.") && event.AgentID != bound.agent {
					t.Fatalf("foreign agent event crossed the session filter: %+v", event)
				}
			}
			next = page.ThroughSequence
		}
		if counts["service.changed"] != 2 || counts["outage.began"] != 1 || counts["outage.joined"] != 1 || counts["outage.ended"] != 1 {
			t.Fatalf("bound session lost service events: %v", counts)
		}
	}
	t.Run("bounded_stale_refresh", func(t *testing.T) {
		stale := &movingServiceHead{Session: f.view, service: f.service}
		owner := runTerminal(t, []terminal.Agent{{AgentID: f.origin.AgentID, Session: stale, Contexts: []terminal.Context{{ContextID: f.origin.ContextID, Title: "General work"}}}}, false)
		owner.wait(t, 0, "ready")
		owner.send(t, "/outages")
		owner.wait(t, 0, "outages", "/outages")
		owner.send(t, "/quit")
		if err := <-owner.done; err != nil {
			t.Fatal(err)
		}
		if stale.changes.Load() != 3 {
			t.Fatalf("refresh did not stop at its retry bound: %d head changes", stale.changes.Load())
		}
		text := strings.Join(owner.all(), "\n")
		if !strings.Contains(text, "outage=") || !strings.Contains(text, "agent=") {
			t.Fatal("bounded refresh discarded the rows it actually obtained")
		}
	})
}

// Every continuation advances the real ledger, so the real session rejects
// its cursor. No error or query result is fabricated by this wrapper.
type movingServiceHead struct {
	session.Session
	service *Service
	changes atomic.Int32
}

func (s *movingServiceHead) Do(ctx context.Context, command session.Command) (session.CommandResult, error) {
	if query, ok := command.Arguments.(session.ServiceOutages); ok {
		query.Limit = 1
		if query.After != nil {
			id := uuid.NewV7()
			if _, err := s.service.RegisterAgent(ctx, AgentRegistration{CommandID: uuid.NewV7(), AgentID: id, SessionID: uuid.NewV7(), ContextID: uuid.NewV7(), Directory: "Page-" + id.String(), AgentMarkdown: []byte("# Instructions\n")}); err != nil {
				return session.CommandResult{}, err
			}
			s.changes.Add(1)
		}
		command.Arguments = query
	}
	return s.Session.Do(ctx, command)
}
