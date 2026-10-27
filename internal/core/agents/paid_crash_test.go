package agents

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

type paidCrashSeed struct {
	Origin               ledger.Origin
	Route, Credential    json.RawMessage
	Grant, Inspect, Turn uuid.UUID
	Next                 int
}
type paidCrashCommand struct {
	Receipt json.RawMessage
	Last    uint64
}
type paidCrashSignal struct {
	Boundary       string
	Before         uint64
	Admission      json.RawMessage
	Commands       []paidCrashCommand
	Evidence       ledger.ObjectRef
	Dispatch       PaidDispatch
	Credential     json.RawMessage
	Authority      string
	Posts, Written int64
	Poisoned       bool
}
type paidCrashRow struct {
	name        string
	records     uint64
	version     int64
	state, cost string
	posts       int64
}

// Platform rows inject an actual append failure rather than a mock store.
var paidCrashLockAppend func(*testing.T, string) func()

func paidCrashRows() []paidCrashRow {
	rows := []paidCrashRow{
		{"before_publication", 0, 0, "", "0", 0},
		{"before_reservation", 0, 0, "", "0", 0},
		{"torn_reservation", 0, 0, "", "0", 0},
		{"reservation_before_projection", 3, 1, "reserved", "0", 0},
		{"reservation_lost_ack", 3, 1, "reserved", "0", 0},
		{"torn_intent", 3, 1, "reserved", "0", 0},
		{"intent_before_bytes", 6, 2, "sent", "0", 0},
		{"partial_post", 6, 2, "sent", "0", 0},
		{"full_post", 6, 2, "sent", "0", 1},
		{"partial_stream", 6, 2, "sent", "0", 1},
		{"final_stream", 6, 2, "sent", "0", 1},
		{"publication_failure", 6, 2, "sent", "0", 1},
		{"published_no_result", 6, 2, "sent", "0", 1},
		{"torn_result", 6, 2, "sent", "0", 1},
		{"result_before_projection", 9, 3, "settled", "11", 1},
		{"result_lost_ack", 9, 3, "settled", "11", 1},
		{"unknown_before_projection", 9, 3, "unknown", "0", 1},
		{"unknown_lost_ack", 9, 3, "unknown", "0", 1},
		{"no_charge_before_projection", 9, 3, "no_charge", "0", 1},
		{"no_charge_lost_ack", 9, 3, "no_charge", "0", 1},
		{"release_before_projection", 6, 2, "released_unsent", "0", 0},
		{"release_lost_ack", 6, 2, "released_unsent", "0", 0},
		{"overrun_before_projection", 9, 3, "settled", "125", 1},
		{"overrun_lost_ack", 9, 3, "settled", "125", 1},
		{"correction_before_projection", 12, 4, "settled", "13", 1},
		{"correction_lost_ack", 12, 4, "settled", "13", 1},
		{"acknowledgement_before_projection", 12, 4, "settled", "125", 1},
		{"acknowledgement_lost_ack", 12, 4, "settled", "125", 1},
		{"revoke_before_intent", 6, 1, "reserved", "0", 0},
		{"intent_before_revoke", 9, 2, "sent", "0", 1},
		{"cap_before_intent", 6, 1, "reserved", "0", 0},
		{"intent_before_cap", 9, 2, "sent", "0", 1},
		{"rollover_before_intent", 6, 2, "sent", "0", 1},
		{"intent_before_rollover", 9, 2, "sent", "0", 1},
	}
	if paidCrashLockAppend != nil {
		rows = append(rows, paidCrashRow{"uncertain_intent", 3, 1, "reserved", "0", 0})
	}
	return rows
}

