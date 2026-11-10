# Spécifications des chantiers « game changers » (lot G1 à G8), du suivi de projet (G9) et de la refonte de la navigation (G10)

Date de rédaction : 2026-11-08. Source : section « game changers » de `docs/BACKLOG.md`, `docs/strategie/ETUDE-MARCHE.md`, `docs/strategie/PLAN-SAAS.md`, et lecture du code (paquets `internal/*`, `cmd/*`, `web/src`).

Chaque fichier de ce dossier est **autosuffisant** : il peut être donné tel quel à un agent de code (Sonnet) qui travaille dans son propre worktree. Il suit le même gabarit en 11 parties (objectif, périmètre, état du code, exigences F, données, sécurité, compatibilité production, critères d'acceptation CA, fichiers touchés, risques/questions, consigne finale).

> Convention de lecture. « **à vérifier** » = information externe (conditions d'un annuaire, API ou comportement de Bambu, taux de frais d'une plateforme, comportement d'un client MCP tiers) que la personne qui a écrit ces specs n'a pas pu lire sur une source primaire : l'agent de code ne doit **ni la présenter comme acquise, ni la figer dans le code ou les tests** sans la vérifier (et citer l'URL + la date dans la doc livrée). Les chemins de fichiers cités ont été lus dans le dépôt le 2026-11-08.

## 0. Avertissement : le dépôt a bougé pendant la rédaction

Ces specs ont été écrites en lisant le dépôt le 2026-11-08 ; **pendant cette lecture, deux chantiers ont été fusionnés sur `main`** : l'**agent local v2** (`internal/agentv2`, `docs/AGENT-V2.md`, `internal/printer/link.go` et `remote.go` : interface `printer.Link` avec transport `direct` (défaut) ou `agent` par imprimante, protocole `/agent/v2`, lancement par autorisation signée Ed25519, migration `0260_agent_v2.sql`) et l'**approbation par passkey (WebAuthn)** (`internal/humanauth/passkey*.go`, page humaine `/setup/passkeys`, `docs/SECURITY.md` § 13, migration `0270_passkeys.sql`), ainsi qu'une page humaine `/setup/printer/{id}`. **Chaque agent de code doit relire `git log` et ces fichiers avant de commencer** : G1 (liste des chemins humains/publics), G6 (passkeys = nouveau chemin d'approbation à classer par rôle), G7 (transport `agent` : le mode livraison doit s'appliquer aussi au lien par agent) sont directement concernés ; les autres seulement par la numérotation des migrations. Chaque spec porte un encadré « Mise à jour » là où elle est touchée. Un fichier inconnu non mentionné ici (`scratch_dummy` à la racine du dépôt) n'a pas été créé par cette rédaction et n'est pas concerné.

## 1. Index

| Id | Fichier | Chantier | Segment | Taille | Dépend de |
|---|---|---|---|---|---|
| G1 | [G1-connecteur-distant.md](G1-connecteur-distant.md) | Connecteur distant officiel et annuaires | Tous | M (code S, démarches M) | — |
| G2 | [G2-cout-devis-marge.md](G2-cout-devis-marge.md) | Coût réel, devis et marge | Vendeurs, fermes | L | — (crée le socle PDF `internal/pdfdoc`) |
| G3 | [G3-apres-generation-ia.md](G3-apres-generation-ia.md) | « Après la génération IA » | Makers, vendeurs | L | — |
| G4 | [G4-rapport-fabricabilite.md](G4-rapport-fabricabilite.md) | Rapport de fabricabilité traçable (PDF signé) | PME, fermes | L | G2 pour `internal/pdfdoc` (peut démarrer sans) |
| G5 | [G5-tracabilite-commandes.md](G5-tracabilite-commandes.md) | Traçabilité par commande | Fermes | L | — (G2 facultatif : devis → commande) |
| G6 | [G6-mode-classe-fablab.md](G6-mode-classe-fablab.md) | Mode classe et fablab | Écoles, fablabs | L | — |
| G7 | [G7-survivre-verrouillage-bambu.md](G7-survivre-verrouillage-bambu.md) | Survivre au verrouillage Bambu | Tous | M | — |
| G8 | [G8-tout-client-mcp.md](G8-tout-client-mcp.md) | Compatible tout client MCP + interface web autonome | Tous | M | G1 (URL publique) pour les essais distants ; idéalement après G6 |
| G9 | [G9-suivi-de-projet.md](G9-suivi-de-projet.md) | Suivi de projet : projets, fiche pièce, fiche impression, verdict en un geste (codes PJ1 à PJ12, demande du 2026-11-09) | Makers, vendeurs, fermes | L (4 vagues) | — (s'appuie sur le journal existant ; N13 et S7 s'y branchent) |
| G10 | [G10-refonte-navigation-tableau-de-bord.md](G10-refonte-navigation-tableau-de-bord.md) | Refonte de la navigation et vrai tableau de bord de l'interface web : 7 entrées, redirections, mobile, recherche globale, Accueil, pages serveur et cartes alignées (codes UX1 à UX12, demande du 2026-11-09) | Tous | L (4 vagues U1 à U4) | G9 (accueille projets, verdict, fiches ; U1 peut démarrer avant) |
| SK | [SK-ignorer-un-objet.md](SK-ignorer-un-objet.md) | Ignorer un objet raté en cours d'impression sur une plaque à plusieurs objets (commande native `skip_objects`, geste humain comme l'arrêt ; codes SK0 à SK6, idée du 2026-11-10) | Makers, vendeurs, fermes | M (étape A, essai sur P1S, puis B et C) | G9 PJ10 et G10 U2 (fiche impression, Accueil) |

