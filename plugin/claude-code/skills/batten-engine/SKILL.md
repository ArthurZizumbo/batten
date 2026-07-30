---
name: batten-engine
description: Run a phase of the workflow declared in this repo's batten.yaml — research, plan, build with fan-out, verify, fix, close. Use when the user names a phase and a work item ("Fase 3 US-034", "plan the ticket", "build US-012"), when they ask to run, resume, or continue a work item, or when they ask how this project's workflow works. Reads batten.yaml; never assume a workflow that is not declared there.
---

# batten-engine

`batten.yaml` is this project's process, as data. Read it before doing anything — it tells you the
phases, the domains, the checks, the invariants, and what gates a close. **Do not invent a workflow
that is not in it, and do not skip what is.**

```bash
command -v batten >/dev/null 2>&1 || { echo "batten: the binary is not installed — nothing is being gated. Start a new session (SessionStart installs it), or run \$CLAUDE_PLUGIN_ROOT/scripts/bootstrap.sh (Windows: bootstrap.cmd)." >&2; exit 1; }
batten doctor          # is there a spec? what capabilities are live?
batten runs            # what is in flight
batten show <unit>     # where one unit stands: phase, write-sets, verdict, budget
```

## The commands

| command | phase |
|---|---|
| `/batten-init` | write `batten.yaml` (optionally `--from` a workflow already written in prose) |
| `/batten-plan` | decide the domains, the parallel sub-tasks, and their **disjoint write-sets** |
| `/batten-build` | the fan-out: one subagent per domain and per sub-task, each fenced to its write-set |
| `/batten-verify` | the gate: check the diff against the criteria, emit a verdict with cited evidence |
| `/batten-close` | provenance, resolution artifact, and the commit that the gate must allow |
| `/batten-night` | unattended: build → verify → fix → re-verify, stopping before the close |

The phase *names* are the user's — they come from `batten.yaml`. These commands read the spec and
run whichever phase matches; they do not hardcode a workflow.

## Running a phase

```bash
batten phase US-034 build
```

That records the phase and, when the phase declares `anchor: git_sha`, the base SHA. **Every later
phase diffs from that anchor, never from `HEAD~N`** — that is what keeps the scope right when work
interleaves or gets rebased.

Then do the phase's actual work, governed by the spec:

- **`reads:`** — load those artifacts first. They are the phase's inputs.
- **`graph_query: true`** — answer "what already exists?" with the code graph, not by grepping and
  reading files. If `capabilities.graph.provider` is set and on PATH:
  `graphify query "what already handles X"`. If it is absent, fall back to grep — the spec degrades,
  it does not fail.
- **`gate:`** — the phase must end with a verdict envelope (see the `batten-verdict` skill).
- **`fanout: true`** — see below. This is the interesting one.

## The fan-out phase

This is where the workflow earns its keep, and where it is easiest to get wrong.

1. **Read the plan artifact.** It lists the domains and, for a large unit, the parallel sub-tasks
   within one domain, each with its own **disjoint write-set**.

2. **Launch one subagent per domain, plus one per parallel sub-task.** Give each agent:
   - its domain's `rules` file (`AGENTS.md`), first thing it reads
   - its domain's `invariants`, **verbatim** — copy them across, do not summarize them. They are in
     the spec because paraphrasing is how they get lost.
   - its domain's `skills`
   - **its exact write-set** and nothing else
   - its domain's `check` commands, to run before finishing

   When a domain declares `agent:`, launch it as **that** subagent type — the user's own custom
   agent. The DAG, the `.canvas` and the hook matchers then show their agent's name instead of
   `general-purpose` seven times over.

3. **Claim each write-set as the agent starts:**
   ```bash
   batten claim <agent-id> <file> <file> ...
   ```
   From that moment, any *other* agent that tries to write those files is **denied by the hook**.
   The disjointness rule stops being discipline and becomes a constraint. If a claim fails with a
   collision, **the plan is wrong** — fix the plan; do not work around the fence.

4. **Sequence what actually depends.** If sub-task C needs the output of A and B, it runs *after*
   them, not in parallel. Parallelism is for disjoint work, not for hopeful work.

5. **Respect declared resources.** A domain with `resources: [gpu]` contends for something scarce.
   Probe it first (`resources.gpu.probe`), and when capacity is short, queue by
   `resources.gpu.priority`. Do not launch two jobs that do not fit.

## Closing

The phase with `requires_verdict: ok` is gated **by a hook, not by good intentions**. A `git commit`
without an `ok` verdict carrying non-empty evidence **is denied**. That is the point. If you find
yourself wanting to skip it, you are the reason it exists.

If a commit is denied, read the reason and do what it says. The escape hatch exists but is recorded:

```bash
batten override US-034 --reason "..."
```

## Seeing the run

```bash
batten canvas US-034     # writes a JSON Canvas of the real path: fan-out, retries, verdict
```

Open it in Obsidian. It shows what actually happened, not what the plan hoped for.

## Budget

The tripwire an unattended run otherwise lacks. With `on_exceed: block`, a run that blows a ceiling
**cannot commit** — the same hook that guards the verdict guards this.

```bash
batten budget            # or: batten budget <unit>
```

On a subscription the marginal cost of a token is zero, so "dollars spent" is the wrong ceiling.
Three honest ones replace it:

| ceiling | what it means |
|---|---|
| `tokens_per_run` | exact. The only thing that can be counted for certain — all five buckets, parent **and** every fanned-out subagent (they live in separate transcript files; counting only the parent badly undercounts a fan-out, which is precisely our case). |
| `imputed_usd_per_run` | what those tokens **would have** cost on the API. Not a bill — a measure of the value pulled out of the subscription. |
| `quota_pct_per_run` | share of the rolling 5-hour window. Anthropic publishes no absolute quota numbers, so a **percentage is the only trustworthy quota metric**. |

`quota_pct` is readable only through the status line, so it needs `batten statusline` installed.
Without it, `batten budget` reports that ceiling as **NOT MEASURABLE** — it does not report it as
0%, and neither should you. An unmeasured ceiling is an unenforced one, and the honest move is to
say which.

> **Never invent a number.** If something could not be measured, report it as unavailable. A budget
> that quietly reports 0 for what it failed to measure is worse than no budget, because it will be
> believed.

## Bootstrapping a repo that has none

If `batten doctor` finds no spec, use `/batten-init`. If the project already has its workflow written
down in prose, point at it: `/batten-init --from docs/workflow.md`. Migrating a written process should
cost a command, not an afternoon.
