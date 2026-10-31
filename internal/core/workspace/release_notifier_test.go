package workspace

import (
	"context"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

// The host's release notifier fires after each release that can let an
// immediate reserve succeed, with the release already visible, and never for
// a reservation, a refusal or a repeated release.
func TestReservationReleaseNotifier(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	colony := t.TempDir()
	store, err := ledgerstore.Create(colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	svc, _, err := Open(ctx, store, colony, Config{Capacity: &CapacityProfile{MaxFiles: 1, MaxBytes: 10, BytesInFlight: 10, CheckpointsPerPair: 4, GenerationWindow: 4}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := svc.Close(); err != nil {
			t.Error(err)
		}
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	type seen struct {
		bytes        int64
		aFree, cIdle bool
	}
	pool := svc.pool
	var calls []seen
	// The callback runs under the pool mutex, so it may read the pool.
	svc.SetReleaseNotifier(func() {
		calls = append(calls, seen{pool.bytes, pool.access("a").exclusive == nil, pool.access("c").writers == 0})
	})
	want := func(step string, n int) {
		t.Helper()
		if len(calls) != n {
			t.Fatalf("%s: %d notifications, want %d", step, len(calls), n)
		}
	}
	first, err := pool.reserve("a", observationReservation)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.reserve("b", observationReservation)
	assertBusy(t, err, "capture_budget")
	want("reserve and refusal", 0)
	if _, err := first.beginCapture("a"); err != nil {
		t.Fatal(err)
	}
	first.releaseCaptureBytes()
	want("capture bytes returned", 1)
	if calls[0].bytes != 0 {
		t.Fatalf("capture release notified before its bytes returned: %+v", calls[0])
	}
	second, err := pool.reserve("b", observationReservation)
	if err != nil {
		t.Fatalf("notified release did not admit the refused capture: %v", err)
	}
	_, err = pool.reserve("a", closureReservation)
	assertBusy(t, err, "scratch_busy")
	first.release()
	want("exclusive cleared", 2)
	if !calls[1].aFree {
		t.Fatal("exclusive release notified before the holder cleared")
	}
	first.release()
	want("repeated release", 2)
	closure, err := pool.reserve("a", closureReservation)
	if err != nil {
		t.Fatal(err)
	}
	closure.release()
	want("closure released", 3)
	done, err := pool.beginWrite(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.reserve("c", closureReservation)
	assertBusy(t, err, "scratch_busy")
	want("writer admitted", 3)
	done()
	want("writer count dropped", 4)
	if !calls[3].cIdle {
		t.Fatal("writer release notified before its count dropped")
	}
	done()
	want("repeated writer release", 4)
	svc.SetReleaseNotifier(nil)
	second.release()
	want("removed notifier", 4)
}
