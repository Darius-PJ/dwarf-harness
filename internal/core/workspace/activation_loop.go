package workspace

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"slices"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// The floor includes trusted live observations as well as replayed timestamps.
// A failed or regressed sample never lowers it and never arms a timer.
func (s *Service) sampleActivationTimeLocked() (int64, bool) {
	now, err := s.deps.activationNow()
	if err != nil {
		return 0, false
	}
	ns, err := activationTimeNS(now)
	if err != nil || ns < s.activationFloor {
		return 0, false
	}
	s.activationFloor = ns
	return ns, true
}

func (s *Service) activationRecoveryPendingLocked(r activationState) bool {
	return !s.activationLoopStarted && r.state.State == "pending" && r.state.Hold != ledger.HoldDue && r.state.Hold != ledger.HoldClockUntrusted
}

func (s *Service) wakeActivationLocked() {
	select {
	case s.activationWake <- struct{}{}:
	default:
	}
}

func (s *Service) changeActivationHoldLocked(ctx context.Context, hold string, observed int64) error {
	r, exists := s.activations[s.pendingActivation]
	if !exists || r.state.Hold == hold {
		return nil
	}
	if hold == ledger.HoldClockUntrusted {
		observed = 0
	}
	p := ledger.PolicyActivationHoldChanged{RequestID: r.request.RequestID, ActivationRequestRecordHash: r.state.RequestRecordHash, Hold: hold, ObservedUTCNS: observed}
	payload, err := p.Encode()
	if err != nil {
		return err
	}
	return s.appendLocked(ctx, []ledger.Draft{{Type: ledger.TypePolicyActivationHoldChanged, TypeVersion: 1, Actor: ledger.ActorCore, Payload: payload}})
}

// observeActivationLocked installs only changed holds. Due is durable and final.
func (s *Service) observeActivationLocked(ctx context.Context) (now int64, trusted, changed bool, err error) {
	r, exists := s.activations[s.pendingActivation]
	if !exists {
		return 0, false, false, nil
	}
	now, trusted = s.sampleActivationTimeLocked()
	if r.state.Hold == ledger.HoldDue {
		return now, trusted, false, nil
	}
	hold := r.state.Hold
	switch {
	case !trusted:
		hold = ledger.HoldClockUntrusted
	case now >= r.request.NotBeforeUTCNS:
		hold = ledger.HoldDue
	case hold == ledger.HoldClockUntrusted:
		hold = ledger.HoldNone
	}
	if hold != r.state.Hold {
		if err := s.changeActivationHoldLocked(ctx, hold, now); err != nil {
			return 0, false, false, err
		}
		changed = true
	}
	return now, trusted, changed, nil
}

