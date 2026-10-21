package agents

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type privateOperation struct {
	ids                               submissionIDs
	id                                uuid.UUID
	digest                            ledger.Digest
	operation, scope, directory, kind string
	expected                          uint64
	changes                           []privateChange
}

func (s *Service) initialIDs(ctx context.Context, b Binding, creation bool) (submissionIDs, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usableLocked(ctx); err != nil {
		return submissionIDs{}, err
	}
	if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
		return submissionIDs{}, err
	}
	return s.bindingLocked(b, creation)
}
func (s *Service) RegisterAgent(ctx context.Context, r AgentRegistration) (Receipt, error) {
	if err := s.enter(ctx); err != nil {
		return Receipt{}, err
	}
	defer s.active.Done()
	ids := submissionIDs{r.AgentID, r.SessionID, r.ContextID}
	if !validID(r.CommandID) || r.ExpectedVersion != 0 {
		return Receipt{}, ErrInvalid
	}
	_, digest, err := encodeRegistration(ids, r.Directory, r.AgentMarkdown)
	if err != nil {
		return Receipt{}, err
	}
	return s.perform(ctx, privateOperation{ids: ids, id: r.CommandID, digest: digest, operation: "agent_register", scope: "agent", directory: r.Directory, changes: []privateChange{{slot: "instructions", mode: "replace", data: r.AgentMarkdown}, {slot: "memory", mode: "replace"}}})
}
func (s *Service) CreateContext(ctx context.Context, r ContextCreation) (Receipt, error) {
	if err := s.enter(ctx); err != nil {
		return Receipt{}, err
	}
	defer s.active.Done()
	ids, err := s.initialIDs(ctx, r.Binding, true)
	if err != nil {
		return Receipt{}, err
	}
	if !validID(r.CommandID) || r.ExpectedVersion != 0 || r.Kind != "work" {
		return Receipt{}, ErrInvalid
	}
	_, digest, err := encodeCreation(ids, r.Kind)
	if err != nil {
		return Receipt{}, err
	}
	return s.perform(ctx, privateOperation{ids: ids, id: r.CommandID, digest: digest, operation: "context_create", scope: "context", kind: r.Kind, changes: []privateChange{{slot: "handoff", mode: "replace"}, {slot: "memory", mode: "replace"}}})
}
func (s *Service) WritePrivate(ctx context.Context, r PrivateWrite) (Receipt, error) {
	if err := s.enter(ctx); err != nil {
		return Receipt{}, err
	}
	defer s.active.Done()
	ids, err := s.initialIDs(ctx, r.Binding, false)
	if err != nil {
		return Receipt{}, err
	}
	if !validID(r.CommandID) {
		return Receipt{}, ErrInvalid
	}
	changes := make([]privateChange, len(r.Changes))
	for i, c := range r.Changes {
		changes[i] = privateChange{c.Slot, c.Mode, c.Bytes}
	}
	_, digest, err := encodePrivateWrite(ids, r.Scope, r.ExpectedVersion, changes)
	if err != nil {
		return Receipt{}, err
	}
	slices.SortFunc(changes, func(a, b privateChange) int {
		if a.slot < b.slot {
			return -1
		}
		if a.slot > b.slot {
			return 1
		}
		return 0
	})
	return s.perform(ctx, privateOperation{ids: ids, id: r.CommandID, digest: digest, operation: "private_write", scope: r.Scope, expected: r.ExpectedVersion, changes: changes})
}
func (s *Service) checkOperationBindingLocked(op privateOperation) error {
	if op.operation == "agent_register" {
		if a, exists := s.p.agents[op.ids.agent]; exists && (a.session != op.ids.session || a.general != op.ids.context) {
			return ErrNotBound
		}
		if owner, exists := s.p.contexts[op.ids.context]; exists && owner != op.ids.agent {
			return ErrNotBound
		}
		return nil
	}
	ids, err := s.bindingLocked(Binding{op.ids.agent, op.ids.context}, op.operation == "context_create")
	if err != nil {
		return err
	}
	if ids != op.ids {
		return ErrNotBound
	}
	return nil
}

