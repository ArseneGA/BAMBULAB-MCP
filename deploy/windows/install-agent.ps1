<#
.SYNOPSIS
  Installe l'agent Windows de l'Atelier Bambu (bambu-agent.exe) et l'appaire au serveur.
.DESCRIPTION
  1. Copie bambu-agent.exe dans %LOCALAPPDATA%\BambuAtelier (verifie SHA256SUMS s'il est present).
  2. Appairage guide : sur le SERVEUR, l'humain lance  sudo atelier-admin agent pair  (Linux) ;
     il colle ici le jeton affiche (usage unique, 15 min). Le secret de l'agent est stocke protege par DPAPI.
  3. Dossiers autorises (lecture seule) : -Folder "Modeles=D:\3D".
  4. Demarrage a l'ouverture de session (tache planifiee, sinon cle Run), sans droits administrateur.
  Le script est idempotent : le relancer met a jour l'exe, ne re-appaire pas un agent deja appaire (sauf -ForcePair).
.PARAMETER Server
  URL de bambud, ex. http://192.168.1.20:7878 (ou https://... derriere un proxy TLS).
.PARAMETER Token
  Jeton d'appairage. S'il est omis, il est demande (saisie masquee).
.PARAMETER Fingerprint
  Empreinte SHA-256 du certificat TLS du serveur (epinglage ; https avec certificat auto-signe).
.PARAMETER Folder
  Dossiers autorises, au format "Etiquette=Chemin" (repetable).
.PARAMETER Source
  Chemin de bambu-agent.exe (defaut : a cote du script, dans le dossier de livraison ou dist\).
.EXAMPLE
  .\install-agent.ps1 -Server http://192.168.1.20:7878 -Folder "Modeles=D:\3D"
#>
[CmdletBinding()]
param(
  [string]$Server,
  [string]$Token,
  [string]$Fingerprint,
  [string]$Name = $env:COMPUTERNAME,
  [string[]]$Folder = @(),
  [string]$Source,
  [ValidateSet('user', 'machine')][string]$DpapiScope = 'user',
  [switch]$ForcePair,
  [switch]$NoAutostart,
  [switch]$NoStart
)
$ErrorActionPreference = 'Stop'
$installDir = Join-Path $env:LOCALAPPDATA 'BambuAtelier'
$exe = Join-Path $installDir 'bambu-agent.exe'
$taskName = 'BambuAtelierAgent'

function Say([string]$m) { Write-Host ''; Write-Host "==> $m" }
function Invoke-Agent {
  param([string[]]$ArgList)
  & $exe @ArgList
  if ($LASTEXITCODE -ne 0) { throw "bambu-agent $($ArgList[0]) a echoue (code $LASTEXITCODE)" }
}

# --- 1. Trouver et copier l'exe ---
Say 'Copie de bambu-agent.exe'
if (-not $Source) {
  $here = Split-Path -Parent $MyInvocation.MyCommand.Path
  $cands = @(
    (Join-Path $here 'bambu-agent-windows-amd64.exe'),
    (Join-Path $here 'bambu-agent.exe'),
    (Join-Path $here '..\..\bambu-agent-windows-amd64.exe'),
    (Join-Path $here '..\..\bin\bambu-agent.exe')
  ) + @(Get-ChildItem (Join-Path $here '..\..\dist') -Recurse -Filter 'bambu-agent-windows-amd64.exe' -ErrorAction SilentlyContinue | Sort-Object LastWriteTime -Descending | ForEach-Object { $_.FullName })
  $Source = $cands | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
}
if (-not $Source -or -not (Test-Path $Source)) {
  throw 'bambu-agent.exe introuvable : indiquer -Source <chemin> (fichier bambu-agent-windows-amd64.exe de la livraison).'
}
$Source = (Resolve-Path $Source).Path
$sums = Join-Path (Split-Path -Parent $Source) 'SHA256SUMS'
if (Test-Path $sums) {
  $line = Get-Content $sums | Where-Object { $_ -match ('\s\*?' + [regex]::Escape((Split-Path -Leaf $Source)) + '$') } | Select-Object -First 1
  if ($line) {
    $want = ($line -split '\s+')[0].ToLower()
    $got = (Get-FileHash $Source -Algorithm SHA256).Hash.ToLower()
    if ($want -ne $got) { throw "Empreinte differente de SHA256SUMS pour $Source : livraison corrompue ou modifiee." }
    Write-Host 'empreinte SHA-256 verifiee'
  }
} else {
  Write-Warning "Pas de SHA256SUMS a cote de $Source : empreinte non verifiee."
}

New-Item -ItemType Directory -Force $installDir | Out-Null
# Arreter l'agent en cours (il ne peut pas etre ecrase tant qu'il tourne).
Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue | Where-Object { $_.State -eq 'Running' } | Stop-ScheduledTask -ErrorAction SilentlyContinue
Get-Process -Name 'bambu-agent' -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $exe } | ForEach-Object {
  Write-Host "arret de l'agent en cours (PID $($_.Id))"
  $_ | Stop-Process -Force
  $_ | Wait-Process -Timeout 15 -ErrorAction SilentlyContinue
}
Copy-Item $Source $exe -Force
Write-Host "installe : $exe"
& $exe version

