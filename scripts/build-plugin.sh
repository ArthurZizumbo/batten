#!/usr/bin/env bash
# Build the batten binary into the plugin's bin/ so a local marketplace install works.
# CGO_ENABLED=0 -> a single static binary (the reason we chose modernc.org/sqlite).
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
ext=""
case "$(go env GOOS)" in windows) ext=".exe" ;; esac
out="plugin/claude-code/bin/batten${ext}"
echo "building $out ..."
CGO_ENABLED=0 go build -ldflags "-s -w" -o "$out" ./cmd/batten
# hooks.json declares ${CLAUDE_PLUGIN_ROOT}/scripts/bootstrap.sh — it must SHIP inside the
# package, or every session greets the user with "No such file or directory" (E0 finding).
mkdir -p plugin/claude-code/scripts
cp scripts/bootstrap.sh plugin/claude-code/scripts/bootstrap.sh
# The bit, explicitly. `cp` onto an existing file keeps the DESTINATION's mode, so a copy that
# once landed without +x stays without it — and a bootstrap that cannot execute is the silent
# failure this whole script exists to avoid.
chmod +x plugin/claude-code/scripts/bootstrap.sh
echo "ok: $(wc -c < "$out") bytes"
echo
echo "Next: in Claude Code run"
echo "  /plugin marketplace add $root"
echo "  /plugin install batten@batten"
