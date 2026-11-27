package launcher

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
	"uuid"
)

type Scope string

const (
	HostScope        Scope = "host"
	SessionScope     Scope = "session"
	ColonyOwnerScope Scope = "colony_owner"
)

type Idempotency string

const (
	NoIdempotency   Idempotency = "none"
	LauncherBind    Idempotency = "f7_30_bind"
	NativeReceipt   Idempotency = "native_receipt"
	HostIncarnation Idempotency = "host_incarnation_ram"
)

// OperationSpec is the per-operation admission contract. No global expected
// kind or binding rule prevents a later colony-owner table row.
type OperationSpec struct {
	Scope          Scope
	NeedsCommandID bool
	NeedsExpected  bool
	ExpectedKinds  []string
	Idempotency    Idempotency
}
type operation struct {
	OperationSpec
	arguments    func([]byte) (any, error)
	result       func([]byte) (any, error)
	argumentType reflect.Type
	resultType   reflect.Type
}

func operationOf[A, R any](scope Scope, idempotency Idempotency, expected ...string) operation {
	return operation{OperationSpec{scope, idempotency != NoIdempotency, len(expected) != 0, expected, idempotency}, readAs[A], readAs[R], reflect.TypeFor[A](), reflect.TypeFor[R]()}
}

var operations = map[string]operation{
	"host.status":       operationOf[HostStatus, HostStatusResult](HostScope, NoIdempotency),
	"host.watch":        operationOf[HostWatch, HostStatusResult](HostScope, NoIdempotency),
	"host.shutdown":     operationOf[HostShutdown, HostStatusResult](HostScope, HostIncarnation, "host"),
	"session.list":      operationOf[SessionList, SessionListResult](HostScope, NoIdempotency),
	"message.send":      operationOf[MessageSend, MutationResult](SessionScope, LauncherBind, "context"),
	"turn.cancel":       operationOf[TurnCancel, MutationResult](SessionScope, LauncherBind, "continuation"),
	"promotion.request": operationOf[PromotionRequest, MutationResult](SessionScope, LauncherBind, "context"),
	"turn.status":       operationOf[TurnStatus, TurnView](SessionScope, NoIdempotency),
	"context.status":    operationOf[ContextStatus, ContextView](SessionScope, NoIdempotency),
	"session.events":    operationOf[SessionEvents, EventPage](SessionScope, NoIdempotency),
	"session.wait":      operationOf[SessionWait, EmptyResult](SessionScope, NoIdempotency),
	"text.attach":       operationOf[TextAttach, TextSnapshotPart](SessionScope, NoIdempotency),
	"text.close":        operationOf[TextClose, EmptyResult](SessionScope, NoIdempotency),
	"text.final":        operationOf[TextFinal, TextPart](SessionScope, NoIdempotency),
	"service.status":    operationOf[ServiceStatus, ServiceView](SessionScope, NoIdempotency),
	"service.outages":   operationOf[ServiceOutages, ServiceOutagePage](SessionScope, NoIdempotency),
}

func LookupOperation(name string) (OperationSpec, bool) {
	op, ok := operations[name]
	spec := op.OperationSpec
	spec.ExpectedKinds = slices.Clone(spec.ExpectedKinds)
	return spec, ok
}

// RequestRefusal is an effect-free per-RPC refusal, not a connection failure.
// In particular unknown operations retain RPCID and permit the next frame.
type RequestRefusal struct {
	RPCID int64
	Code  Code
}

func (e *RequestRefusal) Error() string { return "launcher: request refused: " + string(e.Code) }

