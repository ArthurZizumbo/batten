# batten — Test Matrix (design artifact, nothing run)

> Source of truth read for this design: `README.md`, `ROADMAP.md`, `DESIGN.md`, `batten.schema.json`,
> `cmd/batten/main.go` (`printUsage` + every `cmd*` func), `internal/hooks/hooks.go`,
> `internal/store/store.go`, `internal/spec/spec.go`, `internal/usage/usage.go`,
> `internal/statusline/statusline.go`, `internal/mcp/mcp.go`, `internal/export/export.go`,
> `plugin/claude-code/hooks/hooks.json`, `plugin/claude-code/commands/*.md`.
> **Nothing below has been executed.** Every "expected" is what the docs + code claim, stated so a run can contradict it.

---

## 0. Legend

| mark | meaning |
|---|---|
| **CLI** | Fully drivable from a shell with the prebuilt binary. Proves the behaviour. |
| **CLI\*** | Drivable, but only with a **synthetic** hook payload / synthetic transcript. Proves *batten's logic*; does **not** prove Claude Code ever sends that payload. The claim "this fires in real life" stays unproven. |
| **LIVE** | Requires a live Claude Code session: real hooks firing, real subagents, a real MCP client, a real subscription quota. Untestable from the CLI. |
| **SURGERY** | Needs direct DB or clock manipulation; no CLI path exists. |
| **TTY** | Needs an interactive terminal (Bubbletea). |

Cell ids: `<ROW>-<H|D|A|E><n>` — **H**appy, **D**egraded (capability absent), **A**dversarial (the rule being violated), **E**dge (empty / unmeasurable / first-run).

---

## 1. Harness, and the isolation rules this matrix obeys

```bash
SBX='$SANDBOX/batten-testbed'
BATTEN="$SBX/batten.exe"
REPO="$SBX/proyecto_ui"          # the isolated replica; never $HOME/Proyectos/MNA/proyecto_ui
VAULT="$SBX/vault"               # never the real OneDrive vault
FX="$SBX/fx"                     # transcript + payload fixtures
export BATTEN_DB="$SBX/state/CELL.db"   # <-- per cell; see below
```

Non-negotiables baked into every command in this matrix:

1. **`BATTEN_DB` is exported before every single invocation.** `dbPath()` (`main.go:128`) falls back to `~/.batten/batten.db` — the user's real DB — the moment it is unset. A cell that forgets it is not a failed test, it is a contaminated machine.
2. **One DB file per cell** (`state/VG-A1.db`, …). Runs are namespaced by `spec.project`, so two cells sharing a DB *and* a project name cross-talk; a cell that must be first-run needs a virgin file, not a virgin directory.
3. **`batten hook-debug --tap` is NOT redirectable.** `tapPath()` (`main.go:858`) is `os.UserHomeDir()/.batten/hook-taps.jsonl` and ignores `BATTEN_DB`. Any tap cell must additionally set `USERPROFILE`/`HOME` into the sandbox, or it writes into the user's real `~/.batten`. Cells that need the tap are marked with ⚠.
4. `git push` is never run, no remote is ever added, `$REPO` is a throwaway clone.
5. Vault cells point `capabilities.obsidian.vault` at `$VAULT` only.

### 1.1 The single most important methodological rule

**`batten hook` prints nothing and exits 0 for at least six different reasons:** allow, no `batten.yaml` found, store open failure, malformed stdin, a recovered panic, and an unknown event name (`main.go:157-191`, `hooks.go:159-194`). Silence is therefore **not evidence of ALLOW** — it is evidence of *nothing*.

> **Every cell whose expected result is ALLOW must be run with a paired positive control**: the same payload with exactly one field changed such that a DENY is mandatory. If the control also prints nothing, the hook is dead (wrong cwd, unloadable spec, DB error) and the "PASS" was a plausible-looking FAIL. This pairing is written into every ALLOW cell below as *(control: …)*.

Second-order discriminators used throughout:

| signal | what it proves |
|---|---|
| stdout `"permissionDecision":"deny"` | hard denial, `enforcement: enforce` |
| stdout `"systemMessage"` + `additionalContext`, **no** `permissionDecision` | `advise()` — report mode, or a deliberate fail-open |
| empty stdout **+ a control that denies** | genuine ALLOW |
| empty stdout **+ a control that is also silent** | hook never engaged — inconclusive, not PASS |
| exit code | **useless for `hook` and `doctor`** (both return nil on nearly every internal error); meaningful for `verdict`, `claim`, `check`, `close`, `override`, `show`, `canvas` |

---

## 2. Fixtures

### 2.1 Specs (written to `$REPO/batten.yaml`, one at a time)

`$REPO` is real: `AGENTS.md` at root and in `backend/ db/ frontend/ ml/`, `context/planeacion_proyecto.md` with 36 `### US-0NN` headers **and 8 `### US-UX-0N` headers**, `.claude/agents/*.md` (9), `.claude/skills/*` (47), **no Makefile, no package.json, no pyproject** — i.e. no check command exists in the repo. Checks below therefore use `git` invocations, which are deterministic and present.

**S-full** — the reference spec (enforce, gate with real checks, all ceilings, vault):

```yaml
version: 1
project: proyecto-ui-test
enforcement: enforce
unit: { name: US, pattern: 'US-\d{3}', plan: context/planeacion_proyecto.md, locator: '### {id}' }
artifacts:
  planning: docs/us-planning/{id}.md
  resolved: docs/us-resolved/{id}.md
phases:
  - { id: plan,   graph_query: true }
  - { id: build,  fanout: true, reads: [planning], anchor: git_sha }
  - { id: verify, gate: qa, diff_from: anchor }
  - { id: fix,    interactive: true }
  - { id: close,  requires_verdict: ok }
domains:
  backend:  { path: backend/,  rules: backend/AGENTS.md,  check: ['git --version'], agent: backend-engineer,  invariants: ['session_id in every query'] }
  frontend: { path: frontend/, rules: frontend/AGENTS.md, check: ['git --version'], agent: frontend-engineer }
  db:       { path: db/,       rules: db/AGENTS.md }
  ml:       { path: ml/,       rules: ml/AGENTS.md,       agent: data-engineer }
gates:
  qa: { checks: ['git --version', 'git rev-parse --verify HEAD'], verdict: required, evidence: required }
budget: { tokens_per_run: 3_000_000, imputed_usd_per_run: 8.00, quota_pct_per_run: 15, max_iterations: 3, on_exceed: block }
capabilities:
  obsidian: { vault: <$VAULT>, export: [runs, verdicts, canvas] }
```

| variant | delta from S-full | exists to exercise |
|---|---|---|
| **S-report** | `enforcement: report` | report-mode ramp |
| **S-nochecks** | `gates.qa.checks` removed | "gate verifies nothing" fail-open-out-loud |
| **S-nogate** | `close` phase drops `requires_verdict` | spec that does not gate |
| **S-nobudget** | `budget:` removed | ceilings absent |
| **S-novault** | `capabilities:` removed | vault absent |
| **S-min** | `version/project/unit/phases:[{id: build}]` only | first-run / minimal |
| **S-graph** | `capabilities.graph: {provider: graphify, query_before_read: true}` | declared-and-absent capability |
| **S-ghost** | `domains.backend.skills: ['no-such-skill']`, `domains.ml.agent: 'no-such-agent'`, `rules: ml/MISSING.md` | discovery validation |
| **S-bad** | `version: 2`; `pattern: 'US-(\d{3}'`; artifact `docs/x.md` (no `{id}`); `phase verify gate: nope`; `gates.qa {verdict: required}` with no `evidence`; `domains.ml.resources: [gpu]` with no `resources:`; `models.phases: {ship: opus}` | every loader rejection at once |

