package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// decodeF2_05 uses the decoder selected by a record's type.
func decodeF2_05(typ string, js json.RawMessage) (payload, error) {
	switch typ {
	case TypeWorkspaceRegistered:
		return DecodeWorkspaceRegistered(js)
	case TypeCaptureInputsPinned:
		return DecodeCaptureInputsPinned(js)
	case TypeScratchCreated:
		return DecodeScratchCreated(js)
	case TypeScratchClosed:
		return DecodeScratchClosed(js)
	}
	panic("not an F2-05 type: " + typ)
}

func f2Payloads() []struct {
	typ string
	p   payload
} {
	return []struct {
		typ string
		p   payload
	}{
		{TypeWorkspaceRegistered, WorkspaceRegistered{WorkspaceID: fid("41"), CommonDir: "C:/fixture/repo-a/.git", ObjectFormat: "sha1"}},
		{TypeCaptureInputsPinned, CaptureInputsPinned{WorkspaceID: fid("41"), Revision: 1, CaptureInputsSHA256: goldenB1, PinID: fid("21")}},
		{TypeScratchCreated, ScratchCreated{ScratchID: fid("51"), WorkspaceID: fid("41"), ContextID: fid("d1"), Dir: "0a1b2c3d", Branch: "refs/heads/dwarf-harness/0a1b2c3d", BaseCommit: strings.Repeat("a1", 20), AnchorTree: strings.Repeat("b2", 20)}},
		{TypeScratchClosed, ScratchClosed{ScratchID: fid("51"), ContextID: fid("d1"), Reason: "abandoned"}},
	}
}