// Encode writes one canonical draft JSON object, without its length header.
// responseOperation is required only for a response, since it is correlated
// locally by rpc_id rather than redundantly carried on the wire.
func Encode(message any, responseOperation string) ([]byte, error) {
	switch p := message.(type) {
	case Hello, Welcome, Refused, HostState, TextDelta, TextEnd:
	case Request:
		op, ok := operations[p.Operation]
		if !ok {
			return nil, &RequestRefusal{p.RPCID, InvalidCommand}
		}
		if reflect.TypeOf(p.Arguments) != op.argumentType {
			return nil, ErrProtocol
		}
	case Response:
		op, ok := operations[responseOperation]
		if !ok && p.Result != nil {
			return nil, ErrIncompatible
		}
		want := op.resultType
		if responseOperation == "text.attach" && p.Part > 0 {
			want = reflect.TypeFor[TextPart]()
		}
		if p.Code == Held && op.Idempotency == LauncherBind {
			want = reflect.TypeFor[HeldResult]()
		}
		if p.Result != nil && reflect.TypeOf(p.Result) != want {
			return nil, ErrProtocol
		}
	default:
		return nil, ErrProtocol
	}
	if err := validateValue(message); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(message)
	if err != nil {
		return nil, ErrProtocol
	}
	if len(raw) > MaxFrameBytes {
		return nil, ErrTooLarge
	}
	if _, err := Decode(raw, responseOperation); err != nil {
		return nil, err
	}
	return raw, nil
}

// Decode first checks the envelope, then selects arguments/results from the
// operation table. It does not authenticate a peer or authorize a projection.
func Decode(raw []byte, responseOperation string) (any, error) {
	if len(raw) > MaxFrameBytes {
		return nil, ErrTooLarge
	}
	n, err := parseStrict(raw)
	if err != nil || n.kind != 'o' {
		return nil, ErrProtocol
	}
	kind := n.members["kind"]
	if kind == nil || kind.kind != 's' {
		return nil, ErrProtocol
	}
	switch kind.text {
	case "hello":
		return readAs[Hello](raw)
	case "welcome":
		return readAs[Welcome](raw)
	case "refused":
		return readAs[Refused](raw)
	case "host_state":
		return readAs[HostState](raw)
	case "text_delta":
		return readAs[TextDelta](raw)
	case "text_end":
		return readAs[TextEnd](raw)
	case "request":
		if err := checkShape(n, reflect.TypeFor[Request]()); err != nil {
			return nil, err
		}
		var p Request
		wire := struct {
			*Request
			Arguments json.RawMessage `json:"arguments"`
		}{Request: &p}
		if json.Unmarshal(raw, &wire) != nil || validateValue(p.Envelope) != nil || p.RPCID <= 0 {
			return nil, ErrProtocol
		}
		op, ok := operations[p.Operation]
		if !ok {
			return p, &RequestRefusal{p.RPCID, InvalidCommand}
		}
		if (p.Binding != nil) != (op.Scope == SessionScope) {
			return p, &RequestRefusal{p.RPCID, InvalidBinding}
		}
		if (p.CommandID != (uuid.UUID{})) != op.NeedsCommandID || (p.Expected != nil) != op.NeedsExpected || p.Expected != nil && !slices.Contains(op.ExpectedKinds, p.Expected.Kind) {
			return p, &RequestRefusal{p.RPCID, InvalidCommand}
		}
		p.Arguments, err = op.arguments(wire.Arguments)
		if err != nil {
			return nil, err
		}
		if p.Expected != nil {
			if p.Expected.Kind == "context" && p.Binding != nil && p.Expected.ID != p.Binding.ContextID {
				return p, &RequestRefusal{p.RPCID, InvalidBinding}
			}
			if a, ok := p.Arguments.(TurnCancel); ok && p.Expected.ID != a.TurnID {
				return p, &RequestRefusal{p.RPCID, InvalidCommand}
			}
		}
		return p, nil
	case "response":
		if err := checkShape(n, reflect.TypeFor[Response]()); err != nil {
			return nil, err
		}
		var p Response
		wire := struct {
			*Response
			Result json.RawMessage `json:"result"`
		}{Response: &p}
		if json.Unmarshal(raw, &wire) != nil || validateValue(p.Envelope) != nil || p.RPCID <= 0 {
			return nil, ErrProtocol
		}
		if !validCode(p.Code) {
			return nil, ErrIncompatible
		}
		if len(wire.Result) == 0 {
			if p.Code == OK || p.Code == Held && operations[responseOperation].Idempotency == LauncherBind || !p.Last || p.Part != 0 {
				return nil, ErrProtocol
			}
			return p, nil
		}
		op, ok := operations[responseOperation]
		if !ok {
			return nil, ErrIncompatible
		}
		decode := op.result
		if responseOperation == "text.attach" && p.Part > 0 {
			decode = readAs[TextPart]
		}
		if p.Code == Held && op.Idempotency == LauncherBind {
			decode = readAs[HeldResult]
		}
		if responseOperation != "text.attach" && responseOperation != "text.final" && (p.Part != 0 || !p.Last) {
			return nil, ErrProtocol
		}
		p.Result, err = decode(wire.Result)
		return p, err
	default:
		return nil, ErrProtocol
	}
}

