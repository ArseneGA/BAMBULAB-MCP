# Backlog — lot « v2 » (lancé le 2026-11-07)

Retenus par l'utilisateur : 1 à 11, 13, 16 à 21. Écartés (faisables directement dans Claude) : 12 photo → pièce, 14 réparation, 15 gravure ; 22 export de projet reporté.
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

1. ~~Liste blanche des surcharges, `*_gcode` interdits, bornes~~ + scan thermique du G-code et températures sur /confirm — **en cours**.
2. Premier essai sur la vraie P1S : URL de `project_file`, `md5`, `bed_leveling`/`bed_levelling` (session de branchement).
3. ~~CI GitHub Actions~~ (fait : `.github/workflows/ci.yml`, voir `docs/jalons/QUALITE.md`).
4. Calibrations d'extrusion (débit, avance de pression, rétraction, vitesse volumétrique, première couche) ou pilotage des calibrations natives de Studio.
5. Retrait mesuré sur deux tailles (séparer échelle et décalage de paroi) ou `xy_contour_compensation`.
6. Filetages : tessellation ≤ 0,02 mm, chanfrein de pointe ; engrenages : plus de points de flanc, congé de pied ; charnière print-in-place : axe en goutte.
7. Fabricabilité : volumes et zones d'exclusion lus depuis les profils Studio pour toutes les imprimantes (H2D, X1E, P2S…) au lieu de 5 en dur ; seuil de surplomb lu dans le profil.
8. Réparation de maillage minimale avec rapport avant/après ; détection d'auto-intersections.
9. ~~Golden files des gabarits, maillages « sales » de test, benchmarks gros maillages~~ (faits, voir `docs/jalons/QUALITE.md`) ; température de calibration selon le matériau (pas seulement PLA) ; `FitClearance` selon la dimension.

Restent ouverts hors lot : ponts Windows signés (`bambu-mcp.exe`, `bambu-agent.exe`), licence du dépôt (choix utilisateur), 22 export de projet.
