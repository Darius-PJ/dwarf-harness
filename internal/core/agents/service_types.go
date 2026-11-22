package agents

import (
	"errors"
	"maps"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

var ErrServicePageStale = errors.New("agents: service outage page is stale")

// ServiceCauseView is availability metadata, never an execution permission.
type ServiceCauseView struct {
	CauseID, OutageID                       uuid.UUID
	Cause                                   ledger.ServiceCauseSpec
	CauseVersion, MemberVersion, Generation int64
	Phase, Condition                        string
	FirstSource                             ledger.ServiceSource
	DueUTCNS                                *int64
}

type ServiceRecovery struct {
	UncoveredObservations int
	LastErrorClass        string
}

type ServiceView struct {
	AgentID                      uuid.UUID
	State                        string // available, lost or unsupported; pending does not imply loss
	Active, Pending, Unsupported []ServiceCauseView
	ThroughSequence              uint64
	SourceThrough                int64
	Recovery                     ServiceRecovery
}

type ServiceOutageRow struct {
	OutageID, CauseID, AgentID uuid.UUID
	Cause                      ledger.ServiceCauseSpec
	Generation                 int64
	Active                     bool
	Unsupported                bool
	BeginNoticeID, EndNoticeID uuid.UUID
	BeginSequence, EndSequence uint64
	Members, ActiveMembers     []uuid.UUID
}

// A cursor belongs to one caller/filter and one immutable current-head page.
type ServiceOutageCursor struct {
	OutageID, AgentID                      uuid.UUID
	ThroughSequence                        uint64
	CallerID, FilterAgentID, FilterCauseID uuid.UUID
}

type ServiceOutageQuery struct {
	CallerID, AgentID, CauseID uuid.UUID
	Limit                      int
	Next                       *ServiceOutageCursor
}

type ServiceOutagePage struct {
	Rows            []ServiceOutageRow
	ThroughSequence uint64
	Next            *ServiceOutageCursor
}

type ServiceSnapshot struct {
	Agents          []ServiceView
	Outages         []ServiceOutageRow
	ThroughSequence uint64
	SourceThrough   int64
	Recovery        ServiceRecovery
}

// ServiceCompletionObject is stored separately from financial response objects.
// Its entries are the complete, CauseID-sorted selection at ThroughSequence.
type ServiceCompletionObject struct {
	Schema          int64                            `json:"schema"`
	Kind            string                           `json:"kind"`
	ObservationID   uuid.UUID                        `json:"observation_id"`
	ThroughSequence int64                            `json:"through_sequence"`
	WorksetSHA256   ledger.Digest                    `json:"workset_sha256,omitzero"`
	Permits         []ledger.ServiceCompletionPermit `json:"permits"`
}

type serviceMemberKey struct{ agent, cause uuid.UUID }
type serviceAction struct{ observation, cause uuid.UUID }
type serviceCause struct {
	id          uuid.UUID
	key         ledger.Digest
	spec        ledger.ServiceCauseSpec
	snapshot    ledger.RuntimeServiceCauseChanged
	sequence    uint64
	unsupported bool
}
type serviceMember struct {
	snapshot                ledger.RuntimeServiceAgentChanged
	sequence, onsetSequence uint64
	unsupported             bool
}
type serviceOutageMember struct {
	generation int64
	active     bool
}
type serviceOutage struct {
	id, cause, beginNotice, endNotice uuid.UUID
	beginSequence, endSequence        uint64
	members                           map[uuid.UUID]serviceOutageMember
}
type serviceObservation struct {
	observed       ledger.RuntimeServiceObserved
	receipt        ledger.RuntimeServiceReceipt
	sequence, last uint64
	completion     *ServiceCompletionObject
	selected       []ledger.ServiceCompletionPermit
	issue          *IntegrityIssue
	unsupported    bool
	// semantic is serviceSemantic(observed), set once by the reducer on the
	// live and replay paths. This in-memory entry never crosses JSON.
	semantic ledger.Digest
}
type serviceProjection struct {
	causes          map[uuid.UUID]serviceCause
	keys            map[ledger.Digest]uuid.UUID
	members         map[serviceMemberKey]serviceMember
	outages         map[uuid.UUID]serviceOutage
	open            map[uuid.UUID]uuid.UUID
	notices         map[uuid.UUID]bool
	observations    map[uuid.UUID]serviceObservation
	order           []uuid.UUID
	semantic        map[ledger.Digest]uuid.UUID
	applied         map[serviceAction]bool
	through         int64
	recordedThrough int64
}

func newServiceProjection() serviceProjection {
	return serviceProjection{causes: make(map[uuid.UUID]serviceCause), keys: make(map[ledger.Digest]uuid.UUID), members: make(map[serviceMemberKey]serviceMember), outages: make(map[uuid.UUID]serviceOutage), open: make(map[uuid.UUID]uuid.UUID), notices: make(map[uuid.UUID]bool), observations: make(map[uuid.UUID]serviceObservation), semantic: make(map[ledger.Digest]uuid.UUID), applied: make(map[serviceAction]bool)}
}
func (p serviceProjection) clone() serviceProjection {
	p.causes = maps.Clone(p.causes)
	p.keys = maps.Clone(p.keys)
	p.members = maps.Clone(p.members)
	p.outages = maps.Clone(p.outages)
	p.open = maps.Clone(p.open)
	p.notices = maps.Clone(p.notices)
	p.observations = maps.Clone(p.observations)
	p.order = slices.Clone(p.order)
	p.semantic = maps.Clone(p.semantic)
	p.applied = maps.Clone(p.applied)
	return p
}

type serviceGroup struct {
	first    uint64
	command  ledger.Command
	observed *ledger.RuntimeServiceObserved
	cause    *ledger.RuntimeServiceCauseChanged
	member   *ledger.RuntimeServiceAgentChanged
	notice   *ledger.RuntimeServiceOutageNotice
	receipt  ledger.RuntimeServiceReceipt
}
type servicePlan struct {
	next     serviceProjection
	observed *ledger.RuntimeServiceObserved
	cause    *ledger.RuntimeServiceCauseChanged
	member   *ledger.RuntimeServiceAgentChanged
	notice   *ledger.RuntimeServiceOutageNotice
}
