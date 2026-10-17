package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func activationHold(t *testing.T, s *Service, hold string, observed int64) {
	t.Helper()
	s.mu.Lock()
	err := s.changeActivationHoldLocked(context.Background(), hold, observed)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func activationCount(t *testing.T, store *ledgerstore.Store, kind string) int {
	t.Helper()
	count := 0
	for _, commit := range serviceCommits(t, store) {
		for _, record := range commit.Records {
			if record.Type == kind {
				count++
			}
		}
	}
	return count
}

func reopenActivation(t *testing.T, f *activationFixture, now func() (time.Time, error)) Reconciliation {
	t.Helper()
	if err := f.svc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.store, _, err = ledgerstore.Open(f.colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	before := f.store.Durable()
	var report Reconciliation
	f.svc, report, err = openService(context.Background(), f.store, f.colony, serviceConfig(), serviceDeps{activationNow: now})
	if err != nil {
		t.Fatal(err)
	}
	if f.store.Durable() != before || f.svc.activationLoopStarted {
		t.Fatal("Open executed activation")
	}
	return report
}

func TestActivationCrashMatrix(t *testing.T) {
	t.Parallel()
	// One acknowledged history; each parallel boundary below has its own colony
	// and real store. Activation has no external writer to kill or wait for.
	f := newActivationFixture(t)
	type boundary struct {
		name    string
		through uint64
		state   ActivationState
		floor   int64
		tear    bool
	}
	var rows []boundary
	capture := func(name string, id uuid.UUID, prior ActivationState, floor int64) {
		state := activationStatus(t, f.svc, id)
		rows = append(rows, boundary{name: "after_" + name, through: f.store.Durable(), state: state, floor: f.svc.activationFloor})
		rows = append(rows, boundary{name: "torn_" + name, through: f.store.Durable(), state: prior, floor: floor, tear: true})
	}
	var shapes [][]string
	f.svc.deps.beforeStoreAppend = func(drafts []ledger.Draft) {
		var shape []string
		for _, draft := range drafts {
			shape = append(shape, draft.Type)
		}
		shapes = append(shapes, shape)
	}
	r := f.request()
	initialFloor := f.svc.activationFloor
	state := mustActivation(t, f.svc, r)
	capture("requested", r.RequestID, ActivationState{}, initialFloor)
	activationHold(t, f.svc, ledger.HoldDue, 1000)
	capture("due", r.RequestID, state, 100)
	old := activationStatus(t, f.svc, r.RequestID)
	f.clock.set(1100, nil)
	next := f.request()
	next.Schedule.NotBefore = time.Unix(0, 2000).UTC().Format(time.RFC3339Nano)
	state, err := f.svc.ReplaceActivation(context.Background(), ActivationReplacement{Request: next, ExpectedPendingID: r.RequestID, ExpectedPendingRevision: old.Revision})
	if err != nil {
		t.Fatal(err)
	}
	capture("replaced", next.RequestID, old, 1000)
	if _, err := f.svc.CancelActivation(context.Background(), ActivationCancellation{Actor: ledger.ActorOwner, ExpectedPendingID: next.RequestID, ExpectedPendingRevision: state.Revision}); err != nil {
		t.Fatal(err)
	}
	capture("cancelled", next.RequestID, state, 1100)
	f.clock.set(1200, nil)
	last := f.request()
	last.Schedule.NotBefore = time.Unix(0, 3000).UTC().Format(time.RFC3339Nano)
	mustActivation(t, f.svc, last)
	activationHold(t, f.svc, ledger.HoldDue, 3100)
	state = activationStatus(t, f.svc, last.RequestID)
	f.clock.set(3200, nil)
	if err := f.svc.advanceActivation(context.Background()); err != nil {
		t.Fatal(err)
	}
	capture("activated", last.RequestID, state, 3100)
	wantShapes := [][]string{
		{ledger.TypePolicyActivationRequested, ledger.TypeBodyPin},
		{ledger.TypePolicyActivationHoldChanged},
		{ledger.TypePolicyActivationEnded, ledger.TypePolicyActivationRequested, ledger.TypeBodyPin},
		{ledger.TypePolicyActivationEnded},
		{ledger.TypePolicyActivationRequested, ledger.TypeBodyPin},
		{ledger.TypePolicyActivationHoldChanged},
		{ledger.TypePolicyActivated},
	}
	if !reflect.DeepEqual(shapes, wantShapes) {
		t.Fatalf("store boundaries: %v", shapes)
	}
	history := serviceCommits(t, f.store)
	if err := f.svc.Close(); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				store, colony := activationGoldenPrefix(t, history, row.through)
				for digest, body := range f.artifacts {
					got, _, err := store.PublishBody(context.Background(), bytes.NewReader(body))
					if err != nil || got != digest {
						t.Fatalf("retained body: %s %v", got, err)
					}
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				if row.tear {
					tearFinalCommit(t, colony)
				}
				store, recovery, err := ledgerstore.Open(colony, ledger.Defined())
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				if (recovery.TruncatedBytes > 0) != row.tear {
					t.Fatalf("tail recovery: %+v", recovery)
				}
				before := store.Durable()
				s, report, err := openService(context.Background(), store, colony, serviceConfig(), serviceDeps{activationNow: func() (time.Time, error) {
					t.Error("Open sampled activation time")
					return time.Time{}, errors.New("replay has no clock")
				}})
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				if store.Durable() != before || s.activationLoopStarted || s.activationFloor != row.floor {
					t.Fatalf("inert replay: durable=%d/%d floor=%d/%d", store.Durable(), before, s.activationFloor, row.floor)
				}
				if row.state.RequestID == (uuid.UUID{}) {
					if _, err := s.ActivationStatus(context.Background(), ledger.ActorOwner, r.RequestID); !errors.Is(err, ErrUnknownActivation) {
						t.Fatalf("torn request survived: %v", err)
					}
				} else {
					got := activationStatus(t, s, row.state.RequestID)
					if !reflect.DeepEqual(got, row.state) {
						t.Fatalf("boundary state: got %+v want %+v", got, row.state)
					}
				}
				if row.name == "after_replaced" {
					ended := activationStatus(t, s, r.RequestID)
					if ended.State != "replaced" || ended.ReplacementRequestID != next.RequestID || ended.Revision+1 != row.state.Revision {
						t.Fatalf("replacement was not atomic: %+v", ended)
					}
				}
				if row.name == "torn_replaced" {
					if _, err := s.ActivationStatus(context.Background(), ledger.ActorOwner, next.RequestID); !errors.Is(err, ErrUnknownActivation) {
						t.Fatalf("torn replacement consumed new identity: %v", err)
					}
				}
				fenced := row.state.State == "pending" && row.state.Hold == ""
				for _, policy := range f.target.WorkspacePolicies {
					fence := "activation_recovery_pending " + policy.WorkspaceID.String()
					if slices.Contains(report.Pending, fence) != fenced {
						t.Fatalf("boundary fence: %v", report.Pending)
					}
				}
				active, err := s.ActivePolicy(context.Background(), ledger.ActorOwner)
				if row.state.State == "applied" {
					if err != nil || !reflect.DeepEqual(active, *row.state.Applied) {
						t.Fatalf("applied replay: %+v %v", active, err)
					}
				} else {
					assertUnavailable(t, err, "no_active_policy")
				}
				if row.state.RequestID == (uuid.UUID{}) {
					s.deps.activationNow = func() (time.Time, error) { return time.Unix(0, 100), nil }
					mustActivation(t, s, r)
				}
				s.deps.activationNow = func() (time.Time, error) { return time.Unix(0, 4000), nil }
				if err := s.resumeActivationControls(context.Background()); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				wantApplied := 1
				if row.state.State == "cancelled" {
					wantApplied = 0
				}
				if got := activationCount(t, store, ledger.TypePolicyActivated); got != wantApplied {
					t.Fatalf("switches after resume=%d want %d", got, wantApplied)
				}
				if wantApplied != 0 {
					active, err = s.ActivePolicy(context.Background(), ledger.ActorOwner)
					if err != nil {
						t.Fatal(err)
					}
					wantTime := int64(4000)
					if row.state.State == "applied" {
						wantTime = 3200
					}
					if active.EffectiveUTCNS != wantTime || active.Epoch != 1 {
						t.Fatalf("backdated/repeated cutover: %+v", active)
					}
				}
				before = store.Durable()
				if err := s.resumeActivationControls(context.Background()); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				if store.Durable() != before {
					t.Fatal("second resume repeated switch")
				}
				assertActivationRetained(t, &activationFixture{store: store, artifacts: f.artifacts})
			})
		})
	}
}

