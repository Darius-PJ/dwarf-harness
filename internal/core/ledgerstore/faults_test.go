package ledgerstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
)

// errFault is the error an injected fault returns.
var errFault = errors.New("injected fault")

// faultFiles is the real file system with hooks that a test sets to fail or
// block an operation; a nil hook passes the call through. A test changes the
// hooks only while no other goroutine uses the store, or before starting the
// goroutine that does.
type faultFiles struct {
	osFiles
	onCreate   func(path string) (segmentFile, error)
	onWriteAt  func(f segmentFile, b []byte, off int64) (int, error)
	onSync     func(f segmentFile) error
	onSyncDir  func(dir string) error
	onTempSync func(f bodyFile) error
	onRename   func(from, to string) error
	onRemove   func(path string) error
	onOpenBody func(path string) (io.ReadCloser, error)
}

func (ff *faultFiles) createSegment(path string) (segmentFile, error) {
	if ff.onCreate != nil {
		return ff.onCreate(path)
	}
	f, err := ff.osFiles.createSegment(path)
	if err != nil {
		return nil, err
	}
	return faultFile{f, ff}, nil
}

func (ff *faultFiles) openSegment(path string) (segmentFile, error) {
	f, err := ff.osFiles.openSegment(path)
	if err != nil {
		return nil, err
	}
	return faultFile{f, ff}, nil
}

func (ff *faultFiles) syncDir(dir string) error {
	if ff.onSyncDir != nil {
		return ff.onSyncDir(dir)
	}
	return ff.osFiles.syncDir(dir)
}

// faultFile is a real segment file subject to its faultFiles' hooks.
type faultFile struct {
	segmentFile
	ff *faultFiles
}

func (f faultFile) WriteAt(b []byte, off int64) (int, error) {
	if f.ff.onWriteAt != nil {
		return f.ff.onWriteAt(f.segmentFile, b, off)
	}
	return f.segmentFile.WriteAt(b, off)
}

func (f faultFile) Sync() error {
	if f.ff.onSync != nil {
		return f.ff.onSync(f.segmentFile)
	}
	return f.segmentFile.Sync()
}

func (ff *faultFiles) createTemp(dir string) (bodyFile, error) {
	f, err := ff.osFiles.createTemp(dir)
	if err != nil {
		return nil, err
	}
	return faultBodyFile{f, ff}, nil
}

func (ff *faultFiles) rename(from, to string) error {
	if ff.onRename != nil {
		return ff.onRename(from, to)
	}
	return ff.osFiles.rename(from, to)
}

func (ff *faultFiles) remove(path string) error {
	if ff.onRemove != nil {
		return ff.onRemove(path)
	}
	return ff.osFiles.remove(path)
}

func (ff *faultFiles) openBody(path string) (io.ReadCloser, error) {
	if ff.onOpenBody != nil {
		return ff.onOpenBody(path)
	}
	return ff.osFiles.openBody(path)
}

// faultBodyFile is a real temporary body file subject to its faultFiles'
// hooks.
type faultBodyFile struct {
	bodyFile
	ff *faultFiles
}

func (f faultBodyFile) Sync() error {
	if f.ff.onTempSync != nil {
		return f.ff.onTempSync(f.bodyFile)
	}
	return f.bodyFile.Sync()
}

type appendResult struct {
	c   appender.Committed
	err error
}

func TestDurableAdvancesOnlyAfterSync(t *testing.T) {
	ff := &faultFiles{}
	s := newColony(t, testDeps(ff))
	mustAppend(t, s, sample("acked"))

	entered := make(chan struct{})
	release := make(chan error, 1)
	ff.onSync = func(segmentFile) error {
		close(entered)
		return <-release
	}
	// If the test stops early, the blocked flush fails so that Close can
	// take the writer turn.
	t.Cleanup(func() {
		select {
		case release <- errFault:
		default:
		}
	})
	done := make(chan appendResult, 1)
	go func() {
		c, err := s.Append(context.Background(), []ledger.Draft{sample("pending")})
		done <- appendResult{c, err}
	}()
	<-entered

	if s.Durable() != 1 {
		t.Errorf("Durable while the flush is pending = %d, want 1", s.Durable())
	}
	// The pending commit is complete on disk, so a ToEnd reader sees it; a
	// reader bounded by Durable must not.
	if got, want := ranges(readCommits(t, s.LogDir(), s.Colony(), ledger.ToEnd)), []appender.Committed{{First: 1, Last: 1}, {First: 2, Last: 2}}; !slices.Equal(got, want) {
		t.Errorf("ToEnd reader while the flush is pending = %v, want %v", got, want)
	}
	if got, want := ranges(readCommits(t, s.LogDir(), s.Colony(), s.Durable())), []appender.Committed{{First: 1, Last: 1}}; !slices.Equal(got, want) {
		t.Errorf("reader through Durable while the flush is pending = %v, want %v", got, want)
	}

	release <- errFault
	if r := <-done; !errors.Is(r.err, appender.ErrUncertain) {
		t.Fatalf("Append with a failed flush = %+v, want ErrUncertain", r)
	}
	if s.Durable() != 1 {
		t.Errorf("Durable after the failed flush = %d, want 1", s.Durable())
	}
	ff.onSync = nil
	if err := s.Recover(); err != nil {
		t.Fatalf("Recover = %v", err)
	}
	if c := mustAppend(t, s, sample("after")); c.First != 2 {
		t.Errorf("Append after Recover = %+v, want First 2", c)
	}
}

