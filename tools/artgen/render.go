package main

import (
	"image"
	"image/color"
	"slices"
)

// pick returns the palette index of texture t at canvas pixel (x, y) of a
// tile with the given seed: a splitmix64 hash of (seed, x, y) chooses among
// the weighted entries (§3.13, P14).
func (t *texture) pick(seed uint32, x, y int) int {
	z := uint64(seed)<<32 | uint64(y)<<16 | uint64(x)
	z += 0x9E3779B97F4A7C15
	z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
	z = (z ^ z>>27) * 0x94D049BB133111EB
	z ^= z >> 31
	r := z % t.total
	for i, w := range t.weights {
		if r < w {
			return t.entries[i]
		}
		r -= w
	}
	return t.entries[len(t.entries)-1]
}

// resolve turns the checked rows into palette indices.
func (m *pixmap) resolve(pal *palette) {
	m.px = make([][]uint8, len(m.rows))
	for f, rows := range m.rows {
		px := make([]uint8, m.w*m.h)
		for y, row := range rows {
			for x := range m.w {
				c := row[x]
				if t, ok := pal.texOf[c]; ok {
					px[y*m.w+x] = uint8(t.pick(m.seed, x, y))
				} else {
					px[y*m.w+x] = uint8(pal.glyphs[c])
				}
			}
		}
		m.px[f] = px
	}
}

// paint returns the global palette with the ramp of each given choice's
// mask replaced by the choice's colors, shadow to light (§3.13). A nil
// choice leaves its mask at the palette's default colors.
func (s *artSet) paint(choices ...*choice) []color.NRGBA {
	p := make([]color.NRGBA, len(s.pal.entries))
	for i, e := range s.pal.entries {
		p[i] = e.color
	}
	for _, c := range choices {
		if c == nil {
			continue
		}
		for k, i := range s.pal.ramps[c.mask] {
			p[i] = c.rgb[k]
		}
	}
	return p
}

// strip returns the sprite's frames left to right as one indexed image that
// holds the whole global palette.
func (s *artSet) strip(m *pixmap) *image.Paletted {
	pal := make(color.Palette, len(s.pal.entries))
	for i, e := range s.pal.entries {
		pal[i] = e.color
	}
	img := image.NewPaletted(image.Rect(0, 0, m.w*len(m.px), m.h), pal)
	for f, px := range m.px {
		for y := range m.h {
			copy(img.Pix[y*img.Stride+f*m.w:], px[y*m.w:(y+1)*m.w])
		}
	}
	return img
}

// frameImage returns frame f of m in the given colors; see-through entries
// stay transparent.
func frameImage(m *pixmap, f int, colors []color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, m.w, m.h))
	draw(img, m.px[f], colors, nil)
	return img
}

// draw sets every pixel of dst whose index has an opaque color and is not
// hidden. px covers dst exactly.
func draw(dst *image.NRGBA, px []uint8, colors []color.NRGBA, hidden []bool) {
	w := dst.Rect.Dx()
	for i, idx := range px {
		c := colors[idx]
		if c.A == 0 || hidden != nil && hidden[i] {
			continue
		}
		dst.SetNRGBA(i%w, i/w, c)
	}
}

// compose draws a dwarf in view v at frame k of animation an (§3.10 of the
// round-2 brief): the outfit's parts in slot order, each in its colors and
// without the pixels that an occluding part hides; then on the overworld the
// role's trim and badge when role is set; then in an RPG view the Corps
// marker, the pennant in c's colour and c's emblem, when c is set; then in
// the three-quarter view the role's tool while the role's own action plays.
func (s *artSet) compose(v, an string, k int, o *outfit, role string, c *corpsExample) *image.NRGBA {
	vw := viewOf(v)
	sprites := s.sprites[v]
	img := image.NewNRGBA(image.Rect(0, 0, vw.w, vw.h))
	parts := slices.Clone(o.parts)
	slices.SortFunc(parts, func(a, b outfitPart) int { return slices.Index(slots, a.slot) - slices.Index(slots, b.slot) })
	hidden := map[string][]bool{}
	for _, part := range parts {
		m := sprites[part.id]
		for _, slot := range m.occludes {
			h := hidden[slot]
			if h == nil {
				h = make([]bool, vw.w*vw.h)
				hidden[slot] = h
			}
			for i, idx := range frameOf(m, an, k) {
				if int(idx) == s.pal.occlude {
					h[i] = true
				}
			}
		}
	}
	for _, part := range parts {
		clothing := o.clothing
		if part.clothing != "" {
			clothing = part.clothing
		}
		colors := s.paint(s.choice[o.skin], s.choice[o.hair], s.choice[clothing])
		draw(img, frameOf(sprites[part.id], an, k), colors, hidden[part.slot])
	}
	if vw.grid == gridWorld && role != "" {
		draw(img, frameOf(sprites["overlay.trim"], an, k), s.paint(s.choice["role."+role]), nil)
		draw(img, frameOf(sprites["badge."+role], an, k), s.paint(), nil)
	}
	if vw.grid == gridRPG && c != nil {
		s.drawMarker(img, v, an, k, c)
	}
	if t := sprites["tool."+role]; v == viewThreeQuarter && an == "act_"+role && t != nil {
		draw(img, frameOf(t, an, k), s.paint(), nil)
	}
	return img
}

// marker draws Corps c's marker alone in view v at frame k of animation an.
func (s *artSet) marker(v, an string, k int, c *corpsExample) *image.NRGBA {
	vw := viewOf(v)
	img := image.NewNRGBA(image.Rect(0, 0, vw.w, vw.h))
	s.drawMarker(img, v, an, k, c)
	return img
}

// drawMarker draws the pennant in c's colour, then c's emblem in default
// colours.
func (s *artSet) drawMarker(img *image.NRGBA, v, an string, k int, c *corpsExample) {
	draw(img, frameOf(s.sprites[v][bannerID], an, k), s.paint(s.choice[c.color]), nil)
	draw(img, frameOf(s.sprites[v][c.emblem], an, k), s.paint(), nil)
}

// frameOf returns m's pixels at frame k of animation an.
func frameOf(m *pixmap, an string, k int) []uint8 {
	i, _ := m.frame(an, k)
	return m.px[i]
}

// luma returns the gray level of c, Y = (299R + 587G + 114B + 500) / 1000.
func luma(c color.NRGBA) int {
	return (299*int(c.R) + 587*int(c.G) + 114*int(c.B) + 500) / 1000
}

// grayed returns a copy of src in which every opaque pixel is replaced by its
// luma (§3.13).
func grayed(src *image.NRGBA) *image.NRGBA {
	dst := image.NewNRGBA(src.Rect)
	copy(dst.Pix, src.Pix)
	for i := 0; i < len(dst.Pix); i += 4 {
		p := dst.Pix[i : i+4 : i+4]
		if p[3] != 0xff {
			continue
		}
		y := uint8(luma(color.NRGBA{p[0], p[1], p[2], p[3]}))
		p[0], p[1], p[2] = y, y, y
	}
	return dst
}

// put copies the opaque pixels of src into dst with src's top-left corner
// at (x, y).
func put(dst, src *image.NRGBA, x, y int) {
	b := src.Rect
	for sy := b.Min.Y; sy < b.Max.Y; sy++ {
		for sx := b.Min.X; sx < b.Max.X; sx++ {
			if c := src.NRGBAAt(sx, sy); c.A == 0xff {
				dst.SetNRGBA(x+sx-b.Min.X, y+sy-b.Min.Y, c)
			}
		}
	}
}
