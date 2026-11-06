<#
.SYNOPSIS
  Test de fumee local de bout en bout (aucune imprimante reelle, aucun acces reseau hors loopback).
.DESCRIPTION
  Demarre bambud sur un dossier de donnees temporaire (127.0.0.1, port libre) et le simulateur printer-sim,
  puis : seed-demo, /health, garde du jeton d'API, enregistrement d'un navigateur (bambud enroll puis /enroll),
  quelques outils MCP via bambu-mcp en stdio (avec --api-token-file), sauvegarde a chaud, verification de
  l'archive, restauration dans un dossier neuf et verification de ce dossier.
  Les binaires sont compiles dans un dossier temporaire ; si l'execution d'un .exe est bloquee
  (« Acces refuse » sur certains postes), le script bascule automatiquement sur « go run ».
.PARAMETER UseGoRun
  Force « go run » au lieu d'executables compiles.
.PARAMETER KeepData
  Conserve le dossier temporaire (journaux, donnees) pour inspection.
.EXAMPLE
  .\scripts\smoke.ps1
#>
[CmdletBinding()]
param([switch]$UseGoRun, [switch]$KeepData)
$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo
$env:CGO_ENABLED = '0'
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("bambu-smoke-" + [guid]::NewGuid().ToString('N').Substring(0, 8))
$bin = Join-Path $tmp 'bin'; $data = Join-Path $tmp 'data'; $logs = Join-Path $tmp 'logs'
New-Item -ItemType Directory -Force $bin, $data, $logs | Out-Null

$results = New-Object System.Collections.Generic.List[object]
$procs = New-Object System.Collections.Generic.List[object]
function Check([string]$name, [bool]$ok, [string]$detail = '') {
  $results.Add([pscustomobject]@{ Test = $name; Ok = $ok; Detail = $detail })
  $tag = if ($ok) { 'OK  ' } else { 'FAIL' }
  Write-Host ("[{0}] {1} {2}" -f $tag, $name, $detail)
}

# --- Outils : executables compiles, sinon go run ---
$mode = 'exe'
if ($UseGoRun) { $mode = 'gorun' } else {
  try {
    foreach ($t in 'bambud', 'bambu-mcp', 'printer-sim') {
      & go build -o (Join-Path $bin "$t.exe") "./cmd/$t"
      if ($LASTEXITCODE -ne 0) { throw "go build $t a echoue" }
    }
    $v = & (Join-Path $bin 'bambud.exe') --version 2>&1
    if ($LASTEXITCODE -ne 0) { throw "bambud.exe --version : $v" }
  } catch {
    Write-Host "Executables compiles inutilisables ($($_.Exception.Message)) -> bascule sur 'go run'."
    $mode = 'gorun'
  }
}
Write-Host "Mode d'execution : $mode ; dossier temporaire : $tmp"

function Get-ToolCommand([string]$name, [string[]]$ToolArgs) {
  if ($mode -eq 'exe') { return @{ File = (Join-Path $bin "$name.exe"); Args = $ToolArgs } }
  return @{ File = 'go'; Args = (@('run', "./cmd/$name") + $ToolArgs) }
}
function Quote-Arg([string]$a) { if ($a -match '[\s"]') { '"' + ($a -replace '"', '\"') + '"' } else { $a } }

# Lance un outil au premier plan et renvoie sortie standard + erreur + code.
function Invoke-Tool([string]$name, [string[]]$ToolArgs) {
  $c = Get-ToolCommand $name $ToolArgs
  $psi = New-Object System.Diagnostics.ProcessStartInfo
  $psi.FileName = $c.File; $psi.Arguments = ($c.Args | ForEach-Object { Quote-Arg $_ }) -join ' '
  $psi.UseShellExecute = $false; $psi.RedirectStandardOutput = $true; $psi.RedirectStandardError = $true; $psi.CreateNoWindow = $true
  $p = [System.Diagnostics.Process]::Start($psi)
  $so = $p.StandardOutput.ReadToEndAsync(); $se = $p.StandardError.ReadToEndAsync()
  if (-not $p.WaitForExit(240000)) { try { & taskkill /T /F /PID $p.Id | Out-Null } catch {}; throw "$name : delai depasse" }
  [pscustomobject]@{ Out = $so.Result; Err = $se.Result; Code = $p.ExitCode }
}

