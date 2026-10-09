package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"
	"uuid"
)

// FormatVersion is the ledger format version that this package writes and
// reads.
const FormatVersion = 1

// ZeroHash is the prev_hash of the record with seq 1.
const ZeroHash = "0000000000000000000000000000000000000000000000000000000000000000"

const (
	// MaxRecordBytes caps one encoded line: hash, TAB, JSON and LF.
	MaxRecordBytes = 1 << 20
	// MaxCommitBytes caps all lines of one commit.
	MaxCommitBytes = 16 << 20
)

// Actor is the kind of party a record is attributed to.
type Actor string

// The closed set of actors (D35). Adding one is a format change.
const (
	ActorOwner  Actor = "owner"
	ActorAgent  Actor = "agent"
	ActorCore   Actor = "core"
	ActorRunner Actor = "runner"
)

func (a Actor) valid() bool {
	switch a {
	case ActorOwner, ActorAgent, ActorCore, ActorRunner:
		return true
	}
	return false
}

// Draft is what a producer supplies for one record. A zero ID, an empty
// MeasurementPath and an empty BodyRefs mean the field is absent.
type Draft struct {
	Type               string
	TypeVersion        int
	Actor              Actor
	AgentID            uuid.UUID
	ExecutionSessionID uuid.UUID
	ContextID          uuid.UUID
	TurnID             uuid.UUID
	OperationID        uuid.UUID
	MeasurementPath    string
	BodyRefs           []Digest        // strictly ascending, none zero
	Payload            json.RawMessage // a JSON object
}

// Envelope is a record's decoded envelope. ClockHeld is false when the key is
// absent.
type Envelope struct {
	Format      int
	Seq         uint64
	CommitID    uuid.UUID
	CommitIndex int
	CommitCount int
	PrevHash    string
	EventTimeNS int64
	ClockHeld   bool
	ColonyID    uuid.UUID
	Draft
}

// Record is a verified record as stored. Payload is a sub-slice of JSON.
type Record struct {
	Envelope
	Hash string // lowercase hex SHA-256 of JSON
	JSON []byte // the exact stored JSON bytes
}

// wireRecord fixes the emitted key order (the package documentation's
// table). Optional fields are omitted when zero. BodyRefs uses omitempty,
// not omitzero, because omitzero would emit an empty non-nil slice as [],
// which readers reject.
type wireRecord struct {
	Format             int             `json:"format"`
	Seq                uint64          `json:"seq"`
	CommitID           uuid.UUID       `json:"commit_id"`
	CommitIndex        int             `json:"commit_index"`
	CommitCount        int             `json:"commit_count"`
	PrevHash           string          `json:"prev_hash"`
	EventTimeNS        int64           `json:"event_time_ns"`
	ClockHeld          bool            `json:"clock_held,omitzero"`
	ColonyID           uuid.UUID       `json:"colony_id"`
	Type               string          `json:"type"`
	TypeVersion        int             `json:"type_version"`
	Actor              Actor           `json:"actor"`
	AgentID            uuid.UUID       `json:"agent_id,omitzero"`
	ExecutionSessionID uuid.UUID       `json:"execution_session_id,omitzero"`
	ContextID          uuid.UUID       `json:"context_id,omitzero"`
	TurnID             uuid.UUID       `json:"turn_id,omitzero"`
	OperationID        uuid.UUID       `json:"operation_id,omitzero"`
	MeasurementPath    string          `json:"measurement_path,omitzero"`
	BodyRefs           []Digest        `json:"body_refs,omitempty"`
	Payload            json.RawMessage `json:"payload"`
}

