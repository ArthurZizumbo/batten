# Installing batten in a project

> **English** · [Español](INSTALL.es.md)

batten installs as a Claude Code plugin, and **the binary arrives on its own**: a `SessionStart`
hook runs the bootstrap, which on first run downloads the static binary for your platform from the
GitHub Release into **`${CLAUDE_PLUGIN_ROOT}/bin/batten`**. That path is not a preference: it is
the only one the hooks and the MCP server name, and it is the directory Claude Code puts on PATH —
which is what makes the bare `batten` in the `/batten-*` commands resolve.

> *(Counting rule, because this repo published both "7 hooks" and "8 hooks": `hooks.json` declares
> **8 entries** across **6 events**. Seven invoke the binary; the eighth is the bootstrap, which is
> shell form because it has to run when the binary does not exist yet.)*

**What it verifies before installing anything.** The bootstrap also downloads the `checksums.txt`
from the same release, pulls out **its own asset's line** and compares. This is the one part of the
bootstrap that **fails closed**: a wrong hash, an unreachable `checksums.txt`, or a machine with no
sha256 tool are the same sentence — nobody can vouch for these bytes — and get the same answer:
nothing is installed, the cache is not seeded, and stderr names the url, the expected hash and the
one it got.

A copy is kept in `${CLAUDE_PLUGIN_DATA}/bin`. That is a **cache**, nothing more:
`${CLAUDE_PLUGIN_ROOT}` is wiped on every plugin update, so after one the bootstrap restores from
the copy instead of spending 14 MB again.

If you built locally, the binary is already in the plugin's own `bin/` and the bootstrap downloads
nothing.

If the download fails, it says so and the hooks no-op: **nothing is being gated**, and batten would
rather tell you than pretend it is protecting you.

### Windows without Git Bash

The hook tries `bash bootstrap.sh` and, if this machine has no bash, falls back to `bootstrap.ps1`
— PowerShell 5.1, the one that ships in the box. Nothing to install. To run it by hand (or to see
the full output):

```
<plugin-path>\scripts\bootstrap.cmd
```

It needs `System32\tar.exe`, which Windows has shipped since 10 1803. The script invokes it by full
path on purpose: the `tar` on PATH is usually Git Bash's GNU tar, which reads `C:\Users\...` as a
remote host and unpacks nothing.

### Windows and your antivirus

**Expect Defender to complain at least once, and it does not mean batten is infected.** Windows
Defender classifies freshly built, **unsigned** Go binaries as `Trojan:Win32/*!ml`. The `!ml`
suffix is a machine-learning verdict rather than a signature, and it behaves like one: it happened
to this project's own binary, with two builds of the **same** code getting different answers and an
explicit rescan of those same bytes coming back clean. That is the shape of a false positive.
batten is not signed with an Authenticode certificate yet.

It matters more than an ugly dialog. If the binary is quarantined **after** it installs, every hook
is pointed at a file that no longer exists, they die in silence, and `batten doctor` cannot tell you
because doctor **is** the missing binary. The bootstrap detects the pattern — a second restore from
cache inside a day — and says so at `SessionStart`. If you see that message, check your antivirus
quarantine.

What you can do:

- **Report the false positive** at <https://www.microsoft.com/en-us/wdsi/filesubmission>. It is
  free and it clears that binary; every release is a new one.
- **Build it yourself** — see below. A binary your own toolchain produced never crossed the network.

## Building it yourself, downloading nothing

Three routes, and none of them replaces the bootstrap: all three end in the same place, because
`${CLAUDE_PLUGIN_ROOT}/bin/batten` is the only path the hooks invoke.

```bash
# a) from a checkout — the development path; puts the binary where it belongs
scripts/build-plugin.sh          # macOS/Linux
scripts/build-plugin.ps1         # Windows

# b) go install, without cloning anything
go install github.com/ArthurZizumbo/batten/cmd/batten@latest
#    leaves the binary in $(go env GOPATH)/bin. That alone is NOT enough: it has to go
#    where the hooks look.
cp "$(go env GOPATH)/bin/batten" "$CLAUDE_PLUGIN_ROOT/bin/batten"       # .exe on Windows

# c) from the checkout, by hand
go build -o "$CLAUDE_PLUGIN_ROOT/bin/batten" ./cmd/batten
```

