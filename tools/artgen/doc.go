// Command artgen renders the DwarfHarness art sample from text pixel maps.
//
// Run it from the module root, with no arguments:
//
//	GOTOOLCHAIN=local CGO_ENABLED=0 go run ./tools/artgen
//
// It reads the inputs under tools/artgen/art and checks all of them. Only if
// every check passes does it write the sample to tools/artgen/sample, then
// remove every file and empty directory there that the new sample does not
// contain. On failure it prints each error as path:line: message, with paths
// relative to art, and writes nothing. artgen is a development tool: it never
// ships and makes no model call.
//
// # Inputs
//
// Inputs are text of printable ASCII and LF line endings; only LICENCE.txt may
// hold other UTF-8. A line starting with // is a comment, and comments and
// blank lines may appear anywhere except inside a grid. Fields are separated
// by spaces. IDs have the form <kind-or-slot>.<name>.
//
//   - palette.txt: "entry <char> <name> <mask> <rrggbbaa>" lines define the
//     palette in index order. Entry 0 is transparent and one entry is named
//     occlude, both with alpha 00; every other entry has alpha ff. The entries
//     of a mask (skin, hair, clothing or role), in index order, form its ramp
//     from shadow to light. "texture <char> <entry>:<weight> ..." defines a
//     character that resolves per pixel to one of its entries, chosen by a
//     splitmix64 hash of the tile's seed and the pixel's position.
//   - colors.txt: "<mask>.<name> <rrggbb> ..." color choices with one color per
//     ramp entry, including role.<role> for each of Core's seven roles.
//   - outfits.txt: "outfit <name> skin=<choice> hair=<choice>
//     clothing=<choice>", each followed by its "part <part-id>
//     [clothing=<choice>]" lines: the variant combinations the sheets show.
//   - room.txt: "cell <char> <tile-id> <anim> <frame>" lines, then "grid <cols>
//     <rows>" and its rows of cell characters, then "place <col> <row>
//     <sprite-id> <anim> <frame>" and "dwarf <col> <row> <outfit> <role>
//     <frame>" lines, drawn in that order with each anchor on its cell's
//     ground point.
//   - LICENCE.txt: the licence statement, copied to the sample.
//   - world/<id>.pxm and rpg/<id>.pxm: pixel maps on the 16-pixel and 64-pixel
//     grids. A header of "canvas <w> <h>", "anchor <x> <y>" and, where they
//     apply, "occludes <slot> ...", "category <category>" and "seed <n>" comes
//     before "frame <anim> <index>" blocks of exactly h rows of w palette or
//     texture characters, one character per pixel.
//
// The first segment of an ID names the kind. A slot name (back_accessory,
// body, shoes, legwear, torso_clothing, hair_beard, headwear and
// front_accessory, in layer order) makes a part, which needs a map on both
// grids. overlay.trim, badge.<role>, tile.* and animal.* exist on the world
// grid only. On each grid, every part, the trim and every badge share one
// canvas, anchor and animation list, so the layers register.
//
// # Outputs
//
// tools/artgen/sample holds world/<id>.png and rpg/<id>.png, one indexed PNG
// strip per sprite and grid with its untrimmed frames left to right;
// sample.json; provenance.json; LICENCE.txt; and sheets/, the map and character
// contact sheets at 1x and 4x, with index.tsv naming every cell by sheet,
// section and position.
//
// sample.json is a draft manifest for ticket 107 to inspect, not the manifest
// it freezes. It lists the palette, the occlude entry (occluder), the palette
// indices of each mask, the color choices and the role overlay: the trim, and
// each role's badge and color. Per sprite, in ID order, it gives the kind; the
// slot, occluded slots, category or role where they apply; and per grid image
// the path, SHA-256, canvas, anchor (the ground point in pixel-edge
// coordinates), the masks its pixels use, and each animation's frame
// rectangles [x, y, w, h] within the strip. contentSha256 is the SHA-256 of one
// sha256sum line per sprite PNG in path order. Recoloring a layer with a
// choice replaces the k-th entry of the mask's ramp with the k-th color of the
// choice. The occlude pixels of a part hide the pixels of the slots it
// occludes.
//
// provenance.json records the licence digest; the generator package, the
// SHA-256 of its non-test Go sources (sourceSha256) and the command; the
// toolchain; the linked modules; the texture seeds; and the SHA-256 of every
// input and output file.
//
// # Regenerating
//
// Any change to the inputs or to the package's non-test .go files changes the
// sample, so rerun the command and commit the result with the change.
// TestSampleIsReproducible fails while the committed sample is stale.
//
// STYLE.md, next to this file, holds the art direction for map authors.
package main
