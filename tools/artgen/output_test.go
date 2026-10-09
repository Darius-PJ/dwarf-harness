package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// sampleDoc is sample.json as a reader such as ticket 107 sees it.
type sampleDoc struct {
	ContentSha256 string `json:"contentSha256"`
	Views         []struct {
		View       string   `json:"view"`
		Grid       string   `json:"grid"`
		Facing     string   `json:"facing"`
		Canvas     [2]int   `json:"canvas"`
		Anchor     [2]int   `json:"anchor"`
		Layers     []string `json:"layers"`
		Animations []struct {
			Name        string `json:"name"`
			FrameCount  int    `json:"frameCount"`
			DurationsMs []int  `json:"durationsMs"`
		} `json:"animations"`
		States []struct {
			State     string `json:"state"`
			Role      string `json:"role"`
			Animation string `json:"animation"`
			Tool      string `json:"tool"`
		} `json:"states"`
	} `json:"views"`
	Palette []struct {
		Index int    `json:"index"`
		Name  string `json:"name"`
		RGBA  string `json:"rgba"`
	} `json:"palette"`
	Masks []struct {
		Mask    string `json:"mask"`
		Indices []int  `json:"indices"`
	} `json:"masks"`
	CorpsMarker struct {
		Banner  string   `json:"banner"`
		Mask    string   `json:"mask"`
		Emblems []string `json:"emblems"`
	} `json:"corpsMarker"`
	Sprites []struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Role   string `json:"role"`
		Images []struct {
			Grid       string   `json:"grid"`
			View       string   `json:"view"`
			Path       string   `json:"path"`
			Sha256     string   `json:"sha256"`
			Canvas     [2]int   `json:"canvas"`
			Anchor     [2]int   `json:"anchor"`
			Masks      []string `json:"masks"`
			Animations []struct {
				Name   string   `json:"name"`
				Frames [][4]int `json:"frames"`
			} `json:"animations"`
		} `json:"images"`
	} `json:"sprites"`
}

func sampleOf(t *testing.T, out map[string][]byte) sampleDoc {
	t.Helper()
	var doc sampleDoc
	if err := json.Unmarshal(out["sample.json"], &doc); err != nil {
		t.Fatalf("sample.json: %v", err)
	}
	return doc
}

func hexSum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// isSpritePNG reports whether p is a sprite strip of the sample.
func isSpritePNG(p string) bool {
	return (strings.HasPrefix(p, "world/") || strings.HasPrefix(p, "rpg/")) && strings.HasSuffix(p, ".png")
}

// The view table of §3.1 of the round-2 brief, restated so that a changed
// constant fails.
var wantViews = []struct {
	view, grid, dir, facing string
	canvas, anchor          [2]int
	markers                 []string
}{
	{"overworld", "world", "world", "front", [2]int{16, 16}, [2]int{8, 16}, []string{"overlay", "badge"}},
	{"front", "rpg", "rpg/front", "front", [2]int{64, 64}, [2]int{32, 64}, []string{"banner", "emblem"}},
	{"three_quarter", "rpg", "rpg/three_quarter", "three_quarter_right", [2]int{64, 64}, [2]int{32, 64}, []string{"banner", "emblem", "tool"}},
}

// animLine is one anim line of animations.txt, read without the generator.
type animLine struct {
	name string
	ms   []int
}

func readAnimations(t *testing.T) []animLine {
	t.Helper()
	data, err := os.ReadFile("art/animations.txt")
	if err != nil {
		t.Fatal(err)
	}
	var out []animLine
	for _, l := range strings.Split(string(data), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || f[0] != "anim" {
			continue
		}
		a := animLine{name: f[1]}
		for _, s := range f[2:] {
			v, err := strconv.Atoi(s)
			if err != nil {
				t.Fatal(err)
			}
			a.ms = append(a.ms, v)
		}
		out = append(out, a)
	}
	return out
}