func TestF2_05PayloadsRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name string
		want WorkspaceRegistered
		json string
	}{
		{"sha1", WorkspaceRegistered{WorkspaceID: fid("41"), CommonDir: "C:/fixture/repo-a/.git", ObjectFormat: "sha1"}, `{"workspace_id":"0199c000-0000-7000-8000-000000000041","common_dir":"C:/fixture/repo-a/.git","object_format":"sha1"}`},
		{"sha256", WorkspaceRegistered{WorkspaceID: fid("42"), CommonDir: "/fixture/repo-b/.git", ObjectFormat: "sha256"}, `{"workspace_id":"0199c000-0000-7000-8000-000000000042","common_dir":"/fixture/repo-b/.git","object_format":"sha256"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			js, err := tc.want.Encode()
			if err != nil || !bytes.Equal(js, []byte(tc.json)) {
				t.Fatalf("Encode = %s, %v; want %s", js, err, tc.json)
			}
			got, err := DecodeWorkspaceRegistered(js)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Decode = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
	for _, tc := range f2Payloads() {
		t.Run(tc.typ, func(t *testing.T) {
			values := []payload{tc.p}
			switch p := tc.p.(type) {
			case CaptureInputsPinned:
				p.Revision = 2
				values = append(values, p)
				p.Revision = 1<<63 - 1
				values = append(values, p)
			case ScratchCreated:
				p.BaseCommit = strings.Repeat("c3", 32)
				p.AnchorTree = strings.Repeat("d4", 32)
				values = append(values, p)
			case ScratchClosed:
				p.Reason = "merged" // Future reasons remain readable and re-encodable.
				values = append(values, p)
				p.Reason = strings.Repeat("a", 64)
				values = append(values, p)
			}
			for _, want := range values {
				js := mustEncode(want)
				got, err := decodeF2_05(tc.typ, js)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("Decode(%s) = %+v, %v; want %+v", js, got, err, want)
				}
				if again, err := got.Encode(); err != nil || !bytes.Equal(again, js) {
					t.Fatalf("re-encoding = %s, %v; want %s", again, err, js)
				}
			}
			js := mustEncode(tc.p)
			extended := append([]byte(`{"later":{"a":[1,null],"b":"x"},`), js[1:]...)
			if got, err := decodeF2_05(tc.typ, extended); err != nil || !reflect.DeepEqual(got, tc.p) {
				t.Errorf("unknown member changed decoding: %+v, %v; want %+v", got, err, tc.p)
			}
		})
	}
}

func TestF2_05PayloadsRefuse(t *testing.T) {
	for _, tc := range f2Payloads() {
		t.Run(tc.typ, func(t *testing.T) {
			js := mustEncode(tc.p)
			var members map[string]json.RawMessage
			if err := json.Unmarshal(js, &members); err != nil {
				t.Fatal(err)
			}
			refuse := func(name string, bad []byte) {
				t.Helper()
				t.Run(name, func(t *testing.T) {
					if _, err := decodeF2_05(tc.typ, bad); !errors.Is(err, ErrInvalid) {
						t.Errorf("Decode(%s) = %v; want ErrInvalid", bad, err)
					}
				})
			}
			for _, key := range slices.Sorted(maps.Keys(members)) {
				value := members[key]
				delete(members, key)
				bad, _ := json.Marshal(members)
				refuse("missing "+key, bad)
				members[key] = json.RawMessage("null")
				bad, _ = json.Marshal(members)
				refuse("null "+key, bad)
				members[key] = value
				refuse("repeated "+key, append([]byte(`{"`+key+`":`+string(value)+`,`), js[1:]...))
			}
			refuse("not object", []byte(`[]`))
			refuse("malformed", []byte(`{"`))
			refuse("nested duplicate", append([]byte(`{"later":{"x":1,"x":2},`), js[1:]...))

			badFields := map[string][]string{}
			for key := range members {
				if strings.HasSuffix(key, "_id") {
					badFields[key] = []string{`"6f1c2b6e-4b1d-4c3a-9e2f-0a1b2c3d4e5f"`, `"0199C000-0000-7000-8000-000000000041"`, `1`}
				}
			}
			switch tc.typ {
			case TypeWorkspaceRegistered:
				badFields["common_dir"] = []string{`""`, `false`}
				badFields["object_format"] = []string{`"sha512"`, `""`, `1`}
			case TypeCaptureInputsPinned:
				badFields["revision"] = []string{`0`, `-1`, `1.5`, `1e1`, `9223372036854775808`, `"1"`}
				badFields["capture_inputs_sha256"] = []string{`"` + ZeroHash + `"`, `"` + strings.ToUpper(goldenB1.String()) + `"`, `"xyz"`, `1`}
			case TypeScratchCreated:
				badFields["dir"] = []string{`""`, `"0A1B2C3D"`, `"0a1b2c3"`, `"0a1b2c3d4"`, `"0g1b2c3d"`, `1`}
				badFields["branch"] = []string{`"main"`, `"refs/tags/main"`, `""`, `1`}
				for _, key := range []string{"base_commit", "anchor_tree"} {
					badFields[key] = []string{`"` + strings.Repeat("A", 40) + `"`, `"` + strings.Repeat("a", 39) + `"`, `"` + strings.Repeat("a", 41) + `"`, `"` + strings.Repeat("a", 63) + `"`, `"g` + strings.Repeat("a", 39) + `"`, `1`}
				}
			case TypeScratchClosed:
				badFields["reason"] = []string{`""`, `"Abandoned"`, `"merged-back"`, `"1st"`, `"has.dot"`, `"` + strings.Repeat("a", 65) + `"`, `1`}
			}
			for key, values := range badFields {
				original := members[key]
				for _, value := range values {
					members[key] = json.RawMessage(value)
					bad, _ := json.Marshal(members)
					refuse(key+"="+value, bad)
					// Populate the Go value without the contract decoder to prove
					// Encode also rejects invalid values representable by the type.
					// UUID casing is not representable in the Go UUID value.
					if strings.HasSuffix(key, "_id") && strings.Contains(value, "0199C") {
						continue
					}
					p := reflect.New(reflect.TypeOf(tc.p))
					if json.Unmarshal(bad, p.Interface()) == nil {
						if out, err := p.Elem().Interface().(payload).Encode(); !errors.Is(err, ErrInvalid) || out != nil {
							t.Errorf("Encode %s=%s = %s, %v; want nil, ErrInvalid", key, value, out, err)
						}
					}
				}
				members[key] = original
			}
		})
	}
}

func TestF2_05PathGrammars(t *testing.T) {
	for _, tc := range []struct {
		field string
		good  []string
		bad   []string
	}{
		{
			"common_dir",
			[]string{"/fixture/repo/.git", "C:/fixture/repo/.git", "c:/fixture/repo/.git", "//host/share/repo/.git", "//host/share", "/with space/日本語/.git", "/" + strings.Repeat("é", 2047) + "a"},
			[]string{"", "/", "C:/", "//host", "///share/repo", "//host//repo", `C:\repo\.git`, `\\host\share\repo`, "relative/.git", "C:repo/.git", "/repo/../.git", "/repo/./.git", "/repo//.git", "/repo/.git/", "/repo/\x00.git", "/repo/\x1f.git", "/repo/\x7f.git", "/repo/\xff", "/" + strings.Repeat("é", 2048)},
		},
		{
			"branch",
			[]string{"refs/heads/main", "refs/heads/日本語", "refs/heads/" + strings.Repeat("a", 4085), "refs/heads/../@{/.lock/.hidden/trailing./~^:?*["},
			[]string{"", "main", "refs/tags/main", "refs/heads/", "refs/heads//x", "refs/heads/x/", "refs/heads/with space", `refs/heads/x\y`, "refs/heads/\x00x", "refs/heads/\x1fx", "refs/heads/\x7fx", "refs/heads/\xff", "refs/heads/" + strings.Repeat("a", 4086)},
		},
	} {
		t.Run(tc.field, func(t *testing.T) {
			makePayload := func(s string) (string, payload) {
				if tc.field == "common_dir" {
					return TypeWorkspaceRegistered, WorkspaceRegistered{WorkspaceID: fid("41"), CommonDir: s, ObjectFormat: "sha1"}
				}
				p := f2Payloads()[2].p.(ScratchCreated)
				p.Branch = s
				return TypeScratchCreated, p
			}
			for _, s := range tc.good {
				typ, want := makePayload(s)
				js := mustEncode(want)
				if got, err := decodeF2_05(typ, js); err != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("Decode %q = %+v, %v; want %+v", s, got, err, want)
				}
			}
			for _, s := range tc.bad {
				typ, p := makePayload(s)
				if js, err := p.Encode(); !errors.Is(err, ErrInvalid) || js != nil {
					t.Errorf("Encode %q = %s, %v; want nil, ErrInvalid", s, js, err)
				}
				js, _ := json.Marshal(p)
				// Marshal replaces invalid UTF-8; inject those original bytes
				// to exercise the decoder's raw UTF-8 refusal as well.
				js = bytes.ReplaceAll(js, []byte(`\ufffd`), []byte{0xff})
				js = bytes.ReplaceAll(js, []byte("\ufffd"), []byte{0xff})
				if _, err := decodeF2_05(typ, js); !errors.Is(err, ErrInvalid) {
					t.Errorf("Decode %q = %v; want ErrInvalid", s, err)
				}
			}
		})
	}
}
