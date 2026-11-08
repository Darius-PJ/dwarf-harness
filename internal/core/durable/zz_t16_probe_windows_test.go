//go:build windows

package durable

// Ticket 16's S3 redesign probe. It is throwaway and never lands: it runs locally
// through -overlay and on a throwaway build/16-s3r-diag-* branch only.
//
// It times one fence-sized durable update, three ways, on each volume the tests
// can use:
//   - base: gitprocess's publication cycle (temp file, File.Sync, write-through
//     rename with its directory flush);
//   - slot_sync: an in-place write to a never-used slot of a preallocated file,
//     then File.Sync;
//   - slot_wt: the same write through a FILE_FLAG_WRITE_THROUGH handle, with no
//     separate flush.
//
// The figures (microseconds, from the performance counter) are the names of failing subtests, so that a hosted
// job's signed-out annotations show them. This is design evidence about cost
// only, not about any durability guarantee.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Go's clock on Windows advances in about 0.5 ms steps here, so the probe reads
// the performance counter instead.
var (
	t16Kernel = windows.NewLazySystemDLL("kernel32.dll")
	t16QPC    = t16Kernel.NewProc("QueryPerformanceCounter")
	t16QPF    = t16Kernel.NewProc("QueryPerformanceFrequency")
)

type t16Duration int64 // performance-counter ticks

func t16Now() t16Duration {
	var c int64
	_, _, _ = t16QPC.Call(uintptr(unsafe.Pointer(&c)))
	return t16Duration(c)
}

func (d t16Duration) Sub(e t16Duration) t16Duration { return d - e }

func t16Since(e t16Duration) t16Duration { return t16Now() - e }

func t16Freq() int64 {
	var f int64
	_, _, _ = t16QPF.Call(uintptr(unsafe.Pointer(&f)))
	return f
}

const (
	t16ProbeN    = 200
	t16ProbeSlot = 512
)

type t16Stat struct {
	P50, P95, Mean int64
}

func t16Stats(d []t16Duration) t16Stat {
	freq := t16Freq()
	us := make([]int64, len(d))
	var sum int64
	for i, v := range d {
		us[i] = int64(v) * 1_000_000 / freq
		sum += us[i]
	}
	sort.Slice(us, func(i, j int) bool { return us[i] < us[j] })
	return t16Stat{P50: us[len(us)/2], P95: us[(len(us)*95+99)/100-1], Mean: sum / int64(len(us))}
}

