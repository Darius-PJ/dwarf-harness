package terminal

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

// Agent is one registered agent's Session and the contexts the host enabled.
type Agent struct {
	AgentID  uuid.UUID
	Session  session.Session
	Contexts []Context
}

// Context is an enabled context and its public display title.
type Context struct {
	ContextID uuid.UUID
	Title     string
}

// Config wires Run to its owning host.
type Config struct {
	Agents []Agent
	Input  io.Reader
	Output io.Writer
	// Interactive marks console input. An end-of-input key read while the
	// selected context has an active turn pauses reading until that turn
	// ends, since a console interrupt can also end a pending read.
	Interactive bool
	Interrupts  <-chan os.Signal
	// Describe maps a Core error to fixed text without paths, values or
	// locators; nil prints only "refused".
	Describe func(error) string
	// Drain runs in order after every view is detached, so the executable
	// stops Core before it exits and leaves no paid work running.
	Drain []func() error
}

// ErrConfig refuses a Config without agents, I/O or with duplicate identities.
var ErrConfig = errors.New("terminal: invalid configuration")

type runner struct {
	cfg      Config
	ctx      context.Context
	notes    chan func()
	jobs     chan job
	pending  []job
	resume   chan struct{}
	wg       sync.WaitGroup
	agents   map[uuid.UUID]*Agent
	owners   map[uuid.UUID]uuid.UUID
	titles   map[uuid.UUID]string
	selected lineIDs
	turns    map[uuid.UUID]*turnState
	paused   bool
	finals   int
	inflight int
	exit     bool
	forced   bool
	written  error
}

type turnState struct {
	ids        lineIDs
	live       []byte
	liveDone   bool
	ended      bool
	outcome    string
	finalAsked bool
}

type job struct {
	session session.Session
	command session.Command
	ids     lineIDs
}

type input struct {
	line     string
	eof      bool
	tooLarge bool
}

const liveLineBytes = 160

// Run serves line input and labelled output for the given Sessions until
// /quit, an interrupt at an idle prompt, end of input or ctx ends. It then
// detaches every view and runs Drain. Output is written by one goroutine in
// arrival order; Core calls never block it.
func Run(ctx context.Context, cfg Config) error {
	r := &runner{cfg: cfg, notes: make(chan func()), jobs: make(chan job), resume: make(chan struct{}, 1), agents: map[uuid.UUID]*Agent{}, owners: map[uuid.UUID]uuid.UUID{}, titles: map[uuid.UUID]string{}, turns: map[uuid.UUID]*turnState{}}
	valid := len(cfg.Agents) != 0 && cfg.Input != nil && cfg.Output != nil
	for i := range cfg.Agents {
		a := &cfg.Agents[i]
		if a.Session == nil || len(a.Contexts) == 0 || r.agents[a.AgentID] != nil {
			valid = false
			continue
		}
		r.agents[a.AgentID] = a
		for _, c := range a.Contexts {
			if _, dup := r.owners[c.ContextID]; dup {
				valid = false
			}
			r.owners[c.ContextID], r.titles[c.ContextID] = a.AgentID, c.Title
		}
	}
	if !valid {
		// The host's drain still runs: a refused view must not leave Core running.
		for _, a := range cfg.Agents {
			if a.Session != nil {
				a.Session.Close()
			}
		}
		return errors.Join(ErrConfig, r.drain())
	}
	inner, cancel := context.WithCancel(ctx)
	r.ctx = inner
	lines := make(chan input)
	go r.read(lines)
	r.wg.Add(1)
	go r.worker()
	for i := range cfg.Agents {
		r.start(&cfg.Agents[i])
	}
	first := cfg.Agents[0]
	r.selected = lineIDs{agent: first.AgentID, context: first.Contexts[0].ContextID}
	r.say(lineIDs{}, "ready", "selected "+r.selected.label()+" "+r.titles[r.selected.context]+"; commands: /context <agent> <context>, /status <turn>, /cancel <turn>, /send <command JSON>, /quit")
	// Leaving first finishes the commands already read and the durable
	// answers already being fetched, so a script's last lines and an answer
	// that ended just before /quit are not dropped. Another interrupt leaves
	// at once.
	for !r.exit || !r.forced && (r.finals > 0 || r.inflight > 0 || len(r.pending) > 0) {
		var send chan job
		var next job
		if len(r.pending) > 0 {
			send, next = r.jobs, r.pending[0]
		}
		input := lines
		if r.exit {
			input = nil
		}
		select {
		case <-inner.Done():
			r.exit, r.forced = true, true
		case f := <-r.notes:
			f()
		case send <- next:
			r.pending = r.pending[1:]
			r.inflight++
		case in := <-input:
			r.input(in)
		case <-cfg.Interrupts:
			if r.exit {
				r.forced = true
				r.say(lineIDs{}, "interrupt", "leaving without waiting for pending commands")
			} else {
				r.interrupt()
			}
		}
	}
	cancel()
	for _, a := range cfg.Agents {
		a.Session.Close()
	}
	r.wg.Wait()
	return errors.Join(r.written, r.drain())
}

