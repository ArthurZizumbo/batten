# Changelog

What has actually landed, newest first. Fixes found by *using* batten on itself are marked
**[dogfood]** — they are the ones that justify the exercise.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security

- **The bootstraps verify the release's sha256 before installing anything.** Both scripts
  downloaded 14 MB and checked nothing but that the binary answered `version` — which a hostile
  binary answers happily, and seven hooks plus the MCP server execute that file. GoReleaser has
  published `checksums.txt` with every release since before the first tag, for nobody to read.
  They now fetch it, pull out **their own asset's line** (a bare `sha256sum -c` fails every time:
  the file lists all six assets and five are not on disk) and compare.

  This is the one part of the bootstrap that **fails closed**, and the code says so in both files
  so nobody "fixes" it later. A wrong hash, an unreachable `checksums.txt`, a sums file that does
  not list this asset, and a machine with no sha256 tool are the same sentence — nobody can vouch
  for these bytes — and they get the same answer: nothing is installed, the cache is not seeded,
  stderr names the url, the expected hash and the one it got. The script still exits 0, because
  `hooks.json` dispatches `bash bootstrap.sh || powershell bootstrap.ps1` and a non-zero exit
  there means "there is no bash", not "the download was bad".

  The accepted consequence, written down rather than discovered: a corrupt first download leaves
  the machine with **no gate**. That is already what every failed download leaves behind, the
  alternative is remote code execution by download, and it only ever applies to a first install —
  from the first good one, the cache restores the last *verified* binary without network.

### Changed

- **`declaredAsFuture` is empty: every field `batten.yaml` accepts now has a reader.** Sixteen
  entries, then seven, now none. The list is the mechanism that does to batten what batten does to
  its users — declare a field and you must wire it up or write down, in a review, that you are
  shipping a promise you do not keep.

  The last seven left by **both** exits, which is the point:

  - `phases[].when` and `domains[].coverage` were **wired**. Both are advisory by contract, and
    advisory is not the same as unread: the phase briefing at SessionStart now prints the condition
    and the declared coverage floors to the agent standing in the phase, which is the only reader
    such a field could have. Both say *advisory* where they print, because a floor rendered without
    that reads like a check batten performed.
  - `resources` and `domains[].resources` were **removed**. The schema said in as many words that
    *"the orchestrator runs it BEFORE launching and queues"*, and batten does not orchestrate —
    the same argument that removed `models.tiers`. Four fields promising serialization that nothing
    serialized. Contention now belongs in `domains[].invariants`, which ride verbatim into the
    agent's prompt: a rule an agent reads beats a field nobody ran.
  - `budget.on_exceed: warn` was **wired** and `downgrade_effort` **removed**. Only `block` had ever
    been implemented, so a spec choosing the softer setting behaved exactly like one with no ceiling
    — and `on_exceed: warn` is what `batten init` writes by default, which put every freshly adopted
    repo in the dead branch. Which severity applies is decided by the **spec**, never by the model
    and never by the size of the overrun.

  **Migrating:** a spec still carrying `resources:` keeps loading — batten does not brick a repo
  over a key it stopped reading — and `batten doctor` reports it as an unknown key.
  `on_exceed: downgrade_effort` does not load, and the error names the removal rather than treating
  it as a typo.

### Added

- **`batten scan-diff` now keeps its contrast, and `batten measure` reports it.** The command
  already computed the one number nobody else in this ecosystem reports — how far a hand-declared
  write-set over-declares — and wrote it to stdout, where it died. The denominator had been in the
  database since v1 (`writesets` never deletes a row); the numerator was computed and thrown away.
  Migration **v12** adds a `scans` table, one row per scan, and `batten measure` grows a
  *write-sets* block: the median `unused/claims` across scanned runs, with the N it was computed
  over.

  Three rules keep it from flattering itself. A run that claimed paths and was never scanned reads
  **NOT MEASURED**, never 0% — otherwise the median describes whichever runs somebody bothered to
  check, and those are the ones somebody was already worried about. The median is over runs, taking
  each run's newest scan, so a run scanned six times does not count six times. And `unused` is
  labelled an **upper bound** everywhere it is printed: it mixes "claimed too much" with "the file
  legitimately needed no change", so it picks between actions rather than measuring dishonesty.

  `batten scan-diff --strict` is now in this repo's own `gates.checks`. That is the accumulation
  mechanism, not a flourish: nobody remembers to run scan-diff by hand, so without it the ≥10
  scanned runs the pre-registered threshold needs would never arrive.

### Fixed

**The six field-test findings that blocked adoption are closed**, each with a test that fails
against the commit before it. They were triaged on one question: does it stop an outside adopter
reaching the end of the flow, or break the central promise in silence?

