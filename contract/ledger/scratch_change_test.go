package ledger

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"uuid"
)

// These fixtures cover ticket 07's part of F2, proposed for the owner's
// freeze (D113), through its public payload encoders and decoders.
func decodeF2_07(typ string, js json.RawMessage) (payload, error) {
	switch typ {
	case TypeScratchChanged:
		return DecodeScratchChanged(js)
	case TypeScratchMergeBack:
		return DecodeScratchMergeBack(js)
	}
	panic("not an F2-07 type: " + typ)
}

func f2_07Payloads() []struct {
	name string
	typ  string
	p    payload
} {
	observation := ScratchChanged{
		ObservationID: fid("58"), WorkspaceID: fid("43"), ScratchID: fid("53"), ContextID: fid("44"),
		PreviousCommit: strings.Repeat("c0", 20), PreviousTree: strings.Repeat("c1", 20),
		ScratchCommit: strings.Repeat("c2", 20), ScratchTree: strings.Repeat("c3", 20),
		WrittenPaths: []string{"task.txt"}, ChangedPaths: []string{"task.txt"},
		InputsRevision: 1, CaptureInputsSHA256: goldenB1, ScratchRules: "scratch-rules/1", ManifestSHA256: goldenB2, PinID: fid("a8"),
	}
	temporary := observation
	temporary.ScratchID, temporary.ContextID, temporary.ParentScratchID = fid("55"), fid("45"), fid("53")
	prepared := ScratchMergeBack{
		MergeBackID: fid("95"), WorkspaceID: fid("43"), ScratchID: fid("55"), ParentScratchID: fid("53"),
		ContextID: fid("44"), AuthoringContextID: fid("45"), SourceObservationID: fid("59"),
		ForkCommit: strings.Repeat("c2", 20), ForkTree: strings.Repeat("c3", 20),
		ParentCommit: strings.Repeat("c2", 20), ParentTree: strings.Repeat("c3", 20),
		SourceCommit: strings.Repeat("c4", 20), SourceTree: strings.Repeat("c5", 20),
		InputsRevision: 1, CaptureInputsSHA256: goldenB1, IntegrationRules: "scratch-merge-rules/1", Outcome: MergeBackPrepared,
		TargetCommit: strings.Repeat("c4", 20), TargetTree: strings.Repeat("c5", 20),
		WrittenPaths: []string{"new.txt"}, ManifestSHA256: goldenB2, PinID: fid("ae"),
	}
	moved := prepared
	moved.ParentCommit, moved.ParentTree = strings.Repeat("c8", 20), strings.Repeat("c9", 20)
	moved.TargetCommit, moved.TargetTree = strings.Repeat("ca", 20), strings.Repeat("cb", 20)
	failed := prepared
	failed.Outcome, failed.ManifestSHA256, failed.PinID, failed.Failure = MergeBackWriteFailed, Digest{}, uuid.UUID{}, MergeBackFailureIndexLock
	failedMoved := moved
	failedMoved.Outcome, failedMoved.ManifestSHA256, failedMoved.PinID, failedMoved.Failure = MergeBackWriteFailed, Digest{}, uuid.UUID{}, MergeBackFailureGit
	fastForward := failed
	fastForward.Outcome, fastForward.Failure, fastForward.ChangedPaths = MergeBackFastForward, "", []string{"new.txt", "task.txt"}
	merged := failedMoved
	merged.Outcome, merged.Failure, merged.ChangedPaths = MergeBackMerged, "", []string{"new.txt", "other.txt", "task.txt"}
	conflict := moved
	conflict.Outcome, conflict.TargetCommit, conflict.TargetTree = MergeBackConflict, "", ""
	conflict.ManifestSHA256, conflict.PinID = Digest{}, uuid.UUID{}
	conflict.ConflictSHA256, conflict.ReportPinID = goldenB3, fid("b4")
	return []struct {
		name string
		typ  string
		p    payload
	}{
		{"observation", TypeScratchChanged, observation},
		{"temporary observation", TypeScratchChanged, temporary},
		{"prepared fast forward", TypeScratchMergeBack, prepared},
		{"prepared merge", TypeScratchMergeBack, moved},
		{"write failed fast forward", TypeScratchMergeBack, failed},
		{"write failed merge", TypeScratchMergeBack, failedMoved},
		{"fast forward", TypeScratchMergeBack, fastForward},
		{"merged", TypeScratchMergeBack, merged},
		{"conflict", TypeScratchMergeBack, conflict},
	}
}

