package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

const paidChunkLimit = 1 << 20
const paidOutputLimit = 8 << 20

// These encodings are context-owned evidence, not frozen ledger payloads.
type paidObservationObject struct {
	Schema                   int                  `json:"schema"`
	Kind                     string               `json:"kind"`
	AttemptID                uuid.UUID            `json:"attempt_id"`
	Origin                   ledger.Origin        `json:"origin"`
	ParentTurnID             uuid.UUID            `json:"parent_turn_id"`
	RequestSHA256            ledger.Digest        `json:"request_sha256"`
	ProviderRequestID        string               `json:"provider_request_id,omitempty"`
	ProviderMessageID        string               `json:"provider_message_id,omitempty"`
	Model                    string               `json:"model,omitempty"`
	HTTPStatus               int                  `json:"http_status"`
	Headers                  []paidResponseHeader `json:"headers"`
	StopReason               string               `json:"stop_reason,omitempty"`
	StopSequence             json.RawMessage      `json:"stop_sequence,omitempty"`
	StopDetails              json.RawMessage      `json:"stop_details,omitempty"`
	RawUsageStart            json.RawMessage      `json:"raw_usage_start,omitempty"`
	RawUsageFinal            json.RawMessage      `json:"raw_usage_final,omitempty"`
	RawUsageMerged           providers.Usage      `json:"raw_usage_merged,omitempty"`
	OutputStarted            bool                 `json:"output_started"`
	Complete                 bool                 `json:"complete"`
	MessageObjects           []ledger.ObjectRef   `json:"message_objects"`
	Error                    *paidResponseError   `json:"error,omitempty"`
	UnknownEventTypes        []string             `json:"unknown_event_types"`
	UnknownEventTypeOverflow int                  `json:"unknown_event_type_overflow"`
	ContentBlocks            int                  `json:"content_blocks"`
	ContentError             bool                 `json:"content_error"`
	ProfileViolation         bool                 `json:"profile_violation"`
	ErrorClass               string               `json:"error_class,omitempty"`
}
type paidResponseHeader struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}
type paidResponseError struct {
	Type      string          `json:"type"`
	Message   string          `json:"message"`
	Details   json.RawMessage `json:"details,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
}
type paidOutputObject struct {
	Schema       int           `json:"schema"`
	Kind         string        `json:"kind"`
	AttemptID    uuid.UUID     `json:"attempt_id"`
	Origin       ledger.Origin `json:"origin"`
	ParentTurnID uuid.UUID     `json:"parent_turn_id"`
	BlockIndex   int           `json:"block_index"`
	ChunkIndex   int           `json:"chunk_index"`
	Block        any           `json:"block"`
}
type paidTextBlock struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}
type paidToolBlock struct {
	Kind  string          `json:"kind"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}
type paidOpaqueBlock struct {
	Kind         string          `json:"kind"`
	Provider     string          `json:"provider"`
	Model        string          `json:"model"`
	ArtifactKind string          `json:"artifact_kind"`
	Payload      json.RawMessage `json:"payload"`
}

func (o *paidObservationObject) UnmarshalJSON(raw []byte) error {
	type plain paidObservationObject
	var wire struct {
		*plain
		RequestSHA256  string `json:"request_sha256"`
		MessageObjects []struct {
			SHA256 string `json:"sha256"`
			Bytes  int64  `json:"bytes"`
		} `json:"message_objects"`
	}
	wire.plain = (*plain)(o)
	if err := json.Unmarshal(raw, &wire); err != nil {
		return ErrIntegrity
	}
	digest, err := ledger.ParseDigest(wire.RequestSHA256)
	if err != nil {
		return ErrIntegrity
	}
	o.RequestSHA256 = digest
	o.MessageObjects = []ledger.ObjectRef{}
	for _, ref := range wire.MessageObjects {
		digest, err := ledger.ParseDigest(ref.SHA256)
		if err != nil || ref.Bytes <= 0 {
			return ErrIntegrity
		}
		o.MessageObjects = append(o.MessageObjects, ledger.ObjectRef{SHA256: digest, Bytes: ref.Bytes})
	}
	return nil
}

// Scan decoded strings, including object keys and escaped JSON, before any hash
// or publication. No secret-derived value is returned, persisted or formatted.
func paidContainsSecret(raw, key []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return false
		}
		if err != nil {
			return true
		}
		if text, ok := token.(string); ok && strings.Contains(text, string(key)) {
			return true
		}
	}
}

