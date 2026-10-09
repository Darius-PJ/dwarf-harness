package main

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// Inputs that the tables change.
const (
	bodyMap     = "world/body.stout.pxm"
	hatMap      = "world/headwear.hat.pxm"
	trimMap     = "world/overlay.trim.pxm"
	workerBadge = "world/badge.worker.pxm"
	doorMap     = "world/tile.door.pxm"
	floorMap    = "world/tile.floor_a.pxm"
	wallTopMap  = "world/tile.wall_top.pxm"
	wallFaceMap = "world/tile.wall_face.pxm"
	dogMap      = "world/animal.dog.pxm"

	frontBody    = "rpg/front/body.stout.pxm"
	frontPennant = "rpg/front/banner.pennant.pxm"
	frontEmblem  = "rpg/front/emblem.mountain.pxm"
	tqBody       = "rpg/three_quarter/body.stout.pxm"
	tqHat        = "rpg/three_quarter/headwear.hat.pxm"
	tqPennant    = "rpg/three_quarter/banner.pennant.pxm"
	tqMountain   = "rpg/three_quarter/emblem.mountain.pxm"
	tqCrescent   = "rpg/three_quarter/emblem.crescent.pxm"
	tqTool       = "rpg/three_quarter/tool.worker.pxm"
)

// frameRows returns the frame line and the pixel rows of frame header of p.
func frameRows(t *testing.T, m fstest.MapFS, p, header string) (int, []string) {
	t.Helper()
	n := lineOf(t, m, p, header)
	ls := lines(t, m, p)
	end := n
	for end < len(ls) && !strings.HasPrefix(ls[end], "frame ") {
		end++
	}
	return n, ls[n:end]
}

// truncate deletes the line of p that starts with prefix and every line
// after it.
func truncate(t *testing.T, m fstest.MapFS, p, prefix string) {
	t.Helper()
	setLines(m, p, lines(t, m, p)[:lineOf(t, m, p, prefix)-1])
}

// lines returns the lines of p without their LFs.
func lines(t *testing.T, m fstest.MapFS, p string) []string {
	t.Helper()
	f, ok := m[p]
	if !ok {
		t.Fatalf("%s is not in the art tree", p)
	}
	return strings.Split(strings.TrimSuffix(string(f.Data), "\n"), "\n")
}

func setLines(m fstest.MapFS, p string, ls []string) {
	m[p] = &fstest.MapFile{Data: []byte(strings.Join(ls, "\n") + "\n")}
}

// lineOf returns the number of the first line of p that starts with prefix.
func lineOf(t *testing.T, m fstest.MapFS, p, prefix string) int {
	t.Helper()
	for i, l := range lines(t, m, p) {
		if strings.HasPrefix(l, prefix) {
			return i + 1
		}
	}
	t.Fatalf("%s has no line starting with %q", p, prefix)
	return 0
}

// lastLineOf returns the number of the last line of p that starts with prefix.
func lastLineOf(t *testing.T, m fstest.MapFS, p, prefix string) int {
	t.Helper()
	ls := lines(t, m, p)
	for i := len(ls) - 1; i >= 0; i-- {
		if strings.HasPrefix(ls[i], prefix) {
			return i + 1
		}
	}
	t.Fatalf("%s has no line starting with %q", p, prefix)
	return 0
}

// rowWith returns the number of the first pixel row of p that contains c.
func rowWith(t *testing.T, m fstest.MapFS, p, c string) int {
	t.Helper()
	ls := lines(t, m, p)
	for i := lineOf(t, m, p, "frame "); i < len(ls); i++ {
		if !strings.HasPrefix(ls[i], "frame ") && strings.Contains(ls[i], c) {
			return i + 1
		}
	}
	t.Fatalf("%s has no pixel row containing %q", p, c)
	return 0
}

// replace sets line n of p and returns its location.
func replace(t *testing.T, m fstest.MapFS, p string, n int, text string) (string, int) {
	t.Helper()
	ls := lines(t, m, p)
	ls[n-1] = text
	setLines(m, p, ls)
	return p, n
}

// rewrite applies f to the first line of p that starts with prefix and
// returns its location.
func rewrite(t *testing.T, m fstest.MapFS, p, prefix string, f func(string) string) (string, int) {
	t.Helper()
	n := lineOf(t, m, p, prefix)
	return replace(t, m, p, n, f(lines(t, m, p)[n-1]))
}

// swap replaces old with new in the first line of p that starts with prefix.
func swap(t *testing.T, m fstest.MapFS, p, prefix, old, new string) (string, int) {
	t.Helper()
	return rewrite(t, m, p, prefix, func(l string) string { return strings.Replace(l, old, new, 1) })
}

// insert puts text before line n of p and returns the new line's location.
func insert(t *testing.T, m fstest.MapFS, p string, n int, text string) (string, int) {
	t.Helper()
	setLines(m, p, slices.Insert(lines(t, m, p), n-1, text))
	return p, n
}

// remove deletes the first line of p that starts with prefix.
func remove(t *testing.T, m fstest.MapFS, p, prefix string) {
	t.Helper()
	n := lineOf(t, m, p, prefix)
	setLines(m, p, slices.Delete(lines(t, m, p), n-1, n))
}

// firstRow returns the number of the first pixel row of p.
func firstRow(t *testing.T, m fstest.MapFS, p string) int {
	t.Helper()
	return lineOf(t, m, p, "frame ") + 1
}

// editRow applies f to the first pixel row of p and returns its location.
func editRow(t *testing.T, m fstest.MapFS, p string, f func(string) string) (string, int) {
	t.Helper()
	n := firstRow(t, m, p)
	return replace(t, m, p, n, f(lines(t, m, p)[n-1]))
}

func copyFile(m fstest.MapFS, from, to string) {
	m[to] = &fstest.MapFile{Data: m[from].Data}
}

