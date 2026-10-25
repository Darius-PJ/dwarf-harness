package providers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("providers: invalid input")
	ErrUnsupported = errors.New("providers: unsupported profile or feature")
	ErrExpired     = errors.New("providers: profile not current")
	ErrOverflow    = errors.New("providers: numeric overflow")
	ErrProtocol    = errors.New("providers: invalid or incomplete stream")
	ErrLimit       = errors.New("providers: accumulation limit exceeded")
	ErrUsage       = errors.New("providers: usage unavailable")
)

// checkJSON rejects duplicate keys before decoding into a typed value. The
// decoder's floating-point path is never used, including for opaque tool data.
func checkJSON(b []byte, bound int, forbidNull, integersOnly bool) error {
	if len(b) == 0 || len(b) > bound || !utf8.Valid(b) {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return ErrLimit
		}
		t, err := d.Token()
		if err != nil {
			return ErrInvalid
		}
		switch v := t.(type) {
		case json.Delim:
			switch v {
			case '{':
				keys := make(map[string]bool)
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return ErrInvalid
					}
					s, ok := k.(string)
					if !ok || keys[s] {
						return ErrInvalid
					}
					keys[s] = true
					if e = value(depth + 1); e != nil {
						return e
					}
				}
				end, e := d.Token()
				if e != nil || end != json.Delim('}') {
					return ErrInvalid
				}
			case '[':
				for d.More() {
					if e := value(depth + 1); e != nil {
						return e
					}
				}
				end, e := d.Token()
				if e != nil || end != json.Delim(']') {
					return ErrInvalid
				}
			default:
				return ErrInvalid
			}
		case nil:
			if forbidNull {
				return ErrInvalid
			}
		case json.Number:
			if integersOnly {
				if _, e := decimal(string(v)); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

func decodeStrict(b []byte, dst any) error {
	if err := exactFields(b, reflect.TypeOf(dst).Elem()); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	d.UseNumber()
	if err := d.Decode(dst); err != nil {
		return ErrInvalid
	}
	return nil
}

// encoding/json's field matching is otherwise case-insensitive, even with
// DisallowUnknownFields. Installation must not accept alternate spellings.
func exactFields(b []byte, typ reflect.Type) error {
	if typ == reflect.TypeFor[json.RawMessage]() || bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return nil
	}
	if typ.Kind() == reflect.Pointer {
		return exactFields(b, typ.Elem())
	}
	switch typ.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(b, &object) != nil || object == nil {
			return ErrInvalid
		}
		fields := make(map[string]reflect.Type, typ.NumField())
		for i := range typ.NumField() {
			field := typ.Field(i)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name != "" && name != "-" {
				fields[name] = field.Type
			}
		}
		for key, value := range object {
			field, ok := fields[key]
			if !ok {
				return ErrInvalid
			}
			if err := exactFields(value, field); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var items []json.RawMessage
		if json.Unmarshal(b, &items) != nil {
			return ErrInvalid
		}
		for _, item := range items {
			if err := exactFields(item, typ.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

func compact(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, ErrInvalid
	}
	return bytes.TrimSuffix(b.Bytes(), []byte{'\n'}), nil
}

func visible(s string, maximum int) bool {
	if len(s) == 0 || len(s) > maximum {
		return false
	}
	for i := range len(s) {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func token(s string) bool {
	if len(s) == 0 || len(s) > 64 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}
