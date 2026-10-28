package agents

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestScopedReadRevocationAndArtifacts(t *testing.T) {
	t.Parallel()
	t.Run("artifacts", testScopedArtifactReads)
	t.Run("pinned_retry_units_physical_buffer_and_revocation", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		capacity := scopedCapacity()
		capacity.LocalUnits, capacity.LocalSlots = 1, 2
		f, binding, agent, grant := scopedPrivateFixture(t, serviceDeps{}, capacity)
		writePrivate(t, f.service, binding, 300, 1, Change{Slot: "memory", Mode: "replace", Bytes: []byte("old")})
		request := ScopedPrivateRead{CommandID: privateID(301), GrantID: grant.GrantID, Scope: "context", Slot: "memory", Revision: 2, MaxBytes: 64}
		first, err := agent.ReadPrivate(ctx, request)
		if err != nil || string(first.Page.Bytes) != "old" {
			t.Fatalf("real private read: %+v %v", first, err)
		}
		writePrivate(t, f.service, binding, 302, 2, Change{Slot: "memory", Mode: "replace", Bytes: []byte("new")})
		limits := DefaultLimits()
		limits.MaxScopedBytes = 1
		limits.MaxBudgetScopes = 3
		reopenScoped(t, f, limits)
		agent = scopedBound(t, f.service, binding)
		// The original read has used the sole unit. Its retry skips new-work
		// limits and max_operation_bytes, but still reserves real allocation.
		capacity.BufferBytes, capacity.MaxOperationBytes = 4, 0
		if _, err := f.service.ConfigureCapacity(ctx, binding, CapacitySpec{CommandID: privateID(303), ExpectedVersion: 1, Scope: ledger.ScopeKey{Kind: "colony"}, Limits: capacity}); err != nil {
			t.Fatal(err)
		}
		before := f.store.Durable()
		newRequest := request
		newRequest.CommandID = privateID(305)
		if _, err := agent.ReadPrivate(ctx, newRequest); !errors.Is(err, ErrTooLarge) || f.store.Durable() != before {
			t.Fatalf("lower byte ceiling was not a new-read guard: %v", err)
		}
		replay, err := agent.ReadPrivate(ctx, request)
		if err != nil || !reflect.DeepEqual(first, replay) || f.store.Durable() != before {
			t.Fatalf("retry changed its pinned selection or charged again: %+v %v", replay, err)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		var block atomic.Bool
		block.Store(true)
		f.service.deps.afterRead = func(_ Binding, scope string) {
			if scope == "context" && block.CompareAndSwap(true, false) {
				close(entered)
				<-release
			}
		}
		type result struct {
			page ScopedPrivatePage
			err  error
		}
		pending := make(chan result, 1)
		go func() { page, err := agent.ReadPrivate(ctx, request); pending <- result{page, err} }()
		select {
		case <-entered:
		case got := <-pending:
			t.Fatalf("read completed before the verification barrier: %+v %v", got.page, got.err)
		}
		var busy *BusyError
		if page, err := agent.ReadPrivate(ctx, request); !errors.As(err, &busy) || !reflect.DeepEqual(page, ScopedPrivatePage{}) || f.store.Durable() != before {
			t.Fatalf("retry ignored physical buffer exposure: %+v %v", page, err)
		}
		status, err := agent.ScopedCommandStatus(ctx, ScopedCommandLookup{CommandID: request.CommandID, GrantID: grant.GrantID, SubmissionSHA256: first.Receipt.SubmissionSHA256})
		if err != nil || !reflect.DeepEqual(status, first.Receipt) {
			t.Fatalf("passive receipt query required read capacity: %+v %v", status, err)
		}
		if _, err := f.service.RevokeGrant(ctx, binding, GrantRevocation{CommandID: privateID(304), RevokeGrantID: grant.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: 1}); err != nil {
			t.Fatal(err)
		}
		before = f.store.Durable()
		if page, err := agent.ReadPrivate(ctx, request); !errors.Is(err, ErrDenied) || errors.As(err, &busy) || !reflect.DeepEqual(page, ScopedPrivatePage{}) || f.store.Durable() != before {
			t.Fatalf("revoked retry returned busy or disclosed bytes: %+v %v", page, err)
		}
		once.Do(func() { close(release) })
		got := <-pending
		if !errors.Is(got.err, ErrDenied) || !reflect.DeepEqual(got.page, ScopedPrivatePage{}) || f.store.Durable() != before {
			t.Fatalf("verified bytes escaped the final authority check: %+v %v", got.page, got.err)
		}
		book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
		if err != nil || book.Used.LocalUnits != 1 || book.Held != (Exposure{}) {
			t.Fatalf("read retries charged or leaked preparation: %+v %v", book, err)
		}
	})
	t.Run("transcript_selection_lower_limit_and_corrupt_suffix", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f, binding, agent, grant := scopedPrivateFixture(t, serviceDeps{}, scopedCapacity())
		writePrivate(t, f.service, binding, 300, 1, Change{Slot: "transcript", Mode: "append", Bytes: []byte("first")})
		writePrivate(t, f.service, binding, 301, 2, Change{Slot: "transcript", Mode: "append", Bytes: []byte("second")})
		request := ScopedPrivateRead{CommandID: privateID(302), GrantID: grant.GrantID, Scope: "context", Slot: "transcript", Revision: 3, MaxBytes: 64}
		first, err := agent.ReadPrivate(ctx, request)
		if err != nil || len(first.Page.Chunks) != 2 || string(first.Page.Chunks[0].Bytes) != "first" || string(first.Page.Chunks[1].Bytes) != "second" || first.Page.NextCursor != 0 {
			t.Fatalf("transcript page: %+v %v", first, err)
		}
		limits := DefaultLimits()
		limits.MaxSelectedObjects = 1
		reopenScoped(t, f, limits)
		agent = scopedBound(t, f.service, binding)
		before := f.store.Durable()
		if replay, err := agent.ReadPrivate(ctx, request); err != nil || !reflect.DeepEqual(first, replay) || f.store.Durable() != before {
			t.Fatalf("selected-object limit invalidated an original receipt: %+v %v", replay, err)
		}
		pageRequest := request
		pageRequest.CommandID = privateID(303)
		page, err := agent.ReadPrivate(ctx, pageRequest)
		if err != nil || len(page.Page.Chunks) != 1 || string(page.Page.Chunks[0].Bytes) != "first" || page.Page.NextCursor != first.Page.Chunks[0].Sequence {
			t.Fatalf("new reads ignored the selected-object ceiling: %+v %v", page, err)
		}
		pageRequest.CommandID, pageRequest.Cursor = privateID(304), page.Page.NextCursor
		page, err = agent.ReadPrivate(ctx, pageRequest)
		if err != nil || len(page.Page.Chunks) != 1 || string(page.Page.Chunks[0].Bytes) != "second" || page.Page.NextCursor != 0 {
			t.Fatalf("transcript continuation skipped its occurrence: %+v %v", page, err)
		}
		pageRequest.CommandID, pageRequest.Cursor = privateID(305), first.Page.Chunks[1].Sequence
		empty, err := agent.ReadPrivate(ctx, pageRequest)
		if err != nil || !empty.Page.Present || len(empty.Page.Chunks) != 0 || empty.Page.NextCursor != 0 {
			t.Fatalf("end cursor was confused with an absent transcript: %+v %v", empty, err)
		}
		before = f.store.Durable()
		if replay, err := agent.ReadPrivate(ctx, pageRequest); err != nil || !reflect.DeepEqual(replay, empty) || f.store.Durable() != before {
			t.Fatalf("empty selection was not preserved on replay: %+v %v", replay, err)
		}
		snapshot, err := agent.Snapshot(ctx, ScopedSnapshotLookup{GrantID: grant.GrantID, Scope: "context"})
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(f.root, "agents", "Scout-10", "contexts", binding.ContextID.String(), "core", "objects", snapshot.Heads["transcript"].SHA256.String())
		writeTestFile(t, name, []byte("broken"))
		before = f.store.Durable()
		if page, err := agent.ReadPrivate(ctx, request); err != ErrIntegrity || !reflect.DeepEqual(page, ScopedPrivatePage{}) || f.store.Durable() != before {
			t.Fatalf("corrupt suffix returned a verified prefix or an unauthorized hash: %+v %v", page, err)
		}
	})
	t.Run("whole_snapshot_and_hash_free_inherited_fence", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f, binding, agent, grant := scopedPrivateFixture(t, serviceDeps{}, scopedCapacity())
		writePrivate(t, f.service, binding, 300, 1, Change{Slot: "memory", Mode: "replace", Bytes: []byte("context fact")})
		partial := privateScopedGrant(binding, 210, scopedCapacity(), "context.inspect")
		partial.Resources[0].Slots = []string{"memory"}
		issueScopedRoot(t, f.service, binding, 211, partial)
		if _, err := agent.Snapshot(ctx, ScopedSnapshotLookup{GrantID: partial.GrantID, Scope: "context"}); !errors.Is(err, ErrDenied) {
			t.Fatalf("partial grant disclosed a whole snapshot: %v", err)
		}
		agentGrant := privateScopedGrant(binding, 220, scopedCapacity(), "context.inspect")
		agentGrant.Resources[0].Scope, agentGrant.Resources[0].Slots = "agent", []string{"instructions", "memory"}
		issueScopedRoot(t, f.service, binding, 221, agentGrant)
		writeTestFile(t, filepath.Join(f.root, "agents", "Scout-10", "Agent.md"), []byte("foreign instructions"))
		if err := f.service.Close(); err != nil {
			t.Fatal(err)
		}
		var report Reconciliation
		var err error
		f.service, report, err = Open(ctx, f.store, f.root, DefaultLimits())
		if err != nil || len(report.Issues) != 1 {
			t.Fatalf("foreign agent materialization was not fenced: %+v %v", report, err)
		}
		agent = scopedBound(t, f.service, binding)
		contextView, err := agent.Snapshot(ctx, ScopedSnapshotLookup{GrantID: grant.GrantID, Scope: "context"})
		if err != nil || len(contextView.Issues) != 1 || contextView.Heads["memory"].Bytes != int64(len("context fact")) {
			t.Fatalf("authorized context snapshot lost its facts: %+v %v", contextView, err)
		}
		issue := contextView.Issues[0]
		if issue.Scope != "agent" || issue.Reason != "agent_integrity" || issue.Slot != "" || issue.ExpectedSHA256 != (ledger.Digest{}) || issue.ObservedSHA256 != (ledger.Digest{}) {
			t.Fatalf("inherited fence disclosed another scope's hashes: %+v", issue)
		}
		own, err := agent.Snapshot(ctx, ScopedSnapshotLookup{GrantID: agentGrant.GrantID, Scope: "agent"})
		if err != nil || len(own.Issues) != 1 || own.Issues[0].ExpectedSHA256 == (ledger.Digest{}) || own.Issues[0].Slot != "instructions" {
			t.Fatalf("own-scope authority hid its integrity details: %+v %v", own, err)
		}
		if _, err := agent.ReadPrivate(ctx, ScopedPrivateRead{CommandID: privateID(310), GrantID: grant.GrantID, Scope: "context", Slot: "memory", Revision: 2, MaxBytes: 64}); err != ErrIntegrity {
			t.Fatalf("agent read leaked an integrity hash: %v", err)
		}
		if _, err := agent.WritePrivate(ctx, ScopedPrivateWrite{CommandID: privateID(311), GrantID: grant.GrantID, Scope: "context", ExpectedVersion: 2, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("next")}}}); err != ErrIntegrity {
			t.Fatalf("agent write leaked an integrity hash: %v", err)
		}
		if _, err := agent.StartTurn(ctx, TurnAdmission{CommandID: privateID(312), GrantID: grant.GrantID, TurnID: privateID(313)}); err != ErrIntegrity {
			t.Fatalf("turn admission leaked an integrity hash: %v", err)
		}
	})
	t.Run("scratch_binding_requires_own_workspace_root", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f, agent, _, admit := scopedLocalFixture(t, DefaultLimits())
		binding := Binding{AgentID: agent.ids.agent, ContextID: agent.ids.context}
		owner, err := f.service.ScratchBinding(ctx, ScratchLookup{Binding: binding, WorkspaceID: admit.Target.WorkspaceID})
		if err != nil || owner.State != "present" {
			t.Fatalf("golden dedicated scratch: %+v %v", owner, err)
		}
		lookup := ScopedScratchLookup{GrantID: admit.GrantID, WorkspaceID: admit.Target.WorkspaceID, ExpectedScratchID: owner.ScratchID}
		if _, err := agent.ScratchBinding(ctx, lookup); !errors.Is(err, ErrDenied) {
			t.Fatalf("sub-prefix grant disclosed root binding: %v", err)
		}
		root := admit.Target
		root.PathPrefix = ""
		spec := GrantSpec{GrantID: privateID(9070), Grantee: binding, Operations: []string{"file.read"}, Resources: []ledger.Resource{root}, DataClass: "workspace", Recipients: []Binding{binding}, Limits: scopedCapacity(), Lifetime: "until_revoked", TaskBinding: admit.TaskBinding}
		issueScopedRoot(t, f.service, binding, 9071, spec)
		lookup.GrantID = spec.GrantID
		got, err := agent.ScratchBinding(ctx, lookup)
		if err != nil || got != owner {
			t.Fatalf("root-authorized binding: %+v %v", got, err)
		}
		other := scopedWorkContext(t, f.service, binding, 9080, 9081)
		if _, err := scopedBound(t, f.service, other).ScratchBinding(ctx, lookup); !errors.Is(err, ErrDenied) {
			t.Fatalf("another context borrowed the scratch binding: %v", err)
		}
	})
}

