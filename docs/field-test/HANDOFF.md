# Field test — handoff

> State of an in-progress field test of batten. Written so a session with **no prior context** can
> pick it up. Last updated 2026-07-28.

## ⏸ RESUME HERE — verification is 35/63 done

A second session verified findings **0–34** adversarially, in batches of five, on a binary built
from `HEAD`. Results are in **`verified.json`** (same schema for every entry: verdict, repro,
verbatim evidence, the positive control that was run, `already_fixed_at_head`, an independently
re-judged severity, and a `fix_hint` with file:line).

| batch 1–7 result | count |
|---|---|
| CONFIRMED (reproduced on the HEAD binary) | 31 |
| REFUTED | 4 — #2 already fixed at HEAD, #25 not a defect, #11 and #26 downgraded to polish |

**Next action: verify findings 35–62** — the remaining 28, still five at a time, using the same
brief (the two traps below, refute-by-default, mandatory positive control for every ALLOW claim).
Then reconcile, then fix, then write `docs/FIELD-TEST.md`.

Two things the verification changed about the plan below:

- **Blocker #3 (`on_exceed: block` not enforced without `checks:`) is CONFIRMED with a clean A/B.**
  A run 7.8× over its ceiling got only an advisory with `gates.qa.checks` empty; adding one check
  line and replaying the byte-identical payload produced `permissionDecision: deny`. Fix is at
  `internal/hooks/hooks.go:347` — capture the advisory instead of returning it, and return after
  the budget block at 354–363.
- **Two new gate holes surfaced that are blocker-grade and are not in the original six.**
  Finding **#9**: `batten check` writes its own `source=batten` verdict, which satisfies *both*
  halves of the commit gate, so `batten check` alone closes a unit on an empty diff with nothing
  judging acceptance criteria. Finding **#22**: once *any* unit is batten-verified, a commit whose
  message names a *different* unit is allowed — the commit text is never read. Treat both as
  blockers when scheduling the fixes.

## What happened

A multi-agent field test ran batten against an isolated replica of a real project
(`proyecto_ui`: 4 domains each with their own `AGENTS.md`, a root `AGENTS.md` and `CLAUDE.md`,
47 skills, 9 custom agents, a backlog of `US-001`..`US-0NN`, **and no code yet**), plus a
from-zero demo repo built specifically to walk the five-step adoption path.

Seven dimensions were exercised: `init`, `gate`, `writeset`, `lifecycle`, `observability`,
`budget`, `demo-zero`.

**Result: 90 behaviours confirmed working, 80 findings.** The run hit a session limit partway
through, so 55 of 90 agents died — almost all of them the adversarial verifiers, plus the final
synthesis. That is why most findings are still unverified.

| | count |
|---|---|
| findings total | 80 |
| blocker / major / minor / polish | 6 / 38 / 27 / 9 |
| broken / confusing / missing / wrong-docs | 41 / 22 / 14 / 3 |
| CONFIRMED by a skeptic | 11 |
| REFUTED by a skeptic (not defects) | 6 |
| **still unverified** | **63** |

## Isolation — verified, not assumed

Nothing outside a sandbox was touched, and this was checked afterwards rather than trusted:

- The real `C:/…/proyecto_ui` still has one commit, a clean tree, and no `batten.yaml`.
- The real `~/.batten/batten.db` contains only project `batten` with `TASK-1`/`TASK-2` — this
  repo's own dogfood runs. No `proyecto_ui`, no demo, nothing from the test.

**Every agent must keep this rule.** Export `BATTEN_DB` into its own sandbox before any batten
command; `dbPath()` falls back to the user's real database the moment it is unset. Never write to
the real vault. Never `git push` from a sandbox, never add a remote.

## The files here

| file | what it is |
|---|---|
| `FINDINGS-RAW.md` | all 80 findings, sorted blocker-first, each with the command, verbatim output, expectation, and the verifier's verdict where one exists |
| `unverified.json` | the 63 findings that needed an adversarial pass — the input; findings 0–34 are done, 35–62 remain |
| `verified.json` | the 35 verdicts produced so far, one per finding, with repro, evidence and positive control |
| `dimensions.json` | the raw per-dimension returns, including the 90 `worked` entries |
| `verdicts.json` | the 26 verification verdicts that did complete |
| `TEST-MATRIX.md` | the designed test matrix: every claim × happy/degraded/adversarial/edge, with what is CLI-testable vs what needs a live Claude Code session |
| `ARTIFACT-PLAN.md` | what batten *should* generate for a project like this, and the acceptance criteria for "batten works here" |

Paths in these files were sanitized to `$SANDBOX` / `$HOME`; CI fails on tracked absolute home
paths, so keep it that way.