Tailles : S ≈ 1 à 2 jours d'agent, M ≈ 3 à 5 jours, L ≈ 6 à 12 jours (ordre de grandeur, tests et doc compris).

## 2. Règles communes à tous les chantiers (rappel ; chaque spec les reprend)

1. **Approbation humaine** : lancer et arrêter une impression ne se font jamais par un outil MCP ni par un appel que le modèle peut produire (`docs/SECURITY.md`, tests `TestNoMCPToolCanApproveAConfirmation`, `noapprove_test.go`). Aucun chantier n'ajoute de chemin de lancement automatique.
2. **Secrets** (code d'accès imprimante, jetons, code de lancement, clés de signature) : jamais dans une réponse MCP, un journal, un export ou Git.
3. **Originaux immuables** : tout passe par de nouvelles versions (`model_versions`), jamais par modification d'un fichier source (blobs adressés par contenu, `internal/store/blobs.go`).
4. **Mesuré / calculé / estimé** : toute valeur chiffrée affichée porte son niveau (`internal/engineering/types.go` : `measured`, `calculated`, `heuristic`, `indicative`). Une valeur inconnue est signalée, jamais inventée. Dans les rapports destinés à l'humain : *mesuré* (lu sur le fichier, la machine, l'archive du slicer), *calculé* (formule déterministe sur entrées mesurées), *estimé* (heuristique ou indicatif), *déclaré* (saisi par l'humain, introduit par G7/G5).
5. **Compatibilité de la production actuelle** : serveur Linux, P1S en direct (mode LAN + Développeur), Claude Desktop via `mcp-remote` doivent continuer à fonctionner **sans aucune action** de l'utilisateur après mise à jour : défauts inchangés, aucun outil renommé ni supprimé, `tools/list` conforme (`internal/mcpserver/spec_conformance_test.go`), migrations **additives** seulement (jamais de `DROP`/renommage ; une colonne ajoutée a une valeur par défaut).
6. **Tests** : tout comportement a un test Go (`go test -short ./... -count=1`) et, côté web, un test vitest ; l'imprimante est **toujours simulée** (`cmd/printer-sim`, `internal/printer/sim`) dans les tests ; jamais d'imprimante réelle.
7. **Documentation en français**, dans des **nouveaux fichiers** `docs/<CHANTIER>.md` (voir § 4) ; ne pas éditer `docs/UTILISATION.md`, `docs/ETAT.md` ni `docs/BACKLOG.md` (l'orchestrateur les met à jour à la fusion).
8. **Marque** : le produit s'appelle **Printalk** (`web/src/brand.ts`, constantes `PRODUCT_NAME`, `TAGLINE`) ; style vert (`web/src/design/tokens.css`, composants `web/src/components/ui.tsx`). Aucun logo ni nom de Bambu Lab utilisé comme marque (mention « compatible » seulement).
9. **Licence** : dépôt AGPL-3.0. Toute dépendance Go ajoutée doit être sous licence compatible (MIT, BSD, Apache-2.0 ; pas de GPL-2.0-only, pas de licence non libre) et **pure Go** (le binaire est compilé avec `CGO_ENABLED=0`, voir `Makefile`). L'agent note la licence et la version de chaque dépendance ajoutée dans `docs/<CHANTIER>.md`.

## 3. Numérotation des migrations (additives)

Les migrations sont dans `internal/store/migrations/` (dernières lues en fin de rédaction : `0250_project_export.sql`, `0260_agent_v2.sql`, `0270_passkeys.sql` ; les numéros ne sont pas contigus). **Aucune spec ne fixe de numéro** : chaque agent calcule `dernier numéro existant + 10` **au moment du codage** (`ls internal/store/migrations`), puis revérifie juste avant de fusionner. Comme les chantiers tournent en parallèle, deux agents peuvent choisir le même numéro : règle de fusion → l'orchestrateur fusionne **un chantier à la fois** ; le chantier fusionné en second **renumérote sa migration** (`git mv`, mise à jour des commentaires et du test de migrations `internal/store/store_test.go`) avant de passer ses tests. Chaque migration a un en-tête de commentaire (voir `0160_insights.sql`) disant le chantier et « additive ».

## 4. Fichiers « chauds » (conflits triviaux attendus) et règles pour les éviter

| Fichier | Pourquoi chaud | Règle |
|---|---|---|
| `internal/mcpserver/server.go` | une ligne `registerXxxTools(s, c)` par chantier (lignes 85-106) | chaque chantier met ses outils dans **un nouveau fichier** `internal/mcpserver/<chantier>_tools.go` et n'ajoute **qu'une ligne** dans `server.go` |
| `internal/core/core.go` | câblage d'un service par chantier | ajouter un champ + un constructeur ; pas de réorganisation |
| `internal/httpapi/httpapi.go`, `guard.go` | routes et liste blanche humaine/publique | nouvelle route = nouveau fichier `internal/httpapi/<chantier>.go` ; une ligne d'enregistrement dans `httpapi.go` |
| `cmd/bambud/main.go` | indicateurs de ligne de commande | un indicateur par option, regroupés dans un bloc commenté par chantier |
| `web/src/main.tsx`, `web/src/Shell.tsx` | routes et menu | une `<Route>` et un `NavItem` par chantier |
| `web/src/api/types.ts` | types partagés | nouveaux types dans un **nouveau fichier** `web/src/api/<chantier>Types.ts` |
| `docs/*.md` existants | mise à jour par l'orchestrateur | écrire `docs/<CHANTIER>.md` (ex. `docs/DEVIS.md`, `docs/CLASSE.md`) |

Contrat partagé **PDF** (G2 propriétaire, G4 consommateur) : paquet `internal/pdfdoc`, API minimale décrite dans G2 § 4 (F9). Si G4 démarre avant la fusion de G2, G4 livre d'abord son rapport JSON + HTML imprimable et ne branche le PDF qu'après la fusion de G2 (ou rebase sur la branche de G2).

Contrat partagé **identité de l'approbateur** : l'audit enregistre déjà `human:<via>` à l'approbation (`internal/printing/confirm.go`, `Approve`). G5 **lit** cette trace (entrées d'audit `print.confirmation_approved`) sans modifier l'approbation ; G6 **enrichit** `via` (rôle + étiquette de session). Aucun fichier commun : G5 doit rester tolérant à un `via` enrichi (traiter la valeur comme une chaîne d'affichage).

