package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

// The frozen log is copied byte-for-byte, not reconstructed by the producer
// under test. Body publication and private object publication use real stores.
func scopedGoldenFixture(t testing.TB, chapter string, prefix int) (*serviceFixture, []ledger.Commit) {
	t.Helper()
	dir := filepath.Join("../../../contract/ledger/testdata/f3", chapter)
	colony := uuid.MustParse("0199c000-0000-7000-8000-000000000007")
	if chapter == "11" {
		colony = uuid.MustParse("019a1100-0000-7000-8000-000000000001")
	}
	r, err := ledger.OpenReader(filepath.Join(dir, "log"), ledger.ReadOptions{Colony: colony, Through: ledger.ToEnd, AllTypes: true, Consume: ledger.Consumption{}})
	if err != nil {
		t.Fatal(err)
	}
	var commits []ledger.Commit
	for {
		c, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = r.Close()
			t.Fatal(err)
		}
		commits = append(commits, c)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if prefix < 0 {
		prefix = len(commits)
	}
	root := testRoot(t)
	for _, name := range []string{"log", "bodies"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(root, "colony.json"), []byte(fmt.Sprintf("{\"format\":1,\"colony_id\":\"%s\"}\n", colony)))
	store, _, err := ledgerstore.Open(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	f := &serviceFixture{root: root, store: store}
	t.Cleanup(func() {
		if f.service != nil {
			_ = f.service.Close()
		}
		if f.store != nil {
			_ = f.store.Close()
		}
	})
	objects := map[ledger.Digest][]byte{}
	for _, text := range []string{"# Ada\n", "", "general lesson\n", "opaque chunk\n", "opaque continuation", "retained lesson\n", "next step\n", "local lesson\n", "another chunk\n", "# Ada\nRevised instructions\n"} {
		objects[ledger.Digest(sha256.Sum256([]byte(text)))] = []byte(text)
	}
	if chapter == "11" {
		for _, kind := range []string{"objects", "bodies"} {
			entries, err := os.ReadDir(filepath.Join(dir, kind))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				data := readTestFile(t, filepath.Join(dir, kind, entry.Name()))
				digest := ledger.Digest(sha256.Sum256(data))
				if digest.String() != entry.Name() {
					t.Fatalf("fixture content address: %s", entry.Name())
				}
				if kind == "objects" {
					objects[digest] = data
				} else if got, size, err := store.PublishBody(context.Background(), bytes.NewReader(data)); err != nil || got != digest || size != int64(len(data)) {
					t.Fatalf("publish golden body: %s %d %v", got, size, err)
				}
			}
		}
	}
	directories := map[uuid.UUID]string{}
	// Publish the complete immutable fixture set, including objects needed by
	// the next committed group. Unreferenced objects confer no authority.
	for _, commit := range commits {
		for _, record := range commit.Records {
			if record.Type == ledger.TypeRuntimeAgentRegistered {
				p, err := ledger.DecodeRuntimeAgentRegistered(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				directories[record.AgentID] = p.Directory
			}
			var revision ledger.PrivateRevision
			switch record.Type {
			case ledger.TypePrivateRevision:
				revision, err = ledger.DecodePrivateRevision(record.Payload)
			case ledger.TypePrivateScopedRevision:
				var scoped ledger.PrivateScopedRevision
				scoped, err = ledger.DecodePrivateScopedRevision(record.Payload)
				revision = ledger.PrivateRevision(scoped)
			default:
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			contextID := ""
			if revision.Scope == "context" {
				contextID = record.ContextID.String()
			}
			path, err := newPrivateScope(root, directories[record.AgentID], contextID, &directoryDurability{})
			if err != nil {
				t.Fatal(err)
			}
			for _, change := range revision.Changes {
				if change.Mode == "clear" {
					continue
				}
				data, ok := objects[change.SHA256]
				if !ok || int64(len(data)) != change.Bytes {
					t.Fatalf("missing exact fixture object %s", change.SHA256)
				}
				if _, err := path.publishObject(data); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if prefix != 0 {
		log := readTestFile(t, filepath.Join(dir, "log", ledger.SegmentName(1)))
		lines := bytes.SplitAfter(log, []byte{'\n'})
		last := commits[prefix-1].Last
		writeTestFile(t, filepath.Join(root, "log", ledger.SegmentName(1)), bytes.Join(lines[:last], nil))
	}
	f.store, _, err = ledgerstore.Open(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	return f, commits
}

func scopedGoldenID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("019a1100-0000-7000-8000-%012x", n))
}

func scopedGoldenDrafts(commit ledger.Commit) []ledger.Draft {
	drafts := make([]ledger.Draft, len(commit.Records))
	for i, record := range commit.Records {
		drafts[i] = record.Draft
		drafts[i].Payload = bytes.Clone(record.Payload)
		drafts[i].BodyRefs = slices.Clone(record.BodyRefs)
	}
	return drafts
}

func TestScopedFrozenGoldenReplay(t *testing.T) {
	t.Parallel()
	for _, chapter := range []string{"10", "11"} {
		t.Run(chapter, func(t *testing.T) {
			t.Parallel()
			f, commits := scopedGoldenFixture(t, chapter, -1)
			before := f.store.Durable()
			var report Reconciliation
			var err error
			limits := DefaultLimits()
			if chapter == "11" {
				limits.MaxLiveTasks, limits.MaxLiveGrants, limits.MaxPendingLocalWork = 1, 1, 1
				limits.MaxDelegationDepth, limits.MaxGrantOperations, limits.MaxGrantResources = 0, 1, 1
				limits.MaxGrantRecipients, limits.MaxTaskAudience, limits.MaxTaskDependencies = 1, 1, 0
				limits.MaxBudgetScopes, limits.MaxTaskDescriptionBytes, limits.MaxAgentTurnSlots = 1, 1, 1
			}
			f.service, report, err = Open(context.Background(), f.store, f.root, limits)
			if err != nil || len(report.Issues) != 0 || len(report.PendingWork) != 0 || f.store.Durable() != before {
				t.Fatalf("frozen Open: %+v, %v; durable %d/%d", report, err, f.store.Durable(), before)
			}
			ctx := context.Background()
			general := Binding{AgentID: commits[0].Records[0].AgentID, ContextID: commits[0].Records[0].ContextID}
			work := Binding{AgentID: general.AgentID, ContextID: commits[1].Records[0].ContextID}
			type materializedSlot struct {
				ReadBinding
				slot string
			}
			completed := map[materializedSlot]bool{}
			for _, item := range report.Completed {
				binding := item.ReadBinding
				if binding.Scope == "agent" {
					binding.ContextID = uuid.UUID{}
				}
				key := materializedSlot{binding, item.Slot}
				if completed[key] {
					t.Fatalf("duplicate frozen completion: %+v", item)
				}
				completed[key] = true
			}
			bindings := []ReadBinding{{general, "agent"}, {general, "context"}, {work, "context"}}
			if chapter == "11" {
				for _, index := range []int{2, 3} {
					bindings = append(bindings, ReadBinding{Binding{general.AgentID, commits[index].Records[0].ContextID}, "context"})
				}
			}
			for _, binding := range bindings {
				snapshot, err := f.service.Snapshot(ctx, binding)
				slots := []string{"handoff", "memory"}
				if binding.Scope == "agent" {
					slots = []string{"instructions", "memory"}
					binding.ContextID = uuid.UUID{}
				}
				if err != nil || len(snapshot.Materializations) != len(slots) {
					t.Fatalf("frozen materialization snapshot: %+v %v", snapshot, err)
				}
				for _, slot := range slots {
					key := materializedSlot{binding, slot}
					if snapshot.Materializations[slot] != "current" || !completed[key] {
						t.Fatalf("frozen Markdown not completed and current: %+v, %+v", key, snapshot)
					}
					delete(completed, key)
				}
			}
			if len(completed) != 0 {
				t.Fatalf("unexpected frozen completions: %+v", completed)
			}
			if chapter == "10" {
				for _, row := range []struct {
					binding     Binding
					scope, slot string
					revision    uint64
					want        string
				}{
					{general, "agent", "instructions", 3, "# Ada\nRevised instructions\n"},
					{general, "agent", "memory", 3, "retained lesson\n"},
					{general, "context", "memory", 2, "general lesson\n"},
					{work, "context", "handoff", 6, "next step\n"},
					{work, "context", "memory", 6, "local lesson\n"},
					{work, "context", "memory", 1, ""},
				} {
					page, err := f.service.ReadPrivate(ctx, PrivateRead{ReadBinding: ReadBinding{Binding: row.binding, Scope: row.scope}, Slot: row.slot, Revision: row.revision, MaxBytes: 1024})
					if err != nil || !page.Present || string(page.Bytes) != row.want {
						t.Fatalf("F3-10 %s/%s at %d: %+v %v", row.scope, row.slot, row.revision, page, err)
					}
				}
				continuation, err := f.service.ReadPrivate(ctx, PrivateRead{ReadBinding: ReadBinding{Binding: work, Scope: "context"}, Slot: "continuation", Revision: 6, MaxBytes: 1024})
				if err != nil || continuation.Present {
					t.Fatalf("cleared historical continuation resurrected: %+v %v", continuation, err)
				}
				transcript, err := f.service.ReadPrivate(ctx, PrivateRead{ReadBinding: ReadBinding{Binding: work, Scope: "context"}, Slot: "transcript", Revision: 6, MaxBytes: 1024})
				if err != nil || len(transcript.Chunks) != 3 || transcript.NextCursor != 0 {
					t.Fatalf("F3-10 transcript chain: %+v %v", transcript, err)
				}
				for i, want := range []Chunk{{2, 15, []byte("opaque chunk\n")}, {3, 18, []byte("opaque chunk\n")}, {6, 30, []byte("another chunk\n")}} {
					got := transcript.Chunks[i]
					if got.Revision != want.Revision || got.Sequence != want.Sequence || !bytes.Equal(got.Bytes, want.Bytes) {
						t.Fatalf("F3-10 transcript occurrence %d: %+v want %+v", i, got, want)
					}
				}
				if f.store.Durable() != before {
					t.Fatal("F3-10 replay/read appended records")
				}
				return
			}
			for _, commit := range commits {
				for _, record := range commit.Records {
					if record.Type != ledger.TypePrivateScopedRevision {
						continue
					}
					revision, err := ledger.DecodePrivateScopedRevision(record.Payload)
					if err != nil {
						t.Fatal(err)
					}
					page, err := f.service.ReadPrivate(context.Background(), PrivateRead{ReadBinding: ReadBinding{Binding: Binding{record.AgentID, record.ContextID}, Scope: revision.Scope}, Revision: uint64(revision.Revision), Slot: "memory", MaxBytes: 1024})
					if err != nil || string(page.Bytes) != "work memory\n" {
						t.Fatalf("shared scoped history: %q, %v", page.Bytes, err)
					}
				}
			}
			for _, row := range []struct {
				id                                int
				state                             string
				version, generation, source, size int64
			}{{0x200, "completed", 8, 1, 40, 28}, {0x201, "cancelled", 4, 2, 110, 26}} {
				view, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: scopedGoldenID(row.id)})
				if err != nil || view.Task.State != row.state || view.Task.Version != row.version || view.Task.Assignment == nil || view.Task.Assignment.Generation != row.generation || view.DescriptionRef.SourceSequence != row.source || view.DescriptionRef.Bytes != row.size {
					t.Fatalf("frozen task projection: %+v %v", view, err)
				}
			}
			grant, err := f.service.GrantStatus(ctx, GrantLookup{GrantID: scopedGoldenID(0x302)})
			if err != nil || grant.Current || grant.Reason != "revoked" {
				t.Fatalf("frozen revocation lost: %+v %v", grant, err)
			}
			book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || book.Used != (Exposure{LocalUnits: 8}) || book.Held != (Exposure{}) || book.Limits.LocalUnits != 2 || f.store.Durable() != before {
				t.Fatalf("frozen usage/lowering or inert replay: %+v %v", book, err)
			}
			owner := writePrivate(t, f.service, work, 900, 2, Change{Slot: "memory", Mode: "replace", Bytes: []byte("owner follows scoped\n")})
			if owner.Revision != 3 {
				t.Fatalf("owner and scoped private heads diverged: %+v", owner)
			}
			reopenScoped(t, f, limits)
			for revision, want := range map[uint64]string{1: "", 2: "work memory\n", 3: "owner follows scoped\n"} {
				page := readPrivate(t, f.service, work, revision, "memory")
				if string(page.Bytes) != want {
					t.Fatalf("mixed-family history at %d: %q want %q", revision, page.Bytes, want)
				}
			}
		})
	}
}
