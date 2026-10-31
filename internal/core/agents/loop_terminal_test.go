package agents

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/terminal"
)

// terminalHarness runs the executable's terminal.Run over real Sessions with
// piped input and output. Waits are on output lines, never on time.
type terminalHarness struct {
	in         *io.PipeWriter
	interrupts chan os.Signal
	done       chan error
	mu         sync.Mutex
	lines      []string
	more       chan struct{}
	ended      chan struct{}
}

func terminalDescribe(err error) string {
	var busy *BusyError
	if errors.As(err, &busy) {
		return "busy: " + busy.Reason
	}
	for _, known := range []error{ErrDenied, ErrUnavailable, ErrClosed, ErrCommandMismatch, ErrInvalid, ErrNotBound} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return ""
}

func runTerminal(t *testing.T, views []terminal.Agent, interactive bool, drain ...func() error) *terminalHarness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h := &terminalHarness{in: inW, interrupts: make(chan os.Signal, 1), done: make(chan error, 1), more: make(chan struct{}, 1), ended: make(chan struct{})}
	go func() {
		defer close(h.ended)
		scanner := bufio.NewScanner(outR)
		scanner.Buffer(make([]byte, 64<<10), 4<<20)
		for scanner.Scan() {
			h.mu.Lock()
			h.lines = append(h.lines, scanner.Text())
			h.mu.Unlock()
			select {
			case h.more <- struct{}{}:
			default:
			}
		}
	}()
	go func() {
		err := terminal.Run(context.Background(), terminal.Config{Agents: views, Input: inR, Output: outW, Interactive: interactive, Interrupts: h.interrupts, Describe: terminalDescribe, Drain: drain})
		_ = outW.Close()
		h.done <- err
	}()
	t.Cleanup(func() { _ = inW.Close() })
	return h
}

func (h *terminalHarness) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(h.in, line+"\n"); err != nil {
		t.Fatal("terminal input closed")
	}
}

// wait returns the index of the first line at or after from containing every
// part; a terminal that exits without one fails the test.
func (h *terminalHarness) wait(t *testing.T, from int, parts ...string) int {
	t.Helper()
	for {
		h.mu.Lock()
		for i := from; i < len(h.lines); i++ {
			if containsAll(h.lines[i], parts) {
				h.mu.Unlock()
				return i
			}
		}
		h.mu.Unlock()
		select {
		case <-h.more:
		case <-h.ended:
			h.mu.Lock()
			for i := from; i < len(h.lines); i++ {
				if containsAll(h.lines[i], parts) {
					h.mu.Unlock()
					return i
				}
			}
			h.mu.Unlock()
			t.Fatalf("terminal output ended without a line containing %q", parts)
		}
	}
}

func (h *terminalHarness) line(i int) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lines[i]
}

func (h *terminalHarness) all() []string {
	<-h.ended
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.lines)
}

func containsAll(line string, parts []string) bool {
	for _, part := range parts {
		if !strings.Contains(line, part) {
			return false
		}
	}
	return true
}

func terminalTail(id uuid.UUID) string {
	if id == (uuid.UUID{}) {
		return "--------"
	}
	s := id.String()
	return s[len(s)-8:]
}

func terminalLabel(agent, contextID, turn uuid.UUID) string {
	return "[" + terminalTail(agent) + "/" + terminalTail(contextID) + "/" + terminalTail(turn) + "]"
}

// turnFrom reads the full turn ID that ends a turn.started event line.
func turnFrom(t *testing.T, line string) uuid.UUID {
	t.Helper()
	fields := strings.Fields(line)
	id, err := uuid.Parse(fields[len(fields)-1])
	if err != nil {
		t.Fatal("turn.started line lacks its turn ID")
	}
	return id
}

