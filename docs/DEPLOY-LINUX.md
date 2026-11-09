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
| Comportement par défaut | **Corrigé (`e95e4d5`)** : le wrapper `xvfb-run -a` n'est plus jamais activé implicitement ; seul `BAMBU_STUDIO_WRAPPER`/`--studio-wrapper` explicite compte (`none` ou vide = aucun préfixe). | Non (garder `BAMBU_STUDIO_WRAPPER=none` par clarté) |
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

> **Mise à jour (correctifs « À corriger côté code »)** : les sections 3, 7, 9 et 10.1 ci-dessous décrivent le comportement **après** les commits `e95e4d5` (authentification web, proxy, verrou d'enrôlement, jeton), `77e50b2`/`f3063fb` (worker CAO, racines d'import), `a5e60cb` (capacités), `430c06e` (interface web), `a33e96c` (`scripts/smoke-linux.sh`). Les constats d'origine qui ne sont plus vrais sont signalés « corrigé » ; l'état point par point est dans « À corriger côté code ».

Les flags de `bambud` acceptent `-flag` ou `--flag`, avec `--flag=valeur`. **Priorité : flag explicite > variable d'environnement > défaut** (la variable sert de valeur par défaut au flag, `cmd/bambud/main.go:47-61`). Il n'y a **pas** de sous-commande `serve` : `bambud` sans sous-commande sert ; `bambud serve --addr …` fonctionnerait par accident (l'argument positionnel est ignoré) mais n'est pas supporté.

### 3.1 `bambud` (service) — `cmd/bambud/main.go`

| Flag | Variable | Défaut | Oblig. ? | Effet |
|---|---|---|---|---|
| `--addr` | `BAMBU_ADDR` | `127.0.0.1:7878` | non | adresse d'écoute. Hors loopback → jeton obligatoire ; voir aussi `--require-token`. `0.0.0.0` = IPv4 seulement. |
| `--data-dir` | `BAMBU_DATA_DIR` | `./data` (relatif au répertoire courant du processus !) | **oui en pratique** | racine des données (l. 48). Rendu absolu au démarrage (`internal/core/core.go:95`). |
| `--heavy-workers` | `BAMBU_HEAVY_WORKERS` | 1 | non | travaux lourds simultanés : découpe Studio, import STEP (`printing/slice.go:167`, `goals/experiment.go:308`, `catalog/derive.go:223`). Valeur ≤ 0 ou illisible → défaut (`main.go:31-36`). |
| `--light-workers` | `BAMBU_LIGHT_WORKERS` | 4 | non | travaux légers : analyse/aperçu de maillage, import, lancement (`catalog/derive.go:226-227`, `printing/start.go:98`). |
| `--api-token` | **`BAMBU_API_TOKEN`** | vide | **oui** dès que : écoute hors loopback, `--require-token`, `--public-url` non loopback ou `--allowed-hosts` contenant un nom non loopback | jeton Bearer de l'API et de `/mcp` (l'interface web, elle, passe par le cookie d'un navigateur enregistré). Longueur mini 16. **Refus de démarrer sans jeton dans ces cas** (`httpapi.ValidateServe`) ; message : « jeton d'API obligatoire (…) ». Derrière un proxy, **toujours un jeton**. Le jeton n'est jamais journalisé. |
| `--allowed-hosts` | `BAMBU_ALLOWED_HOSTS` | vide | non mais **indispensable** dès qu'on y accède par un autre nom que `localhost`/`127.x`/`::1` | liste séparée par des virgules, **noms d'hôte sans port**, insensible à la casse, espaces tolérés, crochets IPv6 retirés (`main.go:93-97`, `guard.go:85-91`). Pas de joker. Pas de point final (`nom.` est refusé : vérifié). L'hôte de `--public-url` est ajouté d'office (`guard.go:89-91`). |
| `--require-token` | `BAMBU_REQUIRE_TOKEN` (`1`, `true`, `yes`, `on`) | faux | recommandé derrière un proxy | exige un jeton d'API même en écoute loopback. Inutile si `--public-url`/`--allowed-hosts` désignent déjà un nom non loopback (le jeton est alors exigé d'office), mais explicite pour un proxy qui vise `127.0.0.1` avec un `Host` réécrit. |
| `--trusted-proxies` | `BAMBU_TRUSTED_PROXIES` | **loopback** (127.0.0.0/8 et ::1) | non | adresses IP ou réseaux CIDR, séparés par des virgules, dont `X-Forwarded-For` et `X-Forwarded-Proto` sont crus ; `loopback` (mot-clé) ; `none` = aucun. Sert (1) au cookie `Secure` (`X-Forwarded-Proto: https`) et (2) à l'adresse client du limiteur d'enrôlement (entrée `X-Forwarded-For` la plus à droite qui n'est pas un proxy de confiance). **Jamais lu** sur une connexion qui ne vient pas d'un proxy de confiance. |
| `--drain-timeout` | **`BAMBU_DRAIN_TIMEOUT`** (existe bien) | 30 s | non | durée Go (`45s`, `2m`) d'attente des travaux à l'arrêt (`main.go:53,153-155,196-198`). Invalide → 30 s. S'ajoute aux 15 s fixes d'arrêt HTTP (`main.go:186`). |
| `--log-level` | `BAMBU_LOG_LEVEL` | `info` | non | `debug|info|warn|error` ; valeur invalide → `info` silencieusement (`main.go:86-88`). |
| *(variable seule)* | **`BAMBU_TIMEZONE`** | fuseau du serveur (`time.Local`), puis `TZ` | non, mais **recommandé** (un serveur Linux est souvent en UTC) | fuseau IANA (`Europe/Paris`) dans lequel sont affichées **toutes les heures destinées aux humains** : pages `/confirm`, `/stop`, `/enroll`, `/agents/pair`, `/oauth/consent`, carte de confirmation, texte renvoyé à Claude (`expires_at_local` : `21:03 (08/11/2026, Europe/Paris)`), avec la date au format français. Ordre : `BAMBU_TIMEZONE`, puis `TZ`, puis le fuseau du système ; un nom inconnu est ignoré (avertissement au démarrage). La base de fuseaux est **embarquée** dans le binaire (aucun paquet `tzdata` requis). Les données stockées, les journaux et les champs RFC 3339 (`expires_at`) restent en UTC / avec décalage. Exemple systemd : `Environment=BAMBU_TIMEZONE=Europe/Paris`. |
| `--import-root` | `BAMBU_IMPORT_ROOTS` | vide | non | racines d'import autorisées, séparées par `:` sous Linux (`filepath.SplitList`). Vide = aucun import de chemin arbitraire. **Refus de démarrer si une racine contient le dossier de données (donc `secrets/`, la base, les blobs) ou s'y trouve** (`e95e4d5`→`77e50b2`, `catalog/roots.go`). NB : le dossier de réception de l'agent Windows `agent-inbox` est **toujours** ajouté comme racine (`internal/agentlink/registry.go`), donc `catalog_import.available` est vrai même sans cette variable. |
| `--uv-path` | `BAMBU_UV_PATH` | recherche `uv` dans PATH | non | exécutable `uv` du worker CAO ; doit exister. |
| `--geometry-dir` | `BAMBU_GEOMETRY_DIR` | `workers/geometry` relatif au cwd puis à côté du binaire | non | dossier du projet Python ; doit contenir `pyproject.toml` (`worker.go:26-46`). |
| `--public-url` | `BAMBU_PUBLIC_URL` | vide → adresse d'écoute (0.0.0.0 devient 127.0.0.1) | recommandé | **une ou plusieurs URL** `http(s)://hôte[:port]` séparées par des virgules (sans chemin ni requête ; hôte non spécifié refusé). La **première** sert aux liens `/confirm`, `/stop`, `/setup` et à `bambud enroll`. Tous les hôtes sont autorisés d'office par le garde Host. L'attribut `Secure` du cookie suit le schéma de l'URL dont l'hôte correspond au `Host` de la requête (ex. `http://192.168.1.50:7878,https://atelier.example.ts.net`), sinon celui de la première ; `X-Forwarded-Proto` d'un proxy de confiance l'emporte. Une URL non loopback **exige un jeton**. |
| `--allow-any-printer-host` | `BAMBU_ALLOW_ANY_PRINTER_HOST` (seule la valeur `1` active) | faux | non | lève la restriction « IP privée / lien local / nom `.local` » des hôtes d'imprimante (`internal/printing/service.go:402-417`). **Inutile ici** (P1S en 192.168.1.x). |
| `--studio-bin` | `BAMBU_STUDIO_BIN` | détection (§ 2.3) | non | chemin de Studio. **Corrigé (`e95e4d5`)** : un chemin donné par le flag **ou** par `BAMBU_STUDIO_BIN` mais introuvable (ou dossier) est une **erreur de démarrage** (« --studio-bin/BAMBU_STUDIO_BIN=… : Bambu Studio introuvable »), sans repli silencieux sur une autre installation ; `core.Open` applique la même règle aux usages en bibliothèque (erreur explicite dans `/api/capabilities`). |
| `--studio-wrapper` | `BAMBU_STUDIO_WRAPPER` | vide = aucun | non | préfixe de commande du slicer, ex. `xvfb-run -a`. **Jamais activé implicitement** (corrigé `e95e4d5`) ; `none` (insensible à la casse), `-` ou vide : aucun préfixe. |
| `--version` | — | — | — | affiche `bambud <Version>` (`main.go:79-82`). |

**`BAMBU_STUDIO_WRAPPER`** : valeur non vide → utilisée telle quelle (découpée sur les espaces) ; `none`, `-` ou vide/absente → **aucun préfixe**. Plus aucun défaut automatique : installer `xvfb` ne change rien tant que la variable n'est pas posée explicitement. Si Studio exige un écran un jour : `BAMBU_STUDIO_WRAPPER="xvfb-run -a"`.

**Variables lues ailleurs dans le code (hors flags)** :

| Variable | Lue où | Effet |
|---|---|---|
| *(corrigé `a5e60cb`)* `BAMBU_STUDIO_BIN`, `BAMBU_UV_PATH`, `BAMBU_GEOMETRY_DIR` | `internal/core/core.go` (`Environment`) | l'outil MCP `inspect_environment` rapporte désormais l'installation de Studio **réellement retenue** (chemin + version) et l'`uv`/projet **réellement résolus** ; les anciens noms `BAMBU_STUDIO_PATH`/`BAMBU_PYTHON` ne sont plus lus |
| `BAMBU_LOG_MCP` | `internal/mcpserver/mcplog.go` | défaut : actif. `off` (aussi `0`, `false`, `no`) coupe le journal INFO des requêtes MCP (`tools/call`, `resources/read`, `resources/list`, `resources/templates/list`, `prompts/*` : nom, durée, succès, taille, présence de `_meta.ui` ; jamais d'arguments ni de contenu). Lecture : `journalctl -u atelier \| grep -E "MCP (tools/call\|resources/read)"` ; voir `docs/research/mcp-apps.md` § 11 |
| `BAMBU_KEEP_WORK` | `internal/goals/slicejob.go:173` | non vide → conserve les dossiers de travail de découpe (débogage) |
| `UV_PROJECT_ENVIRONMENT`, `PYTHONDONTWRITEBYTECODE` | `internal/catalog/worker.go` (`workerEnv`) | transmises au worker ; `UV_PROJECT_ENVIRONMENT` fournie est respectée telle quelle ; absente et pas de `.venv` dans le projet → `<data-dir>/venvs/geometry` ; `PYTHONDONTWRITEBYTECODE=1` par défaut |
| `XDG_DATA_HOME` | `internal/core/datadir.go:25` | défaut de `--data-dir` du seul `bambu-mcp --embedded` |
| `LOCALAPPDATA`, `APPDATA` | `internal/agent/config.go:58`, `studio.go:246` | agent Windows seulement |
| `UV_*`, `TMPDIR`, `HOME`, `PATH` | héritées : `exec.Command` sans `Env` (`slicer.go:549-551`, `catalog/worker.go:80`) | pilotent `uv` et Studio (§ 3.4, § 5) |
| Tests | `BAMBU_PROFILES_DIR`, `BAMBU_TEST_WORKROOT`, `BAMBU_TEST_REAL_WORKER`, `BAMBU_GEN_TESTDATA`, `AGENT_REAL_3MF`, `FAKE_STUDIO` | jamais en production |

#### Envoi de fichiers et import par lien (`internal/catalog/upload.go`, `urlimport.go`)

| Variable | Défaut | Rôle |
|---|---|---|
| `BAMBU_MAX_UPLOAD_MB` | `512` | taille maximale d'un fichier envoyé (`upload_file`, glisser-déposer) ; STEP plafonné à 256 Mo ; reste borné par la limite d'import (2 Gio) |
| `BAMBU_UPLOAD_TTL` | `1h` | inactivité au bout de laquelle une session d'envoi est purgée (durée Go : `30m`, `2h`) |
| `BAMBU_URL_IMPORT` | actif | `off` désactive `import_from_url` et la route `POST /api/import-url` |
| `BAMBU_URL_IMPORT_ALLOW` | `makerworld.com,bblmw.com,printables.com` | liste (virgules) des domaines autorisés, sous-domaines compris ; **remplace** la liste par défaut ; les adresses IP sont ignorées |
| `BAMBU_URL_IMPORT_TIMEOUT` | `120s` | durée maximale d'un import par lien (redirections et téléchargement compris) |

Les morceaux en cours s'accumulent dans `$BAMBU_DATA_DIR/uploads/` (fichiers nommés par le service, purgés au démarrage et à l'expiration) avant d'entrer dans `blobs/` : prévoir de l'espace libre pour la taille maximale × 8 sessions simultanées. L'import par lien est le seul accès réseau sortant déclenché par un outil MCP : HTTPS port 443, résolution DNS vérifiée (aucune adresse privée, loopback, lien local ou réservée), pas de proxy d'environnement. Routes ajoutées (même régime que `/api/*` : Bearer, ou cookie de navigateur enregistré + `X-Atelier-Request`) : `POST /api/uploads`, `PUT|GET|DELETE /api/uploads/{id}`, `POST /api/uploads/{id}/finish`, `POST /api/import-url`.

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
| `UV_FROZEN=1` | Utile, plus indispensable | **Corrigé (`77e50b2`, `f3063fb`)** : bambud lance `uv run --frozen --no-dev --extra cad --project <dir> python -m bambu_geometry <cmd>` ; uv.lock n'est jamais réécrit, le groupe `dev` n'est pas installé, CadQuery est garanti. `UV_FROZEN=1` peut rester. |
| `UV_PROJECT_ENVIRONMENT` | Respectée si fournie ; **plus bloquante si absente** | **Corrigé (`77e50b2`)** : si elle est posée, bambud la transmet telle quelle (recommandé : `/var/lib/atelier/venvs/geometry`, la même valeur que pour le `uv sync`). Si elle est absente et que `<projet>/.venv` n'existe pas, bambud place le venv dans `<BAMBU_DATA_DIR>/venvs/geometry` : **bambud n'écrit jamais dans `BAMBU_GEOMETRY_DIR`** (aucun `.venv`, aucun `.pyc`, aucun `uv.lock`). Pour éviter un premier appel lent, pré-synchroniser : `uv sync --frozen --no-dev --extra cad --project /opt/atelier/workers/geometry` avec la même `UV_PROJECT_ENVIRONMENT`. |
| `UV_NO_DEV=1` | Plus nécessaire | **Corrigé** : `--no-dev` est passé par bambud. Peut rester sans effet nuisible. |
| `--extra cad` | OK | sans lui, `step_import` répond `step_backend_missing` et `parametric` `cad_backend_missing` (`workers/geometry/pyproject.toml`). |
| `BAMBU_IMPORT_ROOTS=/var/lib/atelier/import` | OK | le dossier doit exister et appartenir à `atelier`. **Corrigé (`77e50b2`)** : une racine qui contient `BAMBU_DATA_DIR` (donc `secrets/`) ou qui s'y trouve fait refuser le démarrage. `/var/lib/atelier/import` est **dans** `/var/lib/atelier` : **refusé** si `BAMBU_DATA_DIR=/var/lib/atelier` ; choisir un dossier **hors** du dossier de données (ex. `/srv/atelier/import`). |
| `BAMBU_ALLOWED_HOSTS=atelier.example.ts.net,atelier,atelier-pc.local,100.64.0.10,192.168.1.50` | **OK** | format correct (sans port, virgules). Vérifié par sonde : `Host: atelier.example.ts.net` avec ou sans `:7878` passe, en majuscules aussi, `…ts.net.` (point final) et tout hôte absent de la liste → 403 `hôte non autorisé`. Le nom court `atelier` suppose que les clients résolvent ce nom (MagicDNS). `127.0.0.1`/`localhost` sont toujours acceptés. |
| `BAMBU_PUBLIC_URL=http://atelier.example.ts.net:7878` | OK, avec réserves | l'URL que **l'humain** ouvre (liens `/confirm`, `/setup`, code d'enrôlement affiché). Peut maintenant être une **liste** : `http://atelier.example.ts.net:7878,https://atelier.example.ts.net` (la première pour les liens ; le cookie est `Secure` pour l'accès https seulement). Une URL non loopback **exige** `BAMBU_API_TOKEN` (déjà le cas ici). **Le code d'accès LAN de l'imprimante saisi sur `/setup` ne doit pas transiter par `http://192.168.1.50:7878`** (clair sur le LAN). |
| `HOME=/var/lib/atelier` | OK | héberge `~/.config/BambuStudio`, `~/.cache/uv`, `~/.local/share/uv/python` (§ 5.2) ; inscriptible, donc compatible `ProtectSystem=strict`. |
| `BAMBU_STUDIO_WRAPPER` absente | OK | **Corrigé** : absente = aucun préfixe (plus de défaut `xvfb-run`). `none` reste accepté. |
| `BAMBU_DRAIN_TIMEOUT` absente | OK (30 s) | existe ; garder `TimeoutStopSec` ≥ 60 s dans l'unité (15 s d'arrêt HTTP + 30 s de drain = 45 s). |
| `BAMBU_ALLOW_ANY_PRINTER_HOST` absente | OK | |
| Unité : `Restart=` non précisé | À préciser | `Restart=on-failure`, `RestartSec=5`, `StartLimitIntervalSec=300`, `StartLimitBurst=5` : sinon un refus de démarrer (base plus récente que le binaire, § 10.5) boucle indéfiniment. |

