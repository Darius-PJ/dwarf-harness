package main

import (
	"image/color"
	"slices"
	"strings"
	"testing"
)

// firstChoice returns the first color choice of a mask.
func firstChoice(t *testing.T, s *artSet, mask string) string {
	t.Helper()
	for _, c := range s.choices {
		if c.mask == mask {
			return c.id
		}
	}
	t.Fatalf("no %s choice", mask)
	return ""
}

// testOutfit returns an outfit of the given parts, wearing the first skin
// and hair choices and the given clothing choice.
func testOutfit(t *testing.T, s *artSet, clothing string, ids ...string) *outfit {
	t.Helper()
	o := &outfit{name: "test", skin: firstChoice(t, s, "skin"), hair: firstChoice(t, s, "hair"), clothing: clothing}
	for _, id := range ids {
		slot, _, _ := strings.Cut(id, ".")
		o.parts = append(o.parts, outfitPart{id: id, slot: slot})
	}
	return o
}

func TestHatOccludesHair(t *testing.T) {
	s := loaded(t)
	clothing := firstChoice(t, s, "clothing")
	hatted := testOutfit(t, s, clothing, "body.stout", "hair_beard.topknot", "headwear.hat")
	bare := testOutfit(t, s, clothing, "body.stout", "hair_beard.topknot")
	hatNoHair := testOutfit(t, s, clothing, "body.stout", "headwear.hat")
	noHair := testOutfit(t, s, clothing, "body.stout")
	for _, grid := range []string{gridWorld, gridRPG} {
		hat := s.sprites[grid]["headwear.hat"]
		for f := range s.idleFrames(grid) {
			i, _ := hat.frame("idle", f)
			withHat, withoutHat := s.compose(grid, f, hatted, ""), s.compose(grid, f, bare, "")
			hatOnly, bodyOnly := s.compose(grid, f, hatNoHair, ""), s.compose(grid, f, noHair, "")
			reaches, beard := false, false
			for p, idx := range hat.px[i] {
				x, y := p%hat.w, p/hat.w
				if int(idx) == s.pal.occlude {
					reaches = reaches || withoutHat.NRGBAAt(x, y) != bodyOnly.NRGBAAt(x, y)
					if withHat.NRGBAAt(x, y) != hatOnly.NRGBAAt(x, y) {
						t.Errorf("%s idle %d: hair shows at the hat's occlude pixel (%d,%d)", grid, f, x, y)
					}
				} else {
					beard = beard || withHat.NRGBAAt(x, y) != hatOnly.NRGBAAt(x, y)
				}
			}
			if !reaches {
				t.Errorf("%s idle %d: the topknot reaches none of the hat's occlude pixels, so the test proves nothing", grid, f)
			}
			if !beard {
				t.Errorf("%s idle %d: with the hat, no hair shows outside the occlude pixels; the beard is gone", grid, f)
			}
		}
	}
}

func TestRecolorChangesOnlyMaskedPixels(t *testing.T) {
	s := loaded(t)
	red := s.choice["clothing.red"]
	if red == nil || s.choice["clothing.blue"] == nil {
		t.Fatal("colors.txt lacks clothing.blue or clothing.red")
	}
	ids := []string{"body.stout", "legwear.trousers", "torso_clothing.tunic"}
	ramp := s.pal.ramps["clothing"]
	for _, grid := range []string{gridWorld, gridRPG} {
		for f := range s.idleFrames(grid) {
			blue := s.compose(grid, f, testOutfit(t, s, "clothing.blue", ids...), "")
			reds := s.compose(grid, f, testOutfit(t, s, "clothing.red", ids...), "")
			// top holds the palette index of the topmost visible layer.
			top := make([]int, blue.Rect.Dx()*blue.Rect.Dy())
			for i := range top {
				top[i] = -1
			}
			for _, id := range ids {
				m := s.sprites[grid][id]
				i, _ := m.frame("idle", f)
				for p, idx := range m.px[i] {
					if !s.pal.clear(idx) {
						top[p] = int(idx)
					}
				}
			}
			for p, idx := range top {
				x, y := p%blue.Rect.Dx(), p/blue.Rect.Dx()
				k := slices.Index(ramp, idx)
				differs := blue.NRGBAAt(x, y) != reds.NRGBAAt(x, y)
				if differs != (k >= 0) {
					t.Errorf("%s idle %d (%d,%d): blue and red differ: %v; the top layer's index %d is in the clothing mask: %v", grid, f, x, y, differs, idx, k >= 0)
					continue
				}
				if k >= 0 && reds.NRGBAAt(x, y) != red.rgb[k] {
					t.Errorf("%s idle %d (%d,%d) = %v; want red ramp step %d, %v", grid, f, x, y, reds.NRGBAAt(x, y), k, red.rgb[k])
				}
			}
		}
	}
}

func TestRoleMarkersReadable(t *testing.T) {
	s := loaded(t)
	world := s.sprites[gridWorld]
	for i, a := range roles {
		for _, b := range roles[i+1:] {
			if slices.Equal(world["badge."+a].px[0], world["badge."+b].px[0]) {
				t.Errorf("badge.%s and badge.%s have the same frame 0", a, b)
			}
		}
	}

	trim := world["overlay.trim"]
	roleRamp := s.pal.ramps["role"]
	for _, o := range s.outfits {
		for _, role := range roles {
			choice := s.choice["role."+role]
			badge := world["badge."+role]
			for f := range s.idleFrames(gridWorld) {
				img := s.compose(gridWorld, f, o, role)
				ti, _ := trim.frame("idle", f)
				bi, _ := badge.frame("idle", f)
				for p := range trim.px[ti] {
					var want color.NRGBA
					switch b, tr := badge.px[bi][p], trim.px[ti][p]; {
					case !s.pal.clear(b):
						want = s.pal.entries[b].color
					case !s.pal.clear(tr):
						want = choice.rgb[slices.Index(roleRamp, int(tr))]
					default:
						continue
					}
					if got := img.NRGBAAt(p%trim.w, p/trim.w); got != want {
						t.Errorf("%s as %s, idle %d (%d,%d) = %v; want the role marker's %v", o.name, role, f, p%trim.w, p/trim.w, got, want)
					}
				}
			}
		}
	}
}
