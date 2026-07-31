# loom — procedural memory as data

> **English** · [Español](DESIGN.es.md)

> ## ⚠️ Historical document. The tool is called **batten**.
>
> This file is the **v3 design document, dated 2026-07-14**, written before the rename. It is kept
> unrewritten because it is a dated record: rewriting it backwards would turn it into a forgery.
> While reading, translate:
>
> | says | means |
> |---|---|
> | `loom` | `batten` |
> | `loom.yaml` | `batten.yaml` |
> | AgroSat, FarSLIP, US-0NN | the private project it was dogfooded on |
>
> **This is not usage documentation and it may be out of date.** What the tool does today is in
> [README.md](README.md); how to install it, in [docs/INSTALL.md](docs/INSTALL.md); what changed
> and when, in [CHANGELOG.md](CHANGELOG.md); and what remains open, in [ROADMAP.md](ROADMAP.md).
> The decisions in §1 and the thesis in §0 are still the project's reason to exist — that is what
> is worth reading here.

> **v3** of the `ecosistema-agentico-2026` lineage. v2 asked "how do I generalize my harness into a product?".
> v3 answers with a narrower, more defensible thesis: **the gap is not observability, it is procedural memory.**
>
> **Date**: 2026-07-14. Based on verified research into `kepano/obsidian-skills`, `Graphify-Labs/graphify`,
> `headroomlabs-ai/headroom`, the Claude Code plugins/hooks docs, and the actual code of `Gentleman-Programming/engram`.
> **Corrects six things from v2** (§1) and **kills the most expensive item on its roadmap** (§7).

---

## 0. TL;DR

A coding agent has three memories. Two are already solved. The third does not exist.

| Plane | What it remembers | How it forms | Who has it |
|-------|--------------|---------------|----------------|
| **Structural** | what the code **IS**, right now | it is **rebuilt** (AST, deterministic) | graphify |
| **Episodic** | what **WE DECIDED**, over time | it **accumulates** (observations) | engram, claude-mem |
| **Procedural** | **HOW WE WORK** | it is **declared** | **nobody** |

The Medium article that started this (Graphify + Obsidian, "70x fewer tokens") builds plane 1 and calls it memory.
That is half the picture. An agent that knows perfectly what your code is and what you decided yesterday **still does not know how you work**:
which phases it runs, what parallelizes, what a disjoint write-set is, what counts as evidence, what blocks a close.

Today that lives in prose (`prompts_optimizers_v2.local.md`, 700 lines, pinned to one project), gets pasted by hand into every
session, and **cannot be enforced**: "no `APPROVE` with an empty `evidence[]`" is a rule the agent can
ignore without consequence.

**`loom` turns that prose into a declarative spec (`loom.yaml`) + an engine that executes it and enforces it through hooks.**

A pure Go binary + a thin Claude Code plugin. It does not re-orchestrate the agent: **it lays down the rails**.

---

## 1. Corrections to v2 (`ecosistema-agentico-2026`)

The July research forced six corrections. Documenting them avoids repeating the imprecision.

