package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func serviceWithdrawalFuzzPrefix(f *testing.F) loopFuzzPrefix {
	f.Helper()
	fixture := newPaidFixture(f)
	s := fixture.service
	now := s.p.reviewFloor + int64(time.Hour)
	e := servicePaidFailure(f, fixture, "overloaded", now)
	serviceRecord(f, s, e)
	completion := serviceSuccessfulCall(f, fixture, e.CauseID, now+1)
	stop := errors.New("retained withdrawal prefix")
	var withdrawal ledger.RuntimeServiceObserved
	s.deps.afterServiceCommit = func(g *serviceGroup) {
		if g.observed != nil && g.observed.Source.Kind == "demand_withdrawn" {
			withdrawal = *g.observed
			s.deps.beforeServiceSubmit = func() error { return stop }
		}
	}
	if _, err := s.recordServiceEvidence(context.Background(), completion.ObservationID, completion); !errors.Is(err, stop) {
		f.Fatalf("withdrawal prefix boundary: %v", err)
	}
	s.mu.Lock()
	sub, err := s.serviceStateSubmission("service.withdraw", withdrawal.AgentID, withdrawal.CauseID, withdrawal.Source)
	var drafts []ledger.Draft
	if err == nil {
		var plan servicePlan
		plan, err = s.deriveService(context.Background(), nil, sub, s.p.through+1)
		if err == nil {
			drafts, err = serviceDrafts(uuid.NewV7(), sub, plan, s.p.through+1)
		}
	}
	s.mu.Unlock()
	if err != nil {
		f.Fatal(err)
	}
	prefix := retainLoopFuzzPrefix(f, fixture.serviceFixture)
	prefix.draft = drafts
	return prefix
}

func FuzzServiceGroupReplay(f *testing.F) {
	_, commits := serviceGolden(f, 0, serviceDeps{})
	var prefixes []loopFuzzPrefix
	operations := map[string]bool{}
	for index, commit := range commits {
		if commit.Records[len(commit.Records)-1].Type != ledger.TypeRuntimeServiceReceipt {
			continue
		}
		fixture, _ := scopedGoldenFixture(f, "91", index)
		prefix := retainLoopFuzzPrefix(f, fixture)
		prefix.draft = draftsOf(commit)
		g, err := decodeServiceFamily(prefix.draft, prefix.through+1)
		if err != nil || g == nil {
			f.Fatal("invalid frozen service seed", err)
		}
		operations[g.receipt.Submission.Operation] = true
		prefixes = append(prefixes, prefix)
	}
	prefixes = append(prefixes, serviceWithdrawalFuzzPrefix(f))
	operations["service.withdraw"] = true
	for _, operation := range []string{"service.record", "service.observe", "service.expire", "service.clear", "service.withdraw", "service.complete"} {
		if !operations[operation] {
			f.Fatal("missing reachable service operation", operation)
		}
	}
	for shape := range prefixes {
		for mode := byte(0); mode < 12; mode++ {
			f.Add(byte(shape), mode, uint64(1), []byte(`{}`))
		}
	}
	f.Fuzz(func(t *testing.T, shape, mode byte, value uint64, input []byte) {
		t.Parallel()
		if len(input) > ledger.MaxServicePayloadBytes {
			return
		}
		prefix := prefixes[int(shape)%len(prefixes)]
		drafts := make([]ledger.Draft, len(prefix.draft))
		for i, d := range prefix.draft {
			drafts[i] = d
			drafts[i].Payload = bytes.Clone(d.Payload)
		}
		g, err := decodeServiceFamily(drafts, prefix.through+1)
		if err != nil || g == nil {
			t.Fatal("invalid retained service seed", err)
		}
		valid := mode%12 == 0
		switch mode % 12 {
		case 1:
			drafts[1].Payload = bytes.Clone(input)
		case 2:
			drafts[len(drafts)-1].Payload = bytes.Clone(input)
		case 3:
			g.receipt.SourceThrough = int64(value & uint64(^uint64(0)>>1))
			drafts[len(drafts)-1].Payload, err = g.receipt.Encode()
		case 4:
			if g.member != nil {
				g.member.FirstSource.ObservedUTCNS += int64(value % 4)
				for i := range drafts {
					if drafts[i].Type == ledger.TypeRuntimeServiceAgentChanged {
						drafts[i].Payload, err = g.member.Encode()
					}
				}
			}
		case 5:
			if g.member != nil {
				g.member.Phase = "future_phase"
				for i := range drafts {
					if drafts[i].Type == ledger.TypeRuntimeServiceAgentChanged {
						drafts[i].Payload, err = g.member.Encode()
					}
				}
			}
		case 6:
			if g.cause != nil {
				g.cause.Condition = "future_condition"
				for i := range drafts {
					if drafts[i].Type == ledger.TypeRuntimeServiceCauseChanged {
						drafts[i].Payload, err = g.cause.Encode()
					}
				}
			}
		case 7:
			drafts = append(drafts, drafts[1])
		case 8:
			drafts = drafts[:len(drafts)-1]
		case 9:
			if g.receipt.Submission.ExpectedGeneration != nil {
				g.receipt.Submission.ExpectedGeneration = new(*g.receipt.Submission.ExpectedGeneration + int64(value%4))
			} else {
				g.receipt.Submission.AgentID[15] ^= byte(value)
			}
			var raw []byte
			raw, err = g.receipt.Submission.Encode()
			if err == nil {
				g.receipt.SubmissionSHA256 = ledger.Digest(sha256.Sum256(raw))
				g.command.SubmissionSHA256 = g.receipt.SubmissionSHA256
				drafts[0].Payload, err = g.command.Encode()
			}
			if err == nil {
				drafts[len(drafts)-1].Payload, err = g.receipt.Encode()
			}
		case 10:
			drafts[1].Actor = ledger.ActorOwner
		case 11:
			drafts[1].AgentID = uuid.NewV7()
		}
		if err != nil {
			return
		}
		s := prefix.fixture.service
		s.mu.Lock()
		accepted, err := decodeServiceFamily(drafts, prefix.through+1)
		var plan serviceProjection
		if err == nil {
			if accepted == nil {
				err = ErrInvalid
			} else {
				plan, err = s.planService(context.Background(), nil, accepted)
			}
		}
		unchanged := sameProjectionState(s.p, prefix.baseline.p)
		if err != nil {
			_, replayErr := s.acceptCommitLocked(context.Background(), replayDraftCommit(drafts, prefix.through+1), nil)
			unchanged = unchanged && sameProjectionState(s.p, prefix.baseline.p)
			s.mu.Unlock()
			if valid || replayErr == nil || !unchanged || prefix.fixture.store.Durable() != prefix.through {
				t.Fatalf("rejected service group mutated native or service state: valid=%v preflight=%v replay=%v unchanged=%v", valid, err, replayErr, unchanged)
			}
			return
		}
		s.mu.Unlock()
		if !unchanged || prefix.fixture.store.Durable() != prefix.through {
			t.Fatal("service preflight installed a transition")
		}
		root := testRoot(t)
		if err := os.CopyFS(root, os.DirFS(prefix.prepared)); err != nil {
			t.Fatal(err)
		}
		store, _, err := ledgerstore.Open(root, ledger.Defined())
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if _, err := store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
			t.Fatal(err)
		}
		through := store.Durable()
		reopened, _, err := Open(context.Background(), store, root, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		if !reflect.DeepEqual(plan, reopened.p.service) || store.Durable() != through {
			t.Fatal("accepted service transition changed on inert replay")
		}
	})
}
