# Field test — handoff

> Working notes for a field test of batten that is now **complete**. The readable report is
> [`../FIELD-TEST.md`](../FIELD-TEST.md) — start there. This file is the operational record:
> what was run, how, and what to repeat if you run it again. Last updated 2026-07-28.

## ✅ DONE — all 63 verified, all blockers fixed

Verification finished in 13 batches of five, each verifier reproducing on a binary built from
`HEAD` before it was allowed to confirm anything. Results are in **`verified.json`**: one entry
per finding with its verdict, repro, verbatim evidence, the positive control that was run,
`already_fixed_at_head`, an independently re-judged severity, and a `fix_hint` with file:line.

| | count |
|---|---|
| CONFIRMED | 52 |
| REFUTED | 11 |
| blocker / major / minor / polish (confirmed, re-judged) | 3 / 26 / 17 / 6 |

Eight defects were fixed, each with a test that fails against the commit before it:

| commit | what it closed |
|---|---|
| `53227e8` | the `on_exceed: block` regression, and `commitRe` missing three spellings of `git commit` |
| `a1a4075` | global phase/subagent node ids, the renderer dropping unresolvable children, phases never finishing, the leaked `n-` id |
| `cc3bc9b` | an unmeasured run reported as `0 tokens, $0.00`; the silent time fence; misdirecting "install the statusline" |
| `f6aeba2` | ungated and unattributable commits passing in silence |
| `a9d8996` | `batten check` satisfying both halves of the gate; the commit message never being read |
| `92ae1cb` | `check` and `doctor` exiting 0 on failure; `show` hiding one of the two verdicts; missing `--help` entries |

Two of those were **not** in the original six blockers — the verification pass found them itself,
which is the argument for making verifiers reproduce rather than review.

**If you pick this up to continue:** the remaining confirmed findings are the major/minor/polish
tail in `verified.json`, each with a `fix_hint`. Nothing else is blocked on anything.

## What happened

A multi-agent field test ran batten against an isolated replica of a real project
(`proyecto_ui`: 4 domains each with their own `AGENTS.md`, a root `AGENTS.md` and `CLAUDE.md`,
47 skills, 9 custom agents, a backlog of `US-001`..`US-0NN`, **and no code yet**), plus a
from-zero demo repo built specifically to walk the five-step adoption path.

Seven dimensions were exercised: `init`, `gate`, `writeset`, `lifecycle`, `observability`,
`budget`, `demo-zero`.

**Result: 90 behaviours confirmed working, 80 findings.** The run hit a session limit partway
through, so 55 of 90 agents died — almost all of them the adversarial verifiers, plus the final
synthesis. A second session finished the verification in batches of five; the batch size is the
whole reason it completed, and it is the single most transferable lesson here.

| | count |
|---|---|
| findings total | 80 |
| blocker / major / minor / polish | 6 / 38 / 27 / 9 |
| broken / confusing / missing / wrong-docs | 41 / 22 / 14 / 3 |
| CONFIRMED by a skeptic | 11 |
| REFUTED by a skeptic (not defects) | 6 |
| verified in the second pass | 63 (52 confirmed, 11 refuted) |

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
| `unverified.json` | the 63 findings as filed, before verification — the input to the second pass |
| `verified.json` | all 63 verdicts, one per finding, with repro, verbatim evidence and the positive control that was run |
| `dimensions.json` | the raw per-dimension returns, including the 90 `worked` entries |
| `verdicts.json` | the 26 verification verdicts that did complete |
| `TEST-MATRIX.md` | the designed test matrix: every claim × happy/degraded/adversarial/edge, with what is CLI-testable vs what needs a live Claude Code session |
| `ARTIFACT-PLAN.md` | what batten *should* generate for a project like this, and the acceptance criteria for "batten works here" |

Paths in these files were sanitized to `$SANDBOX` / `$HOME`; CI fails on tracked absolute home
paths, so keep it that way.

## How the verification was run — repeat this shape

Five per batch, sequentially. The batch size is not arbitrary: the first attempt died of a
session limit doing all 63 at once and lost 55 agents plus the synthesis. Thirteen batches of
five finished the same work with nothing lost.

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

## Reconciling against HEAD — already done, and how

**The binary the original agents used was frozen before five fixes that had since landed.** This
needed no separate pass in the end: every verifier built from `HEAD` and reproduced there, so
reconciliation happened inside verification. Exactly one finding (#2) turned out to be already
fixed. The five fixes that had landed:

| already fixed | commit |
|---|---|
| `init` derived the unit from branch names only, proposing `TASK-\d+` on a repo whose backlog says `US-001` | reads the backlog now; also fills `unit.plan` and `unit.locator` |
| skill→domain suggestions matched a skill's *description*, so an infra skill landed in two domains | matches the skill's name segments now |
| two tests assumed Windows path semantics everywhere; PowerShell split `-coverprofile=` | CI is green on all three OSes |
| a tag could publish a plugin manifest declaring a different version | guarded in `release.yml` |
| `internal/tui` and `internal/hooks` had little or no coverage | 80.5% and 66.1% |

## The six blockers as originally filed — all now fixed

Kept as filed, because how they were described before verification is part of the record. Two
more of blocker weight were found during verification and are listed at the top of this file.
**#4 is a regression introduced by commit `24d8e4c` in this same session** — the field test
caught it, which is the best possible argument for having run it.

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

## The order it was actually worked, and why

1. Verify the 63, five at a time. This is the step that had failed before; batching is the fix.
2. Reconcile against `HEAD` — which turned out to be free, because every verifier built from
   `HEAD`. Building the binary once, up front, and pointing every verifier at it collapses two
   passes into one. Do it that way.
3. Fix #3 first (a fresh regression, and the cheapest to lose track of), then #1, then #2/#6
   together, then #4, then #5, then the two the verification found.
4. `docs/FIELD-TEST.md` — the readable report.
5. `docs/QUICKSTART.md` — the from-zero walkthrough, captured from a real run rather than
   written from memory. Writing it this way found three more defects (`batten check` exiting 0
   on BLOCKED, `doctor` exiting 0 on an invalid spec, `show` hiding one of the two verdicts),
   because walking the path is a test and describing it is not.

## The demo repo

`taskly`: a small Go project with two domains (`api/`, `store/`), one invariant per domain in its
`AGENTS.md`, a `Makefile`, and a backlog of `US-001`..`US-005`. The invariant that carries the
walkthrough is *`store.ErrNotFound` maps to 404, never to 500* — small enough to state in a line
and real enough that breaking it fails a test.

It lives in a session-scoped sandbox and is not committed. That is deliberate: the artifact worth
keeping is [`../QUICKSTART.md`](../QUICKSTART.md), which records every command and every block of
output the run produced, including the negative control where the invariant is broken on purpose
and the gate is watched refusing the commit.

To rebuild it, follow QUICKSTART from the top — it is written so the repo can be recreated from
it. Two things to keep if you do:

- **At least three `### US-0NN` headings in the backlog.** `init` refuses to infer a unit
  convention from fewer, on purpose, and with two it correctly falls back to `TASK-\d+`.
- **A test that actually fails when the invariant breaks.** Otherwise the negative control is a
  story about a denial rather than a denial.
