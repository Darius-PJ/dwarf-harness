package agents

import (
	"context"
	"errors"
	"maps"
	"math"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

type loopRuntime struct {
	service    *Service
	workspace  *workspace.Service
	options    LoopOptions
	settings   map[Binding]LoopContextSettings
	mu         sync.Mutex
	turns      map[uuid.UUID]*loopTurn
	agents     []uuid.UUID
	contexts   map[uuid.UUID][]uuid.UUID
	queued     map[uuid.UUID]bool
	wake       chan struct{}
	changed    chan struct{}
	generation atomic.Uint64
	releases   atomic.Uint64 // workspace releases; see released.
	completed  chan loopCompletion
	done       chan struct{}
	stopping   bool
	paidTokens map[uuid.UUID]struct{} // Service.mu; shared with ordinary paid dispatch.
	starting   int
}

type loopTurn struct {
	origin                ledger.Origin
	id                    uuid.UUID
	binding               *loopGroup
	settings              LoopContextSettings
	policy                TurnPolicy
	credential            uuid.UUID
	profile               ledger.Profile
	grant                 uuid.UUID
	reservation           scopedReservation
	request               providers.Request
	sources               []loopSource
	artifacts             []ledger.ArtifactRef
	ctx                   context.Context
	cancel                context.CancelFunc
	state, reason         string
	running               bool
	waitGeneration        uint64
	parkedReason          string
	modelCalls, toolCalls int64
	paid                  *loopPaidCall
	tools                 []loopInvocation
	toolResults           []providers.Block
	answer                *ledger.ObjectRef
	writers               map[uuid.UUID]*workspace.Writer
	live                  *loopLive
	cancelID              uuid.UUID
	cancelReceipt         *ScopedReceipt
	endingID              uuid.UUID
	ending                *loopEnding // the committed native ending; see endNative
	endCommandID          uuid.UUID
	done                  chan struct{}
	terminalError         error
}

type loopCompletion struct {
	turn           *loopTurn
	busy           bool
	finishing      bool
	waitGeneration uint64
	err            error
}

type loopPaidCall struct {
	id, admitID, dispatchID, observeID, reconcileID uuid.UUID
	grant                                           uuid.UUID
	prepared                                        *PreparedPaidCall
	admitted, counted                               bool
}

// EnableTurnLoop starts only this service incarnation's scheduler. Recovered
// bindings remain metadata-only, outside its ready rings.
func (s *Service) EnableTurnLoop(ctx context.Context, ws *workspace.Service, options LoopOptions) error {
	if s == nil || ws == nil {
		return ErrInvalid
	}
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.active.Done()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
		return err
	}
	if s.paid == nil || len(options.Contexts) == 0 || len(options.Contexts) > s.limits.MaxRegistrations {
		return ErrInvalid
	}
	if s.loopClosing {
		return ErrClosed
	}
	copy := LoopOptions{Contexts: slices.Clone(options.Contexts)}
	settings := make(map[Binding]LoopContextSettings, len(copy.Contexts))
	for i, setting := range copy.Contexts {
		if setting.TaskBinding != nil {
			setting.TaskBinding = new(*setting.TaskBinding)
			copy.Contexts[i] = setting
		}
		if settings[setting.Binding].RouteID != (uuid.UUID{}) || !validID(setting.RouteID) || !validID(setting.CredentialScopeID) || setting.ExpectedRouteVersion <= 0 || setting.MaxOutputTokens <= 0 || setting.TimeoutMS <= 0 || setting.TimeoutMS > math.MaxInt64/int64(time.Millisecond) || !setting.Policy.validTemplate() {
			return ErrInvalid
		}
		ids, err := s.bindingLocked(setting.Binding, false)
		if err != nil {
			return err
		}
		if !s.p.registeredOrigin(originOf(ids)) {
			return ErrNotBound
		}
		state := s.p.scopes[keyFor(ids, "context")]
		if state == nil || len(state.issues) != 0 {
			return ErrIntegrity
		}
		if !loopDisplayTitle(setting.DisplayTitle) {
			return ErrInvalid
		}
		settings[setting.Binding] = setting
	}
	if s.loop != nil {
		if s.loop.workspace != ws || !reflect.DeepEqual(s.loop.options, copy) {
			return ErrCommandMismatch
		}
		return nil
	}
	r := &loopRuntime{service: s, workspace: ws, options: copy, settings: settings, turns: map[uuid.UUID]*loopTurn{}, contexts: map[uuid.UUID][]uuid.UUID{}, queued: map[uuid.UUID]bool{}, paidTokens: map[uuid.UUID]struct{}{}, wake: make(chan struct{}, 1), changed: make(chan struct{}), completed: make(chan loopCompletion, len(settings)), done: make(chan struct{})}
	s.loop = r
	ws.SetReleaseNotifier(r.released)
	go r.run()
	return nil
}

