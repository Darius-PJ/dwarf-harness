package workspace

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

func parseNULList(b []byte) ([]string, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if b[len(b)-1] != 0 {
		return nil, fmt.Errorf("unterminated NUL list")
	}
	fields := strings.Split(string(b[:len(b)-1]), "\x00")
	for _, s := range fields {
		if s == "" {
			return nil, fmt.Errorf("empty NUL field")
		}
	}
	return fields, nil
}

func validOID(s string, n int) bool {
	if (n != 40 && n != 64) || len(s) != n {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

type stageEntry struct {
	Mode, OID string
	Stage     int
	Path      string
}

func parseStageEntries(b []byte, n int) ([]stageEntry, error) {
	rows, err := parseNULList(b)
	if err != nil {
		return nil, err
	}
	var out []stageEntry
	for _, row := range rows {
		head, path, ok := strings.Cut(row, "\t")
		parts := strings.Split(head, " ")
		if !ok || path == "" || len(parts) != 3 {
			return nil, fmt.Errorf("invalid stage entry")
		}
		switch parts[0] {
		case "100644", "100755", "120000", "160000":
		default:
			return nil, fmt.Errorf("invalid index mode")
		}
		if !validOID(parts[1], n) || len(parts[2]) != 1 || parts[2][0] < '0' || parts[2][0] > '3' {
			return nil, fmt.Errorf("invalid index object or stage")
		}
		out = append(out, stageEntry{Mode: parts[0], OID: parts[1], Stage: int(parts[2][0] - '0'), Path: path})
	}
	return out, nil
}

type attribute struct{ Path, Name, Value string }

func parseCheckAttr(b []byte) ([]attribute, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if b[len(b)-1] != 0 {
		return nil, fmt.Errorf("unterminated attribute triple")
	}
	fields := strings.Split(string(b[:len(b)-1]), "\x00")
	if len(fields)%3 != 0 {
		return nil, fmt.Errorf("incomplete attribute triple")
	}
	var out []attribute
	for i := 0; i < len(fields); i += 3 {
		// Git distinguishes an empty value (attr=) from unspecified or unset.
		if fields[i] == "" || fields[i+1] == "" {
			return nil, fmt.Errorf("empty attribute path or name")
		}
		out = append(out, attribute{Path: fields[i], Name: fields[i+1], Value: fields[i+2]})
	}
	return out, nil
}

type worktreeEntry struct {
	Path, HEAD, Branch               string
	Bare, Detached, Locked, Prunable bool
}

func parseWorktreeList(b []byte, n int) ([]worktreeEntry, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if !bytes.HasSuffix(b, []byte{0, 0}) {
		return nil, fmt.Errorf("unterminated worktree record")
	}
	records := bytes.Split(b[:len(b)-2], []byte{0, 0})
	var out []worktreeEntry
	for _, record := range records {
		fields := strings.Split(string(record), "\x00")
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "worktree ") || len(fields[0]) == len("worktree ") {
			return nil, fmt.Errorf("invalid worktree path")
		}
		e := worktreeEntry{Path: strings.TrimPrefix(fields[0], "worktree ")}
		seen := map[string]bool{}
		for _, field := range fields[1:] {
			key, val, has := strings.Cut(field, " ")
			if seen[key] {
				return nil, fmt.Errorf("repeated worktree field")
			}
			seen[key] = true
			switch key {
			case "HEAD":
				if !has || !validOID(val, n) {
					return nil, fmt.Errorf("invalid worktree HEAD")
				}
				e.HEAD = val
			case "branch":
				if !has || !strings.HasPrefix(val, "refs/heads/") || len(val) == len("refs/heads/") {
					return nil, fmt.Errorf("invalid worktree branch")
				}
				e.Branch = val
			case "bare":
				if has {
					return nil, fmt.Errorf("invalid bare field")
				}
				e.Bare = true
			case "detached":
				if has {
					return nil, fmt.Errorf("invalid detached field")
				}
				e.Detached = true
			case "locked":
				e.Locked = true
			case "prunable":
				e.Prunable = true
			default:
				return nil, fmt.Errorf("unknown worktree field")
			}
		}
		if e.Bare {
			if e.HEAD != "" || e.Branch != "" || e.Detached {
				return nil, fmt.Errorf("contradictory bare worktree")
			}
		} else if e.HEAD == "" || (e.Branch != "") == e.Detached {
			return nil, fmt.Errorf("incomplete worktree record")
		}
		out = append(out, e)
	}
	return out, nil
}

type catFileHeader struct {
	OID, Kind string
	Size      int64
}

func parseCatFileHeader(b []byte, n int) (catFileHeader, error) {
	var zero catFileHeader
	if len(b) == 0 || b[len(b)-1] != '\n' {
		return zero, fmt.Errorf("unterminated cat-file header")
	}
	parts := strings.Split(string(b[:len(b)-1]), " ")
	if len(parts) != 3 || !validOID(parts[0], n) {
		return zero, fmt.Errorf("invalid cat-file object")
	}
	switch parts[1] {
	case "blob", "tree", "commit", "tag":
	default:
		return zero, fmt.Errorf("invalid cat-file kind")
	}
	if parts[2] == "" {
		return zero, fmt.Errorf("missing object size")
	}
	for _, c := range parts[2] {
		if c < '0' || c > '9' {
			return zero, fmt.Errorf("invalid object size")
		}
	}
	size, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return zero, err
	}
	return catFileHeader{OID: parts[0], Kind: parts[1], Size: size}, nil
}