- **#4 — `claim` handed out a fence it could not honour.** It only ever looked inside its own run,
  so the second run of a project claimed the same path, was told *"any other agent writing them is
  now denied"*, and then the guard denied **both** owners at write time. Half the mechanism already
  existed and was never called from here: `store.WriteSetOwnerAcrossOpenRuns`, the same query the
  write guard uses. The discovery moves from mid-fan-out, to both agents, to claim time, to the one
  who can still change the plan. Two worktrees are still allowed — that is the arrangement batten's
  own messages recommend.
- **#7 — a claim outside the repo root was accepted with the same success line.** The guard compares
  repo-relative paths, so it could never match: an imaginary fence around a file batten will never
  guard. Refused by name now, and a relative argument resolves against the root.
- **#16 — the documented flow ended at a deny naming a command the documents did not contain.** With
  `checks:` declared, the gate wants two verdicts from two producers, and no `/batten-*` command or
  skill mentioned `batten check`. `/batten-verify` said to run the gate's checks by hand, which
  produces citations rather than a batten-sourced pass. A guard now holds the rule rather than the
  list: every document that names `batten verdict` must name `batten check`.
- **#27 — evidence containing JSON objects returned the Go decoder's own sentence.** At the moment
  of an adopter's first verdict, `json: cannot unmarshal object into Go struct field
  Verdict.evidence of type string` named a Go type and a struct field that appear nowhere in the
  documentation they were following. The error now names the field, the shape it wants, and the
  convention that replaces the object (`AC-<n>:` as a prefix) — rejecting without teaching sends the
  agent guessing, and it guesses objects again.
- **#50 — a commit closed the unit the session started on, not the unit it names.** The gate had
  learned to read the commit message; the close path had not. On trunk, where the branch names
  nothing, `feat(TASK-002)` was gated as TASK-002 and closed TASK-001 — marking ok a unit nobody
  committed and releasing its write-set claims while it was still being worked. Both sites now
  resolve the unit through one function, because answering the same question in two places
  differently is what produced the bug.
- **#59 — the first thing batten asks anyone to do committed batten's own database.** `batten init`
  wrote `batten.yaml` and said nothing about `.gitignore`. It now adds `/.batten/` when it is
  missing, appending rather than rewriting — `init` is a first-contact command in somebody else's
  repo — and says that it did.

- **The private field-test subject is no longer named outside `docs/field-test/`.** batten was
  exercised against a real private repo, and that repo's name had not stayed in the report: it had
  reached `internal/scan`, `cmd/batten`, `internal/hooks`, both matrix scripts, the ROADMAP and
  `docs/FIELD-TEST.md` — 10 tracked files of live code, tests, scripts and docs, none of which any
  decision about `docs/field-test/` would ever have touched. Where the text means the replica the
  subject is now `replica-ui`, the name the scripts already used; where it means the real repo it
  is described without being named.

### Added

- **A guard against the same leak, on both readings.** `TestNoPrivateProjectTokensAreTracked`
  (`internal/install`) and a matching `ci.yml` step. It needed the *opposite* exclusion list from
  the personal-paths guard beside it, and getting there took two findings: a token is not a path,
  so `:!graphify-out` had to go — and dropping it was still not enough, because `.gitattributes`
  marks the generated graph `-diff` and **`git grep -I` skips `-diff` files as binary**. A token
  planted in `graph.json` walked past the guard that had already dropped the path exclusion. The
  guard runs without `-I`; it was verified red against the previous commit, red with the token
  planted in `graph.json`, and green on the swept tree.

## [0.1.0-beta.1] — 2026-07-29

The first tagged version. It is a **beta** for one honest reason: batten has never been installed
on a repository other than this one, by anyone who did not write it. Everything below has been
exercised, and most of it was found by exercising it.

### The field test, and what it changed

Before this version, batten was run against a replica of a real four-domain project. That produced
**80 findings**; the 63 that had not been fixed on the spot went through an adversarial verifier
that tried to refute each one, leaving **52 confirmed and 11 refuted**. Those 52 were then worked
in four blocks. **37 are fixed and verified at this tag; 15 remain open** and are listed under
*Known gaps* rather than quietly carried.

That ratio is the honest headline of this release. The findings, with their reproductions and
evidence, are in [`docs/field-test/`](docs/field-test/).

The single most useful thing the field test taught was structural: a third of the findings were
**one failure repeated nine times** — batten declaring a governance capability it did not impose.
That is precisely the failure batten exists to remove from other people's workflows, so the fix
was a mechanism rather than nine patches. Four guard tests now hold the line:

| guard | what it imposes |
|---|---|
| `TestEveryDeclaredFieldHasAConsumerOrIsDeclaredFuture` | every `batten.yaml` field has a production consumer, or an explicit entry with a reason |
| `TestDeclaredAsFutureHasNoStaleEntries` | that list cannot document promises that no longer exist |
| `TestEveryUnattendedRuleIsMechanicalOrRegisteredAsProse` | every absolute rule of `/batten-night` has a mechanism whose identifier is *used*, not merely declared |
| `TestEveryEdgeRelationReadHasAProducer` | every `edges.rel` a surface reads has something that writes it |

Adding a field and forgetting it is no longer possible. The list of declared-but-unimplemented
fields went from 16 to **7** — four wired up, three deleted from the spec entirely.

### Added

- **Five process rules became mechanical denials**, joining the two that already were (the commit
  gate and the write-set guard). Merging a worktree without both verdicts, deleting anything during
  an unattended run, `batten override` while nobody is watching, committing during an unattended run
  *with* the verdicts in place, and exceeding the iteration ceiling — all of them used to be prose
  asking the model to behave, in the most dangerous command the plugin ships.
- **`batten worktree`** — one tree per work item, created, registered and anchored, with the merge
  back gated by the same condition as a commit. batten had recommended this in three separate
  messages and then *punished* it: two units in two trees editing the same relative path looked like
  two sessions fighting over one file, and the guard denied both. The lock lives in the
  **git-common-dir**, because a linked worktree's `.git` is a *file* and a lock relative to it is
  per-worktree — every process takes its own and the mutual exclusion is imaginary.
- **`batten unattended` / `batten iterate`** — the unsupervised loop's iteration ceiling is counted
  and enforced. It was declared in the spec, returned over MCP, *drawn in the TUI* as `iters %d/%d`,
  and never incremented by anything: `runs.iterations` was 0 forever. None of the four unattended
  denials carries a `fix` field, deliberately — the way out is `--off`, and printing that to a loop
  nobody is watching hands it the key to its own fence.
- **`batten status`** — the backlog against the record: every work item the plan document defines,
  with its run state and its acceptance-criteria coverage, *including the ones nobody has started*.
  That is the half `batten runs` cannot show. Ad-hoc work is listed separately so the view never
  implies the backlog is the whole world.
- **Acceptance criteria as data.** "Criteria" appeared ten times in the codebase's prose and zero
  times as data. A new `internal/plan` reads `unit.plan` + `unit.locator` — which `batten init` had
  written from day one and nothing read back — into work-item blocks; `batten phase` seeds a
  `criteria` table from the item's block; and an **approving** verdict covers exactly the criteria
  its evidence cites as `AC-<n>:`. A `blocked` verdict naming `AC-2` is describing what failed and
  covers nothing. `batten pr` now says *"AC-1 covered by X"* with the uncovered ones named out loud,
  and the gate-phase briefing lists the numbering so a reviewer can use it without asking.
- **`batten scan-diff`** — asks git what changed and the ledger who claimed what, and contrasts
  them. Deterministic, no shell parsing, no false positives, so a code generator, a Makefile target
  or a `python` script is as visible as a `sed`. It refuses to conclude two things it cannot know:
  *who* touched an unclaimed file, and that a run with zero claims is clean.
- **`batten report`** — what batten saw and what it *stopped*, with a `--share` markdown form. Every
  count states the date it started counting: "2 commits denied" reads as an all-time total when
  batten may have been counting since Tuesday.
- **`batten pr`** — the pull-request body from the record: a Mermaid DAG that GitHub renders
  natively, the verification table with cited evidence, the criteria coverage, and the cost. If
  usage was not measured it says `NOT MEASURED`, never `$0.00`.
- **`batten canvas --html`** — the run graph as one self-contained ~10 KB page, no network request
  at all. And the JSON Canvas export for Obsidian.
- **`batten demo`** — the whole flow on a throwaway git repo in about 30 seconds, touching nothing
  of yours. Adoption used to take ~8 steps to reach a denied commit.
- **`batten recover`** — re-anchors a run whose base moved, and says *what* happened to the old
  anchor. "Someone edited your file" and "the history moved under you" need opposite advice, so the
  tree fingerprint stores the commit and the tree separately.
- **`batten doctor` diagnoses everything in one pass**, with the correction beside each problem. It
  used to stop at the first fatal, which is how people give up on the third iteration.
- **A typed failure envelope on every denial and every warning** — `batten.code` (a stable string
  identifier), `batten.fix` (the exact command), `batten.retry` (whether re-running the same call
  could work). 17 codes. `retry` is the expensive one to get wrong: a missing verdict is *not*
  retryable, and a loop that retries it burns the window on an identical denial.
- **The run graph got typed edges with actual producers**: `retry_of` had five readers and zero
  writers — `batten pr` counted retries for its badge, the canvas painted the edge orange, the vault
  note listed it, MCP answered `retries: N`, the TUI hung it off the node — and the row had only
  ever been inserted by hand. `depends_on` had colour in the canvas and no producer: the graph
  called itself a DAG and had not one edge between two phases.
- **A Bash write guard**, advisory for one measurement cycle. `Edit` on a claimed file was denied
  and the byte-equivalent `sed -i` passed in silence, so the guard the whole fan-out safety argument
  rests on was one `sed` from optional.
- **The three-memory orientation chain reaches the subagent that writes code.** It consulted
  nothing and started by reading files, the most expensive of the three options. The instruction is
  injected by the binary into the phase briefing, and it *requires* saying so when neither memory
  answered — an agent asked to consult two tools will report having consulted them either way.

### Fixed — installation, which is where a first release is actually judged

Five of these were blockers found by auditing the *distribution*, not the engine. batten worked; a
person receiving batten did not.

- **The binary was downloaded to a path nothing invokes.** `bootstrap.sh` installed into
  `${CLAUDE_PLUGIN_DATA}/bin` while the eight hooks in `hooks.json`, the MCP server in `.mcp.json`,
  the bare `batten` in every `/batten-*` command (which resolves only because Claude Code puts a
  plugin's `bin/` on PATH) and `batten doctor` all name `${CLAUDE_PLUGIN_ROOT}/bin/batten`. A release
  install therefore printed `installed` and then: no hook ran, the MCP server never started, every
  bash block was `command not found`, and doctor reported *"the gate is not running at all"* about a
  machine where the bootstrap had just succeeded. One contract written in four files, and nothing
  compared them. `${CLAUDE_PLUGIN_ROOT}/bin` is now the destination; `${CLAUDE_PLUGIN_DATA}/bin` is a
  cache that survives plugin updates, so an update costs a copy instead of 14 MB.
- **And the same bug made itself permanent.** The first check was `command -v batten`, which a dev
  build, a `go install`ed copy or a stale PATH entry all satisfy — so after an update wiped
  `$ROOT/bin`, the bootstrap declared victory over an empty directory forever. The check now names
  the file, because naming PATH *was* the bug.
- **Both `bootstrap.sh` copies were committed without the execute bit** — `Permission denied` on
  macOS and Linux, the two platforms this repo's author cannot reproduce, with the binary never
  downloaded and every hook no-opping in silence. Same class as the CRLF problem `.gitattributes`
  already solves, and it now has the same treatment: the mode is fixed, CI refuses a tracked `.sh`
  that is not `100755`, and `hooks.json` names the interpreter so a bit lost in transit degrades to
  a working install instead of a dead gate.
- **Windows without Git Bash had no way to install at all**, on the platform this project declares
  primary. `bootstrap.ps1` (PowerShell 5.1, the one in the box) and `bootstrap.cmd` now exist, and
  the hook dispatches with `bash … || powershell …` — which is unambiguous because both scripts exit
  0 even when the download fails, so the fallback fires on exactly one condition: no bash here.
- **`tar` broke the Windows download**, found by running the new script rather than reading it: `tar`
  on PATH is usually Git Bash's GNU tar, which reads `C:\Users\…` as a *remote host* — "Cannot
  connect to C: resolve failed" — and unpacks nothing. The PowerShell path now calls
  `System32\tar.exe` by full path; the shell path stopped passing absolute paths to tar at all.
- **Every `/batten-*` command now refuses to run without the binary** instead of stepping over a
  `command not found` and completing the phase ungated.
- **`batten.schema.json` rejected this repo's own `batten.yaml`.** `provenance.format` and
  `models.*` were deleted from the struct and the schema in one commit and left behind in
  `batten.yaml` and two examples, so an editor validating against the published schema called the
  file invalid while `batten doctor` called it perfect — and CI's schema job was red on the spec
  that *is* the product.
- **A typo in a top-level key is no longer silent** — this was *Known gap #1*. `enforcment: report`
  made doctor print a green `enforcement: enforce — gates block` and exit 0. `batten doctor` now
  names every key batten does not read. Loading still tolerates them, because a spec written for a
  newer batten must work on an older one; what changes is that you are told. **14 of the 52
  confirmed findings remain open, not 15.**

### Fixed

- **`batten measure` under-reported token spend by a factor that depended on the traffic** — 21.9×
  in the field test, 107.7× in the re-verification. It summed input+output while
  `runs.tokens_spent`, recomputed from the *same rows*, summed all five buckets. The invariant is
  now a test: `SUM(measure) == SUM(runs.tokens_spent)` over the same rows.
- **A model with no published rate printed `$0.00`** under a heading reading "spend by model",
  byte-identical to a measured row that genuinely rounds to zero. Those are opposite facts and no
  longer share a rendering.
- **A partially priced run presented its dollar figure as a total.** With 38 % of a run's tokens on
  an unpriced model, `$0.39` is a floor. The unpriced share now travels *on the run record*, so no
  surface can fail to see it — `budget`, `runs`, `show`, the TUI, MCP, the PR, the canvas and the
  vault note all render `≥$0.39` and name the gap. Four surfaces had each been formatting that
  number on their own, which is exactly how they came to disagree.
- **An override was invisible across the entire CLI.** After `batten override`, the commit gate
  flips to allow — and `batten show` kept printing *"the close gate will deny a commit"*, the
  literal opposite of the truth, in the text an agent plans against. `store.OverrideFor` returns the
  reason and the timestamp, and the four surfaces that were not asking now ask, in the order the
  hook decides in: the override first, because it makes every other answer moot.
- **A commit batten could not attribute passed in silence.** Failing open is the right call — a tool
  that denies what it cannot verify gets uninstalled on day one — but failing open *silently* is
  worse than having no gate, because the gate is believed. It now says what is not being gated, and
  names the fix.
- **A node id that did not carry its run was not an identifier.** Phase ids were the global string
  `"p-" + phase`, one row for the whole database: the second work item to enter `build` took the
  first one's row, and the first one's canvas collapsed to a bare heading.
- **`batten check` alone could close a unit.** The gate needs two verdicts from two different
  producers — batten's, proving the declared checks *ran*, and a reviewer's, judging the work
  against its criteria — and three surfaces were reading "the latest row", which is always
  `batten check`'s. Its output was painted over the reviewer's evidence, and a run nobody had
  reviewed was filed as approved.
- **"Verified" now means verified against *this* tree.** `batten check` proved the checks passed and
  recorded no trace of *what* they passed against, so a formatter between the check and the commit
  left the verdict claiming `batten-verified` about a state that no longer existed.
- **batten could invalidate its own verdicts by writing its own ledger.** The first tree fingerprint
  hashed everything git reported, so *recording* the verdict changed the tree the verdict was about.
- **The write-set fence compares files, not filenames** (path + `os.SameFile`), and case-folds where
  the filesystem does — otherwise an agent crosses the fence by changing a letter's case.
- **A directory or glob claim is refused** instead of being accepted, reported as protection, and
  fencing nothing. The guard matches exact paths, so `src/**` was a false fence — and a false fence
  is worse than none, because the plan trusts it.
- **Opening a run is `batten phase`'s job and nobody else's.** `batten check` on a closed unit
  silently forked a second run with no anchor and no phase, exit 0, and `batten show` then displayed
  only that empty fork.
- **Work-item ids are validated.** `batten phase FOO-9 build` opened a phantom run with exit 0 while
  the same command hard-rejects a phase that does not exist. The pattern is anchored whole-string:
  with `US-\d{3}`, `US-0001` used to slide through on its `US-000` prefix.
- **`batten show <unit> --run <id>`** resolves that run. It used to discard the flag and its value,
  print the latest run and exit 0 — even for an id that does not exist.
- **Token counts render at their own scale.** The session brief showed 42,600 measured tokens as
  `0.0M`, an apparent zero, in the one line an agent reads before deciding whether there is budget
  to work with. Five packages carried a private copy of that formatter and a sixth hand-rolled
  `%.1fM`; there is now one.
- **Windows exit codes stopped being garbage.** A process that dies abnormally reports a negative
  NTSTATUS, and the raw value rendered as its unsigned 32-bit wraparound: `exit 4294963238` instead
  of `-4058` — and that value was *persisted* into the verdict evidence, which `batten show` replays
  forever.
- **`batten tui` refuses a non-terminal stdout** instead of emitting 96 bytes of terminal setup,
  rendering nothing, and never exiting.
- **`batten init --help` prints usage** instead of writing `batten.yaml` and exiting 0 — it was the
  only command that both writes a file and had no default arm in its flag switch. `--from` now
  requires the document to exist and *records* it as `unit.plan`; it used to be a pure stdout echo,
  producing a byte-identical file whether you passed it or not.
- **The stale-run warning can be cleared.** Its predicate reads `events.run_id`, and the journal
  wrote NULL into that column on every row, so the "no activity" half was dead code and a run being
  worked right now was reported stale on age alone.
- **Cards no longer overlap in the canvas.** A phase card spans 120 px and its first subagent
  started at 60 — half a card of overlap, on the surface that exists to be looked at.
- **`enforcement: report` is recorded on every decision.** Without that, "we ran in report mode for
  three weeks" has no record of what it cost — which is the other half of a kill switch worth having.
- **SQLite contention is classified.** A `SQLITE_BUSY` is transient and says so in the envelope;
  treating it as fatal would brick a session, and treating a missing verdict as retryable burns the
  window. Identity is device+inode based, not name based.
- **A tag can no longer publish over a red suite.** `release.yml` went straight to building
  binaries; a `verify` job now runs the full suite on all three platforms first, and checks that the
  plugin manifests agree with the tag.

### Removed

- **`models.tiers`, `models.phases` and `provenance.format` are gone from the spec.** The schema
  claimed *"batten routes subagents and verifies it from the ledger"* and batten deliberately does
  not orchestrate, so that promise could never be kept; `provenance.format` had neither a writer nor
  a reader. Per-domain routing survives as `domains.<name>.model`, which batten *does* verify
  against the usage ledger — `batten show` flags "declared haiku, ran opus" as a deviation instead of
  a silent overspend. A field a user writes believing it governs, and that does not govern, is worse
  than its absence.

### Known gaps

**14 of the 52 confirmed findings are still open at this tag.** They are listed here rather than
carried quietly, because a release that hides its own defect list is the artefact this project
exists to argue against. Reproductions for all of them are in
[`docs/field-test/verified.json`](docs/field-test/verified.json).

Finding #1 — a silently ignored top-level key — was the other one that mattered most, and it is
fixed above. It had never been triaged into any block, which was its own lesson: the gap list was
where it went to be remembered instead of fixed.

The one that matters most of what remains:

- **`batten claim` only looks for collisions inside its own run** (#4), so a second run can claim a
  file another run's agent already owns, be told *"any other agent writing them is now denied"*, and
  then the hook denies **both** declared owners. The worktree work resolved this structurally for
  the worktree-per-unit arrangement, but nothing requires, creates or even warns toward a worktree,
  so in a single checkout it reproduces exactly as filed.

The rest, in one line each: a write-set claim outside the repo root is still accepted with the same
false assurance (#7) · the write-set stores and reports the case-folded path, so
`useTrace.ts` comes back as `usetrace.ts` (#43) · unit attribution reads the commit message but the
documented happy path still omits the `batten check` step a gate with `checks:` requires (#16, #50)
· a phase that diffs from a missing anchor now warns at runtime but `doctor` does not (#24) ·
`batten runs` prints no run id, start time or age (#23) and drops the "checks ran" mark (#28) ·
`measure` still prints a headroom heading in a repo that never declared compression (#34) · the TUI
run list labels 113 % as "quota" while the detail pane says 17.0 % for the same quantity (#47) ·
`batten init` writes no `.gitignore` entry for `.batten/` (#59) · a verdict whose evidence items are
objects instead of strings fails with a raw Go decoder error (#27) · a cross-fence write through a
`python` heredoc or a Makefile target is still invisible to the Bash guard, which is a stated
boundary rather than an oversight — `batten scan-diff` is the after-the-fact complement (#6, #60).

Beyond the field test:

- **The plugin has never been installed from a published release**, because there has not been one.
  The audit that found the three install blockers above is closed, and the path is now executed
  rather than read: `scripts/release-check.sh` cross-compiles all six platforms and checks each
  archive's name and magic bytes, and the suite drives the real `bootstrap.sh` and `bootstrap.ps1`
  against a real archive over a local server. What no local check can prove is that
  `releases/latest/download` resolves — that needs the tag.
- **The bootstraps verify a checksum but no signature.** The sha256 half is closed (see
  *Unreleased*): both scripts now read the `checksums.txt` GoReleaser already published and refuse
  to install what does not match it. What a checksum cannot cover is a compromised release account
  — the same hand that replaces the asset replaces its line in the sums file. That needs minisign
  with a locally-held key, and it is 0.1.0's work, not this tag's.
- **The transcript format batten parses is not a public API.** When it breaks, batten reports the
  count as unavailable rather than guessing — correct, but the ledger can go blind without notice.
- **No GIF in the README.** The `.tape` scripts are written and verified in content; recording them
  needs vhs + ttyd + ffmpeg.

---

## Earlier in 0.1.0-beta.1 — the hardening that preceded the field test

Everything below also ships in this tag. It is kept as its own run of sections because it was
written as it landed, before the field test reframed what mattered; folding it into the lists above
would have meant rewriting entries that were accurate when they were made.

### Added
- **The vault folder explains itself.** A `<project>.md` index note now sits beside the dashboards,
  linking each one and stating the two things a reader has to know before acting on the numbers:
  imputed dollars are **not a bill** (on a subscription no token has a marginal cost), and every
  note is a projection — SQLite is canonical.
- **`batten doctor` reports graphify's git integration.** `graph.json` is committed on purpose as a
  shared artifact and is a megabyte of generated JSON, so two branches touching code conflict on it
  unavoidably. `graphify hook install` registers a union merge driver for exactly that, and doctor
  now says so when the graph is tracked without it.
- **`/batten-plan` asks the graph through `god-nodes --json` and `affected`** instead of reading
  `GRAPH_REPORT.md`, whose wording is written for humans and changes between versions. `affected`
  is the sharper of the two: a blast radius crossing a domain the plan did not account for means
  the write-sets are not actually disjoint — better found at plan time than at merge time.
- Tests for every package except `internal/tui` (a read-only viewer): `internal/spec` 94.9%,
  `internal/vault` 92.1%, `internal/canvas` 86%, `internal/export` 84.8%, `internal/store` 62.5%,
  `internal/hooks` 29.5%. Total 52.7%, from 0% in six of them.

### Fixed
- **Run ids collided on Windows.** `EnsureRun` built its id from `time.Now().UnixNano()`, which is
  unique only if two calls never land in the same tick — and Windows' clock granularity is often
  half a millisecond or worse. Closing a run and opening the next one for the same unit produced
  the same nanosecond and failed on the primary key, from the hook path, where an error is a broken
  session. Four random bytes now do the work the timestamp was assumed to.
- **"The latest run" was a coin flip.** `started_at` is stored in seconds, so two runs for one unit
  opened in the same second left `ORDER BY started_at DESC LIMIT 1` free to return either, and
  `batten show` could inspect the older one. A rowid tiebreaker makes it deterministic.
- **`batten doctor` suggested a graphify command that does not exist.** `graphify . --update` is
  not a flag; graphify ignores it, runs a full extraction, and fails on a missing LLM API key. The
  command is `graphify update .`. This is the second hint here to outlive graphify's CLI.
- Two personal absolute paths were still tracked in the docs. CI now fails on any tracked absolute
  home path or private working file.
- **CI, which did not exist.** `release.yml` fired on a tag and went straight to building
  binaries, so a tag could publish over a red suite. There is now a `ci.yml` on every push and
  pull request, and the release will not start GoReleaser until it passes. The matrix is Linux,
  macOS and Windows because batten branches on `runtime.GOOS` in three places that decide whether
  a guard holds — most of all `store.normPath`, which case-folds where the filesystem is
  case-insensitive so an agent cannot cross the write-set fence by changing a letter's case.
  CI also pins the contracts that had already drifted: the plugin's generated copy of
  `bootstrap.sh` matching its source, the download name matching the archive template, every
  example validating against the schema *and* loading in the real parser, plus `gofmt` and
  `go mod tidy`.
- **`LICENSE`.** The README, the plugin manifest and the release archive all claimed MIT; without
  the text the default is all-rights-reserved.
- **`.gitattributes`.** A `bootstrap.sh` committed with CRLF has a shebang of
  `#!/usr/bin/env bash\r`, which fails on every non-Windows machine with "bad interpreter" —
  binary never downloaded, hooks silently no-opping. It worked only because one machine happened
  to have `core.autocrlf=true`. The repo decides now, not each developer's config.
- Tests for `internal/spec` (0% → 94.9%), `internal/canvas` (0% → 86%), `internal/hooks` and
  `internal/scan`, all of which had none.

### Changed
- **The repo is releasable.** Everything points at `ArthurZizumbo/batten` — the Go module path
  and every import, both bootstrap scripts, the plugin manifest, the marketplace entry, the schema
  `$id`, the install docs. The module path is the expensive one to change after publishing.

### Fixed
- **Three things that would have broken the first release**, found by reading the release path end
  to end rather than running it. GoReleaser built `batten_0.1.0_linux_amd64.tar.gz` while
  `bootstrap.sh` fetched `batten_linux_amd64.tar.gz` from `releases/latest/download` — every
  platform would have 404'd, since that endpoint can only resolve a name predictable without the
  tag. Windows got a `.zip` while bootstrap ran `tar -xzf`, and bootstrap runs under Git Bash,
  whose tar does not read zip. And `bootstrap.sh` printed "installed" whether or not the move
  succeeded; it now verifies each step, runs the binary once, and on failure removes the
  half-installed file and says plainly that nothing is being gated.
- `docs/INSTALL.md` said state lives in `${CLAUDE_PLUGIN_DATA}/batten.db` — the exact divergence
  that caused two E0 bugs, since hook processes have that variable and the user's terminal does
  not, splitting state across two databases. It is `~/.batten`, always.
- **`batten init` reads the process a repo already has.** The scan now reports `harness[]` (agent
  rules per directory, `CONTRIBUTING.md`, other editor harnesses, build files, prose workflow docs,
  an existing spec), `stack[]` (languages and tooling from marker files that exist — package
  managers from the lockfile, never inferred from directory names), and `purpose[]` (where the repo
  says what it is for). `/batten-init` gained the interview it always claimed to have: read those
  first, then ask about the purpose, the real fan-out axes, the tracker pattern, the commands that
  must pass, and what is scarce enough to force serialization.
  - A directory with an `AGENTS.md` is now a domain even with no code under it. A repo that has
    been planned but not built returned an **empty** domain list before, which is backwards: those
    are exactly the repos where the axes are already decided and nothing else reveals them.
  - The notes claimed checks "were taken from your build files" even when none were found and the
    gate was empty. An empty gate verifies nothing, and it now says so.
  - First tests for `internal/scan`, which had none.
- **Code graph as a first-class capability.** graphify 0.9.25 wired in — 1043 nodes, 2100 edges,
  65 communities. Every run is stamped at open with whether a fresh graph existed, and
  `batten measure` compares runs with and against without, refusing to conclude anything below
  3 runs per side. God nodes from `GRAPH_REPORT.md` raise the plan phase's difficulty tier: touching
  a core abstraction is never classified as mechanical work.
- **Model routing by difficulty** — `models.tiers` / `models.phases` and per-domain `model`.
  `batten show` flags where the declared model diverged from the one actually used, read from the
  ledger rather than from intent.
- **`batten check`** runs a gate's checks for real and records the verdict with `source: batten`.
  The commit gate demands *that* verdict, which kills "wrote that it passes without running it".
- **Run lifecycle** — `batten close`, auto-close when a commit lands in the close phase, and a
  doctor warning for runs left running past 48 h (a stale run keeps its write-set claims alive).
- **Multi-session** — session↔run binding, write-set defence *between* open runs, and visible
  ambiguity when a session cannot tell which unit it is on.
- **Obsidian vault export**, automatic on the `Stop` hook and after a verdict is saved: a note per
  run with frontmatter, `.base` dashboards, and an embedded JSON Canvas 1.0 run graph.
- **`batten init`** — a real repo interview (`internal/scan`) replacing the stub, plus
  `enforcement: report` so gates can be adopted without blocking an active sprint.
- **Binary distribution** — GoReleaser on tag, and a `bootstrap.sh` that fetches the platform
  binary into `${CLAUDE_PLUGIN_DATA}` on first run.

### Fixed
- **[dogfood]** The vault run note reported `not recorded` for every write-set and never printed
  "Files touched". The rendering was correct; nothing ever populated it — `WriteSets` was set only
  in a test. Added `store.WriteSetsByRun` and wired it into the one production path.
- Three `internal/mcp` tests were red for a reason unrelated to what they assert: the time fence
  drops usage older than its run's `started_at`, and the fixture stamped its row at the epoch, so
  every seeded row was silently discarded. The fence is right; the fixture predated it.
- **[dogfood]** Usage attribution is fenced to the run's lifetime — tokens spent before a run opened
  belong to the session, not to that run. Canvas export now also works for closed runs.
- **[dogfood]** Vault export worked only for open runs, so a closed unit never reached the vault.
- **[dogfood]** `batten show` inspected only active runs and went blind the moment a unit closed.
- **[dogfood]** One database for every process: `CLAUDE_PLUGIN_DATA` was dropped from `dbPath`.
  State always lives in `~/.batten`. Two separate bugs came from that divergence.
- **[dogfood]** `bootstrap.sh` was declared in `hooks.json` but nothing copied it into the plugin
  package, so it was never there to run.
- Write-set paths case-fold on macOS as well as Windows. Both default filesystems are
  case-insensitive, and a guard that treated `ml/F.py` and `ml/f.py` as different files would let an
  agent cross the fence by changing capitalization.
- Unattributed writes warn instead of bricking the session; event-log payloads are capped.
- A panic fence around the hook command — a crash in batten must never take a session with it.

### Changed
- **The repo stops carrying what it should not publish.** A private prompt library, a personal note,
  a third-party PDF and a stray canvas were purged from the entire history. A 14.5 MB
  `bin/batten.exe~` — a Windows hot-swap leftover that slipped past ignore rules naming only
  `batten` and `batten.exe` — was untracked, and `bin/` is now ignored wholesale. graphify's
  machine-local sidecars were removed from the index, which the previous cleanup had added to
  `.gitignore` without untracking.

(The *Known gaps* list that used to close this section has been superseded by the one in
[0.1.0-beta.1](#0.1.0-beta.1--2026-07-29) above, which is measured rather than summarized.)
