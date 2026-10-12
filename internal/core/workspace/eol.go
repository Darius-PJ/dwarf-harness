package workspace

import (
	"crypto/sha256"
	"hash"
)

// crlfHasher hashes crlf(blob) without retaining the blob. Hash.Write cannot
// fail; the Writer interface lets io.Copy stream the same bytes to both hashes.
type crlfHasher struct {
	h          hash.Hash
	previousCR bool
}

func newCRLFHasher() *crlfHasher { return &crlfHasher{h: sha256.New()} }

func (h *crlfHasher) Write(p []byte) (int, error) {
	var buf [32 << 10]byte
	n := 0
	for _, c := range p {
		if n > len(buf)-2 {
			_, _ = h.h.Write(buf[:n])
			n = 0
		}
		if c == '\n' && !h.previousCR {
			buf[n] = '\r'
			n++
		}
		buf[n] = c
		n++
		h.previousCR = c == '\r'
	}
	_, _ = h.h.Write(buf[:n])
	return len(p), nil
}

func (h *crlfHasher) Sum() [32]byte { var sum [32]byte; copy(sum[:], h.h.Sum(nil)); return sum }