## 5. Ordre conseillé et parallélisme

Ordre de valeur (voir `docs/BACKLOG.md`) : **G7** (protège toute la valeur si Bambu durcit), **G1** (distribution), **G2** (ROI immédiat pour les vendeurs), **G3**, puis **G4/G5**, **G6**, **G8**.

Lots qui peuvent tourner **en même temps** sans toucher aux mêmes fichiers métier (seuls les fichiers chauds du § 4 se recoupent, trivialement) :

| Vague | Chantiers en parallèle | Zones de code distinctes |
|---|---|---|
| **Vague 1** | **G1** + **G2** + **G3** + **G7** | G1 : `internal/httpapi/public*.go`, `cmd/bambud`, `docs/`, métadonnées serveur. G2 : `internal/insights/*`, `internal/pdfdoc/*`, pages web coûts/devis. G3 : `internal/mesh/glb*.go`, `internal/catalog/importer.go`, `internal/printing/geometry.go`, outil de préparation. G7 : `internal/printer/*`, `internal/printing/` (livraison, fiche de lancement), `internal/agentlink` + `internal/agent`, `cmd/printer-sim`. |
| **Vague 2** | **G4** + **G5** + **G6** (+ **G8** si G6 est fusionné ou si G8 attend) | G4 : `internal/mesh/section*.go`, `internal/fabreport/*` (nouveau), appui sur `internal/pdfdoc` (G2). G5 : `internal/orders/*` (nouveau), `internal/export`, `print_queue`. G6 : `internal/humanauth`, `internal/httpapi/guard.go`, nouveau `internal/classroom/*`, passerelle de rôle MCP. G8 : filtrage `tools/list`, tests clients, docs. |

Note : G7 doit être relu à la lumière de l'agent v2 (`printer.Link`) avant de démarrer ; en pratique, lancer G7 **après** avoir confirmé que la branche de l'agent v2 est stable sur `main`.

Paires à **ne pas** lancer simultanément sans coordination : G1 + G6 (toutes deux modifient la liste de chemins publics/humains de `internal/httpapi/guard.go`), G6 + G8 (toutes deux ajoutent un intergiciel autour de `tools/list`/`tools/call`), G5 + G7 (G7 ajoute un état « déclaré par l'humain » dans le journal ; G5 lit ce journal : fusionner G7 d'abord).

## 6. Estimations, rappel (détail et questions ouvertes dans chaque spec)

G1 M · G2 L · G3 L · G4 L · G5 L · G6 L · G7 M · G8 M.

## 7. Consigne finale commune (reprise dans chaque spec)

Vérifier : `go vet ./...` ; `go test -short ./... -count=1` ; `cd web && npm ci && npm run build` ; `cd web && npx vitest run`. Commits en français, une ligne finale `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. **Ne pas pousser** (pas de `git push`).
