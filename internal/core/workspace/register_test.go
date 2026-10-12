package workspace

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func TestRegisterRecordsPinnedInputs(t *testing.T) {
	f := newUnregisteredService(t)
	f.p.LocalExcludes = HostFile{Present: true, Bytes: []byte("private-local\n")}
	f.p.GlobalExcludes = HostFile{Present: true, Bytes: []byte("private-global\n")}
	var err error
	f.w, err = f.svc.Register(context.Background(), f.p)
	if err != nil {
		t.Fatal(err)
	}
	commits := serviceCommits(t, f.store)
	if len(commits) != 1 || len(commits[0].Records) != 3 {
		t.Fatalf("registration commits: %#v", commits)
	}
	records := commits[0].Records
	for i, typ := range []string{ledger.TypeWorkspaceRegistered, ledger.TypeCaptureInputsPinned, ledger.TypeBodyPin} {
		if records[i].Type != typ || records[i].Actor != ledger.ActorOwner {
			t.Fatalf("record %d: %#v", i, records[i])
		}
	}
	if !reflect.DeepEqual(records[1].BodyRefs, records[2].BodyRefs) {
		t.Fatal("pin differs from inputs refs")
	}
	var bodies [][]byte
	for _, digest := range records[1].BodyRefs {
		body, err := f.store.ReadBody(context.Background(), digest)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
	}
	document, sum, err := encodeCaptureInputs(f.p, "capture-rules/1")
	if err != nil {
		t.Fatal(err)
	}
	wantBodies := map[string]bool{string(document): true, "private-local\n": true, "private-global\n": true}
	for _, file := range []HostFile{f.p.LocalAttributes, f.p.GlobalAttributes, f.p.SystemAttributes} {
		if file.Present {
			wantBodies[string(file.Bytes)] = true
		}
	}
	for _, body := range bodies {
		if !wantBodies[string(body)] {
			t.Fatalf("unexpected pinned body %q", body)
		}
		delete(wantBodies, string(body))
	}
	if len(wantBodies) != 0 {
		t.Fatal("approved bodies omitted")
	}
	pin, err := ledger.DecodeCaptureInputsPinned(records[1].Payload)
	if err != nil || pin.Revision != 1 || pin.CaptureInputsSHA256 != ledger.Digest(sum) {
		t.Fatalf("inputs record: %#v %v", pin, err)
	}
	if _, err := f.store.Collect(context.Background(), ledgerstore.CollectPolicy{Actor: ledger.ActorOwner, BeforeNS: math.MaxInt64}); err != nil {
		t.Fatal(err)
	}
	for _, digest := range records[1].BodyRefs {
		if _, err := f.store.ReadBody(context.Background(), digest); err != nil {
			t.Fatalf("current body collected: %v", err)
		}
	}
	marker, err := f.git.readBlob(context.Background(), f.colony, f.p.CommonDir, "refs/dwarf-harness/colony")
	if err != nil || string(marker) != "dwarf-harness colony "+f.store.Colony().String()+"\n" {
		t.Fatalf("marker: %q %v", marker, err)
	}
	f.reopen(t, serviceConfig())
	if got := f.svc.workspaces[f.w.ID].Workspace; got != f.w {
		t.Fatalf("replay changed workspace: %#v %#v", got, f.w)
	}
}

