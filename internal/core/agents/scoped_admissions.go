package agents

import (
	"context"
	"fmt"
	"math"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func (a *Agent) StartTurn(ctx context.Context, request TurnAdmission) (ScopedReceipt, error) {
	return a.command(ctx, "turn.start", request)
}

func (a *Agent) CancelTurn(ctx context.Context, request TurnCancellation) (ScopedReceipt, error) {
	return a.command(ctx, "turn.cancel", request)
}

func (s *Service) CancelTurn(ctx context.Context, origin Binding, request TurnCancellation) (ScopedReceipt, error) {
	return s.executeScoped(ctx, scopedCaller{actor: "owner", binding: origin}, "turn.cancel", request)
}

// AdmitLocal reserves a concrete file operation. No effect is performed here or
// by BeginLocal. External operation classes have no producer in this build.
func (a *Agent) AdmitLocal(ctx context.Context, request LocalAdmission) (ScopedReceipt, error) {
	if !classOperation("external", request.LocalOperation) {
		return a.command(ctx, "local.admit", request)
	}
	if a == nil || a.service == nil {
		return ScopedReceipt{}, ErrNotBound
	}
	if !validID(request.CommandID) || !validID(request.WorkID) || !validID(request.ParentTurnID) || !validID(request.GrantID) || request.ExpectedVersion != 0 || request.ToolSchema <= 0 || request.MaxBytes < 0 || request.ArgumentsSHA256 == (ledger.Digest{}) || !validResource(request.Target) {
		return ScopedReceipt{}, ErrInvalid
	}
	s := a.service
	done, err := s.scopedQuery(ctx, &a.ids)
	if err != nil {
		return ScopedReceipt{}, err
	}
	defer done()
	if _, err := s.authorize(ctx, originOf(a.ids), request.GrantID, request.LocalOperation, []ledger.Resource{request.Target}, nil, request.MaxBytes, s.p.through); err != nil {
		return ScopedReceipt{}, err
	}
	return ScopedReceipt{}, fmt.Errorf("%w: producer_absent", ErrUnavailable)
}

// BeginLocal durably records the trusted host's dispatch boundary. Its receipt
// identifies the original admitted target; it is not an executor or a reusable
// permit, and replaying it is not another dispatch.
func (s *Service) BeginLocal(ctx context.Context, request LocalDispatch) (ScopedReceipt, error) {
	return s.executeScoped(ctx, scopedCaller{actor: "core"}, "local.begin", request)
}

func (s *Service) EndLocal(ctx context.Context, request LocalEnding) (ScopedReceipt, error) {
	return s.executeScoped(ctx, scopedCaller{actor: "core"}, "local.end", request)
}

func (s *Service) EndTurn(ctx context.Context, request TurnEnding) (ScopedReceipt, error) {
	return s.executeScoped(ctx, scopedCaller{actor: "core"}, "turn.end", request)
}

// ReconcileAdmissions is an explicit, one-work recovery decision. It never
// starts work. An already recorded ending is returned without another record;
// unknown local work retains its holds until the host proves a known outcome.
func (s *Service) ReconcileAdmissions(ctx context.Context, request EndingRequest) (ScopedReceipt, error) {
	if !validID(request.CommandID) || !validID(request.WorkID) || request.ExpectedVersion == 0 || request.ExpectedVersion >= math.MaxInt64 {
		return ScopedReceipt{}, ErrInvalid
	}
	done, err := s.scopedQuery(ctx, nil)
	if err != nil {
		return ScopedReceipt{}, err
	}
	work, found, err := s.loadWork(ctx, request.WorkID, s.p.through)
	if err != nil || !found {
		done()
		if err == nil {
			err = ErrUnavailable
		}
		return ScopedReceipt{}, err
	}
	if work.ended {
		receipt := work.receipt
		receipt.Target.Slots = slices.Clone(receipt.Target.Slots)
		done()
		return receipt, nil
	}
	done()
	if work.admission.Kind == "turn" {
		return s.EndTurn(ctx, TurnEnding{CommandID: request.CommandID, TurnID: request.WorkID, ExpectedVersion: request.ExpectedVersion, Outcome: "interrupted"})
	}
	outcome := "released_unsent"
	if work.dispatched {
		outcome = "unknown"
	}
	return s.EndLocal(ctx, LocalEnding{CommandID: request.CommandID, WorkID: request.WorkID, ExpectedVersion: request.ExpectedVersion, Outcome: outcome})
}

// TurnStatus also accepts a local work ID so an ended-but-held child is not
// confused with an undispatched admission. Agent views are session-exact.
func (s *Service) TurnStatus(ctx context.Context, request WorkLookup) (WorkView, error) {
	done, err := s.scopedQuery(ctx, nil)
	if err != nil {
		return WorkView{}, err
	}
	defer done()
	if !validID(request.WorkID) || request.GrantID != (uuid.UUID{}) {
		return WorkView{}, ErrInvalid
	}
	work, found, err := s.loadWork(ctx, request.WorkID, s.p.through)
	if err != nil {
		return WorkView{}, err
	}
	if !found {
		return WorkView{}, ErrUnavailable
	}
	return s.workView(work)
}

func (a *Agent) TurnStatus(ctx context.Context, request WorkLookup) (WorkView, error) {
	if a == nil || a.service == nil {
		return WorkView{}, ErrNotBound
	}
	s := a.service
	done, err := s.scopedQuery(ctx, &a.ids)
	if err != nil {
		return WorkView{}, err
	}
	defer done()
	origin := originOf(a.ids)
	target := ledger.Resource{Kind: "context", Binding: ledger.Binding(bindingOf(origin)), Scope: "context", Slots: []string{}}
	if _, err := s.authorize(ctx, origin, request.GrantID, "turn.inspect", []ledger.Resource{target}, nil, 0, s.p.through); err != nil {
		return WorkView{}, err
	}
	if !validID(request.WorkID) {
		return WorkView{}, ErrInvalid
	}
	work, found, err := s.loadWork(ctx, request.WorkID, s.p.through)
	if err != nil {
		return WorkView{}, err
	}
	if !found || work.origin != origin {
		return WorkView{}, ErrDenied
	}
	return s.workView(work)
}
