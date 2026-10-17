package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type activationClock struct {
	mu  sync.Mutex
	ns  int64
	err error
}

func (c *activationClock) now() (time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Unix(0, c.ns), c.err
}

func (c *activationClock) set(ns int64, err error) {
	c.mu.Lock()
	c.ns, c.err = ns, err
	c.mu.Unlock()
}

type activationFixture struct {
	store     *ledgerstore.Store
	svc       *Service
	clock     *activationClock
	colony    string
	target    ActivationTarget
	artifacts map[ledger.Digest][]byte
}

// These control histories use real durable stores, but no repository or Git work.
func newActivationFixture(t *testing.T) *activationFixture {
	t.Helper()
	f := &activationFixture{colony: t.TempDir(), clock: &activationClock{ns: 100}}
	var err error
	f.store, err = ledgerstore.Create(f.colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	f.target, f.artifacts = activationTestArtifacts(t)
	digest, _, err := f.store.PublishBody(context.Background(), bytes.NewReader([]byte(`{"format":"dwarf-harness/capture-inputs","version":2}`)))
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range f.target.WorkspacePolicies {
		pin := uuid.NewV7()
		drafts := []ledger.Draft{
			{Type: ledger.TypeWorkspaceRegistered, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: encoded(t, ledger.WorkspaceRegistered{WorkspaceID: policy.WorkspaceID, CommonDir: filepath.ToSlash(filepath.Join(f.colony, policy.WorkspaceID.String(), "missing.git")), ObjectFormat: "sha1"})},
			{Type: ledger.TypeCaptureInputsPinned, TypeVersion: 1, Actor: ledger.ActorOwner, BodyRefs: []ledger.Digest{digest}, Payload: encoded(t, ledger.CaptureInputsPinned{WorkspaceID: policy.WorkspaceID, Revision: 1, CaptureInputsSHA256: digest, PinID: pin})},
			{Type: ledger.TypeBodyPin, TypeVersion: 1, Actor: ledger.ActorOwner, BodyRefs: []ledger.Digest{digest}, Payload: encoded(t, ledger.BodyPin{PinID: pin})},
		}
		if _, err := f.store.Append(context.Background(), drafts); err != nil {
			t.Fatal(err)
		}
	}
	f.svc, _, err = openService(context.Background(), f.store, f.colony, serviceConfig(), serviceDeps{activationNow: f.clock.now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.svc.Close(); err != nil {
			t.Error(err)
		}
		if err := f.store.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f *activationFixture) request() ActivationRequest {
	return ActivationRequest{RequestID: uuid.NewV7(), OwnerApprovalID: uuid.NewV7(), Actor: ledger.ActorOwner, Target: f.target, Schedule: ActivationSchedule{Mode: ledger.ScheduleNotBefore, NotBefore: time.Unix(0, 1000).UTC().Format(time.RFC3339Nano)}, Artifacts: f.artifacts}
}

func mustActivation(t *testing.T, s *Service, r ActivationRequest) ActivationState {
	t.Helper()
	state, err := s.RequestActivation(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func activationStatus(t *testing.T, s *Service, id uuid.UUID) ActivationState {
	t.Helper()
	state, err := s.ActivationStatus(context.Background(), ledger.ActorOwner, id)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func assertActivationRetained(t *testing.T, f *activationFixture) {
	t.Helper()
	for digest, want := range f.artifacts {
		got, err := f.store.ReadBody(context.Background(), digest)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("approved bytes lost: %s %v", digest, err)
		}
	}
	for _, commit := range serviceCommits(t, f.store) {
		for _, record := range commit.Records {
			if record.Type == ledger.TypeBodyUnpin {
				t.Fatal("activation unpinned an approval")
			}
		}
	}
}

func TestActivationOwnerAndExactRetention(t *testing.T) {
	t.Parallel()
	f := newActivationFixture(t)
	r := f.request()
	r.Actor = ledger.ActorAgent
	before := f.store.Durable()
	if _, err := f.svc.RequestActivation(context.Background(), r); !errors.Is(err, ErrNotBound) {
		t.Fatalf("non-owner: %v", err)
	}
	if f.store.Durable() != before {
		t.Fatal("non-owner wrote")
	}
	r.Actor = ledger.ActorOwner
	f.svc.deps.beforeStoreAppend = func(d []ledger.Draft) {
		if d[0].Type != ledger.TypePolicyActivationRequested {
			return
		}
		for digest, want := range f.artifacts {
			got, err := f.store.ReadBody(context.Background(), digest)
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("request preceded publication: %v", err)
			}
		}
	}
	state := mustActivation(t, f.svc, r)
	commits := serviceCommits(t, f.store)
	requestCommit := commits[len(commits)-1]
	if len(requestCommit.Records) != 2 || requestCommit.Records[1].Type != ledger.TypeBodyPin || state.RequestRecordHash.String() != requestCommit.Records[0].Hash || state.Revision != requestCommit.First {
		t.Fatalf("approval identity or atomic retention: %+v", state)
	}
	assertActivationRetained(t, f)
	replacement := f.request()
	state, err := f.svc.ReplaceActivation(context.Background(), ActivationReplacement{Request: replacement, ExpectedPendingID: r.RequestID, ExpectedPendingRevision: state.Revision})
	if err != nil {
		t.Fatal(err)
	}
	assertActivationRetained(t, f)
	if _, err := f.svc.CancelActivation(context.Background(), ActivationCancellation{Actor: ledger.ActorOwner, ExpectedPendingID: replacement.RequestID, ExpectedPendingRevision: state.Revision}); err != nil {
		t.Fatal(err)
	}
	assertActivationRetained(t, f)
	r = f.request()
	mustActivation(t, f.svc, r)
	f.clock.set(1200, nil)
	if err := f.svc.advanceActivation(context.Background()); err != nil {
		t.Fatal(err)
	}
	state = activationStatus(t, f.svc, r.RequestID)
	if state.State != "applied" || state.Applied.EffectiveUTCNS != 1200 {
		t.Fatalf("switch: %+v", state)
	}
	assertActivationRetained(t, f)
	for _, actor := range []ledger.Actor{ledger.ActorAgent, ledger.ActorCore} {
		if _, err := f.svc.ActivationStatus(context.Background(), actor, r.RequestID); !errors.Is(err, ErrNotBound) {
			t.Fatalf("status leaked: %v", err)
		}
		if _, err := f.svc.ActivePolicy(context.Background(), actor); !errors.Is(err, ErrNotBound) {
			t.Fatalf("manifest leaked: %v", err)
		}
	}
	active, err := f.svc.ActivePolicy(context.Background(), ledger.ActorOwner)
	if err != nil {
		t.Fatal(err)
	}
	active.Target.WorkspacePolicies[0].CheckPolicyHash = ledger.Digest{99}
	state.Applied.Target.WorkspacePolicies[0].CheckPolicyHash = ledger.Digest{99}
	again, err := f.svc.ActivePolicy(context.Background(), ledger.ActorOwner)
	if err != nil || again.Target.WorkspacePolicies[0] != f.target.WorkspacePolicies[0] {
		t.Fatal("query borrowed manifest storage")
	}
}

type activationUnreadableLogStore struct {
	workspaceStore
	logDir string
}

func (s activationUnreadableLogStore) LogDir() string { return s.logDir }

func TestActivationHydrationFailurePoisonsUntilReplay(t *testing.T) {
	t.Parallel()
	f := newActivationFixture(t)
	request := f.request()
	before := f.store.Durable()
	f.svc.store = activationUnreadableLogStore{workspaceStore: f.store, logDir: t.TempDir()}
	if _, err := f.svc.RequestActivation(context.Background(), request); !errors.Is(err, ErrPoisoned) {
		t.Fatalf("acknowledged commit with unreadable envelopes: %v", err)
	}
	if f.store.Durable() != before+2 {
		t.Fatal("acknowledged request was lost")
	}
	if _, err := f.svc.ActivationStatus(context.Background(), ledger.ActorOwner, request.RequestID); !errors.Is(err, ErrPoisoned) {
		t.Fatalf("poisoned service remained usable: %v", err)
	}
	if err := f.svc.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := openService(context.Background(), f.store, f.colony, serviceConfig(), serviceDeps{activationNow: f.clock.now})
	if err != nil {
		t.Fatal(err)
	}
	f.svc = reopened
	state := activationStatus(t, f.svc, request.RequestID)
	if state.State != "pending" || state.Revision != before+1 || state.RequestRecordHash == (ledger.Digest{}) {
		t.Fatalf("replayed acknowledgement: %+v", state)
	}
	request.Artifacts = nil
	replayed := mustActivation(t, f.svc, request)
	if replayed.RequestRecordHash != state.RequestRecordHash || f.store.Durable() != before+2 {
		t.Fatal("reopened retry duplicated the acknowledged request")
	}
	if _, err := f.svc.CancelActivation(context.Background(), ActivationCancellation{Actor: ledger.ActorOwner, ExpectedPendingID: request.RequestID, ExpectedPendingRevision: state.Revision}); err != nil {
		t.Fatal(err)
	}
	assertActivationRetained(t, f)
}

// Publication is paused outside Service.mu, allowing real concurrent controls.
type activationPublishingStore struct {
	workspaceStore
	once             sync.Once
	entered, release chan struct{}
}

func (s *activationPublishingStore) PublishBody(ctx context.Context, r io.Reader) (ledger.Digest, int64, error) {
	s.once.Do(func() { close(s.entered); <-s.release })
	return s.workspaceStore.PublishBody(ctx, r)
}

func TestActivationSameIDAndOnePending(t *testing.T) {
	t.Parallel()
	f := newActivationFixture(t)
	r := f.request()
	wrapped := &activationPublishingStore{workspaceStore: f.store, entered: make(chan struct{}), release: make(chan struct{})}
	f.svc.store = wrapped
	done := make(chan error, 1)
	go func() { _, err := f.svc.RequestActivation(context.Background(), r); done <- err }()
	<-wrapped.entered
	_, err := f.svc.RequestActivation(context.Background(), r)
	assertBusy(t, err, "activation_in_flight")
	close(wrapped.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	state := activationStatus(t, f.svc, r.RequestID)
	before := f.store.Durable()
	f.clock.set(0, errors.New("unavailable"))
	replay := r
	replay.Artifacts = nil // Same binding is recognized before loaders or time.
	replay.Schedule.NotBefore = time.Unix(0, 1000).UTC().Format("2006-01-02T15:04:05.000000000Z")
	got, err := f.svc.RequestActivation(context.Background(), replay)
	if err != nil || got.RequestRecordHash != state.RequestRecordHash || got.Revision != state.Revision || f.store.Durable() != before {
		t.Fatalf("idempotent replay: %+v %v", got, err)
	}
	replay.OwnerApprovalID = uuid.NewV7()
	if _, err := f.svc.RequestActivation(context.Background(), replay); !errors.Is(err, ErrActivationMismatch) {
		t.Fatalf("changed binding: %v", err)
	}
	if _, err := f.svc.RequestActivation(context.Background(), f.request()); err == nil {
		t.Fatal("second pending admitted")
	} else {
		assertBusy(t, err, "activation_pending")
	}
	f.clock.set(1200, nil)
	if err := f.svc.advanceActivation(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := activationStatus(t, f.svc, r.RequestID).Applied.RecordHash
	for _, hash := range []ledger.Digest{{}, {77}} {
		r = f.request()
		r.Schedule = ActivationSchedule{Mode: ledger.ScheduleImmediate}
		r.ExpectedActiveRecordHash = hash
		before := f.store.Durable()
		_, err := f.svc.RequestActivation(context.Background(), r)
		want := ErrRevision
		if hash != (ledger.Digest{}) {
			want = ErrInvalid
		}
		if !errors.Is(err, want) || f.store.Durable() != before {
			t.Fatalf("expected hash %s: %v", hash, err)
		}
		r.ExpectedActiveRecordHash = first
		state = mustActivation(t, f.svc, r)
		if _, err := f.svc.CancelActivation(context.Background(), ActivationCancellation{Actor: ledger.ActorOwner, ExpectedPendingID: r.RequestID, ExpectedPendingRevision: state.Revision}); err != nil {
			t.Fatal(err)
		}
	}
	r = f.request()
	r.Schedule = ActivationSchedule{Mode: ledger.ScheduleImmediate}
	r.ExpectedActiveRecordHash = first
	mustActivation(t, f.svc, r)
	if err := f.svc.advanceActivation(context.Background()); err != nil {
		t.Fatal(err)
	}
	r = f.request()
	r.Schedule = ActivationSchedule{Mode: ledger.ScheduleImmediate}
	r.ExpectedActiveRecordHash = first
	before = f.store.Durable()
	if _, err := f.svc.RequestActivation(context.Background(), r); !errors.Is(err, ErrRevision) || f.store.Durable() != before {
		t.Fatalf("stale known manifest: %v", err)
	}
	active, err := f.svc.ActivePolicy(context.Background(), ledger.ActorOwner)
	if err != nil {
		t.Fatal(err)
	}
	r.ExpectedActiveRecordHash = active.RecordHash
	mustActivation(t, f.svc, r)
}

func TestActivationReplacementCancellationCAS(t *testing.T) {
	t.Parallel()
	f := newActivationFixture(t)
	r := f.request()
	state := mustActivation(t, f.svc, r)
	c := ActivationCancellation{Actor: ledger.ActorOwner, ExpectedPendingID: r.RequestID, ExpectedPendingRevision: state.Revision - 1}
	if _, err := f.svc.CancelActivation(context.Background(), c); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale cancellation: %v", err)
	}
	replace := ActivationReplacement{Request: f.request(), ExpectedPendingID: r.RequestID, ExpectedPendingRevision: state.Revision - 1}
	if _, err := f.svc.ReplaceActivation(context.Background(), replace); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale replacement: %v", err)
	}
	replace.ExpectedPendingRevision = state.Revision
	wrapped := &activationPublishingStore{workspaceStore: f.store, entered: make(chan struct{}), release: make(chan struct{})}
	f.svc.store = wrapped
	done := make(chan error, 1)
	go func() { _, err := f.svc.ReplaceActivation(context.Background(), replace); done <- err }()
	<-wrapped.entered
	c.ExpectedPendingRevision = state.Revision
	if _, err := f.svc.CancelActivation(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	close(wrapped.release)
	if err := <-done; !errors.Is(err, ErrRevision) {
		t.Fatalf("replacement did not repeat CAS: %v", err)
	}
	if _, err := f.svc.ActivationStatus(context.Background(), ledger.ActorOwner, replace.Request.RequestID); !errors.Is(err, ErrUnknownActivation) {
		t.Fatalf("loser consumed identity: %v", err)
	}
	state = mustActivation(t, f.svc, replace.Request)
	next := f.request()
	before := len(serviceCommits(t, f.store))
	got, err := f.svc.ReplaceActivation(context.Background(), ActivationReplacement{Request: next, ExpectedPendingID: state.RequestID, ExpectedPendingRevision: state.Revision})
	if err != nil {
		t.Fatal(err)
	}
	commits := serviceCommits(t, f.store)
	last := commits[len(commits)-1]
	if len(commits) != before+1 || len(last.Records) != 3 {
		t.Fatal("replacement was not one commit")
	}
	ended, err := ledger.DecodePolicyActivationEnded(last.Records[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	requested, err := ledger.DecodePolicyActivationRequested(last.Records[1].Payload)
	if err != nil || ended.ExpectedPendingRevision != int64(state.Revision) || requested.ReplacesRevision != ended.ExpectedPendingRevision || requested.ReplacesRequestID != ended.RequestID || ended.ReplacementRequestID != next.RequestID || got.Revision != last.First+1 {
		t.Fatalf("replacement CAS binding: %+v %+v %v", ended, requested, err)
	}
}

func TestActivationReapprovalAfterCancellation(t *testing.T) {
	t.Parallel()
	f := newActivationFixture(t)
	r := f.request()
	state := mustActivation(t, f.svc, r)
	if _, err := f.svc.CancelActivation(context.Background(), ActivationCancellation{Actor: ledger.ActorOwner, ExpectedPendingID: r.RequestID, ExpectedPendingRevision: state.Revision}); err != nil {
		t.Fatal(err)
	}
	next := f.request()
	next.OwnerApprovalID = r.OwnerApprovalID
	if _, err := f.svc.RequestActivation(context.Background(), next); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reused approval: %v", err)
	}
	next.OwnerApprovalID = uuid.NewV7()
	mustActivation(t, f.svc, next)
	var pins []uuid.UUID
	for _, c := range serviceCommits(t, f.store) {
		for _, d := range c.Records {
			if d.Type == ledger.TypePolicyActivationRequested {
				p, _ := ledger.DecodePolicyActivationRequested(d.Payload)
				pins = append(pins, p.PinID)
			}
		}
	}
	if len(pins) != 2 || pins[0] == pins[1] {
		t.Fatal("reapproval reused approval pin")
	}
	assertActivationRetained(t, f)
}

func TestActivationUnavailableTarget(t *testing.T) {
	t.Parallel()
	f := newActivationFixture(t)
	r := f.request()
	state := mustActivation(t, f.svc, r)
	f.clock.set(1000, nil)
	f.svc.mu.Lock()
	if err := f.svc.changeActivationHoldLocked(context.Background(), ledger.HoldDue, 1000); err != nil {
		t.Fatal(err)
	}
	f.svc.mu.Unlock()
	digest := r.Target.WorkspacePolicies[0].CheckPolicyHash
	body := f.artifacts[digest]
	bodyPath := filepath.Join(f.colony, "bodies", digest.String()[:2], digest.String())
	if err := os.Remove(bodyPath); err != nil {
		t.Fatal(err)
	}
	before := f.store.Durable()
	if err := f.svc.advanceActivation(context.Background()); err != nil {
		t.Fatal(err)
	}
	state = activationStatus(t, f.svc, state.RequestID)
	if state.State != "pending" || state.Hold != ledger.HoldDue || state.Reason != "target_unavailable" || f.store.Durable() != before {
		t.Fatalf("missing target switched: %+v", state)
	}
	if _, _, err := f.store.PublishBody(context.Background(), bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.advanceActivation(context.Background()); err != nil {
		t.Fatal(err)
	}
	if activationStatus(t, f.svc, r.RequestID).State != "applied" {
		t.Fatal("restored bytes did not unblock switch")
	}
}

func TestActivationClockHoldAndRecovery(t *testing.T) {
	t.Parallel()
	for _, guarded := range []bool{false, true} {
		t.Run(map[bool]string{false: "advance", true: "guard"}[guarded], func(t *testing.T) {
			t.Parallel()
			f := newActivationFixture(t)
			r := f.request()
			mustActivation(t, f.svc, r)
			// Record a trusted in-process observation above admission, then regress.
			f.clock.set(500, nil)
			f.svc.mu.Lock()
			_, trusted := f.svc.sampleActivationTimeLocked()
			f.svc.mu.Unlock()
			if !trusted {
				t.Fatal("trusted sample refused")
			}
			f.clock.set(499, nil)
			if err := f.svc.advanceActivation(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := activationStatus(t, f.svc, r.RequestID); got.Hold != ledger.HoldClockUntrusted {
				t.Fatalf("rollback bypassed floor: %+v", got)
			}
			f.clock.set(600, nil)
			if guarded {
				f.svc.mu.Lock()
				_, err := f.svc.guardActivationIntentLocked(r.Target.WorkspacePolicies[0].WorkspaceID)
				f.svc.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			} else if err := f.svc.advanceActivation(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := activationStatus(t, f.svc, r.RequestID); got.Hold != ledger.HoldNone {
				t.Fatalf("trust recovery: %+v", got)
			}
			f.clock.set(1200, nil)
			if err := f.svc.advanceActivation(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := activationStatus(t, f.svc, r.RequestID)
			if got.State != "applied" || got.Applied.EffectiveUTCNS != 1200 {
				t.Fatalf("backdated or missing switch: %+v", got)
			}
		})
	}
}

func activationTargetFor(t *testing.T, ids ...uuid.UUID) (ActivationTarget, map[ledger.Digest][]byte) {
	t.Helper()
	target, artifacts := activationTestArtifacts(t)
	policy := target.WorkspacePolicies[0]
	target.WorkspacePolicies = nil
	for _, id := range ids {
		p := policy
		p.WorkspaceID = id
		target.WorkspacePolicies = append(target.WorkspacePolicies, p)
	}
	slices.SortFunc(target.WorkspacePolicies, func(a, b ledger.WorkspacePolicy) int { return bytes.Compare(a.WorkspaceID[:], b.WorkspaceID[:]) })
	return target, artifacts
}

func TestActivationSingleDueWakeup(t *testing.T) {
	t.Parallel()
	if delay := activationDelay(math.MaxInt64, math.MinInt64); delay != time.Duration(math.MaxInt64) {
		t.Fatalf("signed endpoint delay overflowed: %v", delay)
	}
	if delay := activationDelay(math.MaxInt64, math.MaxInt64-1); delay != time.Nanosecond {
		t.Fatalf("adjacent endpoint delay = %v", delay)
	}
	synctest.Test(t, func(t *testing.T) {
		f := newActivationFixture(t)
		defer f.store.Close()
		defer f.svc.Close()
		samples := 0
		f.svc.deps.activationNow = func() (time.Time, error) { samples++; return time.Now(), nil }
		start := time.Now()
		r := f.request()
		r.Schedule.NotBefore = start.Add(time.Hour).Format(time.RFC3339Nano)
		state := mustActivation(t, f.svc, r)
		if err := f.svc.resumeActivationControls(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		next := f.request()
		next.Schedule.NotBefore = start.Add(2 * time.Hour).Format(time.RFC3339Nano)
		if _, err := f.svc.ReplaceActivation(context.Background(), ActivationReplacement{Request: next, ExpectedPendingID: r.RequestID, ExpectedPendingRevision: state.Revision}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		before := f.store.Durable()
		f.svc.mu.Lock()
		samplesBefore := samples
		f.svc.mu.Unlock()
		<-time.NewTimer(time.Hour).C
		synctest.Wait()
		f.svc.mu.Lock()
		samplesAfter := samples
		f.svc.mu.Unlock()
		if f.store.Durable() != before || samplesAfter != samplesBefore {
			t.Fatal("replacement left the old deadline armed")
		}
		<-time.NewTimer(time.Hour).C
		synctest.Wait()
		state = activationStatus(t, f.svc, next.RequestID)
		if state.State != "applied" || state.Applied.EffectiveUTCNS != start.Add(2*time.Hour).UnixNano() {
			t.Fatalf("single due timer failed: %+v", state)
		}
		due, applied := 0, 0
		for _, c := range serviceCommits(t, f.store) {
			for _, d := range c.Records {
				if d.Type == ledger.TypePolicyActivationHoldChanged {
					p, _ := ledger.DecodePolicyActivationHoldChanged(d.Payload)
					if p.Hold == ledger.HoldDue {
						due++
					}
				}
				if d.Type == ledger.TypePolicyActivated {
					applied++
				}
			}
		}
		if due != 1 || applied != 1 {
			t.Fatalf("duplicate deadline: due=%d applied=%d", due, applied)
		}
		// A regressed sample must not arm a timer which manufactures clock trust.
		active, err := f.svc.ActivePolicy(context.Background(), ledger.ActorOwner)
		if err != nil {
			t.Fatal(err)
		}
		r = f.request()
		r.ExpectedActiveRecordHash = active.RecordHash
		r.Schedule.NotBefore = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
		mustActivation(t, f.svc, r)
		synctest.Wait()
		f.svc.mu.Lock()
		samples = 0
		f.svc.deps.activationNow = func() (time.Time, error) { samples++; return start, nil }
		f.svc.wakeActivationLocked()
		f.svc.mu.Unlock()
		synctest.Wait()
		before = f.store.Durable()
		f.svc.mu.Lock()
		samplesBefore = samples
		f.svc.mu.Unlock()
		// Cross any incorrectly armed deadline without saturating fake nanotime.
		<-time.NewTimer(time.Duration(math.MaxInt64 / 2)).C
		synctest.Wait()
		f.svc.mu.Lock()
		samplesAfter = samples
		f.svc.mu.Unlock()
		if samplesAfter != samplesBefore {
			t.Fatal("regressed clock armed a polling timer")
		}
		if f.store.Durable() != before || activationStatus(t, f.svc, r.RequestID).Hold != ledger.HoldClockUntrusted {
			t.Fatal("regressed clock scheduled eligibility")
		}
	})
}

func activationForWorkspace(t *testing.T, f *serviceFixture, clock *activationClock) ActivationRequest {
	t.Helper()
	f.svc.mu.Lock()
	f.svc.deps.activationNow = clock.now
	f.svc.mu.Unlock()
	target, artifacts := activationTargetFor(t, f.w.ID)
	return ActivationRequest{RequestID: uuid.NewV7(), OwnerApprovalID: uuid.NewV7(), Actor: ledger.ActorOwner, Target: target, Artifacts: artifacts, Schedule: ActivationSchedule{Mode: ledger.ScheduleNotBefore, NotBefore: time.Unix(0, 1000).UTC().Format(time.RFC3339Nano)}}
}

func prepareActivationPromotion(t *testing.T, f *serviceFixture, name string) (Request, ledger.GenerationDerived) {
	t.Helper()
	scratch := mustScratch(t, f, uuid.NewV7())
	writeTestFile(t, filepath.Join(scratch.Dir, name+".txt"), []byte(name+"\n"))
	r := rootRequest(scratch)
	mustAdmit(t, f, r)
	mustIntegrate(t, f)
	return r, currentGeneration(t, f, r)
}

func TestActivationIntentAdmissionLinearization(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, order := range []string{"hold_first", "intent_first"} {
		t.Run(order, func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			r, generation := prepareActivationPromotion(t, f, "linearization")
			clock := &activationClock{ns: 100}
			activation := activationForWorkspace(t, f, clock)
			mustActivation(t, f.svc, activation)
			// A durable clock hold permits guarded trust recovery without a timer.
			clock.set(-1, errors.New("not trusted yet"))
			if err := f.svc.advanceActivation(context.Background()); err != nil {
				t.Fatal(err)
			}
			clock.set(500, nil)
			f.svc.mu.Lock()
			f.svc.deps.activationNow = func() (time.Time, error) {
				if f.svc.mu.TryLock() {
					f.svc.mu.Unlock()
					t.Error("activation clock guard escaped sequencer lock")
				}
				return clock.now()
			}
			f.svc.mu.Unlock()
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			step := "intent"
			if order == "intent_first" {
				step = "cas"
			}
			f.svc.deps.beforeFinalizeStep = func(at string) {
				if at == step {
					close(entered)
					<-release
				}
			}
			done := make(chan error, 1)
			go func() {
				_, err := f.svc.finalize(context.Background(), r.RequestID, generation.GenerationID)
				done <- err
			}()
			<-entered
			clock.set(1200, nil)
			if order == "intent_first" {
				if err := f.svc.advanceActivation(context.Background()); err != nil {
					t.Fatal(err)
				}
				if activationStatus(t, f.svc, activation.RequestID).State != "pending" {
					t.Fatal("switch ignored durable intent")
				}
			} else {
				f.svc.mu.Lock()
				err := f.svc.changeActivationHoldLocked(context.Background(), ledger.HoldDue, 1200)
				f.svc.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			unblock()
			err := <-done
			if order == "hold_first" {
				assertUnavailable(t, err, "activation_pending")
				for _, c := range serviceCommits(t, f.store) {
					for _, d := range c.Records {
						if d.Type == ledger.TypeFinalizationIntended {
							t.Fatal("held finalizer appended intent")
						}
					}
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if err := f.svc.advanceActivation(context.Background()); err != nil {
					t.Fatal(err)
				}
				if activationStatus(t, f.svc, activation.RequestID).State != "applied" {
					t.Fatal("resolved intent did not unblock cutover")
				}
			}
		})
	}
}

func TestActivationIntentBindsActualManifest(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	r, _ := prepareActivationPromotion(t, f, "legacy")
	mustFinalize(t, f, r)
	clock := &activationClock{ns: 100}
	activation := activationForWorkspace(t, f, clock)
	activation.Schedule = ActivationSchedule{Mode: ledger.ScheduleImmediate}
	mustActivation(t, f.svc, activation)
	if err := f.svc.advanceActivation(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := f.svc.ActivePolicy(context.Background(), ledger.ActorOwner)
	if err != nil {
		t.Fatal(err)
	}
	r, _ = prepareActivationPromotion(t, f, "first")
	mustFinalize(t, f, r)
	r, _ = prepareActivationPromotion(t, f, "second")
	f.svc.deps.beforeFinalizeStep = func(step string) {
		if step != "intent" {
			return
		}
		activation.RequestID, activation.OwnerApprovalID = uuid.NewV7(), uuid.NewV7()
		activation.ExpectedActiveRecordHash = first.RecordHash
		mustActivation(t, f.svc, activation)
		if err := f.svc.advanceActivation(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	mustFinalize(t, f, r)
	second, err := f.svc.ActivePolicy(context.Background(), ledger.ActorOwner)
	if err != nil {
		t.Fatal(err)
	}
	// No-change carries no policy key, even after multiple activations.
	f.svc.deps.beforeFinalizeStep = nil
	scratch := mustScratch(t, f, uuid.NewV7())
	noChange := rootRequest(scratch)
	mustAdmit(t, f, noChange)
	mustIntegrate(t, f)
	mustFinalize(t, f, noChange)
	var keys []ledger.Digest
	for _, c := range serviceCommits(t, f.store) {
		for _, d := range c.Records {
			switch d.Type {
			case ledger.TypeFinalizationIntended:
				p, _ := ledger.DecodeFinalizationIntended(d.Payload)
				keys = append(keys, p.ActivationRecordHash)
			case ledger.TypeGenerationDerived, ledger.TypePromotionNoChange:
				var p map[string]json.RawMessage
				if err := json.Unmarshal(d.Payload, &p); err != nil {
					t.Fatal(err)
				}
				if _, present := p["activation_record_hash"]; present {
					t.Fatal("activation key escaped intent")
				}
			}
		}
	}
	if !slices.Equal(keys, []ledger.Digest{{}, first.RecordHash, second.RecordHash}) || first.RecordHash == second.RecordHash {
		t.Fatalf("intent did not bind actual append-time manifest: %v", keys)
	}
}

func activationWorkspaceFromHistory(t *testing.T, f *serviceFixture, history gitFixture) *serviceFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo")
	copyGitHistory(t, history, f.env, root)
	proposal, err := f.svc.git.inspect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.svc.Register(context.Background(), proposal)
	if err != nil {
		t.Fatal(err)
	}
	return &serviceFixture{gitFixture: gitFixture{root: root, colony: f.colony, env: f.env, git: f.git}, store: f.store, svc: f.svc, p: proposal, w: w}
}

func appendActivationTestIntent(t *testing.T, f *serviceFixture, r Request, g ledger.GenerationDerived) {
	t.Helper()
	f.svc.mu.Lock()
	defer f.svc.mu.Unlock()
	witness := f.svc.witnesses[f.w.ID]
	intent := ledger.FinalizationIntended{IntentID: uuid.NewV7(), RequestID: r.RequestID, GenerationID: g.GenerationID, PredecessorWitnessSeq: int64(witness.sequence), PredecessorCommit: g.PredecessorCommit, PredecessorTree: g.PredecessorTree, TargetCommit: g.DerivedCommit, TargetTree: g.DerivedTree, FinalizationRules: "finalization-rules/1", ActivationRecordHash: f.svc.activePolicy.RecordHash}
	if err := f.svc.appendLocked(context.Background(), []ledger.Draft{{Type: ledger.TypeFinalizationIntended, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: r.ContextID, Payload: encoded(t, intent)}}); err != nil {
		t.Fatal(err)
	}
}

func TestActivationSharedCutoverWaitsForAllIntents(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, order := range []string{"resume_first", "reconcile_first"} {
		t.Run(order, func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			second := activationWorkspaceFromHistory(t, f, history)
			unaffected := activationWorkspaceFromHistory(t, f, history)
			r1, g1 := prepareActivationPromotion(t, f, "one")
			r2, g2 := prepareActivationPromotion(t, second, "two")
			r3, g3 := prepareActivationPromotion(t, unaffected, "foreign")
			appendActivationTestIntent(t, second, r2, g2)
			appendActivationTestIntent(t, unaffected, r3, g3)
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			f.svc.deps.beforeFinalizeStep = func(step string) {
				if step == "cas" {
					close(entered)
					<-release
				}
			}
			done := make(chan error, 1)
			go func() { _, err := f.svc.finalize(context.Background(), r1.RequestID, g1.GenerationID); done <- err }()
			<-entered
			clock := &activationClock{ns: 100}
			activation := activationForWorkspace(t, f, clock)
			activation.Target, activation.Artifacts = activationTargetFor(t, f.w.ID, second.w.ID)
			mustActivation(t, f.svc, activation)
			clock.set(1200, nil)
			if err := f.svc.advanceActivation(context.Background()); err != nil {
				t.Fatal(err)
			}
			if activationStatus(t, f.svc, activation.RequestID).State != "pending" {
				t.Fatal("cutover skipped both intents")
			}
			unblock()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if err := f.svc.advanceActivation(context.Background()); err != nil {
				t.Fatal(err)
			}
			if activationStatus(t, f.svc, activation.RequestID).State != "pending" {
				t.Fatal("cutover skipped last affected intent")
			}
			prior := f.svc
			if err := prior.Close(); err != nil {
				t.Fatal(err)
			}
			switched := make(chan struct{}, 1)
			var err error
			f.svc, _, err = openService(context.Background(), f.store, f.colony, serviceConfig(), serviceDeps{environ: func() []string { return f.env }, activationNow: clock.now, beforeStoreAppend: func(d []ledger.Draft) {
				if d[0].Type == ledger.TypePolicyActivated {
					switched <- struct{}{}
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			if order == "resume_first" {
				if err := f.svc.resumeActivationControls(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			proof := &writerQuiescence{WorkspaceID: second.w.ID, DrainedService: prior}
			result, err := f.svc.reconcilePromotions(context.Background(), second.w.ID, proof)
			if err != nil || result.Pending != "" {
				t.Fatalf("resolve second intent: %+v %v", result, err)
			}
			if order == "reconcile_first" {
				if err := f.svc.resumeActivationControls(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			<-switched
			state := activationStatus(t, f.svc, activation.RequestID)
			if state.State != "applied" || state.Applied.EffectiveUTCNS != 1200 {
				t.Fatalf("ending did not wake shared cutover: %+v", state)
			}
			f.svc.mu.Lock()
			_, stillForeign := f.svc.intents[unaffected.w.ID]
			f.svc.mu.Unlock()
			if !stillForeign {
				t.Fatal("activation resolved an unaffected intent")
			}
		})
	}
}

func TestActivationScopeAndBootstrap(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	f := serviceFromHistory(t, history)
	unlisted := activationWorkspaceFromHistory(t, f, history)
	r, g := prepareActivationPromotion(t, unlisted, "unlisted")
	clock := &activationClock{ns: 100}
	activation := activationForWorkspace(t, f, clock)
	bad := activation
	bad.Target = cloneActivationTarget(activation.Target)
	bad.Target.WorkspacePolicies[0].WorkspaceID = uuid.NewV7()
	before := f.store.Durable()
	if _, err := f.svc.RequestActivation(context.Background(), bad); !errors.Is(err, ErrInvalid) || f.store.Durable() != before {
		t.Fatalf("unregistered target: %v", err)
	}
	mustActivation(t, f.svc, activation)
	f.svc.mu.Lock()
	affected := slices.Clone(f.svc.activations[activation.RequestID].request.AffectedWorkspaceIDs)
	f.svc.mu.Unlock()
	if !slices.Equal(affected, []uuid.UUID{f.w.ID}) {
		t.Fatalf("bootstrap enlarged scope: %v", affected)
	}
	// Before first activation an unlisted workspace remains eligible to intend.
	mustFinalize(t, unlisted, r)
	r, g = prepareActivationPromotion(t, unlisted, "after")
	clock.set(1200, nil)
	if err := f.svc.advanceActivation(context.Background()); err != nil {
		t.Fatal(err)
	}
	before = f.store.Durable()
	_, err := f.svc.finalize(context.Background(), r.RequestID, g.GenerationID)
	assertUnavailable(t, err, "policy_unavailable")
	if f.store.Durable() != before {
		t.Fatal("unconfigured workspace appended intent")
	}
	// No-change and integration are not barred even for an unlisted workspace.
	if _, err := f.svc.CancelPromotion(context.Background(), Cancellation{RequestID: r.RequestID, Actor: ledger.ActorOwner}); err != nil {
		t.Fatal(err)
	}
	scratch := mustScratch(t, unlisted, uuid.NewV7())
	noChange := rootRequest(scratch)
	mustAdmit(t, unlisted, noChange)
	mustIntegrate(t, unlisted)
	mustFinalize(t, unlisted, noChange)
}

func TestActivationAdmissionRefusals(t *testing.T) {
	t.Parallel()
	for _, row := range []string{"oversized", "untrusted", "past_schedule", "genesis_hash"} {
		t.Run(row, func(t *testing.T) {
			t.Parallel()
			f := newActivationFixture(t)
			r := f.request()
			original := r
			before := f.store.Durable()
			switch row {
			case "oversized":
				r.Target = cloneActivationTarget(r.Target)
				r.Artifacts = make(map[ledger.Digest][]byte, len(f.artifacts))
				for d, b := range f.artifacts {
					r.Artifacts[d] = b
				}
				body := make([]byte, ledgerstore.MaxBodyBytes+1)
				digest := ledger.Digest(sha256.Sum256(body))
				delete(r.Artifacts, r.Target.WorkspacePolicies[0].CheckPolicyHash)
				for i := range r.Target.WorkspacePolicies {
					r.Target.WorkspacePolicies[i].CheckPolicyHash = digest
				}
				r.Artifacts[digest] = body
			case "untrusted":
				f.clock.set(0, errors.New("clock unavailable"))
			case "past_schedule":
				f.clock.set(1000, nil)
			case "genesis_hash":
				r.ExpectedActiveRecordHash = ledger.Digest{3}
			}
			_, err := f.svc.RequestActivation(context.Background(), r)
			switch row {
			case "untrusted":
				assertUnavailable(t, err, "clock_untrusted")
			case "genesis_hash":
				if !errors.Is(err, ErrRevision) {
					t.Fatalf("wrong hash presence: %v", err)
				}
			default:
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("invalid admission: %v", err)
				}
			}
			if f.store.Durable() != before {
				t.Fatal("refused activation wrote a record")
			}
			if row == "oversized" {
				for digest := range r.Artifacts {
					if _, err := f.store.ReadBody(context.Background(), digest); err == nil {
						t.Fatal("doomed approval published a body before checking all sizes")
					}
				}
			}
			// Refusal consumes neither request nor approval identity.
			f.clock.set(1200, nil)
			original.Schedule = ActivationSchedule{Mode: ledger.ScheduleImmediate}
			mustActivation(t, f.svc, original)
		})
	}
}