# Lance un service en arriere-plan (sortie vers des fichiers de journal).
function Start-Service-Tool([string]$name, [string[]]$ToolArgs, [string]$tag) {
  $c = Get-ToolCommand $name $ToolArgs
  $out = Join-Path $logs "$tag.out.log"; $err = Join-Path $logs "$tag.err.log"
  $p = Start-Process -FilePath $c.File -ArgumentList (($c.Args | ForEach-Object { Quote-Arg $_ }) -join ' ') -PassThru -WindowStyle Hidden -RedirectStandardOutput $out -RedirectStandardError $err
  $procs.Add($p)
  return [pscustomobject]@{ Proc = $p; Out = $out; Err = $err }
}
function Stop-All { foreach ($p in $procs) { try { if (-not $p.HasExited) { & taskkill /T /F /PID $p.Id 2>&1 | Out-Null } } catch {} } }

function Free-Port { $l = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0); $l.Start(); $p = $l.LocalEndpoint.Port; $l.Stop(); $p }
function Wait-Until([scriptblock]$cond, [int]$seconds) {
  $end = (Get-Date).AddSeconds($seconds)
  while ((Get-Date) -lt $end) { try { if (& $cond) { return $true } } catch {}; Start-Sleep -Milliseconds 300 }
  return $false
}

