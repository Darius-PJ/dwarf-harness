package main

import (
	"fmt"
	"image/color"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
)

// The files at the top of art/.
const (
	palettePath    = "palette.txt"
	colorsPath     = "colors.txt"
	outfitsPath    = "outfits.txt"
	roomPath       = "room.txt"
	licencePath    = "LICENCE.txt"
	animationsPath = "animations.txt"
	rosterPath     = "roster.txt"
)

// topFiles lists the files art/ holds beside its view directories; each is
// required.
var topFiles = []string{palettePath, colorsPath, outfitsPath, roomPath, licencePath, animationsPath, rosterPath}

// cell is the size of a world grid cell in pixels.
const cell = 16

// bannerID is the only banner: the Corps marker's flag.
const bannerID = "banner.pennant"

// view is one way of drawing the layered dwarf (§3.1 of the round-2 brief).
// Every layer of a view shares its canvas and anchor.
type view struct {
	name, grid, dir, facing string
	w, h, ax, ay            int
}

// views is the view table, in the order images and records list them.
var views = []view{
	{viewOverworld, gridWorld, "world", "front", cell, cell, cell / 2, cell},
	{viewFront, gridRPG, "rpg/front", "front", 64, 64, 32, 64},
	{viewThreeQuarter, gridRPG, "rpg/three_quarter", "three_quarter_right", 64, 64, 32, 64},
}

// viewOf returns the view called name, which is one of the view constants.
func viewOf(name string) view {
	return views[slices.IndexFunc(views, func(v view) bool { return v.name == name })]
}

// rpgViews returns the views on the 64-pixel grid.
func rpgViews() []view {
	return slices.DeleteFunc(slices.Clone(views), func(v view) bool { return v.grid != gridRPG })
}

// dirView returns the view whose directory under art/ is dir, or "".
func dirView(dir string) string {
	for _, v := range views {
		if v.dir == dir {
			return v.name
		}
	}
	return ""
}

// The Corps colour guards (§3.3 of the round-2 brief). They are generator
// rules, not owner decisions: if a review comment names a colour they reject,
// the review round changes the threshold and records the change.
const (
	corpsLumaGap = 48 // least luma between a Corps base and the outline or the light
	corpsMinDist = 64 // least RGB distance between a Corps base and any role or other Corps base
)

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
	loops   []loop
	roster  *roster
	sprites map[string]map[string]*pixmap // view, then ID
	ref     *pixmap                       // the alphabetically first overworld part
}

// ids returns the sprite IDs of a view in byte order.
func (s *artSet) ids(v string) []string {
	return slices.Sorted(maps.Keys(s.sprites[v]))
}

// layerAnims returns the animations, as in a layer's strip, that every layer
// of view v has: the first overworld part's, the front still, or
// animations.txt's loops.
func (s *artSet) layerAnims(v string) []anim {
	switch v {
	case viewFront:
		return []anim{{name: "still", frames: 1}}
	case viewThreeQuarter:
		var out []anim
		first := 0
		for _, l := range s.loops {
			out = append(out, anim{name: l.name, first: first, frames: len(l.ms)})
			first += len(l.ms)
		}
		return out
	}
	if s.ref == nil {
		return nil
	}
	return s.ref.anims
}

// loopNamed returns the animations.txt loop called name, or nil.
func (s *artSet) loopNamed(name string) *loop {
	for i := range s.loops {
		if s.loops[i].name == name {
			return &s.loops[i]
		}
	}
	return nil
}

// idleFrames returns the number of idle frames that every overworld layer
// shares.
func (s *artSet) idleFrames() int {
	for _, a := range s.layerAnims(viewOverworld) {
		if a.name == "idle" {
			return a.frames
		}
	}
	return 0
}

// known reports whether p is a file that art/ may hold.
func known(p string) bool {
	dir, name := path.Split(p)
	if dir == "" {
		return slices.Contains(topFiles, name)
	}
	return dirView(strings.TrimSuffix(dir, "/")) != "" && strings.HasSuffix(name, ".pxm")
}

