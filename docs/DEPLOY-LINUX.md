# Déploiement de bambud sur le serveur Linux de l'atelier

Public : l'administrateur qui maintient `etape19-deploy.sh` et l'unité `atelier.service`.
Périmètre : ce document décrit ce que le CODE fait réellement (chaque affirmation non triviale cite `fichier:ligne`, chemins relatifs à la racine du dépôt) et le confronte à la configuration que le serveur applique déjà. Il ne contient aucun secret : uniquement des noms de variables et la façon de les fournir.

Convention : « gravité » = effet sur ce déploiement. Les mesures de durée et de mémoire ont été prises sur un poste **Windows 11** (pas sur 2 cœurs / 4 threads Linux) : ce sont des ordres de grandeur, pas des garanties ; les estimations non mesurées sont signalées comme telles.

---

## 1. Version à déployer et changements depuis `3d68962`

- Branche : `main`. Code à déployer : commit `96e8882` (« Merge : derniers écrans branchés… »). Le commit qui ajoute ce document ne change **que** `docs/DEPLOY-LINUX.md` : le code compilé est identique à `96e8882`.
- Il n'existe **aucun tag** git (`git tag` vide) : `git describe --always --tags --dirty` renvoie donc un simple hash court, ce qui sera la valeur de `core.Version` (défaut sans `-X` : `0.1.0-j1`, `internal/core/core.go:29`). Le suffixe `-dirty` n'apparaîtra pas après un build web : les artefacts web sont ignorés par git (voir § 2).
- 19 commits (`git log --oneline 3d68962..HEAD`) : 165 fichiers, +14 434 / −8 ; dont Go (`cmd`, `internal`) 30 fichiers +3 327, `web/` 61 fichiers +8 898, `docs/` 61 fichiers, `deploy/` + `scripts/` + `Makefile` + `README.md` + `.gitignore` 13 fichiers. `go.mod`, `go.sum`, `workers/` et `internal/store/migrations/` sont **inchangés**.

| Sujet | Changement depuis `3d68962` | Incompatible ? |
|---|---|---|
| Migrations SQLite | **Aucune ajoutée** (`internal/store/migrations/` identique : 0001, 0020, 0100, 0110, 0120, 0130 ; `/health` annonce `schema_version` 130) | Non : base du serveur actuel et nouveau binaire sont au même schéma, le retour arrière par simple remplacement du binaire fonctionne |
| Interface web | `GET /` sert désormais une SPA embarquée (`internal/httpapi/httpapi.go:87`, `internal/webui/webui.go`). Avant : `/` répondait 404. | Non, mais **inutilisable avec un jeton d'API** (§ 7 et « À corriger » n° 1) |
| Sous-commandes | Nouvelles : `bambud backup`, `bambud restore`, `bambud verify` (`cmd/bambud/main.go:69-74`, `cmd/bambud/backup_cli.go`) | Non (ajout) |
| Flags de `bambud` | Nouveaux : `--studio-bin` (`main.go:60`), `--studio-wrapper` / env `BAMBU_STUDIO_WRAPPER` (`main.go:61`). Aucun flag ni variable renommé ou supprimé. | Non |
| Comportement par défaut | Sous Linux, **sans DISPLAY ni WAYLAND_DISPLAY et si `xvfb-run` est dans le PATH**, Studio est lancé via `xvfb-run -a` (`cmd/bambud/backup_cli.go:187-202`). Ici (pas de xvfb) : aucun effet. | À neutraliser par `BAMBU_STUDIO_WRAPPER=none` |
| `bambu-mcp` | Nouveau : `--api-token-file` / `BAMBU_API_TOKEN_FILE` (`cmd/bambu-mcp/main.go:49,76-100`) | Non |
| Routes HTTP ajoutées | `GET /api/materials`, `GET /api/materials/{id}` (`internal/httpapi/materials.go`) ; `GET /api/experiments`, `GET /api/print-memory`, `GET /api/profiles/filaments` (`goals.go`) ; `POST /api/models/{id}/analyze`, `GET /api/models/{id}/parts`, `GET /api/files/{id}/download`, `POST /api/export` (`catalog.go:173-240`) | Non (ajout) ; toutes derrière le Bearer |
| `POST /api/agents/pairing` → 410 | **Déjà présent à `3d68962`** (`git show 3d68962:internal/httpapi/agent.go` ligne 33) ; non modifié depuis (`internal/httpapi/agent.go:128-130`) | Pas un changement de cette fenêtre |
| MCP | Vues « MCP App » `ui://bambu/*` et outil `get_mesh_chunk` (`internal/mcpserver/app_views.go`) ; elles nécessitent le bundle `internal/mcpserver/ui/views/mcp-app.html` (ignoré par git, voir § 2) | Non |
| Verrou d'instance | `core.LockDataDir` ajouté, utilisé par `bambud restore` (`internal/core/lock.go:94-102`) | Non |

`prototype/` n'a pas été touché (§ 9).

---

## 2. Build

### 2.1 L'interface web N'EST PAS versionnée

`.gitignore:14-17` ignore `/internal/webui/assets/*` et `/internal/mcpserver/ui/views/*` (sauf `.gitkeep`). Vérifié : `git check-ignore -v internal/webui/assets/index.html internal/mcpserver/ui/views/mcp-app.html` répond « ignoré ». Un `git clone` + `go build` produit donc un `bambud` qui compile (le `.gitkeep` suffit à `//go:embed all:assets`, `internal/webui/webui.go:17`) mais dont `GET /` répond **503 « Interface non construite »** (`webui.go:73-79`) et dont les vues MCP App sont absentes (`internal/mcpserver/app_views.go:61-69`). Aucune erreur de build ne l'annonce.

