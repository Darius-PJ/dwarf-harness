# Open-source harnesses: capabilities, audiences, and the price of features

**Status:** Historical first-round research, with a handoff clarification dated 2026-09-29. The original survey does not record its research date; this clarification does not date or refresh its source snapshot.

**Fresh session:** begin at [the current core-design entry point](DESIGN-CORE-AND-LAUNCHERS.md#start-here-for-a-fresh-session) for reading order, authority and the proposed next design step. This survey is supporting evidence, not a current implementation plan. Core §2 owns owner decisions; §§3–4 and §7 distinguish process/context/messaging proposals, and §10 records open mechanisms. In particular, the one-session/many-context broker model remains a proposal, not a conclusion established by this survey.

**Current versus historical assumptions:** the current product is an independently implemented Go application, not a fork, port or runtime dependency of any surveyed project. Go remains selected despite the original language report's Rust recommendation. The earlier no-C-toolchain restriction and external-browser launcher are superseded: a C toolchain is accepted for the native GUI, whose toolkit is still open. Those changes belong to the core decisions, not to a revised cross-project research result. The survey's “not essential to every harness” classifications do not remove capabilities the owner has selected for DwarfHarness.

## First-round research brief

**Purpose:** inform the design of a lightweight model harness before its audience is selected. Priorities are a short effective instruction budget, a small default tool surface, token efficiency, and low device-resource overhead.

**Coverage:** 12 model-era projects, including runnable agents and explicitly identified embeddable runtimes, plus two historical agent runtimes. The historical examples are not counted toward the 12. No existing local design documents were opened during the original first-round research; that describes its research method, not the later handoff review against the current core design.

**Evidence:** public upstream repositories, source/configuration, official documentation, and original historical publications. This is architectural desk research, not an installation or performance benchmark. Numerical configuration values are reported as configuration values, not measured savings. Resource consequences marked **[INFERENCE]** are deductions from architecture. Numerical examples marked **[ILLUSTRATION]** are calculated scenarios, not measurements of these projects. Sources and defaults are a point-in-time snapshot and can change.

**Authorship limitation:** open-source licensing does not establish whether code was written with LLM assistance. No modern project's entire codebase is certified here as human-only. JADE's early open-source implementation and Soar's archived 2010 release provide chronological, pre-LLM-coding-assistant reference points. That conclusion applies to historical implementations, not their current branches. They are agent-runtime precedents, not drop-in LLM harnesses.

## 1. Executive findings

1. **The essential core is small:** model invocation, context/state assembly, an action interface, observation/error handling, and execution/completion control. A GUI, vector database, MCP, multiple agents, or container fleet is not intrinsic to a harness.
2. **Short system prompt is an incomplete metric.** Count all harness-added instructions, schemas, skill descriptions, project guidance, retrieved material, and retained tool output. Moving instructions into a user-message template does not save tokens.
3. **Small tool surface and small authority surface are different.** One unrestricted shell tool has a small description but broad filesystem, process, credential, and network access.
4. **[INFERENCE] The best optimization target is cost per successfully completed task, not tokens per request.** A slightly larger search/edit interface can pay for itself by preventing retries and huge observations.
5. **There are two different resource bills.** Model context/inference is one; the harness process, child tools, browser, sandbox, database, and local model are another. Saving one can increase the other.
6. **The most relevant comparison is minimum useful configuration versus minimum useful configuration.** Comparing a headless Python library with an entire web platform conceals rather than explains costs.
7. **Historical systems already addressed the OS analogy.** JADE explicitly separated agent reasoning from lifecycle, messaging, discovery, and scheduling, with a “pay as you go” resource philosophy. Soar separated working memory from longer-term memory and made expensive learning mechanisms optional.

**First-round research interpretation:** the strongest starting hypothesis was a small execution core with optional services that incur cost only when used—not a comprehensive platform whose features are merely hidden in the UI. This is research rationale, not authority to replace the current core design.

## 2. What is being compared?

Here, a **model harness** is the software that turns model outputs into an ongoing, stateful interaction with tools or an environment. It supplies execution machinery that the model itself does not provide.

Three categories must remain separate:

- **Runnable products:** terminal agents and development environments, such as Pi, Aider, Codex CLI, Gemini CLI, Goose, and OpenCode.
- **Embeddable harnesses/runtime building blocks:** smolagents, LangGraph, and AutoGen; the application supplies some or all prompts, tools, policies, and UI. LangGraph can also run non-agent workflows.
- **Historical analogues:** JADE and Soar. They demonstrate agent operating-environment ideas without modern LLMs, but do not provide equivalent natural-language capability.

OpenHands spans product and SDK categories. SWE-agent and mini-swe-agent span runnable research harnesses and hackable libraries.

An open-source harness can call a proprietary model. The licenses below describe the relevant code, not every hosted service, model, extension, or enterprise offering associated with it.

## 3. Comparative landscape: 12 model-era projects

### 1. Pi — minimal coding surface, extensible runtime

**Code license:** MIT. **Audience:** terminal developers and builders who want to customize their agent rather than adopt a fixed workflow. Runnable CLI plus reusable model/runtime packages. The former `badlogic/pi-mono` repository now redirects to `earendil-works/pi`.

**Functions and differentiation:** the default coding-tool preset contains **four tools: read, bash, edit, write**. Search/listing can be performed through the shell rather than separate default schemas. It supports extensions, progressively loaded skills, branching sessions, compaction, and programmatic interfaces. Project guidance and skill descriptions still contribute to the effective prompt. The upstream repository explicitly states that Pi does not provide a built-in permission system restricting filesystem, process, network, or credential access.

**[INFERENCE] Resource bill:** four schemas keep the fixed tool-description budget bounded; shell-based discovery may require more command generation or produce noisier results than a specialized search tool. Skills loaded only when needed avoid carrying their complete instructions through unrelated work. Compaction spends an extra model call to reduce later history. The TypeScript/Node-oriented distribution has runtime/dependency costs; external isolation adds a separate cost. Four tools does not mean four small capabilities.

**Design value:** study its small model-visible core and extension boundary, without assuming its default trust model fits every audience.

Sources: [repository and permission statement](https://github.com/earendil-works/pi), [tool presets](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/core/tools/index.ts), [context assembly](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/how-pi-works.md).

### 2. mini-swe-agent — shell-only execution as a baseline

**Code license:** MIT. **Audience:** researchers comparing models, engineers automating repository tasks, and users wanting an understandable CLI.

**Functions and differentiation:** a small agent loop, shell-based actions, linear message history, and independent command executions rather than a persistent shell session. Local and sandboxed environments are supported. Step, cost, time, and repeated-format-error controls exist. Its README describes a design that can operate without model-native function calling, but the inspected **v2 default configuration explicitly requests bash tool calls**: do not equate the current product with “zero tool schemas.”

A particularly useful finding: its default system template is one sentence, but the instance template contains substantial workflow, command, and example instructions. The inspected observation template retains up to **10,000 characters** of command-output content, using the first and last 5,000 for long output; this is neither a token limit nor the total serialized observation size.

**[INFERENCE] Resource bill:** independent commands avoid a persistent-shell state machine but require the model to re-establish working-directory/environment assumptions. Linear history is easy to understand but grows unless bounded or managed. The marketed approximately 100-line agent is not the installed footprint: the base package includes dependencies such as LiteLLM, Textual, and Hugging Face `datasets`.

**Design value:** an excellent lower-complexity baseline, and a warning against judging efficiency by core line count or system-prompt length alone.

Sources: [repository](https://github.com/SWE-agent/mini-swe-agent), [pinned prompt/output configuration](https://github.com/SWE-agent/mini-swe-agent/blob/04d809ceab9df28f9adaed044884180159172930/src/minisweagent/config/mini.yaml), [pinned loop](https://github.com/SWE-agent/mini-swe-agent/blob/04d809ceab9df28f9adaed044884180159172930/src/minisweagent/agents/default.py), [pinned dependencies](https://github.com/SWE-agent/mini-swe-agent/blob/04d809ceab9df28f9adaed044884180159172930/pyproject.toml).

### 3. Aider — repository-aware pair programming

**Code license:** Apache-2.0. **Audience:** developers editing existing Git repositories with human involvement.

**Functions and differentiation:** a ranked repository map, explicit selection of full files, model-specific text edit formats, Git integration, and lint/test feedback. Its classic editing interface parses textual changes rather than requiring a conventional function-calling tool catalogue. The repository map uses parsed symbols and graph ranking. The documented `--map-tokens` default is approximately **1,000 tokens**, but the map can expand, especially when no files have been selected; it is not a universal hard cap. Aider also supports chat-history summarization controlled by a soft history-token limit.

**[INFERENCE] Resource bill:** symbol parsing and ranking spend local CPU/cache storage to reduce repository content sent to the model. Diff-style output can avoid regenerating entire files; failed patch matching can require repairs. Architect/editor workflows, automatic commit-message generation, and summarization can add model calls. Python, Git, and parsing dependencies belong in the device budget.

**Design value:** the clearest example of trading deterministic local computation for a smaller, more relevant model context.

Sources: [repository](https://github.com/Aider-AI/aider), [repository-map budget and ranking](https://aider.chat/docs/repomap.html), [edit formats](https://aider.chat/docs/more/edit-formats.html), [history/model options](https://aider.chat/docs/config/options.html#--max-chat-history-tokens-value).

### 4. OpenCode — integrated, multi-provider developer environment

**Code license:** MIT. **Audience:** developers wanting a richer terminal/desktop experience and multiple model providers.

**Functions and differentiation:** client/server architecture; build and plan agents; file/search/shell tools; subagents; project instructions; MCP and custom tools. Its prompt selection varies by model family. The tool registry is broader than Pi's four-tool coding preset, but the exact exposed set depends on model, permissions, client, and feature flags; a single universal default-tool count would be misleading.

**[INFERENCE] Resource bill:** more active schemas and model-specific guidance increase fixed context, while dedicated search/edit tools may reduce turns and output volume. Subagents isolate research context but incur their own model calls. The TypeScript/Bun implementation, client/server state, optional desktop UI, and any spawned tool services are distinct device costs. A compiled distribution still contains runtime costs.

**Design value:** study the boundary between reusable core, UI clients, and permissioned agent profiles; do not assume its full integrated feature set is necessary for a lightweight product.

Sources: [repository](https://github.com/anomalyco/opencode), [tool registry on the upstream dev branch](https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/tool/registry.ts), [model-family prompt selection](https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/session/system.ts).

### 5. Codex CLI — execution policy as a first-class service

**Code license:** Apache-2.0 for the open-source CLI repository. **Audience:** developers using interactive or scripted coding assistance.

**Functions and differentiation:** native Rust implementation, shell/patch workflows, project guidance, execution approvals, OS-level sandbox integrations, and optional MCP connections. Sandbox policy and approval policy are separate controls. Its OpenAI-oriented defaults do **not** mean it is limited to OpenAI-hosted models: official configuration documents custom providers and local Ollama/LM Studio operation through `--oss`.

**[INFERENCE] Resource bill:** a native CLI avoids requiring a separate Node/Python interpreter for the core, but this is not proof of lower measured RAM or startup time. OS sandbox mechanisms can avoid the full image/daemon/VM costs of container deployment, with platform-specific limitations. Model-tuned instructions and active tool schemas still cost context; optional MCP servers and local inference add their own process and memory budgets.

**Design value:** safety policy can be a runtime function, not a long prompt. Evaluate its sandbox/approval separation independently of its model ecosystem.

Sources: [repository](https://github.com/openai/codex), [approvals and security](https://learn.chatgpt.com/docs/agent-approvals-security), [custom and local providers](https://developers.openai.com/codex/config-advanced).

### 6. Gemini CLI — broad terminal capability around the Gemini ecosystem

**Code license:** Apache-2.0. **Audience:** terminal users wanting coding, web-grounded work, and Gemini integration.

**Functions and differentiation:** file operations, shell execution, search, web fetch, Google Search grounding, project/context files, extensions/MCP, and conversation compression. The regular distribution is a TypeScript/Node application. Its documented sandboxing is opt-in rather than automatically equivalent to having tool approvals.

**[INFERENCE] Resource bill:** bundled capabilities reduce integration work but active tool definitions and hierarchical project guidance increase context. Search grounding introduces network/service work. Node/TUI execution has a device footprint, and container-based isolation adds image, daemon, and possibly VM costs. Neither a large supported context window nor a free API quota makes unnecessary context computationally free.

**Design value:** a useful contrast to minimal defaults: broad convenience is purchased with a larger active capability surface and greater provider-specific integration.

Sources: [repository](https://github.com/google-gemini/gemini-cli), [sandbox documentation](https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/sandbox.md), [tool documentation](https://github.com/google-gemini/gemini-cli/tree/main/docs/tools).

### 7. Goose — extension-oriented general automation

**Code license:** Apache-2.0. **Audience:** users automating research, writing, data work, and development across different services and models. Originated at Block; the repository now redirects to `aaif-goose/goose`.

**Functions and differentiation:** Rust core, CLI/desktop/API surfaces, multiple providers, MCP extensions, reusable workflows, and dynamic extension management. Its official permission documentation distinguishes autonomous, manual approval, smart approval, and chat-only modes, and states that autonomous mode is the default. Approval classification is not a substitute for an OS isolation boundary.

**[INFERENCE] Resource bill:** enabling extensions as needed can avoid presenting every integration in every request. Enabled extension instructions, schemas, and observations still cost tokens. External local MCP services may add persistent processes; remote services shift computation off-device but add network/service costs. Desktop and CLI installations must be measured separately. Dynamic discovery itself may cost an additional interaction.

**Design value:** one of the strongest references for an audience not yet restricted to coding. Its extension lifecycle is more relevant to that question than its total integration count.

Sources: [repository and audience](https://github.com/aaif-goose/goose), [permission modes](https://goose-docs.ai/docs/guides/managing-tools/goose-permissions/), [prompt assembly source](https://github.com/aaif-goose/goose/blob/main/crates/goose/src/prompts/system.md).

### 8. OpenHands — agent engine versus complete operating platform

**Code license:** MIT for the inspected open-source repositories. **Audience:** autonomous software engineering, from local tasks to team-operated and remote agents.

**Functions and differentiation:** distinguish the **software-agent-sdk**—agents, conversations, explicit tools, workspaces, context management—from the current **Agent Canvas** control-center product, agent servers, automation services, and commercial hosting. The SDK can operate directly in a local workspace without deploying the whole platform. Canvas provides the broader local/remote/cloud management experience.

**[INFERENCE] Resource bill:** the headless SDK pays for its Python runtime, selected tools, state, and model use. The full product adds frontend/server/automation processes, and isolated remote workspaces can add containers or VMs. Condensation spends model work to reduce future context. Remote execution can lighten the user's device while increasing infrastructure cost elsewhere. Unsandboxed local operation is cheaper in infrastructure, not equivalent in risk.

**Design value:** a particularly useful lesson in separating the agent from everything that operates, hosts, and presents the agent.

Sources: [product repository and deployment modes](https://github.com/OpenHands/OpenHands), [agent SDK](https://github.com/OpenHands/software-agent-sdk), [SDK documentation](https://docs.openhands.dev/sdk).

### 9. SWE-agent — controlled experimentation with agent-computer interfaces

**Code license:** MIT. **Audience:** software-engineering researchers and engineers running automated issue-solving/evaluation workloads.

**Functions and differentiation:** configurable tool bundles, editing interfaces, history processors, trajectories, and execution environments. SWE-ReX supplies persistent shell/session and deployment capabilities. Unlike mini-swe-agent's deliberately simple baseline, SWE-agent is useful when the interface itself is the experiment. Its upstream README now recommends mini-swe-agent for most new use and says development effort has shifted there.

**[INFERENCE] Resource bill:** custom tool descriptions and history processing add instruction/configuration complexity, potentially earning it back through better model interaction. Persistent execution supports interactive tools but adds lifecycle/state management. Parallel benchmark environments multiply processes, storage, and model requests. Keeping a reproducible evaluation environment has a different cost justification from running a personal terminal assistant.

**Design value:** study it for interface experiments and execution reproducibility, not as evidence that every user needs a research/evaluation stack.

Sources: [repository and maintenance direction](https://github.com/SWE-agent/SWE-agent), [default tool/history configuration](https://github.com/SWE-agent/SWE-agent/blob/main/config/default.yaml), [SWE-ReX](https://github.com/SWE-agent/SWE-ReX).

### 10. smolagents — code-first embeddable agents

**Code license:** Apache-2.0. **Audience:** developers building domain-specific agents, including research, retrieval, web, and data workflows. Primarily a library, not a complete end-user environment.

**Functions and differentiation:** code-generating agents and conventional tool-calling agents, explicit user-supplied tools, a multi-step loop, and optional execution backends. An empty user tool list is valid; that does not mean the code agent has no execution capability or no completion mechanism. The local Python executor restricts execution, but upstream warns that local Python sandboxing cannot be treated as completely secure.

**[INFERENCE] Resource bill:** code can compose multiple operations and filter intermediate data without sending each intermediate object back through the model. That can save round trips and observation tokens, but generated code can fail or consume CPU/memory. Prompt examples, code generation, and execution errors still cost tokens. Stronger remote/container isolation adds another resource layer. No universal percentage saving over JSON tools is established by this report.

**Design value:** compare code execution against a small typed-tool interface on the same tasks; do not assume either representation is inherently cheapest or safest.

Sources: [repository](https://github.com/huggingface/smolagents), [agent concepts](https://github.com/huggingface/smolagents/blob/main/docs/source/en/conceptual_guides/intro_agents.md), [execution security](https://github.com/huggingface/smolagents/blob/main/docs/source/en/tutorials/secure_code_execution.md).

### 11. LangGraph — explicit state, interruption, and recovery

**Code license:** MIT. **Audience:** developers embedding long-running agents or workflows with controlled state transitions and human intervention.

**Functions and differentiation:** graph-based execution, state channels, checkpointers, stores, interrupts, and recovery mechanisms. The library is not a ready-made terminal harness with a fixed tool set or universal system prompt. Persistence must be configured; an in-memory checkpointer does not survive process restarts. Database-backed storage and commercial deployment offerings are separate choices.

**[INFERENCE] Resource bill:** deterministic graph transitions need no model call unless the application places one there. Checkpointing spends serialization, RAM/disk/database work to avoid losing progress. State retention can grow without pruning. Persistence does not require placing the entire stored state in every model request. A database server has materially different operating costs from a local file-backed store.

**Design value:** borrow interruption and recovery semantics where needed; do not automatically adopt a workflow graph for an otherwise straightforward loop.

Sources: [repository](https://github.com/langchain-ai/langgraph), [overview](https://docs.langchain.com/oss/python/langgraph/overview), [persistence and restart limitations](https://docs.langchain.com/oss/python/langgraph/persistence).

### 12. AutoGen — agent teams and message-driven orchestration

**Code license:** MIT for code; documentation has a separate license. **Audience:** developers building cooperating agents, team workflows, and event-driven applications.

**Functions and differentiation:** a layered agent runtime, AgentChat team patterns, extension/provider packages, and optional development UI/distributed execution. Single-agent use does not require adopting group chat or the Studio UI. The current upstream README places AutoGen in maintenance mode and directs new users toward Microsoft Agent Framework.

**[INFERENCE] Resource bill:** each model-backed participant can add its own prompt, history, and output; routing or speaker selection may add model calls depending on the pattern. Parallel execution can reduce elapsed time while increasing peak resource use. In-process message routing is not equivalent to deploying distributed workers. A review team is worthwhile only when its improvement in outcomes justifies its coordination and inference bill.

**Design value:** a catalogue of orchestration patterns and their trade-offs, not a reason to make multi-agent interaction the default.

Sources: [repository, license distinction, and maintenance notice](https://github.com/microsoft/autogen), [official documentation](https://microsoft.github.io/autogen/stable/).

## 4. Non-LLM-assisted historical precedents

These examples satisfy a different and explicitly limited comparison: software built before modern LLM coding assistants existed. They are not presented as modern LLM products, and no claim is made about the authorship of their present-day changes.

### 13. JADE — the agent platform as operating environment

**License:** LGPL. **Historical basis:** the current project records the start of its open-source community in February 2000. The original architecture paper describes lifecycle management, messaging, discovery, scheduling, and monitoring around independently supplied agent logic.

**Audience and services:** builders of distributed, interoperable multi-agent systems. The platform supplies agent management, a directory facilitator, communication infrastructure, behaviour scheduling, and monitoring. It can host reasoning engines rather than prescribing one form of intelligence.

**Documented resource thinking:** the original design uses one thread per agent rather than one per behaviour; behaviours can block rather than busy-wait. Local messages avoid remote transport and unnecessary serialization. Its authors explicitly describe a “pay as you go” philosophy. These are historical design facts, not claims about modern JVM performance.

**[INFERENCE] Cost and lesson:** a JVM, agent threads, queues, and distributed messaging have CPU/RAM/network costs, but require no model tokens unless an LLM is added. Those zero token costs do not buy modern LLM capability. The transferable lesson is to keep lifecycle, scheduling, permissions, and transport in deterministic runtime code rather than asking a model to administer them through prose.

Sources: [project history and LGPL](https://jade-project.gitlab.io/page/about/), [original authors' architecture paper, academic mirror](https://jmvidal.cse.sc.edu/library/jade.pdf).

### 14. Soar — working memory is not the same as everything remembered

**License:** BSD, as declared by the upstream project. **Historical basis:** the official project has been in use since 1983; the archived **Soar 9.3.0 source release is dated April 2010**, with contemporary release notes.

**Audience and services:** cognitive-architecture researchers and developers of symbolic intelligent agents. The architecture provides a decision cycle, working memory, rules/operators, learning mechanisms, and interaction with external environments. The 2010 release notes describe reinforcement learning, episodic memory, and semantic memory and state that learning mechanisms are disabled by default.

**[INFERENCE] Cost and lesson:** rule matching, memory retrieval, and recording episodes consume local computation and storage instead of LLM tokens. They do not provide the same general language capability as an LLM. The useful precedent is selective activation: separate active working state from persistent knowledge and do not continuously record or process everything merely because the capability exists.

Sources: [official architecture/history](https://soar.eecs.umich.edu/home/About/), [dated source release and release notes](https://sourceforge.net/projects/soar/files/Soar%209/9.3.0/), [repository/license declaration](https://github.com/SoarGroup/Soar).

**Provenance conclusion:** there is strong chronological evidence for these historical implementations being pre-LLM-assisted development. There is no contribution-by-contribution certification here, nor evidence establishing an entirely non-LLM-authored modern harness. If the intended requirement is specifically a modern LLM harness with certified human-only authorship, that narrower requirement remains unverified rather than being silently claimed as satisfied.

## 5. What is actually essential?

### 5.1 The shared functional core — ESSENTIAL

The common denominator is a set of responsibilities, not identical class names or APIs. For libraries, these are supplied through application composition rather than always preconfigured by the package.

| Essential responsibility | What it provides | Minimum resource consequence |
|---|---|---|
| **Invoke the model** | Send the task/context and consume an answer or action proposal. | Inference tokens/compute and network or local inference resources. A multi-provider abstraction is optional. |
| **Assemble current context/state** | Decide what instructions, task state, history, and observations the model can see. | Context occupancy plus local state storage. A vector database or summarizer is not required. |
| **Interpret and execute actions** | Translate output into edits, commands, API calls, code execution, or other effects. | Interface-description tokens plus the actual action's CPU/I/O/service cost. JSON function calling is not mandatory. |
| **Return observations and errors** | Let subsequent reasoning depend on what actually happened. | Observation tokens and output buffering/storage. Result size needs deliberate control. |
| **Control execution and completion** | Sequence actions, continue or stop, and return a result/status to a person or calling application. | Usually a small deterministic state machine. A planner model or multi-agent scheduler is not required. |

These are essential to the action-capable model harness being discussed, not claims that every generic graph application must call an LLM or that a chat-only mode must execute tools.

### 5.2 Operational essentials recommended for this vision

These are first-round research recommendations, **not owner decisions or features falsely claimed to be universal across all projects**:

- **Cancellation, command timeout, and run budget:** bound runaway work; enforce in code rather than only asking the model to be economical.
- **Bounded, inspectable observations:** return relevant excerpts, signal truncation, and preserve a retrievable full artifact when necessary. Silent loss of evidence is not efficiency.
- **Explicit trust and permissions:** separate approvals from OS sandboxing; gate dangerous authority according to the audience. Prompt instructions are not a security boundary.
- **Minimal usage accounting and execution trace:** expose calls, tokens where reported, tool failures, and stop reason. This can be local and small; a telemetry platform is optional.
- **A history-overflow policy:** even “stop and ask” or deterministic trimming is a policy. Automatic summarization is an additional service, not the definition of a harness.

### 5.3 Common or attractive, but NOT essential to every harness

MCP, repository maps, persistent semantic memory, browser control, multimodal input, planning tools, agent teams, resumable checkpoints, Git integration, remote workers, a desktop UI, a server API, workflow scheduling, a plugin marketplace, and hosted telemetry all serve particular workloads. None should enter the default merely because several competing products offer it.

## 6. Feature-cost catalogue

The functions below are observed in the landscape; the cost assessments and suitability judgments are **[INFERENCE]**, not benchmark results. “No necessary model cost” means the runtime can perform the function deterministically—not that its implementation is literally free.

| Feature | Function added | Token/inference cost | Device/service cost and lightweight reading |
|---|---|---|---|
| Small typed read/search/edit tools | Precise, validated operations | Schemas cost context; precise results may save retries and large reads | Local file/search work. A few reliable tools may beat one broad shell tool overall. |
| General shell or code executor | Broad composability | Small interface; generated commands/code and repairs can be expensive | Arbitrary subprocess CPU/RAM/I/O. Small tool count is not small authority. |
| Progressive skills/tool discovery | Load capabilities only when relevant | Saves unused descriptions; discovery/loading may add a turn | Small registry and on-demand startup. Keep dormant extensions genuinely inactive. |
| MCP integration | Reuse external tools through a standard protocol | Exposed schemas/instructions/results; discovery has its own overhead | Local server processes or remote network/service calls. Do not launch every configured service at startup without need. |
| Repository map/symbol index | Faster repository orientation | Adds a compact map, potentially avoids full-file context | Parsing, ranking, caching, invalidation. Useful for repeated large-repo work, less clear for tiny one-off tasks. |
| Retrieval/semantic memory | Find knowledge beyond current context | Query/embedding work and retrieved excerpts; irrelevant retrieval wastes tokens | Index/storage plus local embedding compute or a remote service. Not required for basic session history. |
| Output filtering/pagination/artifacts | Keep large observations out of context | Often saves repeated input tokens; follow-up paging can add turns | Small filtering/storage work; retention needs limits. Preserve errors and evidence. |
| Model-generated compaction | Continue through long histories | A summarization request plus possible recovery from lost details | Modest local state work. Savings depend on remaining turns and cache effects. |
| Prompt-prefix caching | Reuse model processing of stable context | Provider-specific discounted reads/writes; does not eliminate context occupancy | Usually provider-side state; local inference caches consume RAM/VRAM. Stable ordering matters. |
| Local session persistence | Resume and inspect prior work | No necessary model call; only reloaded context is model-visible | File/SQLite writes and retention. A transcript alone is not guaranteed crash-safe execution recovery. |
| Durable execution/checkpoints | Resume after interruption/failure | No necessary model call; can avoid repeating prior inference | Serialization, disk/database I/O, replay/idempotency design. Valuable for long jobs, not mandatory for every short chat. |
| Planning/reviewer agents | Decompose or critique work | Extra prompts, outputs, and sometimes repeated context | Additional concurrent state/processes. A deterministic checklist is cheaper when it suffices. |
| Multi-agent parallelism | Independent specialization and lower elapsed time | Worker calls plus coordination; isolation may reduce main-agent context | Higher peak concurrency/RAM and API rate pressure. Faster is not necessarily cheaper. |
| Native OS sandbox | Restrict process/file/network effects | Little inherent prompt cost; denials/approvals can cause additional turns | Platform-specific enforcement/setup. Often avoids full container infrastructure, but scope and guarantees differ by OS. |
| Container/VM execution | Reproducibility and stronger environment separation | Little inherent token cost; setup failures can add turns | Images, disk, daemons, startup, possible guest RAM. Remote placement moves rather than removes this bill. |
| Browser/computer control | Operate sites and GUI-only applications | DOM/text observations or images, repeated interaction loops | Browser/rendering processes, screenshot storage, automation runtime. Load only for workloads requiring it. |
| Automatic lint/test/retry loop | Validate and repair produced work | Test observations and repair calls | Potentially large compiler/test CPU and memory costs. Gate retries and avoid redundant runs. |
| GUI/server/scheduler | Accessibility, remote control, unattended automation | No inherent model call for the UI; scheduled work incurs normal calls | Rendering, listeners, background processes, auth and operational maintenance. Keep separable from the execution core. |
| Usage logging/tracing | Diagnose cost and failures | No necessary model tokens | Counters/local logs can be modest; full trace capture/export adds I/O, storage, network, and privacy exposure. |
| Local model support | Offline/privacy/provider independence | Removes API billing, not inference cost | Model weights, KV cache, CPU/GPU/VRAM and energy can dominate the harness. Measure separately. |

Supporting primary discussions: [Anthropic on context engineering](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents), [tool design and output efficiency](https://www.anthropic.com/engineering/writing-tools-for-agents), [OpenAI prompt-cache semantics](https://developers.openai.com/api/docs/guides/prompt-caching). These are supplementary engineering sources, not additional open-source harnesses in the count.

## 7. Quantifying “this feature costs this much” responsibly

### 7.1 Token accounting

For model call `i`, a useful logical input accounting is:

`input_i = instructions_i + active tool definitions_i + task/history_i + retrieved observations_i`

Count the complete rendered request, including task-template instructions and framework-generated messages. Stored provider-side conversation state can still be part of billable/processed context even if the client does not retransmit every byte.

The monetary bill depends on provider/model pricing for uncached input, cached reads, any cache writes, output/reasoning, and separate tool services. The device bill depends on where inference and execution occur. This report does not pretend a token is a universal amount of money, RAM, or elapsed time.

**[ILLUSTRATION — arithmetic calculated for this report]:**

| Hypothetical change | Logical input-token consequence before caching |
|---|---:|
| 3,000 fixed instruction/schema tokens over 50 calls | 150,000 tokens |
| Reduce that fixed budget to 800 over the same 50 calls | 40,000 tokens — 110,000 fewer |
| Retain an 8,000-token tool result through 10 later requests | 80,000 repeated tokens |
| Retain a sufficient 500-token excerpt instead | 5,000 repeated tokens — 75,000 fewer |
| Give four workers the same 12,000-token starting context | 48,000 starting tokens, versus 12,000 for one worker, before coordination/output |

These scenarios do not establish that 800 instructions or a 500-token excerpt would be sufficient for a particular task. Sufficiency must be tested. Cached requests have different prices and processing costs, so the arithmetic is not a dollar-saving claim.

**[INFERENCE] Compaction break-even:** future savings must exceed the summarization request, cache disruption, and any recovery/re-reading caused by information loss. Frequent compaction near the end of short tasks can be wasteful.

**[INFERENCE] Discovery break-even:** omitting many unused schemas can save substantially more than one discovery interaction; discovering a frequently used tiny tool every turn can do the opposite. Preserve a stable core and use provider-compatible append-only/deferred loading where available.

### 7.2 Device accounting

Measure at least these separately:

1. **Core:** cold start, idle/active resident memory, CPU time, executable and installed dependency size.
2. **Enabled services:** MCP servers, language servers, databases, browser, UI, schedulers.
3. **Execution:** shell children, tests/builds, sandbox helpers, containers/VMs.
4. **State:** transcripts, checkpoints, artifacts, indexes, retention growth.
5. **Inference:** remote API latency/cost versus local model CPU/GPU/RAM/VRAM.

There is no defensible cross-project “X MB per feature” table in the collected evidence. A Rust implementation, Python package, or Node application tells us about dependencies—not its measured working set. Likewise, no container or browser is required merely because a project supports one.

## 8. Audience choices and what they make worth paying for

| Possible audience | Features that earn a clearer justification | Costs to resist until justified |
|---|---|---|
| Individual coding developers | Reliable file edits/search, shell, diffs, project guidance, interruption; selected validation | Always-on browser, integration catalogue, multi-agent team, remote control plane |
| General personal/work automation | Easy extension discovery, credentials/permissions, reusable workflows, selected web/browser access | Coding-specific repo indexing, large default development toolchain |
| Developers embedding an agent | Stable API, explicit state/events, caller-defined tools/policy, headless operation | Mandatory TUI/desktop, default background services |
| Teams operating long-running jobs | Durable checkpoints, remote isolation, auditability, concurrency limits, scheduling | Assuming every user's local session needs the entire production stack |
| Local/private/offline users | Local-provider compatibility, dependency control, explicit network boundaries | Mandatory hosted integrations or telemetry; treating local inference as resource-free |
| Agent/model researchers | Reproducible environments, transparent trajectories, swappable interfaces, budget controls | Large product UI and opaque automatic context transformations |

**[INFERENCE] Audience-neutral position:** keep deployment, UI, persistence depth, and integration breadth outside the smallest useful execution configuration. This preserves options without assuming that all future services must be implemented now.

## 9. Implications for a lightweight harness

These are historical first-round research recommendations, not a current implementation plan, owner decisions or a decision on the audience. Read their imperative wording in that scope; current design authority is linked at the start of this report.

### Prefer a small runtime core, not merely a short visible prompt

The OS analogy is useful for responsibilities: session lifecycle resembles process management; tool contracts resemble system calls; context selection resembles working-memory management; permission checks resemble access control; extensions resemble drivers. It is not a mandate to implement a distributed operating system.

**[INFERENCE] Put deterministic responsibilities in code:** budgets, timeouts, argument validation, permission checks, cancellation, persistence, and output limits. Long procedural prompts are a poor substitute for enforcement.

### Optimize capability access, not the smallest possible tool count

Pi and mini-swe-agent show how small an action surface can be. Aider shows why a specialized context mechanism can still be worthwhile. smolagents shows that code can compose operations locally.

**[INFERENCE] The useful target is a small, non-overlapping set with predictable results.** A universal shell interface may be appropriate for technical users; users automating business services may benefit from narrower tools. Select based on task completion, repair rate, and authority boundaries.

### Make optional mean inactive

**[INFERENCE]** An optional feature should ideally contribute no default schema, prompt block, background process, indexer, or model call when unused. Packaging a large feature behind a disabled UI button is not equivalent. A plugin should not require every other plugin's documentation in the prompt.

### Do not save resources by silently removing safety or evidence

**[INFERENCE]** A shorter unsafe run is not a successful optimization. Neither is a small summary that drops the error needed to solve the task. Use explicit truncation, retrievable artifacts, and appropriately scoped execution. Confirmations are not sandboxing, and a sandbox is only as strong as its actual mounts, credentials, and network policy.

### Study mechanisms rather than choose a winner now

Priority references proposed by the original survey for a subsequent design discussion (not the current continuation agenda):

- **Pi:** small default tool surface and progressive capability loading.
- **mini-swe-agent:** transparent loop, independent execution, and the difference between tiny source and tiny deployment.
- **Aider:** local computation exchanged for relevant repository context.
- **Codex CLI:** sandbox/approval separation and runtime-enforced policy.
- **Goose:** integration breadth without making the audience exclusively coding-oriented.
- **smolagents:** code composition versus individual tool calls.
- **LangGraph:** interruption and persistence semantics for genuinely long-running work.
- **JADE and Soar:** deterministic runtime services and selective activation predate LLMs.

OpenCode, OpenHands, SWE-agent, and AutoGen remain useful richer contrasts, particularly for client/server boundaries, platform deployment, interface research, and agent teams respectively.

## 10. What a second round should measure

**Historical follow-on study proposal, not an approved or required next step.** The current continuation agenda belongs to [the core-design entry point](DESIGN-CORE-AND-LAUNCHERS.md#start-here-for-a-fresh-session). Language/prototype measurements recorded elsewhere have their own scope and dates; they do not constitute the controlled cross-project study proposed here.

No project performance ranking is justified by this first round. A useful controlled study would pin versions/configurations and run a small set of common tasks on a common model where supported, with provider-specific results separated where not.

Measure:

- **Success and recovery:** completed task, real validation result, repair attempts, user intervention, unintended side effects.
- **Effective context:** complete initial instruction/schema tokens; per-turn growth; observation volume; cache reads/writes; summarization and worker calls.
- **Run cost:** all input/output/reasoning tokens, external service calls, and elapsed time—not just the final answer.
- **Device footprint:** startup, idle/peak process-tree memory, CPU time, disk/dependency size, background activity, sandbox and browser overhead.
- **Robustness:** large/noisy tool output, a failed action, cancellation, denied permissions, lost network, and interrupted sessions.
- **Optionality:** repeat with extensions, browser, indexing, persistence, and agent teams enabled individually. Attribute their incremental cost and improvement in success.

Compare shell-only versus a few specialized tools; eager schemas versus lazy loading; on-demand search versus a repo map; and a single agent versus independent workers. Do not compare published benchmark scores with different models/prompts/budgets as though they isolate harness quality.

## Bottom line

**First-round conclusion, retained as research interpretation rather than current design authority.**

**[INFERENCE] A lightweight harness should be judged by useful work per token and per unit of device resource, under an explicit safety policy—not by system-prompt length, tool count, core source lines, or a language choice in isolation.**

The surveyed systems support a small common core and a large optional service space. The main design opportunity is not to include every service cheaply; it is to ensure that users pay for the services their workload actually needs.