### 2.2 Hook payload templates (`$FX/*.json`) — fields from `hooks.Input` (`hooks.go:31-51`)

```jsonc
// H-commit : PreToolUse / Bash
{"session_id":"sessA","cwd":"<REPO>","hook_event_name":"PreToolUse","tool_name":"Bash",
 "tool_input":{"command":"git commit -m \"feat: US-034\""},"transcript_path":"<FX>/sessA.jsonl"}

// H-write  : PreToolUse / Edit, attributed to a subagent
{"session_id":"sessA","cwd":"<REPO>","hook_event_name":"PreToolUse","tool_name":"Edit",
 "agent_id":"a1","tool_input":{"file_path":"<REPO>/ml/train.py"}}

// H-write-anon : same, no agent_id  (the unattributed case)
// H-substart   : {"session_id":"sessA","cwd":"<REPO>","hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"ml"}
// H-substop    : {... "SubagentStop","agent_id":"a1","last_assistant_message":"done","transcript_path":"<FX>/sessA.jsonl"}
// H-sessionstart:{"session_id":"sessA","cwd":"<REPO>","hook_event_name":"SessionStart","source":"startup"}
// H-post-phase : PostToolUse/Bash, tool_input.command = "batten phase US-034 build"
// H-post-commit: PostToolUse/Bash, tool_input.command = "git commit -m x"
// H-stop       : {"session_id":"sessA","cwd":"<REPO>","hook_event_name":"Stop","transcript_path":"<FX>/sessA.jsonl"}
```

Invocation form used everywhere: `"$BATTEN" hook PreToolUse < "$FX/H-commit.json"`.

### 2.3 Transcript fixture (layout from `usage.go:276-320`)

```
$FX/sessA.jsonl                          # parent
$FX/sessA/subagents/agent-a1.jsonl       # subagent a1  (agentID comes from the FILENAME)
```
Each line must be `{"type":"assistant","requestId":"…","timestamp":"2026-07-27T10:00:00Z","message":{"id":"…","model":"claude-opus-4-5-20250929","usage":{"input_tokens":N,"output_tokens":N,"cache_read_input_tokens":N,"cache_creation":{"ephemeral_5m_input_tokens":N,"ephemeral_1h_input_tokens":N}}}}`.
Variants: **T-dup** (same `requestId` twice in the parent), **T-unpriced** (`"model":"claude-zeta-9"`), **T-over** (2.5M parent + 1.5M subagent tokens = over the 3M ceiling), **T-garbage** (half the lines are not JSON).

### 2.4 Verdict envelopes (`$FX/*.json`)

- **V-ok** — `{"check_id":"US-034-qa","result":"ok","evidence":["git --version: git version 2.x","criterion 1 verified at backend/api.py:41"],"why":"…","safe_next_step":"…","requires_confirmation":false}`
- **V-empty** — same, `"evidence":[]`
- **V-blocked** — `"result":"blocked"`, evidence non-empty
- **V-forged** — V-ok plus `"source":"batten"` (the field is `json:"-"` in `store.Verdict`, so this must not stick)
- **V-shout** — `"result":"OK"` (case), empty evidence

---

## 3. The matrix

Cells marked with the testability class. Detail for each id is in §4.

| # | Behaviour claimed | Happy path | Degraded (capability absent) | Adversarial (rule violated) | Edge (empty / unmeasurable / first-run) |
|---|---|---|---|---|---|
| 1 | **Verdict gate** — commit denied without an evidenced verdict | `VG-H1` CLI\*, `VG-H2` CLI\* | `VG-D1` `VG-D2` `VG-D3` CLI\* | `VG-A1`…`VG-A7` CLI\* | `VG-E1`…`VG-E4` CLI\* |
| 2 | **Evidence rule in the binary** — `ok` + `evidence:[]` refused before the DB | `EV-H1` CLI | `EV-D1` CLI | `EV-A1` `EV-A2` `EV-A3` CLI | `EV-E1` `EV-E2` CLI |
| 3 | **Write-set guard, within run** | `WS-H1` CLI\* | `WS-D1` `WS-D2` CLI\* | `WS-A1`…`WS-A4` CLI\* | `WS-E1` `WS-E2` CLI\* |
| 4 | **Write-set guard, across open runs** | `XR-H1` CLI\* | `XR-D1` CLI\* | `XR-A1` CLI\* | `XR-E1` CLI\* |
| 5 | **`batten claim`** | `CL-H1` CLI | `CL-D1` CLI | `CL-A1` CLI | `CL-E1` `CL-E2` CLI |
| 6 | **Phases + the anchor** | `PH-H1` `PH-H2` CLI | `PH-D1` CLI | `PH-A1` `PH-A2` CLI | `PH-E1` `PH-E2` `PH-E3` CLI |
| 7 | **Budget ceilings** (tokens / imputed $ / quota %) | `BG-H1` CLI\* | `BG-D1` `BG-D2` CLI | `BG-A1` `BG-A2` CLI\* | `BG-E1` `BG-E2` `BG-E3` CLI |
| 8 | **Multi-session** (session↔run binding, ambiguity) | `MS-H1` `MS-H2` CLI\* | `MS-D1` CLI\* | `MS-A1` `MS-A2` CLI\* | `MS-E1` `MS-E2` CLI\* |
| 9 | **Vault export** (runs / verdicts) | `VA-H1` `VA-H2` CLI | `VA-D1` `VA-D2` CLI | `VA-A1` CLI | `VA-E1` CLI |
| 10 | **Canvas** (JSON Canvas 1.0) | `CV-H1` CLI | `CV-D1` CLI | `CV-A1` CLI | `CV-E1` `CV-E2` CLI |
| 11 | **TUI** | `TU-H1` TTY | `TU-D1` CLI | — | `TU-E1` TTY |
| 12 | **Statusline** (install + the only quota sensor) | `SL-H1` `SL-H2` CLI, `SL-H3` LIVE | `SL-D1` `SL-D2` CLI | `SL-A1` `SL-A2` CLI | `SL-E1` `SL-E2` `SL-E3` CLI |
| 13 | **MCP surface** (6 tools) | `MC-H1` `MC-H2` CLI | `MC-D1` `MC-D2` CLI | `MC-A1` CLI | `MC-E1` CLI, `MC-E2` LIVE |
| 14 | **`init`** | `IN-H1` `IN-H2` CLI | `IN-D1` CLI | `IN-A1` CLI | `IN-E1` `IN-E2` `IN-E3` CLI |
| 15 | **`doctor`** | `DR-H1` CLI | `DR-D1`…`DR-D4` CLI | `DR-A1` `DR-A2` CLI | `DR-E1` `DR-E2` CLI, `DR-E3` SURGERY |
| 16 | **`check`** (evidence batten generates) | `CK-H1` CLI | `CK-D1` `CK-D2` CLI | `CK-A1` CLI | `CK-E1` `CK-E2` CLI |
| 17 | **`close`** | `CO-H1` CLI | `CO-D1` CLI | `CO-A1` `CO-A2` CLI | `CO-E1` `CO-E2` CLI |
| 18 | **`override`** (audited escape) | `OV-H1` CLI | `OV-D1` CLI | `OV-A1` `OV-A2` CLI | `OV-E1` CLI |
| 19 | **`measure`** | `ME-H1` CLI\* | `ME-D1` CLI | `ME-A1` CLI\* | `ME-E1` `ME-E2` CLI |
| 20 | **Token accounting** (`ingest`, parent + subagents) | `TK-H1` CLI\* | `TK-D1` CLI\* | `TK-A1` `TK-A2` CLI\* | `TK-E1` `TK-E2` CLI\*, `TK-E3` LIVE |
| 21 | **`runs` / `show`** (incl. model-routing divergence) | `RS-H1` `RS-H2` CLI | `RS-D1` CLI | `RS-A1` CLI\* | `RS-E1` `RS-E2` CLI |
| 22 | **`enforcement: report`** ramp | `EM-H1` CLI\* | — | `EM-A1` CLI\* | `EM-E1` CLI\* |
| 23 | **Degrade-never-break invariants** | `DG-H1` CLI\* | `DG-D1` `DG-D2` CLI\* | `DG-A1` `DG-A2` CLI\* | `DG-E1` `DG-E2` CLI\* |
| 24 | **Plugin surface** (hooks.json, bootstrap, Windows exec form, latency) | `PL-H1` CLI, `PL-H2` LIVE | `PL-D1` LIVE | `PL-A1` LIVE | `PL-E1` CLI |
| 25 | **Slash commands** (`/batten-plan|build|verify|close|night|init`) | `SC-*` **all LIVE** | LIVE | LIVE | LIVE |