func TestMetadataDescribesImages(t *testing.T) {
	out := outputs(t)
	doc := sampleOf(t, out)
	loops := readAnimations(t)

	palette := make([]color.NRGBA, len(doc.Palette))
	var corpsEntries []int
	for i, e := range doc.Palette {
		v, err := strconv.ParseUint(e.RGBA, 16, 32)
		if e.Index != i || err != nil || len(e.RGBA) != 8 {
			t.Fatalf("palette entry %d = %+v; want index %d and rrggbbaa", i, e, i)
		}
		palette[i] = color.NRGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}
		if strings.HasPrefix(e.Name, "corps.") {
			corpsEntries = append(corpsEntries, i)
		}
	}
	maskOf := map[int]string{}
	var corpsMask []int
	for _, m := range doc.Masks {
		for _, i := range m.Indices {
			maskOf[i] = m.Mask
		}
		if m.Mask == "corps" {
			corpsMask = m.Indices
		}
	}
	if len(corpsEntries) == 0 || !slices.Equal(corpsMask, corpsEntries) {
		t.Errorf("mask corps has indices %v; the corps entries are %v", corpsMask, corpsEntries)
	}

	// The animation list every layer of a view has: the alphabetically first
	// overworld part's, the front still, and animations.txt's loops.
	wantAnims := map[string]string{"front": "still 1"}
	var tq []string
	for _, l := range loops {
		tq = append(tq, fmt.Sprintf("%s %d", l.name, len(l.ms)))
	}
	wantAnims["three_quarter"] = strings.Join(tq, ", ")
	for _, sp := range doc.Sprites {
		if sp.Kind != kindPart {
			continue
		}
		for _, im := range sp.Images {
			if im.View == "overworld" && wantAnims["overworld"] == "" {
				var list []string
				for _, a := range im.Animations {
					list = append(list, fmt.Sprintf("%s %d", a.Name, len(a.Frames)))
				}
				wantAnims["overworld"] = strings.Join(list, ", ")
			}
		}
	}

	if len(doc.Views) != len(wantViews) {
		t.Fatalf("views %+v; want %d views", doc.Views, len(wantViews))
	}
	for i, w := range wantViews {
		v := doc.Views[i]
		if v.View != w.view || v.Grid != w.grid || v.Facing != w.facing || v.Canvas != w.canvas || v.Anchor != w.anchor {
			t.Errorf("view %d = %s %s %s %v %v; want %s %s %s %v %v", i, v.View, v.Grid, v.Facing, v.Canvas, v.Anchor,
				w.view, w.grid, w.facing, w.canvas, w.anchor)
		}
		if want := append(slices.Clone(slots), w.markers...); !slices.Equal(v.Layers, want) {
			t.Errorf("view %s layers %v; want %v", v.View, v.Layers, want)
		}
		var list []string
		for j, a := range v.Animations {
			list = append(list, fmt.Sprintf("%s %d", a.Name, a.FrameCount))
			switch {
			case v.View == "three_quarter" && (j >= len(loops) || !slices.Equal(a.DurationsMs, loops[j].ms)):
				t.Errorf("view three_quarter %s durations %v; animations.txt gives %v", a.Name, a.DurationsMs, loops[min(j, len(loops)-1)].ms)
			case v.View != "three_quarter" && a.DurationsMs != nil:
				t.Errorf("view %s %s has durations %v; only animations.txt sets durations", v.View, a.Name, a.DurationsMs)
			}
		}
		if got := strings.Join(list, ", "); got != wantAnims[v.View] {
			t.Errorf("view %s animations %q; want %q", v.View, got, wantAnims[v.View])
		}
		if v.View != "three_quarter" {
			if v.States != nil {
				t.Errorf("view %s has states %+v; only three_quarter plays loops by state", v.View, v.States)
			}
			continue
		}
		var states []string
		for _, s := range v.States {
			states = append(states, fmt.Sprintf("%s/%s/%s/%s", s.State, s.Role, s.Animation, s.Tool))
		}
		wantStates := []string{"idle//idle/"}
		for _, l := range loops[1:] {
			role := strings.TrimPrefix(l.name, "act_")
			tool := ""
			if _, ok := out["rpg/three_quarter/tool."+role+".png"]; ok {
				tool = "tool." + role
			}
			wantStates = append(wantStates, fmt.Sprintf("working/%s/%s/%s", role, l.name, tool))
		}
		if !slices.Equal(states, wantStates) {
			t.Errorf("three_quarter states %v; want %v", states, wantStates)
		}
	}

	viewOfName := map[string]int{}
	for i, w := range wantViews {
		viewOfName[w.view] = i
	}
	var emblems []string
	listed := map[string]bool{}
	for _, sp := range doc.Sprites {
		var views []string
		for _, im := range sp.Images {
			views = append(views, im.View)
			if listed[im.Path] {
				t.Errorf("%s is listed twice", im.Path)
			}
			listed[im.Path] = true
			vi, ok := viewOfName[im.View]
			if !ok {
				t.Errorf("%s names view %q", im.Path, im.View)
				continue
			}
			w := wantViews[vi]
			if path.Dir(im.Path) != w.dir || im.Grid != w.grid {
				t.Errorf("%s: view %s grid %s; the %s view's strips are %s/*.png on grid %s", im.Path, im.View, im.Grid, w.view, w.dir, w.grid)
			}
			data, ok := out[im.Path]
			if !ok {
				t.Errorf("%s is listed but not written", im.Path)
				continue
			}
			if hexSum(data) != im.Sha256 {
				t.Errorf("%s: sha256 %s; the file hashes to %s", im.Path, im.Sha256, hexSum(data))
			}
			decoded, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Errorf("%s: %v", im.Path, err)
				continue
			}
			img, ok := decoded.(*image.Paletted)
			if !ok {
				t.Errorf("%s decodes as %T; want an indexed image", im.Path, decoded)
				continue
			}
			if len(img.Palette) != len(palette) {
				t.Errorf("%s has %d palette entries; sample.json lists %d", im.Path, len(img.Palette), len(palette))
				continue
			}
			for i, c := range img.Palette {
				if got := color.NRGBAModel.Convert(c).(color.NRGBA); got != palette[i] {
					t.Errorf("%s palette entry %d = %v; sample.json has %v", im.Path, i, got, palette[i])
				}
			}

			w2, h := im.Canvas[0], im.Canvas[1]
			var rects [][4]int
			var list []string
			for _, a := range im.Animations {
				rects = append(rects, a.Frames...)
				list = append(list, fmt.Sprintf("%s %d", a.Name, len(a.Frames)))
			}
			if got, want := img.Rect.Size(), image.Pt(w2*len(rects), h); got != want {
				t.Errorf("%s is %v; want canvas width x %d frames = %v", im.Path, got, len(rects), want)
				continue
			}
			used := map[string]bool{}
			for i, r := range rects {
				if r != [4]int{i * w2, 0, w2, h} {
					t.Errorf("%s frame %d = %v; want %v", im.Path, i, r, [4]int{i * w2, 0, w2, h})
					continue
				}
				visible := false
				for y := range h {
					for x := range w2 {
						idx := img.ColorIndexAt(r[0]+x, y)
						visible = visible || palette[idx].A == 0xff
						if m, ok := maskOf[int(idx)]; ok {
							used[m] = true
						}
					}
				}
				if !visible {
					t.Errorf("%s frame %d has no visible pixel", im.Path, i)
				}
			}
			var wantMasks []string
			for _, m := range masks {
				if used[m] {
					wantMasks = append(wantMasks, m)
				}
			}
			if !slices.Equal(im.Masks, wantMasks) {
				t.Errorf("%s masks %v; its pixels use %v", im.Path, im.Masks, wantMasks)
			}

			// Registration per view (§3.7 of the round-2 brief).
			want := wantAnims[im.View]
			switch sp.Kind {
			case kindTool:
				want = ""
				for _, l := range loops {
					if l.name == "act_"+sp.Role {
						want = fmt.Sprintf("%s %d", l.name, len(l.ms))
					}
				}
			case kindPart, kindOverlay, kindBadge, kindBanner, kindEmblem:
			default:
				continue
			}
			if im.Canvas != w.canvas || im.Anchor != w.anchor || strings.Join(list, ", ") != want {
				t.Errorf("%s registers as %v %v %v; every %s layer has %v %v %s", im.Path, im.Canvas, im.Anchor, list, im.View, w.canvas, w.anchor, want)
			}
		}
		if !slices.IsSortedFunc(views, func(a, b string) int { return viewOfName[a] - viewOfName[b] }) {
			t.Errorf("%s images in views %v; want view-table order", sp.ID, views)
		}
		if sp.Kind == kindEmblem {
			emblems = append(emblems, sp.ID)
			if !slices.Equal(views, []string{"front", "three_quarter"}) {
				t.Errorf("%s has images in %v; an emblem has one in each RPG view", sp.ID, views)
			}
		}
		if sp.Kind == kindBanner && !slices.Equal(views, []string{"front", "three_quarter"}) {
			t.Errorf("%s has images in %v; the banner has one in each RPG view", sp.ID, views)
		}
	}
	if cm := doc.CorpsMarker; cm.Banner != "banner.pennant" || cm.Mask != "corps" || len(emblems) == 0 || !slices.Equal(cm.Emblems, emblems) {
		t.Errorf("corpsMarker %+v; want banner.pennant, mask corps and the emblem sprites %v", cm, emblems)
	}

	var pngs []string
	for p := range out {
		if isSpritePNG(p) {
			pngs = append(pngs, p)
			if !listed[p] {
				t.Errorf("%s is written but not listed in sample.json", p)
			}
		}
	}
	slices.Sort(pngs)
	var content strings.Builder
	for _, p := range pngs {
		fmt.Fprintf(&content, "%s  %s\n", hexSum(out[p]), p)
	}
	if got := hexSum([]byte(content.String())); doc.ContentSha256 != got {
		t.Errorf("contentSha256 %s; the sprite PNGs hash to %s", doc.ContentSha256, got)
	}
}