func validCode(code Code) bool {
	return slices.Contains([]Code{OK, NotReady, Unauthorized, Incompatible, WrongColony, InvalidBinding, InvalidCommand, CommandMismatch, StaleRevision, StalePage, Busy, Held, Unavailable, RecoveryPending, InProgress, TooLarge, ProtocolError, Closed, ClosureUnconfirmed}, code)
}
func lifecycle(state string, revision int64) bool {
	return revision == int64(slices.Index([]string{"booting", "recovering", "ready", "quiescing", "stopped"}, state)+1) && revision > 0
}
func validSID(s string) bool {
	parts := strings.Split(s, "-")
	if len(parts) < 3 || len(parts) > 18 || parts[0] != "S" || parts[1] != "1" {
		return false
	}
	for i, p := range parts[2:] {
		bits := 32
		if i == 0 {
			bits = 48
		}
		n, err := strconv.ParseUint(p, 10, bits)
		if err != nil || strconv.FormatUint(n, 10) != p {
			return false
		}
	}
	return true
}

func validateValue(value any) error {
	switch p := value.(type) {
	case Hello:
		if p.Kind != "hello" {
			return ErrProtocol
		}
		if p.Profile != "base" || len(p.Versions) != 1 || p.Versions[0] != DraftVersion {
			return ErrIncompatible
		}
	case Welcome:
		if p.Kind != "welcome" || p.MaxFrameBytes != MaxFrameBytes || !lifecycle(p.State, p.LifecycleRevision) {
			return ErrProtocol
		}
		if p.Profile != "base" || p.Version != DraftVersion {
			return ErrIncompatible
		}
	case Refused:
		if p.Kind != "refused" || !slices.Contains([]Code{Unauthorized, Incompatible, WrongColony, Busy, ProtocolError, Unavailable}, p.Code) {
			return ErrProtocol
		}
	case Principal:
		if p.Kind == "unix_uid" {
			if p.UID == nil || p.UserSID != "" || p.AuthenticationID != "" {
				return ErrProtocol
			}
		} else if p.Kind == "windows_logon" {
			if p.UID != nil || !validSID(p.UserSID) || !lowerHex(p.AuthenticationID, 16) {
				return ErrProtocol
			}
		} else {
			return ErrIncompatible
		}
	case Envelope:
		if p.Version != DraftVersion {
			return ErrIncompatible
		}
		if p.Seq <= 0 || !slices.Contains([]string{"request", "response", "host_state", "text_delta", "text_end"}, p.Kind) {
			return ErrProtocol
		}
	case HostState:
		if p.Kind != "host_state" || !lifecycle(p.State, p.LifecycleRevision) {
			return ErrProtocol
		}
	case HostStatusResult:
		if !lifecycle(p.State, p.LifecycleRevision) || p.Startup != nil && p.LifecycleRevision < 3 {
			return ErrProtocol
		}
	case StartupSummary:
		if len(p.Conditions) > 8 {
			return ErrProtocol
		}
		for _, condition := range p.Conditions {
			if !slices.Contains([]string{"process_unconfirmed", "private_integrity", "service_recovery_pending", "previous_host_unclean"}, condition) {
				return ErrIncompatible
			}
		}
	case SessionList:
		if p.Limit < 1 || p.Limit > MaxPageRows || (p.AfterAgentID == (uuid.UUID{})) != (p.AfterContextID == (uuid.UUID{})) || (p.AfterAgentID != (uuid.UUID{})) != (p.ThroughSeq != nil) {
			return ErrProtocol
		}
	case SessionListResult:
		if len(p.Rows) > MaxPageRows || (p.NextAgentID == (uuid.UUID{})) != (p.NextContextID == (uuid.UUID{})) {
			return ErrProtocol
		}
	case SessionEvents:
		if p.Limit < 1 || p.Limit > MaxPageRows {
			return ErrProtocol
		}
	case ServiceOutages:
		if p.Limit < 0 || p.Limit > MaxPageRows || p.After != nil && p.ThroughSequence != 0 && p.After.ThroughSequence != p.ThroughSequence {
			return ErrProtocol
		}
	case ServiceOutagePage:
		if len(p.Rows) > MaxPageRows {
			return ErrProtocol
		}
	case ServiceSource:
		if p.Kind == "" || p.Reason == "" || p.ObservationID == (uuid.UUID{}) || p.ObservedUTCNS <= 0 {
			return ErrProtocol
		}
	case EventPage:
		if len(p.Events) > MaxPageRows {
			return ErrProtocol
		}
	case ArtifactRef:
		if p.SourceSequence <= 0 || p.Bytes <= 0 {
			return ErrProtocol
		}
	case RecordPayload:
		if p.RecordVersion != 1 || !slices.Contains([]string{"runtime.turn_bound", "runtime.tool_started", "runtime.tool_result", "runtime.paid_result", "runtime.turn_result"}, p.RecordType) {
			return ErrIncompatible
		}
	case TurnView:
		if p.Result != nil && p.Result.RecordType != "runtime.turn_result" {
			return ErrProtocol
		}
	case MutationResult:
		if !slices.Contains([]string{"bound", "in_progress", "admitted_unbound", "recovery_pending", "resolved"}, p.State) || (p.State == "resolved") != (p.Outcome != "") || p.Outcome != "" && !slices.Contains([]string{"applied", "accepted", "too_late", "interrupted"}, p.Outcome) || p.BindingSequence <= 0 {
			return ErrProtocol
		}
		if p.NativeReceiptSequence != nil && *p.NativeReceiptSequence <= 0 || p.NativeEffectSequence != nil && *p.NativeEffectSequence <= 0 {
			return ErrProtocol
		}
	case TextDelta:
		if p.Kind != "text_delta" || p.Index <= 0 || len(p.Text) > MaxTextBytes {
			return ErrProtocol
		}
	case TextEnd:
		if p.Kind != "text_end" || p.Code == "" {
			return ErrProtocol
		}
	case TextPart:
		if len(p.Text) > MaxTextBytes {
			return ErrTooLarge
		}
	case TextSnapshotPart:
		if len(p.Text) > MaxTextBytes || (p.State == "provisional") != (p.SubscriptionID != (uuid.UUID{})) {
			return ErrProtocol
		}
	case Code:
		if p != "" && !validCode(p) {
			return ErrIncompatible
		}
	case Event:
		if p.Sequence <= 0 {
			return ErrProtocol
		}
		typ, record := eventDataType(p.Type)
		if typ == nil {
			return ErrIncompatible
		}
		if reflect.TypeOf(p.Data) != typ {
			return ErrProtocol
		}
		if tagged, ok := p.Data.(RecordPayload); ok && tagged.RecordType != record {
			return ErrProtocol
		}
		agent, session, context := p.AgentID != (uuid.UUID{}), p.SessionID != (uuid.UUID{}), p.ContextID != (uuid.UUID{})
		switch p.Type {
		case "service.changed", "outage.joined":
			if !agent || session || context || p.TurnID != (uuid.UUID{}) || p.Data.(ServiceChanged).AgentID != p.AgentID {
				return ErrProtocol
			}
		case "outage.began", "outage.ended":
			if agent || session || context || p.TurnID != (uuid.UUID{}) || "outage."+p.Data.(OutageNotice).Edge != p.Type {
				return ErrProtocol
			}
		default:
			if !agent || !session || !context {
				return ErrProtocol
			}
		}
	}
	v := reflect.ValueOf(value)
	if !v.IsValid() {
		return nil
	}
	if v.Type() == uuidType || v.Type() == rawType {
		return nil
	}
	switch v.Kind() {
	case reflect.String:
		if !utf8.ValidString(v.String()) {
			return ErrProtocol
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			return validateValue(v.Elem().Interface())
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if err := validateValue(v.Field(i).Interface()); err != nil {
				return err
			}
		}
	case reflect.Slice:
		for i := range v.Len() {
			if err := validateValue(v.Index(i).Interface()); err != nil {
				return err
			}
		}
	}
	return nil
}