Environnement minimal recommandé pour l'unité (à fusionner avec l'existant) :
`UV_PROJECT_ENVIRONMENT=/var/lib/atelier/venvs/geometry` (recommandé, pré-synchronisé avec `uv sync --frozen --no-dev --extra cad`) ; nouveaux, optionnels : `BAMBU_REQUIRE_TOKEN=1`, `BAMBU_TRUSTED_PROXIES=loopback` (défaut) ; optionnel : `BAMBU_LOG_LEVEL=info`, `BAMBU_DRAIN_TIMEOUT=30s`.

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

- **Aucun besoin d'affichage** côté bambud. Studio : le constat de l'admin (slicing CLI sans écran, sans miniatures `plate_N.png`) est compatible avec le code : le code ne dépend pas des miniatures et lance Studio sans xvfb tant que `BAMBU_STUDIO_WRAPPER` n'est pas posé (plus aucun défaut automatique).
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

Toutes les routes sauf `/agent/v1/*` passent d'abord par le **contrôle de l'en-tête Host** (loopback ou liste blanche), puis l'**authentification** (jeton Bearer ou, pour `/api/*`, session de navigateur enregistré), puis la **protection cross-origin** de Go (`http.CrossOriginProtection`).

| Famille | Routes | Authentification | Source |
|---|---|---|---|
| **Publique (Host contrôlé seulement)** | `GET`/`HEAD /health` (renvoie `status`, `version`, `schema_version`) ; **la SPA** : `GET`/`HEAD` `/`, `/assets/*` et ses routes client (aucune donnée) ; l'URL d'aperçu **signée** `GET /api/assets/<sha>.glb?exp=…&sig=…` (capacité HMAC vérifiée par le handler) | aucune | `guard.go` (`isPublicStatic`, `isSignedAsset`) |
| **Jeton d'API (Bearer) ou navigateur enregistré** | `/mcp` : **Bearer uniquement** ; tout `/api/*` (catalogue, objectifs, impressions, imprimantes, agents, export, téléchargement, `/api/capabilities`…) : **Bearer, ou cookie `bambu_session` d'un navigateur enregistré** | Bearer : `Authorization: Bearer <BAMBU_API_TOKEN>` (temps constant). Cookie : lectures (`GET`/`HEAD`) directes ; **écritures** seulement avec `X-Atelier-Request: 1` **et** `Sec-Fetch-Site: same-origin` (à défaut d'en-têtes Sec-Fetch, hors localhost en http : `Origin` = `Host`), sinon 403 `csrf` ; un Bearer valide n'a pas besoin de CSRF. Sans rien : 401 `{"code":"unauthorized","enroll_url":"/enroll"}` | `guard.go` |
| **Humaines (cookie de navigateur enregistré)** | `GET/POST /enroll` (public mais limité : voir ci-dessous), `POST /enroll/logout`, `GET /api/session` (sans secret), `GET/POST /confirm/{id}`, `/stop/{id}`, `/setup/printer/{id}`, `GET/POST /agents/pair` | exemptées du jeton (`isHumanPath`, `guard.go:35-43`) ; `Require` impose le cookie (sinon 303 vers `/enroll?next=`), et pour les POST `Sec-Fetch-Site: same-origin` + `Sec-Fetch-Mode: navigate` + nonce de formulaire (`humanauth/web.go:71-100`) | |
| **Agent Windows** | `POST /agent/v1/pair` (jeton `pair-…` à usage unique, 128 bits), `/agent/v1/hello|heartbeat|tasks|inventory|uploads|blobs…` | secret d'agent propre ; **ni contrôle Host, ni CSRF, ni jeton d'API** (`guard.go:105-108`) ; pas de limitation de débit | `agentlink/server.go:22-134` |
| Retirée | `POST /api/agents/pairing` | répond 410 | `agent.go:128-130` |

Sondes (après correctifs, `bambud` de test avec jeton + `--require-token`) : `GET /` et `/bibliotheque` → 200 sans rien ; `HEAD /health` → 200 ; `/api/capabilities` sans jeton ni cookie → 401, avec Bearer → 200, avec cookie d'un navigateur enregistré → 200 ; `POST /api/…` par cookie sans `X-Atelier-Request` → 403 ; `/mcp` avec cookie seul → 401 ; `POST /mcp` avec `Host` public autorisé depuis une connexion loopback → 200 ; `/confirm/x`, `/agents/pair` sans cookie → 303 ; `/agent/v1/hello` avec `Host: evil.example` → 401 (et non 403). Parcours `web/scripts/e2e.mjs` complet (import, opérations, découpes réelles, téléchargement, export) exécuté **par cookie** contre un serveur à jeton : OK.

### 7.2 Cas Funnel / `tailscale serve` / reverse proxy dont le `RemoteAddr` est loopback

**Ce que le code fait de `RemoteAddr` (après correctifs)** : un proxy est « de confiance » s'il figure dans `--trusted-proxies` (défaut : loopback). Pour ces connexions seulement, bambud lit `X-Forwarded-Proto` (cookie `Secure`) et `X-Forwarded-For` (adresse client du limiteur d'enrôlement). Une requête qui n'arrive pas d'un proxy de confiance ne peut rien usurper : ces en-têtes sont ignorés. `Forwarded`/`X-Real-IP`/`X-Forwarded-Host` ne sont jamais lus ; seul `Host` compte (le proxy doit le **conserver**).

