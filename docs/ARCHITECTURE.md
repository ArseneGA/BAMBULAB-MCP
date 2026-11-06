# Architecture — décisions de construction

Version : 2026-11-06 (révisée : passage au langage Go à la demande de l'utilisateur).
Cadre : `docs/DOSSIER.md`, D0 livré (`prototype/`), recherches dans `docs/research/`.
Périmètre : écosystème Bambu Lab uniquement (toutes les imprimantes Bambu, FDM, pas de résine). P1S = machine de référence.

## Principe : tout en Go, sauf ce qui ne peut pas l'être

| Partie | Langage | Pourquoi |
|---|---|---|
| Service (catalogue, révisions, file de travaux, API HTTP, serveur MCP, slicing, adaptateur imprimante, simulateur) | **Go** | Binaire unique, léger, idéal sur le vieux serveur Linux |
| Import/analyse maillage STL, OBJ, 3MF ; dérivés GLB ; module de calcul tolérances/contraintes | **Go** | Calcul pur, pas besoin de Python |
| Agent Windows (pilotage Bambu Studio local) | **Go** | Même langage, binaire Windows unique |
| Import STEP + modélisation paramétrique (CAO) | Python (CadQuery/OCP), sous-processus JSON | Aucun noyau CAO équivalent en Go |
| Interface web et vues MCP App | TypeScript/React (navigateur) | Le navigateur n'exécute que du JS ; servie par le binaire Go (embed) |

## Décisions

| Sujet | Choix |
|---|---|
| Module | Un seul module Go `github.com/ArseneGA/BAMBULAB-MCP`, Go ≥ 1.24 (dev : 1.27) |
| MCP | SDK officiel `github.com/modelcontextprotocol/go-sdk` ; transports stdio et Streamable HTTP sur le même cœur ; métadonnées MCP Apps (`ui://`, `text/html;profile=mcp-app`, `_meta.ui.resourceUri`, CSP) émises à la main selon `docs/research/mcp-apps.md` |
| Base | SQLite pur Go `modernc.org/sqlite` (sans cgo → compilation croisée triviale), migrations versionnées |
| Fichiers | Stockage adressé par contenu `data/blobs/ab/cd/<sha256>`, originaux immuables, écriture atomique |
| Travaux | File persistante SQLite, 1 worker lourd par défaut, reprise au redémarrage ; les travaux physiques (start_print) ne sont jamais relancés automatiquement → état `unknown` |
| Contrats | Types Go + JSON Schema générés des types ; machine d'états d'impression du dossier §10 en fonction pure testée |
| HTTP | `net/http` standard (routeur Go 1.22+), pas de framework |
| Slicing | CLI Bambu Studio en sous-processus (arguments en tableau), lecture de `result.json`, aplatissement de l'héritage des presets BBL, profils de toutes les imprimantes Bambu |
| Imprimante | Interface `printer.Adapter` ; Bambu LAN (MQTT TLS 8883 via paho, FTPS implicite 990) ; simulateur conforme (même protocole MQTT/FTPS en local) |
| Calcul | Package `engineering` : fonctions pures, chaque résultat = valeur + hypothèses + domaine + niveau (mesuré/calculé/heuristique) |
| Frontend | `web/` (évolution du prototype D0), build Vite, embarqué dans le binaire via `embed` |
| Tests | `go test ./...` (+ `-race`), pytest pour le worker CAO, test de bout en bout par jalon |
| Secrets | Jamais dans Git ni dans les réponses MCP ; fichier de config local ignoré |

## Arborescence cible

```
cmd/bambud            service (HTTP + MCP Streamable HTTP + worker)
cmd/bambu-mcp         pont MCP stdio pour Claude Desktop (relaie vers bambud)
cmd/bambu-agent       agent Windows (Bambu Studio)
cmd/printer-sim       simulateur d'imprimante Bambu
internal/contracts    types, schémas, machine d'états, confirmation
internal/store        SQLite, migrations, blobs
internal/jobs         file de travaux
internal/catalog      catalogue, versions, doublons, corbeille
internal/mesh         STL/OBJ/3MF, analyse, GLB
internal/engineering  tolérances et contraintes
internal/slicer       CLI Bambu Studio, presets
internal/printer      adaptateur Bambu LAN + interface
internal/goals        objectifs et variantes
internal/mcpserver    outils et ressources MCP / MCP Apps
web/                  atelier web + vues MCP App (TS/React)
workers/cad           Python : STEP + paramétrique
prototype/            maquette D0 figée
```
