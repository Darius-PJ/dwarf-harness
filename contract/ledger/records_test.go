package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"uuid"
)

// payload is an F0 payload value.
type payload interface {
	Encode() (json.RawMessage, error)
}

// decodeF0 decodes p with the decoder of type typ.
func decodeF0(typ string, p json.RawMessage) (payload, error) {
	switch typ {
	case TypeCommand:
		return DecodeCommand(p)
	case TypeBodyPin:
		return DecodeBodyPin(p)
	case TypeBodyUnpin:
		return DecodeBodyUnpin(p)
	case TypeBodiesCollected:
		return DecodeBodiesCollected(p)
	}
	panic("not an F0 type: " + typ)
}

func TestF0PayloadsRoundTrip(t *testing.T) {
	want := map[uint64]payload{
		2: BodyPin{PinID: fid("21")},
		3: Command{CommandID: fid("31"), SubmissionSHA256: goldenSubmission},
		5: BodiesCollected{BeforeNS: 1791000011500000000, Bodies: []CollectedBody{{SHA256: goldenB1, Bytes: 16}}},
		6: BodyUnpin{PinID: fid("21")},
		7: BodiesCollected{BeforeNS: 1791000014500000000, KeepBytes: 20, Bodies: []CollectedBody{{SHA256: goldenB2, Bytes: 16}}},
	}
	f0 := Consumption{TypeCommand: {1}, TypeBodyPin: {1}, TypeBodyUnpin: {1}, TypeBodiesCollected: {1}}
	commits, _, err := readAll(catalogueDir, ReadOptions{Colony: catalogueColony, Through: ToEnd, Consume: f0})
	if err != nil {
		t.Fatal(err)
	}
	got := map[uint64]payload{}
	for _, c := range commits {
		for _, r := range c.Records {
			p, err := decodeF0(r.Type, r.Payload)
			if err != nil {
				t.Errorf("seq %d: decoding %s: %v", r.Seq, r.Payload, err)
				continue
			}
			got[r.Seq] = p
			if js, err := p.Encode(); err != nil || !bytes.Equal(js, r.Payload) {
				t.Errorf("seq %d: Encode(%+v) = %s, %v; want the stored %s", r.Seq, p, js, err, r.Payload)
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decoded payloads by seq:\n got %+v\nwant %+v", got, want)
	}
}

func TestF0PayloadsRefuse(t *testing.T) {
	id := fid("21").String()
	ds := sortedDigests(goldenB1, goldenB2)
	lo, hi := ds[0].String(), ds[1].String()
	q := func(s string) string { return `"` + s + `"` }
	v4 := "6f1c2b6e-4b1d-4c3a-9e2f-0a1b2c3d4e5f"
	body := func(d string, n string) string { return `{"sha256":` + q(d) + `,"bytes":` + n + `}` }
	collected := func(members string) string { return `{` + members + `}` }
	okBodies := `"bodies":[` + body(lo, "1") + `,` + body(hi, "2") + `]`

	refused := []struct {
		typ, name, payload string
	}{
		{TypeCommand, "not an object", `[]`},
		{TypeCommand, "invalid JSON", `{"command_id":`},
		{TypeCommand, "missing command_id", `{"submission_sha256":` + q(lo) + `}`},
		{TypeCommand, "missing submission_sha256", `{"command_id":` + q(id) + `}`},
		{TypeCommand, "repeated key", `{"command_id":` + q(id) + `,"command_id":` + q(id) + `,"submission_sha256":` + q(lo) + `}`},
		{TypeCommand, "repeated key inside an unknown key", `{"x":{"a":1,"a":1},"command_id":` + q(id) + `,"submission_sha256":` + q(lo) + `}`},
		{TypeCommand, "null command_id", `{"command_id":null,"submission_sha256":` + q(lo) + `}`},
		{TypeCommand, "uppercase command_id", `{"command_id":` + q(strings.ToUpper(id)) + `,"submission_sha256":` + q(lo) + `}`},
		{TypeCommand, "non-v7 command_id", `{"command_id":` + q(v4) + `,"submission_sha256":` + q(lo) + `}`},
		{TypeCommand, "command_id as a number", `{"command_id":1,"submission_sha256":` + q(lo) + `}`},
		{TypeCommand, "uppercase digest", `{"command_id":` + q(id) + `,"submission_sha256":` + q(strings.ToUpper(lo)) + `}`},
		{TypeCommand, "short digest", `{"command_id":` + q(id) + `,"submission_sha256":` + q(lo[:62]) + `}`},
		{TypeCommand, "zero digest", `{"command_id":` + q(id) + `,"submission_sha256":` + q(ZeroHash) + `}`},
		{TypeCommand, "null digest", `{"command_id":` + q(id) + `,"submission_sha256":null}`},

		{TypeBodyPin, "missing pin_id", `{}`},
		{TypeBodyPin, "null pin_id", `{"pin_id":null}`},
		{TypeBodyPin, "uppercase pin_id", `{"pin_id":` + q(strings.ToUpper(id)) + `}`},
		{TypeBodyPin, "non-v7 pin_id", `{"pin_id":` + q(v4) + `}`},
		{TypeBodyPin, "repeated pin_id", `{"pin_id":` + q(id) + `,"pin_id":` + q(id) + `}`},
		{TypeBodyUnpin, "missing pin_id", `{"note":"x"}`},
		{TypeBodyUnpin, "null pin_id", `{"pin_id":null}`},
		{TypeBodyUnpin, "uppercase pin_id", `{"pin_id":` + q(strings.ToUpper(id)) + `}`},
		{TypeBodyUnpin, "non-v7 pin_id", `{"pin_id":` + q(v4) + `}`},
		{TypeBodyUnpin, "pin_id as an array", `{"pin_id":[` + q(id) + `]}`},

		{TypeBodiesCollected, "missing before_ns", collected(okBodies)},
		{TypeBodiesCollected, "missing bodies", collected(`"before_ns":5`)},
		{TypeBodiesCollected, "null bodies", collected(`"before_ns":5,"bodies":null`)},
		{TypeBodiesCollected, "null keep_bytes", collected(`"before_ns":5,"keep_bytes":null,` + okBodies)},
		{TypeBodiesCollected, "before_ns 0", collected(`"before_ns":0,` + okBodies)},
		{TypeBodiesCollected, "negative before_ns", collected(`"before_ns":-5,` + okBodies)},
		{TypeBodiesCollected, "before_ns out of range", collected(`"before_ns":9223372036854775808,` + okBodies)},
		{TypeBodiesCollected, "fractional before_ns", collected(`"before_ns":5.0,` + okBodies)},
		{TypeBodiesCollected, "before_ns with an exponent", collected(`"before_ns":5e3,` + okBodies)},
		{TypeBodiesCollected, "keep_bytes 0", collected(`"before_ns":5,"keep_bytes":0,` + okBodies)},
		{TypeBodiesCollected, "bodies not an array", collected(`"before_ns":5,"bodies":{}`)},
		{TypeBodiesCollected, "empty bodies", collected(`"before_ns":5,"bodies":[]`)},
		{TypeBodiesCollected, "unsorted bodies", collected(`"before_ns":5,"bodies":[` + body(hi, "1") + `,` + body(lo, "1") + `]`)},
		{TypeBodiesCollected, "repeated bodies", collected(`"before_ns":5,"bodies":[` + body(lo, "1") + `,` + body(lo, "1") + `]`)},
		{TypeBodiesCollected, "a null body", collected(`"before_ns":5,"bodies":[null]`)},
		{TypeBodiesCollected, "a string body", collected(`"before_ns":5,"bodies":[` + q(lo) + `]`)},
		{TypeBodiesCollected, "a body without sha256", collected(`"before_ns":5,"bodies":[{"bytes":1}]`)},
		{TypeBodiesCollected, "a body without bytes", collected(`"before_ns":5,"bodies":[{"sha256":` + q(lo) + `}]`)},
		{TypeBodiesCollected, "a body with a repeated key", collected(`"before_ns":5,"bodies":[{"sha256":` + q(lo) + `,"bytes":1,"bytes":1}]`)},
		{TypeBodiesCollected, "a zero body digest", collected(`"before_ns":5,"bodies":[` + body(ZeroHash, "1") + `]`)},
		{TypeBodiesCollected, "an uppercase body digest", collected(`"before_ns":5,"bodies":[` + body(strings.ToUpper(lo), "1") + `]`)},
		{TypeBodiesCollected, "negative bytes", collected(`"before_ns":5,"bodies":[` + body(lo, "-1") + `]`)},
		{TypeBodiesCollected, "fractional bytes", collected(`"before_ns":5,"bodies":[` + body(lo, "1.5") + `]`)},
	}
	for _, tc := range refused {
		if p, err := decodeF0(tc.typ, json.RawMessage(tc.payload)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s %s: decoding %s = %+v, %v; want an error matching ErrInvalid", tc.typ, tc.name, tc.payload, p, err)
		}
	}

	// Unknown keys are ignored, at the top level and inside bodies elements,
	// so that a type version can gain optional keys.
	extra := `"later":{"a":[1,null],"b":"x"},`
	accepted := []struct {
		typ, payload string
		want         payload
	}{
		{
			TypeCommand, `{` + extra + `"command_id":` + q(id) + `,"submission_sha256":` + q(lo) + `}`,
			Command{CommandID: fid("21"), SubmissionSHA256: ds[0]},
		},
		{TypeBodyPin, `{"pin_id":` + q(id) + `,` + strings.TrimSuffix(extra, ",") + `}`, BodyPin{PinID: fid("21")}},
		{TypeBodyUnpin, `{` + extra + `"pin_id":` + q(id) + `}`, BodyUnpin{PinID: fid("21")}},
		{
			TypeBodiesCollected, collected(extra + `"before_ns":5,"keep_bytes":3,"bodies":[{"media":"text/plain","sha256":` + q(lo) + `,"bytes":0},` + body(hi, "2") + `]`),
			BodiesCollected{BeforeNS: 5, KeepBytes: 3, Bodies: []CollectedBody{{SHA256: ds[0], Bytes: 0}, {SHA256: ds[1], Bytes: 2}}},
		},
	}
	for _, tc := range accepted {
		if p, err := decodeF0(tc.typ, json.RawMessage(tc.payload)); err != nil || !reflect.DeepEqual(p, tc.want) {
			t.Errorf("%s: decoding %s = %+v, %v; want %+v", tc.typ, tc.payload, p, err, tc.want)
		}
	}

	// Encode refuses every value a decoder would refuse.
	zero, noV7 := Digest{}, uuid.MustParse(v4)
	for _, p := range []payload{
		Command{SubmissionSHA256: ds[0]},
		Command{CommandID: noV7, SubmissionSHA256: ds[0]},
		Command{CommandID: fid("31"), SubmissionSHA256: zero},
		BodyPin{},
		BodyUnpin{PinID: noV7},
		BodiesCollected{Bodies: []CollectedBody{{SHA256: ds[0]}}},
		BodiesCollected{BeforeNS: 5, KeepBytes: -1, Bodies: []CollectedBody{{SHA256: ds[0]}}},
		BodiesCollected{BeforeNS: 5},
		BodiesCollected{BeforeNS: 5, Bodies: []CollectedBody{}},
		BodiesCollected{BeforeNS: 5, Bodies: []CollectedBody{{SHA256: zero}}},
		BodiesCollected{BeforeNS: 5, Bodies: []CollectedBody{{SHA256: ds[0], Bytes: -1}}},
		BodiesCollected{BeforeNS: 5, Bodies: []CollectedBody{{SHA256: ds[1]}, {SHA256: ds[0]}}},
		BodiesCollected{BeforeNS: 5, Bodies: []CollectedBody{{SHA256: ds[0]}, {SHA256: ds[0]}}},
	} {
		if js, err := p.Encode(); !errors.Is(err, ErrInvalid) || js != nil {
			t.Errorf("Encode(%+v) = %s, %v; want nothing and ErrInvalid", p, js, err)
		}
	}

	// ParseDigest accepts exactly the text form a digest is written in.
	if d, err := ParseDigest(lo); err != nil || d != ds[0] {
		t.Errorf("ParseDigest(%s) = %v, %v; want %v", lo, d, err, ds[0])
	}
	for _, s := range []string{strings.ToUpper(lo), lo[:63], lo + "0", ZeroHash, "g" + lo[1:], ""} {
		if _, err := ParseDigest(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseDigest(%q) = %v, want an error matching ErrInvalid", s, err)
		}
	}
}
