---
name: batten-verdict
description: Emit the verdict envelope that closes a gate phase. Use when finishing a verify, QA, review, or gate phase in a batten workflow, when the user asks to close or approve a work item (US, ticket, issue), or when a commit was denied because no verdict exists. Every approval must cite evidence — an empty evidence[] is never an approval.
---

# batten-verdict

A verdict is how a gate phase ends. It is not a summary — **it is a claim with citations.**

The gate's `checks`, `skills` and evidence rule come from `batten.yaml` (`gates[<name>]`). Read them
there; this skill is the shape of the answer, not the content.

## The envelope

```json
{
  "check_id": "<unit>-<gate>",
  "result": "ok | warn | blocked",
  "evidence": [
    "<lint command>: clean, 0 findings",
    "<test command>: 142 passed, 0 failed; coverage 78% (floor: 70%)",
    "<criterion AC-3>: verified at src/api/orders.py:41"
  ],
  "why": "every acceptance criterion checked against the code in the unit's diff",
  "safe_next_step": "close",
  "requires_confirmation": false
}
```

Record it:

```bash
batten verdict --unit <unit> --file verdict.json
# or pipe it:
echo '{...}' | batten verdict --unit <unit>
```

## The rule that makes this worth anything

**`result: "ok"` with an empty `evidence[]` is rejected.** Not discouraged — rejected, by the
binary, before it reaches the database:

```
batten: result "ok" with an empty evidence[] is not allowed: an approval must cite something
(command output, test counts, a criterion verified). Without evidence the result is "blocked"
```

And the `PreToolUse` gate separately denies the commit.

This exists to kill one specific failure: **closing a work item because it *looks* fine.** If you
cannot cite a command's output, a test count, or a criterion you actually checked against the diff,
then you do not know that it is fine — and the honest result is `blocked`.

## What counts as evidence

Each item must point at something that **happened**:

- **Command output** — `<lint>: clean`, `terraform validate: Success`
- **Counts** — `142 passed, 0 failed`, `coverage 78% (floor 70%)`
- **A criterion, verified against the diff** — `AC-3 (rate limit on the write endpoint): confirmed at src/api/orders.py:41`
- **A gate skill's finding** — `code-review: no duplication with the existing utils/`

What does **not** count:

- `looks good` · `tests pass` (which ones? how many?) · `implemented per the plan`
- **Anything you did not run.** If you did not run it, say so, and the result is `warn` or `blocked`.

The test: could a reviewer who does not trust you re-run the cited thing and get the same answer? If
not, it is not evidence.

## Choosing the result

| result | meaning | consequence |
|---|---|---|
| `ok` | every acceptance criterion verified, with evidence | the close gate opens |
| `warn` | passes, but with debt — name the debt in `why` | gate stays shut unless the spec allows `warn` |
| `blocked` | something failed, **or you could not verify it** | gate shut; go to the fix phase |

**Default to `blocked` when uncertain.** A false `blocked` costs one more phase. A false `ok` ships
a bug *and* teaches everyone that the gate is theatre — and a gate nobody believes is not a gate.

Note the third row carefully: a criterion you **could not check** (it needs a browser, a GPU, a
human eye) does not count toward an `ok`. That is not a failure and it is not your fault — it is a
fact, and reporting it is the job. Write those steps into the manual-test artifact if the spec
declares one.

## Scope: the unit's diff, from the anchor

Verify only the unit's own diff, anchored at the base SHA recorded by the build phase:

```bash
batten show <unit>                  # prints the base SHA
git diff --name-only <base-sha>
```

Never `HEAD~N`. The anchor is what keeps the scope correct across rebases and interleaved work —
`HEAD~N` silently reviews somebody else's commits, or silently misses half of yours.

## If a commit was denied

The gate told you why, and the reason was generated from the actual state of the run — it is not
boilerplate. Do the thing it named.

If you genuinely must proceed anyway:

```bash
batten override <unit> --reason "production hotfix; QA in follow-up PR #412"
```

`--reason` is mandatory — the binary refuses an override without one, because an override with no
stated reason is just a disabled gate with extra steps. It lands in the audit log, permanently.

Use it for a real emergency, never because the verdict is inconvenient.
