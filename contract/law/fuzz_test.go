package law

import (
	"crypto/sha256"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func checkErrorFamily(t testing.TB, err error) {
	t.Helper()
	matches := 0
	for _, sentinel := range []error{ErrLimit, ErrMalformed, ErrUnsupported, ErrInvalid} {
		if errors.Is(err, sentinel) {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("error must wrap exactly one artifact sentinel: %T %v (%d matches)", err, err, matches)
	}
}

func FuzzLoad(f *testing.F) {
	for path := range goldenSHA256 {
		if strings.HasPrefix(path, "law/") {
			f.Add(fixture(f, path))
		}
	}
	for _, tc := range lawRejections(f) {
		f.Add(tc.data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var values [2]Law
		var errs [2]error
		for i, p := range profiles() {
			got, err := Load(data, p.limits)
			values[i], errs[i] = got, err
			if (err != nil) != reflect.DeepEqual(got, Law{}) {
				t.Fatalf("zero-result/error invariant: %#v, %v", got, err)
			}
			if err != nil {
				checkErrorFamily(t, err)
			} else if got.SHA256 != Digest(sha256.Sum256(data)) {
				t.Fatal("identity is not exact-byte SHA-256")
			}
			again, againErr := Load(data, p.limits)
			if !reflect.DeepEqual(got, again) || !reflect.DeepEqual(err, againErr) {
				t.Fatal("nondeterministic load")
			}
		}
		if errs[0] == nil && (errs[1] != nil || !reflect.DeepEqual(values[0], values[1])) {
			t.Fatal("compact success changed under standard")
		}
		if errs[1] == nil && len(data) <= 65536 && errs[0] != nil {
			t.Fatalf("bounded v1 artifact rejected by compact: %v", errs[0])
		}
	})
}

func FuzzLoadValidationPolicy(f *testing.F) {
	for path := range goldenSHA256 {
		if strings.HasPrefix(path, "validation-policy/") {
			f.Add(fixture(f, path))
		}
	}
	for _, tc := range policyRejections(f) {
		f.Add(tc.data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var values [2]ValidationPolicy
		var errs [2]error
		for i, p := range profiles() {
			got, err := LoadValidationPolicy(data, p.limits)
			values[i], errs[i] = got, err
			if (err != nil) != (got == (ValidationPolicy{})) {
				t.Fatalf("zero-result/error invariant: %+v, %v", got, err)
			}
			if err != nil {
				checkErrorFamily(t, err)
			} else if got.SHA256 != Digest(sha256.Sum256(data)) {
				t.Fatal("identity is not exact-byte SHA-256")
			}
			again, againErr := LoadValidationPolicy(data, p.limits)
			if got != again || !reflect.DeepEqual(err, againErr) {
				t.Fatal("nondeterministic policy load")
			}
		}
		if errs[0] == nil && (errs[1] != nil || values[0] != values[1]) {
			t.Fatal("compact success changed under standard")
		}
		if errs[1] == nil && len(data) <= 65536 && errs[0] != nil {
			t.Fatalf("bounded v1 policy rejected by compact: %v", errs[0])
		}
	})
}
