---
description: Build a work item by fanning out one subagent per domain and per parallel sub-task, each fenced to a disjoint write-set
---

Build the work item named in $ARGUMENTS. **You are an orchestrator, not an implementer.** You read
the plan, launch the agents, keep them off each other's files, and integrate what comes back. You do
not write the code yourself.

Everything project-specific comes from `batten.yaml`: the domains, their rules, their invariants,
their check commands, their agents, and the scarce resources they contend for. Read it first.

## 1. Anchor the diff — before a single file changes

Find the phase in `phases[]` with `fanout: true` and enter it:

```bash
batten phase <unit> <build-phase-id>
```

If the phase declares `anchor: git_sha`, this records the base SHA **now**, before any edit. Every
later phase diffs from that anchor rather than from `HEAD~N`. That is what keeps the review scope
correct when the work interleaves with someone else's commits, or gets rebased. Do this first, or
the verify phase reviews the wrong set of files and nobody notices.

### If another unit is already open, give this one its own tree

```bash
batten worktree <unit>      # creates the tree, the branch, and anchors the unit where it diverged
```

One tree **per unit**, and the anchor becomes the point that tree branched — which is exactly the
diff the verify phase should be reading. From then on the write-set guard knows the two units are
in different checkouts and stops treating `api/handler.go` in each of them as one contested file.

**Do NOT give each fanned-out subagent its own worktree.** Your Agent tool has
`isolation: "worktree"` and using it here breaks the fan-out: the agents of one unit work on
**disjoint write-sets of the same tree** — that is the premise the whole design rests on — and
isolating them from each other means none of them sees the others' work, plus N merges for one work
item. The isolation that matters is between concurrent *units*.

The way back in is gated exactly like a commit:

```bash
batten worktree <unit> --merge     # refused unless the unit has BOTH verdicts
```

## 2. Read the plan

