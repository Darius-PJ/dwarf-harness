package gitprocess

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
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
	b, err := io.ReadAll(io.LimitReader(file, 4097))
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
	_, err = file.Write(b)
	if err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	return durable.Rename(tmp, path)
}
func (o *Owner) fencePath(id uuid.UUID) string {
	return filepath.Join(o.root, "runs", id.String(), "fence.json")
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
	var debris []struct {
		dir     string
		pending bool
	}
	if hasRuns {
		if _, err := realDirectory(runs); err != nil {
			return err
		}
		entries, err := os.ReadDir(runs)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			id, err := uuid.Parse(entry.Name())
			if err != nil || !validID(id) || id.String() != entry.Name() {
				return ErrInvalid
			}
			dir := filepath.Join(runs, entry.Name())
			if _, err := realDirectory(dir); err != nil {
				return err
			}
			files, err := os.ReadDir(dir)
			if err != nil {
				return err
			}
			if len(files) == 0 {
				debris = append(debris, struct {
					dir     string
					pending bool
				}{dir, false})
				continue
			}
			hasFence, hasPending := false, false
			for _, file := range files {
				switch file.Name() {
				case "fence.json":
					hasFence = true
				case "fence.json.pending":
					hasPending = true
				default:
					return ErrInvalid
				}
				info, err := os.Lstat(filepath.Join(dir, file.Name()))
				if err != nil || !info.Mode().IsRegular() {
					return errors.Join(ErrInvalid, err)
				}
			}
			if !hasFence && hasPending {
				raw, err := readRegular(filepath.Join(dir, "fence.json.pending"))
				if err != nil {
					return err
				}
				f, err := decodeFence(raw)
				if err != nil || f.RunID != id || f.State != "launch_pending" || (!missingIdentity && f.RootID != o.rootID) {
					return errors.Join(ErrInvalid, err)
				}
				debris = append(debris, struct {
					dir     string
					pending bool
				}{dir, true})
				continue
			}
			if missingIdentity {
				return ErrInvalid
			}
			raw, err := readRegular(o.fencePath(id))
			if err != nil {
				return err
			}
			f, err := decodeFence(raw)
			if err != nil || f.RootID != o.rootID || f.RunID != id {
				return errors.Join(ErrInvalid, err)
			}
			o.pending[id] = f
		}
	}
	// Validate the complete layout before removing any debris. A neighboring
	// malformed state must not turn a refused Open into a partial sweep.
	for _, item := range debris {
		if item.pending {
			if err := os.Remove(filepath.Join(item.dir, "fence.json.pending")); err != nil {
				return err
			}
		}
		if err := os.Remove(item.dir); err != nil {
			return err
		}
		if err := durable.SyncDir(runs); err != nil {
			return err
		}
		if item.pending {
			o.debris.PendingRuns++
		} else {
			o.debris.EmptyRuns++
		}
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