Les décisions de sécurité reposent sur :

1. **Le jeton** : obligatoire (refus de démarrer sans) si l'écoute n'est pas loopback, si `--require-token`, si `--public-url` désigne un hôte non loopback ou si `--allowed-hosts` contient un nom non loopback. **Derrière un proxy, toujours un jeton** : écouter sur `127.0.0.1` ne protège de rien quand le proxy relaie un nom public. (Corrigé `e95e4d5`.)
2. **L'en-tête `Host`** : liste blanche (`--allowed-hosts` + hôtes de `--public-url`). Le nom du Funnel (`atelier.example.ts.net`) doit y figurer. Le proxy doit **conserver** le `Host` d'origine : la protection CSRF compare `Origin` à `Host`.
3. **`/mcp` derrière un proxy loopback** : **corrigé (`e95e4d5`)**. Le SDK MCP (`go-sdk@v1.8.0/mcp/streamable.go:317-326`) répondait 403 `invalid Host header` à tout `Host` non loopback arrivant sur une adresse locale loopback ; `StreamableHTTPOptions.DisableLocalhostProtection` est maintenant positionné, notre garde Host (liste blanche) restant maître. Test : connexion loopback, `Host` public autorisé → 200 ; `Host` étranger → 403 (notre garde).
4. **Le cookie `Secure`** : `r.TLS`, ou `X-Forwarded-Proto: https` **d'un proxy de confiance**, sinon le schéma de l'URL publique dont l'hôte correspond au `Host` de la requête, sinon celui de la première. Avec deux URL publiques (`http://192.168.1.50:7878,https://atelier.example.ts.net`), l'accès LAN en http et l'accès `tailscale serve` en https fonctionnent chacun. (Corrigé `e95e4d5`.)

**Ce qui deviendrait accessible depuis Internet via Funnel** (tout ce que le LAN voit) :

