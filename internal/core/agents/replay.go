package agents

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type privateGroup struct {
	ids             submissionIDs
	command         ledger.Command
	receipt         ledger.RuntimeCommandReceipt
	registration    *ledger.RuntimeAgentRegistered
	kind            string
	revisions       []ledger.PrivateRevision
	revisionIndexes []int
	result          Receipt
}

// decodeGroup checks the complete envelope, never a filtered F3 subsequence.
// Both the producer preflight and every replay path use these rules.
func decodeGroup(d []ledger.Draft, first uint64) (*privateGroup, error) {
	found := false
	for _, r := range d {
		found = found || isPrivateType(r.Type)
	}
	if !found {
		return nil, nil
	}
	bad := func(reason string) (*privateGroup, error) { return nil, inconsistent(first, reason) }
	if len(d) != 3 && len(d) != 4 && len(d) != 6 {
		return bad("private group length")
	}
	want := []string{ledger.TypeCommand, ledger.TypePrivateRevision, ledger.TypeRuntimeCommandReceipt}
	if len(d) == 4 {
		want = []string{ledger.TypeCommand, ledger.TypeRuntimeContextCreated, ledger.TypePrivateRevision, ledger.TypeRuntimeCommandReceipt}
	}
	if len(d) == 6 {
		want = []string{ledger.TypeCommand, ledger.TypeRuntimeAgentRegistered, ledger.TypeRuntimeContextCreated, ledger.TypePrivateRevision, ledger.TypePrivateRevision, ledger.TypeRuntimeCommandReceipt}
	}
	ids := submissionIDs{d[0].AgentID, d[0].ExecutionSessionID, d[0].ContextID}
	if !validID(ids.agent) || !validID(ids.session) || !validID(ids.context) {
		return bad("private identity")
	}
	command, err := ledger.DecodeCommand(d[0].Payload)
	if err != nil {
		return bad("command payload")
	}
	g := &privateGroup{ids: ids, command: command}
	for i, r := range d {
		actor := ledger.ActorOwner
		if r.Type == ledger.TypePrivateRevision {
			actor = ledger.ActorCore
		}
		if r.Type != want[i] || r.TypeVersion != 1 || r.Actor != actor || r.AgentID != ids.agent || r.ExecutionSessionID != ids.session || r.ContextID != ids.context || r.OperationID != command.CommandID || r.TurnID != (uuid.UUID{}) || r.MeasurementPath != "" || len(r.BodyRefs) != 0 {
			return bad("private group envelope or order")
		}
		switch r.Type {
		case ledger.TypeRuntimeAgentRegistered:
			p, e := ledger.DecodeRuntimeAgentRegistered(r.Payload)
			if e != nil || p.GeneralContextID != ids.context {
				return bad("registration payload or context")
			}
			g.registration = &p
		case ledger.TypeRuntimeContextCreated:
			p, e := ledger.DecodeRuntimeContextCreated(r.Payload)
			if e != nil {
				return bad("context payload")
			}
			g.kind = p.Kind
		case ledger.TypePrivateRevision:
			p, e := ledger.DecodePrivateRevision(r.Payload)
			if e != nil {
				return bad("revision payload")
			}
			g.revisions = append(g.revisions, p)
			g.revisionIndexes = append(g.revisionIndexes, i)
		case ledger.TypeRuntimeCommandReceipt:
			p, e := ledger.DecodeRuntimeCommandReceipt(r.Payload)
			if e != nil {
				return bad("receipt payload")
			}
			g.receipt = p
		}
	}
	r := g.receipt
	if r.CommandID != command.CommandID || r.SubmissionSHA256 != command.SubmissionSHA256 || r.Outcome != ledger.RuntimeOutcomeApplied || r.RequestID != (uuid.UUID{}) {
		return bad("receipt command binding or outcome")
	}
	var canonical ledger.Digest
	switch len(d) {
	case 6:
		if g.kind != "general" || r.Operation != "agent_register" || g.revisions[0].Scope != "agent" || g.revisions[1].Scope != "context" || g.revisions[0].ExpectedVersion != 0 || g.revisions[1].ExpectedVersion != 0 {
			return bad("registration initialization")
		}
		initial := g.revisions[0].Changes[0]
		_, canonical, err = encodeSubmission(ids, "agent_register", canonicalTarget{Scope: "agent", Slots: []string{}}, 0, registrationArguments{g.registration.Directory, initial.SHA256, initial.Bytes})
	case 4:
		if g.kind != "work" || r.Operation != "context_create" || g.revisions[0].Scope != "context" || g.revisions[0].ExpectedVersion != 0 {
			return bad("context initialization")
		}
		_, canonical, err = encodeCreation(ids, g.kind)
	case 3:
		rev := g.revisions[0]
		slots := make([]string, len(rev.Changes))
		args := writeArguments{Changes: make([]canonicalChange, len(rev.Changes))}
		for i, c := range rev.Changes {
			slots[i] = c.Slot
			args.Changes[i] = canonicalChange{Slot: c.Slot, Mode: c.Mode, SHA256: c.SHA256}
			if c.Mode != "clear" {
				n := c.Bytes
				args.Changes[i].Bytes = &n
			}
		}
		if rev.ExpectedVersion == 0 || r.Operation != "private_write" || r.Target.Scope != rev.Scope || r.ExpectedVersion != rev.ExpectedVersion || !slices.Equal(r.Target.Slots, slots) {
			return bad("write receipt target or expectation")
		}
		_, canonical, err = encodeSubmission(ids, "private_write", canonicalTarget{Scope: rev.Scope, Slots: slots}, uint64(rev.ExpectedVersion), args)
	}
	if err != nil || canonical != command.SubmissionSHA256 {
		return bad("canonical submission digest")
	}
	g.result = Receipt{Binding: Binding{ids.agent, ids.context}, SessionID: ids.session, CommandID: command.CommandID, SubmissionSHA256: command.SubmissionSHA256, Operation: r.Operation, Target: ledger.PrivateTarget{Scope: r.Target.Scope, Slots: slices.Clone(r.Target.Slots)}, ExpectedVersion: uint64(r.ExpectedVersion), Sequence: first + uint64(len(d)) - 1}
	for _, rev := range g.revisions {
		if rev.Scope == "context" {
			g.result.Revision = uint64(rev.Revision)
		} else {
			g.result.AgentRevision = uint64(rev.Revision)
			if len(g.revisions) == 1 {
				g.result.Revision = uint64(rev.Revision)
			}
		}
	}
	return g, nil
}

