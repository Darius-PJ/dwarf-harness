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

// These fixtures cover ticket 06's part of F2, frozen (D118), through
// its public payload encoders and decoders.
func decodeF2_06(typ string, js json.RawMessage) (payload, error) {
	switch typ {
	case TypePromotionAdmitted:
		return DecodePromotionAdmitted(js)
	case TypeGenerationDerived:
		return DecodeGenerationDerived(js)
	case TypePromotionFailed:
		return DecodePromotionFailed(js)
	}
	panic("not an F2-06 type: " + typ)
}

func f2_06Payloads() []struct {
	name string
	typ  string
	p    payload
} {
	root := PromotionAdmitted{
		RequestID: fid("71"), WorkspaceID: fid("61"), ScratchID: fid("62"), ContextID: fid("64"),
		LineageID: fid("81"), Checkpoint: 1,
		ScratchBase: strings.Repeat("a0", 20), BaselineTree: strings.Repeat("a1", 20), DeltaBaseTree: strings.Repeat("a1", 20),
		CaptureID: fid("91"), SealedTree: strings.Repeat("b0", 20), ManifestSHA256: goldenB1,
		InputsRevision: 1, CaptureInputsSHA256: goldenB2, PinID: fid("a3"),
	}
	successor := root
	successor.Checkpoint, successor.ParentRequestID = 2, fid("70")
	restart := root
	restart.RestartFrom = fid("70")
	clean := GenerationDerived{
		GenerationID: fid("11"), RequestID: fid("71"), PredecessorCommit: strings.Repeat("a0", 20),
		PredecessorTree: strings.Repeat("a1", 20), InputsRevision: 1, IntegrationRules: "integration-rules/1",
		Outcome: GenerationClean, DerivedCommit: strings.Repeat("d1", 20), DerivedTree: strings.Repeat("e1", 20),
	}
	local := clean
	local.Supersedes, local.PredecessorRequestID, local.PredecessorGenerationID = fid("10"), fid("70"), fid("16")
	conflict := local
	conflict.Outcome, conflict.DerivedCommit, conflict.DerivedTree = GenerationConflict, "", ""
	conflict.ConflictSHA256, conflict.ReportPinID = goldenB3, fid("b2")
	return []struct {
		name string
		typ  string
		p    payload
	}{
		{"root", TypePromotionAdmitted, root},
		{"successor", TypePromotionAdmitted, successor},
		{"restart", TypePromotionAdmitted, restart},
		{"clean", TypeGenerationDerived, clean},
		{"local", TypeGenerationDerived, local},
		{"conflict", TypeGenerationDerived, conflict},
		{"failed conflict", TypePromotionFailed, PromotionFailed{RequestID: fid("71"), Outcome: FailedConflict, GenerationID: fid("11")}},
		{"dependency failed", TypePromotionFailed, PromotionFailed{RequestID: fid("72"), Outcome: FailedDependencyFailed, FailedRequestID: fid("71")}},
		{"future outcome", TypePromotionFailed, PromotionFailed{RequestID: fid("71"), Outcome: "future_failure"}},
	}
}