---

## 4. Cells

### 1. VG — the verdict gate (README's denial #1)

Spec: **S-full** unless noted. Precondition for all: a run exists and is bound — `"$BATTEN" phase US-034 build` then `"$BATTEN" phase US-034 verify`.

**VG-H1 — allowed after a batten-verified `ok`** · CLI\*
```bash
"$BATTEN" check US-034                     # generates the batten-sourced ok
"$BATTEN" hook PreToolUse < "$FX/H-commit.json"
```
*Expect:* empty stdout, exit 0.
*PASS iff:* the paired control denies — delete the verdict (fresh DB, skip `check`) and the same payload must print `permissionDecision:"deny"`. **Plausible FAIL:** silence because `cwd` in the payload does not resolve a `batten.yaml`, or because `BATTEN_DB` points at an empty DB. Both look identical to a pass.

**VG-H2 — the deny message names the phase, the unit, and the escape** · CLI\*
Fresh run, no verdict, then `hook PreToolUse < H-commit.json`.
*Expect (verbatim, `hooks.go:305`):* `batten: US-034 has no verdict envelope. Run the "verify" phase before committing.` + a second line `To proceed anyway (recorded in the audit log): batten override US-034 --reason "..."`.
*PASS iff:* the phase name is the **spec's** (`verify`), not a hardcoded default — change the phase id to `qa-final` in the spec and re-run; the message must follow. `gateGuess()` returns the first phase carrying a `gate:`; a FAIL that looks fine is the word "verify" appearing because it is hardcoded.

**VG-D1 — spec does not gate the close** · CLI\* · S-nogate → *Expect:* silence (`hooks.go:276`). Control: swap back to S-full, must deny.
**VG-D2 — gate declares no checks: fail open, out loud** · CLI\* · S-nochecks + an agent verdict `ok` with evidence.
*Expect:* **not** a deny — an `advise()` payload: `systemMessage` = `batten (warning — not blocking): batten: gate "qa" declares no checks, …` and `additionalContext`, with **no `permissionDecision` key**, *even though `enforcement: enforce`*.
*PASS iff:* both the absence of `permissionDecision` and the presence of the warning. A silent allow here is the exact "believed gate that isn't one" the README says it refuses to be.
**VG-D3 — no run recorded for the unit** · CLI\* · virgin DB, S-full, `H-commit` → *Expect:* silence (`hooks.go:284`, "the gate has nothing to say"). This is a deliberate fail-open; note whether anything at all tells the user the gate abstained (compare against `SessionStart`). Classify **confusing** if nothing does.

**VG-A1 — verdict `ok`, empty `evidence[]`, inserted past the CLI** · CLI\*
Insert via a DB path that skips `SaveVerdict`'s check (or use S-nochecks + `V-shout`), then commit.
*Expect:* `batten: US-034 has result=ok but an empty evidence[]. An approval must cite something.` + `store.ErrNoEvidence` text.
*PASS iff:* the hook denies **independently of** the binary's own refusal — the README claims belt-and-braces.
**VG-A2 — verdict `blocked`** · CLI\* → `batten: US-034 verdict is "blocked", not "ok".` + `why` + `safe_next_step:` + the override line.
**VG-A3 — the agent asserts a pass it never ran** · CLI\* — record **V-ok** by hand (`source` = agent) with S-full (gate has checks), no `batten check`.
*Expect:* deny — `batten: US-034 has no batten-verified pass. The gate's checks must be RUN, not asserted.` + `Run: batten check US-034`.
*PASS iff:* an evidenced, well-formed, human-plausible `ok` is still denied. **This is the highest-value adversarial cell in the whole matrix** — it is the difference between a gate and a formality.
**VG-A4 — forged provenance** · CLI · `"$BATTEN" verdict --unit US-034 --file $FX/V-forged.json`, then commit.
*Expect:* the `source` field is `json:"-"` (`store.go:~592`) so it must be stored as `agent`; the commit must still be denied as in VG-A3, and `batten runs` must **not** print the `*` batten-verified marker.
**VG-A5 — commit-command obfuscation** · CLI\* — run `H-commit` with each of:
`git commit -m x` · `git   commit` · `git -c user.name=x commit` · `echo hi && git commit -m x` · `cd backend && git commit` · `sh -c "git commit -m x"` · `g=git; $g commit` · `git $(echo commit)` · `git commit-tree …` · a two-line heredoc script then `bash ./c.sh`.
*Expect (what a user would reasonably expect):* every form that actually produces a commit is denied.
*PASS iff:* each denial is real. `commitRe = (^|[;&|]|\s)git\s+(-[^\s]+\s+)*commit\b` (`hooks.go:267`) — the discriminator is that a **silent allow on any variant that does commit is a gate bypass, not a nit**; classify each result as broken / missing / acceptable-by-design separately. Also record over-blocking (e.g. `git commit-tree`, `git commit --dry-run`) — over-blocking is friction, and friction is what gets the plugin uninstalled.
**VG-A6 — commit through a non-`Bash` path** · CLI\* — `hooks.json` matches only `Bash` for PreToolUse. Simulate a commit driven by a tool that is not Bash (write a `.sh` and run it later; a git MCP server). *Expect:* honest statement of the boundary. Classify **missing**, not broken, if the gate cannot see it — but it must be stated somewhere in the docs.
**VG-A7 — over budget blocks the close** · CLI\* — ingest **T-over** so tokens ≥ 3M, then a fully-verified commit.
*Expect:* `batten: US-034 is over budget. budget.on_exceed=block.` + the ceiling table, with `!` on the blown one and `not measurable (install batten statusline)` on `quota_pct`.
*PASS iff:* the `quota_pct` line says *not measurable* rather than `0.0% of 15.0%`. A zero here is the "never invent a number" principle failing.

