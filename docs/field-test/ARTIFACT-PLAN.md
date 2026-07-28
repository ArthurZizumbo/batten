I ran the real binary against an isolated copy of the replica to ground every claim below. Here is the deliverable.

```markdown
# EXPECTED-ARTIFACTS PLAN — batten on `proyecto_ui`

> What batten **should** generate over the life of one work item in a repo shaped like
> `proyecto_ui`, what each artifact must contain to be correct, and how each one fails.
>
> Grounded against the replica at
> `$SANDBOX/batten-testbed/proyecto_ui`
> using `batten.exe 0.1.0`. All commands were run against a private copy in
> `.../scratchpad/expected-plan-sandbox/` with `BATTEN_DB` pointed at
> `.../expected-plan-sandbox/state/batten.db`. The user's real repo, vault and `~/.batten/batten.db`
> were never written to.

---

## 0. Ground truth about this repo (measured, not assumed)

Everything downstream depends on these facts, so they are stated first and were counted, not recalled.

| Fact | Measured value | How |
|---|---|---|
| Per-directory rules | **5** `AGENTS.md`: root, `backend/`, `db/`, `frontend/`, `ml/` (+ `CLAUDE.md`, byte-identical mirror of root) | `find . -name AGENTS.md` |
| Project skills | **27** in `.claude/skills/*/SKILL.md`, all `portal-*` | `find .claude/skills -name SKILL.md \| wc -l` |
| Skills batten can see | **47** = 27 project + 19 plugin + 1 user | `batten init --scan-json` → `skills[].Source` |
| Custom agents | **9** in `.claude/agents/` | `ls .claude/agents/*.md \| wc -l` |
| Backlog | `context/planeacion_proyecto.md`, **44** unit headings: **36** `US-001…US-036` + **8** `US-UX-01…US-UX-08` | `grep -c '^### US-'` |
| Heading shape | `### US-001 — Entorno Docker Compose Multiservicio y Monorepo` | line 660 of the plan doc |
| Build files | **none** — no `Makefile`, `package.json`, `pyproject.toml`, `docker-compose.yml` | `ls` |
| Documented commands | `make dev/check/lint/test/data/db-*`, `poetry run pytest`, `pnpm lint/test` — **documented in `AGENTS.md`, implemented nowhere** | root/`backend`/`frontend`/`ml`/`db` `AGENTS.md` |
| Code | **zero source files.** `docs/` and `data/` are empty directories | `find . -type f` |
| Coverage floors stated in prose | backend ≥70 %, frontend ≥50 % | root + `backend/AGENTS.md` |
| Git | one branch `main`, one commit `44630cd`, no unit id in any branch name | `git branch -a`, `git log` |

**The defining tension of this repo:** it is a repo *planned in full and built not at all*. The process
exists (5 rules files, 9 agents, 27 skills, 44 stories); the executable surface does not (no build
files, no code). Every artifact below has to be correct in that state, and correct again after
`US-001`/`US-002` create the Makefile. An artifact that fabricates a `make test` it cannot run is
worse here than an artifact that says "nothing to run".

---

## 1. Artifact inventory

| # | Artifact | Path | Produced by | Lifetime |
|---|---|---|---|---|
| A | The spec | `batten.yaml` (repo root) | `/batten-init` → `batten init` | once, then edited |
| B | Run record | `$BATTEN_DB` (default `~/.batten/batten.db`) | hooks + `phase`/`claim`/`verdict`/`close` | per unit, forever |
| C.1 | Planning artifact | `docs/us-planning/{id}.md` | `/batten-plan` | per unit |
| C.2 | Handoff artifact | `docs/us-handoff/{id}.md` | `/batten-build` | per unit |
| C.3 | Manual-test artifact | `docs/manual-test/{id}.md` | `/batten-verify` | per unit, only if unverifiable criteria exist |
| C.4 | Resolution artifact | `docs/us-resolved/{id}.md` | `/batten-close` | per unit |
| D | Verdict envelope | stdin/`--file` → `verdicts` table | `/batten-verify` + `batten verdict` | per gate pass |
| E | Obsidian run note | `<vault>/batten/proyecto_ui/runs/{id}.md` | `export.Run` (verdict, Stop hook, `canvas`) | regenerated |
| F.1 | Dashboards | `<vault>/batten/proyecto_ui/{open-runs,blocked-verdicts,imputed-cost-by-unit}.base` | `vault.WriteBases` | regenerated |
| F.2 | Index note | `<vault>/batten/proyecto_ui/proyecto_ui.md` | `vault.writeIndex` | regenerated |
| G | Run graph | `<vault>/batten/proyecto_ui/runs/{id}.canvas` (or `.batten/{id}.canvas` with no vault) | `batten canvas` / Stop hook | regenerated |
| H | Statusline | `.claude/settings.json` → `statusLine`; `quota_snapshots` rows | `batten statusline --install`, then per render | once + per render |
| I | Diagnosis | stdout | `batten doctor` | on demand |

---

## A. `batten.yaml` — every section, for **this** repo

**Path:** `proyecto_ui/batten.yaml` (repo root; `spec.Find` walks up from cwd).
**Validated:** the spec below loads and passes `batten doctor` (`unit "US", 7 phases, 6 domains`).
A working copy is at `.../expected-plan-sandbox/proyecto_ui/batten.yaml`; init's own draft, for
comparison, at `.../expected-plan-sandbox/init-draft-batten.yaml`.

### A.1 `version`, `project`, `enforcement`

```yaml
version: 1
project: proyecto_ui
enforcement: report     # gates WARN. Flip to enforce when gates.qa.checks is non-empty.
```

- **Required:** `enforcement: report` on day one. This repo's gate can run **nothing** — there is no
  Makefile — so an enforcing gate would block commits on the strength of an agent's opinion.
  `report` is the adoption ramp ROADMAP.md prescribes, and it is the honest setting **until US-001
  and US-002 land the Makefile**.
- **Failure mode if wrong:** `enforce` from day one → the first `git commit` in a repo with no code
  is denied by a gate that verified nothing; the team disables the plugin in week one. Conversely,
  leaving `report` *after* the Makefile exists means the two denials never fire and batten is decoration.

### A.2 `unit` — the section most likely to be silently wrong

```yaml
unit:
  name: US
  pattern: 'US-(?:UX-)?\d{2,3}'
  plan: context/planeacion_proyecto.md
  locator: '### {id} —'
```

- **`pattern` must match both tracks.** `US-\d{3}` — the shape any frequency heuristic derives — matches
  36 of 44 stories and **silently excludes all 8 `US-UX-NN`**, which are the *graded* deliverables
  ("Regla de oro: ante conflicto de tiempo, gana el entregable de la actividad UX"). The excluded
  ones fail open: no run is opened, no write-set is claimed, no gate fires, no note is written —
  and nothing says so.
  *Verified both ids resolve under the widened pattern:* `batten phase US-015 build` → `US-015 -> phase build`;
  `batten phase US-UX-03 plan` → `US-UX-03 -> phase plan`; and the commit hook attributed
  `docs(UX): US-UX-03 personas` to `US-UX-03`.
- **`plan` + `locator` are mandatory here**, not optional: the acceptance criteria live only in that
  file, and `/batten-plan` §1 calls the located block "the contract".
- **Failure mode:** wrong `plan` path or a locator that matches nothing → the plan phase invents
  acceptance criteria, and the verify phase then cites evidence against criteria nobody agreed to.
  **`batten doctor` does not check either field** (it stats `domains[].rules` only), so this fails
  at 3am rather than at install.

### A.3 `artifacts`

```yaml
artifacts:
  planning: docs/us-planning/{id}.md
  handoff:  docs/us-handoff/{id}.md
  manual:   docs/manual-test/{id}.md
  resolved: docs/us-resolved/{id}.md
```

- Every value **must** contain `{id}` — the loader refuses otherwise; without it all 44 units
  overwrite one file.
- `docs/` already exists and is empty, and root `AGENTS.md` already routes `docs/` to "Entregables
  del curso, papers, orquestación" — so these four subdirectories agree with the existing process
  instead of inventing a location.
- **`manual` is not optional in this repo.** Eight UX stories (SUS testing, ≥5 recruited
  participants, high-fidelity Figma parity) contain criteria **no command can verify**. Without a
  declared `manual` artifact, `/batten-verify` has nowhere to put "could not check" and the pressure
  is to round it up to `ok`.
- **Failure mode:** a `reads:` referencing an artifact kind that is not a key here is rejected at
  load — good. A *missing* kind (no `manual`) is not rejected, and degrades into false `ok` verdicts.

### A.4 `phases`

```yaml
phases:
  - id: research
    optional: true
  - id: plan
    reads: [research]
    graph_query: true
  - id: build
    fanout: true
    reads: [planning]
    anchor: git_sha
  - id: verify
    gate: qa
    diff_from: anchor
  - id: fix
    interactive: true
  - id: reverify
    gate: qa
    when: fix.changed_logic
  - id: close
    requires_verdict: ok
```

- **`anchor: git_sha` on `build` and nowhere else.** Observed working: `batten phase US-015 build` →
  `anchor: US-015 base SHA = 44630cd`, stored as `runs.base_sha`.
- **Exactly one phase carries `requires_verdict`.** More than one, or none, and doctor says
  `⚠ no phase sets requires_verdict — nothing gates a commit`.
- `research: optional` earns its place here: US-020 (Google ADK), US-022 (OOD classifier) and
  US-012/US-014 (hybrid RAG) bring in genuinely unfamiliar technology; US-019 (an admin CRUD panel)
  does not.
- **Failure mode:** no `diff_from: anchor` on `verify` → QA reviews `HEAD~N` and silently grades
  someone else's commits, which in a 3-person team on `develop` is the normal case, not the edge case.

### A.5 `domains` — the fan-out axes

Six domains. Four come from the `AGENTS.md` boundaries, plus `ml/agent/` (a separate specialist and a
separate skill set) and `docs/` (the graded UX track, which has its own agent and its own stories).

```yaml
domains:
  backend:   { path: backend/, rules: backend/AGENTS.md, agent: backend-engineer,  coverage: 70, check: [], skills: [...], invariants: [...] }
  db:        { path: db/,      rules: db/AGENTS.md,      agent: data-engineer,                  check: [], skills: [...], invariants: [...] }
  frontend:  { path: frontend/,rules: frontend/AGENTS.md,agent: frontend-engineer, coverage: 50, check: [], skills: [...], invariants: [...] }
  ml:        { path: ml/, exclude: [ml/agent/], rules: ml/AGENTS.md, agent: data-engineer,      check: [], skills: [...], invariants: [...] }
  agent:     { path: ml/agent/, rules: ml/AGENTS.md,     agent: agent-engineer,                 check: [], skills: [...], invariants: [...] }
  docs:      { path: docs/,     rules: AGENTS.md,        agent: deliverable-writer,             check: [], skills: [...], invariants: [...] }
```

Required per domain:

1. **`rules`** — the file the subagent reads first. All five exist; doctor stats them.
2. **`agent`** — this repo has a purpose-built subagent per domain. Using them is what makes the run
   graph name *their* agents instead of `general-purpose` six times over.
3. **`skills`** — taken from each `AGENTS.md`'s own "Skills relevantes" table, **not** from a
   name/description keyword match. The heuristic's output is wrong in ways a human would never write:
   observed `portal-terraform-gcp` and `portal-testing` under `backend` *and* `frontend`,
   `portal-observability` under `db`, and **`batten-engine` + `ui-ux-pro-max` — two installed plugin
   skills — under `ml`**.
4. **`coverage`** — 70 backend / 50 frontend, verbatim from the QA gate in root `AGENTS.md`. These are
   the numbers the verdict cites; omitting them makes "coverage 63%" un-gradeable.
5. **`invariants`** — the highest-value lines in the file, mined verbatim from the NON-NEGOTIABLE
   sections. For this repo the non-negotiable ones a distracted agent breaks are: `Security(get_current_user, scopes=[...])`
   on every data endpoint; logic in the service, never the router; **UI in Spanish and i18n forbidden**;
   `shallowRef`/`Lazy*` in Nuxt 4; dbmate as the only schema path with reversible migrations;
   `SEED = 20260720`; the semantic compiler as the only generator of Polars expressions; the 5-tool-call
   budget with Bearer propagation; and never logging a raw prompt (only `llm.prompt_hash`).
6. **`check: []` — deliberately empty, with a comment saying why.** The schema is explicit: *"Take them
   verbatim from the Makefile / package.json — do not invent a command that does not exist in the
   repo."* `make check` is documented in `AGENTS.md` and implemented nowhere. Writing it in makes every
   gate fail with `make: command not found` and teaches the team to ignore the gate.
   **This is the single most important honesty test of the whole install.**
7. **`exclude: [ml/agent/]` on `ml`** — without it, `ml/agent/tools/*.py` has two owners and
   `DomainFor` picks by longest prefix; with a nested domain declared, the exclude makes the ownership
   explicit rather than incidental.

**Known gap, no clean answer:** files at the **repo root** (`Makefile`, `docker-compose.yml`,
`.github/workflows/*`, `infra/`) belong to no domain. `DomainFor` requires `strings.HasPrefix(rel, path+"/")`,
so `path: .` matches nothing. US-001…US-005 are almost entirely root-level work. Write-set *claims*
still fence those files correctly (`writesets` is keyed by path, not by domain), but the fan-out has
no declared axis for them and their nodes get no domain in the run record. Options: declare
`platform: { path: infra/ }` and accept root files being domainless, or create the directories the
plan already names. Decide before US-001, not during it.

### A.6 `gates`

```yaml
gates:
  qa:
    checks: []                                   # nothing to run yet — and doctor must SAY so
    skills: [portal-security-audit, portal-code-review]
    verdict: required
    evidence: required
```

- `verdict: required` without `evidence: required` is rejected at load — it would permit exactly the
  failure the gate exists to prevent.
- The two gate skills exist in the repo and match `security-reviewer`'s remit.
- **`checks: []` must produce a loud warning at both ends,** and it does. Observed at the commit:
  `batten: gate "qa" declares no checks, so US-015 was approved on the agent's word alone — nothing
  was run to verify it.`
- **Failure mode:** filling `checks` with the aspirational `make check` → red gate on every unit;
  leaving it empty *after* the Makefile lands → a permanently toothless gate that still prints ✓.

### A.7 `provenance`

```yaml
provenance:
  format: '{id} @ {git_sha7}'
```

No MLflow, no DVC in this stack — inventing `{mlflow_run}` here would guarantee a wrong or dashed
value on every close. Extend only if US-035's evaluation harness produces a real run id.

### A.8 `budget`

```yaml
budget:
  tokens_per_run: 3_000_000
  imputed_usd_per_run: 8.00
  quota_pct_per_run: 15
  max_iterations: 3
  on_exceed: warn      # -> block when enforcement flips to enforce
```

`quota_pct_per_run` is only enforceable with the statusline installed; doctor says so (observed).
`max_iterations: 3` is the tripwire for `/batten-night`; a course project with a fixed sprint budget
(< $45/month, a `finops-auditor` agent) is exactly the case where an unattended run grinding on a red
test until the 5-hour window is gone is a real cost.

### A.9 `capabilities`

```yaml
capabilities:
  graph:    { provider: graphify, query_before_read: true, lessons: false }
  memory:   { provider: engram }
  obsidian: { vault: <absolute path to the team vault>, export: [runs, verdicts, canvas] }
```

- Declaring `graph: graphify` obliges someone to actually build `graphify-out/graph.json`; doctor
  otherwise nags every run (observed: `⚠ no graphify-out/graph.json yet`). In a repo with **zero code**
  a code graph answers nothing — either build it after US-001 lands code, or set `provider: none`
  until then. A declared-and-empty capability is noise.
- **`export:` is inert.** `Capabilities.Obsidian.Export` is parsed in `internal/spec/spec.go:168` and
  **read by no code path** (`grep -rn "Obsidian.Export" --include=*.go` → no hits outside the struct).
  Whatever you list, `export.Run` writes the note, the canvas and the three dashboards whenever
  `vault` is non-empty. Confirmed by the run below: `export: [runs, verdicts, canvas]` and there is no
  separate verdict file anywhere in the vault — the verdict is a **section of the run note**.
- **Failure mode:** a vault path under the user's OneDrive-synced Obsidian folder means every hook
  writes into a synced directory; `writeIfChanged` mitigates the churn but does not eliminate it.

---

## B. The run record in SQLite

**Path:** `$BATTEN_DB`, else `~/.batten/batten.db`. Schema version 3, `SetMaxOpenConns(1)` + WAL.
**This is the canonical artifact.** Every other artifact in this document is a regenerable projection
of it, and nothing in batten ever reads a projection back.

For one closed unit (`US-015`, a real fan-out over backend + frontend), the record must contain:

| Table | Expected rows | Required content | Failure mode |
|---|---|---|---|
| `runs` | exactly **1** open row per unit | `project=proyecto_ui`, `unit_id=US-015`, `session_id` bound, `phase` = the current phase, `status running→ok`, **`base_sha` non-empty** (the anchor), `started_at`, `ended_at` on close | `base_sha` empty → every later phase diffs from `HEAD~N` and reviews the wrong files. Two open runs for one unit → the statusline and the guard both have to guess |
| `nodes` | 1 per phase entered + **1 per subagent** | `kind` ∈ phase\|subagent\|gate, `label`, **`domain`**, `status`, `agent_id`, `agent_type`, `attempt` | `domain` empty → the run note's Fan-out table, the canvas node body and the note's `domains:` frontmatter all lose the fan-out axis. **This happens here** — see §J.2 |
| `edges` | 1 `spawn` per subagent; `retry_of` per re-run; `depends_on` for sequential sub-tasks | typed relations, `PRIMARY KEY (src,dst,rel)` | only `spawn` recorded → the canvas draws the plan's shape, not the path the work actually took, and the retries the graph exists to show are invisible |
| `writesets` | 1 per claimed file, `PRIMARY KEY (run_id, path)` | repo-relative, slash-normalised, case-folded on Windows/macOS | the PK **is** the disjointness guarantee. Observed: second claim of `backend/app/core/auth.py` → `batten: write-set collision: ... already owned by n-a-be-1`, exit 1 |
| `verdicts` | ≥1 per gate pass | `gate`, `check_id`, `result`, **`evidence_json` non-`[]` when `result=ok`**, `why`, `safe_next_step`, `source` (`agent` vs `batten`) | `ok` + `[]` is refused *before* insert (`ErrNoEvidence`). `source` distinguishes "I verified it" from "it was verified" — with `checks: []` every verdict here is `agent`, which is precisely why doctor must keep shouting |
| `events` | append-only, one per hook | `hook`, `run_id`, `ts`, `payload` — the replay log | observed: `PreToolUse ×4, SubagentStart ×2` |
| `usage` | 1 per (request, run) after transcript ingest | all five token buckets, `model`, `imputed_usd`; `PRIMARY KEY (request_id, run_id)` makes re-ingest idempotent | no rows → the note says *"Usage not measured … Not zero — unknown"*, which is correct. Rows counting only the parent → a fan-out undercounted by ~70 % |
| `quota_snapshots` | 1 per statusline render | `five_hour_pct`, `five_hour_reset` | none → `quota_pct_per_run` is unenforceable, and `batten budget` must print `NOT MEASURABLE`, never `0%` |
| `budget_ledger` | per node spend | `delta_usd`, `tokens_in/out` | — |
| `overrides` | **0 in the happy path** | `run_id`, `gate`, **`reason` (mandatory)**, `ts` | a non-empty overrides table with a full backlog behind it is the signal the gate is theatre |

---

## C. The phase artifacts declared under `artifacts:`

### C.1 `docs/us-planning/{id}.md` — written by `/batten-plan`

Must contain, in this order:

1. **The acceptance criteria copied verbatim** from the `### US-0NN —` block of
   `context/planeacion_proyecto.md`, each rewritten until it is *verifiable*. This repo's criteria are
   already unusually good ("`Hit Rate@3 >= 0.8` sobre el set de 20 consultas", "< 500 ms en catálogo
   durante export 1M filas") — those pass. "Revelación progresiva" (US-026) does not, and the plan
   must say which criteria it could not make checkable.
2. Architecture and the **exact files** to create or modify.
3. Public interfaces (signatures) of new modules, so domains build against contracts, not each other.
4. **The fan-out table: domain → sub-task → exact write-set → sequential/parallel → resource budget.**
   This is the section the build phase executes and the guard enforces.
5. What is reused, **by path**.
6. Test plan honouring `coverage` (70 backend / 50 frontend).
7. Risks.

**Failure mode:** no write-sets → `/batten-build` must refuse to launch. A write-set overlap between
two sub-tasks is a *planning bug*: the fix is to merge or sequence them, never to open the fence.

### C.2 `docs/us-handoff/{id}.md` — written by `/batten-build`

Per agent: its write-set, its check results, what it reused; cross-domain seams resolved at
integration (for this stack: the `SemanticQuery` Pydantic contract shared by `backend/` and
`ml/semantic/`, and the SSE event names `tool_call|token|error|done` shared by `backend/app/api/chat`
and `frontend/app/composables/useChatStream`); resource contention; **every write-set collision and the
planning bug behind it**; `git status --short`. No commit.

**Failure mode:** a handoff that reports success per agent without the seams is how two agents ship
plausibly-disagreeing halves of one contract.

### C.3 `docs/manual-test/{id}.md` — written by `/batten-verify`

Every criterion that could not be checked by running something, as a numbered reproduction script for
a human. For this repo: SUS sessions, Figma parity, the 1M-point ECharts render, real cancellation of
a streaming answer.

**Failure mode:** absent → unverifiable criteria get silently folded into an `ok`. That is the exact
failure batten exists to kill, arriving through the side door.

### C.4 `docs/us-resolved/{id}.md` — written by `/batten-close`

What was done; the **acceptance-criteria table with the evidence carried over from the verdict** (not
re-asserted from memory); standards actually met (linters, coverage per domain against its floor, the
invariants); and **the provenance line filled exactly** — `US-015 @ 8f2a1c9`, `-` for anything that
does not apply. A guessed provenance value is worse than none.

---

## D. The verdict envelope

**Path:** transient JSON → `batten verdict --unit <id> --gate qa --file verdict.json` → `verdicts` row.

```json
{
  "check_id": "US-015-qa",
  "result": "ok",
  "evidence": [
    "AC-1 (POST /api/auth/token devuelve JWT HS256 con scopes del rol): verificado en backend/app/api/auth.py:41",
    "AC-2 (401 sin token con WWW-Authenticate: Bearer, 403 sin scope): tests/backend/test_auth.py 12 passed, 0 failed",
    "gate skill portal-security-audit: sin hallazgos de secretos hardcodeados en el diff de US-015"
  ],
  "why": "cada criterio de aceptacion verificado contra el diff desde el anchor 44630cd",
  "safe_next_step": "close",
  "requires_confirmation": false
}
```

Required:

- **One evidence item per acceptance criterion**, each re-runnable by a reviewer who does not trust
  you. `looks good`, `tests pass`, `implemented per the plan` are not evidence.
- `result` defaults to `blocked` under uncertainty. A criterion you *could not* check never counts
  toward an `ok`.
- Scope is the diff **from the anchor**, never `HEAD~N`.

**Failure modes (both verified working):**

- `ok` + `evidence: []` → refused by the binary before the DB:
  `batten: result "ok" with an empty evidence[] is not allowed: an approval must cite something…` (exit 1).
- No verdict at all + `git commit` → the `PreToolUse` gate fires naming the phase to run.
- **Caveat that matters for this repo:** with `gates.qa.checks: []`, every `evidence[]` item is the
  agent's own prose (`source=agent`). The envelope is well-formed and the gate is still, in substance,
  self-certification — which is why `checks` must be filled the moment the Makefile exists.

---

## E. The Obsidian run note

**Path:** `<vault>/batten/proyecto_ui/runs/US-015.md`. Regenerated after each verdict, on `Stop`, and
on `batten canvas`. `writeIfChanged` suppresses no-op rewrites so Obsidian does not reindex for nothing.

Required frontmatter (typed — the `.base` dashboards query it):

```yaml
unit: US-015
project: proyecto_ui
status: ok          # running|ok|blocked|failed
phase: build
verdict: ok         # ok|warn|blocked|none  — never omitted
evidence_count: 3
base_sha: 44630cd
started: 2026-07-28T06:03:38Z
ended:   2026-07-28T06:06:14Z
domains: [backend, frontend]
tokens: <omitted when not measured>
imputed_usd: <omitted when not measured>
```

- `verdict: none` must be a **real value**, not a missing key — the blocked-verdicts dashboard selects on it.
- `tokens`/`imputed_usd` must be **absent** when unmeasured. `0` would be a number batten invented.

Required body: `# US-015` header line (status · phase · base · run id); the cost line in one of its
three honest forms (measured / *"not priced"* / *"not measured … Not zero — unknown"*); a **Verdict**
section that leads with a `> [!danger]` callout when there is no verdict or when `ok` has empty
evidence; the **Fan-out** table (agent | domain | status | write-set | tokens | imputed); **Files
touched**; **Relations** (every non-`spawn` edge — `retry_of`, `rollback`, `depends_on`); the embedded
canvas `![[US-015.canvas]]`; **Neighbours** (`[[US-014]]`, emitted only if that note actually exists);
and the footer stating SQLite is canonical.

**Failure modes:** a missing `verdict` property silently empties a dashboard; `tokens: 0` for an
un-ingested run is a fabricated number; a wikilink to a note that does not exist plants a phantom node
in Obsidian's graph view and reads as a fact.

---

## F. The `.base` dashboards and the index note

**Paths:** `<vault>/batten/proyecto_ui/{open-runs,blocked-verdicts,imputed-cost-by-unit}.base` and the
front-door note `<vault>/batten/proyecto_ui/proyecto_ui.md`.

| File | Required filter | Why it is right |
|---|---|---|
| `open-runs.base` | `project == "proyecto_ui"` **and** `status == "running"` | one vault can hold several projects; never show someone else's runs |
| `blocked-verdicts.base` | project **and** (`verdict == "blocked"` **or** `verdict == "none"`), `groupBy: verdict` | `blocked` and `none` are the same operational fact: the close gate will deny the commit |
| `imputed-cost-by-unit.base` | project **and** `imputed_usd > 0`, sorted DESC | an un-ingested run has no `imputed_usd` property and must stay **out** of the cost table — unmeasured is not free |

Each carries the header comment stating the rows are **not readable by scripts** (Obsidian forum
#104089) and that SQLite is canonical. The index note must explain the two easily-misread columns —
"Tokens are exact and include every subagent" and "**Imputed $ is not a bill**" — because a reader who
misses that acts on the wrong number, and they will not open a README to find out.

**Failure mode:** a dashboard without the project filter shows another team's runs; a cost table
without `imputed_usd > 0` shows every un-ingested run as `$0.00`.

---

## G. The JSON Canvas

**Path:** `<vault>/batten/proyecto_ui/runs/US-015.canvas`, JSON Canvas 1.0. With no vault it falls back
to `.batten/US-015.canvas` in the repo.

Required: one `group` per phase (labelled, coloured by phase status); one `text` node per phase and per
subagent (subagent body = `**label**` / `domain: x` / `` `status` `` / cost when priced); every edge
from the store, `spawn` unlabelled (obvious from the layout) and `retry_of`/`rollback`/`depends_on`
labelled and colour-coded (orange/red/yellow); a **verdict node** carrying the result, the `why` and
the evidence list — or `_no evidence_ — an approval must cite something`; a **header node** with unit,
run id, status, phase, tokens/imputed and base SHA. Orphan nodes get an `unattributed` column so
nothing is silently dropped. Observed: `wrote …/US-015.canvas (8 nodes, 2 edges)`.

**Failure modes:** a canvas that only ever shows a straight line means the store never recorded the
retries — the graph's whole claim is that it draws the path the work *actually* took. And see §J.6:
the first subagent node currently **overlaps** its phase node.

---

## H. The statusline

**Paths:** `proyecto_ui/.claude/settings.json` →
`"statusLine": {"type": "command", "command": "<abs path>/batten.exe statusline"}` (every other key
preserved; a displaced statusLine is chained into a sidecar), plus one `quota_snapshots` row per render.

Required line, verified verbatim:

```
batten US-UX-03 plan · no verdict: commit DENIED · quota 0.0/15% · 5h 85% (5h49m)
```

Rules: unit + phase always; the verdict segment is the most useful thing it can say; spend is omitted
entirely when tokens are 0 (unmeasured ≠ free); the 5h window only earns space above 80 %.

**Failure modes:** not installed → `quota_pct_per_run` is silently unenforced (doctor must warn, and
does); a statusline that prints `5h 0%` when `rate_limits` is absent would be an invented number.
**And see §J.5: it prints `commit DENIED` while `enforcement: report` means the hook only warns.**

---

## I. What `batten doctor` must report for **this** repo

Expected output, with the correct spec in place. Lines marked ✔ were observed verbatim.

```
✓ <abs>/batten.yaml — project "proyecto_ui", unit "US", 7 phases, 6 domains          ✔
✓ close gate: phase "close" requires verdict "ok" on any gate                        ✔
⚠ gate "qa" declares no checks — it verifies NOTHING and approves on the agent's
  word. Add gates.qa.checks (take them verbatim from your build files).              ✔
● enforcement: REPORT — gates WARN, they do not block yet.                           ✔
✓ store: <sandbox>/state/batten.db                                                   ✔
✓ graph: graphify (on PATH)                                                          ✔
  ⚠ no graphify-out/graph.json yet — run: graphify . --code-only                     ✔
· memory: engram (via MCP; batten does not store episodic memory)                    ✔
✓ obsidian vault: <path>                                                             ✔
✓ statusline installed — the quota ceiling is enforced                               ✔
```

Plus, when applicable: `⚠ domain "x": rules file missing`, `⚠ <where> references "<skill>", which is
not installed (did you mean …?)`, and `⚠ N run(s) open >48h with no activity`.

**The three lines that matter most here** are the `checks`/`enforcement`/graph warnings: together they
say, correctly, *"this gate currently verifies nothing, it is not blocking, and the graph you declared
does not exist yet."* That is an accurate description of `proyecto_ui` on day one.

**What doctor does not check, and should:** that `unit.plan` exists; that `unit.locator` matches at
least one heading in it; that every `artifacts` directory is writable; that `unit.pattern` matches the
ids actually present in the plan doc (which would have caught the missing `US-UX-NN` immediately).

---

## J. Deltas found while grounding this plan

Every item below was reproduced against the prebuilt `batten.exe 0.1.0`; command and output are exact.

| # | Kind | What |
|---|---|---|
| J.1 | **stale binary** | `batten init` on the replica derives `unit: TASK / TASK-\d+` and omits `unit.plan`/`unit.locator`, despite the scan finding `context/planeacion_proyecto.md`. Reproduced on four minimal repos (`### US-001 — A` ×4 in `context/*plan*.md` or `README.md`) → always `TASK-\d+`. **This is a stale-binary artifact, not a live defect:** the testbed binary is dated 23:52 and commit `3122d29` *"fix: init reads the backlog for the unit, not just branch names"* landed at 00:04. I did not rebuild, so the fix is unverified by me. **Note the fix would still yield `US-\d{3}`, which misses the 8 `US-UX-NN` stories** — the widened pattern in §A.2 is a human decision no heuristic makes. |
| J.2 | **broken** | A subagent launched as its domain's declared custom agent loses its domain. `SubagentStart` sets `domain` only when `agent_type` is a **domain key**; `/batten-build` §4 instructs using `domains[].agent`. Observed rows: `('n-a-be-1','subagent','backend-engineer','', …)` vs `('n-a-fe-1','subagent','frontend','frontend', …)`. In `proyecto_ui` **all six domains declare `agent:`**, so every fanned-out node would be domainless: run-note Fan-out shows `—`, frontmatter `domains:` is wrong (observed: `domains: [frontend]` for a two-domain run), canvas bodies drop the domain line. |
| J.3 | **broken (display)** | Write-set paths are case-folded on Windows/macOS **and stored folded**, so they are displayed folded. `batten claim a-fe-1 frontend/app/composables/useSession.ts` → note and guard both print `frontend/app/composables/usesession.ts`, a path that does not exist on a case-sensitive checkout. The folding is correct for *comparison*; the storage of the folded form for *display* is not. A Nuxt repo is all camelCase composables and PascalCase components. |
| J.4 | **missing** | `capabilities.obsidian.export` is parsed (`spec.go:168`) and read nowhere. `export: [runs]` still writes canvases and dashboards; `export: [verdicts]` produces no verdict file at all (verdicts live inside the run note). Documented in the schema as three selectable outputs; it is a no-op. |
| J.5 | **confusing** | Under `enforcement: report` the statusline prints `no verdict: commit DENIED` while the hook returns `batten (warning — not blocking)`. `verdictSeg` never consults `sp.ReportOnly()`. `proyecto_ui` is expected to spend its whole adoption ramp in report mode, so the statusline would misreport for weeks. |
| J.6 | **broken (layout)** | In the emitted canvas the phase node sits at `y:0,h:120` and its first subagent at `y:60,h:120` with the same `x` — a 60px overlap in Obsidian (`padTop = 60`, `nodeH = 120`). Verified in `US-015.canvas`: `p-build {x:420,y:0}` / `n-a-be-1 {x:420,y:60}`. |
| J.7 | **missing** | `doctor` validates `domains[].rules`, skills and agents, but never `unit.plan`, `unit.locator` or the `artifacts` directories. |
| J.8 | **works** | Verified end-to-end and worth recording as clean: the anchor (`base SHA = 44630cd`), the claim collision (`already owned by n-a-be-1`, exit 1), the write-set guard message (names the file, the owner, your own write-set, and refuses to offer a way through), the empty-evidence rejection, the commit gate before/after a verdict, the `US-UX-NN` pattern, vault + bases + index + canvas emission, `close` releasing claims, `budget` printing `NOT MEASURABLE` for quota, and the statusline's quota baseline (`quota_start_5h = 85.0`). |

---

## K. ACCEPTANCE CRITERIA — "batten works on `proyecto_ui`"

Checkable, in order. Each is pass/fail from a command and its output.

**The spec**

1. `batten.yaml` exists at the repo root, loads without error, and `batten doctor` reports
   `project "proyecto_ui", unit "US", 7 phases, 6 domains`.
2. `unit.pattern` matches **all 44** backlog ids: `US-001…US-036` **and** `US-UX-01…US-UX-08`.
   Check: `batten phase US-036 plan` and `batten phase US-UX-08 plan` both open a run.
3. `unit.plan` points at `context/planeacion_proyecto.md` and `unit.locator` locates a real
   `### US-0NN —` heading for a randomly chosen unit.
4. All four `artifacts` paths contain `{id}` and resolve under `docs/`.
5. Exactly one phase carries `requires_verdict`; `build` carries `anchor: git_sha`; `verify` carries
   `diff_from: anchor`.
6. All six domains name an existing `rules` file, an existing `agent:` from `.claude/agents/`, and
   only skills that exist — `batten doctor` emits **zero** `references "…", which is not installed` lines.
7. `domains[].skills` match the "Skills relevantes" table of each domain's own `AGENTS.md`; **no
   plugin skill** (`batten-engine`, `ui-ux-pro-max`) appears in any domain.
8. Every domain's `invariants` are non-empty and copied **verbatim** from its NON-NEGOTIABLE section;
   `backend.coverage: 70` and `frontend.coverage: 50`.
9. `check:` and `gates.qa.checks` are **empty** while no build file exists, and `batten doctor` says so
   out loud. After US-001/US-002 land the Makefile, they are filled **verbatim** from it and
   `enforcement` flips to `enforce` in the same commit.

**The two denials**

10. With no verdict, `git commit` is **denied** (`enforce`) or **warned** (`report`), naming the unit
    and the phase to run: `batten: US-0NN has no verdict envelope. Run the "verify" phase before committing.`
11. `{"result":"ok","evidence":[]}` is **rejected by the binary** with `ErrNoEvidence`, exit ≠ 0, and
    no row lands in `verdicts`.
12. A second agent claiming or editing a file already claimed is **refused**, and the message names the
    file, the owning node, the offender's own write-set, and offers **no way through**.
13. After a valid verdict (`result: ok`, ≥1 evidence item per acceptance criterion), the same
    `git commit` is allowed.

**The run record**

14. One `runs` row per unit with a non-empty `base_sha`, bound to a session, transitioning
    `running → ok` at close.
15. One `nodes` row per phase and per subagent, **each subagent row carrying its `domain`** — including
    when it was launched as the domain's custom `agent:` (currently fails, §J.2).
16. `writesets` holds one row per claimed file, disjoint by `PRIMARY KEY (run_id, path)`, and the stored
    paths reproduce the repo's real casing (currently fails, §J.3).
17. `edges` contains a `spawn` per subagent and a `retry_of` for every re-run, so the graph shows the
    path actually taken.

**The projections**

18. `<vault>/batten/proyecto_ui/runs/{id}.md` exists with complete typed frontmatter; `verdict` is
    always present (`none` when absent); `tokens`/`imputed_usd` are **omitted**, never `0`, when
    unmeasured; `domains:` lists every domain that ran.
19. The three `.base` files and the index note exist, each filtered to `project == "proyecto_ui"`, with
    the "not a bill" and "not script-readable" warnings intact.
20. `{id}.canvas` is valid JSON Canvas 1.0, opens in Obsidian, contains one group per phase, a verdict
    node and a header node, **with no overlapping nodes** (currently fails, §J.6).
21. `batten canvas <unit>` and the `Stop` hook both regenerate note + canvas + dashboards idempotently
    (byte-identical on a second run).

**Budget, statusline, diagnosis**

22. `batten statusline --install` writes only the `statusLine` key into `.claude/settings.json`,
    preserving the existing `permissions`/`enabledPlugins`, and each render appends a `quota_snapshots` row.
23. `batten budget` prints all three ceilings and prints `NOT MEASURABLE` — never `0%` — for any it
    cannot measure.
24. The statusline's verdict segment agrees with what the hook will actually do under the current
    `enforcement` mode (currently fails, §J.5).
25. `batten doctor` is clean apart from the deliberate `checks: []` warning, and every warning it does
    print is actionable and true of this repo.

**End to end**

26. One real unit — `US-015` (Auth JWT: backend + db + frontend, three domains, disjoint write-sets) —
    goes `plan → build → verify → close` producing: 4 artifacts under `docs/`, 1 `runs` row, ≥5 `nodes`,
    ≥3 `edges`, ≥8 `writesets`, 1 `verdict` with ≥3 evidence items, 1 run note, 1 canvas, 3 dashboards,
    1 index note, 1 commit stamped `US-015 @ <sha7>` — **and zero rows in `overrides`.**
27. One UX unit — `US-UX-03` — goes through the same machine, ends `blocked` or `warn` on the criteria
    that need a human, and writes them into `docs/manual-test/US-UX-03.md` instead of rounding them up
    to `ok`.
28. Nothing batten wrote touched anything outside the repo and the declared vault.
```

**Sandbox (all writes confined here):** `$SANDBOX\expected-plan-sandbox\` — containing `proyecto_ui/` (working copy + the validated `batten.yaml`), `init-draft-batten.yaml` (what `batten init` produced, for comparison), `state/batten.db`, `vault/batten/proyecto_ui/` (run note, canvas, 3 `.base` files, index note), `scan.json`, and `mini/ mini2/ m3/ m4/ m5/` (the minimal repros for §J.1). The user's real repo, vault and `~/.batten/batten.db` were never written to; every `batten` invocation had `BATTEN_DB` exported into the sandbox, confirmed by `batten doctor`'s own `✓ store:` line.