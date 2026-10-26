package agents

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type privateStore interface {
	Submit(context.Context, ledger.Draft, []ledger.Draft) (ledgerstore.Receipt, error)
	PublishBody(context.Context, io.Reader) (ledger.Digest, int64, error)
	ReadBody(context.Context, ledger.Digest) ([]byte, error)
	LogDir() string
	Colony() uuid.UUID
	Durable() uint64
}
type serviceDeps struct {
	beforePublish     func(Binding, string) error
	afterPublish      func(Binding, string)
	beforeRead        func(Binding, string) error
	afterRead         func(Binding, string)
	beforeSubmit      func([]ledger.Draft) []ledger.Draft
	afterCommit       func(Receipt)
	afterScopedSubmit func()
	afterScopedCommit func(ScopedReceipt) error
	afterReviewCommit func(ReviewReceipt) error
	reviewClock       func() (int64, error)
	beforeMaterialize func(Binding, string, string) error
	beforeProjection  func(Binding, string)
	beforeLookup      func()
	beforeReplayWait  func()
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
	agents               map[uuid.UUID]agentState
	contexts             map[uuid.UUID]uuid.UUID
	sessions             map[uuid.UUID]uuid.UUID
	scopes               map[scopeKey]*scopeState
	scratches            map[uuid.UUID]scratchState
	authority            int64
	grants               map[uuid.UUID]grantState
	tasks                map[uuid.UUID]scopedTaskState
	work                 map[uuid.UUID]scopedWorkState
	reviews              map[uuid.UUID]reviewSlot
	maxRequest           uuid.UUID
	reviewFloor          int64
	paidRoutes           map[uuid.UUID]ledger.RuntimePaidRouteConfigured
	paidCredentialScopes map[uuid.UUID]paidCredentialScope
	paidCredentials      map[uuid.UUID]ledger.RuntimeCredentialChanged
	paidAttempts         map[uuid.UUID]paidAttempt
	paidChildren         map[uuid.UUID]int
	paidCapabilities     map[string]ledger.Digest
	paidTariffs          map[string]ledger.Digest
	retiredProfiles      map[ledger.Digest]bool
	maxAttempt           uuid.UUID
	maxCredentialVersion uuid.UUID
	book                 budgetBook
	maxCommand           uuid.UUID
	through              uint64
}

func newProjection() projection {
	return projection{
		agents: map[uuid.UUID]agentState{}, contexts: map[uuid.UUID]uuid.UUID{}, sessions: map[uuid.UUID]uuid.UUID{},
		scopes: map[scopeKey]*scopeState{}, scratches: map[uuid.UUID]scratchState{},
		grants: map[uuid.UUID]grantState{}, tasks: map[uuid.UUID]scopedTaskState{}, work: map[uuid.UUID]scopedWorkState{},
		reviews:    map[uuid.UUID]reviewSlot{},
		book:       budgetBook{accounts: map[ledger.ScopeKey]budgetAccount{}, preparing: map[ledger.ScopeKey][4]int64{}, paid: map[ledger.PaidScope]paidAccount{}},
		paidRoutes: map[uuid.UUID]ledger.RuntimePaidRouteConfigured{}, paidCredentialScopes: map[uuid.UUID]paidCredentialScope{},
		paidCredentials: map[uuid.UUID]ledger.RuntimeCredentialChanged{},
		paidAttempts:    map[uuid.UUID]paidAttempt{}, paidChildren: map[uuid.UUID]int{},
		paidCapabilities: map[string]ledger.Digest{}, paidTariffs: map[string]ledger.Digest{}, retiredProfiles: map[ledger.Digest]bool{},
	}
}

// Service owns the private and scoped projections, not the shared colony store.
// Service is the trusted owner/host entrance; BindAgent supplies scoped access.
type Service struct {
	mu            sync.Mutex
	active        sync.WaitGroup
	closed        bool
	poisoned      bool
	store         privateStore
	colony        string
	limits        Limits
	deps          serviceDeps
	dirs          directoryDurability
	p             projection
	gates         map[scopeKey]*scopeGate
	preparing     int
	openedThrough uint64
	reviewSample  int64
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
	if deps.reviewClock == nil {
		deps.reviewClock = func() (int64, error) { return time.Now().UTC().UnixNano(), nil }
	}
	s := &Service{store: store, colony: colony, limits: limits, deps: deps, p: newProjection(), gates: map[scopeKey]*scopeGate{}}
	if err := s.refreshLocked(ctx, store.Durable()); err != nil {
		return nil, report, err
	}
	s.openedThrough = s.p.through
	report, err = s.inspectOpen(ctx)
	if err != nil {
		return nil, report, err
	}
	report.PendingWork, err = s.pendingWorkViews()
	if err != nil {
		return nil, report, err
	}
	report.PendingPaid, err = s.pendingPaidViews()
	if err != nil {
		return nil, report, err
	}
	report.UnboundCredentials = s.unboundCredentialViews()
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
	return ledger.Consumption{
		ledger.TypeCommand: {1}, ledger.TypeBodyPin: {1}, ledger.TypeBodyUnpin: {1},
		ledger.TypeRuntimeAgentRegistered: {1}, ledger.TypeRuntimeContextCreated: {1}, ledger.TypePrivateRevision: {1}, ledger.TypeRuntimeCommandReceipt: {1},
		ledger.TypeWorkspaceRegistered: {1}, ledger.TypeScratchCreated: {1}, ledger.TypeScratchClosed: {1},
		ledger.TypeRuntimeGrantIssued: {1}, ledger.TypeRuntimeGrantRevoked: {1}, ledger.TypeRuntimeTaskChanged: {1}, ledger.TypeRuntimeCapacityConfigured: {1},
		ledger.TypeRuntimeWorkAdmitted: {1}, ledger.TypeRuntimeWorkDispatched: {1}, ledger.TypeRuntimeWorkEnded: {1}, ledger.TypeRuntimeLocalUsed: {1},
		ledger.TypePrivateScopedRevision: {1}, ledger.TypeRuntimeScopedReceipt: {1},
		ledger.TypeRuntimeReviewRequested: {1}, ledger.TypeRuntimeReviewResult: {1}, ledger.TypeRuntimeReviewReceipt: {1},
		ledger.TypeRuntimePaidRouteConfigured: {1}, ledger.TypeRuntimeCredentialChanged: {1}, ledger.TypeRuntimeSpendConfigured: {1},
		ledger.TypeRuntimePaidReserved: {1}, ledger.TypeRuntimePaidDispatchIntent: {1}, ledger.TypeRuntimePaidResult: {1},
		ledger.TypeRuntimePaidOverrunAcknowledged: {1}, ledger.TypeRuntimePaidReceipt: {1},
	}
}
func isPrivateType(t string) bool {
	return t == ledger.TypeRuntimeAgentRegistered || t == ledger.TypeRuntimeContextCreated || t == ledger.TypePrivateRevision || t == ledger.TypeRuntimeCommandReceipt
}
func (s *Service) releasePreparation() { s.mu.Lock(); s.preparing--; s.mu.Unlock() }
func inconsistent(seq uint64, reason string) error {
	return &InconsistencyError{Sequence: seq, Reason: reason}
}
