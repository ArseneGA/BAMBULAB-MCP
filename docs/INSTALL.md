# Installation pas à pas

Ordre : **A.** serveur Linux → **B.** navigateur → **C.** sauvegardes → **D.** agent Windows (optionnel) → **E.** Claude Desktop. Le branchement de l'imprimante est un autre document : `docs/BRANCHEMENT.md`.

Conventions : `IP` = adresse du serveur (ex. `192.168.1.20`), idéalement **fixe** (réservation DHCP sur la box). Les blocs `bash` s'exécutent sur le serveur Zorin, les blocs `powershell` sur le PC Windows.

## 0. Prérequis

- Le serveur a été préparé avec le prompt de `docs/SETUP-LINUX-PROMPT.md` : utilisateur `atelier`, `/opt/atelier`, `/var/lib/atelier`, `/var/log/atelier`, `uv`, Bambu Studio (AppImage dans `/opt/atelier/bambustudio/`), `xvfb`, pare-feu autorisant le port 7878 depuis le réseau local seulement.
- Une machine avec **Go ≥ 1.26** pour construire la livraison (le PC Windows convient ; le serveur aussi, le prompt y installe Go).
- Côté imprimante : rien pour l'instant.

## A. Serveur Linux

### A1. Construire la livraison (sur le PC Windows)

```powershell
Set-Location C:\chemin\vers\BAMBULAB-MCP
.\scripts\release.ps1 -Version 0.9.0          # ajouter -SkipWeb si web\ n'est pas encore là
```

Résultat dans `dist\0.9.0\` : `bambud-linux-amd64|arm64`, `bambu-mcp-linux-*`, `printer-sim-linux-*`, `bambu-mcp-windows-amd64.exe`, `bambu-agent-windows-amd64.exe`, `atelier-cad-worker-0.9.0.tar.gz` (worker CAO sans `.venv`), `atelier-deploy-0.9.0.tar.gz` (scripts et docs) et `SHA256SUMS`. (Sur le serveur lui-même : `scripts/release.sh --version 0.9.0`.)

Le script lance `go vet` et `go test -short` ; `-SkipTests` les saute.

### A2. Copier sur le serveur

```powershell
scp -r .\dist\0.9.0 votre-utilisateur@IP:/tmp/atelier-release
```

### A3. Installer

```bash
ssh votre-utilisateur@IP
cd /tmp/atelier-release
sha256sum -c SHA256SUMS                       # tout doit afficher « OK »
mkdir -p src && tar -xzf atelier-deploy-0.9.0.tar.gz -C src
sudo bash ./src/deploy/linux/install.sh \
     --from /tmp/atelier-release \
     --public-url http://IP:7878 \
     --backup-dir /media/votre-utilisateur/DISQUE-EXTERNE/atelier-sauvegardes
