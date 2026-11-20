package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

type loopInputObject struct {
	Schema        int                               `json:"schema"`
	Kind          string                            `json:"kind"`
	Origin        ledger.Origin                     `json:"origin"`
	TurnID        uuid.UUID                         `json:"turn_id"`
	Text          string                            `json:"text"`
	Artifacts     []ledger.ArtifactRef              `json:"artifacts"`
	OtherWork     []string                          `json:"other_work"`
	TaskResults   []loopTaskResult                  `json:"task_results,omitzero"`
	NoticeCursors []ledger.ContinuationNoticeCursor `json:"notice_cursors,omitzero"`
}

type loopTaskResult struct {
	Sequence           uint64        `json:"sequence"`
	MergeBackID        uuid.UUID     `json:"merge_back_id"`
	ScratchID          uuid.UUID     `json:"scratch_id"`
	ParentScratchID    uuid.UUID     `json:"parent_scratch_id"`
	AuthoringContextID uuid.UUID     `json:"authoring_context_id"`
	Outcome            string        `json:"outcome"`
	WrittenPaths       []string      `json:"written_paths"`
	ChangedPaths       []string      `json:"changed_paths"`
	ConflictSHA256     ledger.Digest `json:"conflict_sha256,omitzero"`
}

type loopNoticeSelection struct {
	task   ledger.TaskBinding
	cursor ledger.ContinuationNoticeCursor
}

func loopNoticeResult(notice workspace.ScratchNotice) loopTaskResult {
	return loopTaskResult{notice.Sequence, notice.MergeBackID, notice.ScratchID, notice.ParentScratchID, notice.AuthoringContextID, notice.Outcome, append([]string{}, notice.WrittenPaths...), append([]string{}, notice.ChangedPaths...), notice.ConflictSHA256}
}

type loopSource struct {
	Scope    string `json:"scope"`
	Slot     string `json:"slot"`
	Revision uint64 `json:"revision"`
	Head     Head   `json:"head"`
}