| # | v2 section | What it said | Verified correction (Jul 2026) |
|---|-----------|--------------|----------------------------------|
| 1 | §8 TUI | "no Go library does Sugiyama→ASCII layout; building the graph TUI (~weeks) is the wedge" | **True, and irrelevant.** The **JSON Canvas 1.0** spec is complete and trivial (nodes `id/type/x/y/width/height/color`, edges `fromNode/toNode/fromSide/toEnd/label`). Emitting the run-DAG as a `.canvas` is ~200 LOC and **Obsidian renders it for free**, navigable and git-diffable. **The TUI stops being the MVP and becomes an optional phase.** The wedge was never drawing the graph — it was *having* the graph. |
| 2 | §10 obsidian-bridge | "build the bridge to the vault (Go module, shelling out to the Obsidian CLI)" | **Do not build it.** (a) graphify already exports a vault + a native `graph.canvas` (`--obsidian`). (b) The Obsidian CLI **requires the desktop app to be running** — useless headless/CI. **Write files straight into the vault** (which is exactly what `kepano/obsidian-skills` enables: `.md`/`.base`/`.canvas` are just files). The CLI stays an interactive luxury, not a dependency. |
| 3 | §5 distribution | "shape = exactly engram" | **Engram gets this wrong and must not be copied.** A plugin's `bin/` **is** added to the Bash tool's PATH automatically (docs, "File locations reference"). Engram does not use it: it installs the binary out of band (brew / `go install`) and **if the user did not install it, its hooks fail silently**. Its hooks are also shell scripts depending on `bash`+`jq`+`curl` → fragile on Windows. **loom ships the binary and reads the hook JSON from stdin in exec form: zero shell dependencies.** |
| 4 | §14 headroom (absent from v2) | — | **Evaluated and admitted as optional.** Issue #951 (the pre-forked daemon did not inherit `ANTHROPIC_BASE_URL`) **has been closed since 2026-06-18**; the correct integration today is `headroom init claude` (`SessionStart`/`PreToolUse` hooks, `supervisor_kind: none`). Its memory is **opt-in**, so it does not step on engram unless you pass `--memory`. Its **CacheAligner preserves the prompt cache** (96% hit, measured independently). **BUT**: the real gain in coding is **~15-25%, not 60-95%** (the 95% is JSON), and there is an unresolved doubt over whether it saves anything in fan-out workflows. → an optional **instrumented** capability, not a piece of the architecture (§9). |
| 5 | §6.1 SQLite | "a single-writer goroutine + busy_timeout" | Incomplete. It misses the thing engram does do and almost everyone omits: **`db.SetMaxOpenConns(1)`**. Without it, the `database/sql` pool opens several connections to the same file and hands you `SQLITE_BUSY` **against yourself**. The 4 PRAGMAs + `SetMaxOpenConns(1)` are the complete pattern. |
| 6 | §2 "the product is observability" | the wedge was "loop + budget governor + DAG-TUI" | **The wedge is narrower and better: the spec.** Observability is a *consequence* of having the process declared, not the product. A DAG-TUI without a spec is a pretty log viewer; a spec without a TUI already blocks closes without evidence and already prevents write-set collisions. **The value is in the `loom.yaml`, not in the graph.** |

---

## 2. The product in one sentence

> **`loom.yaml` is to your process what a `Dockerfile` is to your environment**: a declarative, versioned,
> diffable file that turns an implicit practice into an executable, verifiable artifact.

Everything else (binary, hooks, SQLite, canvas, MCP, TUI) exists to **serve** that file.

---

## 3. The generalization — what is generic and what belongs to the project

Your 7-phase workflow **is already a generic state machine disguised as prose**:

```
research → plan → build(fan-out) → verify(gate) → fix → reverify(gate) → close
```

That is universal. It works for AgroSat, for a TypeScript SaaS, for a research repo.
What belongs to AgroSat is **pure data**:

- the list of skills per domain
- the per-layer rules (`session_id` in every query, Polars not pandas, trilingual i18n)
- the checker commands (`make check`, `pytest`, `pnpm lint`)
- the artifact paths (`docs/us-*/`)
- the contended resource (the H100) and its priority order
- the provenance key (`US + git_sha + mlflow + dvc`)

**All of that moves out into the `loom.yaml`. The engine does not know what FarSLIP is, or an H100, or a US.**

### The neutrality test

The core is neutral if and only if **the same binary runs in two unrelated repos and the only thing that differs is
the `loom.yaml`**. That is Phase 1's acceptance criterion, and it is dogfooded on AgroSat as the *first adapter*,
not as a special case.

---

## 4. The spec (`loom.yaml`)

