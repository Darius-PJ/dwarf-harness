package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sync"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
)

var (
	ErrHistory  = errors.New("invalid graph history")
	ErrRevision = errors.New("graph image changed")
	ErrRecovery = errors.New("graph recovery required")
	ErrClosed   = errors.New("graph service closed")
	ErrTooLarge = errors.New("graph page element too large")
)

// HistoryError identifies the rejected envelope and a stable rule, not prose.
// Rules are analysis_key, graph_digest, chunk_digest, checkpoint_cursor,
// commit_shape, record_envelope, source_binding, attempt_state, reservation,
// chunk_order, dangling, state_cap, counter and version.
type HistoryError struct {
	Seq  uint64
	Rule string
}

func (e *HistoryError) Error() string { return fmt.Sprintf("%s at %d: %s", ErrHistory, e.Seq, e.Rule) }
func (e *HistoryError) Unwrap() error { return ErrHistory }

type Options struct {
	Colony        uuid.UUID
	LogDir        string
	Appender      appender.Appender
	Durable       func() uint64
	Through       uint64
	Tools         ToolConfig
	ReadArtifacts func(context.Context, ArtifactRequest) (ArtifactBatch, error)
}

type Status struct {
	State          string
	Through        uint64
	ChainHash      string
	Version        int64
	Digest         ledger.Digest
	Attempt        uuid.UUID
	AttemptKey     ledger.Digest
	Charged        int64
	VerifiedCursor ledger.GraphCursor
	Pending        []uuid.UUID
	Reasons        []string
}

type ScopeView struct {
	Scope
	Latest       ledger.RecordRef
	GraphPending bool
}

type PageRequest struct {
	Version   int64
	Digest    ledger.Digest
	Through   uint64
	ChainHash string
	Kind      string
	After     []byte
	Limit     int
}

type Page struct {
	Status
	Scopes   []ScopeView
	Nodes    []ledger.GraphNode
	Edges    []ledger.GraphEdge
	Coverage []ledger.CoverageEntry
	Next     []byte
}

type serviceImage struct {
	status    Status
	state     State
	scopes    []ScopeView
	available bool
}

// Service owns no ledger writer or background worker. op serializes replay and
// explicit work; mu protects only the published image and cancellation handle.
type Service struct {
	op                             sync.Mutex
	mu                             sync.Mutex
	image                          *serviceImage
	closed, busy                   bool
	cancel                         context.CancelFunc
	opts                           Options
	deps                           serviceDeps
	replay                         *replayState
	configuration                  ledger.Digest
	epoch                          int64
	recovery, fenced               bool
	overLimit                      bool
	work                           *workState
	tools                          [5]toolSlot
	analyzer                       *Analyzer
	generations                    map[uuid.UUID]generationState
	generationMemory               uint64
	loadDue, epochDue, mismatchDue bool
}

func Open(options Options) (*Service, error) { return openService(options, serviceDeps{}) }

