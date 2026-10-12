package law

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"testing"
)

func TestIdentity(t *testing.T) {
	t.Parallel()
	full := fixture(t, "law/v1/full.json")
	if bytes.ContainsRune(full, '\r') || !bytes.HasSuffix(full, []byte("\n")) || bytes.HasSuffix(full, []byte("\n\n")) {
		t.Fatal("full.json must contain no CR and end in exactly one LF")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, full); err != nil {
		t.Fatal(err)
	}
	compactFile := fixture(t, "law/v1/full-compact.json")
	if !bytes.Equal(compactFile, compact.Bytes()) {
		t.Fatal("compact golden is not precisely json.Compact(full.json)")
	}
	original, err := Load(bytes.Clone(full), StandardV1())
	if err != nil {
		t.Fatal(err)
	}
	copyValue, err := Load(bytes.Clone(full), StandardV1())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, copyValue) {
		t.Fatal("identical bytes produced unequal laws")
	}
	seen := make(map[Digest]bool)
	for _, tc := range []struct {
		name   string
		data   []byte
		digest string
	}{
		{"full", full, goldenSHA256["law/v1/full.json"]},
		{"compact", compactFile, goldenSHA256["law/v1/full-compact.json"]},
		{"reordered", fixture(t, "law/v1/full-reordered.json"), goldenSHA256["law/v1/full-reordered.json"]},
		{"crlf", bytes.ReplaceAll(full, []byte("\n"), []byte("\r\n")), crlfSHA256},
	} {
		got, err := Load(tc.data, StandardV1())
		if err != nil {
			t.Fatal(err)
		}
		if got.SHA256 != Digest(sha256.Sum256(tc.data)) || got.SHA256 != pinnedDigest(t, tc.digest) {
			t.Fatalf("%s: wrong exact-byte digest", tc.name)
		}
		if seen[got.SHA256] {
			t.Fatalf("%s: identity normalized", tc.name)
		}
		seen[got.SHA256] = true
		got.SHA256 = original.SHA256
		if !reflect.DeepEqual(got, original) {
			t.Fatalf("%s: spelling changed values", tc.name)
		}
	}
	// Isolate the final newline too: trimming it must change identity.
	noLF := full[:len(full)-1]
	trimmed, err := Load(noLF, StandardV1())
	if err != nil {
		t.Fatal(err)
	}
	if trimmed.SHA256 == original.SHA256 || trimmed.SHA256 != Digest(sha256.Sum256(noLF)) {
		t.Fatal("newline excluded from identity")
	}
	policy := fixture(t, "validation-policy/v1/policy.json")
	compact.Reset()
	if err := json.Compact(&compact, policy); err != nil {
		t.Fatal(err)
	}
	p, err := LoadValidationPolicy(policy, StandardV1())
	if err != nil {
		t.Fatal(err)
	}
	q, err := LoadValidationPolicy(compact.Bytes(), StandardV1())
	if err != nil {
		t.Fatal(err)
	}
	if p.SHA256 == q.SHA256 || q.SHA256 != Digest(sha256.Sum256(compact.Bytes())) {
		t.Fatal("policy identity normalized")
	}
	q.SHA256 = p.SHA256
	if q != p {
		t.Fatal("policy formatting changed values")
	}
}

func TestEscapedSpellings(t *testing.T) {
	full := fixture(t, "law/v1/full.json")
	for _, tc := range []struct{ name, old, replacement string }{
		{"value", `"id": "affordance"`, `"id": "\u0061ffordance"`},
		{"key", `"law_id": "golden.full"`, `"law\u005fid": "golden.full"`},
	} {
		data := replaceUnique(t, full, tc.old, tc.replacement)
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Load(data, CompactV1())
			if err != nil {
				t.Fatal(err)
			}
			if got.SHA256 != Digest(sha256.Sum256(data)) || got.SHA256 == pinnedDigest(t, goldenSHA256["law/v1/full.json"]) {
				t.Fatal("escape spelling normalized in identity")
			}
			got.SHA256 = Digest{}
			if !reflect.DeepEqual(got, expectedFull()) {
				t.Fatal("escape spelling changed the law")
			}
		})
	}
}

func TestLoadOwnsResult(t *testing.T) {
	t.Parallel()
	data := fixture(t, "law/v1/full.json")
	first, err := Load(data, StandardV1())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Load(data, StandardV1())
	if err != nil {
		t.Fatal(err)
	}
	clear(data)
	if first.LawID != "golden.full" || first.Kinds[0].ID != Affordance {
		t.Fatal("result aliases input")
	}
	first.Kinds[0].ID = Surprise
	first.Kinds[0].Sources[0].RuleID = RouterHeadroomV1
	first.Kinds[0].Sources[0].Coupling.EdgeTypes[0] = RegisteredResourceUse
	second.SHA256 = Digest{}
	if !reflect.DeepEqual(second, expectedFull()) {
		t.Fatal("results share mutable slices")
	}
}