```yaml
version: 1
project: agrosat

# El sustantivo de la unidad de trabajo. US / ticket / issue / story.
unit:
  name: US
  pattern: 'US-\d{3}'
  plan: context/RefinamientoPlaneacionAgroSatCopilot_v8.md
  locator: '### {id}'              # cómo hallar el bloque de la unidad en el plan

# Dónde escribe cada fase. {id} se sustituye.
artifacts:
  research: docs/us-research/{id}.md
  planning: docs/us-planning/{id}.md
  handoff:  docs/us-handoff/{id}.md
  manual:   docs/manual-test/{id}.md
  resolved: docs/us-resolved/{id}.md

# La máquina de estados. Los nombres son tuyos; la mecánica es del motor.
phases:
  - id: research
    optional: true                 # solo si hay tecnología nueva
  - id: plan
    reads: [research]
    graph_query: true              # consulta el grafo en vez de grepear
  - id: build
    fanout: true                   # <-- la fase de fan-out
    reads: [plan]
    anchor: git_sha                # registra el SHA base: ancla del diff
  - id: verify
    gate: qa
    diff_from: anchor              # opera SOLO sobre el diff de la unidad
  - id: fix
    interactive: true
  - id: reverify
    gate: qa
    when: fix.changed_logic
  - id: close
    requires_verdict: ok           # GATE DURO (hook, no convención)

# Los dominios = los ejes del fan-out. LA ÚNICA parte específica del proyecto.
domains:
  backend:
    path: backend/
    rules: backend/AGENTS.md
    check: ['make lint', 'make test']
    coverage: 70
    skills: [agrosat-backend-api, agrosat-backend-services]
    invariants:
      - session_id en toda query/endpoint
      - Depends(get_current_user) en protegidos
      - lógica en service, no en router
  ml:
    path: ml/
    exclude: [ml/agent/]
    rules: ml/AGENTS.md
    check: ['poetry run ruff check .', 'pytest']
    skills: [agrosat-ml-segmentation, agrosat-ml-features]
    resources: [gpu]               # declara contención -> el motor serializa
    invariants:
      - Polars, no pandas en pipelines
      - Spatial CV (build_spatial_kfold), no random split
      - MLflow con data_version + code_version

# Recursos compartidos que fuerzan serialización entre agentes.
resources:
  gpu:
    kind: exclusive_pool
    probe: 'nvidia-smi --query-gpu=memory.free --format=csv,noheader'
    unit: MiB
    priority: [farslip, tsvit, ensembles, qwen]   # orden cuando no caben

# El gate.
gates:
  qa:
    checks: ['make check', 'pnpm lint', 'make test', 'pnpm test']
    skills: [agrosat-security-audit, agrosat-code-review]
    verdict: required
    evidence: required             # evidence[] vacío -> blocked. No negociable.

# Llave de provenance: qué ancla una unidad cerrada a evidencia reproducible.
provenance:
  format: '{id} @ {git_sha7} + mlflow:{mlflow_run} + dvc:{dvc_rev}'

# Budget governor — lo que Claude Code no tiene.
budget:
  usd_per_run: 5.00
  max_iterations: 3
  on_exceed: block                 # block | warn | downgrade_effort

# Capacidades opcionales. Degradan con gracia si faltan.
capabilities:
  graph:
    provider: graphify             # | none
    query_before_read: true
    lessons: false                 # engram es dueño de lo episódico
  memory:
    provider: engram               # | claude-mem | none
  obsidian:
    vault: ~/vaults/agrosat
    export: [runs, verdicts, canvas]
  compression:
    provider: headroom             # | none
    memory: false                  # apagada: engram es dueño
    measure: true                  # instrumentar: ¿ahorra de verdad en NUESTRO fan-out?
```

**Nothing in the engine knows these words.** The engine reads `domains[*].check` and executes it; it does not know what `pytest` is.

### `loom init` — the bootstrap that makes this usable

The spec is only "general" if migrating to it is cheap. `loom init` **interviews the repo**:

- detects languages, package managers, `Makefile` targets, per-folder `AGENTS.md`/`CLAUDE.md`
- detects installed skills and maps them to domains
- `loom init --from docs/general/prompts_optimizers_v2.local.md` → **reads your workflow in prose and proposes the `loom.yaml`**

Migrating AgroSat is one command, not a rewrite.

---

## 5. The three wedges — what prose cannot do and a hook can

This is the only thing that gets built. Everything else is reused.

### 5.1 A real verdict gate

