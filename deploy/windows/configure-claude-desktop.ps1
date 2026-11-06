<#
.SYNOPSIS
  Ajoute (sans ecraser les autres serveurs) l'entree "bambu-atelier" a la configuration de Claude Desktop.
.DESCRIPTION
  - Copie le pont bambu-mcp.exe dans %LOCALAPPDATA%\BambuAtelier.
  - Enregistre le jeton d'API du serveur dans un FICHIER protege (droits limites a votre compte Windows) :
    le jeton n'apparait pas dans claude_desktop_config.json (le pont le lit via --api-token-file).
  - Sauvegarde claude_desktop_config.json (copie horodatee) puis y ajoute / met a jour uniquement
    mcpServers.bambu-atelier. Les autres serveurs et reglages sont conserves. Un JSON invalide n'est jamais modifie.
  Redemarrez ensuite Claude Desktop completement (Quitter depuis la zone de notification, puis relancer).
.PARAMETER Server
  Adresse du serveur, ex. http://192.168.1.20:7878 (le script ajoute /mcp).
.PARAMETER Token
  Jeton d'API (visible cote serveur :  sudo grep BAMBU_API_TOKEN /etc/atelier/atelier.env). S'il est omis et
  qu'aucun fichier de jeton n'existe, il est demande (saisie masquee).
.PARAMETER Remove
  Retire l'entree bambu-atelier (la configuration est sauvegardee avant).
.PARAMETER DryRun
  Affiche ce qui serait ecrit sans rien modifier.
.EXAMPLE
  .\configure-claude-desktop.ps1 -Server http://192.168.1.20:7878
#>
[CmdletBinding()]
param(
  [string]$Server,
  [string]$Token,
  [string]$TokenFile = (Join-Path $env:LOCALAPPDATA 'BambuAtelier\api-token.txt'),
  [string]$BridgeSource,
  [string]$ConfigPath = (Join-Path $env:APPDATA 'Claude\claude_desktop_config.json'),
  [string]$EntryName = 'bambu-atelier',
  [switch]$Remove,
  [switch]$DryRun
)
$ErrorActionPreference = 'Stop'
$installDir = Join-Path $env:LOCALAPPDATA 'BambuAtelier'
$bridge = Join-Path $installDir 'bambu-mcp.exe'
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)

function Say([string]$m) { Write-Host ''; Write-Host "==> $m" }

function Read-Config([string]$path) {
  if (-not (Test-Path $path)) { return [pscustomobject]@{} }
  $raw = [IO.File]::ReadAllText($path)
  if ([string]::IsNullOrWhiteSpace($raw)) { return [pscustomobject]@{} }
  try { return ($raw | ConvertFrom-Json) }
  catch { throw "Le fichier $path n'est pas un JSON valide : il n'a pas ete modifie. Corrigez-le (ou deplacez-le) puis relancez. Detail : $($_.Exception.Message)" }
}

# --- Mode retrait ---
if ($Remove) {
  Say "Retrait de l'entree $EntryName"
  $cfg = Read-Config $ConfigPath
  if (-not ($cfg.PSObject.Properties.Name -contains 'mcpServers') -or -not ($cfg.mcpServers.PSObject.Properties.Name -contains $EntryName)) {
    Write-Host 'Entree absente : rien a faire.'; return
  }
  if ($DryRun) { Write-Host "[DryRun] l'entree $EntryName serait retiree de $ConfigPath"; return }
  $bak = "$ConfigPath.bak-$(Get-Date -Format yyyyMMdd-HHmmss)"
  Copy-Item $ConfigPath $bak
  $cfg.mcpServers.PSObject.Properties.Remove($EntryName)
  [IO.File]::WriteAllText($ConfigPath, ($cfg | ConvertTo-Json -Depth 100), $utf8NoBom)
  Write-Host "Entree retiree. Sauvegarde : $bak"
  return
}

if (-not $Server) { $Server = Read-Host 'Adresse du serveur (ex. http://192.168.1.20:7878)' }
if ($Server -notmatch '^https?://[^/\s]+') { throw "Adresse invalide : '$Server' (attendu http(s)://hote:port)" }
$url = $Server.TrimEnd('/')
if ($url -notmatch '/mcp$') { $url = "$url/mcp" }

$null = Read-Config $ConfigPath   # refuse tout de suite un JSON invalide, avant de copier quoi que ce soit

