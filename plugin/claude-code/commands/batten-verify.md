---
description: Run the gate — check the unit's diff against its acceptance criteria and emit a verdict envelope with cited evidence
---

Verify the work item named in $ARGUMENTS, and end with a verdict.

This phase exists to answer one question honestly: **is this actually done?** Not "does it look
done". The difference between those two is the reason batten exists.

Read `batten.yaml` first. Find the phase carrying a `gate:` and the gate it names — that gate's
`checks`, `skills`, and evidence rule are your job description.

```bash
batten phase <unit> <verify-phase-id>
batten show <unit>          # phase, write-sets, base SHA, budget
```

## 1. Scope: the unit's diff, from the anchor

The phase declares `diff_from: anchor`. Use the base SHA recorded by the build phase:

```bash
git diff --name-only <base-sha>     # `batten show <unit>` prints it
```

**Only these files.** Never `HEAD~N` — the anchor is what keeps the scope right across rebases and
interleaved work. Reviewing files outside the unit's diff wastes the budget and produces findings
nobody asked for; missing files inside it is how a bug ships.

## 2. Run the gate's checks

Run every command in `gates[<name>].checks`, verbatim, and **keep the output**. You are going to
cite it — a check you ran but did not record is a check you cannot use as evidence.

Then run every skill in `gates[<name>].skills` over the diff. Their findings are evidence too.

Then, for each domain the diff touches, re-run its `check` commands and confirm its `coverage`
floor is actually met. "The agent said it passed" is not evidence; the agent is not the gate.

## 3. Check every acceptance criterion against the CODE

Open the plan artifact. Take each acceptance criterion **one at a time** and verify it against the
diff — the real code, not the plan's description of the code, and not the build agent's summary of
what it did.

For each one, you end up in exactly one of three places:

- **Verified** — you ran something, or you read the code and can point at the file and line.
- **Not met** — you found it broken or missing.
- **Could not verify** — no way to check it from here (needs a browser, a GPU, a human eye).

That third bucket is real and you must not pretend it away. Write those into the manual-test
artifact if the spec declares one: the exact steps, and the expected result. Then say so in the
verdict — a criterion you could not verify does **not** count toward an `ok`.

## 4. Emit the verdict envelope

```json
{
  "check_id": "<unit>-<gate>",
  "result": "ok | warn | blocked",
  "evidence": [
    "<command>: <what it actually printed>",
    "<test runner>: 142 passed, 0 failed, coverage 78% (floor 70%)",
    "<criterion>: verified at path/to/file.py:41"
  ],
  "why": "...",
  "safe_next_step": "...",
  "requires_confirmation": false
}
```

```bash
batten verdict --unit <unit> --file verdict.json
```

**`result: "ok"` with an empty `evidence[]` is rejected by the binary**, before it reaches the
database. And the close gate will separately deny the commit. This is not a lint rule you can
argue with — it is the one failure batten was built to make impossible.

Evidence means something that *happened*: command output, a test count, a coverage number, a
criterion verified at a specific line. Not "all good", not "tests pass" (which ones? how many?),
not "implemented as planned". **If you did not run it, you may not cite it.**

Choosing the result:

| | when | consequence |
|---|---|---|
| `ok` | every criterion verified, each with evidence | the close gate opens |
| `warn` | passes, but with debt — name the debt in `why` | gate stays shut unless the spec allows `warn` |
| `blocked` | something failed, **or you could not verify it** | gate shut; go to the fix phase |

**When uncertain, `blocked`.** A false `blocked` costs one more phase. A false `ok` ships a bug and
teaches everyone that the gate is theatre — after which the gate is worth nothing, forever.

Note what "could not verify" implies: a unit whose criteria you could not check is `blocked`, not
`ok`. It is not the agent's fault and it is not a failure — it is a fact, and reporting it is the
job.

## 5. Report

The criteria table (criterion → status → evidence), the files audited, the issues found, the
coverage per domain, and the verdict. If the result is not `ok`, say plainly what has to happen
next — the fix phase is going to read this and act on it.
