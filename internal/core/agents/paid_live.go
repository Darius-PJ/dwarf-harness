package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/credentials"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

type paidRuntime struct {
	limits    PaidLimits
	secrets   map[uuid.UUID]*credentials.Secret
	prepared  map[*preparedPaid]bool
	calls     map[uuid.UUID]*preparedPaid
	transport *providers.Transport
	exchanges map[uuid.UUID]context.CancelFunc
}
type preparedPaid struct {
	origin                ledger.Origin
	admission             ledger.RuntimePaidReserved
	envelope              providers.Envelope
	compiled              providers.Compiled
	body                  []byte
	reservation           scopedReservation
	consumed, closed      bool
	inUse, closeRequested bool
}

// A loopback profile is dispatchable only through the private verified-TLS seam.
// Provider, authority and path are one destination, never independent allowlists.
func (s *Service) paidDestination(p providers.Profile) error {
	var authority, path string
	switch p.Provider {
	case "anthropic":
		authority, path = "api.anthropic.com", "/v1/messages"
	case "openai":
		authority, path = "api.openai.com", "/v1/responses"
	default:
		return providers.ErrUnsupported
	}
	if p.Method != "POST" || p.Path != path || p.Authority != authority && (s.deps.paidTestAuthority == "" || p.Authority != s.deps.paidTestAuthority) {
		return providers.ErrUnsupported
	}
	return nil
}

func OpenPaid(ctx context.Context, store privateStore, colonyDir string, limits Limits, options PaidOptions) (*Service, Reconciliation, error) {
	return openPaidService(ctx, store, colonyDir, limits, options, serviceDeps{})
}
func openPaidService(ctx context.Context, store privateStore, colonyDir string, limits Limits, options PaidOptions, deps serviceDeps) (*Service, Reconciliation, error) {
	if err := options.Limits.validate(); err != nil {
		return nil, Reconciliation{}, err
	}
	s, report, err := openService(ctx, store, colonyDir, limits, deps)
	if err != nil {
		return nil, report, err
	}
	transportLimits := providers.Limits{MaxActivePaid: options.Limits.MaxActivePaid, DispatchTimeout: options.Limits.DispatchTimeout, TLSHandshakeTimeout: options.Limits.TLSHandshakeTimeout, ResponseHeaderTimeout: options.Limits.ResponseHeaderTimeout}
	var transport *providers.Transport
	if deps.paidTestRoots != nil || deps.paidWrapConn != nil {
		transport, err = providers.NewTransportWithRoots(transportLimits, deps.paidTestRoots, deps.paidWrapConn)
	} else {
		transport, err = providers.NewTransport(transportLimits)
	}
	if err != nil {
		_ = s.Close()
		return nil, report, err
	}
	s.paid = &paidRuntime{limits: options.Limits, secrets: map[uuid.UUID]*credentials.Secret{}, prepared: map[*preparedPaid]bool{}, calls: map[uuid.UUID]*preparedPaid{}, transport: transport, exchanges: map[uuid.UUID]context.CancelFunc{}}
	s.p.paidHotLimit = options.Limits.MaxPaidHotResults
	s.p.prunePaid()
	return s, report, nil
}
func (s *Service) releasePaidPreparation(p *preparedPaid) {
	if p.inUse {
		p.closeRequested = true
		return
	}
	if p.closed {
		return
	}
	p.closed = true
	p.body, p.envelope, p.compiled = nil, providers.Envelope{}, providers.Compiled{}
	s.releaseScopedLocked(&p.reservation)
	delete(s.paid.prepared, p)
	if s.paid.calls[p.admission.AttemptID] == p {
		delete(s.paid.calls, p.admission.AttemptID)
	}
}
func (s *Service) closePaidLocked() {
	if s.paid == nil {
		return
	}
	s.paid.transport.CloseIdleConnections()
	for p := range s.paid.prepared {
		s.releasePaidPreparation(p)
	}
	for _, p := range s.paid.calls {
		s.releasePaidPreparation(p)
	}
	for id, secret := range s.paid.secrets {
		secret.Release()
		delete(s.paid.secrets, id)
	}
}