func eventDataType(kind string) (reflect.Type, string) {
	record := map[string]string{"turn.started": "runtime.turn_bound", "tool.started": "runtime.tool_started", "tool.finished": "runtime.tool_result", "usage": "runtime.paid_result", "turn.finished": "runtime.turn_result", "turn.cancelled": "runtime.turn_result", "turn.failed": "runtime.turn_result"}[kind]
	if record != "" {
		return reflect.TypeFor[RecordPayload](), record
	}
	switch kind {
	case "tool.output":
		return reflect.TypeFor[ObjectRef](), ""
	case "eligibility.changed":
		return reflect.TypeFor[EligibilityView](), ""
	case "service.changed", "outage.joined":
		return reflect.TypeFor[ServiceChanged](), ""
	case "outage.began", "outage.ended":
		return reflect.TypeFor[OutageNotice](), ""
	default:
		return nil, ""
	}
}
func (e Event) MarshalJSON() ([]byte, error) {
	if err := validateValue(e); err != nil {
		return nil, err
	}
	type plain Event
	return json.Marshal(plain(e))
}
func (e *Event) UnmarshalJSON(raw []byte) error {
	n, err := parseStrict(raw)
	if err != nil {
		return err
	}
	if err := checkShape(n, reflect.TypeFor[Event]()); err != nil {
		return err
	}
	type plain Event
	var p plain
	wire := struct {
		*plain
		Data json.RawMessage `json:"data"`
	}{plain: &p}
	if json.Unmarshal(raw, &wire) != nil {
		return ErrProtocol
	}
	typ, _ := eventDataType(p.Type)
	if typ == nil {
		return ErrIncompatible
	}
	v := reflect.New(typ)
	if err := decodeTyped(wire.Data, v.Interface()); err != nil {
		return err
	}
	p.Data = v.Elem().Interface()
	if err := validateValue(Event(p)); err != nil {
		return err
	}
	*e = Event(p)
	return nil
}

