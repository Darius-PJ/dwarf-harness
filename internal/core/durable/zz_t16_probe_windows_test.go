//go:build windows

package durable

// Ticket 16's S3 redesign probe, version 2. It is throwaway and never lands: it runs
// locally through -overlay and on a throwaway build/16-s3r-diag-* branch only.
//
// It times one fence-sized durable update, eleven ways, on each volume the tests can
// use (TEMP, and D: when present). 200 updates each, a fresh directory per mode:
//   - base: gitprocess's publication cycle (temp file, File.Sync, write-through
//     rename with its directory flush);
//   - slotsync: WriteAt to a never-used slot of a preallocated file through one
//     handle held for all updates, then File.Sync;
//   - slotwt: the same through one held FILE_FLAG_WRITE_THROUGH handle, no Sync;
//   - opensync: per update, open the preallocated file, WriteAt, File.Sync, Close;
//   - openwt: per update, open it write-through, WriteAt, Close;
//   - retire: per update, open it, write one byte, Close, with no flush;
//   - oc: per update, open it and Close, with no write;
//   - opensyncS, retireS, ocS and retireK: the same on a 20,992-byte (S) or 1 KiB (K)
//     preallocated file instead of the 102,400-byte one.
//
// The figures (microseconds, from the performance counter, because Go's clock steps
// about 0.5 ms here) are the names of failing subtests, so that a hosted job's
// signed-out annotations show them: one header and one line per volume, within the
// workflow's ten-annotation cap. This is design evidence about cost only, not about
// any durability guarantee.

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

const (
	t16ProbeN    = 200
	t16ProbeSlot = 512
)

var (
	t16Kernel = windows.NewLazySystemDLL("kernel32.dll")
	t16QPC    = t16Kernel.NewProc("QueryPerformanceCounter")
	t16QPF    = t16Kernel.NewProc("QueryPerformanceFrequency")
)

func t16Ticks() int64 {
	var c int64
	_, _, _ = t16QPC.Call(uintptr(unsafe.Pointer(&c)))
	return c
}

func t16Freq() int64 {
	var f int64
	_, _, _ = t16QPF.Call(uintptr(unsafe.Pointer(&f)))
	return f
}

type t16Stat struct {
	P50, P95, Mean int64
}

// t16Time runs op n times and returns microsecond statistics of its durations.
func t16Time(t *testing.T, op func(i int) error) t16Stat {
	t.Helper()
	freq := t16Freq()
	us := make([]int64, t16ProbeN)
	var sum int64
	for i := range t16ProbeN {
		t0 := t16Ticks()
		if err := op(i); err != nil {
			t.Fatal(err)
		}
		us[i] = (t16Ticks() - t0) * 1_000_000 / freq
		sum += us[i]
	}
	sort.Slice(us, func(i, j int) bool { return us[i] < us[j] })
	return t16Stat{P50: us[len(us)/2], P95: us[(len(us)*95+99)/100-1], Mean: sum / int64(len(us))}
}

// t16Prealloc creates a published, flushed file of size zeroed bytes in dir.
func t16Prealloc(t *testing.T, dir string, size int) string {
	t.Helper()
	path := filepath.Join(dir, "slots.bin")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(make([]byte, size)); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := SyncDir(dir); err != nil {
		t.Fatal(err)
	}
	return path
}

