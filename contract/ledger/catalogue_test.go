package ledger

import "testing"

func TestNewCatalogue(t *testing.T) {
	for _, tc := range []struct {
		name string
		tvs  []TypeVersion
	}{
		{"type without a namespace", []TypeVersion{{"sample", 1}}},
		{"version below 1", []TypeVersion{{"fixture.sample", 0}}},
		{"repeated entry", []TypeVersion{{"fixture.sample", 1}, {"fixture.other", 1}, {"fixture.sample", 1}}},
	} {
		if _, err := NewCatalogue(tc.tvs...); err == nil {
			t.Errorf("NewCatalogue with a %s succeeded", tc.name)
		}
	}

	cat, err := NewCatalogue(TypeVersion{"fixture.sample", 1}, TypeVersion{"fixture.sample", 2})
	if err != nil {
		t.Fatalf("NewCatalogue = %v", err)
	}
	for tv, want := range map[TypeVersion]bool{
		{"fixture.sample", 1}: true,
		{"fixture.sample", 2}: true,
		{"fixture.sample", 3}: false,
		{"fixture.other", 1}:  false,
	} {
		if got := cat.Contains(tv); got != want {
			t.Errorf("Contains(%v) = %v, want %v", tv, got, want)
		}
	}

	// Defined is F0's catalogue, exactly the four F0 types at version 1, so
	// that a production writer can write F0 records and refuses fixture
	// records.
	def := Defined()
	for _, typ := range []string{TypeCommand, TypeBodyPin, TypeBodyUnpin, TypeBodiesCollected} {
		if !def.Contains(TypeVersion{typ, 1}) || def.Contains(TypeVersion{typ, 2}) {
			t.Errorf("Defined() holds %s at version 1: %v, at version 2: %v; want true, false",
				typ, def.Contains(TypeVersion{typ, 1}), def.Contains(TypeVersion{typ, 2}))
		}
	}
	if len(def.set) != 4 || def.Contains(TypeVersion{"fixture.sample", 1}) {
		t.Errorf("Defined() holds %v, want only the four F0 type versions", def.set)
	}
}
