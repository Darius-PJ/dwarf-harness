# DwarfHarness art style (generator inputs)

Read this before writing or changing a pixel map. The owner reviews the result on
the contact sheets and the loop previews; nobody edits pixels by hand.

These are the map author's working rules. The owner's recorded review comments
override any rule here; his approval of the sheets (G2) approves the art, not this
text.

## Views and light
- Overworld (16 px, world/): three-quarter top-down; the camera looks down at the
  scene from the south. Dwarves and every tile face the viewer; the dog, the horse
  and the wheelbarrow face east, and the rail runs east-west.
- Front still (64 px, rpg/front/): the dwarf at eye level, facing the viewer
  straight on, in its rest pose. One frame. Rosters show it.
- Three-quarter (64 px, rpg/three_quarter/): the same dwarf at eye level, turned
  45 degrees toward screen right. Its right side is the near side, on screen left;
  its left side is the far side, on screen right and partly hidden. Animated. The
  agent's page shows it.
- Every view and every frame is its own pixel map. Never derive one from another by
  mirroring, rotating, scaling or shifting a shape in code.
- One light, from the upper left, in every view. Top and left surfaces take the
  lightest step of their ramp; bottom and right take the darkest.

## Palette
- Use only palette.txt. Keep it to 40 entries or fewer. No partial transparency.
- Every material is a ramp of three steps (two for role trim, Corps colour and
  spark): shadow, base, light. Within a ramp, uppercase is the darkest step,
  lowercase the next and a digit the lightest. Shadows lean cool (toward violet),
  lights lean warm (toward yellow). No pure black or pure white.
- Skin, hair and clothing use only their mask characters so users can recolor them.
  Fabric and leather on clothes, shoes, hats and packs use the clothing ramp; wood
  and metal bits stay fixed.
- The role ramp (T t) belongs to overlay.trim alone and the Corps ramp (B b) to
  banner.pennant alone. Badges and emblems use only # and *.
- Corps colours come from hues the role colours leave free: reds, earths and
  neutrals. A Corps base sits mid-gray, at least 48 above the outline and 48 below
  the light in luma, so its emblem reads in the gray rows. The generator rejects a
  Corps colour that breaks this or lies too close to a role colour or another Corps
  colour. These thresholds (48 in luma, 64 in RGB distance) are generator rules,
  not owner decisions: if a review comment names a colour they reject, the review
  round changes the threshold and records the change.