Load the plan artifact (`artifacts[...]` with `{id}` substituted, per the phase's `reads:`). It
already contains the decisions you are about to execute: the marked domains, the parallel sub-tasks,
each agent's **exact write-set**, what is sequential, and each resource budget.

**If the plan does not list write-sets, stop.** Go back and plan. A fan-out without declared
write-sets is the failure this phase is built to prevent, and launching one "just this once" is how
you find out at 3am.

## 3. Probe the resources — before launching, not after

For every `resources:` entry any marked domain contends for:

```bash
# run resources.<name>.probe verbatim; it prints free capacity in resources.<name>.unit
```

Compare free capacity against the budgets the plan wrote down.

- **Everything fits** → launch them in parallel.
- **It does not fit** → queue by `resources.<name>.priority`, in that order. That list exists
  precisely so this decision is not made ad hoc by whoever launched first.
- **The probe fails or the resource is unreachable** → capacity is **unknown**. Unknown is not zero
  and it is not infinite. Do not launch a job that assumes capacity; say the probe failed and let
  the human decide.

A `kind: mutex` resource takes one holder at a time — no sharing, no "it'll probably be fine".

## 4. Launch the fan-out

**One subagent per marked domain, plus one per parallel sub-task with a disjoint write-set.**

Use `domains[<name>].agent` as the subagent type when the domain declares one — that is the user's
own custom agent, and using it means the DAG, the canvas and the hook matchers all show *their*
agent's name instead of `general-purpose` seven times over. If no `agent:` is declared, use a
general-purpose subagent.

### Orientation, before a single file is read

If `capabilities.graph.query_before_read` is true — or the phase declares `graph_query: true` —
every agent's prompt carries this, in this order:

1. **`capabilities.graph.provider`** (graphify): *what the code IS.* `graphify affected "<the thing
   this agent owns>"` and `graphify god-nodes --json`. Ask it before opening a file.
2. **`capabilities.memory.provider`** (engram): *what we DECIDED.* `mem_search` for this area — the
   constraint somebody already hit and wrote down.
3. **grep** — only as the fallback. It is the most expensive of the three and it answers the other
   two questions badly.

The order is not decoration: this is the phase that WRITES code, and until now it was the only one
that consulted nothing. `/batten-plan` asks both memories, `/batten-verify` asks one,
`/batten-close` writes to one. The fan-out went straight to reading files.

**And the part that makes it more than a suggestion:** if neither memory answered — not installed,
empty, timed out — the agent must **say so explicitly in its return**. An agent told to consult two
tools will report having consulted them whether or not they answered, and that is the single most
likely way this instruction fails. An honest *"graphify was not available, I grepped"* is worth more
than a claimed consultation, because the claimed one poisons every judgement downstream.

`batten doctor` checks the other half: `query_before_read: true` with graphify not on PATH is a
warning, because an instruction to consult a tool that is not installed is worse than no instruction.

### What each prompt must carry

Each agent's prompt must carry, and carry *completely*:

- **Its domain's `rules` file** — it reads that FIRST, before writing anything.
- **Its domain's `invariants`, verbatim.** Copy them across character for character. Do not
  summarize them, do not "improve" them. These are the rules a reviewer would catch and a
  distracted agent would break; they are in the spec precisely because paraphrasing them is how
  they get lost.
- **Its domain's `skills`** — to load before starting.
- **Its exact write-set**, and the instruction that these files and no others are its to write.
- **Its domain's `check` commands** — it runs them before declaring itself finished.
- Whatever the plan says it reuses, so it extends instead of duplicating.
- If it touches a resource: the probe result, its VRAM/lock budget, and the priority order.

Then have each agent **claim its write-set as it starts**:

```bash
batten claim <agent-id> <file> <file> ...
```

From that moment, any *other* agent writing those files is **denied by a hook**. The disjointness
rule stops being discipline and becomes a constraint — which is the entire point, because
discipline is what fails at hour six of an unattended run.

## 5. When a claim collides, THE PLAN IS WRONG

If `batten claim` reports a collision, or an agent's Edit is denied because the file belongs to
someone else, **you have found a planning bug, not an obstacle.**

Do exactly this:

1. **Stop that agent.** Do not let it keep going around the fence.
2. Work out which agent should genuinely own the file.
3. **Fix the plan**: merge the two sub-tasks into one, or move the file into the right write-set,
   or make the pair sequential.
4. Re-launch from the corrected plan.

Do **not**: hand the file to a second agent, ask for an override, retry the write, or "just this
once" edit it yourself. The fence is load-bearing. Two agents editing one file produces work that
looks finished and is silently half-overwritten — that is the exact failure being prevented, and
the denial you just got is the system working.

## 6. Sequence what actually depends

Sub-tasks the plan marked SEQUENTIAL run **after** the ones they consume, not alongside them.
Wait for A and B to finish, verify their outputs actually exist, and only then launch C with the
real artifacts in hand.

Parallelism is for disjoint work. Launching a dependent task early does not make it finish
earlier — it makes it finish wrong.

## 7. Integrate

When the agents return:

**First, contrast what happened with what was declared:**

```bash
batten scan-diff <unit>
```

It asks git what changed and the database who claimed what. This is the check that does not depend
on reading shell commands: a code generator, a Makefile target, a python script or any third-party
tool leaves writes behind that no command parser can see, and this one sees them anyway because it
never looks at commands.

Two things to read in its output:

- **Files changed that nobody claimed.** batten will not guess whether that was you integrating or
  an agent going around the fence — look at each one. If an agent wrote outside its write-set, that
  is the planning bug from §5 arriving late.
- **Over-declaration**: paths claimed and never touched. Report the number. A plan that fences three
  times what it needs is fencing badly, and nobody has this measurement.

- Resolve the cross-domain seams — the shared schemas, the API types the frontend consumes, the
  function signatures two domains both call. These are where independent agents disagree, and they
  disagree *plausibly*, so look rather than assume.
- Run each touched domain's `check` commands and get them green.
- Launch a final tests agent over the unit's diff (`git diff --name-only <anchor>`), covering the
  new and modified files and honoring each domain's `coverage` floor. A test that reproduces a bug
  the unit fixed is worth five that assert the happy path.
- If `capabilities.memory.provider` is set, save the run's real technical decisions — the ones with
  a reason attached, not the summary.

## 8. Report

- Per agent: its write-set, its check results, red or green.
- **Per agent: which memory it actually consulted, and which one was NOT available.** Not "consulted
  the graph" — either the query and what it returned, or the plain statement that it could not.
- What each agent reused instead of writing fresh.
- Cross-domain conflicts found at integration, and how they were resolved.
- Any resource contention, and how it was queued.
- **Any write-set collision, and the planning bug behind it.** Do not quietly fix and move on — a
  collision means the plan was wrong, and the plan is a durable artifact that will be wrong again.
- **`batten scan-diff`'s two numbers**: files changed that nobody claimed, and how much the plan
  over-declared.
- `git status --short`.

Do not commit. The gate phase runs next, and it is what decides whether this is finished.

## Model per agent (if `models` is set)

Launch each subagent on the model the plan assigned it. Precedence: the domain's `model:` (if
set) > the sub-task's tier from the plan > the session default. Pass it as the Agent tool's
`model` parameter; if the domain names a custom `agent:`, that agent's own frontmatter model
applies unless you override it. This is where the routing actually saves tokens — the mechanical
sub-tasks must not silently run on the planning model.

batten records the model each subagent really used (from the transcript). After the run,
`batten show <unit>` flags any node whose real model differs from what its domain declared, and
`batten measure` breaks spend down by model — so the routing is verified, not just intended.
