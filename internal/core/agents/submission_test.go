package agents

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func testSubmissionIDs(t *testing.T) submissionIDs {
	t.Helper()
	var ids [3]uuid.UUID
	for i, s := range []string{"019a0700-0000-7000-8000-000000000001", "019a0700-0000-7000-8000-000000000002", testContext} {
		id, err := uuid.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	return submissionIDs{agent: ids[0], session: ids[1], context: ids[2]}
}

func TestCanonicalSubmissionVectors(t *testing.T) {
	t.Parallel()
	ids := testSubmissionIDs(t)
	for _, row := range []struct {
		name         string
		encode       func() ([]byte, ledger.Digest, error)
		json, digest string
	}{
		{"registration", func() ([]byte, ledger.Digest, error) { return encodeRegistration(ids, "Scout-a1", []byte("<&>\r\n")) },
			`{"schema":1,"actor":"owner","agent_id":"019a0700-0000-7000-8000-000000000001","session_id":"019a0700-0000-7000-8000-000000000002","context_id":"019a0700-0000-7000-8000-000000000003","operation":"agent_register","target":{"scope":"agent","slots":[]},"expected_version":0,"arguments":{"directory":"Scout-a1","agent_sha256":"2d05d9b164cbf995b5e80e318f9c4567cbeaa0ab876b24caf58f6b1d05bf398a","agent_bytes":5}}`,
			"9b10d489910d2b8c597a77d0b9b1c3bc60ac84f5cb51fbf587c86a39652ad7de"},
		{"creation", func() ([]byte, ledger.Digest, error) { return encodeCreation(ids, "work") },
			`{"schema":1,"actor":"owner","agent_id":"019a0700-0000-7000-8000-000000000001","session_id":"019a0700-0000-7000-8000-000000000002","context_id":"019a0700-0000-7000-8000-000000000003","operation":"context_create","target":{"scope":"context","slots":[]},"expected_version":0,"arguments":{"kind":"work"}}`,
			"b7e36c54be2c4b45cbda02f9e5725b33934697899c9706099d053e5d15d36cdd"},
		{"write", func() ([]byte, ledger.Digest, error) {
			return encodePrivateWrite(ids, "context", 7, []privateChange{
				{slot: "transcript", mode: "append", data: []byte{0, 255}},
				{slot: "memory", mode: "replace", data: nil},
				{slot: "continuation", mode: "clear"},
				{slot: "handoff", mode: "replace", data: []byte("<&>\n")},
			})
		},
			`{"schema":1,"actor":"owner","agent_id":"019a0700-0000-7000-8000-000000000001","session_id":"019a0700-0000-7000-8000-000000000002","context_id":"019a0700-0000-7000-8000-000000000003","operation":"private_write","target":{"scope":"context","slots":["continuation","handoff","memory","transcript"]},"expected_version":7,"arguments":{"changes":[{"slot":"continuation","mode":"clear"},{"slot":"handoff","mode":"replace","sha256":"23860470de2a0fb2d8fc9d2ee605cb2127df0b923a223a704d7aa79989ad29d4","bytes":4},{"slot":"memory","mode":"replace","sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","bytes":0},{"slot":"transcript","mode":"append","sha256":"06eb7d6a69ee19e5fbdf749018d3d2abfa04bcbd1365db312eb86dc7169389b8","bytes":2}]}}`,
			"89481b8220348c0c256e92bd0487b2ebcba3d2b5886dc760c0b526a7914a413f"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got, digest, err := row.encode()
			if err != nil || string(got) != row.json || digest.String() != row.digest {
				t.Fatalf("canonical submission = %s\ndigest = %s, error = %v\nwant %s\n%s", got, digest, err, row.json, row.digest)
			}
		})
	}
}

func TestCanonicalSubmissionSortsWithoutChangingCaller(t *testing.T) {
	t.Parallel()
	ids := testSubmissionIDs(t)
	changes := []privateChange{{slot: "memory", mode: "replace", data: []byte("memory")}, {slot: "handoff", mode: "replace", data: []byte("handoff")}}
	before := slices.Clone(changes)
	a, _, err := encodePrivateWrite(ids, "context", 1, changes)
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].slot != before[0].slot || changes[1].slot != before[1].slot || string(changes[0].data) != "memory" || string(changes[1].data) != "handoff" {
		t.Fatal("canonicalization mutated caller input")
	}
	slices.Reverse(changes)
	b, _, err := encodePrivateWrite(ids, "context", 1, changes)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("input order changed submission: %v", err)
	}
}

func TestCanonicalWriteRefusesInvalidChanges(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, scope string
		expected    uint64
		changes     []privateChange
	}{
		{"empty", "context", 1, nil},
		{"duplicate", "context", 1, []privateChange{{slot: "memory", mode: "replace"}, {slot: "memory", mode: "replace"}}},
		{"scope", "colony", 1, []privateChange{{slot: "memory", mode: "replace"}}},
		{"agent_handoff", "agent", 1, []privateChange{{slot: "handoff", mode: "replace"}}},
		{"context_instructions", "context", 1, []privateChange{{slot: "instructions", mode: "replace"}}},
		{"memory_clear", "context", 1, []privateChange{{slot: "memory", mode: "clear"}}},
		{"transcript_replace", "context", 1, []privateChange{{slot: "transcript", mode: "replace", data: []byte("x")}}},
		{"empty_transcript", "context", 1, []privateChange{{slot: "transcript", mode: "append"}}},
		{"empty_continuation", "context", 1, []privateChange{{slot: "continuation", mode: "replace"}}},
		{"clear_bytes", "context", 1, []privateChange{{slot: "continuation", mode: "clear", data: []byte("x")}}},
		{"zero_revision", "context", 0, []privateChange{{slot: "memory", mode: "replace"}}},
		{"overflow_revision", "context", math.MaxInt64, []privateChange{{slot: "memory", mode: "replace"}}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := encodePrivateWrite(testSubmissionIDs(t), row.scope, row.expected, row.changes)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid change accepted: %v", err)
			}
		})
	}
}

func TestCanonicalSubmissionBounds(t *testing.T) {
	t.Parallel()
	ids := testSubmissionIDs(t)
	if _, _, err := encodeRegistration(ids, "Scout-a1", make([]byte, maxObjectBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize instructions: %v", err)
	}
	if _, _, err := encodePrivateWrite(ids, "agent", 1, []privateChange{{slot: "memory", mode: "replace", data: make([]byte, maxObjectBytes+1)}}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize change: %v", err)
	}
	data := bytes.Repeat([]byte{255}, maxObjectBytes)
	changes := []privateChange{{slot: "continuation", mode: "replace", data: data}, {slot: "handoff", mode: "replace", data: data}, {slot: "memory", mode: "replace", data: data}, {slot: "transcript", mode: "append", data: data}}
	if _, _, err := encodePrivateWrite(ids, "context", math.MaxInt64-1, changes); err != nil {
		t.Fatalf("4 MiB operation at last valid expectation refused: %v", err)
	}
	for _, bad := range []submissionIDs{{}, {agent: ids.agent, session: ids.session}} {
		if _, _, err := encodeCreation(bad, "work"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("missing identity accepted: %v", err)
		}
	}
}