**Node est donc requis sur la machine de build** (uniquement au build, jamais à l'exécution).

- Version minimale : `^20.19.0 || >=22.12.0`, imposée par `vite@8.3.6` et les binaires `@rolldown/*` du lockfile (`web/package-lock.json`, champ `engines` de `node_modules/vite`) ; `react-router-dom` demande `>=20`. Recommandation : **Node 22 LTS (≥ 22.12)**. `web/package.json` ne déclare ni `engines` ni `.nvmrc`. Sur Ubuntu 22.04 le paquet `nodejs` d'apt est en 12.x : il faut une source externe (NodeSource, tarball officiel, fnm).
- Le lockfile contient les binaires natifs `linux-x64-gnu` (`@rolldown/binding-linux-x64-gnu`, `@rollup/rollup-linux-x64-gnu`, `lightningcss-linux-x64-gnu`, `@esbuild/linux-x64`) : `npm ci` fonctionne sur le serveur malgré un lockfile généré sous Windows. Le dépôt contient aussi `playwright` en devDependency : `npm ci` ne télécharge pas de navigateur.
- Réseau : `npm ci` va chercher les paquets sur le registre npm (140 paquets, ~15 s ici).

```bash
cd web
npm ci --no-audit --no-fund
npm run build
```

Le script exact (`web/package.json`, ligne `build`) est :
`tsc --noEmit && vite build && vite build --config mcp-app/vite.config.ts && node scripts/embed.mjs --skip-build`.
Il produit **les deux** bundles et les copie là où Go les embarque (`web/scripts/embed.mjs:7-14,36-45`) :

| Source | Destination (embarquée par `go:embed`) | Taille mesurée |
|---|---|---|
| `web/dist/` (SPA, `web/vite.config.ts`) | `internal/webui/assets/` (`index.html` + `assets/*.js|css`) | ~1,1 Mio |
| `web/mcp-app/dist/mcp-app.html` (fichier unique, `web/mcp-app/vite.config.ts`) | `internal/mcpserver/ui/views/mcp-app.html` | ~1 Mio (1023 Kio) |

Mesuré (poste Windows, Node 26) : `npm ci` 14,6 s, `npm run build` 6,6 s. Contrôle à mettre dans le script, avant `go build` :
`test -s internal/webui/assets/index.html && test -s internal/mcpserver/ui/views/mcp-app.html`.
Alternative sans Node sur le serveur : construire sur une machine qui en a un et copier ces deux emplacements avant `go build`.

### 2.2 Binaires

```bash
export CGO_ENABLED=0
go vet ./...
env -u BAMBU_STUDIO_BIN -u BAMBU_PROFILES_DIR -u BAMBU_TEST_REAL_WORKER TMPDIR="$(mktemp -d)" go test -short ./... -count=1   # voir § 2.3
V="$(git describe --always --tags --dirty)"
go build -trimpath -ldflags "-s -w -X github.com/ArseneGA/BAMBULAB-MCP/internal/core.Version=$V" -o bin/bambud ./cmd/bambud
go build -trimpath -ldflags "-s -w -X github.com/ArseneGA/BAMBULAB-MCP/internal/core.Version=$V" -o bin/bambu-mcp ./cmd/bambu-mcp
```

- `go.mod` exige `go 1.26.0` (`go.mod:3`) : Go 1.27.3 convient. Réseau nécessaire au premier build (`go mod download`) sauf cache de modules préchargé.
- Mesuré ici : `bambud` linux/amd64 ≈ 22 Mio (SPA incluse), build ≈ 3,5 s à cache chaud ; `GOOS=linux go vet ./...` (compile aussi les tests Linux) : propre, 12 s. Un build à froid sur 2 cœurs : **estimation** 1 à 3 minutes.
- `cmd/bambu-mcp` (pont stdio) ne sert pas sur le serveur : il est destiné aux postes qui font tourner Claude Desktop (§ 8). Le compiler pour Windows : `GOOS=windows GOARCH=amd64 go build … ./cmd/bambu-mcp` (et `./cmd/bambu-agent` pour l'agent, inutile en mode Linux seul).
- `cmd/printer-sim` (simulateur d'imprimante) n'est pas à déployer.
- Le dépôt fournit ses propres `scripts/release.sh` et `deploy/linux/install.sh` (même nomenclature `atelier`, `/opt/atelier`, `/var/lib/atelier`). `etape19-deploy.sh` les remplace ; ils restent une référence utile (unité systemd, `TimeoutStopSec=60`, `UV_CACHE_DIR`, `UV_PYTHON_INSTALL_DIR` : `deploy/linux/install.sh:262-263,306`).

### 2.3 Tests : lesquels exigent quoi, et comment les neutraliser

| Besoin | Tests concernés | Comment c'est détecté | Neutralisation |
|---|---|---|---|
| **Réseau externe** | Aucun. Les tests n'ouvrent que du loopback (`httptest`, simulateur d'imprimante MQTT/FTPS en mémoire sur ports éphémères) ; aucun port fixe (7878, 8883, 990) n'est lié par les tests. Seul `go build` (modules) et `npm ci` ont besoin d'Internet. | — | — |
| **Bambu Studio réel** | `TestReal*` (`internal/slicer/integration_test.go:34-42,122-298`, `integration2_test.go`), `internal/goals/helpers_test.go:56-71` (+ `goals_test.go:40-54`), `internal/printing/e2e_real_test.go:18-23`, `internal/agent/agent_test.go:512-516` | `slicer.Locate("")` : env **`BAMBU_STUDIO_BIN`**, puis `/opt/bambu-studio/bambu-studio`, `/usr/bin/bambu-studio`, `/usr/local/bin/bambu-studio`, `~/*.AppImage`, `~/Applications`, `~/.local/bin`, `/opt/[Bb]ambu[Ss]tudio*.AppImage`, puis PATH (`internal/slicer/binary.go:44-86`). L'`AppRun` de l'admin n'est dans aucune liste par défaut : seule la variable le désigne. | `-short` saute les tests de slicing réel. **Mais** les tests de profils (`internal/goals/helpers_test.go:36-48`, `goals_test.go:40-54`) lisent les profils du Studio localisé même avec `-short`, et `Locate` exécute `<studio> --help` (20 s max, `binary.go:92-103`). Donc : **ne pas définir `BAMBU_STUDIO_BIN` pendant le build** (`env -u BAMBU_STUDIO_BIN`) |
| Profils Studio | `internal/profiles/install_test.go:10-22` | env `BAMBU_PROFILES_DIR`, sinon chemin Windows `C:\Program Files\Bambu Studio\…` → saute sur Linux | `env -u BAMBU_PROFILES_DIR` |
| **uv / CadQuery réel** | `internal/catalog/step_test.go:172-182` | exige `BAMBU_TEST_REAL_WORKER=1` (sinon saute) | ne pas le définir |
| **Windows** | `internal/agent/unit_test.go:170-176`, `cmdline_test.go:17,39`, `slicer/unit_test.go:149`, `agent/dpapi_windows_test.go` (`//go:build windows`) | `runtime.GOOS` / build tags (`internal/agent/dpapi_*.go`, `studio_*.go`, `printing/secrets_*.go`, `slicer/proc_*.go`, `core/lock_*.go`, `catalog/diskfree_*.go`) | automatique sur Linux |
| **Imprimante physique** | Aucun : tout passe par le simulateur local (`internal/printer/sim`) | — | — |
| Divers | `internal/agent/unit_test.go:154-156` (`AGENT_REAL_3MF`), `internal/mesh/helpers_test.go:151` (`BAMBU_GEN_TESTDATA`) | variables absentes → saute | — |

**Risque si `BAMBU_STUDIO_BIN` est exporté pendant `go test` sur le serveur** (sans `-short`) : les `TestReal*` lancent le vrai Studio (durée mesurée ici avec Studio 02.08.02.61 : paquet `slicer` 25 s au lieu de 6 s). Les répertoires de travail des tests sont des `t.TempDir()` sous `$TMPDIR` (= `/tmp` du compte de build) et Studio y crée `bamboo_model` (constat de l'admin). Conséquences :

- Collision **avec le service : non**, tant que `atelier.service` garde `PrivateTmp=true` (son `/tmp` est distinct de celui du compte de build). Collision **entre comptes** qui utilisent le vrai `/tmp` (build, `sudo -u atelier` en manuel) : oui. Donc, pour tout lancement manuel de Studio ou de tests réels : `TMPDIR="$(mktemp -d)"` propre au lancement.
- Les tests réels ont été écrits contre Studio **02.08.02.61** (sortie de `TestRealP1SCube` : `studio 02.08.02.61`) ; le serveur a **02.09.01.70**. Un écart de noms de profils ou de clés pourrait faire échouer un test et, avec la règle « un échec bloque le déploiement », bloquer le déploiement pour une raison sans rapport avec le service. Recommandation : build avec `-short` et sans `BAMBU_STUDIO_BIN` ; qualifier Studio 02.09 **après** déploiement par les tests de fumée du § 10.

**Durées mesurées** (poste Windows 11, `-count=1`) :

| Commande | Durée | Remarque |
|---|---|---|
| `go test -short ./... -count=1` | **41 s** (cache de compilation chaud) ; 58 s au premier passage | plus lents : `printing` 36 s, `httpapi` 19 s, `goals` 18 s, `profiles` 18 s |
| `go test ./... -count=1` (complet) | **65 s** | ce poste a Studio 02.08.02.61 installé : les tests réels ont donc tourné (`slicer` 25 s) ; sans Studio ni worker le complet ≈ `-short` |
| `go vet ./...` | quelques secondes | — |

Estimation (non mesurée) pour 2 cœurs / 4 threads Linux : compter 2 à 4 minutes à froid pour vet + tests `-short`.

---

## 3. Configuration complète

Les flags de `bambud` acceptent `-flag` ou `--flag`, avec `--flag=valeur`. **Priorité : flag explicite > variable d'environnement > défaut** (la variable sert de valeur par défaut au flag, `cmd/bambud/main.go:47-61`). Il n'y a **pas** de sous-commande `serve` : `bambud` sans sous-commande sert ; `bambud serve --addr …` fonctionnerait par accident (l'argument positionnel est ignoré) mais n'est pas supporté.

### 3.1 `bambud` (service) — `cmd/bambud/main.go`

| Flag | Variable | Défaut | Oblig. ? | Effet |
|---|---|---|---|---|
| `--addr` | `BAMBU_ADDR` | `127.0.0.1:7878` | non | adresse d'écoute (l. 47). Hors loopback → jeton obligatoire (`internal/httpapi/guard.go:76-78`). `0.0.0.0` = IPv4 seulement. |
| `--data-dir` | `BAMBU_DATA_DIR` | `./data` (relatif au répertoire courant du processus !) | **oui en pratique** | racine des données (l. 48). Rendu absolu au démarrage (`internal/core/core.go:95`). |
| `--heavy-workers` | `BAMBU_HEAVY_WORKERS` | 1 | non | travaux lourds simultanés : découpe Studio, import STEP (`printing/slice.go:167`, `goals/experiment.go:308`, `catalog/derive.go:223`). Valeur ≤ 0 ou illisible → défaut (`main.go:31-36`). |
| `--light-workers` | `BAMBU_LIGHT_WORKERS` | 4 | non | travaux légers : analyse/aperçu de maillage, import, lancement (`catalog/derive.go:226-227`, `printing/start.go:98`). |
| `--api-token` | **`BAMBU_API_TOKEN`** | vide | **oui hors loopback** | jeton Bearer de l'API et de `/mcp`. Longueur mini 16 (`guard.go:46,73-75`). Refus de démarrer si écoute hors loopback sans jeton (`guard.go:76-78`, appelé `main.go:125`). Le jeton n'est jamais journalisé. |
| `--allowed-hosts` | `BAMBU_ALLOWED_HOSTS` | vide | non mais **indispensable** dès qu'on y accède par un autre nom que `localhost`/`127.x`/`::1` | liste séparée par des virgules, **noms d'hôte sans port**, insensible à la casse, espaces tolérés, crochets IPv6 retirés (`main.go:93-97`, `guard.go:85-91`). Pas de joker. Pas de point final (`nom.` est refusé : vérifié). L'hôte de `--public-url` est ajouté d'office (`guard.go:89-91`). |
| `--drain-timeout` | **`BAMBU_DRAIN_TIMEOUT`** (existe bien) | 30 s | non | durée Go (`45s`, `2m`) d'attente des travaux à l'arrêt (`main.go:53,153-155,196-198`). Invalide → 30 s. S'ajoute aux 15 s fixes d'arrêt HTTP (`main.go:186`). |
| `--log-level` | `BAMBU_LOG_LEVEL` | `info` | non | `debug|info|warn|error` ; valeur invalide → `info` silencieusement (`main.go:86-88`). |
| `--import-root` | `BAMBU_IMPORT_ROOTS` | vide | non | racines d'import autorisées, séparées par `:` sous Linux (`filepath.SplitList`, `cmd/bambud/seed.go:24`). Vide = aucun import de chemin arbitraire. NB : le dossier de réception de l'agent Windows `agent-inbox` est **toujours** ajouté comme racine (`internal/agentlink/registry.go:136-140`), donc `catalog_import.available` est vrai même sans cette variable. |
| `--uv-path` | `BAMBU_UV_PATH` | recherche `uv` dans PATH | non | exécutable `uv` du worker CAO ; doit exister (`internal/catalog/worker.go:23-25`). |
| `--geometry-dir` | `BAMBU_GEOMETRY_DIR` | `workers/geometry` relatif au cwd puis à côté du binaire | non | dossier du projet Python ; doit contenir `pyproject.toml` (`worker.go:26-46`). |
| `--public-url` | `BAMBU_PUBLIC_URL` | vide → adresse d'écoute (0.0.0.0 devient 127.0.0.1) | recommandé | base des liens `/confirm`, `/stop`, `/setup` remis au modèle (`main.go:169`, `cmd/bambud/cli.go:27-39`). Forme `http(s)://hôte[:port]` sans chemin ni requête, hôte non spécifié refusé (`cli.go:42-57`). `https://` → cookie `Secure` (`internal/httpapi/httpapi.go:71-73`). Son hôte est autorisé d'office par le garde Host. |
| `--allow-any-printer-host` | `BAMBU_ALLOW_ANY_PRINTER_HOST` (seule la valeur `1` active) | faux | non | lève la restriction « IP privée / lien local / nom `.local` » des hôtes d'imprimante (`internal/printing/service.go:402-417`). **Inutile ici** (P1S en 192.168.0.x). |
| `--studio-bin` | *(aucune ; la variable `BAMBU_STUDIO_BIN` est lue par `slicer.Locate`, `internal/slicer/binary.go:49`)* | détection (§ 2.3) | non | chemin de Studio. Un `--studio-bin` inexistant est une erreur franche (`core.go:132-137`) ; une `BAMBU_STUDIO_BIN` inexistante est **ignorée silencieusement** au profit des autres candidats (`binary.go:73-77`). |
| `--studio-wrapper` | `BAMBU_STUDIO_WRAPPER` | auto : voir ci-dessous | non | préfixe de commande du slicer. |
| `--version` | — | — | — | affiche `bambud <Version>` (`main.go:79-82`). |

**`BAMBU_STUDIO_WRAPPER`** (`backup_cli.go:176-202`) : valeur non vide → utilisée telle quelle (découpée sur les espaces) ; `none` ou `-` → désactivé ; **vide ou absente → défaut automatique** : Linux **et** `DISPLAY` vide **et** `WAYLAND_DISPLAY` vide **et** `xvfb-run` trouvé dans le PATH ⇒ `xvfb-run -a`, sinon aucun préfixe. Une variable posée à la chaîne vide revient donc à « auto », pas à « désactivé ». Ici, sans xvfb, aucun wrapper n'est appliqué ; mais installer `xvfb` un jour activerait silencieusement `xvfb-run` : **poser `BAMBU_STUDIO_WRAPPER=none`** pour figer le comportement constaté (« le slicing CLI marche sans affichage »).

**Variables lues ailleurs dans le code (hors flags)** :

| Variable | Lue où | Effet |
|---|---|---|
| `BAMBU_STUDIO_PATH`, `BAMBU_PYTHON` | `internal/core/core.go:346-347` | uniquement l'outil MCP `inspect_environment` ; **noms différents de `BAMBU_STUDIO_BIN`** → rapport « bambu-studio manquant » trompeur (« À corriger » n° 8) |
| `BAMBU_KEEP_WORK` | `internal/goals/slicejob.go:173` | non vide → conserve les dossiers de travail de découpe (débogage) |
| `DISPLAY`, `WAYLAND_DISPLAY` | `backup_cli.go:188` | détection du wrapper |
| `XDG_DATA_HOME` | `internal/core/datadir.go:25` | défaut de `--data-dir` du seul `bambu-mcp --embedded` |
| `LOCALAPPDATA`, `APPDATA` | `internal/agent/config.go:58`, `studio.go:246` | agent Windows seulement |
| `UV_*`, `TMPDIR`, `HOME`, `PATH` | héritées : `exec.Command` sans `Env` (`slicer.go:549-551`, `catalog/worker.go:80`) | pilotent `uv` et Studio (§ 3.4, § 5) |
| Tests | `BAMBU_PROFILES_DIR`, `BAMBU_TEST_WORKROOT`, `BAMBU_TEST_REAL_WORKER`, `BAMBU_GEN_TESTDATA`, `AGENT_REAL_3MF`, `FAKE_STUDIO` | jamais en production |

### 3.2 Sous-commandes de `bambud`

Elles s'exécutent sur le terminal de l'humain / un timer, ouvrent la base **directement** (SQLite WAL) et ne passent par aucune route HTTP. **`store.Open` applique les migrations** (`internal/store/store.go:81`) : voir § 4.4 avant de les lancer avec un binaire plus récent que le service en marche.

| Commande | Flags (défaut) | Fichier |
|---|---|---|
| `bambud enroll` | `--data-dir` (`BAMBU_DATA_DIR`, `./data`), `--addr` (`BAMBU_ADDR`, pour construire le lien affiché), `--public-url` (`BAMBU_PUBLIC_URL`), `--list`, `--revoke ID` (préfixe accepté), `--revoke-all` ; sans option : crée un code | `cmd/bambud/cli.go:66-141` |
| `bambud agent pair` | `--data-dir`, `--addr`, `--public-url` ; affiche un jeton `pair-…` à usage unique, 15 min | `cli.go:144-177`, `internal/agentlink/registry.go:59,192-201` |
| `bambud seed-demo` | `--data-dir`, `--dir` (défaut `<data>/demo-src`), `--no-cad`, `--uv-path`, `--geometry-dir`, `--wait` (2 min) ; **ouvre un `core` complet → refuse si le service tourne** (verrou). Injecte ~13 modèles de démonstration : **à ne pas lancer en production**. | `cmd/bambud/seed.go:38-73` |
| `bambud backup` | `--data-dir`, `--out` (obligatoire ; dossier existant ⇒ nom daté), `--exclude-secrets` ; à chaud, sans arrêter le service ; écrit `<out>.sha256` | `backup_cli.go:45-84` |
| `bambud restore` | `--from` (obligatoire), `--data-dir` (obligatoire), `--force` (le contenu actuel est déplacé dans `.pre-restore-<date>/`) ; **prend le verrou d'instance → refuse si le service tourne** | `backup_cli.go:87-124`, `internal/core/lock.go:94-102` |
| `bambud verify` | `--data-dir` ou `--archive`, `--quick`, `--json` ; code retour 1 si anomalie | `backup_cli.go:127-174` |

### 3.3 `bambu-mcp` (pont stdio, côté poste client) — `cmd/bambu-mcp/main.go:46-53`

`--url` (`BAMBU_URL`, défaut `http://127.0.0.1:7878/mcp`), `--api-token` (`BAMBU_API_TOKEN`), `--api-token-file` (`BAMBU_API_TOKEN_FILE` ; première ligne, BOM toléré, `main.go:76-100`), `--connect-attempts` (4), `--log-level` (`BAMBU_LOG_LEVEL`), `--embedded` + `--data-dir` (autre cœur local : **jamais** sur le dossier du service, le verrou l'interdirait), `--version` (sur stderr).

### 3.4 Verdict sur les valeurs posées par l'admin

| Valeur de l'admin | Verdict | Détail |
|---|---|---|
| `bambud --addr 0.0.0.0:7878` + `BAMBU_API_TOKEN` (64 hex, `EnvironmentFile` 600) | **OK** | jeton ≥ 16 car. ; `/etc/atelier/atelier.env` n'est lisible que par root, systemd le transmet au service. |
| `BAMBU_DATA_DIR=/var/lib/atelier` | OK | |
| `BAMBU_STUDIO_BIN=/opt/atelier/bambustudio/current/AppRun` | OK | `Locate` accepte tout fichier ; l'extension n'étant pas `.AppImage`, `--appimage-extract-and-run` n'est pas ajouté (`binary.go:79-81`) : c'est voulu. Les profils sont trouvés par `filepath.Dir(AppRun)/resources/profiles/BBL` (`binary.go:26-36`) : conforme à `…/current/resources/profiles/BBL`. Version lue par `AppRun --help` + regex `NN.NN.NN.NN` (`binary.go:88-103`) : `02.09.01.70` convient. |
| `BAMBU_UV_PATH=/var/lib/atelier/.local/bin/uv` | OK | doit être un fichier existant (`worker.go:23`). |
| `BAMBU_GEOMETRY_DIR=/opt/atelier/workers/geometry` | OK | projet en lecture seule : voir `UV_PROJECT_ENVIRONMENT` ci-dessous. |
| `UV_FROZEN=1` | **Indispensable mais insuffisant** | bambud lance `uv run --project <dir> python -m bambu_geometry <cmd>` — **sans** `--frozen`, `--no-dev` ni `--extra` (`internal/catalog/worker.go:80`). Sans `UV_FROZEN`, `uv run` revérifie/réécrit `uv.lock` dans le dossier du projet (en lecture seule → échec). |
| `UV_PROJECT_ENVIRONMENT` **manquante côté service** | **MANQUANT — bloquant pour la CAO** | l'admin ne la cite que pour le `uv sync`. Le service lance `uv run` : sans cette variable, uv vise `<projet>/.venv` = `/opt/atelier/workers/geometry/.venv`, que `ProtectSystem=strict` et la lecture seule empêchent de créer ⇒ chaque appel du worker échoue (`worker_failed`). **À ajouter à l'environnement du service**, avec la même valeur que pour le sync : `UV_PROJECT_ENVIRONMENT=/var/lib/atelier/venvs/geometry`. |
| `UV_NO_DEV=1` | **À AJOUTER** | vérifié (uv 0.11 ici) : après `uv sync --frozen --extra cad --no-dev`, un `uv run` ultérieur **réinstalle le groupe `dev` (pytest)** car `dev` est un groupe par défaut ; les extras déjà installés (ici `cad`) sont conservés (sync « inexact » par défaut de `uv run`). `UV_NO_DEV=1` évite cette installation à l'exécution (réseau, écriture dans le venv). `UV_NO_SYNC=1` est une alternative plus radicale (plus aucune synchronisation au `uv run`). |
| `--extra cad` | OK | sans lui, `step_import` répond `step_backend_missing` et `parametric` `cad_backend_missing` (`workers/geometry/pyproject.toml`). |
| `BAMBU_IMPORT_ROOTS=/var/lib/atelier/import` | OK mais surveillé | le dossier doit exister et appartenir à `atelier`. Aucune vérification n'empêche une racine qui contient le dossier de données (`internal/catalog/roots.go`) : ne jamais mettre `/var/lib/atelier` lui-même (« À corriger » n° 12). |
| `BAMBU_ALLOWED_HOSTS=atelier.tail4442b5.ts.net,atelier,lvg-thinkpad-x1-carbon-4th.local,100.65.215.71,192.168.0.8` | **OK** | format correct (sans port, virgules). Vérifié par sonde : `Host: atelier.tail4442b5.ts.net` avec ou sans `:7878` passe, en majuscules aussi, `…ts.net.` (point final) et tout hôte absent de la liste → 403 `hôte non autorisé`. Le nom court `atelier` suppose que les clients résolvent ce nom (MagicDNS). `127.0.0.1`/`localhost` sont toujours acceptés. |
| `BAMBU_PUBLIC_URL=http://atelier.tail4442b5.ts.net:7878` | OK, avec réserves | accepté par `validatePublicURL`. C'est l'URL que **l'humain** ouvre (liens `/confirm`, `/setup`, code d'enrôlement affiché). `http://` ⇒ cookie de session **non** `Secure`. Acceptable tant que l'accès passe par Tailscale (chiffré par WireGuard) ; **le code d'accès LAN de l'imprimante saisi sur `/setup` ne doit pas transiter par `http://192.168.0.8:7878`** (clair sur le LAN). |
| `HOME=/var/lib/atelier` | OK | héberge `~/.config/BambuStudio`, `~/.cache/uv`, `~/.local/share/uv/python` (§ 5.2) ; inscriptible, donc compatible `ProtectSystem=strict`. |
| `BAMBU_STUDIO_WRAPPER` absente | **À poser à `none`** | voir ci-dessus. |
| `BAMBU_DRAIN_TIMEOUT` absente | OK (30 s) | existe ; garder `TimeoutStopSec` ≥ 60 s dans l'unité (15 s d'arrêt HTTP + 30 s de drain = 45 s). |
| `BAMBU_ALLOW_ANY_PRINTER_HOST` absente | OK | |
| Unité : `Restart=` non précisé | À préciser | `Restart=on-failure`, `RestartSec=5`, `StartLimitIntervalSec=300`, `StartLimitBurst=5` : sinon un refus de démarrer (base plus récente que le binaire, § 10.5) boucle indéfiniment. |

Environnement minimal recommandé pour l'unité (à fusionner avec l'existant) :
`BAMBU_STUDIO_WRAPPER=none`, `UV_PROJECT_ENVIRONMENT=/var/lib/atelier/venvs/geometry`, `UV_NO_DEV=1` ; optionnel : `BAMBU_LOG_LEVEL=info`, `BAMBU_DRAIN_TIMEOUT=30s`.

---

## 4. Données

### 4.1 Arborescence réellement créée sous `BAMBU_DATA_DIR` (`/var/lib/atelier`)

| Chemin | Créé par | Contenu | À sauvegarder ? |
|---|---|---|---|
| `bambu.db` | `core.go:108` | base SQLite : catalogue, versions, objectifs, expériences, imprimantes, impressions, sessions humaines, agents, audit | **OUI** (via snapshot cohérent) |
| `bambu.db-wal`, `bambu.db-shm` | SQLite, `journal_mode(WAL)` (`store.go:64`) | journal et mémoire partagée de la base **vivante** | **NON** (jamais copiés à chaud) |
| `.bambud.lock` | `internal/core/lock.go:78` | verrou d'instance (flock) | non |
| `blobs/<aa>/<bb>/<sha256>` + `blobs/tmp/` | `core.go:113`, `internal/store/blobs.go:60,83,102` | fichiers d'origine et dérivés adressés par contenu, en lecture seule (0444 à la restauration) | **OUI** (`blobs/tmp/` non) |
| `secrets/printers.secrets` | `internal/printing/secrets.go:80` | codes d'accès LAN, JSON chiffré AES-256-GCM, un par imprimante | **OUI** |
| `secrets/master.key` | `secrets.go:60,243-261` | clé maître AES : **32 octets bruts, en clair** (mode 0600) sous Linux (`internal/printing/secrets_other.go:9`) ; DPAPI seulement sous Windows | **OUI**, avec le fichier précédent |
| `assets.key` | `internal/catalog/catalog.go:152`, `assets.go:20-35` | clé HMAC de signature des URL d'aperçu | oui (sa perte invalide seulement les URL déjà émises) |
| `work/slicer/`, `work/goals/`, `work/printing/` | `core.go:150,155`, `printing/slice.go:327` | dossiers de travail de découpe (nettoyés après chaque travail sauf `BAMBU_KEEP_WORK`, `goals/slicejob.go:173`, `printing/slice.go:248`) | non (jetable) |
| `tmp/step-*` | `catalog/derive.go:279` | scratch d'import STEP | non |
| `agent-inbox/`, `agent-staging/<agent>/<sha>.part` | `internal/agentlink/registry.go:129`, `transfer.go:62` | réception des fichiers de l'agent Windows ; `.part` abandonnés purgés après 24 h (`agentlink/formats.go:20`, `registry.go:547-565`) | non (transitoire) |
| `demo-src/` | `bambud seed-demo` | maillages de démonstration | non |
| `.restore-*`, `.pre-restore-<date>/` | `bambud restore` | zone de travail / sauvegarde de l'existant | non |

Hors du dossier de données mais sous `HOME=/var/lib/atelier` : `.config/BambuStudio` (Studio), `.cache/uv`, `.local/…` (uv, Python géré), `venvs/geometry`, `import/`. Les exclusions de restic de l'admin (`.cache`, `.local`, `venvs`) sont cohérentes ; **`.config/BambuStudio`** n'est pas exclu : sans conséquence mais jetable.

### 4.2 La base est-elle couverte par la sauvegarde de l'admin ?

- Nom exact : **`bambu.db`** (`.db`) → couvert par le motif `*.db`. Aucune autre base SQLite n'est créée.
- **Piège** : `bambu.db-wal` et `bambu.db-shm` ne correspondent à aucun des motifs `*.db`, `*.sqlite`, `*.sqlite3`. Si restic sauvegarde le dossier vivant **et** une copie `.backup`, il copiera aussi ces deux fichiers bruts, incohérents avec la copie : à **exclure explicitement** (`--exclude '*.db-wal' --exclude '*.db-shm'`) et à ne jamais restaurer. `sqlite3 .backup` exécuté par un autre compte que `atelier` peut créer ou verrouiller ces fichiers : l'exécuter en tant que `atelier` (le script de sauvegarde du dépôt note que SQLite en mode WAL a besoin d'écrire le `-shm` voisin, `deploy/linux/install.sh:373`).
- À exclure aussi : `work/`, `tmp/`, `blobs/tmp/`, `agent-staging/`, `.bambud.lock`, `demo-src/`, `.restore-*`.
- **Cohérence base + blobs + secrets** : l'ordre doit être *base d'abord, blobs ensuite* (un blob ajouté entre les deux est un orphelin inoffensif ; l'ordre inverse peut référencer un blob absent : `internal/backup/backup.go:11-13`). Les secrets (`secrets/` + `assets.key`) doivent partir dans le **même instantané** que la base : une base restaurée sans `master.key` correspondant rend les codes d'accès illisibles (« entrée du coffre illisible (clé maître changée ?) », `secrets.go:161`) — il faudrait les ressaisir sur `/setup` (l'épinglage du certificat, lui, est en base).
- Alternative recommandée, qui fait tout cela en une commande et le vérifie : **`bambud backup --data-dir /var/lib/atelier --out <dossier>`** (VACUUM INTO sur connexion séparée, le service continue ; manifeste SHA-256 ; `internal/backup/backup.go:1-14,41-75`) suivi de **`bambud verify --archive <fichier>`**, puis restic sur le dossier d'archives. À lancer en tant que `atelier`, avec `BAMBU_DATA_DIR`. L'archive contient la clé du coffre : le dépôt restic doit être chiffré (il l'est) ; `--exclude-secrets` produit une archive sans codes d'accès.

### 4.3 Migrations automatiques

Au démarrage, `core.Open` → `store.Open` → `migrate` (`store.go:143-176`) applique dans l'ordre, **chacune dans sa transaction**, les fichiers `internal/store/migrations/NNNN_*.sql` embarqués : **0001, 0020, 0100, 0110, 0120, 0130** (six fichiers, numérotation non contiguë). Il n'y a pas de migration descendante. Une base plus récente que le binaire est **refusée** : `la base est en version N, plus récente que ce binaire (M) : mise à jour du service nécessaire` (`store.go:156-158`) → le processus sort en erreur (code 1). Cette version : schéma 130 avant et après la mise à jour.

### 4.4 Piège des commandes humaines pendant une mise à jour

`bambud enroll`, `agent pair`, `backup`… ouvrent la base avec **le binaire qui les exécute** et la migrent (`store.go:81`, `cli.go:59-64`). Pour une mise à jour qui ajouterait une migration : ne pas lancer `atelier-enroll`/`atelier-backup` avec le nouveau binaire tant que l'ancien service tourne ; à l'inverse, un ancien binaire refuse une base migrée. Pour la mise à jour actuelle (aucune migration) : sans objet.

### 4.5 Restauration

1. Arrêter le service (`systemctl stop atelier`) — `bambud restore` prend le verrou d'instance et refuse sinon (`backup_cli.go:104-110`).
2. Avec une archive `bambud backup` : `bambud restore --from X.tar.gz --data-dir /var/lib/atelier [--force]` (en tant que `atelier`). Dossier non vide sans `--force` → refus ; avec `--force` le contenu actuel de `bambu.db*`, `blobs`, `secrets`, `assets.key` est **déplacé** (pas supprimé) dans `.pre-restore-<date>/` (`backup.go:765-826`). L'archive est entièrement vérifiée avant toute modification ; une base plus récente que le binaire est refusée (`backup.go:809-811`).
3. `bambud verify --data-dir /var/lib/atelier` (lit aussi les blobs, `--quick` pour la présence seule), puis démarrer le service.
4. Avec restic seul : restaurer dans un dossier neuf, **supprimer tout `bambu.db-wal`/`-shm` restant**, vérifier avec `bambud verify`, puis permuter ; restaurer base, blobs et secrets du **même** instantané.

---

## 5. Besoins d'exécution

### 5.1 Réseau sortant

| Destination | Port | Usage | Source |
|---|---|---|---|
| Imprimante | **8883/tcp** (MQTT sur TLS, utilisateur `bblp` + code d'accès) | état et commandes | `internal/printer/bambulan.go:73-75,305` |
| Imprimante | **990/tcp** (FTPS **implicite**) **+ ports de données passifs** choisis par l'imprimante (non configurables dans le code ; pas de plage à ouvrir en sortant puisque « sortant tout autorisé ») | dépôt du fichier à imprimer | `bambulan.go:76-78`, `internal/printer/upload.go:45-60` |
| Imprimante | 8883 également pour **lire l'empreinte du certificat** sur `/setup` (le serveur ouvre une connexion TLS vers `host:8883`, 8 s de délai) | page de configuration | `internal/printing/pages.go:89-107` |
| Caméra (6000/tcp) | **Aucun code** n'y accède (aucune occurrence caméra/RTSP/6000 dans `internal/printer`, `internal/printing`) ; `capture_preview` est un aperçu de modèle, pas la caméra | — | — |
| Internet | 443 | `go mod`, `npm ci`, téléchargements de `uv`/CPython/paquets PyPI (CadQuery) **à l'installation** ; à l'exécution : rien (sauf si `uv run` doit installer un groupe manquant, d'où `UV_NO_DEV=1`) | — |

Les imprimantes sont adressées par IP privée ou nom `.local` (résolution mDNS : nécessite `avahi`/`nss-mdns` sur le serveur ; préférer l'IP avec réservation DHCP sur la Freebox). Les ports d'imprimante sont personnalisables à l'ajout (`mqtt_port`, `ftp_port`, `internal/printing/service.go:389-390`). **Aucune imprimante réelle n'a été contactée par ce code** (`internal/core/core.go:300` : « validé contre le simulateur seulement ») : le FTPS implicite avec reprise de session TLS (`upload.go:17-20`, `bambulan.go:188-190`) est le point à valider en premier sur la P1S.

### 5.2 Écritures hors `BAMBU_DATA_DIR`, affichage, sous-processus

- **Aucun besoin d'affichage** côté bambud. Studio : le constat de l'admin (slicing CLI sans écran, sans miniatures `plate_N.png`) est compatible avec le code : le code ne dépend pas des miniatures et lance Studio sans xvfb si le wrapper est vide (`slicer.go:543-551`).
- Sous-processus lancés : `AppRun` (Studio) avec `cmd.Dir` = dossier de travail du job, répertoire de processus propre (`Setpgid`, `slicer/proc_unix.go:11`) tué avec son arbre à l'annulation/délai (`slicer.go:553-560`), délai par défaut **10 min** (`slicer.go:63-65`) ; `AppRun --help` à chaque détection de version (20 s max, `binary.go:96-98`) — au démarrage de bambud (`core.go:139`) puis à la première découpe (`slicer.go:49`) ; `uv run --project … python -m bambu_geometry <cmd>` (délai 10 min par import STEP, `catalog/catalog.go:79-80`).
- Écritures de Studio (programme fermé, non lisible dans ce dépôt) : `$HOME/.config/BambuStudio` et, d'après le constat de l'admin, `$TMPDIR/bamboo_model`. Avec `HOME=/var/lib/atelier` et `PrivateTmp`, tout reste dans les chemins inscriptibles de l'unité. À contrôler au premier slicing : `find /var/lib/atelier -newer <repère> -not -path '*/blobs/*' | head`.
- uv : cache `~/.cache/uv`, Python géré `~/.local/share/uv/python` (ou `UV_PYTHON_INSTALL_DIR`), environnement `UV_PROJECT_ENVIRONMENT`. Tous sous `/var/lib/atelier`. Le projet `/opt/atelier/workers/geometry` n'est **que lu**… à condition que `UV_FROZEN=1` et `UV_PROJECT_ENVIRONMENT` soient posés (§ 3.4).
- Journaux : stderr uniquement (pas de fichier) → journald ; `/var/log/atelier` n'est pas utilisé par bambud.

### 5.3 Charge typique et concurrence (2 cœurs / 4 threads, 7,6 Gio)

Mesures sur le poste Windows (ordre de grandeur) :

| Travail | Durée | Mémoire |
|---|---|---|
| Découpe d'un cube par Studio 02.08.02.61 (P1S 0,4, 100 couches) : `TestRealP1SCube`, durée Studio 577 ms | < 1 s | pic ≈ 95 Mio de working set (échantillonné toutes les 100 ms : plancher) |
| Analyse + aperçu de maillage (Go, in-process) : 70 000 triangles / STL 3 Mio | analyse 75 ms, GLB 4 ms | ≈ 70 Mio alloués |
| 270 000 triangles / 13 Mio | analyse 0,45 s | ≈ 360 Mio alloués, ≈ 130 Mio de tas |
| 1 000 000 triangles / 48 Mio | analyse 4,4 s, GLB 0,1 s | ≈ 1,4 Gio alloués, ≈ 490 Mio de tas |

Une vraie pièce se découpe en dizaines de secondes à quelques minutes et plusieurs centaines de Mio à ~1 Gio (**estimation**, Studio 02.09 sous Linux non mesuré) ; un import STEP par CadQuery est le plus gourmand (**estimation** 1 à 2 Gio pour un gros assemblage).

Concurrence : deux classes de travaux. **lourd** (découpe Studio, import STEP) défaut **1** ; **léger** (analyse/aperçu, import, lancement) défaut **4** (`internal/jobs/queue.go:45-50`, flags `main.go:49-50`). Sur 2 cœurs/4 threads, **garder `1` lourd** (Studio est lui-même multi-thread) ; `4` légers tient largement dans 7,6 Gio (pire cas 4 × 0,5 Gio + 1 découpe + 1 import STEP < 5 Gio, estimation). Réduire à `2` légers si le serveur sert aussi d'autres charges. Les travaux lourds en trop attendent en file (persistante, reprise au démarrage : `core.go:174`).

---

## 6. Imprimante

### 6.1 Ajout

Trois façons équivalentes de créer l'enregistrement (aucune ne contient ni le code d'accès ni l'empreinte) :

1. Outil MCP **`add_printer`** (`internal/mcpserver/print_tools.go:98-113`) — voie normale, appelée par Claude.
2. API HTTP **`POST /api/printers`** (Bearer ; `internal/httpapi/printing.go:45-52`), corps = `AddPrinterInput` : `name`, `model_id` (liste via `list_printer_models` / `GET /api/printer-models`, ex. `p1s`), `host`, `serial`, `nozzle_mm` (0,4), `ams_units` (0-4), `mqtt_port`/`ftp_port` optionnels (`internal/printing/service.go:381-391`).
3. L'écran « Impression » de l'interface web — **inutilisable avec le jeton actuel** (« À corriger » n° 1).

Validations (`service.go:394-417,423-438`) : `host` = IP privée / lien local ou nom `.local` (loopback et IP publiques refusés) ; `serial` = 6 à 32 alphanumériques ; buse et AMS bornés. La réponse contient `setup_url` = `{BAMBU_PUBLIC_URL}/setup/printer/{id}` (`service.go:571`, base fixée `main.go:169`). Tant que la page n'est pas validée, **aucune connexion** à l'imprimante n'est tentée.

### 6.2 Qui saisit quoi

| Information | Qui | Où | Stockage |
|---|---|---|---|
| Nom, modèle, **IP privée**, **numéro de série**, buse, AMS | Claude (ou l'admin par l'API) | `add_printer` / `POST /api/printers` | table imprimantes de `bambu.db` |
| **Code d'accès LAN** (affiché sur l'écran de l'imprimante) | **L'humain, seul**, dans un navigateur **enregistré** | formulaire `/setup/printer/{id}` (`internal/printing/pages.go:38,143-170`) | `secrets/printers.secrets`, chiffré AES-256-GCM, la clé d'imprimante sert de donnée authentifiée ; clé maître `secrets/master.key` (`secrets.go:39-76,124-141`) |
| **Empreinte SHA-256 du certificat** de l'imprimante | Lue par le **serveur** (`CertFetch` vers `host:8883`), **affichée** à l'humain avec le CN et le numéro de série attendu ; avertissement si le CN ≠ série ou si le certificat a changé | même page | épinglée en base ; l'empreinte épinglée est exactement celle affichée (liée au nonce, `pages.go:128-148`) |

Préalable : un navigateur enregistré → `sudo atelier-enroll` affiche un code de 8 caractères, usage unique, 10 min (`humanauth.go:36-47`) ; on le saisit sur `{BAMBU_PUBLIC_URL}/enroll` ; cookie `bambu_session`, 30 jours, `HttpOnly`, `SameSite=Strict` (`humanauth.go:329-336`).

Nature de la vérification : c'est de la **confiance à la première utilisation** sur le LAN. La P1S n'affiche pas d'empreinte sur son écran ; le seul recoupement disponible est « CN du certificat = numéro de série saisi » (`pages.go:100-103`). Ne faire cette étape que depuis le réseau de confiance.

### 6.3 Garantie que le code d'accès ne passe jamais par Claude

- Aucun champ de ce nom dans les schémas d'outils ou de routes : `AddPrinterInput` n'en a pas (`service.go:381-391`) ; le seul appelant de `PinAndSetAccessCode` est le gestionnaire du formulaire HTML (`pages.go:147`), derrière le cookie de navigateur enregistré et un nonce à usage unique (`pages.go:76-77`, `internal/httpapi/printing.go:36`).
- La file de travaux refuse tout paramètre nommé `secret_*`, `password`, `access_code` (`internal/jobs/queue.go:24`, `internal/contracts/secrets.go:10-18`).
- Aucune réponse d'API/MCP ne renvoie le code (vues `PrinterView` sans champ secret ; note de l'outil : « Le code d'accès LAN n'apparaît jamais ici ») ; l'audit consigne l'action sans la valeur (`service.go:627`) ; l'adaptateur imprimante masque la valeur à l'affichage (`printing/secrets.go:226-235`).
- La seule fuite possible est humaine (le taper dans la conversation) ; l'outil le déconseille explicitement. Sous Linux la clé maître est un fichier 0600 : protéger `/var/lib/atelier` (disque chiffré, accès `atelier` + root) — le service journalise un avertissement à chaque démarrage (`secrets.go:72-74`).

---

## 7. Sécurité et accès

### 7.1 Table des routes (`internal/httpapi/guard.go`, `httpapi.go`)

Toutes les routes sauf `/agent/v1/*` passent d'abord par le **contrôle de l'en-tête Host** (loopback ou liste blanche, `guard.go:109-117`), puis la **protection cross-origin** de Go (`http.CrossOriginProtection`, `guard.go:92,100`), puis le jeton.

| Famille | Routes | Authentification | Source |
|---|---|---|---|
| **Publique (Host contrôlé seulement)** | `GET /health` (renvoie `status`, `version`, `schema_version`) | aucune ; **seul `GET`** est exempté : `HEAD /health` → 401 | `guard.go:118`, `httpapi.go:29-36` |
| **Jeton d'API (Bearer)** | `/mcp` ; tout `/api/*` (catalogue, objectifs, impressions, imprimantes, agents, export, téléchargement, aperçus, `/api/capabilities`…) ; **et la SPA `GET /`, `/assets/*`, `/library`… (repli HTML)** | `Authorization: Bearer <BAMBU_API_TOKEN>`, comparaison en temps constant | `guard.go:118-126` |
| **Humaines (cookie de navigateur enregistré)** | `GET/POST /enroll` (public mais limité : voir ci-dessous), `POST /enroll/logout`, `GET /api/session` (sans secret), `GET/POST /confirm/{id}`, `/stop/{id}`, `/setup/printer/{id}`, `GET/POST /agents/pair` | exemptées du jeton (`isHumanPath`, `guard.go:35-43`) ; `Require` impose le cookie (sinon 303 vers `/enroll?next=`), et pour les POST `Sec-Fetch-Site: same-origin` + `Sec-Fetch-Mode: navigate` + nonce de formulaire (`humanauth/web.go:71-100`) | |
| **Agent Windows** | `POST /agent/v1/pair` (jeton `pair-…` à usage unique, 128 bits), `/agent/v1/hello|heartbeat|tasks|inventory|uploads|blobs…` | secret d'agent propre ; **ni contrôle Host, ni CSRF, ni jeton d'API** (`guard.go:105-108`) ; pas de limitation de débit | `agentlink/server.go:22-134` |
| Retirée | `POST /api/agents/pairing` | répond 410 | `agent.go:128-130` |

Sondes réalisées sur un `bambud` de test (jeton, `AllowedHosts` du serveur, `PublicURL` du serveur) : `GET /` sans jeton → **401** ; `GET /` avec jeton → 200 ; `/api/capabilities` sans jeton → 401, avec → 200 ; `POST /mcp` sans jeton → 401 ; `/enroll`, `/api/session` → 200 sans jeton ; `/confirm/x`, `/agents/pair` sans cookie → 303 ; `/agent/v1/hello` avec `Host: evil.example` → 401 (et non 403) ; `HEAD /health` → 401.

### 7.2 Cas Funnel / reverse proxy dont le `RemoteAddr` est loopback

**Ce que le code fait de `RemoteAddr` : rien.** Aucune occurrence dans `internal/` ou `cmd/` hors tests (`grep RemoteAddr` : néant) ; aucun en-tête `X-Forwarded-*` / `Forwarded` / `X-Real-IP` n'est lu (vérifié par sonde : un `X-Forwarded-Host` ne modifie pas la décision, seul `Host` compte). Une requête Funnel qui arrive en loopback **ne reçoit donc aucun privilège particulier** de bambud, et un proxy n'a rien à usurper.

Les décisions de sécurité reposent sur quatre choses :

1. **L'adresse d'écoute, une seule fois, au démarrage** : jeton obligatoire si l'écoute n'est pas loopback (`guard.go:76-78`). **Le contraire n'est pas vérifié** : si bambud écoute sur `127.0.0.1` (cas typique d'un Funnel ou d'un `tailscale serve` qui vise `127.0.0.1:7878`) **sans** `BAMBU_API_TOKEN`, le jeton n'est pas exigé alors que `BAMBU_ALLOWED_HOSTS`/`BAMBU_PUBLIC_URL` ouvrent le nom public : `/mcp` et toute l'API deviendraient publiques sans authentification (« À corriger » n° 3). Avec la configuration actuelle (0.0.0.0 + jeton) le risque n'existe pas ; il suffit d'enlever le jeton « parce que c'est en loopback » pour le créer.
2. **L'en-tête `Host`** (`guard.go:109-117`) : le nom du Funnel (`atelier.tail4442b5.ts.net`, sans port, port 443) est dans la liste → accepté. Le proxy doit **conserver** le `Host` d'origine : la protection CSRF compare `Origin` à `Host`.
3. **L'adresse locale de la connexion, côté SDK MCP** (seule décision liée à l'adresse « loopback », hors de notre code) : `go-sdk@v1.8.0/mcp/streamable.go:317-326` renvoie **403 `Forbidden: invalid Host header`** sur `/mcp` quand la connexion arrive sur une adresse locale loopback **avec un `Host` non loopback**. Vérifié par sonde. Conséquence : un Funnel / `tailscale serve` / nginx qui se connecte à `127.0.0.1:7878` en gardant le `Host` public **casse `/mcp`** (l'API `/api/*` et les pages humaines continuent de répondre). `StreamableHTTPOptions.DisableLocalhostProtection` n'est pas positionné (`httpapi.go:83-85`, « À corriger » n° 2). Contournements côté déploiement : faire viser au proxy une adresse **non loopback** du serveur (`192.168.0.8:7878` ou l'IP Tailscale) ; ou réécrire le `Host` en `127.0.0.1:7878` (impossible avec `tailscale serve`/Funnel). À noter aussi : un client local qui utilise le nom de la machine (`atelier` → 127.0.1.1 sous Ubuntu) subit la même règle.
4. **Le cookie `Secure`** se déduit uniquement de `r.TLS != nil` ou de `BAMBU_PUBLIC_URL` en `https://` (`httpapi.go:71-73`, `humanauth.go:329-332`) ; `X-Forwarded-Proto` est ignoré. Avec `BAMBU_PUBLIC_URL=http://…` et un accès par Funnel en HTTPS, le cookie de session est émis **sans** `Secure`. Et une seule `BAMBU_PUBLIC_URL` existe : en `https://` elle casse l'enrôlement par `http://192.168.0.8:7878` (les navigateurs rejettent un cookie `Secure` reçu en clair).

**Ce qui deviendrait accessible depuis Internet via Funnel** (tout ce que le LAN voit) :

| Surface | Exposition | Risque |
|---|---|---|
| `GET /health` | publique | divulgue version et schéma ; faible |
| `/mcp`, `/api/*` | derrière le Bearer (64 hex) | pas de limitation de débit sur les 401 ; non devinable ; **mais `/mcp` est 403 en loopback** (point 3) |
| `/enroll` (GET/POST) | publique | le code est à 8 caractères, 10 min, usage unique (non devinable), **mais 6 échecs de n'importe quelle origine verrouillent l'enrôlement de tous pendant 5 min** (`humanauth.go:45-46,148-163`) : déni de service trivial répétable (« À corriger » n° 5) |
| `/confirm`, `/stop`, `/setup`, `/agents/pair` | cookie d'un navigateur enregistré | un navigateur enregistré (30 jours) devient une clé d'accès valable depuis Internet : révoquer (`enroll --revoke`) ; c'est ici que partent lancement et arrêt d'impression |
| `/agent/v1/*` | secret d'agent / jeton d'appairage | hors garde Host ; pas de limite de débit ; secrets ≥ 128 bits ; un agent appairé peut téléverser jusqu'à 1 Gio par fichier (`SECURITY.md` § 6) |
| SPA `/` | 401 sans Bearer | inutilisable (voir ci-dessous) |

**Recommandations** :
- Garder `/agent/v1`, `/enroll`, `/setup`, `/confirm`, `/stop`, `/agents/pair` **sur LAN + Tailscale uniquement**. Si un Funnel est un jour nécessaire, n'y exposer que `/mcp` (et uniquement après avoir réglé les points 2, 3 et 5) — si l'outil Funnel permet un filtrage par chemin ; sinon placer un reverse proxy qui n'achemine que ce chemin.
- Ne jamais enlever `BAMBU_API_TOKEN` tant qu'un proxy ou un nom public existe.
- Choisir **une** URL d'accès humain : idéalement `https://atelier.tail4442b5.ts.net` fournie par `tailscale serve` (certificat valide, privée au tailnet) avec `BAMBU_PUBLIC_URL` en `https://`, et abandonner l'accès humain en `http://` clair. La saisie du code d'accès de l'imprimante ne doit pas se faire en HTTP clair sur le LAN.
- Connecteur distant Claude (cloud Anthropic) : voir § 8.3.

### 7.3 L'interface web est inaccessible avec un jeton d'API

La SPA n'est pas une « route humaine » (`guard.go:35-43`) : `GET /` et `/assets/*` exigent le Bearer, qu'un navigateur ne sait pas envoyer ; et le code de la SPA n'envoie jamais `Authorization` (`web/src/api/client.ts:26`, aucune occurrence de `Authorization` dans `web/src`) ; le cookie de navigateur enregistré **ne remplace pas** le Bearer pour l'API (test `internal/httpapi/human_test.go:223-226`). Sur ce serveur (écoute 0.0.0.0, jeton obligatoire) la SPA répond donc 401 à tout navigateur. Seules les pages HTML du serveur (`/enroll`, `/confirm`, `/stop`, `/setup`, `/agents/pair`) fonctionnent. C'est le premier point de « À corriger ».

---

## 8. Clients

### 8.1 Claude Code (MCP HTTP, jeton en en-tête)

```bash
export BAMBU_API_TOKEN="$(sudo grep '^BAMBU_API_TOKEN=' /etc/atelier/atelier.env | cut -d= -f2-)"   # ne pas l'afficher ni le coller dans une conversation
claude mcp add --transport http bambu http://atelier.tail4442b5.ts.net:7878/mcp \
  --header "Authorization: Bearer ${BAMBU_API_TOKEN}"
```

Ou dans `.mcp.json` (le jeton reste dans l'environnement de l'utilisateur) :

```json
{ "mcpServers": { "bambu": { "type": "http", "url": "http://atelier.tail4442b5.ts.net:7878/mcp",
  "headers": { "Authorization": "Bearer ${BAMBU_API_TOKEN}" } } } }
```

L'hôte de l'URL doit figurer dans `BAMBU_ALLOWED_HOSTS` (sinon 403 `hôte non autorisé`). Un client sur le serveur lui-même doit utiliser `127.0.0.1`/`localhost` ou un nom de la liste ; éviter le nom court de la machine (résolu en 127.0.1.1 : 403 du SDK, § 7.2).

### 8.2 Claude Desktop (pont stdio local)

Claude Desktop lance `bambu-mcp` (compilé pour le poste, Windows en général) qui relaie vers le serveur avec le Bearer :

```json
{ "mcpServers": { "bambu-atelier": { "command": "C:\\Users\\VOUS\\AppData\\Local\\BambuAtelier\\bambu-mcp.exe",
  "args": ["--url", "http://atelier.tail4442b5.ts.net:7878/mcp",
           "--api-token-file", "C:\\Users\\VOUS\\AppData\\Local\\BambuAtelier\\api-token.txt"] } } }
```

`api-token.txt` : une ligne, droits restreints au compte (`deploy/windows/configure-claude-desktop.ps1` le fait ; `deploy/windows/claude-desktop.md`). Le pont reconnecte seul après un redémarrage du serveur (`--connect-attempts`, `cmd/bambu-mcp/main.go:51,121-139`) et n'écrit rien d'autre que le protocole sur stdout. Le poste doit pouvoir joindre `atelier.tail4442b5.ts.net` (Tailscale installé et connecté).

### 8.3 Connecteur distant (non recommandé en l'état)

Les connecteurs distants personnalisés de Claude sont contactés **depuis l'infrastructure d'Anthropic**, pas depuis le poste de l'utilisateur (`docs/DOSSIER.md` § 3, lignes 66 et 325 ; source citée ligne 294). Un tailnet privé ou une IP du LAN ne leur est donc pas joignable : seul un accès public (Funnel) pourrait servir. Cette voie est aujourd'hui **bloquée par trois points du code** : `/mcp` répond 403 en loopback (§ 7.2 point 3), bambud n'offre qu'un Bearer statique (aucun flux OAuth dans le dépôt ; la prise en charge d'un en-tête statique par les connecteurs de Claude **n'est pas vérifiable ici** — à qualifier), et l'enrôlement/les pages humaines ne doivent pas être publiques. Le pont stdio (§ 8.2) reste la voie recommandée (`docs/DOSSIER.md:64`).

### 8.4 Agent Windows (`bambu-agent`)

Rôle : inventorier des dossiers Windows autorisés et transférer des fichiers (STL/OBJ/3MF/STEP, sorties de découpe) vers le serveur ; il **ne peut ni lancer ni arrêter une impression** (`docs/SECURITY.md` § 6). Appairage : `sudo atelier-admin agent pair` (équivalent de `bambud agent pair`, `cli.go:144-177`) ou la page `/agents/pair` d'un navigateur enregistré ; puis `bambu-agent pair --server <URL> --token <jeton>` sur le poste. **Non nécessaire en mode Linux seul** : le serveur découpe lui-même ; l'agent n'est utile que pour alimenter la bibliothèque depuis un poste Windows. Si on l'utilise : l'URL doit être joignable et son hôte reste hors garde Host, mais le trafic est en HTTP clair (secret et fichiers) hors Tailscale ; préférer HTTPS + `--server-fingerprint`.

---

## 9. Interface web : `web/`, pas `prototype/`

- `prototype/` est la maquette D0 **figée** : **ne pas la déployer** ni la servir (aucun code Go ne la référence).
- La vraie interface est **`web/`** (React/Vite/TypeScript, paquet `bambu-atelier-web`). Elle est **embarquée dans `bambud` au build** (`go:embed`, `internal/webui/webui.go:17`) : il n'y a rien à servir à côté, pas de reverse proxy statique, pas de Node à l'exécution.
- Commandes : § 2.1 (`cd web && npm ci && npm run build`, avec **Node ≥ 22.12**, ou ≥ 20.19). Résultat : `internal/webui/assets/` (SPA) et `internal/mcpserver/ui/views/mcp-app.html` (vue MCP App), tous deux ignorés par git, à regénérer à chaque build propre.
- Elle ne fonctionne dans un navigateur que si **aucun jeton d'API n'est configuré** (écoute loopback) ; avec le déploiement actuel elle renvoie 401 (§ 7.3).

---

## 10. Exploitation

### 10.1 Tests de fumée après déploiement (au-delà de `/health`)

`T` = jeton (jamais affiché), `B=http://127.0.0.1:7878` depuis le serveur.

1. `curl -fsS $B/health` → `{"schema_version":130,"status":"ok","version":"<describe>"}` (GET uniquement ; `HEAD` donne 401 avec jeton).
2. `curl -s -o /dev/null -w '%{http_code}' -X POST $B/mcp` → **401** ; idem `GET $B/api/capabilities` → 401.
3. `curl -fsS -H "Authorization: Bearer $T" $B/api/capabilities | jq '{mode, version, concurrency, f:[.features[]|{id,available}]}'` → `mode:"linux_seul"`, `slicing.available:true`, `cad_worker.available:true`, `catalog_import.available:true`, `concurrency` `{heavy:1, light:4}`, `windows_agent.available:false` (`core.go:278-315`). **Limites** : la réponse n'indique ni la version de Studio ni le nombre de profils, et `cad_worker` signifie seulement « uv et projet trouvés » (`catalog/derive.go:252-261`).
4. Studio + profils : `curl -fsS -H "Authorization: Bearer $T" "$B/api/profiles/filaments?machine=Bambu%20Lab%20P1S%200.4%20nozzle" | jq '.filaments|length'` → > 0 (force `Locate` + chargement des profils 02.09.01.70, `goals/lists.go:137-151`) ; une erreur `slicer_missing`/profils introuvables signale `BAMBU_STUDIO_BIN` ou `…/resources/profiles/BBL` incorrect. Version de Studio : `sudo -u atelier env HOME=/var/lib/atelier TMPDIR="$(mktemp -d)" /opt/atelier/bambustudio/current/AppRun --help | head -3` ; elle figure aussi dans le résultat de chaque découpe (`studio_version`).
5. MCP : `initialize` (récupérer l'en-tête `Mcp-Session-Id` avec `-D -`), `notifications/initialized`, puis `tools/list` — en-têtes `Authorization`, `Content-Type: application/json`, `Accept: application/json, text/event-stream`. La liste doit contenir notamment `get_capabilities`, `add_printer`, `execute_headless_slice`, `request_print_confirmation`, `search_models`.
6. Import + analyse de maillage : déposer un STL de test dans `/var/lib/atelier/import/` (propriétaire `atelier`), puis `POST $B/api/import` `{"paths":["/var/lib/atelier/import/cube.stl"],"wait_seconds":60}` et `GET $B/api/models` (analyse et aperçu présents).
7. Worker CAO : `POST $B/api/models/parametric` `{"template":"spacer","params":{"outer_diameter":20,"inner_diameter":8,"height":10},"name":"smoke-spacer"}` → 201 (CadQuery fonctionne) ; sinon `cad_backend_missing`/`worker_failed` (diagnostiquer `UV_PROJECT_ENVIRONMENT`, § 3.4).
8. **Découpe de bout en bout sans imprimer** : enregistrer l'imprimante (`POST /api/printers`, aucune connexion n'est tentée), puis `POST $B/api/print-jobs` `{"model_id":"…","printer_id":"…"}` (`SliceInput`, `printing/slice.go:23-32`) et suivre `GET /api/print-jobs/{id}` jusqu'à `VALIDATED` : exerce Studio headless, la validation de l'archive et le stockage sous `work/printing`. Aucun envoi à l'imprimante. Supprimer ou conserver l'impression de test.
9. `sudo -u atelier env BAMBU_DATA_DIR=/var/lib/atelier /opt/atelier/bin/bambud verify --quick` (lecture seule du service en marche) → « OK : aucune anomalie » ; sans `--quick` il relit tous les blobs.

### 10.2 Journaux

`slog` en texte sur **stderr** → journald (`journalctl -u atelier -f`). Niveau : `--log-level` / `BAMBU_LOG_LEVEL` (`debug|info|warn|error`). Pas de journal d'accès HTTP. Lignes utiles : `bambud démarré addr=… data_dir=… version=…` (`main.go:170`), `reprise après interruption` (`core.go:183`), `arrêt demandé : drain…` / `arrêt terminé` (`main.go:185,195`), `base laissée ouverte : des travaux tournent encore` (`main.go:139`, sortie en erreur). Avertissement attendu à chaque démarrage sous Linux : `coffre des codes d'accès : la clé maître est un simple fichier (droits 0600)…` (`secrets.go:73`). Le journal d'audit métier (enrôlements, épinglage, arrêt, appairage) est dans la table d'audit de la base.

### 10.3 Arrêt propre

`SIGTERM`/`SIGINT` (`main.go:91`) : arrêt HTTP (15 s fixes, `main.go:186`), annulation du contexte des workers, puis drain des travaux jusqu'à `--drain-timeout` / `BAMBU_DRAIN_TIMEOUT` (30 s). Passé ce délai : la base n'est volontairement pas fermée, le processus sort en erreur (`main.go:138-140,196-198`). Un flux MCP/SSE ouvert peut consommer les 15 s d'arrêt HTTP. Une découpe en cours est tuée (arbre de processus) et **rejouée au redémarrage** (`core.go:174`, `jobs/queue.go:520`) ; un lancement d'impression interrompu passe en `START_UNKNOWN` (`core.go:181`). Les pages de confirmation/arrêt ouvertes dans un navigateur sont invalidées (nonces en mémoire) : l'humain recharge la page. Unité : `TimeoutStopSec=60`.

### 10.4 Procédure de mise à jour

1. Construire sur le serveur (ou ailleurs) dans un dossier daté : web (§ 2.1) → vet → tests `-short` → binaires.
2. **Sauvegarder avant** : `bambud backup --out /var/backups/atelier/pre-<version>.tar.gz` puis `bambud verify --archive …` (et le restic nocturne). Noter `schema_version` de `/health`.
3. Choisir le moment : aucune impression en cours (le redémarrage n'interrompt pas l'impression sur l'imprimante, mais le suivi est repris à `pr.Recover`, `core.go:181`).
4. Installer les binaires (`/opt/atelier/bin`, atomiquement : écrire `bambud.new` puis `mv -f`), copier `workers/geometry` si changé puis `uv sync --frozen --extra cad --no-dev` (si `uv.lock` a changé), redémarrer.
5. Vérifier `/health` (200, même ou nouveau `schema_version`), `POST /mcp` sans jeton → 401, puis les tests de fumée du § 10.1.

### 10.5 Retour arrière

- **Aucune migration entre l'ancienne et la nouvelle version (cas actuel : schéma 130 des deux côtés)** : remettre l'ancien binaire et redémarrer suffit ; les données restent valides (fonctionnalités ajoutées = routes et sous-commandes, pas de colonne).
- **Si la nouvelle version a migré la base** (`schema_version` de `/health` plus grand qu'avant) : l'ancien binaire **refuse de démarrer** (`store.go:156-158`) et, avec `Restart=on-failure` sans limite, tourne en boucle. La marche arrière exige de **restaurer la sauvegarde d'avant mise à jour** : arrêter le service, `bambud restore --from pre-<version>.tar.gz --data-dir /var/lib/atelier --force` avec l'**ancien** binaire, `bambud verify`, remettre l'ancien binaire, redémarrer. Les écritures faites depuis la mise à jour (jobs, imprimantes ajoutées, sessions) sont perdues : c'est pourquoi la sauvegarde est faite *avant* et vérifiée. Le retour automatique de `etape19-deploy.sh` (sur échec de `/health`) ne vaut donc que s'il remet aussi la base d'origine quand le schéma a changé.
- Dans les deux cas, ne pas lancer `atelier-enroll` avec le binaire de la nouvelle version tant qu'on n'a pas décidé de la garder (§ 4.4).

---

## À corriger côté code

Aucun changement de code n'a été fait ; chaque point est un constat vérifié.

| # | Gravité | Constat | Preuve | Correctif suggéré |
|---|---|---|---|---|
| 1 | **Haute** (interface web) | Avec un jeton d'API configuré (obligatoire hors loopback), la SPA et ses appels `/api/*` sont refusés en 401 pour tout navigateur : `GET /`, `/assets/*` et l'API de l'interface exigent un Bearer que le navigateur et le code de la SPA n'envoient pas. L'interface web livrée au jalon 3 est inutilisable sur ce serveur ; les liens de téléchargement et les URL d'aperçu signées sont touchés de même. | `internal/httpapi/guard.go:35-43,118-126` ; `web/src/api/client.ts:26` (pas d'`Authorization`) ; sonde : `GET /` sans jeton → 401 ; `human_test.go:223-226` | Accepter la session d'un navigateur enregistré (cookie) comme alternative au Bearer pour les `GET` de l'API de lecture, et exempter les assets statiques ; ou servir la SPA sans jeton et protéger l'API par cookie |
| 2 | **Moyenne** (bloque Funnel / proxy local) | `/mcp` répond 403 `invalid Host header` quand la connexion arrive sur une adresse loopback avec un `Host` public (Funnel, `tailscale serve`, nginx vers `127.0.0.1`). Le garde de bambud valide déjà `Host`. | `go-sdk@v1.8.0/mcp/streamable.go:317-326` ; `internal/httpapi/httpapi.go:83-85` (`DisableLocalhostProtection` absent) ; sonde : 403 | `StreamableHTTPOptions{DisableLocalhostProtection: true}` puisque `guard` contrôle `Host` en amont |
| 3 | **Moyenne** | Le jeton n'est exigé que si l'écoute est hors loopback. Écoute `127.0.0.1` + proxy public + `BAMBU_ALLOWED_HOSTS`/`BAMBU_PUBLIC_URL` publics et pas de jeton ⇒ API et MCP ouverts sans authentification. | `internal/httpapi/guard.go:76-78` ; `cmd/bambud/main.go:125` | Refuser de démarrer si `--allowed-hosts` ou `--public-url` désigne un hôte non loopback sans jeton |
| 4 | Moyenne | Cookie `Secure` déduit seulement de `r.TLS` ou de `--public-url https` ; `X-Forwarded-Proto` ignoré ; une seule URL publique pour deux accès (http LAN, https Funnel/serve). | `httpapi.go:71-73` ; `humanauth.go:329-332` | Option explicite (`--trust-forwarded-proto`) ou décision par requête ; documenter le choix d'une URL canonique |
| 5 | Moyenne (si `/enroll` est joignable depuis Internet) | Verrou **global** de 5 min après 6 codes erronés, tous visiteurs confondus : n'importe qui peut empêcher indéfiniment l'enrôlement d'un nouveau navigateur. | `internal/humanauth/humanauth.go:45-46,148-163` | Limiter par adresse source / par session non authentifiée, avec en-têtes de proxy de confiance configurables |
| 6 | Faible | `HEAD /health` → 401 avec jeton (seul `GET` est exempté) ; certaines sondes ou `curl -I` échouent. | `guard.go:118` | Exempter aussi `HEAD /health` |
| 7 | Faible | `/api/capabilities` ne donne ni la version de Studio ni le nombre de profils machines/filaments ; `cad_worker.available` ne prouve que « uv + projet présents », pas CadQuery. Un test de fumée fiable exige un vrai slicing / import STEP. | `internal/core/core.go:360-365` ; `internal/catalog/derive.go:252-261` | Exposer `studio_version`, comptes de profils, résultat d'un `python -c "import cadquery"` mis en cache |
| 8 | Faible | L'outil MCP `inspect_environment` cherche Studio via `BAMBU_STUDIO_PATH` et Python via `BAMBU_PYTHON`, alors que le service utilise `BAMBU_STUDIO_BIN` : rapport « manquant » trompeur. | `internal/core/core.go:346-347` ; `slicer/binary.go:49` | Aligner sur `slicer.Locate` |
| 9 | Faible | `BAMBU_STUDIO_BIN` pointant vers un fichier inexistant est ignorée sans message au profit d'un autre Studio trouvé ailleurs ; seul `--studio-bin` est strict. | `internal/slicer/binary.go:73-77` ; `core.go:132-137` | Journaliser un avertissement (ou échouer) quand la variable est posée mais invalide |
| 10 | Faible | `.gitignore` contient des marqueurs de fusion commités (`<<<<<<< HEAD`, `=======`, `>>>>>>> main`) ; sans effet nuisible (motifs inertes), mais l'état de fusion est confus. | `.gitignore:11,18,20` | Nettoyer le fichier |
| 11 | Faible | `execWorker` lance `uv run` sans `--frozen`, `--no-dev` ni `--no-sync` : le comportement dépend de variables d'environnement `UV_*` à poser dans l'unité ; sans `UV_PROJECT_ENVIRONMENT` il tente de créer `.venv` dans le projet (lecture seule), et `uv run` installe le groupe `dev` à l'exécution (vérifié). | `internal/catalog/worker.go:80` | Passer `--frozen --no-dev` (et un `--no-sync` configurable) dans la commande |
| 12 | Faible | Aucune vérification qu'une racine d'import ne contient pas le dossier de données : `BAMBU_IMPORT_ROOTS=/var/lib/atelier` permettrait d'importer puis de télécharger `secrets/master.key` et la base. | `internal/catalog/roots.go` (pas de contrôle) ; `GET /api/files/{id}/download` | Refuser toute racine qui contient ou est contenue dans `--data-dir` |
| 13 | Faible (informatif) | `/agent/v1/*` est hors garde Host et sans limitation de débit (secrets de 128 bits, donc non devinables, mais surface publique si le service est exposé). | `guard.go:105-108` | Option pour désactiver `/agent/v1` quand aucun agent Windows n'est utilisé |
| 14 | Faible (informatif) | Aucun TLS natif : tout accès par `http://` sur le LAN transmet en clair le Bearer, le code d'enrôlement, le cookie et le code d'accès de l'imprimante saisi sur `/setup`. | `docs/SECURITY.md` § 8 ; `cmd/bambud/main.go` (aucun listener TLS) | Option `--tls-cert/--tls-key`, ou avertissement au démarrage quand l'URL publique est en `http://` hors loopback |

---

## Résumé pour le script de déploiement

1. **Build web obligatoire avant `go build`** : Node ≥ 22.12 (ou ≥ 20.19) absent aujourd'hui ; `cd web && npm ci && npm run build` ; contrôler `internal/webui/assets/index.html` et `internal/mcpserver/ui/views/mcp-app.html` (ignorés par git, sinon « interface non construite »).
2. **Tests de build** : `env -u BAMBU_STUDIO_BIN -u BAMBU_PROFILES_DIR TMPDIR="$(mktemp -d)" go test -short ./... -count=1` (≈ 1-3 min) ; ne pas exporter `BAMBU_STUDIO_BIN` pendant les tests (Studio 02.09 vs tests écrits sur 02.08).
3. **Unité systemd : ajouter `UV_PROJECT_ENVIRONMENT=/var/lib/atelier/venvs/geometry`** (sinon `uv run` veut créer `.venv` dans `/opt/atelier/workers/geometry` en lecture seule), `UV_NO_DEV=1` et `BAMBU_STUDIO_WRAPPER=none` ; garder `UV_FROZEN=1`.
4. **Unité** : `TimeoutStopSec=60` (arrêt = 15 s + drain 30 s), `Restart=on-failure` avec `StartLimitIntervalSec/StartLimitBurst` pour éviter une boucle si la base est plus récente que le binaire.
5. **Health check** : `GET /health` seulement (`HEAD` → 401) ; ajouter `/api/capabilities` avec jeton et `/api/profiles/filaments?machine=…` (Studio + profils réellement chargés).
6. **Sauvegarde** : préférer `bambud backup` + `bambud verify` (cohérent base/blobs/secrets) ; sinon exclure `*.db-wal`, `*.db-shm`, `work/`, `tmp/`, `blobs/tmp/`, `agent-staging/`, `.bambud.lock`, lancer `sqlite3 .backup` en tant que `atelier`, base avant blobs, secrets dans le même instantané.
7. **Retour arrière** : sûr par simple remplacement du binaire tant que `schema_version` est inchangé (130 → 130 ici) ; sinon l'ancien binaire refuse la base : le rollback doit restaurer la sauvegarde d'avant (`bambud restore --force`), prise avant l'installation.
8. **Ne pas lancer `atelier-enroll`/`backup` avec le nouveau binaire avant le redémarrage** si la mise à jour contient une migration (les sous-commandes migrent la base).
9. **Interface web inutilisable dans un navigateur avec le jeton** (401 sur `/`) : décision à prendre avant de promettre l'UI ; les pages `/enroll`, `/confirm`, `/stop`, `/setup` fonctionnent. **Funnel : ne pas l'activer** avant correctifs n° 2, 3, 5 (`/mcp` 403 en loopback, jeton non exigé en loopback, déni de service sur `/enroll`) ; viser une adresse non loopback ; n'exposer jamais `/agent/v1`, `/enroll`, `/setup`.
10. `BAMBU_ALLOWED_HOSTS` est correct (noms sans port) ; fixer `BAMBU_PUBLIC_URL` sur l'URL réellement ouverte par l'humain (idéalement `https://` via `tailscale serve`) et ne pas saisir le code d'accès de l'imprimante en `http://` clair sur le LAN.
