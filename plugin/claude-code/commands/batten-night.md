---
description: Run a work item unattended — build, verify, fix, re-verify — stopping before the close, with the budget ceilings as the tripwire
---

Run the work item in $ARGUMENTS to completion **without supervision**. Nobody is awake. Do not ask
for confirmation; decide, act, and write down what you decided.

That freedom is exactly why this command is the most dangerous one in the plugin, and why it is
fenced harder than the others. Read `batten.yaml` first — it holds the phases, the domains, the
invariants, the resources, and the ceilings.

## The absolute rules

**1. Never delete anything.** Not a file, not a branch, not a commit, not a migration, not a
checkpoint. Not with `rm`, not with `git checkout --`, not with `git reset --hard`, not by
truncating a file and rewriting it, not by "cleaning up".

If you find yourself *wanting* to delete something — dead code, an obsolete test, a file you
believe you superseded — **do not**. Write it in the "wanted to delete" section of the final
report, with the reason. A human will read it in the morning and it takes them ten seconds.

The asymmetry is the whole point: leaving a stale file costs someone ten seconds tomorrow. Deleting
the wrong one costs work nobody can get back, in a run nobody was watching. You cannot tell which
one you are doing at 3am, and neither can I.

**2. The budget ceilings are the tripwire.** An unattended run has no human to notice it grinding.
`budget:` is what replaces that human — check it, and honor `on_exceed`.

**3. Never override the gate.** `batten override` requires a human reason, and at 3am there is no
human to give one. A blocked verdict at the end of an unattended run is a *successful* outcome —
it means the gate did its job. Report it and stop.

**4. Do not commit.** This run stops before the close phase, on purpose.

## Preflight — stop here if anything is missing

- `batten doctor` — clean? If the spec is broken, stop. Do not guess at it.
- The plan artifact must **exist**. If there is no plan, **stop and say so.** An unattended run
  without a plan has no write-sets, and a fan-out without write-sets is exactly the failure the
  write-set guard exists to catch. Do not plan it yourself and then execute your own plan
  unsupervised — the plan phase is where a human's judgment enters, and it has not entered.
- `batten budget` — how much of the ceiling is already spent? If the run is close to a ceiling
  before it starts, say so and stop.
- If a domain declares `resources:`, probe them now. A probe that fails means capacity is
  **unknown** — do not launch anything that assumes it.

## The loop

**Build.** Run the `fanout: true` phase exactly as `/batten-build` describes it: anchor the SHA,
probe the resources, launch one agent per domain and per disjoint sub-task, `batten claim` each
write-set, sequence the dependent ones, integrate.

A write-set collision means **the plan is wrong**. Unattended, you cannot fix a plan a human wrote.
So: stop that sub-task, leave the rest running, and report the collision. Do not work around the
fence at 3am. Working around the fence is precisely the thing nobody is awake to catch.

**Verify.** Run the gate: the diff from the anchor, the gate's `checks`, its `skills`, every
acceptance criterion against the real code. Emit the verdict envelope with cited evidence.
`result: "ok"` with an empty `evidence[]` is rejected — and at 3am, with nobody watching, that rule
is doing more work than at any other hour.

**Fix — but only what a machine may fix.**

Fix without asking: failing linters, red tests, formatting, a missing translation key, a missing
required argument, an invariant from `domains[].invariants` that an agent broke.

**Do not fix; write it down instead:** anything needing a product decision, an architecture change,
a schema migration on live data, a credential, an expensive retrain nobody approved, or a criterion
you cannot verify without a human or a browser. Document it precisely and move on. A wrong
autonomous decision compounds all night.

**Re-verify.** If the fixes changed logic, run the gate again — the phase whose `when:` says so.
If they only touched formatting or copy, say that and skip it.

**Honor `budget.max_iterations`.** That is the ceiling on how many times this fix → re-verify loop
may go round. When you hit it, **stop.** Do not go around once more. A loop that has failed the same
check three times is not going to pass it on the fourth; it is going to spend the window.

## Stop before the close

Do not run the close phase. Do not commit. A human closes.

## The report — the actual deliverable

You are writing to somebody with a coffee who was asleep for all of this. The report is the only
thing they have. Be specific and be honest; a report that oversells a broken run is worse than no
run, because they will trust it.

```
NIGHT RUN <unit>

Domains / sub-tasks:  [per agent: write-set, checks red/green]
Build:                [ok / issues]
Verify:               [N findings]
  fixed automatically:  [list]
  needs a human:        [list, with WHY each one needs one]
Verdict:              [ok | warn | blocked] — evidence: [the cited items]
Iterations used:      [n of budget.max_iterations]

Budget:               [tokens / imputed usd / quota %, each against its ceiling]
                      [any ceiling that could NOT be measured — say which, and say
                       it is unmeasured. Do not report an unmeasured ceiling as 0.]

WANTED TO DELETE (did not): [file, and why I thought so — or "nothing"]
Resources used:       [what was launched, what the probe said, how it was queued]
Blocked on:           [what stopped, and what it is waiting for]

Next step:            [fix phase with these bugs / ready for close]
```

If the run ended `blocked`, say so in the first line. **Do not bury it.** A blocked verdict is the
system working, and the person reading this needs to know it in the first two seconds, not the last.