// load reads and checks every input (§3.12): first each file on its own,
// then, if none failed, the rules across files.
func load(art fs.FS) (*artSet, error) {
	s := &artSet{
		choice:  map[string]*choice{},
		outfit:  map[string]*outfit{},
		sprites: map[string]map[string]*pixmap{},
	}
	for _, v := range views {
		s.sprites[v.name] = map[string]*pixmap{}
	}
	var unknown []string
	err := fs.WalkDir(art, ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case p == ".":
		case d.IsDir():
			if p != gridRPG && dirView(p) == "" {
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
	if lines, ok := text(animationsPath); ok {
		s.loops = parseAnimations(r, animationsPath, lines)
	}
	if lines, ok := text(rosterPath); ok {
		s.roster = parseRoster(r, rosterPath, lines)
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
		v := dirView(strings.TrimSuffix(dir, "/"))
		if m := parsePixmap(r, s.pal, f.path, v, strings.TrimSuffix(name, ".pxm"), lines); m != nil {
			s.sprites[v][m.id] = m
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
	for _, sprites := range s.sprites {
		for _, m := range sprites {
			m.resolve(s.pal)
		}
	}
	s.check(r, unknown)
	if r.count() > 0 {
		return nil, r.err()
	}
	return s, nil
}

// check runs the rules across files (§3.12; §3.9 of the round-2 brief).
func (s *artSet) check(r *report, unknown []string) {
	for _, p := range unknown {
		r.add(p, 0, "unknown file or directory; art holds %s, world/*.pxm, rpg/front/*.pxm and rpg/three_quarter/*.pxm only",
			strings.Join(topFiles, ", "))
	}
	s.checkViews(r)
	s.checkRegistration(r)

	world := s.sprites[viewOverworld]
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
		r.add("world/overlay.trim.pxm", 0, "missing; the role overlay needs its trim")
	}
	for i, role := range roles {
		b := world["badge."+role]
		if b == nil {
			r.add("world/badge."+role+".pxm", 0, "missing; role %s needs its badge", role)
			continue
		}
		for _, prev := range roles[:i] {
			if a := world["badge."+prev]; a != nil && slices.Equal(a.rows[0], b.rows[0]) {
				r.add(b.path, b.frameLines[0], "frame 0 repeats badge.%s; every role needs a badge of its own", prev)
			}
		}
	}
	s.checkEmblems(r)

	for _, c := range s.choices {
		if want := len(s.pal.ramps[c.mask]); len(c.rgb) != want {
			r.add(colorsPath, c.line, "%s has %d colors; the %s ramp has %d entries", c.id, len(c.rgb), c.mask, want)
		}
	}
	s.checkCorpsColors(r)
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
	for _, c := range s.roster.corps {
		if ch := s.choice[c.color]; ch == nil || ch.mask != "corps" {
			r.add(rosterPath, c.line, "unknown Corps colour %s", c.color)
		}
		if m := s.sprites[viewFront][c.emblem]; m == nil || m.kind != kindEmblem {
			r.add(rosterPath, c.line, "unknown emblem %s", c.emblem)
		}
	}
	for _, m := range s.roster.members {
		if s.outfit[m.outfit] == nil {
			r.add(rosterPath, m.line, "unknown outfit %s", m.outfit)
		}
	}
}

// checkViews checks that every part has a map in every view, that both RPG
// views have the pennant and the same emblems, that every tool has its loop,
// and that a part occludes the same slots in every view.
func (s *artSet) checkViews(r *report) {
	all := map[string]*pixmap{}
	for _, v := range views {
		maps.Copy(all, s.sprites[v.name])
	}
	for _, id := range slices.Sorted(maps.Keys(all)) {
		var need []view
		switch all[id].kind {
		case kindPart:
			need = views
		case kindEmblem:
			need = rpgViews()
		}
		for _, v := range need {
			if s.sprites[v.name][id] == nil {
				r.add(v.dir+"/"+id+".pxm", 0, "missing; %s %s needs a map in the %s view", all[id].kind, id, v.name)
			}
		}
	}
	for _, v := range rpgViews() {
		if s.sprites[v.name][bannerID] == nil {
			r.add(v.dir+"/"+bannerID+".pxm", 0, "missing; the Corps marker needs its pennant")
		}
		if !slices.ContainsFunc(slices.Collect(maps.Values(s.sprites[v.name])), func(m *pixmap) bool { return m.kind == kindEmblem }) {
			r.add(v.dir, 0, "no emblem; the Corps marker needs at least one")
		}
	}
	for _, id := range s.ids(viewThreeQuarter) {
		if m := s.sprites[viewThreeQuarter][id]; m.kind == kindTool && s.loopNamed("act_"+m.role) == nil {
			r.add(m.path, 0, "act_%s is not in %s", m.role, animationsPath)
		}
	}
	for _, id := range s.ids(viewOverworld) {
		ow := s.sprites[viewOverworld][id]
		if ow.kind != kindPart {
			continue
		}
		for _, v := range rpgViews() {
			if m := s.sprites[v.name][id]; m != nil && !slices.Equal(m.occludes, ow.occludes) {
				r.add(m.path, m.header["occludes"], "occludes %v; the overworld map occludes %v", m.occludes, ow.occludes)
			}
		}
	}
}

// checkRegistration applies the registration rules per view: the world grid
// rule to every overworld sprite, and one canvas, anchor and animation list
// to every layer of a view (§3.7 of the round-2 brief).
func (s *artSet) checkRegistration(r *report) {
	for _, id := range s.ids(viewOverworld) {
		if m := s.sprites[viewOverworld][id]; m.kind == kindPart {
			s.ref = m
			break
		}
	}
	if s.ref != nil {
		if _, ok := s.ref.frame("idle", 0); !ok {
			r.add(s.ref.path, 0, "no idle animation; dwarves are composed from idle frames")
		}
	}
	for _, v := range views {
		want := s.layerAnims(v.name)
		for _, id := range s.ids(v.name) {
			m := s.sprites[v.name][id]
			if v.name == viewOverworld {
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
			mine := want
			switch {
			case m.kind == kindTool:
				l := s.loopNamed("act_" + m.role)
				if l == nil {
					continue
				}
				mine = []anim{{name: l.name, frames: len(l.ms)}}
			case !m.layered():
				continue
			}
			if m.w != v.w || m.h != v.h {
				r.add(m.path, m.header["canvas"], "canvas %dx%d; every %s layer is %dx%d", m.w, m.h, v.name, v.w, v.h)
			}
			if m.ax != v.ax || m.ay != v.ay {
				r.add(m.path, m.header["anchor"], "anchor (%d,%d); every %s layer has (%d,%d)", m.ax, m.ay, v.name, v.ax, v.ay)
			}
			if mine != nil && !slices.EqualFunc(m.anims, mine, func(a, b anim) bool { return a.name == b.name && a.frames == b.frames }) {
				r.add(m.path, 0, "animations %s; want %s", animList(m.anims), animList(mine))
			}
		}
	}
}

// animList describes animations, as in "idle 2, walk 2".
func animList(anims []anim) string {
	s := make([]string, len(anims))
	for i, a := range anims {
		s[i] = fmt.Sprintf("%s %d", a.name, a.frames)
	}
	return strings.Join(s, ", ")
}

// checkEmblems checks, in each RPG view, that the emblems differ from each
// other and that every opaque emblem pixel lies on the pennant's Corps field
// in the same animation and frame.
func (s *artSet) checkEmblems(r *report) {
	for _, v := range rpgViews() {
		var emblems []*pixmap
		for _, id := range s.ids(v.name) {
			if m := s.sprites[v.name][id]; m.kind == kindEmblem {
				emblems = append(emblems, m)
			}
		}
		for i, b := range emblems {
			for _, a := range emblems[:i] {
				if slices.Equal(a.rows[0], b.rows[0]) {
					r.add(b.path, b.frameLines[0], "frame 0 repeats %s; every Corps emblem must differ", a.id)
				}
			}
		}
		banner := s.sprites[v.name][bannerID]
		if banner == nil {
			continue
		}
		for _, e := range emblems {
			if e.w != banner.w || e.h != banner.h {
				continue
			}
			for _, a := range e.anims {
				for k := range a.frames {
					bi, ok := banner.frame(a.name, k)
					if !ok {
						continue
					}
					ei := a.first + k
					for p, idx := range e.px[ei] {
						if !s.pal.clear(idx) && s.pal.entries[banner.px[bi][p]].mask != "corps" {
							x, y := p%e.w, p/e.w
							r.add(e.path, e.frameLines[ei]+1+y, "frame %s %d: pixel (%d,%d) lies off the pennant's Corps field", a.name, k, x, y)
							break
						}
					}
				}
			}
		}
	}
}

// checkCorpsColors applies the Corps colour guards to every corps.* choice.
func (s *artSet) checkCorpsColors(r *report) {
	named := func(name string) color.NRGBA {
		return s.pal.entries[slices.IndexFunc(s.pal.entries, func(e entry) bool { return e.name == name })].color
	}
	outline, light := luma(named("outline")), luma(named("light"))
	near := func(a, b color.NRGBA) bool {
		dr, dg, db := int(a.R)-int(b.R), int(a.G)-int(b.G), int(a.B)-int(b.B)
		return dr*dr+dg*dg+db*db < corpsMinDist*corpsMinDist
	}
	for i, c := range s.choices {
		if c.mask != "corps" || len(c.rgb) < 2 {
			continue
		}
		base := c.rgb[1]
		if y := luma(base); y-outline < corpsLumaGap || light-y < corpsLumaGap {
			r.add(colorsPath, c.line, "%s base luma %d lies within %d of the outline or light; its emblem would not read in gray", c.id, y, corpsLumaGap)
		}
		for _, o := range s.choices {
			if o.mask == "role" && len(o.rgb) >= 2 && near(base, o.rgb[1]) {
				r.add(colorsPath, c.line, "%s is too close to %s; a Corps colour must not read as a role colour", c.id, o.id)
				break
			}
		}
		for _, o := range s.choices[:i] {
			if o.mask == "corps" && len(o.rgb) >= 2 && near(base, o.rgb[1]) {
				r.add(colorsPath, c.line, "%s is too close to %s; Corps colours must read apart", c.id, o.id)
				break
			}
		}
	}
}

// checkRoom checks that room.txt names existing sprites, outfits and frames
// and that every sprite drawn in the room lies inside it.
func (s *artSet) checkRoom(r *report) {
	rm := s.room
	world := s.sprites[viewOverworld]
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
	dwarf := viewOf(viewOverworld)
	for _, pl := range rm.placements {
		if pl.dwarf {
			switch {
			case s.outfit[pl.outfit] == nil:
				r.add(roomPath, pl.line, "unknown outfit %s", pl.outfit)
			case s.ref != nil && pl.frame >= s.idleFrames():
				r.add(roomPath, pl.line, "no idle frame %d; dwarves have %d", pl.frame, s.idleFrames())
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