**VG-E1 — first run ever, virgin DB** · CLI\* → silence expected (no run). Control required.
**VG-E2 — unit ambiguity: two open runs, branch `main`** · CLI\* → `activeUnit` returns `""` → silence, **no denial**. *Expect (README §"fail open only out loud"):* something says so. `SessionStart` does (`hooks.go:601`); the commit hook does not. Record whether a user committing at that moment gets any signal at all.
**VG-E3 — branch `feature/US-0345`** · CLI\* → `MatchUnit` with `US-\d{3}` yields **`US-034`**. *Expect:* either a match on the real unit or an honest refusal. Attribution to a *different* unit than the one on the branch is a silent mis-gate.
**VG-E4 — the backlog's `US-UX-01` items** · CLI\* → `US-\d{3}` never matches them; every UX unit is ungated forever with no warning. Classify **confusing** at minimum; check whether `doctor` or `init` ever mentions unmatched plan headings.

### 2. EV — the evidence rule at the binary

**EV-H1** · CLI · `"$BATTEN" verdict --unit US-034 --file $FX/V-ok.json` → `verdict recorded: US-034 qa=ok (2 evidence)`, exit 0, plus (S-full) `updated run note <path>`.
**EV-D1** · CLI · S-full with `gates.qa.evidence` absent → the **loader must refuse the spec** (`spec.go`: "requires a verdict but not evidence… Set evidence: required"). PASS iff the refusal happens at *load*, i.e. `doctor`/`verdict` both fail, not just one.
**EV-A1** · CLI · `--file $FX/V-empty.json` → exit **1**, stderr `batten: result "ok" with an empty evidence[] is not allowed: an approval must cite something (command output, test counts, a criterion verified). Without evidence the result is "blocked"`. PASS iff exit ≠ 0 **and** the verdict is absent from `batten show`.
**EV-A2** · CLI · `V-shout` (`"OK"`) → an uppercase result must not slip the rule. Record which of: refused, stored as `OK` and later denied by VG-A2's branch, or stored and treated as an approval. The third is a hole.
**EV-A3** · CLI · evidence of `["", "   "]` — non-empty array, empty content. *Expect:* a user would call this no evidence. `len(v.Evidence)` is 2, so it passes. Classify **missing** (the rule counts items, not substance) and state it plainly.
**EV-E1** · CLI · malformed JSON on stdin → `verdict must be a JSON envelope {check_id, result, evidence[], why, safe_next_step, requires_confirmation}: …`, exit 1.
**EV-E2** · CLI · no `--unit`, `check_id` with no unit id, branch `main` → `cannot tell which US this verdict is for; pass --unit`, exit 1. Also: a verdict recorded for a unit with **no prior run** must create one (`EnsureRun`) — confirm it then appears in `batten runs` with an empty phase, and judge whether that is intelligible.

### 3. WS — write-set guard, within a run

Precondition (CLI-only path): `hook SubagentStart` for `a1` **and** `a2` (creates nodes `n-a1`, `n-a2`), then `"$BATTEN" claim a2 ml/train.py`.