func TestF2_07PayloadsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range f2_07Payloads() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			values := []payload{tc.p}
			// Preserve Git-ID equalities while covering the other object format.
			wide := reflect.New(reflect.TypeOf(tc.p)).Elem()
			wide.Set(reflect.ValueOf(tc.p))
			for i := range wide.NumField() {
				key, _, _ := strings.Cut(wide.Type().Field(i).Tag.Get("json"), ",")
				if strings.HasSuffix(key, "_commit") || strings.HasSuffix(key, "_tree") {
					if s := wide.Field(i).String(); s != "" {
						wide.Field(i).SetString(strings.Repeat(s[:2], 32))
					}
				}
			}
			values = append(values, wide.Interface().(payload))
			switch p := tc.p.(type) {
			case ScratchChanged:
				p.InputsRevision, p.ScratchRules = 1<<63-1, strings.Repeat("a", 62)+"/1"
				p.WrittenPaths, p.ChangedPaths = []string{"a\tb\nc", "é/日本語"}, []string{}
				values = append(values, p)
				p.WrittenPaths = []string{"A", "a", "e\u0301", strings.Repeat("x", 4097), "é", "\x7f"}
				slices.Sort(p.WrittenPaths)
				values = append(values, p)
				p.ScratchRules, p.ScratchTree, p.WrittenPaths = "a0-/123", p.PreviousTree, []string{}
				values = append(values, p)
				p.WrittenPaths, p.ChangedPaths = nil, nil
				values = append(values, p)
			case ScratchMergeBack:
				p.InputsRevision, p.IntegrationRules = 1<<63-1, strings.Repeat("a", 62)+"/1"
				p.WrittenPaths = []string{"a\tb\nc", "é/日本語"}
				if p.Outcome == MergeBackWriteFailed {
					p.Failure = "future_failure"
				}
				if p.ChangedPaths != nil {
					p.ChangedPaths = []string{}
				}
				values = append(values, p)
				if p.Outcome == MergeBackWriteFailed {
					p.Failure = strings.Repeat("a", 64)
					values = append(values, p)
				}
				p.IntegrationRules, p.ForkTree, p.WrittenPaths = "a0-/123", p.SourceTree, []string{}
				values = append(values, p)
				p.WrittenPaths = nil
				values = append(values, p)
			}
			for _, value := range values {
				js := mustEncode(value)
				want := value
				switch p := value.(type) {
				case ScratchChanged:
					if p.WrittenPaths == nil {
						p.WrittenPaths = []string{}
					}
					if p.ChangedPaths == nil {
						p.ChangedPaths = []string{}
					}
					want = p
				case ScratchMergeBack:
					if p.WrittenPaths == nil {
						p.WrittenPaths = []string{}
					}
					want = p
				}
				got, err := decodeF2_07(tc.typ, js)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("Decode(%s) = %+v, %v; want %+v", js, got, err, want)
				}
				if again, err := got.Encode(); err != nil || !bytes.Equal(again, js) {
					t.Fatalf("re-encoding = %s, %v; want %s", again, err, js)
				}
				extended := append([]byte(`{"later":{"a":[1,null],"b":"x"},`), js[1:]...)
				if got, err := decodeF2_07(tc.typ, extended); err != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("unknown member changed decoding: %+v, %v; want %+v", got, err, want)
				}
			}
		})
	}
}