// lookupOperation leaves mu held. The expensive old-ID prefix scan is outside
// it; refreshing and checking the newly ingested tail closes the race.
func (s *Service) lookupOperation(ctx context.Context, op privateOperation) (commandMatch, error) {
	s.mu.Lock()
	if err := s.usableLocked(ctx); err != nil {
		return commandMatch{}, err
	}
	if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
		return commandMatch{}, err
	}
	if err := s.checkOperationBindingLocked(op); err != nil {
		return commandMatch{}, err
	}
	through := s.p.through
	old := bytes.Compare(op.id[:], s.p.maxCommand[:]) <= 0
	s.mu.Unlock()
	if s.deps.beforeLookup != nil {
		s.deps.beforeLookup()
	}
	var m commandMatch
	var err error
	if old {
		m, err = s.findCommand(ctx, op.id, 0, through)
	}
	s.mu.Lock()
	if err != nil {
		return m, err
	}
	if err := s.usableLocked(ctx); err != nil {
		return m, err
	}
	if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
		return m, err
	}
	if err := s.checkOperationBindingLocked(op); err != nil {
		return m, err
	}
	if !m.found && through < s.p.through {
		m, err = s.findCommand(ctx, op.id, through, s.p.through)
	}
	return m, err
}
func (s *Service) perform(ctx context.Context, op privateOperation) (Receipt, error) {
	m, err := s.lookupOperation(ctx, op)
	if err != nil {
		s.mu.Unlock()
		return Receipt{}, err
	}
	if m.found {
		s.mu.Unlock()
		return matchReceipt(m, op.ids, op.digest)
	}
	total := int64(0)
	for _, c := range op.changes {
		if int64(len(c.data)) > s.limits.MaxObjectBytes {
			s.mu.Unlock()
			return Receipt{}, ErrTooLarge
		}
		total += int64(len(c.data))
	}
	if total > s.limits.MaxOperationBytes {
		s.mu.Unlock()
		return Receipt{}, ErrTooLarge
	}
	if s.preparing >= s.limits.MaxConcurrentWrites {
		s.mu.Unlock()
		return Receipt{}, &BusyError{}
	}
	s.preparing++
	s.mu.Unlock()
	defer s.releasePreparation()
	// No retained private-byte copy exists before the finite reservation.
	for i := range op.changes {
		op.changes[i].data = bytes.Clone(op.changes[i].data)
	}
	b := Binding{op.ids.agent, op.ids.context}
	unlock, err := s.acquireScope(ctx, keyFor(op.ids, op.scope))
	if err != nil {
		return Receipt{}, err
	}
	defer unlock()
	if op.operation == "agent_register" {
		release, err := s.acquireScope(ctx, keyFor(op.ids, "context"))
		if err != nil {
			return Receipt{}, err
		}
		defer release()
	}
	// A waiting same-scope retry can now replay the winner before inspecting files.
	m, err = s.lookupOperation(ctx, op)
	if err == nil && m.found {
		s.mu.Unlock()
		return matchReceipt(m, op.ids, op.digest)
	}
	if err == nil {
		err = s.scopeIssuesLocked(op.ids, op.scope)
	}
	s.mu.Unlock()
	if err != nil {
		return Receipt{}, err
	}
	paths, err := s.operationPaths(op)
	if err != nil {
		return Receipt{}, err
	}
	if op.operation == "private_write" {
		if err := s.inspectLive(ctx, op.ids, op.scope); err != nil {
			return Receipt{}, err
		}
	} else {
		for _, path := range paths {
			if err := refuseInitialMarkdown(path); err != nil {
				return Receipt{}, err
			}
		}
	}
	if s.deps.beforePublish != nil {
		if err := s.deps.beforePublish(b, op.scope); err != nil {
			return Receipt{}, err
		}
	}
	for _, c := range op.changes {
		if c.mode != "clear" {
			if _, err := paths[0].publishObject(c.data); err != nil {
				return Receipt{}, err
			}
		}
	}
	if len(paths) == 2 {
		if _, err := paths[1].publishObject(nil); err != nil {
			return Receipt{}, err
		}
	}
	if s.deps.afterPublish != nil {
		s.deps.afterPublish(b, op.scope)
	}
	m, err = s.lookupOperation(ctx, op)
	if err != nil {
		s.mu.Unlock()
		return Receipt{}, err
	}
	if m.found {
		s.mu.Unlock()
		return matchReceipt(m, op.ids, op.digest)
	}
	if err := s.scopeIssuesLocked(op.ids, op.scope); err != nil {
		s.mu.Unlock()
		return Receipt{}, err
	}
	drafts, err := s.operationDrafts(op)
	if err != nil {
		s.mu.Unlock()
		return Receipt{}, err
	}
	if s.deps.beforeSubmit != nil {
		drafts = s.deps.beforeSubmit(drafts)
	}
	first := s.store.Durable() + 1
	g, err := decodeGroup(drafts, first)
	if err == nil && g == nil {
		err = ErrInvalid
	}
	if err == nil {
		_, err = s.p.planGroup(g, first)
	}
	if err != nil {
		s.mu.Unlock()
		return Receipt{}, err
	}
	// Verify selected bytes again at the final boundary before committing them.
	for i, rev := range g.revisions {
		for _, c := range rev.Changes {
			if c.Mode != "clear" {
				_, err = readObject(paths[i], Head{SHA256: c.SHA256, Bytes: c.Bytes})
				if err != nil {
					s.mu.Unlock()
					return Receipt{}, err
				}
			}
		}
	}
	if err := s.usableLocked(ctx); err != nil {
		s.mu.Unlock()
		return Receipt{}, err
	}
	committed, err := s.store.Submit(ctx, drafts[0], drafts[1:])
	if err != nil {
		if errors.Is(err, appender.ErrUncertain) || errors.Is(err, appender.ErrPoisoned) {
			s.poisoned = true
		}
		s.mu.Unlock()
		if errors.Is(err, ledgerstore.ErrCommandMismatch) {
			return Receipt{}, ErrCommandMismatch
		}
		return Receipt{}, err
	}
	// Submit's actual range is authoritative. Refresh all intervening commits,
	// including workspace records, rather than jumping a guessed cursor.
	if err := s.refreshLocked(context.Background(), max(s.p.through, committed.Last)); err != nil {
		s.mu.Unlock()
		return Receipt{}, err
	}
	actual, err := s.findCommand(context.Background(), op.id, committed.First-1, committed.Last)
	if err != nil || !actual.found {
		s.poisoned = true
		s.mu.Unlock()
		if err == nil {
			err = ErrInconsistent
		}
		return Receipt{}, err
	}
	result, err := matchReceipt(actual, op.ids, op.digest)
	s.mu.Unlock()
	if err != nil {
		return Receipt{}, err
	}
	if committed.Duplicate {
		return result, nil
	}
	if s.deps.afterCommit != nil {
		s.deps.afterCommit(result)
	}
	// Caller cancellation no longer owns this durable transition.
	for _, rev := range actual.group.revisions {
		if err := s.completeScope(context.Background(), op.ids, rev.Scope, false, nil); err != nil {
			return Receipt{}, &MaterializationError{ReadBinding: ReadBinding{b, rev.Scope}, Revision: uint64(rev.Revision), Cause: err}
		}
	}
	return result, nil
}
func (s *Service) operationPaths(op privateOperation) ([]privateScope, error) {
	directory := op.directory
	if directory == "" {
		s.mu.Lock()
		directory = s.p.agents[op.ids.agent].directory
		s.mu.Unlock()
	}
	c := ""
	if op.scope == "context" {
		c = op.ids.context.String()
	}
	path, err := newPrivateScope(s.colony, directory, c, &s.dirs)
	if err != nil {
		return nil, err
	}
	paths := []privateScope{path}
	if op.operation == "agent_register" {
		p, err := newPrivateScope(s.colony, directory, op.ids.context.String(), &s.dirs)
		if err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}
func (s *Service) operationDrafts(op privateOperation) ([]ledger.Draft, error) {
	var d []ledger.Draft
	appendPayload := func(typ string, actor ledger.Actor, payload []byte) {
		d = append(d, ledger.Draft{Type: typ, TypeVersion: 1, Actor: actor, AgentID: op.ids.agent, ExecutionSessionID: op.ids.session, ContextID: op.ids.context, OperationID: op.id, Payload: payload})
	}
	cmd, err := (ledger.Command{CommandID: op.id, SubmissionSHA256: op.digest}).Encode()
	if err != nil {
		return nil, err
	}
	appendPayload(ledger.TypeCommand, ledger.ActorOwner, cmd)
	if op.operation == "agent_register" {
		p, err := (ledger.RuntimeAgentRegistered{Directory: op.directory, GeneralContextID: op.ids.context}).Encode()
		if err != nil {
			return nil, err
		}
		appendPayload(ledger.TypeRuntimeAgentRegistered, ledger.ActorOwner, p)
	}
	if op.operation != "private_write" {
		kind := op.kind
		if op.operation == "agent_register" {
			kind = "general"
		}
		p, err := (ledger.RuntimeContextCreated{Kind: kind}).Encode()
		if err != nil {
			return nil, err
		}
		appendPayload(ledger.TypeRuntimeContextCreated, ledger.ActorOwner, p)
	}
	changes := make([]ledger.PrivateChange, len(op.changes))
	slots := make([]string, len(op.changes))
	for i, c := range op.changes {
		slots[i] = c.slot
		changes[i] = ledger.PrivateChange{Slot: c.slot, Mode: c.mode, Bytes: int64(len(c.data))}
		if c.mode != "clear" {
			changes[i].SHA256, _ = hashObject(c.data)
		}
		if c.slot == "transcript" {
			if st := s.p.scopes[keyFor(op.ids, op.scope)]; st != nil {
				changes[i].PreviousSHA256 = st.heads["transcript"].SHA256
			}
		}
	}
	rev := ledger.PrivateRevision{Scope: op.scope, ExpectedVersion: int64(op.expected), Revision: int64(op.expected + 1), Changes: changes}
	p, err := rev.Encode()
	if err != nil {
		return nil, err
	}
	appendPayload(ledger.TypePrivateRevision, ledger.ActorCore, p)
	if op.operation == "agent_register" {
		empty, _ := hashObject(nil)
		p, err := (ledger.PrivateRevision{Scope: "context", Revision: 1, Changes: []ledger.PrivateChange{{Slot: "handoff", Mode: "replace", SHA256: empty}, {Slot: "memory", Mode: "replace", SHA256: empty}}}).Encode()
		if err != nil {
			return nil, err
		}
		appendPayload(ledger.TypePrivateRevision, ledger.ActorCore, p)
	}
	if op.operation != "private_write" {
		slots = []string{}
	}
	p, err = (ledger.RuntimeCommandReceipt{CommandID: op.id, SubmissionSHA256: op.digest, Operation: op.operation, Target: ledger.PrivateTarget{Scope: op.scope, Slots: slots}, ExpectedVersion: int64(op.expected), Outcome: "applied"}).Encode()
	if err != nil {
		return nil, err
	}
	appendPayload(ledger.TypeRuntimeCommandReceipt, ledger.ActorOwner, p)
	return d, nil
}
func (s *Service) CommandStatus(ctx context.Context, r CommandLookup) (Receipt, error) {
	if err := s.enter(ctx); err != nil {
		return Receipt{}, err
	}
	defer s.active.Done()
	ids, err := s.initialIDs(ctx, r.Binding, false)
	if err != nil {
		return Receipt{}, err
	}
	if !validID(r.CommandID) || r.SubmissionSHA256 == (ledger.Digest{}) {
		return Receipt{}, ErrInvalid
	}
	m, err := s.lookupOperation(ctx, privateOperation{ids: ids, id: r.CommandID, digest: r.SubmissionSHA256})
	s.mu.Unlock()
	if err != nil {
		return Receipt{}, err
	}
	if !m.found {
		return Receipt{}, ErrUnavailable
	}
	return matchReceipt(m, ids, r.SubmissionSHA256)
}
