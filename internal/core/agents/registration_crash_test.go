package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type registrationCrashInput struct {
	Origin  Binding
	Request DeclarationChange
}

type registrationCrashSignal struct {
	Boundary                 string
	Before                   uint64
	Digest                   string
	Receipt                  json.RawMessage `json:",omitempty"`
	EffectSequence, Sequence uint64
	Cut                      int64
}

// These are deliberate process exits and a torn-byte injection, not power-loss,
// hardware-durability or exactly-once-delivery qualification. Children run
// serially and CombinedOutput positively waits/reaps before any store is opened.
func TestRegistrationInjectedCrashRecovery(t *testing.T) {
	ctx := context.Background()
	// Frozen commit 14 ends at seq 50 with declaration 0x500's active
	// context/task replacement. The first withdrawal is in seqs 51-53.
	const activePrefix = 14
	prepared, commits := openRegistrationFixture(t, activePrefix)
	commits = commits[:activePrefix]
	baseThrough := prepared.store.Durable()
	last := commits[len(commits)-1]
	priorReceipt := scopedDecode(t, ledger.DecodeRuntimeRegistrationReceipt, last.Records[2].Payload)
	args := priorReceipt.Submission.Arguments.(ledger.DeclarationArguments)
	input := registrationCrashInput{
		Origin: bindingOf(priorReceipt.Submission.Origin),
		Request: DeclarationChange{
			CommandID: privateID(13900), DeclarationID: args.DeclarationID,
			ExpectedVersion: uint64(priorReceipt.Version), Subject: args.Subject,
			Audience: args.Audience, Consumers: []ledger.DeclarationConsumer{},
		},
	}
	baseView, err := prepared.service.DeclarationStatus(ctx, RegistrationLookup{ID: args.DeclarationID, ThroughSequence: ledger.ToEnd})
	if err != nil || len(baseView.Record.Consumers) == 0 {
		t.Fatalf("crash withdrawal requires active declaration consumers: %+v %v", baseView, err)
	}
	t.Logf("crash prefix: complete_groups=%d durable=%d active_declaration=%s version=%d consumers=%+v", len(commits), baseThrough, baseView.Record.DeclarationID, baseView.Record.Version, baseView.Record.Consumers)
	baseTask, err := prepared.service.TaskStatus(ctx, TaskLookup{TaskID: registrationGoldenID(0x300)})
	if err != nil {
		t.Fatal(err)
	}
	baseGrant, err := prepared.service.GrantStatus(ctx, GrantLookup{GrantID: registrationGoldenID(0x600)})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(prepared.root, "registration-crash-input.json"), encoded)
	if err := prepared.service.Close(); err != nil {
		t.Fatal(err)
	}
	prepared.service = nil
	if err := prepared.store.Close(); err != nil {
		t.Fatal(err)
	}
	prepared.store = nil
	prefix := readTestFile(t, filepath.Join(prepared.root, "log", ledger.SegmentName(1)))

	for _, boundary := range []string{"before_commit", "torn_final_group", "durable_before_install", "installed_before_ack"} {
		t.Run(boundary, func(t *testing.T) {
			root := testRoot(t)
			if err := os.CopyFS(root, os.DirFS(prepared.root)); err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe, "-test.run=^TestRegistrationCrashChild$")
			cmd.Dir = root
			// The child needs no provider environment or credentials. Retain the
			// complete, deliberately restricted launch environment in the log.
			for _, key := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "TMPDIR", "HOME", "USERPROFILE", "LOCALAPPDATA", "GORACE", "GODEBUG", "GOCOVERDIR"} {
				if value, ok := os.LookupEnv(key); ok {
					cmd.Env = append(cmd.Env, key+"="+value)
				}
			}
			cmd.Env = append(cmd.Env, "DWARF_REGISTRATION_CRASH_ROOT="+root, "DWARF_REGISTRATION_CRASH_BOUNDARY="+boundary)
			t.Logf("child launch: argv=%q cwd=%q env=%q requested=%s", cmd.Args, cmd.Dir, cmd.Env, boundary)
			output, waitErr := cmd.CombinedOutput()
			t.Logf("child wait: reaped=%t state=%v error=%v output=%s", cmd.ProcessState != nil, cmd.ProcessState, waitErr, output)
			var exit *exec.ExitError
			if cmd.ProcessState == nil || !errors.As(waitErr, &exit) || exit.ExitCode() != privateCrashExit {
				t.Fatalf("child did not positively exit/reap at %s: %v", boundary, waitErr)
			}
			var signal registrationCrashSignal
			if err := json.Unmarshal(output, &signal); err != nil || signal.Boundary != boundary || signal.Before != baseThrough {
				t.Fatalf("child did not reach requested boundary: %+v %v", signal, err)
			}
			digest, err := ledger.ParseDigest(signal.Digest)
			if err != nil || digest == (ledger.Digest{}) {
				t.Fatalf("invalid child command digest %q: %v", signal.Digest, err)
			}

			segment := filepath.Join(root, "log", ledger.SegmentName(1))
			beforeRepair := readTestFile(t, segment)
			if !bytes.HasPrefix(beforeRepair, prefix) {
				t.Fatal("process death changed previously complete history")
			}
			store, repair, err := ledgerstore.Open(root, ledger.Defined())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			committed := boundary == "durable_before_install" || boundary == "installed_before_ack"
			through := baseThrough
			if committed {
				through += 3
			}
			repaired := readTestFile(t, segment)
			t.Logf("Store.Open: repair=%+v bytes=%d->%d durable=%d sha256=%x", repair, len(beforeRepair), len(repaired), store.Durable(), sha256.Sum256(repaired))
			if store.Durable() != through || (repair.TruncatedBytes > 0) != (boundary == "torn_final_group") {
				t.Fatalf("partial or missing recovered group: durable=%d want=%d repair=%+v", store.Durable(), through, repair)
			}
			if boundary == "torn_final_group" {
				if signal.Cut != int64(len(beforeRepair)) || !bytes.Equal(repaired, prefix) {
					t.Fatal("Store.Open did not discard exactly the injected incomplete group")
				}
			} else if !bytes.Equal(repaired, beforeRepair) {
				t.Fatal("Store.Open changed an untorn history")
			}
			if !committed && (!bytes.Equal(repaired, prefix) || len(signal.Receipt) != 0) {
				t.Fatal("uncommitted operation retained an effect or successful receipt")
			}
			service, report, err := Open(ctx, store, root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			if len(report.Issues) != 0 || store.Durable() != through || !bytes.Equal(repaired, readTestFile(t, segment)) {
				t.Fatalf("Service replay appended or changed repaired history: %+v", report)
			}
			t.Logf("agents.Open: durable=%d ledger_unchanged=true report=%+v", store.Durable(), report)
			assertRegistrationPrefix(t, service, commits, len(commits), baseThrough)
			for _, c := range commits {
				if c.Records[len(c.Records)-1].Type != ledger.TypeRuntimeRegistrationReceipt {
					continue
				}
				r := scopedDecode(t, ledger.DecodeRuntimeRegistrationReceipt, c.Records[2].Payload)
				want := RegistrationReceipt{RuntimeRegistrationReceipt: r, EffectSequence: c.First + 1, Sequence: c.Last}
				got, err := service.RegistrationCommandStatus(ctx, RegistrationCommandLookup{CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256})
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("previous complete receipt lost: %+v want %+v: %v", got, want, err)
				}
			}
			task, err := service.TaskStatus(ctx, TaskLookup{TaskID: baseTask.Task.TaskID})
			if err != nil || !reflect.DeepEqual(task.Task, baseTask.Task) {
				t.Fatalf("previous task lost: %+v %v", task, err)
			}
			grant, err := service.GrantStatus(ctx, GrantLookup{GrantID: baseGrant.Grant.GrantID})
			wantGrant := baseGrant
			wantGrant.ThroughSequence = through
			if err != nil || !reflect.DeepEqual(grant, wantGrant) {
				t.Fatalf("previous grant changed: %+v %v", grant, err)
			}
			wantView := baseView
			wantView.ThroughSequence = through
			lookup := RegistrationCommandLookup{CommandID: input.Request.CommandID, SubmissionSHA256: digest}
			var original RegistrationReceipt
			if committed {
				original = RegistrationReceipt{RuntimeRegistrationReceipt: scopedDecode(t, ledger.DecodeRuntimeRegistrationReceipt, signal.Receipt), EffectSequence: signal.EffectSequence, Sequence: signal.Sequence}
				stored := registrationStoredReceipt(t, store, input.Request.CommandID)
				got, err := service.RegistrationCommandStatus(ctx, lookup)
				if err != nil || !reflect.DeepEqual(got, original) || !reflect.DeepEqual(stored, original) || original.Sequence != through || original.EffectSequence != baseThrough+2 {
					t.Fatalf("original actual receipt not recovered: got=%+v stored=%+v original=%+v err=%v", got, stored, original, err)
				}
				wantView.Record.ExpectedVersion = wantView.Record.Version
				wantView.Record.Version++
				wantView.Record.Consumers = []ledger.DeclarationConsumer{}
				wantView.EffectSequence, wantView.ReceiptSequence = original.EffectSequence, original.Sequence
			} else if got, err := service.RegistrationCommandStatus(ctx, lookup); !errors.Is(err, ErrUnavailable) || !reflect.DeepEqual(got, RegistrationReceipt{}) {
				t.Fatalf("uncommitted receipt became visible: %+v %v", got, err)
			}
			view, err := service.DeclarationStatus(ctx, RegistrationLookup{ID: input.Request.DeclarationID, ThroughSequence: ledger.ToEnd})
			if err != nil || !reflect.DeepEqual(view, wantView) {
				t.Fatalf("partial or missing declaration recovery: %+v want %+v: %v", view, wantView, err)
			}
			first, err := service.SetDeclaration(ctx, input.Origin, input.Request)
			if err != nil {
				t.Fatal(err)
			}
			if committed {
				if !reflect.DeepEqual(first, original) || !bytes.Equal(repaired, readTestFile(t, segment)) {
					t.Fatal("committed retry changed receipt or ledger")
				}
			} else {
				through += 3
				original = registrationStoredReceipt(t, store, input.Request.CommandID)
				if !reflect.DeepEqual(first, original) || first.EffectSequence != baseThrough+2 || first.Sequence != through {
					t.Fatalf("first complete retry did not return its actual receipt: %+v %+v", first, original)
				}
			}
			afterFirst := readTestFile(t, segment)
			again, err := service.SetDeclaration(ctx, input.Origin, input.Request)
			if err != nil || !reflect.DeepEqual(again, original) || store.Durable() != through || !bytes.Equal(afterFirst, readTestFile(t, segment)) || !bytes.HasPrefix(afterFirst, prefix) {
				t.Fatalf("matching retry duplicated a complete effect: %+v %v durable=%d want=%d", again, err, store.Durable(), through)
			}
			view, err = service.DeclarationStatus(ctx, RegistrationLookup{ID: input.Request.DeclarationID, ThroughSequence: ledger.ToEnd})
			if err != nil || view.Record.Version != baseView.Record.Version+1 || len(view.Record.Consumers) != 0 || view.ReceiptSequence != original.Sequence {
				t.Fatalf("retry failed to install exactly one withdrawal: %+v %v", view, err)
			}
			t.Logf("recovery/retry: previously_complete_bytes=%d original_receipt=%+v final_durable=%d no_duplicate=true", len(prefix), original, store.Durable())
		})
	}
}

