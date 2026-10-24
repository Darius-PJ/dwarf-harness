package agents

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type scopedCrashRequests struct {
	Binding     Binding
	Turn        TurnAdmission
	Local       LocalAdmission
	Write       ScopedPrivateWrite
	Read        ScopedPrivateRead
	ReadReceipt ScopedReceipt
	Revoke      GrantRevocation
	Dispatch    LocalDispatch
	End         LocalEnding
}

type scopedCrashSignal struct {
	Boundary string
	Before   uint64
	Receipt  *ScopedReceipt
}

// Injected process exits and a deliberately truncated final record are not
// power-loss or isolation proofs. Each row copies one closed real-store prefix.
func TestScopedInjectedCrashRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	prepared, agent, turn, local := scopedLocalFixture(t, DefaultLimits())
	binding := Binding(local.Target.Binding)
	grant := privateScopedGrant(binding, 9500, scopedCapacity(), "private.read", "private.write")
	issued := issueScopedRoot(t, prepared.service, binding, 9501, grant)
	snapshot, err := prepared.service.Snapshot(ctx, ReadBinding{Binding: binding, Scope: "context"})
	if err != nil {
		t.Fatal(err)
	}
	requests := scopedCrashRequests{
		Binding: binding, Turn: turn, Local: local,
		Write:    ScopedPrivateWrite{CommandID: privateID(9502), GrantID: grant.GrantID, Scope: "context", ExpectedVersion: snapshot.Revision, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("scoped crash memory\n")}, {Slot: "handoff", Mode: "replace", Bytes: []byte("scoped crash handoff\n")}}},
		Read:     ScopedPrivateRead{CommandID: privateID(9503), GrantID: grant.GrantID, Scope: "context", Slot: "memory", Revision: snapshot.Revision, MaxBytes: 4096},
		Revoke:   GrantRevocation{CommandID: privateID(9504), RevokeGrantID: grant.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(issued.Version)},
		Dispatch: LocalDispatch{CommandID: privateID(9505), WorkID: local.WorkID, ExpectedVersion: 1, ArgumentsSHA256: local.ArgumentsSHA256},
		End:      LocalEnding{CommandID: privateID(9506), WorkID: local.WorkID, ExpectedVersion: 1, Outcome: "released_unsent"},
	}
	page, err := agent.ReadPrivate(ctx, requests.Read)
	if err != nil || string(page.Page.Bytes) != "work memory\n" {
		t.Fatalf("prepare real read retry: %+v %v", page, err)
	}
	requests.ReadReceipt = page.Receipt
	if _, err := agent.StartTurn(ctx, turn); err != nil {
		t.Fatal(err)
	}
	baseBook, err := prepared.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
	if err != nil {
		t.Fatal(err)
	}
	baseThrough := prepared.store.Durable()
	var encoded bytes.Buffer
	if err := gob.NewEncoder(&encoded).Encode(requests); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(prepared.root, "scoped-crash-input.gob"), encoded.Bytes())
	if err := prepared.service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := prepared.store.Close(); err != nil {
		t.Fatal(err)
	}

	for _, boundary := range []string{"before_commit", "torn_admission", "admission_lost_ack", "dispatch_no_ending", "private_before_materialization", "ending_lost_ack", "revoke_before_projection"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			root := testRoot(t)
			if err := os.CopyFS(root, os.DirFS(prepared.root)); err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe, "-test.run=^TestScopedCrashChild$")
			cmd.Env = append(os.Environ(), "DWARF_SCOPED_CRASH_ROOT="+root, "DWARF_SCOPED_CRASH_BOUNDARY="+boundary)
			output, err := cmd.CombinedOutput() // Positively wait before opening any colony file.
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != privateCrashExit {
				t.Fatalf("injected child did not exit at %s: %v\n%s", boundary, err, output)
			}
			var signal scopedCrashSignal
			if err := gob.NewDecoder(bytes.NewReader(output)).Decode(&signal); err != nil || signal.Boundary != boundary {
				t.Fatalf("wrong injected boundary signal: %s; %v", output, err)
			}
			before := baseThrough
			if boundary == "dispatch_no_ending" || boundary == "ending_lost_ack" {
				before += 3
			}
			if signal.Before != before {
				t.Fatalf("unexpected pre-transition boundary: %d want %d", signal.Before, before)
			}
			store, recovery, err := ledgerstore.Open(root, ledger.Defined())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if (recovery.TruncatedBytes > 0) != (boundary == "torn_admission") {
				t.Fatalf("wrong injected torn-tail recovery: %+v", recovery)
			}
			through := before + 3
			if boundary == "before_commit" || boundary == "torn_admission" {
				through = before
			}
			if boundary == "private_before_materialization" {
				through = before + 4
			}
			if boundary == "before_commit" {
				reader, err := ledger.OpenReader(store.LogDir(), ledger.ReadOptions{Colony: store.Colony(), Through: store.Durable(), Consume: ledger.Consumption{ledger.TypeCommand: {1}}})
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				for {
					commit, err := reader.Next()
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					for _, record := range commit.Records {
						command, err := ledger.DecodeCommand(record.Payload)
						if err != nil {
							t.Fatal(err)
						}
						if command.CommandID == local.CommandID {
							t.Fatal("uncommitted command ID was consumed")
						}
					}
				}
			}
			if store.Durable() != through {
				t.Fatalf("partial or duplicated recovered group: %d want %d", store.Durable(), through)
			}
			segment := filepath.Join(store.LogDir(), ledger.SegmentName(1))
			recoveredLog := readTestFile(t, segment)
			service, report, err := Open(ctx, store, root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			if len(report.Issues) != 0 || store.Durable() != through || !bytes.Equal(recoveredLog, readTestFile(t, segment)) {
				t.Fatalf("Open changed durable facts or lost integrity: %+v", report)
			}
			completed, pending := 0, 1
			if boundary == "private_before_materialization" {
				completed = 2
			}
			if boundary == "admission_lost_ack" || boundary == "dispatch_no_ending" {
				pending = 2
			}
			if len(report.Completed) != completed || len(report.PendingWork) != pending {
				t.Fatalf("wrong recovery effects: %+v; want completed %d pending %d", report, completed, pending)
			}
			held, used := baseBook.Held, baseBook.Used
			switch boundary {
			case "admission_lost_ack":
				held.LocalSlots++
				held.BufferBytes += local.MaxBytes
				held.LocalUnits++
			case "dispatch_no_ending":
				held.LocalSlots++
				held.BufferBytes += local.MaxBytes
				used.LocalUnits++
			case "private_before_materialization":
				used.LocalUnits++
			}
			assertScopedCrashAccounting(t, service, held, used)
			agent := scopedBound(t, service, binding)
			switch boundary {
			case "before_commit", "torn_admission":
				if _, err := service.TurnStatus(ctx, WorkLookup{WorkID: local.WorkID}); err != ErrUnavailable {
					t.Fatalf("uncommitted admission became visible: %v", err)
				}
				retry := local
				retry.ArgumentsSHA256[0] ^= 1 // A different valid intent proves the command ID was not consumed.
				receipt, err := agent.AdmitLocal(ctx, retry)
				if err != nil || receipt.WorkID != local.WorkID || store.Durable() != through+3 {
					t.Fatalf("uncommitted ID was consumed: %+v %v", receipt, err)
				}
				if replay, err := agent.AdmitLocal(ctx, retry); err != nil || !reflect.DeepEqual(replay, receipt) || store.Durable() != through+3 {
					t.Fatalf("retry did not become exactly one admission: %+v %v", replay, err)
				}
				held.LocalSlots++
				held.BufferBytes += local.MaxBytes
				held.LocalUnits++
				assertScopedCrashAccounting(t, service, held, used)
			case "admission_lost_ack":
				if signal.Receipt == nil {
					t.Fatal("missing committed admission signal")
				}
				if replay, err := agent.AdmitLocal(ctx, local); err != nil || !reflect.DeepEqual(replay, *signal.Receipt) || store.Durable() != through {
					t.Fatalf("lost acknowledgment acquired another hold: %+v %v", replay, err)
				}
				if _, err := agent.StartTurn(ctx, turn); err != nil || store.Durable() != through {
					t.Fatalf("parent turn retry changed recovered admission: %v", err)
				}
				assertScopedCrashAccounting(t, service, held, used)
			case "dispatch_no_ending":
				work, err := service.TurnStatus(ctx, WorkLookup{WorkID: local.WorkID})
				if err != nil || !work.Dispatched || work.Ended || work.Held != (Exposure{LocalSlots: 1, BufferBytes: local.MaxBytes}) {
					t.Fatalf("durable dispatch inferred an ending: %+v %v", work, err)
				}
				request := EndingRequest{CommandID: privateID(9507), WorkID: local.WorkID, ExpectedVersion: 2}
				ended, err := service.ReconcileAdmissions(ctx, request)
				if err != nil || store.Durable() != through+3 {
					t.Fatalf("recovery guessed a dispatch result: %+v %v", ended, err)
				}
				work, err = service.TurnStatus(ctx, WorkLookup{WorkID: local.WorkID})
				if err != nil || !work.Dispatched || !work.Ended || work.Outcome != "unknown" || work.Held != (Exposure{LocalSlots: 1, BufferBytes: local.MaxBytes}) {
					t.Fatalf("unknown lost its retained hold: %+v %v", work, err)
				}
				if replay, err := service.ReconcileAdmissions(ctx, request); err != nil || !reflect.DeepEqual(replay, ended) || store.Durable() != through+3 {
					t.Fatalf("reconciliation repeated an ending: %+v %v", replay, err)
				}
				assertScopedCrashAccounting(t, service, held, used)
			case "private_before_materialization":
				if signal.Receipt == nil {
					t.Fatal("missing committed private signal")
				}
				ids := submissionIDs{signal.Receipt.Origin.AgentID, signal.Receipt.Origin.SessionID, signal.Receipt.Origin.ContextID}
				path, err := service.scopePath(ids, "context")
				if err != nil {
					t.Fatal(err)
				}
				for _, change := range requests.Write.Changes {
					page, err := service.ReadPrivate(ctx, PrivateRead{ReadBinding: ReadBinding{Binding: binding, Scope: "context"}, Revision: snapshot.Revision + 1, Slot: change.Slot, MaxBytes: 4096})
					if err != nil || !bytes.Equal(page.Bytes, change.Bytes) {
						t.Fatalf("lost selected private revision: %+v %v", page, err)
					}
					file, err := path.markdownPath(change.Slot)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(readTestFile(t, file), change.Bytes) {
						t.Fatalf("Open reported but did not complete %s", change.Slot)
					}
				}
				if replay, err := agent.WritePrivate(ctx, requests.Write); err != nil || !reflect.DeepEqual(replay, *signal.Receipt) || store.Durable() != through {
					t.Fatalf("private lost-ack retry changed the revision: %+v %v", replay, err)
				}
				assertScopedCrashAccounting(t, service, held, used)
			case "ending_lost_ack":
				if signal.Receipt == nil {
					t.Fatal("missing committed ending signal")
				}
				work, err := service.TurnStatus(ctx, WorkLookup{WorkID: local.WorkID})
				if err != nil || !work.Ended || work.Dispatched || work.Outcome != "released_unsent" || work.Held != (Exposure{}) {
					t.Fatalf("lost acknowledged ending was not restored: %+v %v", work, err)
				}
				if replay, err := service.EndLocal(ctx, requests.End); err != nil || !reflect.DeepEqual(replay, *signal.Receipt) || store.Durable() != through {
					t.Fatalf("lost ending acknowledgment released twice: %+v %v", replay, err)
				}
				assertScopedCrashAccounting(t, service, held, used)
			case "revoke_before_projection":
				status, err := service.GrantStatus(ctx, GrantLookup{GrantID: grant.GrantID})
				if err != nil || !status.Revoked || status.Current {
					t.Fatalf("durable revoke was not restored: %+v %v", status, err)
				}
				page, err := agent.ReadPrivate(ctx, requests.Read)
				if err != ErrDenied || !reflect.DeepEqual(page, ScopedPrivatePage{}) {
					t.Fatalf("recovered revoke disclosed retry bytes: %+v %v", page, err)
				}
				lookup := ScopedCommandLookup{CommandID: requests.Read.CommandID, GrantID: grant.GrantID, SubmissionSHA256: requests.ReadReceipt.SubmissionSHA256}
				if receipt, err := agent.ScopedCommandStatus(ctx, lookup); err != ErrDenied || !reflect.DeepEqual(receipt, ScopedReceipt{}) {
					t.Fatalf("recovered revoke disclosed a prior receipt: %+v %v", receipt, err)
				}
				if store.Durable() != through {
					t.Fatal("recovered denial appended a record")
				}
				assertScopedCrashAccounting(t, service, held, used)
			}
		})
	}
}

