#!/usr/bin/env bash
# Build the batten binary into the plugin's bin/ so a local marketplace install works.
# CGO_ENABLED=0 -> a single static binary (the reason we chose modernc.org/sqlite).
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
ext=""
case "$(go env GOOS)" in windows) ext=".exe" ;; esac
out="plugin/claude-code/bin/batten${ext}"
# --abbrev=0, not --always: --always falls back to a bare SHA when no tag exists, and a SHA is not
# a version. gen-winres rejects it, which would break the build on a fresh clone.
ver="$(git describe --tags --abbrev=0 2>/dev/null || echo 0.0.0)"
# Without the leading v, because that is what GoReleaser's {{ .Version }} expands to and what
# plugin.json carries ("0.1.0-beta.1"). `batten doctor` compares the two strings, so a local build
# that said "v0.1.0-beta.1" would differ from the released one for no reason.
ver="${ver#v}"
# The VERSIONINFO resource, on Windows only. The dev binary carries the same metadata the released
# one does — otherwise the build that gets tested locally is not the build that ships, and the
# false positive would be chased against the wrong artifact.
if [ "$ext" = ".exe" ]; then
  bash "$root/scripts/gen-winres.sh" "$ver"
fi
echo "building $out ..."
# -X main.version, same as GoReleaser injects. Without it a locally built binary reports Go's
# pseudo-version and `batten doctor` reports the plugin and the binary disagreeing — which makes
# "build it yourself", the answer we give people hit by the antivirus false positive, look like it
# broke something.
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$ver" -o "$out" ./cmd/batten
# hooks.json declares ${CLAUDE_PLUGIN_ROOT}/scripts/bootstrap.{sh,ps1} — they must SHIP inside
# the package, or every session greets the user with "No such file or directory" (E0 finding).
mkdir -p plugin/claude-code/scripts
for s in bootstrap.sh bootstrap.ps1 bootstrap.cmd; do
  cp "scripts/$s" "plugin/claude-code/scripts/$s"
done
# The bit, explicitly. `cp` onto an existing file keeps the DESTINATION's mode, so a copy that
# once landed without +x stays without it — and a bootstrap that cannot execute is the silent
# failure this whole script exists to avoid.
chmod +x plugin/claude-code/scripts/bootstrap.sh
echo "ok: $(wc -c < "$out") bytes"
echo
echo "Next: in Claude Code run"
echo "  /plugin marketplace add $root"
echo "  /plugin install batten@batten"