func (f *turnFixture) terminalView(t *testing.T, binding Binding) session.Session {
	t.Helper()
	view, err := f.service.OpenSession(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

// Lines report the bound identities of their own event, turn or command; the
// selection moves twice while two agents' turns stream.
func TestTerminalAttribution(t *testing.T) {
	t.Parallel()
	gates := map[string]chan struct{}{"alpha": make(chan struct{}), "beta": make(chan struct{})}
	f := newTurnFixture(t, "anthropic", finalScript(t, []string{"alpha", "beta"}, gates, nil), nil)
	work := f.workSetting(t, 73000, "Alpha task")
	peerBinding := registerPrivate(t, f.service, 74000)
	f.enable(t, work, f.contextSetting(t, peerBinding))
	a, general, w, b, g := f.origin.AgentID, f.origin.ContextID, work.Binding.ContextID, peerBinding.AgentID, peerBinding.ContextID
	h := runTerminal(t, []terminal.Agent{
		{AgentID: a, Session: f.terminalView(t, bindingOf(f.origin)), Contexts: []terminal.Context{{ContextID: general, Title: "General work"}, {ContextID: w, Title: "Alpha task"}}},
		{AgentID: b, Session: f.terminalView(t, peerBinding), Contexts: []terminal.Context{{ContextID: g, Title: "General work"}}},
	}, false)
	i := h.wait(t, 0, "ready")
	h.send(t, "/context "+a.String()+" "+w.String())
	i = h.wait(t, i, "selected Alpha task")
	h.send(t, "alpha request")
	i = h.wait(t, i, "event turn.started")
	alpha := turnFrom(t, h.line(i))
	i = h.wait(t, i, "event tool.finished")
	h.send(t, "/context "+b.String()+" "+g.String())
	selectedPeer := h.wait(t, i, "selected General work")
	h.send(t, "beta request")
	i = h.wait(t, selectedPeer, "event turn.started")
	beta := turnFrom(t, h.line(i))
	h.wait(t, i, "event tool.finished")
	close(gates["alpha"])
	alphaFinal := h.wait(t, 0, "final (durable): alpha final answer.")
	alphaLive := h.wait(t, 0, "live (provisional): alpha commentary. alpha final answer.")
	close(gates["beta"])
	h.wait(t, 0, "final (durable): beta final answer.")
	h.send(t, "/quit")
	if err := <-h.done; err != nil {
		t.Fatal(err)
	}
	if alphaLive > alphaFinal || alphaLive < selectedPeer {
		t.Fatal("provisional text did not precede the durable answer after the selection moved")
	}
	labels := map[uuid.UUID]string{alpha: terminalLabel(a, w, alpha), beta: terminalLabel(b, g, beta)}
	markers := map[string]uuid.UUID{"alpha ": alpha, "beta ": beta}
	for _, line := range h.all() {
		for turn, label := range labels {
			if strings.Contains(line, terminalTail(turn)) && !strings.HasPrefix(line, label+" ") {
				t.Fatalf("a line about a turn carried other identities: %q", line)
			}
		}
		for marker, turn := range markers {
			if strings.Contains(line, marker) && !strings.HasPrefix(line, labels[turn]+" ") {
				t.Fatalf("turn text was labelled by the selection: %q", line)
			}
		}
	}
}

// An interrupt cancels only the selected context's active turn and keeps the
// terminal; /cancel keeps the named turn's own context; an idle interrupt
// detaches.
func TestTerminalCancel(t *testing.T) {
	t.Parallel()
	t.Run("interrupt_and_named_cancel", func(t *testing.T) {
		t.Parallel()
		var posts atomic.Int64
		gates := map[string]chan struct{}{"held": make(chan struct{}), "moved": make(chan struct{})}
		entered := make(chan string, 2)
		script := finalScript(t, []string{"moved", "held"}, gates, entered)
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) { posts.Add(1); script(w, r) }, nil)
		work := f.workSetting(t, 75000, "Moved task")
		f.enable(t, work)
		a, general, w := f.origin.AgentID, f.origin.ContextID, work.Binding.ContextID
		h := runTerminal(t, []terminal.Agent{{AgentID: a, Session: f.terminalView(t, bindingOf(f.origin)), Contexts: []terminal.Context{{ContextID: general, Title: "General work"}, {ContextID: w, Title: "Moved task"}}}}, false)
		i := h.wait(t, 0, "ready")
		h.send(t, "held request")
		i = h.wait(t, i, "event turn.started")
		held := turnFrom(t, h.line(i))
		i = h.wait(t, i, "event tool.finished")
		if <-entered != "held" {
			t.Fatal("unexpected continuation")
		}
		h.interrupts <- os.Interrupt
		heldLines := []int{h.wait(t, i, "interrupt cancellation requested"), h.wait(t, i, "command turn.cancel applied"), h.wait(t, i, "event turn.cancelled"), h.wait(t, i, "final (durable) none: the turn ended cancelled")}
		i = slices.Max(heldLines)
		h.send(t, "/context "+a.String()+" "+w.String())
		i = h.wait(t, i, "selected Moved task")
		h.send(t, "moved request")
		i = h.wait(t, i, "event turn.started")
		moved := turnFrom(t, h.line(i))
		i = h.wait(t, i, "event tool.finished")
		if <-entered != "moved" {
			t.Fatal("unexpected continuation")
		}
		h.send(t, "/context "+a.String()+" "+general.String())
		i = h.wait(t, i, "selected General work")
		h.send(t, "/cancel "+moved.String())
		movedLines := []int{h.wait(t, i, "command turn.cancel applied"), h.wait(t, i, "event turn.cancelled")}
		i = slices.Max(movedLines)
		h.send(t, "/status "+held.String())
		heldLines = append(heldLines, h.wait(t, i, "status state=cancelled"))
		h.send(t, "/cancel "+held.String())
		heldLines = append(heldLines, h.wait(t, i, "refused: turn.cancel agents: unavailable"))
		h.interrupts <- os.Interrupt
		h.wait(t, i, "interrupt idle prompt; detaching")
		if err := <-h.done; err != nil {
			t.Fatal(err)
		}
		for _, n := range heldLines {
			if !strings.HasPrefix(h.line(n), terminalLabel(a, general, held)+" ") {
				t.Fatalf("held turn's cancellation line lost its identity: %q", h.line(n))
			}
		}
		for _, n := range movedLines {
			if !strings.HasPrefix(h.line(n), terminalLabel(a, w, moved)+" ") {
				t.Fatalf("cancellation followed the selection: %q", h.line(n))
			}
		}
		for turn, contextID := range map[uuid.UUID]uuid.UUID{held: general, moved: w} {
			result, err := f.view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: contextID, Operation: "turn.status", Arguments: session.TurnStatus{TurnID: turn}})
			if view, ok := result.Data.(session.TurnView); err != nil || !ok || view.State != "cancelled" {
				t.Fatal("a terminal cancellation was not the recorded ending")
			}
		}
		if posts.Load() != 4 {
			t.Fatalf("cancelled turns sent %d POSTs, want 2 each", posts.Load())
		}
	})
	t.Run("interactive_end_waits_for_turn", func(t *testing.T) {
		t.Parallel()
		gates := map[string]chan struct{}{"kept": make(chan struct{})}
		f := newTurnFixture(t, "anthropic", finalScript(t, []string{"kept"}, gates, nil), nil)
		f.enable(t)
		h := runTerminal(t, []terminal.Agent{{AgentID: f.origin.AgentID, Session: f.terminalView(t, bindingOf(f.origin)), Contexts: []terminal.Context{{ContextID: f.origin.ContextID, Title: "General work"}}}}, true)
		i := h.wait(t, 0, "ready")
		h.send(t, "kept request")
		i = h.wait(t, i, "event tool.finished")
		// A console interrupt also ends a pending read: that must not drain
		// Core under the active turn.
		_ = h.in.Close()
		i = h.wait(t, i, "input ended while the selected turn is active")
		close(gates["kept"])
		i = h.wait(t, i, "final (durable): kept final answer.")
		h.wait(t, i, "input ended; detaching")
		if err := <-h.done; err != nil {
			t.Fatal(err)
		}
	})
}

