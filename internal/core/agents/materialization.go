package agents

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
)

type materializationState string

const (
	materializationCurrent materializationState = "current"
	materializationStale   materializationState = "stale"
	materializationForeign materializationState = "foreign"
)

type observationKind string

const (
	observationRegular   observationKind = "regular"
	observationMissing   observationKind = "missing"
	observationWrongKind observationKind = "wrong_kind"
)

type materializationObservation struct {
	state  materializationState
	kind   observationKind
	digest ledger.Digest // absent for missing, wrong-kind or unreadable entries
}

// A hint proves only same-slot historical membership, never file health. Its
// checksum covers both the committed occurrence and the binding used as proof.
type materializationHint struct {
	head Head
	sum  [32]byte
}

func materializationChecksum(ids submissionIDs, scope, slot string, h Head) [32]byte {
	key := keyFor(ids, scope)
	data := append([]byte{}, key.agent[:]...)
	data = append(data, ids.session[:]...)
	data = append(data, key.context[:]...)
	data = append(data, scope...)
	data = append(data, 0)
	data = append(data, slot...)
	data = append(data, 0)
	data = append(data, h.SHA256[:]...)
	data = binary.LittleEndian.AppendUint64(data, uint64(h.Bytes))
	data = binary.LittleEndian.AppendUint64(data, h.Revision)
	data = binary.LittleEndian.AppendUint64(data, h.Sequence)
	return sha256.Sum256(data)
}

func (h materializationHint) valid(ids submissionIDs, scope, slot string, revision, through uint64) bool {
	return markdownSlot(slot) && h.head.SHA256 != (ledger.Digest{}) &&
		h.head.Bytes >= 0 && h.head.Bytes <= maxObjectBytes &&
		h.head.Revision > 0 && h.head.Revision <= revision && h.head.Sequence > 0 && h.head.Sequence <= through &&
		h.sum == materializationChecksum(ids, scope, slot, h.head)
}

// materialize replaces only a managed Markdown slot. Its caller supplies the
// committed bytes and serializes writes to this scope. It does not authorize
// overwriting foreign content; that decision belongs to the committed transition.
func (s privateScope) materialize(slot string, data []byte) (err error) {
	if len(data) > maxObjectBytes {
		return ErrTooLarge
	}
	path, err := s.markdownPath(slot)
	if err != nil {
		return err
	}
	if err := s.dirs.checkDirectories(s.components(), true); err != nil {
		return err
	}
	if _, err := regularEntry(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, _, err := writePrivateTemp(s.directory(), data)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, removePrivateTemp(temp)) }()
	return durable.Rename(temp, path)
}

// classifyMaterialization consults only this slot's supplied committed digests.
// Missing Markdown is stale; a prior committed version is recoverably stale,
// not foreign. The head wins if the same digest also occurred earlier. Wrong
// entry kinds are foreign and wrap ErrInvalid, without opening the entry.
func (s privateScope) classifyMaterialization(slot string, head ledger.Digest, earlier []ledger.Digest) (materializationObservation, error) {
	foreign := materializationObservation{state: materializationForeign}
	if head == (ledger.Digest{}) {
		return foreign, fmt.Errorf("%w: absent Markdown head", ErrInvalid)
	}
	path, err := s.markdownPath(slot)
	if err != nil {
		return foreign, err
	}
	if err := s.dirs.checkDirectories(s.components(), false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return materializationObservation{state: materializationStale, kind: observationMissing}, nil
		}
		return foreign, err
	}
	_, err = regularEntry(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return materializationObservation{state: materializationStale, kind: observationMissing}, nil
	case errors.Is(err, ErrInvalid):
		foreign.kind = observationWrongKind
		return foreign, err
	case err != nil:
		return foreign, err
	}
	foreign.kind = observationRegular
	digest, _, err := hashPrivateFile(path)
	if err != nil {
		return foreign, err
	}
	result := materializationObservation{state: materializationForeign, kind: observationRegular, digest: digest}
	switch {
	case digest == head:
		result.state = materializationCurrent
	case slices.Contains(earlier, digest):
		result.state = materializationStale
	}
	return result, nil
}
