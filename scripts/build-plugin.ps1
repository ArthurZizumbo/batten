# Build the batten binary into the plugin's bin/ so a local marketplace install works.
# CGO_ENABLED=0 -> a single static binary, which is the whole reason we picked modernc.org/sqlite.
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    $env:CGO_ENABLED = "0"
    $out = Join-Path $root "plugin/claude-code/bin/batten.exe"
    Write-Host "building $out ..."
    go build -ldflags "-s -w" -o $out ./cmd/batten
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    Write-Host "ok: $((Get-Item $out).Length) bytes"
    # hooks.json declares ${CLAUDE_PLUGIN_ROOT}/scripts/bootstrap.sh — it must SHIP inside the
    # package, or every session greets the user with "No such file or directory" (E0 finding).
    $scripts = Join-Path $root "plugin/claude-code/scripts"
    New-Item -ItemType Directory -Force $scripts | Out-Null
    Copy-Item (Join-Path $root "scripts/bootstrap.sh") $scripts -Force
    Write-Host ""
    Write-Host "Next: in Claude Code run"
    Write-Host "  /plugin marketplace add $root"
    Write-Host "  /plugin install batten@batten"
} finally { Pop-Location }
