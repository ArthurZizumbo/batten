# batten — what it is, what works, what's next

> The living document. [README.md](README.md) is the pitch; [DESIGN.md](DESIGN.md) is the
> reasoning behind the shape. **This file is the honest inventory**: what is proven, what is
> merely built, what is missing, and what we have not decided yet.
>
> Rule for this file: **nothing is listed as done without evidence.** "Built" and "verified"
> are separate columns because they are separate facts, and conflating them is how a project
> starts lying to itself.
>
> Last updated: **2026-07-27**.

---

## The one-paragraph version

A coding agent has three memories. **Structural** — what the code *is* — belongs to
[graphify](https://github.com/Graphify-Labs/graphify). **Episodic** — what we *decided*, and why —
belongs to [engram](https://github.com/Gentleman-Programming/engram). The third is **procedural**:
*how we work.* Nobody had it. It lives in prose — a `CONTRIBUTING.md`, a 700-line prompt file — and
prose cannot stop a `git commit`. batten turns that prose into a `batten.yaml` a Go binary
**enforces** through Claude Code hooks. The rules stop being advice and become denials.

The governing constraint, which decides every scope argument:

> **If a hook cannot enforce it or a command cannot run it, it does not go in `batten.yaml`.**

---

## Verified — proven with evidence, not asserted

| Capability | The evidence |
|---|---|
| **Verdict gate** | `git commit` without a verdict → `DENY`. With `result: blocked` → `DENY`. With `ok` + evidence → `ALLOW`. An envelope with an empty `evidence[]` is refused by the binary before it reaches the database |
| **Write-set guard** | Agent B editing agent A's file → `DENY`, naming the owner and B's own write-set. Enforced by a `PRIMARY KEY (run_id, path)`, so disjointness is a database constraint, not advice |
| **Subagent token accounting** | 21k tokens counted = parent 6k (duplicate discarded) + subagent 15k (separate transcript file). A naive parser loses 71%. Verified against a real 1.9 MB transcript |
| **Honest budget** | Three ceilings (tokens / imputed USD / % of the rolling quota). An unmeasurable ceiling reports `NOT MEASURABLE`, never a fabricated zero |
| **Multi-session** | Session↔run binding; write-sets defended *between* open runs; ambiguity surfaced rather than guessed. Verified with US-034/sessA vs US-051/sessB |
| **Neutrality** *(blocking criterion for v1)* | The same binary ran on an ML repo (7 domains, H100, MLflow) and a TS webapp (`TCK`, frontend/api). Only `batten.yaml` changed |
| **Windows hooks** | Exec-form `${CLAUDE_PLUGIN_ROOT}/bin/batten` **without** `.exe` resolves on Windows 11. This was an open design risk; it cost zero changes |
| **Hook latency** | 17 ms median fast-path, against a <50 ms budget |
| **Real dogfood** | The plugin installed and governing this repo. E0 spike 5/5. **7 bugs found by using it** (`2674521`, `a0fb8f1`, `b2c465c`, `face412`, `3c33dc5`, `28fe87b`, `e02670f`) |
| **Built by its own flow** | TASK-2 was planned, fanned out to 2 subagents with disjoint write-sets, verified and closed through batten's own gate — `abea1ff` |
| **Code graph live** | graphify 0.9.25 wired in: 1043 nodes / 2100 edges / 65 communities. God nodes from the report raise the plan phase's difficulty tier — touching a god node is never "mechanical" |

## Test coverage, honestly

Every package has tests except `internal/tui`, a read-only Bubbletea viewer. Total **52.7%**, and
the distribution matters more than the number:

| package | coverage | why this level |
|---|---|---|
| `internal/spec` | 94.9% | the parser every other package depends on, and the thing an unfamiliar repo feeds unfamiliar YAML |
| `internal/usage` | 94.2% | subagent token accounting — a naive parser loses 71% |
| `internal/vault` | 92.1% | |
| `internal/canvas` | 86% | the format's reader is Obsidian, which fails silently |
| `internal/mcp` | 86% | |
| `internal/export` | 84.8% | |
| `internal/store` | 62.5% | the guarantees are covered; the reporting queries are not |
| `internal/hooks` | 29.5% | the two denials and the guard's asymmetry are covered; the event plumbing is not |
| `cmd/batten` | 8% | mostly flag parsing and console output; the decisions in it are covered |
| `internal/tui` | 0% | a terminal viewer that reads the store and renders it |

Writing these found four real bugs, which is the argument for them: a run-id collision on Windows,
a non-deterministic "latest run", a gate that passed in silence when it could not verify anything,
and a `doctor` hint pointing at a command that no longer exists.

## Built, not yet proven

Honest status: the code exists and its unit tests pass, but no run outside this repo has exercised it.

- **Install into a repo that is already mid-development.** The whole point, and it has only ever
  happened here. See *The firing test* below.
- **`--from` migration from a prose workflow doc.** Implemented; never run on someone else's doc.
- **Interactive TUI.** Compiles, tested, never opened in a real terminal by a second person.
- **GoReleaser release path.** The workflow exists; no tag has ever been pushed.

## Not built

- **headroom integration.** `capabilities.compression.measure` counts tokens per node already, so the
  comparison is *measurable* — but headroom itself is not installed and the comparison has never run.
- **`graphify hook install`** for graph auto-rebuild on commit.
- **Doc→code edges in the graph.** The semantic layer produced 84 documentation nodes that link to
  `.yaml` and to each other, but **zero `.md`↔`.go` edges**. The knowledge layer and the AST layer
  sit side by side without touching. This is a graphify-side limit, not a batten one.

---

## The firing test

Everything above is prologue until this works, on a repo that is not this one:

```
1. /plugin marketplace add <repo>       # once per machine
2. /plugin install batten@batten        # the binary arrives on its own
3. /batten-init                          # interviews the repo; proposes batten.yaml
4. batten statusline --install           # optional: enables the quota ceiling
5. batten doctor                         # green → working
```

— and it must enter **without breaking the sprint**. Gates start at `enforcement: report` (they
warn, they do not block) and harden to `enforce` when the team trusts them.

**Next target: the private repo this was designed against** — the fixture `scripts/replica-ui.sh`
rebuilds. It is the ideal case *and* the hard case, because **it already has
a harness** — one considerably richer than expected: an `AGENTS.md` at the root and one per domain
(`backend/`, `db/`, `frontend/`, `ml/`), a `CLAUDE.md`, **47 skills and 9 custom agents** under
`.claude/`, and a formal backlog of `US-001`..`US-0NN` in `context/planeacion_proyecto.md`. batten
must read what is there and complement it — never overwrite a process that already works.

Two things about it shape the test rather than being incidental to it. It has **no code yet**:
every domain directory holds only its `AGENTS.md`, so there are no build files and therefore no
checks, which means the gate starts out verifying nothing and has to say so. And it has **one
branch**, so there is no branch-name history to learn a unit convention from — the convention is
in the backlog instead.

---

## Ordered next steps

1. ~~**`batten-init` must complement, not impose.**~~ **Done.** The scan reports the harness already
   in place, the stack from marker files, and where the repo describes itself; the command reads
   those first and then interviews the human instead of guessing quietly. Running it against that
   repo — planned in full but not yet built — is what found two bugs: a domain list
   that came back empty because it demanded code, and a note claiming checks came from build files
   that did not exist.
2. **Prove the firing test on that repo.** The scan now reads it correctly, and getting there
   took four fixes it surfaced: a domain list that came back empty because it demanded code, a note
   claiming checks came from build files that did not exist, a unit convention read from branch
   names when it was written in the backlog, and skill suggestions matched against prose rather
   than names. It now proposes `US`/`US-\d{3}` with the right plan document and locator, four
   domains from their `AGENTS.md`, defensible per-domain skills, and an honest "NO check command
   was found — the gate currently verifies nothing".

   What remains is the part no scan can do for you: filling the invariants from those five
   `AGENTS.md` files, and running a real work item end to end. See `docs/FIELD-TEST.md` for what a
   sandboxed run of the whole surface actually found.
3. **First public release.** The placeholder is gone: the module path, both bootstrap scripts, the
   plugin manifest, the marketplace entry, the schema `$id` and the install docs all point at
   `ArthurZizumbo/batten`. What remains is creating the repo, pushing, and tagging `v0.1.0` —
   GoReleaser builds every platform from there, and `release.yml` now refuses to start until the
   suite passes on Linux, macOS and Windows.
4. **headroom**, once there is a second real repo to measure it on. Measuring compression on one
   repo tells you about that repo.

---

## Open questions — the part to argue with

This section exists so the naming and scope decisions are visible instead of accreting by default.
None of these are settled.

### Names

| Today | The tension | Options |
|---|---|---|
| **The local directory is `LoopWorkFlow`, the product is `batten`** | ~~Decided:~~ the module path, the plugin, the marketplace entry and every doc now say `ArthurZizumbo/batten`, so the GitHub repo must be named `batten`. The local folder can keep its working title; nothing reads it | — |
| `unit` | The spec says `unit`, the CLI says `--unit`, the docs say "work item", and users say "ticket" or "US" | keep `unit` as the schema term and stop saying "work item" in prose; or rename to `item` |
| `verdict envelope` | Precise and ours, but nobody arrives already knowing it | keep it — the precision is the point, and `gate result` invites the vagueness the gate exists to kill |
| `capabilities.compression` | Named after the mechanism (compression), not the goal (headroom) | rename to `capabilities.headroom`? but then it is named after a vendor |
| `enforcement: report \| enforce` | Is there a third mode — enforce for some gates, report for others? | per-gate `enforcement:` overriding the global one |
| `batten claim` | The command claims a write-set; the noun `claim` never appears in the spec | align on one word — `writeset` in both, or `claim` in both |

### Scope

- **Does batten own the resolution artifact, or just require one?** Today `/batten-close` writes it.
  That is the most opinionated thing in the whole product, and it is the least enforceable.
- **Should `batten.yaml` support per-domain budgets?** An ML domain and a docs domain do not deserve
  the same ceiling. Against: every knob added to the spec is a knob someone must maintain.
- **Should the dogfood spec live in the repo at all?** `batten.yaml` at the root declares a vault
  under `~`, which is portable enough to publish, but it is still one person's setup presented as
  the project's. It is a real example, which is useful, and it is someone's machine, which is not.

---

## What batten deliberately does not do

The crowded parts of this space are crowded with good tools.

- **It does not store episodic memory.** That is engram's job.
- **It does not build a code graph.** That is graphify's job — tree-sitter, deterministic, zero LLM
  tokens for the structural layer. batten queries it when present and greps when absent.
- **It does not re-orchestrate the agent.** Claude Code's dynamic workflows already run the fan-out
  well. batten governs it — the rails, not the engine.
- **It does not compress context.** If headroom helps *your* fan-out, use it; batten counts tokens
  per node so you can find out rather than trusting a README.
- **It cannot change the model of the main loop.** That is the user's `/model`, and no plugin
  reaches it. Model routing therefore applies to **subagents only** — the Agent tool takes a `model`
  parameter and custom agents carry one in their frontmatter. What batten adds is the half nobody
  else has: the ledger records which model each node *actually* used, so `batten show` can flag
  where the declared routing and the real one diverged. Declaring a tier for the main loop would be
  a wish; verifying the fan-out's is a fact.

## Principles that do not bend

1. **Never invent a number.** What cannot be measured is reported as unmeasurable.
2. **Degrade, never break.** A hook with no spec, no binary, or malformed input no-ops silently. It
   never takes down a session.
3. **Fail open only out loud.** If the gate cannot attribute the work, it does not block — and it
   says so.
4. **An override goes in the log.** Always.