func (r *runner) drain() error {
	r.say(lineIDs{}, "draining", "views detached; stopping Core")
	var drained error
	for _, step := range r.cfg.Drain {
		if err := step(); err != nil {
			r.say(lineIDs{}, "drain", "refused: "+r.describe(err))
			drained = errors.Join(drained, err)
		}
	}
	r.say(lineIDs{}, "drained", "")
	return drained
}

// start lists each enabled context, attaches every active turn and starts the
// agent's event stream at the high-water of its first status query.
func (r *runner) start(a *Agent) {
	var cursor uint64
	for i, c := range a.Contexts {
		ids := lineIDs{agent: a.AgentID, context: c.ContextID}
		r.say(ids, "context", c.Title)
		result, err := a.Session.Do(r.ctx, session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: c.ContextID, Operation: "context.status", Arguments: session.ContextStatus{}})
		view, ok := result.Data.(session.ContextView)
		if err != nil || !ok {
			r.say(ids, "refused:", "context.status "+r.describe(err))
			continue
		}
		if i == 0 {
			cursor = view.ThroughSequence
		}
		for _, turn := range view.Turns {
			r.turnView(lineIDs{agent: a.AgentID, context: c.ContextID, turn: turn.TurnID}, turn)
			if turn.State != "recovery_pending" {
				r.track(lineIDs{agent: a.AgentID, context: c.ContextID, turn: turn.TurnID})
			}
		}
	}
	r.wg.Add(1)
	go r.pump(a, cursor)
}

// post hands f to the output goroutine; it fails once Run is stopping.
func (r *runner) post(f func()) bool {
	select {
	case r.notes <- f:
		return true
	case <-r.ctx.Done():
		return false
	}
}

func (r *runner) say(ids lineIDs, kind, text string) {
	label := "[terminal]"
	if ids.agent != (uuid.UUID{}) {
		label = ids.label()
	}
	if r.written != nil || r.cfg.Output == nil {
		return
	}
	if _, err := io.WriteString(r.cfg.Output, render(label, kind, text)); err != nil {
		r.written, r.exit, r.forced = err, true, true
	}
}

func (r *runner) describe(err error) string {
	var refusal *RefusalError
	switch {
	case err == nil:
		return "unavailable"
	case errors.As(err, &refusal):
		return refusal.Error()
	case errors.Is(err, session.ErrReattachRequired), errors.Is(err, session.ErrFinished), errors.Is(err, session.ErrClosed):
		return err.Error()
	case errors.Is(err, context.Canceled):
		return "cancelled"
	}
	if r.cfg.Describe != nil {
		if text := r.cfg.Describe(err); text != "" {
			return text
		}
	}
	return "refused"
}

func (r *runner) read(lines chan<- input) {
	br := bufio.NewReaderSize(r.cfg.Input, 64<<10)
	for {
		line, err := readLine(br)
		var in input
		switch {
		case errors.Is(err, bufio.ErrTooLong):
			in.tooLarge = true
		case err != nil:
			if line != "" && !r.deliver(lines, input{line: line}) {
				return
			}
			if !r.deliver(lines, input{eof: true}) || !r.cfg.Interactive {
				return
			}
			select {
			case <-r.resume:
				continue
			case <-r.ctx.Done():
				return
			}
		default:
			in.line = line
		}
		if !r.deliver(lines, in) {
			return
		}
	}
}

