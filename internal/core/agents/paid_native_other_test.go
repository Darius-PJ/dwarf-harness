//go:build !windows || !amd64

package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/internal/core/credentials"
)

func TestNativeCredentialSource(t *testing.T) {
	t.Parallel()
	f := newLivePaidFixture(t)
	r := CredentialBinding{CommandID: f.id(), ScopeID: f.credential.ScopeID, ExpectedVersion: f.service.p.paidCredentialScopes[f.credential.ScopeID].version, Provider: "anthropic", CredentialVersion: f.id(), SourceKind: "windows_credential_manager", SourceLocator: "dwarf-harness-test/unsupported", RevokePreviousVersion: f.credential.CredentialVersion}
	before := f.store.Durable()
	if _, err := f.service.BindCredential(context.Background(), bindingOf(f.origin), r); !errors.Is(err, credentials.ErrUnavailable) || f.store.Durable() != before {
		t.Fatal("native source did not explicitly refuse on this target")
	}
}
