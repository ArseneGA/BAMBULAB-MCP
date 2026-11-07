# Démarrage rapide (Docker)

Pour qui : une personne qui a une imprimante Bambu Lab (P1P, P1S, X1, A1… en mode LAN) et veut l'Atelier chez elle en quelques minutes, sans compiler quoi que ce soit. Tout est dans une image : bambud, l'interface web, la vue 3D pour Claude, Bambu Studio (découpe) et le worker CAO.

Prérequis : Docker (Docker Desktop ou Docker Engine + Compose), ~6 Gio de disque pour l'image, 4 Gio de mémoire disponibles pour les découpes et imports STEP.

> Rien n'est publié ni exposé par ces commandes. L'image se construit **localement** ; Bambu Studio est téléchargé depuis la page officielle des versions de Bambu Lab pendant la construction (version figée, empreinte SHA-256 vérifiée : voir le `Dockerfile`).

## 1. Construire et démarrer

```sh
git clone https://github.com/ArseneGA/BAMBULAB-MCP.git
cd BAMBULAB-MCP
docker compose build        # 10 à 20 min la première fois (Studio ≈ 250 Mio, CadQuery, interface web)
docker compose up -d
docker compose ps           # l'état passe à « healthy » en une minute
curl http://localhost:7878/health
```

`/health` répond `{"status":"ok", …, "mcp_app_views": true}`. `mcp_app_views: false` signifie que la vue 3D de Claude n'est pas dans le binaire (ne devrait pas arriver avec cette image).

Bambu Studio dans l'image : `docker compose exec atelier sh -c 'BAMBU_TOKEN=$(cat /data/api-token); curl -s -H "Authorization: Bearer $BAMBU_TOKEN" localhost:7878/api/capabilities'` montre la version de Studio et le nombre de profils chargés.

## 2. Enregistrer votre navigateur (une fois)

Lancer et arrêter une impression exigent un accord **humain** depuis un navigateur enregistré (jamais Claude) :

```sh
docker compose exec atelier bambud enroll       # affiche un code à 8 caractères, usage unique
```

Ouvrir <http://localhost:7878/enroll>, saisir le code. Le jeton d'API (pour Claude) se lit avec :

```sh
docker compose exec atelier cat /data/api-token
```

## 3. Ajouter votre imprimante

Dans l'interface (<http://localhost:7878>, onglet Impression) ou dans Claude (« ajoute mon imprimante P1S à l'adresse 192.168.1.20, numéro de série 01P00A000000000 »). Le **code d'accès LAN** de l'imprimante (écran de l'imprimante > Réseau) se saisit uniquement sur la page locale `/setup/printer/…` ; il n'est jamais transmis à Claude. Les valeurs ci-dessus sont des exemples : mettez les vôtres.

Le conteneur se connecte **à** l'imprimante (MQTT 8883, FTPS 990) : le réseau Docker par défaut suffit. Seule la découverte automatique exige `network_mode: host` (Linux), voir `docker-compose.yml`.

## 4. Brancher Claude

Choisir selon l'endroit où tourne Claude :

| Claude | Comment |
|---|---|
| **Claude Code** sur la même machine | `claude mcp add --transport http bambu http://localhost:7878/mcp --header "Authorization: Bearer <jeton>"` |
| **Claude Desktop** sur la même machine | `deploy/windows/add-atelier-npx.ps1 -Server http://localhost:7878/mcp` (Windows ; jeton dans un fichier protégé) ou `deploy/windows/claude-desktop.md` |
| **claude.ai, mobile, Desktop** (connecteur distant) | adresse https publique + OAuth : `docs/OAUTH.md` (Tailscale Funnel, n'exposer que `/mcp`, `/.well-known/*`, `/oauth/*`) |

Essayer : « montre-moi mes modèles en 3D » (la vue 3D s'affiche dans Claude Desktop et claude.ai ; voir `docs/research/mcp-apps.md` § 7 en cas de problème).

## 5. Vos fichiers 3D

Deux façons :

- **Dossier de l'hôte importé** : dans `docker-compose.yml`, décommenter `- /chemin/vers/mes-modeles:/data/import/modeles:ro` (mettre votre chemin), `docker compose up -d`, puis demander à Claude « inventorie /data/import/modeles puis importe-le ».
- **Volume Docker** : les données (base, modèles, jeton, configuration de Studio) vivent dans le volume `atelier-data` (`/data`). Sauvegarde : `docker compose exec atelier bambud backup --data-dir /data/atelier --out /data/backup.tar.gz`.

## 6. Accès depuis le réseau local

Par défaut, seul `localhost` est autorisé. Pour y accéder depuis un autre appareil (et recevoir les liens `/confirm` corrects) :

```yaml
ports: ["7878:7878"]
environment:
  BAMBU_PUBLIC_URL: "http://atelier-pc.local:7878"     # EXEMPLE : votre nom ou votre IP
  BAMBU_ALLOWED_HOSTS: "atelier-pc.local,192.168.1.50"  # EXEMPLE
```

Le jeton d'API est toujours obligatoire dans l'image (`BAMBU_REQUIRE_TOKEN=1`). Ne saisissez jamais le code d'accès de l'imprimante sur une page en `http://` qui traverse un réseau que vous ne maîtrisez pas : préférez https (`docs/DEPLOY-LINUX.md`, `docs/OAUTH.md`).

## Sans Studio dans l'image, ou Studio de l'hôte

Si la licence (Bambu Studio est sous AGPL-3.0) ou la taille vous gêne :

```sh
docker build --build-arg WITH_STUDIO=0 -t atelier-bambu:local .      # image sans Studio
```

puis monter le Studio de l'hôte (dossier extrait d'un AppImage : `./BambuStudio….AppImage --appimage-extract`) :

```yaml
environment: { BAMBU_STUDIO_BIN: /studio/AppRun }
volumes: [ "/opt/bambustudio:/studio:ro" ]
```

Il doit être un Studio **Linux** (le conteneur est Linux), compatible Ubuntu 24.04.

## Mise à jour, arrêt, désinstallation

```sh
git pull && docker compose build && docker compose up -d      # mise à jour (la base est migrée seule)
docker compose down                                            # arrêt ; les données restent dans le volume
docker compose down -v                                         # DESTRUCTIF : supprime aussi les données
```

## Dépannage

| Symptôme | Piste |
|---|---|
| `docker compose ps` : `unhealthy` | `docker compose logs atelier` ; la première ouverture charge les profils de Studio (≈ 30 s) |
| `slicer_missing` | Studio absent de l'image (`WITH_STUDIO=0`) : le monter, ou reconstruire sans l'option |
| 403 « hôte non autorisé » | l'adresse utilisée n'est pas dans `BAMBU_ALLOWED_HOSTS` / `BAMBU_PUBLIC_URL` |
| 401 | jeton d'API : `docker compose exec atelier cat /data/api-token` |
| Import STEP lent la première fois | CadQuery se charge ; ensuite en cache dans l'image |
