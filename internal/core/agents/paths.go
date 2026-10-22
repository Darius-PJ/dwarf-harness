package agents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
)

// ErrInvalid reports an invalid private-storage input or filesystem entry.
var ErrInvalid = errors.New("agents: invalid private storage")

// privateScope contains only validated path components. The caller serializes
// writes to a scope; these checks refuse unsafe entries, not same-user races.
type privateScope struct {
	colony  string
	agent   string
	context string
	dirs    *directoryDurability
}

// directoryDurability belongs to one storage instance and is shared by its
// scopes. File identity keeps a replaced directory from inheriting a path's
// durability. The lock covers checks and flushes of shared ancestors.
type directoryDurability struct {
	mu            sync.Mutex
	synced        map[string]os.FileInfo
	beforeSyncDir func(string) error // fault seam; nil in production
}

func newPrivateScope(colony, agent, context string, dirs *directoryDurability) (privateScope, error) {
	if dirs == nil {
		return privateScope{}, fmt.Errorf("%w: missing directory durability instance", ErrInvalid)
	}
	if err := validateBasename(agent, nil); err != nil {
		return privateScope{}, err
	}
	if context != "" {
		id, err := uuid.Parse(context)
		if err != nil || id.String() != context || !validID(id) {
			return privateScope{}, fmt.Errorf("%w: context is not a canonical UUIDv7", ErrInvalid)
		}
	}
	root, err := filepath.Abs(colony)
	if err != nil {
		return privateScope{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return privateScope{colony: root, agent: agent, context: context, dirs: dirs}, nil
}

func validID(id uuid.UUID) bool {
	return id[6]>>4 == 7 && id[8]&0xc0 == 0x80
}

func validateBasename(name string, registered []string) error {
	if len(name) == 0 || len(name) > 128 {
		return fmt.Errorf("%w: directory basename length", ErrInvalid)
	}
	for i := range len(name) {
		c := name[i]
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alnum && (i == 0 || c != '_' && c != '-') {
			return fmt.Errorf("%w: directory basename grammar", ErrInvalid)
		}
	}
	upper := strings.ToUpper(name)
	if upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" ||
		len(upper) == 4 && (upper[:3] == "COM" || upper[:3] == "LPT") && upper[3] >= '1' && upper[3] <= '9' {
		return fmt.Errorf("%w: reserved directory basename", ErrInvalid)
	}
	for _, existing := range registered {
		if strings.EqualFold(name, existing) {
			return fmt.Errorf("%w: directory basename already registered", ErrInvalid)
		}
	}
	return nil
}

func (s privateScope) components() []string {
	parts := []string{s.colony, "agents", s.agent}
	if s.context != "" {
		parts = append(parts, "contexts", s.context)
	}
	return parts
}

func (s privateScope) directory() string { return filepath.Join(s.components()...) }

func (s privateScope) objects() string { return filepath.Join(s.directory(), "core", "objects") }

func (s privateScope) ensureObjects() error {
	return s.dirs.checkDirectories(append(s.components(), "core", "objects"), true)
}

// checkDirectories checks every owned component on every call. With create set,
// it establishes child-before-parent durability, reusing only successful flushes
// by this instance. An unknown/replaced child also invalidates its parent's
// flush, because the parent must persist that child's directory entry.
// Read-only checks do not flush or mark entries.
func (d *directoryDurability) checkDirectories(parts []string, create bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if create && d.synced == nil {
		d.synced = make(map[string]os.FileInfo)
	}
	for i := range parts {
		path := filepath.Join(parts[:i+1]...)
		info, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) && create && i > 0 {
			if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, statErr = os.Lstat(path)
		}
		if statErr != nil {
			return statErr
		}
		if !realDirectory(info) {
			return fmt.Errorf("%w: directory entry %s", ErrInvalid, path)
		}
		if previous := d.synced[path]; create && (previous == nil || !os.SameFile(previous, info)) {
			delete(d.synced, path)
			if i > 0 {
				delete(d.synced, filepath.Join(parts[:i]...))
			}
		}
	}
	if create {
		for i := len(parts) - 1; i >= 0; i-- {
			path := filepath.Join(parts[:i+1]...)
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if !realDirectory(info) {
				return fmt.Errorf("%w: directory entry %s", ErrInvalid, path)
			}
			if previous := d.synced[path]; previous != nil && os.SameFile(previous, info) {
				continue
			}
			if d.beforeSyncDir != nil {
				if err := d.beforeSyncDir(path); err != nil {
					return err
				}
			}
			if err := durable.SyncDir(path); err != nil {
				return err
			}
			// Windows loads the file ID lazily; load it now so a replaced directory cannot later compare equal.
			if !os.SameFile(info, info) {
				return fmt.Errorf("%w: cannot identify directory %s", ErrInvalid, path)
			}
			d.synced[path] = info
		}
	}
	return nil
}

func realDirectory(info os.FileInfo) bool {
	return info.IsDir() && info.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0
}

func regularEntry(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: non-regular entry %s", ErrInvalid, path)
	}
	return info, nil
}

func (s privateScope) markdownPath(slot string) (string, error) {
	name := ""
	switch {
	case slot == "memory":
		name = "Memory.md"
	case slot == "instructions" && s.context == "":
		name = "Agent.md"
	case slot == "handoff" && s.context != "":
		name = "Handoff.md"
	default:
		return "", fmt.Errorf("%w: not a Markdown slot in this scope", ErrInvalid)
	}
	return filepath.Join(s.directory(), name), nil
}
