package credentials

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestEnvironmentBindsOnceAndReleasesOwnedMemory(t *testing.T) {
	t.Parallel()
	value := "synthetic-first-value"
	calls := 0
	src := environmentSource{name: "local-source", lookup: func(string) (string, bool) { calls++; return value, true }}
	secret, e := src.Bind()
	if e != nil {
		t.Fatal(e)
	}
	owned := secret.value
	value = "synthetic-rotated-value"
	var borrowed []byte
	if e = secret.WithBytes(func(b []byte) error {
		if !bytes.Equal(b, []byte("synthetic-first-value")) {
			t.Error("bound value changed after source rotation")
		}
		borrowed = b
		b[0] = 'x'
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(borrowed, make([]byte, len(borrowed))) {
		t.Fatal("borrowed copy not overwritten")
	}
	if e = secret.WithBytes(func(b []byte) error {
		if !bytes.Equal(b, []byte("synthetic-first-value")) {
			t.Error("caller mutated pinned version")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal("source was read after binding")
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(format, secret), "synthetic-first-value") {
			t.Fatal("debug representation exposed credential")
		}
	}
	if b, e := json.Marshal(secret); e == nil || bytes.Contains(b, []byte("synthetic-first-value")) {
		t.Fatal("credential has serializable representation")
	}
	secret.Release()
	if !bytes.Equal(owned, make([]byte, len(owned))) {
		t.Fatal("released owned memory retained value")
	}
	if e = secret.WithBytes(func([]byte) error { t.Error("released callback ran"); return nil }); !errors.Is(e, ErrReleased) {
		t.Fatal("released version still available")
	}
	secret.Release()
}
func TestEnvironmentDefaultLookupIsPinned(t *testing.T) {
	// t.Setenv changes process state; this is deliberately the one sequential row.
	const name = "DWARF_TEST_SYNTHETIC_CREDENTIAL"
	t.Setenv(name, "synthetic-environment-first")
	secret, e := Environment(name).Bind()
	if e != nil {
		t.Fatal(e)
	}
	defer secret.Release()
	t.Setenv(name, "synthetic-environment-second")
	if e = secret.WithBytes(func(b []byte) error {
		if !bytes.Equal(b, []byte("synthetic-environment-first")) {
			t.Error("default environment source reread at use")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestCredentialSourcesRefuseUnsafeValues(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, value string
		present     bool
	}{{"missing", "", false}, {"empty", "", true}, {"space", "bad value", true}, {"newline", "bad\nvalue", true}, {"nul", "bad\x00value", true}, {"nonascii", "bad雪", true}, {"oversize", strings.Repeat("x", 4097), true}} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			src := environmentSource{name: "synthetic-source", lookup: func(string) (string, bool) { return row.value, row.present }}
			if s, e := src.Bind(); e == nil {
				if s != nil {
					s.Release()
				}
				t.Fatal("unsafe credential accepted")
			}
		})
	}
	t.Run("maximum", func(t *testing.T) {
		t.Parallel()
		src := environmentSource{name: "synthetic-source", lookup: func(string) (string, bool) { return strings.Repeat("x", 4096), true }}
		s, e := src.Bind()
		if e != nil {
			t.Fatal(e)
		}
		defer s.Release()
		if e = s.WithBytes(func(b []byte) error {
			if len(b) != 4096 {
				t.Fatal("credential truncated")
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	})
}
func TestReleaseDrainsActiveBorrow(t *testing.T) {
	t.Parallel()
	s, e := bindBytes([]byte("synthetic-drain-value"), 4096)
	if e != nil {
		t.Fatal(e)
	}
	entered, finish := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := s.WithBytes(func(b []byte) error {
			close(entered)
			<-finish
			if !bytes.Equal(b, []byte("synthetic-drain-value")) {
				t.Error("release erased active borrow")
			}
			return nil
		}); err != nil {
			t.Error(err)
		}
	}()
	<-entered
	go func() { defer wg.Done(); s.Release() }()
	close(finish)
	wg.Wait()
	if e = s.WithBytes(func([]byte) error { return nil }); !errors.Is(e, ErrReleased) {
		t.Fatal("release did not finish")
	}
}
func utf16Blob(s string) []byte {
	b := make([]byte, len(s)*2)
	for i := range len(s) {
		b[2*i] = s[i]
	}
	return b
}
func TestNativeBlobRefusesMalformedAndOversize(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name string
		blob []byte
	}{{"empty", nil}, {"odd", []byte{65}}, {"nul", []byte{65, 0, 0, 0}}, {"high_surrogate", []byte{0, 0xd8}}, {"low_surrogate", []byte{0, 0xdc}}, {"bad_pair", []byte{0, 0xd8, 65, 0}}, {"nonascii_pair", []byte{0, 0xd8, 0, 0xdc}}, {"oversize", utf16Blob(strings.Repeat("x", 1281))}, {"newline", utf16Blob("x\ny")}} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if b, e := decodeBlob(row.blob); e == nil {
				clear(b)
				t.Fatal("invalid generic blob accepted")
			}
		})
	}
	b, e := decodeBlob(utf16Blob(strings.Repeat("x", 1280)))
	if e != nil || len(b) != 1280 {
		t.Fatal("valid maximum generic blob refused")
	}
	clear(b)
}
