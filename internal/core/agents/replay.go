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
func (s *Service) scan(ctx context.Context, after, through uint64, visit func(ledger.Commit) error) (err error) {
	r, err := ledger.OpenReader(s.store.LogDir(), ledger.ReadOptions{Colony: s.store.Colony(), After: after, Through: through, Consume: privateConsumption(), AllTypes: true})
	if err != nil {
		return err
	}
	return scanReader(ctx, r, visit)
}

func scanReader(ctx context.Context, r *ledger.Reader, visit func(ledger.Commit) error) (err error) {
	defer func() { err = errors.Join(err, r.Close()) }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c, err := r.Next()
		if errors.Is(err, io.EOF) {
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
func (s *Service) refreshLocked(ctx context.Context, through uint64) error {
	if through == s.p.through {
		return nil
	}
	err := s.scan(ctx, s.p.through, through, func(c ledger.Commit) error {
		_, err := s.acceptCommitLocked(ctx, c, nil)
		return err
	})
	if err != nil && (ctx.Err() == nil || !errors.Is(err, ctx.Err())) {
		s.poisoned = true
	}
	return err
}

// readCommittedRange requires one complete authoritative commit, including a
// successful reader close. A Submit receipt is a range, not decoded effects.
func (s *Service) readCommittedRange(ctx context.Context, first, last uint64, checkpoint *ledger.ReadCheckpoint) (ledger.Commit, *ledger.ReadCheckpoint, error) {
	if first == 0 || last < first {
		return ledger.Commit{}, nil, inconsistent(first, "acknowledgement range")
	}
	opts := ledger.ReadOptions{Colony: s.store.Colony(), After: first - 1, Through: last, Consume: privateConsumption(), AllTypes: true}
	var r *ledger.Reader
	var err error
	if checkpoint == nil {
		r, err = ledger.OpenReader(s.store.LogDir(), opts)
	} else {
		r, err = ledger.OpenReaderWithCheckpoint(s.store.LogDir(), opts, checkpoint)
	}
	if err != nil {
		return ledger.Commit{}, nil, err
	}
	var accepted ledger.Commit
	err = scanReader(ctx, r, func(c ledger.Commit) error {
		if accepted.First != 0 || c.First != first || c.Last != last {
			return inconsistent(first, "acknowledgement commit range")
		}
		accepted = c
		return nil
	})
	if err != nil {
		return ledger.Commit{}, nil, err
	}
	if accepted.First == 0 {
		return ledger.Commit{}, nil, inconsistent(first, "missing acknowledgement commit")
	}
	return accepted, r.Checkpoint(), nil
}

// acceptCommitLocked is shared by refresh and a contiguous scoped acknowledgement.
// expected binds the latter to its actual command before any effect is installed.
// It never turns that single occurrence into a global command-search proof.
func (s *Service) acceptCommitLocked(ctx context.Context, c ledger.Commit, expected *scopedInput) (commandMatch, error) {
	d := draftsOf(c)
	g, scoped, review, paid, registration, loop, err := decodeFamily(d, c.First)
	if err != nil {
		return commandMatch{}, err
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
			return commandMatch{}, inconsistent(c.First, "acknowledgement command or family")
		}
		if _, err := matchScopedReceipt(match, *expected); err != nil {
			return commandMatch{}, err
		}
	}
	if match.found && bytes.Compare(match.command.CommandID[:], s.p.maxCommand[:]) <= 0 {
		old, err := s.findCommand(ctx, match.command.CommandID, 0, c.First-1)
		if err != nil {
			return commandMatch{}, err
		}
		if old.found {
			return commandMatch{}, inconsistent(c.First, "reused command ID")
		}
	}
	if match.found {
		if err := s.loopEffectAvailable(ctx, match.command.CommandID, c.First-1); err != nil {
			return commandMatch{}, err
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
			return commandMatch{}, inconsistent(c.First, "invalid private transition")
		}
	}
	if scoped != nil {
		scopedNext, err = s.planScoped(ctx, scoped, c.First)
		if err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return commandMatch{}, err
			}
			return commandMatch{}, inconsistent(c.First, "invalid scoped transition: "+err.Error())
		}
	}
	if review != nil {
		reviewNext, err = s.planReview(ctx, review, c.First)
		if err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return commandMatch{}, err
			}
			return commandMatch{}, inconsistent(c.First, "invalid review transition: "+err.Error())
		}
	}
	if paid != nil {
		paidNext, err = s.planPaid(ctx, paid, c.First)
		if err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return commandMatch{}, err
			}
			return commandMatch{}, inconsistent(c.First, "invalid paid transition: "+err.Error())
		}
	}
	if registration != nil {
		registrationNext, err = s.planRegistration(ctx, registration, c.First)
		if err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return commandMatch{}, err
			}
			return commandMatch{}, inconsistent(c.First, "invalid registration transition: "+err.Error())
		}
	}
	if loop != nil {
		loopNext, err = s.planLoop(ctx, loop, c.First)
		if err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return commandMatch{}, err
			}
			return commandMatch{}, inconsistent(c.First, "invalid loop transition")
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
				return commandMatch{}, err
			}
			if bytes.Compare(p.CommandID[:], maxCommand[:]) > 0 {
				maxCommand = p.CommandID
			}
		case ledger.TypeScratchCreated:
			p, err := ledger.DecodeScratchCreated(r.Payload)
			if err != nil {
				return commandMatch{}, err
			}
			if p.ContextID != r.ContextID {
				return commandMatch{}, inconsistent(c.First, "scratch context")
			}
			if scratches == nil {
				scratches = make(map[uuid.UUID]scratchState)
			}
			scratches[p.ScratchID] = scratchState{ScratchCreated: p, sequence: c.Last}
		case ledger.TypeScratchClosed:
			p, err := ledger.DecodeScratchClosed(r.Payload)
			if err != nil {
				return commandMatch{}, err
			}
			old, ok := scratches[p.ScratchID]
			if !ok {
				old, ok = s.p.scratches[p.ScratchID]
			}
			if !ok || old.ContextID != p.ContextID || (r.Actor != ledger.ActorOwner && (r.Actor != ledger.ActorAgent || p.ContextID != r.ContextID)) {
				return commandMatch{}, inconsistent(c.First, "scratch closure")
			}
			old.closed = true
			if scratches == nil {
				scratches = make(map[uuid.UUID]scratchState)
			}
			scratches[p.ScratchID] = old
		}
	}
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
	s.p.publishHistory(g, scoped, c.Last)
	s.history.advance(s.p.through, c.Last, d)
	s.history.warmSources(c)
	s.p.through = c.Last
	s.loop.notify()
	return match, nil
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

func (s *Service) findCommand(ctx context.Context, id uuid.UUID, after, through uint64) (commandMatch, error) {
	if err := ctx.Err(); err != nil {
		return commandMatch{}, err
	}
	if match, hit := s.history.command(id, after, through); hit {
		return match, nil
	}
	var matched ledger.Commit
	var found commandMatch
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
			if found.found {
				return inconsistent(c.First, "duplicate command")
			}
			g, scoped, review, paid, registration, loop, err := decodeFamily(draftsOf(c), c.First)
			if err != nil {
				return err
			}
			found = commandMatch{found: true, command: cmd, group: g, scoped: scoped, review: review, paid: paid, registration: registration, loop: loop}
			matched = c
		}
		return nil
	})
	if err == nil {
		if s.deps.beforeHistoryFill != nil {
			s.deps.beforeHistoryFill(historyKey{kind: historyCommand, id: id})
		}
		s.history.putCommand(id, after, through, found, matched)
	}
	return found, err
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