// encodeLine returns the line of e: lowercase hex SHA-256 of the JSON, TAB,
// the JSON and LF, and the hash. It applies no validation or cap.
func encodeLine(e *Envelope) (line []byte, hash string, err error) {
	js, err := json.Marshal(wireRecord{
		Format:             e.Format,
		Seq:                e.Seq,
		CommitID:           e.CommitID,
		CommitIndex:        e.CommitIndex,
		CommitCount:        e.CommitCount,
		PrevHash:           e.PrevHash,
		EventTimeNS:        e.EventTimeNS,
		ClockHeld:          e.ClockHeld,
		ColonyID:           e.ColonyID,
		Type:               e.Type,
		TypeVersion:        e.TypeVersion,
		Actor:              e.Actor,
		AgentID:            e.AgentID,
		ExecutionSessionID: e.ExecutionSessionID,
		ContextID:          e.ContextID,
		TurnID:             e.TurnID,
		OperationID:        e.OperationID,
		MeasurementPath:    e.MeasurementPath,
		BodyRefs:           e.BodyRefs,
		Payload:            e.Payload,
	})
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(js)
	line = make([]byte, 0, 2*sha256.Size+len(js)+2)
	line = hex.AppendEncode(line, sum[:])
	line = append(line, '\t')
	line = append(line, js...)
	line = append(line, '\n')
	return line, string(line[:2*sha256.Size]), nil
}

// frameLine checks the framing and hash of line, whose only LF is its last
// byte. It returns the JSON and whether line is correctly framed and hashed:
// 64 lowercase hex digits, TAB, non-empty JSON whose SHA-256 they encode, LF.
func frameLine(line []byte) (js []byte, ok bool) {
	const h = 2 * sha256.Size
	if len(line) < h+3 || line[len(line)-1] != '\n' || line[h] != '\t' || !isLowerHex(line[:h]) {
		return nil, false
	}
	js = line[h+1 : len(line)-1]
	sum := sha256.Sum256(js)
	var want [h]byte
	hex.Encode(want[:], sum[:])
	if !bytes.Equal(line[:h], want[:]) {
		return nil, false
	}
	return js, true
}

func isLowerHex(b []byte) bool {
	for _, c := range b {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// formatError reports a well-formed format key whose value is not
// FormatVersion.
type formatError struct{ format int }

func (e *formatError) Error() string { return fmt.Sprintf("format %d", e.format) }

// Envelope key bits; key bit 1<<i is named envelopeKeys[i].
const (
	keyFormat = 1 << iota
	keySeq
	keyCommitID
	keyCommitIndex
	keyCommitCount
	keyPrevHash
	keyEventTime
	keyClockHeld
	keyColonyID
	keyType
	keyTypeVersion
	keyActor
	keyAgentID
	keyExecutionSessionID
	keyContextID
	keyTurnID
	keyOperationID
	keyMeasurementPath
	keyBodyRefs
	keyPayload

	keysRequired = keyFormat | keySeq | keyCommitID | keyCommitIndex | keyCommitCount | keyPrevHash |
		keyEventTime | keyColonyID | keyType | keyTypeVersion | keyActor | keyPayload
)

// envelopeKeys names the envelope keys in emitted order.
var envelopeKeys = [...]string{
	"format", "seq", "commit_id", "commit_index", "commit_count", "prev_hash", "event_time_ns",
	"clock_held", "colony_id", "type", "type_version", "actor", "agent_id",
	"execution_session_id", "context_id", "turn_id", "operation_id", "measurement_path", "body_refs",
	"payload",
}

// keyBit returns the bit of an envelope key, or 0 for an unknown key.
func keyBit(name string) int {
	for i, k := range envelopeKeys {
		if k == name {
			return 1 << i
		}
	}
	return 0
}

// decodeEnvelope decodes the JSON of a correctly framed and hashed line. It
// returns a *formatError for a format other than FormatVersion, and another
// error naming the broken rule otherwise. e.Payload is js[at:at+len(e.Payload)].
// compact is scratch space for the compactness check.
func decodeEnvelope(js []byte, compact *bytes.Buffer) (e Envelope, at int, err error) {
	dec := json.NewDecoder(bytes.NewReader(js))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return e, 0, errors.New("not a JSON object")
	}
	// The format is the first key, so that a later format is reported as
	// such whatever else it changes.
	if !dec.More() {
		return e, 0, errors.New("first key is not format")
	}
	if key, err := dec.Token(); err != nil || key != "format" {
		return e, 0, errors.New("first key is not format")
	}
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return e, 0, errors.New("not valid JSON")
	}
	n, ok := parseNat(raw, math.MaxInt)
	if !ok {
		return e, 0, errors.New("format is not a non-negative integer")
	}
	if n != FormatVersion {
		return e, 0, &formatError{format: int(n)}
	}
	e.Format = int(n)

	if !utf8.Valid(js) {
		return e, 0, errors.New("not valid UTF-8")
	}
	compact.Reset()
	if err := json.Compact(compact, js); err != nil {
		return e, 0, errors.New("not valid JSON")
	}
	if !bytes.Equal(compact.Bytes(), js) {
		return e, 0, errors.New("JSON is not compact")
	}

	seen := keyFormat
	var unknown map[string]bool
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return e, 0, errors.New("not valid JSON")
		}
		key := tok.(string)
		if err := dec.Decode(&raw); err != nil {
			return e, 0, errors.New("not valid JSON")
		}
		bit := keyBit(key)
		if bit == 0 {
			if unknown[key] {
				return e, 0, fmt.Errorf("duplicate key %q", key)
			}
			if unknown == nil {
				unknown = make(map[string]bool)
			}
			unknown[key] = true
			continue
		}
		if seen&bit != 0 {
			return e, 0, fmt.Errorf("duplicate key %q", key)
		}
		seen |= bit
		if string(raw) == "null" {
			return e, 0, fmt.Errorf("%s is null", key)
		}
		if bit == keyPayload {
			if raw[0] != '{' {
				return e, 0, errors.New("payload is not a JSON object")
			}
			end := int(dec.InputOffset())
			at = end - len(raw)
			e.Payload = js[at:end:end]
			continue
		}
		if err := e.set(bit, raw); err != nil {
			return e, 0, fmt.Errorf("%s %w", key, err)
		}
	}
	for i, key := range envelopeKeys {
		if bit := 1 << i; keysRequired&bit != 0 && seen&bit == 0 {
			return e, 0, fmt.Errorf("required key %s is missing", key)
		}
	}
	if e.CommitIndex >= e.CommitCount {
		return e, 0, errors.New("commit_index is not below commit_count")
	}
	if e.ClockHeld && e.Seq == 1 {
		return e, 0, errors.New("clock_held on seq 1")
	}
	return e, at, nil
}

