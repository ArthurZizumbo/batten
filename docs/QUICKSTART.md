# Quickstart — adopting batten in a repo that has none

> **English** · Español: pendiente

> Every command and every block of output below was captured from a real run on a real repo
> built from an empty directory. Nothing here is illustrative.

The demo repo is `taskly`: a small Go project with two domains (`api/`, `store/`), an
`AGENTS.md` per domain stating one invariant each, a `Makefile`, and a backlog in
`docs/backlog.md` listing `US-001`..`US-005`. One invariant matters for what follows:

> `store.ErrNotFound` maps to 404, never to 500.

## 1. `batten init` — read the repo, propose a spec

```
$ batten init
wrote batten.yaml — a working draft in report mode (gates warn, don't block).
  project="taskly" unit="US", 2 domain(s) detected, graphify found, engram found
Next:
  1. fill the invariants (the TODOs) — the highest-value part of the file
  2. run: batten doctor
  3. flip enforcement: enforce when you trust the gates
```

It got `unit: US` from the backlog's headings, not from a branch name — the repo is on `main`
and has never had a feature branch. The spec it wrote says where that came from:

```yaml
unit:
  name: US
  pattern: 'US-\d{3}'
  plan: docs/backlog.md
  locator: '### {id}'
```

The check commands were lifted verbatim from the `Makefile`, and the spec says so in a header
comment rather than pretending it invented them:

```yaml
# This repo ALREADY has a process. The spec below should agree with it, not replace it:
#   Makefile (build) — the check commands come from here VERBATIM — never invent one
#   api/AGENTS.md (agent-rules) — per-directory rules — this boundary is probably a fan-out axis
#   store/AGENTS.md (agent-rules) — per-directory rules — this boundary is probably a fan-out axis
```

It also starts in **report mode** — gates warn, they do not block — and ends the file with the
decisions it could not make for you:

```
# - invariants are empty — fill each domain's invariants with the rules a reviewer would catch
# - gates.qa.checks were taken from your build files; confirm they are the right pre-commit checks
# - enforcement starts at 'report' (gates warn, don't block) — flip to 'enforce' when trusted
```

**Do the TODOs before going further.** Fill each domain's `invariants` from its `AGENTS.md`, then
flip `enforcement: enforce`. The rest of this walkthrough assumes both.

## 2. `batten doctor` — is this repo actually governed?

```
$ batten doctor
✓ .../taskly/batten.yaml — project "taskly", unit "US", 3 phases, 2 domains
✓ close gate: phase "close" requires verdict "ok" on any gate
✓ enforcement: enforce — gates block
✓ store: .../state.db
✓ graph: graphify (on PATH)
  ⚠ no graphify-out/graph.json yet — run: graphify . --code-only
· memory: engram (via MCP; batten does not store episodic memory)
$ echo $?
0
```

`doctor` exits non-zero on a spec it calls invalid, so it is safe to put in CI:

```
$ batten doctor
✗ .../batten.yaml: invalid spec:
  - unit.name is required (the work-item noun: US, ticket, issue...)
  - phase "verify" references unknown gate "nope"
$ echo $?
1
```

## 3. Commit before opening a run — batten says it is not governing you

This is the state a newcomer is in for their whole first day, and it used to be silent.

```
$ git checkout -b feature/US-002-missing-task-404
$ git commit -m "feat(US-002): 404 for a missing task"

batten (warning — not blocking): batten: this commit is NOT gated. US-002 has no run on
record, so there is no verdict to check and nothing was verified.
Open one with `batten phase US-002 build` — the gate starts governing from there.
```

It warns rather than denies, on purpose: batten cannot verify what was never declared, and
refusing every commit in a repo that has not started a run would just get it uninstalled. But it
never lets you believe a gate is running when it is not.

## 4. `batten phase` — open the run, record the anchor

```
$ batten phase US-002 build
anchor: US-002 base SHA = ca015de
US-002 -> phase build
```

The anchor is the point every later phase diffs from. Not `HEAD~1`, not "the last few commits" —
a recorded SHA, so a review three days and nine commits later still scopes to this unit's work.

Now write the code. In the demo: `api/handler.go` maps `store.ErrNotFound` to 404, plus a test
that asserts it.

## 5. Commit with no verdict — denied

```
$ git commit -m "feat(US-002): 404 for a missing task"

DENY: batten: US-002 has no verdict envelope. Run the "verify" phase before committing.
To proceed anyway (recorded in the audit log): batten override US-002 --reason "..."
```

There is always an escape. It is on the record.

## 6. The reviewer's verdict alone — still denied

