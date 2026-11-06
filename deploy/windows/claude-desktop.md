# Brancher Claude Desktop sur l'Atelier

Claude Desktop lance un programme local et lui parle sur son entrée/sortie standard (MCP « stdio »). Ce programme, `bambu-mcp.exe`, est un **pont** : il relaie chaque message vers le service `bambud` du serveur Linux (`http://<serveur>:7878/mcp`, MCP Streamable HTTP) avec le jeton d'API en `Authorization: Bearer`. Il reconnecte tout seul si le serveur redémarre, et n'écrit **rien** d'autre que le protocole sur la sortie standard (ses journaux vont sur stderr).

## Méthode recommandée : le script

```powershell
powershell -ExecutionPolicy Bypass -File .\deploy\windows\configure-claude-desktop.ps1 -Server http://192.168.1.20:7878
```

Ce que fait le script (et seulement cela) :

1. copie `bambu-mcp.exe` dans `%LOCALAPPDATA%\BambuAtelier\` ;
2. écrit le jeton d'API dans `%LOCALAPPDATA%\BambuAtelier\api-token.txt` après avoir **retiré tous les droits sauf les vôtres** (`icacls /inheritance:r`) ;
3. **sauvegarde** `%APPDATA%\Claude\claude_desktop_config.json` en `claude_desktop_config.json.bak-AAAAMMJJ-hhmmss` ;
4. **ajoute ou met à jour** uniquement `mcpServers.bambu-atelier` ; vos autres serveurs MCP et réglages sont conservés tels quels. Si le fichier n'est pas un JSON valide, rien n'est modifié et le script s'arrête avec un message.

Options : `-DryRun` (affiche sans écrire), `-Remove` (retire l'entrée, avec sauvegarde), `-TokenFile`, `-BridgeSource`, `-ConfigPath` (autre fichier de configuration), `-Token` (sinon saisie masquée).

Le résultat dans `claude_desktop_config.json` :

```json
{
  "mcpServers": {
    "bambu-atelier": {
      "command": "C:\\Users\\VOUS\\AppData\\Local\\BambuAtelier\\bambu-mcp.exe",
      "args": [
        "--url", "http://192.168.1.20:7878/mcp",
        "--api-token-file", "C:\\Users\\VOUS\\AppData\\Local\\BambuAtelier\\api-token.txt"
      ]
    }
  }
}
```

Le jeton n'y figure pas. Ensuite : **Quitter** Claude Desktop depuis la zone de notification (fermer la fenêtre ne suffit pas), puis le relancer. L'outil `bambu-atelier` apparaît dans la liste des outils d'une nouvelle conversation.

## Méthode manuelle

Même contenu que ci-dessus, à fusionner à la main dans `%APPDATA%\Claude\claude_desktop_config.json` (menu *Paramètres → Développeur → Modifier la configuration*). Créez `api-token.txt` avec une seule ligne (le jeton), puis :

```powershell
$f = "$env:LOCALAPPDATA\BambuAtelier\api-token.txt"
icacls $f /inheritance:r /grant:r "${env:USERDOMAIN}\${env:USERNAME}:(R,W)"
```

Variantes acceptées par le pont : `--api-token <jeton>` ou la variable `BAMBU_API_TOKEN` (le jeton serait alors en clair dans la configuration : à éviter), `--api-token-file` ou `BAMBU_API_TOKEN_FILE` (première ligne du fichier, BOM et fins de ligne Windows tolérés), `--url` ou `BAMBU_URL`, `--connect-attempts N`, `--log-level debug`, `--embedded --data-dir <dossier>` (cœur local sans serveur : pour des essais sur un seul PC, avec un dossier de données **séparé** de celui du serveur).

## Vérifier

- Dans PowerShell, sans Claude : `& "$env:LOCALAPPDATA\BambuAtelier\bambu-mcp.exe" --version` affiche la version sur stderr.
- Journal du pont vu par Claude Desktop : `%APPDATA%\Claude\logs\mcp-server-bambu-atelier.log` (ligne « pont MCP prêt », nombre d'outils).
- Dans Claude : « Quel est l'état de l'Atelier ? » → `get_capabilities` (mode `linux_seul` ou `linux_renfort_windows`).

## Ce que Claude peut et ne peut pas faire avec ce pont

Il peut chercher, préparer, découper, comparer, déposer un fichier sur l'imprimante (sans la lancer) et **demander** une confirmation. Il ne peut **pas** approuver un lancement ni un arrêt : ces accords se donnent uniquement sur une page web (`/confirm/…`, `/stop/…`) depuis un navigateur que *vous* avez enregistré (`sudo atelier-admin enroll`). Voir `docs/SECURITY.md`.

## Mettre à jour ou retirer

Relancer le script avec le nouvel exe (`-BridgeSource`) met à jour le pont ; changer de jeton : `-Token` (le fichier est réécrit avec les mêmes droits). Retrait : `-Remove`, puis supprimer `%LOCALAPPDATA%\BambuAtelier\bambu-mcp.exe` et `api-token.txt`.