With the binary already in place the bootstrap sees it and **downloads nothing** — one stat and it
exits.

> **Why the copy step cannot be skipped**, even though engram allows it: engram's `.mcp.json`
> invokes a bare `engram` and resolves through PATH. batten deliberately does not, and the reason
> has a name — some `batten` on PATH satisfied `command -v batten` while the file the hooks name did
> not exist, so the bootstrap declared victory over an empty `bin/` and nothing was being gated.
> The hooks name a file, not a command.

> **Version reporting, fixed after `v0.1.0-beta.1`.** Route (a) now stamps the version the same way
> the release does, so a binary you built reports the same string as one you downloaded and
> `batten doctor` agrees with `plugin.json`. Route (b) reports the module version it was installed
> at. If you are still on `v0.1.0-beta.1` itself, a `go install` build says `batten 0.1.0` and
> doctor calls out the mismatch — use (a) or (c), or ignore that one warning.
>
> *(What was actually broken: `-X main.version` is silently ignored by the Go linker unless the
> variable is declared uninitialized or with a constant. It was initialized with a function call,
> so the flag GoReleaser has always passed did nothing — no error, no warning.)*

## Installing (5 steps)

```
# 1. register the marketplace (once per machine)
#    - from a published release:
/plugin marketplace add ArthurZizumbo/batten
#    - or from a local checkout (dev): build the binary first, then:
scripts/build-plugin.ps1            # Windows
scripts/build-plugin.sh             # macOS/Linux
/plugin marketplace add <path-to-repo>

# 2. install
/plugin install batten@batten

# 3. generate the spec by interviewing the repo
/batten-init                        # or in a terminal: batten init

# 4. (optional) enable the subscription-quota ceiling
batten statusline --install

# 5. verify
batten doctor                       # green -> ready
```

## Adopting in a project that is ALREADY under way

This is the normal case, and batten is built not to get in the way:

- **`batten init` starts at `enforcement: report`.** Gates WARN, they do not block. You can adopt
  batten mid-sprint without anyone hitting a `deny` on day one.
- When the team trusts the gates, change one line of `batten.yaml`: `enforcement: enforce` (or
  delete the line; enforce is the default).
- **Branches already open**: if your branch names the unit (`feature/US-034-...`), batten binds it
  on its own. If not, run `batten phase <unit> <phase>` once to bind this session to its unit.
- **Migrating a prose workflow**: if your process is already written down (a `prompts.md`, a
  `CONTRIBUTING.md`), bring it: `/batten-init --from docs/your-flow.md`. The agent reconciles your
  prose against the draft the scan produced.

## Two or more sessions in parallel

batten does not break with two Claude Codes working the same repo:

- Each session is bound to **its** unit (per session, not per shared branch).
- If session B tries to write a file an agent of session A claimed, batten stops it and names the
  conflicting unit.
- If a session is bound to no unit (ambiguous), `SessionStart` says so — and warns that the gates
  cannot act until you bind it.
- **Recommended for heavy parallel work**: one git worktree per unit. Each worktree has its own
  branch, so branch attribution becomes automatic again, and the ledger and the cross-run guard are
  shared because batten's database is machine-global.

## Where the state lives

**`~/.batten/batten.db`**, always. Override with `BATTEN_DB`.

That path is deliberate and worth explaining, because it cost two bugs in the dogfood. The state
does NOT live in `${CLAUDE_PLUGIN_DATA}`: hook processes have that variable and your terminal does
not, so a path that depends on the environment splits the state into two databases — the TUI says
"no runs" while the hooks happily write runs somewhere else. And `${CLAUDE_PLUGIN_ROOT}` is
forbidden in any case: it is wiped on every plugin update.

The binary lives in `${CLAUDE_PLUGIN_ROOT}/bin` because that is the only place the hooks invoke it,
and its copy in `${CLAUDE_PLUGIN_DATA}/bin` is cache, not state: losing it costs one download.

## Uninstalling

`/plugin uninstall batten@batten`. The database survives: it is in `~/.batten`, outside the plugin.
Delete it by hand if you really want to start over.
