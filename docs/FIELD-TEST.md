# Field test

> batten run against a replica of a real project and a repo built from zero, by agents that had
> never seen it. 90 behaviours confirmed working, 80 findings, every one of them verified or
> refuted by a second agent whose job was to prove it wrong. 2026-07-28.

## Why this exists

batten's entire claim is that a rule stops being a request and becomes a mechanism. A document
can ask an agent not to approve its own work; a `PreToolUse` hook can refuse the commit. That
claim is only worth anything if the mechanism actually holds, and the only way to find out is to
put it in front of someone who does not already know where the seams are.

So: seven dimensions, exercised by agents given the docs and nothing else. Then a second pass in
which each finding was handed to a fresh agent whose instructions were to **refute** it, with
the default verdict *refuted* unless they reproduced the defect themselves on a binary built
from `HEAD`.

The headline number is not the 80 findings. It is that the run caught a regression introduced
**four commits earlier, in the same session** — a change I had written, reviewed, and believed.

## What was tested

An isolated replica (`replica-ui`) of the private repo batten was designed against: four domains
each with their own `AGENTS.md`, a root
`AGENTS.md` and `CLAUDE.md`, 47 skills, 9 custom agents, a backlog of `US-001`..`US-0NN`, and no
code yet. Plus a demo repo built from zero specifically to walk the five-step adoption path.

| dimension | what it exercised |
|---|---|
| `init` | scanning a repo it has never seen and proposing a spec |
| `gate` | the verdict gate on `git commit` |
| `writeset` | the fan-out fence between parallel subagents |
| `lifecycle` | phases, the anchor, close, multi-unit |
| `observability` | the run graph, the canvas, the TUI |
| `budget` | the token/dollar/quota ledger and its ceilings |
| `demo-zero` | adoption from an empty directory |

Nothing outside a sandbox was touched, and this was checked afterwards rather than assumed. The
repo it replicates still has one commit, a clean tree and no `batten.yaml`. The real
`~/.batten/batten.db` contains only this repo's own dogfood runs.

## Results

| | |
|---|---|
| behaviours confirmed working | 90 |
| findings | 80 |
| verified adversarially | 80 (17 during the run, 63 in the second pass) |
| **confirmed** | **52** |
| refuted | 11 |

Confirmed findings, after the verifier re-judged severity independently of the reporter:

| severity | count |
|---|---|
| blocker | 3 |
| major | 26 |
| minor | 17 |
| polish | 6 |

Eleven refutations, which matter as much as the confirmations:

- **6 were duplicates.** Three restatements of the same missing `--help` entries, two of the
  same unfinished phase nodes, one of the same fabricated zero. Different dimensions found the
  same defect through different surfaces, which is a good sign about coverage and a bad sign
  about counting findings.
- **2 were correct design mistaken for a bug.** The canvas has no phase-to-phase edges because
  the schema has no such relation — phase order is positional and documented as such. And a
  write-set *is* released, by `batten close --status failed`, which the report claimed did not
  exist.
- **1 was already fixed** at `HEAD` by a commit that landed after the run.
- **2 were real but smaller than filed.** `batten show` does name the failing check, contrary to
  the report. And the 7-character anchor cannot be ambiguous: `git rev-parse --short` is a
  *minimum*, and the verifier proved it by building a repo with a real 4-character collision and
  watching git return five.

## The two traps

Verifying findings about a governance tool has two failure modes that produce confident,
wrong answers. Both were briefed to every verifier.

**Deliberate behaviour that looks like a bug.** batten fails open with a warning when it cannot
attribute a write, gives an advisory instead of a denial when it cannot assign blame, and reports
an unmeasurable quantity as unmeasurable rather than as zero. Each of those is a decision, not an
oversight — refusing every commit in a repo that has not declared its checks yet just gets batten
uninstalled on day one. Filing them as defects would have produced "fixes" that make the tool
worse. Eight findings across both passes were refuted on exactly these grounds.

