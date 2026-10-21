package agents

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestPrivatePublishBeforeCommit(t *testing.T) {
	t.Parallel()
	var f *serviceFixture
	var observe atomic.Bool
	var before uint64
	f = newServiceFixture(t, serviceDeps{afterPublish: func(b Binding, scope string) {
		if !observe.Load() {
			return
		}
		if f.store.Durable() != before {
			t.Error("revision committed before publication")
		}
		path, err := f.service.scopePath(submissionIDs{b.AgentID, privateID(11), b.ContextID}, scope)
		if err != nil {
			t.Error(err)
			return
		}
		digest, _ := hashObject([]byte("published"))
		if _, err := readObject(path, Head{SHA256: digest, Bytes: 9}); err != nil {
			t.Errorf("published object unavailable before Submit: %v", err)
		}
	}})
	b := registerPrivate(t, f.service, 10)
	before = f.store.Durable()
	path, _ := f.service.scopePath(submissionIDs{b.AgentID, privateID(11), b.ContextID}, "context")
	digest, _ := hashObject([]byte("published"))
	object := filepath.Join(path.objects(), digest.String())
	if err := os.Mkdir(object, 0o700); err != nil {
		t.Fatal(err)
	}
	req := memoryWrite(b, 20, 1, "published")
	if _, err := f.service.WritePrivate(context.Background(), req); err == nil || f.store.Durable() != before {
		t.Fatalf("publication failure consumed command: %v", err)
	}
	entries, err := os.ReadDir(path.objects())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if bytes.HasPrefix([]byte(entry.Name()), []byte(".private-")) {
			t.Fatal("publication leaked temp")
		}
	}
	if err := os.Remove(object); err != nil {
		t.Fatal(err)
	}
	observe.Store(true)
	r, err := f.service.WritePrivate(context.Background(), req)
	if err != nil || r.Revision != 2 {
		t.Fatalf("reuse refused ID: %+v %v", r, err)
	}
}
func TestPrivateIntegrityBlocksAndReports(t *testing.T) {
	t.Parallel()
	for _, row := range []string{"object_missing", "object_altered", "foreign", "wrong_kind", "stale", "absent", "other_slot_history", "agent_fence"} {
		t.Run(row, func(t *testing.T) {
			t.Parallel()
			f := newServiceFixture(t, serviceDeps{})
			b := registerPrivate(t, f.service, 10)
			sibling := createPrivate(t, f.service, b, 20)
			writePrivate(t, f.service, b, 30, 1, Change{"memory", "replace", []byte("old")}, Change{"handoff", "replace", []byte("handoff-only")})
			writePrivate(t, f.service, b, 31, 2, Change{"memory", "replace", []byte("current")})
			scope := "context"
			if row == "agent_fence" {
				scope = "agent"
				if _, err := f.service.WritePrivate(context.Background(), PrivateWrite{Binding: b, CommandID: privateID(40), Scope: "agent", ExpectedVersion: 1, Changes: []Change{{"memory", "replace", []byte("current")}}}); err != nil {
					t.Fatal(err)
				}
			}
			p, err := f.service.scopePath(submissionIDs{b.AgentID, privateID(11), b.ContextID}, scope)
			if err != nil {
				t.Fatal(err)
			}
			file, _ := p.markdownPath("memory")
			digest, _ := hashObject([]byte("current"))
			obj := filepath.Join(p.objects(), digest.String())
			if err := f.service.Close(); err != nil {
				t.Fatal(err)
			}
			switch row {
			case "object_missing":
				if err := os.Remove(obj); err != nil {
					t.Fatal(err)
				}
			case "object_altered":
				writeTestFile(t, obj, []byte("corrupt"))
			case "wrong_kind":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(file, 0o700); err != nil {
					t.Fatal(err)
				}
			case "stale":
				writeTestFile(t, file, []byte("old"))
			case "absent":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "other_slot_history":
				writeTestFile(t, file, []byte("handoff-only"))
			default:
				writeTestFile(t, file, []byte("foreign"))
			}
			before := f.store.Durable()
			var report Reconciliation
			f.service, report, err = Open(context.Background(), f.store, f.root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			if f.store.Durable() != before {
				t.Fatal("integrity inspection appended")
			}
			if row == "stale" || row == "absent" {
				if len(report.Issues) != 0 || len(report.Completed) != 1 || string(readTestFile(t, file)) != "current" {
					t.Fatalf("stale not completed: %+v", report)
				}
				return
			}
			if len(report.Issues) == 0 {
				t.Fatal("corruption was not reported")
			}
			issue := report.Issues[0]
			if issue.ExpectedSHA256 != digest || issue.Slot != "memory" || issue.AgentID != b.AgentID {
				t.Fatalf("unbound integrity evidence: %+v", issue)
			}
			if _, err := f.service.ReadPrivate(context.Background(), PrivateRead{ReadBinding: ReadBinding{b, "context"}, Slot: "memory", Revision: 3, MaxBytes: 100}); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("blocked read: %v", err)
			}
			_, err = f.service.ReadPrivate(context.Background(), PrivateRead{ReadBinding: ReadBinding{sibling, "context"}, Slot: "memory", Revision: 1, MaxBytes: 100})
			if row == "agent_fence" {
				if !errors.Is(err, ErrIntegrity) {
					t.Fatalf("agent integrity did not fence sibling: %v", err)
				}
			} else if err != nil {
				t.Fatalf("context integrity fenced sibling: %v", err)
			}
			if row == "foreign" || row == "agent_fence" {
				if string(readTestFile(t, file)) != "foreign" {
					t.Fatal("foreign content overwritten")
				}
			}
			if _, err := f.service.WritePrivate(context.Background(), memoryWrite(b, 50, 3, "next")); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("blocked write: %v", err)
			}
		})
	}
}
func TestPrivateMaterializationFailureAndCancellation(t *testing.T) {
	t.Parallel()
	var fail atomic.Bool
	var cancel context.CancelFunc
	f := newServiceFixture(t, serviceDeps{beforeMaterialize: func(_ Binding, scope, slot string) error {
		if fail.Load() && scope == "context" && slot == "memory" {
			return os.ErrPermission
		}
		return nil
	}, afterCommit: func(r Receipt) {
		if cancel != nil {
			cancel()
		}
	}})
	b := registerPrivate(t, f.service, 10)
	fail.Store(true)
	req := memoryWrite(b, 20, 1, "committed")
	ctx, stop := context.WithCancel(context.Background())
	cancel = stop
	defer stop()
	_, err := f.service.WritePrivate(ctx, req)
	var material *MaterializationError
	if !errors.As(err, &material) || material.Revision != 2 {
		t.Fatalf("false ack after committed failure: %v", err)
	}
	if !errors.Is(err, ErrMaterialization) || !errors.Is(err, os.ErrPermission) || !errors.Is(material.Cause, os.ErrPermission) {
		t.Fatalf("committed failure lost original error classification: %v (cause %v)", err, material.Cause)
	}
	snap, err := f.service.Snapshot(context.Background(), ReadBinding{b, "context"})
	if err != nil || snap.Revision != 2 || snap.Materializations["memory"] != "stale" {
		t.Fatalf("lost committed stale state: %+v %v", snap, err)
	}
	before := f.store.Durable()
	receipt, err := f.service.WritePrivate(context.Background(), req)
	if err != nil || receipt.Revision != 2 || f.store.Durable() != before {
		t.Fatalf("committed failure replay: %+v %v", receipt, err)
	}
	fail.Store(false)
	writePrivate(t, f.service, b, 30, 2, Change{"handoff", "replace", []byte("next")})
	snap, err = f.service.Snapshot(context.Background(), ReadBinding{b, "context"})
	if err != nil || snap.Materializations["memory"] != "current" {
		t.Fatalf("successor failed to materialize its whole head: %+v %v", snap, err)
	}
	if p := readPrivate(t, f.service, b, 3, "memory"); string(p.Bytes) != "committed" {
		t.Fatal("committed bytes lost")
	}
}
func TestPrivateRebuildIsInert(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t, serviceDeps{})
	b := registerPrivate(t, f.service, 10)
	r := writePrivate(t, f.service, b, 20, 1, Change{"memory", "replace", []byte("kept")})
	before := f.store.Durable()
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(f.root, "agents", "Scout-10")
	for _, dir := range []string{root, filepath.Join(root, "contexts", b.ContextID.String())} {
		writeTestFile(t, filepath.Join(dir, "core", "state.json"), []byte("not JSON: do not decode"))
	}
	var err error
	f.service, _, err = openService(context.Background(), f.store, f.root, DefaultLimits(), serviceDeps{beforePublish: func(Binding, string) error { t.Error("Open dispatched publication"); return errors.New("unexpected") }, beforeSubmit: func(d []ledger.Draft) []ledger.Draft { t.Error("Open submitted"); return d }})
	if err != nil {
		t.Fatal(err)
	}
	if f.store.Durable() != before {
		t.Fatal("rebuild appended")
	}
	got, err := f.service.CommandStatus(context.Background(), CommandLookup{Binding: b, CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256})
	if err != nil || !reflect.DeepEqual(got, r) {
		t.Fatalf("rebuild lost receipt: %+v %v", got, err)
	}
	if p := readPrivate(t, f.service, b, 2, "memory"); string(p.Bytes) != "kept" {
		t.Fatal("rebuild lost head")
	}
	for _, dir := range []string{root, filepath.Join(root, "contexts", b.ContextID.String())} {
		if string(readTestFile(t, filepath.Join(dir, "core", "state.json"))) != "not JSON: do not decode" {
			t.Fatal("rebuild wrote reserved cache")
		}
	}
}
func TestPrivateReceiptBindingAndPublicPrivacy(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t, serviceDeps{})
	a := registerPrivate(t, f.service, 10)
	b := createPrivate(t, f.service, a, 20)
	secret := []byte("PRIVATE-ONLY-91d9e4\x00\r\n")
	r := writePrivate(t, f.service, a, 30, 1, Change{"memory", "replace", secret})
	if _, err := f.service.CommandStatus(context.Background(), CommandLookup{Binding: b, CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256}); !errors.Is(err, ErrNotBound) {
		t.Fatalf("receipt through another binding: %v", err)
	}
	bad := r.SubmissionSHA256
	bad[0] ^= 1
	if _, err := f.service.CommandStatus(context.Background(), CommandLookup{Binding: a, CommandID: r.CommandID, SubmissionSHA256: bad}); !errors.Is(err, ErrCommandMismatch) {
		t.Fatalf("receipt without digest: %v", err)
	}
	for _, dir := range []string{f.store.LogDir(), filepath.Join(f.root, "bodies")} {
		if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if bytes.Contains(data, secret) {
					t.Errorf("private bytes in public storage %s", path)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CommandStatus(context.Background(), CommandLookup{Binding: a, CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed receipt: %v", err)
	}
	if _, err := f.service.WritePrivate(context.Background(), memoryWrite(a, 40, 2, "closed")); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed write: %v", err)
	}
	if f.store.Durable() != r.Sequence {
		t.Fatal("Close changed shared store")
	}
}
func TestPrivateCloseDrainsOperations(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	fenced := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var hold atomic.Bool
	f := newServiceFixture(t, serviceDeps{afterCloseFence: func() { close(fenced) }, afterCommit: func(Receipt) {
		if hold.Load() {
			close(entered)
			<-release
		}
	}})
	b := registerPrivate(t, f.service, 10)
	hold.Store(true)
	done := make(chan error, 1)
	go func() {
		_, err := f.service.WritePrivate(context.Background(), memoryWrite(b, 20, 1, "drained"))
		done <- err
	}()
	<-entered
	closing := make(chan error, 1)
	go func() { closing <- f.service.Close() }()
	<-fenced
	if _, err := f.service.Snapshot(context.Background(), ReadBinding{b, "context"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("new call during close: %v", err)
	}
	select {
	case err := <-closing:
		t.Fatalf("Close returned with publisher holding colony handles: %v", err)
	default:
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-closing; err != nil {
		t.Fatal(err)
	}
	reopened, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatalf("Close closed shared store: %v", err)
	}
	defer reopened.Close()
	if p := readPrivate(t, reopened, b, 2, "memory"); string(p.Bytes) != "drained" {
		t.Fatal("Close abandoned committed write")
	}
}

func TestPrivateCancelledPublicationConsumesNoID(t *testing.T) {
	t.Parallel()
	var cancel context.CancelFunc
	f := newServiceFixture(t, serviceDeps{afterPublish: func(Binding, string) {
		if cancel != nil {
			cancel()
		}
	}})
	b := registerPrivate(t, f.service, 10)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	cancel = stop
	request := memoryWrite(b, 20, 1, "unreferenced")
	before := f.store.Durable()
	if _, err := f.service.WritePrivate(ctx, request); !errors.Is(err, context.Canceled) || f.store.Durable() != before {
		t.Fatalf("cancelled publication consumed command: %v", err)
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.service, _, err = Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if p := readPrivate(t, f.service, b, 1, "memory"); len(p.Bytes) != 0 {
		t.Fatal("unreferenced publication became selected on restart")
	}
	if _, err := f.service.WritePrivate(context.Background(), request); err != nil {
		t.Fatalf("cancelled ID could not be reused: %v", err)
	}
}

func TestPrivateIntegrityDoesNotInspectLinkedObjects(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t, serviceDeps{})
	b := registerPrivate(t, f.service, 10)
	writePrivate(t, f.service, b, 20, 1, Change{"memory", "replace", []byte("private")})
	path, err := f.service.scopePath(submissionIDs{b.AgentID, privateID(11), b.ContextID}, "context")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(testRoot(t), "external")
	if err := os.Rename(path.objects(), external); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, path.objects()); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	var report Reconciliation
	f.service, report, err = Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Issues) == 0 {
		t.Fatal("linked object directory was not fenced")
	}
	for _, issue := range report.Issues {
		if issue.ObservedSHA256 != (ledger.Digest{}) {
			t.Fatal("diagnostics opened bytes through a rejected ancestor")
		}
	}
}