type activationRefusingStore struct{ workspaceStore }

func (s activationRefusingStore) Append(context.Context, []ledger.Draft) (appender.Committed, error) {
	return appender.Committed{}, appender.ErrRefused
}

// The rollback assertion includes the replay journal's clock and identity facts;
// the same-ID retry and public views below prove their consumer consequences.
func activationProjection(s *Service) any {
	return struct {
		Pending    uuid.UUID
		States     map[uuid.UUID]activationState
		Identities map[uuid.UUID]bool
		Pins       map[uuid.UUID]uuid.UUID
		Hashes     map[ledger.Digest]bool
		Floor      int64
		Active     ActivePolicy
	}{s.pendingActivation, maps.Clone(s.activations), maps.Clone(s.activationIdentities), maps.Clone(s.activationPins), maps.Clone(s.activationHashes), s.activationFloor, s.activePolicy}
}

func TestActivationAppendFailureAndHashHydration(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"requested", "replaced", "hold", "cancelled", "activated"} {
		t.Run("refused_"+kind, func(t *testing.T) {
			t.Parallel()
			f := newActivationFixture(t)
			r := f.request()
			var state ActivationState
			if kind != "requested" {
				state = mustActivation(t, f.svc, r)
			}
			if kind == "hold" || kind == "activated" {
				f.clock.set(1200, nil)
			}
			if kind == "activated" {
				activationHold(t, f.svc, ledger.HoldDue, 1200)
			}
			next := f.request()
			action := func() error {
				switch kind {
				case "requested":
					_, err := f.svc.RequestActivation(context.Background(), r)
					return err
				case "replaced":
					_, err := f.svc.ReplaceActivation(context.Background(), ActivationReplacement{Request: next, ExpectedPendingID: r.RequestID, ExpectedPendingRevision: state.Revision})
					return err
				case "cancelled":
					_, err := f.svc.CancelActivation(context.Background(), ActivationCancellation{Actor: ledger.ActorOwner, ExpectedPendingID: r.RequestID, ExpectedPendingRevision: state.Revision})
					return err
				case "hold":
					f.svc.mu.Lock()
					defer f.svc.mu.Unlock()
					return f.svc.changeActivationHoldLocked(context.Background(), ledger.HoldDue, 1200)
				default:
					return f.svc.advanceActivation(context.Background())
				}
			}
			// Trusted live samples are not undone. Hold the sample fixed so this
			// comparison isolates apply's journal from the clock's live floor.
			f.svc.mu.Lock()
			f.svc.sampleActivationTimeLocked()
			beforeProjection := activationProjection(f.svc)
			f.svc.mu.Unlock()
			before := f.store.Durable()
			f.svc.store = activationRefusingStore{f.store}
			if err := action(); !errors.Is(err, appender.ErrRefused) {
				t.Fatalf("definite refusal: %v", err)
			}
			if f.store.Durable() != before || !reflect.DeepEqual(activationProjection(f.svc), beforeProjection) {
				t.Fatal("refused append leaked projection, slot, identity, pin, hold or floor")
			}
			f.svc.store = f.store
			if err := action(); err != nil {
				t.Fatalf("same-identity retry: %v", err)
			}
			id := r.RequestID
			if kind == "replaced" {
				id = next.RequestID
			}
			got := activationStatus(t, f.svc, id)
			want := "pending"
			if kind == "cancelled" {
				want = "cancelled"
			}
			if kind == "activated" {
				want = "applied"
			}
			if got.State != want || (kind == "hold" && got.Hold != ledger.HoldDue) {
				t.Fatalf("retry projection: %+v", got)
			}
			assertActivationRetained(t, f)
		})
	}
	t.Run("foreign_append", func(t *testing.T) {
		t.Parallel()
		f := newActivationFixture(t)
		body, _, err := f.store.PublishBody(context.Background(), bytes.NewReader([]byte("unrelated producer")))
		if err != nil {
			t.Fatal(err)
		}
		f.svc.deps.beforeStoreAppend = func([]ledger.Draft) {
			_, err := f.store.Append(context.Background(), []ledger.Draft{{Type: ledger.TypeBodyPin, TypeVersion: 1, Actor: ledger.ActorCore, BodyRefs: []ledger.Digest{body}, Payload: encoded(t, ledger.BodyPin{PinID: uuid.NewV7()})}})
			if err != nil {
				t.Fatal(err)
			}
		}
		lastCommit := func() ledger.Commit { commits := serviceCommits(t, f.store); return commits[len(commits)-1] }
		checkRequest := func(state ActivationState, at int) {
			commit := lastCommit()
			if state.Revision != commit.First+uint64(at) || state.RequestRecordHash.String() != commit.Records[at].Hash {
				t.Fatalf("guessed request envelope: %+v commit=%+v", state, commit)
			}
		}
		r := f.request()
		state := mustActivation(t, f.svc, r)
		checkRequest(state, 0)
		next := f.request()
		state, err = f.svc.ReplaceActivation(context.Background(), ActivationReplacement{Request: next, ExpectedPendingID: r.RequestID, ExpectedPendingRevision: state.Revision})
		if err != nil {
			t.Fatal(err)
		}
		checkRequest(state, 1)
		if old := activationStatus(t, f.svc, r.RequestID); old.Revision != lastCommit().First {
			t.Fatalf("guessed replacement ending revision: %+v", old)
		}
		if _, err := f.svc.CancelActivation(context.Background(), ActivationCancellation{Actor: ledger.ActorOwner, ExpectedPendingID: next.RequestID, ExpectedPendingRevision: state.Revision}); err != nil {
			t.Fatal(err)
		}
		if got := activationStatus(t, f.svc, next.RequestID); got.Revision != lastCommit().First {
			t.Fatalf("guessed cancellation revision: %+v", got)
		}
		r = f.request()
		mustActivation(t, f.svc, r)
		activationHold(t, f.svc, ledger.HoldDue, 1200)
		if got := activationStatus(t, f.svc, r.RequestID); got.Revision != lastCommit().First {
			t.Fatalf("guessed hold revision: %+v", got)
		}
		f.clock.set(1300, nil)
		if err := f.svc.advanceActivation(context.Background()); err != nil {
			t.Fatal(err)
		}
		active, err := f.svc.ActivePolicy(context.Background(), ledger.ActorOwner)
		commit := lastCommit()
		if err != nil || active.EffectiveSequence != commit.First || active.RecordHash.String() != commit.Records[0].Hash {
			t.Fatalf("guessed activation envelope: %+v %v", active, err)
		}
		reopenActivation(t, f, f.clock.now)
		replayed, err := f.svc.ActivePolicy(context.Background(), ledger.ActorOwner)
		if err != nil || !reflect.DeepEqual(active, replayed) {
			t.Fatalf("append and replay identities differ: %+v %v", replayed, err)
		}
	})
}

