package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// indexCell is one line of sheets/index.tsv.
type indexCell struct {
	sheet, section string
	r              image.Rectangle
	content        string
}

// decodeNRGBA decodes a PNG into an NRGBA image.
func decodeNRGBA(t *testing.T, data []byte) *image.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewNRGBA(img.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			out.SetNRGBA(x, y, color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA))
		}
	}
	return out
}

// crop returns rectangle r of img with its origin at (0,0).
func crop(img *image.NRGBA, r image.Rectangle) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := range r.Dy() {
		for x := range r.Dx() {
			out.SetNRGBA(x, y, img.NRGBAAt(r.Min.X+x, r.Min.Y+y))
		}
	}
	return out
}

// grayTone is the gray copy's formula (§3.13).
func grayTone(c color.NRGBA) color.NRGBA {
	y := uint8((299*int(c.R) + 587*int(c.G) + 114*int(c.B) + 500) / 1000)
	return color.NRGBA{y, y, y, c.A}
}

// The sheet constants of §3.17, restated so that a changed constant fails.
const sheetZoom = 4

var cellColor = color.NRGBA{0x8c, 0x8c, 0x98, 0xff}

// readIndex parses sheets/index.tsv.
func readIndex(t *testing.T, out map[string][]byte) []indexCell {
	t.Helper()
	ls := strings.Split(strings.TrimSuffix(string(out["sheets/index.tsv"]), "\n"), "\n")
	if ls[0] != "sheet\tsection\tx\ty\tw\th\tcontent" {
		t.Fatalf("index.tsv header %q", ls[0])
	}
	var cells []indexCell
	for _, l := range ls[1:] {
		f := strings.Split(l, "\t")
		if len(f) != 7 {
			t.Fatalf("index.tsv line %q has %d fields; want 7", l, len(f))
		}
		var n [4]int
		for i := range n {
			v, err := strconv.Atoi(f[2+i])
			if err != nil {
				t.Fatalf("index.tsv line %q: %v", l, err)
			}
			n[i] = v
		}
		cells = append(cells, indexCell{f[0], f[1], image.Rect(n[0], n[1], n[0]+n[2], n[1]+n[3]), f[6]})
	}
	return cells
}

