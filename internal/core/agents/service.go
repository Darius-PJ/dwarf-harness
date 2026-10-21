package agents

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type privateStore interface {
	Submit(context.Context, ledger.Draft, []ledger.Draft) (ledgerstore.Receipt, error)
	LogDir() string
	Colony() uuid.UUID
	Durable() uint64
}
type serviceDeps struct {
	beforePublish     func(Binding, string) error
	afterPublish      func(Binding, string)
	beforeSubmit      func([]ledger.Draft) []ledger.Draft
	afterCommit       func(CommandResult)
	beforeMaterialize func(Binding, string, string) error
	beforeProjection  func(Binding, string)
	beforeLookup      func()
	afterCloseFence   func()
}
type scopeKey struct{ agent, context uuid.UUID }
type scopeGate struct {
	turn  chan struct{}
	users int
}
type agentState struct {
	session, general uuid.UUID
	directory        string
}
type scopeState struct {
	ids       submissionIDs
	revision  uint64
	heads     map[string]Head
	materials map[string]string
	issues    []IntegrityIssue
}
type scratchState struct {
	ledger.ScratchCreated
	closed   bool
	sequence uint64
}
type projection struct {
	agents     map[uuid.UUID]agentState
	contexts   map[uuid.UUID]uuid.UUID
	sessions   map[uuid.UUID]uuid.UUID
	scopes     map[scopeKey]*scopeState
	scratches  map[uuid.UUID]scratchState
	maxCommand uuid.UUID
	through    uint64
}

func newProjection() projection {
	return projection{agents: map[uuid.UUID]agentState{}, contexts: map[uuid.UUID]uuid.UUID{}, sessions: map[uuid.UUID]uuid.UUID{}, scopes: map[scopeKey]*scopeState{}, scratches: map[uuid.UUID]scratchState{}}
}

// Service owns the private state projection, not the shared colony store.
// All public entry points are owner-only, under the host's trust boundary.
type Service struct {
	mu        sync.Mutex
	active    sync.WaitGroup
	closed    bool
	poisoned  bool
	store     privateStore
	colony    string
	limits    Limits
	deps      serviceDeps
	dirs      directoryDurability
	p         projection
	gates     map[scopeKey]*scopeGate
	preparing int
}

func Open(ctx context.Context, store privateStore, colonyDir string, limits Limits) (*Service, Reconciliation, error) {
	return openService(ctx, store, colonyDir, limits, serviceDeps{})
}
func openService(ctx context.Context, store privateStore, colonyDir string, limits Limits, deps serviceDeps) (*Service, Reconciliation, error) {
	var report Reconciliation
	if store == nil {
		return nil, report, ErrInvalid
	}
	if err := limits.validate(); err != nil {
		return nil, report, err
	}
	colony, err := filepath.Abs(colonyDir)
	if err != nil {
		return nil, report, err
	}
	want, err := os.Stat(filepath.Join(colony, "log"))
	if err != nil {
		return nil, report, err
	}
	actual, err := os.Stat(store.LogDir())
	if err != nil || !os.SameFile(want, actual) {
		return nil, report, ErrNotBound
	}
	s := &Service{store: store, colony: colony, limits: limits, deps: deps, p: newProjection(), gates: map[scopeKey]*scopeGate{}}
	if err := s.refreshLocked(ctx, store.Durable()); err != nil {
		return nil, report, err
	}
	report, err = s.inspectOpen(ctx)
	if err != nil {
		return nil, report, err
	}
	return s, report, nil
}
func (s *Service) Close() error {
	s.mu.Lock()
	first := !s.closed
	s.closed = true
	s.mu.Unlock()
	if first && s.deps.afterCloseFence != nil {
		s.deps.afterCloseFence()
	}
	s.active.Wait()
	return nil
}
func (s *Service) enter(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.poisoned {
		return ErrPoisoned
	}
	s.active.Add(1)
	return nil
}
func (s *Service) usableLocked(ctx context.Context) error {
	if s.closed {
		return ErrClosed
	}
	if s.poisoned {
		return ErrPoisoned
	}
	return ctx.Err()
}
func (s *Service) bindingLocked(b Binding, creation bool) (submissionIDs, error) {
	a, ok := s.p.agents[b.AgentID]
	if !ok || !validID(b.ContextID) {
		return submissionIDs{}, ErrNotBound
	}
	owner, exists := s.p.contexts[b.ContextID]
	if exists && owner != b.AgentID || !exists && !creation {
		return submissionIDs{}, ErrNotBound
	}
	return submissionIDs{b.AgentID, a.session, b.ContextID}, nil
}
func keyFor(ids submissionIDs, scope string) scopeKey {
	k := scopeKey{agent: ids.agent}
	if scope == "context" {
		k.context = ids.context
	}
	return k
}
func (s *Service) scopePath(ids submissionIDs, scope string) (privateScope, error) {
	s.mu.Lock()
	a, ok := s.p.agents[ids.agent]
	s.mu.Unlock()
	if !ok {
		return privateScope{}, ErrNotBound
	}
	c := ""
	if scope == "context" {
		c = ids.context.String()
	}
	return newPrivateScope(s.colony, a.directory, c, &s.dirs)
}

// acquireScope never waits while holding mu. Its reference keeps the same gate
// alive for readers and publishers, including failed identity creations.
func (s *Service) acquireScope(ctx context.Context, k scopeKey) (func(), error) {
	s.mu.Lock()
	g := s.gates[k]
	if g == nil {
		g = &scopeGate{turn: make(chan struct{}, 1)}
		s.gates[k] = g
	}
	g.users++
	s.mu.Unlock()
	drop := func() {
		s.mu.Lock()
		g.users--
		if g.users == 0 {
			delete(s.gates, k)
		}
		s.mu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		drop()
		return nil, err
	}
	select {
	case g.turn <- struct{}{}:
		return func() { <-g.turn; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}
func (s *Service) scopeIssuesLocked(ids submissionIDs, scope string) error {
	for _, k := range []scopeKey{keyFor(ids, "agent"), keyFor(ids, scope)} {
		if st := s.p.scopes[k]; st != nil && len(st.issues) > 0 {
			return st.issues[0]
		}
	}
	return nil
}
func privateConsumption() ledger.Consumption {
	return ledger.Consumption{ledger.TypeCommand: {1}, ledger.TypeRuntimeAgentRegistered: {1}, ledger.TypeRuntimeContextCreated: {1}, ledger.TypePrivateRevision: {1}, ledger.TypeRuntimeCommandReceipt: {1}, ledger.TypeScratchCreated: {1}, ledger.TypeScratchClosed: {1}}
}
func isPrivateType(t string) bool {
	return t == ledger.TypeRuntimeAgentRegistered || t == ledger.TypeRuntimeContextCreated || t == ledger.TypePrivateRevision || t == ledger.TypeRuntimeCommandReceipt
}
func (s *Service) releasePreparation() { s.mu.Lock(); s.preparing--; s.mu.Unlock() }
func inconsistent(seq uint64, reason string) error {
	return &InconsistencyError{Sequence: seq, Reason: reason}
}
