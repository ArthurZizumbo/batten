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
echo "ok: $(wc -c < "$out") bytes"
echo
echo "Next: in Claude Code run"
echo "  /plugin marketplace add $root"
echo "  /plugin install batten@batten"
