package agents

import (
	"container/heap"
	"context"
	"errors"
	"math/big"
	"slices"
	"strconv"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type serviceDeadline struct {
	key             serviceMemberKey
	generation, due int64
}
type serviceDeadlines []serviceDeadline

func (h serviceDeadlines) Len() int { return len(h) }
func (h serviceDeadlines) Less(i, j int) bool {
	if h[i].due != h[j].due {
		return h[i].due < h[j].due
	}
	if n := compareServiceIDs(h[i].key.cause, h[j].key.cause); n != 0 {
		return n < 0
	}
	return compareServiceIDs(h[i].key.agent, h[j].key.agent) < 0
}
func (h serviceDeadlines) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *serviceDeadlines) Push(v any)   { *h = append(*h, v.(serviceDeadline)) }
func (h *serviceDeadlines) Pop() any {
	old := *h
	n := len(old) - 1
	v := old[n]
	*h = old[:n]
	return v
}

type serviceTimer struct {
	wake   chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
}

func (s *Service) notifyServiceTimerLocked() {
	if s.serviceTimer != nil {
		select {
		case s.serviceTimer.wake <- struct{}{}:
		default:
		}
	}
}
func (s *Service) startServiceTimerLocked() {
	if s.serviceTimer != nil || s.closed || s.loopClosing {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker := &serviceTimer{wake: make(chan struct{}, 1), done: make(chan struct{}), cancel: cancel}
	s.serviceTimer = worker
	go s.runServiceTimer(ctx, worker)
}
func (s *Service) serviceDeadlinesLocked() serviceDeadlines {
	h := serviceDeadlines{}
	for key, m := range s.p.service.members {
		c := s.p.service.causes[key.cause]
		if c.unsupported || m.unsupported || s.p.service.unverifiable(key) || s.p.service.pendingPositiveSource(key, uuid.UUID{}) || m.snapshot.Phase != "pending" || m.snapshot.DueUTCNS == nil || c.snapshot.Condition != "active" {
			continue
		}
		h = append(h, serviceDeadline{key, m.snapshot.Generation, *m.snapshot.DueUTCNS})
	}
	heap.Init(&h)
	return h
}
func (s *Service) sampleServiceLocked() (int64, bool, error) {
	sample, err := s.deps.reviewClock()
	if err != nil {
		return 0, false, ErrPaidClock
	}
	floor := max(s.p.reviewFloor, s.reviewSample)
	if sample < floor {
		if floor <= 0 {
			return 0, false, ErrPaidClock
		}
		return floor, true, nil
	}
	if sample <= 0 {
		return 0, false, ErrPaidClock
	}
	s.reviewSample = max(s.reviewSample, sample)
	return sample, false, nil
}
func (s *Service) runServiceTimer(ctx context.Context, worker *serviceTimer) {
	defer close(worker.done)
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		s.mu.Lock()
		if s.closed || s.loopClosing || ctx.Err() != nil {
			s.mu.Unlock()
			return
		}
		h := s.serviceDeadlinesLocked()
		var timerC <-chan time.Time
		if len(h) > 0 {
			now, _, err := s.sampleServiceLocked()
			if err != nil {
				s.retainServiceErrorLocked(err)
			} else {
				delay := max(int64(0), h[0].due-now)
				timer.Reset(time.Duration(delay))
				timerC = timer.C
			}
		}
		armed := s.deps.afterServiceTimerArm
		s.mu.Unlock()
		if timerC != nil && armed != nil {
			armed()
		}
		select {
		case <-ctx.Done():
			return
		case <-worker.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timerC:
			_, err := s.ReconcileServiceEvidence(ctx)
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-worker.wake:
				}
			}
		}
	}
}

func (s *Service) expireDueServiceLocked(ctx context.Context) error {
	for {
		h := s.serviceDeadlinesLocked()
		if len(h) == 0 {
			return nil
		}
		now, _, err := s.sampleServiceLocked()
		if err != nil {
			return err
		}
		next := heap.Pop(&h).(serviceDeadline)
		if now < next.due {
			return nil
		}
		m := s.p.service.members[next.key]
		if m.snapshot.Generation != next.generation {
			continue
		}
		if err := s.expireServiceMemberLocked(ctx, next.key, uuid.UUID{}); err != nil {
			return err
		}
	}
}

type serviceMoneyHistoryKey struct{ Scope ledger.PaidScope }

