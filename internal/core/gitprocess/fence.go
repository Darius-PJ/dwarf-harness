package gitprocess

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
)

const fenceFormat = "dwarf-harness/git-process"
const identityFormat = "dwarf-harness/git-process-root"

type rootIdentity struct {
	Format  string    `json:"format"`
	Version int       `json:"version"`
	RootID  uuid.UUID `json:"root_id"`
}
type fence struct {
	Format         string    `json:"format"`
	Version        int       `json:"version"`
	RootID         uuid.UUID `json:"root_id"`
	RunID          uuid.UUID `json:"run_id"`
	JobName        string    `json:"job_name"`
	Session        uint32    `json:"windows_session_id"`
	State          string    `json:"state"`
	RootPID        uint32    `json:"root_pid,omitempty"`
	RootCreated    uint64    `json:"root_created_100ns,omitempty"`
	TotalProcesses *uint32   `json:"total_processes,omitempty"`
	Proof          string    `json:"proof,omitempty"`
}

func validID(id uuid.UUID) bool { return id[6]>>4 == 7 && id[8]&0xc0 == 0x80 }
func jobName(root, run uuid.UUID) string {
	return `Local\dwarf-harness-git-` + root.String() + "-" + run.String()
}
func canonicalJSON(value any) ([]byte, error) {
	b, err := json.Marshal(value)
	return append(b, '\n'), err
}
func decodeCanonical(raw []byte, value any) error {
	if len(raw) > 4096 {
		return ErrInvalid
	}
	if err := json.Unmarshal(raw, value); err != nil {
		return errors.Join(ErrInvalid, err)
	}
	canonical, err := canonicalJSON(value)
	if err != nil || !bytes.Equal(raw, canonical) {
		return ErrInvalid
	}
	return nil
}
func decodeFence(raw []byte) (fence, error) {
	var f fence
	if err := decodeCanonical(raw, &f); err != nil {
		return f, err
	}
	if f.Format != fenceFormat || f.Version != 1 || !validID(f.RootID) || !validID(f.RunID) || f.RootID == f.RunID || f.JobName != jobName(f.RootID, f.RunID) {
		return f, ErrInvalid
	}
	switch f.State {
	case "launch_pending":
		if f.RootPID != 0 || f.RootCreated != 0 || f.TotalProcesses != nil || f.Proof != "" {
			return f, ErrInvalid
		}
	case "running":
		if f.RootPID == 0 || f.RootCreated == 0 || f.TotalProcesses != nil || f.Proof != "" {
			return f, ErrInvalid
		}
	case "closed":
		if (f.RootPID == 0) != (f.RootCreated == 0) {
			return f, ErrInvalid
		}
		switch f.Proof {
		case "live", "retained_job":
			if f.TotalProcesses == nil {
				return f, ErrInvalid
			}
		case "name_absent":
			if f.TotalProcesses != nil {
				return f, ErrInvalid
			}
		default:
			return f, ErrInvalid
		}
	default:
		return f, ErrInvalid
	}
	return f, nil
}
func readRegular(path string) ([]byte, error) {
	return readRegularBounded(path, 4096)
}
func readRegularBounded(path string, bound int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, ErrInvalid
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) {
		return nil, errors.Join(ErrInvalid, statErr, file.Close())
	}
	b, err := io.ReadAll(io.LimitReader(file, bound+1))
	return b, errors.Join(err, file.Close())
}
func realDirectory(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalid
	}
	return info, nil
}
func writeMarker(path string, value any) error {
	b, err := canonicalJSON(value)
	if err != nil {
		return err
	}
	return writeMarkerBytes(path, b, nil)
}

// The seam surrounds real calls, recording attempts separately from success.
// Initial-publication hooks can run under reservationMu; unlike observations,
// they may only wait for an external crash parent, never another Owner operation.
func metadataCall(hook func(string, string, bool) error, operation, path string, call func() error) error {
	if hook != nil {
		if err := hook(operation, path, false); err != nil {
			return err
		}
	}
	if err := call(); err != nil {
		return err
	}
	if hook != nil {
		_ = hook(operation, path, true)
	}
	return nil
}