// Injected process/filesystem boundaries, not power-loss, isolation or a proof
// that concurrently restarting hosts can share a colony. Each child is waited
// before the parent opens any file from its isolated, closed real-store seed.
func TestPaidInjectedCrashRecovery(t *testing.T) {
	t.Parallel()
	seed := newLivePaidFixture(t)
	input := paidCrashSeed{Origin: seed.origin, Route: paidEncode(t, seed.route), Credential: paidEncode(t, seed.credential), Grant: seed.grant, Inspect: seed.inspect, Turn: seed.turn, Next: seed.next}
	var encoded bytes.Buffer
	if err := gob.NewEncoder(&encoded).Encode(input); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(seed.root, "paid-crash-seed.gob"), encoded.Bytes())
	if err := seed.service.Close(); err != nil {
		t.Fatal(err)
	}
	seed.service = nil
	if err := seed.store.Close(); err != nil {
		t.Fatal(err)
	}
	seed.store = nil
	for _, row := range paidCrashRows() {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := testRoot(t)
			if err := os.CopyFS(root, os.DirFS(seed.root)); err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe, "-test.run=^TestPaidCrashChild$")
			cmd.Env = append(os.Environ(), "DWARF_PAID_CRASH_ROOT="+root, "DWARF_PAID_CRASH_BOUNDARY="+row.name)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != privateCrashExit {
				t.Fatalf("child missed injected boundary: %v\n%s", err, output)
			}
			var signal paidCrashSignal
			if err := gob.NewDecoder(bytes.NewReader(output)).Decode(&signal); err != nil || signal.Boundary != row.name {
				t.Fatalf("invalid child boundary: %v\n%s", err, output)
			}
			if signal.Posts != row.posts || row.name == "partial_post" && signal.Written <= 0 || row.name == "uncertain_intent" && !signal.Poisoned {
				t.Fatalf("wrong observed I/O/poison boundary: %+v", signal)
			}
			// Reuse the child's actual authority, not an unrelated listener: an
			// accidental recovered send would reach this handler after rebinding.
			listener, err := net.Listen("tcp", signal.Authority)
			if err != nil {
				t.Fatal(err)
			}
			var recoveredPosts atomic.Int64
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				recoveredPosts.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			_ = server.Listener.Close()
			server.Listener = listener
			server.StartTLS()
			t.Cleanup(server.Close)
			t.Cleanup(func() {
				if recoveredPosts.Load() != 0 {
					t.Error("recovered attempt reached the rebound loopback authority")
				}
			})
			store, recovery, err := ledgerstore.Open(root, ledger.Defined())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if (recovery.TruncatedBytes > 0) != strings.HasPrefix(row.name, "torn_") {
				t.Fatalf("wrong torn-tail recovery: %+v", recovery)
			}
			through := signal.Before + row.records
			if store.Durable() != through {
				t.Fatalf("partial group/frontier: %d want %d", store.Durable(), through)
			}
			segment := filepath.Join(store.LogDir(), ledger.SegmentName(1))
			prefix := readTestFile(t, segment)
			deps := paidServerDeps(server, nil)
			deps.reviewClock = func() (int64, error) { t.Error("recovery/query/retry sampled clock"); return 0, ErrPaidClock }
			deps.beforeRead = func(Binding, string) error { t.Error("Open read paid artifact bytes"); return ErrIntegrity }
			service, report, err := openPaidService(context.Background(), store, root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, deps)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			if len(report.Issues) != 0 || len(report.Completed) != 0 || store.Durable() != through || !bytes.Equal(prefix, readTestFile(t, segment)) || recoveredPosts.Load() != 0 {
				t.Fatalf("Open performed work: %+v", report)
			}
			service.deps.beforeRead = nil
			a, err := ledger.DecodeRuntimePaidReserved(signal.Admission)
			if err != nil {
				t.Fatal(err)
			}
			view, err := service.PaidAttemptStatus(context.Background(), PaidAttemptLookup{AttemptID: a.AttemptID})
			ended := row.state != "" && row.state != "reserved" && row.state != "sent"
			fenced := strings.HasPrefix(row.name, "overrun_")
			if row.version == 0 {
				if !errors.Is(err, ErrDenied) {
					t.Fatalf("uncommitted attempt installed: %+v %v", view, err)
				}
			} else if err != nil || view.Version != row.version || view.State != row.state || view.SettledMicros != row.cost || view.Ended != ended || view.Fenced != fenced || view.Acknowledged != strings.HasPrefix(row.name, "acknowledgement_") {
				t.Fatalf("wrong recovered attempt: %+v %v", view, err)
			}
			held := "0"
			if row.state == "reserved" || row.state == "sent" || row.state == "unknown" {
				held = strconv.FormatInt(a.Quote.MaximumMicros, 10)
			}
			if row.version > 0 && view.HeldMicros != held {
				t.Fatalf("recovery refunded uncertainty: %+v", view)
			}
			paidCrashMoney(t, service, a, held, row.cost, fenced)
			if strings.Contains(row.name, "rollover") {
				current, err := service.PaidBudgetStatus(context.Background(), PaidBudgetLookup{Scope: ledger.PaidScope{Kind: "colony"}})
				if err != nil || current.Period == a.AdmissionPeriod || current.SettledMicros != "0" || current.HeldMicros != held {
					t.Fatalf("rollover moved or released exposure: %+v %v", current, err)
				}
			}
			for _, command := range signal.Commands {
				r, err := ledger.DecodeRuntimePaidReceipt(command.Receipt)
				if err != nil {
					t.Fatal(err)
				}
				got, err := service.PaidCommandStatus(context.Background(), PaidCommandLookup{CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256})
				if command.Last > through {
					if !errors.Is(err, ErrUnavailable) {
						t.Fatalf("torn/unsubmitted command consumed: %+v %v", got, err)
					}
					continue
				}
				if err != nil || got.Sequence != command.Last || !reflect.DeepEqual(got.RuntimePaidReceipt, r) {
					t.Fatalf("receipt changed on recovery: %+v %v", got, err)
				}
				if r.Submission.Operation != "paid.dispatch" {
					paidCrashRetry(t, service, r, got.Sequence, false)
				}
			}
			if store.Durable() != through || !bytes.Equal(prefix, readTestFile(t, segment)) {
				t.Fatal("matched retries appended or rewrote history")
			}
			credential, err := ledger.DecodeRuntimeCredentialChanged(signal.Credential)
			if err != nil {
				t.Fatal(err)
			}
			sample := service.p.reviewFloor + int64(time.Hour)
			service.deps.reviewClock = func() (int64, error) { return sample, nil }
			_, err = service.BindCredential(context.Background(), bindingOf(input.Origin), CredentialBinding{CommandID: paidGoldenID(0xf010), ScopeID: credential.ScopeID, ExpectedVersion: service.p.paidCredentialScopes[credential.ScopeID].version, Provider: "anthropic", CredentialVersion: paidGoldenID(0xf011), SourceKind: "environment", SourceLocator: paidTestEnvironment})
			if err != nil {
				t.Fatal(err)
			}
			through = store.Durable()
			service.deps.reviewClock = deps.reviewClock
			for _, command := range signal.Commands {
				r, err := ledger.DecodeRuntimePaidReceipt(command.Receipt)
				if err != nil {
					t.Fatal(err)
				}
				if r.Submission.Operation == "paid.dispatch" && command.Last <= signal.Before+row.records {
					denied := row.name == "intent_before_revoke" || row.name == "intent_before_rollover" || strings.HasPrefix(row.name, "acknowledgement_")
					paidCrashRetry(t, service, r, command.Last, denied)
				}
			}
			if row.version > 0 {
				_, err := service.DispatchPaid(context.Background(), PaidDispatch{CommandID: paidGoldenID(0xf001), AttemptID: a.AttemptID, ExpectedVersion: row.version})
				want := ErrPaidSent
				if row.state == "reserved" {
					want = ErrUnavailable
				}
				if row.name == "revoke_before_intent" || row.name == "intent_before_revoke" || row.name == "intent_before_rollover" || strings.HasPrefix(row.name, "acknowledgement_") {
					want = ErrDenied
				}
				if !errors.Is(err, want) || store.Durable() != through {
					t.Fatalf("recovered attempt regained permission: %v want %v", err, want)
				}
			}
			if recoveredPosts.Load() != 0 || store.Durable() != through {
				t.Fatal("rebound recovered attempt sent or appended")
			}
			if strings.HasPrefix(row.name, "acknowledgement_") {
				if !service.p.retiredProfiles[a.ProfileSHA256] {
					t.Fatal("acknowledgement lost retired profile")
				}
			}
			if row.state == "reserved" || row.state == "sent" {
				req := PaidReconciliation{CommandID: paidGoldenID(0xf002), AttemptID: a.AttemptID, ExpectedVersion: row.version}
				first, err := service.ReconcilePaid(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				want := "unknown"
				if row.state == "reserved" {
					want, held = "released_unsent", "0"
				}
				v, err := service.PaidAttemptStatus(context.Background(), PaidAttemptLookup{AttemptID: a.AttemptID})
				if err != nil || v.State != want || v.Version != row.version+1 || !v.Ended || v.HeldMicros != held {
					t.Fatalf("wrong reconciliation: %+v %v", v, err)
				}
				paidCrashMoney(t, service, a, held, "0", false)
				retry, err := service.ReconcilePaid(context.Background(), req)
				if err != nil || retry.Sequence != first.Sequence || store.Durable() != through+3 {
					t.Fatal("reconciliation retry duplicated money")
				}
				if row.name == "published_no_result" || row.name == "torn_result" {
					late := PaidObservation{CommandID: paidGoldenID(0xf003), AttemptID: a.AttemptID, ExpectedVersion: v.Version, TrustedEvidence: &signal.Evidence}
					r, err := service.RecordPaidObservation(context.Background(), late)
					if err != nil {
						t.Fatal(err)
					}
					paidCrashMoney(t, service, a, "0", "11", false)
					late.CommandID = paidGoldenID(0xf004)
					dup, err := service.RecordPaidObservation(context.Background(), late)
					if err != nil || dup.Sequence != r.Sequence || store.Durable() != through+6 {
						t.Fatal("late evidence settled more than once")
					}
				}
			}
			t.Logf("injected boundary=%s child_exit=%d frontier=%d version=%d state=%s posts=%d", row.name, privateCrashExit, through, row.version, row.state, signal.Posts)
		})
	}
}

