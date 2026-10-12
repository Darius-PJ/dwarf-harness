package law

import (
	"bytes"
	"testing"
)

// Measurements inform D78's freeze packet; there is no performance threshold.
func BenchmarkLoad(b *testing.B) {
	full := fixture(b, "law/v1/full.json")
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"full", full},
		{"standard-max", append(bytes.Clone(full), bytes.Repeat([]byte{' '}, 262144-len(full))...)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(tc.data)))
			for b.Loop() {
				if _, err := Load(tc.data, StandardV1()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
