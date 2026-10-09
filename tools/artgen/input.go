package main

import (
	"bytes"
	"fmt"
	"image/color"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// slots lists the part slots in layer order (FS:130).
var slots = []string{"back_accessory", "body", "shoes", "legwear", "torso_clothing", "hair_beard", "headwear", "front_accessory"}

// roles lists Core's roles in display order (FS:55). It names Core roles, not
// art, so it is code.
var roles = []string{"worker", "orchestrator", "griot", "diplomat", "grand_orchestrator", "grand_griot", "grand_diplomat"}

// masks lists the palette masks in the order sample.json reports them.
var masks = []string{"skin", "hair", "clothing", "role", "corps"}

// categories lists the draft tile categories: FS:136's set that the sample
// uses, plus wall and stockpile (P7).
var categories = []string{"rock", "floor", "wall", "door", "station", "stockpile", "rail"}

// The grids, by name.
const (
	gridWorld = "world"
	gridRPG   = "rpg"
)

// The views, by name (§3.1 of the round-2 brief).
const (
	viewOverworld    = "overworld"
	viewFront        = "front"
	viewThreeQuarter = "three_quarter"
)

// The sprite kinds.
const (
	kindPart    = "part"
	kindOverlay = "overlay"
	kindBadge   = "badge"
	kindTile    = "tile"
	kindAnimal  = "animal"
	kindBanner  = "banner"
	kindEmblem  = "emblem"
	kindTool    = "tool"
)

// kindViews lists the views in which each kind may have a map.
var kindViews = map[string][]string{
	kindPart:    {viewOverworld, viewFront, viewThreeQuarter},
	kindOverlay: {viewOverworld},
	kindBadge:   {viewOverworld},
	kindTile:    {viewOverworld},
	kindAnimal:  {viewOverworld},
	kindBanner:  {viewFront, viewThreeQuarter},
	kindEmblem:  {viewFront, viewThreeQuarter},
	kindTool:    {viewThreeQuarter},
}

// maxCoord bounds every size, coordinate and frame index in the inputs.
const maxCoord = 65535

// artError is one validation failure. line is 0 when no single line applies.
type artError struct {
	path string
	line int
	msg  string
}

func (e artError) Error() string {
	if e.line == 0 {
		return e.path + ": " + e.msg
	}
	return fmt.Sprintf("%s:%d: %s", e.path, e.line, e.msg)
}

// errorList is every validation failure of one run, sorted by path and line.
type errorList []artError

func (l errorList) Error() string {
	s := make([]string, len(l))
	for i, e := range l {
		s[i] = e.Error()
	}
	return strings.Join(s, "\n")
}

// report collects validation failures.
type report struct{ list errorList }

func (r *report) add(path string, line int, format string, args ...any) {
	r.list = append(r.list, artError{path, line, fmt.Sprintf(format, args...)})
}

func (r *report) count() int { return len(r.list) }

// err returns the failures sorted by path, then line, or nil.
func (r *report) err() error {
	if len(r.list) == 0 {
		return nil
	}
	l := slices.Clone(r.list)
	slices.SortStableFunc(l, func(a, b artError) int {
		if c := strings.Compare(a.path, b.path); c != 0 {
			return c
		}
		return a.line - b.line
	})
	return l
}

// textLine is one line of an input without its LF.
type textLine struct {
	n    int
	text string
}

// skip reports whether the line is a comment or blank.
func (l textLine) skip() bool {
	return strings.HasPrefix(l.text, "//") || strings.Trim(l.text, " ") == ""
}

// readLines applies the byte rules of §3.3 and splits data into lines. Every
// input holds printable ASCII and LF only, except that freeText allows any
// UTF-8 apart from CR. It returns false after reporting a violation.
func readLines(r *report, p string, data []byte, freeText bool) ([]textLine, bool) {
	start := r.count()
	if i := bytes.IndexByte(data, '\r'); i >= 0 {
		r.add(p, 1+bytes.Count(data[:i], []byte{'\n'}), "CR line ending; art inputs use LF (B1)")
	}
	line, bad := 1, 0
	for i := 0; i < len(data); {
		b := data[i]
		size := 1
		ok := b == '\n' || b == '\r' || b >= 0x20 && b <= 0x7e
		if freeText && !ok {
			var c rune
			c, size = utf8.DecodeRune(data[i:])
			ok = c != utf8.RuneError || size > 1
		}
		if !ok && bad != line {
			if freeText {
				r.add(p, line, "invalid UTF-8")
			} else {
				r.add(p, line, "byte 0x%02x; art inputs hold printable ASCII, spaces and LF only", b)
			}
			bad = line
		}
		if b == '\n' {
			line++
		}
		i += size
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		r.add(p, line, "missing final LF")
	}
	if r.count() > start {
		return nil, false
	}
	if len(data) == 0 {
		return nil, true
	}
	parts := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	lines := make([]textLine, len(parts))
	for i, t := range parts {
		lines[i] = textLine{i + 1, t}
	}
	return lines, true
}

// isName reports whether s matches ^[a-z][a-z0-9_]*$.
func isName(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if c := s[i]; !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// isID reports whether s matches ^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$.
func isID(s string) bool {
	a, b, ok := strings.Cut(s, ".")
	return ok && isName(a) && isName(b)
}

// isEntryName reports whether s matches ^[a-z][a-z0-9_.]*$.
func isEntryName(s string) bool {
	return s != "" && isName(strings.ReplaceAll(s, ".", "_"))
}

// isHex reports whether s is n lowercase hexadecimal digits.
func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := range n {
		if c := s[i]; !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// rgb returns the color of a checked rrggbb or rrggbbaa string; rrggbb is
// opaque.
func rgb(s string) color.NRGBA {
	v, _ := strconv.ParseUint(s, 16, 32)
	if len(s) == 6 {
		v = v<<8 | 0xff
	}
	return color.NRGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}
}

// number parses a field of decimal digits no greater than max.
func number(s string, max uint64) (uint64, bool) {
	if s == "" || len(s) > 10 {
		return 0, false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseUint(s, 10, 64)
	return v, err == nil && v <= max
}

// coord parses a size, coordinate or frame index.
func coord(s string) (int, bool) {
	v, ok := number(s, maxCoord)
	return int(v), ok
}

// entry is one palette entry. mask is "" for the file's "-".
type entry struct {
	name  string
	mask  string
	color color.NRGBA
	line  int
}

// texture is a character that resolves per pixel to one of its entries.
type texture struct {
	names   []string
	entries []int
	weights []uint64
	total   uint64
	line    int
}

// palette is palette.txt: the global palette and the legend.
type palette struct {
	entries  []entry
	textures []*texture
	glyphs   map[byte]int // character to entry index
	texOf    map[byte]*texture
	occlude  int
	ramps    map[string][]int // mask to entry indices, shadow to light
}

// clear reports whether entry i is see-through (alpha 0).
func (p *palette) clear(i uint8) bool { return p.entries[i].color.A == 0 }

// parsePalette parses palette.txt (§3.4). It returns nil after reporting a
// violation.
func parsePalette(r *report, p string, lines []textLine) *palette {
	start := r.count()
	pal := &palette{glyphs: map[byte]int{}, texOf: map[byte]*texture{}, ramps: map[string][]int{}, occlude: -1}
	chars := map[byte]int{}
	names := map[string]int{}
	claim := func(l textLine, s string) (byte, bool) {
		if len(s) != 1 {
			r.add(p, l.n, "character %q must be a single character", s)
			return 0, false
		}
		if prev, dup := chars[s[0]]; dup {
			r.add(p, l.n, "character %q is already defined on line %d", s, prev)
			return 0, false
		}
		chars[s[0]] = l.n
		return s[0], true
	}
	for _, l := range lines {
		if l.skip() {
			continue
		}
		f := strings.Fields(l.text)
		switch f[0] {
		case "entry":
			if len(f) != 5 {
				r.add(p, l.n, "want entry <char> <name> <mask> <rrggbbaa>")
				continue
			}
			c, ok := claim(l, f[1])
			name, mask, hex := f[2], f[3], f[4]
			_, taken := names[name]
			switch {
			case !ok:
			case !isEntryName(name):
				r.add(p, l.n, "entry name %q must match [a-z][a-z0-9_.]*", name)
			case taken:
				r.add(p, l.n, "entry name %q is already defined", name)
			case mask != "-" && !slices.Contains(masks, mask):
				r.add(p, l.n, "mask %q; want - or one of %s", mask, strings.Join(masks, ", "))
			case !isHex(hex, 8):
				r.add(p, l.n, "color %q; want lowercase rrggbbaa", hex)
			default:
				if mask == "-" {
					mask = ""
				}
				names[name] = len(pal.entries)
				pal.glyphs[c] = len(pal.entries)
				pal.entries = append(pal.entries, entry{name, mask, rgb(hex), l.n})
			}
		case "texture":
			if len(f) < 3 {
				r.add(p, l.n, "want texture <char> <entry>:<weight> ...")
				continue
			}
			c, ok := claim(l, f[1])
			if !ok {
				continue
			}
			t := &texture{line: l.n}
			for _, spec := range f[2:] {
				name, w, _ := strings.Cut(spec, ":")
				weight, ok := number(w, 255)
				if !ok || weight == 0 {
					r.add(p, l.n, "texture part %q; want <entry>:<weight> with weight 1-255", spec)
					continue
				}
				t.names = append(t.names, name)
				t.weights = append(t.weights, weight)
				t.total += weight
			}
			pal.textures = append(pal.textures, t)
			pal.texOf[c] = t
		default:
			r.add(p, l.n, "unknown keyword %q; want entry or texture", f[0])
		}
	}
	if r.count() > start {
		return nil
	}
	switch {
	case len(pal.entries) == 0:
		r.add(p, 0, "no entries; entry 0 must be transparent")
	case pal.entries[0].name != "transparent":
		r.add(p, pal.entries[0].line, "entry 0 must be named transparent")
	}
	for i, e := range pal.entries {
		see := i == 0 && e.name == "transparent" || e.name == "occlude"
		switch {
		case see && e.color.A != 0:
			r.add(p, e.line, "%s must have alpha 00", e.name)
		case see && e.mask != "":
			r.add(p, e.line, "%s must have mask -", e.name)
		case !see && e.color.A != 0xff:
			r.add(p, e.line, "alpha %02x; only transparent and occlude have alpha 00, every other entry ff", e.color.A)
		}
		if e.name == "occlude" {
			pal.occlude = i
		}
		if e.mask != "" {
			pal.ramps[e.mask] = append(pal.ramps[e.mask], i)
		}
	}
	if pal.occlude < 0 {
		r.add(p, 0, "no entry named occlude")
	}
	// The Corps colour guard measures against outline and light.
	for _, name := range []string{"outline", "light"} {
		if !slices.ContainsFunc(pal.entries, func(e entry) bool { return e.name == name }) {
			r.add(p, 0, "no entry named %s", name)
		}
	}
	for _, m := range masks {
		ramp := pal.ramps[m]
		switch {
		case len(ramp) == 0:
			r.add(p, 0, "mask %s has no entries; a ramp has 2 or 3", m)
		case len(ramp) == 1:
			r.add(p, pal.entries[ramp[0]].line, "mask %s has 1 entry; a ramp has 2 or 3", m)
		case len(ramp) > 3:
			r.add(p, pal.entries[ramp[3]].line, "mask %s has %d entries; a ramp has 2 or 3", m, len(ramp))
		}
	}
	for _, t := range pal.textures {
		for _, name := range t.names {
			i, ok := names[name]
			switch {
			case !ok:
				r.add(p, t.line, "texture names unknown entry %q", name)
			case pal.entries[i].mask != "":
				r.add(p, t.line, "texture names %q of mask %s; textures use unmasked entries", name, pal.entries[i].mask)
			case pal.entries[i].color.A != 0xff:
				r.add(p, t.line, "texture names see-through entry %q", name)
			default:
				t.entries = append(t.entries, i)
			}
		}
	}
	if r.count() > start {
		return nil
	}
	return pal
}

// choice is one color choice for a mask from colors.txt.
type choice struct {
	id   string
	mask string
	hex  []string
	rgb  []color.NRGBA
	line int
}

// parseColors parses colors.txt (§3.5).
func parseColors(r *report, p string, lines []textLine) []*choice {
	var out []*choice
	seen := map[string]bool{}
	for _, l := range lines {
		if l.skip() {
			continue
		}
		f := strings.Fields(l.text)
		id := f[0]
		mask, name, _ := strings.Cut(id, ".")
		switch {
		case !isID(id):
			r.add(p, l.n, "choice %q must look like <mask>.<name>", id)
			continue
		case !slices.Contains(masks, mask):
			r.add(p, l.n, "choice %q names no mask; want skin, hair, clothing or role", id)
			continue
		case mask == "role" && !slices.Contains(roles, name):
			r.add(p, l.n, "choice %q names no role", id)
			continue
		case seen[id]:
			r.add(p, l.n, "choice %q is already defined", id)
			continue
		case len(f) < 2:
			r.add(p, l.n, "choice %q has no colors", id)
			continue
		}
		c := &choice{id: id, mask: mask, line: l.n}
		for _, h := range f[1:] {
			if !isHex(h, 6) {
				r.add(p, l.n, "color %q; want lowercase rrggbb", h)
				continue
			}
			c.hex = append(c.hex, h)
			c.rgb = append(c.rgb, rgb(h))
		}
		seen[id] = true
		out = append(out, c)
	}
	for _, m := range []string{"skin", "hair", "clothing", "corps"} {
		if !slices.ContainsFunc(out, func(c *choice) bool { return c.mask == m }) {
			r.add(p, 0, "no %s choice", m)
		}
	}
	for _, role := range roles {
		if !seen["role."+role] {
			r.add(p, 0, "no role.%s choice", role)
		}
	}
	return out
}

// outfit is one example outfit from outfits.txt.
type outfit struct {
	name                 string
	skin, hair, clothing string
	parts                []outfitPart
	line                 int
}

// outfitPart is one part of an outfit; clothing is "" when the part takes
// the outfit's clothing choice.
type outfitPart struct {
	id, slot string
	clothing string
	line     int
}

// parseOutfits parses outfits.txt (§3.9).
func parseOutfits(r *report, p string, lines []textLine) []*outfit {
	var out []*outfit
	var cur *outfit
	finish := func() {
		if cur != nil && !slices.ContainsFunc(cur.parts, func(o outfitPart) bool { return o.slot == "body" }) {
			r.add(p, cur.line, "outfit %s has no body part", cur.name)
		}
	}
	// pick checks a key=<choice> field and returns the choice.
	pick := func(l textLine, field, key, mask string) (string, bool) {
		v, ok := strings.CutPrefix(field, key+"=")
		if !ok || !isID(v) || !strings.HasPrefix(v, mask+".") {
			r.add(p, l.n, "field %q; want %s=%s.<name>", field, key, mask)
			return "", false
		}
		return v, true
	}
	for _, l := range lines {
		if l.skip() {
			continue
		}
		f := strings.Fields(l.text)
		switch f[0] {
		case "outfit":
			finish()
			cur = nil
			if len(f) != 5 {
				r.add(p, l.n, "want outfit <name> skin=<choice> hair=<choice> clothing=<choice>")
				continue
			}
			if !isName(f[1]) {
				r.add(p, l.n, "outfit name %q must match [a-z][a-z0-9_]*", f[1])
				continue
			}
			if slices.ContainsFunc(out, func(o *outfit) bool { return o.name == f[1] }) {
				r.add(p, l.n, "outfit %s is already defined", f[1])
				continue
			}
			skin, ok1 := pick(l, f[2], "skin", "skin")
			hair, ok2 := pick(l, f[3], "hair", "hair")
			clothing, ok3 := pick(l, f[4], "clothing", "clothing")
			if !ok1 || !ok2 || !ok3 {
				continue
			}
			cur = &outfit{name: f[1], skin: skin, hair: hair, clothing: clothing, line: l.n}
			out = append(out, cur)
		case "part":
			if cur == nil {
				r.add(p, l.n, "part outside a valid outfit")
				continue
			}
			if len(f) != 2 && len(f) != 3 {
				r.add(p, l.n, "want part <part-id> [clothing=<choice>]")
				continue
			}
			slot, _, _ := strings.Cut(f[1], ".")
			if !isID(f[1]) || !slices.Contains(slots, slot) {
				r.add(p, l.n, "%q is not a part ID <slot>.<name>", f[1])
				continue
			}
			if i := slices.IndexFunc(cur.parts, func(o outfitPart) bool { return o.slot == slot }); i >= 0 {
				r.add(p, l.n, "outfit %s already has %s in slot %s", cur.name, cur.parts[i].id, slot)
				continue
			}
			part := outfitPart{id: f[1], slot: slot, line: l.n}
			if len(f) == 3 {
				c, ok := pick(l, f[2], "clothing", "clothing")
				if !ok {
					continue
				}
				part.clothing = c
			}
			cur.parts = append(cur.parts, part)
		default:
			r.add(p, l.n, "unknown keyword %q; want outfit or part", f[0])
		}
	}
	finish()
	if len(out) == 0 {
		r.add(p, 0, "no outfit")
	}
	return out
}

// room is room.txt, the sample room.
type room struct {
	cells      map[byte]roomCell
	cellList   []roomCell
	cols, rows int
	grid       []textLine
	gridLine   int
	placements []placement
}

// roomCell is one cell character and the tile frame it draws.
type roomCell struct {
	char  byte
	tile  string
	anim  string
	frame int
	line  int
}

// placement is a place or dwarf line of room.txt.
type placement struct {
	dwarf        bool
	col, row     int
	sprite, anim string // place
	outfit, role string // dwarf
	frame        int
	line         int
}

// parseRoom parses room.txt (§3.10).
func parseRoom(r *report, p string, lines []textLine) *room {
	rm := &room{cells: map[byte]roomCell{}}
	const (
		cells = iota
		placements
	)
	phase := cells
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if l.skip() {
			continue
		}
		f := strings.Fields(l.text)
		switch f[0] {
		case "cell":
			if phase != cells {
				r.add(p, l.n, "cell after grid; cell lines come first")
				continue
			}
			if len(f) != 5 {
				r.add(p, l.n, "want cell <char> <tile-id> <anim> <frame>")
				continue
			}
			frame, ok := coord(f[4])
			kind, _, _ := strings.Cut(f[2], ".")
			switch {
			case len(f[1]) != 1:
				r.add(p, l.n, "cell character %q must be a single character", f[1])
			case rm.cells[f[1][0]].line > 0:
				r.add(p, l.n, "cell %q is already defined on line %d", f[1], rm.cells[f[1][0]].line)
			case !isID(f[2]) || kind != kindTile:
				r.add(p, l.n, "%q is not a tile ID", f[2])
			case !isName(f[3]) || !ok:
				r.add(p, l.n, "want <anim> <frame> after the tile ID")
			default:
				c := roomCell{f[1][0], f[2], f[3], frame, l.n}
				rm.cells[c.char] = c
				rm.cellList = append(rm.cellList, c)
			}
		case "grid":
			if phase != cells {
				r.add(p, l.n, "a second grid")
				continue
			}
			phase = placements
			cols, ok1 := coord(f[min(1, len(f)-1)])
			rows, ok2 := coord(f[len(f)-1])
			if len(f) != 3 || !ok1 || !ok2 || cols == 0 || rows == 0 {
				r.add(p, l.n, "want grid <cols> <rows>, each at least 1")
				return nil
			}
			rm.cols, rm.rows, rm.gridLine = cols, rows, l.n
			for k := range rows {
				if i+1 >= len(lines) {
					r.add(p, l.n, "grid has %d of %d rows", k, rows)
					break
				}
				i++
				row := lines[i]
				rm.grid = append(rm.grid, row)
				switch {
				case len(row.text) < cols:
					r.add(p, row.n, "row too short: %d of %d cells", len(row.text), cols)
				case len(row.text) > cols:
					r.add(p, row.n, "row too long: %d of %d cells", len(row.text), cols)
				default:
					for x := range cols {
						if _, ok := rm.cells[row.text[x]]; !ok {
							r.add(p, row.n, "undeclared cell %q", row.text[x])
							break
						}
					}
				}
			}
		case "place", "dwarf":
			if phase != placements {
				r.add(p, l.n, "%s before grid", f[0])
				continue
			}
			if len(f) != 6 {
				if f[0] == "place" {
					r.add(p, l.n, "want place <col> <row> <sprite-id> <anim> <frame>")
				} else {
					r.add(p, l.n, "want dwarf <col> <row> <outfit> <role> <frame>")
				}
				continue
			}
			col, ok1 := coord(f[1])
			row, ok2 := coord(f[2])
			frame, ok3 := coord(f[5])
			if !ok1 || !ok2 || !ok3 {
				r.add(p, l.n, "column, row and frame must be numbers 0-%d", maxCoord)
				continue
			}
			pl := placement{dwarf: f[0] == "dwarf", col: col, row: row, frame: frame, line: l.n}
			if pl.dwarf {
				pl.outfit, pl.role = f[3], f[4]
				if !isName(pl.outfit) || !slices.Contains(roles, pl.role) {
					r.add(p, l.n, "want an outfit name and one of the roles %s", strings.Join(roles, ", "))
					continue
				}
			} else {
				pl.sprite, pl.anim = f[3], f[4]
				if !isID(pl.sprite) || !isName(pl.anim) {
					r.add(p, l.n, "want <sprite-id> <anim>")
					continue
				}
			}
			rm.placements = append(rm.placements, pl)
		default:
			r.add(p, l.n, "unknown keyword %q; want cell, grid, place or dwarf", f[0])
		}
	}
	if phase == cells {
		r.add(p, 0, "no grid")
	}
	return rm
}

// anim is one animation of a pixel map: frames first to first+frames-1 of
// its strip.
type anim struct {
	name   string
	first  int
	frames int
}

// pixmap is one parsed <view directory>/<id>.pxm.
type pixmap struct {
	path, id, view string
	kind           string
	slot           string // parts
	role           string // badges and tools
	category       string // tiles
	w, h           int
	ax, ay         int
	occludes       []string // slot order
	seed           uint32
	anims          []anim
	rows           [][]string // per frame, h rows of w characters
	frameLines     []int
	header         map[string]int // keyword to line
	px             [][]uint8      // per frame, w*h palette indices; set once every file parses
}

// textured reports whether the map uses a texture character.
func (m *pixmap) textured() bool { _, ok := m.header["seed"]; return ok }

// layered reports whether the sprite is a layer that registers with the
// parts of its view: a part, the trim, a badge, the banner or an emblem.
func (m *pixmap) layered() bool {
	switch m.kind {
	case kindPart, kindOverlay, kindBadge, kindBanner, kindEmblem:
		return true
	}
	return false
}

// frame returns the strip index of frame i of animation name.
func (m *pixmap) frame(name string, i int) (int, bool) {
	for _, a := range m.anims {
		if a.name == name {
			return a.first + i, i >= 0 && i < a.frames
		}
	}
	return 0, false
}

// kindOf sets the kind and the slot or role that the map's ID names, and
// checks that the kind may exist in the map's view (§3.6).
func (m *pixmap) kindOf(r *report) bool {
	if !isID(m.id) {
		r.add(m.path, 0, "file name must be <slot-or-kind>.<name>.pxm in [a-z0-9_]")
		return false
	}
	first, name, _ := strings.Cut(m.id, ".")
	switch {
	case slices.Contains(slots, first):
		m.kind, m.slot = kindPart, first
	case first == kindOverlay:
		m.kind = kindOverlay
		if name != "trim" {
			r.add(m.path, 0, "the only overlay is overlay.trim")
			return false
		}
	case first == kindBanner:
		m.kind = kindBanner
		if name != "pennant" {
			r.add(m.path, 0, "the only banner is banner.pennant")
			return false
		}
	case first == kindBadge || first == kindTool:
		m.kind, m.role = first, name
		if !slices.Contains(roles, name) {
			r.add(m.path, 0, "%s %q names no role", first, name)
			return false
		}
	case first == kindTile || first == kindAnimal || first == kindEmblem:
		m.kind = first
	default:
		r.add(m.path, 0, "%q is no slot and no kind (overlay, badge, tile, animal, banner, emblem, tool)", first)
		return false
	}
	if !slices.Contains(kindViews[m.kind], m.view) {
		r.add(m.path, 0, "%s maps belong in the %s view only, not %s", m.kind, strings.Join(kindViews[m.kind], " and "), m.view)
		return false
	}
	return true
}

// refuse returns why character c may not appear in this map, or "".
func (m *pixmap) refuse(pal *palette, c byte) string {
	if _, ok := pal.texOf[c]; ok {
		if m.kind == kindTile && (m.category == "rock" || m.category == "floor") {
			return ""
		}
		return "textures only in rock and floor tiles"
	}
	i, ok := pal.glyphs[c]
	if !ok {
		return "unknown character"
	}
	e := pal.entries[i]
	switch m.kind {
	case kindPart:
		if e.mask == "role" || e.mask == "corps" {
			return "role and Corps characters belong to overlay.trim and banner.pennant"
		}
	case kindOverlay:
		if i != 0 && e.mask != "role" {
			return "overlay.trim uses transparent and role characters only"
		}
	case kindBadge, kindEmblem:
		if e.name != "transparent" && e.name != "outline" && e.name != "light" {
			return "badges and emblems use transparent, outline and light only"
		}
	case kindBanner:
		if e.mask != "" && e.mask != "corps" || i == pal.occlude {
			return "banner.pennant uses Corps and unmasked characters only, without occlude"
		}
	case kindTile, kindAnimal, kindTool:
		if e.mask != "" {
			return "mask characters only in parts, overlay.trim and banner.pennant"
		}
		if i == pal.occlude {
			return "occlude only in parts"
		}
	}
	return ""
}

// parsePixmap parses one pixel map (§3.6, §3.7). It returns nil after
// reporting a violation.
func parsePixmap(r *report, pal *palette, p, view, id string, lines []textLine) *pixmap {
	start := r.count()
	m := &pixmap{path: p, id: id, view: view, header: map[string]int{}}
	if !m.kindOf(r) {
		return nil
	}
	var framed bool
	var xLine, texLine int // first rows that use occlude or a texture
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if l.skip() {
			continue
		}
		f := strings.Fields(l.text)
		switch key := f[0]; key {
		case "canvas", "anchor", "occludes", "category", "seed":
			if framed {
				r.add(p, l.n, "%s after the first frame; header keywords come first", key)
				continue
			}
			if prev, dup := m.header[key]; dup {
				r.add(p, l.n, "%s already given on line %d", key, prev)
				continue
			}
			m.header[key] = l.n
			m.parseHeader(r, l, f)
		case "frame":
			if !framed {
				framed = true
				if !m.checkHeader(r) {
					return nil
				}
			}
			idx, ok := coord(f[len(f)-1])
			last := len(m.anims) - 1
			counted := false
			switch {
			case len(f) != 3 || !isName(f[1]) || !ok:
				r.add(p, l.n, "want frame <anim> <index>")
			case last >= 0 && m.anims[last].name == f[1]:
				if counted = idx == m.anims[last].frames; !counted {
					r.add(p, l.n, "frame %s %d; want index %d", f[1], idx, m.anims[last].frames)
				}
			case slices.ContainsFunc(m.anims, func(a anim) bool { return a.name == f[1] }):
				r.add(p, l.n, "animation %s is not contiguous", f[1])
			case idx != 0:
				r.add(p, l.n, "frame %s %d; want index 0", f[1], idx)
			default:
				m.anims = append(m.anims, anim{name: f[1], first: len(m.rows)})
				counted = true
			}
			// Rows are positional: the h lines after the header are the frame.
			before := r.count()
			var rows []string
			visible := false
			for k := range m.h {
				if i+1 >= len(lines) {
					r.add(p, l.n, "frame has %d of %d rows", k, m.h)
					break
				}
				i++
				row := lines[i]
				rows = append(rows, row.text)
				if len(row.text) != m.w {
					what := "short"
					if len(row.text) > m.w {
						what = "long"
					}
					r.add(p, row.n, "row too %s: %d of %d pixels", what, len(row.text), m.w)
					continue
				}
				for x := range m.w {
					c := row.text[x]
					if why := m.refuse(pal, c); why != "" {
						r.add(p, row.n, "character %q at x=%d: %s", c, x, why)
						break
					}
					if j, ok := pal.glyphs[c]; ok {
						visible = visible || !pal.clear(uint8(j))
						if j == pal.occlude && xLine == 0 {
							xLine = row.n
						}
					} else {
						visible = true
						if texLine == 0 {
							texLine = row.n
						}
					}
				}
			}
			if r.count() == before && !visible {
				r.add(p, l.n, "frame has no visible pixel")
			}
			if counted {
				m.anims[len(m.anims)-1].frames++
				m.rows = append(m.rows, rows)
				m.frameLines = append(m.frameLines, l.n)
			}
		default:
			r.add(p, l.n, "unknown keyword %q", key)
		}
	}
	if !framed {
		if m.checkHeader(r) {
			r.add(p, 0, "no frames")
		}
		return nil
	}
	if line, ok := m.header["occludes"]; ok && xLine == 0 {
		r.add(p, line, "occludes without occlude pixels")
	}
	if xLine > 0 && len(m.occludes) == 0 {
		r.add(p, xLine, "occlude pixels need an occludes header")
	}
	if line, ok := m.header["seed"]; ok && texLine == 0 {
		r.add(p, line, "seed without texture characters")
	}
	if texLine > 0 && !m.textured() {
		r.add(p, texLine, "texture characters need a seed header")
	}
	if r.count() > start {
		return nil
	}
	return m
}

// parseHeader parses one header keyword line.
func (m *pixmap) parseHeader(r *report, l textLine, f []string) {
	p := m.path
	switch f[0] {
	case "canvas", "anchor":
		a, ok1 := coord(f[min(1, len(f)-1)])
		b, ok2 := coord(f[len(f)-1])
		switch {
		case f[0] == "canvas" && (len(f) != 3 || !ok1 || !ok2 || a == 0 || b == 0):
			r.add(p, l.n, "want canvas <w> <h>, each 1-%d", maxCoord)
			return
		case len(f) != 3 || !ok1 || !ok2:
			r.add(p, l.n, "want anchor <x> <y>, each 0-%d", maxCoord)
			return
		}
		if f[0] == "canvas" {
			m.w, m.h = a, b
		} else {
			m.ax, m.ay = a, b
		}
	case "occludes":
		own := slices.Index(slots, m.slot)
		switch {
		case m.kind != kindPart:
			r.add(p, l.n, "occludes is for parts only")
		case len(f) < 2:
			r.add(p, l.n, "want occludes <slot> ...")
		default:
			for _, s := range f[1:] {
				i := slices.Index(slots, s)
				switch {
				case i < 0:
					r.add(p, l.n, "%q is not a slot", s)
				case i >= own:
					r.add(p, l.n, "slot %s is not below %s in layer order", s, m.slot)
				case slices.Contains(m.occludes, s):
					r.add(p, l.n, "slot %s listed twice", s)
				default:
					m.occludes = append(m.occludes, s)
				}
			}
			slices.SortFunc(m.occludes, func(a, b string) int { return slices.Index(slots, a) - slices.Index(slots, b) })
		}
	case "category":
		switch {
		case m.kind != kindTile:
			r.add(p, l.n, "category is for tiles only")
		case len(f) != 2 || !slices.Contains(categories, f[1]):
			r.add(p, l.n, "want category <%s>", strings.Join(categories, "|"))
		default:
			m.category = f[1]
		}
	case "seed":
		v, ok := number(f[len(f)-1], 1<<32-1)
		if len(f) != 2 || !ok {
			r.add(p, l.n, "want seed <0-4294967295>")
			return
		}
		m.seed = uint32(v)
	}
}

// checkHeader checks the header once the frames start, and reports whether
// the canvas is known so that the frames can be read.
func (m *pixmap) checkHeader(r *report) bool {
	p := m.path
	if m.w == 0 {
		if _, ok := m.header["canvas"]; !ok {
			r.add(p, 0, "missing canvas")
		}
		return false
	}
	if line, ok := m.header["anchor"]; !ok {
		r.add(p, 0, "missing anchor")
	} else if m.ax > m.w || m.ay > m.h {
		r.add(p, line, "anchor (%d,%d) outside the %dx%d canvas", m.ax, m.ay, m.w, m.h)
	}
	if _, ok := m.header["category"]; m.kind == kindTile && !ok {
		r.add(p, 0, "tiles need a category")
	}
	return true
}

// Bounds of a three-quarter frame duration. GIF delays count hundredths of a
// second, so durations are multiples of 10 ms. These are generator input
// rules; ticket 107 may set the pack's own.
const (
	minFrameMs = 50
	maxFrameMs = 2000
)

// loop is one animation line of animations.txt: the three-quarter view's
// animation and its frame durations in milliseconds.
type loop struct {
	name string
	ms   []int
}

// parseAnimations parses animations.txt (§3.4 of the round-2 brief).
func parseAnimations(r *report, p string, lines []textLine) []loop {
	var out []loop
	first := true
	for _, l := range lines {
		if l.skip() {
			continue
		}
		f := strings.Fields(l.text)
		if f[0] != "anim" {
			r.add(p, l.n, "unknown keyword %q; want anim", f[0])
			continue
		}
		if len(f) < 2 {
			r.add(p, l.n, "want anim <name> <ms> <ms> ...")
			continue
		}
		isFirst := first
		first = false
		name := f[1]
		role, act := strings.CutPrefix(name, "act_")
		switch {
		case isFirst && name != "idle":
			r.add(p, l.n, "the first loop must be idle, not %s", name)
			continue
		case !isFirst && (!act || !slices.Contains(roles, role)):
			r.add(p, l.n, "%s is no act_<role> loop", name)
			continue
		case slices.ContainsFunc(out, func(o loop) bool { return o.name == name }):
			r.add(p, l.n, "loop %s is already defined", name)
			continue
		case len(f) < 4:
			r.add(p, l.n, "loop %s has %d frames; a loop has at least 2", name, len(f)-2)
			continue
		}
		lp := loop{name: name}
		for _, s := range f[2:] {
			v, ok := number(s, maxFrameMs)
			if !ok || v < minFrameMs || v%10 != 0 {
				r.add(p, l.n, "duration %q; want a multiple of 10 from %d to %d ms", s, minFrameMs, maxFrameMs)
				break
			}
			lp.ms = append(lp.ms, int(v))
		}
		if len(lp.ms) == len(f)-2 {
			out = append(out, lp)
		}
	}
	if first {
		r.add(p, 0, "no anim line")
	}
	return out
}

// corpsExample is one example Corps of roster.txt: a colour choice and an
// emblem sprite.
type corpsExample struct {
	name, color, emblem string
	line                int
}

// member is one roster dwarf of roster.txt.
type member struct {
	outfit, role, corps string
	line                int
}

// roster is roster.txt: sheet examples, not pack data.
type roster struct {
	corps   []*corpsExample
	members []member
}

// corpsNamed returns the example Corps called name, or nil.
func (rs *roster) corpsNamed(name string) *corpsExample {
	for _, c := range rs.corps {
		if c.name == name {
			return c
		}
	}
	return nil
}

// parseRoster parses roster.txt (§3.5 of the round-2 brief).
func parseRoster(r *report, p string, lines []textLine) *roster {
	rs := &roster{}
	members := false // a member line has been read
	for _, l := range lines {
		if l.skip() {
			continue
		}
		f := strings.Fields(l.text)
		switch f[0] {
		case "corps":
			color, ok1 := strings.CutPrefix(f[min(2, len(f)-1)], "color=")
			emblem, ok2 := strings.CutPrefix(f[len(f)-1], "emblem=")
			switch {
			case members:
				r.add(p, l.n, "corps after member; corps lines come first")
			case len(f) != 4 || !ok1 || !ok2 || !isName(f[1]):
				r.add(p, l.n, "want corps <name> color=<corps choice> emblem=<emblem id>")
			case !isID(color) || !strings.HasPrefix(color, "corps."):
				r.add(p, l.n, "color %q is no corps.<name> choice ID", color)
			case !isID(emblem) || !strings.HasPrefix(emblem, kindEmblem+"."):
				r.add(p, l.n, "emblem %q is no emblem.<name> ID", emblem)
			case rs.corpsNamed(f[1]) != nil:
				r.add(p, l.n, "corps %s is already defined", f[1])
			default:
				rs.corps = append(rs.corps, &corpsExample{f[1], color, emblem, l.n})
			}
		case "member":
			members = true
			switch {
			case len(f) != 4 || !isName(f[1]):
				r.add(p, l.n, "want member <outfit> <role> <corps>")
			case !slices.Contains(roles, f[2]):
				r.add(p, l.n, "role %q; want one of %s", f[2], strings.Join(roles, ", "))
			case rs.corpsNamed(f[3]) == nil:
				r.add(p, l.n, "corps %s is not defined above", f[3])
			case slices.ContainsFunc(rs.members, func(m member) bool { return m.outfit == f[1] }):
				r.add(p, l.n, "outfit %s already has a member", f[1])
			default:
				rs.members = append(rs.members, member{f[1], f[2], f[3], l.n})
			}
		default:
			r.add(p, l.n, "unknown keyword %q; want corps or member", f[0])
		}
	}
	if len(rs.members) == 0 {
		r.add(p, 0, "no member")
	}
	return rs
}
