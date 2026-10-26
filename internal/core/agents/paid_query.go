package agents

import (
	"bytes"
	"context"
	"math/big"
	"slices"
	"strconv"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type PaidAttemptLookup struct {
	AttemptID, GrantID uuid.UUID
	VerifyInput        bool
}
type PaidCommandLookup struct {
	CommandID, GrantID uuid.UUID
	SubmissionSHA256   ledger.Digest
}
type PaidBudgetLookup struct {
	Scope   ledger.PaidScope
	Period  string
	GrantID uuid.UUID
}

// PaidAttemptView is an owned ledger snapshot. It conveys no send permit and
// never treats an absent private artifact as proof of a free request.
type PaidAttemptView struct {
	Admission                                          ledger.RuntimePaidReserved
	Origin                                             ledger.Origin
	Intent                                             *ledger.RuntimePaidDispatchIntent
	Result                                             *ledger.RuntimePaidResult
	Version                                            int64
	State                                              string
	InputIntegrity                                     string
	HeldMicros, SettledMicros                          string
	Ended, Fenced, Acknowledged                        bool
	AdmissionSequence, ResultSequence, ThroughSequence uint64
}
type PaidReceipt struct {
	ledger.RuntimePaidReceipt
	EffectSequence, Sequence, ThroughSequence uint64
}
type PaidBudgetView struct {
	Scope                      ledger.PaidScope
	Period                     string
	Terms                      *ledger.SpendTerms
	Version                    int64
	SettledMicros, HeldMicros  string
	Available, Warning, Fenced bool
	ObservedUTCNS              int64
	ThroughSequence            uint64
}
type UnboundCredential struct {
	ScopeID, Version          uuid.UUID
	SourceKind, SourceLocator string
}

func (s *Service) paidView(a paidAttempt) (PaidAttemptView, error) {
	raw, err := a.admission.Encode()
	if err != nil {
		return PaidAttemptView{}, err
	}
	admission, err := ledger.DecodeRuntimePaidReserved(raw)
	if err != nil {
		return PaidAttemptView{}, err
	}
	view := PaidAttemptView{Admission: admission, Origin: a.origin, Version: a.version, State: a.state, HeldMicros: "0", SettledMicros: strconv.FormatInt(a.cost, 10), Ended: a.ended, Fenced: a.fenced, Acknowledged: a.acknowledged, AdmissionSequence: a.sequence, ResultSequence: a.resultSequence, ThroughSequence: s.p.through}
	if a.held {
		view.HeldMicros = strconv.FormatInt(a.admission.Quote.MaximumMicros, 10)
	}
	if a.intent != nil {
		copy := *a.intent
		view.Intent = &copy
	}
	if a.result != nil {
		raw, err := a.result.Encode()
		if err != nil {
			return PaidAttemptView{}, err
		}
		copy, err := ledger.DecodeRuntimePaidResult(raw)
		if err != nil {
			return PaidAttemptView{}, err
		}
		view.Result = &copy
	}
	return view, nil
}
func (s *Service) pendingPaidViews() ([]PaidAttemptView, error) {
	ids := []uuid.UUID{}
	for id, a := range s.p.paidAttempts {
		if a.held {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	views := make([]PaidAttemptView, 0, len(ids))
	for _, id := range ids {
		v, err := s.paidView(s.p.paidAttempts[id])
		if err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, nil
}
func (s *Service) unboundCredentialViews() []UnboundCredential {
	views := make([]UnboundCredential, 0, len(s.p.paidCredentials))
	for id, c := range s.p.paidCredentials {
		views = append(views, UnboundCredential{ScopeID: c.ScopeID, Version: id, SourceKind: c.SourceKind, SourceLocator: c.SourceLocator})
	}
	slices.SortFunc(views, func(a, b UnboundCredential) int {
		if n := bytes.Compare(a.ScopeID[:], b.ScopeID[:]); n != 0 {
			return n
		}
		return bytes.Compare(a.Version[:], b.Version[:])
	})
	return views
}
func (s *Service) paidInspect(ctx context.Context, origin ledger.Origin, grant uuid.UUID) error {
	target := ledger.Resource{Kind: "context", Binding: ledger.Binding(bindingOf(origin)), Scope: "context", Slots: []string{}}
	if _, err := s.authorize(ctx, origin, grant, "turn.inspect", []ledger.Resource{target}, nil, 0, s.p.through); err != nil {
		return reviewDenied(err)
	}
	return nil
}
func (s *Service) PaidAttemptStatus(ctx context.Context, request PaidAttemptLookup) (PaidAttemptView, error) {
	return s.paidAttemptStatus(ctx, nil, request)
}
func (a *Agent) PaidAttemptStatus(ctx context.Context, request PaidAttemptLookup) (PaidAttemptView, error) {
	if a == nil || a.service == nil {
		return PaidAttemptView{}, ErrNotBound
	}
	return a.service.paidAttemptStatus(ctx, &a.ids, request)
}
func (s *Service) paidAttemptStatus(ctx context.Context, bound *submissionIDs, request PaidAttemptLookup) (PaidAttemptView, error) {
	done, err := s.scopedQuery(ctx, bound)
	if err != nil {
		return PaidAttemptView{}, err
	}
	defer done()
	if !validID(request.AttemptID) || bound == nil && request.GrantID != (uuid.UUID{}) {
		return PaidAttemptView{}, ErrInvalid
	}
	if bound != nil {
		if err := s.paidInspect(ctx, originOf(*bound), request.GrantID); err != nil {
			return PaidAttemptView{}, err
		}
	}
	attempt, found, err := s.loadPaid(ctx, request.AttemptID, s.p.through, ledger.Digest{})
	if err != nil {
		return PaidAttemptView{}, err
	}
	if !found || bound != nil && attempt.origin != originOf(*bound) {
		return PaidAttemptView{}, ErrDenied
	}
	view, err := s.paidView(attempt)
	if err == nil && request.VerifyInput {
		view.InputIntegrity = "verified"
		if s.verifyPaidInput(attempt) != nil {
			view.InputIntegrity = "missing_or_invalid"
		}
	}
	return view, err
}
func (s *Service) PaidCommandStatus(ctx context.Context, request PaidCommandLookup) (PaidReceipt, error) {
	return s.paidCommandStatus(ctx, nil, request)
}
func (a *Agent) PaidCommandStatus(ctx context.Context, request PaidCommandLookup) (PaidReceipt, error) {
	if a == nil || a.service == nil {
		return PaidReceipt{}, ErrNotBound
	}
	return a.service.paidCommandStatus(ctx, &a.ids, request)
}
func (s *Service) paidCommandStatus(ctx context.Context, bound *submissionIDs, request PaidCommandLookup) (PaidReceipt, error) {
	done, err := s.scopedQuery(ctx, bound)
	if err != nil {
		return PaidReceipt{}, err
	}
	defer done()
	if !validID(request.CommandID) || request.SubmissionSHA256 == (ledger.Digest{}) || bound == nil && request.GrantID != (uuid.UUID{}) {
		return PaidReceipt{}, ErrInvalid
	}
	if bound != nil {
		if err := s.paidInspect(ctx, originOf(*bound), request.GrantID); err != nil {
			return PaidReceipt{}, err
		}
	}
	match, err := s.findCommand(ctx, request.CommandID, 0, s.p.through)
	if err != nil {
		return PaidReceipt{}, err
	}
	g := match.paid
	if g == nil || bound != nil && (g.receipt.Submission.Origin != originOf(*bound) || g.receipt.Submission.Actor == "owner") {
		return PaidReceipt{}, ErrDenied
	}
	if g.command.SubmissionSHA256 != request.SubmissionSHA256 {
		return PaidReceipt{}, ErrCommandMismatch
	}
	raw, err := g.receipt.Encode()
	if err != nil {
		return PaidReceipt{}, err
	}
	copy, err := ledger.DecodeRuntimePaidReceipt(raw)
	if err != nil {
		return PaidReceipt{}, err
	}
	return PaidReceipt{RuntimePaidReceipt: copy, EffectSequence: g.first + 1, Sequence: g.first + 2, ThroughSequence: s.p.through}, nil
}
func (s *Service) PaidBudgetStatus(ctx context.Context, request PaidBudgetLookup) (PaidBudgetView, error) {
	return s.paidBudgetStatus(ctx, nil, request)
}
func (a *Agent) PaidBudgetStatus(ctx context.Context, request PaidBudgetLookup) (PaidBudgetView, error) {
	if a == nil || a.service == nil {
		return PaidBudgetView{}, ErrNotBound
	}
	return a.service.paidBudgetStatus(ctx, &a.ids, request)
}
func (s *Service) paidBudgetStatus(ctx context.Context, bound *submissionIDs, request PaidBudgetLookup) (PaidBudgetView, error) {
	done, err := s.scopedQuery(ctx, bound)
	if err != nil {
		return PaidBudgetView{}, err
	}
	defer done()
	if !slices.Contains([]string{"colony", "agent", "credential", "task", "grant"}, request.Scope.Kind) || request.Scope.Kind == "colony" && request.Scope.ID != (uuid.UUID{}) || request.Scope.Kind != "colony" && !validID(request.Scope.ID) || bound == nil && request.GrantID != (uuid.UUID{}) {
		return PaidBudgetView{}, ErrInvalid
	}
	period := request.Period
	if period == "" {
		period = s.p.book.paidPeriod
	}
	if !validPaidQueryPeriod(s.p.book.paidPeriodKind, period) {
		return PaidBudgetView{}, ErrInvalid
	}
	account, exists := s.p.book.paid[request.Scope]
	if bound != nil {
		if err := s.paidInspect(ctx, originOf(*bound), request.GrantID); err != nil {
			return PaidBudgetView{}, err
		}
	}
	view := PaidBudgetView{Scope: request.Scope, Period: period, Version: account.version, ObservedUTCNS: s.p.book.paidObserved, ThroughSequence: s.p.through, Fenced: account.fences != 0, Warning: paidWarning(account, period)}
	settled, held := paidSettled(account, period), paidInteger(account.held)
	ownTerms := bound == nil
	related := bound == nil && exists
	var origin *ledger.Origin
	if bound != nil {
		o := originOf(*bound)
		origin = &o
		// The cold scope fold below also establishes historical relatedness;
		// a bounded hot set is never an existence or disclosure oracle.
		ownTerms = request.Scope.Kind == "agent" && request.Scope.ID == o.AgentID
		related = request.Scope.Kind == "colony" || ownTerms
		if request.Scope.Kind == "grant" {
			grant, found, err := s.loadGrant(ctx, request.Scope.ID, s.p.through)
			if err != nil {
				return PaidBudgetView{}, err
			}
			ownTerms = found && grant.definition.Grantee == ledger.Binding(bindingOf(o))
			related = ownTerms
		}
	}
	if bound != nil || !exists || period != s.p.book.paidPeriod {
		var historical bool
		settled, held, historical, err = s.paidHistoryTotal(ctx, request.Scope, period, origin, s.p.through)
		if err != nil {
			return PaidBudgetView{}, err
		}
		related = related || historical
	}
	if !related {
		return PaidBudgetView{}, ErrDenied
	}
	view.SettledMicros, view.HeldMicros = settled.String(), held.String()
	if bound == nil {
		warningAccount := account
		warningAccount.period, warningAccount.settled = period, settled
		view.Warning = paidWarning(warningAccount, period)
	}
	view.Available = account.terms.Enabled && account.fences == 0 && (account.terms.Mode == "unlimited" || paidOccupied(account, s.p.book.paidPeriod).Cmp(big.NewInt(*account.terms.CeilingMicros)) < 0)
	if request.Scope.Kind == "task" && account.version == 0 {
		view.Available = account.fences == 0
	}
	if ownTerms && account.version != 0 {
		copy := account.terms
		if copy.CeilingMicros != nil {
			n := *copy.CeilingMicros
			copy.CeilingMicros = &n
		}
		if copy.WarningPPM != nil {
			n := *copy.WarningPPM
			copy.WarningPPM = &n
		}
		view.Terms = &copy
	}
	return view, nil
}