func (r *loopRuntime) start(ctx context.Context, reads *historyReadSet, ids submissionIDs, id uuid.UUID, input session.MessageSend) (_ *loopGroup, err error) {
	s := r.service
	setting, configured := r.settings[Binding{ids.agent, ids.context}]
	r.mu.Lock()
	if r.stopping {
		r.mu.Unlock()
		return nil, ErrClosed
	}
	r.starting++
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.starting--; r.mu.Unlock(); r.notify() }()
	origin := originOf(ids)
	var match commandMatch
	var grant, credential uuid.UUID
	var sample int64
	var route ledger.RuntimePaidRouteConfigured
	var scopes []ledger.ScopeKey
	var first *ledger.RuntimeEligibilityChanged
	defer func() { err = s.firstActionRefused(ctx, ids.agent, first, err) }()
	s.mu.Lock()
	err = s.prepareHistoryLocked(ctx, reads, &ids, true, func(reads *historyReadSet) error {
		grant, credential, sample, scopes = uuid.UUID{}, uuid.UUID{}, 0, nil
		first = nil
		var err error
		match, err = s.findCommand(ctx, reads, id, 0, s.p.through)
		if err != nil || match.found {
			return err
		}
		var eligibilityErr error
		first, eligibilityErr = s.classifyFirstAction(origin, id, input, setting.TaskBinding)
		work, found, err := s.loadWork(ctx, reads, input.TurnID, s.p.through)
		if err != nil {
			return err
		}
		if found {
			if work.origin != origin {
				return ErrDenied
			}
			return ErrUnavailable
		}
		target := ledger.Resource{Kind: "context", Binding: ledger.Binding(bindingOf(origin)), Scope: "context", Slots: []string{}}
		grant, err = s.loopGrantLocked(ctx, reads, origin, "turn.start", []ledger.Resource{target}, 0)
		if isHistoryNeed(err) {
			return err
		}
		if err == nil {
			if eligibilityErr != nil {
				var held *EligibilityHeldError
				if errors.As(eligibilityErr, &held) {
					view, filterErr := s.filterEligibility(ctx, reads, origin, eligibilityView(s.p.agents[ids.agent].eligibility, s.p.through))
					if filterErr != nil {
						return filterErr
					}
					return &EligibilityHeldError{AgentID: ids.agent, EligibilityView: view}
				}
				return eligibilityErr
			}
			if !configured {
				return ErrUnavailable
			}
			sample, err = s.samplePaidLocked()
		}
		if err == nil && sample > math.MaxInt64-setting.TimeoutMS*int64(time.Millisecond) {
			err = ErrInvalid
		}
		route = s.p.paidRoutes[setting.RouteID]
		if err == nil && (!route.Enabled || route.Version != setting.ExpectedRouteVersion || route.CredentialScopeID != setting.CredentialScopeID || setting.MaxOutputTokens > route.Profile.OutputMax) {
			err = ErrUnavailable
		}
		var credentialVersion int64
		for id, c := range s.p.paidCredentials {
			if c.ScopeID == setting.CredentialScopeID && c.Provider == route.Profile.Provider && c.Version > credentialVersion && s.paid.secrets[id] != nil {
				credential, credentialVersion = id, c.Version
			}
		}
		if err == nil && credential == (uuid.UUID{}) {
			err = ErrUnavailable
		}
		if s.p.agents[ids.agent].general == ids.context {
			if setting.DisplayTitle != "General work" || setting.TaskBinding != nil {
				err = ErrInvalid
			}
		} else if setting.TaskBinding == nil {
			err = ErrInvalid
		}
		// The bounded manifest retains refs rather than large output bodies.
		manifestBound := int64(1024) + setting.Policy.MaxModelCalls*(320*128+1024) + setting.Policy.MaxToolCalls*1024
		if manifestBound > maxObjectBytes {
			err = ErrTooLarge
		}
		if err == nil {
			var chain []grantRules
			chain, err = s.currentChain(ctx, reads, origin, grant, s.p.through)
			if err == nil {
				scopes, err = chainScopes(origin, chain)
			}
		}
		return err
	})
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if match.found {
		s.mu.Unlock()
		return s.retryLoopStart(ctx, ids, id, input, setting.TaskBinding, match.loop)
	}
	policy := setting.Policy
	policy.DeadlineUTCNS = sample + setting.TimeoutMS*int64(time.Millisecond)
	t := &loopTurn{origin: origin, id: input.TurnID, settings: setting, policy: policy, credential: credential, profile: route.Profile, grant: grant, state: "admitted", writers: map[uuid.UUID]*workspace.Writer{}, live: newLoopLive(policy.MaxLiveBytes), done: make(chan struct{}), endingID: uuid.NewV7(), endCommandID: uuid.NewV7()}
	t.reservation.origin = origin
	t.reservation.scopes = scopes
	// Main live buffer, eight snapshots, queued copies, request/render and
	// manifest/encoding scratch are separate physical retained memory.
	t.reservation.budget = budgetPreparation{hold: [4]int64{0, 0, 10*policy.MaxLiveBytes + loopTextSubscribers*loopTextQueue*loopTextChunk + 24*providers.RequestLimit + 4*maxObjectBytes, 0}}
	err = s.p.book.reservePreparation(origin, scopes, t.reservation.budget)
	if err != nil {
		t.reservation.scopes = nil
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	owned := true
	defer func() {
		if owned {
			s.mu.Lock()
			s.releaseScopedLocked(&t.reservation)
			s.mu.Unlock()
		}
	}()
	admissionID := uuid.NewV7()
	if first != nil {
		admissionID = first.FirstAction.PermittedTurn.AdmissionCommandID
	}
	native, err := s.executeScopedResult(ctx, scopedCaller{actor: "agent", binding: bindingOf(origin), fixed: ids}, "turn.start", TurnAdmission{CommandID: admissionID, GrantID: grant, TurnID: t.id, TaskBinding: setting.TaskBinding})
	if err != nil {
		return nil, agentError(err)
	}
	if !native.nativeAdmitted {
		match, err := queryHistory(ctx, s, &ids, func(reads *historyReadSet) (commandMatch, error) {
			return s.findCommand(ctx, reads, id, 0, s.p.through)
		})
		if err != nil {
			return nil, err
		}
		if !match.found {
			return nil, ErrUnavailable
		}
		return s.retryLoopStart(ctx, ids, id, input, setting.TaskBinding, match.loop)
	}
	bound := false
	defer func() {
		if !bound {
			_, endingErr := s.EndTurn(context.Background(), TurnEnding{CommandID: t.endingID, TurnID: t.id, ExpectedVersion: 1, Outcome: "released"})
			if err == nil {
				err = endingErr
			}
		}
	}()
	note := r.otherWork(t)
	inputObject := loopInputObject{Schema: 1, Kind: "owner_input", Origin: origin, TurnID: t.id, Text: input.Text, Artifacts: slices.Clone(input.Artifacts), OtherWork: note}
	if inputObject.Artifacts == nil {
		inputObject.Artifacts = []ledger.ArtifactRef{}
	}
	t.ctx, t.cancel = context.WithCancel(context.Background())
	defer func() {
		if owned {
			t.cancel()
		}
	}()
	request, sources, err := r.render(ctx, t, &inputObject)
	if err != nil {
		return nil, err
	}
	t.request, t.sources, t.artifacts = request, sources, slices.Clone(input.Artifacts)
	render := modelJSON(struct {
		Schema            int                  `json:"schema"`
		Origin            ledger.Origin        `json:"origin"`
		TurnID            uuid.UUID            `json:"turn_id"`
		RouteID           uuid.UUID            `json:"route_id"`
		RouteVersion      int64                `json:"route_version"`
		Provider          string               `json:"provider"`
		Model             string               `json:"model"`
		CredentialVersion uuid.UUID            `json:"credential_version"`
		ToolsSHA256       ledger.Digest        `json:"tools_sha256"`
		Sources           []loopSource         `json:"sources"`
		Artifacts         []ledger.ArtifactRef `json:"artifacts"`
		Request           providers.Request    `json:"request"`
	}{1, origin, t.id, setting.RouteID, setting.ExpectedRouteVersion, t.profile.Provider, t.profile.ModelID, credential, sha256.Sum256(modelJSON(request.Tools)), sources, inputObject.Artifacts, request})
	unlock, err := s.acquireScope(ctx, keyFor(ids, "context"))
	if err != nil {
		return nil, err
	}
	defer unlock()
	inputRef, err := s.publishLoopObject(ctx, origin, credential, modelJSON(inputObject))
	if err != nil {
		return nil, err
	}
	policyRef, err := s.publishLoopObject(ctx, origin, credential, modelJSON(policy))
	if err != nil {
		return nil, err
	}
	renderRef, err := s.publishLoopObject(ctx, origin, credential, render)
	if err != nil {
		return nil, err
	}
	// A view can read turn.started as soon as loop.bind is installed, before
	// owner.initialize commits, so LiveText must already find the turn. It
	// stays "admitted", which the scheduler, the deadline sweep, shutdown,
	// status and otherWork all skip, until its owner exists.
	r.mu.Lock()
	r.turns[t.id] = t
	r.mu.Unlock()
	defer func() {
		if owned {
			r.mu.Lock()
			delete(r.turns, t.id)
			r.mu.Unlock()
			t.live.close("discarded", nil, ErrUnavailable)
		}
	}()
	g, err := s.commitLoop(ctx, id, ledger.LoopSubmission{Schema: 6, Actor: "owner", Origin: origin, TurnID: t.id, Operation: "loop.bind", Arguments: ledger.LoopBindArguments{TurnID: t.id, AdmissionCommandID: admissionID, InputObject: inputRef, PolicyObject: policyRef, RenderObject: renderRef}}, nil)
	if err != nil {
		return nil, err
	}
	bound = true
	owner, err := s.initializeContinuation(ctx, g, policy, "ready", &t.reservation)
	if err != nil {
		return nil, err
	}
	owned = false
	r.mu.Lock()
	t.binding, t.owner, t.state = g, owner.change, "ready"
	r.enqueue(t)
	r.mu.Unlock()
	r.notify()
	return g, nil
}

