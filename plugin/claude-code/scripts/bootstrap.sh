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
if [ -x "$cache" ] && install_from "$cache"; then
  echo "batten: restored from $DATA/bin (plugin update)" >&2
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
url="${base}/batten_${os}_${arch}.tar.gz"
tmp="$(mktemp -d)"

ok=0
if curl -fsSL "$url" -o "$tmp/b.tgz" 2>/dev/null &&
   tar -xzf "$tmp/b.tgz" -C "$tmp" 2>/dev/null &&
   [ -f "$tmp/batten${ext}" ] &&
   install_from "$tmp/batten${ext}"; then
  ok=1
  # Seed the cache so the next plugin update is a copy, not a download. A cache we cannot write
  # is not a failure: the install already succeeded.
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