The inverse is a real defect, and the distinction is sharp: reporting an *unmeasured* quantity as
a hard `0` is the failure the whole design exists to prevent. Two confirmed findings were exactly
that.

**Silence is not evidence of allow.** `batten hook` prints nothing and exits 0 for at least six
different reasons: allow, no spec found, a store failure, malformed stdin, a recovered panic, an
unknown event. Every claim that something was allowed therefore required a paired positive
control — the same payload with one field changed so a denial is mandatory. If the control was
also silent, the hook never engaged and the "PASS" proved nothing.

This is not hypothetical rigour. The blocker below was proven precisely this way: byte-identical
payload, one line of `checks:` added back, denial appears.

## The three blockers

All three were confirmed by reproduction, not by reading code.

### 1. `on_exceed: block` stopped being enforced — a regression from four commits earlier

Commit `24d8e4c` added an advisory for a gate that declares no `checks:`. That advisory is
correct and still stands. What was wrong is that the branch **returned**, so everything after it
was skipped — including the budget evaluation.

The result: a repo that had not declared its checks yet also silently lost its budget ceiling.
Two conditions with nothing to do with each other, coupled by control flow.

The A/B, on one run 7.8× over its token ceiling under `on_exceed: block` and
`enforcement: enforce`:

```
gates.qa.checks: []        -> advisory only, commit lands
gates.qa.checks: ['...']   -> permissionDecision: deny, citing the budget
```

Same binary, same database, byte-identical payload. This is the finding that justifies having
run the field test at all: I wrote that regression, reviewed it, and shipped it four commits
before an agent found it.

### 2. A node id that does not carry its run is not an identifier

Phase nodes were named `"p-" + phaseID`, and `node_id` is a `PRIMARY KEY` under an
`INSERT OR REPLACE`. So a phase called `build` was **one row for the whole database**. The
second unit to enter `build` rewrote the first one's row to point at its own run.

Reported independently by two dimensions, and proven at the storage layer rather than the
renderer: the verifier read the row back out of SQLite and watched its `run_id` change. The
first unit's canvas collapsed from four nodes to a bare header; its TUI tree read
"no nodes recorded yet". Subagent ids collided the same way, because agent ids are only unique
within the session that minted them.

Two work items open at once is the headline use case.

### 3. The renderers dropped what the collision left behind

Scoping the ids was necessary and not sufficient. Both the canvas and the TUI bucketed a child
under whatever its spawn edge named, and only rescued orphans whose parent was the *empty
string*. A subagent whose parent id did not resolve was dropped from both surfaces — while
`batten show`, reading the same database, still listed it.

A missing agent is exactly the one someone opens the canvas to find.

## Two more the verification pass found on its own

Neither was in the original report. Both are the same shape as the gate's central promise, and
both are why the verifiers were told to reproduce rather than to review.

**`batten check` alone could close a unit.** It writes its own `source='batten'` verdict, and
that row was both the newest verdict *and* the batten-verified one — so it satisfied both of the
gate's conditions by itself. `batten check` on an empty diff, still in the build phase, with
nothing having judged the acceptance criteria, cleared the way for a commit. The gate was
one-sided: an agent's verdict alone denied, batten's own passed.

**The commit message was never read.** A session bound to a verified `TASK-1` could land
`feat(TASK-2): ...` while `TASK-2` had no verdict at all — one unit's review credited to
another unit's work. Under trunk-based development, where the branch names nothing, the message
is the only signal there is. `batten.schema.json` had promised this resolution the whole time.

## What was fixed

Every blocker, both new gate holes, and the confirmed findings that shared their root cause.
Each fix carries a test that fails against the commit before it.