| Surface | Exposition | Risque |
|---|---|---|
| `GET /health` | publique | divulgue version et schéma ; faible |
| `/mcp`, `/api/*` | derrière le Bearer (64 hex) | pas de limitation de débit sur les 401 ; non devinable ; **mais `/mcp` est 403 en loopback** (point 3) |
| `/enroll` (GET/POST) | publique | code à 8 caractères, 10 min, usage unique (non devinable). **Corrigé (`e95e4d5`)** : le verrou est **par adresse client** (6 échecs → 5 min, doublé à chaque récidive jusqu'à 1 h) ; un plafond **global** plus haut (60 échecs en 5 min, ce qui exige au moins 10 adresses distinctes) suspend tous les enrôlements 1 min, doublé à chaque déclenchement jusqu'à 15 min. Un attaquant seul ne peut plus bloquer tout le monde ; une armée d'adresses ne bloque que temporairement. L'adresse client tient compte du proxy de confiance. Exposer `/enroll` à Internet reste déconseillé |
| `/confirm`, `/stop`, `/setup`, `/agents/pair` | cookie d'un navigateur enregistré | un navigateur enregistré (30 jours) devient une clé d'accès valable depuis Internet : révoquer (`enroll --revoke`) ; c'est ici que partent lancement et arrêt d'impression |
| `/agent/v1/*` | secret d'agent / jeton d'appairage | hors garde Host ; pas de limite de débit ; secrets ≥ 128 bits ; un agent appairé peut téléverser jusqu'à 1 Gio par fichier (`SECURITY.md` § 6) |
| SPA `/` | publique (statique) | ne contient aucune donnée ; l'interface appelle `/api/*`, qui exige un navigateur enregistré ou le Bearer |

**Recommandations** :
- Garder `/agent/v1`, `/enroll`, `/setup`, `/confirm`, `/stop`, `/agents/pair` **sur LAN + Tailscale uniquement**. Si un Funnel est un jour nécessaire, n'y exposer que `/mcp` (les points 2, 3 et 5 sont corrigés ; vérifier `--trusted-proxies` et un jeton) — si l'outil Funnel permet un filtrage par chemin ; sinon placer un reverse proxy qui n'achemine que ce chemin.
- Ne jamais enlever `BAMBU_API_TOKEN` tant qu'un proxy ou un nom public existe.
- Accès humain : idéalement `https://atelier.example.ts.net` fourni par `tailscale serve` (certificat valide, privé au tailnet), **en premier** dans `BAMBU_PUBLIC_URL` (`https://atelier.example.ts.net,http://192.168.1.50:7878` : la première sert aux liens), et abandonner l'accès humain en `http://` clair. La saisie du code d'accès de l'imprimante ne doit pas se faire en HTTP clair sur le LAN.
- Connecteur distant Claude (cloud Anthropic) : voir § 8.3.

### 7.3 L'interface web avec un jeton d'API (corrigé : `e95e4d5`, `430c06e`)

La SPA (`GET /`, `/assets/*`, routes client) est servie **sans authentification** (elle ne contient aucune donnée). Ses appels `/api/*` sont authentifiés par le **cookie** `bambu_session` d'un navigateur enregistré (`bambud enroll` / `atelier-enroll`, puis `/enroll`), en plus du Bearer. Sans cookie, l'API répond 401 `unauthorized` et la SPA affiche un écran **« Enregistrer ce navigateur »** qui mène à `/enroll?next=…`.

Protection CSRF des écritures par cookie : `web/src/api/client.ts` envoie `credentials: 'same-origin'` et `X-Atelier-Request: 1` sur tous les appels ; le serveur exige cet en-tête **et** `Sec-Fetch-Site: same-origin` (ou, à défaut d'en-têtes Sec-Fetch — les navigateurs n'en envoient pas en `http://` hors localhost —, un `Origin` identique au `Host`). `/mcp` reste **Bearer uniquement** (le cookie n'y vaut rien). Les routes d'approbation (`/confirm`, `/stop`, `/setup`, `/agents/pair`) sont **inchangées** : cookie + navigation de même origine + nonce, jamais d'API JSON ni de Bearer pour lancer ou arrêter une impression (`docs/SECURITY.md`). L'URL d'aperçu signée reste lisible sans Bearer (capacité HMAC).

**Pages humaines : HTTPS ou localhost uniquement.** `/enroll`, `/confirm`, `/stop`, `/setup/*`, `/oauth/consent` et `/agents/pair` ne fonctionnent qu'en `https://` (par exemple `tailscale serve`) ou sur `http://localhost`. En `http://IP:7878`, la page pose `Referrer-Policy: no-referrer`, donc Chrome envoie `Origin: null` et, hors contexte sécurisé, aucun en-tête `Sec-Fetch-*` : la protection cross-origin refuse la soumission (« cross-origin request detected »). bambud **n'assouplit pas** cette protection (`Origin: null` n'est jamais accepté). À la place, une page humaine demandée en http hors loopback affiche « Cette page exige HTTPS : ouvrez … » avec un lien (même chemin) vers la première URL https de `BAMBU_PUBLIC_URL`, et la SPA affiche un bandeau équivalent. Déclarez donc une URL https, par exemple `BAMBU_PUBLIC_URL=http://192.168.0.8:7878,https://atelier.example.ts.net`, et enregistrez le navigateur depuis l'URL https. L'API JSON et `/mcp` restent utilisables en http avec le jeton.

---

## 8. Clients

### 8.1 Claude Code (MCP HTTP, jeton en en-tête)

```bash
export BAMBU_API_TOKEN="$(sudo grep '^BAMBU_API_TOKEN=' /etc/atelier/atelier.env | cut -d= -f2-)"   # ne pas l'afficher ni le coller dans une conversation
claude mcp add --transport http bambu http://atelier.example.ts.net:7878/mcp \
  --header "Authorization: Bearer ${BAMBU_API_TOKEN}"
```

Ou dans `.mcp.json` (le jeton reste dans l'environnement de l'utilisateur) :

```json
{ "mcpServers": { "bambu": { "type": "http", "url": "http://atelier.example.ts.net:7878/mcp",
  "headers": { "Authorization": "Bearer ${BAMBU_API_TOKEN}" } } } }
```

L'hôte de l'URL doit figurer dans `BAMBU_ALLOWED_HOSTS` (sinon 403 `hôte non autorisé`). Un client sur le serveur lui-même doit utiliser `127.0.0.1`/`localhost` ou un nom de la liste ; éviter le nom court de la machine (résolu en 127.0.1.1 : 403 du SDK, § 7.2).

### 8.2 Claude Desktop (pont stdio local)

Claude Desktop lance `bambu-mcp` (compilé pour le poste, Windows en général) qui relaie vers le serveur avec le Bearer :

```json
{ "mcpServers": { "bambu-atelier": { "command": "C:\\Users\\VOUS\\AppData\\Local\\BambuAtelier\\bambu-mcp.exe",
  "args": ["--url", "http://atelier.example.ts.net:7878/mcp",
           "--api-token-file", "C:\\Users\\VOUS\\AppData\\Local\\BambuAtelier\\api-token.txt"] } } }
```

`api-token.txt` : une ligne, droits restreints au compte (`deploy/windows/configure-claude-desktop.ps1` le fait ; `deploy/windows/claude-desktop.md`). Le pont reconnecte seul après un redémarrage du serveur (`--connect-attempts`, `cmd/bambu-mcp/main.go:51,121-139`) et n'écrit rien d'autre que le protocole sur stdout. Le poste doit pouvoir joindre `atelier.example.ts.net` (Tailscale installé et connecté).

### 8.3 Connecteur distant (non recommandé en l'état)

Les connecteurs distants personnalisés de Claude sont contactés **depuis l'infrastructure d'Anthropic**, pas depuis le poste de l'utilisateur (`docs/DOSSIER.md` § 3, lignes 66 et 325 ; source citée ligne 294). Un tailnet privé ou une IP du LAN ne leur est donc pas joignable : seul un accès public (Funnel) pourrait servir. Cette voie est aujourd'hui **bloquée par trois points du code** : `/mcp` répond 403 en loopback (§ 7.2 point 3), bambud n'offre qu'un Bearer statique (aucun flux OAuth dans le dépôt ; la prise en charge d'un en-tête statique par les connecteurs de Claude **n'est pas vérifiable ici** — à qualifier), et l'enrôlement/les pages humaines ne doivent pas être publiques. Le pont stdio (§ 8.2) reste la voie recommandée (`docs/DOSSIER.md:64`).

### 8.4 Agent Windows (`bambu-agent`)

Rôle : inventorier des dossiers Windows autorisés et transférer des fichiers (STL/OBJ/3MF/STEP, sorties de découpe) vers le serveur ; il **ne peut ni lancer ni arrêter une impression** (`docs/SECURITY.md` § 6). Appairage : `sudo atelier-admin agent pair` (équivalent de `bambud agent pair`, `cli.go:144-177`) ou la page `/agents/pair` d'un navigateur enregistré ; puis `bambu-agent pair --server <URL> --token <jeton>` sur le poste. **Non nécessaire en mode Linux seul** : le serveur découpe lui-même ; l'agent n'est utile que pour alimenter la bibliothèque depuis un poste Windows. Si on l'utilise : l'URL doit être joignable et son hôte reste hors garde Host, mais le trafic est en HTTP clair (secret et fichiers) hors Tailscale ; préférer HTTPS + `--server-fingerprint`.

---

## 9. Interface web : `web/`, pas `prototype/`

- `prototype/` est la maquette D0 **figée** : **ne pas la déployer** ni la servir (aucun code Go ne la référence).
- La vraie interface est **`web/`** (React/Vite/TypeScript, paquet `bambu-atelier-web`). Elle est **embarquée dans `bambud` au build** (`go:embed`, `internal/webui/webui.go:17`) : il n'y a rien à servir à côté, pas de reverse proxy statique, pas de Node à l'exécution.
- Commandes : § 2.1 (`cd web && npm ci && npm run build`, avec **Node ≥ 22.12**, ou ≥ 20.19). Résultat : `internal/webui/assets/` (SPA) et `internal/mcpserver/ui/views/mcp-app.html` (vue MCP App), tous deux ignorés par git, à regénérer à chaque build propre.
- **Corrigé** : avec un jeton d'API configuré, elle fonctionne dans un navigateur **enregistré** (cookie de session) ; un navigateur inconnu voit l'écran « Enregistrer ce navigateur » (§ 7.3).

---

## 10. Exploitation

### 10.1 Tests de fumée après déploiement (au-delà de `/health`)

**Script prêt à l'emploi** (`a33e96c`) : `scripts/smoke-linux.sh --url http://127.0.0.1:7878 --token-file /etc/atelier/atelier.env` (avec `sudo`) (le jeton est lu dans un fichier et passé à curl par stdin ; rien de secret dans la sortie). Il vérifie `GET`/`HEAD /health`, le refus sans jeton, `/api/capabilities` (version de Bambu Studio, nombre de modèles/machines/process/filaments chargés, CadQuery **réellement importé** par un `ping` du worker, mis en cache), MCP `initialize` + `tools/list` et les profils filament d'une machine (`--machine`, défaut P1S 0.4) ; il attend jusqu'à `--wait` secondes (90) le chargement des profils et le ping, `--no-cad` n'exige pas la CAO. Code de sortie 0/1. Le détail manuel ci-dessous reste valable.

`T` = jeton (jamais affiché), `B=http://127.0.0.1:7878` depuis le serveur.

1. `curl -fsS $B/health` → `{"schema_version":130,"status":"ok","version":"<describe>"}` (`HEAD /health` répond 200 aussi).
2. `curl -s -o /dev/null -w '%{http_code}' -X POST $B/mcp` → **401** ; idem `GET $B/api/capabilities` → 401.
3. `curl -fsS -H "Authorization: Bearer $T" $B/api/capabilities | jq '{mode, version, concurrency, f:[.features[]|{id,available}]}'` → `mode:"linux_seul"`, `slicing.available:true`, `cad_worker.available:true`, `catalog_import.available:true`, `concurrency` `{heavy:1, light:4}`, `windows_agent.available:false` (`core.go:278-315`). La réponse contient maintenant le bloc `studio` (`available`, `path`, `version`, `profiles_dir`, `profiles{models,machines,processes,filaments}` — absent « chargement des profils en cours » les premières secondes) et `cad_worker` reflète un **ping réel** du worker (`CadQuery <version> vérifié`), rafraîchi au plus toutes les 10 min (30 s après un échec).
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
- **Avec les mises à jour automatiques (§ 11), ces deux cas sont traités par le programme**, y compris la restauration de la sauvegarde quand le schéma a changé.

---

## 11. Mises à jour automatiques

> Statut : **désactivées par défaut**. Rien ne change sur un serveur tant que l'administrateur n'a pas lancé la commande du § 11.3. Les installations existantes (`install.sh` sans `--auto-update`) sont inchangées.

### 11.1 Principe et garanties

Le serveur **tire** les mises à jour : il interroge GitHub (`git fetch` + API publique). Rien n'est ouvert en entrée, aucune clé de déploiement ni webhook n'existe, ce qui convient à un serveur privé derrière Tailscale. Un timer systemd lance un passage toutes les 5 minutes ; un passage sans nouveauté ne fait qu'un `git fetch` et ne journalise rien.

| Garantie | Mécanisme |
|---|---|
| Seul un commit de `main` dont **toute la CI est verte** est installé | API GitHub `check-runs` + statut combiné du commit ; rouge, annulé, en cours, inconnu (réseau, limite de débit) : on attend, on réessaie au passage suivant (`internal/upgrade/ci.go`) |
| Redémarrage sans interrompre l'impression | `GET /api/system/busy` du service (§ 11.4) : une impression **en cours** n'empêche plus la mise à jour (le fichier est sur la carte SD de l'imprimante ; `ATELIER_UPDATE_ALLOW_DURING_PRINT=1` par défaut, `0` pour attendre). Restent bloquants : dépôt FTPS / découpe, confirmation, arrêt ou action en attente, lancement en cours d'accusé, travail lourd ; état **indéterminé** : reporté |
| Rien n'est touché avant d'avoir un binaire testé | construction dans `releases/<sha>`, tests `go test -short`, validateur MCP ; un échec laisse le service intact |
| Retour possible | sauvegarde `bambud backup` + `bambud verify` (avec `.sha256`) juste avant la bascule ; bascule par lien symbolique atomique ; versions précédentes conservées |
| Retour arrière automatique | `/health` (version + `schema_version` attendus, stabilité 20 s), puis `scripts/smoke-linux.sh`, puis, si le worker CAO est configuré, **vérification de sa version** (§ 11.11) : un échec ramène l'ancienne version, **avec restauration de la sauvegarde si le schéma de la base a changé** (§ 11.7) |
| Une seule mise à jour à la fois | verrou `flock` `/run/lock/atelier-update.lock` (pris aussi par `install.sh`) |
| Pas de boucle de redémarrages | un commit qui échoue ici est mémorisé et **n'est plus retenté** tant qu'un nouveau commit n'arrive pas (ou `--retry`) |
| Historique de `main` non réécrit | le nouveau commit doit prolonger l'installé (`merge-base --is-ancestor`) ; sinon refusé et notifié |

### 11.2 Architecture choisie et pourquoi

Une **sous-commande Go** (`bambud upgrade --auto`, paquet `internal/upgrade`) plutôt qu'un script shell :

- la logique de décision (CI, occupé, désactivé, épinglé, fenêtre, échec connu) et le scénario de **retour arrière avec restauration** sont des fonctions pures / pilotées par une interface `Env` : elles sont testées sur Windows, macOS et Linux, en CI, avec un environnement simulé et, pour les liens symboliques, sur Linux ;
- le JSON de GitHub est lu par un vrai parseur (pas de `grep`) ; le jeton ne sort jamais dans un message d'erreur ;
- les notifications réutilisent le système existant de `bambud` (`internal/notify`) ;
- les effets de bord réels (git, npm, go, systemctl, runuser) sont isolés dans `internal/upgrade/sysenv.go` et `build.go` derrière des commandes enregistrables.

Les parties qui sont naturellement du shell (migration d'une installation, unités systemd) sont dans `deploy/linux/enable-auto-update.sh`, testé par `scripts/test-enable-auto-update.sh`.

```
/opt/atelier/
  bin/bambud, bambu-mcp, printer-sim   -> ../current/bin/...      (liens ; atelier.service ne change pas)
  bin/atelier-admin, atelier-backup.sh                              (inchangés)
  releases/<sha>/{bin,geometry,scripts,REVISION,.ready}             (construit, testé, root:root)
  releases/legacy-<date>/                                           (copie de l'installation d'avant l'automatique)
  current  -> releases/<sha>          previous -> version précédente
  updater/bambud       (outil exécuté EN ROOT par le timer ; jamais remplacé par du code téléchargé : seul l'administrateur le met à jour)
/var/lib/atelier-update/   repo (clone git), build/, state.json     (propriété de atelier-update)
/etc/atelier/auto-update.env, auto-update.disabled, auto-update.pin, github-token (facultatifs)
```

Privilèges : l'orchestrateur tourne en root (bascule du lien, `systemctl`, ménage) mais **tout le code téléchargé** (`git`, `npm ci`, `npm run build`, `go build`, `go test`, validateur) s'exécute sous l'utilisateur `atelier-update`, avec un environnement vidé (aucun `BAMBU_*`, aucun jeton) et sans accès à `/var/lib/atelier` ni `/etc/atelier` (modes 0750). La sauvegarde, la restauration, les notifications **et le test de fumée** (un script du dépôt) s'exécutent sous `atelier`, jamais en root ; le jeton d'API est remis au test par un fichier 0600 temporaire, supprimé ensuite. L'outil de mise à jour lui-même, exécuté en root par le timer, **n'est pas remplacé automatiquement** par la version construite (sinon un commit malveillant obtiendrait root au passage suivant) : il est rafraîchi par `enable-auto-update.sh` ou `install.sh` (actions d'administrateur) ou, à vos risques, avec `ATELIER_UPDATE_REFRESH_UPDATER=1` ; il est donc **plus ancien que le service** après quelques mises à jour, ce qui est sans conséquence (il lit la base sans migration et ne dépend pas du schéma). Les processus de `atelier-update` sont tués avant la publication d'une version. L'unité est à priorité minimale (`Nice=19`, `IOSchedulingClass=idle`, `CPUWeight=20`) pour ne pas gêner le suivi de l'imprimante.

### 11.3 Activation (une commande)

Prérequis (tous installés pour **tous** les utilisateurs, pas dans un dossier personnel) : `git`, `curl`, `go` (version de `go.mod`), `node` ≥ 22.12, `npm`, `runuser`, `flock`. Le script le vérifie et s'arrête sans rien modifier s'il manque quelque chose.

```bash
cd <dépôt cloné sur le serveur>            # ou n'importe où si --from est donné
sudo bash deploy/linux/enable-auto-update.sh
```

Options utiles : `--window 07:00-23:00` (jamais la nuit), `--smoke required|advisory|off`, `--keep-releases N`, `--from <livraison>` (binaire déjà construit au lieu de compiler l'outil), `--assume-sha <sha>` (commit de la version installée), `--no-start`. Idempotent : relancer ne change rien (vérifié par le harnais). Avec `install.sh` : `sudo ./install.sh --from … --auto-update [--update-window 07:00-23:00]`.

Ce que fait la commande, **sans arrêter ni redémarrer le service** :

1. contrôles préalables (rien n'est modifié s'ils échouent) ;
2. crée l'utilisateur système `atelier-update` et `/var/lib/atelier-update` ;
3. copie l'installation actuelle dans `releases/legacy-<date>` (version, commit si connu, schéma de la base dans `REVISION`) et crée `current` ;
4. remplace `bin/bambud`, `bin/bambu-mcp`, `bin/printer-sim` par des **liens atomiques** vers `current/bin/*` : le processus en marche garde son ancien fichier ouvert, l'unité `atelier.service`, `atelier-admin` et la sauvegarde quotidienne suivent `current` ;
5. installe l'outil (`/opt/atelier/updater/bambud`), la commande `atelier-update`, `auto-update.env`, `atelier-update.service` et `atelier-update.timer` ;
6. joue `scripts/smoke-linux.sh` sur la version en service : s'il échoue **déjà** aujourd'hui, le mode passe à `advisory` (alerte sans retour arrière) pour ne pas annuler chaque mise à jour, et le script le dit ;
7. active le timer et fait un contrôle à blanc (`atelier-update --check`).

Le service ne sera redémarré qu'à la **première** mise à jour réellement installée ; une impression en cours n'est pas interrompue (§ 11.4).

### 11.4 Quand une mise à jour est-elle reportée ? Définition de « occupé »

`GET /api/system/busy` (lecture seule ; connexion TCP loopback **sans** en-tête de proxy, plus le jeton d'API ; absent de `/health`, public) renvoie `{"busy": bool, "reasons": [{"code","detail"}]}` : c'est la liste **brute** des raisons ; l'outil de mise à jour (`internal/upgrade/busy.go`, `ApplyBusyPolicy`) en retire celles que la politique tolère. Codes (`internal/printing/busy.go`, `internal/core/busy.go`) :

| Code | Signifie | Bloque la mise à jour ? |
|---|---|---|
| `print_running` | un PrintJob en `PRINTING` : le fichier est sur la carte SD, le suivi reprend après redémarrage | **non** par défaut (`ATELIER_UPDATE_ALLOW_DURING_PRINT=1`), oui si `0` |
| `printer_active` | un adaptateur ouvert voit l'imprimante en préparation / impression / pause (même lancée depuis Bambu Studio ou l'écran) | **non** par défaut, oui si `0` |
| `print_active` | un PrintJob en `START_REQUESTED` (accusé du lancement en cours) ou `START_UNKNOWN` (lancement incertain : à trancher par l'humain, `reconcile_print`) | oui, toujours |
| `confirmation_pending` | une confirmation de lancement émise et non consommée (valable 10 min au plus) | oui |
| `stop_pending` | une demande d'arrêt attend l'humain (10 min au plus), ou un arrêt est en cours d'application | oui |
| `action_pending` | reprise / chargement / nettoyage de carte SD en attente de validation | oui |
| `deposit_in_progress` | découpe ou dépôt FTPS du fichier sur l'imprimante en cours (`UPLOADING`, `SLICING`) | oui |
| `job_running` | un travail de la file (découpe, analyse, CAO, import) s'exécute | oui |

**Pourquoi l'impression peut continuer.** Redémarrer `bambud` ne coupe pas l'imprimante : le G-code est sur sa carte SD et c'est elle qui exécute. Au démarrage, `Recover` remet chaque impression `PRINTING` sous suivi (reconnexion MQTT, rapport complet, réconciliation) et ne renvoie **aucune commande** (ni lancement, ni arrêt, ni pause, ni reprise) ; l'objectif est que le suivi (carte « ça en est où ? ») revienne tout seul après quelques secondes. Un lancement en cours d'accusé (`START_REQUESTED`) devient `START_UNKNOWN` s'il est interrompu : c'est pourquoi il bloque. Après un redémarrage, le journal de la mise à jour dit « impression en cours : la mise à jour continue » ; une notification `installed` reste envoyée comme d'habitude. Pendant l'impression, les vérifications de santé et de fumée (`/health`, `scripts/smoke-linux.sh`) ne pilotent pas l'imprimante.

Si le service ne répond pas (arrêté) ou est trop ancien pour connaître la route, repli sur la **lecture seule de la base** (`bambud upgrade-probe` : impressions actives et travaux en cours, sans les demandes humaines en mémoire). Si tout est illisible : **traité comme occupé**. La vérification est faite avant la construction **et** de nouveau juste avant la sauvegarde : une impression démarrée pendant la construction (plusieurs minutes) reporte l'installation, la version construite est conservée pour le passage suivant. Un report est notifié **une fois** par commit.

La fenêtre horaire (`ATELIER_UPDATE_WINDOW`) s'applique à l'**installation** (le redémarrage), heure locale du serveur ; elle peut passer minuit (`22:00-06:00`).

### 11.5 Réglages, interrupteur, épingle

Fichier `/etc/atelier/auto-update.env` (aucun secret ; relu à chaque passage) :

| Variable | Défaut | Rôle |
|---|---|---|
| `ATELIER_UPDATE_REPO` / `_BRANCH` | dépôt public / `main` | ce qui est suivi |
| `ATELIER_UPDATE_WINDOW` | vide | plage horaire d'installation `HH:MM-HH:MM` |
| `ATELIER_UPDATE_REQUIRED_CHECKS` | vide (tous) | noms exacts des jobs de CI exigés, séparés par `;` |
| `ATELIER_UPDATE_ALLOW_DURING_PRINT` | `1` | `1` : une impression en cours (`print_running`, `printer_active`) ne retarde pas la mise à jour ; `0` : attendre sa fin (§ 11.4) |
| `ATELIER_UPDATE_KEEP_RELEASES` / `_KEEP_BACKUPS` | 3 / 5 | versions et sauvegardes `preupdate-*` conservées (la sauvegarde quotidienne a sa propre rotation) |
| `ATELIER_UPDATE_SMOKE` | `required` | `advisory` : alerte sans retour arrière ; `off` |
| `ATELIER_UPDATE_HEALTH_WAIT` | 180s | attente max. de `/health` après redémarrage |
| `ATELIER_UPDATE_PATH` | écrit à l'activation | PATH de l'utilisateur de construction |
| `ATELIER_UPDATE_REFRESH_UPDATER` | vide | `1` : remplace l'outil exécuté en root par celui de la version installée (**déconseillé**, § 11.2) |
| `ATELIER_UPDATE_TOKEN_FILE` | `/etc/atelier/github-token` | jeton GitHub **facultatif**, lecture seule, droits **600** (refusé sinon) : relève la limite de débit (60 requêtes/heure sans jeton ; l'API n'est appelée que lorsqu'un nouveau commit existe) |

- **Désactiver** : `sudo touch /etc/atelier/auto-update.disabled` (ou `sudo bash deploy/linux/enable-auto-update.sh --disable`) ; réactiver : `rm` du fichier (ou `--enable`). Désactivé, un passage ne fait **rien**, pas même un `git fetch`. Seule une mise à jour **déjà commencée** est toujours menée à son terme.
- **Épingler une version** : `sudo atelier-update --pin current` (ou `--pin <sha>`) ; retirer : `--unpin`. Un commit épinglé doit appartenir à `origin/main` et avoir une CI verte ; le schéma ne peut pas descendre en dessous de celui de la base.
- `sudo atelier-update --status` (installé, précédent, épingle, interrupteur, dernier passage, commits en échec), `--check` (explique ce qui serait fait, ne change rien), `--now` (ignore interrupteur et fenêtre ; CI verte et service libre restent exigés), `--retry` (retente un commit déjà échoué).

### 11.6 Journaux et notifications

- Journal : `journalctl -u atelier-update -f` (un passage sans nouveauté n'écrit rien ; un report, un refus, chaque étape d'une installation et chaque échec sont écrits en clair) ; `systemctl list-timers atelier-update.timer` ; état : `/var/lib/atelier-update/state.json`.
- Les échecs (construction, retour arrière, critique) font échouer l'unité : `systemctl --failed`.
- Notifications : évènement **`system_update`** sur les canaux ntfy / webhook / e-mail existants (page `/setup/notifications`). Pour les canaux à **liste d'évènements explicite**, ajouter `system_update` à l'abonnement. Genres : mise à jour installée, report « serveur occupé », CI rouge, construction échouée, retour arrière, retour arrière avec restauration, échec critique, refus, test de fumée en alerte. Aucun secret n'y figure. La commande `bambud upgrade-notify` ouvre la base **sans migration**.

### 11.7 Retour arrière, y compris quand le schéma a changé

Avant la bascule, la nouvelle version a été construite et testée mais n'a **pas encore touché la base**. Le service n'applique ses migrations qu'à son premier démarrage. Après la bascule :

1. **Échec sans migration** (démarrage impossible, `/health` KO, mauvaise version, test de fumée) : arrêt, `current` remis sur l'ancienne version, redémarrage, `/health` attendu sur l'ancienne version ; **les données ne sont pas touchées**.
2. **Échec après migration** : l'ancien binaire **refuse** une base plus récente que lui (`store.go`) : un simple retour du lien le ferait tourner en boucle. Le programme relit `schema_version` de la base (lecture seule) ; s'il dépasse celui d'avant la mise à jour, il **arrête le service, restaure la sauvegarde d'avant (`bambud restore --force` avec l'ANCIEN binaire), contrôle (`bambud verify --quick`), remet le lien, redémarre**. Les écritures faites par la version échouée (quelques secondes ou minutes) ne sont **pas détruites** : elles sont conservées à part dans `/var/lib/atelier/.pre-restore-<date>/` (base, blobs, secrets).
3. **Retour arrière impossible** (restauration ou ancienne version qui ne répond pas) : l'état reste « en cours », une alerte **critique** est envoyée **une fois**, le passage suivant retente ; voir § 11.8.
4. **Coupure pendant la bascule** (reboot, unité tuée) : un journal d'intention est écrit avant le lien. Au passage suivant, la nouvelle version est vérifiée (sinon retour arrière avec les mêmes informations), même si l'automatique a été désactivé entre-temps.
5. Une base plus récente que le binaire visé (`schema` du binaire < `schema_version`) : mise à jour **refusée** (un retour en arrière de version est impossible sans restaurer une sauvegarde).

Retour arrière **manuel** vers la version précédente (schéma inchangé) : `sudo ln -sfn "$(readlink /opt/atelier/previous)" /opt/atelier/current.new && sudo mv -T /opt/atelier/current.new /opt/atelier/current && sudo systemctl restart atelier`, puis `sudo atelier-update --pin current` pour que l'automatique ne la remplace pas. Si le schéma a changé, restaurer d'abord la sauvegarde `/var/backups/atelier/preupdate-*.tar.gz` avec l'ancien binaire (§ 10.5).

### 11.8 Que faire si ça casse

1. `sudo atelier-update --status` puis `journalctl -u atelier-update -n 200 --no-pager` : la cause est écrite (commande, dernières lignes de sortie).
2. Service en marche mais dans la mauvaise version : `curl -s http://127.0.0.1:7878/health` (version, `schema_version`) ; `readlink /opt/atelier/current /opt/atelier/previous`.
3. Service arrêté en boucle après une alerte critique : `systemctl stop atelier`, identifier la sauvegarde `ls -t /var/backups/atelier/preupdate-*`, `sudo -u atelier /opt/atelier/releases/<ancienne version>/bin/bambud restore --from <archive> --data-dir /var/lib/atelier --force`, `… verify --data-dir /var/lib/atelier`, remettre `current` (§ 11.7), `systemctl start atelier`. Les données de la version échouée sont dans `.pre-restore-*`.
4. Un commit qui échoue **chaque fois** : il est déjà mis de côté. Corriger sur `main` (nouveau commit = nouvel essai), ou geler : `sudo atelier-update --pin current`.
5. Tout arrêter : `sudo touch /etc/atelier/auto-update.disabled`.
6. Construction qui échoue sur le serveur seulement (outil introuvable pour `atelier-update`) : `sudo runuser -u atelier-update -- env -i PATH="$(grep ^ATELIER_UPDATE_PATH /etc/atelier/auto-update.env | cut -d'"' -f2):/usr/bin" go version`.
7. Désinstaller le mécanisme : `deploy/linux/uninstall.sh` retire aussi `releases/`, `updater/` et les unités ; les données ne sont jamais supprimées.

### 11.9 Ce qui est prouvé par les tests, et ce qui ne l'est que sur le vrai serveur

**Prouvé (CI et `go test`)** : décision (CI verte / rouge / en cours / inconnue, occupé, indéterminé, désactivé, épinglé, rien de neuf, fenêtre horaire, échec connu, `--now`, `--retry`) ; occupation (`internal/core/busy_test.go`, `internal/printing/busy_test.go`, `internal/httpapi/busy_test.go` : loopback seul, proxy refusé, jeton) ; lecture de la CI contre un faux GitHub (pagination, limite de débit, jeton jamais divulgué) ; ordre exact et hygiène des commandes de construction, échec à chaque étape sans rien publier ; **retour arrière simulé** : `/health` en échec (schéma inchangé), migration puis échec (restauration avant relance de l'ancienne version), restauration en échec (critique, une seule alerte), reprise après coupure ; moteur complet avec de vrais liens symboliques (`e2e_test.go`, Linux) ; git réel (fetch, ancêtre, historique réécrit) ; commandes réelles `backup`/`verify`/`restore` sur le scénario « base migrée refusée par l'ancien binaire » (`cmd/bambud/upgrade_cli_test.go`) ; migration d'une installation sans interruption, idempotence, interrupteurs, installation neuve inchangée (`scripts/test-enable-auto-update.sh`, `scripts/test-install-linux-container.sh`, joués dans un conteneur Linux et dans la CI).

Pendant une impression : `TestApplyBusyPolicy` (chaque raison, bloquante ou tolérée), `TestSysEnvBusyAllowsPrintButNotDeposit`, `TestLoadConfigAllowDuringPrint`, et de bout en bout avec le simulateur `TestRestartDuringPrintKeepsTrackingWithoutCommands` (`internal/printing`) : impression en cours, arrêt puis réouverture du service sur le même dossier de données, l'impression reste `PRINTING`, son suivi reprend et le simulateur n'a reçu **aucune** commande. Worker CAO (§ 11.11) : `internal/upgrade/duringprint_worker_test.go`. Scripts : `scripts/test-enable-auto-update.sh` (**à jouer sur Linux**, il saute sous Windows).

**À valider sur le vrai serveur Zorin / Ubuntu 22.04** : `useradd` et `runuser` réels, droits 0750 empêchant `atelier-update` de lire les données ; durée et mémoire réelles de `npm ci` + `go test` sur 2 cœurs (priorité minimale) ; durcissement `ProtectSystem=strict` de l'unité avec `systemctl` ; accès réel à l'API GitHub depuis Tailscale ; `Restart=on-failure` de `atelier.service` pendant un retour arrière ; la première mise à jour réelle (à lancer d'abord avec `atelier-update --check`, puis `--now`, imprimante au repos) ; la valeur réelle de `/api/system/busy` avec la P1S en pleine impression (`print_running`, `printer_active`) ; **une vraie mise à jour installée pendant une impression** (la P1S continue, la carte de suivi revient seule, aucune commande envoyée) ; `sudo bash deploy/linux/enable-auto-update.sh` (contrôles `go version` et `runuser` depuis `/` sous un `$HOME` illisible) ; `systemctl show atelier -p Environment -p EnvironmentFiles` avec le drop-in réel (`UV_PROJECT_ENVIRONMENT`, `UV_FROZEN`) et la réinstallation non éditable de `bambu-geometry`.

### 11.10 Limites assumées

- Une impression lancée par l'humain **entre** la dernière vérification d'occupation et le redémarrage (quelques secondes, la sauvegarde comprise) n'est pas retenue ; le redémarrage n'interrompt pas l'impression sur l'imprimante, mais le suivi est repris au démarrage (§ 10.3). Comme une impression en cours est désormais tolérée, c'est le cas général : seules les demandes humaines en attente et les lancements en cours d'accusé retardent l'installation.
- Pendant le redémarrage (quelques secondes), la carte de suivi, la caméra du service et les notifications de fin ne sont pas servies ; une impression qui se termine exactement à ce moment est constatée à la reconnexion.
- `START_UNKNOWN` bloque les mises à jour tant que l'humain n'a pas tranché : c'est voulu (on ignore si l'imprimante imprime).
- Les commits ne sont pas vérifiés par signature GPG : la confiance repose sur la branche `main` et sa CI (docs/SECURITY.md § 16, protection de branche recommandée).
- Le worker géométrie (`workers/geometry`) n'est resynchronisé que si son arbre git a changé ; `uv sync` y est non bloquant (le test de fumée tranche, § 11.11).
- Un serveur hors ligne ne se met pas à jour (le passage journalise « réseau indisponible » et sort sans erreur).

### 11.11 Worker CAO : environnement du service et vérification de la version

En production l'environnement Python du worker est **hors** de son dossier (`UV_PROJECT_ENVIRONMENT=/var/lib/atelier/venvs/geometry`, `UV_FROZEN=1`, posés par un drop-in systemd du service) et le paquet `bambu-geometry` y est installé **non éditable** : copier le nouveau code dans `/opt/atelier/cad/geometry` ne suffit pas, le worker continuerait d'exécuter l'ancien paquet. Quand `workers/geometry` change, l'outil de mise à jour :

1. lit l'environnement **du service** (`systemctl show atelier -p Environment -p EnvironmentFiles`, drop-ins compris, puis les fichiers d'environnement déclarés ; `Environment=` l'emporte) et en reprend toutes les variables `UV_*` ; sans `UV_PROJECT_ENVIRONMENT` ni `.venv` dans le dossier, il applique la même règle que le service (`<données>/venvs/geometry`) ;
2. lance, sous l'utilisateur du service, `uv sync --frozen --no-dev --extra cad --reinstall-package bambu-geometry` ;
3. après redémarrage, le test de fumée lance `uv run … python -m bambu_geometry ping` **avec ce même environnement** et compare l'**empreinte du code** que le worker rapporte (`worker.code_sha256`, SHA-256 des fichiers `.py` du paquet réellement importé : `workers/geometry/src/bambu_geometry/fingerprint.py`) à celle du code de la version installée (`internal/upgrade/worker.go`, `WorkerFingerprint`). Un paquet resté sur l'ancien code, ou un worker sans empreinte, fait **échouer** le test de fumée : retour arrière, avec réinstallation du paquet depuis l'ancien code.

Autres corrections de `enable-auto-update.sh` et de l'outil : le contrôle du compte de construction appelle `go version` (go n'accepte pas `--version`), et toute commande lancée sous un autre utilisateur (`runuser`, `sudo -u`) part de `/` au lieu du dossier courant de root (souvent `/home/<admin>`, illisible : « go: cannot determine current directory »).

---

## À corriger côté code

Constats d'origine vérifiés, **traités dans l'ordre** par les commits ci-dessous (aucun n'affaiblit `docs/SECURITY.md` : aucune approbation de lancement ou d'arrêt hors des pages humaines).

| # | Gravité | Constat (origine) | Statut |
|---|---|---|---|
| 1 | **Haute** | SPA et appels `/api/*` en 401 pour tout navigateur dès qu'un jeton est configuré | **Corrigé** — `e95e4d5` + `430c06e` : cookie de session = authentification de `/api/*` (écritures : `X-Atelier-Request: 1` + same-origin), SPA et `/assets/*` publiques, écran « Enregistrer ce navigateur », `/mcp` Bearer seulement |
| 2 | Moyenne | `/mcp` 403 `invalid Host header` derrière un proxy loopback | **Corrigé** — `e95e4d5` : `DisableLocalhostProtection` (le garde Host reste maître) ; test loopback + `Host` public |
| 3 | Moyenne | Jeton exigé seulement hors loopback | **Corrigé** — `e95e4d5` : `--require-token`/`BAMBU_REQUIRE_TOKEN`, et jeton obligatoire si `--public-url` ou `--allowed-hosts` désigne un hôte non loopback |
| 4 | Moyenne | Cookie `Secure` ignorant `X-Forwarded-Proto`, une seule URL publique | **Corrigé** — `e95e4d5` : `X-Forwarded-Proto` d'un proxy de confiance seulement (`--trusted-proxies`, défaut loopback) ; `BAMBU_PUBLIC_URL` accepte une liste (la première pour les liens, schéma selon le `Host`) |
| 5 | Moyenne | Verrou d'enrôlement global (déni de service) | **Corrigé** — `e95e4d5` : verrou par adresse client (backoff 5 min → 1 h) + plafond global haut (60 échecs, 1 → 15 min) |
| 6 | Faible | `HEAD /health` → 401 | **Corrigé** — `e95e4d5` |
| 7 | Faible | `/api/capabilities` sans version de Studio ni profils ; `cad_worker` non vérifié | **Corrigé** — `77e50b2` (commande `ping` du worker, cache) + `a5e60cb` (bloc `studio`, comptes de profils) |
| 8 | Faible | `inspect_environment` lit `BAMBU_STUDIO_PATH`/`BAMBU_PYTHON` | **Corrigé** — `a5e60cb` : reflète `BAMBU_STUDIO_BIN`, `BAMBU_UV_PATH`, `BAMBU_GEOMETRY_DIR` réellement résolus |
| 9 | Faible | `BAMBU_STUDIO_BIN` invalide ignorée | **Corrigé** — `e95e4d5` (erreur de démarrage) + `a5e60cb` (`core.Open`) |
| 10 | Faible | Marqueurs de fusion dans `.gitignore` | **Corrigé** — `26eae78` |
| 11 | Faible | `uv run` sans `--frozen`/`--no-dev`, écriture possible dans le projet | **Corrigé** — `77e50b2` + `f3063fb` : `uv run --frozen --no-dev --extra cad`, `UV_PROJECT_ENVIRONMENT` respectée sinon `<data>/venvs/geometry`, `PYTHONDONTWRITEBYTECODE=1` |
| 12 | Faible | Racine d'import contenant le dossier de données | **Corrigé** — `77e50b2` : refus au démarrage (racine égale, parente ou enfant du dossier de données, donc de `secrets/`) |
| 13 | Faible (informatif) | `/agent/v1/*` hors garde Host, sans limitation de débit | Ouvert (inchangé : option pour désactiver `/agent/v1` sans agent Windows) |
| 14 | Faible (informatif) | Aucun TLS natif | Ouvert (inchangé : utiliser `tailscale serve`/proxy TLS) |
| — | Défaut | Wrapper `xvfb-run -a` activé implicitement | **Corrigé** — `e95e4d5` : jamais implicite, seulement `BAMBU_STUDIO_WRAPPER`/`--studio-wrapper` (`none` accepté) |
| — | Outil | Pas de test de fumée de déploiement | **Ajouté** — `a33e96c` : `scripts/smoke-linux.sh` |

---

## Résumé pour le script de déploiement

1. **Build web obligatoire avant `go build`** : Node ≥ 22.12 (ou ≥ 20.19) ; `cd web && npm ci && npm run build` ; contrôler `internal/webui/assets/index.html` et `internal/mcpserver/ui/views/mcp-app.html` (ignorés par git, sinon « interface non construite »). Ne pas exporter `BAMBU_STUDIO_BIN` pendant `go test -short ./...` (Studio 02.09 vs tests écrits sur 02.08), `TMPDIR="$(mktemp -d)"`.
2. **Unité systemd** : `BAMBU_API_TOKEN` (EnvironmentFile 600, ≥ 16 car.), `BAMBU_REQUIRE_TOKEN=1` derrière un proxy ; `UV_PROJECT_ENVIRONMENT=/var/lib/atelier/venvs/geometry` **pré-synchronisé** (`uv sync --frozen --no-dev --extra cad`) ; plus besoin de `UV_NO_DEV`/`BAMBU_STUDIO_WRAPPER=none` (aucun xvfb implicite), `UV_FROZEN=1` facultatif ; `TimeoutStopSec=60`, `Restart=on-failure` avec `StartLimitIntervalSec/StartLimitBurst`.
3. **Chemins** : `BAMBU_IMPORT_ROOTS` **hors** de `BAMBU_DATA_DIR` (ex. `/srv/atelier/import`) sinon refus de démarrer ; `BAMBU_STUDIO_BIN` doit exister (sinon erreur au démarrage, pas de repli) ; `BAMBU_GEOMETRY_DIR` peut rester en lecture seule (bambud n'y écrit jamais).
4. **Accès** : `BAMBU_ALLOWED_HOSTS` (noms sans port) et `BAMBU_PUBLIC_URL` = liste `https://atelier.example.ts.net,http://192.168.1.50:7878` (la première sert aux liens ; le cookie `Secure` suit l'hôte de la requête) ; `BAMBU_TRUSTED_PROXIES` seulement si le proxy n'est pas en loopback ; ne jamais saisir le code d'accès de l'imprimante en `http://` clair sur le LAN.
5. **Interface web** : utilisable avec le jeton configuré — enregistrer chaque navigateur une fois (`sudo atelier-enroll` / `bambud enroll`, code saisi sur `/enroll`) ; le cookie dure 30 jours, révocable (`enroll --revoke`). `/mcp` reste Bearer seulement ; les pages `/confirm`, `/stop`, `/setup` sont inchangées.
6. **Funnel / `tailscale serve`** : `/mcp` fonctionne maintenant derrière un proxy loopback (Host public autorisé) et le jeton est exigé d'office ; n'exposer **jamais** `/agent/v1`, `/enroll`, `/setup`, `/confirm`, `/stop`, `/agents/pair` sur Internet.
7. **Health check** : `GET` ou `HEAD /health` (sans jeton) ; puis `scripts/smoke-linux.sh --url … --token-file …` (Studio, profils, CadQuery par ping, MCP `tools/list`, profils filament) — attendre jusqu'à 90 s au premier démarrage.
8. **Sauvegarde** : préférer `bambud backup` + `bambud verify` (cohérent base/blobs/secrets) ; sinon exclure `*.db-wal`, `*.db-shm`, `work/`, `tmp/`, `blobs/tmp/`, `agent-staging/`, `.bambud.lock`, `venvs/` (reconstructible) ; secrets dans le même instantané.
9. **Retour arrière** : sûr par remplacement du binaire tant que `schema_version` est inchangé (130 → 130) ; sinon restaurer la sauvegarde d'avant (`bambud restore --force`). Ne pas lancer `atelier-enroll`/`backup` avec le nouveau binaire avant le redémarrage si une migration est possible.
10. **Reste ouvert** : `/agent/v1/*` hors garde Host et sans limite de débit (point 13), aucun TLS natif (point 14) — rester sur LAN + Tailscale.


## Annexe : connecteur distant de Claude (OAuth) et Docker

- Exposer bambud à claude.ai / au mobile **sans jeton collé** : OAuth 2.1 intégré, `docs/OAUTH.md` (Funnel : n'exposer que `/mcp`, `/.well-known/*`, `/oauth/*` ; `BAMBU_PUBLIC_URL` en https ; `bambud oauth --list|--revoke`). Migration ajoutée : `0190_oauth.sql` (le `schema_version` de `/health` passe à 190 ou plus : retour arrière = restaurer la sauvegarde d'avant mise à jour, voir plus haut).
- Alternative sans installation sur l'hôte : image Docker, `docs/QUICKSTART.md`.

## Caméra et notifications (chantier D)

- **Flux sortants** : le serveur ouvre en plus **TCP 6000** vers chaque P1/A1 (caméra, TLS, mêmes identifiants `bblp` + code d'accès), et ce qu'il faut pour les canaux choisis : HTTPS vers le serveur ntfy ou le webhook, SMTP (587 STARTTLS ou 465) vers le serveur de messagerie.
- **Variables** : `BAMBU_CAMERA_PORT` (simulateur seulement : port caméra différent de 6000) ; `BAMBU_NOTIFY_<CANAL>_URL`, `_HMAC_KEY`, `_TOKEN`, `_PASSWORD` et leurs variantes `_FILE` (chemin d'un fichier en 0600 contenant la valeur) ; elles l'emportent sur le coffre. Nom du canal en majuscules, non alphanumériques remplacés par `_`.
- **Empreinte** : la caméra réutilise l'empreinte épinglée pour MQTT. Si la vraie P1S présente un autre certificat sur le port 6000, ouvrir `/setup/camera/{id}` (navigateur enregistré) et épingler l'empreinte de la caméra.
- Migration `0170_camera_notify.sql` ; les photos sont des blobs (sauvegardées si référencées par `camera_photos`).