// planGroup returns only changed scope copies. A failed preflight cannot leak
// a partial multi-slot revision or an identity into the live projection.
func (p *projection) planGroup(g *privateGroup, first uint64) (map[scopeKey]*scopeState, error) {
	ids := g.ids
	a, exists := p.agents[ids.agent]
	if g.registration != nil {
		if exists || p.sessions[ids.session] != (uuid.UUID{}) || p.contexts[ids.context] != (uuid.UUID{}) {
			return nil, inconsistent(first, "reused registration identity")
		}
		names := make([]string, 0, len(p.agents))
		for _, a := range p.agents {
			names = append(names, a.directory)
		}
		if err := validateBasename(g.registration.Directory, names); err != nil {
			return nil, inconsistent(first, "reused directory")
		}
	} else {
		if !exists || a.session != ids.session {
			return nil, inconsistent(first, "agent session binding")
		}
		owner, exists := p.contexts[ids.context]
		if g.kind != "" {
			if exists {
				return nil, inconsistent(first, "reused context")
			}
		} else if !exists || owner != ids.agent {
			return nil, inconsistent(first, "context binding")
		}
	}
	return p.planPrivateRevisions(ids, g.revisions, g.revisionIndexes, first)
}

func (p *projection) planPrivateRevisions(ids submissionIDs, revisions []ledger.PrivateRevision, indexes []int, first uint64) (map[scopeKey]*scopeState, error) {
	next := map[scopeKey]*scopeState{}
	for i, rev := range revisions {
		k := keyFor(ids, rev.Scope)
		old := p.scopes[k]
		current := uint64(0)
		if old != nil {
			current = old.revision
		}
		if current != uint64(rev.ExpectedVersion) {
			return nil, &RevisionError{Expected: uint64(rev.ExpectedVersion), Current: current}
		}
		st := &scopeState{ids: ids, heads: map[string]Head{}, materials: map[string]string{}}
		if old != nil {
			*st = *old
			st.heads = maps.Clone(old.heads)
			st.materials = maps.Clone(old.materials)
			st.materialized = maps.Clone(old.materialized)
			st.issues = slices.Clone(old.issues)
		}
		for _, c := range rev.Changes {
			if rev.ExpectedVersion == 0 && c.Slot != "instructions" {
				empty, _ := hashObject(nil)
				if c.Bytes != 0 || c.SHA256 != empty {
					return nil, inconsistent(first, "non-empty initial private document")
				}
			}
			if c.Bytes > maxObjectBytes {
				return nil, inconsistent(first, "unsupported object size")
			}
			if c.Slot == "transcript" && c.PreviousSHA256 != st.heads["transcript"].SHA256 {
				return nil, inconsistent(first, "transcript predecessor")
			}
			if c.Mode == "clear" {
				delete(st.heads, c.Slot)
			} else {
				st.heads[c.Slot] = Head{c.SHA256, c.Bytes, uint64(rev.Revision), first + uint64(indexes[i])}
			}
			if markdownSlot(c.Slot) {
				st.materials[c.Slot] = string(materializationStale)
			}
		}
		st.revision = uint64(rev.Revision)
		next[k] = st
	}
	return next, nil
}
func (p *projection) install(g *privateGroup, next map[scopeKey]*scopeState) {
	if g.registration != nil {
		p.agents[g.ids.agent] = agentState{g.ids.session, g.ids.context, g.registration.Directory}
		p.sessions[g.ids.session] = g.ids.agent
	}
	if g.kind != "" {
		p.contexts[g.ids.context] = g.ids.agent
	}
	for k, st := range next {
		p.scopes[k] = st
	}
}
func draftsOf(c ledger.Commit) []ledger.Draft {
	d := make([]ledger.Draft, len(c.Records))
	for i, r := range c.Records {
		d[i] = r.Draft
	}
	return d
}
func (s *Service) scan(ctx context.Context, after, through uint64, visit func(ledger.Commit) error) error {
	_, err := s.readRange(ctx, after, through, s.history.checkpoint(after), visit)
	return err
}

