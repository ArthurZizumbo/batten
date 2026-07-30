# batten bootstrap for Windows without Git Bash.
#
# bootstrap.sh is the same logic in the same order, and the two must stay in step. This file
# exists because Windows is the declared primary target and, until now, the only way to install
# the binary there was through a bash that a Windows machine is not required to have. hooks.json
# tries bash first and falls back to this; `scripts\bootstrap.cmd` is the by-hand entry point.
#
# Written for PowerShell 5.1 — the one that ships in the box. No ternaries, no `??`, no 3-argument
# Join-Path: a bootstrap that needs PowerShell 7 installed first is not a bootstrap.
#
# Everything is best-effort and the exit code is always 0: a bootstrap must never break a session.
$ErrorActionPreference = 'Continue'
$ProgressPreference = 'SilentlyContinue'   # Invoke-WebRequest's progress bar costs more than the download

# stderr, never stdout. A SessionStart hook's stdout is fed to the model as context, so a chatty
# bootstrap would put "batten: installed to ..." inside the conversation.
function Note($msg) { [Console]::Error.WriteLine($msg) }

$repo = 'ArthurZizumbo/batten'

$root = $env:CLAUDE_PLUGIN_ROOT
if (-not $root) { $root = '.' }
$data = $env:CLAUDE_PLUGIN_DATA
if (-not $data) { $data = Join-Path $HOME '.batten' }

$binDir = Join-Path $root 'bin'
$target = Join-Path $binDir 'batten.exe'          # the only path hooks.json and .mcp.json name
$cacheDir = Join-Path $data 'bin'
$cache = Join-Path $cacheDir 'batten.exe'         # survives a plugin update; nothing invokes it

# 1. Already where the hooks look? Done. Every session runs this, so it stays one stat.
if (Test-Path -LiteralPath $target -PathType Leaf) { exit 0 }

# InstallFrom copies a binary into place and proves it runs before anything claims success. A
# bootstrap that prints "installed" over a half-written file teaches the user to trust a gate
# that is not there.
function InstallFrom($src) {
    try {
        New-Item -ItemType Directory -Force -Path $binDir -ErrorAction Stop | Out-Null
        Copy-Item -LiteralPath $src -Destination $target -Force -ErrorAction Stop
    } catch {
        return $false
    }
    & $target version *> $null
    if ($LASTEXITCODE -eq 0) { return $true }
    Remove-Item -LiteralPath $target -Force -ErrorAction SilentlyContinue
    return $false
}

# 2. The post-update path: bin/ ships empty, so an update wiped the binary and the cache is the
#    only copy left. Restore it instead of spending 14 MB of network again.
if ((Test-Path -LiteralPath $cache -PathType Leaf) -and (InstallFrom $cache)) {
    Note "batten: restored from $cacheDir (plugin update)"
    exit 0
}

# 3. Fetch. The asset name is a contract with .goreleaser.yaml — tar.gz on every platform, no
#    version in the name, because releases/latest/download only resolves a name we can predict
#    without knowing the tag. Change one, change the other in the same commit.
$arch = 'amd64'
if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64' -or $env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') { $arch = 'arm64' }

Note "batten: fetching the binary for windows/$arch (first run)..."

# Overridable for ONE reason: so the test suite can point this script at a local server and
# exercise this exact code path against a real archive. Nothing in an install sets it.
$base = $env:BATTEN_BOOTSTRAP_BASE_URL
if (-not $base) { $base = "https://github.com/$repo/releases/latest/download" }
$url = "$base/batten_windows_$arch.tar.gz"

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("batten-boot-" + [System.Guid]::NewGuid().ToString('N'))
$ok = $false
$why = ''
try {
    New-Item -ItemType Directory -Force -Path $tmp -ErrorAction Stop | Out-Null
    $tgz = Join-Path $tmp 'b.tgz'
    Invoke-WebRequest -Uri $url -OutFile $tgz -UseBasicParsing -ErrorAction Stop

    # tar.exe by FULL PATH, and that is not fussiness. `tar` on PATH is very often MSYS2 or Git
    # Bash's GNU tar, which reads `C:\Users\...` as a remote host spec — it prints "Cannot connect
    # to C: resolve failed" and unpacks nothing. Windows has shipped bsdtar at System32\tar.exe
    # since 10 1803, and bsdtar takes drive letters. (Found by running this script. Reading it is
    # what missed A1 in the first place.)
    $tarExe = Join-Path $env:SystemRoot 'System32\tar.exe'
    if (-not (Test-Path -LiteralPath $tarExe -PathType Leaf)) { $tarExe = 'tar.exe' }
    & $tarExe -xzf $tgz -C $tmp *> $null
    $unpacked = Join-Path $tmp 'batten.exe'
    if (Test-Path -LiteralPath $unpacked -PathType Leaf) {
        $ok = InstallFrom $unpacked
    }
    if ($ok) {
        # Seed the cache so the next plugin update is a copy, not a download. A cache we cannot
        # write is not a failure: the install already succeeded.
        try {
            New-Item -ItemType Directory -Force -Path $cacheDir -ErrorAction Stop | Out-Null
            Copy-Item -LiteralPath $target -Destination $cache -Force -ErrorAction Stop
        } catch { }
    }
} catch {
    $ok = $false
    $why = $_.Exception.Message
} finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

if ($ok) {
    Note "batten: installed to $binDir"
} else {
    Note "batten: could not install the binary from $url"
    if ($why) { Note "batten: $why" }
    Note "batten: build it once instead: $root\scripts\build-plugin.ps1"
    Note "batten: until then the hooks no-op - nothing is being gated."
}
exit 0
