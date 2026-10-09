package main

import (
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
)

// The files at the top of art/.
const (
	palettePath = "palette.txt"
	colorsPath  = "colors.txt"
	outfitsPath = "outfits.txt"
	roomPath    = "room.txt"
	licencePath = "LICENCE.txt"
)

// cell is the size of a world grid cell in pixels.
const cell = 16

// registration is the canvas and anchor that every part, the trim and every
// badge of a grid share (§3.8).
type registration struct{ w, h, ax, ay int }

var layerRegistration = map[string]registration{
	gridWorld: {cell, cell, cell / 2, cell},
	gridRPG:   {64, 64, 32, 64},
}

// inputFile is one file under art/.
type inputFile struct {
	path string
	data []byte
}

// artSet is every input, parsed and checked.
type artSet struct {
	inputs  []inputFile // in path order
	licence []byte
	pal     *palette
	choices []*choice
	choice  map[string]*choice
	outfits []*outfit
	outfit  map[string]*outfit
	room    *room
	sprites map[string]map[string]*pixmap // grid, then ID
	ref     map[string]*pixmap            // grid to its alphabetically first part
}

// ids returns the sprite IDs of a grid in byte order.
func (s *artSet) ids(grid string) []string {
	return slices.Sorted(maps.Keys(s.sprites[grid]))
}

// idleFrames returns the number of idle frames that every layer of a grid
// shares.
func (s *artSet) idleFrames(grid string) int {
	for _, a := range s.ref[grid].anims {
		if a.name == "idle" {
			return a.frames
		}
	}
	return 0
}

// known reports whether p is a file that art/ may hold.
func known(p string) bool {
	switch dir, name := path.Split(p); dir {
	case "":
		return slices.Contains([]string{palettePath, colorsPath, outfitsPath, roomPath, licencePath}, name)
	case gridWorld + "/", gridRPG + "/":
		return strings.HasSuffix(name, ".pxm")
	}
	return false
}

// load reads and checks every input (§3.12): first each file on its own,
// then, if none failed, the rules across files.
func load(art fs.FS) (*artSet, error) {
	s := &artSet{
		choice:  map[string]*choice{},
		outfit:  map[string]*outfit{},
		sprites: map[string]map[string]*pixmap{gridWorld: {}, gridRPG: {}},
		ref:     map[string]*pixmap{},
	}
	var unknown []string
	err := fs.WalkDir(art, ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case p == ".":
		case d.IsDir():
			if p != gridWorld && p != gridRPG {
				unknown = append(unknown, p)
				return fs.SkipDir
			}
		case !d.Type().IsRegular() || !known(p):
			unknown = append(unknown, p)
		default:
			data, err := fs.ReadFile(art, p)
			if err != nil {
				return err
			}
			s.inputs = append(s.inputs, inputFile{p, data})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the art inputs: %w", err)
	}
	slices.SortFunc(s.inputs, func(a, b inputFile) int { return strings.Compare(a.path, b.path) })

	r := &report{}
	file := map[string][]byte{}
	for _, f := range s.inputs {
		file[f.path] = f.data
	}
	text := func(p string) ([]textLine, bool) {
		data, ok := file[p]
		if !ok {
			r.add(p, 0, "missing")
			return nil, false
		}
		return readLines(r, p, data, false)
	}
	if lines, ok := text(palettePath); ok {
		s.pal = parsePalette(r, palettePath, lines)
	}
	if lines, ok := text(colorsPath); ok {
		s.choices = parseColors(r, colorsPath, lines)
	}
	if lines, ok := text(outfitsPath); ok {
		s.outfits = parseOutfits(r, outfitsPath, lines)
	}
	if lines, ok := text(roomPath); ok {
		s.room = parseRoom(r, roomPath, lines)
	}
	if data, ok := file[licencePath]; !ok {
		r.add(licencePath, 0, "missing")
	} else if _, ok := readLines(r, licencePath, data, true); ok {
		if len(data) == 0 {
			r.add(licencePath, 0, "empty")
		}
		s.licence = data
	}
	for _, f := range s.inputs {
		dir, name := path.Split(f.path)
		if dir == "" {
			continue
		}
		lines, ok := readLines(r, f.path, f.data, false)
		if !ok || s.pal == nil {
			// Without a valid palette the legend is unknown.
			continue
		}
		grid := strings.TrimSuffix(dir, "/")
		if m := parsePixmap(r, s.pal, f.path, grid, strings.TrimSuffix(name, ".pxm"), lines); m != nil {
			s.sprites[grid][m.id] = m
		}
	}
	if r.count() > 0 {
		return nil, r.err()
	}

	for _, c := range s.choices {
		s.choice[c.id] = c
	}
	for _, o := range s.outfits {
		s.outfit[o.name] = o
	}
	s.check(r, unknown)
	if r.count() > 0 {
		return nil, r.err()
	}
	for _, grid := range s.sprites {
		for _, m := range grid {
			m.resolve(s.pal)
		}
	}
	return s, nil
}

