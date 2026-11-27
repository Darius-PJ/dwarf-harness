package agents

import (
	"bytes"
	"context"
	"math"
	"slices"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// SetupIdentity is owner-only metadata, not an agent's view or a permission.
type SetupIdentity struct {
	Binding
	SessionID uuid.UUID
	Directory string
	General   bool
	Busy      bool
}

// SetupSnapshot contains copied current facts needed to review owner commands.
// It never binds credentials, admits work or reads private content.
type SetupSnapshot struct {
	ThroughSequence    uint64
	AuthorityVersion   int64
	Contexts           []SetupIdentity
	Routes             map[uuid.UUID]ledger.RuntimePaidRouteConfigured
	Credentials        []ledger.RuntimeCredentialChanged
	CredentialVersions map[uuid.UUID]int64
	CapacityVersions   map[ledger.ScopeKey]int64
	SpendVersions      map[ledger.PaidScope]int64
}

func (s *Service) OwnerSetupSnapshot(ctx context.Context) (SetupSnapshot, error) {
	done, err := s.scopedQuery(ctx, &historyReadSet{}, nil)
	if err != nil {
		return SetupSnapshot{}, err
	}
	defer done()
	out := SetupSnapshot{ThroughSequence: s.p.through, AuthorityVersion: s.p.authority,
		Contexts: []SetupIdentity{}, Routes: map[uuid.UUID]ledger.RuntimePaidRouteConfigured{},
		Credentials: []ledger.RuntimeCredentialChanged{}, CredentialVersions: map[uuid.UUID]int64{},
		CapacityVersions: map[ledger.ScopeKey]int64{}, SpendVersions: map[ledger.PaidScope]int64{}}
	busy := map[uuid.UUID]bool{}
	for _, w := range s.p.work {
		if w.admission.Kind == "turn" && !w.released {
			busy[w.origin.ContextID] = true
		}
	}
	for _, w := range s.p.loopTurns {
		busy[w.origin.ContextID] = true
	}
	for _, w := range s.p.owners {
		busy[w.Origin.ContextID] = true
	}
	for _, p := range s.p.paidAttempts {
		if !p.ended || p.held {
			busy[p.origin.ContextID] = true
		}
	}
	for id, owner := range s.p.contexts {
		a := s.p.agents[owner]
		out.Contexts = append(out.Contexts, SetupIdentity{Binding: Binding{owner, id}, SessionID: a.session, Directory: a.directory, General: id == a.general, Busy: busy[id]})
	}
	slices.SortFunc(out.Contexts, func(a, b SetupIdentity) int { return bytes.Compare(a.ContextID[:], b.ContextID[:]) })
	for id, route := range s.p.paidRoutes {
		raw, err := route.Encode()
		if err != nil {
			return SetupSnapshot{}, err
		}
		copy, err := ledger.DecodeRuntimePaidRouteConfigured(raw)
		if err != nil {
			return SetupSnapshot{}, err
		}
		out.Routes[id] = copy
	}
	for _, c := range s.p.paidCredentials {
		out.Credentials = append(out.Credentials, c)
	}
	slices.SortFunc(out.Credentials, func(a, b ledger.RuntimeCredentialChanged) int {
		if n := bytes.Compare(a.ScopeID[:], b.ScopeID[:]); n != 0 {
			return n
		}
		return intCompare(a.Version, b.Version)
	})
	for id, c := range s.p.paidCredentialScopes {
		out.CredentialVersions[id] = c.version
	}
	for scope, c := range s.p.book.accounts {
		out.CapacityVersions[scope] = c.version
	}
	for scope, c := range s.p.book.paid {
		out.SpendVersions[scope] = c.version
	}
	return out, nil
}

func intCompare(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// ValidateSetupTemplate applies the current turn template bounds without
// enabling a loop or requiring the forward-referenced route, task or scope.
func ValidateSetupTemplate(setting LoopContextSettings) error {
	if !validID(setting.RouteID) || !validID(setting.CredentialScopeID) || setting.ExpectedRouteVersion <= 0 || setting.MaxOutputTokens <= 0 ||
		setting.TimeoutMS <= 0 || setting.TimeoutMS > math.MaxInt64/int64(time.Millisecond) || !setting.Policy.validTemplate() || !loopDisplayTitle(setting.DisplayTitle) {
		return ErrInvalid
	}
	return nil
}
