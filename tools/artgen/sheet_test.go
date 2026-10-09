package main

import (
	"bytes"
	"image"
	"image/color"
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

// luma is the gray copy's formula (§3.13).
func luma(c color.NRGBA) color.NRGBA {
	y := uint8((299*int(c.R) + 587*int(c.G) + 114*int(c.B) + 500) / 1000)
	return color.NRGBA{y, y, y, c.A}
}

// The sheet constants of §3.17, restated so that a changed constant fails.
const sheetZoom = 4

var cellColor = color.NRGBA{0x8c, 0x8c, 0x98, 0xff}

func TestSheetsIndexEveryFrame(t *testing.T) {
	out := outputs(t)
	doc := sampleOf(t, out)
	s := loaded(t)

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

	wantSections := map[string][]string{
		"map":       {"tiles", "animals", "parts", "roles", "roles-varied", "roles-gray", "room"},
		"character": {"parts", "examples"},
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

	// Every frame of every sprite, cut from its strip.
	type frameRef struct {
		img    *image.NRGBA
		anchor image.Point
	}
	frames := map[string]frameRef{} // "<grid> <id> <anim> <frame>"
	partAnchor := map[string]image.Point{}
	for _, sp := range doc.Sprites {
		for _, im := range sp.Images {
			strip := decodeNRGBA(t, out[im.Path])
			for _, a := range im.Animations {
				for k, r := range a.Frames {
					key := im.Grid + " " + sp.ID + " " + a.Name + " " + strconv.Itoa(k)
					frames[key] = frameRef{crop(strip, image.Rect(r[0], r[1], r[0]+r[2], r[1]+r[3])), image.Pt(im.Anchor[0], im.Anchor[1])}
				}
			}
			if sp.Kind == kindPart {
				partAnchor[im.Grid] = image.Pt(im.Anchor[0], im.Anchor[1])
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

	seen := map[string]int{}
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
		case f[0] == "dwarf" && c.sheet == "character" && len(f) == 4:
			checkCell(c, s.compose(gridRPG, frame(f[3]), s.outfit[f[1]], ""), same)
		case (f[0] == "dwarf" || f[0] == "dwarf-gray") && c.sheet == "map" && len(f) == 5:
			tone := same
			if f[0] == "dwarf-gray" {
				tone = luma
			}
			checkCell(c, s.compose(gridWorld, frame(f[4]), s.outfit[f[1]], f[2]), tone)
		default:
			grid := gridWorld
			if c.sheet == "character" {
				grid = gridRPG
			}
			key := grid + " " + c.content
			ref, ok := frames[key]
			if !ok {
				t.Errorf("%s cell %q names no %s frame", c.sheet, c.content, grid)
				continue
			}
			seen[key]++
			checkCell(c, ref.img, same)
		}
	}
	for key := range frames {
		if seen[key] != 1 {
			t.Errorf("frame %s appears %d times on its sheet; want once", key, seen[key])
		}
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
	for _, pl := range s.room.placements {
		var img *image.NRGBA
		var anchor image.Point
		if pl.dwarf {
			img, anchor = s.compose(gridWorld, pl.frame, s.outfit[pl.outfit], pl.role), partAnchor[gridWorld]
		} else {
			ref := frames[gridWorld+" "+pl.sprite+" "+pl.anim+" "+strconv.Itoa(pl.frame)]
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