func TestF2_06PayloadsRoundTrip(t *testing.T) {
	for _, tc := range f2_06Payloads() {
		t.Run(tc.name, func(t *testing.T) {
			values := []payload{tc.p}
			switch p := tc.p.(type) {
			case PromotionAdmitted:
				p.ScratchBase, p.BaselineTree = strings.Repeat("a", 64), strings.Repeat("b", 64)
				p.DeltaBaseTree, p.SealedTree = strings.Repeat("c", 64), strings.Repeat("d", 64)
				p.InputsRevision = 1<<63 - 1
				if p.ParentRequestID != (uuid.UUID{}) {
					p.Checkpoint = 1<<63 - 1
				}
				values = append(values, p)
			case GenerationDerived:
				p.PredecessorCommit, p.PredecessorTree = strings.Repeat("a", 64), strings.Repeat("b", 64)
				if p.Outcome == GenerationClean {
					p.DerivedCommit, p.DerivedTree = strings.Repeat("c", 64), strings.Repeat("d", 64)
				}
				p.InputsRevision, p.IntegrationRules = 1<<63-1, strings.Repeat("a", 62)+"/1"
				values = append(values, p)
				p.IntegrationRules = "a0-/123"
				values = append(values, p)
			case PromotionFailed:
				if p.Outcome == "future_failure" {
					p.GenerationID = fid("11")
					values = append(values, p)
					p.FailedRequestID = fid("70")
					values = append(values, p)
					p.GenerationID = uuid.UUID{}
					values = append(values, p)
					p.Outcome = strings.Repeat("a", 64)
					values = append(values, p)
				}
			}
			for _, want := range values {
				js := mustEncode(want)
				got, err := decodeF2_06(tc.typ, js)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("Decode(%s) = %+v, %v; want %+v", js, got, err, want)
				}
				if again, err := got.Encode(); err != nil || !bytes.Equal(again, js) {
					t.Fatalf("re-encoding = %s, %v; want %s", again, err, js)
				}
				extended := append([]byte(`{"later":{"a":[1,null],"b":"x"},`), js[1:]...)
				if got, err := decodeF2_06(tc.typ, extended); err != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("unknown member changed decoding: %+v, %v; want %+v", got, err, want)
				}
			}
		})
	}
}