| fix | closes |
|---|---|
| an advisory is held and returned last, so a budget denial can still overtake it | 30 |
| `commitRe` matches `git -c k=v commit`, `git -C dir commit`, `git.exe commit` | the original blocker #1 |
| node ids are built by `store.PhaseNodeID` / `store.AgentNodeID`, so the two producers cannot drift | 5, 14, 38 |
| a child whose parent id does not resolve lands in the unattributed column | 39 |
| closing a run finishes its phase nodes | 18, 44, 55 |
| the write-set denial prints the id the agent was launched under, and says what to do when it owns nothing | 8, 61 |
| an unmeasured run reports as unmeasured, never as `0 tokens, $0.00` | 40, 57 |
| `batten ingest` reports what the time fence kept out, in tokens and dollars | 29 |
| unavailable ceilings carry the actual reason instead of "install the statusline" | 35 |
| an ungated or unattributable commit says so instead of passing in silence | 20, 49 |
| the gate requires two verdicts from two different producers, and `close` uses the same rule | 9, 15 |
| the commit message decides which unit is gated | 22, 50 |

The remaining confirmed findings — the majority of the 52 — were recorded in
`field-test/verified.json` with their reproduction, verbatim evidence, the positive control that
was run, and a `fix_hint` naming the file and line. **That directory was retired from the tree**
before the first tag: it described a private repository rather than the synthetic replica, and a
document about someone else's codebase is not publishable at any amount of rewriting. It is in git
history. Their current status — 45 fixed, 7 open, each open one named — is in
[CHANGELOG.md](../CHANGELOG.md) under *Known gaps*.

## What this says about the method

Three things worth keeping.

**Reproduce, do not review.** Verifiers who read code produced plausible findings; verifiers who
ran commands produced correct ones. Every verdict in `verified.json` that says CONFIRMED has a
command list and pasted output behind it, and the ones that could not execute the path are
marked PLAUSIBLE rather than promoted.

**Default to refuted.** Eleven of 63 findings did not survive, and six of those were duplicates
that a counting-oriented process would have shipped as six separate bugs. The refutations also
caught two cases where "fixing" the finding would have made the tool worse.

**Batch the work.** The first attempt at this verification died of a session limit trying to do
all 63 at once, losing 55 agents and the synthesis. Five at a time, sequentially, finished the
same work with nothing lost. The batch size is the only reason this document exists.

## Reproducing it

**What you can run, and it is the part that matters.** The replica this was executed against is a
committed script, and so is the acceptance matrix over it:

```bash
scripts/replica-ui.sh <sandbox>      # rebuilds the fixture from scratch
scripts/matrix-replica.sh <sandbox>  # 41 assertions over it
scripts/matrix-demo.sh <sandbox>     # 26 over the from-zero adoption path
```

That is deliberate, and it came out of this exercise: the matrix used to be prose in a document
with eight numbered tests, while the counts being reported (11/11, then 12/12) matched no written
list — the newer tests lived in the memory of whoever had run them. An acceptance matrix nobody
else can re-run exactly is not a matrix, it is a recollection.

**What you cannot, and why it is said rather than quietly omitted.** The raw material — 63 verdicts
with per-finding reproductions, verbatim evidence and positive controls, plus the 80 findings as
filed and the per-dimension returns — was in `docs/field-test/`. It was **retired from the tree
before the first tag**: it described the private repository the replica was modelled on, not the
replica, and a document about someone else's codebase is not publishable at any amount of
rewriting. It is in git history, which is where a decision to stop publishing something belongs
rather than in a purge that would invalidate every commit SHA this project cites.

So the honest statement of what survives: the analysis is this document, the findings' status is in
[CHANGELOG.md](../CHANGELOG.md) under *Known gaps* (45 fixed, 7 open, each open one named), and the
executable evidence is the three scripts above. The per-finding repro steps are not public.

**If you run any of it yourself:** export `BATTEN_DB` into your sandbox before every batten command.
`dbPath()` falls back to the real database the moment it is unset, and a field test that
contaminates the user's own vault has failed before it started.
