package terminal

import (
	"encoding/json"
	"reflect"
	"strings"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// DecodeObject decodes a bounded strict JSON object into a tagged struct.
// Required members lack omitempty/omitzero. RawMessage members are checked for
// JSON syntax here and must subsequently be decoded into their concrete type.
// Refusals contain only schema member paths, never supplied names or values.
func DecodeObject(raw []byte, maximum int, value any) error {
	r := &reader{kind: ErrSettings}
	t := reflect.TypeOf(value)
	if maximum <= 0 || len(raw) > maximum || t == nil || t.Kind() != reflect.Pointer || reflect.ValueOf(value).IsNil() || t.Elem().Kind() != reflect.Struct {
		r.fail("$", "a bounded JSON object and a struct destination")
		return r.err
	}
	n, err := parseStrict(raw)
	if err != nil {
		r.fail("$", "one UTF-8 JSON object without duplicate members, null, deep nesting or trailing data")
		return r.err
	}
	r.objectShape(n, t.Elem(), "$")
	if r.err != nil {
		return r.err
	}
	decoded := reflect.New(t.Elem()).Elem()
	if err := decodeObjectValue(raw, decoded); err != nil {
		r.fail("$", "values matching the declared member types")
		return r.err
	}
	reflect.ValueOf(value).Elem().Set(decoded)
	return nil
}

func (r *reader) objectShape(n *node, t reflect.Type, path string) {
	if n == nil || r.err != nil {
		return
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeFor[json.RawMessage]() {
		return
	}
	if t == reflect.TypeFor[uuid.UUID]() {
		r.id(n, path)
		return
	}
	switch t.Kind() {
	case reflect.Struct:
		var required, optional []string
		fields := make(map[string]reflect.Type)
		for i := range t.NumField() {
			f := t.Field(i)
			name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
			if !f.IsExported() || name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			fields[name] = f.Type
			if strings.Contains(opts, "omitempty") || strings.Contains(opts, "omitzero") {
				optional = append(optional, name)
			} else {
				required = append(required, name)
			}
		}
		if !r.object(n, path, required, optional) {
			return
		}
		// Visit schema order rather than map order for stable member paths.
		for _, name := range append(required, optional...) {
			r.objectShape(n.members[name], fields[name], join(path, name))
		}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return // byte strings and digest text have their own JSON codecs
		}
		for i, item := range r.array(n, path, 0) {
			r.objectShape(item, t.Elem(), index(path, i))
		}
	}
}

// Ledger's frozen Digest deliberately has no UnmarshalText method. Decode it
// through its canonical parser while retaining encoding/json's scalar rules.
func decodeObjectValue(raw []byte, dst reflect.Value) error {
	if dst.Kind() == reflect.Pointer {
		dst.Set(reflect.New(dst.Type().Elem()))
		return decodeObjectValue(raw, dst.Elem())
	}
	if dst.Type() == reflect.TypeFor[ledger.Digest]() {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return err
		}
		digest, err := ledger.ParseDigest(text)
		if err == nil {
			dst.Set(reflect.ValueOf(digest))
		}
		return err
	}
	if dst.Type() == reflect.TypeFor[json.RawMessage]() || dst.Type() == reflect.TypeFor[uuid.UUID]() {
		return json.Unmarshal(raw, dst.Addr().Interface())
	}
	switch dst.Kind() {
	case reflect.Struct:
		var members map[string]json.RawMessage
		if err := json.Unmarshal(raw, &members); err != nil {
			return err
		}
		for i := range dst.NumField() {
			field := dst.Type().Field(i)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if !field.IsExported() || name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			if value, present := members[name]; present {
				if err := decodeObjectValue(value, dst.Field(i)); err != nil {
					return err
				}
			}
		}
		return nil
	case reflect.Slice:
		if dst.Type().Elem().Kind() != reflect.Uint8 {
			var items []json.RawMessage
			if err := json.Unmarshal(raw, &items); err != nil {
				return err
			}
			dst.Set(reflect.MakeSlice(dst.Type(), len(items), len(items)))
			for i, raw := range items {
				if err := decodeObjectValue(raw, dst.Index(i)); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return json.Unmarshal(raw, dst.Addr().Interface())
}