## Next step: verify the remaining 63, **five at a time**

The batch size is not arbitrary — the previous run died of a session limit doing all of them at
once. Five per batch, sequentially, is the instruction.

For each finding, the verifier's job is to **refute** it, defaulting to refuted unless personally
reproduced. Two traps to brief them on:

1. **batten has deliberate behaviours that look like bugs.** Fail-open with a warning when it
   cannot attribute; advisory instead of deny for an unattributed write; reporting a ceiling as
   unmeasurable rather than zero. A correct design must not be filed as a defect. Six findings
   were already refuted on exactly these grounds.
2. **Silence is not evidence of ALLOW.** `batten hook` prints nothing and exits 0 for at least six
   different reasons — allow, no spec found, store failure, malformed stdin, a recovered panic, an
   unknown event. Every ALLOW claim needs a paired positive control: the same payload with one
   field changed so a DENY is mandatory. If the control is also silent, the hook never engaged and
   the "PASS" proves nothing. `TEST-MATRIX.md` §1.1 explains this at length.

## Then: reconcile against HEAD before fixing anything

**The binary the agents used was frozen before five fixes that have since landed.** Check each
finding against current `HEAD` before acting on it — some are already fixed:

| already fixed | commit |
|---|---|
| `init` derived the unit from branch names only, proposing `TASK-\d+` on a repo whose backlog says `US-001` | reads the backlog now; also fills `unit.plan` and `unit.locator` |
| skill→domain suggestions matched a skill's *description*, so an infra skill landed in two domains | matches the skill's name segments now |
| two tests assumed Windows path semantics everywhere; PowerShell split `-coverprofile=` | CI is green on all three OSes |
| a tag could publish a plugin manifest declaring a different version | guarded in `release.yml` |
| `internal/tui` and `internal/hooks` had little or no coverage | 80.5% and 66.1% |

## The six blockers

Ordered by how much they matter. **#4 is a regression introduced by commit `24d8e4c` in this same
session** — the field test caught it, which is the best possible argument for having run it.

1. **Three ordinary spellings of `git commit` walk straight through the verdict gate.**
   `commitRe` in `internal/hooks/hooks.go` misses `git -c user.name=… -c user.email=… commit`,
   which is the standard non-interactive commit an agent or CI issues. **CONFIRMED by a skeptic.**
   This is the one denial the product is built around.

2. **Phase node ids are global rather than per-run.** `NodeID: "p-" + phaseID` in
   `cmd/batten/main.go` collides across units: a second work item entering the same phase name
   takes the phase row out of the first one's run, and its canvas collapses to a bare header.
   Reported independently by two dimensions (`lifecycle` and `observability`). Two units open at
   once is the headline use case.

3. **`on_exceed: block` is silently not enforced when the gate declares no `checks:`.** The
   `else` branch added in `24d8e4c` returns `advise(...)` *before* the budget evaluation below it,
   so an over-budget run lands. Proven A/B by adding one line of checks back and replaying a
   byte-identical payload. **My regression — fix this first.**

4. **`batten ingest` discards every request predating the run and then reports "0 tokens" as a
   measured number.** `store.RecordUsage` fences on `runs.started_at`, and the default flow opens
   the run *after* the work happened. This is the default path, not an edge case, and reporting an
   unmeasured zero is the exact failure principle #1 exists to prevent.

5. **The commit gate does not exist until some batten command has created a run**, so the first
   commit after adopting batten is ungated — silently. A newcomer branches, codes, commits, sees
   it succeed, and concludes the gate is working.

6. **A second unit entering the same phase name steals the phase row from the first unit's run
   record** (the record-level face of #2).

## Suggested order of work

1. Verify the 63, five at a time.
2. Reconcile survivors against `HEAD`.
3. Fix blocker #3 first (it is a fresh regression), then #1, then #2/#6 together, then #4, then #5.
4. Write `docs/FIELD-TEST.md` — the readable report the synthesis agent never got to produce.
5. Finish the demo repo's `README.md` quickstart with its real captured output.

## The demo repo

Built during the run, in the sandbox at `$SANDBOX/batten-testbed/demo-batten-quickstart`: a real
Go project (`taskly`) with two domains, `AGENTS.md` per domain, a backlog, plan and resolution
artifacts, a `verdict.json`, a `scripts/break-the-invariant.patch` for demonstrating a denial, and
two real commits — one of which went through the batten flow. **Its `README.md` quickstart was
never written** (the agent died first). The sandbox is session-scoped and may be cleaned up; if it
is gone, rebuild it from the `demo-zero` task in the workflow script.