func openService(options Options, deps serviceDeps) (*Service, error) {
	if options.Colony[6]>>4 != 7 || options.Colony[8]&0xc0 != 0x80 || options.LogDir == "" || options.Through == ledger.ToEnd || options.Through == 0 && (options.Durable == nil || options.Appender == nil) {
		return nil, ErrInvalid
	}
	for _, target := range []**Tool{&options.Tools.Python, &options.Tools.Node, &options.Tools.TypeScriptParser} {
		if *target != nil {
			tool := **target
			if !filepath.IsAbs(tool.Path) || tool.SHA256 == (ledger.Digest{}) || !text(tool.Version, 128) {
				return nil, ErrInvalid
			}
			*target = &tool
		}
	}
	if options.Tools.Go != nil {
		goConfig := *options.Tools.Go
		if !validGoConfig(goConfig) {
			return nil, ErrInvalid
		}
		options.Tools.Go = &goConfig
	}
	if reader := options.ReadArtifacts; reader != nil {
		options.ReadArtifacts = func(ctx context.Context, request ArtifactRequest) (ArtifactBatch, error) {
			if analysisFence.Load() {
				return ArtifactBatch{}, ErrRecovery
			}
			batch, err := reader(ctx, request)
			if analysisFence.Load() {
				return ArtifactBatch{}, ErrRecovery
			}
			return batch, err
		}
	}
	s := &Service{opts: options, deps: deps, replay: newReplayState(), configuration: options.Tools.ConfigurationDigest(), generations: make(map[uuid.UUID]generationState), loadDue: true}
	if deps.retainedCap != nil {
		s.replay.retainedCap = *deps.retainedCap
	}
	if deps.nodeCap > 0 {
		s.replay.nodeCap = deps.nodeCap
	}
	if deps.edgeCap != nil {
		s.replay.edgeCap = *deps.edgeCap
	}
	s.analyzer = NewAnalyzer(Tools{})
	s.analyzer.configuration, s.analyzer.deps = s.configuration, deps
	if err := s.hydrate(context.Background(), nil, appender.Committed{}); err != nil {
		return nil, err
	}
	s.epoch = s.replay.lastEpoch
	s.epochDue = s.epoch != 0 && s.configuration != s.replay.lastConfiguration
	if s.epoch == 0 || s.configuration != s.replay.lastConfiguration {
		s.epoch++
	}
	s.recovery = s.replay.attempt != nil
	s.publish()
	return s, nil
}

func (s *Service) snapshot() (serviceImage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	image := *s.image
	image.status.Pending = slices.Clone(image.status.Pending)
	image.status.Reasons = slices.Clone(image.status.Reasons)
	if s.closed {
		image.status.State = "closed"
		return image, ErrClosed
	}
	if s.busy && image.available {
		image.status.State = "busy"
	}
	return image, nil
}

func (s *Service) publish() {
	selection := s.replay.next(s.configuration, s.epoch, s.rebuildReason())
	if selection.confirmed {
		s.loadDue, s.epochDue, s.mismatchDue = false, false, false
	}
	image := s.replay.view(selection)
	image.available = true
	if s.epoch != 0 && s.deps.projectionIdentity != nil {
		image.status.Version, image.status.Digest = s.deps.projectionIdentity(image.status.Version, image.status.Digest)
	}
	if s.recovery || s.fenced || analysisFence.Load() {
		image.status.State = "recovery_pending"
	}
	if s.overLimit {
		image = &serviceImage{status: Status{State: "unknown", Reasons: []string{ledger.ReasonStateCap}, Through: s.replay.through, ChainHash: s.replay.chainHash}}
	}
	s.mu.Lock()
	s.image = image
	s.mu.Unlock()
}

// Observe catches up only when the worker is idle. It never waits for its op
// lock, a callback, a child or an appender; busy observations use one old image.
func (s *Service) Observe(ctx context.Context) (Status, error) {
	image, err := s.snapshot()
	if err != nil {
		return image.status, err
	}
	if err := ctx.Err(); err != nil {
		return image.status, err
	}
	if !image.available {
		return image.status, nil
	}
	if !s.op.TryLock() {
		return image.status, nil
	}
	defer s.op.Unlock()
	if _, err := s.snapshot(); err != nil {
		return Status{State: "closed"}, err
	}
	if !s.fenced {
		if err := s.hydrate(ctx, nil, appender.Committed{}); err != nil {
			return image.status, err
		}
	}
	image, err = s.snapshot()
	return image.status, err
}

func (s *Service) Step(ctx context.Context) (Status, error) { return s.run(ctx, nil) }
func (s *Service) Resume(ctx context.Context, analysisID uuid.UUID) (Status, error) {
	return s.run(ctx, &analysisID)
}

