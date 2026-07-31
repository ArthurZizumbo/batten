# Build the batten binary into the plugin's bin/ so a local marketplace install works.
# CGO_ENABLED=0 -> a single static binary, which is the whole reason we picked modernc.org/sqlite.
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    $env:CGO_ENABLED = "0"
    $out = Join-Path $root "plugin/claude-code/bin/batten.exe"
    # The VERSIONINFO resource. The dev binary carries the same metadata the released one does —
    # otherwise the build that gets tested locally is not the build that ships, and the false
    # positive would be chased against the wrong artifact.
    # --abbrev=0, not --always: --always falls back to a bare SHA when no tag exists, and a SHA is
    # not a version. gen-winres rejects it, which would break the build on a fresh clone.
    $desc = (git describe --tags --abbrev=0 2>$null)
    if (-not $desc) { $desc = "0.0.0" }
    # Without the leading v, because that is what GoReleaser's {{ .Version }} expands to and what
    # plugin.json carries ("0.1.0-beta.1"). `batten doctor` compares the two strings, so a local
    # build that said "v0.1.0-beta.1" would differ from the released one for no reason.
    $desc = $desc -replace '^v', ''
    & (Join-Path $PSScriptRoot "gen-winres.ps1") -Version $desc
    Write-Host "building $out ..."
    # -X main.version, same as GoReleaser injects. Without it a locally built binary reports Go's
    # pseudo-version (0.1.0-beta.1.0.20260731002105-cdcb379+dirty) and `batten doctor` reports the
    # plugin and the binary disagreeing — which makes "build it yourself", the answer we give
    # people hit by the antivirus false positive, look like it broke something.
    go build -trimpath -ldflags "-s -w -X main.version=$desc" -o $out ./cmd/batten
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    Write-Host "ok: $((Get-Item $out).Length) bytes"
    # hooks.json declares ${CLAUDE_PLUGIN_ROOT}/scripts/bootstrap.{sh,ps1} — they must SHIP inside
    # the package, or every session greets the user with "No such file or directory" (E0 finding).
    $scripts = Join-Path $root "plugin/claude-code/scripts"
    New-Item -ItemType Directory -Force $scripts | Out-Null
    foreach ($s in @("bootstrap.sh", "bootstrap.ps1", "bootstrap.cmd")) {
        Copy-Item (Join-Path $root "scripts/$s") $scripts -Force
    }
    Write-Host ""
    Write-Host "Next: in Claude Code run"
    Write-Host "  /plugin marketplace add $root"
    Write-Host "  /plugin install batten@batten"
} finally { Pop-Location }
