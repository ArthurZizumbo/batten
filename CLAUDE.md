# CLAUDE.md — operating this repo with Claude Code

**Read [`AGENTS.md`](AGENTS.md) first. It is the contract** — what this project is, what it refuses
to be, the layout, and the five non-negotiables. This file does not restate it; duplicating a rule
is how this codebase's worst defects began, and *one contract, one home* is itself one of the rules.

This file carries the part AGENTS.md leaves out on purpose: **how to actually run things here
without breaking something that belongs to the person running you.**

Both files are English-only by design. They are working contracts, not reference documentation —
the bilingual rule below applies to what a reader adopts, not to what a contributor obeys.

---

## The one-paragraph version

batten is a Go CLI that ships as a Claude Code plugin. It turns a repo's declared process — phases,
gates, domains, write-sets — into **mechanical denials**: no `git commit` without a verdict citing
evidence, no two fanned-out agents writing the same file. Everything else it does (the run graph,
the token ledger, the TUI, the canvas, the generated PR) exists so those denials are auditable
instead of suspicious. `~/.batten/batten.db` is canonical; every other surface is a lossy
projection.

Start with [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the code, [`README.md`](README.md) for
the argument, [`ROADMAP.md`](ROADMAP.md) for what is proven versus merely built, and
[`docs/FIELD-TEST.md`](docs/FIELD-TEST.md) for what broke when strangers ran it.

---

## This repo is governed by batten itself

[`batten.yaml`](batten.yaml) is real, not a sample: two domains (`cmd`, `internal`), a `qa` gate that
runs the suite plus `batten scan-diff --strict`, and `enforcement: report` while the dogfood settles.
Consequences you will actually meet:

- **`batten.yaml` is the authority on process.** Phases, gates and domains come from it. Do not
  invent a workflow it does not declare.
- **Report mode means the gate warns instead of denying.** A warning on your commit is the gate
  working, not noise — read it. Do not "fix" it by editing the spec.
- **`batten scan-diff --strict` runs in the gate on purpose.** It compares the real git diff against
  declared write-sets, and it is the only thing that makes the over-declaration metric accumulate.
  A file you changed that no write-set claimed will be named. That is the check doing its job.

---

## Commands

```bash
go build ./...                      # all packages
go test ./...                       # the suite — 17 packages
go vet ./... && gofmt -l .          # both must be clean; gofmt prints nothing when it is

go test ./internal/spec/ -run TestEveryDeclaredFieldHasAConsumerOrIsDeclaredFuture   # one guard
go test ./internal/install/                                                          # package tests

scripts/matrix-replica.sh           # acceptance matrix, 41 assertions (builds its own sandbox)
scripts/matrix-demo.sh              # acceptance matrix, 26 assertions
scripts/release-check.sh v0.1.0     # everything verifiable before publishing: versions, six
                                    # cross-compiles, asset names, magic bytes per platform
scripts/build-plugin.sh             # build the binary into the plugin tree (and .ps1 on Windows)

graphify . --code-only && graphify cluster-only --no-label   # rebuild the code graph — see below
```

There is no Makefile, and no CLI framework: `cmd/batten/main.go` dispatches with a plain
`switch os.Args[1]`. That is deliberate (zero CLI dependencies, `CGO_ENABLED=0`, one static binary).

---

## Add code the way this repo already grew: modular, and additive at the seams

**An improvement must not put a finished module at risk.** The packages under `internal/` each own
one concern — `spec` parses, `store` persists, `install` packages, `hook` decides. New work goes
*next to* a finished module through its existing surface, not *through* its middle. Concretely:

- **Prefer a new file to a bigger one, and a new function to a wider one.** A change that only adds
  cannot regress what was passing. `winres_test.go` and `version_stamp_test.go` are new files for
  exactly this reason: neither can break a test that already existed.
- **Cross a package boundary through its exported surface.** If a change needs a package's internals
  opened up, that is the signal to ask whether the concern is in the right package — not to widen
  the API and move on.
- **Keep the write-sets in `batten.yaml` real.** Two domains (`cmd`, `internal`) exist so parallel
  work is fenced. Code that makes every change touch both domains defeats the fencing that this tool
  exists to provide, and `batten scan-diff --strict` will name it.
- **Expand-only, always.** Migrations already obey this (`len(migrations) == schemaVersion`). Apply
  the same instinct to spec fields, envelope codes and CLI arms: add a case, do not repurpose one.
- **A module is "complete" when its guards say so.** Before changing finished code, run its package
  tests first, so you know whether you broke it or found it broken.

**The counter-pressure, which is the whole point of writing this down:** modularity is not a licence
to duplicate. *One contract, one home* outranks it — this codebase's worst defects began as a rule
copied into a second place "to keep the modules independent". When the choice is between duplicating
a rule and importing across a seam, **import**.

---

## Rejected — do not reintroduce

Each of these was argued and closed. They keep coming back because they sound good, so they are
listed rather than remembered. Reopening one needs a *new* argument stated as such, not a patch that
quietly reinstates it.

| rejected | why |
|---|---|
| **Optimistic concurrency with LLM self-repair** | putting the model in charge of judging whether its own write collision "matters" reintroduces the exact failure the gate kills. What *is* adoptable: extending `advise()` to low-severity collisions — with the severity decided by a **rule, not the model** |
| **Fail-closed hooks** | SQLite under contention returns `SQLITE_BUSY`; fail-closed turns that into a denial of *every* tool call. Noisy fail-open is what ships |
| **`models.*` in the spec** (tiers, phases) | batten does not orchestrate, so it cannot keep the promise. Deleted rather than implemented |
| **`resources.*`** (kind, probe, unit, priority) | the schema promised the orchestrator would probe and queue. Same argument, four more fields |
| **Receipt-Driven Development as a frame** | batten already has its envelope; adopting outside vocabulary for the same mechanism adds concepts without adding enforcement |
| **A "Strict TDD mode"** | what batten injects comes from the user's `batten.yaml`, never from its opinion about how they should work |
| **A CLI framework (cobra et al.)** | a plain `switch` keeps the dependency list empty and the binary static |

`declaredAsFuture` in `internal/spec/declared_test.go` is **empty, and stays empty**. A new entry is
a decision to publish a promise batten does not keep.

---

## Before you call something done

The gate is a function of your diff, not a fixed list:

| if you touched | then |
|---|---|
| anything | `go build ./...`, `go test ./...`, `go vet ./...`, `gofmt -l .` clean |
| a spec field | update `batten.schema.json` **and** every `examples/*/batten.yaml` — CI validates the examples against the published schema, and it has been red for exactly this |
| `scripts/*.sh` or `.ps1` | re-copy into `plugin/claude-code/scripts/`; new `.sh` files are tracked `100755` |
| a hook's decision | a differential control, both directions, quoted in your report |
| the store | the migration is expand-only and `len(migrations) == schemaVersion` |
| a reference document | both languages, both switches, links resolving |
| behaviour of any command | the matrix scenario goes in `scripts/matrix-*.sh`, not in a document |

---

## The rule that matters most: `BATTEN_DB`

**Without `BATTEN_DB`, every `batten` command writes to the real database of whoever is running
you** — `~/.batten/batten.db`, which holds their actual work. Before *each* manual `batten`
invocation:

```bash
export BATTEN_DB="$(mktemp -d)/batten.db"    # a sandbox, per experiment
```

Two paths it must never point at: `${CLAUDE_PLUGIN_ROOT}` (wiped on every plugin update) and
`${CLAUDE_PLUGIN_DATA}` (the hooks have that variable and your terminal does not, so state silently
splits into two databases).

The matrix scripts already do this for you. Ad-hoc commands do not.

---

## Verifying anything about the gate

**Silence is not proof of ALLOW.** `batten hook` prints nothing and exits 0 for at least six
different reasons — a genuine allow, no `batten.yaml`, a store failure, malformed stdin, a recovered
panic, an unknown event. A PASS observed without a control proves nothing at all.

So every claim about the gate needs a **differential control**: the same payload with one field
changed so a denial is mandatory. If the control is also silent, the hook never engaged.

The same discipline in the suite: **a fix ships with a test that FAILS against the previous commit.**
Revert the *behaviour* and leave the symbols standing — reverting a whole file fails to compile, and
a compile error proves nothing about the fix.

**And when a guard fires on legitimate work, narrow its claim — never widen the exception.** The
distinction that matters is between a guard being *wrong* and a guard being *too broad*. The
archive-name check greps `.goreleaser.yaml` for `{{ .Version }}`, because an asset name carrying a
version cannot be fetched from `releases/latest/download`. That rule is right. Its reach was not:
it read the whole file, so it also fired on `before.hooks` and on ldflags, where the template is
exactly what should be there. The fix was to scope the grep to the `archives:` block — **and then
to prove, with a control, that it still fails on a versioned `name_template`.** A narrowed guard
that was never re-tested against its own positive case is a deleted guard with extra steps.

---

## Documentation rules

**1. Every reference document exists in both languages.** English is `NAME.md`, Spanish is
`NAME.es.md`, and both carry a language switch on the third line — immediately after the H1 and one
blank line. Taking the README pair as the model:

```markdown
> **English** · [Español](README.es.md)     ← in the English file
> [English](README.md) · **Español**        ← in the Spanish file
```

The eight pairs today: `README`, `ROADMAP`, `CHANGELOG`, `DESIGN` at the root; `INSTALL`,
`QUICKSTART`, `ARCHITECTURE`, `FIELD-TEST` under `docs/`. **A new reference document is not done
until both halves exist**, and a translation is not done until the switch points both ways.

Inside a Spanish document, links go to the `.es` sibling when one exists, with a parenthesised
English pointer beside it — the form `([en](README.md))`. English documents always link English.

**Never translate:** code blocks, literal CLI output quoted in prose (that is real program output),
paths, identifiers, field names, test names, finding numbers, SHAs, URLs.

`DESIGN.md` is a **dated historical record** from before the rename — it still says `loom`, and it
carries a translation table. Do not modernise it. Rewriting a dated document backwards turns it into
a forgery, which is what its own header says.

**2. `TestEveryRelativeLinkResolves` only scans *tracked* markdown** (`git ls-files "*.md"`). A new
translation does not trigger the guard until it is staged, and a link to a sibling that does not
exist yet breaks it. When creating pairs in a batch, stage them together or the guard lies in both
directions.

**3. The document that describes a fix goes stale the moment the fix lands.** This project produced
that defect four separate ways; three are now mechanised —
`TestNoCommentPointsAtADocumentThatIsNotHere`, `TestEveryRelativeLinkResolves`, and
`TestEveryDocumentThatDrivesAVerdictNamesBattenCheck`. After closing anything, ask which document
just started lying.

**4. Never publish a number without its counting rule.** "47 commands" lived in a plan for days
because nobody wrote how it was counted — the real answer is 29 switch arms, one of which is
`version` with three spellings. The repo has separate counts for the same question in three files.

---

## Privacy

This repo was validated against a private project, and its name leaked into code, scripts and docs
before anyone noticed. `TestNoPrivateProjectTokensAreTracked` and the CI personal-path guard hold
that line now. Two things follow:

- **Never commit an absolute home path** (`C:\Users\…`, `/home/…`, `/Users/…`). Use `~` or a
  placeholder. A JSON settings file escapes its backslashes, which is how the first version of that
  guard ran green for weeks over a real path.
- **The rule covers examples, comments and the prose that explains the rule.** There is no "but it
  is only an illustration" exemption. `.goreleaser.yaml` once pasted the author's real path into a
  comment whose subject was *why `-trimpath` removes personal paths* — the sentence explaining the
  fix committed the defect. If you are quoting a path to make a point, the point survives
  `C:/Users/<user>/<...>/file.go`; the privacy leak does not.
- **Run the guard before pushing, not after CI runs it for you.** Every check in the `lint` job is
  a shell one-liner you can paste into a terminal; the personal-path one is a single `git grep`.
  Discovering the violation from a red PR costs a round trip and puts the bad bytes on a remote,
  where "it was only in a branch" is not the same as never having pushed it.
- **`graphify . --code-only`, always.** Plain `graphify update .` indexes documents too and pulls
  prose into a committed 2 MB JSON that no diff review will ever read — and CI's personal-path guard
  excludes `graphify-out`. `.graphifyignore` is the second line of defence, not the first.

---

## Limits — batten's, and yours while working here

**What batten deliberately does not do.** It does not orchestrate: it never chooses a model, spawns
an agent, or runs a loop. When a capability would require driving the session, the answer is to
*verify* instead. Seven spec fields were deleted rather than implemented for exactly this reason.
It also does not compress context, does not store episodic memory, and never sends anything over the
network.

**Known blind spots, declared rather than hidden.** The Bash write-set guard cannot see a write made
through a `python` heredoc, a Makefile target, or any third-party tool — no shell parser reaches
there. `batten scan-diff` is the structural complement, because git cannot be fooled by a heredoc.
The open findings are listed in the CHANGELOG under *Known gaps*, with the ones that are limits
marked as limits rather than as debt.

**What you must not do here without being asked.**

- **No `git push`, no tags, no releases.** A published tag freezes content and cannot be taken back.
- **Do not weaken a guard to make something pass.** If a guard fires, the guard is usually right;
  the two times it was wrong, the fix was to narrow its claim, not to delete it.
- **Do not edit `plugin/claude-code/scripts/`** — those are copies. Edit `scripts/` and re-copy, or
  CI fails on the mismatch.
- **Do not touch `~/.batten/batten.db`,** or any path outside this repo.

**`docs/general/` is gitignored** and will not be in a clone. On the author's machine it holds the
Spanish working documents — the live plan (the newest `plan_*.md` there, each superseding the last)
and the long manual of what currently exists. If the directory is present, read the newest plan
before proposing work: every item in it carries an acceptance criterion, a cost and its
dependencies. **Never link to those files from a tracked document** — the link would resolve for the
author and break for everyone else, which is the exact defect three guards in this repo exist to
prevent.
