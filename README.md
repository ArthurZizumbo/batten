# batten

**A workflow written in prose cannot enforce itself.**

Every serious team writes its process down. Phases, review gates, "two agents never touch the same
file", "never approve without evidence". It goes in a `CONTRIBUTING.md`, or a 700-line prompt file,
and it gets pasted into each new session.

Then an agent ignores it, and **nothing happens.** No error, no denial. The rule was a sentence in a
document, and a sentence cannot stop a `git commit`.

The two rules that matter most are exactly the two that fail this way:

> *"Never close a work item with `result: ok` and an empty `evidence[]`."*
> *"Two agents must never write the same file."*

Both are pleas. A distracted agent breaks the first one because the code *looks* fine, and you find
out in production. It breaks the second one at hour six of an overnight fan-out, and you find out in
the merge.

batten turns that document into **data** — a `batten.yaml` your repo declares — and then **enforces
it with Claude Code hooks.** The rules stop being advice and start being denials.

---

## The product is two denials

Everything else in this repo exists to serve these.

### 1. The verdict gate

An agent finishes a work item, feels good about it, and reaches for the commit:

```console
$ git commit -m "feat: add order rate limiting"

  PreToolUse:Bash [batten] permission denied

  batten: US-034 has no verdict envelope. Run the "qa" phase before committing.
  To proceed anyway (recorded in the audit log): batten override US-034 --reason "..."
```

It runs the QA phase, and tries to approve with nothing to point at:

```console
$ echo '{"check_id":"US-034-qa","result":"ok","evidence":[]}' | batten verdict --unit US-034

  batten: result "ok" with an empty evidence[] is not allowed: an approval must cite
  something (command output, test counts, a criterion verified). Without evidence the
  result is "blocked"
```

Rejected by the binary, before it ever reaches the database. And if a verdict somehow got in by
another path, the commit hook catches it independently.

**This is the one failure batten exists to kill: closing a work item because it *looks* fine.**

A verdict that cites three real things gets through, and only then:

```console
$ batten verdict --unit US-034 --file verdict.json
verdict US-034-qa: ok (3 evidence)

$ git commit -m "feat: add order rate limiting"
[feature/US-034 8f2a1c9] feat: add order rate limiting
```

### 2. The write-set guard

A fan-out is running. Four subagents, disjoint write-sets, each one claimed at launch with
`batten claim`. Agent `ml/A` — which owns the data loader — decides it also needs to fix the
trainer, which belongs to `ml/C`:

```console
  PreToolUse:Edit [batten] permission denied

  batten: write-set collision. ml/farslip/train.py belongs to another agent's write-set
  (node-7f3a/ml-C); you are node-2b91/ml-A.
  Two agents must never write the same file — that is what makes the fan-out safe.
  Your write-set:
    ml/data/pastis_filter.py
    tests/ml/data/test_pastis_filter.py
  If this file genuinely belongs to you, the plan is wrong: fix the plan, do not cross the fence.
```

Not a merge conflict discovered at 3am. A denied `Edit`, five seconds in, naming the file, the
owner, and what to do about it.

And note what the message refuses to do: it does not offer a way through. **If two agents both need
that file, the plan was wrong** — the fix is to merge the sub-tasks or sequence them, not to open
the fence. The fence is the feature.

---

## What goes in `batten.yaml`

Your process, as data. The engine knows nothing about your project — it reads `check` and runs it;
it has no idea what `pytest` is.

```yaml
version: 1
project: acme

unit:                                  # the work-item noun YOU already use
  name: US
  pattern: 'US-\d{3}'                  # found in branch names, prompts, commits
  plan: docs/backlog.md

artifacts:
  planning: docs/us-planning/{id}.md   # {id} is required — else every unit
  resolved: docs/us-resolved/{id}.md   # would overwrite the same file

phases:                                # the state machine. Names are yours.
  - id: plan
    graph_query: true                  # ask the code graph "what already exists?"
  - id: build
    fanout: true                       # one subagent per domain + per sub-task
    reads: [planning]
    anchor: git_sha                    # record the base SHA: every later phase
  - id: verify                         #   diffs from HERE, never from HEAD~N
    gate: qa
    diff_from: anchor
  - id: close
    requires_verdict: ok               # THE HARD GATE — a hook, not a convention

domains:                               # the fan-out axes
  backend:
    path: backend/
    rules: backend/AGENTS.md           # the agent reads this FIRST
    check: ['make lint', 'make test']  # verbatim from your Makefile
    coverage: 70
    agent: acme-backend-dev            # your own custom subagent, if you have one
    invariants:                        # ride VERBATIM into every agent's prompt
      - session_id in every query
      - logic in the service, never in the router

resources:                             # scarce things that force serialization
  gpu:
    kind: exclusive_pool
    probe: 'nvidia-smi --query-gpu=memory.free --format=csv,noheader,nounits'
    priority: [train, distill, eval]   # the order when they don't all fit

gates:
  qa:
    checks: ['make check', 'make test']
    verdict: required
    evidence: required                 # empty evidence[] -> blocked. Non-negotiable.

budget:
  tokens_per_run: 3_000_000
  imputed_usd_per_run: 8.00
  quota_pct_per_run: 15
  max_iterations: 3
  on_exceed: block
```