func paidObservation(a paidAttempt, evidence providers.ResponseEvidence) paidObservationObject {
	r := evidence.Stream
	o := paidObservationObject{Schema: 1, Kind: "paid_observation", AttemptID: a.admission.AttemptID, Origin: a.origin, ParentTurnID: a.admission.ParentTurnID, RequestSHA256: a.admission.Quote.RequestSHA256, ProviderMessageID: r.MessageID, Model: r.Model, HTTPStatus: evidence.Status, Headers: []paidResponseHeader{}, StopReason: r.StopReason, StopSequence: r.StopSequence, StopDetails: r.StopDetails, RawUsageStart: r.RawUsageStart, RawUsageFinal: r.RawUsageFinal, RawUsageMerged: r.RawUsageMerged, OutputStarted: r.OutputStarted, Complete: r.Complete, MessageObjects: []ledger.ObjectRef{}, UnknownEventTypes: slices.Clone(r.UnknownEventTypes), UnknownEventTypeOverflow: r.UnknownEventTypeOverflow, ContentBlocks: r.ContentBlocks, ContentError: r.ContentError, ProfileViolation: r.ProfileViolation, ErrorClass: evidence.ErrorClass}
	if o.UnknownEventTypes == nil {
		o.UnknownEventTypes = []string{}
	}
	for _, h := range evidence.Headers {
		o.Headers = append(o.Headers, paidResponseHeader{h.Name, slices.Clone(h.Values)})
		if h.Name == "request-id" && len(h.Values) == 1 && paidProviderID(h.Values[0]) {
			o.ProviderRequestID = h.Values[0]
		}
	}
	raw := evidence.ErrorBody
	if len(r.ProviderError) != 0 {
		raw = r.ProviderError
	}
	if len(raw) != 0 {
		var wrapper struct {
			Error     *paidResponseError `json:"error"`
			RequestID string             `json:"request_id"`
		}
		if json.Unmarshal(raw, &wrapper) == nil && wrapper.Error != nil {
			o.Error = wrapper.Error
			o.Error.RequestID = wrapper.RequestID
		}
	}
	if r.ContentError && o.ErrorClass == "" {
		o.ErrorClass = "protocol_error"
	}
	return o
}