**WS-H1 — the owner writes its own file** · CLI\* · `H-write` with `agent_id:"a1"` on `ml/data/loader.py` claimed by `a1` → silence. *Control:* same payload against `ml/train.py` (a2's) must deny.
**WS-D1 — no `agent_id` in the payload, file owned by someone** · CLI\* · `H-write-anon` → **advise**, never deny: `batten: ml/train.py is claimed by a fanned-out agent (n-a2) in this run. If you are that agent, ignore this; …`. PASS iff there is no `permissionDecision`. This asymmetry is deliberate (`hooks.go:436-447`) and a hard deny here would be the FAIL.
**WS-D2 — nothing claimed at all** · CLI\* · no `claim` run → silence on every write. *Expect:* the fan-out is unguarded when the build phase forgets `batten claim`. Verify whether anything warns. Classify **missing** if a fan-out can run entirely unfenced in silence.
**WS-A1 — the README's headline denial** · CLI\* · `agent_id:"a1"` writing `ml/train.py` (owned by `n-a2`) →
```
batten: write-set collision. ml/train.py belongs to another agent's write-set (n-a2); you are n-a1.
Two agents must never write the same file — that is what makes the fan-out safe.
Your write-set:
  ml/data/loader.py
If this file genuinely belongs to you, the plan is wrong: fix the plan, do not cross the fence.
```
*PASS iff:* all four elements are present — the file, the **owner**, **your own id**, and **your own write-set enumerated**. A deny that omits the owner or your write-set is a technically-correct FAIL: the message is the product here.
**WS-A2 — separator normalisation** · CLI\* · claim `ml\train.py` (backslashes), then a payload with `ml/train.py`, and the reverse. *Expect:* one file, one owner, denied both ways (`normPath` / `filepath.ToSlash`).
**WS-A3 — case** · CLI\* · claim `ml/train.py`, write `ML/Train.py` on a case-insensitive Windows filesystem. *Expect (user):* still the same file, still denied. A silent allow is a real bypass on the platform batten calls first-class.
**WS-A4 — path escapes** · CLI\* · `file_path` = `<REPO>/ml/../ml/train.py`, a `..`-relative path, an absolute path outside the repo, a symlink into the repo. *Expect:* the first two denied, the outside-repo one ignored (`hooks.go:413`).
**WS-E1 — `file_path` empty / `tool_input` missing** · CLI\* → silence, no crash.
**WS-E2 — `NotebookEdit`** · CLI\* → same guard applies (`hooks.go:256`); `hooks.json` matcher includes it. Verify both halves agree.

### 4. XR — write-set guard across open runs (multi-session)

**XR-H1** · CLI\* — run A (`US-034`, sess A) claims `backend/api.py`; session B (`sessB`, unbound, no branch match) issues `H-write` on `backend/api.py`. *Expect:* deny — `batten: backend/api.py is being worked by US-034 in another open session (run <id>). Editing it now races that work. Coordinate, or use a worktree per unit…`.
**XR-D1** · CLI\* — run A **closed** (`batten close US-034 --status failed`) → the same write is now allowed. PASS iff a closed run releases its claims (README/`main.go:689` promise) *and* the control (re-open) denies again.
**XR-A1** · CLI\* — session B is bound to its own run US-051 and writes A's claimed file → still denied, message naming US-034. This is the ROADMAP's "verified with US-034/sessA vs US-051/sessB" claim; reproduce it or contradict it.
**XR-E1** · CLI\* — same path claimed in two runs of the *same* unit id in different projects → must not cross projects (`project` namespacing).

### 5. CL — `batten claim`

**CL-H1** · CLI · `claim a1 <abs path> ml/x.py` → `a1 owns 2 file(s); any other agent writing them is now denied`, absolute path relativised to repo-root form in `batten show`.
**CL-D1** · CLI · `claim a9 …` with no `SubagentStart` for `a9` → exit 1, `no subagent "a9" on record; the SubagentStart hook creates it`. *Judge:* is that message actionable from a CLI-only context? A user cannot create the node by hand. Classify **confusing** if the only remedy is a live session.
**CL-A1** · CLI · `claim a1 ml/train.py` when `n-a2` owns it → exit 1, `write-set collision: ml/train.py is already owned by n-a2`. PASS iff the transaction rolls back — a second file listed *after* the colliding one must **not** be claimed (check `batten show`); a partial claim is a real defect.
**CL-E1** · CLI · re-claiming your own file → idempotent, no error.
**CL-E2** · CLI · `claim a1` with no files → usage error `claim: batten claim <agent-id> <file>...`; and a path outside the repo → stays absolute, so the guard (which compares repo-relative) will never match it. Record as a **silent hole** if so.

### 6. PH — phases and the anchor

**PH-H1** · CLI · `phase US-034 build` → `anchor: US-034 base SHA = <7 hex>` **then** `US-034 -> phase build`; `batten show US-034` prints `base=<same sha>`.
**PH-H2** · CLI · `phase US-034 verify` → `US-034 -> phase verify` **and** `this phase must emit a verdict for gate "qa" (evidence required)`.
**PH-D1** · CLI · S-min (no `anchor`, no gate) → phase advances with neither extra line. Control: S-full prints both.
**PH-A1 — the anchor must not move** · CLI · `phase build`, make a commit, `phase build` again → the base SHA must be **unchanged** (`main.go:372`, `run.BaseSHA == ""`). A moved anchor silently shrinks the verify scope; this is the cell that catches it.
**PH-A2 — phase order is not enforced** · CLI · `phase US-034 close` directly from nothing. *Expect:* the state machine is declared in order; does anything object to skipping build and verify? If not, classify **missing** (the ordering is advisory) and say so — the schema calls `phases` "the state machine, in order".
**PH-E1** · CLI · unknown phase → exit 1, `no phase "shipit" in batten.yaml`.
**PH-E2** · CLI · run in a directory that is a batten repo but **not** a git repo (`rm -rf .git` on a scratch copy) → no anchor line, **no error**, phase still advances. PASS iff the absence is visible, not silent; a run whose `base=` is empty and never says why will be reviewed against the wrong diff.
**PH-E3** · CLI · `diff_from: anchor` — grep the binary's behaviour: nothing in `cmd/` or `internal/` computes a diff. *Expect:* confirm in writing that `diff_from` is **advisory only** (consumed by `/batten-verify` prose). Classify **missing**, not broken — but the schema's wording ("Operate only on this unit's diff") reads as enforcement.

### 7. BG — budget ceilings

**BG-H1** · CLI\* · ingest a transcript worth ~1.4M tokens, then `batten budget`:
```
US-034  1.4M tokens, $X.XX imputed
  · tokens       1.4M / 3.0M  [=====.......]
  · imputed_usd  $X.XX / $8.00  [=====.......]
  · quota_pct    NOT MEASURABLE — install the statusline (`batten statusline --install`)
```
*PASS iff:* the README's exact third line appears. **The single defining assertion of this row: no `0%`, ever.**
**BG-D1** · CLI · S-nobudget → `no ceilings declared in budget:` under the unit line.
**BG-D2** · CLI · `quota_pct_per_run` declared, statusline not installed → `NOT MEASURABLE` in `budget`, and `doctor` must print `⚠ budget.quota_pct_per_run is set but the statusline is not installed — that ceiling is NOT enforced.` Both, not one.
**BG-A1** · CLI\* · ingest **T-over** → `!` marker on `tokens`, `OVER — on_exceed=block`, and VG-A7 denies the commit.
**BG-A2** · CLI\* · `on_exceed: warn` with the same overspend → `!` still shown, commit **not** denied. PASS iff the two modes differ observably.
**BG-E1** · CLI · empty DB → `no open runs`.
**BG-E2** · CLI · `batten budget US-034` for a **closed** run → prints it anyway (unit filter bypasses the running-only filter, `main.go:1085-1090`). Judge whether that is intelligible or surprising.
**BG-E3** · CLI · quota snapshot exists but the window rolled over (negative delta) → must report unmeasurable, not a negative or a zero (`store.go: QuotaBurned`). Needs a crafted pair of snapshots — see SL-H2.

### 8. MS — multi-session binding

**MS-H1** · CLI\* · `hook PostToolUse` with `H-post-phase` (`batten phase US-034 build`) → the session is adopted onto the run. Verify via `hook SessionStart` for `sessA`, which must end with `→ this session is working **US-034**.`
**MS-H2** · CLI\* · branch-based binding: check out `feature/US-034`, `hook PreToolUse` from `sessB` → adoption happens through `activeUnit` step 2.
**MS-D1** · CLI\* · payload with **no** `session_id` → binding impossible; behaviour must degrade to branch/single-open-run resolution without crashing.
**MS-A1 — session stealing** · CLI\* · run bound to `sessA`; `sessB` on the same branch → `AdoptSession` moves the run to `sessB` (`hooks.go:642`). *Expect (user):* two sessions on one branch is a real scenario; find out whether A silently loses its binding, and whether anything tells either session. Classify **confusing** at least.
**MS-A2** · CLI\* · two open runs, neither owned, branch `main` → `activeUnit` returns `""`; both gates stand down. `SessionStart` must print `⚠ 2 units are open (US-034, US-051) and this session isn't bound to one.` + the `batten phase` remedy + the worktree advice.
**MS-E1** · CLI\* · exactly one open, unowned run → adopted silently. PASS iff `SessionStart` then reports the binding.
**MS-E2** · CLI\* · zero runs → `SessionStart` emits **nothing** (`hooks.go:609`, the `- **` guard). Confirm that a first-ever session gets no noise.

### 9. VA — vault export

**VA-H1** · CLI · S-full, `batten verdict …` → `updated run note <VAULT>/…`; the note exists, has typed frontmatter, and lists the write-sets actually claimed.
**VA-H2** · CLI · `hook Stop` (`H-stop`) → the run note refreshes with no explicit command. PASS iff the file's mtime/content changes; control: unset the vault, must not write.
**VA-D1** · CLI · S-novault → `batten canvas US-034` writes `$REPO/.batten/US-034.canvas` and prints **no** run-note line. This is the README's "No vault? No canvas is written" — note the doc and the code disagree in wording (code still writes a canvas into `.batten/`); record which is true.
**VA-D2** · CLI · vault path that does not exist → `doctor` prints `⚠ obsidian vault not found: <p> (canvas falls back to .batten/)`; the export must actually fall back, not error.
**VA-A1** · CLI · unit id containing a path separator or `..` (`US-034/../../etc`) → must not write outside the vault. Path-traversal check on `RunNotePath`/`CanvasPath`.
**VA-E1** · CLI · export for a unit with zero nodes → note written with an honest "not recorded" for write-sets (there is an `export_test` asserting this), not an empty table implying disjointness was verified.

### 10. CV — canvas

**CV-H1** · CLI · `batten canvas US-034 --out $SBX/out.canvas` → `wrote … (N nodes, M edges)`; the file parses as JSON and every node has `id,type,x,y,width,height`, every edge `fromNode,toNode` per JSON Canvas 1.0. PASS iff a schema check passes, **not** iff Obsidian "looks fine" — the format's reader fails silently.
**CV-D1** · CLI · no vault → canvas lands in `.batten/`.
**CV-A1** · CLI · a run with a retry and a blocked-then-fixed verdict → the graph must show the path **actually taken** (retry edges present). Build it by feeding two SubagentStart/Stop pairs and two verdicts. PASS iff the second verdict does not overwrite the first in the drawing.
**CV-E1** · CLI · unknown unit → exit 1, `no run recorded for US-999`.
**CV-E2** · CLI · a run with one phase node and no edges → still emits a valid canvas (0 edges), not an empty file.

### 11. TU — TUI

**TU-H1** · TTY · `batten tui` in a real terminal → renders runs; **cannot be judged from this harness** (Bubbletea needs a TTY; piped stdin gives a false failure). ROADMAP already lists it as built-not-proven and 0% covered.
**TU-D1** · CLI · `batten tui < /dev/null` non-interactively → must fail *gracefully* (a clear message), not paint escape codes or hang. This part **is** CLI-checkable.
**TU-E1** · TTY · empty DB → an empty state that says so.

### 12. SL — statusline

**SL-H1 — install** · CLI · `batten statusline --install` in a copy of `$REPO` → `.claude/settings.json` gains a `statusLine` pointing at the **absolute** binary path; message `statusline installed in .claude/settings.json — it now samples your subscription quota`. PASS iff the existing `settings.json` keys survive (the replica has one).
**SL-H2 — the sensor records a quota snapshot** · CLI\* · pipe a payload with `{"session_id":"sessA","rate_limits":{"five_hour":{"used_percentage":41.5,"resets_at":1793000000}}}` into `batten statusline` → a one-line status prints; then `batten budget` must switch `quota_pct` **from** `NOT MEASURABLE` **to** a real delta. That transition is the only honest proof the sensor works.
**SL-H3 — real quota values** · LIVE · only a real subscription session emits `rate_limits`.
**SL-D1** · CLI\* · payload with `rate_limits` absent (API-key users, first turn) → no snapshot recorded, `budget` still says NOT MEASURABLE, line still prints.
**SL-D2** · CLI\* · payload where `five_hour.used_percentage` is missing but the window object exists → nil, not 0. Assert the store holds NULL, not 0.0.
**SL-A1** · CLI · `--install` where a **foreign** statusLine already exists → refuses: `a statusLine is already configured ("…"). Re-run with --chain to have batten wrap it…`; with `--chain` it wraps and the wrapped command's output still appears.
**SL-A2** · CLI\* · a chained command that hangs → must be cut at the 3s `chainTimeout` and degrade to no extra segment. Use `cmd /c timeout 30`.
**SL-E1** · CLI\* · malformed JSON on stdin → prints something minimal, exit 0, never a Go panic trace.
**SL-E2** · CLI\* · run outside a batten repo → still records the (account-global) quota and prints generic segments.
**SL-E3** · CLI\* · unreadable DB (point `BATTEN_DB` at a directory) → prints exactly `batten`, exit 0.

### 13. MC — MCP surface

Driven over stdio, newline-delimited JSON-RPC (go-sdk `StdioTransport`):
```bash
printf '%s\n' \
 '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}' \
 '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
 '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
 '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"batten_verdict_status","arguments":{"unit":"US-034"}}}' \
 | "$BATTEN" mcp
```
**MC-H1** · CLI · `tools/list` returns exactly the six: `batten_runs, batten_run_graph, batten_verdict_status, batten_budget, batten_writeset_owner, batten_spec`.
**MC-H2** · CLI · `batten_verdict_status` on a gated, unverified unit must say **DENIED** and give the concrete unblock step — the same fact the hook would give. PASS iff the MCP answer and the hook's denial agree; a divergence is a trust bug.
**MC-D1** · CLI · run `batten mcp` from a directory with **no** `batten.yaml` → server still starts, every tool answers `batten does not govern this repo: no batten.yaml was found…`. PASS iff **stdout carries only protocol frames** — a single stray `fmt.Println` corrupts the stream (the package doc names this as the failure).
**MC-D2** · CLI · `BATTEN_DB` pointing at a fresh path → empty results with a note, not an error.
**MC-A1** · CLI · `batten_writeset_owner` for a file owned by another agent → names the owner; call it with a traversal path and a Windows-cased path (mirrors WS-A3/A4) and check it agrees with the hook. Disagreement between the advisory tool and the enforcing hook is the worst outcome here.
**MC-E1** · CLI · `batten_budget` with an unmeasurable quota → `available:false` + reason, never `0`.
**MC-E2** · LIVE · that Claude Code's own client connects via `.mcp.json` with `${CLAUDE_PLUGIN_ROOT}/bin/batten mcp`.

### 14. IN — init

**IN-H1** · CLI · fresh copy of `$REPO`, `batten init` → writes `batten.yaml`, prints `wrote batten.yaml — a working draft in report mode (gates warn, don't block).` and `project=… unit=… N domain(s) detected`. PASS iff (a) the draft **loads** (`batten doctor` clean), (b) `enforcement: report`, (c) domains reflect the 4 `AGENTS.md` dirs, (d) **no invented check commands** — the repo has no build files, so `check:` must be empty/TODO. An invented `make test` here is a serious FAIL (ROADMAP records this exact bug being fixed once).
**IN-H2** · CLI · `batten init --scan-json` → JSON with `harness[] stack[] purpose[] notes[]`; writes nothing. PASS iff `harness` names the real `AGENTS.md`/`CLAUDE.md`/`.claude/` assets, `stack` is honestly **empty**, and `notes` includes the "no check command was found" style admission.
**IN-D1** · CLI · `batten init --from context/planeacion_proyecto.md` → *Expect (from README):* "migrates a workflow you already wrote in prose". *Observed contract in code (`main.go:1429-1481`):* `--from` only changes one printed hint. PASS/FAIL judgement: if the CLI does not migrate, the README's claim belongs to the **slash command**, not the binary — record precisely which, and whether a user reading `batten init --from` would be misled. Also check the printed "Next:" list numbering (`1., 2., 2., 3.` — two items numbered 2 when `--from` is given).
**IN-A1** · CLI · `batten init` where `batten.yaml` exists → exit 1, `batten.yaml already exists — remove it or edit it by hand`; the existing file byte-identical afterwards.
**IN-E1** · CLI · an empty directory (no git, no files) → must produce something loadable or refuse clearly; never a half-written yaml.
**IN-E2** · CLI · a repo whose branch names carry no unit → `unit.pattern` defaults to `TASK-N` **and** `notes[]` says so.
**IN-E3** · CLI · the generated draft must round-trip: `init` → `doctor` → `phase` → `check` → `close` on a scratch unit without hand-editing anything except invariants.

### 15. DR — doctor

**DR-H1** · CLI · S-full → `✓ batten.yaml — project "…", unit "US", 5 phases, 4 domains`, `✓ close gate: phase "close" requires verdict "ok" on any gate`, `✓ enforcement: enforce — gates block`, `✓ store: <BATTEN_DB>`, `✓ obsidian vault: <VAULT>`.
*PASS iff:* `✓ store:` prints the **sandbox** path — this doubles as the isolation assertion for the whole run.
**DR-D1** · CLI · S-nochecks → `⚠ gate "qa" declares no checks — it verifies NOTHING and approves on the agent's word.`
**DR-D2** · CLI · S-graph with graphify absent from PATH → `⚠ graph: graphify declared but not on PATH — batten degrades gracefully` and `⚠ no graphify-out/graph.json yet — run: graphify . --code-only`.
**DR-D3** · CLI · S-ghost → `⚠ domains.backend.skills references "no-such-skill", which is not installed (did you mean "…"?)` — with the replica's 47 skills and 9 agents present, the *hint* is testable: point `agent:` at `backend-enginer` (typo) and require the suggestion `backend-engineer`.
**DR-D4** · CLI · S-full minus vault → the vault line simply absent, no warning.
**DR-A1** · CLI · **S-bad** → every one of the seven declared errors must appear in one `invalid spec:` block, not just the first. PASS iff all seven are listed; a loader that stops at the first is a slow, annoying FAIL.
**DR-A2** · CLI · `doctor` with a broken spec → note the **exit code is 0** (`cmdDoctor` returns nil on every path). *Expect (user):* a validator usable in CI exits non-zero. Classify **broken-for-CI / confusing**; the README shows `batten doctor` as the "green → working" step of the firing test.
**DR-E1** · CLI · no `batten.yaml` anywhere up the tree → `✗ no batten.yaml found. Run: batten init` (still exit 0 — same judgement as DR-A2).
**DR-E2** · CLI · `doctor` run from a **subdirectory** (`$REPO/backend`) → must find the spec by walking up, and report the same root.
**DR-E3** · SURGERY · stale-run warning (`>48h`) requires back-dating `last_event` in SQLite; no CLI path. Expected text: `⚠ N run(s) open >48h with no activity — close or resume them:` + a `batten close` line per run.

### 16. CK — check (the evidence batten generates)

**CK-H1** · CLI · S-full, `batten check US-034` → `✓ git --version`, `✓ git rev-parse --verify HEAD`, then `US-034: OK (batten-verified). all gate checks passed (batten ran them)`. Then `batten runs` shows `ok*` and the footnote `* batten-verified: the gate's checks were actually run`.
**CK-D1** · CLI · S-nochecks → exit 1, `gate "qa" declares no checks to run`. PASS iff this is an error, not a silent `ok`.
**CK-D2** · CLI · a check naming a tool that does not exist (`make test` on this Windows box, which has no make) → `✗ make test (exit N)` + the tail of the output, verdict `blocked`, `the commit gate will deny until this passes.` PASS iff the failure is attributed to the command, not swallowed.
**CK-A1** · CLI · a check that hangs (`cmd /c timeout /t 400`) → cut at 5 min with `124 … TIMED OUT` in the evidence, verdict `blocked`. (Long cell; run once.)
**CK-E1** · CLI · a check whose output is 10k lines → evidence keeps only the last 8 lines (`lastLines`), and says so intelligibly.
**CK-E2** · CLI · `batten check` with no unit and branch `main` → `check: batten check <unit> [--gate <name>]`; with `--gate nope` → `no gate "nope" in batten.yaml`.

### 17. CO — close

**CO-H1** · CLI · verdict ok + evidence + batten-verified → `US-034 closed (ok). Write-set claims released — files it held are free again.`; then XR-D1's write is allowed and `batten runs` shows the closed status.
**CO-D1** · CLI · no vault → same, minus the note refresh.
**CO-A1** · CLI · `close US-034` (defaults `--status ok`) with **no** verdict → exit 1, `cannot close US-034 as ok: needs a verdict with result=ok and cited evidence (run \`batten check US-034\`, or the verify phase). Use --status failed to close a run that went wrong`. PASS iff `close` and the commit gate agree — a unit that cannot be committed must not be closable.
**CO-A2** · CLI · `close --status ok` **after** an override → allowed (`gateReadyToClose` honours the override) and the override is on record.
**CO-E1** · CLI · `close US-034 --status failed` with no verdict → allowed. This is intentional (a wrong run must always be closable, or its claims stick forever); PASS iff the claims genuinely release.
**CO-E2** · CLI · `close` on a unit with no open run → `no open run for US-034`; `--status bogus` → `--status must be ok|failed|rolled_back, got "bogus"`.

### 18. OV — override

**OV-H1** · CLI · `batten override US-034 --reason "prod hotfix; QA in PR #412"` → `override recorded for US-034 (gate close): …` + `the gate will now allow the commit. This override is in the audit log.` Then VG's commit payload → silence (control: a second unit without the override still denies).
**OV-D1** · CLI · S-nogate (no closing phase) → the gate is recorded as `*`; confirm it does not silently override *everything* in the DB across units.
**OV-A1** · CLI · `override US-034` with no `--reason` → exit 1, `--reason is required: an override with no stated reason is just a disabled gate`. Also `--reason "   "` (whitespace) → same refusal.
**OV-A2** · CLI · after an override, is it **visible**? Check `batten show`, `batten runs`, the run note and the canvas. ROADMAP principle #4 is "an override goes in the log, always" — PASS iff a human reviewing the unit later sees it without querying SQLite. If it is only in a table nobody reads, classify **missing**.
**OV-E1** · CLI · override for a unit with no active run → `no active run for US-034` (so the escape hatch is unavailable exactly when a user is most likely to reach for it — judge whether the message points anywhere).

### 19. ME — measure

**ME-H1** · CLI\* · ≥3 runs tagged with headroom on and ≥3 off, plus per-model usage → `spend by model (imputed, not billed):`, then the with/without block, then `→ with headroom used X.X% fewer tokens on average` + `(still noisy — runs are not identical work; treat as directional, not exact)`.
**ME-D1** · CLI · headroom absent, `compression.measure` unset → the headroom block must be absent, not zeroed.
**ME-A1** · CLI\* · 1 run each side → each line carries `(insufficient — need ≥3 runs to compare meaningfully)` and **no percentage is printed**. PASS iff the percentage is genuinely withheld; printing it "for information" would be the exact failure mode this row tests.
**ME-E1** · CLI · empty DB → `batten measure` prints **nothing at all**, exit 0. *Expect (user):* "no data yet". Classify **confusing** if silence is all you get.
**ME-E2** · CLI · usage rows with `model` empty → grouped as `(unknown)`, never merged into a named model.

### 20. TK — token accounting

**TK-H1** · CLI\* · `batten ingest US-034 --transcript $FX/sessA.jsonl` with 6k parent + 15k subagent → `US-034: +N requests, 21.0k tokens total, $X.XX imputed`. PASS iff the subagent's 15k is included — parse only the parent and you lose 71% (ROADMAP's number). Discriminator: run once with `$FX/sessA/` renamed away and compare the totals; the delta must equal the subagent's tokens exactly.
**TK-D1** · CLI\* · **T-unpriced** → `unpriced models (counted as $0, tokens still exact): claude-zeta-9`. PASS iff both halves: the naming *and* the tokens still counted.
**TK-A1** · CLI\* · **T-dup** (same `requestId` twice) → counted once. Re-run `ingest` on the same file → `+0 requests` and an unchanged total (idempotence, since hooks fire it repeatedly).
**TK-A2** · CLI\* · **T-garbage** (half the lines corrupt) → the valid lines are still counted, no crash, no fatal. The transcript format is explicitly not a public API; the claim is "reports unavailable rather than guessing".
**TK-E1** · CLI\* · transcript path that does not exist → clean error from `ingest` (exit 1), and **silence** from the hook path (`h.ingest` swallows everything).
**TK-E2** · CLI\* · `ingest` with no active run → `no active run for US-034`.
**TK-E3** · LIVE · that a real 1.9 MB Claude Code transcript still parses (format drift is the stated risk).