func TestF2_06PayloadsRefuse(t *testing.T) {
	for _, tc := range f2_06Payloads() {
		t.Run(tc.name, func(t *testing.T) {
			js := mustEncode(tc.p)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(js, &fields); err != nil {
				t.Fatal(err)
			}
			refuse := func(name string, bad []byte, checkEncode bool) {
				t.Helper()
				t.Run(name, func(t *testing.T) {
					if _, err := decodeF2_06(tc.typ, bad); !errors.Is(err, ErrInvalid) {
						t.Errorf("Decode(%s) = %v; want ErrInvalid", bad, err)
					}
					if checkEncode {
						// Build the Go value without the contract decoder.
						// Digest has no UnmarshalText, so populate its array
						// directly, including zero. Skip only wire values
						// that the corresponding Go field cannot represent.
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
			optional := map[string]bool{}
			switch tc.typ {
			case TypePromotionAdmitted:
				optional = map[string]bool{"parent_request_id": true, "restart_from": true}
			case TypeGenerationDerived:
				optional = map[string]bool{"supersedes": true, "predecessor_request_id": true, "predecessor_generation_id": true, "derived_commit": true, "derived_tree": true, "conflict_sha256": true, "report_pin_id": true}
			case TypePromotionFailed:
				optional = map[string]bool{"generation_id": true, "failed_request_id": true}
			}
			// A present empty optional value is not absence, including
			// when the outcome would allow the field to be absent.
			for _, key := range slices.Sorted(maps.Keys(optional)) {
				if _, present := fields[key]; present {
					continue
				}
				mutate("null "+key, key, "null", false)
				mutate("empty "+key, key, `""`, false)
				mutate("non-string "+key, key, `1`, false)
				if strings.HasSuffix(key, "_id") || key == "restart_from" || key == "supersedes" {
					mutate("zero "+key, key, `"00000000-0000-0000-0000-000000000000"`, false)
				} else if strings.HasSuffix(key, "_sha256") {
					mutate("zero "+key, key, `"`+ZeroHash+`"`, false)
				}
			}
			for _, key := range slices.Sorted(maps.Keys(fields)) {
				value := fields[key]
				if !optional[key] {
					mutate("missing "+key, key, "", true)
				}
				mutate("null "+key, key, "null", false)
				refuse("repeated "+key, append([]byte(`{"`+key+`":`+string(value)+`,`), js[1:]...), false)
				if strings.HasSuffix(key, "_id") || key == "restart_from" || key == "supersedes" {
					mutate("non-v7 "+key, key, `"6f1c2b6e-4b1d-4c3a-9e2f-0a1b2c3d4e5f"`, true)
					mutate("uppercase "+key, key, `"0199C000-0000-7000-8000-000000000071"`, false)
					mutate("zero "+key, key, `"00000000-0000-0000-0000-000000000000"`, !optional[key])
					mutate("empty "+key, key, `""`, !optional[key])
					mutate("non-string "+key, key, `1`, false)
				}
				if strings.HasSuffix(key, "_sha256") {
					mutate("zero "+key, key, `"`+ZeroHash+`"`, !optional[key])
					mutate("uppercase "+key, key, `"`+strings.ToUpper(goldenB1.String())+`"`, false)
					mutate("empty "+key, key, `""`, false)
					mutate("non-string "+key, key, `1`, false)
				}
				if key == "scratch_base" || strings.HasSuffix(key, "_tree") || strings.HasSuffix(key, "_commit") {
					for _, value := range []string{`""`, `"` + strings.Repeat("A", 40) + `"`, `"` + strings.Repeat("a", 39) + `"`, `"` + strings.Repeat("a", 63) + `"`, `"g` + strings.Repeat("a", 39) + `"`, `1`} {
						mutate(key+"="+value, key, value, value != `""` || !optional[key])
					}
				}
				if key == "checkpoint" || key == "inputs_revision" {
					for _, value := range []string{`0`, `-1`, `1.5`, `1e1`, `9223372036854775808`, `"1"`} {
						mutate(key+"="+value, key, value, true)
					}
					refuse("signed "+key, bytes.Replace(js, []byte(`"`+key+`":`+string(value)), []byte(`"`+key+`":+1`), 1), false)
				}
			}
			refuse("not object", []byte(`[]`), false)
			refuse("malformed", []byte(`{"`), false)
			refuse("nested duplicate", append([]byte(`{"later":{"x":1,"x":2},`), js[1:]...), false)
			switch p := tc.p.(type) {
			case PromotionAdmitted:
				if p.ParentRequestID == (uuid.UUID{}) {
					mutate("checkpoint without parent", "checkpoint", `2`, true)
				} else {
					mutate("parent at checkpoint one", "checkpoint", `1`, true)
					mutate("restart with parent", "restart_from", `"`+fid("70").String()+`"`, true)
				}
			case GenerationDerived:
				mutate("unknown generation outcome", "outcome", `"future"`, true)
				for _, value := range []string{`""`, `"Rules/1"`, `"rules_name/1"`, `"1rules/1"`, `"rules/0"`, `"rules/01"`, `"rules/+1"`, `"rules/1/2"`, `"rules/1x"`, `"rules/"`, `"` + strings.Repeat("a", 63) + `/1"`, `1`} {
					mutate("integration_rules="+value, "integration_rules", value, true)
				}
				if p.PredecessorRequestID == (uuid.UUID{}) {
					mutate("predecessor request alone", "predecessor_request_id", `"`+fid("70").String()+`"`, true)
					mutate("predecessor generation alone", "predecessor_generation_id", `"`+fid("10").String()+`"`, true)
				}
				if p.Outcome == GenerationClean {
					mutate("clean without commit", "derived_commit", "", true)
					mutate("clean without tree", "derived_tree", "", true)
					mutate("clean with conflict digest", "conflict_sha256", `"`+goldenB1.String()+`"`, true)
					mutate("clean with report pin", "report_pin_id", `"`+fid("b2").String()+`"`, true)
				} else {
					mutate("conflict without digest", "conflict_sha256", "", true)
					mutate("conflict without report pin", "report_pin_id", "", true)
					mutate("conflict with commit", "derived_commit", `"`+strings.Repeat("a", 40)+`"`, true)
					mutate("conflict with tree", "derived_tree", `"`+strings.Repeat("b", 40)+`"`, true)
				}
			case PromotionFailed:
				for _, value := range []string{`""`, `"Conflict"`, `"dependency-failed"`, `"1st"`, `"has.dot"`, `"` + strings.Repeat("a", 65) + `"`, `1`} {
					mutate("outcome="+value, "outcome", value, true)
				}
				if p.Outcome == FailedConflict {
					mutate("failure without generation", "generation_id", "", true)
					mutate("conflict with failed request", "failed_request_id", `"`+fid("70").String()+`"`, true)
				} else if p.Outcome == FailedDependencyFailed {
					mutate("dependency without failed request", "failed_request_id", "", true)
					mutate("dependency with generation", "generation_id", `"`+fid("11").String()+`"`, true)
				}
			}
		})
	}
}