// Read the actual complete group, independently of the Service projection and
// acknowledgement. A durable-before-install child can use this while mu is held.
func registrationStoredReceipt(t *testing.T, store *ledgerstore.Store, command uuid.UUID) RegistrationReceipt {
	t.Helper()
	reader, err := ledger.OpenReader(store.LogDir(), ledger.ReadOptions{Colony: store.Colony(), Through: store.Durable(), AllTypes: true, Consume: ledger.Consumption{}})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for {
		commit, err := reader.Next()
		if errors.Is(err, io.EOF) {
			t.Fatalf("missing committed registration receipt for %s", command)
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range commit.Records {
			if record.Type != ledger.TypeRuntimeRegistrationReceipt {
				continue
			}
			r := scopedDecode(t, ledger.DecodeRuntimeRegistrationReceipt, record.Payload)
			if r.CommandID == command {
				if len(commit.Records) != 3 || record.Seq != commit.Last {
					t.Fatal("receipt is not in one complete registration group")
				}
				return RegistrationReceipt{RuntimeRegistrationReceipt: r, EffectSequence: commit.Records[1].Seq, Sequence: record.Seq}
			}
		}
	}
}

func TestRegistrationCrashChild(t *testing.T) {
	root := os.Getenv("DWARF_REGISTRATION_CRASH_ROOT")
	if root == "" {
		return // Only the parent supplies the subprocess protocol.
	}
	boundary := os.Getenv("DWARF_REGISTRATION_CRASH_BOUNDARY")
	var input registrationCrashInput
	if err := json.Unmarshal(readTestFile(t, filepath.Join(root, "registration-crash-input.json")), &input); err != nil {
		t.Fatal(err)
	}
	store, _, err := ledgerstore.Open(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	segment := filepath.Join(store.LogDir(), ledger.SegmentName(1))
	prefix := readTestFile(t, segment)
	signal := registrationCrashSignal{Boundary: boundary, Before: store.Durable()}
	stop := func(receipt *RegistrationReceipt) {
		if receipt != nil {
			var err error
			signal.Receipt, err = receipt.RuntimeRegistrationReceipt.Encode()
			if err != nil {
				t.Fatal(err)
			}
			signal.EffectSequence, signal.Sequence = receipt.EffectSequence, receipt.Sequence
		}
		if err := json.NewEncoder(os.Stdout).Encode(signal); err != nil {
			t.Fatal(err)
		}
		os.Exit(privateCrashExit) // No Service.Close, Store.Close or deferred cleanup.
	}
	var service *Service
	deps := serviceDeps{
		beforeSubmit: func(d []ledger.Draft) []ledger.Draft {
			signal.Digest = scopedDecode(t, ledger.DecodeCommand, d[0].Payload).SubmissionSHA256.String()
			if boundary == "before_commit" {
				stop(nil)
			}
			return d
		},
		afterScopedSubmit: func() {
			switch boundary {
			case "durable_before_install":
				receipt := registrationStoredReceipt(t, store, input.Request.CommandID)
				stop(&receipt)
			case "torn_final_group":
				// Deliberate truncation models an incomplete final group; it is
				// not evidence that a completed flush loses bytes on power failure.
				full := readTestFile(t, segment)
				start := bytes.LastIndexByte(full[:len(full)-1], '\n') + 1
				cut := start + (len(full)-start)/2
				if start <= len(prefix) || cut <= start || cut >= len(full) || !bytes.HasPrefix(full, prefix) {
					t.Fatal("cut is not inside the new group's final record")
				}
				file, err := os.OpenFile(segment, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(int64(cut)); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
				if err := file.Sync(); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				signal.Cut = int64(cut)
				stop(nil)
			}
		},
		afterRegistrationCommit: func(receipt RegistrationReceipt) error {
			if boundary == "installed_before_ack" {
				view, err := service.DeclarationStatus(context.Background(), RegistrationLookup{ID: input.Request.DeclarationID, ThroughSequence: ledger.ToEnd})
				if err != nil || view.Record.Version != receipt.Version || view.ReceiptSequence != receipt.Sequence || len(view.Record.Consumers) != 0 {
					t.Fatalf("ack boundary lacks committed public state: %+v %v", view, err)
				}
				actual := registrationStoredReceipt(t, store, input.Request.CommandID)
				if !reflect.DeepEqual(actual, receipt) {
					t.Fatalf("ack boundary returned predicted receipt: %+v want %+v", receipt, actual)
				}
				stop(&receipt)
			}
			return nil
		},
	}
	service, _, err = openService(context.Background(), store, root, DefaultLimits(), deps)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.SetDeclaration(context.Background(), input.Origin, input.Request)
	t.Fatalf("injected boundary %q was not reached: %v", boundary, err)
}