Today the golden rule ("no `ok` with an empty `evidence[]`") is a **plea**. The agent can close anyway.

`PreToolUse` with matcher `Bash`, inspecting `tool_input.command`: if it is a `git commit` and no `ok` verdict
with a non-empty `evidence[]` exists for the active unit → **deny**.

```json
{ "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "deny",
    "permissionDecisionReason": "US-034 no tiene verdict envelope con result=ok. Corre la fase verify. (loom)"
}}
```

It is not a convention. **It is impossible to skip.**

### 5.2 Verified disjoint write-sets

Today "two agents NEVER write the same file" is **discipline**. A distracted agent breaks it and you find out in the merge.

The engine knows each sub-agent's write-set (the `plan` phase's plan declares it). `PreToolUse` with matcher
`Write|Edit`, crossing `tool_input.file_path` against the write-set of the `agent_id` requesting it → **deny** if the file
belongs to another agent.

This turns your most important and most fragile rule into a mechanical invariant.

### 5.3 The run-DAG as a `.canvas`

The `SubagentStart`/`SubagentStop` hooks carry `agent_id` and `agent_type`. **The DAG falls out on its own.** It materializes as
a JSON Canvas 1.0 file in the vault:

- nodes = phases, sub-agents, gates; colored by state (green ok / red blocked / yellow warn)
- typed edges = `spawn`, `depends_on`, `retry_of`, `rollback`
- groups = the phases

Obsidian opens it and you navigate it. **Zero LOC of layout.**

---

## 6. Architecture

A pure Go binary (`CGO_ENABLED=0`, `modernc.org/sqlite`) with five faces, and a thin plugin that invokes it.

```
loom init            # entrevista el repo -> genera loom.yaml (o migra desde un doc en prosa)
loom hook <event>    # lee el JSON del hook por stdin  (exec form; cero jq/curl/bash)
loom run <unit>      # conduce la máquina de fases
loom runs | show     # consulta
loom canvas <run>    # emite el .canvas al vault
loom budget | doctor # governor + diagnóstico
loom mcp             # servidor MCP (query del grafo de corridas)
loom tui             # visor Bubbletea            (FASE 3 — opcional)
loom serve           # sink HTTP de hooks + OTLP  (FASE 3 — opcional)
```

### The plugin (thin, self-contained)

```
.claude-plugin/plugin.json        # name, version, license
.claude-plugin/marketplace.json   # en la raíz del repo
bin/loom[.exe]                    # <-- auto-PATH. Engram no hace esto; es su error.
.mcp.json                         # { "command": "${CLAUDE_PLUGIN_ROOT}/bin/loom", "args": ["mcp"] }
hooks/hooks.json                  # exec form -> bin/loom hook <event>
commands/                         # /loom-init /loom-research /loom-plan /loom-build ...
skills/loom-engine/SKILL.md       # cómo leer loom.yaml y ejecutar una fase
skills/loom-verdict/SKILL.md      # el sobre + la regla de la evidencia
```

**State in `${CLAUDE_PLUGIN_DATA}`, never in `${CLAUDE_PLUGIN_ROOT}`** — the root is wiped on every plugin update
(the docs say it explicitly: "treat it as ephemeral, do not write state here"). This is a classic footgun.

### The hooks

| Event | What loom does |
|--------|---------------|
| `SessionStart` | injects the active unit's state (phase, verdict, budget spent) |
| `PreToolUse` (`Bash`) | **verdict gate**: deny on `git commit` without an ok verdict |
| `PreToolUse` (`Write\|Edit`) | **write-set guard**: deny if the file belongs to another agent |
| `SubagentStart` | creates the node in the DAG (`agent_id`, `agent_type`) |
| `SubagentStop` | closes the node, captures `last_assistant_message`, ingests tokens/cost |
| `Stop` | closes the run; recomputes the budget; re-emits the `.canvas` |
| `PostToolUse` | ingests events into the append-only log (replay) |

### The SQLite spine

In `${CLAUDE_PLUGIN_DATA}/loom.db`. The v2 §6.2 schema, intact — it was correct.
**Complete** concurrency setup (v2 omitted the line that matters most):

