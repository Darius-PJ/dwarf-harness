package launcher

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
	"uuid"
)

func testID(n byte) uuid.UUID           { return uuid.UUID{0: 1, 6: 0x70, 8: 0x80, 15: n} }
func testEnvelope(kind string) Envelope { return Envelope{DraftVersion, testID(1), 1, kind} }
func testHello() Hello                  { return Hello{"hello", []int64{DraftVersion}, "base", testID(2), testID(1)} }
func testWelcome() Welcome {
	uid := uint32(1000)
	return Welcome{"welcome", DraftVersion, "base", testID(1), testID(3), testID(2), Principal{Kind: "unix_uid", UID: &uid}, "ready", 3, MaxFrameBytes}
}
func mustEncode(t *testing.T, v any, op string) []byte {
	t.Helper()
	raw, err := Encode(v, op)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func readyServer(t *testing.T) *Decoder {
	t.Helper()
	d := NewServerDecoder()
	if _, err := d.Decode(mustEncode(t, testHello(), "")); err != nil {
		t.Fatal(err)
	}
	if err := d.AcceptWelcome(testWelcome()); err != nil {
		t.Fatal(err)
	}
	return d
}

type shortWriter struct{ bytes.Buffer }

func (w *shortWriter) Write(p []byte) (int, error) { return w.Buffer.Write(p[:min(3, len(p))]) }

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	frames := []any{
		testHello(), testWelcome(), Refused{"refused", Unauthorized},
		HostState{testEnvelope("host_state"), testID(3), "quiescing", 4, ""},
		TextDelta{testEnvelope("text_delta"), testID(7), testID(8), 1, "a\U0001f642"},
		TextEnd{testEnvelope("text_end"), testID(7), Closed},
	}
	windows := testWelcome()
	windows.Principal = Principal{Kind: "windows_logon", UserSID: "S-1-5-21-1000", AuthenticationID: "0000000000000001"}
	frames = append(frames, windows)
	for _, frame := range frames {
		raw := mustEncode(t, frame, "")
		var w shortWriter
		if err := WriteFrame(&w, raw); err != nil {
			t.Fatal(err)
		}
		body, err := ReadFrame(&w)
		if err != nil || !bytes.Equal(body, raw) {
			t.Fatalf("frame bytes: %v", err)
		}
		got, err := Decode(body, "")
		if err != nil || !reflect.DeepEqual(frame, got) {
			t.Fatalf("%T round-trip: got %#v, err %v", frame, got, err)
		}
		if _, err := ReadFrame(&w); err != io.EOF {
			t.Fatalf("clean detach: %v", err)
		}
	}
	status := HostStatusResult{HostID: testID(3), ColonyID: testID(2), State: "ready", LifecycleRevision: 3, Startup: &StartupSummary{Conditions: []string{"previous_host_unclean"}, Credentials: []CredentialVersion{{testID(4), "synthetic", testID(5), 0}}}}
	turn := TurnView{TurnID: testID(8), State: "running", Reason: "", BoundSequence: 10, OwnerVersion: 1, Generation: 1, Pending: []Pending{}, DeadlineUTCNS: 100}
	eligibility := EligibilityView{State: "eligible", Reason: "ready", Version: 1, ThroughSequence: 10}
	service := ServiceView{AgentID: testID(4), State: "available", Active: []ServiceCauseView{}, Pending: []ServiceCauseView{}, Unsupported: []ServiceCauseView{}, ThroughSequence: 10}
	digest := Digest(strings.Repeat("a", 64))
	object := ObjectRef{digest, 5}
	source := ServiceSource{Kind: "paid_refusal", ObservationID: testID(20), ObservedThrough: 10, ObservedUTCNS: 100, Reason: "rate_limited"}
	changed := ServiceChanged{AgentID: testID(4), CauseID: testID(21), Version: 1, Generation: 1, Phase: "lost", FirstSource: source, OutageID: testID(22), Source: source}
	notice := OutageNotice{testID(22), testID(21), "began", testID(23), 11}
	payload := RecordPayload{"runtime.turn_result", 1, []byte(`{"turn_id":"01000000-0000-7000-8000-000000000008","bound_sequence":10,"outcome":"finished","reason":"natural_stop","model_calls":1,"tool_dispatches":0,"ending_sequence":11}`)}
	events := []Event{
		{Sequence: 11, Type: "turn.finished", AgentID: testID(4), SessionID: testID(5), ContextID: testID(6), TurnID: testID(8), Data: payload},
		{Sequence: 12, Type: "tool.output", AgentID: testID(4), SessionID: testID(5), ContextID: testID(6), Data: object},
		{Sequence: 13, Type: "eligibility.changed", AgentID: testID(4), SessionID: testID(5), ContextID: testID(6), Data: eligibility},
		{Sequence: 14, Type: "service.changed", AgentID: testID(4), Data: changed},
		{Sequence: 14, Type: "outage.joined", AgentID: testID(4), Data: changed},
		{Sequence: 15, Type: "outage.began", Data: notice},
	}
	mutation := MutationResult{RequestID: testID(9), BindingSequence: 12, State: "resolved", Outcome: "applied", TurnID: testID(8)}
	rows := []struct {
		op                string
		arguments, result any
		expected          string
	}{
		{"host.status", HostStatus{}, status, ""},
		{"host.watch", HostWatch{2}, status, ""},
		{"host.shutdown", HostShutdown{}, HostStatusResult{HostID: testID(3), ColonyID: testID(2), State: "quiescing", LifecycleRevision: 4}, "host"},
		{"session.list", SessionList{Limit: 128}, SessionListResult{Rows: []SessionRow{{testID(4), testID(5), testID(6), "Synthetic", 1}}, ThroughSeq: 10}, ""},
		{"message.send", MessageSend{testID(8), "hello", []ArtifactRef{{10, digest, 5}}}, mutation, "context"},
		{"turn.cancel", TurnCancel{testID(8)}, mutation, "continuation"},
		{"promotion.request", PromotionRequest{WorkspaceID: testID(10), Head: testID(11)}, mutation, "context"},
		{"turn.status", TurnStatus{testID(8)}, turn, ""},
		{"context.status", ContextStatus{}, ContextView{ContextID: testID(6), Revision: 1, Turns: []TurnView{turn}, ThroughSeq: 10, Eligibility: &eligibility, Service: &service}, ""},
		{"session.events", SessionEvents{0, 128}, EventPage{events, 15, testID(12)}, ""},
		{"session.wait", SessionWait{testID(12)}, EmptyResult{}, ""},
		{"text.attach", TextAttach{testID(8)}, TextSnapshotPart{Text: "snapshot", DeltaHighWater: 2, State: "provisional", SubscriptionID: testID(7)}, ""},
		{"text.close", TextClose{testID(7)}, EmptyResult{}, ""},
		{"text.final", TextFinal{testID(8)}, TextPart{"final"}, ""},
		{"service.status", ServiceStatus{}, service, ""},
		{"service.outages", ServiceOutages{Limit: 128}, ServiceOutagePage{Rows: []ServiceOutageRow{{OutageID: testID(22), CauseID: testID(21), AgentID: testID(4), Cause: ServiceCause{Family: "paid", Code: "rate_limited", ScopeKind: "provider", Provider: "synthetic"}, Generation: 1, Active: true, BeginNoticeID: testID(23), BeginSequence: 15, Members: []uuid.UUID{testID(4)}, ActiveMembers: []uuid.UUID{testID(4)}}}, ThroughSequence: 15}, ""},
	}
	for _, row := range rows {
		t.Run(row.op, func(t *testing.T) {
			t.Parallel()
			request := Request{Envelope: testEnvelope("request"), RPCID: 1, Operation: row.op, Arguments: row.arguments}
			if !strings.HasPrefix(row.op, "host.") && row.op != "session.list" {
				request.Binding = &Binding{testID(2), testID(4), testID(5), testID(6)}
			}
			if row.expected != "" {
				id := testID(6)
				if row.expected == "host" {
					id = testID(3)
				}
				if row.expected == "continuation" {
					id = testID(8)
				}
				request.Expected, request.CommandID = &Expected{row.expected, id, 1}, testID(9)
			}
			server := readyServer(t)
			got, err := server.Decode(mustEncode(t, request, ""))
			if err != nil || !reflect.DeepEqual(request, got) {
				t.Fatalf("request: got %#v, err %v", got, err)
			}
			client, err := NewClientDecoder(testHello())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Decode(mustEncode(t, testWelcome(), "")); err != nil {
				t.Fatal(err)
			}
			if err := client.ExpectResponse(request); err != nil {
				t.Fatal(err)
			}
			response := Response{Envelope: testEnvelope("response"), RPCID: 1, Code: OK, Last: true, Result: row.result}
			got, err = client.Decode(mustEncode(t, response, row.op))
			if err != nil || !reflect.DeepEqual(response, got) {
				t.Fatalf("response: got %#v, err %v", got, err)
			}
			if err := server.SentResponse(response); err != nil {
				t.Fatal(err)
			}
			if row.expected != "" && row.expected != "host" {
				held := response
				held.Code, held.Result = Held, HeldResult{EligibilityView{State: "held", Reason: "no_goal", Version: 1, ThroughSequence: 10}}
				got, err := Decode(mustEncode(t, held, row.op), row.op)
				if err != nil || !reflect.DeepEqual(held, got) {
					t.Fatalf("held result: got %#v, err %v", got, err)
				}
			}
		})
	}
	// Escapes and a multibyte code point at the cut exercise byte caps rather
	// than rune counts. Every part is decoded and concatenated losslessly.
	text := strings.Repeat("x", MaxTextBytes-1) + "\U0001f642" + strings.Repeat("\t", MaxTextBytes)
	for _, op := range []string{"text.attach", "text.final"} {
		response := Response{Envelope: testEnvelope("response"), RPCID: 1, Code: OK, Result: TextPart{text}}
		if op == "text.attach" {
			response.Result = TextSnapshotPart{Text: text, DeltaHighWater: 2, State: "provisional", SubscriptionID: testID(7)}
		}
		parts, err := ChunkText(response, op)
		if err != nil {
			t.Fatal(err)
		}
		var rebuilt strings.Builder
		for i, part := range parts {
			raw := mustEncode(t, part, op)
			got, err := Decode(raw, op)
			if err != nil {
				t.Fatal(err)
			}
			p := got.(Response)
			if p.Part != int64(i) || p.Last != (i == len(parts)-1) || p.Seq != int64(i+1) {
				t.Fatal("noncontiguous text parts")
			}
			var chunk string
			if i == 0 && op == "text.attach" {
				s := p.Result.(TextSnapshotPart)
				chunk = s.Text
				if s.DeltaHighWater != 2 || s.SubscriptionID != testID(7) {
					t.Fatal("lost snapshot boundary")
				}
			} else {
				chunk = p.Result.(TextPart).Text
			}
			if !utf8.ValidString(chunk) || len(chunk) > MaxTextBytes || len(raw) > MaxFrameBytes {
				t.Fatal("invalid chunk boundary")
			}
			rebuilt.WriteString(chunk)
		}
		if rebuilt.String() != text {
			t.Fatal("text was truncated or reordered")
		}
	}
}

