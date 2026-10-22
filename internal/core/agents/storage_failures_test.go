package agents

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// These directory faults are injected at the real filesystem boundary. They
// prove refusal and retry semantics, not native media or power-loss behavior.
func TestPrivateStorageDirectoryFailures(t *testing.T) {
	t.Parallel()
	for _, boundary := range []string{"publication", "materialization"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			f := newServiceFixture(t, serviceDeps{})
			b := registerPrivate(t, f.service, 10)
			before := f.store.Durable()
			fault := errors.New("injected directory synchronization failure")
			blocked := true
			arm := func() {
				// A new instance must flush existing directories. Forgetting this
				// instance's successes models that boundary without replacing I/O.
				f.service.dirs.synced = nil
				f.service.dirs.beforeSyncDir = func(path string) error {
					if blocked && path == filepath.Join(f.root, "agents") {
						return fault
					}
					return nil
				}
			}
			if boundary == "publication" {
				arm()
			} else {
				f.service.deps.afterCommit = func(Receipt) { arm() }
			}
			req := memoryWrite(b, 20, 1, "directory durable bytes")
			_, err := f.service.WritePrivate(context.Background(), req)
			if !errors.Is(err, fault) {
				t.Fatalf("directory fault swallowed: %v", err)
			}
			lookup := CommandLookup{Binding: b, CommandID: req.CommandID, SubmissionSHA256: crashDigest(t, req)}
			receipt, lookupErr := f.service.CommandStatus(context.Background(), lookup)
			if boundary == "publication" {
				if !errors.Is(lookupErr, ErrUnavailable) || f.store.Durable() != before {
					t.Fatalf("failed publication committed: %+v %v", receipt, lookupErr)
				}
				if _, err := f.service.WritePrivate(context.Background(), req); !errors.Is(err, fault) || f.store.Durable() != before {
					t.Fatalf("persistent directory failure remembered as success: %v", err)
				}
			} else {
				var material *MaterializationError
				if !errors.As(err, &material) || material.Revision != 2 || lookupErr != nil || receipt.Revision != 2 || f.store.Durable() != before+3 {
					t.Fatalf("committed flush failure lost receipt: %+v %v / %v", receipt, lookupErr, err)
				}
				snap, err := f.service.Snapshot(context.Background(), ReadBinding{b, "context"})
				if err != nil || snap.Materializations["memory"] != "stale" {
					t.Fatalf("failed materialization not stale: %+v %v", snap, err)
				}
				replay, err := f.service.WritePrivate(context.Background(), req)
				if err != nil || !reflect.DeepEqual(replay, receipt) || f.store.Durable() != before+3 {
					t.Fatalf("failure changed immutable receipt: %+v %v", replay, err)
				}
			}
			blocked = false
			if boundary == "publication" {
				applied, err := f.service.WritePrivate(context.Background(), req)
				if err != nil || applied.Revision != 2 || f.store.Durable() != before+3 {
					t.Fatalf("publication refusal consumed ID: %+v %v", applied, err)
				}
			} else {
				if err := f.service.Close(); err != nil {
					t.Fatal(err)
				}
				segment := filepath.Join(f.store.LogDir(), ledger.SegmentName(1))
				log := readTestFile(t, segment)
				var report Reconciliation
				f.service, report, err = Open(context.Background(), f.store, f.root, DefaultLimits())
				if err != nil || len(report.Completed) != 1 || len(report.Issues) != 0 || !bytes.Equal(readTestFile(t, segment), log) {
					t.Fatalf("failed directory completion recovery: %+v %v", report, err)
				}
			}
			if got := readPrivate(t, f.service, b, 2, "memory"); !bytes.Equal(got.Bytes, req.Changes[0].Bytes) {
				t.Fatal("directory fault retry changed private bytes")
			}
		})
	}
}

func TestPrivateCorruptPublicationIsUncommitted(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t, serviceDeps{})
	b := registerPrivate(t, f.service, 10)
	path, err := f.service.scopePath(submissionIDs{b.AgentID, privateID(11), b.ContextID}, "context")
	if err != nil {
		t.Fatal(err)
	}
	req := memoryWrite(b, 20, 1, "replacement")
	digest, _ := hashObject(req.Changes[0].Bytes)
	target := filepath.Join(path.objects(), digest.String())
	corrupt := []byte("bad content") // Same length: a size-only check is insufficient.
	writeTestFile(t, target, corrupt)
	unrelated := filepath.Join(path.objects(), ".private-other-call")
	writeTestFile(t, unrelated, []byte("preserve"))
	before := f.store.Durable()
	if _, err := f.service.WritePrivate(context.Background(), req); !errors.Is(err, ErrIntegrity) || f.store.Durable() != before {
		t.Fatalf("corrupt addressed object accepted: %v", err)
	}
	if !bytes.Equal(readTestFile(t, target), corrupt) || string(readTestFile(t, unrelated)) != "preserve" {
		t.Fatal("failed publication overwrote foreign object or removed another call's temp")
	}
	entries, err := os.ReadDir(path.objects())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if bytes.HasPrefix([]byte(entry.Name()), []byte(".private-")) && entry.Name() != filepath.Base(unrelated) {
			t.Fatal("failed publication leaked its temporary file")
		}
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	var report Reconciliation
	f.service, report, err = Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil || len(report.Issues) != 0 || len(report.Completed) != 0 || f.store.Durable() != before {
		t.Fatalf("unselected corrupt object affected recovery: %+v %v", report, err)
	}
	if _, err := f.service.CommandStatus(context.Background(), CommandLookup{Binding: b, CommandID: req.CommandID, SubmissionSHA256: crashDigest(t, req)}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("corrupt publication got a receipt: %v", err)
	}
	if len(readPrivate(t, f.service, b, 1, "memory").Bytes) != 0 {
		t.Fatal("uncommitted object was selected")
	}
}