```go
db, _ := sql.Open("sqlite", path)      // driver "sqlite", no "sqlite3"
db.SetMaxOpenConns(1)                  // <-- SIN ESTO te das SQLITE_BUSY a ti mismo
// PRAGMA journal_mode=WAL; busy_timeout=5000; synchronous=NORMAL; foreign_keys=ON
```

`SetMaxOpenConns(1)` serializes writes **inside** the process; WAL + `busy_timeout` absorbs contention
**across** processes (hooks are separate processes). Both, not one.

---

## 7. What does NOT get built

As important as what does. v2 was going to build three things that are not needed:

| v2 was going to build | Why not |
|--------------------|------------|
| **Bubbletea graph TUI + its own Sugiyama layout** (~weeks) | JSON Canvas + Obsidian renders it for free. Optional Phase 3, for pleasure, not need. |
| **obsidian-bridge (Go module)** | They are **files**. Write them directly. The Obsidian CLI demands the app open → useless headless. |
| **Semantic memory** | engram. Saturated niche (~89k★). Interoperate over MCP. |
| **Fan-out engine** | Claude Code's Dynamic Workflows (GA, 16 concurrent / 1000 total). **Govern, do not re-orchestrate.** |
| **Code graph** | graphify (tree-sitter, 0 LLM tokens, MIT). |
| **Context compression** | headroom, if it is shown to save (§9). |

---

## 8. How the three external pieces fit

### graphify — structural memory (adopt, decoupled)

MIT, tree-sitter, **0 LLM tokens for code**. It plugs in where your workflow **pays the re-orientation tax**:
Phase 2 literally says *"understand what ALREADY exists (Grep before Read)"* — that is exactly the spend the
graph eliminates.

```
antes:  grep -r "keyword" ml/ -l  →  Read  →  Read  →  Read   (~15-20k tokens)
ahora:  graphify query "qué ya existe para filtrado de PASTIS"  (~1-2k)
```

**With honest reservations**: the 70x is the best case of **one** benchmark. Independent reviews: <100 files ≈ nothing;
100-500 ≈ 6-15x; 500-5000 ≈ 30-49x. Its `PreToolUse` hook **already broke** with Claude Code ≥2.1.117. It is pre-1.0
(v0.9.15, branch `v8`, 537 open issues).

→ **Decoupled**: `capabilities.graph.provider: graphify | none`. If it is missing, fall back to grep. **We do not use its hook**
(we use ours) nor its `lessons`/`reflect` layer (**it steps on engram** and is its weakest part).

### obsidian-skills — the human surface (adopt, soft dependency)

