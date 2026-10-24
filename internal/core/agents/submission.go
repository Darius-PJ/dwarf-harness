package agents

import (
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type submissionIDs struct {
	agent, session, context uuid.UUID
}

// privateChange carries supplied bytes only. In particular, neither a command
// ID nor a derived transcript predecessor is an input to canonicalization.
type privateChange struct {
	slot, mode string
	data       []byte
}

// Field order is schema 1's producer rule, not an incidental JSON ordering.
// json.Marshal uses the same escaping and compact encoding as ledger.Encode.
type canonicalSubmission struct {
	Schema          int             `json:"schema"`
	Actor           string          `json:"actor"`
	AgentID         uuid.UUID       `json:"agent_id"`
	SessionID       uuid.UUID       `json:"session_id"`
	ContextID       uuid.UUID       `json:"context_id"`
	Operation       string          `json:"operation"`
	Target          canonicalTarget `json:"target"`
	ExpectedVersion uint64          `json:"expected_version"`
	Arguments       any             `json:"arguments"`
}

type canonicalTarget struct {
	Scope string   `json:"scope"`
	Slots []string `json:"slots"`
}

type registrationArguments struct {
	Directory   string        `json:"directory"`
	AgentSHA256 ledger.Digest `json:"agent_sha256"`
	AgentBytes  int64         `json:"agent_bytes"`
}

type creationArguments struct {
	Kind string `json:"kind"`
}

type writeArguments struct {
	Changes []canonicalChange `json:"changes"`
}

type canonicalChange struct {
	Slot   string        `json:"slot"`
	Mode   string        `json:"mode"`
	SHA256 ledger.Digest `json:"sha256,omitzero"`
	Bytes  *int64        `json:"bytes,omitempty"`
}

func encodeRegistration(ids submissionIDs, directory string, agent []byte) ([]byte, ledger.Digest, error) {
	if err := validateBasename(directory, nil); err != nil {
		return nil, ledger.Digest{}, err
	}
	digest, err := hashObject(agent)
	if err != nil {
		return nil, ledger.Digest{}, err
	}
	return encodeSubmission(ids, "agent_register", canonicalTarget{Scope: "agent", Slots: []string{}}, 0,
		registrationArguments{Directory: directory, AgentSHA256: digest, AgentBytes: int64(len(agent))})
}

func encodeCreation(ids submissionIDs, kind string) ([]byte, ledger.Digest, error) {
	// Version 1 produces only these kinds; the record's token grammar remains
	// open for later producers without changing this submission schema.
	if kind != "general" && kind != "work" {
		return nil, ledger.Digest{}, fmt.Errorf("%w: context kind", ErrInvalid)
	}
	return encodeSubmission(ids, "context_create", canonicalTarget{Scope: "context", Slots: []string{}}, 0, creationArguments{Kind: kind})
}

func encodePrivateWrite(ids submissionIDs, scope string, expected uint64, changes []privateChange) ([]byte, ledger.Digest, error) {
	target, args, err := canonicalPrivateWrite(scope, expected, changes)
	if err != nil {
		return nil, ledger.Digest{}, err
	}
	return encodeSubmission(ids, "private_write", target, expected, args)
}

// Both submission families hash the same borrowed changes before reserving a
// retained byte copy. Only their envelopes differ.
func canonicalPrivateWrite(scope string, expected uint64, changes []privateChange) (canonicalTarget, writeArguments, error) {
	var target canonicalTarget
	var args writeArguments
	if scope != "agent" && scope != "context" || expected == 0 || expected >= math.MaxInt64 {
		return target, args, fmt.Errorf("%w: scope or expected version", ErrInvalid)
	}
	limit := 4
	if scope == "agent" {
		limit = 2
	}
	if len(changes) == 0 || len(changes) > limit {
		return target, args, fmt.Errorf("%w: change count", ErrInvalid)
	}
	ordered := slices.Clone(changes)
	slices.SortFunc(ordered, func(a, b privateChange) int { return cmp.Compare(a.slot, b.slot) })
	args.Changes = make([]canonicalChange, 0, len(ordered))
	target = canonicalTarget{Scope: scope, Slots: make([]string, 0, len(ordered))}
	total := 0
	for i, change := range ordered {
		if i > 0 && change.slot == ordered[i-1].slot {
			return target, args, fmt.Errorf("%w: duplicate slot", ErrInvalid)
		}
		if err := validateChange(scope, change); err != nil {
			return target, args, err
		}
		if len(change.data) > maxObjectBytes || len(change.data) > maxOperationBytes-total {
			return target, args, ErrTooLarge
		}
		total += len(change.data)
		entry := canonicalChange{Slot: change.slot, Mode: change.mode}
		if change.mode != "clear" {
			digest, err := hashObject(change.data)
			if err != nil {
				return target, args, err
			}
			n := int64(len(change.data))
			entry.SHA256, entry.Bytes = digest, &n
		}
		args.Changes = append(args.Changes, entry)
		target.Slots = append(target.Slots, change.slot)
	}
	return target, args, nil
}

func validateChange(scope string, change privateChange) error {
	valid := false
	switch change.slot {
	case "instructions":
		valid = scope == "agent" && change.mode == "replace"
	case "memory":
		valid = change.mode == "replace"
	case "handoff":
		valid = scope == "context" && change.mode == "replace"
	case "transcript":
		valid = scope == "context" && change.mode == "append" && len(change.data) > 0
	case "continuation":
		valid = scope == "context" && (change.mode == "clear" && len(change.data) == 0 || change.mode == "replace" && len(change.data) > 0)
	}
	if !valid {
		return fmt.Errorf("%w: slot, mode or bytes", ErrInvalid)
	}
	return nil
}

func encodeSubmission(ids submissionIDs, operation string, target canonicalTarget, expected uint64, arguments any) ([]byte, ledger.Digest, error) {
	if !validID(ids.agent) || !validID(ids.session) || !validID(ids.context) {
		return nil, ledger.Digest{}, fmt.Errorf("%w: submission identity is not UUIDv7", ErrInvalid)
	}
	data, err := json.Marshal(canonicalSubmission{
		Schema: 1, Actor: "owner", AgentID: ids.agent, SessionID: ids.session, ContextID: ids.context,
		Operation: operation, Target: target, ExpectedVersion: expected, Arguments: arguments,
	})
	if err != nil {
		return nil, ledger.Digest{}, err
	}
	return data, ledger.Digest(sha256.Sum256(data)), nil
}
