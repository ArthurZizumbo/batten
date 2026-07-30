#!/usr/bin/env bash
# batten bootstrap — puts the binary where every hook already looks for it.
#
# THE destination is ${CLAUDE_PLUGIN_ROOT}/bin/batten[.exe]. Not a preference: hooks.json names
# that file seven times, .mcp.json names it once, Claude Code puts that directory on PATH (which
# is what makes the bare `batten` in the /batten-* commands resolve), and `batten doctor` inspects
# exactly that file to answer "is the gate running?". A binary anywhere else is a binary nothing
# invokes.
#
# ${CLAUDE_PLUGIN_DATA}/bin is a CACHE, and only that. It exists because a plugin update replaces
# ${CLAUDE_PLUGIN_ROOT} wholesale — bin/ ships empty on purpose, so an update wipes the binary —
# and re-downloading 14 MB on every update is rude. So: install to ROOT, keep a copy in DATA, and
# after an update restore from the copy instead of hitting the network.
#
# Two distribution paths, and this handles the gap between them:
#   - dev: `scripts/build-plugin.sh` compiles straight into the plugin's bin/. This script sees
#     the file and exits without a word.
#   - release: the plugin ships WITHOUT a committed binary (they bloat the repo and go stale), so
#     first run fetches the right static binary from the GitHub Release.
#
# Runs from the SessionStart hook. Best-effort: if the download fails it prints a hint and exits 0
# — a bootstrap must never break a session, and hooks.json's fallback to bootstrap.ps1 depends on
# this script's exit code meaning "bash could not be found", not "the download failed".
set -u

REPO="ArthurZizumbo/batten"
ROOT="${CLAUDE_PLUGIN_ROOT:-.}"
DATA="${CLAUDE_PLUGIN_DATA:-$HOME/.batten}"
ext=""; case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) ext=".exe" ;; esac

target="$ROOT/bin/batten${ext}"   # the only path that counts
cache="$DATA/bin/batten${ext}"    # survives plugin updates; nothing invokes it

# 1. Already where the hooks look? Done. This is the every-session path, so it stays cheap: one
#    stat, no process spawn.
#
#    Note what is NOT tested here: whether `batten` is on PATH. It was, and that was the bug —
#    a dev build, a `go install`ed copy, or a stale entry all satisfy `command -v batten` while
#    the file the hooks name does not exist. The check has to name the file.
if [ -x "$target" ]; then
  exit 0
fi

mkdir -p "$ROOT/bin" 2>/dev/null

# install <src> — put a binary in place and prove it runs before claiming anything. A bootstrap
# that prints "installed" over a half-written file teaches the user to trust a gate that is not
# there, which is the exact failure this tool exists to eliminate.
install_from() {
  cp "$1" "$target" 2>/dev/null || return 1
  chmod +x "$target" 2>/dev/null
  if "$target" version >/dev/null 2>&1; then
    return 0
  fi
  rm -f "$target" 2>/dev/null
  return 1
}

# 2. A previous install left a copy in the cache — this is the post-update path. Restore it
#    rather than downloading again.
#
# This branch used to print "(plugin update)", asserting a cause it had not checked. A plugin
# update is the USUAL reason ${CLAUDE_PLUGIN_ROOT}/bin is empty; it is not the only one, and the
# other one is much worse:
#
#   an antivirus quarantines the binary AFTER a good install.
#
# Windows Defender classifies freshly built, unsigned Go binaries as `Trojan:Win32/*!ml` often
# enough that this project hit it on its own machine — an ML verdict, not a signature, which is
# why byte-identical builds get different answers and an explicit rescan comes back clean.
# batten cannot stop that. What it must not do is be quiet about it, because the state it leaves
# behind is precisely the one batten exists to eliminate: the seven hooks are exec-form on a file
# that no longer exists, so they die in silence; the MCP server does not start; and `batten
# doctor` cannot report it because doctor IS the missing binary. The gate is down and every
# surface that could say so is the thing that was removed.
#
# The tell is the REPEAT. A plugin update explains this once. Twice inside a day means something
# removed the binary after a good install — and restoring from the cache just hands the same
# bytes back for the same treatment, printing a reassuring line every session while nothing is
# ever gated. So the stamp is read before restoring and written after.
stamp="$DATA/bin/.last-restore"
if [ -x "$cache" ] && install_from "$cache"; then
  prev=0
  if [ -f "$stamp" ]; then
    prev="$(cat "$stamp" 2>/dev/null)"
    case "$prev" in '' | *[!0-9]*) prev=0 ;; esac
  fi
  now="$(date +%s 2>/dev/null)"
  case "$now" in '' | *[!0-9]*) now=0 ;; esac
  mkdir -p "$DATA/bin" 2>/dev/null && printf '%s\n' "$now" >"$stamp" 2>/dev/null

  if [ "$now" -gt 0 ] && [ "$prev" -gt 0 ] && [ "$((now - prev))" -lt 86400 ]; then
    echo "batten: the binary was MISSING AGAIN and had to be restored from the cache." >&2
    echo "batten: a plugin update explains that once; twice in a day means something is" >&2
    echo "batten: deleting $target after a good install." >&2
    echo "batten: on Windows that is nearly always an antivirus quarantine — Defender reports" >&2
    echo "batten: unsigned Go binaries as Trojan:*!ml. Check its quarantine for batten." >&2
    echo "batten: until it stops, the hooks have no binary to run and NOTHING IS BEING GATED" >&2
    echo "batten: between the moment it is removed and the next session that restores it." >&2
  else
    echo "batten: restored from $DATA/bin — the plugin's bin/ was empty (usually a plugin update)" >&2
  fi
  exit 0