// failsAt runs generate and fails the test unless the errors include one at
// path:line (line 0: the whole file).
func failsAt(t *testing.T, art fs.FS, path string, line int) {
	t.Helper()
	_, err := generate(art)
	var list errorList
	if !errors.As(err, &list) {
		t.Fatalf("generate = %v; want a validation failure at %s:%d", err, path, line)
	}
	for _, e := range list {
		if e.path == path && e.line == line {
			return
		}
	}
	t.Fatalf("errors:\n%v\nwant one at %s:%d", list, path, line)
}

type breakage struct {
	name string
	edit func(t *testing.T, m fstest.MapFS) (path string, line int)
}

func runBreakages(t *testing.T, rows []breakage) {
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			m := realArt(t)
			path, line := row.edit(t, m)
			failsAt(t, m, path, line)
		})
	}
}

func TestMalformedInputsFailWithLocation(t *testing.T) {
	runBreakages(t, []breakage{
		// Text rules (§3.3).
		{"CR line ending", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, bodyMap, "canvas", func(l string) string { return l + "\r" })
		}},
		{"tab", func(t *testing.T, m fstest.MapFS) (string, int) {
			return editRow(t, m, bodyMap, func(r string) string { return "\t" + r[1:] })
		}},
		{"non-ASCII byte", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, palettePath, 1, "// caf\u00e9")
		}},
		{"missing final LF", func(t *testing.T, m fstest.MapFS) (string, int) {
			ls := lines(t, m, colorsPath)
			m[colorsPath] = &fstest.MapFile{Data: []byte(strings.Join(ls, "\n"))}
			return colorsPath, len(ls)
		}},
		{"licence not UTF-8", func(t *testing.T, m fstest.MapFS) (string, int) {
			m[licencePath] = &fstest.MapFile{Data: append([]byte{0xff}, m[licencePath].Data...)}
			return licencePath, 1
		}},
		{"empty licence", func(t *testing.T, m fstest.MapFS) (string, int) {
			m[licencePath] = &fstest.MapFile{Data: nil}
			return licencePath, 0
		}},
		{"missing palette", func(t *testing.T, m fstest.MapFS) (string, int) {
			delete(m, palettePath)
			return palettePath, 0
		}},

		// IDs and kinds (§3.7).
		{"ID not in lowercase", func(t *testing.T, m fstest.MapFS) (string, int) {
			copyFile(m, bodyMap, "world/Body.stout.pxm")
			return "world/Body.stout.pxm", 0
		}},
		{"first segment no slot or kind", func(t *testing.T, m fstest.MapFS) (string, int) {
			copyFile(m, bodyMap, "world/cape.red.pxm")
			return "world/cape.red.pxm", 0
		}},
		{"overlay other than the trim", func(t *testing.T, m fstest.MapFS) (string, int) {
			copyFile(m, trimMap, "world/overlay.sash.pxm")
			return "world/overlay.sash.pxm", 0
		}},
		{"badge name not a role", func(t *testing.T, m fstest.MapFS) (string, int) {
			copyFile(m, workerBadge, "world/badge.king.pxm")
			return "world/badge.king.pxm", 0
		}},
		{"trim character in a part", func(t *testing.T, m fstest.MapFS) (string, int) {
			return editRow(t, m, bodyMap, func(r string) string { return "t" + r[1:] })
		}},
		{"texture character in a part", func(t *testing.T, m fstest.MapFS) (string, int) {
			return editRow(t, m, bodyMap, func(r string) string { return "%" + r[1:] })
		}},
		{"mask character in a tile", func(t *testing.T, m fstest.MapFS) (string, int) {
			return editRow(t, m, wallTopMap, func(r string) string { return "s" + r[1:] })
		}},
		{"occlude character in a tile", func(t *testing.T, m fstest.MapFS) (string, int) {
			return editRow(t, m, wallTopMap, func(r string) string { return "x" + r[1:] })
		}},
		{"color character in a badge", func(t *testing.T, m fstest.MapFS) (string, int) {
			n := rowWith(t, m, workerBadge, "*")
			return replace(t, m, workerBadge, n, strings.Replace(lines(t, m, workerBadge)[n-1], "*", "c", 1))
		}},
		{"outline character in the trim", func(t *testing.T, m fstest.MapFS) (string, int) {
			n := rowWith(t, m, trimMap, "t")
			return replace(t, m, trimMap, n, strings.Replace(lines(t, m, trimMap)[n-1], "t", "#", 1))
		}},

		// palette.txt (§3.4).
		{"duplicate palette character", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, palettePath, "entry S ", "entry S ", "entry . ")
		}},
		{"palette alpha other than 00 or ff", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, palettePath, "entry # ", "1c1626ff", "1c162680")
		}},
		{"palette without occlude", func(t *testing.T, m fstest.MapFS) (string, int) {
			remove(t, m, palettePath, "entry x occlude")
			return palettePath, 0
		}},
		{"entry 0 not transparent", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, palettePath, "entry . transparent", "transparent", "clear")
		}},
		{"occlude with a mask", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, palettePath, "entry x occlude", " - ", " skin ")
		}},
		{"unknown mask", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, palettePath, "entry S ", " skin ", " fur ")
		}},
		{"uppercase color", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, palettePath, "entry # ", "1c1626ff", "1C1626ff")
		}},
		{"entry name not lowercase", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, palettePath, "entry S ", "skin.shadow", "Skin.shadow")
		}},
		{"duplicate entry name", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, palettePath, "entry s ", "skin.base", "skin.shadow")
		}},
		{"unknown palette keyword", func(t *testing.T, m fstest.MapFS) (string, int) {
			return insert(t, m, palettePath, lineOf(t, m, palettePath, "texture"), "swatch q red - ff0000ff")
		}},
		{"texture naming a masked entry", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, palettePath, "texture %", "stone.base", "skin.base")
		}},
		{"texture naming an unknown entry", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, palettePath, "texture %", "stone.base", "granite")
		}},
		{"texture naming a see-through entry", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, palettePath, "texture %", "stone.base", "transparent")
		}},
		{"texture weight 0", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, palettePath, "texture %", "stone.base:2", "stone.base:0")
		}},
		{"texture weight 256", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, palettePath, "texture %", "stone.base:2", "stone.base:256")
		}},
		{"ramp of four entries", func(t *testing.T, m fstest.MapFS) (string, int) {
			return insert(t, m, palettePath, lineOf(t, m, palettePath, "entry 1 ")+1, "entry ! skin.extra skin aabbccff")
		}},
		{"ramp of one entry", func(t *testing.T, m fstest.MapFS) (string, int) {
			swap(t, m, palettePath, "entry H ", " hair ", " - ")
			swap(t, m, palettePath, "entry h ", " hair ", " - ")
			return palettePath, lineOf(t, m, palettePath, "entry 2 ")
		}},
		{"mask without entries", func(t *testing.T, m fstest.MapFS) (string, int) {
			swap(t, m, palettePath, "entry T ", " role ", " - ")
			swap(t, m, palettePath, "entry t ", " role ", " - ")
			return palettePath, 0
		}},

		// colors.txt (§3.5).
		{"color ramp of wrong length", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, colorsPath, "skin.", func(l string) string { return l[:strings.LastIndex(l, " ")] })
		}},
		{"choice naming no mask", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, colorsPath, "skin.", "skin.", "fur.")
		}},
		{"choice ID without a name", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, colorsPath, "hair.", func(l string) string { return "hair" + l[strings.Index(l, " "):] })
		}},
		{"role choice naming no role", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, colorsPath, "role.worker", "role.worker", "role.king")
		}},
		{"missing role choice", func(t *testing.T, m fstest.MapFS) (string, int) {
			remove(t, m, colorsPath, "role.griot")
			return colorsPath, 0
		}},
		{"no skin choice", func(t *testing.T, m fstest.MapFS) (string, int) {
			for strings.Contains(string(m[colorsPath].Data), "\nskin.") {
				remove(t, m, colorsPath, "skin.")
			}
			return colorsPath, 0
		}},
		{"choice color in uppercase", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, colorsPath, "skin.", func(l string) string {
				i := strings.LastIndex(l, " ")
				return l[:i+1] + strings.ToUpper(l[i+1:])
			})
		}},
		{"choice without colors", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, colorsPath, "hair.", func(l string) string { return l[:strings.Index(l, " ")] })
		}},
		{"duplicate choice", func(t *testing.T, m fstest.MapFS) (string, int) {
			ls := lines(t, m, colorsPath)
			setLines(m, colorsPath, append(ls, ls[lineOf(t, m, colorsPath, "hair.")-1]))
			return colorsPath, len(ls) + 1
		}},

		// Pixel maps (§3.6).
		{"row too short", func(t *testing.T, m fstest.MapFS) (string, int) {
			return editRow(t, m, bodyMap, func(r string) string { return r[1:] })
		}},
		{"row too long", func(t *testing.T, m fstest.MapFS) (string, int) {
			return editRow(t, m, bodyMap, func(r string) string { return r + "." })
		}},
		{"too few rows", func(t *testing.T, m fstest.MapFS) (string, int) {
			ls := lines(t, m, bodyMap)
			setLines(m, bodyMap, ls[:len(ls)-1])
			return bodyMap, lastLineOf(t, m, bodyMap, "frame ")
		}},
		{"unknown character", func(t *testing.T, m fstest.MapFS) (string, int) {
			return editRow(t, m, bodyMap, func(r string) string { return "?" + r[1:] })
		}},
		{"missing canvas", func(t *testing.T, m fstest.MapFS) (string, int) {
			remove(t, m, bodyMap, "canvas")
			return bodyMap, 0
		}},
		{"missing anchor", func(t *testing.T, m fstest.MapFS) (string, int) {
			remove(t, m, bodyMap, "anchor")
			return bodyMap, 0
		}},
		{"anchor outside canvas", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, bodyMap, lineOf(t, m, bodyMap, "anchor"), "anchor 8 17")
		}},
		{"canvas of zero width", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, bodyMap, lineOf(t, m, bodyMap, "canvas"), "canvas 0 16")
		}},
		{"frame index gap", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, bodyMap, lineOf(t, m, bodyMap, "frame idle 1"), "frame idle 2")
		}},
		{"duplicate frame header", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, bodyMap, lineOf(t, m, bodyMap, "frame idle 1"), "frame idle 0")
		}},
		{"animation not contiguous", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, dogMap, lineOf(t, m, dogMap, "frame running 1"), "frame idle 2")
		}},
		{"duplicate header keyword", func(t *testing.T, m fstest.MapFS) (string, int) {
			return insert(t, m, bodyMap, lineOf(t, m, bodyMap, "anchor")+1, "anchor 8 16")
		}},
		{"keyword after the first frame", func(t *testing.T, m fstest.MapFS) (string, int) {
			return insert(t, m, bodyMap, lastLineOf(t, m, bodyMap, "frame "), "seed 7")
		}},
		{"unknown map keyword", func(t *testing.T, m fstest.MapFS) (string, int) {
			return insert(t, m, bodyMap, lineOf(t, m, bodyMap, "canvas"), "palette old")
		}},
		{"frame without visible pixels", func(t *testing.T, m fstest.MapFS) (string, int) {
			n := lineOf(t, m, workerBadge, "frame idle 0")
			ls := lines(t, m, workerBadge)
			for i := n; i < n+16; i++ {
				ls[i] = strings.Repeat(".", 16)
			}
			setLines(m, workerBadge, ls)
			return workerBadge, n
		}},
		{"no frames", func(t *testing.T, m fstest.MapFS) (string, int) {
			setLines(m, workerBadge, lines(t, m, workerBadge)[:lineOf(t, m, workerBadge, "frame ")-1])
			return workerBadge, 0
		}},
		{"texture in a wall tile", func(t *testing.T, m fstest.MapFS) (string, int) {
			return editRow(t, m, wallFaceMap, func(r string) string { return "%" + r[1:] })
		}},
		{"texture without seed", func(t *testing.T, m fstest.MapFS) (string, int) {
			remove(t, m, floorMap, "seed")
			return floorMap, rowWith(t, m, floorMap, "~")
		}},
		{"seed without texture", func(t *testing.T, m fstest.MapFS) (string, int) {
			return insert(t, m, wallTopMap, lineOf(t, m, wallTopMap, "category")+1, "seed 5")
		}},
		{"seed out of range", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, floorMap, lineOf(t, m, floorMap, "seed"), "seed 4294967296")
		}},
		{"x without occludes", func(t *testing.T, m fstest.MapFS) (string, int) {
			remove(t, m, hatMap, "occludes")
			return hatMap, rowWith(t, m, hatMap, "x")
		}},
		{"occludes without x", func(t *testing.T, m fstest.MapFS) (string, int) {
			return insert(t, m, bodyMap, lineOf(t, m, bodyMap, "anchor")+1, "occludes back_accessory")
		}},
		{"occludes naming a higher slot", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, hatMap, lineOf(t, m, hatMap, "occludes"), "occludes front_accessory")
		}},
		{"occludes naming no slot", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, hatMap, lineOf(t, m, hatMap, "occludes"), "occludes hair")
		}},
		{"occludes in a tile", func(t *testing.T, m fstest.MapFS) (string, int) {
			return insert(t, m, doorMap, lineOf(t, m, doorMap, "anchor")+1, "occludes body")
		}},
		{"category in a part", func(t *testing.T, m fstest.MapFS) (string, int) {
			return insert(t, m, bodyMap, lineOf(t, m, bodyMap, "anchor")+1, "category floor")
		}},
		{"tile without category", func(t *testing.T, m fstest.MapFS) (string, int) {
			remove(t, m, doorMap, "category")
			return doorMap, 0
		}},
		{"unknown category", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, doorMap, lineOf(t, m, doorMap, "category"), "category gate")
		}},

		// outfits.txt (§3.9).
		{"outfit without a body part", func(t *testing.T, m fstest.MapFS) (string, int) {
			n := lineOf(t, m, outfitsPath, "outfit ")
			remove(t, m, outfitsPath, "part body.")
			return outfitsPath, n
		}},
		{"two parts in one slot", func(t *testing.T, m fstest.MapFS) (string, int) {
			return insert(t, m, outfitsPath, lineOf(t, m, outfitsPath, "part torso_clothing.")+1, "part torso_clothing.vest")
		}},
		{"part before any outfit", func(t *testing.T, m fstest.MapFS) (string, int) {
			return insert(t, m, outfitsPath, 1, "part body.stout")
		}},
		{"duplicate outfit name", func(t *testing.T, m fstest.MapFS) (string, int) {
			first := strings.Fields(lines(t, m, outfitsPath)[lineOf(t, m, outfitsPath, "outfit ")-1])[1]
			n := lastLineOf(t, m, outfitsPath, "outfit ")
			ls := lines(t, m, outfitsPath)
			f := strings.Fields(ls[n-1])
			f[1] = first
			return replace(t, m, outfitsPath, n, strings.Join(f, " "))
		}},
		{"choice of another mask", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, outfitsPath, "outfit ", func(l string) string {
				f := strings.Fields(l)
				f[2] = "skin=hair.black"
				return strings.Join(f, " ")
			})
		}},
		{"part ID that is no part", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, outfitsPath, lineOf(t, m, outfitsPath, "part body."), "part tile.door")
		}},
		{"unknown outfits keyword", func(t *testing.T, m fstest.MapFS) (string, int) {
			return insert(t, m, outfitsPath, lineOf(t, m, outfitsPath, "part body."), "wear body.stout")
		}},
		{"no outfit", func(t *testing.T, m fstest.MapFS) (string, int) {
			m[outfitsPath] = &fstest.MapFile{Data: []byte("// none\n")}
			return outfitsPath, 0
		}},

		// room.txt (§3.10).
		{"undeclared room grid character", func(t *testing.T, m fstest.MapFS) (string, int) {
			n := lineOf(t, m, roomPath, "grid ") + 1
			return replace(t, m, roomPath, n, "Z"+lines(t, m, roomPath)[n-1][1:])
		}},
		{"room grid row too short", func(t *testing.T, m fstest.MapFS) (string, int) {
			n := lineOf(t, m, roomPath, "grid ") + 1
			return replace(t, m, roomPath, n, lines(t, m, roomPath)[n-1][1:])
		}},
		{"too few room grid rows", func(t *testing.T, m fstest.MapFS) (string, int) {
			n := lineOf(t, m, roomPath, "grid ")
			rows, _ := strconv.Atoi(strings.Fields(lines(t, m, roomPath)[n-1])[2])
			setLines(m, roomPath, lines(t, m, roomPath)[:n+rows-1])
			return roomPath, n
		}},
		{"room grid of zero columns", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, roomPath, lineOf(t, m, roomPath, "grid "), "grid 0 8")
		}},
		{"cell after the grid", func(t *testing.T, m fstest.MapFS) (string, int) {
			ls := lines(t, m, roomPath)
			setLines(m, roomPath, append(ls, "cell Z tile.rock_a idle 0"))
			return roomPath, len(ls) + 1
		}},
		{"a second grid", func(t *testing.T, m fstest.MapFS) (string, int) {
			ls := lines(t, m, roomPath)
			setLines(m, roomPath, append(ls, "grid 1 1", "R"))
			return roomPath, len(ls) + 1
		}},
		{"place before the grid", func(t *testing.T, m fstest.MapFS) (string, int) {
			return insert(t, m, roomPath, lineOf(t, m, roomPath, "grid "), "place 2 3 tile.workbench idle 0")
		}},
		{"place with a column that is no number", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, roomPath, "place ", func(l string) string {
				f := strings.Fields(l)
				f[1] = "x"
				return strings.Join(f, " ")
			})
		}},
		{"duplicate cell character", func(t *testing.T, m fstest.MapFS) (string, int) {
			first := strings.Fields(lines(t, m, roomPath)[lineOf(t, m, roomPath, "cell ")-1])[1]
			n := lineOf(t, m, roomPath, "cell ") + 1
			f := strings.Fields(lines(t, m, roomPath)[n-1])
			f[1] = first
			return replace(t, m, roomPath, n, strings.Join(f, " "))
		}},
		{"cell naming no tile", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, roomPath, "cell ", func(l string) string {
				f := strings.Fields(l)
				f[2] = "animal.dog"
				return strings.Join(f, " ")
			})
		}},
		{"dwarf with an unknown role", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, roomPath, "dwarf ", func(l string) string {
				f := strings.Fields(l)
				f[4] = "king"
				return strings.Join(f, " ")
			})
		}},
		{"no grid", func(t *testing.T, m fstest.MapFS) (string, int) {
			setLines(m, roomPath, lines(t, m, roomPath)[:lineOf(t, m, roomPath, "grid ")-1])
			return roomPath, 0
		}},

		// animations.txt (§3.4 of the round-2 brief).
		{"first loop not idle", func(t *testing.T, m fstest.MapFS) (string, int) {
			n := lineOf(t, m, animationsPath, "anim ")
			ls := lines(t, m, animationsPath)
			ls[n-1], ls[n] = ls[n], ls[n-1]
			setLines(m, animationsPath, ls)
			return animationsPath, n
		}},
		{"loop for no role", func(t *testing.T, m fstest.MapFS) (string, int) {
			ls := lines(t, m, animationsPath)
			setLines(m, animationsPath, append(ls, "anim act_smith 300 300"))
			return animationsPath, len(ls) + 1
		}},
		{"second loop of a role", func(t *testing.T, m fstest.MapFS) (string, int) {
			ls := lines(t, m, animationsPath)
			setLines(m, animationsPath, append(ls, "anim act_worker 300 300 200"))
			return animationsPath, len(ls) + 1
		}},
		{"loop of one frame", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, animationsPath, lineOf(t, m, animationsPath, "anim act_diplomat"), "anim act_diplomat 700")
		}},
		{"duration not a multiple of 10", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, animationsPath, lineOf(t, m, animationsPath, "anim act_diplomat"), "anim act_diplomat 700 55")
		}},
		{"duration below 50", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, animationsPath, lineOf(t, m, animationsPath, "anim act_diplomat"), "anim act_diplomat 700 40")
		}},
		{"duration above 2000", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, animationsPath, lineOf(t, m, animationsPath, "anim act_diplomat"), "anim act_diplomat 700 2010")
		}},
		{"duration not a number", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, animationsPath, lineOf(t, m, animationsPath, "anim act_diplomat"), "anim act_diplomat 700 x")
		}},
		{"unknown animations keyword", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, animationsPath, "anim ", func(l string) string { return "loop" + strings.TrimPrefix(l, "anim") })
		}},
		{"missing animations.txt", func(t *testing.T, m fstest.MapFS) (string, int) {
			delete(m, animationsPath)
			return animationsPath, 0
		}},
		{"animations.txt without anim lines", func(t *testing.T, m fstest.MapFS) (string, int) {
			m[animationsPath] = &fstest.MapFile{Data: []byte("// no loops\n")}
			return animationsPath, 0
		}},

		// roster.txt (§3.5 of the round-2 brief).
		{"unknown roster keyword", func(t *testing.T, m fstest.MapFS) (string, int) {
			return insert(t, m, rosterPath, lineOf(t, m, rosterPath, "member "), "team peak")
		}},
		{"corps without emblem", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, rosterPath, "corps peak", func(l string) string { return l[:strings.LastIndex(l, " ")] })
		}},
		{"corps colour of another mask", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, rosterPath, "corps peak", "color=corps.crimson", "color=clothing.red")
		}},
		{"corps after the members", func(t *testing.T, m fstest.MapFS) (string, int) {
			ls := lines(t, m, rosterPath)
			setLines(m, rosterPath, append(ls, "corps hill color=corps.slate emblem=emblem.crescent"))
			return rosterPath, len(ls) + 1
		}},
		{"duplicate corps name", func(t *testing.T, m fstest.MapFS) (string, int) {
			return insert(t, m, rosterPath, lineOf(t, m, rosterPath, "corps peak")+1, "corps peak color=corps.slate emblem=emblem.crescent")
		}},
		{"second member of an outfit", func(t *testing.T, m fstest.MapFS) (string, int) {
			ls := lines(t, m, rosterPath)
			setLines(m, rosterPath, append(ls, "member miner diplomat peak"))
			return rosterPath, len(ls) + 1
		}},
		{"member of an undefined Corps", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, rosterPath, "member miner", " moon", " hill")
		}},
		{"member of three fields", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, rosterPath, "member miner", func(l string) string { return l[:strings.LastIndex(l, " ")] })
		}},
		{"no member", func(t *testing.T, m fstest.MapFS) (string, int) {
			for strings.Contains(string(m[rosterPath].Data), "\nmember ") {
				remove(t, m, rosterPath, "member ")
			}
			return rosterPath, 0
		}},
		{"missing roster.txt", func(t *testing.T, m fstest.MapFS) (string, int) {
			delete(m, rosterPath)
			return rosterPath, 0
		}},

		// Kinds, views and characters (§3.6 of the round-2 brief).
		{"Corps character in a part", func(t *testing.T, m fstest.MapFS) (string, int) {
			return editRow(t, m, frontBody, func(r string) string { return "b" + r[1:] })
		}},
		{"Corps character in a tile", func(t *testing.T, m fstest.MapFS) (string, int) {
			return editRow(t, m, wallTopMap, func(r string) string { return "B" + r[1:] })
		}},
		{"Corps character in an emblem", func(t *testing.T, m fstest.MapFS) (string, int) {
			n := rowWith(t, m, frontEmblem, "*")
			return replace(t, m, frontEmblem, n, strings.Replace(lines(t, m, frontEmblem)[n-1], "*", "b", 1))
		}},
		{"skin character in a tool", func(t *testing.T, m fstest.MapFS) (string, int) {
			return editRow(t, m, tqTool, func(r string) string { return "s" + r[1:] })
		}},
		{"skin character in the pennant", func(t *testing.T, m fstest.MapFS) (string, int) {
			return editRow(t, m, frontPennant, func(r string) string { return "s" + r[1:] })
		}},
		{"occlude character in the pennant", func(t *testing.T, m fstest.MapFS) (string, int) {
			return editRow(t, m, frontPennant, func(r string) string { return "x" + r[1:] })
		}},
		{"banner other than the pennant", func(t *testing.T, m fstest.MapFS) (string, int) {
			copyFile(m, frontPennant, "rpg/front/banner.flag.pxm")
			return "rpg/front/banner.flag.pxm", 0
		}},
		{"tool naming no role", func(t *testing.T, m fstest.MapFS) (string, int) {
			copyFile(m, tqTool, "rpg/three_quarter/tool.smith.pxm")
			return "rpg/three_quarter/tool.smith.pxm", 0
		}},
		{"banner on the overworld", func(t *testing.T, m fstest.MapFS) (string, int) {
			copyFile(m, frontPennant, "world/banner.pennant.pxm")
			return "world/banner.pennant.pxm", 0
		}},
		{"tool in the front view", func(t *testing.T, m fstest.MapFS) (string, int) {
			copyFile(m, tqTool, "rpg/front/tool.worker.pxm")
			return "rpg/front/tool.worker.pxm", 0
		}},
		{"palette without corps entries", func(t *testing.T, m fstest.MapFS) (string, int) {
			remove(t, m, palettePath, "entry B ")
			remove(t, m, palettePath, "entry b ")
			return palettePath, 0
		}},
		{"palette without light", func(t *testing.T, m fstest.MapFS) (string, int) {
			remove(t, m, palettePath, "entry * light")
			return palettePath, 0
		}},
		{"no corps choice", func(t *testing.T, m fstest.MapFS) (string, int) {
			for strings.Contains(string(m[colorsPath].Data), "\ncorps.") {
				remove(t, m, colorsPath, "corps.")
			}
			return colorsPath, 0
		}},
		{"canvas beyond the coordinate limit", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, bodyMap, lineOf(t, m, bodyMap, "canvas"), "canvas 65536 16")
		}},
		{"transparent with a mask", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, palettePath, "entry . transparent", " - ", " skin ")
		}},
	})
}

