package ledger

import (
	"maps"
	"testing"
)

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

	// Defined contains F0, F2-05 and ticket 06's part of F2, proposed for
	// the owner's freeze (D113), but never fixture records or an
	// undefined version.
	def := Defined()
	for _, typ := range []string{TypeCommand, TypeBodyPin, TypeBodyUnpin, TypeBodiesCollected, TypeWorkspaceRegistered, TypeCaptureInputsPinned, TypeScratchCreated, TypeScratchClosed, TypePromotionAdmitted, TypeGenerationDerived, TypePromotionFailed} {
		if !def.Contains(TypeVersion{typ, 1}) || def.Contains(TypeVersion{typ, 2}) {
			t.Errorf("Defined() holds %s at version 1: %v, at version 2: %v; want true, false",
				typ, def.Contains(TypeVersion{typ, 1}), def.Contains(TypeVersion{typ, 2}))
		}
	}
	want := map[TypeVersion]bool{
		{"ledger.command", 1}: true, {"ledger.body_pin", 1}: true,
		{"ledger.body_unpin", 1}: true, {"ledger.bodies_collected", 1}: true,
		{"workspace.registered", 1}: true, {"workspace.capture_inputs_pinned", 1}: true,
		{"workspace.scratch_created", 1}: true, {"workspace.scratch_closed", 1}: true,
		{"workspace.promotion_admitted", 1}: true, {"workspace.generation_derived", 1}: true,
		{"workspace.promotion_failed", 1}: true,
	}
	if !maps.Equal(def.set, want) || def.Contains(TypeVersion{"fixture.sample", 1}) {
		t.Errorf("Defined() holds %v, want %v", def.set, want)
	}
}
