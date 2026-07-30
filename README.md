# batten

> **78% of agent failures are silent.** They do not raise an error — the agent reports success and
> the work is wrong. In the same measurements, **deterministic gates tripled reliability**, and they
> did it by refusing things rather than by asking an agent to be careful.
> ([arXiv:2607.07405](https://arxiv.org/html/2607.07405v1))
>
> **batten is one of those gates**, for the workflow your repo already declares.

See it before you configure anything — it builds a throwaway repo, runs the whole flow, and deletes
it. It does not touch your repository or your database:

```console
$ batten demo
```

**And it does not block you by default.** `batten init` writes `enforcement: report`: the gates
watch and warn, nothing is refused, and you flip it to `enforce` when you trust what you have been
reading. `batten report` is what you read.

---

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

**Five more have joined them since**, and they are listed here rather than buried because each one
used to be prose asking a model to behave: merging a worktree without both verdicts, deleting
anything during an unattended run, `batten override` while nobody is watching, committing during an
unattended run *even with* the verdicts in place, and exceeding the declared iteration ceiling. The
last four are the absolute rules of `/batten-night` — 112 lines of markdown, in the most dangerous
command this plugin ships, in the one place where a mistake is irreversible and nobody is awake to
catch it. That was the single loudest instance of batten not taking its own medicine.

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

So it cites three real things. That is still not enough, because a citation is also just text:

```console
$ batten verdict --unit US-034 --file verdict.json
verdict recorded: US-034 qa=ok (3 evidence)

$ git commit -m "feat: add order rate limiting"

  batten: US-034 has no batten-verified pass. The gate's checks must be RUN, not asserted.
  Run: batten check US-034
```

When the gate declares `checks:`, batten insists on running them itself:

```console
$ batten check US-034
  ✓ go build ./...
  ✓ go vet ./...
  ✓ go test ./...

US-034: OK (batten-verified). all gate checks passed (batten ran them)

$ git commit -m "feat: add order rate limiting"
[feature/US-034 8f2a1c9] feat: add order rate limiting
```

**Two verdicts, from two different producers.** `batten check` proves the declared checks ran;
the envelope proves somebody judged the work against its acceptance criteria. Neither one
substitutes for the other, and `batten check` on its own does not close a unit.

**And the criteria are data, not prose.** "Acceptance criteria" is the phrase every review gate is
written around, and for most of this project's life it existed only in sentences: `evidence` was a
flat list of strings, and nothing could say *which* criterion a given piece of evidence covered — so
"3 evidence" told you a number and not whether the work was done.

batten reads the criteria out of the document you already keep them in. `unit.plan` names your
backlog and `unit.locator` says what a work item's heading looks like (`### {id}`); entering a phase
seeds the item's criteria as rows. A verdict covers a criterion by **citing it**:

```json
{ "check_id": "US-034-qa", "result": "ok",
  "evidence": ["AC-1: curl -i shows 429 on request 11",
               "AC-2: the Retry-After header names the window",
               "go test ./...: PASS (exit 0)"] }
```

A string prefix on the format that already existed — not a new nested object, deliberately, because
handing objects to a field that expects strings is one of the findings in this repo's own defect
list. Uncited evidence is still evidence; it just does not claim to cover anything.

What that buys is a PR body that makes a much stronger claim than a list of citations:

```markdown
### Acceptance criteria — 2 of 3 covered

| # | criterion | covered by |
|---|---|---|
| AC-1 | returns 429 over the limit | ✅ `curl -i shows 429 on request 11` |
| AC-2 | the header names the window | ✅ `the Retry-After header names the window` |
| AC-3 | the limit is per API key | — **not covered**: no approving evidence cites it |
```

The uncovered row is the point. A scoreboard that shows only the green ones flatters the run, and a
reviewer reading it cannot tell the difference between finished and unexamined. Only an **approving**
verdict covers anything — a `blocked` verdict citing `AC-3` is describing what failed, and marking
it covered would invert its meaning. A work item with no criteria in the plan reports *"no criteria
seeded"*, never `0/0`: an empty list is not a satisfied list.

### 2. The write-set guard — what makes parallel fan-out safe

This one reads like a restriction and is the opposite. **Running four agents at once is only safe
if they cannot land on the same file**, and "they won't, because the plan says so" is not a
property — it is a hope that holds until hour six. The guard is what turns it into a property, and
the property is what lets you fan out at all.

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

One owner per file is a `PRIMARY KEY` in SQLite, not a paragraph asking agents to be careful. And
because a path is only a *name* for a file, the guard also asks the operating system: two names for
one file on disk — a hard link, a symlinked directory — resolve to the same owner.

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

#### The metric nobody else reports

There is a whole ecosystem measuring what agents cost — Langfuse, OpenTelemetry, ccusage,
CloudZero. **Every one of them measures API dollars.** On a subscription that is the wrong unit: the
marginal cost of a token is zero, and the thing that actually runs out is the rolling window.

Nobody on a Max plan wants to know what their afternoon "would have cost" on the API. They want to
know whether they will still have quota at five o'clock.

`quota_pct_per_run` is that number, and `batten statusline` is the only local sensor for it — the
quota is exposed to the status line and nowhere else, which is why batten can read it and a
dashboard cannot.

---

## Install

> **The marketplace path is the path.** An audit of the install route — the one thing this project
> had verified by *reading* rather than by executing — found that `bootstrap.sh` fetched into
> `${CLAUDE_PLUGIN_DATA}/bin` while every hook and the MCP server invoke
> `${CLAUDE_PLUGIN_ROOT}/bin/batten`, so a marketplace install announced success and then gated
> nothing. That is fixed, along with four more install blockers the same audit surfaced, and the
> suite now RUNS both bootstrap scripts against a real archive instead of reading them.
>
> What is still true, and is a different sentence: **no release has been published yet**, so the
> download half of this path has not been exercised against a real tag. Building from source
> remains a supported route and is what this repo runs on:
>
> ```console
> $ git clone https://github.com/ArthurZizumbo/batten && cd batten
> $ go build -o plugin/claude-code/bin/batten ./cmd/batten   # .exe on Windows
> ```

```
/plugin marketplace add ArthurZizumbo/batten
/plugin install batten
```

**The full install guide is [`docs/INSTALL.md`](docs/INSTALL.md)** — Windows without Git Bash,
building it yourself instead of downloading (`go install` included), where the state lives and why,
adopting mid-sprint, and running two sessions in parallel.

> **On Windows, expect your antivirus to complain at least once.** Defender classifies freshly
> built, unsigned Go binaries as `Trojan:Win32/*!ml` — a machine-learning verdict, not a signature.
> It happened to this project's own binary: byte-identical builds got different answers and an
> explicit rescan of the same bytes came back clean, which is what a false positive looks like.
> batten is not signed with an Authenticode certificate yet, so this can happen to you.
>
> It matters more than a scary dialog. If the binary is quarantined **after** it installs, every
> hook is pointed at a file that no longer exists, they die in silence, and `batten doctor` cannot
> tell you because doctor *is* the missing binary. The bootstrap now notices the pattern — a second
> restore from cache inside a day — and says so on `SessionStart`. If you see that message, check
> your antivirus quarantine.

**The binary is meant to arrive on its own.** A `SessionStart` hook runs `bootstrap.sh`, which
fetches the static binary for your platform from the GitHub Release. A dev build
(`scripts/build-plugin.sh`) puts it in the plugin's own `bin/` instead, and bootstrap sees it and
exits. The repo itself ships `bin/` **empty**; committed binaries bloat a repo and go stale.

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

The CLI underneath them is small enough to use directly, and
[`docs/QUICKSTART.md`](docs/QUICKSTART.md) walks the whole adoption path that way — from an empty
directory to a denied commit and back — with output captured from a real run:

Read in that order — the gate is the last thing you turn on, not the first:

| command | what it does |
|---|---|
| `batten demo` | the whole flow on a repo batten builds and throws away. Touches nothing of yours |
| `batten report` | what batten saw, and what it stopped. This is what `report` mode is for |
| `batten init` | read the repo and write `batten.yaml`, in `report` mode |
| `batten doctor` | one pass, everything it knows, with the fix beside each problem |
| `batten phase <unit> <phase>` | open or advance a run; records the anchor SHA |
| `batten verdict --file v.json` | record the reviewer's judgment, with cited evidence |
| `batten check <unit>` | **run** the gate's declared checks and record what they printed |
| `batten close <unit>` | close through the gate and release the write-set claims |
| `batten pr <unit>` | a PR body from the run record: the real DAG as Mermaid, evidence, cost |
| `batten recover <unit>` | re-anchor a run whose base moved under it (rebase, amend, pull) |
| `batten status` | the backlog against the record: every work item, its run state, its criteria coverage |
| `batten scan-diff <unit>` | the real git diff against the declared write-sets. No shell parsing, no false positives |
| `batten worktree <unit>` | one tree per work item; the merge back is gated like a commit |
| `batten unattended <unit>` | nobody is watching: four rules become denials |
| `batten iterate <unit>` | count one fix→re-verify round; refuses at the ceiling |
| `batten budget [<unit>]` | the governor: tokens, imputed $, share of the rolling quota |
| `batten measure` | spend by model, whether the optional capabilities paid for themselves, and how far the declared write-sets over-declare |

Two of those are worth a sentence each, because they close gaps the other commands could not.

**`batten scan-diff`** is the check that reads no shell. The Bash write guard has a boundary it
states out loud: it cannot see a write made by a `python` script, a Makefile target or a `go run`,
because no parser of commands reaches inside one. `scan-diff` asks git what changed and the ledger
who claimed what, and contrasts them — so a code generator is as visible as a `sed`. It refuses to
conclude two things it cannot know: *who* touched an unclaimed file (from a diff, the orchestrator
integrating and an agent crossing the fence look identical), and that a run with zero claims is
clean. Zero claims is a planning hole, and calling it clean would be the emptiest green tick there is.

Each run of it is also **recorded**, which is what turns one contrast into a measurement.
`batten measure` reports the median over-declaration across your scanned runs, with the number of
runs it was computed over and — separately, never folded in as zero — how many runs claimed paths
and were never scanned. S-Bus ([arXiv:2605.17076](https://arxiv.org/pdf/2605.17076)) measured 32–49%
over-declaration in automatically *reconstructed* read-sets; whether hand-declared write-sets do the
same is a question about your repo, and this is the only place it gets answered with your data.

**`batten status`** is the view `batten runs` cannot be. A work item nobody has started has no run
to list, and those are exactly the ones you want to see:

```console
$ batten status
backlog docs/backlog.md — 3 unit(s)

US-001  rate limit          ✓ closed ok        AC 2/2 covered
US-002  retry budget        ◐ running (verify)  AC 1/3 covered
US-003  audit log           · not started

not in the backlog: US-099 (running)
```

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

And the same graph goes into the pull request, where the people reviewing the work will actually
see it — GitHub renders Mermaid natively in a PR description:

```console
$ batten pr US-034 --out /tmp/body.md
$ gh pr create --body-file /tmp/body.md
```

The body carries the DAG the run actually took (including the retry a plan diagram cannot show),
both verdicts with their cited evidence, and what it cost. If the usage was never measured the
table says **NOT MEASURED**, not `$0.00`; if nobody reviewed the work, the badge says so instead of
claiming `batten-verified`. A generated PR that flatters the run is worse than no generated PR.

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

**It has now been run outside this repo.** A multi-agent field test put batten in front of agents
that had never seen it, on a replica of a real project and on a repo built from an empty
directory: 90 behaviours confirmed working and 80 findings, each one then handed to a second
agent whose job was to refute it. 52 survived that pass. Three were blockers, and one of those
was a regression introduced four commits earlier, in the same session, by a change I had written
and reviewed and believed.

Every blocker is fixed, each with a test that fails against the commit before it. The full
account — what broke, what was refuted and why, and what the method got right — is in
[docs/FIELD-TEST.md](docs/FIELD-TEST.md).

Since then the field test's replica was **rebuilt as a committed script**
([`scripts/replica-ui.sh`](scripts/replica-ui.sh)) and the eight fixes were re-run against it — a
repo with no code, no build files and no git history at all, which is the shape none of the
synthetic sandboxes had. Nothing previously fixed came undone. What it did turn up was two
silences and a false green, including the one that mattered most: **the very first commit after
installing batten used to get no output at all**, which is indistinguishable from approval. That
is fixed, and the matrix that found it now runs as a committed script:
[`scripts/matrix-replica.sh`](scripts/matrix-replica.sh), 41 assertions.

### Where the 52 findings stand

Those 52 confirmed findings were then worked. **45 are fixed and verified; 7 remain open.** They are
enumerated, with reproductions, under *Known gaps* in [CHANGELOG.md](CHANGELOG.md) — because a
project whose whole argument is *"never report a number you do not have"* does not get to summarize
its own defect list as "mostly done".

*(Counting rule, because this number has been wrong twice: a finding is **open** if it is CONFIRMED
in `verified.json` and has no fix at HEAD, counted once. The 7 are 6 cosmetic ones plus one declared
LIMIT — a cross-fence write through a `python` heredoc or a Makefile target, which no shell parser
reaches and `batten scan-diff` answers structurally instead. Two earlier printings said 15 and then
14: the first predated the typo fix below, and the second double-counted one finding under two
numbers.)*

The six that **blocked an outside adopter** are closed, each with a test that fails against the
commit before it — including the two that were instances of the pattern batten exists to eliminate:
a typo in a top-level `batten.yaml` key used to be ignored in silence while `doctor` printed a green
`enforcement: enforce — gates block`; and `batten claim` only checked collisions inside its own run,
so two concurrent runs in one checkout were each told they owned the same file, after which the
guard denied both.

What has **not** happened yet, stated plainly: **the plugin has never been installed from a
release**, because no release has been published — see the note at the top of *Install*. batten has
not been adopted by a project it does not belong to, with people who did not write it. The bootstraps verify the release's **sha256** before installing anything and
refuse to install what does not match, but they verify no **signature** — a compromised release
account replaces the asset and its checksum line together, so that half of the supply chain is still
open. And there is no GIF in this README — the `.tape` scripts that generate one are
written and verified ([`docs/tape/`](docs/tape/)), but the machine this was built on has no `vhs`
installed, and `batten demo` is the live version anyway.

The transcript format batten reads for token accounting is **not a public API** and can change
without notice; if parsing breaks, batten reports the count as unavailable rather than guessing.

The full inventory — what is proven, what is merely built, what is missing, and the naming decisions
still open — is in [ROADMAP.md](ROADMAP.md). What has landed so far is in
[CHANGELOG.md](CHANGELOG.md).

MIT.
