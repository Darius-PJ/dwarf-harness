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
	Palette       []struct {
		Index int    `json:"index"`
		Name  string `json:"name"`
		RGBA  string `json:"rgba"`
	} `json:"palette"`
	Masks []struct {
		Mask    string `json:"mask"`
		Indices []int  `json:"indices"`
	} `json:"masks"`
	Sprites []struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Images []struct {
			Grid       string   `json:"grid"`
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

func TestMetadataDescribesImages(t *testing.T) {
	out := outputs(t)
	doc := sampleOf(t, out)

	palette := make([]color.NRGBA, len(doc.Palette))
	for i, e := range doc.Palette {
		v, err := strconv.ParseUint(e.RGBA, 16, 32)
		if e.Index != i || err != nil || len(e.RGBA) != 8 {
			t.Fatalf("palette entry %d = %+v; want index %d and rrggbbaa", i, e, i)
		}
		palette[i] = color.NRGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}
	}
	maskOf := map[int]string{}
	for _, m := range doc.Masks {
		for _, i := range m.Indices {
			maskOf[i] = m.Mask
		}
	}

	listed := map[string]bool{}
	layers := map[string]string{} // grid to the registration every layer shares
	for _, sp := range doc.Sprites {
		for _, im := range sp.Images {
			if listed[im.Path] {
				t.Errorf("%s is listed twice", im.Path)
			}
			listed[im.Path] = true
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

			w, h := im.Canvas[0], im.Canvas[1]
			var rects [][4]int
			for _, a := range im.Animations {
				rects = append(rects, a.Frames...)
			}
			if got, want := img.Rect.Size(), image.Pt(w*len(rects), h); got != want {
				t.Errorf("%s is %v; want canvas width x %d frames = %v", im.Path, got, len(rects), want)
				continue
			}
			used := map[string]bool{}
			for i, r := range rects {
				if r != [4]int{i * w, 0, w, h} {
					t.Errorf("%s frame %d = %v; want %v", im.Path, i, r, [4]int{i * w, 0, w, h})
					continue
				}
				visible := false
				for y := range h {
					for x := range w {
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

			if sp.Kind == kindPart || sp.Kind == kindOverlay || sp.Kind == kindBadge {
				sig := fmt.Sprint(im.Canvas, im.Anchor)
				for _, a := range im.Animations {
					sig += fmt.Sprintf(" %s:%d", a.Name, len(a.Frames))
				}
				if prev, ok := layers[im.Grid]; !ok {
					layers[im.Grid] = sig
				} else if sig != prev {
					t.Errorf("%s registers as %s; the other %s layers as %s", im.Path, sig, im.Grid, prev)
				}
			}
		}
	}
	if got := layers[gridWorld]; !strings.HasPrefix(got, "[16 16] [8 16] ") {
		t.Errorf("world layers register as %q; want canvas 16x16, anchor (8,16)", got)
	}
	if got := layers[gridRPG]; !strings.HasPrefix(got, "[64 64] [32 64] ") {
		t.Errorf("rpg layers register as %q; want canvas 64x64, anchor (32,64)", got)
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
