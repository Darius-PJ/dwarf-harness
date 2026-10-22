package agents

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

const privateCrashExit = 73

func crashWrite() PrivateWrite {
	return PrivateWrite{Binding: Binding{privateID(10), privateID(12)}, CommandID: privateID(20), Scope: "context", ExpectedVersion: 1, Changes: []Change{
		{"memory", "replace", []byte("crash memory\x00\r\n")},
		{"transcript", "append", []byte("crash chunk\x00\r\n")},
	}}
}

func crashDigest(t *testing.T, r PrivateWrite) ledger.Digest {
	t.Helper()
	changes := make([]privateChange, len(r.Changes))
	for i, c := range r.Changes {
		changes[i] = privateChange{c.Slot, c.Mode, c.Bytes}
	}
	_, digest, err := encodePrivateWrite(submissionIDs{r.AgentID, privateID(11), r.ContextID}, r.Scope, r.ExpectedVersion, changes)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

// These are process-death boundaries, not a claim about power-loss survival.
// The torn row additionally injects a truncated final commit before death.
func TestPrivateInterruptedRecovery(t *testing.T) {
	for _, boundary := range []string{"before_commit", "torn_commit", "after_commit", "after_materialization"} {
		t.Run(boundary, func(t *testing.T) {
			f := newServiceFixture(t, serviceDeps{})
			b := registerPrivate(t, f.service, 10)
			before := f.store.Durable()
			segment := filepath.Join(f.store.LogDir(), ledger.SegmentName(1))
			prefix := readTestFile(t, segment)
			if err := f.service.Close(); err != nil {
				t.Fatal(err)
			}
			if err := f.store.Close(); err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe, "-test.run=^TestPrivateCrashChild$")
			cmd.Env = append(os.Environ(), "DWARF_PRIVATE_CRASH_ROOT="+f.root, "DWARF_PRIVATE_CRASH_BOUNDARY="+boundary)
			output, err := cmd.CombinedOutput() // Run positively waits for this child.
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != privateCrashExit {
				t.Fatalf("child did not die at %s: %v\n%s", boundary, err, output)
			}
			var recovery ledgerstore.Recovery
			f.store, recovery, err = ledgerstore.Open(f.root, ledger.Defined())
			if err != nil {
				t.Fatal(err)
			}
			if (recovery.TruncatedBytes > 0) != (boundary == "torn_commit") {
				t.Fatalf("wrong torn-tail recovery: %+v", recovery)
			}
			committed := boundary == "after_commit" || boundary == "after_materialization"
			wantThrough := before
			if committed {
				wantThrough += 3
			}
			if f.store.Durable() != wantThrough {
				t.Fatalf("wrong recovered commit boundary: %d, want %d", f.store.Durable(), wantThrough)
			}
			recoveredLog := readTestFile(t, segment)
			if !committed && !bytes.Equal(recoveredLog, prefix) {
				t.Fatal("uncommitted tail survived recovery")
			}
			var report Reconciliation
			f.service, report, err = Open(context.Background(), f.store, f.root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Issues) != 0 || f.store.Durable() != wantThrough || !bytes.Equal(readTestFile(t, segment), recoveredLog) {
				t.Fatalf("service recovery changed ledger or reported corruption: %+v", report)
			}
			wantCompleted := 0
			if boundary == "after_commit" {
				wantCompleted = 1
			}
			if len(report.Completed) != wantCompleted {
				t.Fatalf("wrong stale completion report: %+v", report)
			}
			r := crashWrite()
			digest := crashDigest(t, r)
			lookup := CommandLookup{Binding: b, CommandID: r.CommandID, SubmissionSHA256: digest}
			receipt, lookupErr := f.service.CommandStatus(context.Background(), lookup)
			path, err := f.service.scopePath(submissionIDs{b.AgentID, privateID(11), b.ContextID}, "context")
			if err != nil {
				t.Fatal(err)
			}
			objectDigest, _ := hashObject(r.Changes[0].Bytes)
			if !bytes.Equal(readTestFile(t, filepath.Join(path.objects(), objectDigest.String())), r.Changes[0].Bytes) {
				t.Fatal("published object not retained")
			}
			if committed {
				if lookupErr != nil || receipt.Revision != 2 || receipt.Sequence != wantThrough {
					t.Fatalf("lost committed receipt: %+v %v", receipt, lookupErr)
				}
			} else {
				if !errors.Is(lookupErr, ErrUnavailable) {
					t.Fatalf("uncommitted receipt selected: %+v %v", receipt, lookupErr)
				}
				snap, err := f.service.Snapshot(context.Background(), ReadBinding{b, "context"})
				empty, _ := hashObject(nil)
				if err != nil || snap.Revision != 1 || snap.Heads["memory"].SHA256 != empty {
					t.Fatalf("unreferenced object selected: %+v %v", snap, err)
				}
				if got := readPrivate(t, f.service, b, 1, "memory"); len(got.Bytes) != 0 {
					t.Fatal("uncommitted bytes became readable")
				}
				if got := readPrivate(t, f.service, b, 1, "transcript"); got.Present || len(got.Chunks) != 0 {
					t.Fatal("uncommitted transcript became visible")
				}
			}
			applied, err := f.service.WritePrivate(context.Background(), r)
			if err != nil || applied.Revision != 2 {
				t.Fatalf("same-ID completion/replay: %+v %v", applied, err)
			}
			if committed && !reflect.DeepEqual(applied, receipt) {
				t.Fatal("restart changed immutable receipt")
			}
			through := f.store.Durable()
			if through != before+3 {
				t.Fatalf("retry applied more than once: %d", through)
			}
			again, err := f.service.WritePrivate(context.Background(), r)
			if err != nil || !reflect.DeepEqual(again, applied) || f.store.Durable() != through {
				t.Fatalf("second replay changed effect: %+v %v", again, err)
			}
			if got := readPrivate(t, f.service, b, 2, "memory"); !bytes.Equal(got.Bytes, r.Changes[0].Bytes) {
				t.Fatal("committed memory changed")
			}
			page := readPrivate(t, f.service, b, 2, "transcript")
			if len(page.Chunks) != 1 || !bytes.Equal(page.Chunks[0].Bytes, r.Changes[1].Bytes) || page.NextCursor != 0 {
				t.Fatalf("transcript replay duplicated or lost occurrence: %+v", page)
			}
			file, _ := path.markdownPath("memory")
			if !bytes.Equal(readTestFile(t, file), r.Changes[0].Bytes) {
				t.Fatal("committed Markdown not completed")
			}
		})
	}
}

