# DwarfHarness

A persistent colony of agents, inspected and operated through terminal and fortress views. This glossary records resolved domain language; it does not approve implementation or freeze contracts.

## Language

### Fortress and work

**Dwarf**:
The visual representation of one agent. Portraits of that dwarf in character, party and relationship screens represent the same identity, not additional agents.

**Z-level**:
A horizontal level of the fortress view. Its buildable range runs from z-15 through z+2; z+3 and higher are reserved and inaccessible until their informational use is decided.

**Stockpile**:
A visual representation of self-assigned and delegated tasks waiting to be worked, distinguishing ready and blocked work from active work at workshops. It is neither a message queue nor an automatic staffing diagnosis.
_Avoid_: Message queue, inbox, outbox

**Rail**:
A visual communication connection along which incoming messages are represented. Its drawn route does not create communication rights or control delivery.

**Task runner (fortress)**:
An ephemeral horse-drawn visual courier representing a task handoff. It is not an additional agent or the validation runner that checks work.
_Avoid_: Validation runner

**Resident horse**:
The horse and wheelbarrow present at an office in its resting state. After departure, a replacement appears following a brief cooldown, independently of the travelling runner disappearing on delivery.

**Build TODO**:
The checklist of unfinished fortress construction and placement, including unmet connections, applicable size/furnishing standards and mandatory functional furniture, preserved across GUI shutdowns. It is distinct from the agents' work and never gates their operations.

**Cosmetic undo history**:
A bounded sequence of reversible presentation edits available during a GUI run and discarded when the GUI closes. It is distinct from the saved fortress, unfinished construction and operational history.

**Functional furniture**:
The mandatory work furniture of a room: a desk for an officer, or a Corps-appropriate station, desk or lab for a worker. Its requirement cannot be disabled for construction completion, but never gates the agent's operations.

### Purpose and creation

**Purpose**:
A mutable shared statement of intended work and guiding values for a Corps, or for the Colony at Grand-leadership level, with role-specific mandates beneath it. It is distinct from an independently editable agent objective or the cosmetic designation of a room.

**Purpose alignment**:
The required initial `grill-with-docs` discussion with a new Orchestrator or Griot about expected work or values, respectively, through GUI or terminal. The human decides when alignment has been reached and approves it; completion does not require exhausting possible questions.

**Re-alignment**:
A follow-on discussion with existing leadership after meaningful changes to its Purpose domain. The human may skip, shorten or disable it without thereby waiving initial alignment for new leaders.

**Corp Creed**:
The versioned shared agreement produced by the Orchestrator's Purpose interview, containing Purpose, role mandates and resolved vocabulary/decisions for the affected agents. It is distinct from the raw interview and the Griot's mythology.
_Avoid_: Corps/Colony agreement

**Mythology**:
A reviewable cultural narrative distinct from the Corp Creed and operational history. It includes the Grand Griot's shared prehistory and Corps Griots' Purpose-shaped interpretations.

**Prehistory**:
The Grand Griot's potentially fictional founding narrative for the Colony, including present or anticipated Corps and possible reasons for later arrivals. Its heroes, characters and events are deliberately loose enough to support differing Corps interpretations.

**Founding story**:
A narrative of the Colony's or a Corps' origins. The Grand Griot provides the Colony's shared prehistory; each Corps Griot creates a Purpose-shaped retelling with creative license over its events and characters.

**Leadership triad**:
Exactly one Orchestrator, one Griot and one Diplomat forming a Corps' leadership.

**Creation draft**:
An editable proposal for a new Corps or one or more new agents, including scratch creations or clones, before final creation approval. Completing a draft's rooms, character sheets or proposed roster does not itself create agents.

### Officers and service

**Officer**:
The holder of a leadership role: a Corps Orchestrator, Griot or Diplomat, or one of their Grand counterparts.

**Service loss**:
A recorded period in which Core cannot run an agent's required work because of a provider failure, an exhausted owner-set money limit, an execution failure or lost contact with the agent's host. Idling, waiting, an interrupted turn, a detached view and a stopped colony are not service loss.
_Avoid_: Connection loss

**Outage**:
A single cause, such as an exhausted provider account, that puts one or more agents into service loss at the same time.

**Service restoration**:
The end of an agent's service loss, established by its first successfully completed call after the cause clears or stops applying to the agent.

**Service Restoration message**:
Core's notice, sent in a restored officer's name and marked as from Core, that the officer has returned to service.

**Reserved question**:
A question a member sends to an officer in service loss, held in that officer's queue and counted against the member's question allotment.
_Avoid_: Held question

**Question allotment**:
The number of reserved questions at which a member pauses its own ordinary work.

**Unrecoverable officer**:
An officer in service loss that the owner has declared will not return, so that an Aspirant can succeed it without the officer's live handoff.

**Owner stand-in**:
The owner answering questions and making decisions for an officer in service loss, within that office's authority and labelled as the owner's own acts. The owner never becomes the office's holder.
_Avoid_: Acting officer, impersonation

**Task proposal**:
A drafted task that cannot start until an officer or the owner accepts it. Members may draft them while their officer is in service loss.

### Succession

**Incumbent**:
The current holder of an officer position being prepared for succession.

**Aspirant**:
An agent being prepared and aligned to succeed an Incumbent. It does not occupy that officer position before the handover.

**Succession handoff**:
Communication from an Incumbent to its Aspirant covering ongoing processes, lessons learned and the relationship roster. Relationship values undergo deterministic normalized attenuation toward zero, retaining trust more strongly than sentiment; the predecessor retires after successful handover, without transferring its private contexts or authority through the communication.

### Relationships and inspection

**Trust**:
One agent's confidence in a counterpart's reliability, considered independently of its interpersonal sentiment toward that counterpart. Its signed normalized scale runs from -1 (distrust) through 0 (neutral) to +1 (trust); unknown remains distinct from neutral.

**Sentiment**:
One agent's positive or negative interpersonal attitude toward a counterpart, considered independently of trust.

**Relationship label**:
A one-word description of an agent's perspective on a counterpart, based only on evidence available to that agent and located in the nine-region trust-and-sentiment space. It is neither an access grant nor an independent performance score.

**Agent-private memory**:
Memory whose privacy boundaries restrict access by other agents, not inspection by an authenticated colony administrator. Inspecting it does not confer its contents or permissions on the agent being viewed.
