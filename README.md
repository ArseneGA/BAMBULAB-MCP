# Atelier Bambu

Un gestionnaire de fichiers 3D et un copilote de fabrication pour les imprimantes **Bambu Lab** (FDM), pilotable depuis **Claude** (Claude Desktop ou tout client MCP) et depuis une interface web. Machine de référence : la **P1S**.

Ce que cela fait, concrètement :

- **Ranger et retrouver** vos modèles (STL, OBJ, 3MF, STEP) : import non destructif, collections, étiquettes, doublons exacts, versions immuables, corbeille, aperçus 3D. « Retrouve ma pince » fonctionne.
- **Créer et modifier** : modèles paramétriques (pince, équerre, boîte, entretoise, crochet) via un worker CAO, mise à l'échelle, rotation, coupe, pose à plat — toujours sur une **copie**, l'original reste intact.
- **Préparer** : objectifs de fabrication (« solide, surface propre, rapide »), variantes (orientation, remplissage, supports…), découpe par **Bambu Studio en ligne de commande**, tableau comparatif honnête (mesures réelles du slicer, estimations clairement étiquetées).
- **Imprimer, avec votre accord et seulement avec lui** : dépôt du fichier sur l'imprimante **sans la lancer**, puis lancement après une confirmation que **vous seul** donnez sur une page web. Aucun outil de Claude ne peut approuver un lancement.
- **Fonctionner sans le PC Windows** : le serveur Linux prépare, découpe et dépose seul. Le PC Windows, quand il est allumé, apporte un renfort (import de vos dossiers, découpe, ouverture dans Bambu Studio).

> **État réel** : tout ce qui touche l'imprimante a été validé contre un **simulateur** conforme à notre lecture du protocole, **pas encore sur la vraie P1S**. `docs/ETAT.md` dit, fonction par fonction, ce qui est implémenté, testé, simulé ou restant. `docs/BRANCHEMENT.md` est la procédure du jour où l'on branche la vraie machine.

## Architecture en un schéma

```
 Claude Desktop (PC Windows)            Navigateur (PC, téléphone, tablette — réseau local)
        │  MCP (stdio)                                   │  HTTP
        ▼                                                ▼
 bambu-mcp.exe  ───── HTTP /mcp + jeton d'API ─────►  ┌─────────────────────────────────────────┐
 (pont, --api-token-file)                             │  SERVEUR LINUX  (Zorin OS / Ubuntu)      │
                                                      │  bambud  :7878                           │
 PC Windows (renfort, optionnel)                      │   ├─ API HTTP + MCP /mcp + pages humaines│
 bambu-agent.exe ── protocole /agent/v1 (appairé) ──► │   │    (/enroll /confirm /stop /setup)   │
   • inventaire/envoi de vos dossiers                 │   ├─ catalogue + file de travaux (SQLite)│
   • découpe de renfort, ouverture dans Studio        │   ├─ blobs adressés par contenu          │
                                                      │   ├─ worker CAO (Python/CadQuery, uv)    │
                                                      │   ├─ Bambu Studio CLI (AppImage + xvfb)  │
                                                      │   └─ sauvegarde quotidienne (timer)      │
                                                      └───────────────┬─────────────────────────┘
                                                                      │ LAN : MQTT TLS 8883 + FTPS 990
                                                                      ▼
                                                              Imprimante Bambu (P1S)
                                                              Developer Mode + LAN Only
```

Deux modes, les deux livrés : **Linux seul** (Windows éteint) et **Linux + renfort Windows**. La perte du PC Windows ne relance jamais une impression : les travaux concernés passent à l'état « inconnu » et attendent une réconciliation.

## Démarrage rapide

**Le plus simple : Docker** (image tout-en-un avec Bambu Studio et le worker CAO) : `docs/QUICKSTART.md`. Connecteur distant de Claude (claude.ai, mobile) : `docs/OAUTH.md`.

### Essayer sur un seul PC (sans imprimante)

Prérequis : Go ≥ 1.26 (développé avec 1.27). PowerShell sous Windows :

```powershell
git clone https://github.com/ArseneGA/BAMBULAB-MCP.git ; cd BAMBULAB-MCP
.\scripts\smoke.ps1          # démarre bambud + simulateur d'imprimante sur un dossier temporaire, teste tout, nettoie
```

