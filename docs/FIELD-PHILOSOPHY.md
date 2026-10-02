# Field Philosophy

**Status:** Research-backed pre-design philosophy with owner clarifications from 2026-09-29 and 2026-09-30; not an approved Field Design or an implementation claim.

**Owner's direction:** A universe-wide, extensible environment, independent of colony boundaries, that communicates causality and consequences through evolving fields. Agents indirectly affect one another through concrete actions, without choosing recipients. Existing direct messages, Corps boards and Griot stories remain separate. Consequential activity should have wider effects than inconsequential activity. Agents remain individuals who develop through experience; their unique memories and context may reveal connections the system cannot predict. They cannot simply declare their own importance.

**Reading convention:** The owner's direction above and passages explicitly labelled **Owner decision** or **Owner decisions** below record requirements clarified in discussion. The explicitly provisional access decision remains open to revision. Other charter, field-candidate and mathematical recommendations are **[INFERENCE] — proposals for satisfying those requirements**, not additional owner decisions. Cited research establishes the named concepts and results, not that they will work securely for DwarfHarness. [The core design](DESIGN-CORE-AND-LAUNCHERS.md) remains authoritative for current organization, permissions and private relationship memory; [the routing design](DESIGN-ROUTING-LAYER.md) remains authoritative within those boundaries. The new behavioral requirements do not approve runtime mechanisms or expand communication permissions.

> **Recommendation: shared laws, situated perception, influence earned through consequences.**
>
> Build the philosophy around an environment that remembers what actions have changed, not a system that broadcasts what agents say matters. A few fields over dependencies and resources are a possible starting point, not a fixed catalogue or an exclusive map of what agents may encounter.

## 1. What kind of world is this?

A message says, “I want you to know something.” An environmental consequence says, “Something is different here now.”

An agent consumes a scarce resource; conditions become more congested. It repairs a shared dependency; work that was difficult becomes possible. It discovers a reproducible defect; activities exposed to that defect become riskier. These changes can matter to agents the original actor neither knows nor can contact.

This is not communication-free. It is communication **without an addressed social exchange**. The environment mediates influence; an agent need not compose a message, select an audience, persuade a superior or maintain a conversation for the consequence to exist.

**Owner decision — an extensible dynamical medium:** the field must be flexible enough to grow with the project; the full set of fields is not yet known. Decay and wave-propagation speeds should be adjustable, leaving room for emergent behavior in the system itself. A classifier that recognizes known situations and distributes relevant observations may meet an immediate need, but is not a substitute for this vision. These requirements do not select a field equation, topology or tuning mechanism.