func TestSheetsIndexEveryFrame(t *testing.T) {
	out := outputs(t)
	doc := sampleOf(t, out)
	s := loaded(t)
	cells := readIndex(t, out)

	sheets := map[string]*image.NRGBA{}
	for _, name := range []string{"map", "character"} {
		small := decodeNRGBA(t, out["sheets/"+name+"-1x.png"])
		big := decodeNRGBA(t, out["sheets/"+name+"-4x.png"])
		sheets[name] = small
		if big.Rect.Size() != small.Rect.Size().Mul(sheetZoom) {
			t.Errorf("%s-4x is %v; want 4 x %v", name, big.Rect.Size(), small.Rect.Size())
			continue
		}
	zoomed:
		for y := range big.Rect.Dy() {
			for x := range big.Rect.Dx() {
				if big.NRGBAAt(x, y) != small.NRGBAAt(x/sheetZoom, y/sheetZoom) {
					t.Errorf("%s-4x (%d,%d) is not the 1x pixel (%d,%d) enlarged", name, x, y, x/sheetZoom, y/sheetZoom)
					break zoomed
				}
			}
		}
	}
	frameCount := 0
	for _, l := range readAnimations(t) {
		frameCount += len(l.ms)
	}
	if got, want := sheets["character"].Rect.Dx(), 4+68*frameCount; got != want {
		t.Errorf("character sheet is %d px wide; want 4 + 68 x %d frames = %d", got, frameCount, want)
	}

	wantSections := map[string][]string{
		"map":       {"tiles", "animals", "parts", "roles", "roles-varied", "roles-gray", "room"},
		"character": {"front", "three_quarter", "corps", "roster", "roster-gray", "page"},
	}
	gotSections := map[string][]string{}
	for i, c := range cells {
		sheet, ok := sheets[c.sheet]
		if !ok {
			t.Fatalf("cell %d names sheet %q", i, c.sheet)
		}
		if secs := gotSections[c.sheet]; len(secs) == 0 || secs[len(secs)-1] != c.section {
			gotSections[c.sheet] = append(secs, c.section)
		}
		if !c.r.In(sheet.Rect) {
			t.Errorf("%s cell %v (%s) lies outside the %v sheet", c.sheet, c.r, c.content, sheet.Rect)
		}
		for _, d := range cells[:i] {
			if d.sheet == c.sheet && d.r.Overlaps(c.r) {
				t.Errorf("%s cells %q at %v and %q at %v overlap", c.sheet, d.content, d.r, c.content, c.r)
			}
		}
	}
	for name, want := range wantSections {
		if !slices.Equal(gotSections[name], want) {
			t.Errorf("%s sheet sections %v; want %v", name, gotSections[name], want)
		}
	}

	// Every frame of every sprite, cut from its strip by its rectangle and
	// keyed "<view> <id> <anim> <frame>".
	type frameRef struct {
		img    *image.NRGBA
		anchor image.Point
	}
	frames := map[string]frameRef{}
	for _, sp := range doc.Sprites {
		for _, im := range sp.Images {
			strip := decodeNRGBA(t, out[im.Path])
			for _, a := range im.Animations {
				for k, r := range a.Frames {
					key := fmt.Sprintf("%s %s %s %d", im.View, sp.ID, a.Name, k)
					frames[key] = frameRef{crop(strip, image.Rect(r[0], r[1], r[0]+r[2], r[1]+r[3])), image.Pt(im.Anchor[0], im.Anchor[1])}
				}
			}
		}
	}

	// checkCell compares a cell with img's opaque pixels over the cell
	// background, after applying tone.
	checkCell := func(c indexCell, img *image.NRGBA, tone func(color.NRGBA) color.NRGBA) {
		t.Helper()
		if img.Rect.Size() != c.r.Size() {
			t.Errorf("%s cell %q is %v; its image is %v", c.sheet, c.content, c.r.Size(), img.Rect.Size())
			return
		}
		sheet := sheets[c.sheet]
		for y := range c.r.Dy() {
			for x := range c.r.Dx() {
				want := cellColor
				if px := img.NRGBAAt(x, y); px.A == 0xff {
					want = tone(px)
				}
				if got := sheet.NRGBAAt(c.r.Min.X+x, c.r.Min.Y+y); got != want {
					t.Errorf("%s cell %q pixel (%d,%d) = %v; want %v", c.sheet, c.content, x, y, got, want)
					return
				}
			}
		}
	}
	same := func(c color.NRGBA) color.NRGBA { return c }
	toneOf := func(word string) func(color.NRGBA) color.NRGBA {
		if strings.HasSuffix(word, "-gray") {
			return grayTone
		}
		return same
	}

	seen := map[string]int{}
	rows := map[string][]indexCell{} // a three-quarter sprite's or a member's cells
	var room *indexCell
	for i, c := range cells {
		f := strings.Fields(c.content)
		frame := func(s string) int {
			n, err := strconv.Atoi(s)
			if err != nil {
				t.Fatalf("cell %q: %v", c.content, err)
			}
			return n
		}
		switch {
		case c.content == "room":
			room = &cells[i]
		case c.sheet == "map" && (f[0] == "dwarf" || f[0] == "dwarf-gray"):
			checkCell(c, s.compose(viewOverworld, f[3], frame(f[4]), s.outfit[f[1]], f[2], nil), toneOf(f[0]))
		case c.section == "corps":
			checkCell(c, s.marker(f[2], f[3], frame(f[4]), s.roster.corpsNamed(f[1])), toneOf(f[0]))
		case c.section == "roster" || c.section == "roster-gray" || c.section == "page":
			view := viewFront
			if c.section == "page" {
				view = viewThreeQuarter
			}
			if f[4] != view {
				t.Errorf("%s cell %q is in view %s; want %s", c.section, c.content, f[4], view)
			}
			checkCell(c, s.compose(f[4], f[5], frame(f[6]), s.outfit[f[1]], f[2], s.roster.corpsNamed(f[3])), toneOf(f[0]))
			if c.section == "page" {
				rows["page "+f[1]] = append(rows["page "+f[1]], c)
			}
		default:
			key := c.content
			if c.sheet == "map" {
				key = viewOverworld + " " + key
			} else if f[0] != c.section {
				t.Errorf("character cell %q lies in section %s", c.content, c.section)
			}
			ref, ok := frames[key]
			if !ok {
				t.Errorf("%s cell %q names no frame", c.sheet, c.content)
				continue
			}
			seen[key]++
			checkCell(c, ref.img, same)
			if f[0] == viewThreeQuarter {
				rows[f[1]] = append(rows[f[1]], c)
			}
		}
	}
	for key := range frames {
		if seen[key] != 1 {
			t.Errorf("frame %s appears %d times on its sheet; want once", key, seen[key])
		}
	}
	// A three-quarter sprite's frames share one row in strip order; a
	// member's page cells share one row, idle frames first.
	for name, row := range rows {
		for i, c := range row {
			if c.r.Min.Y != row[0].r.Min.Y || i > 0 && c.r.Min.X <= row[i-1].r.Min.X {
				t.Errorf("%s: cell %q at %v leaves the row of %q at %v", name, c.content, c.r.Min, row[0].content, row[0].r.Min)
			}
		}
		if strings.HasPrefix(name, "page ") && !strings.Contains(row[0].content, " idle 0") {
			t.Errorf("%s starts with %q; want its idle frames first", name, row[0].content)
		}
	}
	if len(s.roster.members) == 0 || len(rows) != len(s.roster.members)+len(s.sprites[viewThreeQuarter]) {
		t.Errorf("%d page and three-quarter rows; want one per member and per three-quarter sprite", len(rows))
	}

	if room == nil {
		t.Fatal("index.tsv has no room cell")
	}
	// Each placement's opaque pixels, in room coordinates, by file order.
	w, h := s.room.cols*cell, s.room.rows*cell
	type placed struct {
		img  *image.NRGBA
		at   image.Point
		line int
	}
	var all []placed
	ow := viewOf(viewOverworld)
	for _, pl := range s.room.placements {
		var img *image.NRGBA
		var anchor image.Point
		if pl.dwarf {
			img, anchor = s.compose(viewOverworld, "idle", pl.frame, s.outfit[pl.outfit], pl.role, nil), image.Pt(ow.ax, ow.ay)
		} else {
			ref := frames[fmt.Sprintf("%s %s %s %d", viewOverworld, pl.sprite, pl.anim, pl.frame)]
			img, anchor = ref.img, ref.anchor
		}
		all = append(all, placed{img, image.Pt(pl.col*cell+cell/2, pl.row*cell+cell).Sub(anchor), pl.line})
	}
	opaque := func(p placed, x, y int) bool {
		q := image.Pt(x, y).Sub(p.at)
		return q.In(p.img.Rect) && p.img.NRGBAAt(q.X, q.Y).A == 0xff
	}
	sheet := sheets["map"]
	for i, p := range all {
		for y := range p.img.Rect.Dy() {
			for x := range p.img.Rect.Dx() {
				px := p.img.NRGBAAt(x, y)
				rx, ry := p.at.X+x, p.at.Y+y
				if px.A != 0xff || rx < 0 || ry < 0 || rx >= w || ry >= h {
					continue
				}
				if slices.ContainsFunc(all[i+1:], func(q placed) bool { return opaque(q, rx, ry) }) {
					continue
				}
				if got := sheet.NRGBAAt(room.r.Min.X+rx, room.r.Min.Y+ry); got != px {
					t.Errorf("room.txt:%d: room pixel (%d,%d) = %v; want the placed sprite's %v", p.line, rx, ry, got, px)
				}
			}
		}
	}
}

