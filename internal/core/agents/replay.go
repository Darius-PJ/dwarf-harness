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
	result          CommandResult
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
	g.result = CommandResult{Binding: Binding{ids.agent, ids.context}, SessionID: ids.session, CommandID: command.CommandID, SubmissionSHA256: command.SubmissionSHA256, Operation: r.Operation, Target: ledger.PrivateTarget{Scope: r.Target.Scope, Slots: slices.Clone(r.Target.Slots)}, ExpectedVersion: uint64(r.ExpectedVersion), Sequence: first + uint64(len(d)) - 1}
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
	next := map[scopeKey]*scopeState{}
	for i, rev := range g.revisions {
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
				st.heads[c.Slot] = Head{c.SHA256, c.Bytes, uint64(rev.Revision), first + uint64(g.revisionIndexes[i])}
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
		d := draftsOf(c)
		g, err := decodeGroup(d, c.First)
		if err != nil {
			return err
		}
		if g != nil {
			if bytes.Compare(g.command.CommandID[:], s.p.maxCommand[:]) <= 0 {
				old, err := s.findCommand(ctx, g.command.CommandID, 0, c.First-1)
				if err != nil {
					return err
				}
				if old.found {
					return inconsistent(c.First, "reused command ID")
				}
			}
			next, err := s.p.planGroup(g, c.First)
			if err != nil {
				return inconsistent(c.First, "invalid private transition")
			}
			s.p.install(g, next)
		}
		for _, r := range d {
			switch r.Type {
			case ledger.TypeCommand:
				p, err := ledger.DecodeCommand(r.Payload)
				if err != nil {
					return err
				}
				if bytes.Compare(p.CommandID[:], s.p.maxCommand[:]) > 0 {
					s.p.maxCommand = p.CommandID
				}
			case ledger.TypeScratchCreated:
				p, err := ledger.DecodeScratchCreated(r.Payload)
				if err != nil {
					return err
				}
				if p.ContextID != r.ContextID {
					return inconsistent(c.First, "scratch context")
				}
				s.p.scratches[p.ScratchID] = scratchState{ScratchCreated: p, sequence: c.Last}
			case ledger.TypeScratchClosed:
				p, err := ledger.DecodeScratchClosed(r.Payload)
				if err != nil {
					return err
				}
				old, ok := s.p.scratches[p.ScratchID]
				if !ok || old.ContextID != p.ContextID || p.ContextID != r.ContextID {
					return inconsistent(c.First, "scratch closure")
				}
				old.closed = true
				s.p.scratches[p.ScratchID] = old
			}
		}
		s.p.through = c.Last
		return nil
	})
	if err != nil && (ctx.Err() == nil || !errors.Is(err, ctx.Err())) {
		s.poisoned = true
	}
	return err
}

type commandMatch struct {
	found   bool
	command ledger.Command
	group   *privateGroup
}

func (s *Service) findCommand(ctx context.Context, id uuid.UUID, after, through uint64) (commandMatch, error) {
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
			g, err := decodeGroup(draftsOf(c), c.First)
			if err != nil {
				return err
			}
			found = commandMatch{true, cmd, g}
		}
		return nil
	})
	return found, err
}
func matchReceipt(m commandMatch, ids submissionIDs, digest ledger.Digest) (CommandResult, error) {
	if m.group != nil && m.group.ids != ids {
		return CommandResult{}, ErrNotBound
	}
	if m.command.SubmissionSHA256 != digest {
		return CommandResult{}, ErrCommandMismatch
	}
	if m.group == nil {
		return CommandResult{}, ErrUnavailable
	}
	return m.group.result, nil
}
func markdownSlot(slot string) bool {
	return slot == "memory" || slot == "instructions" || slot == "handoff"
}
