package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// These fixtures cover ticket 08's part of F2, frozen (D123), through
// its public payload encoders and decoders.
func decodeF2_08(typ string, js json.RawMessage) (payload, error) {
	switch typ {
	case TypeFinalizationIntended:
		return DecodeFinalizationIntended(js)
	case TypePromotionAccepted:
		return DecodePromotionAccepted(js)
	case TypePromotionNoChange:
		return DecodePromotionNoChange(js)
	}
	panic("not an F2-08 type: " + typ)
}

func f2_08Payloads() []struct {
	name string
	typ  string
	p    payload
} {
	intent := FinalizationIntended{
		IntentID: fid("ee"), RequestID: fid("98"), GenerationID: fid("ea"),
		PredecessorCommit: strings.Repeat("e6", 20), PredecessorTree: strings.Repeat("e7", 20),
		TargetCommit: strings.Repeat("e9", 20), TargetTree: strings.Repeat("e8", 20),
		FinalizationRules: "finalization-rules/1",
	}
	accepted := PromotionAccepted{
		IntentID: intent.IntentID, RequestID: intent.RequestID, GenerationID: intent.GenerationID,
		PredecessorCommit: intent.PredecessorCommit, PredecessorTree: intent.PredecessorTree,
		AcceptedCommit: intent.TargetCommit, AcceptedTree: intent.TargetTree, ChangedPaths: []string{"file.txt"},
	}
	noChange := PromotionNoChange{
		RequestID: fid("99"), GenerationID: fid("eb"),
		PredecessorCommit: intent.TargetCommit, PredecessorTree: intent.TargetTree,
	}
	witnessIntent, witnessAccepted, witnessNoChange := intent, accepted, noChange
	witnessIntent.PredecessorWitnessSeq, witnessAccepted.PredecessorWitnessSeq, witnessNoChange.PredecessorWitnessSeq = 9, 9, 9
	return []struct {
		name string
		typ  string
		p    payload
	}{
		{"intent", TypeFinalizationIntended, intent},
		{"intent with witness", TypeFinalizationIntended, witnessIntent},
		{"accepted", TypePromotionAccepted, accepted},
		{"accepted with witness", TypePromotionAccepted, witnessAccepted},
		{"no change", TypePromotionNoChange, noChange},
		{"no change with witness", TypePromotionNoChange, witnessNoChange},
	}
}

func TestF2_08PayloadsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range f2_08Payloads() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			values := []payload{tc.p}
			wide := reflect.New(reflect.TypeOf(tc.p)).Elem()
			wide.Set(reflect.ValueOf(tc.p))
			for i := range wide.NumField() {
				key, _, _ := strings.Cut(wide.Type().Field(i).Tag.Get("json"), ",")
				if strings.HasSuffix(key, "_commit") || strings.HasSuffix(key, "_tree") {
					wide.Field(i).SetString(strings.Repeat(wide.Field(i).String()[:2], 32))
				}
			}
			values = append(values, wide.Interface().(payload))
			for _, seq := range []int64{1, math.MaxInt64} {
				v := reflect.New(reflect.TypeOf(tc.p)).Elem()
				v.Set(reflect.ValueOf(tc.p))
				v.FieldByName("PredecessorWitnessSeq").SetInt(seq)
				values = append(values, v.Interface().(payload))
			}
			switch p := tc.p.(type) {
			case FinalizationIntended:
				p.FinalizationRules = strings.Repeat("a", 62) + "/1"
				values = append(values, p)
				p.FinalizationRules = "a0-/123"
				values = append(values, p)
			case PromotionAccepted:
				p.ChangedPaths = []string{"a\tb\nc", "nested/file", "é/日本語"}
				values = append(values, p)
			}
			for _, want := range values {
				js := mustEncode(want)
				got, err := decodeF2_08(tc.typ, js)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("Decode(%s) = %+v, %v; want %+v", js, got, err, want)
				}
				if again, err := got.Encode(); err != nil || !bytes.Equal(again, js) {
					t.Fatalf("re-encoding = %s, %v; want %s", again, err, js)
				}
				extended := append([]byte(`{"later":{"a":[1,null],"b":"x"},`), js[1:]...)
				got, err = decodeF2_08(tc.typ, extended)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("unknown member changed decoding: %+v, %v; want %+v", got, err, want)
				}
				if again, err := got.Encode(); err != nil || !bytes.Equal(again, js) {
					t.Errorf("unknown member changed re-encoding: %s, %v; want %s", again, err, js)
				}
			}
		})
	}
}