## Outline and shading
- Every sprite and every part has a closed one-pixel outline (#) around its own
  silhouette, so any combination of parts stays outlined.
- At 16 px, interior lines may use # sparingly (eyes, belt). At 64 px, separate
  interior forms with the darker material's shadow shade, not #.
- Shade by form, not around the edges (no pillow shading). No dithering and no
  hand anti-aliasing. Avoid orphan pixels except deliberate glints (eyes, sparks).
- Draw curves and diagonals with even steps (1-1, 2-1, 2-2); no jaggies.

## Dwarves on the overworld (16 px)
- Stocky and beard-first: the beard is the strongest shape after the silhouette.
- Canvas 16 x 16, anchor (8,16); rows count from 0 at the top. The head's top
  outline is on row 2 and the face fills rows 3-6. Row 7 is the shoulder line; the
  torso and arms fill rows 8-11, the legs rows 12-13 and the feet row 14, with the
  sole outline on row 15. The stout body is 14 px wide with its arms (x 1-14) and
  8 px at head and torso (x 4-11), centred on x = 8.
- A hair cap starts on the head's top outline (row 2); a topknot's bun takes rows
  0-1 above it, with its band on row 2. The beard is widest (x 3-12) on rows 6-8;
  its lowest pixels are on row 10 and its outline on row 11.
- Hats use rows 0-4: the crown on rows 1-2, the brim on rows 2-4. A hat's x pixels
  sit in rows 0-1, where a bun would show above the crown.
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

## Dwarves on the RPG views (64 px)
- Canvas 64 x 64, anchor (32,64) in both views; the ground point is x = 32 on the
  bottom edge. In the front still the crown's top outline is on row 14 and the
  shoulder line on row 30; the arms reach row 47, the legs fill rows 48-55 and the
  feet rows 56-62, with the sole outline on row 63. Hair and beard run from row 13
  (a hair cap) to row 46 (the braids' outline), and a topknot bun rises to row 5. A
  hat's top outline is on row 9, its x pixels cover rows 5-8 above it, and its brim
  covers rows 16-20. A bedroll pack spans rows 19-29 beside the head. The stout body
  is 36 px wide with its arms (x 14-49), centred on x = 32.
- Both views show the same dwarf as the overworld sprite: same parts, same mask use
  and the same silhouette features (beard shape, braids, bun, hat brim, pack). The
  three-quarter idle frames keep the rows listed above within 1 px; a held accessory
  and the pennant pole may end lower, as the turned pose needs.
- The 64 px sprites carry no role trim, badge or crest in either view. The page's
  role action shows the role; rosters show it as text.
- Front still: straight on, rest pose, arms at the sides, weight on both feet, a
  front accessory in the hand on screen left. Draw both halves; never mirror a half
  in code.
- Three-quarter construction: the body shows narrower than in the front still
  (about 28-34 px with the arms), and the near side shows more. Nose, beard and toes
  point toward screen right. The far eye is one or two pixels or hidden; the far arm
  and far foot are mostly hidden by the body; the near arm overlaps the torso's
  screen-left edge. A back accessory shows behind the body on screen left. The near
  leg stands in front and the far leg shows 4-5 px beside it, the line between them
  drawn in the shadow step, never #. Both soles end on row 63, the near foot a
  little left of the far one.
- In idle frames the near (right) hand holds what the front accessory holds. During
  a role action it holds the role's tool or makes the action's gesture, and a front
  accessory held in that hand is stowed on the back, across the pack, in that
  action's frames; only the pixels that show from the front are drawn.
  A stowed accessory lies across the pack and assumes it: give an outfit with such an
  accessory the pack too, until ticket 108 settles how a stowed accessory reads
  without one.
- tool.<role> holds the action's equipment: the tool in the near hand and, where the
  action needs a target, one fixed prop (the worker's anvil). Both exist only while
  act_<role> plays, so they appear with the action; nothing in the layer carries a
  role mark.
- Layers keep their order in every frame. Where the near arm or hand must pass in
  front of a later layer (beard, hat, front accessory), that later layer leaves
  those pixels empty in that frame so the arm shows. Pose actions so such crossings
  stay small. A tool map leaves its handle open where the fist closes round it.
- The hat's x pixels cover exactly the hair that would show above its brim, in every
  view and every frame; the topknot reaches into them so the occlusion is visible.

## Animation (three-quarter loops)
- animations.txt lists the loops and their frame durations. idle plays while the
  agent is idle; act_<role> plays while an agent of that role works. Core's actual
  work status chooses the loop; a loop never implies a status Core did not report.
- With no role marker on the 64 px sprites, a role action must read as that role
  from its silhouette and motion alone, at 1x.
- Few frames, strong key poses. idle: rest, breath. act_worker: ready, raise,
  strike: the body leans into the blow, the arm reaches forward and the hammer
  lands on the anvil with a spark. act_diplomat: upright, bowed.
- Let timing ease the motion: hold key poses longer in animations.txt instead of
  adding in-between frames. The anticipation (the raise) holds; the contact (the
  strike) is brief: act_worker plays 250 ms ready, 400 ms raise, 150 ms strike.
- Move whole pixels. The breath settles chest, shoulders, head, beard, hat, pack,
  pennant and emblem by 1 px, as on the overworld; legs and feet stay. The bow
  bends the upper body forward from the waist: the torso leans toward screen right,
  the head, beard and hat drop about 7 px forward and 7 px down, and pack, pennant
  and emblem follow the back; hips, legs and feet stay. The strike leans into the
  blow: head, beard, hat, pack, pennant and emblem move (+2,+1) and the torso
  slants from +2 at the shoulders to 0 at the hips; hips, legs and feet stay. A
  tool travels in an arc. In a three-frame blow each frame is a key pose, so the
  tool may move far between frames.
- Every layer has every frame. A layer that does not move repeats its rows exactly;
  layers that ride on the body (clothing, beard, hat, pack, pennant, emblem) move
  with it by the same amount so seams stay closed (collar, belt, brim, pole). Where
  the canvas edge or another layer stops a riding layer short, it moves as far as it
  can and its seam hides behind another layer: in the bow the pennant moves (+2,+2)
  against the shoulders' (+4,+4), the finial at the canvas edge and the flag clear
  of the hat crown, with the pole's base behind the bowed head.
- Keep volumes: a part keeps its size from frame to frame unless the pose changes
  it, and its outline stays closed in every frame.
- No smears, motion blur, sub-pixel in-betweens or anti-aliasing.
- The last frame leads back into frame 0 without a jump.
- Check every loop in its preview (sheets/page-*.gif), not only frame by frame.

## Corps marker (64 px)
- A Corps shows its colour and emblem on banner.pennant: a flag on a short wooden
  pole that rises from behind the dwarf's left shoulder, upright or slanting
  outward. In the front still the flag flies above that shoulder on screen right,
  beside the hat's crown and clear of its brim; in the three-quarter view, where
  that shoulder is the far one, it flies above and behind it. Draw only the pole
  and flag that show; the pole may cross a pack. The pole never crosses a hat
  brim except in the diplomat's bow, where the bent back carries it across the
  brim; there the pole is drawn over the brim. The generator draws the marker over
  every slot layer, and only a role tool may cross it.
- The flag is a field of b (base) with B (shadow) for folds and its shaded edge, a
  # outline and a metal finial. Its field gives at least 11 x 11 px of base around
  a 7 x 7 emblem box. No * on the flag outside the emblem.
- emblem.<name> is a 7 x 7 glyph, * with a # outline, drawn in the emblem box on the
  same canvas so that it rides the flag frame by frame. The generator rejects an
  emblem pixel that does not lie on the flag's Corps field. Emblems differ from each
  other in silhouette and avoid the badge shapes (hammer T, cross +, ring, X) and
  the crown row.
- The flag and emblem never cover the head, hat, beard, hands or a held accessory in
  any frame of the example outfits.

## Tiles and props
- Rock is darker and busier than floor; floor stays low in contrast so dwarves
  read on it. Texture (% and ~) only in rock and floor tiles, and only on interior
  pixels; draw edges and cracks explicitly.
- wall_top is the rock mass seen from above and joins its neighbours without a
  seam: its four borders are plain base stone and its few marks lie inside;
  wall_face is the lighter vertical face with horizontal strata. The door is wood
  planks with metal bands.
- Station, stockpile and rail are transparent sprites drawn over floor.
- Motion frames of tiles, props and animals move 1-2 px at most; frame 0 is the
  rest pose.

## Readability at 1x
- Check every sprite at 1x on the sheet and in the room before asking for review.
  On the overworld each role must be told apart at 1x by its badge alone (see the
  gray row); on the RPG views each Corps by its emblem alone (see the gray rows).
- No feature smaller than 2 px except eyes and glints.

## Originality
- Draw everything new. Do not trace or recreate any existing sprite, character
  kit or Dwarf Fortress tile. Open-source projects may teach technique only; record
  any you study under the ticket's Comments (project, licence, what was learned,
  whether code was reused).