MIT, by Steph Ango (Obsidian's CEO). Five pure-markdown skills. They contribute no code: **they teach the formats**.
With them, the agent authors `.md` with typed frontmatter, `.base` (dashboards), and `.canvas` (the DAG) correctly.

`loom` writes the files straight into the vault. Obsidian renders them. **There is no integration to maintain.**

A caveat that does matter: the **rendered rows of a `.base` are not script-readable**. The `.base` is a human view;
SQLite is canonical. Never scrape Bases output.

### headroom — compression (optional, and **instrumented**)

Admitted after correcting my own mistake (§1.4). But it comes in **measured, not on faith**:

- `capabilities.compression.provider: headroom`, `memory: false` (engram owns the episodic).
- Installation: `headroom init claude` (hooks, `supervisor_kind: none`). **Not** the MCP mode: there `headroom_compress`
  is a tool the model calls **voluntarily**, *after* the content already entered the context and **was already
  billed**. Only the proxy truly intercepts.
- **`measure: true`**: since `loom` already keeps per-node dollar accounting, **we measure whether it saves in YOUR fan-out**
  instead of believing the README. There is an open, real doubt over whether the hooks fire for sub-agents launched with the
  Agent tool — and your workflow is pure fan-out. If it does not save, it is turned off with one line of yaml.

Calibrated expectation: **15-25% in coding**, ~90ms of latency per request, 200-500 tokens of overhead in passthrough,
and a **silent** failure mode (misconfigured it raises no error, it simply does not save → check `/stats`).

---

## 9. Roadmap

| Phase | What | Success criterion |
|------|-----|-------------------|
| **0** | `loom.yaml` + JSON Schema + `loom init --from <doc in prose>` | AgroSat's workflow migrates to a ~80-line yaml and the original becomes obsolete |
| **1 (MVP)** | Go binary: `init`, `hook`, SQLite spine, **verdict gate**, **write-set guard** | A `git commit` without evidence **is impossible**. Two agents cannot step on one file. |
| **2** | `run` (phase machine) + `canvas` + budget governor + MCP | The `.canvas` draws the real path with retries visible; the governor cuts off when the dollar ceiling breaks |
| **3** | graphify + headroom wired and **measured**; OTLP export | Real savings numbers in YOUR fan-out, not the README's |
| **4 (gated)** | Bubbletea TUI | Only if the `.canvas` proves not to be enough |

**Neutrality criterion (blocking for v1)**: the same binary runs on AgroSat and on an unrelated TS web repo,
and **the only thing that differs is the `loom.yaml`**.

---

## 10. Risks

- **Over-declaring.** If the `loom.yaml` tries to express everything, it becomes a DSL and nobody writes it. Rule: **if a
  hook cannot enforce it or a command cannot execute it, it does not go in the yaml.** Whatever prose remains, let it stay prose.
- **A mediocre `loom init` = a dead product.** If migrating costs an afternoon, nobody migrates. It is the most important
  feature of Phase 0, not a utility.
- **Pre-1.0 dependencies** (graphify v0.9.15, headroom v0.31): which is why they are **optional capabilities that degrade**,
  not hard dependencies. The core does not know them.
- **Claude Code APIs in motion**: Dynamic Workflows is GA but the scripting may change. Hooks are the stable
  surface — lean on that.
- **The gate as friction.** A verdict gate that blocks wrongly is worse than no gate at all. It needs an explicit, audited
  escape (`loom override --reason "..."`) that **stays in the log**. Without an escape, the user uninstalls the plugin.

---

## 11. Sources (verified Jul 2026)

**Claude Code** — [plugins-reference](https://code.claude.com/docs/en/plugins-reference) (`bin/`→PATH, `${CLAUDE_PLUGIN_DATA}`, `plugin.json`/`marketplace.json` schema) · [hooks](https://code.claude.com/docs/en/hooks) (30 events, `permissionDecision: deny`, exec form, `http` type)

**Implementation reference** — [Gentleman-Programming/engram](https://github.com/Gentleman-Programming/engram) (`store.go`: `SetMaxOpenConns(1)` + 4 PRAGMAs; `hooks.json`; binary distribution)

**Obsidian** — [kepano/obsidian-skills](https://github.com/kepano/obsidian-skills) (MIT) · [JSON Canvas 1.0](https://jsoncanvas.org/spec/1.0/) · [Bases syntax](https://help.obsidian.md/bases/syntax) · [CLI](https://obsidian.md/help/cli) (requires the app open)

**Code graph** — [Graphify-Labs/graphify](https://github.com/Graphify-Labs/graphify) (MIT, branch `v8`) · [critical review](https://www.roborhythms.com/graphify-review/) (provenance of the 71x, broken hook)

**Compression** — [headroomlabs-ai/headroom](https://github.com/headroomlabs-ai/headroom) (Apache-2.0) · [#951 CLOSED 2026-06-18](https://github.com/headroomlabs-ai/headroom/issues/951) · [MCP docs](https://headroom-docs.vercel.app/docs/mcp) (manual compression, post-billing) · [independent measurement](https://andrewpatterson.dev/posts/token-savings-rtk-headroom/) (96% cache-hit; the bulk of the savings came from another tool)

**SQLite** — [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) v1.53.0 (pure Go, `"sqlite"` driver)

**Internal** — `ecosistema-agentico-2026.local.md` (v2, superseded by this doc) · `prompts_optimizers_v2.local.md` (the workflow being generalized; it becomes the first `loom.yaml`)
