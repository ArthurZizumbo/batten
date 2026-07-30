# AGENTS.md — working on batten

The contract for anyone changing this repo, human or model. It is short on purpose: a context file
long enough to skim is a context file nobody reads, and `batten.yaml` already carries the *process*.
This carries the *boundaries*.

`batten.yaml` is the authority on phases, gates and domains. Read it. Do not invent a workflow it
does not declare.

## What this is, and what it refuses to be

batten records what actually happened in a repo's agentic workflow and enforces the rules the repo
declared: no commit without a verdict that cites evidence, no two fanned-out agents writing the same
file, a budget ceiling, and the run graph as data.

It **does not orchestrate.** It does not choose models, spawn agents, or run the loop. When a
capability would require batten to drive the session, the answer is to *verify* instead — the
`models.*` keys were deleted from the spec for exactly this reason, because a promise batten cannot
keep is worse than a missing feature.

## Layout

- `cmd/batten/` — the single binary: flag parsing, command dispatch, `doctor`, output.
- `internal/<capability>/` — one directory per capability, named after the domain and not after a
  technical layer: `store`, `spec`, `hooks`, `mcp`, `canvas`, `vault`, `export`, `render`, `plan`,
  `statusline`, `tui`, `usage`, `scan`, `discovery`, `gitx`, `install`.
- `plugin/claude-code/` — what ships: manifests, hooks, commands, skills, bootstrap scripts.
  Files under `plugin/claude-code/scripts/` are **copies**; edit the original in `scripts/` and
  re-copy, or CI fails.
- `examples/`, `docs/`, `scripts/` — read `scripts/matrix-*.sh` before adding a test scenario.

**One contract, one home.** The recurring defect in this codebase's history is the same fact spelled
in several files until they disagree: five copies of token formatting (`internal/render` now owns
it), three of the closing-gate name (`spec.ClosingGateName`), four of the installed binary's path
(`internal/install`), six readers of the vault path (`export.VaultPath`). Before adding a
`filepath.Join` or a format string, look for the package that already owns that answer. If a second
place needs it, that is the signal to give it one home, not two.

## Non-negotiable

These come from `batten.yaml`'s `invariants:` and from bugs that shipped.

1. **Never invent a number.** Anything unmeasurable reports as *unavailable* — never as `0`, never
   as `$0.00`. A measured zero and an unknown are opposite facts and must not share a rendering.
2. **A hook must degrade, never break a session** — and degradation must be *visible*. Fail-open is
   allowed; silent fail-open is the failure this tool exists to eliminate.
3. **Never write state under `CLAUDE_PLUGIN_ROOT`.** A plugin update wipes it. State is
   `~/.batten/batten.db` (override with `BATTEN_DB`); the binary in the plugin's `bin/` and the copy
   in `CLAUDE_PLUGIN_DATA` are cache, not state.
4. **An empty `evidence[]` is never an approval.** No verdict, no commit.
5. **Migrations are expand-only.** Add a column, add a table, never take one away — two batten
   binaries share one database and `doctor` warns about their version skew because skew is normal.
   `TestEveryMigrationIsAdditive` holds this.

## Changing something

- **Every fix ships with a test that FAILS against the previous commit**, reverting the behaviour
  with the symbols left standing. Run it both ways and say so. "The suite is green" is not evidence
  that a fix works; it is evidence that nothing else broke.
- **The silence of `batten hook` is not proof of ALLOW.** Use a differential control: one run that
  must be denied and one that must pass.
- **New matrix scenarios go in the script**, never in a document. `scripts/matrix-replica.sh` (41)
  and `scripts/matrix-demo.sh` (26) are the acceptance matrices; a matrix nobody else can re-run is
  a memory.
- **Guard tests are load-bearing.** Fields declared in the spec must have a consumer or an explicit
  future entry; every absolute rule of `/batten-night` must have a mechanism; every `edges.rel` a
  surface reads must have a writer. If a guard fires, the guard is usually right.
- **Comments say why, and name the failure.** The house style is a comment that explains the bug the
  code prevents, so the next reader cannot delete it by accident. Match the density around you.

## Platform

Windows is the primary target and CI runs all three OSes. Two classes of bug are invisible on the
author's machine, and both are settled by the repo rather than by a developer's environment:

- **Line endings** — `.gitattributes` decides. A `bootstrap.sh` with CRLF is `#!/usr/bin/env bash\r`,
  a bad interpreter, and every hook no-ops in silence.
- **The execute bit** — every tracked `.sh` is `100755`; CI and `internal/install` both check.

## Tools this repo leans on

- **graphify** — the code graph. Rebuild with `graphify . --code-only` only. Plain
  `graphify update .` indexes documents too and pulls `docs/` into a committed 2 MB JSON that no
  diff review will ever read; `.graphifyignore` is the second line of defence.
- **engram** — episodic memory. batten does not store decisions; it records process.