// The review path and the paid path share this clock and its effective floor.
// The replay projection holds committed observations; reviewSample holds live
// samples (including refusals), so replay never judges history by a later sample.
func (s *Service) samplePaidLocked() (int64, error) {
	sample, err := s.deps.reviewClock()
	if err != nil {
		return 0, ErrPaidClock
	}
	s.reviewSample = max(s.reviewSample, sample)
	if sample <= 0 || sample < max(s.p.reviewFloor, s.reviewSample) {
		return 0, ErrPaidClock
	}
	return sample, nil
}

// lookupPaid follows the existing watermark/tail-closure pattern. The caller's
// current authority is checked before lookup and again at the refreshed tail.
// It returns with mu held, including on error.
func (s *Service) lookupPaid(ctx context.Context, reads *historyReadSet, bound *submissionIDs, id uuid.UUID, authorize func(*historyReadSet) error) (commandMatch, error) {
	s.mu.Lock()
	var match commandMatch
	if err := s.prepareHistoryLocked(ctx, reads, bound, true, authorize); err != nil {
		return match, err
	}
	through := s.p.through
	old := bytes.Compare(id[:], s.p.maxCommand[:]) <= 0
	s.mu.Unlock()
	if s.deps.beforeLookup != nil {
		s.deps.beforeLookup()
	}
	var err error
	if old {
		match, err = s.findCommand(ctx, nil, id, 0, through)
	}
	s.mu.Lock()
	prior := match
	lookupErr := err
	err = s.prepareHistoryLocked(ctx, reads, bound, true, func(reads *historyReadSet) error {
		match = prior
		if err := authorize(reads); err != nil {
			return err
		}
		if lookupErr != nil {
			return lookupErr
		}
		if !match.found && through < s.p.through {
			var err error
			match, err = s.findCommand(ctx, reads, id, through, s.p.through)
			return err
		}
		return nil
	})
	return match, err
}

// relookupPaidLocked repeats lookup and authority after a refused conditional
// submission. Its caller reruns all guards before constructing the next group.
func (s *Service) relookupPaidLocked(ctx context.Context, reads *historyReadSet, bound *submissionIDs, id uuid.UUID, authorize func(*historyReadSet) error) (commandMatch, error) {
	var match commandMatch
	err := s.prepareHistoryLocked(ctx, reads, bound, true, func(reads *historyReadSet) error {
		match = commandMatch{}
		var err error
		match, err = s.findCommand(ctx, reads, id, 0, s.p.through)
		if err == nil {
			err = authorize(reads)
		}
		return err
	})
	return match, err
}

type paidEncoding interface {
	Encode() (json.RawMessage, error)
}

func paidDigest(sub ledger.PaidSubmission) (ledger.Digest, error) {
	raw, err := sub.Encode()
	return ledger.Digest(sha256.Sum256(raw)), err
}
func copyPaidReceipt(g *paidGroup, through uint64) (PaidReceipt, error) {
	raw, err := g.receipt.Encode()
	if err != nil {
		return PaidReceipt{}, err
	}
	copy, err := ledger.DecodeRuntimePaidReceipt(raw)
	return PaidReceipt{RuntimePaidReceipt: copy, EffectSequence: g.first + 1, Sequence: g.first + 2, ThroughSequence: through}, err
}
func (s *Service) matchPaid(match commandMatch, sub ledger.PaidSubmission, digest ledger.Digest) (PaidReceipt, error) {
	if match.paid == nil || match.command.SubmissionSHA256 != digest {
		return PaidReceipt{}, ErrCommandMismatch
	}
	if match.paid.receipt.Submission.Actor != sub.Actor || match.paid.receipt.Submission.Origin != sub.Origin {
		return PaidReceipt{}, ErrDenied
	}
	return copyPaidReceipt(match.paid, s.p.through)
}
func paidDrafts(id uuid.UUID, sub ledger.PaidSubmission, effect paidEncoding, typ, outcome string, version int64, attempt, turn uuid.UUID, usage *ledger.PaidLocalUsage) ([]ledger.Draft, error) {
	digest, err := paidDigest(sub)
	if err != nil || !validID(id) {
		return nil, ErrInvalid
	}
	receipt := ledger.RuntimePaidReceipt{CommandID: id, SubmissionSHA256: digest, Submission: sub, Outcome: outcome, Version: version, AttemptID: attempt, Usage: usage}
	drafts := make([]ledger.Draft, 3)
	for i, p := range []paidEncoding{ledger.Command{CommandID: id, SubmissionSHA256: digest}, effect, receipt} {
		raw, err := p.Encode()
		if err != nil {
			return nil, err
		}
		actor := ledger.Actor(sub.Actor)
		if i == 1 && sub.Operation == "paid.admit" {
			actor = ledger.ActorCore
		}
		drafts[i] = ledger.Draft{Type: []string{ledger.TypeCommand, typ, ledger.TypeRuntimePaidReceipt}[i], TypeVersion: 1, Actor: actor, AgentID: sub.Origin.AgentID, ExecutionSessionID: sub.Origin.SessionID, ContextID: sub.Origin.ContextID, TurnID: turn, OperationID: id, Payload: raw}
	}
	return drafts, nil
}