# --- 1. Le pont ---
Say 'Pont bambu-mcp.exe'
if (-not $BridgeSource) {
  $here = Split-Path -Parent $MyInvocation.MyCommand.Path
  $cands = @(
    (Join-Path $here 'bambu-mcp-windows-amd64.exe'),
    (Join-Path $here 'bambu-mcp.exe'),
    (Join-Path $here '..\..\bambu-mcp-windows-amd64.exe'),
    (Join-Path $here '..\..\bin\bambu-mcp.exe'),
    $bridge
  ) + @(Get-ChildItem (Join-Path $here '..\..\dist') -Recurse -Filter 'bambu-mcp-windows-amd64.exe' -ErrorAction SilentlyContinue | Sort-Object LastWriteTime -Descending | ForEach-Object { $_.FullName })
  $BridgeSource = $cands | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
}
if (-not $BridgeSource -or -not (Test-Path $BridgeSource)) { throw 'bambu-mcp.exe introuvable : indiquer -BridgeSource <chemin vers bambu-mcp-windows-amd64.exe>.' }
$BridgeSource = (Resolve-Path $BridgeSource).Path
if (-not $DryRun) {
  New-Item -ItemType Directory -Force $installDir | Out-Null
  if ($BridgeSource -ne $bridge) { Copy-Item $BridgeSource $bridge -Force }
  Write-Host "pont installe : $bridge"
} else { Write-Host "[DryRun] pont : $bridge (source $BridgeSource)" }

# --- 2. Fichier de jeton protege ---
Say 'Jeton d''API'
if ($Token -or -not (Test-Path $TokenFile)) {
  if (-not $Token) {
    Write-Host 'Cote serveur :  sudo grep BAMBU_API_TOKEN /etc/atelier/atelier.env'
    $sec = Read-Host 'Collez le jeton d''API (saisie masquee)' -AsSecureString
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec)
    try { $Token = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr) } finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr) }
  }
  if (-not $Token -or $Token.Length -lt 16) { throw 'Jeton vide ou trop court.' }
  if ($DryRun) { Write-Host "[DryRun] le jeton serait ecrit dans $TokenFile (droits limites a votre compte)" }
  else {
    New-Item -ItemType Directory -Force (Split-Path -Parent $TokenFile) | Out-Null
    # Fichier cree vide, droits restreints AVANT d'y ecrire le jeton.
    [IO.File]::WriteAllText($TokenFile, '', $utf8NoBom)
    & icacls $TokenFile /inheritance:r /grant:r "$($env:USERDOMAIN)\$($env:USERNAME):(R,W)" | Out-Null
    if ($LASTEXITCODE -ne 0) { Remove-Item $TokenFile -Force; throw "icacls a echoue : le jeton n'a pas ete ecrit (code $LASTEXITCODE)." }
    [IO.File]::WriteAllText($TokenFile, $Token + "`n", $utf8NoBom)
    Write-Host "jeton enregistre dans $TokenFile (lisible par votre compte seulement)"
  }
  $Token = $null
} else {
  Write-Host "fichier de jeton existant conserve : $TokenFile"
}

# --- 3. claude_desktop_config.json ---
Say "Configuration de Claude Desktop ($ConfigPath)"
$cfg = Read-Config $ConfigPath
$entry = [pscustomobject]@{
  command = $bridge
  args    = @('--url', $url, '--api-token-file', $TokenFile)
}
if (-not ($cfg.PSObject.Properties.Name -contains 'mcpServers') -or $null -eq $cfg.mcpServers) {
  $cfg | Add-Member -NotePropertyName mcpServers -NotePropertyValue ([pscustomobject]@{}) -Force
}
$others = @($cfg.mcpServers.PSObject.Properties.Name | Where-Object { $_ -ne $EntryName })
$cfg.mcpServers | Add-Member -NotePropertyName $EntryName -NotePropertyValue $entry -Force
$json = $cfg | ConvertTo-Json -Depth 100
if ($DryRun) {
  Write-Host '[DryRun] contenu qui serait ecrit :'; Write-Host $json
  Write-Host "Autres serveurs conserves : $($others -join ', ')"
  return
}
New-Item -ItemType Directory -Force (Split-Path -Parent $ConfigPath) | Out-Null
if (Test-Path $ConfigPath) {
  $bak = "$ConfigPath.bak-$(Get-Date -Format yyyyMMdd-HHmmss)"
  Copy-Item $ConfigPath $bak
  Write-Host "sauvegarde : $bak"
}
$tmp = "$ConfigPath.tmp"
[IO.File]::WriteAllText($tmp, $json, $utf8NoBom)
Move-Item $tmp $ConfigPath -Force
Write-Host "entree '$EntryName' ecrite ($url). Autres serveurs conserves : $(if ($others) { $others -join ', ' } else { 'aucun' })"

Say 'Derniere etape'
Write-Host 'Quittez Claude Desktop completement (clic droit sur son icone de la zone de notification > Quitter), puis relancez-le.'
Write-Host "Dans une conversation, l'outil 'bambu-atelier' doit apparaitre (menu des outils/connecteurs)."
