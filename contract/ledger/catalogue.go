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

// Defined returns the record type versions this contract version defines:
// none yet, because F0's record types produced by Core are specified by
// later steps. It never contains a type in the reserved fixture namespace.
func Defined() Catalogue { return Catalogue{} }