func writeMarkerBytes(path string, b []byte, hook func(string, string, bool) error) error {
	tmp := path + ".pending"
	if info, err := os.Lstat(tmp); err == nil && !info.Mode().IsRegular() {
		return ErrInvalid
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	err = metadataCall(hook, "temp_write", path, func() error {
		n, err := file.Write(b)
		if err == nil && n != len(b) {
			err = io.ErrShortWrite
		}
		return err
	})
	if err == nil {
		err = metadataCall(hook, "temp_sync", path, file.Sync)
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	return metadataCall(hook, "rename", path, func() error { return durable.Rename(tmp, path) })
}
func (o *Owner) fencePath(id uuid.UUID) string {
	return filepath.Join(o.root, "runs", id.String()+".json")
}
func (o *Owner) slotsPath(id uuid.UUID) string {
	return filepath.Join(o.root, "runs", id.String()+".slots")
}
func (o *Owner) loadFences() error {
	path := filepath.Join(o.root, "identity.json")
	raw, err := readRegular(path)
	missingIdentity := errors.Is(err, os.ErrNotExist)
	if err != nil && !missingIdentity {
		return err
	}
	if !missingIdentity {
		var identity rootIdentity
		if err := decodeCanonical(raw, &identity); err != nil {
			return err
		}
		if identity.Format != identityFormat || identity.Version != 1 || !validID(identity.RootID) {
			return ErrInvalid
		}
		o.rootID = identity.RootID
	}
	entries, err := os.ReadDir(o.root)
	if err != nil {
		return err
	}
	hasRuns, pendingIdentity := false, false
	for _, entry := range entries {
		switch entry.Name() {
		case "lock", "identity.json":
		case "identity.json.pending":
			pendingIdentity = true
			candidate, err := readRegular(filepath.Join(o.root, entry.Name()))
			if err != nil {
				return err
			}
			var identity rootIdentity
			if missingIdentity && decodeCanonical(candidate, &identity) == nil && identity.Format == identityFormat && identity.Version == 1 && validID(identity.RootID) {
				o.rootID = identity.RootID
			}
		case "runs":
			hasRuns = true
		default:
			return ErrInvalid
		}
	}
	runs := filepath.Join(o.root, "runs")
	var debris []string
	if hasRuns {
		if _, err := realDirectory(runs); err != nil {
			return err
		}
		entries, err := os.ReadDir(runs)
		if err != nil {
			return err
		}
		debris, err = o.loadRunLayout(entries, missingIdentity)
		if err != nil {
			return err
		}
	}
	// Validate the complete layout before removing any debris. A neighboring
	// malformed state must not turn a refused Open into a partial sweep.
	for _, path := range debris {
		if err := os.Remove(path); err != nil {
			return err
		}
		if err := durable.SyncDir(runs); err != nil {
			return err
		}
		o.debris.PendingRuns++
	}
	if missingIdentity {
		if !validID(o.rootID) {
			o.rootID = uuid.NewV7()
		}
		if err := writeMarker(path, rootIdentity{identityFormat, 1, o.rootID}); err != nil {
			return err
		}
		if pendingIdentity {
			o.debris.PendingIdentities++
		}
	} else if pendingIdentity {
		// No identity rewrite can authorize a child; the existing identity wins.
		if err := os.Remove(path + ".pending"); err != nil {
			return err
		}
	}
	if !hasRuns {
		if err := os.Mkdir(runs, 0o700); err != nil {
			return err
		}
	}
	return durable.SyncDir(o.root)
}

// All entries and cross-file links are checked before loadFences sweeps debris.
func (o *Owner) loadRunLayout(entries []os.DirEntry, missingIdentity bool) ([]string, error) {
	type files struct{ authoritative, pending bool }
	containers := make(map[uuid.UUID]files)
	records := make(map[uuid.UUID]files)
	for _, entry := range entries {
		var stem, suffix string
		for _, candidate := range []string{".slots.pending", ".slots", ".json.pending", ".json"} {
			if name, ok := strings.CutSuffix(entry.Name(), candidate); ok {
				stem, suffix = name, candidate
				break
			}
		}
		id, err := uuid.Parse(stem)
		if err != nil || !validID(id) || id.String() != stem {
			return nil, ErrInvalid
		}
		info, err := os.Lstat(filepath.Join(o.root, "runs", entry.Name()))
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.Join(ErrInvalid, err)
		}
		target := records
		if strings.HasPrefix(suffix, ".slots") {
			target = containers
		}
		group := target[id]
		if strings.HasSuffix(suffix, ".pending") {
			group.pending = true
		} else {
			group.authoritative = true
		}
		target[id] = group
	}
	o.loadedSlots = make(map[uuid.UUID]*slotBatch)
	membership := make(map[uuid.UUID]*slotBatch)
	used := map[uuid.UUID]bool{o.rootID: true}
	for id := range containers {
		if used[id] {
			return nil, ErrInvalid
		}
		used[id] = true
	}
	var debris []string
	for id, group := range containers {
		path := o.slotsPath(id)
		read := func(path string) (slotsContainer, error) {
			raw, err := readRegularBounded(path, slotsSize)
			if err != nil {
				return slotsContainer{}, err
			}
			c, err := decodeSlots(raw)
			if err != nil || c.Header.RootID != o.rootID || c.Header.BatchID != id {
				return c, errors.Join(ErrInvalid, err)
			}
			return c, nil
		}
		var c, temp slotsContainer
		if group.pending {
			var err error
			temp, err = read(path + ".pending")
			if err != nil || !temp.initial() {
				return nil, errors.Join(ErrInvalid, err)
			}
		}
		if group.authoritative {
			if missingIdentity {
				return nil, ErrInvalid
			}
			var err error
			c, err = read(path)
			if err != nil {
				return nil, err
			}
			if group.pending {
				a, _ := canonicalJSON(c.Header)
				b, _ := canonicalJSON(temp.Header)
				if !bytes.Equal(a, b) {
					return nil, ErrInvalid
				}
			}
		} else {
			c = temp
		}
		batch := &slotBatch{data: c}
		for i, run := range c.Header.RunIDs {
			if used[run] {
				return nil, ErrInvalid
			}
			used[run] = true
			if !group.authoritative {
				if _, exists := records[run]; exists {
					return nil, ErrInvalid
				}
				continue
			}
			membership[run] = batch
			if c.Frames[i] != (fence{}) && c.Retired[i] == 0 {
				o.pending[run] = c.Frames[i]
			}
		}
		if group.authoritative {
			var err error
			batch.identity, err = os.Lstat(path)
			if err != nil {
				return nil, err
			}
			o.loadedSlots[id] = batch
		} else {
			debris = append(debris, path+".pending")
		}
	}
	for id, group := range records {
		path := o.fencePath(id)
		batch := membership[id]
		if batch == nil && used[id] {
			return nil, ErrInvalid
		}
		read := func(path string) (fence, error) {
			raw, err := readRegular(path)
			if err != nil {
				return fence{}, err
			}
			f, err := decodeFence(raw)
			if err != nil || f.RootID != o.rootID || f.RunID != id {
				return f, errors.Join(ErrInvalid, err)
			}
			return f, nil
		}
		if batch != nil {
			for _, pending := range []bool{false, true} {
				if pending && !group.pending || !pending && !group.authoritative {
					continue
				}
				file := path
				if pending {
					file += ".pending"
				}
				f, err := read(file)
				if err != nil || !closedSlotMatches(batch.data, f) {
					return nil, errors.Join(ErrInvalid, err)
				}
			}
			continue
		}
		if !group.authoritative {
			f, err := read(path + ".pending")
			if err != nil || f.State != "launch_pending" {
				return nil, errors.Join(ErrInvalid, err)
			}
			debris = append(debris, path+".pending")
			continue
		}
		if missingIdentity {
			return nil, ErrInvalid
		}
		f, err := read(path)
		if err != nil {
			return nil, err
		}
		o.pending[id] = f
	}
	return debris, nil
}

func closedSlotMatches(c slotsContainer, f fence) bool {
	if f.State != "closed" || f.RootID != c.Header.RootID || f.Session != c.Header.Session {
		return false
	}
	for i, id := range c.Header.RunIDs {
		if id == f.RunID {
			frame := c.Frames[i]
			return f.RootPID == frame.RootPID && f.RootCreated == frame.RootCreated
		}
	}
	return false
}
