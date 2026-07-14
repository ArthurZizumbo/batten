---
description: Interview this repo (and optionally an existing prose workflow doc) and write batten.yaml
---

Generate `batten.yaml` for this repository: its process, expressed as data.

$ARGUMENTS may contain `--from <path>` pointing at a workflow that already exists in prose
(a `prompts.md`, a `CONTRIBUTING.md`, a phase-by-phase doc). If it does, **that document is the
primary source** — you are not designing a workflow, you are extracting one that already works.

## 1. Read the ground

- If `--from <doc>` was given, **read it completely first.** It already contains the phases, the
  gates, the layer rules, and the fan-out policy. Your job is to separate the *executable* from
  the *prose*.
- Then inspect the repo: build files (`Makefile`, `package.json`, `pyproject.toml`, `go.mod`,
  `*.tf`), per-directory `AGENTS.md` / `CLAUDE.md`, test targets, lint targets, CI config, and the
  top-level directory layout.
- List installed skills. Map each to the domain it serves.

## 2. Decide what belongs in the spec

The governing rule, and it matters more than completeness:

> **If a hook cannot enforce it or a command cannot run it, it does not belong in `batten.yaml`.**

A phase's long explanatory prompt stays in prose. A `check` command from the Makefile goes in the
spec. "Two agents never write the same file" goes in the spec (it becomes a constraint). "Think
carefully about the architecture" stays in prose.

Over-declaring turns the spec into a DSL nobody maintains. Under-declaring turns it into a comment.

## 3. Write it

Follow the schema in `batten.schema.json`. The parts, and how to fill them:

- **`unit`** — the work-item noun this repo already uses. Look at branch names and issue references:
  `US-034`, `TICKET-12`, `#451`. Derive `pattern` from what you actually see, and verify it matches
  the current branch.
- **`phases`** — the state machine. Most real workflows are some variant of
  research → plan → build → verify → fix → close. Use the repo's own names for them.
  Exactly one phase should carry `requires_verdict: ok` — that is the gate that protects the close.
  The build phase should carry `anchor: git_sha`.
- **`domains`** — one per fan-out axis. `path`, its `rules` file, its `check` commands (take these
  verbatim from the Makefile/package.json — do not invent them), and its `invariants`.
  **The invariants are the highest-value thing in the file**: they are the rules a reviewer would
  catch and a distracted agent would break. Mine them from the `AGENTS.md` files and from the
  prose doc's per-layer rules.
- **`resources`** — anything scarce that forces serialization (a shared GPU, a staging DB, a rate-
  limited API). If the prose says "coordinate before doing X", X is a resource.
- **`gates`** — the checks that must pass. `evidence: required` unless the user overrides; a gate
  that permits an approval with no evidence is not a gate.
- **`capabilities`** — only what is actually installed. Check PATH. Leave the rest out; they degrade
  gracefully but a declared-and-absent capability is noise.

## 4. Verify before you hand it over

```bash
batten doctor
```

Fix whatever it reports. Then **show the user the diff between what the prose said and what the spec
captures** — specifically, name what you deliberately left in prose and why. They should be able to
disagree with your judgment, which means they need to see it.

Do not claim the migration is complete if you dropped something and did not say so.