type headerOnly struct {
	header   *bytes.Reader
	bodyRead bool
}

func (r *headerOnly) Read(p []byte) (int, error) {
	if r.header.Len() == 0 {
		r.bodyRead = true
		return 0, errors.New("body must not be read")
	}
	return r.header.Read(p)
}
func TestRefusals(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name   string
		length uint32
	}{{"zero_length", 0}, {"cap_plus_one", MaxFrameBytes + 1}, {"uint32_max", 0xffffffff}} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var header [4]byte
			binary.BigEndian.PutUint32(header[:], row.length)
			r := &headerOnly{header: bytes.NewReader(header[:])}
			body, err := ReadFrame(r)
			if err == nil || body != nil || r.bodyRead {
				t.Fatal("invalid length reached the body")
			}
		})
	}
	t.Run("truncated_body", func(t *testing.T) {
		t.Parallel()
		if _, err := ReadFrame(bytes.NewReader([]byte{0, 0, 0, 2, '{'})); err != io.ErrUnexpectedEOF {
			t.Fatalf("got %v", err)
		}
	})
	hello := string(mustEncode(t, testHello(), ""))
	rows := []struct{ name, raw string }{
		{"duplicate_plain", strings.Replace(hello, `"kind":"hello"`, `"kind":"hello","kind":"hello"`, 1)},
		{"duplicate_escaped", strings.Replace(hello, `"kind":"hello"`, `"kind":"hello","\u006bind":"hello"`, 1)},
		{"null", strings.Replace(hello, `"profile":"base"`, `"profile":null`, 1)},
		{"trailing_data", hello + `{}`},
		{"unpaired_surrogate", strings.Replace(hello, `"base"`, `"\ud800"`, 1)},
		{"depth_33", `{"kind":"hello","extra":` + strings.Repeat(`[`, 32) + `0` + strings.Repeat(`]`, 32) + `}`},
		{"unknown_kind", strings.Replace(hello, `"hello"`, `"future"`, 1)},
		{"other_version", strings.Replace(hello, `"versions":[1]`, `"versions":[2]`, 1)},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Decode([]byte(row.raw), ""); err == nil {
				t.Fatal("accepted invalid frame")
			}
		})
	}
	request := Request{Envelope: testEnvelope("request"), RPCID: 7, Operation: "host.status", Arguments: HostStatus{}}
	raw := mustEncode(t, request, "")
	t.Run("unknown_operation", func(t *testing.T) {
		t.Parallel()
		d := readyServer(t)
		unknown := bytes.Replace(raw, []byte(`"host.status"`), []byte(`"future.op"`), 1)
		got, err := d.Decode(unknown)
		var refusal *RequestRefusal
		if !errors.As(err, &refusal) || refusal.RPCID != 7 || refusal.Code != InvalidCommand || got.(Request).RPCID != 7 {
			t.Fatalf("lost RPC: %#v %v", got, err)
		}
		response := Response{Envelope: testEnvelope("response"), RPCID: 7, Code: InvalidCommand, Last: true}
		if err := d.SentResponse(response); err != nil {
			t.Fatal(err)
		}
		next := request
		next.Seq = 2
		next.RPCID = 8
		if _, err := d.Decode(mustEncode(t, next, "")); err != nil {
			t.Fatalf("unknown operation killed connection: %v", err)
		}
	})
	t.Run("request_before_hello", func(t *testing.T) {
		t.Parallel()
		unknown := bytes.Replace(raw, []byte(`"host.status"`), []byte(`"future.op"`), 1)
		if _, err := NewServerDecoder().Decode(unknown); !errors.Is(err, ErrProtocol) {
			t.Fatalf("prehello request was not a protocol failure: %v", err)
		}
	})
	for _, row := range []struct {
		name string
		seq  int64
	}{{"seq_repeat", 1}, {"seq_gap", 3}} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			d := readyServer(t)
			if _, err := d.Decode(raw); err != nil {
				t.Fatal(err)
			}
			next := request
			next.Seq = row.seq
			next.RPCID = 8
			if _, err := d.Decode(mustEncode(t, next, "")); err == nil {
				t.Fatal("accepted noncontiguous sequence")
			}
		})
	}
}
