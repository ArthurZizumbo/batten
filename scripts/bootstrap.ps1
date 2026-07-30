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
#
# This branch used to print "(plugin update)", asserting a cause it had not checked — and on THIS
# platform the other cause is the likely one. Windows Defender classifies freshly built, unsigned
# Go binaries as `Trojan:Win32/*!ml` often enough that this project hit it on its own machine: an
# ML verdict rather than a signature, which is why byte-identical builds get different answers and
# an explicit rescan comes back clean.
#
# batten cannot stop that; being quiet about it is the part it must not do. A quarantined binary
# leaves exactly the state batten exists to eliminate — the seven hooks are exec-form on a file
# that no longer exists and die in silence, the MCP server does not start, and `batten doctor`
# cannot report it because doctor IS the missing binary.
#
# The tell is the REPEAT: an update explains this once, twice inside a day does not. See the same
# block in bootstrap.sh; the two scripts are one contract in two files.
$stamp = Join-Path $cacheDir '.last-restore'
if ((Test-Path -LiteralPath $cache -PathType Leaf) -and (InstallFrom $cache)) {
    $prev = 0
    if (Test-Path -LiteralPath $stamp -PathType Leaf) {
        $raw = (Get-Content -LiteralPath $stamp -TotalCount 1 -ErrorAction SilentlyContinue)
        [long]::TryParse($raw, [ref]$prev) | Out-Null
    }
    $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    try {
        New-Item -ItemType Directory -Force -Path $cacheDir -ErrorAction Stop | Out-Null
        Set-Content -LiteralPath $stamp -Value $now -ErrorAction Stop
    } catch { }

    if ($prev -gt 0 -and ($now - $prev) -lt 86400) {
        Note "batten: the binary was MISSING AGAIN and had to be restored from the cache."
        Note "batten: a plugin update explains that once; twice in a day means something is"
        Note "batten: deleting $target after a good install."
        Note "batten: on Windows that is nearly always an antivirus quarantine - Defender reports"
        Note "batten: unsigned Go binaries as Trojan:*!ml. Check its quarantine for batten."
        Note "batten: until it stops, the hooks have no binary to run and NOTHING IS BEING GATED"
        Note "batten: between the moment it is removed and the next session that restores it."
    } else {
        Note "batten: restored from $cacheDir - the plugin's bin/ was empty (usually a plugin update)"
    }
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
$asset = "batten_windows_$arch.tar.gz"
$url = "$base/$asset"
$sumsUrl = "$base/checksums.txt"

# VerifyArchive is THE ONE PART OF THIS SCRIPT THAT FAILS CLOSED, and the mirror of
# verify_archive in bootstrap.sh. Do not "fix" it into a warning.
#
# Everything else here is best-effort because a bootstrap must not break a session. This is not:
# the file being checked is about to be executed by seven hooks and an MCP server, so installing
# it unverified is remote code execution by download. A mismatched hash, an unreachable
# checksums.txt and a checksums.txt that does not list this asset all get the SAME answer —
# refuse — because they are the same statement: nobody can vouch for these bytes. The accepted
# consequence is written down in plan_publicacion.md §3.2.
#
# It throws, which the caller's catch turns into $ok = $false and an exit code of 0. hooks.json
# dispatches `bash bootstrap.sh || powershell bootstrap.ps1`, so the exit code carries "there is
# no bash", never "the download was bad".
#
# Not a `-c`-style whole-file check: checksums.txt lists all six assets and five of them are not
# on this disk. Only this asset's line is ever compared. Get-FileHash is in PowerShell 5.1.
function VerifyArchive($path, $sumsPath) {
    $got = (Get-FileHash -LiteralPath $path -Algorithm SHA256 -ErrorAction Stop).Hash.ToLower()
    $want = ''
    foreach ($line in (Get-Content -LiteralPath $sumsPath -ErrorAction Stop)) {
        $parts = @($line -split '\s+' | Where-Object { $_ -ne '' })
        if ($parts.Count -ge 2) {
            $name = $parts[1] -replace '^\*', ''    # the BSD spelling writes `*name`
            if ($name -eq $asset) { $want = $parts[0].ToLower(); break }
        }
    }
    if (-not $want) {
        throw "$sumsUrl does not list $asset - refusing to install a binary nothing can verify."
    }
    if ($want -ne $got) {
        throw "CHECKSUM MISMATCH: expected $want, got $got - refusing to install it. Nothing was changed."
    }
}

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("batten-boot-" + [System.Guid]::NewGuid().ToString('N'))
$ok = $false
$why = ''
try {
    New-Item -ItemType Directory -Force -Path $tmp -ErrorAction Stop | Out-Null
    $tgz = Join-Path $tmp 'b.tgz'
    Invoke-WebRequest -Uri $url -OutFile $tgz -UseBasicParsing -ErrorAction Stop

    # Between the download and the unpack, and it may not move after them: extracting an
    # unverified archive already runs attacker-chosen bytes through tar, and the cache seeding
    # below is what would make one bad download outlive itself. A 404 on checksums.txt throws
    # out of Invoke-WebRequest and lands in the same catch as a mismatch — deliberately the
    # same answer.
    #
    # The fetch gets its own try so the refusal names checksums.txt. Left to Invoke-WebRequest,
    # the message is a bare, LOCALISED "(404) Not Found" — it does not say which of the two URLs
    # failed, and on a Spanish Windows it does not even say it in English. The person reading
    # stderr has to be able to tell "this mirror has no sums" from "this asset was replaced".
    $sums = Join-Path $tmp 'checksums.txt'
    try {
        Invoke-WebRequest -Uri $sumsUrl -OutFile $sums -UseBasicParsing -ErrorAction Stop
    } catch {
        throw "could not fetch $sumsUrl - refusing to install a binary nothing can verify."
    }
    VerifyArchive $tgz $sums

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
        # write is not a failure: the install already succeeded. Only VERIFIED bytes reach this
        # line — the cache restores without network and therefore without a second chance.
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