var (
	errNat     = errors.New("is not an integer in range")
	errID      = errors.New("is not a canonical UUIDv7")
	errGrammar = errors.New("does not match its grammar")
)

// set decodes raw, the value of the key bit other than payload, into e.
func (e *Envelope) set(bit int, raw json.RawMessage) error {
	switch bit {
	case keySeq:
		n, ok := parseNat(raw, math.MaxUint64)
		if !ok || n == 0 {
			return errNat
		}
		e.Seq = n
	case keyCommitIndex:
		n, ok := parseNat(raw, math.MaxInt)
		if !ok {
			return errNat
		}
		e.CommitIndex = int(n)
	case keyCommitCount:
		n, ok := parseNat(raw, math.MaxInt)
		if !ok || n == 0 {
			return errNat
		}
		e.CommitCount = int(n)
	case keyTypeVersion:
		n, ok := parseNat(raw, math.MaxInt)
		if !ok || n == 0 {
			return errNat
		}
		e.TypeVersion = int(n)
	case keyEventTime:
		n, ok := parseNat(raw, math.MaxInt64)
		if !ok || n == 0 {
			return errNat
		}
		e.EventTimeNS = int64(n)
	case keyClockHeld:
		if string(raw) != "true" {
			return errors.New("is not true")
		}
		e.ClockHeld = true
	case keyBodyRefs:
		refs, err := parseDigests(raw)
		if err != nil {
			return err
		}
		e.BodyRefs = refs
	default:
		s, ok := parseString(raw)
		if !ok {
			return errors.New("is not a string")
		}
		return e.setString(bit, s)
	}
	return nil
}