func (serviceMoneyHistoryKey) historyKind() historyNeedKind { return needServiceMoney }

type serviceSpendState struct {
	Sequence int64
	Terms    ledger.SpendTerms
}

func (s *Service) serviceSpendAt(ctx context.Context, reads *historyReadSet, scope ledger.PaidScope, through uint64) (serviceSpendState, error) {
	return readHistory(ctx, reads, 0, through, serviceMoneyHistoryKey{scope}, func(ctx context.Context) (serviceSpendState, error) {
		var state serviceSpendState
		err := s.scan(ctx, 0, through, func(c ledger.Commit) error {
			for _, r := range c.Records {
				if r.Type != ledger.TypeRuntimeSpendConfigured {
					continue
				}
				p, err := ledger.DecodeRuntimeSpendConfigured(r.Payload)
				if err != nil {
					return err
				}
				if p.Scope == scope {
					state = serviceSpendState{int64(r.Seq), p.Terms}
				}
			}
			return nil
		})
		return state, err
	})
}

func (s *Service) validateServiceMoney(ctx context.Context, reads *historyReadSet, agent uuid.UUID, src ledger.ServiceSource, records map[int64]ledger.Record) error {
	w := src.Money
	if w.Scope.Kind == "agent" && w.Scope.ID != agent {
		return ErrInvalid
	}
	if src.Kind == "demand_withdrawn" {
		return nil
	} // It repeats its already-validated clearing K's witness.
	through := uint64(src.ObservedThrough)
	period := ""
	if src.Reason == "budget_available" && src.Origin != nil {
		if src.SourceSequence == nil {
			return ErrInvalid
		}
		r := records[*src.SourceSequence]
		if r.Type != ledger.TypeRuntimePaidReserved || r.AgentID != agent || r.ExecutionSessionID != src.Origin.SessionID || r.ContextID != src.Origin.ContextID || r.TurnID != src.TurnID {
			return ErrInvalid
		}
		admission, err := ledger.DecodeRuntimePaidReserved(r.Payload)
		if err != nil || admission.ParentTurnID != src.TurnID || admission.AdmissionUTCNS > src.ObservedUTCNS || !slices.Contains(admission.Scopes, w.Scope) {
			return ErrInvalid
		}
		through = r.Seq - uint64(r.CommitIndex) - 1
		period = admission.AdmissionPeriod
	} else if src.Reason == "budget_available" && (src.SourceSequence == nil || *src.SourceSequence != w.SpendSequence || src.TurnID != (uuid.UUID{})) {
		return ErrInvalid
	}
	config, err := s.serviceSpendAt(ctx, reads, w.Scope, through)
	if err != nil {
		return err
	}
	if config.Sequence != w.SpendSequence || !config.Terms.Enabled || config.Terms.Mode != w.Mode {
		return ErrInvalid
	}
	if period == "" {
		period = paidPeriod(config.Terms.PeriodKind, src.ObservedUTCNS)
	}
	if w.Period != period {
		return ErrInvalid
	}
	if w.Mode == "finite" && (config.Terms.CeilingMicros == nil || w.CeilingMicros == nil || *w.CeilingMicros != strconv.FormatInt(*config.Terms.CeilingMicros, 10)) {
		return ErrInvalid
	}
	settled, held, _, err := s.paidHistoryTotal(ctx, reads, w.Scope, period, nil, through)
	if err != nil {
		return err
	}
	occupied := new(big.Int).Add(settled, held)
	if occupied.String() != w.OccupiedMicros {
		return ErrInvalid
	}
	if src.Reason == "budget_available" && w.Mode == "finite" && occupied.Cmp(big.NewInt(*config.Terms.CeilingMicros)) >= 0 {
		return ErrInvalid
	}
	if src.Kind == "money_refusal" && src.ResetUTCNS != nil {
		at := time.Unix(0, src.ObservedUTCNS).UTC()
		var reset int64
		switch config.Terms.PeriodKind {
		case "utc_day":
			reset = time.Date(at.Year(), at.Month(), at.Day()+1, 0, 0, 0, 0, time.UTC).UnixNano()
		case "utc_month":
			reset = time.Date(at.Year(), at.Month()+1, 1, 0, 0, 0, 0, time.UTC).UnixNano()
		default:
			return ErrInvalid
		}
		if *src.ResetUTCNS != reset {
			return ErrInvalid
		}
	}
	return nil
}