// notify is safe under Service.mu: it never waits, takes another lock or calls
// out. A generation distinguishes another holder's release from our own retry.
func (r *loopRuntime) notify() {
	if r == nil {
		return
	}
	r.generation.Add(1)
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// released is the workspace release notifier. It runs under workspace locks,
// so like notify it only counts and signals; quantum compares the count across
// a busy attempt so a release during the attempt is never lost.
func (r *loopRuntime) released() {
	r.releases.Add(1)
	r.notify()
}
func (r *loopRuntime) changedLocked() {
	close(r.changed)
	r.changed = make(chan struct{})
}
func (r *loopRuntime) noteStop(t *loopTurn, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t.reason != "cancelled" && t.reason != "shutdown" {
		t.reason = reason
	}
}
func (r *loopRuntime) enqueue(t *loopTurn) {
	if t.running || r.queued[t.id] {
		return
	}
	agent := t.origin.AgentID
	if len(r.contexts[agent]) == 0 {
		r.agents = append(r.agents, agent)
	}
	r.contexts[agent] = append(r.contexts[agent], t.id)
	r.queued[t.id] = true
}
func (r *loopRuntime) next() *loopTurn {
	// Parked contexts retain their ring positions. A wakeup must not rebuild
	// fairness from map iteration order, and a blocked entry is visited once.
	for range len(r.queued) {
		agent := r.agents[0]
		r.agents = r.agents[1:]
		ids := r.contexts[agent]
		id := ids[0]
		ids = ids[1:]
		if len(ids) == 0 {
			delete(r.contexts, agent)
		} else {
			r.contexts[agent] = ids
			r.agents = append(r.agents, agent)
		}
		delete(r.queued, id)
		t := r.turns[id]
		// A parked turn waits until a release follows its park or its stop
		// reason changes, so a finish parked while stopping cannot spin.
		if t.state == "waiting_resource" && t.waitGeneration >= r.generation.Load() && t.reason == t.parkedReason {
			r.enqueue(t)
			continue
		}
		return t
	}
	return nil
}

// The scheduler claims a shared exchange token in ring order, before launching
// a worker. Letting workers race for it would undo agent/context fairness.
func (r *loopRuntime) claimPaid(t *loopTurn) (bool, uint64) {
	s := r.service
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.paid.exchanges)+len(r.paidTokens) >= s.paid.limits.MaxActivePaid {
		return false, r.generation.Load()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if t.paid == nil {
		t.paid = &loopPaidCall{id: uuid.NewV7(), admitID: uuid.NewV7(), dispatchID: uuid.NewV7(), observeID: uuid.NewV7(), reconcileID: uuid.NewV7()}
	}
	r.paidTokens[t.paid.id] = struct{}{}
	return true, 0
}
func (r *loopRuntime) shutdown() {
	r.mu.Lock()
	r.stopping = true
	for _, t := range r.turns {
		if t.reason != "cancelled" && t.state != "finished" {
			t.reason = "shutdown"
		}
		t.cancel()
	}
	r.mu.Unlock()
	r.notify()
	<-r.done
}

func (r *loopRuntime) run() {
	defer close(r.done)
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	var deadline <-chan time.Time
	for {
		r.mu.Lock()
		generation := r.generation.Load()
		r.changedLocked()
		for _, t := range r.turns {
			if !t.running && (t.state != "waiting_resource" || t.waitGeneration < generation || t.reason != t.parkedReason) {
				r.enqueue(t)
			}
		}
		if r.stopping && len(r.turns) == 0 && r.starting == 0 {
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()
		for {
			r.mu.Lock()
			t := r.next()
			r.mu.Unlock()
			if t == nil {
				break
			}
			reason := r.service.loopBoundary(t)
			r.mu.Lock()
			if t.reason == "cancelled" || t.reason == "shutdown" {
				reason = t.reason
			}
			if reason != "" {
				t.reason, t.state = reason, "stopping"
				t.cancel()
			}
			model := reason == "" && len(t.tools) == 0 && t.answer == nil
			r.mu.Unlock()
			if model {
				if claimed, generation := r.claimPaid(t); !claimed {
					r.mu.Lock()
					// Park under the reason selection decided with, so a
					// cancellation racing the claim still reselects the turn.
					t.state, t.waitGeneration, t.parkedReason = "waiting_resource", generation, reason
					r.enqueue(t)
					r.changedLocked()
					r.mu.Unlock()
					continue
				}
			}
			r.mu.Lock()
			t.running = true
			if reason == "" {
				if model {
					t.state = "model_running"
				} else if len(t.tools) != 0 {
					t.state = "tool_running"
				}
			}
			r.changedLocked()
			r.mu.Unlock()
			go r.quantum(t, model)
		}
		// One timer covers all admitted deadlines; no dormant-context timers.
		r.service.mu.Lock()
		now, clockErr := r.service.samplePaidLocked()
		r.service.mu.Unlock()
		r.mu.Lock()
		var earliest int64
		var expired []*loopTurn
		stopped := false
		for _, t := range r.turns {
			if clockErr != nil && t.reason == "" {
				t.reason = "integrity"
				t.cancel()
				stopped = true
			}
			if t.reason == "" {
				if now >= t.policy.DeadlineUTCNS {
					expired = append(expired, t)
				} else if earliest == 0 || t.policy.DeadlineUTCNS < earliest {
					earliest = t.policy.DeadlineUTCNS
				}
			}
		}
		r.mu.Unlock()
		for _, t := range expired {
			reason := r.service.loopBoundary(t)
			r.mu.Lock()
			if reason != "" && t.state != "finished" && t.state != "recovery_pending" && t.reason != "cancelled" && t.reason != "shutdown" {
				t.reason = reason
				t.cancel()
			}
			r.mu.Unlock()
		}
		if stopped || len(expired) != 0 {
			r.notify()
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		deadline = nil
		if earliest != 0 {
			timer.Reset(time.Duration(max(earliest-now, 0)))
			deadline = timer.C
		}
		select {
		case <-r.wake:
		case <-deadline:
			r.notify()
		case completion := <-r.completed:
			r.mu.Lock()
			t := completion.turn
			t.running = false
			if completion.err != nil && t.reason == "" {
				t.reason = loopFailureReason(completion.err)
			}
			if t.state == "finished" || t.state == "recovery_pending" {
				t.terminalError = completion.err
				delete(r.turns, t.id)
				close(t.done)
			} else if completion.busy && (t.reason == "" || completion.finishing) {
				t.state, t.waitGeneration, t.parkedReason = "waiting_resource", completion.waitGeneration, t.reason
				r.enqueue(t)
			} else {
				t.state = "ready"
				r.enqueue(t)
			}
			r.changedLocked()
			r.mu.Unlock()
		}
	}
}

func (r *loopRuntime) quantum(t *loopTurn, model bool) {
	var err error
	// Compare availability across the quantum before parking. Capturing just
	// the last notification would lose a concurrent holder's release; capturing
	// the first would spin on our own temporary-buffer/token releases.
	s := r.service
	s.mu.Lock()
	before := maps.Clone(s.p.book.preparing)
	beforeThrough, beforePrepared, beforePreparing := s.p.through, len(s.paid.prepared), s.preparing
	beforeSlots := len(s.paid.exchanges) + len(r.paidTokens)
	beforeReleases := r.releases.Load()
	var token uuid.UUID
	if model {
		r.mu.Lock()
		token = t.paid.id
		r.mu.Unlock()
		if _, held := r.paidTokens[token]; held {
			beforeSlots--
		}
	}
	s.mu.Unlock()
	r.mu.Lock()
	finishing := t.reason != "" || t.answer != nil && len(t.tools) == 0
	r.mu.Unlock()
	if finishing {
		err = r.finish(t)
	} else if model {
		err = r.model(t)
	} else {
		err = r.tool(t)
	}
	if model {
		s.mu.Lock()
		delete(r.paidTokens, token)
		r.notify()
		s.mu.Unlock()
	}
	busy := loopBusy(err)
	if finishing {
		busy = finishBusy(err)
	}
	var waitGeneration uint64
	if busy {
		s.mu.Lock()
		// The generation is read before the release count: a release the
		// comparison misses advances the generation past this park.
		waitGeneration = r.generation.Load()
		changed := beforeReleases != r.releases.Load() || beforeThrough != s.p.through || beforePrepared != len(s.paid.prepared) || beforePreparing != s.preparing || beforeSlots != len(s.paid.exchanges)+len(r.paidTokens) || !maps.Equal(before, s.p.book.preparing)
		s.mu.Unlock()
		if changed {
			busy = false
		}
		err = nil
	}
	r.completed <- loopCompletion{turn: t, busy: busy, finishing: finishing, waitGeneration: waitGeneration, err: err}
}

func loopFailureReason(err error) string {
	switch {
	case errors.Is(err, ErrDenied), errors.Is(err, ErrNotBound):
		return "authority_lost"
	case errors.Is(err, ErrIntegrity):
		return "integrity"
	case errors.Is(err, errLoopContent):
		return "content_invalid"
	case errors.Is(err, ErrTooLarge):
		return "request_too_large"
	default:
		return "provider_error"
	}
}

func (s *Service) loopBoundary(t *loopTurn) string {
	r := s.loop
	r.mu.Lock()
	reason, calls, tools := t.reason, t.modelCalls, t.toolCalls
	pending, answer := len(t.tools), t.answer != nil
	counted := t.paid != nil && t.paid.counted
	unadmitted := int64(0)
	for i := range t.tools {
		if !t.tools[i].counted {
			unadmitted++
		}
	}
	r.mu.Unlock()
	if reason == "cancelled" || reason == "shutdown" {
		return reason
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.usableLocked(context.Background()) != nil || s.refreshLocked(context.Background(), s.store.Durable()) != nil {
		return "integrity"
	}
	target := ledger.Resource{Kind: "context", Binding: ledger.Binding(bindingOf(t.origin)), Scope: "context", Slots: []string{}}
	if _, err := s.authorize(context.Background(), t.origin, t.grant, "turn.start", []ledger.Resource{target}, nil, 0, s.p.through); err != nil {
		return "authority_lost"
	}
	if st := s.p.scopes[scopeKey{t.origin.AgentID, t.origin.ContextID}]; st == nil || len(st.issues) != 0 {
		return "integrity"
	}
	route := s.p.paidRoutes[t.settings.RouteID]
	if !route.Enabled || route.Version != t.settings.ExpectedRouteVersion || s.paid.secrets[t.credential] == nil {
		return "authority_lost"
	}
	if _, err := s.loopGrantLocked(context.Background(), t.origin, "model.call", []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + t.settings.RouteID.String()}}, 0); err != nil {
		return "authority_lost"
	}
	for _, source := range t.sources {
		target := ledger.Resource{Kind: "context", Binding: ledger.Binding(bindingOf(t.origin)), Scope: source.Scope, Slots: []string{source.Slot}}
		if _, err := s.loopGrantLocked(context.Background(), t.origin, "private.read", []ledger.Resource{target}, source.Head.Bytes); err != nil {
			return "authority_lost"
		}
	}
	for _, ref := range t.artifacts {
		if _, err := s.artifactAuthority(context.Background(), t.origin, ref, uuid.UUID{}, s.p.through, nil); err != nil {
			return "authority_lost"
		}
	}
	now, err := s.samplePaidLocked()
	if err != nil {
		return "integrity"
	}
	if now >= t.policy.DeadlineUTCNS {
		return "deadline"
	}
	if reason != "" {
		return reason
	}
	if pending > 0 && (unadmitted > t.policy.MaxToolCalls-tools || calls >= t.policy.MaxModelCalls) || pending == 0 && !answer && calls >= t.policy.MaxModelCalls && !counted {
		return "loop_limit"
	}
	return ""
}

func (r *loopRuntime) turnView(t *loopTurn) session.TurnView {
	return session.TurnView{TurnID: t.id, State: t.state, Reason: t.reason, ModelCalls: t.modelCalls, ToolDispatches: t.toolCalls, BoundSequence: t.binding.first + 1}
}

func loopBusy(err error) bool {
	var busy *BusyError
	return errors.As(err, &busy)
}

// finishBusy adds the workspace's own busy: only turn end commits observations
// directly, while a tool's workspace busy becomes its result.
func finishBusy(err error) bool {
	var busy *workspace.BusyError
	return loopBusy(err) || errors.As(err, &busy)
}