// resumeActivationControls is independent of promotion reconciliation: activation
// owns no external writer. Only this entry starts the single service-owned loop.
func (s *Service) resumeActivationControls(ctx context.Context) error {
	if err := s.start(); err != nil {
		return err
	}
	defer s.ops.Done()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usableLocked(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r, exists := s.activations[s.pendingActivation]; exists && r.state.Hold != ledger.HoldDue {
		if _, _, _, err := s.observeActivationLocked(ctx); err != nil {
			return err
		}
	}
	if !s.activationLoopStarted {
		s.activationLoopStarted = true
		s.ops.Add(1)
		go s.activationLoop()
	}
	s.wakeActivationLocked()
	return nil
}

// advanceActivation checks retained bytes without mu, then repeats identity,
// revision and eligibility under the same lock used by finalization intents.
func (s *Service) advanceActivation(ctx context.Context) error {
	if err := s.start(); err != nil {
		return err
	}
	defer s.ops.Done()
	s.mu.Lock()
	r, exists := s.activations[s.pendingActivation]
	s.mu.Unlock()
	if !exists {
		return nil
	}
	available := true
	for _, digest := range r.refs {
		body, err := s.store.ReadBody(ctx, digest)
		if err != nil || ledger.Digest(sha256.Sum256(body)) != digest {
			available = false
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usableLocked(); err != nil {
		return err
	}
	current, exists := s.activations[s.pendingActivation]
	if !exists || current.request.RequestID != r.request.RequestID || current.state.Revision != r.state.Revision {
		return nil
	}
	if !available {
		current.state.Reason = "target_unavailable"
		s.activations[r.request.RequestID] = current
		return nil
	}
	current.state.Reason = ""
	s.activations[r.request.RequestID] = current
	now, trusted, _, err := s.observeActivationLocked(ctx)
	if err != nil {
		return err
	}
	current = s.activations[r.request.RequestID]
	if current.state.Hold != ledger.HoldDue || !trusted || now < current.request.NotBeforeUTCNS {
		return nil
	}
	for _, id := range current.request.AffectedWorkspaceIDs {
		if _, unresolved := s.intents[id]; unresolved {
			return nil
		}
	}
	if current.request.ExpectedActiveRecordHash != s.activePolicy.RecordHash {
		s.poisoned = true
		return fmt.Errorf("%w: %w: pending activation expected manifest changed", ErrPoisoned, ErrInconsistent)
	}
	if s.activePolicy.Epoch >= math.MaxInt64 {
		return fmt.Errorf("%w: activation epoch exhausted", ErrInvalid)
	}
	p := current.request
	active := ledger.PolicyActivated{ActivationID: p.RequestID, Epoch: int64(s.activePolicy.Epoch) + 1, FieldLawSHA256: p.FieldLawSHA256, ValidationPolicyHash: p.ValidationPolicyHash, WorkspacePolicies: p.WorkspacePolicies, OwnerApprovalID: p.OwnerApprovalID, ActivationRequestRecordHash: current.state.RequestRecordHash, ExpectedActiveRecordHash: p.ExpectedActiveRecordHash, NotBeforeUTCNS: p.NotBeforeUTCNS, EffectiveUTCNS: now}
	payload, err := active.Encode()
	if err != nil {
		return err
	}
	return s.appendLocked(ctx, []ledger.Draft{{Type: ledger.TypePolicyActivated, TypeVersion: 1, Actor: ledger.ActorCore, BodyRefs: current.refs, Payload: payload}})
}

func activationDelay(notBefore, now int64) time.Duration {
	// Unsigned subtraction represents the full signed-endpoint distance.
	delta := uint64(notBefore) - uint64(now)
	if delta > math.MaxInt64 {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(delta)
}

func (s *Service) activationLoop() {
	defer s.ops.Done()
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		// Every wake asks for eligibility again; no timer timestamp is authority.
		advanceErr := s.advanceActivation(s.activationContext)
		s.mu.Lock()
		if s.closed || s.poisoned {
			s.mu.Unlock()
			return
		}
		var timerC <-chan time.Time
		if r, exists := s.activations[s.pendingActivation]; advanceErr == nil && exists && r.state.Hold != ledger.HoldDue && r.state.Reason != "target_unavailable" {
			now, trusted := s.sampleActivationTimeLocked()
			if trusted && r.state.Hold != ledger.HoldClockUntrusted && now < r.request.NotBeforeUTCNS {
				timer = time.NewTimer(activationDelay(r.request.NotBeforeUTCNS, now))
				timerC = timer.C
			} else if trusted || r.state.Hold != ledger.HoldClockUntrusted {
				// Time changed after the body check. Recheck the retained target
				// before recording the corresponding hold, never poll a failure.
				s.wakeActivationLocked()
			}
		}
		s.mu.Unlock()
		select {
		case <-s.activationContext.Done():
			return
		case <-s.activationWake:
		case <-timerC:
		}
		if timer != nil {
			timer.Stop()
			timer = nil
		}
	}
}

// guardActivationIntentLocked has no train, Git, body or wait dependency. Its
// caller keeps mu through encoding and append so the returned hash stays current.
func (s *Service) guardActivationIntentLocked(workspaceID uuid.UUID) (ledger.Digest, error) {
	if err := s.usableLocked(); err != nil {
		return ledger.Digest{}, err
	}
	if r, exists := s.activations[s.pendingActivation]; exists && slices.Contains(r.request.AffectedWorkspaceIDs, workspaceID) {
		if s.activationRecoveryPendingLocked(r) {
			return ledger.Digest{}, &UnavailableError{Reason: "activation_recovery_pending"}
		}
		if r.state.Hold != ledger.HoldDue {
			_, _, changed, err := s.observeActivationLocked(context.Background())
			if err != nil {
				return ledger.Digest{}, err
			}
			if changed {
				s.wakeActivationLocked()
			}
			r = s.activations[s.pendingActivation]
		}
		switch r.state.Hold {
		case ledger.HoldDue:
			return ledger.Digest{}, &UnavailableError{Reason: "activation_pending"}
		case ledger.HoldClockUntrusted:
			return ledger.Digest{}, &UnavailableError{Reason: "clock_untrusted"}
		}
	}
	if s.activePolicy.RecordHash != (ledger.Digest{}) && !s.activationHasWorkspaceLocked(workspaceID) {
		return ledger.Digest{}, &UnavailableError{Reason: "policy_unavailable"}
	}
	return s.activePolicy.RecordHash, nil
}
