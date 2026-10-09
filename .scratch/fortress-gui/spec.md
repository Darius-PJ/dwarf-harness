# Fortress GUI: colony building and agent character screens

Status: ready-for-agent

Published: 2026-10-06
Updated: 2026-10-09 — character sprites in two views with Corps emblems and colours (D108); 2026-10-08 — Go modules for sprite generation and art rounds before the GUI (D105), pixel maps and owner sprite review (D104), Go art generator replaces Aseprite (D103), ordinary officer service-loss procedure (D97–D102) and triaged ready-for-agent after ticket publication; 2026-10-07 — consolidated pre-alpha alignment (D96)

Scope: the agreed **pre-alpha design**, not a runnable GUI, exhaustive design closure or permission to bypass implementation gates. [D89](../../docs/DECISIONS.md#d89-a-fortress-overworld-and-rpg-agent-screens) establishes the fortress requirements; [B16](../../docs/DECISIONS.md#b16-native-sprite-authoring-and-durable-cosmetic-presentation) supplies engineering baselines where not amended. [D90](../../docs/DECISIONS.md#d90-fortress-gui-interview-downward-building-task-stockpiles-and-administrator-inspection), [D91](../../docs/DECISIONS.md#d91-unified-creation-drafts-and-mandatory-purpose-alignment), [D92](../../docs/DECISIONS.md#d92-shared-purpose-bounded-construction-and-perspective-bound-relationships), [D93](../../docs/DECISIONS.md#d93-human-ended-alignment-corp-creeds-and-aspirant-succession), [D94](../../docs/DECISIONS.md#d94-attenuated-succession-relationships-and-shared-colony-prehistory) and [D95](../../docs/DECISIONS.md#d95-signed-trust-semantics-with-attenuation-implementation-left-open) are incorporated below. The chronological comments retain the interview history, not competing current requirements.

[D96](../../docs/DECISIONS.md#d96-pre-alpha-fortress-alignment-and-consolidation-boundary) authorizes this consolidation while leaving further discussion possible. [D97–D102](../../docs/DECISIONS.md#d97-officer-service-loss-causes-onset-held-questions-and-restoration-evidence) add the ordinary officer service-loss procedure ([Core §13.11](../../docs/DESIGN-CORE-AND-LAUNCHERS.md#1311-ordinary-service-loss-and-officer-recovery-d97d102)). [D103](../../docs/DECISIONS.md#d103-default-fortress-art-a-go-generator-replaces-aseprite) replaces B16's Aseprite selection with a Go art generator, [D104](../../docs/DECISIONS.md#d104-default-fortress-art-pixel-maps-and-owner-review-of-generated-sprites) adds pixel maps and the owner's review of the generated sprites, and [D105](../../docs/DECISIONS.md#d105-default-fortress-art-go-modules-for-sprite-generation-and-art-rounds-before-the-gui) allows Go modules that assist sprite generation (§6). Exact attenuation implementation, producer encodings, native-terminal qualification and measured resource/accessibility/runtime gates remain open. This local triage status neither reopens settled owner requirements nor advances approval, contract freeze or implementation readiness.

## 1. Experience and invariants

Building an agent team should feel like building a fortress: excavate a Corps hall, carve its workshop siderooms and leadership offices, place its dwarves, decorate, then inspect the people and their work. The overworld is a top-down Dwarf Fortress-inspired tile grid; character and party inspection deliberately switch to an RPG presentation with larger, customizable sprites. Neither view is a game simulation of agent competence.

- Exactly one overworld dwarf represents each visible agent identity, regardless of how many contexts or turns it has. An unplaced agent appears in the staging roster, never disappears from access. Sprites in menus are portraits of the same identity, not additional agents.
- Agent identity, role, Corps membership, relationships, contexts, work, permissions, budgets and memory belong to Core. Coordinates, excavation, room assignments, visual rail paths, colors, appearance and decoration are presentation data. A presentation command cannot mutate the former set.
- Layout is never an execution prerequisite. Undug rock, an unfinished office, a missing stockpile, distance, disconnected decorative rail geometry or a closed GUI cannot pause a turn or block an authorized message.
- Core remains the single authority and durable writer. GUI reads only authorized C7 projections, not colony/private files; it holds no provider keys and performs no model calls. Cosmetic changes do not enter prompts, routing, task scheduling, Undercurrent or agent memory.
- An authenticated colony administrator may inspect all colony records, including agent-private memory and both sides of relationships; provider credentials remain Core-held. Administrator visibility never becomes the viewed agent's evidence or permission.
- Initial Purpose alignment, creation/activation intent and officer succession are Core rules shared by terminal and GUI. They can gate ordinary work; construction completion cannot. Opening a view never authorizes paid work.
- Preserve native operation, simultaneous terminal attachment, plain terminal output, role-graph restrictions, private relationship contexts and existing accessibility/operational panels. D89 supersedes the earlier operational-layout-only art restriction and D88's prior-task GUI-edit deferral, not these guarantees.

## 2. Fortress layout and visual language

### Spatial vocabulary

Build and navigate stacked top-down levels from **z-15 through z+2**, with z0 the surface and up to two above-ground levels available, including for Grand offices. Construction primarily proceeds downward. z+3 and higher are reserved and inaccessible until their informational use is decided. These are product bounds, not measured renderer capacity.

| Core subject or feature | Overworld representation | Binding and behavior |
|---|---|---|
| Colony | A navigable excavated fortress, with Corps districts and a Grand-office precinct | Pan, zoom, search and textual roster address stable IDs, never infer identity from position |
| Corps | A greater common area with a board and attached workshop siderooms | Common-area background opens its party screen; its actual roster comes from Core, not occupants standing inside |
| Worker | Dwarf seated at a workshop in a sideroom of its Corps common area | Workshop/room click opens the worker; multiple private contexts do not create multiple workers |
| Corps Orchestrator, Griot, Diplomat | One personally bound office for each leadership dwarf | Office interior opens its owner even when that dwarf is elsewhere |
| Grand Orchestrator, Grand Griot, Grand Diplomat | Individually bound Grand offices, larger and more elaborate in the default template | Size and furnishing confer no authority or statistics |
| Diplomats together | A shared diplomatic gathering hall, in addition to their individual offices | Display each diplomat once, normally gathered here; its home office retains a nameplate rather than a duplicate dwarf. Gathering never creates a new Corps or communication permission |
| Enabled classifier assistance, including jev or another configured classifier | A dog in the assisted agent's office, or workshop for a worker | Bound to an actual Core-reported helper association; not a new dwarf, autonomous agent, pet simulation or inference from the model name |
| Waiting self-assigned or delegated tasks | Nearby stockpile, distinguishing ready and blocked work | Actual C3 task identities/state; active work is shown at workshops. Neither incoming messages nor a staffing diagnosis |
| Authorized communication edge | Rails between the corresponding station/stockpile ports | Endpoint identities come from Core; routing and bends are cosmetic |
| Corps board | Shared board hub in the common area, distinct from task stockpiles | Worker-peer discussion and Griot story routes terminate at this shared hub, not fictional private worker-worker or Griot-worker rails |

Default templates place stockpiles beside stations and workshops off the common hall. Manual planning can change dimensions, shapes and positions. A workshop's logical Corps binding stays explicit when moved; adjacency/containment problems are layout TODOs, never membership edits. Moving an entire Corps district preserves its internal placements. Diplomats can be displayed at home or in the gathering hall without altering their availability; this is presentation, not a claimed live location from agent execution.

Dogs identify the configured helper and show Core-reported idle/running/unavailable state. A disabled or removed association removes its current dog; an unknown state is labeled unknown, not busy. Multiple helpers remain individually inspectable through a bounded helper list. A shared helper may have a dog marker in each assisted agent's room without implying another helper process. The requirement names jev but does not identify an installable package or authorize installing one. Paid classifiers retain ordinary Core admission/accounting; external helpers retain R5/C10 containment gates.

### Work and representative motion

Animate workshops from actual work state, dogs from helper state, and incoming message traffic on authorized rails. Stockpile size represents waiting work, including ready-but-queued and blocked tasks; it does not diagnose understaffing, competence or a stall-age threshold. Inspecting a pile reveals the underlying tasks and blocked reasons. Self-assignment and delegation remain Core actions.

A horse and wheelbarrow are each office's resting state. On a represented task handoff the resident departs; after a short cooldown a replacement fades in at home, independently of the travelling runner fading out on delivery. The office may briefly be empty during cooldown. Batch same-route handoffs and gate departures to avoid clutter: not every handoff needs a trip. Runners are neither additional agents nor validation runners. Actual assignment, stockpile counts and delivery never wait for travel, cooldown, a visible route or an open GUI. Reduced motion and hidden-view suspension retain those facts and their textual equivalents; exact animation/cooldown caps require calibration.

### Role colors and art

The overworld stays small-tile, top-down and grid-aligned even while RPG screens use larger sprites. Every dwarf has a reserved role-colored silhouette/trim and a non-color role badge. Default palette baseline: Worker green, Orchestrator amber, Griot violet, Diplomat cyan; Grand Orchestrator gold, Grand Griot magenta, Grand Diplomat blue, with a Grand crest. These are adjustable presentation defaults, not new Core classes. Worker specialization may add an emblem without inventing a role or capability.

Clothing, room paint and a tileset change cannot erase the role marker. Status has separate icon/text rather than overloading role color. High-contrast palettes, badges and the textual roster keep classes distinguishable without color perception. Create original modular graphical pixel dwarves and terrain for the default tileset, with larger layered RPG sprites; do not substitute a pre-existing character kit. Replacement packs must carry appropriate licenses. Inspiration is not permission to copy Dwarf Fortress assets.

### Rails without new messaging rights

Rails represent currently authorized direct channels, with board channels visibly terminating at the shared board hub. Distinguish an available link from actual message traffic using labeled receipt state; absence of activity does not delete a permitted channel. Selecting a rail shows its permitted endpoints/channel, not private message bodies by default. Traffic accents use actual authorized receipts, not a fabricated movement simulation.

Core's existing matrix still applies: workers contact their parent Orchestrator and own Diplomat directly; workers use the board with peers; Griots reach workers through published board stories; assigned parent/child role links are vertical; only Diplomats have lateral cross-Corps direct channels. A relationship memory alone creates no rail or DM permission. Dragging a bend never adds an edge; deleting a drawn path never revokes an edge. On a real edge retirement/revocation, stop rendering live connectivity from the new authorized projection. Overview aggregation and selected-agent highlighting reduce clutter while the rail list retains every authorized edge.

## 3. Clicks, navigation and agent CLI

Inspect and Build are explicit modes. In Inspect, pick in order: dwarf/helper or station, owned room, then common-area background. Decoration does not swallow the room hit target. In Build, clicking selects presentation objects; Enter/Open Inspector still reaches the same agent screen. Keyboard room/agent lists provide the same actions without precise pointer targeting.

| Action | Destination |
|---|---|
| Click a dwarf, its workshop, or anywhere inside its office/owned room | Agent RPG screen: sprite, name, role, Corps, status and tabbed details |
| Click a Corps common-area background | Corps RPG-party screen with all authorized members and their telemetry |
| Click diplomatic-hall background | Party-style gathering roster of its diplomats, retaining their actual Corps labels; not a new Corps |
| Click a party member sprite | That agent's RPG screen |
| Open Relationships on an agent | Its dedicated relationship roster, with counterpart sprites and directional trust/sentiment labels |
| Click counterpart B's sprite from A's relationship roster | Focused A/B pair, showing A about B and B about A as separate perspectives, with separately scoped memories |
| Select either agent from the pair | That agent's own relationship roster; retain pair/viewpoint breadcrumbs for Back |
| Open a pair's detail entry | Named directional evidence and separately identified exchanged history; never merge private memories |
| Open Memory | The selected agent's separately scoped memory browser |
| Open CLI | Attach a terminal view to the selected agent's existing execution session and an explicitly selected context |

Back navigation restores map camera, selection and list position. A named identity becoming unreadable returns an unavailable view without revealing its new name, membership or private contents. Relationship navigation, not just the first screen, rechecks authorization.

**CLI means the real plain terminal launcher, preferably in a qualified native embedded terminal.** Reuse a suitable complete off-the-shelf component with bounded integration/adaptation, within the owner's approximately **5–10% of overall-project work** allowance. If suitability or that allowance fails, use a separate **user-selected default terminal**. D92 supersedes D91's literal “no off-the-shelf” condition; neither a bespoke emulator/render/history subsystem nor an effort estimate is approved. [Terminal research](terminal-research.md) identifies a candidate and qualification blockers, including scrollback; it is historical source evidence, not selection or runtime proof.

Offer Open CLI after choosing an authorized context, or keep a still-valid current context. C7 supplies the authenticated binding to the installed `dwarf-harness` terminal entry point; bind colony/agent/session/context by IDs, use argument vectors rather than shell text assembled from names, and never put provider secrets in arguments. Embedded or external attachment reaches the same execution session: no duplicate model loop, transcript or provider continuation. Report attach/spawn failure and offer safe manual instructions using the eventual frozen syntax, not invented existing flags or a fake console. Closing the terminal is detach unless the user explicitly cancels/shuts down through existing commands. Alignment and approval must remain resumable from either launcher.

## 4. RPG character, party and memory screens

### Agent character sheet

Show the full assembled dwarf sprite in three-quarter profile, playing its idle loop while the agent is idle and its role action loop while it works, as Core's actual work status reports (D108), with Stats, Contexts/CLI, Relationships, Memory and Appearance tabs, plus applicable Purpose/alignment and succession status. Actual Core facts supply the statistics: identity/role/Corps, configured provider/model and supported capabilities, current execution state including service loss and its cause, active-context count and concurrency limit, assigned work and blocked reasons, authorized queue/receipt state, measured token/cache use, charged/reserved cost and observation freshness. Keep task status, accepted promotion and narrative accomplishment distinct.

Each statistic identifies scope, unit and observation period/revision. Unavailable is not zero; estimated cost is not settled cost; a helper's usage is attributed once using Core's accounting. Do not manufacture HP, intelligence, competence, affinity bonuses or levels from clothes, rank, room size or token counts. RPG is the layout and presentation of real statistics, not a second scoring system.

### Corps party sheet

List all authorized Corps agents, including its exactly one Orchestrator, one Griot, one Diplomat and workers, with their straight-on still sprites (D108), role, current work/status and the same bounded telemetry categories. Show an Aspirant separately from the occupied leadership slot. Paging/filtering is allowed; clipping the population or omitting an unplaced member is not. Core supplies authorized aggregates with the same period and scope as the member rows. Do not sum overlapping relationship/context views or count a shared provider attempt twice. Include the shared Purpose, Corp Creed, alignment/activation state and founding-story publication state without merging their approval gates. During an officer outage, show how many members are paused and how many, and which, reserved questions are waiting, filtered for the inspecting principal; a Grand officer's outage shows on the colony status. Retain drill-down to existing resource, grants, task, promotion, workflow and story panels; the fortress does not replace operational parity.

### Memory browser

General memory, relationship memory, task memory and board memory are separate selectors, with owner agent/context, counterpart or task, version, provenance and freshness visible. General memory is not a union of private memories. Inspect current readable entries and permitted revision history as inert text; selecting a memory does not inject it into a prompt, begin a turn or copy it elsewhere. Any later edit/share uses its own existing authorized Core command rather than a cosmetic editor.

A's memory of B and B's memory of A stay separate. Exchanged history is a third record. The authenticated administrator can inspect all colony records and both private perspectives without impersonation; other principals retain their actual grants. D83 still limits a retired agent to its already-delivered read-only history and private memories, including holes, not unseen backlog or successor contexts. Those agent boundaries also constrain model inputs; they do not hide colony records from the administrator. Recheck the inspecting principal's rights for thumbnails, snippets, search, counts, accessibility announcements, clipboard and exports as well as the body pane. Provider credentials are never exposed.

## 5. Trust, sentiment and relationship labels

Use two orthogonal axes: **trust**, the agent's confidence in a counterpart's reliability, and **sentiment**, its interpersonal attitude. The one-word label expresses that agent's perspective, not a permission, independent performance judgment, compatibility score or messaging route. The agreed nine-region vocabulary replaces B16's mixed single-axis rubric:

| Sentiment / Trust | Low | Medium | High |
|---|---|---|---|
| Negative | Hostile | Strained | Grudging |
| Neutral | Wary | Neutral | Trusted |
| Positive | Hopeful | Friendly | Bonded |

Trust is signed and normalized: **-1 distrust, 0 neutral, +1 trust**. Keep `unknown` outside the grid and distinct from known neutral zero. Show available axes and missing/uncertain evidence explicitly; do not invent a midpoint or assign a nine-region label when a required axis is unknown. Sentiment's numerical encoding, classification criteria and numeric region cutpoints remain producer/calibration work, not implications of the display table.

Classify **A about B**, bound to A's own relationship context and current evidence scope. Use only evidence A is privy to: an administrator's ability to inspect B's private memories or otherwise-hidden task failures must not inject those facts. A's failed-task knowledge may inform its perspective; independent performance telemetry remains separate. Never pool the two directions or infer relationships from rank, clothes, proximity, dog presence or model choice.

The producer uses immutable, revision-bound eligible evidence, a versioned bounded rubric/input window and auditable source/provenance. Record the agent/counterpart/context, membership scope, evidence versions, classifier/rubric version and freshness. Reject malformed values or words outside the agreed vocabulary rather than displaying arbitrary classifier prose. Exact producer schema, input ceilings and treatment of conflicting evidence require validation; the old six-word rubric and its heuristics are not the contract.

Relevant eligible evidence changes trigger **automatic, coalesced, spending-bounded** classification through Core's actual configured producer. No view open, hover, repaint, map move or CLI attach starts a call, and no speculative sweep classifies every colony pair. Paid attempts retain admission/accounting; external helpers retain qualification. Pending refresh may show the last **still-authorized** label clearly marked **outdated/updating**, never as current. With no usable label, show `unknown` and why. Revoked/inaccessible evidence cannot be retained for display or classification. A release claiming live labels must exercise a real producer, not only an `unknown` fallback.

Succession supplies explicitly scoped second-hand relationship evidence, not another agent's private context. Attenuate trust and sentiment deterministically toward zero, retaining trust more strongly; soften inherited distrust as well as trust and preserve unknown values. **The exact function, coefficients, numerical representation and cutpoints remain open pending further testing (D95).** The illustrative linear formula and example factors are not adopted defaults.

## 6. Chosen sprite and tileset pipeline

**Retain Fyne v2.8.1 as the native shell; generate the default art with a Go program kept in the repository (D103).** Fyne supplies image/custom-widget primitives, not a game engine or dress-up system. DwarfHarness implements the tile viewport, hit testing and layered sprite compositor within `gui/`, using Go image composition and Fyne pixel scaling. Do not add Godot, a browser renderer, a second event loop/runtime or a Core graphics dependency merely for this art pipeline.

The generator is a development tool built with the pinned Go toolchain. It uses the standard library and, once each has its D29 record, Go modules from outside it that assist sprite generation (D105). It writes independent transparent parts on shared canvases and anchors from pixel maps, palettes and seeds kept in the repository. A pixel map is a text grid in which each character is one pixel; agents write one for the shape of every part, variant and frame, and code rules supply only colors, palette masks, combinations of variants and small seeded texture in rock and floor tiles (D104). No image editor, image-generation model or third-party art is used, and neither the owner nor users edit pixels by hand. The baseline layer order is back accessory, body, shoes, legwear, torso clothing, hair/beard, headwear, front accessory, with explicit occlusion/compatibility metadata where a hat covers hair. Include skin/hair/clothing palette masks and a reserved overworld role overlay. Default grids are 16-pixel overworld cells and 64-pixel RPG sprites; a pack declares its actual dimensions, while logical room cells stay unchanged.

**Owner review (D104).** The generator writes two contact sheets, each at 1x and at an integer zoom. The map sheet shows the 16-pixel tiles, props, animals and overworld dwarves in every role, plus a small sample room built from the tiles; the character sheet shows every 64-pixel part, variant and frame, plus assembled example dwarves. The owner reviews both, starting with the small sample, and agents turn his comments into changes to pixel maps or rules. Generator code is approved only after he approves both sheets. Later review rounds may learn technique and style from open-source projects; none of their art is copied, and reused code follows D29 and its source licence. Review rounds can continue before the native GUI exists (D105).

**Character views and Corps markers (D108).** Every 64-pixel character part is drawn in two views: straight on, used as the still frame in rosters, and in three-quarter profile for the agent's page, where an idle loop and a role action loop play. Each Corps has a colour and an emblem, drawn as the emblem on a field of the Corps colour (for example a pennant or shield) in both views. Every view and frame is a hand-written pixel map that registers with the other layers. The 16-pixel map sprites keep the role trim, the non-colour badge and the Grand crest and are otherwise unchanged; the 64-pixel sprites carry no role marker, because the page's role action loop and the roster's role text show the role ([D109](../../docs/DECISIONS.md#d109-character-sprites-carry-no-role-marker)).

1. Generate original dwarf parts, dogs, horses/wheelbarrows, rock/floor/walls/doors/stairs, station/stockpile/rail tiles, statues and furniture. Include bounded motion frames for workshops, helpers, traffic and runners. Keep parts and their variants as separate layers; one flattened character sheet is insufficient for customization.
2. Write independently selectable parts as transparent untrimmed PNG frames/layers, or an atlas with explicit source rectangles and offsets, and tiles as PNG art. The same inputs and pinned toolchain must reproduce the same files; record and explain any difference. Prove registration and anchors with a small generated sample, approved under the owner review above, before freezing the importer.
3. Use a versioned DwarfHarness asset manifest with stable pack/part IDs, content version/hash, license/provenance, world/RPG variants, layer slot/order, anchor, frame rectangle, palette masks and compatibility constraints. Tile IDs map to semantic rock/floor/door/station/rail/decor categories. Store ID/version choices, not guessed meanings from filenames or atlas offsets. The generator writes the default pack's manifest; a replacement pack supplies its own.
4. On first appearance, randomly choose a compatible outfit locally, then persist both its seed and exact selected part/color IDs. Restart, role change, resizing, reconnect and pack upgrades do not reroll. A new cloned agent has a new appearance unless the user explicitly copies cosmetic choices; this copies no memory or agent property.
5. Appearance UI offers selectable body/hair/beard/clothing/shoes/accessories/colors, live preview, Apply/Cancel, and explicit reroll with chosen slots lockable. The RPG sprite shows the full outfit. The overworld variant remains the same dwarf identity with mandatory class color/badge readability.
6. Switching tilesets changes artwork only. Preserve logical coordinates, room bindings, completed build progress and exact appearance choices. A missing part has a clearly marked bundled visual substitute without overwriting its saved ID; restoring the pack restores the intended look. Reject an invalid pack rather than partially corrupting the layout.

Load only bounded PNG data and a strict passive manifest from bundled or explicitly imported local asset packs. Validate paths against the pack root, image dimensions/decoded-byte budgets, rectangles, IDs and compatibility; reject traversal, executable content and remote URLs. Do not use Fyne's network-capable URI loading for pack assets. No runtime editor-document parser, editor or generator executable, scripts, plug-in code or network art fetch is required. Cosmetic assets cannot specify tool commands or authority.

The generator needs no editor licence. It uses the Go standard library (BSD-3-Clause) and any modules recorded under D29 (D105), and neither it nor its modules ship. The default pack is original project work and ships with its licence and provenance records. Replacement packs remain supported: a user may import a local pack made with any tool, under the same manifest and loader rules, if its licence permits the use. D103 supersedes B16's Aseprite selection; no editor was bought or installed and no art was commissioned.

## 7. Building, creation and leadership

### Cosmetic building and completion

The per-colony **Manual digging and planning** setting defaults on as an assistant-selected baseline for the fortress-building experience. On: choose an area, designate excavation, assign its visual room use and agent/Corps binding, place station/stockpile and apply the plan. Excavation changes tiles immediately; there is no material economy, simulated labor delay or model turn. Off: deterministic templates may be prepared and applied for GUI-initiated approved topology changes. The toggle never completes/discards existing unfinished plans or bypasses creation/alignment approval.

Both settings retain Edit Layout: move/resize rooms or Corps districts, change corridors/stairs, move stations/stockpiles, bend visual rails, choose wall/floor/room colors, place/rotate/remove furniture and statues, and undo/redo cosmetic edits. Appearance grants no buffs, mood effects, priority, authority, memory or performance changes. Decorations cannot obstruct inspector access. Core-produced names/membership stay distinguishable from cosmetic room labels.

Build is an explicit reversible editing state, not drag-to-reassign. Persist incomplete blueprints, retain the last applied state on invalid/unsaved edits, and derive completion from actual saved objects. Enforce usable floors, designated room use, non-overlapping placements, required stations/nearby stockpiles and corridor/stair connectivity. A disconnected room remains on Build TODO; it never prevents its agent from working or receiving messages.

Room-size requirements and Corps/Grand officer decorative furniture type/count thresholds are enforced unless the user disables those size/decorative requirements. Offer autocomplete at the user's discretion. **Functional furniture cannot be disabled:** officer desks and Corps-appropriate worker stations, desks or labs remain mandatory for construction completion. Every room-size preset must fit its required furniture. Exact sizes, counts and footprint-qualified templates remain design/asset work, not fixed measured values. Completion rules are cosmetic standards, not competence or operational gates.

Removing a room returns its visual placement to pending/staging, never deletes or retires its agent. Preserve an emptied worker room for explicit reuse, relocation or reclamation/demolition; underground reclamation fills it with dirt. Creating, reassigning or retiring an agent is a separately labeled Core operation with its own authority. The no-vacant-officer rule below applies to Core role transitions, not physical office occupancy.

### Unified creation drafts

Provide **one RPG-style starting screen** for creating a new Corps or adding one/multiple agents to an existing Corps. Offer scratch creation and batches of one or multiple clones of a selected agent. The space-first journey is discrete and transparent: define space and common-room/office/workroom use, create/configure proposed characters, and plan their placements. For a new Corps, establish its shared Purpose, complete its Orchestrator/Griot/Diplomat triad, then add workers. Grand leadership uses the Colony Purpose. A Corps has exactly one holder of each triad role; cloning a leader cannot silently occupy an already-filled slot.

Until finalization these are reversible **creation drafts**, not agents, spend grants or live membership. Final review enumerates the exact Corps/agents, role destinations, clone inheritance and applicable grants/budgets; explicitly approve one-way-door creation. Changing reviewed creation facts invalidates that approval. Extending a cosmetic Corps boundary needs no birth-approval box. Save unfinished draft/placement progress without manufacturing topology. Core owns finalized creation and durable results; partial failures/retries must remain visible and never duplicate births. The precise batch commit/failure encoding belongs to C8/C7.

Clones receive new identities and fresh private contexts; only explicitly authorized shareable material may be inherited. Appearance copying is separately selectable and transfers neither memory nor grants. Preserve existing approval rules for agent-initiated births in addition to mandatory human alignment acceptance.

### Purpose alignment, activation and changes

Offer **Create** and **Create and activate**. Warn before finalization that even **Create-only immediately starts the first alignment interview turn**, incurs model usage under normal grants/budget admission, and leaves its questions waiting for the human. A warning is not a spend grant; expose failed/pending admission rather than hiding an unstarted interview. Waiting, reopening a screen or switching launchers must not launch unattended interview rounds.

Every new Orchestrator or Griot, including clones and Grand counterparts, first runs the pinned `grill-with-docs` skill against the shared Purpose, whether created/activated through GUI, terminal or agent-initiated birth. This is Core-enforced startup/continuation behavior, not an optional suggested prompt or GUI-only wizard. Team onboarding presents the Orchestrator's expected-work interview, then the Griot's values interview. Core/C9 own the actual skill capability and durable progress; restart or terminal/GUI reattachment resumes it rather than duplicating paid first turns.

The human must approve initial alignment; a superior agent or the interviewer cannot approve on the human's behalf. A new Corps waits before **ordinary work** until both leadership alignments are accepted; inspection, alignment and authorized message receipt remain available without authorizing ordinary turns. Adding workers to an already-aligned Corps does not restart its interviews. After alignment, **Create** leaves the team ready but inactive; **Create and activate** proceeds once existing grants, budgets and operational gates permit, without another mandatory Activate step.

Use one mutable shared Purpose per Corps, with role-specific mandates, and a Colony Purpose for Grand leadership; do not add independent per-agent Purposes. Stage proposed revisions while the approved Purpose continues governing current work. Switch after the required human decision, retaining version/approval provenance. Work changes target the Orchestrator; value changes target the Griot; corrections alone need no new interview. The human may skip, shorten or disable **re-alignment**, not initial alignment for new leaders. Exact in-flight revision/cutover handling remains producer work.

The human decides when alignment has been reached, even with questions remaining. Explain that interviews can continue endlessly and recommend stopping when questions no longer feel consequential. Exhausting a question tree, an empty frontier or a model's confidence is not the completion criterion.

### Corp Creed and founding mythology

The Orchestrator interview produces the versioned **Corp Creed**: Purpose, role mandates and resolved vocabulary/decisions, readable by the affected agents. The Griot produces a distinct reviewable mythology/origin story. Raw interviews remain separately private and administrator-inspectable. Project-document exports require separate review; onboarding does not automatically write working repositories or create a second decision store.

The Grand Griot authors a deliberately loose, possibly fictional **prehistory** as the Colony's founding story. Its interview asks about and suggests Corps to include, including anticipated Corps absent at startup, and may explain their later arrival. Corps Griots use the shared prehistory and their own Purpose to reinterpret events, heroes and characters creatively. Authorized Griot delivery/publication carries source/version provenance without granting other Corps' private memory or board access. A story's named Corps is not a real birth or approval.

Require human approval **before initial publication** of both Grand and Corps founding stories. Alignment may finish, and an otherwise-eligible team may activate, while its story remains an unpublished draft. This is not another activation gate or blanket approval requirement for every later Griot story. Clearly distinguish fictional narrative and reinterpretation from factual history, completed work and statistics.

### Prepared officer succession

Prepare and align the **Aspirant** outside the occupied officer slot while the **Incumbent** remains responsible. New Orchestrator/Griot Aspirants retain their mandatory initial skill/human-alignment requirement. The Incumbent's explicitly authorized recipient-scoped handoff covers ongoing processes, lessons learned and the relationship roster. Apply the deterministic attenuation requirement in §5, preserving second-hand provenance, direction and unknowns; do not copy exact values, raw private contexts, counterpart memory or grants.

Officer positions may not be deliberately vacated without a ready Aspirant, and there is never a second holder of a Corps leadership slot. Core must define readiness and the reviewed role/handoff cutover; a filled room or completed interview alone is not proof of successful handover. **Retire the predecessor after successful handover**, not before and not into an automatic adviser role. Preserve its retained records and private memories under the existing retirement policy.

If the Incumbent or the Aspirant is in service loss during succession, the succession pauses at its last saved step and continues after service restoration, under the ordinary procedure below. Do not invent an emergency successor promotion, waive the live handoff except through the owner's unrecoverable declaration, or treat a connection failure/GUI animation as successful transfer. A GUI detach alone does not stop Core operation.

### Officer outages

Core §13.11 (D97–D102) defines **service loss**: a recorded period in which Core cannot run an agent's required work because of a provider failure, an exhausted owner money limit, an execution failure or lost contact with its host. One cause that puts one or more agents into service loss is an **outage**. The fortress shows these facts; it never decides them.

- Agent sheets and dwarves show service loss and its cause with a non-color status, and outage begin and end notices list every affected agent without stealing focus. The owner answers one wait-or-stop prompt per cause, with per-turn overrides; switching provider and compacting stay per-turn choices.
- While an officer is lost its Corps keeps working. Further questions from its Corps (or, for a Grand officer, from the officers it leads) are **reserved questions** counted against each member's allotment, three by default; a member at its allotment pauses its ordinary work, while a paused officer still serves its own Corps. Members may claim waiting tasks and split their own tasks, and may draft new work only as task proposals, at most three waiting each by default.
- The Corps party sheet shows how many members are paused and how many, and which, reserved questions are waiting, filtered for the inspecting principal.
- The owner may stand in for the office: answer its questions and make its decisions within its authority, every act labelled "the owner, acting for" the office.
- Restoration is the agent's first completed call after its cause clears or stops applying. Core then sends a Service Restoration message in the officer's name, and paused members resume in step as the officer answers their questions.
- The owner may declare a lost officer unrecoverable; a prepared, aligned Aspirant then takes over at the normal cutover without the live handoff, and the office is never vacant. If the officer is restored before cutover, the owner chooses whether to cancel, switch to an ordinary succession or proceed.

These views make no model call and confer no authority; viewing an outage never restores an agent.

## 8. Terminal changes and persistent build TODO

### Startup and live reconciliation

After authenticated attach, obtain the current authorized topology snapshot, its revision/high-water sequence, and the persisted presentation/plan state. Compare stable IDs and revisions against the last **reviewed topology baseline**, not object names, wall-clock timestamps or the GUI event cursor. This detects additions while GUI was closed, including a new Corps and its leadership/workers. The same reconciliation runs after live terminal changes and authorized snapshot recovery on a stream gap.

Create/reconcile one durable change group before showing its choices. Prompt with the authorized change summary and **Autocomplete build**, **Build manually**, and **Later**. Even with manual digging off, externally created topology is announced rather than silently rearranging the user's fortress. First launch uses the same process to place the existing colony. Repeated attach/restart/duplicate events reuse the same group and items, not another copy of the team.

- **Autocomplete:** preview the missing construction and any proposed rearrangement of completed rooms; the user may accept such a rearrangement to make space. Preserve existing placements, blueprints, outfits, furniture and pins except for explicitly reviewed changes. Stay within z bounds. If a filled room lacks required furniture types, warn before replacing redundant furniture with the necessary pieces. Every room-size preset must accommodate its required furniture. No valid fit/space or a reached limit leaves explained pending work, never silent demolition, lost customization or falsely completed items. Apply only accepted bounded presentation transactions; create or modify no agents.
- **Build manually:** persist the choice and expose a docked TODO grouped by Corps/agent. Selecting an item focuses its region/ghost plan and offers the appropriate tool. Each applied placement saves its own progress; there is no requirement to finish the Corps before exiting.
- **Later:** persist deferral and retain a visible pending-build badge and resumable TODO. Unplaced agents still work and remain accessible from the roster/party screens. Dismissing a dialog does not mark work complete.

A reviewed baseline means the user has seen/selected a disposition for that topology revision; it is not a completion marker. Pending build items survive advancement of the baseline. On next launch, resume pending items and notify about additional unseen changes without repeating already completed work. The event stream's `through_seq` is independent of both concepts.

### Checklist contents and transitions

For a new Corps, required items cover its common area/board, Orchestrator/Griot/Diplomat offices, each worker workshop sideroom, adjacent task stockpiles, placement of each dwarf, configured helper-dog positions and presentation of authorized rail links. Shared diplomatic-hall seats and missing Grand offices are colony-scoped, not duplicated per Corps. Dogs/rails derive only from actual associations. Include floors/designation/non-overlap, corridor/stair connections, applicable size and decorative furnishing standards, and non-waivable functional furniture. Extra decoration is optional; disabled size/decorative rules cease blocking completion, but missing functional furniture never does.

Each item has a stable identity from change-group, subject ID and requirement kind, plus the source topology revision, target room/object IDs, dependencies, draft geometry and state. States are `pending`, `placed`, `blocked` (layout explanation) or `superseded` (subject/requirement removed). Completion is derived from committed presentation objects satisfying that requirement, not a checkbox that can claim a missing room exists. Clearing/replacing a required object reopens its layout item without touching Core work. A duplicate observation cannot create a duplicate room or dwarf.

Renaming updates the authorized label and preserves IDs/placement. A real role or Corps change flags affected bindings for explicit reuse/relocation review, never silently rebirths an agent, rerolls appearance or transfers an office's old identity to its successor. Supersede obsolete pending items after removal. Preserve empty worker architecture for reuse, movement or explicit reclamation/demolition, filling underground space with dirt; no automatic bulldozing. Core's officer succession rule remains independent of this room disposition. Loss of an inspecting principal's authorization removes sensitive labels, references and cached content. Mid-build changes merge into remaining work; unaffected completed placements stay intact.

### Persistence and crash semantics

Keep **Core-owned, owner-scoped durable current presentation state**, accessed through typed C7 projections/commands under Core's single-writer storage discipline. This supersedes B16's requirement to retain every cosmetic edit forever in the operational ledger. Persist recoverable current snapshots/checkpoints, unfinished plans and progress; the replacement/compaction mechanism and bounded retry receipts must be specified by the producer. No second GUI database, direct colony-file writes, private-transcript cache or GUI-owned appender. Cosmetic state remains excluded from execution/context/field projections. Births, approvals, Purpose/alignment, publications and succession retain their separate operational-history guarantees.

Persist colony/presentation IDs and revision; tileset and appearance selections; applied map/room/object/rail geometry; unfinished blueprints; manual-mode and construction-standard settings; change groups and stable TODO item identities/dispositions; the reviewed topology baseline and item completion bindings. Preserve current creation-draft and interview progress under their owning Core producers. Camera/focus may be disposable local preferences; required progress may not. Art bytes are immutable bundled/imported assets, not repeated in every save.

A save binds current authorization, expected presentation/topology revisions and a command idempotency key. Core atomically saves the affected layout/draft objects and associated TODO progress, then acknowledges. Display **Saved** only after that receipt; a disconnected/failed save remains visibly unsaved and cannot complete an item. Do not close with an unsaved edit silently: allow retry, remain open or explicit discard. On revision conflict, reload/reconcile and let the user reapply the still-relevant edit rather than overwrite newer layout/topology.

A crash before save leaves the preceding durable layout/progress; after save but before acknowledgement, recovery/retry must observe one committed result rather than apply the edit twice. Within the producer's bounded retry window, immutable command IDs return their saved result; outside it, refuse stale/expired commands and reconcile current state rather than reapplying them or requiring immortal edit history. Exact recovery/receipt encoding remains open. Restart reconstructs saved state without half-actions, duplicate placements, births or paid work. Interrupted autocomplete resumes complete bounded items from their remaining TODOs.

**Undo/redo history is bounded to a sustainable number of steps per GUI run and discarded on GUI close.** Current saved fortress, appearance, blueprints, creation progress and Build TODO survive. Undo applies a new version-checked cosmetic transition; it never rewinds operational history, resurrects an identity or reverses a finalized birth/approval/handover. Clear/prune undo references that become unauthorized or invalid after topology changes. Unsupported presentation schemas fail read-only with an explanation rather than resetting the fortress. Exact history limits and checkpoint/retry mechanisms still need sustainable resource evidence.

## 9. Producer handoffs and implementation order

These are obligations to add to the existing producers, not a seventh cross-roadmap contract or a second domain model:

| Producer | Required handoff |
|---|---|
| C3 | Authoritative task state for waiting/active work; runtime first-action, continuation and ordinary-work eligibility guards, including startup/recovery without repeated interview spending; per-agent service-loss and outage state with cause, onset and restoration (Core §13.11) |
| C6 | Directional relationship/evidence and memory boundaries; perspective-bound labels/refresh; administrator inspection distinct from agent evidence; authorized handoff and Griot publication/source lineage |
| C8 | Stable topology, shared Purpose/Creed, creation drafts/exact approvals and intent, initial/re-alignment acceptance, prepared succession/readiness/cutover and retirement; outage Corps operation, owner stand-in, unrecoverable declarations and office handover on C3's service-loss state (Core §13.11) |
| C9 backend | Real pinned `grill-with-docs` skill/workflow capability used by C8 onboarding and C3 first-action enforcement; not just a suggested command or dependency on whole-C9 GUI parity |
| Router/helper owner | Authorized helper associations and runtime status; eligible bounded relationship classification with separately admitted/accounted usage; the grouped outage prompt, one per cause. R5/C10 qualify external helpers, not opening the whole GUI |
| C7, with C1 producer-ready catalogue support | Administrator and other authorized projections; shared terminal/GUI creation, interview/approval/Purpose/publication/succession progress; atomic durable current presentation/TODO saves with bounded history/retry semantics; context-bound terminal attachment; outage, pause and stand-in projections and commands |
| GUI | Native tile/sprite renderer, representative animation, RPG navigation, draft/editor/approval views, construction standards/templates and reconciliation proposals; no independent permissions, role transitions or model execution |
| GUI acceptance | Actual native art/component/accessibility/terminal qualification, real producer-backed creation/alignment/social/lifecycle flows, cosmetic isolation and persistent restart/reconciliation scenarios |

Specify/freeze only real producer-ready additions to the existing contracts; all six remain **Not started**. Operational record families belong to the existing ledger contracts; bounded current cosmetic checkpointing must not be mistaken for an append-only history of every gesture. C7 base host work does not wait for C6/C8 or GUI; stage extensions with their real producers. GUI-A/B retain C7 prerequisites. Preserve **C9 backend capability → GUI-E operational parity → whole C9 completion**. C8 first-action alignment requires actual C9 backend skill capability, not whole-C9 completion. GUI-F additionally consumes real C3/C6/C8 alignment/social/lifecycle capabilities and corresponding C7 extensions; Core has no reverse dependency on the GUI.

Implementation order: native host/cursor shell; authorized data and durable current-presentation seam; sprite/tileset viewport and RPG navigation; building/restart reconciliation and real shared creation/alignment/succession views as their producers become available; native accessibility, isolation, packaging and declared workload evidence. This orders full scope rather than deferring requirements into an unnamed later version. Existing implementation tickets are not regenerated or approved by this consolidation.

### Open pre-alpha work

- Producer schemas and failure semantics: creation batch results, Purpose revision/cutover with in-flight work, interview/approval recovery, succession readiness and atomic handover/retirement, authorized mythology delivery and source versions.
- Service-loss, outage, stand-in and office-handover record encodings (Core §13.11), with calibration of the question allotment, task-proposal cap and restart bursts after a shared cause.
- Exact attenuation function, coefficients, numeric encoding, sentiment normalization and label cutpoints/rubric, with actual calibration/testing. No illustrative formula is selected.
- Bounded current-presentation checkpoint/save/retry/undo mechanisms and measured resource limits; room dimensions, furniture thresholds and footprint-qualified presets.
- Qualified native terminal reuse/effort comparison with the external fallback; real generated modular art and its composition, animation bounds, accessibility, packaging and workload evidence.

Further product/design discussion remains possible. None of these open items is satisfied by the pre-alpha agreement, a document check or a source-only tooling report.

## 10. Accessibility, resource and privacy gates

- Retain every operational panel in the GUI roadmap. The tile map has a synchronized native tree/list and keyboard cell/room selection; building, decoration, outfit changes and rail inspection cannot be drag-only. Text, not just a sprite/room color, identifies the selected subject and status.
- Screen transitions keep focus predictable. Announce topology/TODO changes without stealing focus. Support text scaling, integer pixel zoom, high contrast, non-color status and reduced motion. Animate the agreed work/helper/message/handoff representations with bounded update work, not unbounded idle wandering; suspend hidden optional motion without losing actual task/message facts.
- Use a viewport/chunk renderer rather than a persistent native widget for every world tile. Bound raster/cache memory, lists, relationship pages, image decoding, map extents, plan transactions and telemetry update work. Coalesce cosmetic redraws, not durable Core events or commands; never hide required agents to meet a performance number.
- Initial design calibration: at most 64 MiB decoded source art per active pack and 32 MiB cached composite sprites, bounded viewport rasters, and presentation edit payloads below the C7 frame cap (initial maximum 512 KiB per edit). Larger plans commit complete bounded items; asset/map limits fail explicitly. Exact limits/schema encodings must be measured and frozen by their producer, not inferred from these planning ceilings.

- Ordinary visual actions spend no model tokens; explicitly approved creation/alignment commands can initiate the warned, admitted model work described in §7. Native workload gate: declare an 8-GiB integrated-graphics laptop, 20 agents with three contexts each, then a 200-agent navigation/layout case; steady 50 projected updates/second and a 500/second ten-second burst are synthetic assumptions, not claims about Core arrivals. Measure cold start, idle CPU/RAM, whole-process peak memory, pan/zoom/input latency, event backlog/recovery, animation and save latency alongside active Core work. A failed workload is a failed gate, not permission to shrink it silently.
- Filter every snapshot, aggregate, rail, dog, label, memory, TODO summary, cache and announcement for the authenticated inspecting principal. Administrators can inspect all colony records, not provider secrets; other principals retain their scopes. Classifier/agent inputs are separately constrained to agent-eligible evidence even under an administrator's UI. Asset import is passive content handling; model output cannot construct executable UI widgets/assets.

## 11. Observable acceptance scenarios

All are **required, unrun implementation gates**. Run them against the real native client and actual Core/terminal once dependencies exist; document checks and a static art preview do not satisfy them.

| ID | Scenario and required observation |
|---|---|
| FG01 | Load a real colony with Corps and Grand roles: one dwarf per visible agent, required offices/Grand offices/workshop siderooms/common areas/task stockpiles; role colors/badges distinguish classes across tilesets. Build/navigate z-15 through z+2, including surface/above-ground rooms; refuse higher reserved levels and lower out-of-range edits |
| FG02 | A Diplomat has its own clickable office and appears in the shared gathering hall without a duplicate dwarf; actual configured helper associations show dogs in the correct offices/workshops, with no dog created merely by decorating |
| FG03 | Inspect agent/direct-channel rails and board hubs against C6; no worker-peer/Griot-worker DM appears. Reroute/move/remove cosmetic geometry and observe identical Core membership, relationships, permissions, work and message delivery |
| FG04 | Click dwarf, room interior, workshop and common-area background; reach the correct RPG agent/party sheets with matching sprites, all authorized members and fresh, scoped real telemetry. Keyboard equivalents reach the same subjects |
| FG05 | Qualify the actual embedded native terminal against suitability and the bounded-effort policy, or exercise the user-selected external fallback. Attach a chosen existing agent/context to its real session without a duplicate turn; detach leaves Core alive, failure is explicit, and terminal/GUI resume the same alignment progress |
| FG06 | A's relationship roster -> focused A/B pair -> either agent's roster -> Back. Keep labels, each private memory and exchanged history separately identified. Exercise administrator-wide inspection, other-principal denial, retired agent history holes and relationship memory without DM rights |
| FG07 | Exercise a real eligible classifier across the nine-region vocabulary and partial/unknown evidence. Keep hidden task failures unavailable to A's classification even when the administrator sees them. Eligible changes automatically coalesce refreshes; preserve only still-authorized old labels marked outdated/updating, reject malformed values, remove revoked evidence. Viewing spends nothing; actual helper usage is accounted once |
| FG08 | Administrator reads general/task/board and both relationship memories separately, without provider credentials or prompt injection. Other principals cannot access private material without grants; changed authorization reaches search/counts/snippets/clipboard/accessible output as well as the body |
| FG09 | Randomize once, customize and lock hair/clothing/shoes, cancel/apply/reroll, restart and switch packs. Exact saved choices survive, class markers stay readable and missing parts retain their saved IDs. Exercise the actual generated original layered parts and their composition, not a flattened stock portrait |
| FG10 | Toggle manual planning; dig/place/rearrange/decorate and undo within one GUI run. Disconnected/unfurnished rooms stay on TODO. Size/decorative requirements can be disabled, functional desks/stations cannot. Restart preserves current fortress/blueprints/TODO but discards cosmetic undo history. Geometry never changes Core properties, relationships or operational eligibility |
| FG11 | Close GUI, create a Corps in terminal, reopen and receive the change prompt even in automatic mode. Build half manually and quit; resume the same placements/TODO. Autocomplete previews remaining work and any proposed rearrangement; replacing redundant furniture to meet missing types warns before acceptance, with no unreviewed changes to completed work |
| FG12 | Repeat/reconnect/defer the same change, add another Corps mid-build, rename/reassign/retire an agent, and recover an event gap. Preserve stable IDs, no duplicate placement/items, no lost pending work or unauthorized identity disclosure |
| FG13 | Crash before atomic presentation/TODO save and after save before acknowledgement; recover one complete result. Bounded same-ID retry never applies twice; expired/stale commands reconcile rather than resurrect edits. Exercise storage failure, disconnected save, stale revision, unsupported schema and interrupted autocomplete without retaining infinite cosmetic history |
| FG14 | No valid space, overlapping/disconnected drafts or bad furniture fit leaves explained pending work and preserves the last saved fortress. Malformed/oversized/path-traversing packs and missing assets cannot execute code or overwrite saved choices. Empty worker rooms support explicit reuse/move/reclaim; underground reclamation fills dirt; no silent bulldozing or false completion |
| FG15 | Run native keyboard, screen-reader, focus, non-color, scaling/reduced-motion, packaging and declared workload gates, including animated views; exercise existing operational/C9 parity separately. Record actual platform/tool versions/results, not inferred support |
| FG16 | One creation entry handles new Corps and existing-Corps additions, scratch and clone batches. New Corps drafts establish Purpose/triad before workers; reversible drafts survive restart without births. Final approval names exact entities/inheritance/grants; changes invalidate approval, retries do not duplicate births, and cosmetic boundary extension needs no birth approval |
| FG17 | New GUI-, terminal- and agent-born Orchestrators/Griots (including clones/Grand roles) first run pinned alignment in their domains. Create-only warns and starts the admitted first turn, then waits without unattended rounds. Both initial alignments require human approval before new-Corps ordinary work. Resume without duplicate spending; honor ready/inactive versus automatic activation intent, and adding workers does not restart alignment |
| FG18 | Stage changed shared Purpose while approved Purpose still governs work. Human accepts completion despite remaining questions; work/value changes target the relevant leader, corrections need no interview, and re-alignment can be skipped/shortened/disabled without waiving initial alignment. Corp Creed is versioned/shared with affected agents; raw interviews and reviewed project exports stay separate |
| FG19 | Grand prehistory includes fictional present/future Corps without creating them; Corps Griots produce Purpose-shaped reinterpretations with authorized source lineage. Human approves each initial Grand/Corps publication; unpublished mythology does not block otherwise-eligible activation. Fiction never becomes operational achievement, and private interviews/other boards remain outside agent grants |
| FG20 | Prepare/align Aspirant outside the occupied slot and deliver only authorized ongoing-process/lesson/relationship handoff. Under the eventually qualified deterministic function, known trust/sentiment move toward zero with stronger trust retention, unknown remains unknown and neither raw contexts nor grants transfer. One holder persists until successful handover; only then predecessor retires. Officer loss invokes ordinary recovery and continuation, never emergency promotion or fabricated success |
| FG21 | Waiting self-assigned/delegated tasks appear in ready/blocked stockpiles, active work at workshops and incoming messages on rails, without staffing diagnosis. Represent batched handoffs with departure/cooldown/resident replacement/delivery fade; disabled motion, missing routes and GUI close never delay actual assignments/delivery |
| FG22 | Exhaust a real owner money limit, or a controlled provider failure, for a Corps Orchestrator. After the grace period the outage notice and the officer's service-loss state and cause appear; members' reserved questions and paused counts show on the Corps party sheet, with question text only for authorized principals, and one grouped prompt covers every paused turn. The owner answers a question as stand-in under his own label. Restoration sends the Service Restoration message and releases members in step. Declaring an officer unrecoverable passes its messages, decisions and open tasks to a prepared Aspirant without a vacancy, and restoring it before cutover asks the owner to choose |

## 12. Primary-source tooling evidence and limits

Checked 2026-10-06; art tooling rechecked 2026-10-08. These sources support tool selection, **not an implemented compositor or generator, usable art, accessibility or performance**.

- [Fyne v2.8.1 image source](https://raw.githubusercontent.com/fyne-io/fyne/v2.8.1/canvas/image.go): `canvas.Image` consumes Go images/resources and `ImageScalePixels` is nearest-neighbor. URI loading can fetch network resources, hence the passive local-pack restriction above.
- [Fyne custom widgets](https://docs.fyne.io/extend/custom-widget/): a custom widget/renderer can own the map presentation. Viewport, culling, hit testing and accessible navigation remain DwarfHarness work.
- [Fyne setup](https://docs.fyne.io/started/quick/) and [repository metadata](https://api.github.com/repos/fyne-io/fyne): native build prerequisites; BSD-3-Clause and not archived at inspection. Preserve the existing B11 toolkit selection and separate GUI build gates.
- [Go `image/png`](https://pkg.go.dev/image/png) and its [encoder source](https://github.com/golang/go/blob/master/src/image/png/writer.go): paletted and RGBA PNG output with no timestamp chunks. The exact bytes are outside the Go 1 compatibility promise, so reproducible output relies on the pinned toolchain and pinned module versions.
- The [sprite-tool research](sprite-tool-research.md) compares Aseprite, LibreSprite, Pixelorama, Krita, GIMP, GrafX2, Piskel and a Go generator from primary sources; D103 selected the generator. Registration and anchors must still be exercised before the importer freezes.

The [native-terminal source report](terminal-research.md) is likewise a candidate investigation, not component adoption. Its original evidence date and qualification limits stand; D92's reuse-first preference governs the current interaction policy.

## Comments

### 2026-10-06 — Owner-directed GUI expansion

The owner explicitly requested the fortress/RPG presentation, configurable sprites and purely cosmetic room editing, including terminal-change prompts and a build TODO surviving shutdown. This is a new GUI authorization after D88's narrower prior task. The specification retains every named behavior and records tooling/engineering choices separately in B16. Existing implementation tickets and roadmap/contract statuses are intentionally unchanged; the GUI roadmap gains explicit fortress acceptance work rather than a claim that anything was built.

### 2026-10-06 — Grilling round one

The owner's complete response and its provenance are preserved in [D90](../../docs/DECISIONS.md#d90-fortress-gui-interview-downward-building-task-stockpiles-and-administrator-inspection). Resolved vocabulary is recorded in [the glossary](../../GLOSSARY.md).

The settled direction includes animated activity; downward z-level construction with z+3 and higher reserved for other information; staged, approval-gated space-first Corps/agent creation; real statistics in RPG presentation; orthogonal trust/sentiment regions; original modular art; task stockpiles distinct from message rails; horse-drawn task-handoff animation; administrator-wide colony inspection; and bounded cosmetic undo/history discarded on GUI close while saved construction persists.

The [terminal source research](terminal-research.md) identifies an existing native widget and concrete qualification blockers; no terminal component is adopted or runtime verified. Remaining interview questions include the reserved levels, creation/finalization boundaries, relationship regions/rubric, stockpile interpretation and horse lifecycle, credential handling under administrator inspection, and cosmetic persistence. Recording these answers does not confirm shared understanding of the entire design, rewrite its unresolved mechanisms or advance implementation/contract gates.

### 2026-10-06 — Grilling round two

[D91](../../docs/DECISIONS.md#d91-unified-creation-drafts-and-mandatory-purpose-alignment) preserves the owner's complete response and the adopted choices. Buildings may extend through z+2; the level range is bounded, and reserved higher layers remain inaccessible. One RPG entry handles new Corps or additions to existing Corps, scratch creation or batch clones. A new Corps draft establishes Purpose and completes its leadership triad before adding workers; nothing is created until final batch approval.

Both Create and Create-and-activate are required. Every new Orchestrator or Griot first runs `grill-with-docs` against mutable Purpose, through either launcher; the activation presentation conducts expected-work alignment with the Orchestrator, then values alignment with the Griot. This needs a real Core startup/workflow contract, not a GUI-only prompt sequence. Existing skill discovery, birth approvals and durable records do not yet specify the lifecycle, who approves alignment, when its paid calls begin, or its document outputs.

The nine relationship labels are accepted. Horse-and-wheelbarrow resting/departure/cooldown behavior is settled; handoff animation is batched and rate-gated representation, not a trip for every handoff. Provider credentials remain Core-held, and the original default tileset is graphical pixel art. The GUI makes no staffing diagnoses.

Two owner phrases remain explicit clarification points: the task set meant by his phrase about representing only stalled work, and the conditional native-terminal rule concerning a marginal 5–10% of overall-project work and the absence of an off-the-shelf option. The required fallback is a separate user-selected default terminal. No terminal route or percentage estimate is silently selected. The remaining Purpose/leadership boundaries and numerical depth limit are also interview questions; full design reconciliation and implementation gates remain unchanged.

### 2026-10-06 — Grilling round three

[D92](../../docs/DECISIONS.md#d92-shared-purpose-bounded-construction-and-perspective-bound-relationships) records the complete owner response. Purpose is shared per Corps with role-specific mandates; Grand leadership uses a Colony Purpose. Each Corps has exactly one Orchestrator, Griot and Diplomat. Alignment requires human approval even for agent-initiated births. Create-only immediately runs the first interview turn and leaves its questions waiting, with an advance warning about model usage and the existing budget/capability checks preserved.

Stockpiles include ready-but-queued tasks waiting to be worked, distinguish blocked work, and leave active work at workshops; no staffing diagnosis is introduced. The buildable fortress range is z-15 through z+2. Corridor/stair connectivity and comparable basic construction standards determine Build TODO completion, never operational eligibility.

Relationship labels represent each agent's own perspective, including only failed-task evidence it is privy to. Eligible evidence changes trigger coalesced, spending-bounded classification, not screen inspection. A counterpart opens the focused pair; either agent in that view links to its own relationship roster.

Suitable off-the-shelf native terminal embedding is preferred within the clarified effort allowance; the user-selected external terminal remains the fallback. This resolves D91's literal “no off-the-shelf” ambiguity without selecting a component or claiming runtime qualification. Remaining branches include Purpose changes, alignment completion/work eligibility and artifacts, leadership replacement, incomplete construction/reconciliation, and partial relationship evidence. Full-spec reconciliation still waits for confirmed shared understanding; no gate or readiness status changes.

### 2026-10-06 — Grilling round four

[D93](../../docs/DECISIONS.md#d93-human-ended-alignment-corp-creeds-and-aspirant-succession) preserves the complete owner response. A new Corps waits for both leadership alignments to be human-approved before ordinary work; completing alignment then honors Create versus Create-and-activate intent. Live Purpose changes are staged. The human decides when alignment is reached and should be encouraged to stop once questions no longer feel consequential. Meaningful work/value changes target the corresponding role's re-alignment, which the human may skip, shorten or disable; new-leader initial alignment remains required.

The Orchestrator interview produces the versioned **Corp Creed**. The Griot additionally produces a reviewable mythology or origin story. Approved shared artifacts remain separate from private raw interviews and separately reviewed project exports. Prepare and align an **Aspirant** before succession; the **Incumbent** provides a handoff covering ongoing processes, lessons and a relationship roster whose values are discounted rather than copied. Officer positions cannot be vacated without a ready Aspirant. Exact handoff authorization, readiness/discount semantics and initial mythology publication are not silently invented.

Relationship refresh may retain a still-authorized historical label marked outdated/updating. Construction enforces room sizes and Corps/Grand officer furnishing thresholds, with user-requested autocomplete and disableable size/decorative requirements. Functional officer desks and Corps-appropriate worker stations/desks/labs cannot be waived for Build TODO completion; they never gate real agent work.

Autocomplete may preview rearrangement of completed rooms. Room presets must fit required furniture; when a filled room has the wrong types, TODO autocomplete warns before replacing redundant furnishings with required pieces. Empty worker rooms remain available for reuse, relocation or explicit reclamation/demolition, including filling underground rooms with dirt. Full design reconciliation awaits human confirmation, not exhaustive questioning; producer contracts and implementation gates remain unchanged.

### 2026-10-06 — Grilling round five

[D94](../../docs/DECISIONS.md#d94-attenuated-succession-relationships-and-shared-colony-prehistory) preserves the owner's complete response. The succession handoff applies a deterministic normalized function moving sentiment and trust toward zero, retaining trust more strongly so the Aspirant incorporates second-hand evidence without copying the predecessor's exact opinions. Preserve provenance and unknown values. The predecessor retires after successful handover; it does not remain an adviser or automatically take another role.

If the Incumbent loses connection during succession, use the ordinary officer connection-loss/recovery procedure and continue after service is restored. That broader operational policy is a separate documentation dependency; this interview does not create an emergency promotion or missing-handoff waiver.

The Grand Griot's interview develops a potentially fictional **prehistory** as the Colony's founding story, asking about and suggesting present and future Corps and possible reasons for later arrivals. Corps Griots use this mythology and their Purpose to write their own founding stories, creatively reinterpreting selected heroes, characters and events. Grand prehistory must remain loose enough for those different interpretations. Narrative references are not real agent/Corps creation or operational evidence.

Human approval precedes initial publication of the Grand source and Corps founding stories. Alignment can finish while a story remains an unpublished draft; this is not another activation gate or a new approval requirement for every later Griot story. Private interviews and explicit publication audiences remain separate.

The owner has not selected the numerical domain, meaning of zero, attenuation curve/coefficient values or classifier cutpoints. Distinguish those producer/design choices from the fixed nine-region vocabulary and the required deterministic, trust-preserving attenuation. Full feature/system/producer/roadmap reconciliation remains deferred until shared-understanding confirmation; no runtime or implementation gate has advanced.

### 2026-10-07 — Grilling round six

[D95](../../docs/DECISIONS.md#d95-signed-trust-semantics-with-attenuation-implementation-left-open) records the owner's choice of option 47A, its exact implementation to be settled by further testing. Trust runs from -1 (distrust), through 0 (neutral), to +1 (trust); attenuation toward zero softens both inherited extremes. Unknown remains distinct from a known neutral value.

The exact attenuation function, coefficients, numerical representation and classification cutpoints remain open pending testing. Neither the proposed linear formula nor its illustrative coefficients are adopted defaults. The deterministic, normalized behavior and stronger retention of trust required by D94 remain fixed requirements.

No further owner-semantic question is identified in this interview. Full feature/system/producer/roadmap reconciliation awaits the owner's confirmation of shared understanding; no implementation, contract or runtime gate advances.

### 2026-10-07 — Pre-alpha alignment consolidated

The owner authorized consolidation at a useful pre-alpha stopping point, explicitly leaving more available for discussion ([D96](../../docs/DECISIONS.md#d96-pre-alpha-fortress-alignment-and-consolidation-boundary)). The current body now incorporates D89-D95 instead of deferring to an interview warning. Core/terminal, system, roadmap and contract handoffs are reconciled with that agreement. Earlier comments remain chronological evidence, not current blockers.

Exact attenuation implementation remains open pending testing. Ordinary officer recovery stays separately owned; terminal/art qualification, producer freezes and measured runtime gates remain unrun. No implementation, D52 approval, readiness advancement or contract freeze is claimed; existing implementation tickets are unchanged.

### 2026-10-08 — Triaged ready-for-agent

The owner approved moving this specification from `needs-triage` to `ready-for-agent` after its tickets were published as 87–129 in the shared DwarfHarness tracker. As with the system specification, the status means this spec is available for dependency-bounded agent work. It grants no D52 approval, contract freeze, roadmap readiness or implementation approval, and the open pre-alpha work in §9 stays open.

### 2026-10-08 — Officer service-loss procedure (D97–D102)

Ticket 87's owner interview (Q48–Q80) specified the ordinary officer service-loss procedure recorded in D97–D102 and Core §13.11. §7 now defers succession to it and describes the outage surfaces, §9's handoffs and open work are updated, and FG22 adds an outage acceptance scenario. No implementation, contract freeze or readiness advancement is claimed.

### 2026-10-08 — Go art generator (D103)

Before buying an Aseprite licence, the owner compared alternatives and approved a Go generator kept in the repository ([D103](../../docs/DECISIONS.md#d103-default-fortress-art-a-go-generator-replaces-aseprite)). Neither he nor users will edit pixels by hand, and importable replacement packs stay. §6 now describes the generator, FG09 exercises its generated parts, and §9's open work and §12's evidence follow. The runtime pack contract is unchanged, and no implementation, contract freeze or readiness advancement is claimed.

### 2026-10-08 — Pixel maps and owner sprite review (D104)

The owner adopted pixel maps for sprite shapes and made his review of both the map and the character sprites the condition for approving generator code, starting with ticket 88's sample ([D104](../../docs/DECISIONS.md#d104-default-fortress-art-pixel-maps-and-owner-review-of-generated-sprites)). Later review rounds may learn from open-source projects without copying their art. §6 states both controls; the runtime pack contract is unchanged, and no implementation, contract freeze or readiness advancement is claimed.

### 2026-10-08 — Go modules and art rounds before the GUI (D105)

The owner allowed Go modules from outside the standard library when they assist sprite generation, each with its D29 record, and confirmed D104's other readings ([D105](../../docs/DECISIONS.md#d105-default-fortress-art-go-modules-for-sprite-generation-and-art-rounds-before-the-gui)). Ticket 137 lets review rounds improve the map and character sprites before the native GUI exists. §6 and §12 follow; pixel maps, the review gate and the runtime pack contract are unchanged, and no implementation, contract freeze or readiness advancement is claimed.

### 2026-10-09 — Character views and Corps markers (D108)

Reviewing ticket 88's first contact sheets, the owner approved the map sheet and asked for animation-ready character sprites ([D108](../../docs/DECISIONS.md#d108-character-sprites-a-still-front-view-an-animated-three-quarter-view-and-corps-emblems-and-colours)): a straight-on still for rosters, a three-quarter view on the agent's page with an idle loop and a role action loop, and a Corps emblem on a field of the Corps colour. §4's agent and party sheets and §6 follow. The map sprites, the separation of presentation from Core facts and the runtime pack contract are unchanged.
