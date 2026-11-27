package launcher

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
	"uuid"
)

var ErrProtocol = errors.New("launcher: protocol error")
var ErrIncompatible = errors.New("launcher: incompatible draft")
var ErrTooLarge = errors.New("launcher: frame too large")

// node keeps numbers as source text until a typed integer check.
type node struct {
	kind    byte
	text    string
	members map[string]*node
	items   []*node
}

func parseStrict(raw []byte) (*node, error) {
	if !utf8.Valid(raw) || !pairedSurrogates(raw) {
		return nil, ErrProtocol
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	n, err := parseValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrProtocol
	}
	return n, nil
}

func parseValue(d *json.Decoder, depth int) (*node, error) {
	token, err := d.Token()
	if err != nil {
		return nil, ErrProtocol
	}
	switch v := token.(type) {
	case json.Delim:
		if depth >= MaxJSONDepth {
			return nil, ErrProtocol
		}
		n := &node{}
		switch v {
		case '{':
			n.kind, n.members = 'o', map[string]*node{}
			for d.More() {
				token, err := d.Token()
				key, ok := token.(string)
				if err != nil || !ok || n.members[key] != nil {
					return nil, ErrProtocol
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
			return nil, ErrProtocol
		}
		if _, err := d.Token(); err != nil {
			return nil, ErrProtocol
		}
		return n, nil
	case string:
		return &node{kind: 's', text: v}, nil
	case json.Number:
		// All draft numbers are nonnegative exact signed-64-bit integers.
		// Int64 rejects decimal points and exponents without float coercion.
		x, err := v.Int64()
		if err != nil || x < 0 || strings.HasPrefix(string(v), "-") {
			return nil, ErrProtocol
		}
		return &node{kind: 'n', text: string(v)}, nil
	case bool:
		return &node{kind: 'b'}, nil
	default:
		return nil, ErrProtocol // null is never an omission
	}
}

// The JSON tokenizer handles syntax; this pass only rejects surrogate escapes
// encoding/json would otherwise silently replace with U+FFFD.
func pairedSurrogates(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

var uuidType = reflect.TypeFor[uuid.UUID]()
var digestType = reflect.TypeFor[Digest]()
var rawType = reflect.TypeFor[json.RawMessage]()

// checkShape uses only explicit JSON field tags, never Go type/field names.
// Required presence is independent of the Go zero value, recursively. The
// operation/event discriminants select concrete union members in codec.go.
func checkShape(n *node, typ reflect.Type) error {
	if n == nil {
		return ErrProtocol
	}
	if typ.Kind() == reflect.Pointer {
		return checkShape(n, typ.Elem())
	}
	if typ == rawType {
		if n.kind != 'o' {
			return ErrProtocol
		}
		return nil
	}
	if typ == uuidType {
		if n.kind != 's' {
			return ErrProtocol
		}
		id, err := uuid.Parse(n.text)
		if err != nil || id[6]>>4 != 7 || id[8]&0xc0 != 0x80 || id.String() != n.text {
			return ErrProtocol
		}
		return nil
	}
	if typ == digestType {
		if n.kind != 's' || !lowerHex(n.text, 64) {
			return ErrProtocol
		}
		return nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		if n.kind != 'o' {
			return ErrProtocol
		}
		if typ == reflect.TypeFor[Principal]() {
			kind := n.members["kind"]
			if kind == nil || kind.kind != 's' {
				return ErrProtocol
			}
			if kind.text == "unix_uid" && (n.members["user_sid"] != nil || n.members["authentication_id"] != nil) || kind.text == "windows_logon" && n.members["uid"] != nil {
				return ErrProtocol
			}
		}
		if typ == reflect.TypeFor[MutationResult]() {
			state := n.members["state"]
			if state == nil || state.kind != 's' || (state.text == "resolved") != (n.members["outcome"] != nil) {
				return ErrProtocol
			}
		}
		fields := map[string]reflect.StructField{}
		var collect func(reflect.Type)
		collect = func(t reflect.Type) {
			for i := range t.NumField() {
				f := t.Field(i)
				if f.Anonymous {
					collect(f.Type)
					continue
				}
				name := strings.Split(f.Tag.Get("json"), ",")[0]
				if name != "" && name != "-" {
					fields[name] = f
				}
			}
		}
		collect(typ)
		for key := range n.members {
			if _, ok := fields[key]; !ok {
				return ErrProtocol
			}
		}
		for key, field := range fields {
			child := n.members[key]
			optional := strings.Contains(field.Tag.Get("json"), ",omit")
			if child == nil && optional {
				continue
			}
			if err := checkShape(child, field.Type); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if n.kind != 'a' {
			return ErrProtocol
		}
		for _, child := range n.items {
			if err := checkShape(child, typ.Elem()); err != nil {
				return err
			}
		}
	case reflect.String:
		if n.kind != 's' {
			return ErrProtocol
		}
	case reflect.Bool:
		if n.kind != 'b' {
			return ErrProtocol
		}
	case reflect.Int, reflect.Int64, reflect.Uint32:
		if n.kind != 'n' {
			return ErrProtocol
		}
	case reflect.Interface:
		if n.kind != 'o' {
			return ErrProtocol
		}
	default:
		return ErrProtocol
	}
	return nil
}

func decodeTyped(raw []byte, out any) error {
	n, err := parseStrict(raw)
	if err != nil {
		return err
	}
	if err := checkShape(n, reflect.TypeOf(out).Elem()); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		if errors.Is(err, ErrIncompatible) {
			return ErrIncompatible
		}
		return ErrProtocol
	}
	return nil
}
func readAs[T any](raw []byte) (any, error) {
	var v T
	if err := decodeTyped(raw, &v); err != nil {
		return nil, err
	}
	return v, validateValue(v)
}
func lowerHex(s string, length int) bool {
	if len(s) != length {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