func parseGitVersion(b []byte) (string, error) {
	return parseGitVersionAtLeast(b, 45)
}

// Persisted input formats keep their original floor independently of the
// executable's current requirements. Both use the same version grammar.
func parseGitVersionAtLeast(b []byte, minimumMinor int) (string, error) {
	s := strings.TrimSuffix(string(b), "\n")
	s = strings.TrimSuffix(s, "\r")
	if !strings.HasPrefix(s, "git version ") {
		return "", fmt.Errorf("missing git version prefix")
	}
	s = strings.TrimPrefix(s, "git version ")
	if strings.ContainsAny(s, "\r\n\x00") {
		return "", fmt.Errorf("invalid git version")
	}
	var nums [3]int
	end := 0
	for i := range nums {
		start := end
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			end++
		}
		n, err := strconv.Atoi(s[start:end])
		if err != nil {
			return "", fmt.Errorf("invalid version triple")
		}
		nums[i] = n
		if i < 2 {
			if end == len(s) || s[end] != '.' {
				return "", fmt.Errorf("missing major.minor.patch")
			}
			end++
		}
	}
	if end < len(s) && s[end] != '.' && s[end] != ' ' {
		return "", fmt.Errorf("invalid version suffix")
	}
	if nums[0] < 2 || nums[0] == 2 && nums[1] < minimumMinor {
		return "", fmt.Errorf("git 2.%d or later is required", minimumMinor)
	}
	return s, nil
}

func parseMergeTree(b []byte, exit, n int) (string, []string, error) {
	fields, err := parseNULList(b)
	if err != nil {
		return "", nil, err
	}
	if len(fields) == 0 || !validOID(fields[0], n) {
		return "", nil, fmt.Errorf("invalid merge tree")
	}
	if (exit != 0 && exit != 1) || (exit == 0 && len(fields) != 1) || (exit == 1 && len(fields) < 2) {
		return "", nil, fmt.Errorf("merge exit and output disagree")
	}
	seen := make(map[string]bool, len(fields)-1)
	for _, path := range fields[1:] {
		if seen[path] {
			return "", nil, fmt.Errorf("repeated merge path")
		}
		seen[path] = true
	}
	if exit == 0 {
		return fields[0], nil, nil
	}
	return fields[0], fields[1:], nil
}
