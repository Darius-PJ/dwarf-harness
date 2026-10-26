package agents

import (
	"bytes"
	"context"
	"math/big"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type paidCredentialScope struct {
	version  int64
	provider string
}
type paidAttempt struct {
	admission                         ledger.RuntimePaidReserved
	profile                           ledger.Profile
	origin                            ledger.Origin
	version                           int64
	sequence, resultSequence          uint64
	intent                            *ledger.RuntimePaidDispatchIntent
	result                            *ledger.RuntimePaidResult
	held, ended, fenced, acknowledged bool
	cost                              int64
	state                             string
	receipt                           ledger.RuntimePaidReceipt
	receiptSequence                   uint64
	evidence                          map[ledger.Digest]uint64
}
type paidPlan struct {
	route      *ledger.RuntimePaidRouteConfigured
	credential *ledger.RuntimeCredentialChanged
	spend      *ledger.RuntimeSpendConfigured
	attempt    *paidAttempt
	prior      paidAttempt
	paid       map[ledger.PaidScope]paidAccount
	local      map[ledger.ScopeKey]budgetAccount
	grants     []grantState
	tasks      []scopedTaskState
	observed   int64
	period     string
	retired    ledger.Digest
}

func (p *projection) installPaid(plan paidPlan) {
	if v := plan.route; v != nil {
		p.paidRoutes[v.RouteID] = *v
		digest, _ := v.Profile.SHA256()
		tariff, _ := v.Profile.TariffSHA256()
		p.paidCapabilities[v.Profile.CapabilityVersion] = digest
		p.paidTariffs[v.Profile.TariffVersion] = tariff
	}
	if v := plan.credential; v != nil {
		p.paidCredentialScopes[v.ScopeID] = paidCredentialScope{v.Version, v.Provider}
		if v.Action == "bind" {
			p.paidCredentials[v.CredentialVersion] = *v
			if bytes.Compare(v.CredentialVersion[:], p.maxCredentialVersion[:]) > 0 {
				p.maxCredentialVersion = v.CredentialVersion
			}
		} else {
			delete(p.paidCredentials, v.CredentialVersion)
		}
		if v.RevokePreviousVersion != (uuid.UUID{}) {
			delete(p.paidCredentials, v.RevokePreviousVersion)
		}
	}
	if v := plan.spend; v != nil && v.Scope.Kind == "colony" && p.book.paidPeriodKind == "" {
		p.book.paidPeriodKind = v.Terms.PeriodKind
	}
	for scope, account := range plan.paid {
		p.book.paid[scope] = account
	}
	p.book.install(plan.local)
	for _, grant := range plan.grants {
		p.grants[grant.definition.GrantID] = grant
	}
	for _, task := range plan.tasks {
		p.tasks[task.record.TaskID] = task
	}
	if plan.observed > 0 {
		p.reviewFloor = max(p.reviewFloor, plan.observed)
		p.book.paidObserved = max(p.book.paidObserved, plan.observed)
	}
	if p.book.paidObserved > 0 {
		p.book.paidPeriod = paidPeriod(p.book.paidPeriodKind, p.book.paidObserved)
	}
	if plan.retired != (ledger.Digest{}) {
		p.retiredProfiles[plan.retired] = true
	}
	if v := plan.attempt; v != nil {
		id := v.admission.AttemptID
		p.paidAttempts[id] = *v
		if bytes.Compare(id[:], p.maxAttempt[:]) > 0 {
			p.maxAttempt = id
		}
		parent := v.admission.ParentTurnID
		if plan.prior.version == 0 {
			p.paidChildren[parent]++
		}
		if !plan.prior.ended && v.ended {
			p.paidChildren[parent]--
		}
		if p.paidChildren[parent] == 0 {
			delete(p.paidChildren, parent)
		}
	}
	p.prunePaid()
	p.pruneScoped()
}

// Unresolved holds and fences never evict. Settled results are merely a bounded
// hot set; their absence is never proof that an identity is new.
func (p *projection) prunePaid() {
	hot := []uuid.UUID{}
	for id, a := range p.paidAttempts {
		if !a.held && !a.fenced {
			hot = append(hot, id)
		}
	}
	slices.SortFunc(hot, func(a, b uuid.UUID) int {
		x, y := p.paidAttempts[a].receiptSequence, p.paidAttempts[b].receiptSequence
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
		return bytes.Compare(a[:], b[:])
	})
	if len(hot) > p.paidHotLimit {
		for _, id := range hot[:len(hot)-p.paidHotLimit] {
			delete(p.paidAttempts, id)
		}
	}
	for scope, a := range p.book.paid {
		if a.version == 0 && paidInteger(a.held).Sign() == 0 && a.fences == 0 {
			delete(p.book.paid, scope)
		}
	}
}

func (s *Service) credentialVersionExists(ctx context.Context, id uuid.UUID, through uint64) (bool, error) {
	if bytes.Compare(id[:], s.p.maxCredentialVersion[:]) > 0 {
		return false, nil
	}
	found := false
	err := s.scan(ctx, 0, through, func(c ledger.Commit) error {
		for _, r := range c.Records {
			if r.Type != ledger.TypeRuntimeCredentialChanged {
				continue
			}
			v, err := ledger.DecodeRuntimeCredentialChanged(r.Payload)
			if err != nil {
				return err
			}
			found = found || v.Action == "bind" && v.CredentialVersion == id
		}
		return nil
	})
	return found, err
}

// loadPaid is one explicit cold scan for a historical ID, retaining only that
// attempt and its admission tariff. It does not read an object or re-authorize
// a fact which was validated at its original ledger position.
func (s *Service) loadPaid(ctx context.Context, id uuid.UUID, through uint64, evidence ledger.Digest) (paidAttempt, bool, error) {
	if bytes.Compare(id[:], s.p.maxAttempt[:]) > 0 {
		return paidAttempt{}, false, nil
	}
	if a, ok := s.p.paidAttempts[id]; ok {
		if evidence != (ledger.Digest{}) && a.evidence[evidence] != 0 {
			return a, true, ErrNoEffect
		}
		return a, true, nil
	}
	var a paidAttempt
	routes := map[uuid.UUID]ledger.Profile{}
	err := s.scan(ctx, 0, through, func(c ledger.Commit) error {
		_, _, _, g, err := decodeFamily(draftsOf(c), c.First)
		if err != nil || g == nil {
			return err
		}
		if g.route != nil {
			routes[g.route.RouteID] = g.route.Profile
		}
		if v := g.reserved; v != nil && v.AttemptID == id {
			a = paidAttempt{admission: *v, profile: routes[v.RouteID], origin: originOf(g.ids), version: 1, sequence: c.First + 1, held: true, state: "reserved"}
		}
		if a.version == 0 || g.receipt.AttemptID != id {
			return nil
		}
		if g.intent != nil {
			a.intent, a.version, a.state = g.intent, g.intent.Version, "sent"
		}
		if g.result != nil {
			a, err = paidResultState(a, *g.result, c.First+1, g.receipt.Outcome)
			if err != nil {
				return err
			}
		}
		if g.acknowledgement != nil {
			a.version, a.fenced, a.acknowledged = g.acknowledgement.Version, false, true
		}
		a.receipt, a.receiptSequence = g.receipt, c.Last
		return nil
	})
	if err == nil && evidence != (ledger.Digest{}) && a.evidence[evidence] != 0 {
		err = ErrNoEffect
	}
	return a, a.version != 0, err
}

// Historical period queries and first enrollment rebuild one selected scope in
// one cold pass. This operation-local map is never installed as a history index.
func (s *Service) paidHistoryTotal(ctx context.Context, scope ledger.PaidScope, period string, origin *ledger.Origin, through uint64) (*big.Int, *big.Int, bool, error) {
	attempts := map[uuid.UUID]paidAttempt{}
	routes := map[uuid.UUID]ledger.Profile{}
	err := s.scan(ctx, 0, through, func(c ledger.Commit) error {
		_, _, _, g, err := decodeFamily(draftsOf(c), c.First)
		if err != nil || g == nil {
			return err
		}
		if g.route != nil {
			routes[g.route.RouteID] = g.route.Profile
		}
		if v := g.reserved; v != nil && slices.Contains(v.Scopes, scope) && (origin == nil || *origin == originOf(g.ids)) {
			attempts[v.AttemptID] = paidAttempt{admission: *v, profile: routes[v.RouteID], version: 1, held: true, state: "reserved"}
		}
		a, found := attempts[g.receipt.AttemptID]
		if !found {
			return nil
		}
		if g.intent != nil {
			a.intent, a.version = g.intent, g.intent.Version
		}
		if g.result != nil {
			a, err = paidResultState(a, *g.result, c.First+1, g.receipt.Outcome)
			if err != nil {
				return err
			}
		}
		if g.acknowledgement != nil {
			a.version, a.fenced, a.acknowledged = g.acknowledgement.Version, false, true
		}
		attempts[g.receipt.AttemptID] = a
		return nil
	})
	settled, held := new(big.Int), new(big.Int)
	for _, a := range attempts {
		if a.admission.AdmissionPeriod == period {
			settled.Add(settled, big.NewInt(a.cost))
		}
		if a.held {
			held.Add(held, big.NewInt(a.admission.Quote.MaximumMicros))
		}
	}
	return settled, held, len(attempts) != 0, err
}