func TestActivationLostAcknowledgment(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"requested", "activated"} {
		for _, persist := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/persist_%t", kind, persist), func(t *testing.T) {
				t.Parallel()
				f := newActivationFixture(t)
				r := f.request()
				if kind == "activated" {
					mustActivation(t, f.svc, r)
					activationHold(t, f.svc, ledger.HoldDue, 1200)
					f.clock.set(1300, nil)
				}
				before := f.store.Durable()
				f.svc.store = &uncertainStore{Store: f.store, persist: persist}
				var err error
				if kind == "requested" {
					_, err = f.svc.RequestActivation(context.Background(), r)
				} else {
					err = f.svc.advanceActivation(context.Background())
				}
				if !errors.Is(err, appender.ErrUncertain) || !errors.Is(err, ErrPoisoned) {
					t.Fatalf("lost acknowledgement: %v", err)
				}
				if _, err := f.svc.ActivationStatus(context.Background(), ledger.ActorOwner, r.RequestID); !errors.Is(err, ErrPoisoned) {
					t.Fatalf("poisoned status: %v", err)
				}
				if _, err := f.svc.RequestActivation(context.Background(), r); !errors.Is(err, ErrPoisoned) {
					t.Fatalf("poisoned retry: %v", err)
				}
				if err := f.svc.resumeActivationControls(context.Background()); !errors.Is(err, ErrPoisoned) {
					t.Fatalf("poisoned resume: %v", err)
				}
				want := before
				if persist {
					want++
					if kind == "requested" {
						want++
					}
				}
				if f.store.Durable() != want {
					t.Fatalf("lost-ack durable=%d want %d", f.store.Durable(), want)
				}
				reopenActivation(t, f, func() (time.Time, error) { t.Error("Open read clock"); return time.Time{}, errors.New("forbidden") })
				f.svc.deps.activationNow = f.clock.now
				if !persist && kind == "requested" {
					if _, err := f.svc.ActivationStatus(context.Background(), ledger.ActorOwner, r.RequestID); !errors.Is(err, ErrUnknownActivation) {
						t.Fatalf("absent request survived: %v", err)
					}
				}
				state := mustActivation(t, f.svc, r)
				if kind == "activated" && persist && state.State != "applied" {
					t.Fatalf("committed switch lost: %+v", state)
				}
				f.clock.set(1500, nil)
				if err := f.svc.advanceActivation(context.Background()); err != nil {
					t.Fatal(err)
				}
				if activationCount(t, f.store, ledger.TypePolicyActivated) != 1 {
					t.Fatal("lost acknowledgement duplicated or lost switch")
				}
				assertActivationRetained(t, f)
			})
		}
	}
}