func TestLoopPreviewsMatchPageCells(t *testing.T) {
	out := outputs(t)
	doc := sampleOf(t, out)
	s := loaded(t)
	big := decodeNRGBA(t, out["sheets/character-4x.png"])
	pages := map[string]image.Rectangle{}
	for _, c := range readIndex(t, out) {
		if c.section == "page" {
			pages[c.content] = c.r
		}
	}
	durations := map[string][]int{}
	for _, v := range doc.Views {
		if v.View == viewThreeQuarter {
			for _, a := range v.Animations {
				durations[a.Name] = a.DurationsMs
			}
		}
	}

	var want []string
	for _, m := range s.roster.members {
		for _, an := range []string{"idle", "act_" + m.role} {
			ms, ok := durations[an]
			if !ok {
				continue
			}
			p := fmt.Sprintf("sheets/page-%s-%s-4x.gif", m.outfit, an)
			want = append(want, p)
			g, err := gif.DecodeAll(bytes.NewReader(out[p]))
			if err != nil {
				t.Errorf("%s: %v", p, err)
				continue
			}
			if len(g.Image) != len(ms) || g.LoopCount != 0 {
				t.Errorf("%s has %d frames and loop count %d; want %d frames looping forever", p, len(g.Image), g.LoopCount, len(ms))
				continue
			}
			for k, frame := range g.Image {
				if g.Delay[k]*10 != ms[k] {
					t.Errorf("%s frame %d delay %d cs; animations.txt gives %d ms", p, k, g.Delay[k], ms[k])
				}
				r, ok := pages[fmt.Sprintf("dwarf %s %s %s %s %s %d", m.outfit, m.role, m.corps, viewThreeQuarter, an, k)]
				if !ok {
					t.Errorf("%s frame %d has no page cell", p, k)
					continue
				}
				cellImg := crop(big, image.Rectangle{r.Min.Mul(sheetZoom), r.Max.Mul(sheetZoom)})
				if frame.Rect.Size() != cellImg.Rect.Size() {
					t.Errorf("%s frame %d is %v; the 4x page cell is %v", p, k, frame.Rect.Size(), cellImg.Rect.Size())
					continue
				}
			pixels:
				for y := range frame.Rect.Dy() {
					for x := range frame.Rect.Dx() {
						got := color.NRGBAModel.Convert(frame.At(frame.Rect.Min.X+x, frame.Rect.Min.Y+y)).(color.NRGBA)
						if got != cellImg.NRGBAAt(x, y) {
							t.Errorf("%s frame %d (%d,%d) = %v; the 4x page cell has %v", p, k, x, y, got, cellImg.NRGBAAt(x, y))
							break pixels
						}
					}
				}
			}
		}
	}
	var got []string
	for p := range out {
		if strings.HasSuffix(p, ".gif") {
			got = append(got, p)
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if len(want) == 0 || !slices.Equal(got, want) {
		t.Errorf("previews %v; want one per member and loop: %v", got, want)
	}
}