The **invariants** are the highest-value lines in the file. They are the rules a reviewer would
catch and a distracted agent would break, and they are copied character-for-character into every
fanned-out agent's prompt.

The governing rule for what belongs here:

> **If a hook cannot enforce it or a command cannot run it, it does not go in `batten.yaml`.**

Prose that survives that test becomes data. Prose that does not — "think carefully about the
architecture" — stays prose, and that is fine. Over-declaring turns the spec into a DSL nobody
maintains.

There is a JSON Schema at [`batten.schema.json`](batten.schema.json), so your editor completes and
validates the file as you type.

### About the budget

On a subscription, the marginal cost of a token is **zero** — so "dollars spent" is the wrong
ceiling. Three honest ones replace it:

| ceiling | what it is |
|---|---|
| `tokens_per_run` | Exact. The only thing that can be counted for certain — and counted across the parent **and every subagent**, because they live in separate transcripts and counting only the parent badly undercounts a fan-out. |
| `imputed_usd_per_run` | What those tokens *would have* cost on the API. **Not a bill** — a measure of the value you are pulling out of the subscription. |
| `quota_pct_per_run` | Share of the rolling 5-hour window. Anthropic publishes no absolute quota numbers, so a **percentage is the only trustworthy quota metric.** |

```console
$ batten budget
US-034  1.4M tokens, $3.40 imputed
  · tokens       1.4M / 3.0M  [=====.......]
  · imputed_usd  $3.40 / $8.00  [=====.......]
  · quota_pct    NOT MEASURABLE — install the statusline (`batten statusline --install`)
```

That last line is deliberate. The quota is exposed **only** to the status line, so without it
batten cannot see it — and it says so rather than printing `0%`.

**batten never invents a number.** A budget that quietly reports zero for what it failed to measure
is worse than no budget at all, because it will be believed.

---

## Install

```
/plugin marketplace add ArthurZizumbo/batten
/plugin install batten
```

**The binary arrives on its own.** A `SessionStart` hook runs `bootstrap.sh`, which fetches the
static binary for your platform from the GitHub Release into `${CLAUDE_PLUGIN_DATA}/bin` — a
location that survives plugin updates. A dev build (`scripts/build-plugin.sh`) puts it in the
plugin's own `bin/` instead, and bootstrap sees it and exits. Either way the hooks and the MCP
server resolve it with no out-of-band install step. The repo itself ships `bin/` **empty**;
committed binaries bloat a repo and go stale.

This is worth explaining, because the alternative is a real and common bug: a plugin whose
`.mcp.json` invokes a **bare command name**, expecting you to have installed the binary separately
via brew or `go install`. When you haven't, the MCP server fails *silently*, the hooks fail
*silently*, and the gate that was supposed to protect you simply isn't there. batten points at the
binary absolutely (`${CLAUDE_PLUGIN_ROOT}/bin/batten`) and, if the download fails, says so and
no-ops rather than pretending to guard you.

The hooks are **exec form** — the binary reads the hook JSON on stdin. No `bash`, no `jq`, no
`curl`. **Windows is a first-class target**, not an afterthought: exec-form resolution without the
`.exe` extension is verified working on Windows 11.

Then, in your repo:

```console
$ batten init                              # interviews the repo, writes batten.yaml
$ batten init --from docs/workflow.md      # ...or migrates a workflow you already wrote in prose
$ batten doctor                            # validates the spec; reports which capabilities are live
```

`init` derives what can be honestly derived — your unit pattern from branch names, your domains from
the layout, your `check` commands **verbatim** from your build files — and leaves the rest as
explicit TODOs rather than guessing. It refuses to overwrite a `batten.yaml` that already exists.

`--from` matters more than it looks: a spec is only "general" if migrating to it is cheap. If
adopting batten costs an afternoon, nobody adopts it.

Worked specs at three depths, from 30 lines to 220, are in [`examples/`](examples/).

## The commands