func TestPrivateCrashChild(t *testing.T) {
	root := os.Getenv("DWARF_PRIVATE_CRASH_ROOT")
	if root == "" {
		return // The parent alone supplies the subprocess protocol.
	}
	boundary := os.Getenv("DWARF_PRIVATE_CRASH_BOUNDARY")
	store, _, err := ledgerstore.Open(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	segment := filepath.Join(store.LogDir(), ledger.SegmentName(1))
	before := readTestFile(t, segment)
	armed := false // Open's own completion must not trigger a live-write crash.
	deps := serviceDeps{
		afterPublish: func(Binding, string) {
			if armed && boundary == "before_commit" {
				os.Exit(privateCrashExit)
			}
		},
		afterCommit: func(Receipt) {
			if boundary == "torn_commit" {
				full := readTestFile(t, segment)
				start := bytes.LastIndexByte(full[:len(full)-1], '\n') + 1
				cut := start + (len(full)-start)/2
				if start <= len(before) || cut <= start || cut >= len(full) || !bytes.Equal(full[:len(before)], before) {
					t.Fatal("cut is not inside the final commit's final record")
				}
				file, err := os.OpenFile(segment, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(int64(cut)); err != nil {
					t.Fatal(err)
				}
				if err := file.Sync(); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				os.Exit(privateCrashExit) // Nothing writes after the injected cut.
			}
			if boundary == "after_commit" {
				os.Exit(privateCrashExit)
			}
		},
		beforeProjection: func(Binding, string) {
			if armed && boundary == "after_materialization" {
				os.Exit(privateCrashExit)
			}
		},
	}
	s, _, err := openService(context.Background(), store, root, DefaultLimits(), deps)
	if err != nil {
		t.Fatal(err)
	}
	armed = true
	_, err = s.WritePrivate(context.Background(), crashWrite())
	t.Fatalf("crash boundary was not reached: %v", err)
}