Pour garder un service local avec des modèles de démonstration :

```powershell
go run ./cmd/bambud seed-demo --data-dir .\data          # ~13 modèles de démonstration
go run ./cmd/bambud --data-dir .\data                     # http://127.0.0.1:7878
go run ./cmd/bambud enroll --data-dir .\data              # code pour enregistrer votre navigateur (/enroll)
go run ./cmd/printer-sim --mqtt-port 0 --ftp-port 0       # imprimante simulée (jamais d'imprimante réelle)
```

### Installer pour de bon

1. **Préparer le serveur Linux** avec le prompt de `docs/SETUP-LINUX-PROMPT.md` (à donner à Claude Code sur le Zorin).
2. **Construire la livraison** : `.\scripts\release.ps1` (ou `scripts/release.sh`) → `dist\<version>\` avec les binaires Linux/Windows et `SHA256SUMS`.
3. **Installer** : `sudo bash deploy/linux/install.sh --from <livraison>` sur le serveur, puis `deploy\windows\install-agent.ps1` et `deploy\windows\configure-claude-desktop.ps1` sur le PC Windows.

Pas à pas, commandes exactes : **`docs/INSTALL.md`**.

## Les documents

| Document | Pour quoi faire |
|---|---|
| `docs/INSTALL.md` | Installer le serveur Linux, l'agent Windows, brancher Claude Desktop ; sauvegarde et restauration |
| `docs/BRANCHEMENT.md` | Le jour où l'on branche la vraie P1S : procédure étape par étape, avec points d'arrêt |
| `docs/UTILISATION.md` | Exemples de conversations avec Claude (« retrouve ma pince », « compare 3 variantes », « envoie et lance »…) |
| `docs/ETAT.md` | État honnête : implémenté, testé, simulé, restant — avec les tests qui le prouvent |
| `docs/SECURITY.md` | Modèle de menace : pourquoi Claude ne peut pas lancer seul une impression |
| `docs/ARCHITECTURE.md`, `docs/DOSSIER.md`, `docs/jalons/` | Décisions de construction, cahier des charges, rapports de jalons |

## Principes non négociables

- **Aucun lancement sans accord humain**, lié à *cette* impression (imprimante, empreinte du fichier, plateau, matières), donné sur une page web depuis un **navigateur que vous avez enregistré** avec un code affiché sur *votre* terminal.
- **Les secrets** (code d'accès de l'imprimante, jeton d'API, jeton d'appairage) ne passent jamais par Claude, ni par les journaux, ni par Git.
- **Les originaux ne sont jamais modifiés** : tout passe par des copies et des versions.
- **Distinguer le mesuré, le calculé, le simulé** : une valeur inconnue est signalée, jamais inventée.

## Sauvegarde

`bambud backup --out <fichier.tar.gz>` (à chaud, sans arrêter le service), `bambud verify --archive <fichier>`, `bambud restore --from <fichier> --data-dir <dossier>`. Le serveur installé sauvegarde chaque nuit (rotation). **Une copie sur le serveur n'est pas une sauvegarde** : pointez `--backup-dir` vers un disque externe ou un NAS, et faites un essai de restauration (voir `docs/INSTALL.md`).

## Développement

```powershell
go vet ./... ; go test -short ./... -count=1
$env:GOOS='linux'; go build ./... ; Remove-Item Env:GOOS      # compilation croisée (SQLite pur Go, sans cgo)
cd workers\geometry ; uv run pytest                            # worker CAO
```

Le dossier `prototype/` est la maquette D0 figée ; `web/` (interface et vues MCP App) est développé séparément.

## Licence

Ce projet est distribué sous **GNU Affero General Public License v3.0** (AGPL-3.0) : voir [LICENSE](LICENSE).

En résumé : vous pouvez utiliser, modifier et redistribuer ce logiciel, y compris commercialement ; si vous le modifiez et le mettez à disposition d'autres personnes, y compris sous forme de service en réseau, vous devez publier le code source de vos modifications sous la même licence.

Bambu Studio n'est pas inclus dans ce dépôt : il est lancé comme programme externe (et téléchargé lors de la construction de l'image Docker). Il reste distribué par Bambu Lab sous sa propre licence (AGPL-3.0).