| command | what it does |
|---|---|
| `/batten-init` | interview the repo (or a prose doc) and write `batten.yaml` |
| `/batten-plan` | decide the domains, the parallel sub-tasks, and their **disjoint write-sets** |
| `/batten-build` | the fan-out: one subagent per domain and per sub-task, each fenced to its write-set |
| `/batten-verify` | the gate: check the diff against the criteria, emit a verdict with cited evidence |
| `/batten-close` | provenance, resolution artifact, and the commit the gate must allow |
| `/batten-night` | unattended: build → verify → fix → re-verify, stopping before the close |

The phase *names* come from your `batten.yaml`. The commands read the spec and run whichever phase
matches — they do not hardcode a workflow.

`/batten-night` is the one to read before trusting. It never deletes anything (if it *wanted* to, it
tells you in the morning report instead), it never overrides the gate, and it stops before the
close. The budget ceilings are the tripwire an unattended run otherwise lacks — there is no human
awake to notice it grinding on the same red test until the window is gone.

## The run graph

```console
$ batten canvas US-034
```

Writes a [JSON Canvas 1.0](https://jsoncanvas.org/) file. Open it in Obsidian: the phases, the
fan-out, the retries, the blocked verdict that got fixed and re-verified. It draws the path the work
**actually took**, not the one the plan hoped for.

Zero lines of graph-layout code on our side. Obsidian already renders it.

---

## What batten does *not* do

An honest list, because the crowded parts of this space are crowded with good tools and you should
use them:

- **It does not store episodic memory.** What you *decided*, and why, over months — that is
  [engram](https://github.com/Gentleman-Programming/engram)'s job, and it is good at it. batten
  interoperates; it does not compete.
- **It does not build a code graph.** What the code *is*, right now — that is
  [graphify](https://github.com/Graphify-Labs/graphify)'s job (tree-sitter, deterministic, zero LLM
  tokens). batten queries it if you have it and falls back to grep if you don't.
- **It does not re-orchestrate the agent.** Claude Code's Dynamic Workflows already run the fan-out,
  and they run it well. batten **governs** it — the rails, not the engine. Rebuilding the
  orchestrator would be a worse orchestrator and a much worse use of everyone's time.
- **It does not compress your context.** If [headroom](https://github.com/headroomlabs-ai/headroom)
  saves tokens in *your* fan-out, use it — and because batten already counts tokens per node, you
  can find out whether it actually does instead of taking a README's word for it.

There are three memories in a coding agent. **Structural** (what the code *is*) and **episodic**
(what we *decided*) are solved. The third — **procedural**, *how we work* — was the one nobody had,
and it is the only thing batten claims.

## Optional capabilities degrade, on purpose

Everything under `capabilities:` is optional, and every one of them **degrades gracefully**:

```yaml
capabilities:
  graph:      { provider: graphify, query_before_read: true }
  memory:     { provider: engram }
  obsidian:   { vault: ~/vaults/acme, export: [runs, verdicts, canvas] }
  compression:{ provider: headroom, measure: true }
```

No graph provider? The plan phase greps. No vault? No canvas is written. No status line? The quota
ceiling reports itself as unmeasurable and the other two ceilings still bite.

**Be aware that these dependencies are pre-1.0** and move fast — graphify and headroom both ship
breaking changes, and graphify's own `PreToolUse` hook has already broken against a Claude Code
release. That is precisely why they are declared capabilities rather than hard dependencies: batten's
core doesn't import them, doesn't call them on any critical path, and doesn't fail when they're
absent or broken. A missing optional dependency should cost you a fallback, never a run.

## Status

**Pre-1.0, and dogfooded.** batten is installed in this repo and governs its own development: the
last work item here was planned, fanned out to two subagents with disjoint write-sets, verified, and
closed through batten's own gate. Using it that way is what found the last seven bugs.

The gates are verified working — a commit without cited evidence is denied, a subagent writing
another agent's file is denied — as is the part that was an open design risk: Windows hook
resolution, and whether `agent_id` reaches `PreToolUse` at all (the write-set guard hangs off that
field, and degrades to advisory *out loud* when it is absent).

What has **not** happened yet, stated plainly: no release has been tagged, and batten has never been
installed on a repo other than this one. Neutrality is verified across two very different specs, but
the five-step install into a project already mid-development is still the firing test.

The transcript format batten reads for token accounting is **not a public API** and can change
without notice; if parsing breaks, batten reports the count as unavailable rather than guessing.

The full inventory — what is proven, what is merely built, what is missing, and the naming decisions
still open — is in [ROADMAP.md](ROADMAP.md). What has landed so far is in
[CHANGELOG.md](CHANGELOG.md).

MIT.
