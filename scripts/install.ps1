[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [string]$BinaryPath
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$projectDir = Split-Path -Parent $PSScriptRoot
$installDir = Join-Path ([Environment]::GetFolderPath("LocalApplicationData")) "Programs\Charta"
$targetPath = Join-Path $installDir "charta.exe"

if ($BinaryPath) {
    if (-not (Test-Path -LiteralPath $BinaryPath -PathType Leaf)) {
        throw "Binary not found: $BinaryPath"
    }
    $sourcePath = (Resolve-Path -LiteralPath $BinaryPath).Path
}
else {
    $candidates = @(
        (Join-Path $projectDir "bin\charta-windows-amd64.exe"),
        (Join-Path $projectDir "bin\tui-db-windows-amd64.exe"),
        (Join-Path $PSScriptRoot "charta-windows-amd64.exe"),
        (Join-Path $PSScriptRoot "charta.exe"),
        (Join-Path $PSScriptRoot "tui-db-windows-amd64.exe")
    )
    $sourcePath = $candidates | Where-Object {
        Test-Path -LiteralPath $_ -PathType Leaf
    } | Select-Object -First 1

    if (-not $sourcePath) {
        throw "Windows binary not found. Run 'make build-windows' first or pass its path to this installer."
    }
}

New-Item -ItemType Directory -Force -Path $installDir | Out-Null
Copy-Item -LiteralPath $sourcePath -Destination $targetPath -Force
Unblock-File -LiteralPath $targetPath -ErrorAction SilentlyContinue

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
$userEntries = @($userPath -split ";" | Where-Object { $_ })
$normalizedInstallDir = $installDir.TrimEnd("\")
$alreadyMapped = $false

foreach ($entry in $userEntries) {
    if ([string]::Equals(
        $entry.Trim().TrimEnd("\"),
        $normalizedInstallDir,
        [StringComparison]::OrdinalIgnoreCase
    )) {
        $alreadyMapped = $true
        break
    }
}

if (-not $alreadyMapped) {
    $updatedUserPath = (($userEntries + $installDir) -join ";")
    [Environment]::SetEnvironmentVariable("Path", $updatedUserPath, "User")
}

$processEntries = @($env:Path -split ";" | Where-Object { $_ })
$availableNow = $false
foreach ($entry in $processEntries) {
    if ([string]::Equals(
        $entry.Trim().TrimEnd("\"),
        $normalizedInstallDir,
        [StringComparison]::OrdinalIgnoreCase
    )) {
        $availableNow = $true
        break
    }
}
if (-not $availableNow) {
    $env:Path = "$env:Path;$installDir"
}

Write-Host "charta installed at $targetPath"
Write-Host "PATH updated for the current user. Run: charta"
