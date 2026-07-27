---
description: Interview this repo (and whatever process it already has) and write batten.yaml
---

Generate `batten.yaml` for this repository: its process, expressed as data.

**Most repos worth adopting batten already govern themselves somehow** — `AGENTS.md` per
directory, a `CONTRIBUTING.md`, a Makefile everyone runs, a 700-line prompt file. That existing
process is not an obstacle to work around. **It is the primary source.** You are not designing a
workflow; you are finding the one that already runs and separating its executable part from its
prose. A spec that contradicts the harness a team already trusts gets uninstalled in a week.

$ARGUMENTS may contain `--from <path>` pointing at a workflow already written in prose. If it
does, read that document **completely, first** — it already contains the phases, the gates, the
layer rules and the fan-out policy.

## 1. Get the facts before forming an opinion

```bash
batten init --scan-json
```

This writes nothing. It reports what a heuristic can honestly derive, and four fields matter most:

- **`harness[]`** — the process this repo already has. Every entry is something a human already
  decided. An existing `batten.yaml` appears here too: `init` will not overwrite it, and neither
  should you — reconcile by hand and say what you changed.
- **`stack[]`** — languages and tooling, from marker files that actually exist. Empty means the
  repo has no build files yet, not that it has no stack.
- **`purpose[]`** — where the repo describes what it is for. **Read these.** A spec written
  without knowing what the project does produces domains named after folders and invariants
  nobody believes.
- **`notes[]`** — what the scan could not decide. These are your interview questions, pre-written.

Then read every file in `harness[]` and `purpose[]`. Also list the installed skills and agents and
map each to the domain it serves.

## 2. Interview the human — do not skip this

The scan gives you facts. It cannot give you judgment, and **guessing quietly is the failure mode
this step exists to prevent.** Ask about anything the scan flagged, plus these, and *ask them as
questions* rather than deciding and hoping nobody notices:

- **Purpose.** "From the README this looks like <X>. Is that right, and what part is most likely
  to break?" The answer decides which invariants matter.
- **The axes.** "You have `AGENTS.md` in backend/, frontend/, ml/ — are those your parallel work
  axes, or does work usually cut across them?" Directories are a hypothesis, not an answer.
- **The unit.** Confirm the derived pattern against the real tracker. Branch names are a proxy for
  it, and a proxy is wrong often enough to be worth one question.
- **The gate.** "Which commands must pass before a commit is allowed?" If `stack[]` is empty or no
  check was found, say plainly that **the gate currently verifies nothing** and that it must be
  filled before `enforcement` moves to `enforce`.
- **The scarce thing.** "Is there anything two people can't use at once — a GPU, a staging DB, a
  rate-limited API?" That is a `resource`, and it is the one thing a spec cannot infer.
- **Sequencing.** If work has an order the team actually follows, name the phases after *their*
  names for them, not after batten's.

Prefer a handful of sharp questions over a questionnaire. If the repo has a rich harness, most
answers are already in it — ask to **confirm**, not to gather.

## 3. Decide what belongs in the spec

The governing rule, and it matters more than completeness:

> **If a hook cannot enforce it or a command cannot run it, it does not belong in `batten.yaml`.**

A phase's long explanatory prompt stays in prose. A `check` command from the Makefile goes in the
spec. "Two agents never write the same file" goes in the spec — it becomes a constraint. "Think
carefully about the architecture" stays in prose.

Over-declaring turns the spec into a DSL nobody maintains. Under-declaring turns it into a comment.

## 4. Write it

Follow `batten.schema.json`. The parts, and how to fill them:

- **`unit`** — the work-item noun this repo already uses (`US-034`, `TICKET-12`, `#451`). Derive
  `pattern` from what you actually saw and verify it matches the current branch.
- **`phases`** — the state machine, under the repo's own names. Most real workflows are a variant
  of research → plan → build → verify → fix → close. Exactly one phase carries
  `requires_verdict: ok` — that is the gate protecting the close. The build phase carries
  `anchor: git_sha`.
- **`domains`** — one per fan-out axis, as confirmed in step 2. `path`, its `rules` file, its
  `check` commands **verbatim** from the build files (never invented — a check that does not run
  is worse than no check), and its `invariants`.
  **The invariants are the highest-value thing in the file**: the rules a reviewer would catch and
  a distracted agent would break. Mine them from the `AGENTS.md` files and the prose doc's
  per-layer rules. Keep the author's wording where you can — they ride character-for-character
  into every fanned-out agent's prompt.
- **`resources`** — anything scarce that forces serialization. If the prose says "coordinate
  before doing X", X is a resource.
- **`gates`** — `evidence: required` unless the user overrides. A gate that permits an approval
  with no evidence is not a gate.
- **`capabilities`** — only what is actually installed. Check PATH. A declared-and-absent
  capability is noise.
- **`enforcement: report`** to start, always. Gates warn instead of blocking so batten can enter a
  repo mid-sprint without becoming the reason a release slipped.

## 5. Verify, then hand it over honestly

```bash
batten doctor
```

Fix whatever it reports. Then show the user **two** things:

1. **The diff between what the existing process said and what the spec captures** — specifically,
   name what you deliberately left in prose and why. They should be able to disagree with your
   judgment, which means they need to see it.
2. **What you could not answer**, as open questions rather than silent defaults.

Do not claim the migration is complete if you dropped something and did not say so. And if the
repo already had a `batten.yaml`, say exactly what you changed and what you left alone.
