package credentials

import (
	"errors"
	"os"
	"strings"
	"sync"
)

var (
	ErrUnavailable = errors.New("credentials: source unavailable")
	ErrMissing     = errors.New("credentials: source missing")
	ErrInvalid     = errors.New("credentials: invalid source or value")
	ErrReleased    = errors.New("credentials: released")
	ErrNative      = errors.New("credentials: native source failure")
)

// Source is resolved only at bind. A bound Secret has no source reference.
type Source interface{ Bind() (*Secret, error) }

// Secret must not be copied. Concurrent WithBytes callbacks each borrow an owned
// temporary copy while preventing Release from completing. Callbacks must not
// retain it or call Release.
type Secret struct {
	mu    sync.RWMutex
	value []byte
}

func (*Secret) String() string               { return "<credential>" }
func (*Secret) GoString() string             { return "<credential>" }
func (*Secret) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (s *Secret) WithBytes(use func([]byte) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.value) == 0 {
		return ErrReleased
	}
	b := append([]byte(nil), s.value...)
	defer clear(b)
	return use(b)
}
func (s *Secret) Release() { s.mu.Lock(); defer s.mu.Unlock(); clear(s.value); s.value = nil }

func bindBytes(b []byte, limit int) (*Secret, error) {
	if len(b) == 0 || len(b) > limit {
		return nil, ErrInvalid
	}
	for _, v := range b {
		if v < 0x21 || v > 0x7e {
			return nil, ErrInvalid
		}
	}
	return &Secret{value: append([]byte(nil), b...)}, nil
}

type environmentSource struct {
	name   string
	lookup func(string) (string, bool)
}

// Environment selects one variable; it does not read it until Bind.
func Environment(name string) Source { return environmentSource{name: name, lookup: os.LookupEnv} }
func (s environmentSource) Bind() (*Secret, error) {
	if s.name == "" || strings.ContainsAny(s.name, "=\x00") {
		return nil, ErrInvalid
	}
	lookup := s.lookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	value, ok := lookup(s.name)
	if !ok {
		return nil, ErrMissing
	}
	if len(value) > 4096 {
		return nil, ErrInvalid
	}
	b := []byte(value)
	defer clear(b)
	return bindBytes(b, 4096)
}

type nativeSource struct{ target string }

// Native selects a generic Credential Manager target. It refuses explicitly on
// targets other than Windows/amd64, without invoking an alternate mechanism.
func Native(target string) Source { return nativeSource{target: target} }

// Generic blobs use the application's UTF-16LE convention, without a terminator.
// Header-safe ASCII is a strict subset of well-formed UTF-16; surrogates and
// every non-ASCII code point are rejected, never lossy-decoded.
func decodeBlob(blob []byte) ([]byte, error) {
	if len(blob) == 0 || len(blob) > 2560 || len(blob)%2 != 0 {
		return nil, ErrInvalid
	}
	b := make([]byte, len(blob)/2)
	for i := range b {
		v := uint16(blob[2*i]) | uint16(blob[2*i+1])<<8
		if v < 0x21 || v > 0x7e {
			clear(b)
			return nil, ErrInvalid
		}
		b[i] = byte(v)
	}
	return b, nil
}
