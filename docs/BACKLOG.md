# Backlog — lot « v2 » (lancé le 2026-11-07)

Retenus par l'utilisateur : 1 à 11, 13, 16 à 21. Écartés (faisables directement dans Claude) : 12 photo → pièce, 14 réparation, 15 gravure ; 22 export de projet (repris et fait, voir plus bas).
Règle commune : tout est utilisable depuis Claude (outil MCP + rendu texte/image, vue MCP App quand elle existe) et depuis l'interface web ; lancer et arrêter une impression restent des gestes humains sur les pages /confirm et /stop (docs/SECURITY.md).

| # | Chantier | Agent | Migration |
|---|---|---|---|
| 1 | Envoi de fichiers direct (outil `upload_file` par morceaux, glisser-déposer web, dossiers d'import dans `get_capabilities`) | A | 0140 |
| 11 | Import depuis un lien MakerWorld / Printables | A | 0140 |
| 2 | Plusieurs pièces sur un plateau, répartition sur plaques, réglages par objet dans le 3MF | B | 0150 |
| 3 | Aperçu du plateau (image) + diagnostic du viewer 3D dans Claude via `mcp-remote` | B (image) / F (viewer) | — |
| 16 | Estimation du coût (filament, électricité, temps, usure) et total par projet | C | 0160 |
| 18 | Conseils de post-traitement selon matériau | C | 0160 |
| 20 | Journal des impressions (photo, réglages réels, note) qui affine les réglages | C | 0160 |
| 21 | Tableau de bord d'usage de la machine | C | 0160 |
| 6 | Caméra P1S : instantané, photo sur /confirm, suivi | D | 0170 |
| 19 | Détection d'échec par la caméra + alerte (l'arrêt reste humain) | D | 0170 |
| 8 | Notifications (fin, erreur, alerte) : ntfy / webhook / e-mail | D | 0170 |
| 7 | Inventaire des bobines et de l'AMS, alerte de manque | E | 0180 |
| 17 | Multicolore AMS : couleur par zone, coût de purge | E | 0180 |
| 9 | File d'attente d'impressions (un accord humain par impression) | E | 0180 |
| 4 | Accès direct par URL : OAuth 2.1 pour connecteur distant | F | 0190 (fait : docs/OAUTH.md) |
| 5 | Image Docker + démarrage rapide + doc neutralisée | F | 0190 (fait : docs/QUICKSTART.md) |
| 3 | Viewer 3D dans Claude (vues MCP App) | F | — (diagnostic : docs/research/mcp-apps.md § 7) |
| 10 | Pièces de calibration générées + mesures réinjectées | G (fait : `generate_calibration_set`, `record_calibration`, `get_calibration`) | 0200 |
| 13 | Bibliothèque de pièces paramétriques extensible | G (fait : 17 gabarits, `list_part_templates`, `describe_part_template`, `create_from_template`, `save_as_template`) | 0200 |

## Lot « qualité 3D » (audit FDM du 2026-11-07)

