<#
.SYNOPSIS
  Construit une livraison complete dans dist\<version>\ (equivalent de release.sh).
.DESCRIPTION
  - interface web + vues MCP App (si web\ existe : npm ci puis npm run build[, build:mcp-app]) ;
  - bambud, bambu-mcp, printer-sim pour linux/amd64 et linux/arm64 ;
  - bambu-mcp.exe et bambu-agent.exe pour windows/amd64 ;
  - archive du worker CAO (workers\geometry sans .venv ni caches), archive de deploiement, SHA256SUMS.
  SQLite est pur Go : CGO_ENABLED=0, aucune chaine C pour la compilation croisee.
.EXAMPLE
  .\scripts\release.ps1 -Version 0.9.0
  .\scripts\release.ps1 -SkipWeb -SkipTests
#>
param(
  [string]$Version = '',
  [switch]$SkipWeb,
  [switch]$RequireWeb,
  [switch]$SkipTests,
  [string]$OutRoot = 'dist'
)
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path -Parent $PSScriptRoot)
$env:CGO_ENABLED = '0'

function Invoke-Native {
  param([string]$Exe, [string[]]$ArgList)
  & $Exe @ArgList
  if ($LASTEXITCODE -ne 0) { throw "$Exe $($ArgList -join ' ') a echoue ($LASTEXITCODE)" }
}
function Say([string]$m) { Write-Host ""; Write-Host "==> $m" }

if (-not $Version) {
  $Version = (& git describe --tags --always --dirty 2>$null)
  if (-not $Version) { $Version = 'dev' }
}
$out = Join-Path $OutRoot $Version
$ldflags = "-s -w -X github.com/ArseneGA/BAMBULAB-MCP/internal/core.Version=$Version"

# --- 1. Interface web et vues MCP App ---
if ($SkipWeb) {
  Say 'web : ignore (-SkipWeb)'
} elseif (Test-Path 'web\package.json') {
  Say 'web : npm ci && npm run build'
  Push-Location web
  try {
    Invoke-Native 'npm' @('ci')
    Invoke-Native 'npm' @('run', 'build')
    $pkg = Get-Content package.json -Raw | ConvertFrom-Json
    if ($pkg.scripts -and $pkg.scripts.PSObject.Properties.Name -contains 'build:mcp-app') {
      Write-Host 'vues MCP App : npm run build:mcp-app'
      Invoke-Native 'npm' @('run', 'build:mcp-app')
    }
  } finally { Pop-Location }
} elseif ($RequireWeb) {
  throw 'web\ est absent et -RequireWeb est demande'
} else {
  Say 'web : dossier web\ absent, etape ignoree'
}

# --- 2. Controles ---
Say 'go vet'
Invoke-Native 'go' @('vet', './...')
if (-not $SkipTests) {
  Say 'go test -short'
  Invoke-Native 'go' @('test', '-short', './...', '-count=1')
}

# --- 3. Binaires ---
if (Test-Path $out) { Remove-Item -Recurse -Force $out }
New-Item -ItemType Directory -Force $out | Out-Null

function Build-One([string]$goos, [string]$goarch, [string]$cmd, [string]$name) {
  Write-Host "  $goos/$goarch $cmd -> $name"
  $env:GOOS = $goos; $env:GOARCH = $goarch
  try { Invoke-Native 'go' @('build', '-trimpath', '-ldflags', $ldflags, '-o', (Join-Path $out $name), "./cmd/$cmd") }
  finally { Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue }
}

Say 'binaires Linux'
foreach ($arch in 'amd64', 'arm64') {
  Build-One 'linux' $arch 'bambud' "bambud-linux-$arch"
  Build-One 'linux' $arch 'bambu-mcp' "bambu-mcp-linux-$arch"
  Build-One 'linux' $arch 'printer-sim' "printer-sim-linux-$arch"
}
Say 'binaires Windows'
Build-One 'windows' 'amd64' 'bambu-mcp' 'bambu-mcp-windows-amd64.exe'
Build-One 'windows' 'amd64' 'bambu-agent' 'bambu-agent-windows-amd64.exe'

# --- 4. Worker CAO ---
Say 'worker CAO'
$cad = "atelier-cad-worker-$Version.tar.gz"
Invoke-Native 'tar' @('--exclude=.venv', '--exclude=__pycache__', '--exclude=.pytest_cache', '--exclude=*.pyc',
  '--exclude=.mypy_cache', '--exclude=.ruff_cache', '-czf', (Join-Path $out $cad), '-C', 'workers', 'geometry')
Write-Host "  $cad"

# --- 5. Deploiement et documentation ---
Say 'archive de deploiement'
$items = @('deploy', 'README.md', 'docs/INSTALL.md', 'docs/BRANCHEMENT.md', 'docs/UTILISATION.md', 'docs/ETAT.md', 'docs/SECURITY.md') | Where-Object { Test-Path $_ }
$dep = "atelier-deploy-$Version.tar.gz"
Invoke-Native 'tar' (@('-czf', (Join-Path $out $dep)) + $items)
Write-Host "  $dep ($($items.Count) elements)"

# --- 6. Empreintes (format sha256sum : "<hash>  <nom>") ---
Say 'SHA256SUMS'
$lines = Get-ChildItem $out -File | Where-Object { $_.Name -ne 'SHA256SUMS' } | Sort-Object Name | ForEach-Object {
  '{0}  {1}' -f (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower(), $_.Name
}
[System.IO.File]::WriteAllText((Join-Path $out 'SHA256SUMS'), (($lines -join "`n") + "`n"), (New-Object System.Text.UTF8Encoding($false)))
$lines | ForEach-Object { Write-Host $_ }
Say "livraison prete : $out"
