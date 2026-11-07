# Ajoute l'entrée MCP « atelier-bambu-lab » (pont npx mcp-remote) à Claude Desktop.
# À lancer pendant que Claude Desktop est FERMÉ : l'application réécrit son fichier en quittant.
# Claude Desktop ne transmet pas les variables d'environnement de l'utilisateur au serveur MCP :
# le jeton est donc placé dans un fichier protégé, lu par cmd (set /p) au lancement, jamais écrit
# dans claude_desktop_config.json.
# -Server : URL MCP de votre bambud, p. ex. http://atelier.example.ts.net:7878/mcp (OBLIGATOIRE : aucune valeur par défaut).
param([Parameter(Mandatory = $true)][string]$Server)

$auth = [Environment]::GetEnvironmentVariable('BAMBU_AUTH', 'User')
if (-not $auth) {
  $t = Read-Host 'Jeton BAMBU_API_TOKEN (saisi, non affiché ailleurs)'
  $auth = "Bearer $t"; Remove-Variable t
}
$dir = Join-Path $env:LOCALAPPDATA 'BambuAtelier'
New-Item -ItemType Directory -Force $dir | Out-Null
$tokenFile = Join-Path $dir 'auth.txt'
[IO.File]::WriteAllText($tokenFile, $auth, (New-Object Text.UTF8Encoding($false)))
icacls $tokenFile /inheritance:r /grant:r "${env:USERDOMAIN}\${env:USERNAME}:(R,W)" | Out-Null
Remove-Variable auth
Write-Host "Jeton enregistré dans $tokenFile (droits restreints à votre compte)."

Write-Host 'Attente de la fermeture complète de Claude Desktop...'
while (Get-Process -Name 'Claude' -ErrorAction SilentlyContinue) { Start-Sleep -Seconds 1 }

$cfg = Join-Path $env:APPDATA 'Claude\claude_desktop_config.json'
Copy-Item $cfg "$cfg.bak-$(Get-Date -Format yyyyMMdd-HHmmss)"
$j = Get-Content $cfg -Raw -Encoding UTF8 | ConvertFrom-Json
if (-not $j.mcpServers) { $j | Add-Member -NotePropertyName mcpServers -NotePropertyValue ([pscustomobject]@{}) }
$inner = "set /p BAMBU_AUTH=<$tokenFile & npx -y mcp-remote $Server --allow-http --header Authorization:`${BAMBU_AUTH}"
$entry = [pscustomobject]@{ command = 'cmd'; args = @('/c', $inner) }
$j.mcpServers | Add-Member -NotePropertyName 'atelier-bambu-lab' -NotePropertyValue $entry -Force
[IO.File]::WriteAllText($cfg, ($j | ConvertTo-Json -Depth 50), (New-Object Text.UTF8Encoding($false)))
Write-Host 'Entree atelier-bambu-lab ajoutee. Relancez Claude Desktop.'