// readRange is the shared numeric, checked-close reader. Its returned token is
// not published here: the consumer must also finish its own acceptance.
func (s *Service) readRange(ctx context.Context, after, through uint64, checkpoint *ledger.ReadCheckpoint, visit func(ledger.Commit) error) (*ledger.ReadCheckpoint, error) {
	if s.deps.beforeRangeRead != nil {
		s.deps.beforeRangeRead(after, through)
	}
	r, err := ledger.OpenReaderWithCheckpoint(s.store.LogDir(), ledger.ReadOptions{Colony: s.store.Colony(), After: after, Through: through, Consume: privateConsumption(), AllTypes: true}, checkpoint)
	if err != nil {
		return nil, err
	}
	if err := scanReader(ctx, r, visit); err != nil {
		if errors.Is(err, ledger.ErrCorrupt) {
			s.history.discard(historyKey{kind: historyCheckpoint, sequence: after})
		}
		return nil, err
	}
	return r.Checkpoint(), nil
}

func scanReader(ctx context.Context, r *ledger.Reader, visit func(ledger.Commit) error) (err error) {
	defer func() { err = errors.Join(err, r.Close()) }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c, err := r.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := visit(c); err != nil {
			return err
		}
	}
}

// refreshLocked returns with mu held on every path. Only the performing
// caller owns a Reader; waiters resume at the installed frontier.
func (s *Service) refreshLocked(ctx context.Context, reads *historyReadSet, through uint64) error {
	for {
		if err := s.usableLocked(ctx); err != nil {
			return err
		}
		if s.refreshDone == nil {
			if through <= s.p.through {
				return s.rebuildDirectoryLocked(ctx)
			}
			break
		}
		done, closed := s.refreshDone, s.closeSignal
		s.mu.Unlock()
		if s.deps.beforeRefreshWait != nil {
			s.deps.beforeRefreshWait()
		}
		select {
		case <-done:
		case <-closed:
		case <-ctx.Done():
		}
		s.mu.Lock()
		if err := s.usableLocked(ctx); err != nil {
			return err
		}
		if through <= s.p.through {
			return s.rebuildDirectoryLocked(ctx)
		}
	}
	after := s.p.through
	checkpoint := s.history.checkpoint(after)
	s.refreshDone = make(chan struct{})
	// Some internal callers are not public entrants. Close must still drain
	// the performing read before releasing the history and runtime.
	s.active.Add(1)
	s.mu.Unlock()
	checkpoint, err := s.readRange(ctx, after, through, checkpoint, func(c ledger.Commit) error {
		if s.deps.beforeRefreshInstall != nil {
			s.deps.beforeRefreshInstall(c)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.usableLocked(ctx); err != nil {
			return err
		}
		if c.First <= s.p.through {
			return nil
		}
		if c.First != s.p.through+1 {
			return inconsistent(c.First, "noncontiguous refresh")
		}
		return s.prepareCommitLocked(ctx, reads, c)
	})
	s.mu.Lock()
	if err == nil {
		err = s.usableLocked(ctx)
	}
	if err == nil {
		s.history.putCheckpoint(through, checkpoint)
	} else if !errors.Is(err, ErrClosed) && !errors.Is(err, ErrPoisoned) && (ctx.Err() == nil || !errors.Is(err, ctx.Err())) {
		s.poisoned = true
	}
	close(s.refreshDone)
	s.refreshDone = nil
	s.active.Done()
	return err
}

// Rebuilding is independent of the refresh flight. Other callers keep using
// legacy scans while this reader constructs a replacement from ledger bytes.
func (s *Service) rebuildDirectoryLocked(ctx context.Context) error {
	through := s.p.through
	generation, needed := s.history.directoryState(through)
	if !needed || s.rebuilding {
		return nil
	}
	s.rebuilding = true
	s.active.Add(1)
	s.mu.Unlock()
	directory := newCommandDirectory(0)
	_, err := s.readRange(ctx, 0, through, nil, func(c ledger.Commit) error {
		directory.advance(c, directory.generation+1)
		return nil
	})
	s.mu.Lock()
	s.rebuilding = false
	s.active.Done()
	if usable := s.usableLocked(ctx); usable != nil {
		return usable
	}
	if err == nil && s.p.through == through {
		s.history.installDirectory(directory, through, generation)
	}
	// Failed acceleration never clears a projection or adds a refusal. The
	// subsequent authoritative lookup still takes the checked legacy path.
	return nil
}

// acknowledgeLocked preserves the successful submitter's lock through its
// caller-owned completion. It must not wait for an unlocked refresh, which
// may itself be waiting to install under mu.
func (s *Service) acknowledgeLocked(through uint64) error {
	if through == s.p.through {
		return nil
	}
	ctx := context.Background()
	checkpoint, err := s.readRange(ctx, s.p.through, through, s.history.checkpoint(s.p.through), func(c ledger.Commit) error {
		if c.First != s.p.through+1 {
			return inconsistent(c.First, "noncontiguous acknowledgement")
		}
		_, err := s.acceptCommitLocked(ctx, c, nil)
		return err
	})
	if err != nil {
		s.poisoned = true
		return err
	}
	s.history.putCheckpoint(through, checkpoint)
	return nil
}

// readCommittedRange requires one complete authoritative commit, including a
// successful reader close. A Submit receipt is a range, not decoded effects.
func (s *Service) readCommittedRange(ctx context.Context, first, last uint64, checkpoint *ledger.ReadCheckpoint) (ledger.Commit, *ledger.ReadCheckpoint, error) {
	if first == 0 || last < first {
		return ledger.Commit{}, nil, inconsistent(first, "committed range")
	}
	var accepted ledger.Commit
	checkpoint, err := s.readRange(ctx, first-1, last, checkpoint, func(c ledger.Commit) error {
		if accepted.First != 0 || c.First != first || c.Last != last {
			return inconsistent(first, "complete commit range")
		}
		accepted = c
		return nil
	})
	if err != nil {
		return ledger.Commit{}, nil, err
	}
	if accepted.First == 0 {
		return ledger.Commit{}, nil, inconsistent(first, "missing committed range")
	}
	return accepted, checkpoint, nil
}

// The durable acknowledgement path never suspends: its caller's completion
// must stay atomic with installation. Refresh uses prepareCommitLocked instead.
func (s *Service) acceptCommitLocked(ctx context.Context, c ledger.Commit, expected *scopedInput) (commandMatch, error) {
	match, install, err := s.stageCommitLocked(ctx, nil, c, expected)
	if err == nil {
		install()
	}
	return match, err
}

func (s *Service) prepareCommitLocked(ctx context.Context, reads *historyReadSet, c ledger.Commit) error {
	var install func()
	err := s.prepareHistoryLocked(ctx, reads, nil, false, func(reads *historyReadSet) error {
		install = nil
		if c.Last <= s.p.through {
			return nil
		}
		if c.First != s.p.through+1 {
			return inconsistent(c.First, "noncontiguous refresh")
		}
		var err error
		_, install, err = s.stageCommitLocked(ctx, reads, c, nil)
		return err
	})
	if err == nil && install != nil {
		install()
	}
	return err
}

// No installer exists until all decoding and planning succeeds. On a need the
// caller discards every local plan and reruns this function from its top.
func (s *Service) stageCommitLocked(ctx context.Context, reads *historyReadSet, c ledger.Commit, expected *scopedInput) (commandMatch, func(), error) {
	d := draftsOf(c)
	g, scoped, review, paid, registration, loop, err := decodeFamily(d, c.First)
	if err != nil {
		return commandMatch{}, nil, err
	}
	match := commandMatch{group: g, scoped: scoped, review: review, paid: paid, registration: registration, loop: loop}
	switch {
	case g != nil:
		match.command, match.found = g.command, true
	case scoped != nil:
		match.command, match.found = scoped.command, true
	case review != nil:
		match.command, match.found = review.command, true
	case paid != nil:
		match.command, match.found = paid.command, true
	case registration != nil:
		match.command, match.found = registration.command, true
	case loop != nil:
		match.command, match.found = loop.command, true
	}
	if expected != nil {
		if !match.found || match.command.CommandID != expected.id || match.scoped == nil {
			return commandMatch{}, nil, inconsistent(c.First, "acknowledgement command or family")
		}
		if _, err := matchScopedReceipt(match, *expected); err != nil {
			return commandMatch{}, nil, err
		}
	}
	if match.found {
		used, err := s.commandUsed(ctx, reads, match.command.CommandID, c.First-1)
		if err != nil {
			return commandMatch{}, nil, err
		}
		if used {
			return commandMatch{}, nil, inconsistent(c.First, "reused command ID")
		}
	}
	if match.found {
		if err := s.loopEffectAvailable(ctx, reads, match.command.CommandID, c.First-1); err != nil {
			return commandMatch{}, nil, err
		}
	}
	var privateNext map[scopeKey]*scopeState
	var scopedNext scopedPlan
	var reviewNext reviewPlan
	var paidNext paidPlan
	var registrationNext registrationPlan
	var loopNext loopPlan
	if g != nil {
		privateNext, err = s.p.planGroup(g, c.First)
		if err != nil {
			return commandMatch{}, nil, inconsistent(c.First, "invalid private transition")
		}
	}
	if scoped != nil {
		scopedNext, err = s.planScoped(ctx, reads, scoped, c.First)
		if err != nil {
			if isHistoryNeed(err) || ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return commandMatch{}, nil, err
			}
			return commandMatch{}, nil, inconsistent(c.First, "invalid scoped transition: "+err.Error())
		}
	}
	if review != nil {
		reviewNext, err = s.planReview(ctx, reads, review, c.First)
		if err != nil {
			if isHistoryNeed(err) || ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return commandMatch{}, nil, err
			}
			return commandMatch{}, nil, inconsistent(c.First, "invalid review transition: "+err.Error())
		}
	}
	if paid != nil {
		paidNext, err = s.planPaid(ctx, reads, paid, c.First)
		if err != nil {
			if isHistoryNeed(err) || ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return commandMatch{}, nil, err
			}
			return commandMatch{}, nil, inconsistent(c.First, "invalid paid transition: "+err.Error())
		}
	}
	if registration != nil {
		registrationNext, err = s.planRegistration(ctx, reads, registration, c.First)
		if err != nil {
			if isHistoryNeed(err) || ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return commandMatch{}, nil, err
			}
			return commandMatch{}, nil, inconsistent(c.First, "invalid registration transition: "+err.Error())
		}
	}
	if loop != nil {
		loopNext, err = s.planLoop(ctx, reads, loop, c.First)
		if err != nil {
			if isHistoryNeed(err) || ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return commandMatch{}, nil, err
			}
			return commandMatch{}, nil, inconsistent(c.First, "invalid loop transition")
		}
	}
	// Stage the shared envelope effects too: a later invalid closure in an
	// otherwise unconsumed group must not publish an earlier scratch or maximum.
	maxCommand := s.p.maxCommand
	var scratches map[uuid.UUID]scratchState
	for _, r := range d {
		switch r.Type {
		case ledger.TypeCommand:
			p, err := ledger.DecodeCommand(r.Payload)
			if err != nil {
				return commandMatch{}, nil, err
			}
			if bytes.Compare(p.CommandID[:], maxCommand[:]) > 0 {
				maxCommand = p.CommandID
			}
		case ledger.TypeScratchCreated:
			p, err := ledger.DecodeScratchCreated(r.Payload)
			if err != nil {
				return commandMatch{}, nil, err
			}
			if p.ContextID != r.ContextID {
				return commandMatch{}, nil, inconsistent(c.First, "scratch context")
			}
			if scratches == nil {
				scratches = make(map[uuid.UUID]scratchState)
			}
			scratches[p.ScratchID] = scratchState{ScratchCreated: p, sequence: c.Last}
		case ledger.TypeScratchClosed:
			p, err := ledger.DecodeScratchClosed(r.Payload)
			if err != nil {
				return commandMatch{}, nil, err
			}
			old, ok := scratches[p.ScratchID]
			if !ok {
				old, ok = s.p.scratches[p.ScratchID]
			}
			if !ok || old.ContextID != p.ContextID || (r.Actor != ledger.ActorOwner && (r.Actor != ledger.ActorAgent || p.ContextID != r.ContextID)) {
				return commandMatch{}, nil, inconsistent(c.First, "scratch closure")
			}
			old.closed = true
			if scratches == nil {
				scratches = make(map[uuid.UUID]scratchState)
			}
			scratches[p.ScratchID] = old
		}
	}
	return match, func() {
		if g != nil {
			s.p.install(g, privateNext)
		}
		if scoped != nil {
			s.p.installScoped(scopedNext)
		}
		if review != nil {
			s.p.installReview(reviewNext)
		}
		if paid != nil {
			s.p.installPaid(paidNext)
		}
		if registration != nil {
			s.p.installRegistration(registrationNext)
		}
		if loop != nil {
			s.p.installLoop(loopNext)
		}
		s.p.maxCommand = maxCommand
		maps.Copy(s.p.scratches, scratches)
		for _, record := range c.Records {
			s.p.reviewFloor = max(s.p.reviewFloor, record.EventTimeNS)
		}
		s.publishCommitLocked(c, g, scoped)
		s.p.through = c.Last
		s.loop.notify()
	}, nil
}

