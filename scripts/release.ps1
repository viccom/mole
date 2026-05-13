param(
  [string]$OutputDir = "..\_release"
)

$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = Split-Path -Parent $scriptDir
Set-Location $repoRoot

$binaryName = "moleagent-client"
$cmdPath = ".\cmd\moleagent-client"
$releaseDir = [System.IO.Path]::GetFullPath((Join-Path $repoRoot $OutputDir))
$targets = @(
  @{ GOOS = "linux"; GOARCH = "amd64"; Ext = "" },
  @{ GOOS = "linux"; GOARCH = "arm64"; Ext = "" },
  @{ GOOS = "linux"; GOARCH = "arm"; GOARM = "7"; Ext = "" },
  @{ GOOS = "darwin"; GOARCH = "amd64"; Ext = "" },
  @{ GOOS = "darwin"; GOARCH = "arm64"; Ext = "" },
  @{ GOOS = "windows"; GOARCH = "amd64"; Ext = ".exe" },
  @{ GOOS = "windows"; GOARCH = "arm64"; Ext = ".exe" }
)

$version = (git describe --tags --always --dirty 2>$null)
if (-not $version) { $version = "dev" }

$build = (git rev-parse --short HEAD 2>$null)
if (-not $build) { $build = "unknown" }

$date = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
$ldflags = "-s -w -X moleAgent_client/internal/version.Version=$version -X moleAgent_client/internal/version.GitHash=$build -X moleAgent_client/internal/version.BuildDate=$date"

New-Item -ItemType Directory -Path $releaseDir -Force | Out-Null

foreach ($target in $targets) {
  $archTag = if ($target.GOARM) { "armv$($target.GOARM)" } else { $target.GOARCH }
  $output = Join-Path $releaseDir "$binaryName-$($target.GOOS)-$archTag$($target.Ext)"

  Write-Host ">> Building $output ..."
  $env:CGO_ENABLED = "0"
  $env:GOOS = $target.GOOS
  $env:GOARCH = $target.GOARCH
  if ($target.GOARM) {
    $env:GOARM = $target.GOARM
  } else {
    Remove-Item Env:GOARM -ErrorAction SilentlyContinue
  }

  go build -trimpath -ldflags $ldflags -o $output $cmdPath
}

Remove-Item Env:GOOS, Env:GOARCH, Env:GOARM -ErrorAction SilentlyContinue
Write-Host ">> All platforms built in $releaseDir"