func (s *Service) retryLoopStart(ctx context.Context, ids submissionIDs, id uuid.UUID, input session.MessageSend, task *ledger.TaskBinding, group *loopGroup) (*loopGroup, error) {
	if group == nil || group.bound == nil || group.ids != ids || group.bound.TurnID != input.TurnID {
		return nil, ErrCommandMismatch
	}
	origin := originOf(ids)
	target := ledger.Resource{Kind: "context", Binding: ledger.Binding(bindingOf(origin)), Scope: "context", Slots: []string{}}
	if _, err := s.loopGrant(ctx, origin, "turn.inspect", []ledger.Resource{target}, 0); err != nil {
		return nil, err
	}
	prior, err := s.readLoopInput(ctx, origin, group.bound.InputObject)
	if err != nil {
		return nil, err
	}
	want, digestErr := s.eligibilityInputDigest(origin, id, input, task)
	have, priorErr := s.eligibilityInputDigest(prior.Origin, id, session.MessageSend{TurnID: prior.TurnID, Text: prior.Text, Artifacts: prior.Artifacts}, task)
	if digestErr != nil || priorErr != nil || want != have {
		return nil, ErrCommandMismatch
	}
	if _, err := s.loopGrant(ctx, origin, "turn.inspect", []ledger.Resource{target}, 0); err != nil {
		return nil, err
	}
	return group, nil
}