// End of input detaches the views, then the host's drain stops Core: the
// in-flight turn gets a recorded interrupted ending, a failing step does not
// skip later ones, and no paid work outlives the run.
func TestTerminalDrain(t *testing.T) {
	t.Parallel()
	var posts atomic.Int64
	gates := map[string]chan struct{}{"held": make(chan struct{})}
	entered := make(chan string, 1)
	script := finalScript(t, []string{"held"}, gates, entered)
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) { posts.Add(1); script(w, r) }, nil)
	f.enable(t)
	var order []string
	h := runTerminal(t, []terminal.Agent{{AgentID: f.origin.AgentID, Session: f.terminalView(t, bindingOf(f.origin)), Contexts: []terminal.Context{{ContextID: f.origin.ContextID, Title: "General work"}}}}, false,
		func() error { order = append(order, "core"); return f.service.Close() },
		func() error { order = append(order, "refusing"); return ErrUnavailable },
		func() error { order = append(order, "workspace"); return f.workspace.Close() })
	i := h.wait(t, 0, "ready")
	h.send(t, "held request")
	i = h.wait(t, i, "event turn.started")
	held := turnFrom(t, h.line(i))
	i = h.wait(t, i, "event tool.finished")
	<-entered
	_ = h.in.Close()
	if err := <-h.done; !errors.Is(err, ErrUnavailable) {
		t.Fatal("a refused drain step was not reported", err)
	}
	if !slices.Equal(order, []string{"core", "refusing", "workspace"}) {
		t.Fatalf("drain steps ran as %v", order)
	}
	steps := []int{h.wait(t, i, "input ended; detaching"), h.wait(t, i, "draining"), h.wait(t, i, "drain refused: agents: unavailable"), h.wait(t, i, "drained")}
	if !slices.IsSorted(steps) {
		t.Fatal("detach and drain were reported out of order")
	}
	service, report, err := Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if len(report.PendingLoops) != 0 {
		t.Fatal("exit left an unended turn")
	}
	var outcome, reason string
	err = service.scan(context.Background(), 0, f.store.Durable(), func(c ledger.Commit) error {
		for _, record := range c.Records {
			if record.Type == ledger.TypeRuntimeTurnResult && record.TurnID == held {
				result, err := ledger.DecodeRuntimeTurnResult(record.Payload)
				outcome, reason = result.Outcome, result.Reason
				return err
			}
		}
		return nil
	})
	if err != nil || outcome != "interrupted" || reason != "shutdown" {
		t.Fatalf("drained turn ended %q/%q, want interrupted/shutdown", outcome, reason)
	}
	if posts.Load() != 2 {
		t.Fatalf("drained turn sent %d POSTs, want 2", posts.Load())
	}
}

