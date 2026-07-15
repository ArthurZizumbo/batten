---
description: Plan a work item — decide the domains, the parallel sub-tasks, and the disjoint write-sets the build phase will fan out over
---

Plan the work item named in $ARGUMENTS.

Everything project-specific comes from `batten.yaml`. Read it first — it names the unit, the plan
document, the artifacts, the domains and their rules. Do not assume a workflow it does not declare.

```bash
batten doctor          # is there a spec? which capabilities are actually live?
batten show <unit>     # has this unit been through phases already?
```

Resolve the unit id from $ARGUMENTS using `unit.pattern`. Then find the phase in `phases[]` that
this command serves (the one whose output feeds the `fanout: true` phase) and record it:

```bash
batten phase <unit> <plan-phase-id>
```

## 1. Read the ground, in this order

1. The unit's own block: open `unit.plan` and locate it with `unit.locator` (`{id}` substituted).
   That block holds the acceptance criteria. **They are the contract** — everything downstream is
   judged against them, so copy them out rather than paraphrasing.
2. Any artifacts this phase `reads:` — resolve each through `artifacts[kind]` with `{id}` substituted.
3. The `rules` file of every domain you expect to touch.
4. If `capabilities.memory.provider` is set, search episodic memory for this unit's topic. Somebody
   may have already learned the thing you are about to learn expensively.

## 2. Understand what ALREADY exists — before designing anything

The single most expensive mistake in this phase is planning a module that is already in the repo
under a different name.

- If the phase declares `graph_query: true` and `capabilities.graph.provider` is live, **query the
  code graph**: ask what already handles the concern. That is a couple of thousand tokens.
- If it is absent, fall back to grep-then-read. The spec degrades; it does not fail. Just know you
  are paying the re-orientation tax the graph exists to remove — so grep for the *concept*, list
  the directory, and read only what the grep actually hit.

Write down what you will **reuse or extend**, by path. A plan that does not name what it reuses is
a plan to duplicate it.

## 3. Decompose into domains and parallel sub-tasks — THE OUTPUT THAT MATTERS

This section is what the build phase consumes. Get it wrong and the fan-out either collides or
serializes for no reason.

- **Mark every `domains[]` entry the unit touches.** One subagent will be launched per marked domain.
- **Within a single domain, split independent work into parallel sub-tasks.** A large unit in one
  domain is not one agent — it is several, if and only if their file sets do not intersect.
- **Give every agent an exact write-set: a list of file paths it alone may write.**

The rule that makes the fan-out safe, and the only one you cannot bend:

> **Two agents never write the same file.**

This is not advice. In the build phase each agent calls `batten claim`, and from then on a hook
**denies** any write to another agent's file. So a write-set overlap does not become a merge
conflict at 3am — it becomes a denied Edit five seconds in. Plan accordingly.

- **If two sub-tasks share a file, they are ONE sub-task.** Merge them.
- **If sub-task C consumes the output of A and B, it is SEQUENTIAL, not parallel.** Say so
  explicitly, and say what it waits for. Parallelism is for disjoint work, not for hopeful work.
- **If a domain declares `resources:`, say what it needs from that resource** (how much VRAM, how
  long the lock). The build phase probes capacity before launching, and it can only queue sensibly
  if you wrote down the budget.

Express it so the build phase can execute it without re-deciding anything:

```
domain <D> — 4 agents
  <D>/A  loader   write-set: <dir>/loader.py, tests/<dir>/test_loader.py
  <D>/B  features write-set: <dir>/features/*.py, tests/<dir>/features/...
  <D>/E  scripts  write-set: scripts/run_pipeline.py           (imports <dir>/, edits nothing there)
  <D>/C  the core write-set: <dir>/core/train.py, <dir>/core/model.py
         SEQUENTIAL — waits for A and B; consumes their outputs
         resource: <gpu>, ~40 units of capacity for the smoke run
domain <other> — 1 agent
  write-set: ...
```

## 4. Write the plan artifact

Write to the path from `artifacts[<this phase's kind>]` with `{id}` substituted. It must contain:

1. **Acceptance criteria**, with *verifiable* measures. "Fast" is not a criterion; a latency
   number is. The verify phase will cite these, one by one, as evidence — if a criterion cannot be
   checked by running something, rewrite it until it can.
2. The architecture, and the **exact files** to create or modify.
3. Public interfaces (signatures) of every new module, so the domains can be built against each
   other's contracts without waiting.
4. **The fan-out table from §3** — domains, sub-tasks, write-sets, sequencing, resource budgets.
5. What is reused, by path (from §2).
6. The test plan, honoring each domain's `coverage` floor.
7. Risks, and what you would do about each.

## 5. Stop

Do not write implementation code. The fan-out is the next phase, and it is a separate phase because
a planner that starts coding stops planning.

Report: the domains, the agent count, which sub-tasks are sequential and why, and anything in the
acceptance criteria you could **not** turn into something verifiable — that last one is the part
people skip, and it is the part that produces an un-closable unit at the gate.

## Memory (if `capabilities.memory.provider` is set — e.g. engram)

Before planning, **search past work**: `mem_search "<unit title / the technology involved>"`. A unit
like this may have been solved before; batten governs the process, but the decisions live in engram.
Pull anything relevant into the plan rather than re-deriving it. batten does NOT store this itself —
it interoperates; episodic memory is engram's job, structural memory is graphify's.

## Model routing (if `models` is set in batten.yaml)

Classify each sub-task by difficulty so the build phase runs it on the right-sized model — a
color change does not need the model that plans architecture. Use three questions:

- touches >2 files, or crosses a module boundary? → higher tier
- requires a design decision (not just carrying one out)? → higher tier
- can it fail in a subtle, hard-to-notice way (ML, concurrency, auth)? → higher tier

Zero yes → `mechanical`; one → `moderate`; two or three → `complex`. Map each to its model via
`models.tiers`, and note the tier + model beside each sub-task's write-set in the plan artifact.
A domain with its own `model:` overrides the tier. Phases in `models.phases` (usually plan and
verify) always use their pinned model regardless of tier.