func (r *loopRuntime) otherWork(t *loopTurn) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var turns []*loopTurn
	for _, other := range r.turns {
		if other.state != "admitted" && other.origin.AgentID == t.origin.AgentID && other.origin.ContextID != t.origin.ContextID {
			turns = append(turns, other)
		}
	}
	slices.SortFunc(turns, func(a, b *loopTurn) int { return bytes.Compare(a.origin.ContextID[:], b.origin.ContextID[:]) })
	titles := make([]string, 0, len(turns))
	for _, other := range turns {
		titles = append(titles, other.settings.DisplayTitle)
	}
	return titles
}

func (r *loopRuntime) render(ctx context.Context, t *loopTurn, input *loopInputObject) (providers.Request, []loopSource, error) {
	s := r.service
	agent := &Agent{service: s, ids: submissionIDs{t.origin.AgentID, t.origin.SessionID, t.origin.ContextID}}
	request := providers.Request{MaxTokens: t.settings.MaxOutputTokens, Tools: modelToolDefinitions(), ToolChoice: &providers.ToolChoice{Type: "auto"}}
	reads := &historyReadSet{}
	var sources []loopSource
	var contextText strings.Builder
	for _, scope := range []string{"agent", "context"} {
		snapshot, err := s.Snapshot(ctx, ReadBinding{Binding: bindingOf(t.origin), Scope: scope})
		if err != nil {
			return request, nil, err
		}
		slots := []string{"instructions", "memory"}
		if scope == "context" {
			slots = []string{"memory", "handoff", "transcript"}
		}
		for _, slot := range slots {
			head, exists := snapshot.Heads[slot]
			if !exists || head.SHA256 == (ledger.Digest{}) {
				continue
			}
			target := ledger.Resource{Kind: "context", Binding: ledger.Binding(bindingOf(t.origin)), Scope: scope, Slots: []string{slot}}
			grant, err := s.loopGrant(ctx, t.origin, "private.read", []ledger.Resource{target}, head.Bytes)
			if err != nil {
				return request, nil, err
			}
			page, err := agent.ReadPrivate(ctx, ScopedPrivateRead{CommandID: uuid.NewV7(), GrantID: grant, Scope: scope, Slot: slot, Revision: snapshot.Revision, MaxBytes: providers.RequestLimit})
			if err != nil {
				return request, nil, err
			}
			if page.Page.NextCursor != 0 {
				return request, nil, ErrTooLarge
			}
			sources = append(sources, loopSource{Scope: scope, Slot: slot, Revision: snapshot.Revision, Head: head})
			if slot == "transcript" {
				for _, chunk := range page.Page.Chunks {
					s.mu.Lock()
					var record ledger.Record
					var found bool
					err := s.prepareHistoryLocked(ctx, reads, &agent.ids, true, func(reads *historyReadSet) error {
						var err error
						record, found, err = s.sourceRecord(ctx, reads, int64(chunk.Sequence), s.p.through)
						return err
					})
					s.mu.Unlock()
					if err != nil {
						return request, nil, err
					}
					if !found {
						return request, nil, ErrIntegrity
					}
					switch record.Type {
					case ledger.TypePrivateTurnRevision:
						text, err := r.renderTranscript(ctx, t, chunk.Bytes)
						if err != nil {
							return request, nil, err
						}
						contextText.WriteString(text)
					case ledger.TypePrivateRevision, ledger.TypePrivateScopedRevision:
						// Earlier transcript chunks are opaque private text, not
						// Core manifests, even when their bytes resemble one.
						contextText.WriteString("\nPrior transcript:\n")
						contextText.Write(chunk.Bytes)
					default:
						return request, nil, ErrIntegrity
					}
				}
			} else if scope == "agent" && slot == "instructions" {
				request.System = append(request.System, providers.Text{Type: "text", Text: string(page.Page.Bytes)})
			} else {
				contextText.WriteString("\n" + scope + " " + slot + ":\n")
				contextText.Write(page.Page.Bytes)
			}
			if contextText.Len() > providers.RequestLimit {
				return request, nil, ErrTooLarge
			}
		}
	}
	for _, ref := range input.Artifacts {
		s.mu.Lock()
		var chain []grantRules
		err := s.prepareHistoryLocked(ctx, reads, &agent.ids, true, func(reads *historyReadSet) error {
			var err error
			chain, err = s.artifactAuthority(ctx, reads, t.origin, ref, uuid.UUID{}, s.p.through, nil)
			return err
		})
		s.mu.Unlock()
		if err != nil {
			return request, nil, err
		}
		page, err := agent.ReadArtifact(ctx, ArtifactRead{CommandID: uuid.NewV7(), GrantID: chain[0].id, Artifact: ref, MaxBytes: providers.RequestLimit})
		if err != nil {
			return request, nil, err
		}
		contextText.WriteString("\nSelected shared artifact:\n")
		contextText.Write(page.Bytes)
		if contextText.Len() > providers.RequestLimit {
			return request, nil, ErrTooLarge
		}
	}
	if err := r.selectLoopNotices(ctx, reads, t, input); err != nil {
		return request, nil, err
	}
	if err := renderLoopNotices(&contextText, *input); err != nil {
		return request, nil, err
	}
	contextText.WriteString("\nOwner input:\n")
	contextText.WriteString(input.Text)
	if len(input.OtherWork) != 0 {
		contextText.WriteString("\nOther active work:\n")
		for _, title := range input.OtherWork {
			contextText.WriteString("- " + title + "\n")
		}
	}
	request.Messages = []providers.Message{{Role: "user", Content: []providers.Block{{Type: "text", Text: contextText.String()}}}}
	if _, err := requestStorageBound(request); err != nil {
		return request, nil, err
	}
	return request, sources, nil
}