### 21. RS — runs / show

**RS-H1** · CLI · `batten runs` → the `UNIT STATUS PHASE VERDICT TOKENS IMPUTED` table; `ok*` only when a batten-sourced verdict exists, plus the footnote.
**RS-H2** · CLI · `batten show US-034` → run id, status, phase, `base=<sha>`, one line per node with `owns N file(s)`, then the verdict and its evidence lines — or `no verdict: the close gate will deny a commit`.
**RS-D1** · CLI · a run with no usage rows → `—` in TOKENS/IMPUTED, never `0`.
**RS-A1** · CLI\* · **model-routing divergence**: `domains.ml.model: haiku`, but the ingested usage for that node's agent is `claude-opus-4-5-…` → `⚠ declared haiku` beside the node in `batten show`. This is the ROADMAP's "verifying the fan-out's routing is a fact" claim; it needs a node, a claim, and usage rows attributed to that `agent_id`.
**RS-E1** · CLI · empty DB → `no runs yet`.
**RS-E2** · CLI · `show` on an unknown unit → exit 1, `no run recorded for US-999`. And `show` right after a clean close must still print the run (`LatestRun`, not `ActiveRun`) — "no active run" right after closing reads like a bug.

### 22. EM — enforcement: report

**EM-H1** · CLI\* · S-report + VG-H2's precondition → the denial arrives as `systemMessage`/`additionalContext` with **no `permissionDecision`**. Byte-compare the reason text against the enforce-mode denial: identical text, different envelope.
**EM-A1** · CLI\* · S-report + WS-A1 → the write-set collision also degrades to a warning. PASS iff **both** gates ramp together; one that keeps blocking under `report` is a broken adoption ramp.
**EM-E1** · CLI\* · `enforcement: bogus` → the loader refuses (`enforcement must be "enforce" or "report"`), rather than defaulting to permissive.

