# Sprite and tileset tool options

Evidence checked **2026-10-08**, before the owner decided whether to buy an Aseprite licence. This is source and documentation research only. No tool was installed, built or run, and no exported output was compared.

**Outcome:** on 2026-10-08 the owner approved the Go generator and kept importable replacement packs ([D103](../../docs/DECISIONS.md#d103-default-fortress-art-a-go-generator-replaces-aseprite)). The comparison below is the evidence for that choice. [D105](../../docs/DECISIONS.md#d105-default-fortress-art-go-modules-for-sprite-generation-and-art-rounds-before-the-gui) later allowed Go modules from outside the standard library to assist sprite generation, each recorded under D29.

## What the pipeline needs

Tickets [88](../dwarf-harness/issues/88-generate-a-minimal-original-layered-art-sample-with-the-go-art-generator.md) and [108](../dwarf-harness/issues/108-generate-the-complete-original-default-fortress-art-pack.md) and [§6 of the fortress spec](spec.md#6-chosen-sprite-and-tileset-pipeline) set the bar. The owner chose agent-scripted art, so the tool must:

- let version-controlled scripts and seeds build the art without a GUI on Windows: dwarf parts in eight layer slots on 16-pixel and 64-pixel grids, palette masks, tiles, props and animals with a bounded number of motion frames;
- export transparent, untrimmed per-layer PNG frames, or an atlas with source rectangles and offsets, plus JSON, from a pinned command that reproduces its output;
- allow personal and commercial use of the exported art without shipping the editor. The runtime reads only PNGs and the project's own manifest, and §6 already allows "other tools" to emit that manifest without changing the GUI contract.

## Summary

| Option | Cost and licence | Scripts build art without a GUI | Per-layer PNG, sheet and JSON export | Layered source a person can edit | Latest release |
|---|---|---|---|---|---|
| Aseprite, bought | $19.99 once; proprietary EULA; one licence per developer in a company | Yes (Lua in `--batch`) | Yes, built in | Yes | v1.3.18.6, 2026-09-22 |
| Aseprite, compiled from source | Free; the EULA allows compiling only "for your own personal purpose" | Yes | Yes | Yes | Same source |
| LibreSprite | Free; GPL-2.0 | Partly: scripts run in batch mode but cannot create a new sprite there | Yes | Yes; layer groups are flattened | v1.3, 2026-10-02 |
| Pixelorama | Free; MIT | No documented way | Partly: its JSON is the project file, not an atlas | Yes | v1.2.3, 2026-09-15 |
| Krita, GIMP 3 | Free; GPL-3.0 | Krita: undocumented runner; GIMP: batch Python-Fu | Only through custom scripts | Yes; Krita has no indexed colour | 5.3.4; 3.2.6 |
| GrafX2, Piskel | Free; GPL-2.0, Apache-2.0 | No | GrafX2: none; Piskel: PNG sheet only, through a PhantomJS CLI | Yes | v2.9 (2024-04-28); v0.15.0 (2018-11-25) |
| Go generator, no editor | Free; Go standard library (BSD-3-Clause), already in the toolchain | Yes: the generator is the script | Yes, written by project code | Only if it also writes `.aseprite` files | Go 1.27 |

## Aseprite

**Fit.** The Lua API creates everything the tickets need in a batch run: `Sprite(width, height, colorMode)`, `sprite:newLayer()`, `sprite:newFrame()`, `sprite:newCel()`, `image:drawPixel()` and `sprite:saveAs()`, and `app.isUIAvailable` is false under `--batch` ([API](https://www.aseprite.org/api/), [app](https://github.com/aseprite/api/blob/main/api/app.md)). The [CLI](https://www.aseprite.org/docs/cli/) runs scripts with `-b --script file.lua --script-param name=value`, and exports with `--split-layers`, `--layer`, `--sheet`, `--sheet-type`, `--data` and `--format json-hash|json-array`. Output stays untrimmed unless `--trim` is given. Its JSON lists each frame's `frame`, `spriteSourceSize`, `sourceSize` and `duration`. Scripts can also call `app.command.ExportSpriteSheet{ ui=false, … }` ([command](https://github.com/aseprite/api/blob/main/api/command/ExportSpriteSheet.md)). Tilemaps are supported but not needed at runtime.

**Licence** ([EULA](https://raw.githubusercontent.com/aseprite/aseprite/main/EULA.txt), [FAQ](https://www.aseprite.org/faq/)):
- "You may not distribute copies of the SOFTWARE PRODUCT to third parties" (§2(b)).
- "You may only compile and modify the source code of the SOFTWARE PRODUCT for your own personal purpose or to propose a contribution" (§2(g)).
- The EULA says nothing about scripted, batch or CI use.
- The FAQ allows copies on all of one's own computers, but "If you are in a company, you need one license for each developer."
- Art made with it, including with a self-compiled build, may be used "in any product (personal or commercial)".
- A purchase includes updates up to v1.9.

**Cost.** $19.99 from [aseprite.org](https://www.aseprite.org/buy/) or [Steam](https://store.steampowered.com/app/431730). The [trial](https://www.aseprite.org/trial/) "CANNOT SAVE" files, so it cannot run the pipeline.

**Compiling it yourself** is free under §2(g) if the project is a personal one, and the FAQ confirms its art may be sold. The compiled build may not be shared with anyone else, including other developers' machines. Build steps and effort were not checked.

## LibreSprite

A GPL-2.0 fork made at the last GPL commit of Aseprite (1.1.8-dev, August 2016) ([README](https://github.com/LibreSprite/LibreSprite/blob/master/README.md)). It is active: v1.3 shipped on 2026-10-02 with a Windows installer and zip ([release](https://github.com/LibreSprite/LibreSprite/releases/tag/v1.3)).

- Its CLI keeps `--batch`, `--script`, `--sheet`, `--data`, `--format`, `--split-layers`, `--layer` and `--filename-format` ([options](https://github.com/LibreSprite/LibreSprite/blob/master/src/app/app_options.cpp)). It lacks `--script-param` and has no tilemaps.
- Scripts are JavaScript (QuickJS). The API has no sprite constructor or layer and frame creation; layers and frames come only from commands such as `NewLayer` and `NewFrame` ([SCRIPTING.md](https://github.com/LibreSprite/LibreSprite/blob/master/SCRIPTING.md)).
- The `NewFile` command always opens a window ([source](https://github.com/LibreSprite/LibreSprite/blob/master/src/app/commands/cmd_new_file.cpp)), so a batch script would have to start from a template file. Whether `NewLayer` and `NewFrame` work without a UI is unverified.
- It flattens layer groups when it opens `.ase` files; the flat eight-slot stacks would not be affected.

## Pixelorama

MIT, Godot-based, v1.2.3 on 2026-09-15.
- **Command line** ([CLI manual](https://pixelorama.org/user_manual/cli)): `--headless --quit -- --export --spritesheet --output … --split-layers --json`.
- **JSON:** `--json` writes the project's own serialisation, not an atlas with source rectangles.
- **Scripting:** GDScript extensions can create projects, layers and frames and set pixel images ([ExtensionsApi](https://raw.githubusercontent.com/Orama-Interactive/Pixelorama/v1.2.3/src/Autoload/ExtensionsApi.gd)). No documented way runs an extension from the command line, so scripted authoring without the GUI is unverified.
- **Other features:** indexed colour, tilemap layers since v1.1, limited `.aseprite` import, and Aseprite export since v1.2.2 ([changelog](https://github.com/Orama-Interactive/Pixelorama/blob/master/CHANGELOG.md)).

It is a capable free editor for touch-ups, not a proven generator.

## General editors and others

- **Krita** (GPL-3.0; 5.3.4 on 2026-09-15). `--export` and `--export-sequence` export a whole image, with no per-layer, sheet or JSON flags ([manual](https://docs.krita.org/en/reference_manual/linux_command_line.html)). A headless Python runner, `kritarunner`, exists only in source; whether Windows builds ship it is unverified. Krita has "no plans to support indexed color images" ([FAQ](https://docs.krita.org/en/KritaFAQ.html)).
- **GIMP 3** (GPL-3.0; 3.2.6 on 2026-09-10). Runs batch Python-Fu with `gimp-console -i --batch-interpreter=python-fu-eval -b … --quit`, and its API can add layers, set pixels and convert to indexed ([libgimp](https://developer.gimp.org/api/3.0/libgimp/)). It has no frame model, sprite-sheet or JSON export, so the export logic would be custom code.
- **GrafX2** (GPL-2.0-only; v2.9). Its Lua scripts cannot save, load or add layers, and it has no export or batch flags ([source](https://gitlab.com/GrafX2/grafX2/-/raw/master/src/factory.c)). It is GUI-only.
- **Piskel** (Apache-2.0). The last release, v0.15.0, dates from 2018, and the README says feature pull requests are unlikely to be merged. Its CLI depends on PhantomJS and writes PNG sheets only ([cli](https://github.com/piskelapp/piskel/tree/master/cli)).
- **Tiled** (GPL-2.0-or-later; v1.12.2). It is a map and tileset editor with headless `--export-map`, `--export-tileset` and `--evaluate script.js` ([scripting](https://doc.mapeditor.org/en/stable/manual/scripting/)). The fortress map format is DwarfHarness's own, so it is not needed.

## Generating the art in code, without an editor

**Go standard library** (`image`, `image/draw`, `image/png`; BSD-3-Clause).
- **Encoder:** writes no timestamp or text chunks, and for a given Go version the same image gives the same bytes ([writer.go](https://github.com/golang/go/blob/master/src/image/png/writer.go)). Its documentation warns that the exact bytes are not covered by the Go 1 compatibility promise, so reproducibility comes from the pinned toolchain the project already uses.
- **Indexed PNG:** `image.NewPaletted` and `SetColorIndex` write palette PNGs with transparency ([image](https://pkg.go.dev/image)).
- **Missing pieces:** there is no packer, sprite-sheet JSON or layered format; project code supplies them. Frames are untrimmed on fixed 16-pixel and 64-pixel grids, so a sheet can be a plain grid whose rectangles are computed, with no packer needed [INFERENCE].

**Python with Pillow** (MIT-CMU; 12.3.0) also writes no timestamp by default. Its compressed bytes can depend on the zlib build it links ([11.1.0 notes](https://pillow.readthedocs.io/en/stable/releasenotes/11.1.0.html)), so byte-identical output across installs is unverified.

**Layered sources a person can edit later.** The Aseprite format is documented ([spec](https://github.com/aseprite/aseprite/blob/main/docs/ase-file-specs.md)).
- `mattt/aseprite-py` (Apache-2.0; beta 0.1.0, 2026-09-05; Python 3.12+) writes layers, frames, cels and palettes without the application ([PyPI](https://pypi.org/project/aseprite/)).
- No maintained Go writer was found; the Go packages found only read the format.
- Such files open in Aseprite, in LibreSprite (groups flattened) and in Pixelorama (limited import).

**Packers, if a packed atlas is ever wanted:** free-tex-packer-core (MIT, npm 0.3.9), urraka/texpack (MIT, C) and pedroac/sprat-cli (MIT, v0.14.0). Each emits JSON with source rectangles and offsets.

## Recommendation and decision readiness

The art is agent-scripted, so in tickets 88 and 108 Aseprite only renders and exports what scripts draw pixel by pixel. A small Go generator using only the standard library can do that job with no licence, install or editor pin. It would also write the DwarfHarness manifest that §6 step 3 requires in any case. **That is the strongest alternative.**

Aseprite remains the better choice if people will touch up or redraw parts by hand in a full editor, or if the owner prefers its documented sheet exporter to project code. At $19.99 it is cheap. Compiling it is free only for a personal project.

The free editors do not remove that trade-off:
- LibreSprite and Pixelorama both stop short of building art from scripts without a GUI.
- Krita and GIMP would need custom export code anyway.

A middle path keeps the Go generator and adds `.aseprite` output later, so a person can open the parts in Aseprite, LibreSprite or Pixelorama. Today that output would need the beta Python writer or a small Go writer of the documented format.

None of this was proven by running anything. Before deciding, a short spike could generate one dwarf part set and a few tiles both ways and compare effort and output.

**The switch** (D103) amended B16 and changed tickets 88 (retitled), 107 and 108, fortress spec §6, §11 and §12, the system specification, the GUI roadmap, the Core design and the Core roadmap. The GUI's runtime contract does not change: it still loads PNGs and the manifest.