func (s *Service) run(ctx context.Context, resume *uuid.UUID) (Status, error) {
	image, err := s.snapshot()
	if err != nil {
		return image.status, err
	}
	if analysisFence.Load() {
		image.status.State = "recovery_pending"
		return image.status, ErrRecovery
	}
	if !image.available {
		return image.status, nil
	}
	if s.opts.Through != 0 {
		return image.status, ErrClosed
	}
	if !s.op.TryLock() {
		image, _ = s.snapshot()
		return image.status, ErrBusy
	}
	defer s.op.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Status{State: "closed"}, ErrClosed
	}
	workContext, cancel := context.WithCancel(ctx)
	s.busy, s.cancel = true, cancel
	s.mu.Unlock()
	defer cancel()
	if s.fenced || analysisFence.Load() {
		err = ErrRecovery
	} else if err = s.hydrate(workContext, nil, appender.Committed{}); err == nil && !s.overLimit {
		err = s.drive(workContext, resume)
	}
	if errors.Is(err, errRetainedCap) {
		err = nil
	}
	s.publish()
	s.mu.Lock()
	s.busy, s.cancel = false, nil
	s.mu.Unlock()
	image, closed := s.snapshot()
	if err == nil {
		err = closed
	}
	return image.status, err
}

// Page binds both topology and the checked source frontier to one immutable
// image. Values may describe an older covered subject; ScopeView marks its lag.
func (s *Service) Page(request PageRequest) (Page, error) {
	image, err := s.snapshot()
	page := Page{Status: image.status}
	if err != nil {
		return page, err
	}
	if !image.available {
		return page, ErrRevision
	}
	if request.Limit < 1 || request.Limit > 256 {
		return page, ErrInvalid
	}
	if request.Version != image.status.Version || request.Digest != image.status.Digest || request.Through != image.status.Through || request.ChainHash != image.status.ChainHash {
		return page, ErrRevision
	}
	var count int
	var element func(int) ([]byte, any)
	switch request.Kind {
	case "scopes":
		count = len(image.scopes)
		element = func(i int) ([]byte, any) { v := image.scopes[i]; return v.Source.WorkspaceID[:], v }
	case "nodes":
		count = len(image.state.Nodes)
		element = func(i int) ([]byte, any) { v := image.state.Nodes[i]; return keyBytes(v.Key), v }
	case "edges":
		count = len(image.state.Edges)
		element = func(i int) ([]byte, any) { v := image.state.Edges[i]; return v.EdgeID[:], v }
	case "coverage":
		count = len(image.state.Coverage)
		element = func(i int) ([]byte, any) {
			v := image.state.Coverage[i]
			v.Reasons = slices.Clone(v.Reasons)
			return []byte(coverageKey(v)), v
		}
	default:
		return page, ErrInvalid
	}
	used, returned := 2, 0
	var last []byte
	for i := 0; i < count; i++ {
		key, value := element(i)
		if bytes.Compare(key, request.After) <= 0 {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return page, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if returned == request.Limit || used+len(encoded)+1 > 1<<20 {
			if returned == 0 {
				return page, ErrTooLarge
			}
			page.Next = slices.Clone(last)
			break
		}
		switch value := value.(type) {
		case ScopeView:
			page.Scopes = append(page.Scopes, value)
		case ledger.GraphNode:
			page.Nodes = append(page.Nodes, value)
		case ledger.GraphEdge:
			page.Edges = append(page.Edges, value)
		case ledger.CoverageEntry:
			page.Coverage = append(page.Coverage, value)
		}
		used += len(encoded) + 1
		returned++
		last = key
	}
	return page, nil
}

func (s *Service) Close() error {
	s.mu.Lock()
	alreadyClosed := s.closed
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.op.Lock()
	defer s.op.Unlock()
	if alreadyClosed {
		return ErrClosed
	}
	var err error
	for i := range s.tools {
		if s.tools[i].verification != nil {
			err = errors.Join(err, s.tools[i].verification.Close())
		}
	}
	return err
}

// hydrate uses exactly one bounded reader. Unconsumed payloads remain opaque;
// their verified envelopes still supply the final checked chain hash.
func (s *Service) hydrate(ctx context.Context, own []ledger.Draft, acknowledged appender.Committed) error {
	s.checkProjection()
	mark := s.opts.Through
	if mark == 0 {
		mark = s.opts.Durable()
	}
	if mark == ledger.ToEnd || mark < s.replay.through || acknowledged.Last > mark {
		return ErrHistory
	}
	if own != nil {
		mark = acknowledged.Last
	}
	if mark == s.replay.through {
		if own != nil {
			return ErrHistory
		}
		s.publish()
		return nil
	}
	reader, err := ledger.OpenReader(s.opts.LogDir, ledger.ReadOptions{Colony: s.opts.Colony, After: s.replay.through, Through: mark, Consume: graphConsumption(), AllTypes: true})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrHistory, err)
	}
	defer reader.Close()
	next := s.replay.clone()
	found := own == nil
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		commit, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: %w", ErrHistory, err)
		}
		if own != nil && commit.First == acknowledged.First {
			if commit.Last != acknowledged.Last || len(commit.Records) != len(own) {
				return ErrHistory
			}
			for i, draft := range own {
				record := commit.Records[i]
				if record.Type != draft.Type || record.TypeVersion != draft.TypeVersion || !bytes.Equal(record.Payload, draft.Payload) {
					return ErrHistory
				}
			}
			found = true
		}
		priorThrough, priorHash := next.through, next.chainHash
		if err := next.reduce(commit); err != nil {
			if errors.Is(err, errRetainedCap) {
				s.stopProjection(priorThrough, priorHash)
				return nil
			}
			return err
		}
	}
	if !found || next.through != mark {
		return ErrHistory
	}
	if s.epoch != 0 && next.lastRebuild != s.replay.lastRebuild {
		s.loadDue, s.epochDue, s.mismatchDue = false, false, false
	}
	s.replay = next
	s.publish()
	return nil
}

