package agents

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type serviceCrashSignal struct {
	Root, Boundary, State string
	Agent                 uuid.UUID
	Through               uint64
	Sample                int64
	Active, Notices       int
	CoverageBefore        int64
}

func TestServiceCrashConvergence(t *testing.T) {
	if boundary := os.Getenv("DWARF_SERVICE_CRASH"); boundary != "" {
		serviceCrashChild(t, boundary)
		return
	}
	if boundary := os.Getenv("DWARF_SERVICE_LIVE_CRASH"); boundary != "" {
		serviceLiveCrashChild(t, boundary)
		return
	}
	t.Parallel()
	testServiceLiveCrash(t)
	testServiceMetadataRecovery(t)
	testServiceStartup(t)
	testServiceDuplicateRecovery(t)
	// One crash-and-recover row for the service family.
	for _, boundary := range []string{"observation_before_financial"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			container := testRoot(t)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestServiceCrashConvergence$")
			cmd.Env = append(os.Environ(), "DWARF_SERVICE_CRASH="+boundary, "TMP="+container, "TEMP="+container, "TMPDIR="+container)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			t.Cleanup(func() {
				if !waited {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			})
			var signal serviceCrashSignal
			if err = gob.NewDecoder(stdout).Decode(&signal); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				waited = true
				t.Fatalf("child did not reach %s: %v", boundary, err)
			}
			relative, err := filepath.Rel(container, signal.Root)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || signal.Boundary != boundary || signal.Through == 0 {
				t.Fatal("invalid isolated crash boundary")
			}
			if err = cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			waited = true
			var killed *exec.ExitError
			if !errors.As(err, &killed) {
				t.Fatalf("child was not killed: %v", err)
			}
			store, recovery, err := ledgerstore.Open(signal.Root, ledger.Defined())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Error(err)
				}
			})
			if recovery.TruncatedBytes != 0 || store.Durable() != signal.Through {
				t.Fatal("acknowledged crash boundary was not preserved")
			}
			segment := filepath.Join(store.LogDir(), ledger.SegmentName(1))
			before := readTestFile(t, segment)
			sampled := false
			s, report, err := openService(context.Background(), store, signal.Root, DefaultLimits(), serviceDeps{reviewClock: func() (int64, error) { sampled = true; return signal.Sample, nil }})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			if sampled || len(report.Issues) != 0 || store.Durable() != signal.Through || !bytes.Equal(before, readTestFile(t, segment)) {
				t.Fatal("Open performed service recovery")
			}
			if signal.CoverageBefore >= 0 {
				v, err := s.ServiceStatus(context.Background(), signal.Agent)
				if err != nil {
					t.Fatal(err)
				}
				if v.SourceThrough != signal.CoverageBefore {
					t.Fatalf("captured E was covered before all transitions: %d/%d", v.SourceThrough, signal.CoverageBefore)
				}
			}
			if _, err = s.ReconcileServiceEvidence(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			v, err := s.ServiceStatus(context.Background(), signal.Agent)
			if err != nil {
				t.Fatal(err)
			}
			if v.State != signal.State || len(v.Active) != signal.Active || len(v.Pending) != 0 || v.Recovery.UncoveredObservations != 0 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != signal.Notices {
				t.Fatalf("crash did not converge: state=%s active=%d pending=%d recovery=%+v", v.State, len(v.Active), len(v.Pending), v.Recovery)
			}
			after := store.Durable()
			if _, err = s.ReconcileServiceEvidence(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			if store.Durable() != after || !bytes.HasPrefix(readTestFile(t, segment), before) {
				t.Fatal("recovery repeated an effect or rewrote history")
			}
		})
	}
}

func serviceCrashChild(t *testing.T, boundary string) {
	var f *serviceFixture
	signal := serviceCrashSignal{Boundary: boundary, CoverageBefore: -1}
	barrier := func() {
		signal.Root, signal.Through = f.root, f.store.Durable()
		if err := gob.NewEncoder(os.Stdout).Encode(signal); err != nil {
			t.Fatal(err)
		}
		var one [1]byte
		_, _ = os.Stdin.Read(one[:])
		t.Fatal("crash barrier unexpectedly released")
	}
	paid := newPaidFixture(t)
	f = paid.serviceFixture
	s := f.service
	now := s.p.reviewFloor + int64(time.Hour)
	s.deps.reviewClock = func() (int64, error) { return now, nil }
	signal.Agent, signal.State, signal.Active, signal.Notices, signal.Sample = paid.origin.AgentID, "lost", 1, 1, now
	signal.CoverageBefore = 0
	e := servicePaidFailure(t, paid, "credential_denied", now)
	s.deps.afterServiceCommit = func(g *serviceGroup) {
		if boundary == "observation_before_financial" && g.observed != nil {
			barrier()
		}
	}
	serviceRecord(t, s, e)
	t.Fatal("onset boundary not reached")
}

func testServiceDuplicateRecovery(t *testing.T) {
	t.Run("duplicate_new_command", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		now := f.service.p.reviewFloor + int64(time.Hour)
		failure := servicePaidFailure(t, f, "credential_denied", now)
		original := serviceRecord(t, f.service, failure)
		duplicate := func(phase string) {
			t.Helper()
			e := failure
			e.ObservationID = uuid.NewV7()
			e.Source.ObservationID = e.ObservationID
			through := f.store.Durable()
			got := serviceRecord(t, f.service, e)
			if f.store.Durable() != through || !sameScopedValue(got, original) {
				t.Fatalf("duplicate %s appended or changed its original receipt", phase)
			}
		}
		duplicate("while_lost")
		serviceRecord(t, f.service, serviceSuccessfulCall(t, f, failure.CauseID, now+1))
		serviceReopen(t, f.serviceFixture, serviceDeps{})
		duplicate("after_end")
		v, err := f.service.ServiceStatus(context.Background(), failure.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		m := f.service.p.service.members[serviceMemberKey{failure.AgentID, failure.CauseID}].snapshot
		outages := serviceSnapshot(t, f.service).Outages
		if v.State != "available" || m.Generation != 1 || m.Phase != "restored" || m.FirstSource.ObservationID != failure.ObservationID || len(outages) != 1 || outages[0].Active || serviceCountRecords(t, f.service, ledger.TypeRuntimeServiceOutageNotice) != 2 {
			t.Fatal("duplicate reopened an ended incident or replaced its generation")
		}
	})
}
