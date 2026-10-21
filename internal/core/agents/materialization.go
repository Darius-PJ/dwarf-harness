package agents

import (
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