func TestActivationClockRollback(t *testing.T) {
	t.Parallel()
	for _, due := range []bool{false, true} {
		t.Run(fmt.Sprintf("due_%t", due), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newActivationFixture(t)
				defer func() { f.svc.Close(); f.store.Close() }()
				r := f.request()
				mustActivation(t, f.svc, r)
				activationHold(t, f.svc, ledger.HoldClockUntrusted, 0)
				activationHold(t, f.svc, ledger.HoldNone, 500)
				floor := int64(500)
				if due {
					activationHold(t, f.svc, ledger.HoldDue, 1200)
					floor = 1200
				}
				// An unrecorded trusted sample must not survive a restart.
				f.clock.set(2000, nil)
				f.svc.mu.Lock()
				f.svc.sampleActivationTimeLocked()
				f.svc.mu.Unlock()
				reopenActivation(t, f, func() (time.Time, error) { t.Error("Open sampled clock"); return time.Time{}, errors.New("forbidden") })
				if f.svc.activationFloor != floor {
					t.Fatalf("replayed floor=%d want %d", f.svc.activationFloor, floor)
				}
				samples := 0
				f.clock.set(floor-1, nil)
				f.svc.deps.activationNow = func() (time.Time, error) { samples++; return f.clock.now() }
				before := f.store.Durable()
				if err := f.svc.resumeActivationControls(context.Background()); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				wantHold, wantDurable := ledger.HoldClockUntrusted, before+1
				if due {
					wantHold, wantDurable = ledger.HoldDue, before
				}
				if got := activationStatus(t, f.svc, r.RequestID); got.Hold != wantHold || got.State != "pending" || f.store.Durable() != wantDurable {
					t.Fatalf("rollback bypass: %+v durable=%d want %d", got, f.store.Durable(), wantDurable)
				}
				f.svc.mu.Lock()
				samplesBefore := samples
				f.svc.mu.Unlock()
				<-time.NewTimer(time.Duration(math.MaxInt64 / 2)).C
				synctest.Wait()
				f.svc.mu.Lock()
				samplesAfter := samples
				f.svc.mu.Unlock()
				if samplesAfter != samplesBefore || f.store.Durable() != wantDurable {
					t.Fatal("regressed clock polled or rearmed a timer")
				}
				if !due {
					f.clock.set(600, nil)
					if err := f.svc.resumeActivationControls(context.Background()); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
					if got := activationStatus(t, f.svc, r.RequestID); got.Hold != ledger.HoldNone {
						t.Fatalf("trust did not recover below due: %+v", got)
					}
				}
				f.clock.set(1500, nil)
				// Clock recovery alone is not a wake. Explicit resume rechecks it.
				if activationCount(t, f.store, ledger.TypePolicyActivated) != 0 {
					t.Fatal("clock update alone switched")
				}
				if err := f.svc.resumeActivationControls(context.Background()); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				active, err := f.svc.ActivePolicy(context.Background(), ledger.ActorOwner)
				if err != nil || active.EffectiveUTCNS != 1500 || activationCount(t, f.store, ledger.TypePolicyActivated) != 1 {
					t.Fatalf("trusted live switch: %+v %v", active, err)
				}
				reopenActivation(t, f, func() (time.Time, error) {
					t.Error("Open sampled effective clock")
					return time.Time{}, errors.New("forbidden")
				})
				if f.svc.activationFloor != 1500 {
					t.Fatalf("effective time missing from replay floor: %d", f.svc.activationFloor)
				}
			})
		})
	}
}