```
$ batten phase US-002 verify
$ batten verdict --unit US-002 --file v.json
verdict recorded: US-002 qa=ok (3 evidence)

$ git commit -m "feat(US-002): 404 for a missing task"

DENY: batten: US-002 has no batten-verified pass. The gate's checks must be RUN, not asserted.
Run: batten check US-002
```

This is the whole point of the tool. An agent — or a person — writing "tests pass" in an
envelope is a claim. The gate wants the claim *and* the run.

## 7. `batten check` — batten runs the gate's own checks

```
$ batten check US-002
  ✓ go build ./...
  ✓ go vet ./...
  ✓ go test ./...

US-002: OK (batten-verified). all gate checks passed (batten ran them)
```

Now the same commit goes through — silently, because an allowed commit is an ordinary commit.

```
$ git commit -m "feat(US-002): a missing task returns 404, not 500"
[feature/US-002-missing-task-404 3a1c9f2] feat(US-002): a missing task returns 404, not 500
```

**Both verdicts are required, and they must come from different producers.** `batten check`
proves the declared checks ran. The verdict envelope proves somebody judged the work against its
acceptance criteria. Neither substitutes for the other:

```
$ batten check US-003          # only batten's verdict exists
$ git commit -m "feat(US-003): mark a task done"

DENY: batten: US-003 has only batten's own check result. `batten check` proves the checks ran;
it does not judge whether the work meets its acceptance criteria.
Record a verdict from the verify phase: batten verdict --file v.json
```

## 8. `batten close` — and what the record looks like

```
$ batten phase US-002 close
$ batten close US-002 --status ok
US-002 closed (ok). Write-set claims released — files it held are free again.

$ batten show US-002
US-002  run=US-002-1785264143887316500-6ad6e86c  status=ok  phase=close  base=ca015de
  [phase] build                    ok
  [phase] verify                   ok
  [phase] close                    ok

verdict qa=ok (agent): a missing task now returns 404; the store invariant is unchanged
  - api/handler.go:14 maps store.ErrNotFound to http.StatusNotFound
  - TestAMissingTaskIs404NotA500 asserts w.Code == 404
  - store/AGENTS.md invariant untouched: Get still returns ErrNotFound
verdict qa=ok (batten): all gate checks passed (batten ran them)
  - go build ./...: PASS (exit 0, 585ms)
  - go vet ./...: PASS (exit 0, 686ms)
  - go test ./...: PASS (exit 0, 637ms)
```

Two verdicts, labelled by who produced them. The reviewer's cites the acceptance criteria;
batten's cites its own exit codes and timings.

## The negative control — watch it actually deny

A walkthrough where everything passes proves nothing. Break the invariant on purpose: make the
handler return 500 where the domain's `AGENTS.md` says 404.

```
$ batten phase US-003 verify
$ batten check US-003
  ✓ go build ./...
  ✓ go vet ./...
  ✗ go test ./... (exit 1)
    --- FAIL: TestAMissingTaskIs404NotA500 (0.00s)
        handler_test.go:14: missing task returned 500, want 404
    FAIL	taskly/api	0.832s

US-003: BLOCKED (batten-verified). one or more gate checks failed
the commit gate will deny until this passes.
$ echo $?
1
```

`batten check` exits **1** when the unit is blocked, so `batten check && ...` stops and CI fails.
And the commit is refused with the reason and the next step:

```
$ git commit -m "feat(US-003): mark a task done"

DENY: batten: US-003 verdict is "blocked", not "ok". one or more gate checks failed
safe_next_step: fix the failures, then run batten check again
```

Fix the handler, re-check, and it clears:

```
$ batten check US-003
  ✓ go test ./...

US-003: OK (batten-verified). all gate checks passed (batten ran them)
$ echo $?
0
```

## What the ledger says when nobody measured it

```
$ batten budget
US-005  usage NOT MEASURED (not zero — nothing has been ingested for this run)
  · tokens       NOT MEASURABLE — no usage has been measured for this run —
                 run `batten ingest <unit> --transcript <path>`
```

Not `0 tokens, $0.00`. A run nobody has priced has not spent nothing; it is unmeasured, and
those two need opposite responses from whoever is reading. This is the first principle of the
whole tool: **never report a number you do not have.**

## Where to go next

- [`README.md`](../README.md) — what batten is and why the gate is a hook rather than a document.
- [`FIELD-TEST.md`](FIELD-TEST.md) — batten run against a real project by agents that had never
  seen it, and the 52 confirmed defects that came back.
- `batten tui` — the same records, reviewable without leaving the terminal.
- `batten canvas <unit>` — the run graph as a JSON Canvas, which Obsidian renders.

**One rule if you are scripting against batten:** export `BATTEN_DB` before every command when
you are working in a sandbox. It falls back to your real database the moment it is unset.
