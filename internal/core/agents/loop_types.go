package agents

import (
	"bytes"
	"math"
	"slices"
	"unicode"
	"unicode/utf8"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// TurnPolicy is immutable after admission and is selected by runtime.turn_bound.
// The host supplies a template with DeadlineUTCNS zero and a positive TimeoutMS.
type TurnPolicy struct {
	Schema              int   `json:"schema"`
	MaxModelCalls       int64 `json:"max_model_calls"`
	MaxToolCalls        int64 `json:"max_tool_calls"`
	DeadlineUTCNS       int64 `json:"deadline_utc_ns"`
	MaxArgumentBytes    int64 `json:"max_argument_bytes"`
	MaxResultBytes      int64 `json:"max_result_bytes"`
	MaxDiscoveryEntries int64 `json:"max_discovery_entries"`
	MaxSearchEntries    int64 `json:"max_search_entries"`
	MaxSearchMatches    int64 `json:"max_search_matches"`
	MaxLiveBytes        int64 `json:"max_live_bytes"`
}

// LoopContextSettings selects no caller-provided transport or authorization
// hook. The route, output ceiling and credential scope belong to the host.
type LoopContextSettings struct {
	Binding              Binding
	RouteID              uuid.UUID
	ExpectedRouteVersion int64
	CredentialScopeID    uuid.UUID
	MaxOutputTokens      int64
	DisplayTitle         string
	TaskBinding          *ledger.TaskBinding
	TimeoutMS            int64
	Policy               TurnPolicy
}
type LoopOptions struct{ Contexts []LoopContextSettings }

type LoopRecovery struct {
	Origin          ledger.Origin `json:"origin"`
	TurnID          uuid.UUID     `json:"turn_id"`
	ToolID          uuid.UUID     `json:"tool_id,omitzero"`
	BoundSequence   uint64        `json:"bound_sequence"`
	StartedSequence uint64        `json:"started_sequence,omitempty"`
	State           string        `json:"state"`
}

func (p TurnPolicy) validTemplate() bool {
	return p.Schema == 1 && p.DeadlineUTCNS == 0 && p.MaxModelCalls > 0 && p.MaxModelCalls <= math.MaxInt32 && p.MaxToolCalls > 0 && p.MaxToolCalls <= math.MaxInt32 &&
		p.MaxArgumentBytes > 0 && p.MaxArgumentBytes <= 64<<10 && p.MaxResultBytes >= 512 && p.MaxResultBytes <= 64<<10 &&
		p.MaxDiscoveryEntries > 0 && p.MaxDiscoveryEntries <= int64(len(modelCatalogue)) && p.MaxSearchEntries > 0 && p.MaxSearchEntries <= math.MaxInt32 &&
		p.MaxSearchMatches > 0 && p.MaxSearchMatches <= p.MaxSearchEntries && p.MaxLiveBytes > 0 && p.MaxLiveBytes <= 8<<20
}
func loopDisplayTitle(title string) bool {
	if !utf8.ValidString(title) || len(title) == 0 || len(title) > 128 {
		return false
	}
	for _, r := range title {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (p *projection) pendingLoopViews() []LoopRecovery {
	out := make([]LoopRecovery, 0, len(p.loopTurns)+len(p.loopTools))
	for id, turn := range p.loopTurns {
		out = append(out, LoopRecovery{Origin: turn.origin, TurnID: id, BoundSequence: turn.sequence, State: "recovery_pending"})
	}
	for id, tool := range p.loopTools {
		turn := p.loopTurns[tool.start.ParentTurnID]
		out = append(out, LoopRecovery{Origin: tool.origin, TurnID: tool.start.ParentTurnID, ToolID: id, BoundSequence: turn.sequence, StartedSequence: tool.sequence, State: "recovery_pending"})
	}
	slices.SortFunc(out, func(a, b LoopRecovery) int {
		if c := bytes.Compare(a.TurnID[:], b.TurnID[:]); c != 0 {
			return c
		}
		return bytes.Compare(a.ToolID[:], b.ToolID[:])
	})
	return out
}
