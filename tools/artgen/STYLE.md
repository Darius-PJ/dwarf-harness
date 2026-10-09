# DwarfHarness art style (generator inputs)

Read this before writing or changing a pixel map. The owner reviews the result on
the contact sheets; nobody edits pixels by hand.

These are the map author's working rules. The owner's recorded review comments
override any rule here; his approval of the sheets (G2) approves the art, not this
text.

## View and light
- Three-quarter top-down front view: the camera looks down at the scene from the
  south. Dwarves, the dog and every tile face the viewer; the horse and wheelbarrow
  face east and the rail runs east-west.
- One light, from the upper left. Top and left surfaces take the lightest step of
  their ramp; bottom and right take the darkest.

## Palette
- Use only palette.txt. Keep it to 40 entries or fewer. No partial transparency.
- Every material is a ramp of three steps (two for role trim and spark): shadow,
  base, light. Within a ramp, uppercase is the darkest step, lowercase the next and
  a digit the lightest. Shadows lean cool (toward violet), lights lean warm (toward
  yellow). No pure black or pure white.
- Skin, hair and clothing use only their mask characters so users can recolor them.
  Fabric and leather on clothes, shoes, hats and packs use the clothing ramp; wood
  and metal bits stay fixed.
- The role ramp (T t) belongs to overlay.trim alone. Badges use only # and *.

## Outline and shading
- Every sprite and every part has a closed one-pixel outline (#) around its own
  silhouette, so any combination of parts stays outlined.
- At 16 px, interior lines may use # sparingly (eyes, belt). At 64 px, separate
  interior forms with the darker material's shadow shade, not #.
- Shade by form, not around the edges (no pillow shading). No dithering and no
  hand anti-aliasing. Avoid orphan pixels except deliberate glints (eyes, sparks).
- Draw curves and diagonals with even steps (1-1, 2-1, 2-2); no jaggies.

## Dwarves
- Stocky and beard-first: the beard is the strongest shape after the silhouette.
- 16 px canvas, anchor (8,16): head and beard in rows 1-8, torso rows 8-12, legs
  rows 12-14, feet on row 15. Body about 10-12 px wide, centred on x = 8. Hats may
  use rows 0-4.
- The badge box is x 11-15, y 0-5, drawn above every layer. Badge.<role> fills it:
  a 3 x 3 glyph in * with a # outline in rows 1-5; Grand badges add a crest (a
  crown row such as *.*.* ) on row 0. Base glyphs differ in silhouette: worker T
  (hammer), orchestrator + (baton cross), griot hollow square ring (story circle),
  diplomat X. Every badge must differ from every other in shape, without color.
- overlay.trim is a role-colored belt or sash across the waist, at least 6 pixels,
  using T and t only.
- Idle frame 1: rows 0-11 shift down one pixel, row 11 merges into row 12, rows
  12-15 stay. All parts and the overlay follow the same shift so the layers stay
  registered.
- 64 px canvas, anchor (32,64): the same dwarf at higher detail. Feet on row 63,
  about 44-52 px tall and 32-40 px wide, centred on x = 32; hats stay below row 4.
  Same parts, same mask use and the same silhouette features as the 16 px version.
- The hat's x pixels cover exactly the hair that would show above its brim; the
  topknot must reach into them so the occlusion is visible.

## Tiles and props
- Rock is darker and busier than floor; floor stays low in contrast so dwarves
  read on it. Texture (% and ~) only in rock and floor tiles, and only on interior
  pixels; draw edges and cracks explicitly.
- wall_top is the rock mass seen from above; wall_face is the lighter vertical
  face with horizontal strata. The door is wood planks with metal bands.
- Station, stockpile and rail are transparent sprites drawn over floor.
- Motion frames move 1-2 px at most; frame 0 is the rest pose.

## Readability at 1x
- Check every sprite at 1x on the sheet and in the room before asking for review.
  Each role must be told apart at 1x by its badge alone (see the gray row).
- No feature smaller than 2 px except eyes and glints.

## Originality
- Draw everything new. Do not trace or recreate any existing sprite, character
  kit or Dwarf Fortress tile. Open-source projects may teach technique only; record
  any you study under the ticket's Comments (project, licence, what was learned,
  whether code was reused).