// The executable's terminal entry against the in-process loopback host: a real
// edit/read turn, a selection change during its output, a multiline /send, an
// interrupt cancellation, an idle exit and the drain.
func TestTurnLoopTerminalSmoke(t *testing.T) {
	t.Parallel()
	var calls []struct{ ID, Name, Arguments string }
	var posts atomic.Int64
	heldEntered := make(chan struct{})
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		model, raw := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		switch {
		case bytes.Contains(raw, []byte("held smoke request")):
			close(heldEntered)
			<-r.Context().Done()
		case bytes.Contains(raw, []byte("work line one")):
			if !bytes.Contains(raw, []byte(`work line one\nwork line two`)) {
				t.Error("multiline /send text did not reach the model whole")
			}
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "Work final answer."))
		case !bytes.Contains(raw, []byte("smoke request")):
			t.Error("unexpected request reached the provider")
		case !bytes.Contains(raw, []byte("tool_result")):
			_, _ = io.WriteString(w, turnAnthropic(model, calls, ""))
		default:
			if !bytes.Contains(raw, []byte("after smoke edit")) {
				t.Error("edited bytes absent from the continuation")
			}
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "Smoke final answer."))
		}
	}, nil)
	scratch, _ := f.files(t, "safe")
	file := filepath.Join(scratch.Dir, "safe", "value.txt")
	if err := os.Mkdir(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	seeded := "before smoke edit"
	if err := os.WriteFile(file, []byte(seeded), 0600); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf(`"workspace_id":%q,"path":"safe/value.txt"`, scratch.WorkspaceID)
	calls = []struct{ ID, Name, Arguments string }{
		{"edit-one", "file_edit_v1", fmt.Sprintf(`{"schema":1,%s,"expected_sha256":%q,"old_text":"before","new_text":"after"}`, base, ledger.Digest(sha256.Sum256([]byte(seeded))).String())},
		{"read-one", "file_read_v1", fmt.Sprintf(`{"schema":1,%s,"max_bytes":1024}`, base)},
	}
	work := f.workSetting(t, 76000, "Smoke work")
	f.enable(t, work)
	a, general, w := f.origin.AgentID, f.origin.ContextID, work.Binding.ContextID
	h := runTerminal(t, []terminal.Agent{{AgentID: a, Session: f.terminalView(t, bindingOf(f.origin)), Contexts: []terminal.Context{{ContextID: general, Title: "General work"}, {ContextID: w, Title: "Smoke work"}}}}, false, f.service.Close, f.workspace.Close)
	i := h.wait(t, 0, "ready")
	h.send(t, "smoke request")
	i = h.wait(t, i, "event turn.started")
	smoke := turnFrom(t, h.line(i))
	h.send(t, "/context "+a.String()+" "+w.String())
	label := terminalLabel(a, general, smoke) + " "
	smokeLines := []int{
		h.wait(t, i, "event tool.started file_edit_v1 file.edit"), h.wait(t, i, "event tool.started file_read_v1 file.read"),
		h.wait(t, i, "event tool.finished ok"), h.wait(t, i, "live (provisional): Smoke final answer."),
		h.wait(t, i, "event turn.finished outcome=finished"), h.wait(t, i, "final (durable): Smoke final answer."),
	}
	selected := h.wait(t, i, "selected Smoke work")
	for _, n := range smokeLines {
		if !strings.HasPrefix(h.line(n), label) {
			t.Fatalf("smoke turn line lost its identity: %q", h.line(n))
		}
	}
	if text, err := f.view.FinalText(context.Background(), smoke); err != nil || text != "Smoke final answer." {
		t.Fatal("printed answer differs from the durable final message", err)
	}
	if actual, err := os.ReadFile(file); err != nil || string(actual) != "after smoke edit" {
		t.Fatal("the terminal's turn did not edit the actual file")
	}
	i = max(slices.Max(smokeLines), selected)
	h.send(t, fmt.Sprintf(`/send {"schema":1,"command_id":%q,"context_id":%q,"operation":"message.send","arguments":{"turn_id":%q,"text":"work line one\nwork line two"}}`, uuid.NewV7(), w, uuid.NewV7()))
	i = h.wait(t, i, "final (durable): Work final answer.")
	if !strings.HasPrefix(h.line(i), "["+terminalTail(a)+"/"+terminalTail(w)+"/") {
		t.Fatalf("work answer lost its context: %q", h.line(i))
	}
	h.send(t, "held smoke request")
	i = h.wait(t, i, "event turn.started")
	held := turnFrom(t, h.line(i))
	<-heldEntered
	h.interrupts <- os.Interrupt
	i = h.wait(t, i, "event turn.cancelled")
	if !strings.HasPrefix(h.line(i), terminalLabel(a, w, held)+" ") {
		t.Fatalf("cancellation reported another turn: %q", h.line(i))
	}
	h.interrupts <- os.Interrupt
	if err := <-h.done; err != nil {
		t.Fatal(err)
	}
	h.wait(t, i, "drained")
	if posts.Load() != 4 {
		t.Fatalf("smoke sent %d POSTs, want 4", posts.Load())
	}
}
