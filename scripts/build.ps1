<#
.SYNOPSIS
  Build, tests et compilation croisée (équivalent du Makefile pour Windows).
.EXAMPLE
  ./scripts/build.ps1 -Task all     # vet + test + build
  ./scripts/build.ps1 -Task cross   # linux/amd64 + linux/arm64
#>
param(
  [ValidateSet('all', 'build', 'test', 'test-race', 'vet', 'cross', 'clean')]
  [string]$Task = 'all'
)
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path -Parent $PSScriptRoot)
$env:CGO_ENABLED = '0'   # SQLite pur Go : pas de cgo
$ldflags = '-s -w'
$cmds = 'bambud', 'bambu-mcp'

function Invoke-Go {
  & go @args
  if ($LASTEXITCODE -ne 0) { throw "go $($args -join ' ') a échoué ($LASTEXITCODE)" }
}

function Build {
  New-Item -ItemType Directory -Force bin | Out-Null
  $ext = if ($IsLinux -or $IsMacOS) { '' } else { '.exe' }
  foreach ($c in $cmds) { Invoke-Go build -ldflags $ldflags -o "bin/$c$ext" "./cmd/$c" }
}

function Cross {
  foreach ($arch in 'amd64', 'arm64') {
    $env:GOOS = 'linux'; $env:GOARCH = $arch
    try {
      foreach ($c in $cmds) {
        New-Item -ItemType Directory -Force "bin/linux-$arch" | Out-Null
        Invoke-Go build -ldflags $ldflags -o "bin/linux-$arch/$c" "./cmd/$c"
      }
      Invoke-Go build ./...
    } finally { Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue }
  }
  Write-Host 'cross-compile OK (linux/amd64, linux/arm64)'
}

switch ($Task) {
  'vet'       { Invoke-Go vet ./... }
  'test'      { Invoke-Go test ./... -count=1 }
  # -race exige cgo + compilateur C ; échoue proprement si absent.
  'test-race' { $env:CGO_ENABLED = '1'; Invoke-Go test ./... -race -count=1 }
  'build'     { Build }
  'cross'     { Cross }
  'clean'     { Remove-Item -Recurse -Force bin, data -ErrorAction SilentlyContinue }
  'all'       { Invoke-Go vet ./...; Invoke-Go test ./... -count=1; Build }
}
