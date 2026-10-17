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
// # Views
//
// A view is one way of drawing the layered dwarf. Each view has a directory
// of pixel maps, a grid, a facing, and the canvas and anchor that all of its
// layers share:
//
//	view           directory           grid   facing               canvas  anchor
//	overworld      world/              world  front                16x16   (8,16)
//	front          rpg/front/          rpg    front                64x64   (32,64)
//	three_quarter  rpg/three_quarter/  rpg    three_quarter_right  64x64   (32,64)
//
// Overworld layers share the animation list of the alphabetically first part,
// which must have idle. Front layers have one animation, still, of one frame.
// Three-quarter layers have exactly the loops of animations.txt.
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
//     occlude, both with alpha 00; every other entry has alpha ff; entries
//     named outline and light exist without a mask. The entries of a mask
//     (skin, hair, clothing, role or corps), in index order, form its ramp
//     from shadow to light: two to four steps, of which a fourth, the
//     highlight, may appear only in the 64-pixel views.
//     "texture <char> <entry>:<weight> ..." defines a character that
//     resolves per pixel to one of its entries, chosen by a splitmix64 hash
//     of the tile's seed and the pixel's position.
//   - colors.txt: "<mask>.<name> <rrggbb> ..." color choices with one color per
//     ramp entry, including role.<role> for each of Core's seven roles and at
//     least one corps.<name>.
//   - outfits.txt: "outfit <name> skin=<choice> hair=<choice>
//     clothing=<choice>", each followed by its "part <part-id>
//     [clothing=<choice>]" lines: the variant combinations the sheets show.
//   - room.txt: "cell <char> <tile-id> <anim> <frame>" lines, then "grid <cols>
//     <rows>" and its rows of cell characters, then "place <col> <row>
//     <sprite-id> <anim> <frame>" and "dwarf <col> <row> <outfit> <role>
//     <frame>" lines, drawn in that order with each anchor on its cell's
//     ground point.
//   - animations.txt: "anim <name> <ms> ..." lines, the three-quarter loops in
//     strip order with one duration per frame: idle first, then at most one
//     act_<role> per role, each of two frames or more, each duration a
//     multiple of 10 from 50 to 2000 ms.
//   - roster.txt: "corps <name> color=<corps choice> emblem=<emblem id>"
//     lines, then "member <outfit> <role> <corps>" lines, one outfit per
//     member. The roster is an example for the character sheet and the loop
//     previews, not pack data: a Corps's colour and emblem belong to the
//     Corps and are chosen in the GUI.
//   - LICENCE.txt: the licence statement, copied to the sample.
//   - <view directory>/<id>.pxm: pixel maps. A header of "canvas <w> <h>",
//     "anchor <x> <y>" and, where they apply, "occludes <slot> ...",
//     "category <category>" and "seed <n>" comes before "frame <anim> <index>"
//     blocks of exactly h rows of w palette or texture characters, one
//     character per pixel.
//
// The first segment of an ID names the kind. A slot name (back_accessory,
// body, shoes, legwear, torso_clothing, hair_beard, headwear and
// front_accessory, in layer order) makes a part, which needs a map in every
// view and occludes the same slots in each. overlay.trim, badge.<role>,
// tile.* and animal.* exist in the overworld only. banner.pennant, the Corps
// marker's flag, and emblem.<name> exist in both 64-pixel views, each emblem
// in both. tool.<role> exists in the three-quarter view only, with the one
// animation act_<role>. Every emblem pixel lies on the pennant's corps field
// in the same frame.
//
// A dwarf is drawn in a view as its parts in slot order, then in the
// overworld the role's trim and badge, in a 64-pixel view the pennant in the
// Corps colour and the Corps's emblem, and in the three-quarter view the
// role's tool while its act_<role> loop plays.
//
// # Outputs
//
// tools/artgen/sample holds one indexed PNG strip per sprite and view, at
// <view directory>/<id>.png, with its untrimmed frames left to right;
// sample.json; provenance.json; LICENCE.txt; and sheets/, the map and character
// contact sheets at 1x and 4x, index.tsv naming every cell by sheet, section
// and position, and page-<outfit>-<anim>-4x.gif, one looping preview per
// roster member and loop. The previews and sheets are review outputs, not pack
// content: sample.json does not reference them, and a loader must load only
// what sample.json references.
//
// sample.json is a draft manifest for ticket 107 to inspect, not the manifest
// it freezes. It lists the grids; the views with their facing, canvas, anchor,
// layers in draw order and animations (with durationsMs from animations.txt
// for the three-quarter view); the palette, the occlude entry (occluder), the
// palette indices of each mask, and the color choices; the role overlay (the
// trim, and each role's badge and color); and the Corps marker (the banner,
// its mask and every emblem). The three-quarter view also lists states: idle,
// then one working state per act_<role> loop, naming the tool where one
// exists. The state names are presentation labels: Core's actual work status
// selects the state, and no animation sets or implies a status. Per sprite, in
// ID order over all views, it gives the kind; the slot, occluded slots,
// category or role where they apply; and per view image, in view order, the
// grid, view, path, SHA-256, canvas, anchor (the ground point in pixel-edge
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
