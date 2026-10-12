package law

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// The ordered tree preserves unknown-member order and number spellings. The
// token budget bounds it, and the depth budget bounds recursive descent.
type node struct {
	kind     byte // '{', '[', 's', 'n', 'b', or '0' (null)
	text     string
	members  []member
	elements []node
}
type member struct {
	name  string
	value node
}

func (n node) get(name string) (node, bool) {
	for _, m := range n.members {
		if m.name == name {
			return m.value, true
		}
	}
	return node{}, false
}

type walker struct {
	dec    *json.Decoder
	limits LoadLimits
	tokens int
}

func readArtifact(data []byte, limits LoadLimits) (node, error) {
	if err := limits.validate(); err != nil {
		return node{}, err
	}
	if len(data) > limits.Bytes {
		return node{}, &LimitError{"bytes", limits.Bytes, int64(limits.Bytes)}
	}
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		return node{}, &MalformedError{"bom", 0}
	}
	if !utf8.Valid(data) {
		for i := 0; i < len(data); {
			r, size := utf8.DecodeRune(data[i:])
			if r == utf8.RuneError && size == 1 {
				return node{}, &MalformedError{"invalid_utf8", int64(i)}
			}
			i += size
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	w := walker{dec: dec, limits: limits}
	tok, err := w.token()
	if err != nil {
		return node{}, err
	}
	root, err := w.value(tok, 1, 0)
	if err != nil {
		return node{}, err
	}
	// Anything after the root except JSON whitespace is trailing data, including
	// a decoder syntax error on a non-JSON byte. Do not leak that raw error.
	if _, err := dec.Token(); err != io.EOF {
		return node{}, &MalformedError{"trailing_data", dec.InputOffset()}
	}
	return root, nil
}

func (w *walker) limit(bound Bound, max int) error {
	return &LimitError{bound, max, w.dec.InputOffset()}
}
func (w *walker) malformed(problem Problem) error {
	return &MalformedError{problem, w.dec.InputOffset()}
}

func (w *walker) token() (json.Token, error) {
	tok, err := w.dec.Token()
	if err != nil {
		offset := w.dec.InputOffset()
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			offset = syntax.Offset
		}
		return nil, &MalformedError{"syntax", offset}
	}
	w.tokens++
	if w.tokens > w.limits.Tokens {
		return nil, w.limit("tokens", w.limits.Tokens)
	}
	return tok, nil
}

// arrayIndex is one-based for an array element, otherwise zero. The opening
// token's depth check precedes its parent's element check, per phase 1.
func (w *walker) value(tok json.Token, depth, arrayIndex int) (node, error) {
	delim, container := tok.(json.Delim)
	if container && depth > w.limits.Depth {
		return node{}, w.limit("depth", w.limits.Depth)
	}
	if arrayIndex > w.limits.Elements {
		return node{}, w.limit("elements", w.limits.Elements)
	}
	switch v := tok.(type) {
	case string:
		if len(v) > w.limits.StringBytes {
			return node{}, w.limit("string_bytes", w.limits.StringBytes)
		}
		return node{kind: 's', text: v}, nil
	case json.Number:
		text := string(v)
		for i := range text {
			if text[i] < '0' || text[i] > '9' {
				return node{}, w.malformed("not_unsigned_integer")
			}
		}
		return node{kind: 'n', text: text}, nil
	case bool:
		return node{kind: 'b'}, nil
	case nil:
		return node{kind: '0'}, nil
	}
	if delim == '{' {
		return w.object(depth)
	}
	return w.array(depth)
}

func (w *walker) object(depth int) (node, error) {
	n := node{kind: '{'}
	keys := make(map[string]bool)
	for {
		tok, err := w.token()
		if err != nil {
			return node{}, err
		}
		if tok == json.Delim('}') {
			return n, nil
		}
		// Decoder.Token guarantees that this token is a key. Counting members
		// before duplicate detection means a repeated key counts toward the cap.
		key := tok.(string)
		if len(n.members)+1 > w.limits.Members {
			return node{}, w.limit("members", w.limits.Members)
		}
		if len(key) > w.limits.StringBytes {
			return node{}, w.limit("string_bytes", w.limits.StringBytes)
		}
		if keys[key] {
			return node{}, w.malformed("duplicate_key")
		}
		keys[key] = true
		tok, err = w.token()
		if err != nil {
			return node{}, err
		}
		value, err := w.value(tok, depth+1, 0)
		if err != nil {
			return node{}, err
		}
		n.members = append(n.members, member{key, value})
	}
}

func (w *walker) array(depth int) (node, error) {
	n := node{kind: '['}
	for {
		tok, err := w.token()
		if err != nil {
			return node{}, err
		}
		if tok == json.Delim(']') {
			return n, nil
		}
		value, err := w.value(tok, depth+1, len(n.elements)+1)
		if err != nil {
			return node{}, err
		}
		n.elements = append(n.elements, value)
	}
}