func paidCrashMoney(t *testing.T, s *Service, a ledger.RuntimePaidReserved, held, cost string, fenced bool) {
	t.Helper()
	for _, scope := range a.Scopes {
		v, err := s.PaidBudgetStatus(context.Background(), PaidBudgetLookup{Scope: scope, Period: a.AdmissionPeriod})
		if err != nil || v.HeldMicros != held || v.SettledMicros != cost || v.Fenced != fenced {
			t.Fatalf("scope %v changed original-period money: %+v %v", scope, v, err)
		}
	}
}
func paidCrashRetry(t *testing.T, s *Service, r ledger.RuntimePaidReceipt, sequence uint64, deniedDispatch bool) {
	t.Helper()
	ctx := context.Background()
	var got PaidReceipt
	var err error
	switch a := r.Submission.Arguments.(type) {
	case ledger.PaidAttemptArguments:
		if r.Submission.Operation == "paid.dispatch" {
			var out PaidDispatchOutcome
			out, err = s.DispatchPaid(ctx, PaidDispatch{CommandID: r.CommandID, AttemptID: a.AttemptID, ExpectedVersion: a.ExpectedVersion})
			got = out.IntentReceipt
			if out.ObservedPaidCall != nil {
				t.Fatal("retry returned an observation/send capability")
			}
			// The passive receipt above survives revocation/route retirement;
			// dispatch still checks current authority before looking it up.
			if deniedDispatch {
				if !errors.Is(err, ErrDenied) || got.Sequence != 0 {
					t.Fatal("matched dispatch bypassed current authority")
				}
				return
			}
		} else {
			got, err = s.ReconcilePaid(ctx, PaidReconciliation{CommandID: r.CommandID, AttemptID: a.AttemptID, ExpectedVersion: a.ExpectedVersion})
		}
	case ledger.PaidObserveArguments:
		got, err = s.RecordPaidObservation(ctx, PaidObservation{CommandID: r.CommandID, AttemptID: a.AttemptID, ExpectedVersion: a.ExpectedVersion, TrustedEvidence: &a.EvidenceObject})
	case ledger.PaidCorrectArguments:
		got, err = s.CorrectPaidSettlement(ctx, PaidCorrection{PaidObservation: PaidObservation{CommandID: r.CommandID, AttemptID: a.AttemptID, ExpectedVersion: a.ExpectedVersion, TrustedEvidence: &a.EvidenceObject}, PreviousResultSequence: a.PreviousResultSequence, PreviousEvidenceSHA256: a.PreviousEvidenceSHA256})
	case ledger.PaidOverrunArguments:
		got, err = s.AcknowledgePaidOverrun(ctx, bindingOf(r.Submission.Origin), PaidOverrunAcknowledgement{CommandID: r.CommandID, AttemptID: a.AttemptID, ExpectedVersion: a.ExpectedVersion, ResultSequence: a.ResultSequence, EvidenceSHA256: a.EvidenceSHA256, RetiredProfileSHA256: a.RetiredProfileSHA256})
	default:
		return // Admission handles intentionally do not survive an incarnation.
	}
	if err != nil || got.Sequence != sequence {
		t.Fatalf("matched retry did not preserve original receipt: %+v %v", got, err)
	}
}