func (s *Service) append(ctx context.Context, drafts []ledger.Draft) error {
	if s.deps.beforeAppend != nil {
		s.deps.beforeAppend(drafts)
	}
	if analysisFence.Load() {
		return ErrRecovery
	}
	committed, err := s.opts.Appender.Append(ctx, drafts)
	if err != nil {
		if errors.Is(err, appender.ErrUncertain) || errors.Is(err, appender.ErrPoisoned) {
			s.fenced = true
			return fmt.Errorf("%w: %w", ErrRecovery, err)
		}
		if s.work != nil {
			// A refused output may already have advanced private cursors and
			// hashes. Reconstruct like Resume; never reuse uncommitted output.
			s.work = s.newWork(s.replay.attempt.started)
		}
		return err
	}
	// A cancelled caller cannot turn an acknowledged append into an unhydrated
	// retry. The reader is finite, bounded by the acknowledged commit end.
	if err := s.hydrate(context.WithoutCancel(ctx), drafts, committed); err != nil {
		s.fenced = true
		return fmt.Errorf("%w: %w", ErrRecovery, err)
	}
	if s.overLimit {
		return errRetainedCap
	}
	return nil
}

func (s *Service) stopProjection(through uint64, chainHash string) {
	retainedCap, nodeCap, edgeCap := s.replay.retainedCap, s.replay.nodeCap, s.replay.edgeCap
	s.replay = newReplayState()
	s.replay.retainedCap, s.replay.nodeCap, s.replay.edgeCap = retainedCap, nodeCap, edgeCap
	s.replay.through, s.replay.chainHash = through, chainHash
	s.work, s.generations, s.overLimit = nil, nil, true
	s.generationMemory = 0
	for i := range s.tools {
		if s.tools[i].verification != nil {
			_ = s.tools[i].verification.Close()
			s.tools[i].verification = nil
		}
	}
	s.publish()
}
