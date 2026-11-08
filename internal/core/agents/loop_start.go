package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
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
	Schema    int                  `json:"schema"`
	Kind      string               `json:"kind"`
	Origin    ledger.Origin        `json:"origin"`
	TurnID    uuid.UUID            `json:"turn_id"`
	Text      string               `json:"text"`
	Artifacts []ledger.ArtifactRef `json:"artifacts"`
	OtherWork []string             `json:"other_work"`
}
type loopSource struct {
	Scope    string `json:"scope"`
	Slot     string `json:"slot"`
	Revision uint64 `json:"revision"`
	Head     Head   `json:"head"`
}

func (r *loopRuntime) start(ctx context.Context, reads *historyReadSet, ids submissionIDs, id uuid.UUID, input session.MessageSend) (_ *loopGroup, err error) {
	s := r.service
	setting, ok := r.settings[Binding{ids.agent, ids.context}]
	if !ok {
		return nil, ErrUnavailable
	}
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
	s.mu.Lock()
	err = s.prepareHistoryLocked(ctx, reads, &ids, true, func(reads *historyReadSet) error {
		grant, credential, sample, scopes = uuid.UUID{}, uuid.UUID{}, 0, nil
		var err error
		match, err = s.findCommand(ctx, reads, id, 0, s.p.through)
		if err != nil || match.found {
			return err
		}
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
		if match.loop == nil || match.loop.bound == nil || match.loop.ids != ids || match.loop.bound.TurnID != input.TurnID {
			return nil, ErrCommandMismatch
		}
		raw, err := s.readLoopObject(ctx, originOf(ids), match.loop.bound.InputObject)
		if err != nil {
			return nil, err
		}
		var prior loopInputObject
		if err := decodeLoopInput(raw, &prior); err != nil {
			return nil, err
		}
		if prior.Origin != originOf(ids) || prior.TurnID != input.TurnID || prior.Text != input.Text || !slices.Equal(prior.Artifacts, input.Artifacts) {
			return nil, ErrCommandMismatch
		}
		return match.loop, nil
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
	agent := &Agent{service: s, ids: ids}
	admissionID := uuid.NewV7()
	_, err = agent.StartTurn(ctx, TurnAdmission{CommandID: admissionID, GrantID: grant, TurnID: t.id, TaskBinding: setting.TaskBinding})
	if err != nil {
		return nil, err
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
	request, sources, err := r.render(ctx, t, inputObject)
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

func (r *loopRuntime) render(ctx context.Context, t *loopTurn, input loopInputObject) (providers.Request, []loopSource, error) {
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

func decodeLoopInput(raw []byte, out *loopInputObject) error {
	// Digest fields use the frozen ledger parser, not array JSON unmarshalling.
	var wire struct {
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
	if json.Unmarshal(raw, &wire) != nil || wire.Schema != 1 || wire.Kind != "owner_input" {
		return ErrIntegrity
	}
	*out = loopInputObject{Schema: 1, Kind: wire.Kind, Origin: wire.Origin, TurnID: wire.TurnID, Text: wire.Text, Artifacts: []ledger.ArtifactRef{}, OtherWork: wire.OtherWork}
	for _, ref := range wire.Artifacts {
		digest, err := ledger.ParseDigest(ref.SHA256)
		if err != nil {
			return ErrIntegrity
		}
		out.Artifacts = append(out.Artifacts, ledger.ArtifactRef{SourceSequence: ref.SourceSequence, SHA256: digest, Bytes: ref.Bytes})
	}
	return nil
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