type commandMatch struct {
	found        bool
	command      ledger.Command
	group        *privateGroup
	scoped       *scopedGroup
	review       *reviewGroup
	paid         *paidGroup
	registration *registrationGroup
	loop         *loopGroup
}

func (s *Service) findCommand(ctx context.Context, reads *historyReadSet, id uuid.UUID, after, through uint64) (commandMatch, error) {
	if err := ctx.Err(); err != nil {
		return commandMatch{}, err
	}
	rows, hit, err := s.history.occurrences(directoryKey{kind: directoryCommand, id: id}, after, through)
	if err != nil {
		return commandMatch{}, err
	}
	if hit {
		if len(rows) == 0 {
			if s.deps.beforeHistoryFill != nil {
				s.deps.beforeHistoryFill(historyKey{kind: historyCommand, id: id})
			}
			return commandMatch{}, nil
		}
		if len(rows) > 1 {
			return commandMatch{}, inconsistent(rows[1].first, "duplicate command")
		}
		if match, cached := s.history.command(id, after, through); cached && match.found {
			return match, nil
		}
		commit, err := s.directoryCommit(ctx, reads, rows[0], through)
		if err != nil {
			return commandMatch{}, err
		}
		return s.commandFromCommit(ctx, reads, id, after, through, commit)
	}
	return s.findCommandLegacy(ctx, reads, id, after, through)
}

