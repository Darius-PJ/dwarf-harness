package agents

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func loopInputSeeds() [][]byte {
	input := loopInputObject{Schema: 1, Kind: "owner_input", Origin: ledger.Origin{AgentID: privateID(83000), SessionID: privateID(83001), ContextID: privateID(83002)}, TurnID: privateID(83003), Text: "Synthetic natural input.", Artifacts: []ledger.ArtifactRef{}, OtherWork: []string{}}
	seeds := [][]byte{modelJSON(input)}
	input.Schema, input.TaskResults, input.NoticeCursors = 2, []loopTaskResult{}, []ledger.ContinuationNoticeCursor{}
	seeds = append(seeds, modelJSON(input))
	input.TaskResults = []loopTaskResult{{Sequence: 19, MergeBackID: privateID(83004), ScratchID: privateID(83005), ParentScratchID: privateID(83006), AuthoringContextID: privateID(83007), Outcome: ledger.MergeBackFastForward, WrittenPaths: []string{"safe/result.txt"}, ChangedPaths: []string{"safe/result.txt"}}}
	input.NoticeCursors = []ledger.ContinuationNoticeCursor{{WorkspaceID: privateID(83008), ScratchID: privateID(83006), AfterSequence: 19}}
	seeds = append(seeds, modelJSON(input))
	input.TaskResults[0].Outcome, input.TaskResults[0].ChangedPaths = ledger.MergeBackConflict, []string{}
	input.TaskResults[0].ConflictSHA256 = sha256.Sum256([]byte("synthetic conflict reference"))
	seeds = append(seeds, modelJSON(input))
	return seeds
}

// This is the schema-1 acceptance oracle at S4's base, not a strict new
// decoder: unknown fields, including future-schema-looking fields, stay ignored.
func legacyLoopInputAccepts(raw []byte) bool {
	var wire struct {
		Schema    int           `json:"schema"`
		Kind      string        `json:"kind"`
		Origin    ledger.Origin `json:"origin"`
		TurnID    uuid.UUID     `json:"turn_id"`
		Text      string        `json:"text"`
		Artifacts []struct {
			SourceSequence int64  `json:"source_sequence"`
			SHA256         string `json:"sha256"`
			Bytes          int64  `json:"bytes"`
		} `json:"artifacts"`
		OtherWork []string `json:"other_work"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Schema != 1 || wire.Kind != "owner_input" {
		return false
	}
	for _, ref := range wire.Artifacts {
		if _, err := ledger.ParseDigest(ref.SHA256); err != nil {
			return false
		}
	}
	return true
}

func FuzzLoopInput(f *testing.F) {
	seeds := loopInputSeeds()
	for _, seed := range seeds {
		f.Add(seed)
	}
	for _, row := range []struct{ before, after string }{
		{`"task_results":[]`, `"task_results":null`},
		{`"notice_cursors":[]`, `"notice_cursors":null`},
		{`"task_results":[],`, ``},
		{`"schema":2`, `"schema":2,"schema":2`},
	} {
		f.Add(bytes.Replace(seeds[1], []byte(row.before), []byte(row.after), 1))
	}
	f.Add(bytes.Replace(seeds[2], []byte(`"after_sequence":19`), []byte(`"after_sequence":18`), 1))
	f.Add(bytes.Replace(seeds[2], []byte(`"written_paths":["safe/result.txt"]`), []byte(`"written_paths":["../outside"]`), 1))
	f.Add(bytes.Replace(seeds[0], []byte(`"schema":1`), []byte(`"schema":1,"task_results":"ignored"`), 1))
	f.Fuzz(func(t *testing.T, raw []byte) {
		t.Parallel()
		if len(raw) > maxObjectBytes {
			return
		}
		var input loopInputObject
		err := decodeLoopInput(raw, &input)
		legacy := legacyLoopInputAccepts(raw)
		if legacy != (err == nil && input.Schema == 1) {
			t.Fatal("schema-1 acceptance changed")
		}
		if err != nil {
			return
		}
		var first, second strings.Builder
		a, b := renderLoopNotices(&first, input), renderLoopNotices(&second, input)
		if !errors.Is(a, b) || first.String() != second.String() || first.Len() > providers.RequestLimit {
			t.Fatal("input rendering was nondeterministic or exceeded its bound")
		}
		if input.Schema == 1 {
			if first.Len() != 0 {
				t.Fatal("legacy input acquired a result block")
			}
			return
		}
		encoded := modelJSON(input)
		var again loopInputObject
		if input.Schema != 2 || len(encoded) > maxObjectBytes || decodeLoopInput(encoded, &again) != nil || !sameScopedValue(input, again) {
			t.Fatal("accepted schema-2 input is not a bounded stable object")
		}
		if input.TaskResults == nil || input.NoticeCursors == nil || (len(input.TaskResults) == 0) != (first.Len() == 0) {
			t.Fatal("schema-2 arrays or nonempty block rule changed")
		}
		for _, seed := range seeds {
			if bytes.Equal(raw, seed) && a != nil {
				t.Fatal("producer seed did not render", a)
			}
		}
	})
}
