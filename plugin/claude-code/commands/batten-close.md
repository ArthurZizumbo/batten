---
description: Close a work item — write the resolution artifact, stamp provenance, and commit through the verdict gate
---

Close the work item named in $ARGUMENTS.

Read `batten.yaml`. The phase carrying `requires_verdict:` is the one you are running, and it is the
only phase in the file that a **hook** enforces rather than a convention.

## 1. The entry gate

```bash
batten show <unit>
```

The unit needs a verdict whose `result` matches the phase's `requires_verdict` (normally `ok`), with
a **non-empty `evidence[]`**.

If it does not have one: **stop and say so.** Do not close it. Do not run the checks yourself and
call that a verdict. Do not close "because it obviously works". The verify phase exists; run it.

You will not be able to commit anyway — this is not a request:

```
batten: US-034 has no verdict envelope. Run the "qa" phase before committing.
To proceed anyway (recorded in the audit log): batten override US-034 --reason "..."
```

The gate is a `PreToolUse` hook. It inspects the `git commit` before it runs and returns
`permissionDecision: deny`. There is no flag that turns it off and no phrasing of the commit that
slips past it. If you find yourself looking for one, you are the reason it exists.

## 2. Write the resolution artifact

To `artifacts[<closing kind>]` with `{id}` substituted:

- What was done, briefly.
- The acceptance-criteria table: each criterion, what satisfies it, **and the evidence** — carried
  over from the verdict, not re-asserted from memory.
- Standards actually met: linters, coverage per domain against its floor, each domain's invariants.
- **The provenance key.** If `provenance.format` is declared, fill it exactly — every placeholder,
  `-` for the ones that do not apply to this unit. `{git_sha7}` is `git rev-parse --short=7 HEAD`.
  This one line is what makes the unit reproducible a year from now; a wrong one is worse than none,
  so do not guess a value you do not have.

## 3. Commit

Write a commit message in the repo's existing convention — read the last twenty commits and match
them rather than importing a style from somewhere else.

Now commit. The gate reads the verdict, sees `ok` with cited evidence, and allows it.

If the gate denies, **read the reason** — it names the missing thing. Fix that. The reason is not
boilerplate; it was generated from the actual state of the run.

## 4. The override, and what it costs

There is an escape. It is deliberately not free:

```bash
batten override <unit> --reason "production hotfix; QA in follow-up PR #412"
```

`--reason` is mandatory — the binary refuses an override without one, because an override with no
stated reason is just a disabled gate with extra steps. It lands in the audit log, permanently,
with your reason attached.

Use it for a real emergency. Do not use it because the verdict is inconvenient. A gate that gets
overridden routinely is a gate nobody believes, and then it is not a gate.

## 5. Finish

- Mark the unit resolved wherever the project tracks that (`unit.plan`, a status doc, an issue).
- If `capabilities.memory.provider` is set, save what this unit taught you — the decisions with
  reasons, the bugs and their causes, the patterns that worked. Next quarter's agent reads this.
- Emit the run graph:

  ```bash
  batten canvas <unit>
  ```

  It draws the path the work **actually** took — the fan-out, the retries, the blocked verdict that
  got fixed — not the path the plan hoped for. Open it in Obsidian.

Report: the provenance line, the commit SHA, and anything left undone (a manual test still pending,
a debt noted in a `warn`). A close that hides its loose ends is how they become somebody's surprise.

## Memory (if `capabilities.memory.provider` is set)

On a clean close, persist what was non-obvious: `mem_save` the key technical decisions and any
gotcha found, then `mem_session_summary` (Goal / Discoveries / Accomplished / Next / Files). This is
the episodic memory the next unit's plan phase will `mem_search`. The provenance line stays batten's;
the reasoning behind it is engram's.
