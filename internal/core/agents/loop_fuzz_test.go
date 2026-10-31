package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type loopFuzzPrefix struct {
	fixture  *serviceFixture
	baseline *Service
	prepared string
	draft    []ledger.Draft
	through  uint64
}

func retainLoopFuzzPrefix(f *testing.F, fixture *serviceFixture) loopFuzzPrefix {
	f.Helper()
	if fixture.service != nil {
		if err := fixture.service.Close(); err != nil {
			f.Fatal(err)
		}
		fixture.service = nil
	}
	if err := fixture.store.Close(); err != nil {
		f.Fatal(err)
	}
	fixture.store = nil
	prepared := testRoot(f)
	if err := os.CopyFS(prepared, os.DirFS(fixture.root)); err != nil {
		f.Fatal(err)
	}
	var err error
	fixture.store, _, err = ledgerstore.Open(fixture.root, ledger.Defined())
	if err != nil {
		f.Fatal(err)
	}
	fixture.service, _, err = Open(context.Background(), fixture.store, fixture.root, DefaultLimits())
	if err != nil {
		f.Fatal(err)
	}
	baseline, _, err := Open(context.Background(), fixture.store, fixture.root, DefaultLimits())
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { _ = baseline.Close() })
	return loopFuzzPrefix{fixture: fixture, baseline: baseline, prepared: prepared, through: fixture.store.Durable()}
}
func FuzzLoopGroupReplay(f *testing.F) {
	_, groups := loopGoldenGroups(f)
	var prefixes []loopFuzzPrefix
	for _, operation := range []string{"loop.bind", "tool.begin", "tool.result", "loop.end"} {
		for _, selected := range groups {
			if selected.group.receipt.Submission.Operation != operation {
				continue
			}
			fixture, commits := scopedGoldenFixture(f, "15", selected.index)
			prefix := retainLoopFuzzPrefix(f, fixture)
			prefix.draft = draftsOf(commits[selected.index])
			prefixes = append(prefixes, prefix)
			break
		}
	}
	if len(prefixes) != 4 {
		f.Fatal("missing frozen loop shapes")
	}
	for shape := byte(0); shape < 4; shape++ {
		for mode := byte(0); mode < 10; mode++ {
			f.Add(shape, mode, uint64(0), []byte(`{}`))
			f.Add(shape, mode, uint64(1), []byte(nil))
		}
	}
	f.Fuzz(func(t *testing.T, shape, mode byte, value uint64, input []byte) {
		t.Parallel()
		if len(input) > ledger.MaxLoopPayloadBytes {
			return
		}
		prefix := prefixes[int(shape)%len(prefixes)]
		_, _, _, _, _, g, err := decodeFamily(prefix.draft, prefix.through+1)
		if err != nil {
			t.Fatal(err)
		}
		drafts := loopGroupDrafts(t, g, prefix.through+1)
		valid := false
		switch mode % 10 {
		case 0:
			valid = true
		case 1:
			drafts[1].Payload = bytes.Clone(input)
		case 2:
			drafts[len(drafts)-1].Payload = bytes.Clone(input)
		case 3:
			drafts[1].ContextID[15] ^= byte(value)
			valid = byte(value) == 0
		case 4:
			drafts[1].TypeVersion = int(value%256) + 1
			valid = value%256 == 0
		case 5:
			g.receipt.Submission.Origin.ContextID[15] ^= byte(value)
			drafts = loopGroupDrafts(t, g, prefix.through+1)
			valid = byte(value) == 0
		case 6:
			delta := int64(value % 4)
			valid = delta == 0
			switch a := g.receipt.Submission.Arguments.(type) {
			case ledger.LoopBindArguments:
				g.bound.AdmissionSequence += delta
			case ledger.LoopToolBeginArguments:
				a.AttemptID[15] ^= byte(delta)
				g.receipt.Submission.Arguments = a
				v := ledger.RuntimeToolStarted(a)
				g.start = &v
			case ledger.LoopToolResultArguments:
				a.StartedSequence += delta
				g.receipt.Submission.Arguments = a
				v := ledger.RuntimeToolResult(a)
				g.result = &v
			case ledger.LoopEndArguments:
				g.revision.ExpectedVersion += delta
				g.revision.Revision += delta
			}
			drafts = loopGroupDrafts(t, g, prefix.through+1)
		case 7:
			drafts = append(drafts, drafts[1])
		case 8:
			drafts = drafts[:1]
		case 9:
			drafts[1].Actor = ledger.ActorOwner
		}
		accepted, err := loopPreflight(t, prefix.fixture.service, prefix.baseline, drafts)
		if prefix.fixture.store.Durable() != prefix.through {
			t.Fatal("loop preflight appended an effect")
		}
		if err != nil {
			if valid {
				t.Fatal("valid loop control refused", err)
			}
			return
		}
		root := testRoot(t)
		if err := os.CopyFS(root, os.DirFS(prefix.prepared)); err != nil {
			t.Fatal(err)
		}
		store, _, err := ledgerstore.Open(root, ledger.Defined())
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if _, err := store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
			t.Fatal(err)
		}
		through := store.Durable()
		service, report, err := Open(context.Background(), store, root, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		if store.Durable() != through {
			t.Fatal("accepted loop replay repaired itself")
		}
		pending := func(turn, tool uuid.UUID) bool {
			for _, entry := range report.PendingLoops {
				if entry.TurnID == turn && entry.ToolID == tool && entry.State == "recovery_pending" {
					return true
				}
			}
			return false
		}
		switch {
		case accepted.bound != nil:
			if !pending(accepted.bound.TurnID, uuid.UUID{}) {
				t.Fatal("bound turn was not recovered")
			}
		case accepted.start != nil:
			if !pending(accepted.start.ParentTurnID, accepted.start.ToolID) {
				t.Fatal("started tool was not recovered")
			}
		case accepted.result != nil:
			if pending(accepted.receipt.Submission.TurnID, accepted.result.ToolID) {
				t.Fatal("ended tool stayed pending")
			}
		case accepted.ending != nil:
			if pending(accepted.ending.TurnID, uuid.UUID{}) {
				t.Fatal("ended loop stayed pending")
			}
			view, err := service.Snapshot(context.Background(), ReadBinding{Binding: bindingOf(accepted.receipt.Submission.Origin), Scope: "context"})
			if err != nil || view.Revision != uint64(accepted.revision.Revision) || view.Heads["transcript"].SHA256 != accepted.revision.Changes[0].SHA256 {
				t.Fatal("replayed ending lost its common private head")
			}
		}
	})
}