func TestF2_07PayloadsRefuse(t *testing.T) {
	t.Parallel()
	for _, tc := range f2_07Payloads() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			js := mustEncode(tc.p)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(js, &fields); err != nil {
				t.Fatal(err)
			}
			refuse := func(name string, bad []byte, checkEncode bool) {
				t.Helper()
				t.Run(name, func(t *testing.T) {
					if _, err := decodeF2_07(tc.typ, bad); !errors.Is(err, ErrInvalid) {
						t.Errorf("Decode(%s) = %v; want ErrInvalid", bad, err)
					}
					if !checkEncode {
						return
					}
					// Build the Go value independently of the contract decoder,
					// as in F2-06; Digest has no UnmarshalText.
					var raw map[string]json.RawMessage
					if err := json.Unmarshal(bad, &raw); err != nil {
						t.Fatal(err)
					}
					p := reflect.New(reflect.TypeOf(tc.p)).Elem()
					for i := range p.NumField() {
						key, _, _ := strings.Cut(p.Type().Field(i).Tag.Get("json"), ",")
						value, present := raw[key]
						if !present {
							continue
						}
						field := p.Field(i)
						if field.Type() == reflect.TypeFor[Digest]() {
							var text string
							if json.Unmarshal(value, &text) != nil {
								return
							}
							digest, err := hex.DecodeString(text)
							if err != nil || len(digest) != len(Digest{}) {
								return
							}
							field.Set(reflect.ValueOf(Digest(digest)))
						} else if json.Unmarshal(value, field.Addr().Interface()) != nil {
							return
						}
					}
					if out, err := p.Interface().(payload).Encode(); !errors.Is(err, ErrInvalid) || out != nil {
						t.Errorf("Encode(%s) = %s, %v; want nil, ErrInvalid", bad, out, err)
					}
				})
			}
			mutate := func(name, key, value string, checkEncode bool) {
				t.Helper()
				bad := maps.Clone(fields)
				if value == "" {
					delete(bad, key)
				} else {
					bad[key] = json.RawMessage(value)
				}
				data, err := json.Marshal(bad)
				if err != nil {
					t.Fatal(err)
				}
				refuse(name, data, checkEncode)
			}
			optional := map[string]bool{"parent_scratch_id": true}
			if tc.typ == TypeScratchMergeBack {
				optional = map[string]bool{
					"target_commit": true, "target_tree": true, "changed_paths": true,
					"manifest_sha256": true, "pin_id": true, "conflict_sha256": true,
					"report_pin_id": true, "failure": true,
				}
			}
			for _, key := range slices.Sorted(maps.Keys(optional)) {
				if _, present := fields[key]; present {
					continue
				}
				mutate("null "+key, key, "null", false)
				mutate("empty "+key, key, `""`, false)
				mutate("non-string "+key, key, `1`, false)
				if strings.HasSuffix(key, "_id") {
					mutate("zero "+key, key, `"00000000-0000-0000-0000-000000000000"`, false)
					mutate("non-v7 "+key, key, `"6f1c2b6e-4b1d-4c3a-9e2f-0a1b2c3d4e5f"`, true)
				} else if strings.HasSuffix(key, "_sha256") {
					mutate("zero "+key, key, `"`+ZeroHash+`"`, false)
				}
			}
			for _, key := range slices.Sorted(maps.Keys(fields)) {
				value := fields[key]
				if !optional[key] {
					mutate("missing "+key, key, "", key != "written_paths" && key != "changed_paths")
				}
				mutate("null "+key, key, "null", false)
				refuse("repeated "+key, append([]byte(`{"`+key+`":`+string(value)+`,`), js[1:]...), false)
				switch {
				case strings.HasSuffix(key, "_id"):
					mutate("non-v7 "+key, key, `"6f1c2b6e-4b1d-4c3a-9e2f-0a1b2c3d4e5f"`, true)
					mutate("uppercase "+key, key, `"0199C000-0000-7000-8000-000000000053"`, false)
					mutate("zero "+key, key, `"00000000-0000-0000-0000-000000000000"`, !optional[key])
					mutate("empty "+key, key, `""`, !optional[key])
					mutate("non-string "+key, key, `1`, false)
				case strings.HasSuffix(key, "_sha256"):
					mutate("zero "+key, key, `"`+ZeroHash+`"`, !optional[key])
					mutate("uppercase "+key, key, `"`+strings.ToUpper(goldenB1.String())+`"`, false)
					mutate("empty "+key, key, `""`, false)
					mutate("non-string "+key, key, `1`, false)
				case strings.HasSuffix(key, "_commit"), strings.HasSuffix(key, "_tree"):
					for _, value := range []string{`""`, `"` + strings.Repeat("A", 40) + `"`, `"` + strings.Repeat("a", 39) + `"`, `"` + strings.Repeat("a", 63) + `"`, `"g` + strings.Repeat("a", 39) + `"`, `1`} {
						mutate(key+"="+value, key, value, true)
					}
				case key == "inputs_revision":
					for _, value := range []string{`0`, `-1`, `1.5`, `1e1`, `9223372036854775808`, `"1"`} {
						mutate(key+"="+value, key, value, true)
					}
					refuse("signed "+key, bytes.Replace(js, []byte(`"`+key+`":`+string(value)), []byte(`"`+key+`":+1`), 1), false)
				case strings.HasSuffix(key, "_rules"):
					for _, value := range []string{`""`, `"Rules/1"`, `"rules_name/1"`, `"1rules/1"`, `"rules/0"`, `"rules/01"`, `"rules/+1"`, `"rules/1/2"`, `"rules/1x"`, `"rules/"`, `"` + strings.Repeat("a", 63) + `/1"`, `1`} {
						mutate(key+"="+value, key, value, true)
					}
				case key == "written_paths", key == "changed_paths":
					for _, value := range []string{`{}`, `"path"`, `[1]`, `[null]`, `[""]`, `["/a"]`, `["a/"]`, `["a//b"]`, `["a/./b"]`, `["a/../b"]`, `["a\\b"]`, `["a\u0000b"]`, `["a","a"]`, `["b","a"]`, `["é","z"]`} {
						mutate(key+"="+value, key, value, true)
					}
					bad := bytes.Replace(js, value, []byte("[\"a\xffb\"]"), 1)
					refuse("invalid UTF-8 "+key, bad, false)
					p := reflect.New(reflect.TypeOf(tc.p)).Elem()
					p.Set(reflect.ValueOf(tc.p))
					field := "WrittenPaths"
					if key == "changed_paths" {
						field = "ChangedPaths"
					}
					p.FieldByName(field).Set(reflect.ValueOf([]string{"a\xffb"}))
					if _, err := p.Interface().(payload).Encode(); !errors.Is(err, ErrInvalid) {
						t.Errorf("Encode invalid UTF-8 %s = %v; want ErrInvalid", key, err)
					}
				}
			}
			refuse("not object", []byte(`[]`), false)
			refuse("malformed", []byte(`{"`), false)
			refuse("nested duplicate", append([]byte(`{"later":{"x":1,"x":2},`), js[1:]...), false)
			switch p := tc.p.(type) {
			case ScratchChanged:
				mutate("same commit", "scratch_commit", `"`+p.PreviousCommit+`"`, true)
				mutate("empty paths different trees", "written_paths", `[]`, true)
				mutate("nonempty paths equal trees", "scratch_tree", `"`+p.PreviousTree+`"`, true)
			case ScratchMergeBack:
				mutate("unknown outcome", "outcome", `"future"`, true)
				if p.Outcome == MergeBackPrepared {
					// Isolate the closed outcome grammar from the variant
					// fields, which otherwise also make an unknown invalid.
					future := p
					future.Outcome, future.ManifestSHA256, future.PinID = "future", Digest{}, uuid.UUID{}
					bad, err := json.Marshal(future)
					if err != nil {
						t.Fatal(err)
					}
					refuse("unknown outcome without variant fields", bad, true)
				}
				mutate("empty paths different trees", "written_paths", `[]`, true)
				mutate("nonempty paths equal trees", "fork_tree", `"`+p.SourceTree+`"`, true)
				mutate("same context", "authoring_context_id", `"`+p.ContextID.String()+`"`, true)
				if p.Outcome == MergeBackConflict {
					mutate("conflict with target commit", "target_commit", `"`+p.SourceCommit+`"`, true)
					mutate("conflict with target tree", "target_tree", `"`+p.SourceTree+`"`, true)
				} else {
					mutate("without target commit", "target_commit", "", true)
					mutate("without target tree", "target_tree", "", true)
				}
				if p.Outcome == MergeBackFastForward || p.Outcome == MergeBackMerged {
					mutate("without changed paths", "changed_paths", "", true)
				} else {
					mutate("with changed paths", "changed_paths", `[]`, true)
				}
				if p.Outcome == MergeBackPrepared {
					mutate("without manifest", "manifest_sha256", "", true)
					mutate("without manifest pin", "pin_id", "", true)
				} else {
					mutate("with manifest", "manifest_sha256", `"`+goldenB2.String()+`"`, true)
					mutate("with manifest pin", "pin_id", `"`+fid("ae").String()+`"`, true)
				}
				if p.Outcome == MergeBackConflict {
					mutate("without conflict digest", "conflict_sha256", "", true)
					mutate("without report pin", "report_pin_id", "", true)
				} else {
					mutate("with conflict digest", "conflict_sha256", `"`+goldenB3.String()+`"`, true)
					mutate("with report pin", "report_pin_id", `"`+fid("b4").String()+`"`, true)
				}
				if p.Outcome == MergeBackWriteFailed {
					mutate("without failure", "failure", "", true)
					for _, value := range []string{`""`, `"Git"`, `"index-lock"`, `"1st"`, `"has.dot"`, `"` + strings.Repeat("a", 65) + `"`, `1`} {
						mutate("failure="+value, "failure", value, true)
					}
				} else {
					mutate("with failure", "failure", `"git"`, true)
				}
				switch p.Outcome {
				case MergeBackFastForward:
					mutate("fast forward moved parent", "parent_commit", `"`+strings.Repeat("cf", 20)+`"`, true)
					mutate("fast forward wrong target commit", "target_commit", `"`+strings.Repeat("cf", 20)+`"`, true)
					mutate("fast forward wrong target tree", "target_tree", `"`+strings.Repeat("cf", 20)+`"`, true)
				case MergeBackMerged:
					mutate("merged unmoved parent", "parent_commit", `"`+p.ForkCommit+`"`, true)
					mutate("merged source target", "target_commit", `"`+p.SourceCommit+`"`, true)
				case MergeBackPrepared, MergeBackWriteFailed:
					if p.ParentCommit == p.ForkCommit {
						mutate("unmoved parent different target", "target_commit", `"`+strings.Repeat("cf", 20)+`"`, true)
					} else {
						mutate("moved parent source target", "target_commit", `"`+p.SourceCommit+`"`, true)
					}
				}
			}
		})
	}
}