func (r *runner) deliver(lines chan<- input, in input) bool {
	select {
	case lines <- in:
		return true
	case <-r.ctx.Done():
		return false
	}
}

// readLine returns one line without its line ending, discarding the rest of a
// line beyond MaxLineBytes and reporting bufio.ErrTooLong for it.
func readLine(br *bufio.Reader) (string, error) {
	var line []byte
	tooLong := false
	for {
		chunk, err := br.ReadSlice('\n')
		if !tooLong {
			line = append(line, chunk...)
			if len(line) > MaxLineBytes+2 {
				tooLong, line = true, nil
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if tooLong {
			return "", bufio.ErrTooLong
		}
		line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
		return string(line), err
	}
}

func (r *runner) input(in input) {
	switch {
	case in.tooLarge:
		r.say(lineIDs{}, "refused:", "input line exceeds 2 MiB")
	case in.eof && r.cfg.Interactive && r.activeSelected() != nil:
		r.paused = true
		r.say(lineIDs{}, "input", "ended while the selected turn is active; reading resumes when it ends (/quit leaves)")
	case in.eof:
		r.say(lineIDs{}, "input", "ended; detaching")
		r.exit = true
	default:
		r.line(in.line)
	}
}

func (r *runner) line(line string) {
	if line == "" {
		return
	}
	if !strings.HasPrefix(line, "/") {
		if !utf8.ValidString(line) {
			r.say(lineIDs{}, "refused:", "input is not valid UTF-8")
			return
		}
		ids := r.selected
		ids.turn = uuid.NewV7()
		r.queue(ids, session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: ids.context, Operation: "message.send", Arguments: session.MessageSend{TurnID: ids.turn, Text: line, Artifacts: []ledger.ArtifactRef{}}})
		return
	}
	name, rest, _ := strings.Cut(line, " ")
	fields := strings.Fields(rest)
	switch name {
	case "/quit":
		r.exit = true
	case "/context":
		agent, okAgent := parseFieldID(fields, 0, 2)
		contextID, okContext := parseFieldID(fields, 1, 2)
		if !okAgent || !okContext || r.owners[contextID] != agent {
			r.say(lineIDs{}, "refused:", "/context needs an enabled agent UUID and one of its context UUIDs")
			return
		}
		r.selected = lineIDs{agent: agent, context: contextID}
		r.say(r.selected, "selected", r.titles[contextID])
	case "/status", "/cancel":
		turn, ok := parseFieldID(fields, 0, 1)
		if !ok {
			r.say(lineIDs{}, "refused:", name+" needs one turn UUID")
			return
		}
		ids := r.selected
		if t := r.turns[turn]; t != nil {
			ids = t.ids
		}
		ids.turn = turn
		if name == "/status" {
			r.queue(ids, session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: ids.context, Operation: "turn.status", Arguments: session.TurnStatus{TurnID: turn}})
		} else {
			r.queue(ids, session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: ids.context, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: turn}})
		}
	case "/send":
		command, err := DecodeCommand([]byte(rest))
		if err != nil {
			r.say(lineIDs{}, "refused:", r.describe(err))
			return
		}
		agent, ok := r.owners[command.ContextID]
		if !ok {
			r.say(lineIDs{}, "refused:", "/send names a context that is not enabled")
			return
		}
		ids := lineIDs{agent: agent, context: command.ContextID}
		switch v := command.Arguments.(type) {
		case session.MessageSend:
			ids.turn = v.TurnID
		case session.TurnCancel:
			ids.turn = v.TurnID
		case session.TurnStatus:
			ids.turn = v.TurnID
		}
		r.queue(ids, command)
	default:
		r.say(lineIDs{}, "refused:", "unknown command; use /context, /status, /cancel, /send or /quit")
	}
}

func parseFieldID(fields []string, i, count int) (uuid.UUID, bool) {
	if len(fields) != count {
		return uuid.UUID{}, false
	}
	return parseID(fields[i])
}

func (r *runner) queue(ids lineIDs, command session.Command) {
	r.pending = append(r.pending, job{session: r.agents[ids.agent].Session, command: command, ids: ids})
}