func FuzzLoopTranscript(f *testing.F) {
	fixture := newTurnFixture(f, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, _ := turnRequest(f, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, turnAnthropic(model, nil, "Verified prior answer."))
	}, nil)
	fixture.enable(f)
	_, start := fixture.send(f)
	result, _ := fixture.finished(f, start.TurnID)
	if result.Outcome != "finished" {
		f.Fatal("transcript fixture did not finish")
	}
	view, err := fixture.service.Snapshot(context.Background(), ReadBinding{Binding: bindingOf(fixture.origin), Scope: "context"})
	if err != nil {
		f.Fatal(err)
	}
	page, err := fixture.service.ReadPrivate(context.Background(), PrivateRead{ReadBinding: ReadBinding{Binding: bindingOf(fixture.origin), Scope: "context"}, Slot: "transcript", Revision: view.Revision, MaxBytes: maxObjectBytes})
	if err != nil || len(page.Chunks) != 1 {
		f.Fatal("missing committed transcript", err)
	}
	seed := bytes.Clone(page.Chunks[0].Bytes)
	origin := fixture.origin
	prefix := retainLoopFuzzPrefix(f, fixture.serviceFixture)
	f.Add(seed)
	f.Add([]byte(`{"schema":1,"schema":1,"entries":[]}`))
	f.Add(bytes.ReplaceAll(seed, []byte(origin.ContextID.String()), []byte(uuid.NewV7().String())))
	f.Add([]byte(`{"schema":1,"entries":null}`))
	var renderMu sync.Mutex
	f.Fuzz(func(t *testing.T, input []byte) {
		t.Parallel()
		if len(input) > maxObjectBytes {
			return
		}
		runtime := &loopRuntime{service: prefix.fixture.service}
		turn := &loopTurn{origin: origin, ctx: context.Background()}
		renderMu.Lock()
		text, err := runtime.renderTranscript(context.Background(), turn, input)
		prefix.fixture.service.mu.Lock()
		unchanged := sameProjectionState(prefix.fixture.service.p, prefix.baseline.p)
		prefix.fixture.service.mu.Unlock()
		renderMu.Unlock()
		if !unchanged || prefix.fixture.store.Durable() != prefix.through {
			t.Fatal("transcript parsing changed its authority or ledger")
		}
		if err != nil {
			if bytes.Equal(input, seed) {
				t.Fatal("producer transcript refused", err)
			}
			return
		}
		if !json.Valid(input) {
			t.Fatal("rendered invalid transcript JSON")
		}
		root := testRoot(t)
		if err := os.CopyFS(root, os.DirFS(prefix.prepared)); err != nil {
			t.Fatal(err)
		}
		store, _, err := ledgerstore.Open(root, ledger.Defined())
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		service, _, err := Open(context.Background(), store, root, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		replayed, err := (&loopRuntime{service: service}).renderTranscript(context.Background(), turn, input)
		if err != nil || replayed != text || store.Durable() != prefix.through {
			t.Fatal("accepted transcript changed disclosure on Open", err)
		}
	})
}