// The initial pure phase fixes the schema predicate and the eligible own
// scratches. Workspace reads happen without Service.mu. The final pure phase
// rechecks the saved binding and permissions, never selecting a replacement.
func (r *loopRuntime) selectLoopNotices(ctx context.Context, reads *historyReadSet, t *loopTurn, input *loopInputObject) error {
	s := r.service
	ids := submissionIDs{t.origin.AgentID, t.origin.SessionID, t.origin.ContextID}
	var selections []loopNoticeSelection
	var through uint64
	s.mu.Lock()
	err := s.prepareHistoryLocked(ctx, reads, &ids, true, func(reads *historyReadSet) error {
		input.Schema, input.TaskResults, input.NoticeCursors = 1, nil, nil
		selections, through = nil, s.p.through
		if t.settings.TaskBinding == nil {
			return nil
		}
		for id, scratch := range s.p.scratches {
			if scratch.ContextID != t.origin.ContextID || scratch.ParentScratchID != (uuid.UUID{}) || scratch.closed {
				continue
			}
			input.Schema = 2
			selection := loopNoticeSelection{task: *t.settings.TaskBinding, cursor: ledger.ContinuationNoticeCursor{WorkspaceID: scratch.WorkspaceID, ScratchID: id}}
			if err := s.loopNoticeAuthority(ctx, reads, t.origin, selection); err != nil {
				if err == ErrDenied {
					continue
				}
				return err
			}
			cursors := s.p.noticeCursors[scopeKey{t.origin.AgentID, t.origin.ContextID}]
			if i, found := slices.BinarySearchFunc(cursors, selection.cursor, compareNoticeCursor); found {
				selection.cursor.AfterSequence = cursors[i].AfterSequence
			}
			selections = append(selections, selection)
		}
		return nil
	})
	s.mu.Unlock()
	if err != nil || input.Schema == 1 {
		return err
	}
	slices.SortFunc(selections, func(a, b loopNoticeSelection) int { return compareNoticeCursor(a.cursor, b.cursor) })
	notices := make([][]workspace.ScratchNotice, len(selections))
	for i, selection := range selections {
		notices[i], err = r.workspace.ScratchNotices(ctx, t.origin.ContextID, selection.cursor.ScratchID, uint64(selection.cursor.AfterSequence))
		if err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prepareHistoryLocked(ctx, reads, &ids, true, func(reads *historyReadSet) error {
		input.TaskResults, input.NoticeCursors = []loopTaskResult{}, []ledger.ContinuationNoticeCursor{}
		t.notices, t.owedNoticeCursors = nil, nil
		size := len(modelJSON(*input))
		for i, selection := range selections {
			if err := s.loopNoticeAuthority(ctx, reads, t.origin, selection); err != nil {
				if err == ErrDenied {
					continue
				}
				return err
			}
			after := selection.cursor.AfterSequence
			current := s.p.noticeCursors[scopeKey{t.origin.AgentID, t.origin.ContextID}]
			if at, found := slices.BinarySearchFunc(current, selection.cursor, compareNoticeCursor); found {
				after = max(after, current[at].AfterSequence)
			}
			for _, notice := range notices[i] {
				if notice.Sequence > through || notice.Sequence <= uint64(after) {
					continue
				}
				result := loopNoticeResult(notice)
				size += len(modelJSON(result)) + 1
				if size > maxObjectBytes {
					return ErrTooLarge
				}
				input.TaskResults = append(input.TaskResults, result)
				selection.cursor.AfterSequence = int64(notice.Sequence)
			}
			if selection.cursor.AfterSequence > after {
				input.NoticeCursors = append(input.NoticeCursors, selection.cursor)
				t.notices = append(t.notices, selection)
			}
		}
		slices.SortFunc(input.TaskResults, func(a, b loopTaskResult) int {
			if a.Sequence < b.Sequence {
				return -1
			}
			if a.Sequence > b.Sequence {
				return 1
			}
			return 0
		})
		if len(input.NoticeCursors) > 1024 || len(modelJSON(*input)) > maxObjectBytes {
			return ErrTooLarge
		}
		if err := s.verifyLoopNotices(ctx, reads, *input); err != nil {
			return err
		}
		t.owedNoticeCursors = slices.Clone(input.NoticeCursors)
		return nil
	})
}

// A current task assignment is necessary but not read permission. Root file
// authority or private context inspection is checked before scratch readiness.
func (s *Service) loopNoticeAuthority(ctx context.Context, reads *historyReadSet, origin ledger.Origin, selection loopNoticeSelection) error {
	task, found, err := s.contextTask(ctx, reads, origin, s.p.through)
	if err != nil {
		return err
	}
	if !found || task.record.TaskID != selection.task.TaskID || task.record.Assignment == nil || task.record.Assignment.AssignmentID != selection.task.AssignmentID || task.record.Assignment.Generation != selection.task.Generation || task.record.Assignment.Assignee != ledger.Binding(bindingOf(origin)) {
		return ErrDenied
	}
	target := ledger.Resource{Kind: "workspace", Binding: ledger.Binding(bindingOf(origin)), WorkspaceID: selection.cursor.WorkspaceID}
	if _, err := s.loopGrantLocked(ctx, reads, origin, "file.read", []ledger.Resource{target}, 0); err != nil {
		if err != ErrDenied {
			return err
		}
		private := ledger.Resource{Kind: "context", Binding: ledger.Binding(bindingOf(origin)), Scope: "context", Slots: []string{}}
		if _, err := s.loopGrantLocked(ctx, reads, origin, "context.inspect", []ledger.Resource{private}, 0); err != nil {
			return err
		}
	}
	if err := s.workspaceReady(ctx, reads, target, s.p.through); err != nil {
		return err
	}
	scratch, found := s.p.scratches[selection.cursor.ScratchID]
	if !found || scratch.closed || scratch.ContextID != origin.ContextID || scratch.WorkspaceID != selection.cursor.WorkspaceID || scratch.ParentScratchID != (uuid.UUID{}) {
		return ErrDenied
	}
	return nil
}

func renderLoopNotices(out *strings.Builder, input loopInputObject) error {
	if input.Schema != 2 || len(input.TaskResults) == 0 {
		return nil
	}
	const heading = "\nTask scratch results:\n"
	if out.Len() > providers.RequestLimit-len(heading) {
		return ErrTooLarge
	}
	out.WriteString(heading)
	for _, result := range input.TaskResults {
		line := modelJSON(result)
		if len(line)+3 > providers.RequestLimit-out.Len() {
			return ErrTooLarge
		}
		out.WriteString("- ")
		out.Write(line)
		out.WriteByte('\n')
	}
	return nil
}

func (s *Service) verifyLoopNotices(ctx context.Context, reads *historyReadSet, input loopInputObject) error {
	for _, result := range input.TaskResults {
		source, found, err := s.sourceRecord(ctx, reads, int64(result.Sequence), s.p.through)
		if err != nil {
			return err
		}
		if !found || source.Type != ledger.TypeScratchMergeBack {
			return ErrIntegrity
		}
		record, err := ledger.DecodeScratchMergeBack(source.Payload)
		if err != nil || record.Outcome == ledger.MergeBackPrepared || record.ContextID != input.Origin.ContextID {
			return ErrIntegrity
		}
		exact := loopNoticeResult(workspace.ScratchNotice{Sequence: source.Seq, MergeBackID: record.MergeBackID, ScratchID: record.ScratchID, ParentScratchID: record.ParentScratchID, AuthoringContextID: record.AuthoringContextID, Outcome: record.Outcome, WrittenPaths: record.WrittenPaths, ChangedPaths: record.ChangedPaths, ConflictSHA256: record.ConflictSHA256})
		if !sameScopedValue(exact, result) || !slices.ContainsFunc(input.NoticeCursors, func(cursor ledger.ContinuationNoticeCursor) bool {
			return cursor.WorkspaceID == record.WorkspaceID && cursor.ScratchID == record.ParentScratchID && cursor.AfterSequence >= int64(result.Sequence)
		}) {
			return ErrIntegrity
		}
	}
	return nil
}

func (s *Service) readLoopInput(ctx context.Context, origin ledger.Origin, ref ledger.ObjectRef) (loopInputObject, error) {
	var input loopInputObject
	raw, err := s.readLoopObject(ctx, origin, ref)
	if err != nil {
		return input, err
	}
	if err := decodeLoopInput(raw, &input); err != nil {
		return input, err
	}
	if input.Origin != origin {
		return input, ErrIntegrity
	}
	if input.Schema == 2 {
		s.mu.Lock()
		ids := submissionIDs{origin.AgentID, origin.SessionID, origin.ContextID}
		err = s.prepareHistoryLocked(ctx, &historyReadSet{}, &ids, true, func(reads *historyReadSet) error {
			return s.verifyLoopNotices(ctx, reads, input)
		})
		s.mu.Unlock()
	}
	return input, err
}

func decodeLoopInput(raw []byte, out *loopInputObject) error {
	// Digest fields use the frozen ledger parser, not array JSON unmarshalling.
	type inputWire struct {
		Schema    int           `json:"schema"`
		Kind      string        `json:"kind"`
		Origin    ledger.Origin `json:"origin"`
		TurnID    uuid.UUID     `json:"turn_id"`
		Text      string        `json:"text"`
		Artifacts []struct {
			SourceSequence int64  `json:"source_sequence"`
			SHA256         string `json:"sha256"`
			Bytes          int64  `json:"bytes"`
		} `json:"artifacts"`
		OtherWork []string `json:"other_work"`
	}
	var wire inputWire
	if json.Unmarshal(raw, &wire) != nil || wire.Schema != 1 && wire.Schema != 2 || wire.Kind != "owner_input" {
		return ErrIntegrity
	}
	*out = loopInputObject{Schema: wire.Schema, Kind: wire.Kind, Origin: wire.Origin, TurnID: wire.TurnID, Text: wire.Text, Artifacts: []ledger.ArtifactRef{}, OtherWork: wire.OtherWork}
	for _, ref := range wire.Artifacts {
		digest, err := ledger.ParseDigest(ref.SHA256)
		if err != nil {
			return ErrIntegrity
		}
		out.Artifacts = append(out.Artifacts, ledger.ArtifactRef{SourceSequence: ref.SourceSequence, SHA256: digest, Bytes: ref.Bytes})
	}
	if wire.Schema == 2 {
		var next struct {
			inputWire
			TaskResults   []json.RawMessage                 `json:"task_results"`
			NoticeCursors []ledger.ContinuationNoticeCursor `json:"notice_cursors"`
		}
		var keys map[string]json.RawMessage
		if decodeLoopJSON(raw, &next) != nil || json.Unmarshal(raw, &keys) != nil || len(keys) != 9 || next.Artifacts == nil || next.OtherWork == nil || next.TaskResults == nil || next.NoticeCursors == nil || len(next.NoticeCursors) > 1024 || !validID(wire.Origin.AgentID) || !validID(wire.Origin.SessionID) || !validID(wire.Origin.ContextID) || !validID(wire.TurnID) {
			return ErrIntegrity
		}
		out.TaskResults, out.NoticeCursors = []loopTaskResult{}, next.NoticeCursors
		for i, cursor := range out.NoticeCursors {
			if !validID(cursor.WorkspaceID) || !validID(cursor.ScratchID) || cursor.AfterSequence <= 0 || i > 0 && compareNoticeCursor(out.NoticeCursors[i-1], cursor) >= 0 {
				return ErrIntegrity
			}
		}
		latest := map[uuid.UUID]int64{}
		for _, raw := range next.TaskResults {
			var result struct {
				Sequence           uint64    `json:"sequence"`
				MergeBackID        uuid.UUID `json:"merge_back_id"`
				ScratchID          uuid.UUID `json:"scratch_id"`
				ParentScratchID    uuid.UUID `json:"parent_scratch_id"`
				AuthoringContextID uuid.UUID `json:"authoring_context_id"`
				Outcome            string    `json:"outcome"`
				WrittenPaths       []string  `json:"written_paths"`
				ChangedPaths       []string  `json:"changed_paths"`
				ConflictSHA256     *string   `json:"conflict_sha256,omitempty"`
			}
			var keys map[string]json.RawMessage
			if decodeLoopJSON(raw, &result) != nil || json.Unmarshal(raw, &keys) != nil || len(keys) != 8 && len(keys) != 9 || result.Sequence == 0 || result.Sequence > math.MaxInt64 || !validID(result.MergeBackID) || !validID(result.ScratchID) || !validID(result.ParentScratchID) || !validID(result.AuthoringContextID) || result.ScratchID == result.ParentScratchID || !loopNoticePaths(result.WrittenPaths) || !loopNoticePaths(result.ChangedPaths) {
				return ErrIntegrity
			}
			if len(out.TaskResults) != 0 && out.TaskResults[len(out.TaskResults)-1].Sequence >= result.Sequence {
				return ErrIntegrity
			}
			var digest ledger.Digest
			switch result.Outcome {
			case ledger.MergeBackConflict:
				if result.ConflictSHA256 == nil || len(result.ChangedPaths) != 0 {
					return ErrIntegrity
				}
				var err error
				digest, err = ledger.ParseDigest(*result.ConflictSHA256)
				if err != nil || digest == (ledger.Digest{}) {
					return ErrIntegrity
				}
			case ledger.MergeBackWriteFailed:
				if len(result.ChangedPaths) != 0 {
					return ErrIntegrity
				}
			case ledger.MergeBackFastForward, ledger.MergeBackMerged:
			default:
				return ErrIntegrity
			}
			if _, present := keys["conflict_sha256"]; present != (result.Outcome == ledger.MergeBackConflict) {
				return ErrIntegrity
			}
			out.TaskResults = append(out.TaskResults, loopTaskResult{result.Sequence, result.MergeBackID, result.ScratchID, result.ParentScratchID, result.AuthoringContextID, result.Outcome, result.WrittenPaths, result.ChangedPaths, digest})
			latest[result.ParentScratchID] = int64(result.Sequence)
		}
		for _, cursor := range out.NoticeCursors {
			if latest[cursor.ScratchID] != cursor.AfterSequence {
				return ErrIntegrity
			}
			delete(latest, cursor.ScratchID)
		}
		if len(latest) != 0 {
			return ErrIntegrity
		}
	}
	return nil
}

// The frozen F2 repository-path grammar is host-independent and less
// restrictive than the file tool's resource grammar; do not normalize it.
func loopNoticePaths(paths []string) bool {
	if paths == nil {
		return false
	}
	for i, path := range paths {
		if path == "" || strings.ContainsAny(path, "\x00\\") || i > 0 && paths[i-1] >= path {
			return false
		}
		for component := range strings.SplitSeq(path, "/") {
			if component == "" || component == "." || component == ".." {
				return false
			}
		}
	}
	return true
}

func (s *Service) readLoopObject(ctx context.Context, origin ledger.Origin, ref ledger.ObjectRef) ([]byte, error) {
	return s.readLoopObjectForOwner(ctx, origin, ref, nil)
}

func (s *Service) readLoopObjectForOwner(ctx context.Context, origin ledger.Origin, ref ledger.ObjectRef, owner *continuationState) ([]byte, error) {
	if ref.Bytes <= 0 || ref.Bytes > maxObjectBytes {
		return nil, ErrIntegrity
	}
	s.mu.Lock()
	reservation := scopedReservation{origin: origin}
	err := s.reserveScopedProcessLocked(&reservation)
	s.mu.Unlock()
	path, pathErr := s.scopePath(submissionIDs{origin.AgentID, origin.SessionID, origin.ContextID}, "context")
	defer func() { s.mu.Lock(); s.releaseScopedLocked(&reservation); s.mu.Unlock() }()
	if err != nil {
		return nil, err
	}
	if pathErr != nil {
		return nil, pathErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if owner != nil {
		if owner.Origin != origin {
			return nil, ErrContinuationStale
		}
		next, err := s.resumeFinishingClaim(ctx, *owner)
		if err != nil {
			return nil, err
		}
		*owner = next
	}
	return readObject(path, Head{SHA256: ref.SHA256, Bytes: ref.Bytes})
}