func TestT16FileCostProbe(t *testing.T) {
	if t16ProbeRace {
		t.Skip("the probe reports from the plain tests step only")
	}
	record := append(bytes.Repeat([]byte("x"), 300), '\n')
	type location struct{ name, dir string }
	temp := t.TempDir()
	locations := []location{{"TEMP", temp}}
	dStatus := "absent"
	if info, err := os.Stat(`D:\`); err == nil && info.IsDir() {
		if d, err := os.MkdirTemp(`D:\`, "dwarf-t16-probe-"); err == nil {
			t.Cleanup(func() { _ = os.RemoveAll(d) })
			locations = append(locations, location{"D", d})
			dStatus = "used"
		} else {
			dStatus = "unwritable"
		}
	}
	volume := strings.TrimSuffix(filepath.VolumeName(temp), ":")
	var rows []map[string]any
	names := []string{fmt.Sprintf("cpu%d_temp%s_d_%s_n%d_record%d_qpf%d", runtime.NumCPU(), volume, dStatus, t16ProbeN, len(record), t16Freq())}
	for _, loc := range locations {
		// base: the publication cycle, a fresh file each time as launch_pending.
		var bw, bs, br, bt []t16Duration
		for i := range t16ProbeN {
			path := filepath.Join(loc.dir, fmt.Sprintf("base-%03d.json", i))
			tmp := path + ".pending"
			t0 := t16Now()
			f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write(record); err != nil {
				t.Fatal(err)
			}
			t1 := t16Now()
			if err := f.Sync(); err != nil {
				t.Fatal(err)
			}
			t2 := t16Now()
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if err := Rename(tmp, path); err != nil {
				t.Fatal(err)
			}
			t3 := t16Now()
			bw, bs, br, bt = append(bw, t1.Sub(t0)), append(bs, t2.Sub(t1)), append(br, t3.Sub(t2)), append(bt, t3.Sub(t0))
		}
		prealloc := func(name string) string {
			path := filepath.Join(loc.dir, name)
			f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write(make([]byte, t16ProbeN*t16ProbeSlot)); err != nil {
				t.Fatal(err)
			}
			if err := f.Sync(); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if err := SyncDir(loc.dir); err != nil {
				t.Fatal(err)
			}
			return path
		}
		// slot_sync: in-place write to a never-used slot, then File.Sync.
		pa := prealloc("slots-a.bin")
		fa, err := os.OpenFile(pa, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		var aw, af, at []t16Duration
		for i := range t16ProbeN {
			t0 := t16Now()
			if _, err := fa.WriteAt(record, int64(i*t16ProbeSlot)); err != nil {
				t.Fatal(err)
			}
			t1 := t16Now()
			if err := fa.Sync(); err != nil {
				t.Fatal(err)
			}
			t2 := t16Now()
			aw, af, at = append(aw, t1.Sub(t0)), append(af, t2.Sub(t1)), append(at, t2.Sub(t0))
		}
		if err := fa.Close(); err != nil {
			t.Fatal(err)
		}
		// slot_wt: the same write through a write-through handle, no separate flush.
		pb := prealloc("slots-b.bin")
		name, err := windows.UTF16PtrFromString(pb)
		if err != nil {
			t.Fatal(err)
		}
		h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ, nil,
			windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_WRITE_THROUGH, 0)
		if err != nil {
			t.Fatal(err)
		}
		fb := os.NewFile(uintptr(h), pb)
		var wt []t16Duration
		for i := range t16ProbeN {
			t0 := t16Now()
			if _, err := fb.WriteAt(record, int64(i*t16ProbeSlot)); err != nil {
				t.Fatal(err)
			}
			wt = append(wt, t16Since(t0))
		}
		if err := fb.Close(); err != nil {
			t.Fatal(err)
		}
		b, s, r, tot := t16Stats(bw), t16Stats(bs), t16Stats(br), t16Stats(bt)
		a, aflush, atot := t16Stats(aw), t16Stats(af), t16Stats(at)
		w := t16Stats(wt)
		names = append(names,
			fmt.Sprintf("%s_base_tot50_%d_tot95_%d_mean_%d_w50_%d_sync50_%d_ren50_%d", loc.name, tot.P50, tot.P95, tot.Mean, b.P50, s.P50, r.P50),
			fmt.Sprintf("%s_slot_sync_tot50_%d_tot95_%d_mean_%d_w50_%d_flush50_%d", loc.name, atot.P50, atot.P95, atot.Mean, a.P50, aflush.P50),
			fmt.Sprintf("%s_slot_wt_tot50_%d_tot95_%d_mean_%d", loc.name, w.P50, w.P95, w.Mean))
		rows = append(rows, map[string]any{"location": loc.name, "base": map[string]t16Stat{"write": b, "sync": s, "rename": r, "total": tot},
			"slot_sync": map[string]t16Stat{"write": a, "flush": aflush, "total": atot}, "slot_wt": map[string]t16Stat{"total": w}})
	}
	raw, err := json.Marshal(map[string]any{"cpu": runtime.NumCPU(), "temp": volume, "d": dStatus, "n": t16ProbeN, "record": len(record), "rows": rows})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("T16_PROBE %s", raw)
	if out := os.Getenv("DWARF_T16_PROBE_OUT"); out != "" {
		if err := os.WriteFile(out, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range names {
		t.Run(n, func(t *testing.T) { t.Fail() })
	}
}