// Decoder checks one incoming direction. Use serially. A server must explicitly
// AcceptWelcome only after native authentication; a client tracks each outgoing
// request so its responses have a known type and contiguous part ordinals.
type Decoder struct {
	server    bool
	hello     Hello
	helloSeen bool
	welcomed  bool
	closed    bool
	next      int64
	calls     map[int64]pendingCall
}
type pendingCall struct {
	operation string
	part      int64
}

func NewServerDecoder() *Decoder {
	return &Decoder{server: true, next: 1, calls: map[int64]pendingCall{}}
}
func NewClientDecoder(hello Hello) (*Decoder, error) {
	if _, err := Encode(hello, ""); err != nil {
		return nil, err
	}
	return &Decoder{hello: hello, helloSeen: true, next: 1, calls: map[int64]pendingCall{}}, nil
}
func (d *Decoder) AcceptWelcome(w Welcome) error {
	if !d.server || !d.helloSeen || d.welcomed || d.closed || w.ConnectionID != d.hello.ConnectionID || w.ColonyID != d.hello.ColonyID {
		return ErrProtocol
	}
	if _, err := Encode(w, ""); err != nil {
		return err
	}
	d.welcomed = true
	return nil
}
func (d *Decoder) ExpectResponse(request Request) error {
	if d.server || !d.welcomed || d.closed || request.ConnectionID != d.hello.ConnectionID {
		return ErrProtocol
	}
	if _, err := Encode(request, ""); err != nil {
		return err
	}
	return d.addCall(request.RPCID, request.Operation)
}
func (d *Decoder) addCall(id int64, operation string) error {
	if _, exists := d.calls[id]; exists || len(d.calls) >= MaxResources {
		return ErrProtocol
	}
	d.calls[id] = pendingCall{operation: operation}
	return nil
}