func paidProviderID(s string) bool {
	if len(s) == 0 || len(s) > 256 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func paidCaptureFailure(a paidAttempt, started bool, reason string) paidObservationObject {
	o := paidObservation(a, providers.ResponseEvidence{ErrorClass: "protocol_error"})
	o.OutputStarted = started
	o.Error = &paidResponseError{Type: reason, Message: "Provider detail unavailable at the capture boundary."}
	return o
}

// Bound the encoded observation before allocating it, and inspect decoded raw
// JSON (including opaque extensions) rather than a lossy struct serialization.
func paidCaptureAllowed(o paidObservationObject, blocks []providers.OutputBlock, key []byte) bool {
	size := 2048 + 128*len(o.MessageObjects)
	safe := true
	text := func(v string) {
		size += 6*len(v) + 4
		if len(key) != 0 && strings.Contains(v, string(key)) {
			safe = false
		}
	}
	raw := func(v json.RawMessage) {
		size += 6*len(v) + 4
		if len(key) != 0 && len(v) != 0 && paidContainsSecret(v, key) {
			safe = false
		}
	}
	for _, v := range []string{o.ProviderRequestID, o.ProviderMessageID, o.Model, o.StopReason, o.ErrorClass} {
		text(v)
	}
	for _, h := range o.Headers {
		text(h.Name)
		for _, v := range h.Values {
			text(v)
		}
	}
	for _, v := range o.UnknownEventTypes {
		text(v)
	}
	for _, v := range []json.RawMessage{o.StopSequence, o.StopDetails, o.RawUsageStart, o.RawUsageFinal} {
		raw(v)
	}
	for k, v := range o.RawUsageMerged {
		text(k)
		raw(v)
	}
	if o.Error != nil {
		text(o.Error.Type)
		text(o.Error.Message)
		text(o.Error.RequestID)
		raw(o.Error.Details)
	}
	if !safe || size > paidOutputLimit {
		return false
	}
	// Output objects are chunk-bounded separately; these checks cover even
	// fields excluded by OutputBlock's ordinary JSON encoding.
	for _, b := range blocks {
		if len(key) != 0 {
			for _, v := range []string{b.Type, b.Text, b.ID, b.Name, b.Thinking, b.Signature, b.Data} {
				if strings.Contains(v, string(key)) {
					return false
				}
			}
			for _, v := range []json.RawMessage{b.Input, b.OpaquePayload} {
				if len(v) != 0 && paidContainsSecret(v, key) {
					return false
				}
			}
		}
	}
	return true
}

func (s *Service) publishPaidObservation(ctx context.Context, a paidAttempt, evidence providers.ResponseEvidence, key []byte) (*ObservedPaidCall, error) {
	ids := submissionIDs{a.origin.AgentID, a.origin.SessionID, a.origin.ContextID}
	release, err := s.acquireScope(ctx, keyFor(ids, "context"))
	if err != nil {
		return nil, err
	}
	defer release()
	scope, err := s.scopePath(ids, "context")
	if err != nil {
		return nil, err
	}
	o := paidObservation(a, evidence)
	if !paidCaptureAllowed(o, evidence.Stream.Blocks, key) {
		o = paidCaptureFailure(a, o.OutputStarted, "capture_refused")
	} else {
		result := derivePaidResult(a, o)
		if result.Kind == "usage" && !evidence.Stream.ContentError {
			objects, e := s.publishPaidOutputs(scope, a, evidence.Stream.Blocks)
			if e != nil {
				o = paidCaptureFailure(a, o.OutputStarted, "output_capture_failed")
			} else {
				o.MessageObjects = objects
			}
		}
	}
	raw, err := json.Marshal(o)
	if err != nil || len(raw) > paidOutputLimit {
		o = paidCaptureFailure(a, o.OutputStarted, "observation_capture_failed")
		raw, err = json.Marshal(o)
	}
	if err != nil {
		return nil, ErrIntegrity
	}
	if s.deps.beforePublish != nil {
		if err := s.deps.beforePublish(bindingOf(a.origin), "paid_observation"); err != nil {
			return nil, err
		}
	}
	digest, err := scope.publishObject(raw)
	if err != nil {
		return nil, err
	}
	if s.deps.afterPublish != nil {
		s.deps.afterPublish(bindingOf(a.origin), "paid_observation")
	}
	return &ObservedPaidCall{service: s, attempt: a.admission.AttemptID, origin: a.origin, evidence: ledger.ObjectRef{SHA256: digest, Bytes: int64(len(raw))}}, nil
}

func (s *Service) publishPaidOutputs(scope privateScope, a paidAttempt, blocks []providers.OutputBlock) ([]ledger.ObjectRef, error) {
	refs := []ledger.ObjectRef{}
	total := 0
	for index, block := range blocks {
		o := paidOutputObject{Schema: 1, Kind: "paid_output", AttemptID: a.admission.AttemptID, Origin: a.origin, ParentTurnID: a.admission.ParentTurnID, BlockIndex: index}
		remaining := block.Text
		for {
			used := len(remaining)
			switch block.Type {
			case "text":
				// Six bytes per UTF-8 byte is a strict JSON expansion bound. The
				// envelope has at most 1 KiB; split only on a rune boundary.
				used = min(used, (paidChunkLimit-1024)/6)
				for used < len(remaining) && used > 0 && !utf8.RuneStart(remaining[used]) {
					used--
				}
				o.Block = paidTextBlock{"text", remaining[:used]}
			case "tool_use":
				o.Block = paidToolBlock{"local_tool_call", block.ID, block.Name, block.Input}
			case "thinking", "redacted_thinking":
				o.Block = paidOpaqueBlock{"opaque_provider", a.profile.Provider, a.profile.ModelID, block.Type, block.OpaquePayload}
			default:
				return nil, ErrInvalid
			}
			raw, err := json.Marshal(o)
			if err != nil || len(raw) > paidChunkLimit || len(raw) > paidOutputLimit-total {
				return nil, ErrTooLarge
			}
			if s.deps.beforePublish != nil {
				if err := s.deps.beforePublish(bindingOf(a.origin), "paid_output"); err != nil {
					return nil, err
				}
			}
			digest, err := scope.publishObject(raw)
			if err != nil {
				return nil, err
			}
			refs = append(refs, ledger.ObjectRef{SHA256: digest, Bytes: int64(len(raw))})
			total += len(raw)
			if s.deps.afterPublish != nil {
				s.deps.afterPublish(bindingOf(a.origin), "paid_output")
			}
			if block.Type != "text" || used == len(remaining) {
				break
			}
			remaining = remaining[used:]
			o.ChunkIndex++
		}
	}
	return refs, nil
}