func TestF2_08PayloadsRefuse(t *testing.T) {
	t.Parallel()
	for _, tc := range f2_08Payloads() {
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
					if _, err := decodeF2_08(tc.typ, bad); !errors.Is(err, ErrInvalid) {
						t.Errorf("Decode(%s) = %v; want ErrInvalid", bad, err)
					}
					if !checkEncode {
						return
					}
					// Construct a Go value independently of the contract decoder.
					p := reflect.New(reflect.TypeOf(tc.p))
					if json.Unmarshal(bad, p.Interface()) != nil {
						return // The Go field cannot represent this wire value.
					}
					if out, err := p.Elem().Interface().(payload).Encode(); !errors.Is(err, ErrInvalid) || out != nil {
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
			for _, key := range slices.Sorted(maps.Keys(fields)) {
				value := fields[key]
				if key != "predecessor_witness_seq" {
					mutate("missing "+key, key, "", true)
				}
				mutate("null "+key, key, "null", false)
				refuse("repeated "+key, append([]byte(`{"`+key+`":`+string(value)+`,`), js[1:]...), false)
				switch {
				case strings.HasSuffix(key, "_id"):
					mutate("non-v7 "+key, key, `"6f1c2b6e-4b1d-4c3a-9e2f-0a1b2c3d4e5f"`, true)
					mutate("zero "+key, key, `"00000000-0000-0000-0000-000000000000"`, true)
					mutate("uppercase "+key, key, `"0199C000-0000-7000-8000-000000000098"`, false)
					mutate("non-string "+key, key, `1`, false)
				case strings.HasSuffix(key, "_commit"), strings.HasSuffix(key, "_tree"):
					for _, value := range []string{`""`, `"` + strings.Repeat("A", 40) + `"`, `"` + strings.Repeat("a", 39) + `"`, `"` + strings.Repeat("a", 63) + `"`, `"g` + strings.Repeat("a", 39) + `"`, `1`} {
						mutate(key+"="+value, key, value, true)
					}
					mutate("mixed lengths "+key, key, `"`+strings.Repeat("a", 64)+`"`, true)
				}
			}
			for _, value := range []string{`0`, `null`, `-1`, `1.5`, `1e1`, `9223372036854775808`, `"1"`, `true`, `[]`, `{}`} {
				mutate("witness="+value, "predecessor_witness_seq", value, value == `-1`)
			}
			refuse("not object", []byte(`[]`), false)
			refuse("malformed", []byte(`{"`), false)
			refuse("nested duplicate", append([]byte(`{"later":{"x":1,"x":2},`), js[1:]...), false)
			switch p := tc.p.(type) {
			case FinalizationIntended:
				mutate("same commit", "target_commit", `"`+p.PredecessorCommit+`"`, true)
				mutate("same tree", "target_tree", `"`+p.PredecessorTree+`"`, true)
				for _, value := range []string{`""`, `"Rules/1"`, `"rules_name/1"`, `"1rules/1"`, `"rules/0"`, `"rules/01"`, `"rules/+1"`, `"rules/1/2"`, `"rules/1x"`, `"rules/"`, `"` + strings.Repeat("a", 63) + `/1"`, `1`} {
					mutate("rules="+value, "finalization_rules", value, true)
				}
			case PromotionAccepted:
				mutate("same commit", "accepted_commit", `"`+p.PredecessorCommit+`"`, true)
				mutate("same tree", "accepted_tree", `"`+p.PredecessorTree+`"`, true)
				for _, value := range []string{`[]`, `null`, `{}`, `"path"`, `[1]`, `[null]`, `[""]`, `["/a"]`, `["a/"]`, `["a//b"]`, `["a/./b"]`, `["a/../b"]`, `["a\\b"]`, `["a\u0000b"]`, `["a","a"]`, `["b","a"]`, `["é","z"]`} {
					mutate("paths="+value, "changed_paths", value, true)
				}
				refuse("invalid UTF-8", bytes.Replace(js, fields["changed_paths"], []byte("[\"a\xffb\"]"), 1), false)
				for _, paths := range [][]string{nil, {}, {"a\xffb"}} {
					p.ChangedPaths = paths
					if out, err := p.Encode(); !errors.Is(err, ErrInvalid) || out != nil {
						t.Errorf("Encode paths %q = %s, %v; want nil, ErrInvalid", paths, out, err)
					}
				}
			}
		})
	}
}

// TestFinalizationIntendedActivationBinding covers ticket 09's optional
// binding, frozen (D124), without changing F2-08.
func TestFinalizationIntendedActivationBinding(t *testing.T) {
	t.Parallel()
	p := f2_08Payloads()[0].p.(FinalizationIntended)
	g := mustFinalization(t)
	absent := g.inputs[4].drafts[0].Payload
	if got := mustEncode(p); !bytes.Equal(got, absent) {
		t.Fatalf("absent binding = %s; want frozen payload %s", got, absent)
	}
	for _, width := range []int{20, 32} {
		p.PredecessorCommit, p.PredecessorTree = strings.Repeat("e6", width), strings.Repeat("e7", width)
		p.TargetCommit, p.TargetTree = strings.Repeat("e9", width), strings.Repeat("e8", width)
		base := mustEncode(p)
		p.ActivationRecordHash = goldenB1
		want := append(bytes.Clone(base[:len(base)-1]), []byte(`,"activation_record_hash":"`+goldenB1.String()+`"}`)...)
		js := mustEncode(p)
		got, err := DecodeFinalizationIntended(js)
		if err != nil || got != p || !bytes.Equal(js, want) || !bytes.Equal(mustEncode(got), want) {
			t.Fatalf("binding round-trip = %+v, %v, %s; want %+v, %s", got, err, js, p, want)
		}
		for _, value := range []string{`"` + strings.Repeat("0", 64) + `"`, `null`, `"abc"`, `"` + strings.Repeat("A", 64) + `"`, `1`} {
			bad := append(bytes.Clone(base[:len(base)-1]), []byte(`,"activation_record_hash":`+value+`}`)...)
			if _, err := DecodeFinalizationIntended(bad); !errors.Is(err, ErrInvalid) {
				t.Errorf("Decode(%s) = %v; want ErrInvalid", bad, err)
			}
		}
		p.ActivationRecordHash = Digest{}
	}
}