func TestRegisterRefusals(t *testing.T) {
	for _, reason := range []string{"not_git", "no_main", "main_checked_out", "other_colony", "same_repository", "stale_normalization"} {
		t.Run(reason, func(t *testing.T) {
			f := newUnregisteredService(t)
			p := f.p
			want := ErrStaleProposal
			switch reason {
			case "not_git":
				p.CommonDir = t.TempDir()
				want = ErrNotGitRepository
			case "no_main":
				gitCommand(t, f.env, f.root, nil, "update-ref", "-d", "refs/heads/main")
				want = ErrNoCanonical
			case "main_checked_out":
				gitCommand(t, f.env, f.root, nil, "checkout", "main")
				want = ErrCanonicalCheckedOut
			case "other_colony":
				if err := f.git.ensureMarker(context.Background(), f.colony, p, uuid.NewV7().String()); err != nil {
					t.Fatal(err)
				}
				want = ErrOtherColony
			case "same_repository":
				if _, err := f.svc.Register(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				linked := filepath.Join(t.TempDir(), "linked")
				gitCommand(t, f.env, f.root, nil, "worktree", "add", "--detach", linked, "main")
				var err error
				p, err = f.svc.git.inspect(context.Background(), linked)
				if err != nil {
					t.Fatal(err)
				}
				want = ErrExists
			case "stale_normalization":
				p.Normalization.AutoCRLF = "input"
			}
			before := f.store.Durable()
			if _, err := f.svc.Register(context.Background(), p); !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
			if f.store.Durable() != before {
				t.Fatal("refusal wrote a record")
			}
		})
	}
}

func TestRegisterResumesAfterMarker(t *testing.T) {
	f := newUnregisteredService(t)
	if err := f.git.ensureMarker(context.Background(), f.colony, f.p, f.store.Colony().String()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Register(context.Background(), f.p); err != nil {
		t.Fatal(err)
	}
	if f.store.Durable() != 3 {
		t.Fatalf("registration durable = %d", f.store.Durable())
	}
}

func TestUpdateInputsCompareAndSwap(t *testing.T) {
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	writeTestFile(t, filepath.Join(s.Dir, "later.tmp"), []byte("eligible under revision one\n"))
	sealed, err := f.capture(s, sealCapture)
	if err != nil {
		t.Fatal(err)
	}
	p := f.p
	p.LocalExcludes = HostFile{Present: true, Bytes: []byte("later.tmp\n")}
	before := f.store.Durable()
	if _, err := f.svc.UpdateInputs(context.Background(), f.w.ID, 0, p); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale revision: %v", err)
	}
	if f.store.Durable() != before {
		t.Fatal("stale revision wrote")
	}
	updated, err := f.svc.UpdateInputs(context.Background(), f.w.ID, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	commits := serviceCommits(t, f.store)
	records := commits[len(commits)-1].Records
	if len(records) != 3 {
		t.Fatalf("update has %d records", len(records))
	}
	for i, typ := range []string{ledger.TypeCaptureInputsPinned, ledger.TypeBodyPin, ledger.TypeBodyUnpin} {
		if records[i].Type != typ {
			t.Fatalf("update record %d = %s", i, records[i].Type)
		}
	}
	unpin, err := ledger.DecodeBodyUnpin(records[2].Payload)
	if err != nil || unpin.PinID != f.w.PinID {
		t.Fatalf("previous pin not released: %#v %v", unpin, err)
	}
	if updated.Revision != 2 {
		t.Fatalf("revision = %d", updated.Revision)
	}
	observed, err := f.capture(s, observeCapture)
	if err != nil || observed.Tree != s.AnchorTree || observed.InputsRevision != 2 {
		t.Fatalf("new revision not in effect: %#v %v", observed, err)
	}
	oldListing := string(gitCommand(t, f.env, f.root, nil, "ls-tree", "-r", "--name-only", sealed.Tree))
	if !strings.Contains(oldListing, "later.tmp") {
		t.Fatal("input update changed prior seal")
	}
	if _, err := f.store.Collect(context.Background(), ledgerstore.CollectPolicy{Actor: ledger.ActorOwner, BeforeNS: math.MaxInt64}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ReadBody(context.Background(), f.w.InputsSHA256); !errors.Is(err, ledgerstore.ErrBodyUnavailable) {
		t.Fatalf("superseded document stayed pinned: %v", err)
	}
	report := f.reopen(t, serviceConfig())
	if len(report.Pending) != 0 {
		t.Fatalf("collected history reported: %#v", report)
	}
	if f.svc.workspaces[f.w.ID].Revision != 2 {
		t.Fatal("reopen lost current revision")
	}
	observed, err = f.capture(s, observeCapture)
	if err != nil || observed.Tree != s.AnchorTree || observed.InputsRevision != 2 {
		t.Fatalf("reopened capture: %#v %v", observed, err)
	}
}

func TestPinnedGitVersionGatesCapture(t *testing.T) {
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	f.svc.git.version = "2.99.0"
	assertUnavailable(t, f.svc.RequestPromotion(context.Background(), s.ContextID, f.w.ID), "inputs_stale")
	_, err := f.svc.CloseScratch(context.Background(), ownerClosure(s))
	assertUnavailable(t, err, "inputs_stale")
	p, err := f.svc.git.inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.UpdateInputs(context.Background(), f.w.ID, 1, p); err != nil {
		t.Fatal(err)
	}
	if _, err := f.capture(s, observeCapture); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(s)); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterWorkspaceSuffixCollisions(t *testing.T) {
	for _, existingDirectory := range []bool{false, true} {
		t.Run(fmt.Sprintf("directory_%t", existingDirectory), func(t *testing.T) {
			f := newServiceFixture(t)
			other := newGitFixture(t, "sha1")
			p, err := f.svc.git.inspect(context.Background(), other.root)
			if err != nil {
				t.Fatal(err)
			}
			collision := uuid.NewV7()
			if existingDirectory {
				if err := os.MkdirAll(filepath.Join(f.colony, "workspaces", shortID(collision)), 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				copy(collision[12:], f.w.ID[12:])
			}
			fresh := uuid.NewV7()
			calls := 0
			f.svc.deps.newID = func() uuid.UUID {
				calls++
				switch calls {
				case 1:
					return collision
				case 2:
					return fresh
				default:
					return uuid.NewV7()
				}
			}
			w, err := f.svc.Register(context.Background(), p)
			if err != nil || w.ID != fresh {
				t.Fatalf("workspace collision not reminted: %#v %v", w, err)
			}
			third := newGitFixture(t, "sha1")
			p, err = f.svc.git.inspect(context.Background(), third.root)
			if err != nil {
				t.Fatal(err)
			}
			calls = 0
			f.svc.deps.newID = func() uuid.UUID { calls++; return collision }
			before := f.store.Durable()
			if _, err := f.svc.Register(context.Background(), p); !errors.Is(err, ErrInvalid) || calls != 3 {
				t.Fatalf("exhaustion: %v, %d mints", err, calls)
			}
			if f.store.Durable() != before {
				t.Fatal("exhausted workspace name wrote")
			}
		})
	}
}