func TestAppendFailurePoisonsUntilRecover(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target int64 // the segment target; 0 keeps the default
		arm    func(*faultFiles)
		want   error
	}{
		{"short write", 0, func(ff *faultFiles) {
			ff.onWriteAt = func(f segmentFile, b []byte, off int64) (int, error) { return f.WriteAt(b[:len(b)/2], off) }
		}, appender.ErrUncertain},
		{"write error", 0, func(ff *faultFiles) {
			ff.onWriteAt = func(segmentFile, []byte, int64) (int, error) { return 0, errFault }
		}, appender.ErrUncertain},
		{"flush error", 0, func(ff *faultFiles) {
			ff.onSync = func(segmentFile) error { return errFault }
		}, appender.ErrUncertain},
		{"log directory flush error while rotating", 1, func(ff *faultFiles) {
			ff.onSyncDir = func(string) error { return errFault }
		}, appender.ErrPoisoned},
		{"segment creation error while rotating", 1, func(ff *faultFiles) {
			ff.onCreate = func(string) (segmentFile, error) { return nil, errFault }
		}, appender.ErrPoisoned},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ff := &faultFiles{}
			d := testDeps(ff)
			if tc.target > 0 {
				d.segmentTarget = tc.target
			}
			s := newColony(t, d)
			acked := []appender.Committed{mustAppend(t, s, sample("1")), mustAppend(t, s, sample("2"), sample("3"))}
			drafts := []ledger.Draft{sample("4"), sample("5")}
			ackedFiles := snapshot(t, s.LogDir())

			tc.arm(ff)
			if _, err := s.Append(context.Background(), drafts); !errors.Is(err, tc.want) {
				t.Fatalf("failing Append = %v, want %v", err, tc.want)
			}
			*ff = faultFiles{}
			if s.Durable() != 3 {
				t.Errorf("Durable after the failure = %d, want 3", s.Durable())
			}
			before := snapshot(t, s.LogDir())
			if _, err := s.Append(context.Background(), drafts); !errors.Is(err, appender.ErrPoisoned) {
				t.Errorf("Append after the failure = %v, want ErrPoisoned", err)
			}
			assertSnapshot(t, s.LogDir(), before)
			// Bodies do not take the writer turn, so they keep working.
			if dg, _, err := s.PublishBody(context.Background(), strings.NewReader("while poisoned")); err != nil {
				t.Errorf("PublishBody while poisoned = %v", err)
			} else if data, err := s.ReadBody(context.Background(), dg); err != nil || string(data) != "while poisoned" {
				t.Errorf("ReadBody while poisoned = %q, %v", data, err)
			}

			if err := s.Recover(); err != nil {
				t.Fatalf("Recover = %v", err)
			}
			// Recover leaves the acknowledged bytes and, after a failed
			// rotation, at most an empty new segment.
			for name, data := range snapshot(t, s.LogDir()) {
				if want, ok := ackedFiles[name]; (ok && !bytes.Equal(data, want)) || (!ok && len(data) != 0) {
					t.Errorf("%s after Recover holds %d bytes, want only its %d acknowledged ones", name, len(data), len(want))
				}
			}
			c := mustAppend(t, s, drafts...)
			if c != (appender.Committed{First: 4, Last: 5}) {
				t.Errorf("Append after Recover = %+v, want 4-5", c)
			}
			acked = append(acked, c)

			s2, rec := reopen(t, s, d)
			if rec.TruncatedBytes != 0 {
				t.Errorf("Open after Recover truncated %+v, want nothing", rec)
			}
			if got := ranges(readCommits(t, s2.LogDir(), s2.Colony(), s2.Durable())); !slices.Equal(got, acked) {
				t.Errorf("commits after reopening = %v, want the acknowledged %v", got, acked)
			}
		})
	}
}

func TestCancelledAppendWritesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ff := &faultFiles{}
		s := newColony(t, testDeps(ff))
		mustAppend(t, s, sample("1"))
		before := snapshot(t, s.LogDir())

		// An ended ctx never takes the turn, even when it is free.
		ended, cancel := context.WithCancel(context.Background())
		cancel()
		for range 100 {
			if _, err := s.Append(ended, []ledger.Draft{sample("ended")}); !errors.Is(err, context.Canceled) {
				t.Fatalf("Append with an ended ctx = %v, want context.Canceled", err)
			}
		}
		assertSnapshot(t, s.LogDir(), before)

		// A holder keeps the turn, blocked in its flush, while another
		// Append waits for the turn and is cancelled.
		release := make(chan struct{})
		ff.onSync = func(f segmentFile) error {
			<-release
			return f.Sync()
		}
		held := make(chan appendResult, 1)
		go func() {
			c, err := s.Append(context.Background(), []ledger.Draft{sample("2")})
			held <- appendResult{c, err}
		}()
		synctest.Wait()
		ctx, cancel := context.WithCancel(context.Background())
		waited := make(chan error, 1)
		go func() {
			_, err := s.Append(ctx, []ledger.Draft{sample("cancelled")})
			waited <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-waited; !errors.Is(err, context.Canceled) {
			t.Errorf("Append cancelled while waiting = %v, want context.Canceled", err)
		}
		close(release)
		if r := <-held; r.err != nil || r.c != (appender.Committed{First: 2, Last: 2}) {
			t.Fatalf("holding Append = %+v, want 2-2", r)
		}
		ff.onSync = nil

		if c := mustAppend(t, s, sample("3")); c != (appender.Committed{First: 3, Last: 3}) {
			t.Errorf("next Append = %+v, want 3-3", c)
		}
		commits := readCommits(t, s.LogDir(), s.Colony(), s.Durable())
		if got, want := tags(t, commits), []string{"1", "2", "3"}; !slices.Equal(got, want) {
			t.Errorf("records %v, want %v", got, want)
		}
	})
}
