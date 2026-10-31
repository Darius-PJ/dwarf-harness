package agents

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"uuid"
)

// The host's startup binding reads this view instead of guessing a CAS
// version, so a wrong version, provider or active set breaks every launch.
func TestCredentialScopeStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newLivePaidFixture(t)
	owner := bindingOf(f.origin)
	status := func(scope uuid.UUID) CredentialScopeView {
		t.Helper()
		view, err := f.service.CredentialScopeStatus(ctx, CredentialScopeLookup{ScopeID: scope})
		if err != nil {
			t.Fatal(err)
		}
		if text := fmt.Sprintf("%+v", view); strings.Contains(text, paidTestEnvironment) || strings.Contains(text, "environment") {
			t.Fatal("scope view disclosed a credential source")
		}
		return view
	}
	existing := status(f.credential.ScopeID)
	if existing.Provider != "anthropic" || !slices.Equal(existing.ActiveVersions, []uuid.UUID{f.credential.CredentialVersion}) {
		t.Fatal("existing scope misreported its provider or active versions")
	}
	rotation := CredentialBinding{CommandID: f.id(), ScopeID: f.credential.ScopeID, ExpectedVersion: existing.Version, Provider: "anthropic", CredentialVersion: f.id(), SourceKind: "environment", SourceLocator: paidTestEnvironment, RevokePreviousVersion: f.credential.CredentialVersion}
	if _, err := f.service.BindCredential(ctx, owner, rotation); err != nil {
		t.Fatal("the reported version was not the one a bind accepts", err)
	}
	rotated := status(f.credential.ScopeID)
	if rotated.Version != existing.Version+1 || !slices.Equal(rotated.ActiveVersions, []uuid.UUID{rotation.CredentialVersion}) {
		t.Fatal("rotation did not advance the version or replace the active set")
	}
	stale := rotation
	stale.CommandID, stale.CredentialVersion, stale.RevokePreviousVersion = f.id(), f.id(), uuid.UUID{}
	before := f.store.Durable()
	if _, err := f.service.BindCredential(ctx, owner, stale); !errors.Is(err, ErrRevision) || f.store.Durable() != before {
		t.Fatal("a stale scope version bound a credential")
	}

	fresh := f.id()
	genesis := status(fresh)
	if genesis.Version != 0 || genesis.Provider != "" || len(genesis.ActiveVersions) != 0 {
		t.Fatal("a scope without history reported state")
	}
	first := CredentialBinding{CommandID: f.id(), ScopeID: fresh, ExpectedVersion: genesis.Version, Provider: "openai", CredentialVersion: f.id(), SourceKind: "environment", SourceLocator: paidTestEnvironment}
	if _, err := f.service.BindCredential(ctx, owner, first); err != nil {
		t.Fatal("the genesis version was not the one a bind accepts", err)
	}
	second := first
	second.CommandID, second.CredentialVersion, second.ExpectedVersion = f.id(), f.id(), status(fresh).Version
	if _, err := f.service.BindCredential(ctx, owner, second); err != nil {
		t.Fatal(err)
	}
	both := []uuid.UUID{first.CredentialVersion, second.CredentialVersion}
	slices.SortFunc(both, func(a, b uuid.UUID) int { return a.Compare(b) })
	if view := status(fresh); view.Provider != "openai" || view.Version != 2 || !slices.Equal(view.ActiveVersions, both) {
		t.Fatal("a launch without revocation did not keep both versions active")
	}
	if !slices.Equal(status(f.credential.ScopeID).ActiveVersions, []uuid.UUID{rotation.CredentialVersion}) {
		t.Fatal("another scope's bindings leaked into the view")
	}
}