### 23. DG — degrade-never-break (ROADMAP principle #2)

**DG-H1** · CLI\* · every one of the 6 handled events fed with a well-formed payload → exit 0, no stack trace, no stray stdout beyond the documented JSON.
**DG-D1** · CLI\* · `cwd` pointing at a non-batten directory → every event silent, exit 0.
**DG-D2** · CLI\* · `BATTEN_DB` pointing at an unwritable path → hooks silent, exit 0, session unaffected.
**DG-A1** · CLI\* · malformed / truncated / 10 MB / empty stdin on each event → exit 0, no panic (the `recover()` in `cmdHook` is the safety net; a printed Go stack would be the FAIL).
**DG-A2** · CLI\* · `tool_input` present but of the wrong JSON type (`"tool_input": "hello"`, `"tool_input": []`) → silence, no crash.
**DG-E1** · CLI\* · an unknown event name (`batten hook Frobnicate`) → silence, exit 0.
**DG-E2** · CLI\* · `batten hook` with no event name → exit 1, `hook: need an event name`. (The only hook path that is allowed to fail loudly.)

### 24. PL — plugin surface

**PL-H1 — hook latency** · CLI · time 50 sequential `hook PreToolUse` invocations against a warm DB → median must beat the <50 ms budget (ROADMAP claims 17 ms). CLI-measurable as a *proxy*; the real number includes Claude Code's process spawn.
**PL-H2 — exec-form resolution on Windows** · LIVE · `hooks.json` names `${CLAUDE_PLUGIN_ROOT}/bin/batten` **without `.exe`**. Only a live session proves the resolution. This is the load-bearing Windows claim.
**PL-D1 — bootstrap** · LIVE · with `bin/` empty and no network, `SessionStart` must say so and no-op, not fail silently — the README makes a point of this against the "bare command name" bug.
**PL-A1 — the tap: does `agent_id` reach a real subagent `PreToolUse`?** · LIVE ⚠ · `batten hook-debug --tap`, launch a subagent that edits a file, `--show` → `--- PreToolUse payloads: N WITH agent_id, M without ---` and one of the two VERDICT lines. **The entire write-set guard's hard-deny path hangs off this field.** ⚠ requires redirecting `USERPROFILE`/`HOME` into the sandbox, since `tapPath()` ignores `BATTEN_DB`.
**PL-E1** · CLI · `batten` with no args → usage on **stderr**, exit 2; `batten version` → `batten 0.1.0`; an unknown subcommand → usage + exit 2. Also: `printUsage` omits `check`, `close`, `phase`(listed), `measure`, `hook-debug`, `ingest`(listed) — cross-check the printed list against the `switch` in `main()` and report every command that exists but is undocumented, and every documented flag that does not exist.