try {
  # --- 1. Simulateur d'imprimante (ports ephemeres, loopback) ---
  $sim = Start-Service-Tool 'printer-sim' @('--mqtt-port', '0', '--ftp-port', '0', '--control-port', '-1') 'sim'
  $simOk = Wait-Until { (Test-Path $sim.Out) -and ((Get-Content $sim.Out -Raw) -match 'cert sha256 ([0-9a-f]{64})') } 120
  $fp = ''
  if ($simOk -and ((Get-Content $sim.Out -Raw) -match 'cert sha256 ([0-9a-f]{64})')) { $fp = $Matches[1] }
  Check 'printer-sim demarre' $simOk $(if ($simOk) { "empreinte $($fp.Substring(0, 16))..." } else { (Get-Content $sim.Err -Raw -ErrorAction SilentlyContinue) })

  # --- 2. seed-demo (avant le service : meme dossier, verrou d'instance) ---
  $seed = Invoke-Tool 'bambud' @('seed-demo', '--data-dir', $data, '--no-cad', '--wait', '90s')
  $seedOk = ($seed.Code -eq 0)
  Check 'bambud seed-demo' $seedOk $(if (-not $seedOk) { $seed.Err.Trim() } else { 'modeles de demonstration importes' })

  # --- 3. bambud ---
  $port = Free-Port
  $token = -join ((1..48) | ForEach-Object { '0123456789abcdef'[(Get-Random -Maximum 16)] })
  $tokFile = Join-Path $tmp 'api-token.txt'
  [IO.File]::WriteAllText($tokFile, "$token`n", (New-Object System.Text.UTF8Encoding($false)))
  $base = "http://127.0.0.1:$port"
  $svc = Start-Service-Tool 'bambud' @('--addr', "127.0.0.1:$port", '--data-dir', $data, '--api-token', $token, '--public-url', $base, '--allow-any-printer-host', '--log-level', 'warn') 'bambud'
  $up = Wait-Until { (Invoke-WebRequest "$base/health" -UseBasicParsing -TimeoutSec 3).StatusCode -eq 200 } 180
  Check 'GET /health' $up $base
  if (-not $up) { throw "bambud ne repond pas : $(Get-Content $svc.Err -Raw -ErrorAction SilentlyContinue)" }
  $h = Invoke-RestMethod "$base/health" -TimeoutSec 5
  Check '/health sans jeton' $true ($h | ConvertTo-Json -Compress)

  # --- 4. Garde du jeton d'API ---
  $noAuth = 0; try { Invoke-WebRequest "$base/api/capabilities" -UseBasicParsing -TimeoutSec 5 | Out-Null; $noAuth = 200 } catch { $noAuth = [int]$_.Exception.Response.StatusCode }
  Check 'API sans jeton refusee (401)' ($noAuth -eq 401) "code $noAuth"
  $auth = @{ Authorization = "Bearer $token" }
  $caps = Invoke-RestMethod "$base/api/capabilities" -Headers $auth -TimeoutSec 10
  Check 'GET /api/capabilities avec jeton' ($caps.service -eq 'bambud') "mode=$($caps.mode) version=$($caps.version)"

  # --- 5. Enregistrement d'un navigateur (code lu sur le terminal de l'humain) ---
  $en = Invoke-Tool 'bambud' @('enroll', '--data-dir', $data, '--public-url', $base)
  $code = if ($en.Out -match "Code d'enregistrement : ([A-Z2-9]{4}-[A-Z2-9]{4})") { $Matches[1] } else { '' }
  Check 'bambud enroll affiche un code' ($code -ne '') $(if ($code) { 'code a 8 caracteres (non reproduit)' } else { $en.Err })
  if ($code) {
    $sess = $null
    $hdr = @{ 'Sec-Fetch-Site' = 'same-origin'; 'Sec-Fetch-Mode' = 'navigate' }
    $r = Invoke-WebRequest "$base/enroll" -Method Post -Body @{ code = $code; label = 'smoke' } -Headers $hdr -SessionVariable sess -UseBasicParsing -TimeoutSec 10
    $si = Invoke-RestMethod "$base/api/session" -Headers $auth -WebSession $sess -TimeoutSec 10
    Check 'navigateur enregistre via /enroll' ([bool]$si.enrolled) "HTTP $($r.StatusCode)"
    $noCookie = Invoke-RestMethod "$base/api/session" -Headers $auth -TimeoutSec 10
    Check 'sans cookie : non enregistre' (-not [bool]$noCookie.enrolled)
    $reuse = $null
    try { $reuse = Invoke-WebRequest "$base/enroll" -Method Post -Body @{ code = $code } -Headers $hdr -UseBasicParsing -TimeoutSec 10 } catch { $reuse = $_.Exception.Response }
    Check 'code a usage unique (rejeu refuse)' ([int]$reuse.StatusCode -ge 400) "HTTP $([int]$reuse.StatusCode)"
  }

  # --- 6. Outils MCP via bambu-mcp (stdio) ---
  $mc = Get-ToolCommand 'bambu-mcp' @('--url', "$base/mcp", '--api-token-file', $tokFile, '--connect-attempts', '3')
  $psi = New-Object System.Diagnostics.ProcessStartInfo
  $psi.FileName = $mc.File; $psi.Arguments = ($mc.Args | ForEach-Object { Quote-Arg $_ }) -join ' '
  $psi.UseShellExecute = $false; $psi.RedirectStandardInput = $true; $psi.RedirectStandardOutput = $true; $psi.RedirectStandardError = $true; $psi.CreateNoWindow = $true
  $psi.StandardOutputEncoding = [Text.Encoding]::UTF8
  # PS 5.1 : le flux d'entree d'un processus enfant commence par un BOM UTF-8 quand la page de codes console est 65001 ;
  # une page ASCII compatible (850) l'evite (nos requetes JSON sont en ASCII pur).
  $oldCp = $null; try { $oldCp = [Console]::InputEncoding; [Console]::InputEncoding = [Text.Encoding]::GetEncoding(850) } catch {}
  $mcp = [System.Diagnostics.Process]::Start($psi); $procs.Add($mcp)
  if ($oldCp) { try { [Console]::InputEncoding = $oldCp } catch {} }
  $mcpErr = $mcp.StandardError.ReadToEndAsync()
  $mcpIn = New-Object System.IO.StreamWriter($mcp.StandardInput.BaseStream, (New-Object System.Text.UTF8Encoding($false)))   # PS 5.1 : pas de BOM sur le flux du protocole
  $script:nextId = 0
  function Send-Rpc([string]$method, $params, [switch]$Notify) {
    $msg = [ordered]@{ jsonrpc = '2.0'; method = $method }
    if ($null -ne $params) { $msg.params = $params }
    if (-not $Notify) { $script:nextId++; $msg.id = $script:nextId }
    $mcpIn.WriteLine(($msg | ConvertTo-Json -Depth 20 -Compress)); $mcpIn.Flush()
    if ($Notify) { return $null }
    $want = $script:nextId
    while ($true) {
      $t = $mcp.StandardOutput.ReadLineAsync()
      if (-not $t.Wait(120000)) { throw "MCP : pas de reponse a $method" }
      if ($null -eq $t.Result) { throw "MCP : flux ferme ($($mcpErr.Result))" }
      $obj = $t.Result | ConvertFrom-Json   # une ligne qui n'est pas du JSON ferait echouer ici : stdout doit rester du protocole
      if ($obj.PSObject.Properties.Name -contains 'id' -and $obj.id -eq $want) { return $obj }
    }
  }
  $init = Send-Rpc 'initialize' @{ protocolVersion = '2025-06-18'; capabilities = @{}; clientInfo = @{ name = 'smoke'; version = '1' } }
  Check 'MCP initialize (pont stdio -> bambud)' ($null -ne $init.result.serverInfo) "$($init.result.serverInfo.name) $($init.result.serverInfo.version)"
  Send-Rpc 'notifications/initialized' $null -Notify | Out-Null
  $tl = Send-Rpc 'tools/list' @{}
  $names = @($tl.result.tools | ForEach-Object { $_.name })
  Check 'MCP tools/list' ($names.Count -ge 40 -and $names -contains 'get_capabilities' -and $names -contains 'search_models') "$($names.Count) outils"
  $noApprove = -not ($names -contains 'confirm_print_from_app')
  Check 'aucun outil d''approbation de lancement' $noApprove
  function Call-Tool([string]$tool, $arguments) {
    $r = Send-Rpc 'tools/call' @{ name = $tool; arguments = $arguments }
    if ($r.result.isError) { return [pscustomobject]@{ Ok = $false; Text = ($r.result.content | ForEach-Object { $_.text }) -join ' '; Data = $null } }
    [pscustomobject]@{ Ok = $true; Text = ''; Data = $r.result.structuredContent }
  }
  $c1 = Call-Tool 'get_capabilities' @{}
  Check 'MCP get_capabilities' ($c1.Ok -and $c1.Data.service -eq 'bambud') "mode=$($c1.Data.mode)"
  $c2 = Call-Tool 'inspect_environment' @{}
  Check 'MCP inspect_environment' ($c2.Ok -and $c2.Data.data_dir_writable -ne $false) $c2.Text
  $c3 = Call-Tool 'search_models' @{ limit = 5 }
  Check 'MCP search_models (catalogue de demonstration)' ($c3.Ok -and $c3.Data.total -ge 5) "total=$($c3.Data.total)"
  $c4 = Call-Tool 'list_printer_models' @{}
  Check 'MCP list_printer_models' ($c4.Ok) $c4.Text
  $c5 = Call-Tool 'list_printers' @{}
  Check 'MCP list_printers (vide, aucune imprimante reelle)' ($c5.Ok) $c5.Text
  try { $mcpIn.Close() } catch {}
  if (-not $mcp.WaitForExit(15000)) { try { & taskkill /T /F /PID $mcp.Id | Out-Null } catch {} }

  # --- 7. Sauvegarde a chaud, verification, restauration ---
  $arch = Join-Path $tmp 'smoke-backup.tar.gz'
  $b = Invoke-Tool 'bambud' @('backup', '--data-dir', $data, '--out', $arch)
  Check 'bambud backup (service en marche)' ($b.Code -eq 0 -and (Test-Path $arch)) $(if ($b.Code -ne 0) { $b.Err.Trim() } else { ((Get-Item $arch).Length / 1KB).ToString('N0') + ' Kio' })
  $va = Invoke-Tool 'bambud' @('verify', '--archive', $arch)
  Check 'bambud verify --archive' ($va.Code -eq 0) ($va.Out -split "`n" | Select-Object -Last 2 | Out-String).Trim()
  $newData = Join-Path $tmp 'restored'
  $rs = Invoke-Tool 'bambud' @('restore', '--from', $arch, '--data-dir', $newData)
  Check 'bambud restore (dossier neuf)' ($rs.Code -eq 0) $(if ($rs.Code -ne 0) { $rs.Err.Trim() })
  $vd = Invoke-Tool 'bambud' @('verify', '--data-dir', $newData)
  Check 'bambud verify --data-dir (restaure)' ($vd.Code -eq 0) ($vd.Out -split "`n" | Select-Object -Last 2 | Out-String).Trim()
  $again = Invoke-Tool 'bambud' @('restore', '--from', $arch, '--data-dir', $newData)
  Check 'restore refuse un dossier non vide' ($again.Code -ne 0)
  $lock = Invoke-Tool 'bambud' @('restore', '--from', $arch, '--data-dir', $data, '--force')
  Check 'restore refuse un service en marche' ($lock.Code -ne 0)

  # --- 8. Le service est toujours sain ---
  $still = (Invoke-WebRequest "$base/health" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200
  Check '/health apres tout cela' $still
}
catch {
  Check 'execution du test de fumee' $false $_.Exception.Message
}
finally {
  Stop-All
  Start-Sleep -Milliseconds 500
  $failed = @($results | Where-Object { -not $_.Ok })
  Write-Host ''
  Write-Host ("Resultat : {0} controle(s), {1} echec(s) - mode {2}" -f $results.Count, $failed.Count, $mode)
  if ($KeepData) { Write-Host "Donnees conservees : $tmp" } else { Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue }
  if ($failed.Count -gt 0) { exit 1 } else { exit 0 }
}
