#!/usr/bin/env bash
# batten bootstrap — ensures the binary exists before the hooks need it.
#
# Two distribution paths, and this handles the gap between them:
#   - dev: `scripts/build-plugin.sh` puts the binary in the plugin's bin/, which Claude Code
#     adds to PATH. Nothing to download; this script sees it and exits.
#   - release: the plugin ships WITHOUT a committed binary (they bloat the repo and go stale),
#     so on first run this fetches the right static binary from the GitHub Release into
#     ${CLAUDE_PLUGIN_DATA}/bin — which survives plugin updates, unlike ${CLAUDE_PLUGIN_ROOT}.
#
# Runs from the SessionStart hook (shell form, so it works under Git Bash on Windows too).
# Best-effort: if the download fails, it prints a hint and exits 0 — a bootstrap must never
# break a session. The batten hooks already no-op silently when the binary is absent.
set -u

REPO="ArthurZizumbo/batten"
ROOT="${CLAUDE_PLUGIN_ROOT:-.}"
DATA="${CLAUDE_PLUGIN_DATA:-$HOME/.batten}"
ext=""; case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) ext=".exe" ;; esac

# 1. already on PATH (dev build in plugin bin/, or a prior bootstrap)?
if command -v "batten${ext}" >/dev/null 2>&1 || [ -x "$ROOT/bin/batten${ext}" ] || [ -x "$DATA/bin/batten${ext}" ]; then
  exit 0
fi

# 2. figure out the asset name for this platform.
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in mingw*|msys*|cygwin*) os="windows" ;; esac
arch="$(uname -m)"; case "$arch" in x86_64) arch="amd64" ;; aarch64|arm64) arch="arm64" ;; esac

echo "batten: fetching the binary for ${os}/${arch} (first run)..." >&2
mkdir -p "$DATA/bin"

# This name is a contract with .goreleaser.yaml's archives.name_template: tar.gz on every
# platform, no version in the name, because releases/latest/download only resolves a name we can
# predict without knowing the tag. Change one, change the other in the same commit.
url="https://github.com/${REPO}/releases/latest/download/batten_${os}_${arch}.tar.gz"
tmp="$(mktemp -d)"

# Every step is checked, and success is only claimed once the binary is actually in place and
# runnable. A bootstrap that prints "installed" over a failed move teaches the user to trust a
# gate that is not there.
ok=0
if curl -fsSL "$url" -o "$tmp/b.tgz" 2>/dev/null &&
   tar -xzf "$tmp/b.tgz" -C "$tmp" 2>/dev/null &&
   [ -f "$tmp/batten${ext}" ] &&
   mv "$tmp/batten${ext}" "$DATA/bin/batten${ext}" 2>/dev/null; then
  chmod +x "$DATA/bin/batten${ext}" 2>/dev/null
  if "$DATA/bin/batten${ext}" version >/dev/null 2>&1; then
    ok=1
  fi
fi

if [ "$ok" = 1 ]; then
  echo "batten: installed to $DATA/bin" >&2
else
  rm -f "$DATA/bin/batten${ext}" 2>/dev/null   # never leave a half-installed binary behind
  echo "batten: could not install the binary from $url" >&2
  echo "batten: build it once instead: $ROOT/scripts/build-plugin.sh" >&2
  echo "batten: until then the hooks no-op — nothing is being gated." >&2
fi
rm -rf "$tmp"
exit 0
