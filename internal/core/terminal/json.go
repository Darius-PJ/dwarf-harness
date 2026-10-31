package terminal

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

var (
	// ErrSettings classifies a refused settings file.
	ErrSettings = errors.New("terminal: settings refused")
	// ErrCommand classifies a refused /send command.
	ErrCommand = errors.New("terminal: command refused")
)

// RefusalError names the refused member's path and its allowed form. It never
// repeats a supplied value or an unknown member's name.
type RefusalError struct {
	Kind    error
	Path    string
	Allowed string
}

func (e *RefusalError) Error() string { return e.Kind.Error() + " at " + e.Path + ": " + e.Allowed }
func (e *RefusalError) Unwrap() error { return e.Kind }

// node is one strictly parsed JSON value. Numbers keep their source text, so
// no float conversion or coercion happens before a typed check.
type node struct {
	kind    byte // 'o' object, 'a' array, 's' string, 'n' number, 'b' boolean
	members map[string]*node
	items   []*node
	text    string
}

const maxJSONDepth = 8

var errJSON = errors.New("terminal: strict JSON refused")

// parseStrict accepts exactly one UTF-8 JSON value without duplicate members,
// null, nesting beyond maxJSONDepth or trailing data.
func parseStrict(raw []byte) (*node, error) {
	if !utf8.Valid(raw) {
		return nil, errJSON
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	n, err := parseValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errJSON
	}
	return n, nil
}

func parseValue(d *json.Decoder, depth int) (*node, error) {
	if depth > maxJSONDepth {
		return nil, errJSON
	}
	token, err := d.Token()
	if err != nil {
		return nil, errJSON
	}
	switch v := token.(type) {
	case json.Delim:
		n := &node{}
		switch v {
		case '{':
			n.kind, n.members = 'o', map[string]*node{}
			for d.More() {
				token, err := d.Token()
				key, ok := token.(string)
				if err != nil || !ok {
					return nil, errJSON
				}
				if _, duplicate := n.members[key]; duplicate {
					return nil, errJSON
				}
				if n.members[key], err = parseValue(d, depth+1); err != nil {
					return nil, err
				}
			}
		case '[':
			n.kind = 'a'
			for d.More() {
				item, err := parseValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				n.items = append(n.items, item)
			}
		default:
			return nil, errJSON
		}
		if _, err := d.Token(); err != nil {
			return nil, errJSON
		}
		return n, nil
	case string:
		return &node{kind: 's', text: v}, nil
	case json.Number:
		return &node{kind: 'n', text: string(v)}, nil
	case bool:
		return &node{kind: 'b'}, nil
	default:
		// null is refused for every member; absence is the only omission.
		return nil, errJSON
	}
}

// reader records the first refusal; later checks become no-ops.
type reader struct {
	kind error
	err  *RefusalError
}

func (r *reader) fail(path, allowed string) {
	if r.err == nil {
		r.err = &RefusalError{Kind: r.kind, Path: path, Allowed: allowed}
	}
}

// object requires an object with every required member and no member outside
// required and optional.
func (r *reader) object(n *node, path string, required, optional []string) bool {
	if r.err != nil {
		return false
	}
	allowed := "an object with members " + strings.Join(append(slices.Clone(required), optional...), ", ")
	if n == nil || n.kind != 'o' {
		r.fail(path, allowed)
		return false
	}
	for key := range n.members {
		if !slices.Contains(required, key) && !slices.Contains(optional, key) {
			r.fail(path, allowed)
			return false
		}
	}
	for _, key := range required {
		if n.members[key] == nil {
			r.fail(join(path, key), "a required member")
			return false
		}
	}
	return true
}

func (r *reader) array(n *node, path string, minimum int) []*node {
	if r.err != nil {
		return nil
	}
	if n == nil || n.kind != 'a' || len(n.items) < minimum {
		r.fail(path, "an array of at least "+strconv.Itoa(minimum)+" items")
		return nil
	}
	return n.items
}

// integer accepts only a JSON integer token: no string form, fraction or
// exponent, within [minimum, maximum].
func (r *reader) integer(n *node, path string, minimum, maximum int64) int64 {
	if r.err != nil {
		return 0
	}
	allowed := "a JSON integer from " + strconv.FormatInt(minimum, 10) + " to " + strconv.FormatInt(maximum, 10)
	if n == nil || n.kind != 'n' || strings.ContainsAny(n.text, ".eE") {
		r.fail(path, allowed)
		return 0
	}
	v, err := strconv.ParseInt(n.text, 10, 64)
	if err != nil || v < minimum || v > maximum {
		r.fail(path, allowed)
		return 0
	}
	return v
}

// text accepts a string of 1..maximum UTF-8 bytes without control characters.
func (r *reader) text(n *node, path string, maximum int) string {
	if r.err != nil {
		return ""
	}
	allowed := "a string of 1 to " + strconv.Itoa(maximum) + " UTF-8 bytes without control characters"
	if n == nil || n.kind != 's' || len(n.text) == 0 || len(n.text) > maximum || strings.ContainsFunc(n.text, unicode.IsControl) {
		r.fail(path, allowed)
		return ""
	}
	return n.text
}

func (r *reader) choice(n *node, path string, values ...string) string {
	if r.err != nil {
		return ""
	}
	if n == nil || n.kind != 's' || !slices.Contains(values, n.text) {
		r.fail(path, "one of "+strings.Join(values, ", "))
		return ""
	}
	return n.text
}

// id accepts only the canonical lowercase hyphenated form of a UUIDv7.
func (r *reader) id(n *node, path string) uuid.UUID {
	if r.err != nil {
		return uuid.UUID{}
	}
	if n == nil || n.kind != 's' {
		r.fail(path, "a canonical lowercase UUIDv7 string")
		return uuid.UUID{}
	}
	id, ok := parseID(n.text)
	if !ok {
		r.fail(path, "a canonical lowercase UUIDv7 string")
	}
	return id
}

func (r *reader) digest(n *node, path string) ledger.Digest {
	if r.err != nil {
		return ledger.Digest{}
	}
	if n == nil || n.kind != 's' {
		r.fail(path, "64 lowercase hexadecimal digits")
		return ledger.Digest{}
	}
	d, err := ledger.ParseDigest(n.text)
	if err != nil || d.String() != n.text {
		r.fail(path, "64 lowercase hexadecimal digits")
		return ledger.Digest{}
	}
	return d
}

func parseID(text string) (uuid.UUID, bool) {
	id, err := uuid.Parse(text)
	return id, err == nil && id.String() == text && id[6]>>4 == 7 && id[8]&0xc0 == 0x80
}

func join(path, member string) string {
	if path == "$" {
		return member
	}
	return path + "." + member
}

func index(path string, i int) string { return path + "[" + strconv.Itoa(i) + "]" }
