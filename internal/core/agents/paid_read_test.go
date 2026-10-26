package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func paidArtifactDisclosureRows(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"owned-input-output-evidence", "partial-slots-denied", "unrelated-denied", "other-origin-is-missing", "revoked-during-read", "maxbytes", "physical-buffer-denied"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) { paidSuccess(t, w, r) })
			f := newLivePaidFixture(t, paidServerDeps(server, nil))
			admission, d, out := paidRun(t, f)
			paidRecord(t, f, d, out)
			v := f.view(t, d.AttemptID)
			binding := bindingOf(f.origin)
			reader := privateScopedGrant(binding, 0xf00, paidPhysicalCapacity(), "private.read")
			if name == "partial-slots-denied" {
				reader.Resources[0].Slots = []string{"memory"}
			}
			if name == "physical-buffer-denied" {
				reader.Limits.BufferBytes = 1
			}
			if name == "other-origin-is-missing" {
				binding = scopedWorkContext(t, f.service, binding, 0xf01, 0xf02)
				reader = privateScopedGrant(binding, 0xf00, paidPhysicalCapacity(), "private.read")
			}
			issueScopedRoot(t, f.service, bindingOf(f.origin), 0xf03, reader)
			a := scopedBound(t, f.service, binding)
			request := PaidObjectLookup{AttemptID: d.AttemptID, GrantID: reader.GrantID, Object: *v.Result.EvidenceObject, MaxBytes: paidOutputLimit}
			if name == "unrelated-denied" {
				scope, err := newPrivateScope(f.root, f.service.p.agents[f.origin.AgentID].directory, f.origin.ContextID.String(), &f.service.dirs)
				if err != nil {
					t.Fatal(err)
				}
				body := []byte("unrelated context object")
				digest, err := scope.publishObject(body)
				if err != nil {
					t.Fatal(err)
				}
				request.Object = ledger.ObjectRef{SHA256: digest, Bytes: int64(len(body))}
			}
			if name == "maxbytes" {
				request.MaxBytes = request.Object.Bytes - 1
			}
			if name == "revoked-during-read" {
				f.service.deps.afterRead = func(_ Binding, scope string) {
					if scope != "paid_object" {
						return
					}
					_, err := f.service.RevokeGrant(ctx, binding, GrantRevocation{CommandID: f.id(), RevokeGrantID: reader.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(f.service.p.authority)})
					if err != nil {
						t.Error(err)
					}
				}
			}
			before := f.store.Durable()
			f.service.deps.reviewClock = func() (int64, error) { t.Error("passive object read sampled clock"); return 0, ErrPaidClock }
			got, err := a.ReadPaidObject(ctx, request)
			if name == "owned-input-output-evidence" {
				if err != nil {
					t.Fatal(err)
				}
				for _, ref := range []ledger.ObjectRef{admission.InputObject, v.Result.MessageObjects[0], *v.Result.EvidenceObject} {
					request.Object = ref
					got, err = a.ReadPaidObject(ctx, request)
					if err != nil {
						t.Fatal(err)
					}
					if int64(len(got)) != ref.Bytes || ledger.Digest(sha256.Sum256(got)) != ref.SHA256 {
						t.Fatal("selected object bytes changed")
					}
					got[0] ^= 0xff
					again, err := a.ReadPaidObject(ctx, request)
					if err != nil || bytes.Equal(again, got) {
						t.Fatal("object result aliases retained data")
					}
				}
				if f.store.Durable() != before {
					t.Fatal("read appended a record")
				}
				return
			}
			if got != nil || err == nil {
				t.Fatal("denied read disclosed bytes")
			}
			if name == "maxbytes" {
				if !errors.Is(err, ErrTooLarge) {
					t.Fatal("missing selected-size bound")
				}
				return
			}
			if name == "physical-buffer-denied" {
				return
			}
			if !errors.Is(err, ErrDenied) {
				t.Fatalf("private denial disclosed existence: %v", err)
			}
			if name == "other-origin-is-missing" {
				request.AttemptID = f.id()
				if _, otherErr := a.ReadPaidObject(ctx, request); otherErr != err {
					t.Fatal("missing and other-origin attempts differ")
				}
			}
		})
	}
}