```

Options utiles : `--studio /opt/atelier/bambustudio/Bambu_Studio_xxx.AppImage` (détecté automatiquement s'il est seul dans ce dossier), `--import-root /srv/modeles` (dossier que `import_files`/`scan_folder` ont le droit de lire ; répétable), `--listen 0.0.0.0:7878`, `--backup-keep 14`, `--backup-time 03:30`, `--no-cad`, `--no-start`, `--no-restart`. `--help` les liste toutes.

Le script est **idempotent** : le relancer met à jour les binaires et la configuration sans toucher aux données ni changer le jeton d'API. Il :

| Étape | Résultat |
|---|---|
| utilisateur et dossiers | `atelier` (système, sans connexion) ; `/opt/atelier/bin`, `/var/lib/atelier`, `/var/log/atelier`, `/etc/atelier` |
| binaires | `/opt/atelier/bin/bambud`, `bambu-mcp` (copie atomique), outil `atelier-admin` (lien `/usr/local/bin`) |
| worker CAO | `/opt/atelier/cad/geometry` + `uv sync --extra cad` sous l'utilisateur `atelier` (quelques minutes la première fois : télécharge Python 3.12 et CadQuery) |
| Bambu Studio | l'AppImage est **extraite une fois** dans `/opt/atelier/bambustudio/squashfs-root` (profils BBL à côté de `AppRun`) ; `BAMBU_STUDIO_BIN` pointe sur `AppRun` |
| écran virtuel | `xvfb-run` détecté → `BAMBU_STUDIO_WRAPPER="xvfb-run -a"` (posé **explicitement** par le script : bambud n'active plus jamais `xvfb-run` de lui-même ; `BAMBU_STUDIO_WRAPPER=none` ou vide = aucun préfixe) |
| `/etc/atelier/atelier.env` (600, root) | `BAMBU_DATA_DIR`, `BAMBU_ADDR`, `BAMBU_PUBLIC_URL`, `BAMBU_ALLOWED_HOSTS` (nom d'hôte, `.local`, IP), `BAMBU_STUDIO_*`, `BAMBU_UV_PATH`, `BAMBU_GEOMETRY_DIR`, **`BAMBU_API_TOKEN` généré une fois, jamais affiché** |
| service | `atelier.service` (durci : `NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`…), activé et démarré ; attend `/health` |
| sauvegarde | `atelier-backup.timer` (03:30 par défaut), rotation des `--backup-keep` dernières archives |

### A4. Vérifier

```bash
systemctl status atelier --no-pager
curl -s http://127.0.0.1:7878/health               # {"schema_version":…,"status":"ok","version":"0.9.0"}
journalctl -u atelier -n 30 --no-pager
```

Au démarrage, `journalctl` signale en avertissement un éventuel Bambu Studio introuvable. Un `BAMBU_STUDIO_BIN` (ou `--studio-bin`) qui désigne un fichier absent **empêche le démarrage** (message « Bambu Studio introuvable »), sans repli sur une autre installation. La découpe ne fonctionne que si `get_capabilities` annonce `slicing` disponible (voir E4).

Test de fumée complet (santé, Studio et profils chargés, CadQuery réellement importé, MCP `tools/list`, profils filament) :

```bash
sudo scripts/smoke-linux.sh --url http://127.0.0.1:7878 --token-file /etc/atelier/atelier.env   # le jeton est lu dans le fichier, jamais affiché
```

Options de sécurité utiles derrière un proxy (Funnel, `tailscale serve`, nginx) — **toujours un jeton d'API** : `BAMBU_REQUIRE_TOKEN=1`, `BAMBU_PUBLIC_URL` (une ou plusieurs URL séparées par des virgules ; la première sert aux liens), `BAMBU_ALLOWED_HOSTS`, `BAMBU_TRUSTED_PROXIES` (défaut : loopback). `BAMBU_IMPORT_ROOTS` ne doit pas contenir `BAMBU_DATA_DIR` ni s'y trouver. Détails : `docs/DEPLOY-LINUX.md` § 3 et § 7, `docs/SECURITY.md`.

## B. Enregistrer votre navigateur

L'accord de lancement d'une impression ne se donne que depuis un navigateur **enregistré**. Le code s'affiche sur *votre* terminal et n'est jamais transmis à Claude.

```bash
sudo atelier-admin enroll
# Code d'enregistrement : ABCD-EFGH   (usage unique, 10 minutes)
```

Ouvrez `http://IP:7878/enroll` dans le navigateur (PC, téléphone, tablette du réseau local) et saisissez le code. Répétez pour chaque appareil. **L'interface web (`http://IP:7878/`) a besoin de ce même enregistrement** : le jeton d'API reste sur le serveur, c'est le cookie du navigateur enregistré qui authentifie les appels de l'interface. Un navigateur inconnu voit l'écran « Enregistrer ce navigateur » qui mène à `/enroll`. Gestion : `sudo atelier-admin enroll --list`, `--revoke <id>`, `--revoke-all`.

> Le serveur est en HTTP clair sur le réseau local. Acceptable à la maison ; ne l'exposez pas sur Internet. Pour un accès hors LAN, placez un proxy HTTPS devant bambud (alors `--public-url https://…` rend le cookie `Secure`).

## C. Sauvegarde et restauration

La sauvegarde quotidienne est déjà en place (A3). Une archive contient une copie **cohérente** de la base (prise à chaud, sans arrêter le service), tous les blobs (vos fichiers 3D), le coffre chiffré des codes d'accès et sa clé, et un manifeste avec les empreintes SHA-256.

```bash
sudo systemctl list-timers atelier-backup.timer           # prochaine exécution
sudo systemctl start atelier-backup.service               # sauvegarde immédiate
journalctl -u atelier-backup -n 20 --no-pager
ls -l /media/votre-utilisateur/DISQUE-EXTERNE/atelier-sauvegardes
sudo atelier-admin verify                                  # contrôle du dossier de données (base + blobs)
sudo atelier-admin verify --archive /chemin/atelier-AAAAMMJJThhmmssZ.tar.gz
```