1. ~~Liste blanche des surcharges, `*_gcode` interdits, bornes, scan thermique du G-code, températures sur /confirm~~ — **fait** (+ bug `include` des profils BBL corrigé).
2. Premier essai sur la vraie P1S : URL de `project_file`, `md5` (session de branchement). `bed_leveling` **et** `bed_levelling` sont envoyés à `true` par défaut (option `bed_leveling` de `request_print_confirmation`, affichée et liée à l'approbation, 2026-11-08) ; reste à **constater sur la vraie P1S** que la mise à niveau a bien lieu.
3. ~~CI GitHub Actions~~ (fait : `.github/workflows/ci.yml`, voir `docs/jalons/QUALITE.md`).
4. ~~Calibrations d'extrusion~~ — **fait** (séries de pièces par valeur, débit, rétraction, débit volumétrique, première couche ; avance de pression : réglage manuel, pas de clé BBL). Le pilotage des calibrations natives de Studio reste impossible par CLI (interface graphique).
5. ~~Retrait sur deux tailles~~ — **fait** (`shrink_sizes`, `xy_contour_compensation` / `xy_hole_compensation` proposés).
6. ~~Filetages, engrenages, charnière~~ — **fait** (maillage fin, chanfreins, 24 points par flanc, congé 0,38·m, axe en goutte).
7. ~~Fabricabilité : volumes et zones d'exclusion lus depuis les profils Studio pour toutes les imprimantes (H2D, X1E, P2S…) au lieu de 5 en dur ; seuil de surplomb lu dans le profil.~~ — **fait**.
8. ~~Réparation de maillage minimale avec rapport avant/après ; détection d'auto-intersections~~ — **fait** (`repair_mesh`).
9. ~~Golden files des gabarits, maillages « sales » de test, benchmarks gros maillages~~ (faits, voir `docs/jalons/QUALITE.md`) ; ~~température de calibration selon le matériau~~ et ~~`FitClearance` selon la dimension~~ (faits).

## Retours terrain P1S (2026-11-07) — en cours

-50 « plaque vide » > 214 mm, tolérance de hauteur, START_UNKNOWN à tort (accusé lent), jetons d'URL finissant par `_`, progression et délais des outils longs, profil X1C choisi pour P1S, noms de matériaux, couleur de la bobine externe, découpes bloquées « en cours », historique encombré, affichage « failed » après arrêt.

## Confirmer l'impression sans quitter Claude (demande du 2026-11-07)

Besoin : le bouton « Lancer » (et « Arrêter ») s'affiche dans la conversation, sans ouvrir le navigateur.
Contrainte de sécurité (SECURITY.md) : le serveur ne distingue pas un appel venant de la vue MCP App d'un appel fait par le modèle ; un simple bouton dans la vue serait donc approuvable par Claude lui-même (faille déjà corrigée une fois).
Piste retenue : vue MCP App de confirmation (résumé, températures, photo du plateau) avec un **code de lancement personnel** saisi par l'humain dans la vue : code à 6 chiffres défini une fois sur une page humaine, stocké haché, jamais transmis par la conversation, verrouillage après 3 essais, lié à la confirmation (imprimante, empreinte, plateau, matières) et valable 2 min. Le modèle ne connaît pas le code ; un appel deviné est bloqué par le verrouillage. Variante plus forte à étudier : passkey/WebAuthn dans la vue si le bac à sable de Claude l'autorise. Le lien navigateur reste disponible en secours.
Prérequis : vue MCP App réellement affichée dans Claude Desktop (point 3).
**Fait (2026-11-07, à éprouver dans un vrai Claude Desktop)** : code de lancement personnel (`/setup/launch-code`, `bambud launch-code --unlock|--reset`), vue de confirmation et d'arrêt avec champ de code, outils app-only `approve_print_with_code` / `approve_stop_with_code`, verrouillage après 3 échecs. Voir `docs/SECURITY.md` § 12 (risque résiduel : code tapé dans la conversation) et `docs/UTILISATION.md` § 4 bis. Reste ouvert : passkey/WebAuthn dans la vue.

## 22. Export de projet — fait

`export_project` / `get_project_export` (paquet `internal/export`, outils `internal/mcpserver/export_tools.go`, routes `/api/exports` dans `internal/httpapi/export.go`, bouton « Exporter le projet » sur une collection de la Bibliothèque, migration 0250). Archive zip (originaux vérifiés par sha256, sources paramétriques, profils effectifs sans G-code, `.gcode.3mf` validés en option, README en français, manifest.json, photos en option), construite en flux par un travail asynchrone, taille bornée, téléchargée par URL signée de 10 minutes, conservée 24 h. Aucune publication automatique : la réponse liste ce qu'il reste à faire à la main (voir `docs/UTILISATION.md` § 14).

Restent ouverts hors lot : ponts Windows signés (`bambu-mcp.exe`, `bambu-agent.exe`), licence du dépôt (choix utilisateur).
