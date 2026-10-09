package main

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io/fs"
	"maps"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
)

// Fixed values of the output records.
const (
	sampleFormat     = "dwarf-harness.art-sample.v2"
	provenanceFormat = "dwarf-harness.art-provenance.v1"
	packID           = "dwarf-harness.sample"
	generatorPackage = "github.com/Darius-PJ/dwarf-harness/tools/artgen"
	generatorCommand = "GOTOOLCHAIN=local CGO_ENABLED=0 go run ./tools/artgen"
	statement        = "Original DwarfHarness project art. Project agents wrote every pixel map, the palette, the color choices, the outfits, the room, the animation timings, the example roster and the seeds as text; the generator below rendered them. No image editor, image-generation model or model call, pre-existing character kit, Dwarf Fortress asset or third-party sprite was used."
)

// Output paths, relative to the sample root.
const (
	samplePath     = "sample.json"
	provenancePath = "provenance.json"
	indexPath      = "sheets/index.tsv"
)

// outFile is one generated file; path is slash-separated and relative to
// the sample root.
type outFile struct {
	path string
	data []byte
}

// generate builds every output from the inputs in art, in memory and sorted
// by path. It touches no other file system.
func generate(art fs.FS) ([]outFile, error) {
	s, err := load(art)
	if err != nil {
		return nil, err
	}
	var out []outFile
	add := func(p string, data []byte) { out = append(out, outFile{p, data}) }

	// One record per sprite ID over every view, in ID order, with its images
	// in view order.
	var sprites []spriteJSON
	var pngs []outFile
	for _, v := range views {
		for _, id := range s.ids(v.name) {
			m := s.sprites[v.name][id]
			data, err := encodePNG(s.strip(m))
			if err != nil {
				return nil, err
			}
			p := v.dir + "/" + id + ".png"
			pngs = append(pngs, outFile{p, data})
			i := slices.IndexFunc(sprites, func(sp spriteJSON) bool { return sp.ID == id })
			if i < 0 {
				i = len(sprites)
				sprites = append(sprites, spriteRecord(m))
			}
			sprites[i].Images = append(sprites[i].Images, s.imageRecord(v, m, p, data))
		}
	}
	slices.SortFunc(sprites, func(a, b spriteJSON) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(pngs, byPath)
	var content strings.Builder
	for _, f := range pngs {
		fmt.Fprintf(&content, "%s  %s\n", digest(f.data), f.path)
	}
	out = append(out, pngs...)

	var index strings.Builder
	index.WriteString("sheet\tsection\tx\ty\tw\th\tcontent\n")
	for _, sh := range []*sheet{s.mapSheet(), s.characterSheet()} {
		img := sh.image()
		for _, z := range []int{1, zoom} {
			if z > 1 {
				img = scale(img, z)
			}
			data, err := encodePNG(img)
			if err != nil {
				return nil, err
			}
			add(fmt.Sprintf("sheets/%s-%dx.png", sh.name, z), data)
		}
		sh.index(&index)
	}
	add(indexPath, []byte(index.String()))
	for _, m := range s.roster.members {
		for _, l := range s.memberLoops(m) {
			data, err := s.preview(m, l)
			if err != nil {
				return nil, err
			}
			add(fmt.Sprintf("sheets/page-%s-%s-%dx.gif", m.outfit, l.name, zoom), data)
		}
	}

	sample, err := marshal(s.sampleRecord(digest([]byte(content.String())), sprites))
	if err != nil {
		return nil, err
	}
	add(samplePath, sample)
	add(licencePath, s.licence)

	slices.SortFunc(out, byPath)
	prov, err := s.provenanceRecord(out)
	if err != nil {
		return nil, err
	}
	data, err := marshal(prov)
	if err != nil {
		return nil, err
	}
	add(provenancePath, data)
	slices.SortFunc(out, byPath)
	return out, nil
}

func byPath(a, b outFile) int { return strings.Compare(a.path, b.path) }

func encodePNG(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func digest(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// preview encodes roster member m's loop l as a looping GIF (§3.14 of the
// round-2 brief). Frame k is m's page cell for frame k, scaled like the 4x
// sheets; the frames share one global palette, their colours sorted by RGB.
func (s *artSet) preview(m member, l *loop) ([]byte, error) {
	tq := viewOf(viewThreeQuarter)
	var frames []*image.NRGBA
	used := map[color.NRGBA]bool{}
	for k := range l.ms {
		img := image.NewNRGBA(image.Rect(0, 0, tq.w, tq.h))
		fill(img, img.Rect, cellBackground)
		put(img, s.page(m, l.name, k), 0, 0)
		img = scale(img, zoom)
		for i := 0; i < len(img.Pix); i += 4 {
			used[color.NRGBA{img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]}] = true
		}
		frames = append(frames, img)
	}
	rgb := func(c color.NRGBA) int { return int(c.R)<<16 | int(c.G)<<8 | int(c.B) }
	colors := slices.SortedFunc(maps.Keys(used), func(a, b color.NRGBA) int { return cmp.Compare(rgb(a), rgb(b)) })
	if len(colors) > 256 {
		return nil, fmt.Errorf("preview of %s %s has %d colours; a GIF holds 256", m.outfit, l.name, len(colors))
	}
	pal := make(color.Palette, len(colors))
	index := map[color.NRGBA]uint8{}
	for i, c := range colors {
		pal[i], index[c] = c, uint8(i)
	}
	w, h := tq.w*zoom, tq.h*zoom
	g := &gif.GIF{Config: image.Config{ColorModel: pal, Width: w, Height: h}}
	for k, img := range frames {
		p := image.NewPaletted(img.Rect, pal)
		for i := range p.Pix {
			p.Pix[i] = index[img.NRGBAAt(i%w, i/w)]
		}
		g.Image = append(g.Image, p)
		g.Delay = append(g.Delay, l.ms[k]/10)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// marshal encodes a record as indented JSON with a final LF.
func marshal(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// sampleJSON is sample.json (§3.15; §3.11 of the round-2 brief), a draft
// manifest; it is not the manifest that ticket 107 freezes.
type sampleJSON struct {
	Format        string          `json:"format"`
	Pack          string          `json:"pack"`
	Licence       string          `json:"licence"`
	Provenance    string          `json:"provenance"`
	ContentSha256 string          `json:"contentSha256"`
	Grids         []gridJSON      `json:"grids"`
	Views         []viewJSON      `json:"views"`
	Slots         []string        `json:"slots"`
	Palette       []paletteJSON   `json:"palette"`
	Occluder      int             `json:"occluder"`
	Masks         []maskJSON      `json:"masks"`
	Colors        []colorJSON     `json:"colors"`
	RoleOverlay   roleOverlayJSON `json:"roleOverlay"`
	CorpsMarker   corpsMarkerJSON `json:"corpsMarker"`
	Sprites       []spriteJSON    `json:"sprites"`
}

type gridJSON struct {
	Grid string `json:"grid"`
	Cell int    `json:"cell"`
}

type viewJSON struct {
	View       string         `json:"view"`
	Grid       string         `json:"grid"`
	Facing     string         `json:"facing"`
	Canvas     [2]int         `json:"canvas"`
	Anchor     [2]int         `json:"anchor"`
	Layers     []string       `json:"layers"`
	Animations []viewAnimJSON `json:"animations"`
	States     []stateJSON    `json:"states,omitempty"`
}

type viewAnimJSON struct {
	Name        string `json:"name"`
	FrameCount  int    `json:"frameCount"`
	DurationsMs []int  `json:"durationsMs,omitempty"`
}

type stateJSON struct {
	State     string `json:"state"`
	Role      string `json:"role,omitempty"`
	Animation string `json:"animation"`
	Tool      string `json:"tool,omitempty"`
}

type paletteJSON struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	RGBA  string `json:"rgba"`
}

type maskJSON struct {
	Mask    string `json:"mask"`
	Indices []int  `json:"indices"`
}

type colorJSON struct {
	ID   string   `json:"id"`
	Mask string   `json:"mask"`
	RGB  []string `json:"rgb"`
}

type roleOverlayJSON struct {
	Trim  string     `json:"trim"`
	Roles []roleJSON `json:"roles"`
}

type roleJSON struct {
	Role  string `json:"role"`
	Badge string `json:"badge"`
	Color string `json:"color"`
}

type corpsMarkerJSON struct {
	Banner  string   `json:"banner"`
	Mask    string   `json:"mask"`
	Emblems []string `json:"emblems"`
}

type spriteJSON struct {
	ID       string      `json:"id"`
	Kind     string      `json:"kind"`
	Slot     string      `json:"slot,omitempty"`
	Occludes []string    `json:"occludes,omitempty"`
	Category string      `json:"category,omitempty"`
	Role     string      `json:"role,omitempty"`
	Images   []imageJSON `json:"images"`
}

type imageJSON struct {
	Grid       string     `json:"grid"`
	View       string     `json:"view"`
	Path       string     `json:"path"`
	Sha256     string     `json:"sha256"`
	Canvas     [2]int     `json:"canvas"`
	Anchor     [2]int     `json:"anchor"`
	Masks      []string   `json:"masks"`
	Animations []animJSON `json:"animations"`
}

type animJSON struct {
	Name   string   `json:"name"`
	Frames [][4]int `json:"frames"`
}

func spriteRecord(m *pixmap) spriteJSON {
	return spriteJSON{ID: m.id, Kind: m.kind, Slot: m.slot, Occludes: m.occludes, Category: m.category, Role: m.role}
}

// imageRecord describes the PNG strip of m in view v.
func (s *artSet) imageRecord(v view, m *pixmap, p string, data []byte) imageJSON {
	used := map[string]bool{}
	for _, px := range m.px {
		for _, i := range px {
			used[s.pal.entries[i].mask] = true
		}
	}
	img := imageJSON{Grid: v.grid, View: v.name, Path: p, Sha256: digest(data), Canvas: [2]int{m.w, m.h}, Anchor: [2]int{m.ax, m.ay}, Masks: []string{}}
	for _, mask := range masks {
		if used[mask] {
			img.Masks = append(img.Masks, mask)
		}
	}
	for _, a := range m.anims {
		aj := animJSON{Name: a.name}
		for k := range a.frames {
			aj.Frames = append(aj.Frames, [4]int{(a.first + k) * m.w, 0, m.w, m.h})
		}
		img.Animations = append(img.Animations, aj)
	}
	return img
}

// markerKinds lists the kinds drawn after the slots, in draw order; a view
// draws those that may exist in it (§3.8 of the round-2 brief).
var markerKinds = []string{kindOverlay, kindBadge, kindBanner, kindEmblem, kindTool}

// viewRecord describes view v: its layers in draw order, its layer
// animations and, where animations.txt sets the loops, their timings and
// the states that play them.
func (s *artSet) viewRecord(v view) viewJSON {
	rec := viewJSON{View: v.name, Grid: v.grid, Facing: v.facing, Canvas: [2]int{v.w, v.h}, Anchor: [2]int{v.ax, v.ay}, Layers: slices.Clone(slots)}
	for _, k := range markerKinds {
		if slices.Contains(kindViews[k], v.name) {
			rec.Layers = append(rec.Layers, k)
		}
	}
	for _, a := range s.layerAnims(v.name) {
		aj := viewAnimJSON{Name: a.name, FrameCount: a.frames}
		if v.name == viewThreeQuarter {
			aj.DurationsMs = s.loopNamed(a.name).ms
		}
		rec.Animations = append(rec.Animations, aj)
	}
	if v.name != viewThreeQuarter {
		return rec
	}
	rec.States = []stateJSON{{State: "idle", Animation: "idle"}}
	for _, l := range s.loops[1:] {
		role := strings.TrimPrefix(l.name, "act_")
		st := stateJSON{State: "working", Role: role, Animation: l.name}
		if s.sprites[v.name]["tool."+role] != nil {
			st.Tool = "tool." + role
		}
		rec.States = append(rec.States, st)
	}
	return rec
}

func (s *artSet) sampleRecord(content string, sprites []spriteJSON) sampleJSON {
	rec := sampleJSON{
		Format:        sampleFormat,
		Pack:          packID,
		Licence:       licencePath,
		Provenance:    provenancePath,
		ContentSha256: content,
		Grids:         []gridJSON{{gridWorld, cell}, {gridRPG, viewOf(viewFront).w}},
		Slots:         slots,
		Occluder:      s.pal.occlude,
		RoleOverlay:   roleOverlayJSON{Trim: "overlay.trim"},
		CorpsMarker:   corpsMarkerJSON{Banner: bannerID, Mask: "corps", Emblems: []string{}},
		Sprites:       sprites,
	}
	for _, v := range views {
		rec.Views = append(rec.Views, s.viewRecord(v))
	}
	for i, e := range s.pal.entries {
		c := e.color
		rec.Palette = append(rec.Palette, paletteJSON{i, e.name, fmt.Sprintf("%02x%02x%02x%02x", c.R, c.G, c.B, c.A)})
	}
	for _, m := range masks {
		rec.Masks = append(rec.Masks, maskJSON{m, s.pal.ramps[m]})
	}
	for _, c := range s.choices {
		rec.Colors = append(rec.Colors, colorJSON{c.id, c.mask, c.hex})
	}
	for _, role := range roles {
		rec.RoleOverlay.Roles = append(rec.RoleOverlay.Roles, roleJSON{role, "badge." + role, "role." + role})
	}
	for _, id := range s.ids(viewFront) {
		if s.sprites[viewFront][id].kind == kindEmblem {
			rec.CorpsMarker.Emblems = append(rec.CorpsMarker.Emblems, id)
		}
	}
	return rec
}

// provenanceJSON is provenance.json (§3.16).
type provenanceJSON struct {
	Format    string        `json:"format"`
	Statement string        `json:"statement"`
	Licence   fileJSON      `json:"licence"`
	Generator generatorJSON `json:"generator"`
	Toolchain string        `json:"toolchain"`
	Modules   []moduleJSON  `json:"modules"`
	Seeds     []seedJSON    `json:"seeds"`
	Inputs    []fileJSON    `json:"inputs"`
	Outputs   []fileJSON    `json:"outputs"`
}

type fileJSON struct {
	Path   string `json:"path"`
	Sha256 string `json:"sha256"`
}

type generatorJSON struct {
	Package      string `json:"package"`
	SourceSha256 string `json:"sourceSha256"`
	Command      string `json:"command"`
}

type moduleJSON struct {
	Path    string      `json:"path"`
	Version string      `json:"version"`
	Sum     string      `json:"sum"`
	Replace *moduleJSON `json:"replace,omitempty"`
}

type seedJSON struct {
	Sprite string `json:"sprite"`
	Seed   uint32 `json:"seed"`
}

// provenanceRecord describes the generator, its inputs and the outputs out,
// which are sorted by path.
func (s *artSet) provenanceRecord(out []outFile) (provenanceJSON, error) {
	source, err := sourceDigest()
	if err != nil {
		return provenanceJSON{}, err
	}
	mods, err := modules()
	if err != nil {
		return provenanceJSON{}, err
	}
	rec := provenanceJSON{
		Format:    provenanceFormat,
		Statement: statement,
		Licence:   fileJSON{licencePath, digest(s.licence)},
		Generator: generatorJSON{generatorPackage, source, generatorCommand},
		Toolchain: runtime.Version(),
		Modules:   mods,
		Seeds:     []seedJSON{},
	}
	for _, id := range s.ids(viewOverworld) {
		if m := s.sprites[viewOverworld][id]; m.textured() {
			rec.Seeds = append(rec.Seeds, seedJSON{id, m.seed})
		}
	}
	for _, f := range s.inputs {
		rec.Inputs = append(rec.Inputs, fileJSON{f.path, digest(f.data)})
	}
	for _, f := range out {
		rec.Outputs = append(rec.Outputs, fileJSON{f.path, digest(f.data)})
	}
	return rec, nil
}

// sourceDigest is the generator version: the SHA-256 of one sha256sum line
// per embedded non-test Go file, in name order.
func sourceDigest() (string, error) {
	entries, err := fs.ReadDir(sources, ".")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		data, err := fs.ReadFile(sources, e.Name())
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s  %s\n", digest(data), e.Name())
	}
	return digest([]byte(b.String())), nil
}

// modules lists the modules linked into the running binary, by path.
func modules() ([]moduleJSON, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil, errors.New("build information is unavailable, so the modules cannot be recorded; build artgen in module mode")
	}
	mods := []moduleJSON{}
	for _, d := range info.Deps {
		mods = append(mods, module(d))
	}
	slices.SortFunc(mods, func(a, b moduleJSON) int { return strings.Compare(a.Path, b.Path) })
	return mods, nil
}

func module(d *debug.Module) moduleJSON {
	m := moduleJSON{Path: d.Path, Version: d.Version, Sum: d.Sum}
	if d.Replace != nil {
		r := module(d.Replace)
		m.Replace = &r
	}
	return m
}