func (s *Service) serviceMoneyWitness(ctx context.Context, reads *historyReadSet, first ledger.ServiceMoney, now int64) (ledger.ServiceMoney, bool, error) {
	a, ok := s.p.book.paid[first.Scope]
	if !ok || a.version == 0 || !a.terms.Enabled {
		return ledger.ServiceMoney{}, false, ErrUnavailable
	}
	period := paidPeriod(a.terms.PeriodKind, now)
	if period == "" {
		return ledger.ServiceMoney{}, false, ErrUnavailable
	}
	occupied := paidOccupied(a, period)
	if a.period != period {
		settled, held, _, err := s.paidHistoryTotal(ctx, reads, first.Scope, period, nil, s.p.through)
		if err != nil {
			return ledger.ServiceMoney{}, false, err
		}
		occupied = new(big.Int).Add(settled, held)
	}
	config, err := s.serviceSpendAt(ctx, reads, first.Scope, s.p.through)
	if err != nil {
		return ledger.ServiceMoney{}, false, err
	}
	w := first
	w.Period = period
	w.SpendSequence = config.Sequence
	w.Mode = a.terms.Mode
	w.OccupiedMicros = occupied.String()
	w.CeilingMicros = nil
	available := a.terms.Mode == "unlimited"
	if a.terms.Mode == "finite" && a.terms.CeilingMicros != nil {
		w.CeilingMicros = new(strconv.FormatInt(*a.terms.CeilingMicros, 10))
		available = occupied.Cmp(big.NewInt(*a.terms.CeilingMicros)) < 0
	}
	return w, available, nil
}

func (s *Service) expireServiceMemberLocked(ctx context.Context, key serviceMemberKey, beforeClear uuid.UUID) error {
	var clearing *ledger.RuntimeServiceObserved
	// The same final sample selects the book period and the expiry. Any cold
	// read or moved tail discards both, including this sample's observation ID.
	command := uuid.NewV7()
	_, err := s.commitServiceLocked(ctx, command, func(reads *historyReadSet) (ledger.ServiceSubmission, error) {
		clearing = nil
		m := s.p.service.members[key]
		c := s.p.service.causes[key.cause]
		if c.unsupported || m.unsupported || s.p.service.unverifiable(key) || c.snapshot.Condition != "active" || m.snapshot.Phase != "pending" || m.snapshot.DueUTCNS == nil || s.p.service.pendingPositiveSource(key, beforeClear) {
			return ledger.ServiceSubmission{}, ErrNoEffect
		}
		now, held, err := s.sampleServiceLocked()
		if err != nil {
			return ledger.ServiceSubmission{}, err
		}
		if now < *m.snapshot.DueUTCNS {
			return ledger.ServiceSubmission{}, ErrNoEffect
		}
		if m.snapshot.FirstSource.Money != nil {
			money, available, err := s.serviceMoneyWitness(ctx, reads, *m.snapshot.FirstSource.Money, now)
			if err != nil {
				return ledger.ServiceSubmission{}, err
			}
			if available {
				agents := []uuid.UUID{}
				for member, m := range s.p.service.members {
					if member.cause == key.cause && m.snapshot.Phase == "pending" {
						agents = append(agents, member.agent)
					}
				}
				slices.SortFunc(agents, compareServiceIDs)
				id := command
				src := ledger.ServiceSource{Kind: "cause_clear", ObservationID: id, ObservedThrough: int64(s.p.through), SourceSequence: new(money.SpendSequence), ObservedUTCNS: now, ClockHeld: held, Reason: "budget_available", Money: &money}
				e := ledger.RuntimeServiceObserved{ObservationID: id, AgentID: agents[0], CauseID: key.cause, Source: src}
				clearing = &e
				return serviceRecordSubmission(e), nil
			}
		}
		src := ledger.ServiceSource{Kind: "grace_expiry", ObservationID: uuid.NewV7(), ObservedThrough: int64(s.p.through), ObservedUTCNS: now, ClockHeld: held, Reason: "grace_due"}
		return s.serviceStateSubmission("service.expire", key.agent, key.cause, src)
	})
	if errors.Is(err, ErrNoEffect) {
		return nil
	}
	if err != nil {
		return err
	}
	if clearing != nil {
		if err := s.applyServiceSourceLocked(ctx, "service.clear", clearing.AgentID, clearing.CauseID, clearing.Source); err != nil {
			return err
		}
		return s.withdrawClearedServiceLocked(ctx)
	}
	return nil
}