// worker runs Session commands one at a time in arrival order.
func (r *runner) worker() {
	defer r.wg.Done()
	for {
		select {
		case <-r.ctx.Done():
			return
		case j := <-r.jobs:
			result, err := j.session.Do(r.ctx, j.command)
			if !r.post(func() { r.result(j, result, err) }) {
				return
			}
		}
	}
}

func (r *runner) result(j job, result session.CommandResult, err error) {
	r.inflight--
	operation := j.command.Operation
	if err != nil {
		r.say(j.ids, "refused:", operation+" "+r.describe(err))
		return
	}
	switch v := result.Data.(type) {
	case session.TurnView:
		r.turnView(j.ids, v)
	case session.ContextView:
		r.say(j.ids, "context", fmt.Sprintf("revision=%d turns=%d", v.Revision, len(v.Turns)))
		for _, turn := range v.Turns {
			r.turnView(lineIDs{agent: j.ids.agent, context: j.ids.context, turn: turn.TurnID}, turn)
		}
	default:
		text := operation + " " + result.Outcome
		if result.TurnID != (uuid.UUID{}) {
			text += " turn " + result.TurnID.String()
		}
		if result.ReceiptSequence != 0 {
			text += fmt.Sprintf(" receipt=%d effect=%d", result.ReceiptSequence, result.EffectSequence)
		}
		r.say(j.ids, "command", text)
	}
}

func (r *runner) turnView(ids lineIDs, v session.TurnView) {
	if v.State == "recovery_pending" {
		r.say(ids, "unresumable", "recovery pending from an earlier run; no resume is offered")
		return
	}
	r.say(ids, "status", fmt.Sprintf("state=%s reason=%s model_calls=%d tool_dispatches=%d", v.State, v.Reason, v.ModelCalls, v.ToolDispatches))
}

func (r *runner) activeSelected() *turnState {
	for _, t := range r.turns {
		if t.ids.context == r.selected.context && !t.ended {
			return t
		}
	}
	return nil
}

func (r *runner) interrupt() {
	if t := r.activeSelected(); t != nil {
		r.say(t.ids, "interrupt", "cancellation requested")
		r.queue(t.ids, session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: t.ids.context, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: t.ids.turn}})
		return
	}
	r.say(lineIDs{}, "interrupt", "idle prompt; detaching")
	r.exit = true
}

// pump pages the agent's durable events and waits on Wake, never polling.
func (r *runner) pump(a *Agent, cursor uint64) {
	defer r.wg.Done()
	for {
		page, err := a.Session.Since(r.ctx, cursor, 128)
		if err != nil {
			if r.ctx.Err() == nil {
				r.post(func() { r.say(lineIDs{agent: a.AgentID}, "refused:", "event stream "+r.describe(err)) })
			}
			return
		}
		if events := page.Events; len(events) != 0 && !r.post(func() {
			for _, e := range events {
				r.event(e)
			}
		}) {
			return
		}
		if page.ThroughSequence == cursor {
			select {
			case <-page.Wake:
			case <-r.ctx.Done():
				return
			}
		}
		cursor = page.ThroughSequence
	}
}

func (r *runner) event(e session.Event) {
	ids := lineIDs{agent: e.AgentID, context: e.ContextID, turn: e.TurnID}
	switch v := e.Data.(type) {
	case ledger.RuntimeTurnBound:
		r.track(ids)
		r.say(ids, "event", e.Type+" "+e.TurnID.String())
	case ledger.RuntimeToolStarted:
		r.say(ids, "event", e.Type+" "+v.ToolName+" "+v.Operation)
	case ledger.RuntimeToolResult:
		r.say(ids, "event", e.Type+" "+v.Outcome)
	case ledger.ObjectRef:
		r.say(ids, "event", fmt.Sprintf("%s %d bytes", e.Type, v.Bytes))
	case ledger.RuntimePaidResult:
		text := e.Type + " " + v.Kind
		if v.CostMicros != nil {
			text += fmt.Sprintf(" cost_micros=%d", *v.CostMicros)
		}
		r.say(ids, "event", text)
	case ledger.RuntimeTurnResult:
		r.say(ids, "event", fmt.Sprintf("%s outcome=%s reason=%s model_calls=%d tool_dispatches=%d", e.Type, v.Outcome, v.Reason, v.ModelCalls, v.ToolDispatches))
		t := r.turns[ids.turn]
		if t == nil {
			t = &turnState{ids: ids, liveDone: true}
			r.turns[ids.turn] = t
		}
		t.ended, t.outcome = true, v.Outcome
		r.settle(t)
	default:
		r.say(ids, "event", e.Type)
	}
}

