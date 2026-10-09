package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// Digest is a SHA-256 digest. Its text form is 64 lowercase hex digits.
// The zero Digest means absent; no present value may be zero.
type Digest [sha256.Size]byte

// String returns d as 64 lowercase hex digits.
func (d Digest) String() string { return hex.EncodeToString(d[:]) }

// MarshalText returns d as 64 lowercase hex digits.
func (d Digest) MarshalText() ([]byte, error) { return hex.AppendEncode(nil, d[:]), nil }

// ParseDigest parses s as exactly 64 lowercase hex digits that are not all
// zero. Its errors wrap ErrInvalid.
func ParseDigest(s string) (Digest, error) {
	d, err := parseDigest(s)
	if err != nil {
		return Digest{}, fmt.Errorf("%w: digest %w", ErrInvalid, err)
	}
	return d, nil
}

func parseDigest(s string) (Digest, error) {
	var d Digest
	if len(s) != 2*sha256.Size || !isLowerHex([]byte(s)) {
		return Digest{}, errors.New("is not 64 lowercase hex digits")
	}
	hex.Decode(d[:], []byte(s))
	if d == (Digest{}) {
		return Digest{}, errors.New("is zero")
	}
	return d, nil
}

// parseDigests parses raw, a JSON value, as a non-empty array of digest
// strings in strictly ascending order.
func parseDigests(raw json.RawMessage) ([]Digest, error) {
	var elems []json.RawMessage
	if raw[0] != '[' || json.Unmarshal(raw, &elems) != nil {
		return nil, errors.New("is not an array")
	}
	if len(elems) == 0 {
		return nil, errors.New("is empty")
	}
	ds := make([]Digest, len(elems))
	for i, el := range elems {
		s, ok := parseString(el)
		if !ok {
			return nil, fmt.Errorf("element %d is not a string", i)
		}
		d, err := parseDigest(s)
		if err != nil {
			return nil, fmt.Errorf("element %d %w", i, err)
		}
		ds[i] = d
	}
	if err := checkDigests(ds); err != nil {
		return nil, err
	}
	return ds, nil
}

// checkDigests reports the first digest of ds that is zero or not above the
// one before it, so that a valid ds is a strictly ascending set.
func checkDigests(ds []Digest) error {
	for i, d := range ds {
		switch {
		case d == (Digest{}):
			return fmt.Errorf("element %d is zero", i)
		case i > 0 && bytes.Compare(ds[i-1][:], d[:]) >= 0:
			return fmt.Errorf("element %d is not above the one before it", i)
		}
	}
	return nil
}