### 25. SC — the slash commands — **all LIVE**

`/batten-init`, `/batten-plan`, `/batten-build`, `/batten-verify`, `/batten-close`, `/batten-night` are **prompt files**. Nothing about them is CLI-testable; what can be checked from the CLI is only that the commands they *tell* the agent to run exist and behave (every cell above). A live session must establish:

| id | claim to test live |
|---|---|
| SC-1 | `/batten-build` actually launches one subagent per domain **and** each one calls `batten claim` before writing — the guard is inert if claiming is skipped (see WS-D2) |
| SC-2 | `domains[].agent` is used as the subagent type, so `SubagentStart.agent_type` equals the domain name and `hooks.go:483` resolves the domain directly |
| SC-3 | invariants ride **verbatim** into each agent's prompt |
| SC-4 | `/batten-verify` produces a verdict the binary accepts, and `blocked` when a criterion could not be verified |
| SC-5 | `/batten-night` honours `max_iterations`, never overrides, never deletes, stops before the close, and its report distinguishes an unmeasured ceiling from zero |
| SC-6 | `/batten-plan` degrades to grep when graphify is absent and **says** it did |
| SC-7 | a denied Edit mid-fan-out causes the orchestrator to stop that agent and fix the plan, rather than route around the fence |

---

## 5. Explicitly UNTESTABLE from the CLI

Everything below can be *simulated* but not *proven* without a live Claude Code session. Any report that claims these work from CLI evidence alone is overclaiming.

1. **That Claude Code invokes the hooks at all** — matcher wiring, `${CLAUDE_PLUGIN_ROOT}` expansion, exec form without `.exe`, the 10 s timeouts, `async: true` on PostToolUse/SubagentStop/Stop.
2. **That `permissionDecision: "deny"` actually blocks the tool.** The CLI proves batten *emits* the JSON; only a session proves the tool call dies.
3. **That `agent_id` is present in a real subagent's `PreToolUse`** (PL-A1). Every `CLI*` write-set cell assumes it. If it is absent live, the guard is advisory-only and half of row 3 collapses to row 3's `WS-D1`.
4. **That `SubagentStart`/`SubagentStop` fire for Agent-tool subagents**, and that `agent_type` carries the domain name.
5. **Real transcript shape** (`TK-E3`) — the format is explicitly not a public API.
6. **Real `rate_limits`** in the statusline payload, therefore the only genuinely enforced `quota_pct` (SL-H3). Everything else in row 7 is measurable; this one is not.
7. **`SessionStart.additionalContext` actually reaching the model's context.**
8. **The MCP client handshake from Claude Code** (the protocol itself is CLI-drivable; the wiring is not).
9. **The TUI** (needs a TTY) and **bootstrap.sh's download path** (needs the release that has never been tagged).
10. **Every `/batten-*` command** (row 25), because their subject is agent behaviour.
11. **Stale-run detection** (DR-E3) — needs clock/DB surgery, not a live session.

## 6. Suggested execution order

1. **DG + PL-E1** first — establishes that the binary is alive, isolated (`✓ store:` points into the sandbox), and that silence has a known cause. Without this, every ALLOW cell is uninterpretable.
2. **DR-H1 / DR-A1** — the spec loader, because every other row depends on a spec that loads.
3. **EV → VG → WS → XR** — the two denials, which are the product.
4. **PH, CL, MS** — the state the denials read from.
5. **CK, CO, OV, BG, TK** — the gate's supporting cast.
6. **VA, CV, SL, MC, ME, RS, TU** — the surfaces.
7. **IN last, on a throwaway copy** — it writes into the repo.