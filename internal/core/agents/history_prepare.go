package agents

import (
	"context"
	"encoding/json"
	"errors"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// These are metadata selectors, never permissions or prepared effects. The
// concrete selection is encoded into the key and checked again in the answer.
type historyNeedKind byte

const (
	needCommit historyNeedKind = iota + 1
	needCommand
	needGrant
	needTask
	needWork
	needReview
	needAssignment
	needWorkspace
	needContextTask
	needSources
	needCredential
	needPaid
	needPaidTotal
	needBudget
	needRegistration
	needScope
	needDirectoryScan
)

// Only these selectors can construct a need. Each kind has one key shape.
type historySelection interface{ historyKind() historyNeedKind }
type commitHistoryKey struct{ First, Last uint64 }
type commandHistoryKey struct{ ID uuid.UUID }
type grantHistoryKey struct{ ID uuid.UUID }
type taskHistoryKey struct{ ID uuid.UUID }
type workHistoryKey struct{ ID uuid.UUID }
type reviewHistoryKey struct{ ID uuid.UUID }
type assignmentHistoryKey struct{ Assignment ledger.AssignmentInput }
type workspaceHistoryKey struct{ ID uuid.UUID }
type contextTaskHistoryKey struct{ ID uuid.UUID }
type sourcesHistoryKey struct{ Sequences []int64 }
type credentialHistoryKey struct{ ID uuid.UUID }
type paidHistoryKey struct{ ID uuid.UUID }
type paidTotalHistoryKey struct {
	Scope    ledger.PaidScope
	Period   string
	Origin   ledger.Origin
	Filtered bool
}
type budgetHistoryKey struct {
	Scope    ledger.ScopeKey
	Origin   ledger.Origin
	Filtered bool
}
type registrationHistoryKey struct {
	Kind    string
	ID      uuid.UUID
	Address string
	Version int64
}
type scopeHistoryKey struct {
	Agent, Session, Context string
	Usage                   ledger.RuntimeLocalUsed
}
type directoryReadSelection struct {
	Purpose     string
	Origin      ledger.Origin
	Object      ledger.ObjectRef
	PendingKind string
	PendingID   uuid.UUID
}
type directoryHistoryKey struct {
	Kind      byte
	ID        uuid.UUID
	Selection directoryReadSelection
}

func (commitHistoryKey) historyKind() historyNeedKind       { return needCommit }
func (commandHistoryKey) historyKind() historyNeedKind      { return needCommand }
func (grantHistoryKey) historyKind() historyNeedKind        { return needGrant }
func (taskHistoryKey) historyKind() historyNeedKind         { return needTask }
func (workHistoryKey) historyKind() historyNeedKind         { return needWork }
func (reviewHistoryKey) historyKind() historyNeedKind       { return needReview }
func (assignmentHistoryKey) historyKind() historyNeedKind   { return needAssignment }
func (workspaceHistoryKey) historyKind() historyNeedKind    { return needWorkspace }
func (contextTaskHistoryKey) historyKind() historyNeedKind  { return needContextTask }
func (sourcesHistoryKey) historyKind() historyNeedKind      { return needSources }
func (credentialHistoryKey) historyKind() historyNeedKind   { return needCredential }
func (paidHistoryKey) historyKind() historyNeedKind         { return needPaid }
func (paidTotalHistoryKey) historyKind() historyNeedKind    { return needPaidTotal }
func (budgetHistoryKey) historyKind() historyNeedKind       { return needBudget }
func (registrationHistoryKey) historyKind() historyNeedKind { return needRegistration }
func (scopeHistoryKey) historyKind() historyNeedKind        { return needScope }
func (directoryHistoryKey) historyKind() historyNeedKind    { return needDirectoryScan }

type historyReadKey struct {
	kind           historyNeedKind
	after, through uint64
	selection      string
}

type historyReadAnswer struct {
	key   historyReadKey
	entry historyEntry
}

type historyNeed struct {
	key     historyReadKey
	resolve func(context.Context) (historyReadAnswer, error)
}

func (*historyNeed) Error() string { return "agents: unresolved historical metadata" }

func isHistoryNeed(err error) bool {
	var need *historyNeed
	return errors.As(err, &need)
}

type historyReadSet struct {
	answers             map[historyReadKey]historyReadAnswer
	failures            map[historyReadKey]error
	binding             historyReadBinding
	rounds              int
	through, generation uint64
	havePrefix          bool
}

type historyReadBinding struct {
	bound          bool
	ids            submissionIDs
	agent          agentState
	owner, session uuid.UUID
}

func (s *Service) historyBindingLocked(bound *submissionIDs) historyReadBinding {
	if bound == nil {
		return historyReadBinding{}
	}
	return historyReadBinding{true, *bound, s.p.agents[bound.agent], s.p.contexts[bound.context], s.p.sessions[bound.session]}
}

// readHistory returns a closed typed suspension on a miss. A nil read set is
// the explicit already-unlocked or durable-acknowledgement mode. Resolve may
// capture only immutable inputs and cold-fold locals, not live service state.
func readHistory[T any](ctx context.Context, reads *historyReadSet, after, through uint64, selection historySelection, resolve func(context.Context) (T, error)) (T, error) {
	var zero T
	kind := selection.historyKind()
	selected, err := json.Marshal(selection)
	if err != nil {
		return zero, err
	}
	key := historyReadKey{kind: kind, after: after, through: through, selection: string(selected)}
	if reads == nil {
		return resolve(ctx)
	}
	if answer, found := reads.answers[key]; found {
		if answer.key != key || answer.entry.key.kind != byte(kind) || answer.entry.after != after || answer.entry.from != through || answer.entry.checksum != answer.entry.sum() {
			delete(reads.answers, key)
		} else {
			var encoded struct {
				Selection string
				Value     T
			}
			if json.Unmarshal(answer.entry.data, &encoded) == nil && encoded.Selection == key.selection {
				return encoded.Value, nil
			}
			delete(reads.answers, key)
		}
	}
	if reads.rounds >= 2 {
		return resolve(ctx)
	}
	if err := reads.failures[key]; err != nil {
		return zero, err
	}
	return zero, &historyNeed{key: key, resolve: func(ctx context.Context) (historyReadAnswer, error) {
		value, err := resolve(ctx)
		if err != nil {
			return historyReadAnswer{}, err
		}
		data, err := json.Marshal(struct {
			Selection string
			Value     T
		}{key.selection, value})
		entry := historyEntry{key: historyKey{kind: byte(kind)}, after: after, from: through, found: true, data: data}
		entry.checksum = entry.sum()
		return historyReadAnswer{key: key, entry: entry}, err
	}}
}

// prepareHistoryLocked reruns the whole pure phase. Only its caller performs
// effects, after this function returns with mu held. Existing reservations stay
// owned across the unlock. The third and subsequent needs resolve under mu.
// Acceptance passes catchUp=false because it is itself the refresh performer;
// every operation entrant catches up before capturing its prefix and binding.
func (s *Service) prepareHistoryLocked(ctx context.Context, reads *historyReadSet, bound *submissionIDs, catchUp bool, phase func(*historyReadSet) error) error {
	if reads.answers == nil {
		reads.answers = make(map[historyReadKey]historyReadAnswer)
	}
	if reads.failures == nil {
		reads.failures = make(map[historyReadKey]error)
	}
	for {
		if err := s.usableLocked(ctx); err != nil {
			return err
		}
		if catchUp {
			if err := s.refreshLocked(ctx, reads, s.store.Durable()); err != nil {
				return err
			}
		}
		through := s.p.through
		generation, _ := s.history.directoryState(through)
		binding := s.historyBindingLocked(bound)
		if reads.havePrefix && (through != reads.through || generation != reads.generation || binding != reads.binding) {
			clear(reads.answers)
			clear(reads.failures)
		}
		reads.through, reads.generation, reads.havePrefix, reads.binding = through, generation, true, binding
		sample := s.reviewSample
		err := phase(reads)
		var need *historyNeed
		if !errors.As(err, &need) {
			return err
		}
		s.reviewSample = sample
		reads.rounds++
		s.active.Add(1)
		inject := s.deps.historyResolveError
		s.mu.Unlock()
		var answer historyReadAnswer
		var readErr error
		if inject != nil {
			readErr = inject(need.key.kind)
		}
		if readErr == nil {
			answer, readErr = need.resolve(ctx)
		}
		s.mu.Lock()
		s.active.Done()
		if err := s.usableLocked(ctx); err != nil {
			return err
		}
		currentGeneration, _ := s.history.directoryState(s.p.through)
		if s.p.through != through || currentGeneration != generation || s.historyBindingLocked(bound) != binding {
			// Even an error belongs to the old selection. Do not disclose it
			// ahead of the new phase's binding and authority checks.
			clear(reads.answers)
			clear(reads.failures)
			continue
		}
		if readErr != nil {
			if isHistoryNeed(readErr) {
				return ErrIntegrity
			}
			reads.failures[need.key] = readErr
			continue
		}
		if answer.key != need.key || answer.entry.key.kind != byte(need.key.kind) || answer.entry.checksum != answer.entry.sum() {
			return ErrIntegrity
		}
		reads.answers[need.key] = answer
	}
}

// The encoded source convention preserves payload and body refs which the
// envelope's JSON methods intentionally omit, and lends no mutable storage.
type historyCommitValue struct {
	ID          uuid.UUID
	First, Last uint64
	Records     []historySourceValue
}

func encodeHistoryCommit(c ledger.Commit) historyCommitValue {
	value := historyCommitValue{ID: c.ID, First: c.First, Last: c.Last}
	for _, record := range c.Records {
		refs := make([]string, len(record.BodyRefs))
		for i, ref := range record.BodyRefs {
			refs[i] = ref.String()
		}
		value.Records = append(value.Records, historySourceValue{Record: record, Payload: record.Payload, BodyRefs: refs})
	}
	return value
}

func decodeHistoryCommit(value historyCommitValue) (ledger.Commit, error) {
	c := ledger.Commit{ID: value.ID, First: value.First, Last: value.Last}
	for _, source := range value.Records {
		record := source.Record
		record.Payload = source.Payload
		record.BodyRefs = nil
		for _, text := range source.BodyRefs {
			digest, err := ledger.ParseDigest(text)
			if err != nil {
				return ledger.Commit{}, err
			}
			record.BodyRefs = append(record.BodyRefs, digest)
		}
		c.Records = append(c.Records, record)
	}
	return c, nil
}

type historyPaidValue struct {
	Admission, Receipt                        json.RawMessage
	Intent                                    json.RawMessage `json:",omitempty"`
	Result                                    json.RawMessage `json:",omitempty"`
	Profile                                   ledger.Profile
	Origin                                    ledger.Origin
	Version, Cost                             int64
	Sequence, ResultSequence, ReceiptSequence uint64
	Held, Ended, Fenced, Acknowledged         bool
	State                                     string
	Evidence                                  map[string]uint64
}

func encodeHistoryPaid(a paidAttempt) (historyPaidValue, error) {
	if a.version == 0 {
		return historyPaidValue{}, nil
	}
	v := historyPaidValue{Profile: a.profile, Origin: a.origin, Version: a.version,
		Cost: a.cost, Sequence: a.sequence, ResultSequence: a.resultSequence,
		ReceiptSequence: a.receiptSequence, Held: a.held, Ended: a.ended,
		Fenced: a.fenced, Acknowledged: a.acknowledged, State: a.state}
	var err error
	if v.Admission, err = a.admission.Encode(); err != nil {
		return historyPaidValue{}, err
	}
	if v.Receipt, err = a.receipt.Encode(); err != nil {
		return historyPaidValue{}, err
	}
	if a.intent != nil {
		if v.Intent, err = a.intent.Encode(); err != nil {
			return historyPaidValue{}, err
		}
	}
	if a.result != nil {
		if v.Result, err = a.result.Encode(); err != nil {
			return historyPaidValue{}, err
		}
	}
	if a.evidence != nil {
		v.Evidence = make(map[string]uint64, len(a.evidence))
		for digest, sequence := range a.evidence {
			v.Evidence[digest.String()] = sequence
		}
	}
	return v, nil
}

func decodeHistoryPaid(v historyPaidValue) (paidAttempt, error) {
	if v.Version == 0 {
		return paidAttempt{}, nil
	}
	a := paidAttempt{profile: v.Profile, origin: v.Origin, version: v.Version,
		cost: v.Cost, sequence: v.Sequence, resultSequence: v.ResultSequence,
		receiptSequence: v.ReceiptSequence, held: v.Held, ended: v.Ended,
		fenced: v.Fenced, acknowledged: v.Acknowledged, state: v.State}
	var err error
	if a.admission, err = ledger.DecodeRuntimePaidReserved(v.Admission); err != nil {
		return paidAttempt{}, err
	}
	if a.receipt, err = ledger.DecodeRuntimePaidReceipt(v.Receipt); err != nil {
		return paidAttempt{}, err
	}
	if len(v.Intent) != 0 {
		intent, err := ledger.DecodeRuntimePaidDispatchIntent(v.Intent)
		if err != nil {
			return paidAttempt{}, err
		}
		a.intent = &intent
	}
	if len(v.Result) != 0 {
		result, err := ledger.DecodeRuntimePaidResult(v.Result)
		if err != nil {
			return paidAttempt{}, err
		}
		a.result = &result
	}
	if v.Evidence != nil {
		a.evidence = make(map[ledger.Digest]uint64, len(v.Evidence))
		for text, sequence := range v.Evidence {
			digest, err := ledger.ParseDigest(text)
			if err != nil {
				return paidAttempt{}, err
			}
			a.evidence[digest] = sequence
		}
	}
	return a, nil
}

// foldDirectory keeps coverage and positions inside the phase. A cold payload
// needs its exact containing commit; a missing directory needs only the folded
// value, never an operation-local copy of the entire ledger.
func foldDirectory[T any](ctx context.Context, s *Service, reads *historyReadSet, key directoryKey, after, through uint64, selection directoryReadSelection, visit func(*T, ledger.Commit) error) (T, error) {
	var value T
	rows, hit, err := s.history.occurrences(key, after, through)
	if err != nil {
		return value, err
	}
	if !hit {
		selector := directoryHistoryKey{byte(key.kind), key.id, selection}
		return readHistory(ctx, reads, after, through, selector, func(ctx context.Context) (T, error) {
			var result T
			err := s.scan(ctx, after, through, func(c ledger.Commit) error { return visit(&result, c) })
			return result, err
		})
	}
	var last uint64
	for _, row := range rows {
		if row.last == last {
			continue
		}
		c, err := s.directoryCommit(ctx, reads, row, through)
		if err != nil {
			return value, err
		}
		if err := visit(&value, c); err != nil {
			return value, err
		}
		last = row.last
	}
	return value, nil
}
