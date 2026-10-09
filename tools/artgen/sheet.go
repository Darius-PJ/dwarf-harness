package main

import (
	"fmt"
	"image"
	"image/color"
	"strings"
)

// Contact sheet layout (§3.17; §3.13 of the round-2 brief).
const (
	mapSheetWidth = 324 // 16 world cells per row
	sheetMargin   = 4
	sectionGap    = 12
	zoom          = 4
	maxMapCell    = mapSheetWidth - 2*sheetMargin // the widest cell the map sheet holds
)

var (
	sheetBackground = color.NRGBA{0x7c, 0x7c, 0x88, 0xff}
	cellBackground  = color.NRGBA{0x8c, 0x8c, 0x98, 0xff}
)

// sheetCell is one cell of a contact sheet, at 1x.
type sheetCell struct {
	section string
	x, y    int
	img     *image.NRGBA // opaque pixels drawn over the cell background
	content string
}

// sheet lays cells out by flow: left to right, a new row when the next cell
// would cross the right margin, and a new row with a wider gap for each
// section.
type sheet struct {
	name    string
	width   int
	cells   []sheetCell
	section string
	x, y    int
	rowH    int
}

func newSheet(name string, width int) *sheet {
	return &sheet{name: name, width: width, x: sheetMargin, y: sheetMargin}
}

// begin starts a section on a new row.
func (sh *sheet) begin(section string) {
	if sh.x > sheetMargin {
		sh.x, sh.y, sh.rowH = sheetMargin, sh.y+sh.rowH+sectionGap, 0
	}
	sh.section = section
}

// row starts a new row when the current one holds a cell.
func (sh *sheet) row() {
	if sh.x > sheetMargin {
		sh.x, sh.y, sh.rowH = sheetMargin, sh.y+sh.rowH+sheetMargin, 0
	}
}

// add places a cell.
func (sh *sheet) add(img *image.NRGBA, format string, args ...any) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if sh.x+w+sheetMargin > sh.width {
		sh.row()
	}
	sh.cells = append(sh.cells, sheetCell{sh.section, sh.x, sh.y, img, fmt.Sprintf(format, args...)})
	sh.x += w + sheetMargin
	sh.rowH = max(sh.rowH, h)
}

// image draws the sheet at 1x.
func (sh *sheet) image() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, sh.width, sh.y+sh.rowH+sheetMargin))
	fill(img, img.Rect, sheetBackground)
	for _, c := range sh.cells {
		fill(img, c.img.Rect.Add(image.Pt(c.x, c.y)), cellBackground)
		put(img, c.img, c.x, c.y)
	}
	return img
}

// index appends the sheet's lines of sheets/index.tsv.
func (sh *sheet) index(b *strings.Builder) {
	for _, c := range sh.cells {
		fmt.Fprintf(b, "%s\t%s\t%d\t%d\t%d\t%d\t%s\n", sh.name, c.section, c.x, c.y, c.img.Rect.Dx(), c.img.Rect.Dy(), c.content)
	}
}

func fill(img *image.NRGBA, r image.Rectangle, c color.NRGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
}

// scale returns src with every pixel enlarged to a k-by-k block.
func scale(src *image.NRGBA, k int) *image.NRGBA {
	b := src.Rect
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx()*k, b.Dy()*k))
	for y := range dst.Rect.Dy() {
		for x := range dst.Rect.Dx() {
			dst.SetNRGBA(x, y, src.NRGBAAt(b.Min.X+x/k, b.Min.Y+y/k))
		}
	}
	return dst
}

// frames adds every frame of m in the given colors; each cell's content is
// prefix followed by "<id> <anim> <frame>".
func (sh *sheet) frames(m *pixmap, colors []color.NRGBA, prefix string) {
	for _, a := range m.anims {
		for k := range a.frames {
			sh.add(frameImage(m, a.first+k, colors), "%s%s %s %d", prefix, m.id, a.name, k)
		}
	}
}

// layers returns the layers of view v in sheet order: the parts in slot
// order, by ID within a slot, then the banner, the emblems by ID and the
// tools in role order.
func (s *artSet) layers(v string) []*pixmap {
	sprites := s.sprites[v]
	ids := s.ids(v)
	var out []*pixmap
	for _, slot := range slots {
		for _, id := range ids {
			if m := sprites[id]; m.kind == kindPart && m.slot == slot {
				out = append(out, m)
			}
		}
	}
	if m := sprites[bannerID]; m != nil {
		out = append(out, m)
	}
	for _, id := range ids {
		if m := sprites[id]; m.kind == kindEmblem {
			out = append(out, m)
		}
	}
	for _, role := range roles {
		if m := sprites["tool."+role]; m != nil {
			out = append(out, m)
		}
	}
	return out
}

