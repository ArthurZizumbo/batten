# Generate the Windows VERSIONINFO resources that get linked into batten.exe.
#
# The PowerShell half of the pair. gen-winres.sh carries the argument for why this exists at all;
# this file exists because build-plugin.ps1 is the path for a Windows developer who has no bash,
# and the same convention already governs bootstrap.{sh,ps1,cmd}.
#
# Keep the two halves behaviourally identical. They are one contract written in two files.
param([string]$Version = "0.0.0")

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    # Accepts v0.1.0, v0.1.0-beta.1, 0.1.0. goversioninfo rejects a leading "v", and VERSIONINFO
    # has no field for a pre-release label: the four numbers Windows shows must BE numbers.
    $verStr = $Version -replace '^v', ''
    $ver = ($verStr -split '-')[0]
    $parts = $ver -split '\.'

    $nums = @(0, 0, 0)
    for ($i = 0; $i -lt 3; $i++) {
        if ($i -lt $parts.Count -and $parts[$i] -ne "") {
            if ($parts[$i] -notmatch '^\d+$') {
                throw "gen-winres: '$Version' does not parse as a version (component '$($parts[$i])' is not a number)"
            }
            $nums[$i] = [int]$parts[$i]
        }
    }

    go tool goversioninfo `
        -platform-specific `
        -ver-major $nums[0] -ver-minor $nums[1] -ver-patch $nums[2] -ver-build 0 `
        -product-ver-major $nums[0] -product-ver-minor $nums[1] -product-ver-patch $nums[2] -product-ver-build 0 `
        -file-version $verStr -product-version $verStr `
        cmd/batten/versioninfo.json
    if ($LASTEXITCODE -ne 0) { throw "goversioninfo failed" }

    # -platform-specific ignores -o and writes to the working directory. It emits four
    # architectures; .goreleaser.yaml builds two, and an unused .syso is dead weight in the tree.
    foreach ($f in Get-ChildItem -Path . -Filter "resource_windows_*.syso" -File) {
        if ($f.Name -match '_(amd64|arm64)\.syso$') {
            Move-Item $f.FullName (Join-Path "cmd/batten" $f.Name) -Force
        }
        else { Remove-Item $f.FullName -Force }
    }

    Write-Host "gen-winres: $Version -> $($nums[0]).$($nums[1]).$($nums[2]).0 ($verStr)"
}
finally { Pop-Location }
