package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/credentials"
)

func TestPaidCredentialPinning(t *testing.T) {
	// Mutable environment is process state; all other live rows use the immutable
	// TestMain fixture and run in parallel after this test completes.
	t.Run("dispatch", paidLiveCredentialRows)
	ctx := context.Background()
	f := newLivePaidFixture(t)
	const name = "DWARF_PAID_ROTATION_SYNTHETIC"
	const first = "synthetic-credential-before-rotation"
	const second = "synthetic-credential-after-rotation"
	t.Setenv(name, first)
	binding := CredentialBinding{CommandID: f.id(), ScopeID: f.credential.ScopeID, ExpectedVersion: f.service.p.paidCredentialScopes[f.credential.ScopeID].version, Provider: "anthropic", CredentialVersion: f.id(), SourceKind: "environment", SourceLocator: name, RevokePreviousVersion: f.credential.CredentialVersion}
	original, err := f.service.BindCredential(ctx, bindingOf(f.origin), binding)
	if err != nil {
		t.Fatal("binding synthetic source failed")
	}
	f.credential = f.service.p.paidCredentials[binding.CredentialVersion]
	r := f.prepare(t)
	t.Setenv(name, second)
	if err = f.service.paid.secrets[binding.CredentialVersion].WithBytes(func(b []byte) error {
		if !bytes.Equal(b, []byte(first)) {
			t.Error("bound source was reread")
		}
		return nil
	}); err != nil {
		t.Fatal("bound credential became unavailable")
	}
	_, bound := f.admit(t, r)
	admission, _ := bound.Admission()
	if admission.Request.Credential.Version != binding.CredentialVersion {
		t.Fatal("admission changed prepared version")
	}
	before := f.store.Durable()
	retry, err := f.service.BindCredential(ctx, bindingOf(f.origin), binding)
	if err != nil || retry.Sequence != original.Sequence || f.store.Durable() != before {
		t.Fatal("same binding command reread or recommitted")
	}
	changed := binding
	changed.SourceLocator = paidTestEnvironment
	if _, err = f.service.BindCredential(ctx, bindingOf(f.origin), changed); !errors.Is(err, ErrCommandMismatch) {
		t.Fatal("changed source locator reused command")
	}

	reserved := f.prepare(t)
	rotation := binding
	rotation.CommandID = f.id()
	rotation.CredentialVersion = f.id()
	rotation.ExpectedVersion++
	rotation.RevokePreviousVersion = binding.CredentialVersion
	if _, err = f.service.BindCredential(ctx, bindingOf(f.origin), rotation); err != nil {
		t.Fatal("rotation failed")
	}
	before = f.store.Durable()
	a := scopedBound(t, f.service, bindingOf(f.origin))
	if _, _, err = a.AdmitPaid(ctx, reserved); !errors.Is(err, ErrDenied) || f.store.Durable() != before {
		t.Fatal("revoked prepared version admitted")
	}
	if _, err = f.service.ReconcilePaid(ctx, PaidReconciliation{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	f.credential = f.service.p.paidCredentials[rotation.CredentialVersion]
	reserved = f.prepare(t)
	if _, err = f.service.RevokeCredential(ctx, bindingOf(f.origin), CredentialRevocation{CommandID: f.id(), ScopeID: f.credential.ScopeID, ExpectedVersion: rotation.ExpectedVersion + 1, Provider: "anthropic", CredentialVersion: rotation.CredentialVersion}); err != nil {
		t.Fatal(err)
	}
	before = f.store.Durable()
	if _, _, err = a.AdmitPaid(ctx, reserved); !errors.Is(err, ErrDenied) || f.store.Durable() != before {
		t.Fatal("explicitly revoked version admitted")
	}

	// Reopen lists the authorization but never consults its now-missing source.
	fresh := binding
	fresh.CommandID = f.id()
	fresh.CredentialVersion = f.id()
	fresh.ExpectedVersion = f.service.p.paidCredentialScopes[fresh.ScopeID].version
	fresh.RevokePreviousVersion = ledger.CredentialRef{}.Version
	if _, err = f.service.BindCredential(ctx, bindingOf(f.origin), fresh); err != nil {
		t.Fatal(err)
	}
	if err = os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	if err = f.service.Close(); err != nil {
		t.Fatal(err)
	}
	var report Reconciliation
	f.service, report, err = openPaidService(ctx, f.store, f.root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, serviceDeps{paidTestAuthority: f.route.Profile.Authority, reviewClock: func() (int64, error) { t.Error("replay sampled clock"); return 0, ErrPaidClock }})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.UnboundCredentials) != 1 || report.UnboundCredentials[0].Version != fresh.CredentialVersion || report.UnboundCredentials[0].SourceLocator != name {
		t.Fatal("restart did not report unavailable authorized version")
	}
	f.credential = f.service.p.paidCredentials[fresh.CredentialVersion]
	if _, err = f.service.PrepareAnthropic(ctx, bindingOf(f.origin), f.input()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("restart rehydrated credential")
	}
	if _, err = f.service.BindCredential(ctx, bindingOf(f.origin), fresh); err != nil {
		t.Fatal("durable binding retry required a source")
	}
	bad := fresh
	bad.CommandID = f.id()
	bad.CredentialVersion = f.id()
	bad.ExpectedVersion++
	bad.RevokePreviousVersion = fresh.CredentialVersion
	if _, err = f.service.BindCredential(ctx, bindingOf(f.origin), bad); !errors.Is(err, credentials.ErrMissing) {
		t.Fatal("missing source did not refuse")
	}
	for _, value := range []string{first, second} {
		hash := sha256.Sum256([]byte(value))
		if err != nil && (strings.Contains(err.Error(), value) || strings.Contains(err.Error(), hex.EncodeToString(hash[:]))) {
			t.Fatal("credential escaped in error")
		}
		walkErr := filepath.WalkDir(f.root, func(path string, entry fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if entry.IsDir() || entry.Name() == "lock" {
				return nil
			} // The store lease contains no payload.
			data, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			if bytes.Contains(data, []byte(value)) || bytes.Contains(data, []byte(hex.EncodeToString(hash[:]))) {
				t.Error("credential value or digest persisted")
			}
			return nil
		})
		if walkErr != nil {
			t.Fatal(walkErr)
		}
	}
}
