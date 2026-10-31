package agents

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func FuzzRegistrationGroupReplay(f *testing.F) {
	for target := range byte(3) {
		f.Add(target, byte(0), uint64(0), []byte(nil))
		f.Add(target, byte(1), uint64(0), []byte(`{}`))
		f.Add(target, byte(2), uint64(0), []byte(`{}`))
		f.Add(target, byte(3), uint64(1), []byte(nil))
		f.Add(target, byte(4), uint64(1), []byte(nil))
		f.Add(target, byte(5), uint64(0), []byte(nil))
		f.Add(target, byte(6), uint64(0), []byte(nil))
		f.Add(target, byte(7), uint64(0), []byte(nil))
	}
	prefixes := []int{5, 6, 13}
	type fixture struct {
		service  *serviceFixture
		baseline *Service
		commit   ledger.Commit
		prepared string
		input    registrationInput
		receipt  ledger.RuntimeRegistrationReceipt
		before   uint64
	}
	fixtures := make([]fixture, len(prefixes))
	for i, prefix := range prefixes {
		real, commits, prepared := preparedRegistrationFixture(f, prefix)
		baseline, _, err := Open(context.Background(), real.store, real.root, DefaultLimits())
		if err != nil {
			f.Fatal(err)
		}
		f.Cleanup(func() { _ = baseline.Close() })
		r, err := ledger.DecodeRuntimeRegistrationReceipt(commits[prefix].Records[2].Payload)
		if err != nil {
			f.Fatal(err)
		}
		fixtures[i] = fixture{real, baseline, commits[prefix], prepared, registrationInput{id: r.CommandID, sub: r.Submission, digest: r.SubmissionSHA256}, r, real.store.Durable()}
	}
	f.Fuzz(func(t *testing.T, target, mode byte, value uint64, input []byte) {
		t.Parallel()
		if len(input) > ledger.MaxRegistrationPayloadBytes {
			return
		}
		fixture := fixtures[int(target)%len(fixtures)]
		d := scopedGoldenDrafts(fixture.commit)
		valid := false
		switch mode % 8 {
		case 0:
			valid = true
		case 1:
			d[1].Payload = append([]byte(nil), input...)
		case 2:
			d[2].Payload = append([]byte(nil), input...)
		case 3:
			d[1].TypeVersion = int(value%256) + 1
			valid = d[1].TypeVersion == 1
		case 4:
			d[1].ContextID[15] ^= byte(value)
			valid = byte(value) == 0
		case 5:
			d = d[:2]
		case 6:
			d = append(d, d[1])
		case 7:
			// Rehash the whole receipt so this reaches origin/state checks,
			// rather than merely failing a canonical submission digest.
			r := scopedDecode(t, ledger.DecodeRuntimeRegistrationReceipt, d[2].Payload)
			r.Submission.Origin.SessionID = registrationGoldenID(0x999)
			for i := range d {
				d[i].ExecutionSessionID = r.Submission.Origin.SessionID
			}
			rewriteRegistrationReceipt(t, d, r)
		}
		fixture.service.service.mu.Lock()
		_, _, _, _, group, _, err := decodeFamily(d, fixture.before+1)
		if err == nil && group != nil {
			_, err = fixture.service.service.planRegistration(context.Background(), group, fixture.before+1)
		}
		if err == nil {
			err = fixture.service.service.preflightRegistration(context.Background(), fixture.input, d)
		}
		unchanged := sameProjectionState(fixture.service.service.p, fixture.baseline.p)
		fixture.service.service.mu.Unlock()
		if !unchanged || fixture.service.store.Durable() != fixture.before {
			t.Fatal("registration preflight installed state")
		}
		if err != nil {
			if valid {
				t.Fatalf("valid frozen group refused: %v", err)
			}
			return
		}
		if group == nil || !reflect.DeepEqual(group.receipt, fixture.receipt) {
			t.Fatal("accepted a different receipt for the fixed original request")
		}
		// Canonicalize accepted payloads, then observe actual store replay.
		d[0].Payload = testPayload(t, group.command)
		switch {
		case group.resource != nil:
			d[1].Payload = testPayload(t, *group.resource)
		case group.domain != nil:
			d[1].Payload = testPayload(t, *group.domain)
		case group.declaration != nil:
			d[1].Payload = testPayload(t, *group.declaration)
		}
		d[2].Payload = testPayload(t, group.receipt)
		root := testRoot(t)
		if err := os.CopyFS(root, os.DirFS(fixture.prepared)); err != nil {
			t.Fatal(err)
		}
		store, _, err := ledgerstore.Open(root, ledger.Defined())
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if _, err := store.Submit(context.Background(), d[0], d[1:]); err != nil {
			t.Fatal(err)
		}
		service, report, err := Open(context.Background(), store, root, DefaultLimits())
		if err != nil || len(report.Issues) != 0 {
			t.Fatalf("accepted group replay: %+v %v", report, err)
		}
		defer service.Close()
		switch {
		case group.resource != nil:
			view, err := service.ResourceStatus(context.Background(), ResourceRegistrationLookup{Resource: group.resource.Resource, ThroughSequence: ledger.ToEnd})
			if err != nil || !reflect.DeepEqual(view.Record, *group.resource) {
				t.Fatalf("canonical resource replay: %+v %v", view, err)
			}
		case group.domain != nil:
			view, err := service.WorkDomainStatus(context.Background(), RegistrationLookup{ID: group.domain.WorkDomainID, ThroughSequence: ledger.ToEnd})
			if err != nil || !reflect.DeepEqual(view.Record, *group.domain) {
				t.Fatalf("canonical domain replay: %+v %v", view, err)
			}
		case group.declaration != nil:
			view, err := service.DeclarationStatus(context.Background(), RegistrationLookup{ID: group.declaration.DeclarationID, ThroughSequence: ledger.ToEnd})
			if err != nil || !reflect.DeepEqual(view.Record, *group.declaration) {
				t.Fatalf("canonical declaration replay: %+v %v", view, err)
			}
		}
		receipt, err := service.RegistrationCommandStatus(context.Background(), RegistrationCommandLookup{CommandID: group.command.CommandID, SubmissionSHA256: fixture.input.digest})
		if err != nil || !reflect.DeepEqual(receipt.RuntimeRegistrationReceipt, group.receipt) || receipt.EffectSequence != fixture.before+2 || receipt.Sequence != fixture.before+3 {
			t.Fatalf("canonical receipt replay: %+v %v", receipt, err)
		}
	})
}
