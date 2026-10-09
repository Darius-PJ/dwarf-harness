package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
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

// eachFrame calls f for every animation and frame that the layers of view v
// have.
func eachFrame(s *artSet, v string, f func(an string, k int)) {
	for _, a := range s.layerAnims(v) {
		for k := range a.frames {
			f(a.name, k)
		}
	}
}

func TestHatOccludesHair(t *testing.T) {
	s := loaded(t)
	clothing := firstChoice(t, s, "clothing")
	hatted := testOutfit(t, s, clothing, "body.stout", "hair_beard.topknot", "headwear.hat")
	bare := testOutfit(t, s, clothing, "body.stout", "hair_beard.topknot")
	hatNoHair := testOutfit(t, s, clothing, "body.stout", "headwear.hat")
	noHair := testOutfit(t, s, clothing, "body.stout")
	for _, v := range views {
		hat := s.sprites[v.name]["headwear.hat"]
		eachFrame(s, v.name, func(an string, k int) {
			withHat, withoutHat := s.compose(v.name, an, k, hatted, "", nil), s.compose(v.name, an, k, bare, "", nil)
			hatOnly, bodyOnly := s.compose(v.name, an, k, hatNoHair, "", nil), s.compose(v.name, an, k, noHair, "", nil)
			reaches, beard := false, false
			for p, idx := range frameOf(hat, an, k) {
				x, y := p%hat.w, p/hat.w
				if int(idx) == s.pal.occlude {
					reaches = reaches || withoutHat.NRGBAAt(x, y) != bodyOnly.NRGBAAt(x, y)
					if withHat.NRGBAAt(x, y) != hatOnly.NRGBAAt(x, y) {
						t.Errorf("%s %s %d: hair shows at the hat's occlude pixel (%d,%d)", v.name, an, k, x, y)
					}
				} else {
					beard = beard || withHat.NRGBAAt(x, y) != hatOnly.NRGBAAt(x, y)
				}
			}
			if !reaches {
				t.Errorf("%s %s %d: the topknot reaches none of the hat's occlude pixels, so the test proves nothing", v.name, an, k)
			}
			if !beard {
				t.Errorf("%s %s %d: with the hat, no hair shows outside the occlude pixels; the beard is gone", v.name, an, k)
			}
		})
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
	for _, v := range views {
		eachFrame(s, v.name, func(an string, k int) {
			blue := s.compose(v.name, an, k, testOutfit(t, s, "clothing.blue", ids...), "", nil)
			reds := s.compose(v.name, an, k, testOutfit(t, s, "clothing.red", ids...), "", nil)
			// top holds the palette index of the topmost visible layer.
			top := make([]int, blue.Rect.Dx()*blue.Rect.Dy())
			for i := range top {
				top[i] = -1
			}
			for _, id := range ids {
				for p, idx := range frameOf(s.sprites[v.name][id], an, k) {
					if !s.pal.clear(idx) {
						top[p] = int(idx)
					}
				}
			}
			for p, idx := range top {
				x, y := p%blue.Rect.Dx(), p/blue.Rect.Dx()
				i := slices.Index(ramp, idx)
				differs := blue.NRGBAAt(x, y) != reds.NRGBAAt(x, y)
				if differs != (i >= 0) {
					t.Errorf("%s %s %d (%d,%d): blue and red differ: %v; the top layer's index %d is in the clothing mask: %v", v.name, an, k, x, y, differs, idx, i >= 0)
					continue
				}
				if i >= 0 && reds.NRGBAAt(x, y) != red.rgb[i] {
					t.Errorf("%s %s %d (%d,%d) = %v; want red ramp step %d, %v", v.name, an, k, x, y, reds.NRGBAAt(x, y), i, red.rgb[i])
				}
			}
		})
	}

	// The Corps colour recolours exactly the pennant's field.
	slate := s.choice["corps.slate"]
	if slate == nil || s.choice["corps.crimson"] == nil {
		t.Fatal("colors.txt lacks corps.crimson or corps.slate")
	}
	corpsRamp := s.pal.ramps["corps"]
	for _, v := range rpgViews() {
		banner := s.sprites[v.name][bannerID]
		for _, id := range s.ids(v.name) {
			emblem := s.sprites[v.name][id]
			if emblem.kind != kindEmblem {
				continue
			}
			crimsonCorps := &corpsExample{name: "a", color: "corps.crimson", emblem: id}
			slateCorps := &corpsExample{name: "b", color: "corps.slate", emblem: id}
			eachFrame(s, v.name, func(an string, k int) {
				a, b := s.marker(v.name, an, k, crimsonCorps), s.marker(v.name, an, k, slateCorps)
				field := 0
				ep := frameOf(emblem, an, k)
				for p, bi := range frameOf(banner, an, k) {
					x, y := p%banner.w, p/banner.w
					i := slices.Index(corpsRamp, int(bi))
					want := i >= 0 && s.pal.clear(ep[p])
					if differs := a.NRGBAAt(x, y) != b.NRGBAAt(x, y); differs != want {
						t.Errorf("%s %s %s %d (%d,%d): crimson and slate differ: %v; want %v", v.name, id, an, k, x, y, differs, want)
						continue
					}
					if want {
						field++
						if b.NRGBAAt(x, y) != slate.rgb[i] {
							t.Errorf("%s %s %s %d (%d,%d) = %v; want slate ramp step %d, %v", v.name, id, an, k, x, y, b.NRGBAAt(x, y), i, slate.rgb[i])
						}
					}
				}
				if field == 0 {
					t.Errorf("%s %s %d: the pennant has no Corps field", v.name, an, k)
				}
			})
		}
	}
}

func TestRoleMarkersReadable(t *testing.T) {
	s := loaded(t)
	world := s.sprites[viewOverworld]
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
			for f := range s.idleFrames() {
				img := s.compose(viewOverworld, "idle", f, o, role, nil)
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

func TestEachLayerDrawsItsOwnFrame(t *testing.T) {
	out := outputs(t)
	doc := sampleOf(t, out)
	s := loaded(t)
	colorsOf := func(o *outfit) []color.NRGBA {
		return s.paint(s.choice[o.skin], s.choice[o.hair], s.choice[o.clothing])
	}
	checked := map[string]bool{}
	for _, sp := range doc.Sprites {
		if sp.Kind != kindPart {
			continue
		}
		o := testOutfit(t, s, "clothing.blue", sp.ID)
		colors := colorsOf(o)
		for _, im := range sp.Images {
			decoded, err := png.Decode(bytes.NewReader(out[im.Path]))
			if err != nil {
				t.Fatalf("%s: %v", im.Path, err)
			}
			strip := decoded.(*image.Paletted)
			for _, a := range im.Animations {
				for k, r := range a.Frames {
					checked[im.View+" "+a.Name] = true
					got := s.compose(im.View, a.Name, k, o, "", nil)
				pixels:
					for y := range r[3] {
						for x := range r[2] {
							want := colors[strip.ColorIndexAt(r[0]+x, r[1]+y)]
							if want.A == 0 {
								want = color.NRGBA{}
							}
							if got.NRGBAAt(x, y) != want {
								t.Errorf("%s alone, %s %s %d (%d,%d) = %v; its strip frame has %v", sp.ID, im.View, a.Name, k, x, y, got.NRGBAAt(x, y), want)
								break pixels
							}
						}
					}
				}
			}
		}
	}
	for _, v := range views {
		for _, a := range s.layerAnims(v.name) {
			if !checked[v.name+" "+a.name] {
				t.Errorf("no part drawn in %s %s", v.name, a.name)
			}
		}
	}
}

func TestRoleToolOnlyDuringItsAction(t *testing.T) {
	s := loaded(t)
	tools := 0
	for _, m := range s.roster.members {
		o, c := s.outfit[m.outfit], s.roster.corpsNamed(m.corps)
		for _, role := range roles {
			front, frontless := s.compose(viewFront, "still", 0, o, role, c), s.compose(viewFront, "still", 0, o, "", c)
			if !bytes.Equal(front.Pix, frontless.Pix) {
				t.Errorf("%s front still as %s differs from the role-less still", m.outfit, role)
			}
			tool := s.sprites[viewThreeQuarter]["tool."+role]
			eachFrame(s, viewThreeQuarter, func(an string, k int) {
				with, without := s.compose(viewThreeQuarter, an, k, o, role, c), s.compose(viewThreeQuarter, an, k, o, "", c)
				if an != "act_"+role || tool == nil {
					if !bytes.Equal(with.Pix, without.Pix) {
						t.Errorf("%s as %s, %s %d differs from the role-less composite; the role draws only its own tool in its own action", m.outfit, role, an, k)
					}
					return
				}
				tools++
				colors := s.paint()
				tp := frameOf(tool, an, k)
				for p := range tp {
					x, y := p%tool.w, p/tool.w
					want := without.NRGBAAt(x, y)
					if tc := colors[tp[p]]; tc.A == 0xff {
						want = tc
					}
					if with.NRGBAAt(x, y) != want {
						t.Errorf("%s as %s, %s %d (%d,%d) = %v; want %v", m.outfit, role, an, k, x, y, with.NRGBAAt(x, y), want)
						return
					}
				}
			})
		}
	}
	if tools == 0 {
		t.Fatal("no member plays an action that has a tool")
	}
}

func TestCorpsMarkerDrawnOverSlots(t *testing.T) {
	s := loaded(t)
	defaults := s.paint()
	for _, c := range s.roster.corps {
		corps := s.paint(s.choice[c.color])
		for _, o := range s.outfits {
			for _, v := range rpgViews() {
				banner, emblem := s.sprites[v.name][bannerID], s.sprites[v.name][c.emblem]
				eachFrame(s, v.name, func(an string, k int) {
					with, without := s.compose(v.name, an, k, o, "", c), s.compose(v.name, an, k, o, "", nil)
					bp, ep := frameOf(banner, an, k), frameOf(emblem, an, k)
					for p := range bp {
						x, y := p%banner.w, p/banner.w
						want := without.NRGBAAt(x, y)
						switch {
						case defaults[ep[p]].A == 0xff:
							want = defaults[ep[p]]
						case corps[bp[p]].A == 0xff:
							want = corps[bp[p]]
						}
						if with.NRGBAAt(x, y) != want {
							t.Errorf("%s with Corps %s, %s %s %d (%d,%d) = %v; want %v", o.name, c.name, v.name, an, k, x, y, with.NRGBAAt(x, y), want)
							return
						}
					}
				})
			}
		}
	}
}