The closest established concept is **stigmergy**: an action leaves a trace in a medium, and that trace stimulates subsequent action. [Heylighen's treatment](https://pespmc1.vub.ac.be/Papers/Stigmergy-varieties.pdf) is especially useful because it distinguishes effects of work itself from markers deposited for signaling. DwarfHarness should lean toward the former: a partly built bridge changes what others can do; a sign saying “my bridge is important” does not make it stronger.

There is a useful quantitative precedent in [Dorigo, Maniezzo and Colorni's Ant System](https://jmvidal.cse.sc.edu/library/dorigo96a.pdf): reinforcement depends on a measured completed tour, and traces evaporate. But that algorithm has a known optimization objective and cooperative artificial ants. It does not solve open-ended importance or adversarial reporting. We should borrow the separation between outcome and reinforcement, not assume agents can safely write their own pheromone values.

### Universal does not mean uniformly visible

The universe is the entire participating DwarfHarness environment, not one colony's bulletin board. A colony is an organization **inside** this environment, not the owner of its physical laws.

Universal scope means the same law applies wherever its conditions apply. It does not mean:

- every agent receives every observation;
- every colony sees another colony's private work;
- all resources belong to a common account;
- influence is instantaneous or equally strong everywhere;
- one giant file must be readable and writable by everyone.

A shared service can couple distant colonies strongly; unrelated work in the same Corps can be weakly coupled. Meaningful exposure and dependency are useful sources of locality, not organizational rank. They must not become an exclusive relevance filter that prevents agents from discovering previously unknown connections (§4).

Whether one universe spans processes on one computer, several trusted computers, or independently administered installations remains a Field Design question. Common rules do not, by themselves, solve networking, authentication or agreement between operators.

## 2. What to borrow from physics — and what not to

The productive analogy is **lawful mediation**, not quantum machinery.

In quantum field theory, fields are fundamental and particles arise as their excitations; [Tong's introductory lectures](https://davidtong.org/pdfs/teaching/quantum-field-theory/qft1.pdf) explain this directly. However, “particles exchanging messages” is not the right engineering interpretation of quantum interactions.

Gravity is not the absence of a field. In general relativity, the spacetime metric is dynamical; matter and energy influence geometry, and geometry influences motion. [Carroll's lectures](https://preposterousuniverse.com/grnotes/) develop that relationship. Gravitational disturbances propagate causally, not instantaneously; the joint gravitational-wave and gamma-ray observations of [GW170817](https://arxiv.org/abs/1710.05834) strongly constrain their propagation-speed difference. A graviton is hypothetical, not an experimentally established messenger needed to explain this philosophy.

The useful lessons are simpler:

| Physical idea | Useful interpretation here | Limit of the analogy |
|---|---|---|
| Local interaction | Effects pass through meaningful couplings | Software must define and justify those couplings |
| Universal laws | Equivalent conditions receive equivalent treatment across colonies | Operators still choose and can change the software |
| Different couplings | The same condition matters differently to different capabilities | Agents must not invent privileges or immunity by declaring a new “type” |
| Conservation | A finite resource cannot be spent twice | Attention, truth and usefulness are not conserved substances |
| Dissipation | Transient disturbances can fade | An unresolved hazard must not disappear merely because time passed |
| Persistent geometry | Past work changes future possibilities | No need to solve Einstein's equations to represent dependencies |

**Do not adopt inverse-square falloff merely because gravity has it.** Its physical setting matters. A dependency network is not automatically three-dimensional space.

Most importantly, nature's laws do not determine what is morally valuable. A destructive event can have enormous consequences. “Heavy” must mean consequential in a specified dimension, not good, authoritative or deserving of reward.

## 3. A proposed constitutional layer

Five things must stay distinct:

1. **Laws:** which state changes are admissible and how consequences evolve.
2. **World facts:** observed resource use, artifact states, dependencies and outcomes, with their provenance and limitations.
3. **Fields:** derived conditions over that world — pressure, obstruction, opportunity and similar quantities.
4. **Perception:** the authorized, situated observations available to an agent.
5. **Choice:** what that individual does in response.

Confusing any two produces trouble. A claim is not a fact. A fact is not its interpretation. A strong field is not a permission. An observation is not an instruction.

### Proposed principles

**Law is enforced outside the model.** A prompt asking agents to obey physics is a social rule, not an environmental constraint. A resource boundary must hold even if the agent ignores its description. Field derivation must not depend on the agent voluntarily reporting an inconvenient event.

**Agents act on the world, not on their own field strength.** They may build, test, consume, repair and explore through permitted actions. The environment derives consequences from those actions. Neither a worker nor a Grand role gets an arbitrary “increase my influence” operation.

**Owner decision — concrete actions only:** hypotheses, intentions and aspirational thoughts do not seed field perturbations. Clarifying or announcing an insight does not strengthen the field representing its subject. The concrete actions taken while pursuing it can have environmental consequences; for example, actually using a resource is different from imagining its use. Existing fields can continue to propagate and decay under their laws without a fresh action. This source rule does not mean every action must affect every field, or that an observed action proves the agent's interpretation of it.

**The same law can produce different effects without favoritism.** Two agents exposed to the same resource face the same accounting rules. Different skills, attachments and circumstances can create different observations and responses. Institutional title alone should not multiply physical influence.

**No field grants authority.** High opportunity does not authorize a tool call. High risk does not unlock another agent's memory. Environmental relevance does not create a direct-message route or override a budget approval.

**The environment is not a collective mind.** Agents retain private interpretation, relationship memory, goals and learning. They need not converge on one opinion or maximize one universal utility. A scout may investigate an obstruction, a specialist repair it, and an orchestrator choose a different plan.

**Evidence has limits.** “Observed by the host” means an observation came through an accepted measurement path. It does not prove an artifact is correct, a test is adequate or a real-world assertion is true. Human judgment and ordinary discussion remain necessary where evidence is insufficient.

**History matters without becoming an eternal noise floor.** Transient disturbances may fade. A working artifact, an unresolved defect or an exhausted account persists according to its actual state. The field is a view of consequences, not a replacement for durable facts.

**Law changes are constitutional changes.** Authorized operators may revise the rules, but agents should not rewrite them through persuasion or field activity. Changes need explicit versions and an intelligible transition, rather than silently changing the meaning of yesterday's evidence. Whether multiple operators share that authority is unresolved.

### Recoverable causal history

**Owner decision:** the environment must retain causal history or permit its reconstruction through reversible mathematics. Recoverability is the requirement; storage, replay and mathematical reversibility are alternative mechanisms to investigate, not selected implementations.

**[INFERENCE] — reconstruction limit:** a reversible update rule alone cannot recover originating actions or external inputs that were never retained and cannot otherwise be inferred. Any proposed mechanism must demonstrate recovery of the causal history, not merely inversion of one field-state update. The illustrative diffusion model in §7 does not establish that guarantee.

Environmental retention and agent access are separate decisions. Retaining history does not require feeding it into every agent's context. The current preference is for Griots to encode learning from agents who “changed the world”; ordinary agents should remain focused on their tasks and memories. The provisional investigation-access rule is in §9.

### Core-first build order and evidence substrate

**Owner decisions (2026-09-30):** build the harness and colony core first. The field layer comes later as a package inside the harness that can be tested as a standalone engine, and it must undergo substantial testing before it is allowed to affect agents. The test scope and acceptance criteria remain for Field Design.

The core will record typed action deltas in an append-only action ledger; larger bodies will live in a content-addressed store under an operator retention policy. This selects the logical action record and body-storage approach, not whether the action ledger shares a physical log or database with session/view events, nor how long bodies are retained. The causal-history requirement above therefore remains: these records are a candidate evidence substrate, not yet a demonstrated guarantee of complete recovery.

**Owner decision — private work artifacts:** each bound work context owns a private scratch per workspace root, intended to use a Git worktree, and canonical promotion is fast-forward only. This defines the workspace model, not field-source admissibility or disclosure. A promoted artifact becomes part of canonical workspace state; promotion alone does not make it a field source or authorize disclosure. Whether concrete actions within private scratch may affect outward fields remains unresolved in §9.

**Security limit:** linked Git worktrees share repository metadata and are not OS sandboxes. They do not by themselves prevent a same-user shell from changing shared refs or reading other private files. The core design records this as an unresolved implementation boundary; the field design must not treat a worktree or a Git hash as proof against such access.

## 4. What should carry the fields?

### Dependency graphs are useful, but not exclusive perception maps

[Signal processing on graphs](https://arxiv.org/abs/1211.0053) supplies a practical mathematical language. A value lives at each node; weighted edges say which nodes interact. The graph supplies the geometry.

For DwarfHarness, useful locations could be artifacts, work domains and resources. Edges could represent actual dependencies, shared capacity or exposure to a common failure. Agents are situated through their current work and authorized access, not simply through a permanent coordinate assigned to their identity.

**Owner decision — unanticipated relevance must remain discoverable:** an agent may be the only one whose context, memory or task connects a field impression to a useful action. The environment may not know that connection is important. Consequently, known dependencies cannot be the exclusive gate for encountering field representations. This is a requirement for broader discovery, not permission to inspect everything or a decision to broadcast every condition to every agent.

**[INFERENCE] — implication for the graph proposal:** distinguish an evidenced causal coupling from an agent's opportunity to perceive a condition and discover its relevance. A graph remains a candidate substrate, but the broader encounter geometry and sensing rules are unresolved; they cannot be declared complete merely by mapping known dependencies.

This **consequence graph is not the communication-permission graph**. A library used by two colonies can connect their work environmentally without authorizing worker-to-worker chat. Conversely, permission to speak does not prove that two tasks share a resource or dependency.

An arbitrary two-dimensional grid would make proximity a drawing decision. A language-model embedding could mistake semantic similarity for causal dependence. Either may be useful for visualization or discovery, but neither should silently determine which agents can affect one another.

The graph is itself a security-sensitive model: invented dependencies and inflated edge weights can manufacture reach. Calling it a graph does not make it objective. Relationships need evidence and rules for admission, removal and uncertainty.

### Is a “tensor state file” the right object?

It could be a useful storage representation. In machine-learning usage, a tensor often means a multidimensional array. In geometry and physics, a tensor has defined transformation behavior under changes of coordinates or basis. Merely storing `agent × field × time` values does not supply those semantics.

More importantly, a container does not answer who may change a value, what it means, how it propagates, or how it is checked.

| Mathematical option | What it contributes | Recommendation |
|---|---|---|
| Scalar fields on a weighted graph | One interpretable quantity per location; natural local differences and spreading | Best starting point |
| A collection of scalar fields | Several dimensions without pretending congestion and reliability have the same units | Prefer to one “importance” number |
| Edge flows / graph vector calculus | Directional transfer and circulation | Add only for a real directional quantity; [Hodge Laplacians](https://arxiv.org/abs/1507.05379) provide the mathematics |
| Potential fields | A landscape that biases choices toward opportunities or away from costs | Useful guidance, not a compulsory policy or global-optimization guarantee |
| Tensor-valued fields | Direction-dependent or coupled responses with meaningful mathematical structure | Defer until an actual phenomenon needs them |
| Computational field calculus | A formal way to compose distributed field computations | Useful future design vocabulary, not an automatic security property |

[Beal and Viroli's aggregate-programming tutorial](https://jakebeal.github.io/Publications/QUANTICOL16-AggregateProgramming.pdf) is a good bridge from the philosophy to executable local rules. It separates aggregate field behavior from device-level implementation. Its resilience results have assumptions; recovery from transient faults is not immunity to malicious inputs. Adopting the ideas does not require adopting its language or runtime.

**Recommendation:** choose semantics and invariants first. Treat any state file, database or tensor array as a representation of that model, not the constitution itself. Physical transport and storage belong in Field Design.

## 5. Candidate fields

Start with phenomena that have a defensible connection to concrete actions and their consequences. Names below are conceptual, not selected APIs. **Owner decision:** the field catalogue must remain extensible; these candidates are not a complete or closed set.

| Field | What it represents | What can substantiate it | What must not substitute for evidence |
|---|---|---|---|
| **Resource pressure** | Scarcity, contention and delay around a resource | Actual available capacity, reservations, measured queues and service behavior | Claimed urgency; spending more to appear more important |
| **Dependency strain** | Unresolved obstruction or exposure around work that other work depends on | Reproducible failures, unmet prerequisites, validated downstream dependencies | Repeated complaints; self-assigned criticality; one hundred copies of one failure |
| **Affordance** | New or improved possibilities for action | An available capability, validated artifact, demonstrated reduction of a relevant obstacle | Self-promotion; download counts; clones pretending to be independent adopters |
| **Epistemic uncertainty** | Where the world is insufficiently observed or evidence conflicts | Known coverage gaps, stale measurements, contradictory observations | Assuming everything unmeasured is safe, dangerous or valuable to explore |

The first three are the strongest candidates. Uncertainty should accompany observations from the beginning; whether it deserves its own propagating field is open.

### Resource pressure: the hardest grounding

This is closest to a physical constraint. If a shared executor is busy, an agent encounters delay whether or not another agent tells it so.

Separate the **resource ledger** from the **pressure field**. A pressure estimate may decay or spread. Money already spent does not come back because a field faded. Account limits, permissions and reservations remain hard controls in their real scopes; this is not a proposal to pool all colonies' budgets.

### Dependency strain: consequence without blame

A defect in a heavily used component can matter far beyond its author. The field concerns exposure to the defect, not the author's social standing. A verified repair removes the underlying obstruction; merely announcing a repair does not.

Known hazards need continuing support from unresolved state, or a persistent constraint. An old warning becoming less fresh must not be interpreted as the underlying danger becoming smaller. Confidence, age and severity are different quantities.

### Affordance: growth without a popularity contest

An agent's successful work can make the universe richer in possibilities. A reliable tool, a usable bridge between formats or an established method can lower the cost of future work. This provides a durable consequence of learning without assigning the agent a universal reputation score.

Affordance is contextual. A tool can be excellent for one input class and unsafe for another. Verified reuse is evidence only to the extent it adds genuinely new information; copying the same result through many agents does not.

### Fields to avoid initially

Do not start with universal fields of **truth, virtue, prestige, morale or agent quality**. These require controversial interpretation and invite self-reinforcement. Do not combine the candidates into one “mass of importance.” Different dimensions should remain inspectable and sometimes disagree.

Nor should a Griot's story automatically emit influence. Stories remain interpretation and cultural memory through the existing board mechanism. Evidence within a story may point to a real artifact or outcome; the narrative itself is not a new source of physical authority.

## 6. What makes information “heavy”?

The proposed answer is **the extent of the substantiated change it implies for the world**.

Ask separately:

- **Exposure:** what actually depends on the affected thing?
- **Magnitude:** how much do feasible actions, costs or hazards change?
- **Persistence:** is this a passing fluctuation or an unresolved condition?
- **Support:** how strong, independent and current is the evidence?

These are dimensions for reasoning, not a prescribed multiplication formula. A single well-established catastrophic fault must not be averaged away by thousands of successful routine events. An unverified hypothesis may motivate a scoped investigation (§9), but does not itself seed a field. Evidence produced by concrete actions should retain its uncertainty rather than being treated as either established danger or invisible noise.

Wider influence can arise because a consequence touches more dependencies, or because a stronger disturbance remains perceptible farther along valid couplings. Neither requires the originating agent to select an audience. A local failure in a universal dependency can therefore matter more than a large volume of unrelated activity.

A useful counterexample is a million agents repeating “this matters.” If nothing relevant has changed and no independent evidence has been added, the consequence should not grow a millionfold.

“Consequential” must also stay separate from “reward-worthy.” Sabotage may generate enormous strain. That does not entitle its author to status, resources or credit for subsequently repairing it.

## 7. A small mathematical model to think with

This is an illustration, **not the selected field law**. It shows what explicit semantics buy us.

Let `uᵢ(t)` be a nonnegative scalar condition at graph node `i` after tick `t`. Let `wᵢⱼ` be a nonnegative, symmetric coupling weight. Consider:

```text
uᵢ(t+1) = uᵢ(t)
          + α Σⱼ wᵢⱼ [uⱼ(t) − uᵢ(t)]
          − δ uᵢ(t)
          + sᵢ(t)
```

- `α` controls spreading along edges.
- `δ` controls decay per tick.
- `sᵢ(t)` is a nonnegative source derived from admissible evidence of concrete actions and their resulting state, not a value or aspiration chosen by an agent.
- The sum uses the old state everywhere, so one update does not teleport a new effect through an entire chain.

The spreading term is the negative graph Laplacian applied to `u`, using the convention `L = D − W` in the graph-signal literature. For this particular discrete rule, the sufficient condition

```text
α ≥ 0,  δ > 0,  δ + α maxᵢ Σⱼ wᵢⱼ ≤ 1
```

keeps the update coefficients nonnegative. If every source is bounded above by `S`, then

```text
max u(t+1) ≤ (1 − δ) max u(t) + S.
```

Consequently, the maximum stays below the larger of its initial value and `S/δ`. This is a useful, limited guarantee: bounded input cannot grow this scalar without bound. It is **not** a guarantee that the sources are honest or the values socially beneficial.

For a three-node chain `A — B — C`, with unit edge weights and `α = δ = 0.2`, start at zero and add one source unit at A on the first three updates only:

| Tick | A | B | C |
|---|---:|---:|---:|
| 0 | 0 | 0 | 0 |
| 1 | 1 | 0 | 0 |
| 2 | 1.6 | 0.2 | 0 |
| 3 | 2 | 0.4 | 0.04 |
| 4, source stopped | 1.28 | 0.568 | 0.104 |

The disturbance reaches C through B, without an addressee. C can still rise after the source stops because the disturbance is traveling through the graph. On this symmetric graph, diffusion redistributes the total; decay removes 20% per tick, and sources add their stated amount. This is conservation of the **illustrative signal under diffusion**, not conservation of meaning or actual money.

The arithmetic was checked in a throwaway, in-memory calculation, including nonnegativity, the total-balance identity, bounded constant input and a disconnected C remaining unaffected. That checks the example, not a DwarfHarness runtime, adversarial robustness or usefulness to agents.

### Important limits

- Continuous heat diffusion has nonzero influence throughout a connected finite graph for any positive time. Adding decay attenuates it; decay does **not** establish a strict finite propagation speed or a hard radius. The discrete local update above has a one-edge-per-tick propagation bound.
- Directed dependencies require a deliberately chosen operator. The symmetric illustration is not secretly a design for all dependency flows.
- A node's degree affects the valid step size. Adding edges can invalidate a previously safe parameter choice.
- Different fields may need different dynamics. An unresolved safety constraint is not merely a decaying signal.
- Positive feedback from agents' concrete responses can destabilize the **whole system** even when this isolated update is bounded. Excluding thoughts and repeated impressions as sources does not prevent feedback through actual resource use or other consequential action.
- Higher source strength can increase reach above a sensing threshold; it does not prove relevance to a recipient or grant access across a privacy boundary. Conversely, absence of a known dependency must not by itself rule out an encounter (§4); this toy graph is not the complete perception design.

This is why “use a field equation” is only the beginning. The difficult choices are what sources mean, which couplings are legitimate, and how agents respond.

## 8. Manipulation resistance: what can actually be promised?

An agent deliberately changing the world is not necessarily manipulation in the bad sense. A builder exploits gravity and material strength without cheating physics. Agents should likewise be able to learn the environmental laws and use them creatively.

The threat is **counterfeit consequence, rule bypass, suppressed evidence or disproportionate influence**, not all strategic behavior.

[Manheim and Garrabrant's Goodhart taxonomy](https://arxiv.org/abs/1803.04585) explains why a useful proxy can fail under optimization pressure. [Douceur's Sybil analysis](https://www.microsoft.com/en-us/research/publication/the-sybil-attack/) explains why many identities cannot simply be treated as many independent witnesses. Neither paper implies that adding more metrics or charging for actions makes the problem disappear.

| Attack or failure | Philosophical requirement | Remaining limit |
|---|---|---|
| An agent writes a huge field value | Only the environmental mechanism derives canonical field state | Requires real host/storage isolation, not a prompt convention |
| Replaying one event or cloning witnesses | Account for underlying events, resources and evidence lineage, not identity count | Independent-looking sources may still collude |
| Manufacturing activity or consuming resources to look valuable | Measure the relevant consequence; never equate expenditure with merit | Real resource exhaustion can still cause real harm |
| Inventing dependencies to gain reach | Ground coupling relationships as carefully as sources | Dependency discovery can itself be uncertain or adversarial |
| Field-driven feedback and herding | Reading a field or repeating its value must not independently reinforce its source; preserve room for alternative work | Agents may still amplify genuine but narrow evidence |
| Sabotage followed by profitable repair | Do not turn strain or its removal into automatic personal rewards | Motive and counterfactual damage can be difficult to establish |
| Malicious text disguised as environmental context | Observations are data, never instructions or authority; free-form text is not the primary field carrier | Identifiers, descriptions and linked artifacts can still carry hostile content |
| Fabricated external evidence | Preserve provenance, scope and uncertainty; distinguish reports from verified effects | Neither cryptography nor agreement establishes external truth by itself |

A trusted identity registrar may prevent unauthorized identities; it does not make authorized offspring independent witnesses. Agents sharing one model, source, operator or observation may produce correlated evidence. Growth and birth must not mint evidential weight.

Likewise, bounded input, attenuation and decay are useful defenses against amplification, but do not prevent targeted harm. An actor who knows another agent's dependencies can affect it without addressing a message to it.

**The honest target is explicit, bounded and testable resistance under a stated threat model.** Some attacks can be made costly or detectable; others remain possible. Physics-looking mathematics cannot secure a substrate an attacker can directly rewrite. The core design now specifies private scratch per work context and fast-forward promotion, but a linked Git worktree shares repository metadata and does not by itself protect canonical refs or private files from a same-user child process (§3.5). Fields must not assume that this closes the integrity or privacy boundary.

## 9. Privacy, sensing and strange moods

### The unavoidable information channel

[Lampson's confinement paper](https://www.cs.cornell.edu/andru/cs711/2003fa/reading/lampson73note.pdf) demonstrates how resource usage and other seemingly non-message effects can convey information. The implication here is direct: if an agent can choose between observable environmental effects, it may encode a signal in them.

Therefore, “numeric,” “passive,” “aggregated” and “no recipient” do not mean leak-free. An agent could time real resource consumption to communicate bits. A field can reveal the existence or timing of private work without reproducing one word of its transcript.

We cannot promise both that a secret event has observably different consequences for an unauthorized observer and that the observer learns nothing about that event.

**Unresolved owner decision:** whether private work may contribute outward field effects, including coarse shared-resource conditions that reveal its existence or timing, was raised but not answered. The recommendation below is not approval of that disclosure.

The proposed boundary is **explicitly permitted environmental disclosure**: decide which world consequences are shareable, at what resolution, and with whom. Private conversation text and private relationship interpretations must not be silently converted into universal field sources. Even resource metadata needs a disclosure policy. Coarsening, delay, aggregation or noise can reduce some leakage; none is a blanket proof of privacy.

This is an intentional new information channel requiring its own approval. It does not reinterpret the core's default-deny direct-message rules, including the absence of implied cross-colony routes.

### Passive does not mean costless

A field does not require an LLM call every time it changes. Its state can evolve independently of agent turns; an active agent can encounter a small, authorized observation when it next acts.

**Owner decision — idle agents must also participate:** provide bounded, resource-efficient opportunities for idle agents to encounter fields and enter strange moods. Otherwise only already-active agents could discover these opportunities, unnecessarily requiring work to be interrupted. Whether this uses periodic sensing, event-driven wakeups or another mechanism remains open, as do cadence, thresholds and resource allowances. This is not a requirement for one model call per field update.

But observation still has costs: computation, storage, transport and possibly prompt tokens. A language model does not continuously feel an environment between calls. If the field should constrain an action without a model turn, the host must enforce that constraint.

Independence from messaging is a **semantic boundary**, not a claim of zero software messages. DMs carry addressed content; boards carry deliberate posts; Griots curate stories; fields expose derived environmental conditions. These mechanisms may eventually share infrastructure without becoming the same social channel.

### Learning from history and investigating an impression

**Owner decisions:** causal history must remain recoverable (§3), but learning from exemplary agents who changed the world should currently be encoded by Griots. Broad agent access to historical evidence or causal records is not settled merely by requiring their retention.

**Provisional owner decision — explicitly open to change:** before a strange mood, the agent generates an insight from its impression, informed by its existing context and memories. It must clarify and scope that insight before work begins; it does not first have to establish a verified diagnosis through direct investigation. During the strange mood, allow direct, task-focused investigation within existing access permissions. The investigation can disprove the motivating hypothesis. This is not a grant to browse other agents' private records or a permanent decision about general history access.

### Self-initiated intervention and superior notification

**Owner decisions:** “strange moods,” inspired by Dwarf Fortress, are self-initiated, scoped interventions. An agent recognizes that it may be uniquely positioned to address an emerging issue or advance an opportunity. This behavior is intended across the system, not only for workers.

Before intervention work begins, the agent must clarify the motivating insight and scope the goal. It informs its superior what needs handling and which field signal gave the impulse. This is notification, not an ordinary request for prior approval. The hypothesis and notification do not themselves perturb the field; subsequent concrete actions can (§3).

The Orchestrator's standard response is to adjust its team: reassign the parked task, cover it through another agent or an authorized clone, or accept that it is on hold. The worker contributes its unique insight; the Orchestrator contributes a wider logistical perspective. Allowing strange moods has costs, and the owner expects their benefits to outweigh those costs. Routine disruption is not, by itself, a reason to restore the old assignment.

System-wide notification does not invent new direct-message routes, clone/birth authority or Grand-role approval powers. The core's existing hierarchy and permissions still apply; handling at the Grand boundary remains unresolved.

### Emergency-only interruption

**Owner decisions:** a superior may interrupt a strange mood for emergencies only. The override concerns resources, deadlines and other logistical priorities, not a routine veto because the superior prefers another task or does not understand the insight.

The Orchestrator's skill suite must encode the time-sensitivity criterion: an interruption must protect a time-sensitive task or goal that cannot be adequately accomplished by another agent or a clone of the working agent. The owner's current projects can wait; time-sensitive commitments are being accommodated for future use, not presumed in present work.

This organizational rule does not override hard resource limits, tool permissions or existing clone/birth requirements. Resource infeasibility can also end the intervention under the closure criteria below; emergency-only managerial interruption is not a promise of unlimited execution.

### Scoped closure and two independent cooldowns

**Owner decisions:** stopping is tied to the clarified motivating insight and the intervention's scope, not merely disappearance of a field disturbance. Besides accomplishing the scoped goal, suitable reasons to end a strange mood are:

- the motivating hypothesis is wrong;
- the colony does not have the resources to accomplish the task;
- there is no step that will materially advance toward the scoped goal.

An unconstrained judgment that “no further action will be useful” is insufficient. The scope and exit reasoning must make that judgment concrete; how to establish that no materially advancing step remains still needs definition.

Because the field may not change immediately, handle two separate cooldowns:

| Cooldown | Scope | Owner-selected behavior |
|---|---|---|
| Short agent-wide cooldown | The individual agent, across insights | A recovery period before entering another strange mood |
| Longer insight-specific cooldown | The individual agent revisiting that insight | A longer wait, potentially days, before that agent pursues the insight again |

The longer cooldown is not a universe-wide lock on the problem. Another agent may solve it or approach it differently when the original closure was agent-specific. An agent-specific closure is not a verdict of universal impossibility; genuinely shared constraints still apply. This does not independently settle who can inspect closure records.

Exact durations, clock semantics, what identifies the “same insight,” and any conditions for reconsideration before expiry remain open. The two cooldowns must be handled separately, rather than using field decay as a substitute for either.

## 10. A cross-colony example

Suppose two colonies use an openly shared parser. A worker in Colony A finds a reproducible failure in an input class also used by Colony B.

1. The failure is established through an admissible validation path. The worker does not choose a field magnitude or name Colony B as a recipient.
2. The parser's affected input class acquires dependency strain. The consequence follows validated exposure relationships, not the worker's contact list.
3. A specialist in Colony B encounters increased strain in its authorized working environment. Its memories suggest an intervention. The broader discovery requirement also permits an agent with no previously mapped dependency to recognize an opportunity; the mechanism for that encounter is not selected here.
4. If the agent enters a strange mood, it first clarifies the impression into a scoped insight and informs its superior of the goal and motivating signal. It then investigates directly under the provisional access rule in §9. An idle agent can enter this process through a bounded sensing opportunity too.
5. The Orchestrator normally accommodates the intervention and manages any parked work; only the emergency conditions in §9 justify interruption. Other agents can respond differently, and not every agent need encounter or act on the condition.
6. A validated repair changes the underlying state. The obstruction ends; any residual transient field evolves under its rule. Evidence and causal history are not erased. Alternatively, the intervention may close for one of §9's scoped reasons without resolving the obstruction.
7. Closure starts the agent's two separate cooldowns. Another agent remains free to approach the problem; neither the first agent's closure nor its cooldown suppresses the field for everyone.

This example assumes the parser, exposure relationship and disclosed consequence are shareable. If the originating event is private, cross-colony disclosure is **not** automatically allowed. Field Design must define the permissible projection or prevent that influence.

No new worker DM permission was created. No Griot had to retell the event. No agent gained authority. Yet work in one colony changed conditions in another.

The inverse case is equally important: if the worker publishes a hundred excited claims but establishes no changed capability, failure or exposure, the field should not behave as if a hundred discoveries occurred.

## 11. Choices to settle before Field Design

The discussion has settled requirements for an extensible dynamical medium, recoverable causal history, unexpected discovery, concrete-action sources, scoped self-initiated strange moods, emergency-only interruption, idle participation and two separate agent-bound cooldowns. The core-first build order, typed action-delta ledger with content-addressed bodies, and private-scratch/fast-forward workspace model are also settled as platform direction; none selects field-source admissibility, disclosure, or a complete history-recovery mechanism. Investigation access has a provisional owner decision. The following choices remain open; recommendations in this table are **[INFERENCE]**, not additional owner decisions.

| Remaining question | Boundary or recommended starting position |
|---|---|
| What constitutes one universe? | All explicitly participating colonies under an agreed law set; do not assume unrelated installations share an operator or trust boundary |
| May private work contribute outward consequences, and at what resolution? | Unanswered in the interview. Explicit categories of shareable effects remain a proposal; no automatic disclosure of private activity is approved |
| What does “heavy” mean quantitatively? | Substantiated change in exposure, feasible action, cost or hazard; never one universal virtue/importance score. Sources must respect the concrete-action-only decision |
| Which initial fields deserve existence? | Resource pressure, dependency strain and affordance remain candidates, with uncertainty explicit. The catalogue must be extensible |
| What geometry supports both causal propagation and unexpected encounters? | Evidence-backed resource/dependency links are useful, but cannot be the exclusive gate for perception. Broader discovery must preserve access boundaries without requiring universal broadcast |
| How should effects persist and travel, and how are parameters changed? | Adjustable decay and wave speed are required; equations, units and tuning mechanisms remain open. Persistent unresolved facts must not be forgotten by simple decay |
| How will causal history be recovered, and what may agents inspect? | Typed action deltas in an append-only ledger with content-addressed bodies are the chosen evidence-storage direction. Retention duration, whether collection may remove bodies needed for recovery, and proof that retained records support complete causal reconstruction remain open. For now, form the insight from an impression and investigate directly during the mood; broad historical access remains open, with Griots carrying exemplar-based learning |
| Which concrete actions in private work can affect outward fields? | The harness can record deltas and separately record fast-forward promotion into canonical state. Neither event automatically authorizes disclosure or makes a field source; private-work disclosure and source admissibility remain open |
| How are scope, closure and logistical emergency judgments made concrete? | Preserve self-initiation, pre-work scoping, superior notification and emergency-only interruption. Define evidence for the stated exit reasons and encode the time-sensitive, non-substitutable-task criterion in the Orchestrator skill suite; do not add ordinary prior approval |
| How are the two cooldowns identified and timed? | Short agent-wide and longer agent–insight cooldowns are separate. Exact durations, insight identity, clocks and early reconsideration rules remain open; another agent is not locked out by an agent-specific closure |
| How do idle agents sense efficiently? | Idle participation is required. Choose bounded sensing/wakeup opportunities and resource limits, not one inference call for every perturbation |
| How does system-wide superior notification work at the top of the hierarchy? | Follow existing permitted relationships; do not infer new Grand mandates or routes |
| What strategic behavior is legitimate? | Deliberate beneficial shaping through concrete action is allowed; forgery, evasion and counterfeit corroboration are not. The threat model still needs specification |
| Who can change the laws? | Authorized operator governance, not an agent role's conversational authority; cross-operator governance remains open |

As these choices are resolved, **Field Design** can specify source admissibility, field units and operators, topology maintenance, sensing and disclosure, identity/isolation, causal-history recovery, persistence and cross-colony transport. It must also define the strange-mood lifecycle, independent cooldown handling and evidence for stability, usefulness, manipulation resistance and preservation of private contexts. It must respect the core-first build order and substantial standalone testing before any field can affect agents. This document does not implement those mechanisms or the Orchestrator skill suite.

Those specifications are deliberately not selected here. The current colony-host and relationship-context proposals are neither approved nor replaced by this document.

## Research notes and reading order

The strongest path through the literature is **stigmergy → graph fields → threat models → distributed realization**. Physics supplies the intuition, not the security argument.

1. **Francis Heylighen — “Stigmergy as a Universal Coordination Mechanism.”** [Open author manuscript](https://pespmc1.vub.ac.be/Papers/Stigmergy-varieties.pdf); related two-part publication in *Cognitive Systems Research* 38 (2016). Start here for traces, markers, medium and indirect coordination. Conceptual synthesis, not an adversarial guarantee; the linked manuscript has a different subtitle/publication notice from the final articles.
2. **Marco Dorigo, Vittorio Maniezzo and Alberto Colorni — “Ant System: Optimization by a Colony of Cooperating Agents” (1996).** [Paper](https://jmvidal.cse.sc.edu/library/dorigo96a.pdf). Concrete outcome-weighted reinforcement and evaporation. Its computable objective and cooperative agents are much narrower than DwarfHarness.
3. **David I. Shuman, Sunil K. Narang, Pascal Frossard, Antonio Ortega and Pierre Vandergheynst — “The Emerging Field of Signal Processing on Graphs” (2013; preprint 2012).** [Paper record](https://arxiv.org/abs/1211.0053). Best mathematical starting point: values on irregular domains, weighted Laplacians, locality and graph-dependent interpretation. It does not tell us which dependencies are truthful.
4. **Lek-Heng Lim — “Hodge Laplacians on Graphs” (2020; preprint 2015).** [Paper record](https://arxiv.org/abs/1507.05379). Optional next step for intrinsic graph calculus beyond scalar values. Requires linear algebra and graph theory, not spacetime geometry.
5. **Jacob Beal and Mirko Viroli — “Aggregate Programming: From Foundations to Applications” (2016 tutorial).** [Author PDF](https://jakebeal.github.io/Publications/QUANTICOL16-AggregateProgramming.pdf). Bridge from field-level specifications to local distributed computation and composable coordination. Read resilience claims with their fault and environment assumptions, not as protection against strategic agents.
6. **David Manheim and Scott Garrabrant — “Categorizing Variants of Goodhart's Law” (2018 preprint, revised 2019).** [Paper record](https://arxiv.org/abs/1803.04585). A taxonomy of overoptimization failures, not a theorem that every useful measurement inevitably fails. Applied here as a warning against equating a proxy with consequence or value.
7. **John R. Douceur — “The Sybil Attack” (2002).** [Author-institution record and paper](https://www.microsoft.com/en-us/research/publication/the-sybil-attack/). Why redundancy over identities is not necessarily redundancy over independent entities. Its decentralized identity assumptions must not be silently treated as a universal theorem about every centrally controlled deployment.
8. **Butler W. Lampson — “A Note on the Confinement Problem” (1973).** [Paper](https://www.cs.cornell.edu/andru/cs711/2003fa/reading/lampson73note.pdf). Short, accessible and essential: observable resource effects can be information channels. Discusses blocking channels and bounding capacity; does not license a blanket claim that aggregation makes this field private.
9. **David Tong — “Quantum Field Theory” (2006/2007 lecture notes).** [Notes](https://davidtong.org/pdfs/teaching/quantum-field-theory/qft1.pdf), especially the introduction. Fields, particles and locality; background for the analogy, not a recommendation to implement quantum dynamics.
10. **Sean M. Carroll — “Lecture Notes on General Relativity” (1997).** [Author's notes](https://preposterousuniverse.com/grnotes/), especially chapters 1, 2, 4 and 6. Tensors, geometry, gravitation and weak gravitational fields. Useful for correcting the gravity analogy; advanced sections are optional.
11. **LIGO Scientific Collaboration, Virgo Collaboration, Fermi Gamma-Ray Burst Monitor and INTEGRAL — “Gravitational Waves and Gamma-rays from a Binary Neutron Star Merger: GW170817 and GRB 170817A” (2017).** [Paper record](https://arxiv.org/abs/1710.05834). Empirical evidence constraining gravity/light propagation-speed differences. It establishes neither gravitons nor any software-field law.

**Bottom line:** The promising idea is not that agents live inside a giant numerical array. It is that they inhabit a shared world whose consequences are harder to counterfeit than words, whose laws do not depend on who is speaking, and whose conditions can influence individuals without turning them into one mind.