func t16OpenWriteThrough(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_WRITE_THROUGH, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
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
	// Sizes: big is 200 slots of 512 bytes; s is the S3 redesign brief's 20,992-byte
	// container; k is 1 KiB. The suffix picks the preallocated file's size.
	modes := []string{"base", "slotsync", "slotwt", "opensync", "openwt", "retire", "oc", "opensyncS", "retireS", "retireK", "ocS"}
	sizes := map[string]int{"opensyncS": 20992, "retireS": 20992, "ocS": 20992, "retireK": 1024}
	names := []string{fmt.Sprintf("v2_cpu%d_temp%s_d_%s_n%d_record%d_qpf%d", runtime.NumCPU(), volume, dStatus, t16ProbeN, len(record), t16Freq())}
	results := map[string]map[string]t16Stat{}
	for _, loc := range locations {
		stats := map[string]t16Stat{}
		for _, mode := range modes {
			dir := filepath.Join(loc.dir, mode)
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			size, kind := t16ProbeN*t16ProbeSlot, mode
			if s, ok := sizes[mode]; ok {
				size, kind = s, strings.TrimRight(mode, "SK")
			}
			switch kind {
			case "base":
				stats[mode] = t16Time(t, func(i int) error {
					path := filepath.Join(dir, fmt.Sprintf("base-%03d.json", i))
					tmp := path + ".pending"
					f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
					if err != nil {
						return err
					}
					if _, err := f.Write(record); err != nil {
						return err
					}
					if err := f.Sync(); err != nil {
						return err
					}
					if err := f.Close(); err != nil {
						return err
					}
					return Rename(tmp, path)
				})
			case "slotsync":
				f, err := os.OpenFile(t16Prealloc(t, dir, size), os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				stats[mode] = t16Time(t, func(i int) error {
					if _, err := f.WriteAt(record, int64(i*t16ProbeSlot%size)); err != nil {
						return err
					}
					return f.Sync()
				})
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			case "slotwt":
				f, err := t16OpenWriteThrough(t16Prealloc(t, dir, size))
				if err != nil {
					t.Fatal(err)
				}
				stats[mode] = t16Time(t, func(i int) error {
					_, err := f.WriteAt(record, int64(i*t16ProbeSlot%size))
					return err
				})
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			case "opensync":
				path := t16Prealloc(t, dir, size)
				stats[mode] = t16Time(t, func(i int) error {
					f, err := os.OpenFile(path, os.O_RDWR, 0)
					if err != nil {
						return err
					}
					_, err = f.WriteAt(record, int64(i*t16ProbeSlot%size))
					if err == nil {
						err = f.Sync()
					}
					if cerr := f.Close(); err == nil {
						err = cerr
					}
					return err
				})
			case "openwt":
				path := t16Prealloc(t, dir, size)
				stats[mode] = t16Time(t, func(i int) error {
					f, err := t16OpenWriteThrough(path)
					if err != nil {
						return err
					}
					_, err = f.WriteAt(record, int64(i*t16ProbeSlot%size))
					if cerr := f.Close(); err == nil {
						err = cerr
					}
					return err
				})
			case "retire":
				path := t16Prealloc(t, dir, size)
				stats[mode] = t16Time(t, func(i int) error {
					f, err := os.OpenFile(path, os.O_RDWR, 0)
					if err != nil {
						return err
					}
					_, err = f.WriteAt([]byte{'r'}, int64((i*t16ProbeSlot+t16ProbeSlot-1)%size))
					if cerr := f.Close(); err == nil {
						err = cerr
					}
					return err
				})
			case "oc":
				path := t16Prealloc(t, dir, size)
				stats[mode] = t16Time(t, func(i int) error {
					f, err := os.OpenFile(path, os.O_RDWR, 0)
					if err != nil {
						return err
					}
					return f.Close()
				})
			}
		}
		results[loc.name] = stats
		var p50, p95 []string
		for _, mode := range modes {
			p50 = append(p50, fmt.Sprintf("%s_%d", mode, stats[mode].P50))
			p95 = append(p95, fmt.Sprintf("%s_%d", mode, stats[mode].P95))
		}
		names = append(names, fmt.Sprintf("%s_p50_%s_p95_%s", loc.name, strings.Join(p50, "_"), strings.Join(p95, "_")))
	}
	raw, err := json.Marshal(map[string]any{"version": 2, "cpu": runtime.NumCPU(), "temp": volume, "d": dStatus, "n": t16ProbeN, "record": len(record), "results": results})
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