// check runs the rules across files (§3.12).
func (s *artSet) check(r *report, unknown []string) {
	for _, p := range unknown {
		r.add(p, 0, "unknown file or directory; art holds %s, %s, %s, %s, %s, world/*.pxm and rpg/*.pxm only",
			palettePath, colorsPath, outfitsPath, roomPath, licencePath)
	}
	world, rpg := s.sprites[gridWorld], s.sprites[gridRPG]
	for _, id := range s.ids(gridRPG) {
		switch m := rpg[id]; {
		case m.kind != kindPart:
			r.add(m.path, 0, "the rpg grid holds parts only")
		case world[id] == nil:
			r.add(gridWorld+"/"+id+".pxm", 0, "missing; part %s has an rpg map", id)
		}
	}
	for _, id := range s.ids(gridWorld) {
		if world[id].kind == kindPart && rpg[id] == nil {
			r.add(gridRPG+"/"+id+".pxm", 0, "missing; part %s has a world map", id)
		}
	}
	s.checkRegistration(r)
	filled := map[string]bool{}
	for _, m := range world {
		filled[m.slot] = true
	}
	for _, slot := range slots {
		if !filled[slot] {
			r.add(gridWorld, 0, "slot %s has no part", slot)
		}
	}
	if world["overlay.trim"] == nil {
		r.add(gridWorld+"/overlay.trim.pxm", 0, "missing; the role overlay needs its trim")
	}
	for i, role := range roles {
		b := world["badge."+role]
		if b == nil {
			r.add(gridWorld+"/badge."+role+".pxm", 0, "missing; role %s needs its badge", role)
			continue
		}
		for _, prev := range roles[:i] {
			if a := world["badge."+prev]; a != nil && slices.Equal(a.rows[0], b.rows[0]) {
				r.add(b.path, b.frameLines[0], "frame 0 repeats badge.%s; every role needs a badge of its own", prev)
			}
		}
	}
	for _, c := range s.choices {
		if want := len(s.pal.ramps[c.mask]); len(c.rgb) != want {
			r.add(colorsPath, c.line, "%s has %d colors; the %s ramp has %d entries", c.id, len(c.rgb), c.mask, want)
		}
	}
	for _, o := range s.outfits {
		for _, id := range []string{o.skin, o.hair, o.clothing} {
			if s.choice[id] == nil {
				r.add(outfitsPath, o.line, "unknown color choice %s", id)
			}
		}
		for _, part := range o.parts {
			if world[part.id] == nil {
				r.add(outfitsPath, part.line, "unknown part %s", part.id)
			}
			if part.clothing != "" && s.choice[part.clothing] == nil {
				r.add(outfitsPath, part.line, "unknown color choice %s", part.clothing)
			}
		}
	}
	s.checkRoom(r)
}