func assertScopedCrashAccounting(t *testing.T, service *Service, held, used Exposure) {
	t.Helper()
	book, err := service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
	if err != nil || book.Held != held || book.Used != used {
		t.Fatalf("wrong recovered accounting: %+v; want held %+v used %+v; %v", book, held, used, err)
	}
}

func TestScopedCrashChild(t *testing.T) {
	root := os.Getenv("DWARF_SCOPED_CRASH_ROOT")
	if root == "" {
		return
	} // Only the parent supplies the subprocess protocol.
	boundary := os.Getenv("DWARF_SCOPED_CRASH_BOUNDARY")
	var requests scopedCrashRequests
	if err := gob.NewDecoder(bytes.NewReader(readTestFile(t, filepath.Join(root, "scoped-crash-input.gob")))).Decode(&requests); err != nil {
		t.Fatal(err)
	}
	store, _, err := ledgerstore.Open(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	segment := filepath.Join(store.LogDir(), ledger.SegmentName(1))
	var before uint64
	var prefix []byte
	armed := false
	stop := func(receipt *ScopedReceipt) {
		if err := gob.NewEncoder(os.Stdout).Encode(scopedCrashSignal{Boundary: boundary, Before: before, Receipt: receipt}); err != nil {
			t.Fatal(err)
		}
		os.Exit(privateCrashExit)
	}
	deps := serviceDeps{
		beforeSubmit: func(drafts []ledger.Draft) []ledger.Draft {
			if armed && boundary == "before_commit" {
				stop(nil)
			}
			return drafts
		},
		afterScopedSubmit: func() {
			if armed && boundary == "revoke_before_projection" {
				stop(nil)
			}
		},
		afterScopedCommit: func(receipt ScopedReceipt) error {
			if !armed {
				return nil
			}
			if boundary == "torn_admission" {
				full := readTestFile(t, segment)
				start := bytes.LastIndexByte(full[:len(full)-1], '\n') + 1
				cut := start + (len(full)-start)/2
				if start <= len(prefix) || cut <= start || cut >= len(full) || !bytes.Equal(full[:len(prefix)], prefix) {
					t.Fatal("injected cut is not inside the new group's final record")
				}
				file, err := os.OpenFile(segment, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(int64(cut)); err != nil {
					t.Fatal(err)
				}
				if err := file.Sync(); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			stop(&receipt)
			return nil
		},
	}
	service, _, err := openService(context.Background(), store, root, DefaultLimits(), deps)
	if err != nil {
		t.Fatal(err)
	}
	agent := scopedBound(t, service, requests.Binding)
	ctx := context.Background()
	if boundary == "dispatch_no_ending" || boundary == "ending_lost_ack" {
		if _, err := agent.AdmitLocal(ctx, requests.Local); err != nil {
			t.Fatal(err)
		}
	}
	before, prefix = store.Durable(), readTestFile(t, segment)
	armed = true
	switch boundary {
	case "before_commit", "torn_admission", "admission_lost_ack":
		_, err = agent.AdmitLocal(ctx, requests.Local)
	case "dispatch_no_ending":
		_, err = service.BeginLocal(ctx, requests.Dispatch)
	case "private_before_materialization":
		_, err = agent.WritePrivate(ctx, requests.Write)
	case "ending_lost_ack":
		_, err = service.EndLocal(ctx, requests.End)
	case "revoke_before_projection":
		_, err = service.RevokeGrant(ctx, requests.Binding, requests.Revoke)
	default:
		t.Fatalf("unknown injected boundary %q", boundary)
	}
	t.Fatalf("injected boundary was not reached: %v", err)
}
