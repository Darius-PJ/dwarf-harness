package ledgerstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
)

// colonyFile names the colony's identity file in its directory. Its content is
// Core-internal, not a contract.
const colonyFile = "colony.json"

// colonyFormat is the colony.json format this package writes and reads.
const colonyFormat = 1

// encodeColony returns the exact colony.json bytes for colony id:
// {"format":1,"colony_id":"<id>"} and LF.
func encodeColony(id uuid.UUID) []byte {
	return fmt.Appendf(nil, "{\"format\":%d,\"colony_id\":\"%s\"}\n", colonyFormat, id)
}

// writeColony publishes colony.json in dir for id: it writes and flushes a
// temporary file, then renames it into place durably, so colony.json is
// either absent or complete.
func writeColony(dir string, id uuid.UUID) error {
	tmp := filepath.Join(dir, colonyFile+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(encodeColony(id))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return durable.Rename(tmp, filepath.Join(dir, colonyFile))
}

// readColony returns the colony ID in dir's colony.json, which must be a JSON
// object with format 1 and a canonical UUIDv7 colony_id. Other keys are
// ignored, so later steps can add optional ones. A missing file is
// ErrNotColony.
func readColony(dir string) (uuid.UUID, error) {
	path := filepath.Join(dir, colonyFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return uuid.UUID{}, fmt.Errorf("%w: %s has no %s", ErrNotColony, dir, colonyFile)
	}
	if err != nil {
		return uuid.UUID{}, err
	}
	var c struct {
		Format   *int    `json:"format"`
		ColonyID *string `json:"colony_id"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return uuid.UUID{}, fmt.Errorf("ledgerstore: %s: %w", path, err)
	}
	switch {
	case c.Format == nil:
		return uuid.UUID{}, fmt.Errorf("ledgerstore: %s has no format", path)
	case *c.Format != colonyFormat:
		return uuid.UUID{}, fmt.Errorf("ledgerstore: %s has format %d; this build reads format %d", path, *c.Format, colonyFormat)
	case c.ColonyID == nil:
		return uuid.UUID{}, fmt.Errorf("ledgerstore: %s has no colony_id", path)
	}
	id, err := uuid.Parse(*c.ColonyID)
	if err != nil || id.String() != *c.ColonyID || !isV7(id) {
		return uuid.UUID{}, fmt.Errorf("ledgerstore: %s: colony_id %q is not a canonical UUIDv7", path, *c.ColonyID)
	}
	return id, nil
}

// isV7 reports whether u is a version 7 UUID of the RFC 9562 variant.
func isV7(u uuid.UUID) bool { return u[6]>>4 == 7 && u[8]&0xC0 == 0x80 }
