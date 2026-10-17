package ledger

import "fmt"

// TypeVersion names one version of a record type.
type TypeVersion struct {
	Type    string
	Version int
}

// Catalogue is a set of record type versions that a writer may append.
type Catalogue struct {
	set map[TypeVersion]bool
}

// NewCatalogue returns the catalogue of tvs. Every type must match the type
// grammar, every version must be at least 1, and no entry may repeat.
func NewCatalogue(tvs ...TypeVersion) (Catalogue, error) {
	set := make(map[TypeVersion]bool, len(tvs))
	for _, tv := range tvs {
		switch {
		case !validType(tv.Type):
			return Catalogue{}, fmt.Errorf("ledger: catalogue type %q does not match the type grammar", tv.Type)
		case tv.Version < 1:
			return Catalogue{}, fmt.Errorf("ledger: catalogue type %s version %d is below 1", tv.Type, tv.Version)
		case set[tv]:
			return Catalogue{}, fmt.Errorf("ledger: catalogue repeats type %s version %d", tv.Type, tv.Version)
		}
		set[tv] = true
	}
	return Catalogue{set: set}, nil
}

// Contains reports whether tv is in the catalogue.
func (c Catalogue) Contains(tv TypeVersion) bool { return c.set[tv] }

// Defined returns the record type versions this contract version defines,
// F0's catalogue: TypeCommand, TypeBodyPin, TypeBodyUnpin and
// TypeBodiesCollected; and the F2-05 additions:
// TypeWorkspaceRegistered, TypeCaptureInputsPinned, TypeScratchCreated and
// TypeScratchClosed; and ticket 06's part of F2, frozen (D118):
// TypePromotionAdmitted, TypeGenerationDerived and
// TypePromotionFailed; and ticket 07's part of F2, frozen (D119):
// TypeScratchChanged and TypeScratchMergeBack; and ticket 08's part of F2,
// frozen (D123): TypeFinalizationIntended,
// TypePromotionAccepted and TypePromotionNoChange; and ticket 09's part of F2,
// frozen (D124): TypePolicyActivationRequested,
// TypePolicyActivationHoldChanged, TypePolicyActivationEnded and
// TypePolicyActivated; and F-U3-61, ticket 61's part and U3's first producer
// family, frozen (D130): TypeGraphAnalysisStarted, TypeGraphAnalysisProgress,
// TypeGraphDeltaChunk and TypeGraphVersionCommitted. All twenty-four are at
// version 1. It never contains a type in the reserved fixture namespace.
func Defined() Catalogue {
	return Catalogue{set: map[TypeVersion]bool{
		{TypeCommand, 1}:                     true,
		{TypeBodyPin, 1}:                     true,
		{TypeBodyUnpin, 1}:                   true,
		{TypeBodiesCollected, 1}:             true,
		{TypeWorkspaceRegistered, 1}:         true,
		{TypeCaptureInputsPinned, 1}:         true,
		{TypeScratchCreated, 1}:              true,
		{TypeScratchClosed, 1}:               true,
		{TypePromotionAdmitted, 1}:           true,
		{TypeGenerationDerived, 1}:           true,
		{TypePromotionFailed, 1}:             true,
		{TypeScratchChanged, 1}:              true,
		{TypeScratchMergeBack, 1}:            true,
		{TypeFinalizationIntended, 1}:        true,
		{TypePromotionAccepted, 1}:           true,
		{TypePromotionNoChange, 1}:           true,
		{TypePolicyActivationRequested, 1}:   true,
		{TypePolicyActivationHoldChanged, 1}: true,
		{TypePolicyActivationEnded, 1}:       true,
		{TypePolicyActivated, 1}:             true,
		{TypeGraphAnalysisStarted, 1}:        true,
		{TypeGraphAnalysisProgress, 1}:       true,
		{TypeGraphDeltaChunk, 1}:             true,
		{TypeGraphVersionCommitted, 1}:       true,
	}}
}
