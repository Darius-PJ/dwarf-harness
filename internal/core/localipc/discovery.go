package localipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/launcher"
)

const discoveryLimit = 4096

// PrepareRun may renew a previous sign-in session's owner-only Windows DACL.
// Like Publish, it must be called only while holding the colony Claim.
func (o *Owner) PrepareRun(dir string) error {
	_, err := o.privateDir(filepath.Join(dir, "run"), true)
	return err
}

func (o *Owner) ReadDiscovery(dir string, colony uuid.UUID) (Discovery, error) {
	var d Discovery
	run := filepath.Join(dir, "run")
	before, err := o.privateDir(run, false)
	if err != nil {
		return d, err
	}
	f, err := o.openPrivate(filepath.Join(run, "host.json"), false)
	if err != nil {
		return d, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, discoveryLimit+1))
	err = errors.Join(readErr, f.Close())
	if err != nil {
		return d, err
	}
	after, err := o.privateDir(run, false)
	if err != nil || !os.SameFile(before, after) {
		return d, ErrPrivate
	}
	if len(raw) > discoveryLimit {
		return d, ErrUnavailable
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&d) != nil || dec.Decode(new(any)) != io.EOF {
		return Discovery{}, ErrUnavailable
	}
	if d.Format != "dwarf-harness/host" || d.Version != 1 || d.ColonyID != colony || d.HostID == (uuid.UUID{}) || len(d.ProtocolVersions) == 0 || len(d.ProtocolVersions) > 16 {
		return Discovery{}, ErrUnavailable
	}
	endpoint, transport, err := o.address(colony, d.HostID)
	if err != nil || d.Endpoint != endpoint || d.Transport != transport {
		return Discovery{}, ErrUnavailable
	}
	return d, nil
}

// Publish is called only by the Claim holder, after listening is possible.
func (o *Owner) Publish(dir string, colony, host uuid.UUID, endpoint string) error {
	expected, transport, err := o.address(colony, host)
	if err != nil {
		return err
	}
	if endpoint != expected {
		return ErrPrivate
	}
	run := filepath.Join(dir, "run")
	before, err := o.privateDir(run, false)
	if err != nil {
		return err
	}
	pending := filepath.Join(run, "host.json.pending")
	f, err := o.preparePending(pending)
	if err == nil {
		if err := f.Close(); err != nil {
			return err
		}
		if err := os.Remove(pending); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err = o.openPrivate(pending, true)
	if err != nil {
		return err
	}
	d := Discovery{"dwarf-harness/host", 1, colony, host, transport, endpoint, []int64{launcher.DraftVersion}}
	err = json.NewEncoder(f).Encode(d)
	err = errors.Join(err, f.Close())
	if err != nil {
		_ = os.Remove(pending)
		return err
	}
	after, err := o.privateDir(run, false)
	if err != nil || !os.SameFile(before, after) {
		return ErrPrivate
	}
	return replaceDiscovery(pending, filepath.Join(run, "host.json"))
}

func (o *Owner) RemoveDiscovery(dir string, colony, host uuid.UUID) error {
	d, err := o.ReadDiscovery(dir, colony)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if d.HostID != host {
		return nil
	}
	return os.Remove(filepath.Join(dir, "run", "host.json"))
}
