package law

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func boundFields(l *LoadLimits) []struct {
	name  Bound
	value *int
} {
	return []struct {
		name  Bound
		value *int
	}{
		{"bytes", &l.Bytes}, {"depth", &l.Depth}, {"members", &l.Members}, {"elements", &l.Elements}, {"string_bytes", &l.StringBytes}, {"tokens", &l.Tokens},
	}
}
func zeros(n int) string {
	if n == 0 {
		return ""
	}
	return "0" + strings.Repeat(",0", n-1)
}
func tokenInput(tokens, maxElements int) []byte {
	// 5 outer tokens, then 2 delimiters plus n scalars per inner array.
	remaining := tokens - 5
	var arrays []string
	for remaining > 0 {
		n := min(maxElements-1, remaining-2)
		arrays = append(arrays, "["+zeros(n)+"]")
		remaining -= n + 2
	}
	return []byte(`{"x":[` + strings.Join(arrays, ",") + `]} `)
}

func checkLimit(t testing.TB, err error, bound Bound, max int) {
	t.Helper()
	var e *LimitError
	if !errors.Is(err, ErrLimit) || !errors.As(err, &e) || e.Bound != bound || e.Max != max {
		t.Fatalf("got %T %v, want limit %s=%d", err, err, bound, max)
	}
}

func TestLoadLimitsBounds(t *testing.T) {
	full := fixture(t, "law/v1/full.json")
	for _, p := range profiles() {
		for _, tc := range []struct {
			bound    Bound
			max      int
			generate func(int) []byte
		}{
			{"bytes", p.limits.Bytes, func(n int) []byte { return append(bytes.Clone(full), bytes.Repeat([]byte{' '}, n-len(full))...) }},
			{"depth", p.limits.Depth, func(n int) []byte { return []byte(`{"x":` + strings.Repeat("[", n-1) + strings.Repeat("]", n-1) + `}`) }},
			{"members", p.limits.Members, func(n int) []byte {
				var b strings.Builder
				b.WriteByte('{')
				for i := range n {
					if i > 0 {
						b.WriteByte(',')
					}
					fmt.Fprintf(&b, `"k%04d":0`, i)
				}
				b.WriteByte('}')
				return []byte(b.String())
			}},
			{"elements", p.limits.Elements, func(n int) []byte { return []byte(`{"x":[` + zeros(n) + `]}`) }},
			{"string_bytes", p.limits.StringBytes, func(n int) []byte { return []byte(`{"x":"` + strings.Repeat("a", n) + `"}`) }},
			{"tokens", p.limits.Tokens, func(n int) []byte { return tokenInput(n, p.limits.Elements) }},
		} {
			for _, delta := range []int{0, 1} {
				data := tc.generate(tc.max + delta)
				t.Run(p.name+"/"+string(tc.bound)+fmt.Sprint(delta), func(t *testing.T) {
					t.Parallel()
					_, err := Load(data, p.limits)
					if delta == 1 {
						checkLimit(t, err, tc.bound, tc.max)
					} else {
						var e *LimitError
						if errors.As(err, &e) || errors.Is(err, ErrMalformed) {
							t.Fatalf("constructed boundary failed phase 1: %v", err)
						}
						if tc.bound == "bytes" && err != nil {
							t.Fatal(err)
						}
					}
					// Both loaders apply the same budgets, even to an unsupported artifact.
					_, err = LoadValidationPolicy(data, p.limits)
					if delta == 1 {
						checkLimit(t, err, tc.bound, tc.max)
					} else {
						var e *LimitError
						if errors.As(err, &e) || errors.Is(err, ErrMalformed) {
							t.Fatalf("policy phase 1: %v", err)
						}
					}
				})
			}
		}
		for _, tc := range []struct {
			name, data string
			over       bool
		}{
			{"key-at", `{"` + strings.Repeat("a", p.limits.StringBytes) + `":0}`, false},
			{"key-over", `{"` + strings.Repeat("a", p.limits.StringBytes+1) + `":0}`, true},
			{"utf8-at", `{"x":"` + strings.Repeat("é", p.limits.StringBytes/2) + `"}`, false},
			{"utf8-over", `{"x":"` + strings.Repeat("é", p.limits.StringBytes/2+1) + `"}`, true},
			{"escapes-at", `{"x":"` + strings.Repeat(`\u00e9`, p.limits.StringBytes/2) + `"}`, false},
			{"escapes-over", `{"x":"` + strings.Repeat(`\u00e9`, p.limits.StringBytes/2+1) + `"}`, true},
		} {
			t.Run(p.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				_, err := Load([]byte(tc.data), p.limits)
				if tc.over {
					checkLimit(t, err, "string_bytes", p.limits.StringBytes)
				} else {
					var e *LimitError
					if errors.As(err, &e) || errors.Is(err, ErrMalformed) {
						t.Fatalf("decoded string count: %v", err)
					}
				}
			})
		}
		t.Run(p.name+"/bytes-before-utf8", func(t *testing.T) {
			t.Parallel()
			_, err := Load(bytes.Repeat([]byte{0xff}, p.limits.Bytes+1), p.limits)
			checkLimit(t, err, "bytes", p.limits.Bytes)
		})
	}
}