func (e *Envelope) setString(bit int, s string) error {
	switch bit {
	case keyPrevHash:
		if len(s) != len(ZeroHash) || !isLowerHex([]byte(s)) {
			return errors.New("is not 64 lowercase hex digits")
		}
		e.PrevHash = s
	case keyType:
		if !validType(s) {
			return errGrammar
		}
		e.Type = s
	case keyActor:
		if !Actor(s).valid() {
			return errors.New("is not a known actor")
		}
		e.Actor = Actor(s)
	case keyMeasurementPath:
		if !validPath(s) {
			return errGrammar
		}
		e.MeasurementPath = s
	default:
		u, ok := parseID(s)
		if !ok {
			return errID
		}
		switch bit {
		case keyCommitID:
			e.CommitID = u
		case keyColonyID:
			e.ColonyID = u
		case keyAgentID:
			e.AgentID = u
		case keyExecutionSessionID:
			e.ExecutionSessionID = u
		case keyContextID:
			e.ContextID = u
		case keyTurnID:
			e.TurnID = u
		case keyOperationID:
			e.OperationID = u
		}
	}
	return nil
}

// parseNat parses raw as a JSON integer in [0, limit]. Signs, fractions and
// exponents are refused.
func parseNat(raw []byte, limit uint64) (uint64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var n uint64
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, false
		}
		d := uint64(c - '0')
		if n > (limit-d)/10 {
			return 0, false
		}
		n = n*10 + d
	}
	return n, true
}

func parseString(raw []byte) (string, bool) {
	if raw[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// parseID parses s as a canonical lowercase dashed UUIDv7.
func parseID(s string) (uuid.UUID, bool) {
	u, err := uuid.Parse(s)
	if err != nil || u.String() != s || !isV7(u) {
		return uuid.UUID{}, false
	}
	return u, true
}

// isV7 reports whether u has version 7 and the RFC 9562 variant.
func isV7(u uuid.UUID) bool {
	return u[6]>>4 == 7 && u[8]&0xC0 == 0x80
}

// validType reports whether s matches ^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$.
func validType(s string) bool {
	n, ok := dottedTokens(s)
	return ok && n >= 2
}

// validPath reports whether s matches ^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$.
func validPath(s string) bool {
	_, ok := dottedTokens(s)
	return ok
}

// dottedTokens splits s at dots and checks that every token matches
// [a-z][a-z0-9_]*. It returns the number of tokens.
func dottedTokens(s string) (int, bool) {
	n := 0
	for tok := range strings.SplitSeq(s, ".") {
		if tok == "" || tok[0] < 'a' || tok[0] > 'z' {
			return 0, false
		}
		for _, c := range tok {
			if !('a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '_') {
				return 0, false
			}
		}
		n++
	}
	return n, true
}

// checkPayload checks that p is a JSON object in valid UTF-8 with no
// duplicate key in any object at any depth.
func checkPayload(p json.RawMessage) error {
	if !utf8.Valid(p) {
		return errors.New("payload is not valid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(p))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return errors.New("payload is not a JSON object")
	}
	// One level per open object or array; keys is nil for an array.
	type level struct {
		keys      map[string]bool
		expectKey bool
	}
	stack := []level{{keys: map[string]bool{}, expectKey: true}}
	for len(stack) > 0 {
		tok, err := dec.Token()
		if err != nil {
			return errors.New("payload is not valid JSON")
		}
		if top := &stack[len(stack)-1]; top.keys != nil && top.expectKey && tok != json.Delim('}') {
			key := tok.(string)
			if top.keys[key] {
				return fmt.Errorf("payload has duplicate key %q", key)
			}
			top.keys[key] = true
			top.expectKey = false
			continue
		}
		switch tok {
		case json.Delim('{'):
			stack = append(stack, level{keys: map[string]bool{}, expectKey: true})
			continue
		case json.Delim('['):
			stack = append(stack, level{})
			continue
		case json.Delim('}'), json.Delim(']'):
			stack = stack[:len(stack)-1]
		}
		// A value ended, so an enclosing object expects a key next.
		if n := len(stack); n > 0 && stack[n-1].keys != nil {
			stack[n-1].expectKey = true
		}
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("payload has data after the object")
	}
	return nil
}