// track attaches live text once per turn; the turn keeps its bound IDs.
func (r *runner) track(ids lineIDs) {
	if r.turns[ids.turn] != nil {
		return
	}
	r.turns[ids.turn] = &turnState{ids: ids}
	r.wg.Add(1)
	go r.live(r.agents[ids.agent].Session, ids)
}

// live forwards provisional text. A slow view reattaches and prints only the
// suffix beyond the bytes it already displayed.
func (r *runner) live(s session.Session, ids lineIDs) {
	defer r.wg.Done()
	shown := 0
	for {
		snapshot, sub, err := s.LiveText(r.ctx, ids.turn)
		if err != nil {
			r.post(func() { r.liveDone(ids, err) })
			return
		}
		if len(snapshot.Text) > shown {
			text := snapshot.Text[shown:]
			shown = len(snapshot.Text)
			if !r.post(func() { r.liveText(ids, text) }) {
				return
			}
		}
		if sub == nil {
			r.post(func() { r.liveDone(ids, nil) })
			return
		}
		for delta := range sub.Deltas {
			text := delta.Text
			shown += len(text)
			if !r.post(func() { r.liveText(ids, text) }) {
				sub.Close()
				return
			}
		}
		err = sub.Err()
		sub.Close()
		if !errors.Is(err, session.ErrReattachRequired) {
			r.post(func() { r.liveDone(ids, err) })
			return
		}
	}
}

func (r *runner) liveText(ids lineIDs, text string) {
	t := r.turns[ids.turn]
	if t == nil {
		return
	}
	t.live = append(t.live, text...)
	for {
		end := bytes.IndexByte(t.live, '\n')
		if end < 0 && len(t.live) > liveLineBytes {
			// A long unbroken line is shown in rune-aligned pieces.
			end = liveLineBytes
			for end > 0 && !utf8.RuneStart(t.live[end]) {
				end--
			}
		}
		if end < 0 {
			return
		}
		r.say(ids, "live (provisional):", string(t.live[:end]))
		if end < len(t.live) && t.live[end] == '\n' {
			end++
		}
		t.live = t.live[end:]
	}
}

func (r *runner) liveDone(ids lineIDs, err error) {
	t := r.turns[ids.turn]
	if t == nil {
		return
	}
	if len(t.live) > 0 {
		r.say(ids, "live (provisional):", string(t.live))
		t.live = nil
	}
	if err != nil && !errors.Is(err, session.ErrFinished) && !errors.Is(err, session.ErrClosed) {
		r.say(ids, "live (provisional)", "stopped: "+r.describe(err))
	}
	t.liveDone = true
	r.settle(t)
}

// settle prints the durable answer only after both the recorded ending and
// the end of provisional text, so the two never interleave for one turn.
func (r *runner) settle(t *turnState) {
	if !t.ended || !t.liveDone || t.finalAsked {
		return
	}
	t.finalAsked = true
	if t.outcome != "finished" {
		r.say(t.ids, "final (durable)", "none: the turn ended "+t.outcome)
		delete(r.turns, t.ids.turn)
		r.resumeInput()
		return
	}
	ids, s := t.ids, r.agents[t.ids.agent].Session
	r.finals++
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		text, err := s.FinalText(r.ctx, ids.turn)
		r.post(func() {
			if err != nil {
				r.say(ids, "final (durable)", "unavailable: "+r.describe(err))
			} else {
				r.say(ids, "final (durable):", text)
			}
			delete(r.turns, ids.turn)
			r.finals--
			r.resumeInput()
		})
	}()
}

// resumeInput restarts paused interactive input once the selected context
// has no active turn and that turn's answer is displayed.
func (r *runner) resumeInput() {
	if r.paused && r.activeSelected() == nil {
		r.paused = false
		select {
		case r.resume <- struct{}{}:
		default:
		}
	}
}