// SentResponse retires a server RPC only on its final part. The writer calls it
// in send order; it never cancels the native operation associated with the RPC.
func (d *Decoder) SentResponse(response Response) error {
	if !d.server || !d.welcomed || d.closed || response.ConnectionID != d.hello.ConnectionID {
		return ErrProtocol
	}
	call, ok := d.calls[response.RPCID]
	if !ok {
		return ErrProtocol
	}
	if _, err := Encode(response, call.operation); err != nil {
		return err
	}
	return d.finishPart(response)
}
func (d *Decoder) finishPart(p Response) error {
	call, ok := d.calls[p.RPCID]
	if !ok || p.Part != call.part || p.Part == math.MaxInt64 {
		return ErrProtocol
	}
	if p.Last {
		delete(d.calls, p.RPCID)
	} else {
		call.part++
		d.calls[p.RPCID] = call
	}
	return nil
}
func (d *Decoder) Decode(raw []byte) (message any, err error) {
	if d.closed {
		return nil, ErrProtocol
	}
	defer func() {
		var refusal *RequestRefusal
		if err != nil && !errors.As(err, &refusal) {
			d.closed = true
		}
	}()
	if !d.welcomed {
		if len(raw) > MaxFrameBytes {
			return nil, ErrTooLarge
		}
		n, parseErr := parseStrict(raw)
		if parseErr != nil || n.kind != 'o' {
			return nil, ErrProtocol
		}
		kind := n.members["kind"]
		if kind == nil || kind.kind != 's' || d.server && (d.helloSeen || kind.text != "hello") || !d.server && kind.text != "welcome" && kind.text != "refused" {
			return nil, ErrProtocol
		}
		message, err = Decode(raw, "")
		if err != nil {
			return nil, err
		}
		if d.server {
			h, ok := message.(Hello)
			if !ok || d.helloSeen {
				return nil, ErrProtocol
			}
			d.hello, d.helloSeen = h, true
		} else {
			switch p := message.(type) {
			case Welcome:
				if p.ConnectionID != d.hello.ConnectionID || p.ColonyID != d.hello.ColonyID {
					return nil, ErrProtocol
				}
				d.welcomed = true
			case Refused:
				d.closed = true
			default:
				return nil, ErrProtocol
			}
		}
		return message, nil
	}
	if len(raw) > MaxFrameBytes {
		return nil, ErrTooLarge
	}
	if _, err = parseStrict(raw); err != nil {
		return nil, err
	}
	var header struct {
		Envelope
		RPCID int64 `json:"rpc_id"`
	}
	if json.Unmarshal(raw, &header) != nil || validateValue(header.Envelope) != nil || header.ConnectionID != d.hello.ConnectionID || header.Seq != d.next || d.next == 0 {
		return nil, ErrProtocol
	}
	if d.server && header.Kind != "request" || !d.server && header.Kind == "request" {
		return nil, ErrProtocol
	}
	operation := ""
	if header.Kind == "response" {
		call, ok := d.calls[header.RPCID]
		if !ok {
			return nil, ErrProtocol
		}
		operation = call.operation
	}
	message, err = Decode(raw, operation)
	var refusal *RequestRefusal
	if err != nil && !errors.As(err, &refusal) {
		return nil, err
	}
	if header.Seq == math.MaxInt64 {
		d.next = 0
	} else {
		d.next++
	}
	if p, ok := message.(Request); ok {
		if e := d.addCall(p.RPCID, p.Operation); e != nil {
			return nil, e
		}
	}
	if p, ok := message.(Response); ok {
		if e := d.finishPart(p); e != nil {
			return nil, e
		}
	}
	return message, err
}
