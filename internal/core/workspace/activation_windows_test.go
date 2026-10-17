package workspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
)

func TestWindowsActivationSharing(t *testing.T) {
	t.Parallel()
	f := newActivationFixture(t)
	r := f.request()
	before := f.store.Durable()
	logPath := filepath.Join(f.store.LogDir(), "00000000000000000001.jsonl")
	logBefore := readTestFile(t, logPath)
	var release func()
	published := make(map[ledger.Digest]os.FileInfo)
	f.svc.deps.beforeStoreAppend = func(drafts []ledger.Draft) {
		if drafts[0].Type != ledger.TypePolicyActivationRequested {
			t.Fatalf("unexpected native boundary: %s", drafts[0].Type)
		}
		for digest := range f.artifacts {
			path := filepath.Join(f.colony, "bodies", digest.String()[:2], digest.String())
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			published[digest] = info
		}
		release = lockNextLedgerAppend(t, f.store.LogDir())
	}
	_, err := f.svc.RequestActivation(context.Background(), r)
	if release == nil {
		t.Fatal("request append boundary not reached")
	}
	release()
	if !errors.Is(err, appender.ErrUncertain) || !errors.Is(err, ErrPoisoned) {
		t.Fatalf("native append did not poison: %v", err)
	}
	if f.store.Durable() != before || !bytes.Equal(readTestFile(t, logPath), logBefore) {
		t.Fatal("locked append changed durable records or log bytes")
	}
	if _, err := f.svc.RequestActivation(context.Background(), r); !errors.Is(err, ErrPoisoned) {
		t.Fatalf("poisoned request retry: %v", err)
	}
	if _, err := f.svc.ActivationStatus(context.Background(), ledger.ActorOwner, r.RequestID); !errors.Is(err, ErrPoisoned) {
		t.Fatalf("half-applied slot observable: %v", err)
	}
	reopenActivation(t, f, f.clock.now)
	if f.store.Durable() != before {
		t.Fatal("native failed append became durable after recovery")
	}
	if _, err := f.svc.ActivationStatus(context.Background(), ledger.ActorOwner, r.RequestID); !errors.Is(err, ErrUnknownActivation) {
		t.Fatalf("native failed request survived: %v", err)
	}
	state := mustActivation(t, f.svc, r)
	if state.State != "pending" || state.Revision != before+1 || f.store.Durable() != before+2 {
		t.Fatalf("same-ID retry lost slot or identity: %+v", state)
	}
	for digest, old := range published {
		path := filepath.Join(f.colony, "bodies", digest.String()[:2], digest.String())
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(old, info) || !old.ModTime().Equal(info.ModTime()) {
			t.Fatalf("retry rewrote published body %s", digest)
		}
	}
	assertActivationRetained(t, f)
	t.Log("native LockFileEx refusal: uncertain/poisoned, absent after reopen, same-ID retry without body rewrite")
}
