# Changelog

What has actually landed, newest first. Fixes found by *using* batten on itself are marked
**[dogfood]** — they are the ones that justify the exercise.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Nothing has been released yet.

## [Unreleased]

### Added
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

### Known gaps
See [ROADMAP.md](ROADMAP.md). The short list: no release has been tagged, batten has never been
installed on a repo other than this one, and the graph's documentation layer has no edges into the
code layer.