# --- 2. Appairage guide ---
$paired = $false
try {
  $st = (& $exe status 2>$null) -join "`n" | ConvertFrom-Json
  $paired = [bool]$st.paired
} catch { $paired = $false }

if ($paired -and -not $ForcePair) {
  Say "Agent deja appaire ($($st.server)) : appairage conserve (-ForcePair pour recommencer)"
} else {
  Say 'Appairage avec le serveur'
  if (-not $Server) {
    Write-Host "Sur le SERVEUR Linux, lancez :   sudo atelier-admin agent pair"
    Write-Host 'Il affiche un jeton (usage unique, valable 15 minutes) et l''adresse du serveur.'
    $Server = Read-Host 'Adresse du serveur (ex. http://192.168.1.20:7878)'
  }
  if (-not $Token) {
    $sec = Read-Host 'Collez le jeton d''appairage (saisie masquee)' -AsSecureString
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec)
    try { $Token = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr) } finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr) }
  }
  if (-not $Server -or -not $Token) { throw 'Adresse du serveur et jeton requis.' }
  $pairArgs = @('pair', '--server', $Server.TrimEnd('/'), '--token', $Token, '--name', $Name, '--dpapi-scope', $DpapiScope)
  if ($Fingerprint) { $pairArgs += @('--server-fingerprint', $Fingerprint) }
  if ($Server -match '^http://' -and $Server -notmatch '^http://(127\.0\.0\.1|localhost|\[::1\])') {
    Write-Warning 'Liaison http:// hors de cette machine : le secret de l''agent et les fichiers circulent en clair sur le reseau local. Acceptable sur un reseau domestique de confiance ; sinon placer un proxy HTTPS devant bambud et utiliser -Fingerprint.'
  }
  Invoke-Agent $pairArgs
  $Token = $null
}

# --- 3. Dossiers autorises ---
foreach ($f in $Folder) {
  $label, $path = $f -split '=', 2
  if (-not $label -or -not $path) { throw "Format de -Folder invalide : '$f' (attendu Etiquette=Chemin)" }
  if (-not (Test-Path $path -PathType Container)) { Write-Warning "Dossier introuvable, ignore : $path"; continue }
  Invoke-Agent @('folder', 'add', '--label', $label, '--path', (Resolve-Path $path).Path)
}

# --- 4. Demarrage a l'ouverture de session ---
if (-not $NoAutostart) {
  Say 'Demarrage automatique a l''ouverture de session'
  $runCmd = "powershell.exe -NoProfile -WindowStyle Hidden -Command `"& '$exe' run`""
  $done = $false
  try {
    $action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument "-NoProfile -WindowStyle Hidden -Command `"& '$exe' run`""
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew
    Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Settings $settings -Description "Agent de l'Atelier Bambu (ouverture de session)" -Force | Out-Null
    Write-Host "tache planifiee '$taskName' creee (a l'ouverture de session de $env:USERNAME)"
    $done = $true
  } catch {
    Write-Warning "Tache planifiee impossible ($($_.Exception.Message)) : repli sur la cle Run de l'utilisateur."
  }
  if (-not $done) {
    Set-ItemProperty -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name 'BambuAtelierAgent' -Value $runCmd
    Write-Host 'cle Run HKCU creee (BambuAtelierAgent)'
  }
  if (-not $NoStart) {
    if ($done) { Start-ScheduledTask -TaskName $taskName } else { Start-Process powershell.exe -ArgumentList '-NoProfile', '-WindowStyle', 'Hidden', '-Command', "& '$exe' run" -WindowStyle Hidden }
    Write-Host 'agent demarre'
  }
} else {
  Write-Host "Demarrage automatique ignore. Lancement manuel : & '$exe' run"
}

Say 'Termine'
Write-Host "Etat   : & '$exe' status"
Write-Host "Retrait: Unregister-ScheduledTask -TaskName $taskName -Confirm:`$false ; Remove-Item '$installDir\bambu-agent.exe'   (agent.json et le secret DPAPI restent)"
Write-Host "Cote serveur, l'agent doit apparaitre 'en ligne' (get_capabilities, ou la page Etat de l'Atelier)."