func (s *Service) findCommandLegacy(ctx context.Context, reads *historyReadSet, id uuid.UUID, after, through uint64) (commandMatch, error) {
	value, err := readHistory(ctx, reads, after, through, commandHistoryKey{id}, func(ctx context.Context) (historyCommitValue, error) {
		var matched ledger.Commit
		err := s.scan(ctx, after, through, func(c ledger.Commit) error {
			for _, r := range c.Records {
				if r.Type != ledger.TypeCommand {
					continue
				}
				cmd, err := ledger.DecodeCommand(r.Payload)
				if err != nil {
					return err
				}
				if cmd.CommandID != id {
					continue
				}
				if matched.First != 0 {
					return inconsistent(c.First, "duplicate command")
				}
				if _, _, _, _, _, _, err := decodeFamily(draftsOf(c), c.First); err != nil {
					return err
				}
				matched = c
			}
			return nil
		})
		if err == nil && s.deps.beforeHistoryFill != nil {
			s.deps.beforeHistoryFill(historyKey{kind: historyCommand, id: id})
		}
		return encodeHistoryCommit(matched), err
	})
	if err != nil {
		return commandMatch{}, err
	}
	if value.First == 0 {
		s.history.putCommand(id, after, through, commandMatch{}, ledger.Commit{})
		return commandMatch{}, nil
	}
	commit, err := decodeHistoryCommit(value)
	if err != nil {
		return commandMatch{}, err
	}
	return s.commandFromCommit(ctx, reads, id, after, through, commit)
}