// mapSheet lays out the 16-pixel sheet.
func (s *artSet) mapSheet() *sheet {
	sh := newSheet("map", mapSheetWidth)
	world := s.sprites[viewOverworld]
	colors := s.paint()
	for _, sec := range []struct{ name, kind string }{{"tiles", kindTile}, {"animals", kindAnimal}} {
		sh.begin(sec.name)
		for _, id := range s.ids(viewOverworld) {
			if m := world[id]; m.kind == sec.kind {
				sh.frames(m, colors, "")
			}
		}
	}
	sh.begin("parts")
	for _, m := range s.layers(viewOverworld) {
		sh.frames(m, colors, "")
	}
	sh.frames(world["overlay.trim"], colors, "")
	for _, role := range roles {
		sh.frames(world["badge."+role], colors, "")
	}
	idle := s.idleFrames()
	dwarf := func(f int, o *outfit, role string) *image.NRGBA {
		return s.compose(viewOverworld, "idle", f, o, role, nil)
	}
	sh.begin("roles")
	for _, role := range roles {
		for f := range idle {
			sh.add(dwarf(f, s.outfits[0], role), "dwarf %s %s idle %d", s.outfits[0].name, role, f)
		}
	}
	sh.begin("roles-varied")
	for i, role := range roles {
		o := s.outfits[i%len(s.outfits)]
		for f := range idle {
			sh.add(dwarf(f, o, role), "dwarf %s %s idle %d", o.name, role, f)
		}
	}
	sh.begin("roles-gray")
	for _, role := range roles {
		for f := range idle {
			sh.add(grayed(dwarf(f, s.outfits[0], role)), "dwarf-gray %s %s idle %d", s.outfits[0].name, role, f)
		}
	}
	sh.begin("room")
	sh.add(s.roomImage(), "room")
	return sh
}

// memberLoops returns the three-quarter loops a roster member plays: idle,
// then its role's action when animations.txt has one.
func (s *artSet) memberLoops(m member) []*loop {
	out := []*loop{s.loopNamed("idle")}
	if l := s.loopNamed("act_" + m.role); l != nil {
		out = append(out, l)
	}
	return out
}

// page draws roster member m in the three-quarter view at frame k of loop an.
func (s *artSet) page(m member, an string, k int) *image.NRGBA {
	return s.compose(viewThreeQuarter, an, k, s.outfit[m.outfit], m.role, s.roster.corpsNamed(m.corps))
}

// characterSheet lays out the 64-pixel sheet (§3.13 of the round-2 brief).
// It is as wide as one layer's three-quarter frames.
func (s *artSet) characterSheet() *sheet {
	tq := viewOf(viewThreeQuarter)
	n := 0
	for _, l := range s.loops {
		n += len(l.ms)
	}
	sh := newSheet("character", sheetMargin+n*(tq.w+sheetMargin))
	colors := s.paint()
	sh.begin(viewFront)
	for _, m := range s.layers(viewFront) {
		sh.frames(m, colors, viewFront+" ")
	}
	sh.begin(viewThreeQuarter)
	for _, m := range s.layers(viewThreeQuarter) {
		sh.row()
		sh.frames(m, colors, viewThreeQuarter+" ")
	}
	sh.begin("corps")
	for _, c := range s.roster.corps {
		sh.row()
		front, back := s.marker(viewFront, "still", 0, c), s.marker(viewThreeQuarter, "idle", 0, c)
		sh.add(front, "corps %s %s still 0", c.name, viewFront)
		sh.add(back, "corps %s %s idle 0", c.name, viewThreeQuarter)
		sh.add(grayed(front), "corps-gray %s %s still 0", c.name, viewFront)
		sh.add(grayed(back), "corps-gray %s %s idle 0", c.name, viewThreeQuarter)
	}
	still := func(m member) *image.NRGBA {
		return s.compose(viewFront, "still", 0, s.outfit[m.outfit], m.role, s.roster.corpsNamed(m.corps))
	}
	sh.begin("roster")
	for _, m := range s.roster.members {
		sh.add(still(m), "dwarf %s %s %s %s still 0", m.outfit, m.role, m.corps, viewFront)
	}
	sh.begin("roster-gray")
	for _, m := range s.roster.members {
		sh.add(grayed(still(m)), "dwarf-gray %s %s %s %s still 0", m.outfit, m.role, m.corps, viewFront)
	}
	sh.begin("page")
	for _, m := range s.roster.members {
		sh.row()
		for _, l := range s.memberLoops(m) {
			for k := range l.ms {
				sh.add(s.page(m, l.name, k), "dwarf %s %s %s %s %s %d", m.outfit, m.role, m.corps, viewThreeQuarter, l.name, k)
			}
		}
	}
	return sh
}

// roomImage draws room.txt: the terrain grid, then the placements in file
// order, each with its anchor on its cell's ground point.
func (s *artSet) roomImage() *image.NRGBA {
	rm := s.room
	world := s.sprites[viewOverworld]
	colors := s.paint()
	img := image.NewNRGBA(image.Rect(0, 0, rm.cols*cell, rm.rows*cell))
	at := func(src *image.NRGBA, ax, ay, col, row int) {
		put(img, src, col*cell+cell/2-ax, row*cell+cell-ay)
	}
	for row, line := range rm.grid {
		for col := range rm.cols {
			c := rm.cells[line.text[col]]
			m := world[c.tile]
			f, _ := m.frame(c.anim, c.frame)
			at(frameImage(m, f, colors), m.ax, m.ay, col, row)
		}
	}
	dwarf := viewOf(viewOverworld)
	for _, pl := range rm.placements {
		if pl.dwarf {
			at(s.compose(viewOverworld, "idle", pl.frame, s.outfit[pl.outfit], pl.role, nil), dwarf.ax, dwarf.ay, pl.col, pl.row)
			continue
		}
		m := world[pl.sprite]
		f, _ := m.frame(pl.anim, pl.frame)
		at(frameImage(m, f, colors), m.ax, m.ay, pl.col, pl.row)
	}
	return img
}