fi

# 3. Fetch. Work out the asset name for this platform first.
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in mingw*|msys*|cygwin*) os="windows" ;; esac
arch="$(uname -m)"; case "$arch" in x86_64) arch="amd64" ;; aarch64|arm64) arch="arm64" ;; esac

echo "batten: fetching the binary for ${os}/${arch} (first run)..." >&2

# This name is a contract with .goreleaser.yaml's archives.name_template: tar.gz on every
# platform, no version in the name, because releases/latest/download only resolves a name we can
# predict without knowing the tag. Change one, change the other in the same commit.
#
# The base is overridable for ONE reason: so the test suite can point this script at a local
# server and exercise this exact code path against a real archive. Nothing in an install sets it.
base="${BATTEN_BOOTSTRAP_BASE_URL:-https://github.com/${REPO}/releases/latest/download}"
asset="batten_${os}_${arch}.tar.gz"
url="${base}/${asset}"
sums_url="${base}/checksums.txt"
tmp="$(mktemp -d)"

# sha256_of <file> — the digest, or nothing if this machine cannot compute one.
#
# Three tools because no one of them is everywhere: macOS ships `shasum` and NOT `sha256sum`,
# most Linux ships `sha256sum`, Git Bash ships both, and openssl is the last resort. "Nothing"
# is a real answer and the caller treats it as a failure — see verify_archive.
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" 2>/dev/null | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" 2>/dev/null | cut -d' ' -f1
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$1" 2>/dev/null | sed 's/.*= *//'
  fi
}

# verify_archive <file> — THE ONE PART OF THIS SCRIPT THAT FAILS CLOSED. Do not "fix" it.
#
# Everything else here is best-effort because a bootstrap must not break a session. This is not:
# the file being checked is about to be executed by seven hooks and an MCP server, so installing
# it unverified is remote code execution by download. A mismatched hash, an unreachable
# checksums.txt, a checksums.txt that does not list this asset, and a machine with no sha256 tool
# all get the SAME answer — refuse — because they are the same statement: nobody can vouch for
# these bytes. The failure mode is deliberate and accepted: the
# machine is left WITHOUT a gate, which is already what every failed download leaves behind, and
# the cache path above means it only ever applies to a first install.
#
# Refusing still exits 0. hooks.json dispatches `bash bootstrap.sh || powershell bootstrap.ps1`,
# so a non-zero exit here means "there is no bash", not "the download was bad" — and would fire
# the Windows fallback on macOS.
#
# Not `sha256sum -c`: checksums.txt lists all six assets and five of them are not on this disk,
# so a bare -c fails every single time. Only this asset's line is ever compared.
verify_archive() {
  got="$(sha256_of "$1")"
  if [ -z "$got" ]; then
    echo "batten: no sha256 tool here (sha256sum, shasum or openssl)" >&2
    echo "batten: refusing to install a binary nothing can verify." >&2
    return 1
  fi
  if ! curl -fsSL "$sums_url" -o "$tmp/checksums.txt" 2>/dev/null; then
    echo "batten: could not fetch $sums_url" >&2
    echo "batten: refusing to install a binary nothing can verify." >&2
    return 1
  fi
  # `$2 == "*"f` covers the BSD spelling; GoReleaser writes `<hash>  <name>`.
  want="$(awk -v f="$asset" '$2 == f || $2 == "*"f {print $1; exit}' "$tmp/checksums.txt" 2>/dev/null)"
  if [ -z "$want" ]; then
    echo "batten: $sums_url does not list $asset" >&2
    echo "batten: refusing to install a binary nothing can verify." >&2
    return 1
  fi
  if [ "$want" != "$got" ]; then
    echo "batten: CHECKSUM MISMATCH for $url" >&2
    echo "batten:   expected $want" >&2
    echo "batten:   got      $got" >&2
    echo "batten: refusing to install it. Nothing was changed." >&2
    return 1
  fi
  return 0
}

# `cd` instead of `tar -C "$tmp"`, and no absolute path in the tar arguments. Whether `tar` here is
# GNU tar or bsdtar depends on the machine, and the two disagree about paths that carry a drive
# letter: GNU tar reads `C:\Users\...` as a remote host spec ("Cannot connect to C: resolve
# failed") and unpacks nothing. A relative name in the process's own working directory is the one
# form both read the same way. bootstrap.ps1 hit exactly this and has the mirror-image fix.
#
# verify_archive sits between the download and the unpack, and nothing may move it after them:
# `tar -xzf` on an unverified archive is already running attacker-chosen bytes through tar, and
# the cache seeding below is what would make one bad download outlive itself.
ok=0
if curl -fsSL "$url" -o "$tmp/b.tgz" 2>/dev/null &&
   verify_archive "$tmp/b.tgz" &&
   ( cd "$tmp" && tar -xzf b.tgz ) 2>/dev/null &&
   [ -f "$tmp/batten${ext}" ] &&
   install_from "$tmp/batten${ext}"; then
  ok=1
  # Seed the cache so the next plugin update is a copy, not a download. A cache we cannot write
  # is not a failure: the install already succeeded. Only VERIFIED bytes reach this line — the
  # cache restores without network and therefore without a second chance to check.
  mkdir -p "$DATA/bin" 2>/dev/null && cp "$target" "$cache" 2>/dev/null
fi

if [ "$ok" = 1 ]; then
  echo "batten: installed to $ROOT/bin" >&2
else
  echo "batten: could not install the binary from $url" >&2
  echo "batten: build it once instead: $ROOT/scripts/build-plugin.sh" >&2
  echo "batten: until then the hooks no-op — nothing is being gated." >&2
fi
rm -rf "$tmp"
exit 0