func TestPrivateCurrentHeadSelection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, b, agent, grant := scopedPrivateFixture(t, serviceDeps{}, scopedCapacity())
	writePrivate(t, f.service, b, 300, 1, Change{"continuation", "replace", []byte("kept")}, Change{"memory", "replace", []byte("same")}, Change{"transcript", "append", []byte("same")})
	first, err := f.service.Snapshot(ctx, ReadBinding{b, "context"})
	if err != nil {
		t.Fatal(err)
	}
	olderPrefix := f.store.Durable()
	if _, err := agent.WritePrivate(ctx, ScopedPrivateWrite{CommandID: privateID(301), GrantID: grant.GrantID, Scope: "context", ExpectedVersion: 2, Changes: []Change{{"handoff", "replace", []byte("later")}, {"transcript", "append", []byte("same")}}}); err != nil {
		t.Fatal(err)
	}
	second, err := f.service.Snapshot(ctx, ReadBinding{b, "context"})
	if err != nil {
		t.Fatal(err)
	}
	writePrivate(t, f.service, b, 302, 3, Change{"continuation", "clear", nil}, Change{"transcript", "append", []byte("last")})
	third, err := f.service.Snapshot(ctx, ReadBinding{b, "context"})
	if err != nil {
		t.Fatal(err)
	}
	type readCase struct {
		slot             string
		revision, cursor uint64
		maxBytes         int64
		heads            []Head
		present          bool
		next             uint64
		body             string
	}
	cases := []readCase{
		{"memory", 4, 0, 4, []Head{first.Heads["memory"]}, true, 0, "same"},
		{"continuation", 4, 0, 1, nil, false, 0, ""},
		{"continuation", 2, 0, 4, []Head{first.Heads["continuation"]}, true, 0, "kept"},
		{"transcript", 4, 0, 4, []Head{first.Heads["transcript"]}, true, first.Heads["transcript"].Sequence, "same"},
		{"transcript", 4, first.Heads["transcript"].Sequence, 4, []Head{second.Heads["transcript"]}, true, second.Heads["transcript"].Sequence, "same"},
		{"transcript", 4, second.Heads["transcript"].Sequence, 4, []Head{third.Heads["transcript"]}, true, 0, "last"},
		{"transcript", 4, third.Heads["transcript"].Sequence, 4, nil, true, 0, ""},
	}
	var requests []ScopedPrivateRead
	var pages []ScopedPrivatePage
	for i, row := range cases {
		// Cold and repeated warm calls have the same observable receipt/page.
		f.service.mu.Lock()
		f.service.history.close()
		f.service.history = newHistoryIndex()
		f.service.mu.Unlock()
		request := ScopedPrivateRead{CommandID: privateID(310 + i), GrantID: grant.GrantID, Scope: "context", Slot: row.slot, Revision: row.revision, Cursor: row.cursor, MaxBytes: row.maxBytes}
		got, err := agent.ReadPrivate(ctx, request)
		if err != nil || got.Page.Revision != row.revision || got.Page.Present != row.present || got.Page.NextCursor != row.next {
			t.Fatalf("selection %d: %+v %v", i, got, err)
		}
		if row.slot == "transcript" {
			if len(got.Page.Chunks) != len(row.heads) {
				t.Fatalf("transcript occurrence %d: %+v", i, got.Page)
			}
			for j, h := range row.heads {
				if got.Page.Chunks[j].Sequence != h.Sequence || got.Page.Chunks[j].Revision != h.Revision || string(got.Page.Chunks[j].Bytes) != row.body {
					t.Fatalf("repeated digest lost occurrence %d: %+v", i, got.Page)
				}
			}
		} else if string(got.Page.Bytes) != row.body {
			t.Fatalf("selected wrong bytes %d: %q", i, got.Page.Bytes)
		}
		var selected []ledger.SelectedObject
		var usageFound bool
		if err := f.service.scan(ctx, 0, f.store.Durable(), func(c ledger.Commit) error {
			for _, record := range c.Records {
				if record.Type == ledger.TypeRuntimeLocalUsed && record.OperationID == request.CommandID {
					usage, err := ledger.DecodeRuntimeLocalUsed(record.Payload)
					if err != nil {
						return err
					}
					selected, usageFound = usage.Selected, true
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if !usageFound || len(selected) != len(row.heads) {
			t.Fatalf("receipt lost exact selected metadata %d: %+v", i, selected)
		}
		for j, h := range row.heads {
			want := ledger.SelectedObject{SHA256: h.SHA256, Bytes: h.Bytes, Revision: int64(h.Revision), Sequence: int64(h.Sequence)}
			if selected[j] != want {
				t.Fatalf("scope revision replaced unchanged Head occurrence %d: %+v want %+v", i, selected[j], want)
			}
		}
		before := f.store.Durable()
		if replay, err := agent.ReadPrivate(ctx, request); err != nil || !reflect.DeepEqual(replay, got) || f.store.Durable() != before {
			t.Fatalf("warm selection replay %d: %+v %v", i, replay, err)
		}
		requests, pages = append(requests, request), append(pages, got)
	}
	before := f.store.Durable()
	if _, err := agent.ReadPrivate(ctx, ScopedPrivateRead{CommandID: privateID(330), GrantID: grant.GrantID, Scope: "context", Slot: "memory", Revision: 4, MaxBytes: 3}); !errors.Is(err, ErrTooLarge) || f.store.Durable() != before {
		t.Fatalf("current head exceeded byte boundary: %v", err)
	}
	writePrivate(t, f.service, b, 331, 4, Change{"memory", "replace", []byte("future")})
	ids := submissionIDs{b.AgentID, privateID(11), b.ContextID}
	read := PrivateRead{ReadBinding: ReadBinding{b, "context"}, Slot: "memory", Revision: 2, MaxBytes: 4}
	selection, err := f.service.selectPrivateRead(ctx, ids, read, olderPrefix, 1)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := f.service.scopePath(ids, "context")
	page, err := f.service.readPrivateSelection(path, read, selection)
	if err != nil || string(page.Bytes) != "same" {
		t.Fatalf("older prefix borrowed future current bytes: %+v %v", page, err)
	}
	read.Revision = 5
	if _, err := f.service.selectPrivateRead(ctx, ids, read, olderPrefix, 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("current revision became available at an older prefix: %v", err)
	}
	reopenScoped(t, f, DefaultLimits())
	agent = scopedBound(t, f.service, b)
	before = f.store.Durable()
	for i, request := range requests {
		if replay, err := agent.ReadPrivate(ctx, request); err != nil || !reflect.DeepEqual(replay, pages[i]) || f.store.Durable() != before {
			t.Fatalf("reduced receipt changed selection %d: %+v %v", i, replay, err)
		}
	}
}
