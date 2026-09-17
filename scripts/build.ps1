param(
  [string]$OutputDir = "..\_release"
)

$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = Split-Path -Parent $scriptDir
Set-Location $repoRoot

$binaryName = "moleagent-client.exe"
$cmdPath = ".\cmd\moleagent-client"
$releaseDir = [System.IO.Path]::GetFullPath((Join-Path $repoRoot $OutputDir))

$version = (git describe --tags --always --dirty 2>$null)
if (-not $version) { $version = "dev" }

$build = (git rev-parse --short HEAD 2>$null)
if (-not $build) { $build = "unknown" }

$date = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
$ldflags = "-s -w -X moleAgent_client/internal/version.Version=$version -X moleAgent_client/internal/version.GitHash=$build -X moleAgent_client/internal/version.BuildDate=$date"

New-Item -ItemType Directory -Path $releaseDir -Force | Out-Null
$output = Join-Path $releaseDir $binaryName

Write-Host ">> Building $binaryName ($version)..."
$env:CGO_ENABLED = "0"
go build -tags p2p -trimpath -ldflags $ldflags -o $output $cmdPath
Write-Host ">> Done: $output"
