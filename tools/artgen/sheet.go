package main

import (
	"fmt"
	"image"
	"image/color"
	"strings"
)

// Contact sheet layout (§3.17).
const (
	mapSheetWidth       = 324 // 16 world cells per row
	characterSheetWidth = 276 // 4 rpg cells per row
	sheetMargin         = 4
	sectionGap          = 12
	zoom                = 4
	maxMapCell          = mapSheetWidth - 2*sheetMargin // the widest cell the map sheet holds
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

// add places a cell.
func (sh *sheet) add(img *image.NRGBA, format string, args ...any) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if sh.x > sheetMargin && sh.x+w+sheetMargin > sh.width {
		sh.x, sh.y, sh.rowH = sheetMargin, sh.y+sh.rowH+sheetMargin, 0
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

// frames adds every frame of m in the given colors.
func (sh *sheet) frames(m *pixmap, colors []color.NRGBA) {
	for _, a := range m.anims {
		for k := range a.frames {
			sh.add(frameImage(m, a.first+k, colors), "%s %s %d", m.id, a.name, k)
		}
	}
}

// parts adds every part of a grid in slot order, by ID within a slot.
func (s *artSet) parts(sh *sheet, grid string) {
	colors := s.paint()
	for _, slot := range slots {
		for _, id := range s.ids(grid) {
			if m := s.sprites[grid][id]; m.slot == slot {
				sh.frames(m, colors)
			}
		}
	}
}

// mapSheet lays out the 16-pixel sheet.
func (s *artSet) mapSheet() *sheet {
	sh := newSheet("map", mapSheetWidth)
	world := s.sprites[gridWorld]
	colors := s.paint()
	for _, sec := range []struct{ name, kind string }{{"tiles", kindTile}, {"animals", kindAnimal}} {
		sh.begin(sec.name)
		for _, id := range s.ids(gridWorld) {
			if m := world[id]; m.kind == sec.kind {
				sh.frames(m, colors)
			}
		}
	}
	sh.begin("parts")
	s.parts(sh, gridWorld)
	sh.frames(world["overlay.trim"], colors)
	for _, role := range roles {
		sh.frames(world["badge."+role], colors)
	}
	idle := s.idleFrames(gridWorld)
	sh.begin("roles")
	for _, role := range roles {
		for f := range idle {
			sh.add(s.compose(gridWorld, f, s.outfits[0], role), "dwarf %s %s idle %d", s.outfits[0].name, role, f)
		}
	}
	sh.begin("roles-varied")
	for i, role := range roles {
		o := s.outfits[i%len(s.outfits)]
		for f := range idle {
			sh.add(s.compose(gridWorld, f, o, role), "dwarf %s %s idle %d", o.name, role, f)
		}
	}
	sh.begin("roles-gray")
	for _, role := range roles {
		for f := range idle {
			sh.add(grayed(s.compose(gridWorld, f, s.outfits[0], role)), "dwarf-gray %s %s idle %d", s.outfits[0].name, role, f)
		}
	}
	sh.begin("room")
	sh.add(s.roomImage(), "room")
	return sh
}

// characterSheet lays out the 64-pixel sheet.
func (s *artSet) characterSheet() *sheet {
	sh := newSheet("character", characterSheetWidth)
	sh.begin("parts")
	s.parts(sh, gridRPG)
	sh.begin("examples")
	for _, o := range s.outfits {
		for f := range s.idleFrames(gridRPG) {
			sh.add(s.compose(gridRPG, f, o, ""), "dwarf %s idle %d", o.name, f)
		}
	}
	return sh
}

// roomImage draws room.txt: the terrain grid, then the placements in file
// order, each with its anchor on its cell's ground point.
func (s *artSet) roomImage() *image.NRGBA {
	rm := s.room
	world := s.sprites[gridWorld]
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
	dwarf := layerRegistration[gridWorld]
	for _, pl := range rm.placements {
		if pl.dwarf {
			at(s.compose(gridWorld, pl.frame, s.outfit[pl.outfit], pl.role), dwarf.ax, dwarf.ay, pl.col, pl.row)
			continue
		}
		m := world[pl.sprite]
		f, _ := m.frame(pl.anim, pl.frame)
		at(frameImage(m, f, colors), m.ax, m.ay, pl.col, pl.row)
	}
	return img
}