func (s *Service) commandFromCommit(ctx context.Context, reads *historyReadSet, id uuid.UUID, after, through uint64, c ledger.Commit) (commandMatch, error) {
	for _, r := range c.Records {
		if r.Type != ledger.TypeCommand {
			continue
		}
		cmd, err := ledger.DecodeCommand(r.Payload)
		if err != nil {
			return commandMatch{}, err
		}
		if cmd.CommandID != id {
			continue
		}
		g, scoped, review, paid, registration, loop, err := decodeFamily(draftsOf(c), c.First)
		if err != nil {
			return commandMatch{}, err
		}
		match := commandMatch{found: true, command: cmd, group: g, scoped: scoped, review: review, paid: paid, registration: registration, loop: loop}
		if s.deps.beforeHistoryFill != nil {
			s.deps.beforeHistoryFill(historyKey{kind: historyCommand, id: id})
		}
		s.history.putCommand(id, after, through, match, c)
		return match, nil
	}
	// A damaged position cannot manufacture absence.
	return s.findCommandLegacy(ctx, reads, id, after, through)
}

func (s *Service) commandUsed(ctx context.Context, reads *historyReadSet, id uuid.UUID, through uint64) (bool, error) {
	if _, found, hit := s.history.membership(directoryKey{kind: directoryCommand, id: id}, through); hit {
		return found, nil
	}
	if bytes.Compare(id[:], s.p.maxCommand[:]) > 0 {
		return false, nil
	}
	old, err := s.findCommandLegacy(ctx, reads, id, 0, through)
	return old.found, err
}

