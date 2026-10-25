//go:build !windows || !amd64

package credentials

import (
	"errors"
	"testing"
)

func TestNativeCredentialSource(t *testing.T) {
	t.Parallel()
	if secret, err := Native("dwarf-harness-test/unavailable").Bind(); secret != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatal("native source silently substituted a mechanism")
	}
}
