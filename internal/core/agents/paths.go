package agents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
}

func newPrivateScope(colony, agent, context string) (privateScope, error) {
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
	return privateScope{colony: root, agent: agent, context: context}, nil
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
	return checkDirectories(append(s.components(), "core", "objects"), true)
}

// checkDirectories checks from the colony root down; it never follows an owned
// symlink or reparse point. The colony itself must already exist. New directories
// and their first existing parent are flushed child-before-parent, even if a
// later creation fails. No unused sibling producer directories are created.
func checkDirectories(parts []string, create bool) (err error) {
	firstCreated, lastCreated := -1, -1
	defer func() {
		if firstCreated >= 0 {
			for i := lastCreated; i >= firstCreated-1; i-- {
				path := filepath.Join(parts[:i+1]...)
				info, statErr := os.Lstat(path)
				switch {
				case statErr != nil:
					err = errors.Join(err, statErr)
				case !realDirectory(info):
					err = errors.Join(err, fmt.Errorf("%w: directory entry %s", ErrInvalid, path))
				default:
					err = errors.Join(err, durable.SyncDir(path))
				}
			}
		}
	}()
	for i := range parts {
		path := filepath.Join(parts[:i+1]...)
		info, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) && create && i > 0 {
			if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			} else if err == nil {
				if firstCreated < 0 {
					firstCreated = i
				}
				lastCreated = i
			}
			info, statErr = os.Lstat(path)
		}
		if statErr != nil {
			return statErr
		}
		if !realDirectory(info) {
			return fmt.Errorf("%w: directory entry %s", ErrInvalid, path)
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