// checkRegistration applies §3.8: the world grid rule to every world sprite,
// and one canvas, anchor and animation list to the layers of each grid.
func (s *artSet) checkRegistration(r *report) {
	for _, grid := range []string{gridWorld, gridRPG} {
		want := layerRegistration[grid]
		ids := s.ids(grid)
		for _, id := range ids {
			if m := s.sprites[grid][id]; m.kind == kindPart {
				s.ref[grid] = m
				break
			}
		}
		ref := s.ref[grid]
		if ref != nil {
			if _, ok := ref.frame("idle", 0); !ok {
				r.add(ref.path, 0, "no idle animation; dwarves are composed from idle frames")
			}
		}
		for _, id := range ids {
			m := s.sprites[grid][id]
			if grid == gridWorld {
				if m.w%cell != 0 || m.h%cell != 0 {
					r.add(m.path, m.header["canvas"], "canvas %dx%d; world sprites cover whole %d-pixel cells", m.w, m.h, cell)
				}
				if m.w > maxMapCell {
					r.add(m.path, m.header["canvas"], "canvas %d px wide; the map sheet holds at most %d", m.w, maxMapCell)
				}
				if m.ay != m.h || m.ax%cell != cell/2 {
					r.add(m.path, m.header["anchor"], "anchor (%d,%d); a world anchor is the bottom centre of a cell", m.ax, m.ay)
				}
			}
			if !m.layered() {
				continue
			}
			if m.w != want.w || m.h != want.h {
				r.add(m.path, m.header["canvas"], "canvas %dx%d; every %s layer is %dx%d", m.w, m.h, grid, want.w, want.h)
			}
			if m.ax != want.ax || m.ay != want.ay {
				r.add(m.path, m.header["anchor"], "anchor (%d,%d); every %s layer has (%d,%d)", m.ax, m.ay, grid, want.ax, want.ay)
			}
			if ref != nil && !slices.EqualFunc(m.anims, ref.anims, func(a, b anim) bool { return a.name == b.name && a.frames == b.frames }) {
				r.add(m.path, 0, "animations %s differ from %s in %s", animList(m), animList(ref), ref.id)
			}
		}
	}
}

// animList describes the animations of m, as in "idle 2, walk 2".
func animList(m *pixmap) string {
	s := make([]string, len(m.anims))
	for i, a := range m.anims {
		s[i] = fmt.Sprintf("%s %d", a.name, a.frames)
	}
	return strings.Join(s, ", ")
}

// checkRoom checks that room.txt names existing sprites, outfits and frames
// and that every sprite drawn in the room lies inside it.
func (s *artSet) checkRoom(r *report) {
	rm := s.room
	world := s.sprites[gridWorld]
	width, height := rm.cols*cell, rm.rows*cell
	if width > maxMapCell {
		r.add(roomPath, rm.gridLine, "room is %d px wide; the map sheet holds at most %d", width, maxMapCell)
	}
	inside := func(w, h, ax, ay, col, row int) bool {
		x, y := col*cell+cell/2-ax, row*cell+cell-ay
		return x >= 0 && y >= 0 && x+w <= width && y+h <= height
	}
	tiles := map[byte]*pixmap{}
	for _, c := range rm.cellList {
		m := world[c.tile]
		switch {
		case m == nil:
			r.add(roomPath, c.line, "unknown tile %s", c.tile)
		case !hasFrame(m, c.anim, c.frame):
			r.add(roomPath, c.line, "%s has no frame %s %d", c.tile, c.anim, c.frame)
		default:
			tiles[c.char] = m
		}
	}
	for y, row := range rm.grid {
		for x := range rm.cols {
			if m := tiles[row.text[x]]; m != nil && !inside(m.w, m.h, m.ax, m.ay, x, y) {
				r.add(roomPath, row.n, "cell %q at column %d reaches outside the room", row.text[x], x)
				break
			}
		}
	}
	dwarf := layerRegistration[gridWorld]
	for _, pl := range rm.placements {
		if pl.dwarf {
			switch {
			case s.outfit[pl.outfit] == nil:
				r.add(roomPath, pl.line, "unknown outfit %s", pl.outfit)
			case s.ref[gridWorld] != nil && pl.frame >= s.idleFrames(gridWorld):
				r.add(roomPath, pl.line, "no idle frame %d; dwarves have %d", pl.frame, s.idleFrames(gridWorld))
			case !inside(dwarf.w, dwarf.h, dwarf.ax, dwarf.ay, pl.col, pl.row):
				r.add(roomPath, pl.line, "the dwarf reaches outside the room")
			}
			continue
		}
		m := world[pl.sprite]
		switch {
		case m == nil:
			r.add(roomPath, pl.line, "unknown world sprite %s", pl.sprite)
		case !hasFrame(m, pl.anim, pl.frame):
			r.add(roomPath, pl.line, "%s has no frame %s %d", pl.sprite, pl.anim, pl.frame)
		case !inside(m.w, m.h, m.ax, m.ay, pl.col, pl.row):
			r.add(roomPath, pl.line, "%s reaches outside the room", pl.sprite)
		}
	}
}

// hasFrame reports whether m has frame i of animation name.
func hasFrame(m *pixmap, name string, i int) bool {
	_, ok := m.frame(name, i)
	return ok
}