// tile returns a world tile map of w by h outline pixels.
func tile(w, h, ax int) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "canvas %d %d\nanchor %d %d\ncategory wall\nframe idle 0\n", w, h, ax, h)
	for range h {
		b.WriteString(strings.Repeat("#", w) + "\n")
	}
	return []byte(b.String())
}

func TestInconsistentArtIsRejected(t *testing.T) {
	runBreakages(t, []breakage{
		{"world part anchor 7 16", func(t *testing.T, m fstest.MapFS) (string, int) {
			p := "world/legwear.trousers.pxm"
			return replace(t, m, p, lineOf(t, m, p, "anchor"), "anchor 7 16")
		}},
		{"three-quarter part with another anchor", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, tqBody, lineOf(t, m, tqBody, "anchor"), "anchor 31 64")
		}},
		{"layer with another canvas", func(t *testing.T, m fstest.MapFS) (string, int) {
			p := "world/badge.diplomat.pxm"
			ls := lines(t, m, p)
			for i := lineOf(t, m, p, "frame "); i <= len(ls); i++ {
				if !strings.HasPrefix(ls[i-1], "frame ") {
					ls[i-1] += strings.Repeat(".", 16)
				}
			}
			setLines(m, p, ls)
			return replace(t, m, p, lineOf(t, m, p, "canvas"), "canvas 32 16")
		}},
		{"hat missing an idle frame", func(t *testing.T, m fstest.MapFS) (string, int) {
			setLines(m, hatMap, lines(t, m, hatMap)[:lineOf(t, m, hatMap, "frame idle 1")-1])
			return hatMap, 0
		}},
		{"first part without idle", func(t *testing.T, m fstest.MapFS) (string, int) {
			var parts []string
			for p := range m {
				first, _, _ := strings.Cut(strings.TrimPrefix(p, "world/"), ".")
				if strings.HasPrefix(p, "world/") && slices.Contains(slots, first) {
					parts = append(parts, p)
				}
			}
			p := slices.Min(parts)
			setLines(m, p, strings.Split(strings.ReplaceAll(strings.Join(lines(t, m, p), "\n"), "frame idle ", "frame rest "), "\n"))
			return p, 0
		}},
		{"front map of a part deleted", func(t *testing.T, m fstest.MapFS) (string, int) {
			delete(m, "rpg/front/shoes.boots.pxm")
			return "rpg/front/shoes.boots.pxm", 0
		}},
		{"three-quarter map of a part deleted", func(t *testing.T, m fstest.MapFS) (string, int) {
			delete(m, "rpg/three_quarter/shoes.boots.pxm")
			return "rpg/three_quarter/shoes.boots.pxm", 0
		}},
		{"world map of a part deleted", func(t *testing.T, m fstest.MapFS) (string, int) {
			delete(m, "world/shoes.boots.pxm")
			return "world/shoes.boots.pxm", 0
		}},
		{"tile in the front view", func(t *testing.T, m fstest.MapFS) (string, int) {
			copyFile(m, doorMap, "rpg/front/tile.door.pxm")
			return "rpg/front/tile.door.pxm", 0
		}},
		{"slot without a part", func(t *testing.T, m fstest.MapFS) (string, int) {
			delete(m, "world/front_accessory.pick.pxm")
			delete(m, "rpg/front/front_accessory.pick.pxm")
			delete(m, "rpg/three_quarter/front_accessory.pick.pxm")
			return gridWorld, 0
		}},
		{"badge deleted", func(t *testing.T, m fstest.MapFS) (string, int) {
			delete(m, "world/badge.griot.pxm")
			return "world/badge.griot.pxm", 0
		}},
		{"trim deleted", func(t *testing.T, m fstest.MapFS) (string, int) {
			delete(m, trimMap)
			return trimMap, 0
		}},
		{"badge copied over another", func(t *testing.T, m fstest.MapFS) (string, int) {
			copyFile(m, workerBadge, "world/badge.griot.pxm")
			return "world/badge.griot.pxm", lineOf(t, m, "world/badge.griot.pxm", "frame ")
		}},
		{"world tile anchor off the cell centre", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, doorMap, lineOf(t, m, doorMap, "anchor"), "anchor 7 16")
		}},
		{"world sprite of part cells", func(t *testing.T, m fstest.MapFS) (string, int) {
			m["world/tile.chip.pxm"] = &fstest.MapFile{Data: tile(8, 8, 4)}
			return "world/tile.chip.pxm", 1
		}},
		{"world sprite wider than the map sheet", func(t *testing.T, m fstest.MapFS) (string, int) {
			m["world/tile.long.pxm"] = &fstest.MapFile{Data: tile(320, 16, 8)}
			return "world/tile.long.pxm", 1
		}},
		{"unknown file", func(t *testing.T, m fstest.MapFS) (string, int) {
			m["notes.md"] = &fstest.MapFile{Data: []byte("notes\n")}
			return "notes.md", 0
		}},
		{"unknown directory", func(t *testing.T, m fstest.MapFS) (string, int) {
			m["extra/plan.txt"] = &fstest.MapFile{Data: []byte("plan\n")}
			return "extra", 0
		}},
		{"unknown file in a grid directory", func(t *testing.T, m fstest.MapFS) (string, int) {
			m["world/readme.txt"] = &fstest.MapFile{Data: []byte("readme\n")}
			return "world/readme.txt", 0
		}},
		{"choice ramp of wrong length", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, colorsPath, "role.worker", func(l string) string { return l + " 66cc66" })
		}},
		{"outfit naming an unknown part", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, outfitsPath, lineOf(t, m, outfitsPath, "part torso_clothing."), "part torso_clothing.robe")
		}},
		{"outfit naming an unknown choice", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, outfitsPath, "outfit ", func(l string) string {
				f := strings.Fields(l)
				f[4] = "clothing=clothing.green"
				return strings.Join(f, " ")
			})
		}},
		{"part override naming an unknown choice", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, outfitsPath, lineOf(t, m, outfitsPath, "part body."), "part body.stout clothing=clothing.green")
		}},
		{"room placement in column 10", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, roomPath, "place ", func(l string) string {
				f := strings.Fields(l)
				f[1] = "10"
				return strings.Join(f, " ")
			})
		}},
		{"room wider than the map sheet", func(t *testing.T, m fstest.MapFS) (string, int) {
			n := lineOf(t, m, roomPath, "grid ")
			ls := lines(t, m, roomPath)
			f := strings.Fields(ls[n-1])
			cols, _ := strconv.Atoi(f[1])
			rows, _ := strconv.Atoi(f[2])
			for i := n; i < n+rows; i++ {
				ls[i] += ls[i]
			}
			ls[n-1] = fmt.Sprintf("grid %d %d", 2*cols, rows)
			setLines(m, roomPath, ls)
			return roomPath, n
		}},
		{"room cell naming an unknown tile", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, roomPath, "cell ", func(l string) string {
				f := strings.Fields(l)
				f[2] = "tile.lava"
				return strings.Join(f, " ")
			})
		}},
		{"room cell naming a missing frame", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, roomPath, "cell ", func(l string) string {
				f := strings.Fields(l)
				f[4] = "9"
				return strings.Join(f, " ")
			})
		}},
		{"room terrain reaching outside", func(t *testing.T, m fstest.MapFS) (string, int) {
			m["world/tile.wide.pxm"] = &fstest.MapFile{Data: tile(32, 16, 24)}
			grid := lineOf(t, m, roomPath, "grid ")
			first := lines(t, m, roomPath)[grid][0]
			n := lineOf(t, m, roomPath, "cell "+string(first)+" ")
			f := strings.Fields(lines(t, m, roomPath)[n-1])
			replace(t, m, roomPath, n, "cell "+f[1]+" tile.wide idle 0")
			return roomPath, grid + 1
		}},
		{"place naming an unknown sprite", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, roomPath, "place ", func(l string) string {
				f := strings.Fields(l)
				f[3] = "animal.cat"
				return strings.Join(f, " ")
			})
		}},
		{"place naming a missing frame", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, roomPath, "place ", func(l string) string {
				f := strings.Fields(l)
				f[5] = "9"
				return strings.Join(f, " ")
			})
		}},
		{"dwarf naming an unknown outfit", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, roomPath, "dwarf ", func(l string) string {
				f := strings.Fields(l)
				f[3] = "tailor"
				return strings.Join(f, " ")
			})
		}},
		{"dwarf frame beyond idle", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, roomPath, "dwarf ", func(l string) string {
				f := strings.Fields(l)
				f[5] = "2"
				return strings.Join(f, " ")
			})
		}},
		{"dwarf outside the room", func(t *testing.T, m fstest.MapFS) (string, int) {
			return rewrite(t, m, roomPath, "dwarf ", func(l string) string {
				f := strings.Fields(l)
				f[2] = "8"
				return strings.Join(f, " ")
			})
		}},
		{"three-quarter emblem on a wider canvas", func(t *testing.T, m fstest.MapFS) (string, int) {
			ls := lines(t, m, tqMountain)
			for i := lineOf(t, m, tqMountain, "frame "); i <= len(ls); i++ {
				if !strings.HasPrefix(ls[i-1], "frame ") {
					ls[i-1] += strings.Repeat(".", 64)
				}
			}
			setLines(m, tqMountain, ls)
			return replace(t, m, tqMountain, lineOf(t, m, tqMountain, "canvas"), "canvas 128 64")
		}},
		{"front map with a second still frame", func(t *testing.T, m fstest.MapFS) (string, int) {
			_, rows := frameRows(t, m, frontBody, "frame still 0")
			setLines(m, frontBody, append(append(lines(t, m, frontBody), "frame still 1"), rows...))
			return frontBody, 0
		}},
		{"front map naming idle", func(t *testing.T, m fstest.MapFS) (string, int) {
			replace(t, m, frontBody, lineOf(t, m, frontBody, "frame still 0"), "frame idle 0")
			return frontBody, 0
		}},
		{"three-quarter part without act_diplomat", func(t *testing.T, m fstest.MapFS) (string, int) {
			truncate(t, m, tqBody, "frame act_diplomat 0")
			return tqBody, 0
		}},
		{"three-quarter part with a third idle frame", func(t *testing.T, m fstest.MapFS) (string, int) {
			_, rows := frameRows(t, m, tqBody, "frame idle 1")
			at := lineOf(t, m, tqBody, "frame act_worker 0") - 1
			setLines(m, tqBody, slices.Insert(lines(t, m, tqBody), at, append([]string{"frame idle 2"}, rows...)...))
			return tqBody, 0
		}},
		{"tool without its last frame", func(t *testing.T, m fstest.MapFS) (string, int) {
			truncate(t, m, tqTool, "frame act_worker 2")
			return tqTool, 0
		}},
		{"tool for a role without a loop", func(t *testing.T, m fstest.MapFS) (string, int) {
			p := "rpg/three_quarter/tool.griot.pxm"
			m[p] = &fstest.MapFile{Data: []byte(strings.ReplaceAll(string(m[tqTool].Data), "frame act_worker", "frame act_griot"))}
			return p, 0
		}},
		{"tool loop removed from animations.txt", func(t *testing.T, m fstest.MapFS) (string, int) {
			remove(t, m, animationsPath, "anim act_worker")
			return tqTool, 0
		}},
		{"three-quarter pennant deleted", func(t *testing.T, m fstest.MapFS) (string, int) {
			delete(m, tqPennant)
			return tqPennant, 0
		}},
		{"front crescent deleted", func(t *testing.T, m fstest.MapFS) (string, int) {
			delete(m, "rpg/front/emblem.crescent.pxm")
			return "rpg/front/emblem.crescent.pxm", 0
		}},
		{"every front emblem deleted", func(t *testing.T, m fstest.MapFS) (string, int) {
			for p := range m {
				if strings.HasPrefix(p, "rpg/front/emblem.") {
					delete(m, p)
				}
			}
			return "rpg/front", 0
		}},
		{"emblem copied over another", func(t *testing.T, m fstest.MapFS) (string, int) {
			copyFile(m, tqMountain, tqCrescent)
			return tqMountain, lineOf(t, m, tqMountain, "frame ")
		}},
		{"emblem off the pennant's field", func(t *testing.T, m fstest.MapFS) (string, int) {
			n, rows := frameRows(t, m, tqMountain, "frame act_diplomat 1")
			for i, r := range rows {
				if strings.Contains(r, "#") {
					return replace(t, m, tqMountain, n+1+i, r[20:]+r[:20])
				}
			}
			t.Fatalf("%s act_diplomat 1 has no glyph", tqMountain)
			return "", 0
		}},
		{"views disagreeing on occlusion", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, tqHat, lineOf(t, m, tqHat, "occludes"), "occludes hair_beard torso_clothing")
		}},
		{"map directly under rpg", func(t *testing.T, m fstest.MapFS) (string, int) {
			copyFile(m, frontBody, "rpg/body.stout.pxm")
			return "rpg/body.stout.pxm", 0
		}},
		{"unknown view directory", func(t *testing.T, m fstest.MapFS) (string, int) {
			copyFile(m, frontBody, "rpg/side/x.pxm")
			return "rpg/side", 0
		}},
		{"Corps colour of a role", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, colorsPath, lineOf(t, m, colorsPath, "corps.slate"), "corps.slate 2e6e34 4caa50")
		}},
		{"Corps colour near the light", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, colorsPath, lineOf(t, m, colorsPath, "corps.slate"), "corps.slate cfc8b8 e8e0d0")
		}},
		{"Corps colour near another Corps", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, colorsPath, lineOf(t, m, colorsPath, "corps.slate"), "corps.slate 6e1e22 a2342e")
		}},
		{"roster naming an unknown outfit", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, rosterPath, lineOf(t, m, rosterPath, "member miner"), "member tinker worker moon")
		}},
		{"roster naming an unknown role", func(t *testing.T, m fstest.MapFS) (string, int) {
			return replace(t, m, rosterPath, lineOf(t, m, rosterPath, "member miner"), "member miner smith moon")
		}},
		{"Corps naming an unknown colour", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, rosterPath, "corps peak", "corps.crimson", "corps.teal")
		}},
		{"Corps naming an unknown emblem", func(t *testing.T, m fstest.MapFS) (string, int) {
			return swap(t, m, rosterPath, "corps peak", "emblem.mountain", "emblem.star")
		}},
	})
}