// tailAttempts bounds a producer's submissions of one group whose facts the
// ledger's tail can falsify: each refusal at a moved tail re-plans at the new
// tail, and the last one answers BusyError{ledger_tail} with nothing written.
const tailAttempts = 3

// preflightPaidLocked validates only. The enclosing pure phase reruns its
// authority, current guards and drafts whenever a historical read suspends it.
func (s *Service) preflightPaidLocked(ctx context.Context, reads *historyReadSet, drafts []ledger.Draft) ([]ledger.Draft, *paidGroup, error) {
	_, _, _, original, _, _, _, err := decodeFamily(drafts, s.p.through+1)
	if err != nil || original == nil {
		return nil, nil, ErrInvalid
	}
	if s.deps.beforeSubmit != nil {
		drafts = s.deps.beforeSubmit(drafts)
	}
	p, scoped, review, g, registration, loop, continuation, err := decodeFamily(drafts, s.p.through+1)
	if err == nil && (p != nil || scoped != nil || review != nil || registration != nil || loop != nil || continuation != nil || g == nil || g.command != original.command || !sameScopedValue(g.receipt.Submission, original.receipt.Submission)) {
		err = ErrInvalid
	}
	if err == nil {
		_, err = s.planPaid(ctx, reads, g, s.p.through+1)
	}
	if err == nil {
		err = s.usableLocked(ctx)
	}
	return drafts, g, err
}

// submitPaidGroupLocked installs through the locked acknowledgement reader.
// Conditional groups retain the producer's existing SubmitAt attempt bound.
func (s *Service) submitPaidGroupLocked(ctx context.Context, drafts []ledger.Draft, g *paidGroup, conditional bool) (PaidReceipt, error) {
	var err error
	var committed ledgerstore.Receipt
	if conditional {
		committed, err = s.store.SubmitAt(ctx, s.p.through, drafts[0], drafts[1:])
	} else {
		committed, err = s.store.Submit(ctx, drafts[0], drafts[1:])
	}
	if err != nil {
		if errors.Is(err, appender.ErrUncertain) || errors.Is(err, appender.ErrPoisoned) {
			s.poisoned = true
		}
		if errors.Is(err, ledgerstore.ErrCommandMismatch) {
			err = ErrCommandMismatch
		}
		return PaidReceipt{}, err
	}
	if s.deps.afterScopedSubmit != nil {
		s.deps.afterScopedSubmit()
	}
	err = s.acknowledgeLocked(max(s.p.through, committed.Last))
	if err == nil && committed.Duplicate {
		var m commandMatch
		m, err = s.findCommand(context.Background(), nil, g.command.CommandID, committed.First-1, committed.Last)
		if err == nil {
			return s.matchPaid(m, g.receipt.Submission, g.command.SubmissionSHA256)
		}
	}
	if err != nil {
		s.poisoned = true
		return PaidReceipt{}, err
	}
	g.first = committed.First
	return copyPaidReceipt(g, s.p.through)
}