> **L'archive contient la clé du coffre et le coffre des codes d'accès.** Rangez-la sur un support à accès restreint (`--exclude-secrets` les omet : il faudra alors ressaisir les codes d'accès dans `/setup`). Une copie sur le serveur n'est pas une sauvegarde : utilisez un disque externe ou un NAS.

**Essai de restauration** (à faire une fois, sans toucher au service) :

```bash
sudo install -d -o atelier -g atelier -m 0700 /tmp/essai-restauration
sudo atelier-admin restore --from /chemin/atelier-AAAAMMJJThhmmssZ.tar.gz --data-dir /tmp/essai-restauration
sudo atelier-admin verify --data-dir /tmp/essai-restauration
sudo rm -rf /tmp/essai-restauration
```

**Restauration réelle** (disque mort, mauvaise manipulation) :

```bash
sudo systemctl stop atelier
sudo atelier-admin restore --from /chemin/atelier-AAAAMMJJThhmmssZ.tar.gz --data-dir /var/lib/atelier --force
sudo atelier-admin verify
sudo systemctl start atelier
```

`restore` refuse un dossier non vide sans `--force` ; avec `--force`, le contenu existant n'est **pas supprimé** mais déplacé dans `/var/lib/atelier/.pre-restore-<date>/`. Elle refuse aussi d'écraser les données d'un service en marche, vérifie toute l'archive (manifeste, empreintes, intégrité de la base) avant de modifier quoi que ce soit, et n'accepte aucun chemin hors de sa liste blanche. Les jetons d'agents, navigateurs enregistrés et travaux reviennent avec la base.

Changer le dossier, le nombre d'archives ou l'heure : relancez `install.sh` avec `--backup-dir`, `--backup-keep`, `--backup-time` (réglages dans `/etc/atelier/backup.env`).

## D. Agent Windows (renfort, optionnel)

À quoi il sert : importer vos dossiers 3D actuellement sur Windows (lecture seule, empreintes vérifiées, originaux jamais modifiés), découper en renfort, ouvrir un résultat dans Bambu Studio. Sans lui, tout le reste fonctionne.

1. **Sur le serveur**, créez un jeton d'appairage (usage unique, 15 minutes) :
   ```bash
   sudo atelier-admin agent pair
   # Jeton d'appairage (usage unique, valable jusqu'à 14:32:10) : pair-…
   ```
2. **Sur le PC Windows**, depuis le dossier de la livraison ou du dépôt :
   ```powershell
   Get-ChildItem -Recurse | Unblock-File                      # fichiers téléchargés ou copiés
   powershell -ExecutionPolicy Bypass -File .\deploy\windows\install-agent.ps1 `
       -Server http://IP:7878 -Folder "Modeles=D:\3D"
   ```
   Le script copie `bambu-agent.exe` dans `%LOCALAPPDATA%\BambuAtelier` (en vérifiant `SHA256SUMS`), demande le jeton (saisie masquée), appaire l'agent (secret protégé par DPAPI, jamais en clair), ajoute les dossiers autorisés et crée une tâche planifiée à l'ouverture de session. `-Source <chemin>` indique l'exe s'il n'est pas à côté ; `-Fingerprint <sha256>` épingle le certificat d'un proxy HTTPS.
3. **Contrôle** : `& "$env:LOCALAPPDATA\BambuAtelier\bambu-agent.exe" status` (doit afficher `"paired": true`) ; côté serveur, `get_capabilities` liste l'agent « en ligne ».
4. **Premier inventaire** (lecture seule) : `& "$env:LOCALAPPDATA\BambuAtelier\bambu-agent.exe" inventory --label Modeles`, puis demandez à Claude de sélectionner ce qu'il faut copier sur le serveur.

L'agent parle au serveur en HTTP clair : l'agent le signale. Sur un réseau domestique de confiance c'est acceptable ; sinon proxy HTTPS + `-Fingerprint`. Un service Windows est possible (`bambu-agent service install`, droits administrateur, appairer avec `--dpapi-scope machine`) mais la tâche à l'ouverture de session suffit.

Retrait : `Unregister-ScheduledTask -TaskName BambuAtelierAgent -Confirm:$false` puis supprimer `%LOCALAPPDATA%\BambuAtelier\bambu-agent.exe`. Pour révoquer l'agent côté serveur, voir `docs/jalons/J8.md`.

## E. Claude Desktop

Claude Desktop (stdio) ne parle pas HTTP : le pont `bambu-mcp.exe` relaie vers `http://IP:7878/mcp` avec le jeton d'API. Le jeton est lu dans un **fichier protégé**, pas écrit dans `claude_desktop_config.json`.

1. Lire le jeton d'API sur le serveur (il s'affiche sur votre terminal, ne le collez dans aucune conversation) :
   ```bash
   sudo grep BAMBU_API_TOKEN /etc/atelier/atelier.env
   ```
2. Sur le PC Windows :
   ```powershell
   powershell -ExecutionPolicy Bypass -File .\deploy\windows\configure-claude-desktop.ps1 -Server http://IP:7878
   ```
   (le jeton est demandé en saisie masquée). Le script : copie `bambu-mcp.exe` dans `%LOCALAPPDATA%\BambuAtelier`, écrit `api-token.txt` avec des droits limités à votre compte, **sauvegarde** `%APPDATA%\Claude\claude_desktop_config.json` (copie horodatée), puis **ajoute seulement** l'entrée `bambu-atelier` (vos autres serveurs MCP sont conservés ; un JSON invalide n'est jamais modifié). `-DryRun` montre le résultat sans rien écrire, `-Remove` retire l'entrée.