func TestProvenanceListsEveryInputAndOutput(t *testing.T) {
	out := outputs(t)
	type file struct {
		Path   string `json:"path"`
		Sha256 string `json:"sha256"`
	}
	var prov struct {
		Licence   file `json:"licence"`
		Generator struct {
			Package      string `json:"package"`
			SourceSha256 string `json:"sourceSha256"`
			Command      string `json:"command"`
		} `json:"generator"`
		Toolchain string          `json:"toolchain"`
		Modules   json.RawMessage `json:"modules"`
		Seeds     []struct {
			Sprite string `json:"sprite"`
			Seed   uint64 `json:"seed"`
		} `json:"seeds"`
		Inputs  []file `json:"inputs"`
		Outputs []file `json:"outputs"`
	}
	if err := json.Unmarshal(out[provenancePath], &prov); err != nil {
		t.Fatalf("provenance.json: %v", err)
	}

	art := os.DirFS("art")
	var inputs []file
	var seeds []string
	err := fs.WalkDir(art, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(art, p)
		inputs = append(inputs, file{p, hexSum(data)})
		id, world := strings.CutPrefix(strings.TrimSuffix(p, ".pxm"), "world/")
		for _, l := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(l, "frame ") {
				break
			}
			if n, ok := strings.CutPrefix(l, "seed "); ok && world {
				seeds = append(seeds, id+" "+n)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(inputs, func(a, b file) int { return strings.Compare(a.Path, b.Path) })
	if !slices.Equal(prov.Inputs, inputs) {
		t.Errorf("inputs\n%v\nwant every file under art:\n%v", prov.Inputs, inputs)
	}

	var outs []file
	for p, data := range out {
		if p != provenancePath {
			outs = append(outs, file{p, hexSum(data)})
		}
	}
	slices.SortFunc(outs, func(a, b file) int { return strings.Compare(a.Path, b.Path) })
	if !slices.Equal(prov.Outputs, outs) {
		t.Errorf("outputs\n%v\nwant every other written file:\n%v", prov.Outputs, outs)
	}

	slices.Sort(seeds)
	var gotSeeds []string
	for _, s := range prov.Seeds {
		gotSeeds = append(gotSeeds, fmt.Sprintf("%s %d", s.Sprite, s.Seed))
	}
	if !slices.Equal(gotSeeds, seeds) {
		t.Errorf("seeds %v; the textured tiles are %v", gotSeeds, seeds)
	}

	licence, err := os.ReadFile("art/LICENCE.txt")
	if err != nil {
		t.Fatal(err)
	}
	if prov.Licence != (file{"LICENCE.txt", hexSum(licence)}) || !bytes.Equal(out["LICENCE.txt"], licence) {
		t.Errorf("licence %+v; want LICENCE.txt as a byte copy of art/LICENCE.txt with its digest", prov.Licence)
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var lines strings.Builder
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		onDisk, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		embedded, err := fs.ReadFile(sources, name)
		if err != nil || !bytes.Equal(embedded, onDisk) {
			t.Errorf("%s is not embedded as it is on disk (%v)", name, err)
		}
		fmt.Fprintf(&lines, "%s  %s\n", hexSum(onDisk), name)
	}
	if got := hexSum([]byte(lines.String())); prov.Generator.SourceSha256 != got {
		t.Errorf("sourceSha256 %s; the non-test sources hash to %s", prov.Generator.SourceSha256, got)
	}
	if prov.Generator.Package != "github.com/Darius-PJ/dwarf-harness/tools/artgen" ||
		prov.Generator.Command != "GOTOOLCHAIN=local CGO_ENABLED=0 go run ./tools/artgen" {
		t.Errorf("generator %+v; want the package path and the canonical command", prov.Generator)
	}
	if prov.Toolchain != runtime.Version() {
		t.Errorf("toolchain %q; want %q", prov.Toolchain, runtime.Version())
	}
	if string(prov.Modules) != "[]" {
		t.Errorf("modules %s; the generator links the standard library only", prov.Modules)
	}
}

func TestEditChangesOnlyNamedSprite(t *testing.T) {
	base := outputs(t)
	sprites := func(files []outFile) map[string][]byte {
		m := map[string][]byte{}
		for _, f := range files {
			if isSpritePNG(f.path) {
				m[f.path] = f.data
			}
		}
		return m
	}
	baseSprites := map[string][]byte{}
	for p, data := range base {
		if isSpritePNG(p) {
			baseSprites[p] = data
		}
	}
	for _, c := range []struct {
		name string
		edit func(t *testing.T, m fstest.MapFS) string
	}{
		{"seed of tile.rock_a", func(t *testing.T, m fstest.MapFS) string {
			p := "world/tile.rock_a.pxm"
			rewrite(t, m, p, "seed ", func(l string) string {
				n, err := strconv.ParseUint(strings.TrimPrefix(l, "seed "), 10, 32)
				if err != nil {
					t.Fatal(err)
				}
				return fmt.Sprintf("seed %d", n+1)
			})
			return "world/tile.rock_a.png"
		}},
		{"one pixel of hair_beard.braided", func(t *testing.T, m fstest.MapFS) string {
			p := "world/hair_beard.braided.pxm"
			n := rowWith(t, m, p, "h")
			replace(t, m, p, n, strings.Replace(lines(t, m, p)[n-1], "h", "H", 1))
			return "world/hair_beard.braided.png"
		}},
		{"one pixel of three-quarter hair_beard.braided in act_worker 1", func(t *testing.T, m fstest.MapFS) string {
			p := "rpg/three_quarter/hair_beard.braided.pxm"
			ls := lines(t, m, p)
			for n := lineOf(t, m, p, "frame act_worker 1") + 1; n <= len(ls); n++ {
				if strings.Contains(ls[n-1], "h") {
					replace(t, m, p, n, strings.Replace(ls[n-1], "h", "H", 1))
					return "rpg/three_quarter/hair_beard.braided.png"
				}
			}
			t.Fatalf("%s act_worker 1 has no h", p)
			return ""
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := realArt(t)
			want := c.edit(t, m)
			files, err := generate(m)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			if got := differing(baseSprites, sprites(files)); !slices.Equal(got, []string{want}) {
				t.Errorf("sprite PNGs that changed: %v; want only %s", got, want)
			}
		})
	}
}

// TestStripPixelsFollowTheLegend reads every pixel map and palette.txt
// without the generator and checks that every strip holds, frame by frame
// in file order, the palette index each character names.
func TestStripPixelsFollowTheLegend(t *testing.T) {
	out := outputs(t)
	data, err := os.ReadFile("art/palette.txt")
	if err != nil {
		t.Fatal(err)
	}
	legend := map[byte]uint8{}
	textures := map[byte]bool{}
	for _, l := range strings.Split(string(data), "\n") {
		f := strings.Fields(l)
		switch {
		case len(f) > 1 && f[0] == "entry":
			legend[f[1][0]] = uint8(len(legend))
		case len(f) > 1 && f[0] == "texture":
			textures[f[1][0]] = true
		}
	}
	maps := 0
	for _, w := range wantViews {
		files, err := os.ReadDir("art/" + w.dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			src, err := os.ReadFile("art/" + w.dir + "/" + f.Name())
			if err != nil {
				t.Fatal(err)
			}
			p := w.dir + "/" + strings.TrimSuffix(f.Name(), ".pxm") + ".png"
			decoded, err := png.Decode(bytes.NewReader(out[p]))
			if err != nil {
				t.Fatalf("%s: %v", p, err)
			}
			strip, ok := decoded.(*image.Paletted)
			if !ok {
				t.Fatalf("%s decodes as %T", p, decoded)
			}
			maps++
			frame, y, width := -1, 0, 0
			for n, l := range strings.Split(strings.TrimSuffix(string(src), "\n"), "\n") {
				switch fields := strings.Fields(l); {
				case strings.HasPrefix(l, "//") || len(fields) == 0:
				case fields[0] == "canvas":
					width, _ = strconv.Atoi(fields[1])
				case fields[0] == "frame":
					frame, y = frame+1, 0
				case frame >= 0:
					for x := range len(l) {
						if textures[l[x]] {
							continue
						}
						if got := strip.ColorIndexAt(frame*width+x, y); got != legend[l[x]] {
							t.Errorf("art/%s/%s:%d: pixel (%d,%d) of frame %d is index %d in %s; %q is entry %d",
								w.dir, f.Name(), n+1, x, y, frame, got, p, l[x], legend[l[x]])
						}
					}
					y++
				}
			}
		}
	}
	if maps == 0 {
		t.Fatal("no pixel maps read")
	}
}