type paidCrashConn struct {
	net.Conn
	armed *atomic.Bool
	stop  func(int)
}

func (c *paidCrashConn) Write(raw []byte) (int, error) {
	if c.armed.CompareAndSwap(true, false) {
		n, err := c.Conn.Write(raw[:max(1, len(raw)/2)])
		if err != nil {
			return n, err
		}
		c.stop(n)
	}
	return c.Conn.Write(raw)
}

func TestPaidCrashChild(t *testing.T) {
	root := os.Getenv("DWARF_PAID_CRASH_ROOT")
	if root == "" {
		return
	}
	boundary := os.Getenv("DWARF_PAID_CRASH_BOUNDARY")
	var seed paidCrashSeed
	if err := gob.NewDecoder(bytes.NewReader(readTestFile(t, filepath.Join(root, "paid-crash-seed.gob")))).Decode(&seed); err != nil {
		t.Fatal(err)
	}
	store, _, err := ledgerstore.Open(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	f := &paidFixture{serviceFixture: &serviceFixture{root: root, store: store}, origin: seed.Origin, grant: seed.Grant, inspect: seed.Inspect, turn: seed.Turn, next: seed.Next}
	f.route, err = ledger.DecodeRuntimePaidRouteConfigured(seed.Route)
	if err != nil {
		t.Fatal(err)
	}
	f.credential, err = ledger.DecodeRuntimeCredentialChanged(seed.Credential)
	if err != nil {
		t.Fatal(err)
	}
	signal := paidCrashSignal{Boundary: boundary}
	var posts atomic.Int64
	stop := func() {
		signal.Posts = posts.Load()
		if err := gob.NewEncoder(os.Stdout).Encode(signal); err != nil {
			t.Fatal(err)
		}
		os.Exit(privateCrashExit)
	}
	server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		model := paidRequestModel(t, r)
		if boundary == "full_post" {
			stop()
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Request-Id", "request-one")
		usage := `{"input_tokens":7,"output_tokens":4}`
		if strings.HasPrefix(boundary, "acknowledgement_") || strings.HasPrefix(boundary, "overrun_") {
			usage = `{"input_tokens":121,"output_tokens":4}`
		}
		if strings.HasPrefix(boundary, "no_charge_") {
			_, _ = io.WriteString(w, paidSSE(model, `{"input_tokens":0,"output_tokens":0}`, `{"output_tokens":0}`, "", "refusal", `{"type":"refusal","category":null}`, true))
			return
		}
		complete := boundary != "partial_stream" && !strings.HasPrefix(boundary, "unknown_")
		_, _ = io.WriteString(w, paidSSE(model, `{"input_tokens":3,"output_tokens":1}`, usage, "answer", "end_turn", "", complete))
	})
	var armed atomic.Bool
	deps := paidServerDeps(server, func(c net.Conn) net.Conn {
		return &paidCrashConn{Conn: c, armed: &armed, stop: func(n int) { signal.Written = int64(n); stop() }}
	})
	signal.Authority = deps.paidTestAuthority
	f.service, _, err = openPaidService(context.Background(), store, root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, deps)
	if err != nil {
		t.Fatal(err)
	}
	sample := f.service.p.reviewFloor + int64(time.Hour)
	f.service.deps.reviewClock = func() (int64, error) { return sample, nil }
	version := f.id()
	_, err = f.service.BindCredential(context.Background(), bindingOf(f.origin), CredentialBinding{CommandID: f.id(), ScopeID: f.credential.ScopeID, ExpectedVersion: f.credential.Version, Provider: "anthropic", CredentialVersion: version, SourceKind: "environment", SourceLocator: paidTestEnvironment, RevokePreviousVersion: f.credential.CredentialVersion})
	if err != nil {
		t.Fatal(err)
	}
	f.credential = f.service.p.paidCredentials[version]
	signal.Credential = paidEncode(t, f.credential)
	f.liveProfile(t, func(p *providers.Profile) {
		p.Authority = deps.paidTestAuthority
		if strings.HasPrefix(boundary, "no_charge_") {
			p.ModelID, p.RequestedTier = providers.Sonnet, "auto"
			p.NoChargeRules = []string{"anthropic_refusal_before_output_2026_09_24"}
		}
	})
	signal.Before = store.Durable()
	segment := filepath.Join(store.LogDir(), ledger.SegmentName(1))
	var operation string
	var releaseAppend func()
	f.service.deps.beforeSubmit = func(d []ledger.Draft) []ledger.Draft {
		if d[len(d)-1].Type != ledger.TypeRuntimePaidReceipt {
			t.Fatal("unexpected non-paid transition")
		}
		r, err := ledger.DecodeRuntimePaidReceipt(d[len(d)-1].Payload)
		if err != nil {
			t.Fatal(err)
		}
		operation = r.Submission.Operation
		if operation != "paid.admit" {
			signal.Commands = append(signal.Commands, paidCrashCommand{Receipt: bytes.Clone(d[len(d)-1].Payload), Last: store.Durable() + 3})
		}
		if boundary == "before_reservation" && operation == "paid.admit" {
			stop()
		}
		if boundary == "uncertain_intent" && operation == "paid.dispatch" {
			releaseAppend = paidCrashLockAppend(t, segment)
		}
		return d
	}
	f.service.deps.afterScopedSubmit = func() {
		torn := boundary == "torn_reservation" && operation == "paid.admit" || boundary == "torn_intent" && operation == "paid.dispatch" || boundary == "torn_result" && operation == "paid.observe"
		if torn {
			raw := readTestFile(t, segment)
			start := bytes.LastIndexByte(raw[:len(raw)-1], '\n') + 1
			file, err := os.OpenFile(segment, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Truncate(int64(start + (len(raw)-start)/2)); err != nil {
				t.Fatal(err)
			}
			if err := file.Sync(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			stop()
		}
		if boundary == "reservation_before_projection" && operation == "paid.admit" || boundary == "intent_before_bytes" && operation == "paid.dispatch" || boundary == "result_before_projection" && operation == "paid.observe" || boundary == "correction_before_projection" && operation == "paid.correct" || boundary == "acknowledgement_before_projection" && operation == "paid.overrun.acknowledge" {
			stop()
		}
		if strings.HasSuffix(boundary, "_before_projection") &&
			(operation == "paid.release" && strings.HasPrefix(boundary, "release_") ||
				operation == "paid.observe" && (strings.HasPrefix(boundary, "unknown_") || strings.HasPrefix(boundary, "no_charge_") || strings.HasPrefix(boundary, "overrun_"))) {
			stop()
		}
	}
	fault := errors.New("injected paid publication directory sync failure")
	f.service.deps.beforePublish = func(_ Binding, kind string) error {
		if kind == "paid_input" && boundary == "before_publication" {
			stop()
		}
		if kind == "paid_observation" && (boundary == "partial_stream" || boundary == "final_stream") {
			stop()
		}
		if kind == "paid_observation" && boundary == "publication_failure" {
			f.service.dirs.synced = nil
			f.service.dirs.beforeSyncDir = func(string) error { return fault }
		}
		return nil
	}
	input := f.input()
	if boundary == "partial_post" {
		input.Request.Messages[0].Content[0].Text = strings.Repeat("x", 64<<10)
	}
	prepared, err := f.service.PrepareAnthropic(context.Background(), bindingOf(f.origin), input)
	if err != nil {
		t.Fatal(err)
	}
	admission := prepared.state.admission
	admission.AdmissionUTCNS, admission.AdmissionPeriod = sample, paidPeriod("utc_day", sample)
	signal.Admission = paidEncode(t, admission)
	request := PaidAdmission{CommandID: f.id(), AttemptID: input.AttemptID, GrantID: input.GrantID, ParentTurnID: input.ParentTurnID, Prepared: prepared}
	sub := ledger.PaidSubmission{Schema: 4, Actor: "agent", Origin: f.origin, Operation: "paid.admit", GrantID: input.GrantID, Arguments: paidAdmitArguments(admission)}
	digest, err := paidDigest(sub)
	if err != nil {
		t.Fatal(err)
	}
	receipt := ledger.RuntimePaidReceipt{CommandID: request.CommandID, SubmissionSHA256: digest, Submission: sub, Outcome: "reserved", Version: 1, AttemptID: input.AttemptID}
	signal.Commands = append(signal.Commands, paidCrashCommand{Receipt: paidEncode(t, receipt), Last: signal.Before + 3})
	f.admit(t, request)
	signal.Admission = paidEncode(t, prepared.state.admission)
	if boundary == "reservation_lost_ack" {
		stop()
	}
	if strings.HasPrefix(boundary, "release_") {
		if _, err := f.service.ReconcilePaid(context.Background(), PaidReconciliation{CommandID: f.id(), AttemptID: input.AttemptID, ExpectedVersion: 1}); err != nil {
			t.Fatal(err)
		}
		stop()
	}
	revoke := func() {
		_, err := f.service.RevokeCredential(context.Background(), bindingOf(f.origin), CredentialRevocation{CommandID: f.id(), ScopeID: f.credential.ScopeID, ExpectedVersion: f.credential.Version, Provider: "anthropic", CredentialVersion: f.credential.CredentialVersion})
		if err != nil {
			t.Fatal(err)
		}
	}
	cap := func() {
		f.liveSpend(t, ledger.PaidScope{Kind: "colony"}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0", PeriodKind: "utc_day"})
	}
	if boundary == "revoke_before_intent" {
		revoke()
	}
	if boundary == "cap_before_intent" {
		cap()
	}
	if boundary == "rollover_before_intent" {
		sample += int64(24 * time.Hour)
	}
	dispatch := PaidDispatch{CommandID: f.id(), AttemptID: input.AttemptID, ExpectedVersion: 1}
	signal.Dispatch = dispatch
	ctx := context.Background()
	if boundary == "partial_post" {
		ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { armed.Store(true) }})
	}
	out, err := f.service.DispatchPaid(ctx, dispatch)
	if boundary == "uncertain_intent" {
		if releaseAppend == nil || !errors.Is(err, appender.ErrUncertain) {
			t.Fatalf("real append did not fail uncertain: %v", err)
		}
		releaseAppend()
		_, err = f.service.DispatchPaid(ctx, dispatch)
		if !errors.Is(err, ErrPoisoned) {
			t.Fatalf("uncertain invocation was reusable: %v", err)
		}
		signal.Poisoned = true
		stop()
	}
	if boundary == "revoke_before_intent" || boundary == "cap_before_intent" {
		if err == nil || out.IntentReceipt.Sequence != 0 {
			t.Fatal("ordered denial allowed intent")
		}
		stop()
	}
	if boundary == "publication_failure" {
		if !errors.Is(err, fault) || out.IntentReceipt.Sequence == 0 || out.ObservedPaidCall != nil {
			t.Fatalf("publication fault did not retain sent intent: %v", err)
		}
		stop()
	}
	if err != nil {
		t.Fatal(err)
	}
	signal.Evidence, err = out.ObservedPaidCall.Evidence()
	if err != nil {
		t.Fatal(err)
	}
	if boundary == "published_no_result" || boundary == "rollover_before_intent" {
		stop()
	}
	if boundary == "intent_before_revoke" {
		revoke()
		stop()
	}
	if boundary == "intent_before_cap" {
		cap()
		stop()
	}
	if boundary == "intent_before_rollover" {
		sample += int64(24 * time.Hour)
		f.liveProfile(t, func(p *providers.Profile) { p.CapabilityVersion += "-rollover" })
		stop()
	}
	paidRecord(t, f, dispatch, out)
	if boundary == "result_lost_ack" || boundary == "unknown_lost_ack" || boundary == "no_charge_lost_ack" || boundary == "overrun_lost_ack" {
		stop()
	}
	v := f.view(t, input.AttemptID)
	if strings.HasPrefix(boundary, "correction_") {
		late := paidLateEvidence(t, f, out, func(o *paidObservationObject) { o.RawUsageMerged["input_tokens"] = json.RawMessage(`9`) })
		_, err = f.service.CorrectPaidSettlement(ctx, PaidCorrection{PaidObservation: PaidObservation{CommandID: f.id(), AttemptID: input.AttemptID, ExpectedVersion: 3, TrustedEvidence: &late}, PreviousResultSequence: int64(v.ResultSequence), PreviousEvidenceSHA256: v.Result.EvidenceSHA256})
	} else if strings.HasPrefix(boundary, "acknowledgement_") {
		if !v.Fenced || v.SettledMicros != "125" {
			t.Fatal("acknowledgement fixture lacks true overrun")
		}
		_, err = f.service.AcknowledgePaidOverrun(ctx, bindingOf(f.origin), PaidOverrunAcknowledgement{CommandID: f.id(), AttemptID: input.AttemptID, ExpectedVersion: 3, ResultSequence: int64(v.ResultSequence), EvidenceSHA256: v.Result.EvidenceSHA256, RetiredProfileSHA256: v.Admission.ProfileSHA256})
	} else {
		t.Fatalf("unreached boundary %s", boundary)
	}
	if err != nil {
		t.Fatal(err)
	}
	stop()
}