3. **Quittez complètement Claude Desktop** (icône de la zone de notification → Quitter) et relancez-le.
4. Testez : « Quel est l'état de l'Atelier ? » → Claude appelle `get_capabilities`. Détails et configuration manuelle : `deploy/windows/claude-desktop.md`.

Le PC Windows éteint, Claude Desktop n'est évidemment pas disponible : utilisez alors l'interface web depuis un autre appareil du réseau (voir `docs/BRANCHEMENT.md`, dernière étape).

## F. Mises à jour

```powershell
.\scripts\release.ps1 -Version 0.9.1 ; scp -r .\dist\0.9.1 votre-utilisateur@IP:/tmp/atelier-release-0.9.1
```
```bash
cd /tmp/atelier-release-0.9.1 && sha256sum -c SHA256SUMS
tar -xzf atelier-deploy-0.9.1.tar.gz && sudo bash ./deploy/linux/install.sh --from . --no-restart   # puis, hors impression :
sudo systemctl restart atelier
```
Le service vide ses travaux en cours à l'arrêt (30 s) et reprend au démarrage ; une impression déjà lancée sur l'imprimante n'est pas interrompue, mais le suivi est repris à la reconnexion. Une base plus récente que le binaire est refusée au démarrage (jamais de rétrogradation silencieuse). Agent et pont : relancez `install-agent.ps1` / `configure-claude-desktop.ps1` avec les nouveaux exe.

## G. Dépannage

| Symptôme | Cause probable et remède |
|---|---|
| `curl …/health` ne répond pas | `journalctl -u atelier -n 50` ; écoute hors loopback sans jeton refusée (le jeton est généré par `install.sh`) |
| « hôte non autorisé » (403) | l'adresse utilisée dans le navigateur n'est pas dans `BAMBU_ALLOWED_HOSTS` : l'ajouter dans `/etc/atelier/atelier.env` (ou `--allowed-hosts`) puis `sudo systemctl restart atelier` |
| `/enroll` refuse le code | code expiré (10 min), déjà utilisé, ou 6 échecs de votre adresse (verrou 5 min, doublé à chaque récidive ; un plafond global suspend brièvement tous les enrôlements après de très nombreux échecs) : relancer `sudo atelier-admin enroll` |
| L'interface web affiche « Enregistrer ce navigateur » | normal pour un navigateur inconnu : `sudo atelier-admin enroll`, puis le code sur `/enroll` |
| bambud refuse de démarrer : « jeton d'API obligatoire » | écoute hors loopback, `--require-token`, ou `BAMBU_PUBLIC_URL`/`BAMBU_ALLOWED_HOSTS` désignant un nom non loopback sans `BAMBU_API_TOKEN` (≥ 16 caractères) |
| bambud refuse de démarrer : racine d'import | `BAMBU_IMPORT_ROOTS` contient le dossier de données (ou s'y trouve) : choisir un dossier distinct |
| `get_capabilities` : `slicing` indisponible | Studio introuvable ou profils absents : vérifier `BAMBU_STUDIO_BIN` (doit être `…/squashfs-root/AppRun`), `ls /opt/atelier/bambustudio/squashfs-root/resources/profiles/BBL`, `which xvfb-run` ; `/api/capabilities` donne `studio` (chemin, version, profils chargés) |
| `cad_worker` indisponible | la note de `/api/capabilities` donne la cause (c'est le résultat d'un vrai `ping` du worker qui importe CadQuery) : `sudo -u atelier env HOME=/var/lib/atelier/home /opt/atelier/bin/uv sync --frozen --no-dev --extra cad` dans `/opt/atelier/cad/geometry` ; relancer `install.sh` |
| Claude Desktop n'affiche pas l'outil | Claude Desktop pas redémarré **complètement** ; JSON de config invalide ; serveur injoignable (`Test-NetConnection IP -Port 7878`) ; jeton erroné (journal du pont : `%APPDATA%\Claude\logs\mcp-server-bambu-atelier.log`) |
| `Accès refusé` à l'exécution d'un `.exe` compilé | politique du poste : utiliser `go run ./cmd/…` (c'est ce que fait `scripts/smoke.ps1` en repli) |
| Sauvegarde en échec « blob corrompu » | un fichier du magasin ne correspond plus à son empreinte : `sudo atelier-admin verify` le nomme ; restaurez ce blob depuis une sauvegarde saine |

## H. Désinstallation

```bash
sudo bash ./src/deploy/linux/uninstall.sh
```
Retire le service, le timer, `/opt/atelier/bin`, `/opt/atelier/cad`. **Ne supprime jamais** `/var/lib/atelier` (vos données), `/var/log/atelier`, les sauvegardes, `/etc/atelier` (jeton, réglages), l'utilisateur `atelier` ni Bambu Studio. Une réinstallation retrouve tout.