func TestLoadLimitsTightened(t *testing.T) {
	full := fixture(t, "law/v1/full.json")
	tight := LoadLimits{len(full), 7, 8, 6, 35, 681}
	if _, err := Load(full, tight); err != nil {
		t.Fatal(err)
	}
	for i := range boundFields(&tight) {
		limits := tight
		fields := boundFields(&limits)
		*fields[i].value--
		name, max := fields[i].name, *fields[i].value
		t.Run(string(name), func(t *testing.T) {
			t.Parallel()
			_, err := Load(bytes.Clone(full), limits)
			checkLimit(t, err, name, max)
		})
	}
}

func TestLoadLimitsRefused(t *testing.T) {
	full := fixture(t, "law/v1/full.json")
	policy := fixture(t, "validation-policy/v1/policy.json")
	cases := []struct {
		name   string
		limits LoadLimits
	}{{"zero", LoadLimits{}}}
	standard := StandardV1()
	for i, f := range boundFields(&standard) {
		for _, n := range []int{0, -1, *f.value + 1} {
			limits := StandardV1()
			*boundFields(&limits)[i].value = n
			cases = append(cases, struct {
				name   string
				limits LoadLimits
			}{fmt.Sprintf("%s-%d", f.name, n), limits})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, data := range [][]byte{full, {0xff}} {
				got, err := Load(data, tc.limits)
				if !errors.Is(err, ErrInvalidLimits) || !reflect.DeepEqual(got, Law{}) {
					t.Fatalf("law invalid limits: %#v, %v", got, err)
				}
			}
			for _, data := range [][]byte{policy, {0xff}} {
				got, err := LoadValidationPolicy(data, tc.limits)
				if !errors.Is(err, ErrInvalidLimits) || got != (ValidationPolicy{}) {
					t.Fatalf("policy invalid limits: %+v, %v", got, err)
				}
			}
		})
	}
	for _, limits := range []LoadLimits{CompactV1(), StandardV1(), {len(full), 7, 8, 6, 35, 681}} {
		if _, err := Load(full, limits); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadValidationPolicy(policy, limits); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPhaseOnePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		data    []byte
		limits  LoadLimits
		bound   Bound
		problem Problem
	}{
		{"members-before-duplicate", []byte(`{"a":0,"a":1}`), LoadLimits{100, 4, 1, 4, 10, 20}, "members", ""},
		{"tokens-before-depth", []byte(`[[[]]]`), LoadLimits{100, 1, 4, 4, 10, 1}, "tokens", ""},
		{"depth-before-elements", []byte(`[0,[]]`), LoadLimits{100, 1, 4, 1, 10, 20}, "depth", ""},
		{"elements-before-string", []byte(`[0,"xx"]`), LoadLimits{100, 4, 4, 1, 1, 20}, "elements", ""},
		{"utf8-before-syntax", []byte{'x', 0xff}, CompactV1(), "", "invalid_utf8"},
		{"bom-before-utf8", []byte{0xef, 0xbb, 0xbf, 0xff}, CompactV1(), "", "bom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Load(tc.data, tc.limits)
			if tc.problem != "" {
				checkRejection(t, err, rejection{problem: tc.problem})
				return
			}
			for _, f := range boundFields(&tc.limits) {
				if f.name == tc.bound {
					checkLimit(t, err, tc.bound, *f.value)
					return
				}
			}
		})
	}
}
