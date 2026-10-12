package workspace

import (
	"crypto/sha256"
	"testing"
)

func referenceCRLF(b []byte) []byte {
	var out []byte
	for i, c := range b {
		if c == '\n' && (i == 0 || b[i-1] != '\r') {
			out = append(out, '\r')
		}
		out = append(out, c)
	}
	return out
}

func TestCRLFStreaming(t *testing.T) {
	for _, tc := range []struct{ raw, converted string }{{"a\nb\n", "a\r\nb\r\n"}, {"a\r\nb\n", "a\r\nb\r\n"}, {"", ""}, {"\r\r\n", "\r\r\n"}, {"\x00\n\r\n\r", "\x00\r\n\r\n\r"}} {
		for split := range len(tc.raw) + 1 {
			h := newCRLFHasher()
			if _, err := h.Write([]byte(tc.raw[:split])); err != nil {
				t.Fatal(err)
			}
			if _, err := h.Write(nil); err != nil {
				t.Fatal(err)
			}
			if _, err := h.Write([]byte(tc.raw[split:])); err != nil {
				t.Fatal(err)
			}
			if h.Sum() != sha256.Sum256([]byte(tc.converted)) {
				t.Fatalf("wrong digest for %q split %d", tc.raw, split)
			}
		}
	}
}