// Both accepted publication paths call this before exposing p.through. The
// commit contains authoritative envelopes, not the preflight's draft positions.
func (s *Service) publishCommitLocked(c ledger.Commit, g *privateGroup, scoped *scopedGroup) {
	s.p.publishHistory(g, scoped, c.Last)
	s.history.advance(s.p.through, c)
	s.history.warmSources(c)
}

// directoryCommit shares the existing charged source LRU. A partial payload
// hit needs only one containing-commit read; no index mutex spans that read.
func (s *Service) directoryCommit(ctx context.Context, reads *historyReadSet, row directoryRow, through uint64) (ledger.Commit, error) {
	if err := ctx.Err(); err != nil {
		return ledger.Commit{}, err
	}
	c := ledger.Commit{First: row.first, Last: row.last}
	for seq := row.first; seq <= row.last; seq++ {
		record, hit := s.history.acceptedRecord(int64(seq), through, s.store.Colony())
		if !hit {
			first, last := row.first, row.last
			checkpoint := s.history.checkpoint(first - 1)
			value, err := readHistory(ctx, reads, first-1, last, commitHistoryKey{first, last}, func(ctx context.Context) (historyCommitValue, error) {
				accepted, _, err := s.readCommittedRange(ctx, first, last, checkpoint)
				return encodeHistoryCommit(accepted), err
			})
			if err != nil {
				return ledger.Commit{}, err
			}
			accepted, err := decodeHistoryCommit(value)
			if err == nil {
				s.history.warmSources(accepted)
			}
			return accepted, err
		}
		c.Records = append(c.Records, record)
	}
	return c, nil
}

// Selected consumers keep their existing visitor, validation and lock scope.
// Missing, stale or damaged metadata always uses the original range scan.
func (s *Service) scanDirectory(ctx context.Context, key directoryKey, after, through uint64, visit func(ledger.Commit) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rows, hit, err := s.history.occurrences(key, after, through)
	if err != nil {
		return err
	}
	if !hit {
		return s.scan(ctx, after, through, visit)
	}
	var last uint64
	for _, row := range rows {
		if row.last == last {
			continue
		}
		c, err := s.directoryCommit(ctx, nil, row, through)
		if err != nil {
			return err
		}
		if err := visit(c); err != nil {
			return err
		}
		last = row.last
	}
	return nil
}
func matchReceipt(m commandMatch, ids submissionIDs, digest ledger.Digest) (Receipt, error) {
	if m.group != nil && m.group.ids != ids {
		return Receipt{}, ErrNotBound
	}
	if m.command.SubmissionSHA256 != digest {
		return Receipt{}, ErrCommandMismatch
	}
	if m.group == nil {
		return Receipt{}, ErrUnavailable
	}
	return m.group.result, nil
}
func markdownSlot(slot string) bool {
	return slot == "memory" || slot == "instructions" || slot == "handoff"
}
