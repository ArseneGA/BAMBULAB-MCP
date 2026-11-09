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

## Lot « game changers » (issu de docs/strategie/ETUDE-MARCHE.md, 2026-11-08)

Ce qui change la position du produit sur le marché, par ordre d'impact. Règle : la valeur doit survivre à un durcissement de Bambu (risque n°1) et ne pas se battre sur la gestion de flotte (gratuite ailleurs) ni sur la génération 3D (déjà chez Bambu).

| # | Chantier | Pourquoi c'est décisif | Segment |
|---|---|---|---|
| G1 | **Connecteur distant officiel + annuaire Anthropic** : OAuth en production (HTTPS public), fiche dans l'annuaire des connecteurs Claude, mcpservers.org, Glama, registre MCP | La distribution passe par les annuaires et claude.ai (aucun MCP Bambu sérieux n'y est) ; débloque aussi les cartes (vues) et le mobile | Tous |
| G2 | **Coût réel, devis et marge** : prix de revient par pièce et par commande, prix de vente conseillé selon marge cible, frais de plateforme (Etsy, MakerWorld), devis PDF, rapprochement commande ↔ impressions | Le seul argument que les vendeurs paient tout de suite : ROI chiffrable en quelques ventes | Vendeurs, fermes |
| G3 | **« Après la génération IA »** : importer un modèle généré (MakerLab/Meshy/Tripo, image ou texte), le réparer, l'orienter, vérifier la fabricabilité, le découper et le chiffrer en une conversation | La génération est banalisée et presque jamais imprimable telle quelle : la valeur est juste après, là où personne n'est | Makers, vendeurs |
| G4 | **Rapport de fabricabilité traçable** : résistance, surplombs, adhérence, profils comparés, avec preuves (mesuré / calculé / estimé), exporté en PDF signé et rattaché à la pièce | Ouvre les bureaux d'études et PME (exigence qualité), et justifie un prix plus haut que la flotte | PME, fermes |
| G5 | **Traçabilité par commande pour les fermes** : commande client → pièces → impressions (photo, réglages réels, bobine, opérateur, accord humain), file par priorité et échéance | Se différencie des gestionnaires de flotte en vendant la preuve et la qualité, pas le nombre d'imprimantes | Fermes |
| G6 | **Mode classe / fablab** : approbation par un adulte (un élève prépare, un encadrant lance), sans compte élève, sur site, journal auditable | Marché institutionnel qui exige justement ce que le produit a déjà (approbation humaine, données chez soi) | Écoles, fablabs |
| G7 | **Survivre au verrouillage Bambu** : fonctionnement complet « sans lancement » (préparer, déposer via carte SD/Handy/Bambu Connect, suivre ce qui est possible), et voie Bambu Connect / API officielle si elle s'ouvre | Protège 100 % de la valeur hors lancement si le mode Développeur disparaît | Tous |
| G8 | **Compatible tout client MCP** (ChatGPT, Gemini, clients open source) et interface web autonome | Réduit la dépendance à Claude (risque n°4) et élargit le marché | Tous |

À valider avant de coder G2–G6 : entretiens clients (questions en § 10.1 de l'étude) et page d'attente (§ 10.2).

## Lot « parité Bambu Studio + atelier » (demande du 2026-11-09)

Principe : **toutes les options classiques de Bambu Studio doivent être pilotables en une phrase depuis Claude**, avec un aperçu (visionneuse couche par couche) et une carte quand c'est utile. Aucune option ne contourne les règles de sécurité (liste blanche des réglages, scan thermique, lancement humain).

| # | Fonction | Ce que ça fait | Équivalent Bambu Studio |
|---|---|---|---|
| S1 | **Couper une grande pièce** | Découpe en 2-3 parties avec tenons/mortaises ou trous de goupille (jeu d'assemblage selon la matière), réparties sur une ou plusieurs plaques | Outil Couper + connecteurs |
| S2 | **Hauteur de couche variable** | Couches fines sur les courbes, épaisses sur les parties droites ; aperçu du gain de temps | Hauteur de couche adaptative / variable |
| S3 | **Finitions de surface** | Lissage au fer (ironing), peau texturée (fuzzy skin), position de couture (arrière, alignée, peinte), en une phrase avec aperçu | Ironing, Fuzzy skin, Seam |
| S4 | **Pause programmée / changement de couleur** | « Pause à 12 mm pour insérer un aimant », « change de couleur à la couche 80 » sans AMS ; notification, geste humain, reprise validée par l'humain | Pause / Changement de filament à la couche |
| S5 | **Heure de fin choisie / départ différé** | « Finie à 18 h » → l'atelier dit quand lancer ; départ différé validé comme un lancement normal (l'accord humain reste lié et daté) | — (Farm Manager : départs décalés) |
| S6 | **Timelapse automatique** | Récupéré à la fin depuis l'imprimante et rangé avec l'impression (vidéo dans le journal) | Timelapse |
| S7 | **« Refais la même »** | Relance une pièce réussie avec exactement les mêmes réglages et la même découpe (ou redécoupe si la bobine a changé), en une phrase | Réimprimer depuis l'historique |
| S8 | **Supports et zones à la main, en parlant** | « Supports seulement sous le goujon », « 6 parois ici », « bordure seulement autour des pieds » : peinture de supports, bloqueurs/forceurs, modificateurs par volume | Peinture de supports, modificateurs |
| S9 | **Parité des réglages** | Toutes les clés courantes des profils process/filament/machine exposées et expliquées (ponts, surplombs, rétraction, coasting, vitesses par type, accélérations, refroidissement, brim ears, radeau, mode vase, séquence objet par objet, arrangement auto, mise à l'échelle / miroir / rotation / pose à plat par face) | Onglets Qualité, Résistance, Vitesse, Support, Autres |
| S10 | **Calibrations natives** | Débit, avance de pression, température (tour), rétraction, vitesse volumétrique, déjà en séries de pièces : ajouter la lecture guidée des résultats par photo | Menu Calibration |

### Nouvelles idées — N1 à N8 (N2 à N7 retenues le 2026-11-09)

| # | Idée | Pourquoi |
|---|---|---|
| N1 | **Diagnostic d'échec** : « pourquoi ça a raté ? » à partir de la photo, des codes HMS, des étapes et des réglages ; propose la correction et la redécoupe | Les deux échecs du porte-guitare auraient été expliqués en une réponse |
| N2 ✅ retenue | **Rendu réaliste avant impression** dans la vraie matière et couleur (translucide, soie, mat) | Choisir une variante sur l'aspect, pas seulement sur les chiffres |
| N3 ✅ retenue | **Pièces de rechange / impression en série** : « imprime 12 fois ce clip » → remplissage optimal des plaques, répartition par lots, suivi du compte | Gain de temps énorme, déjà presque possible avec plan_plate |
| N4 ✅ retenue | **Pilotage des accessoires de l'imprimante** : éclairage de la chambre, ventilateurs, vitesse d'impression en cours (silencieux / normal / sport), sans jamais lancer ni arrêter | Commandes Bambu classiques, sans risque |
| N5 ✅ retenue | **Insertion d'éléments** : logements prêts pour inserts filetés, aimants, écrous captifs, avec la pause automatique au bon moment (lien S4) | Pièces fonctionnelles sans post-traitement |
| N6 ✅ retenue | **Assistant d'assemblage** : génère la notice d'assemblage (ordre, vis, photos annotées) d'un projet multi-pièces | Projets comme le porte-guitare découpé (S1) |
| N7 ✅ retenue | **Bobine bientôt vide** : coupe l'impression en deux avec pause de changement de bobine au bon moment si le poids restant ne suffit pas | Évite une impression ratée à 90 % |
| N8 | **Conversion d'un STL en pièce paramétrique simple** (boîte, équerre, plaque) pour pouvoir ensuite modifier les cotes en parlant | Rendre modifiable ce qui ne l'était pas |

### Idées plus larges — N9 à N22 (à arbitrer)

**L'atelier autour de l'imprimante**
| # | Idée | Pourquoi |
|---|---|---|
| N9 | **Domotique (Home Assistant)** : prise connectée qui coupe l'imprimante une fois refroidie, purificateur d'air allumé pour ABS/ASA, lumière de la pièce, alerte détecteur de fumée → pause demandée à l'humain | L'atelier devient sûr et autonome la nuit |
| N10 | **Séchage et humidité des bobines** : sécheur connecté ou capteur, rappel « ce PETG a pris l'humidité, sèche-le 4 h à 65 °C » avant une impression longue | Cause n°1 des fils et bulles |
| N11 | **Stock et réapprovisionnement** : alerte quand une matière passe sous un seuil, liste de courses (bobines, inserts, vis, aimants) avec liens d'achat | Ne jamais manquer en plein projet |
| N12 | **Planning de la machine sur la semaine** : file, réservations, heures creuses d'électricité, impressions longues placées la nuit | Rentabiliser la P1S |

**Du projet à l'objet fini**
| # | Idée | Pourquoi |
|---|---|---|
| N13 ✅ retenue | **Mode projet complet** : « je veux un support mural pour 3 guitares » → découpage en pièces, nomenclature (pièces imprimées + vis/chevilles/inserts), planning d'impression, notice | Passer de l'idée à l'objet sans rien oublier |
| N14 | **Préparation au post-traitement** : surépaisseur pour ponçage, préparation à la galvanoplastie (surface conductrice, accroches, trous d'évacuation), peinture, lissage vapeur | Ton porte-guitare Liquid Chrome |
| N15 ✅ retenue | **Moules et coulée** : générer un moule en 2 parties à partir d'une pièce (silicone, résine, béton, chocolat) avec évents et clés d'alignement | Étend l'atelier au-delà du plastique imprimé |
| N16 ✅ retenue | **Aller-retour avec Blender** : envoyer une sculpture de Blender vers Printalk (réparée, orientée, chiffrée) et renvoyer le résultat de résistance en couleurs dans Blender | Ton flux réel Blender + Printalk |

**Comprendre et progresser**
| # | Idée | Pourquoi |
|---|---|---|
| N17 | **Explique-moi ce réglage** : Claude explique chaque option en contexte, avec l'effet visuel dans la visionneuse (avant/après) | Apprendre en imprimant |
| N18 ✅ retenue | **Profil maître par matière** qui apprend de chaque impression réussie ou ratée (journal + photos) et propose ses propres réglages | Le mémoire d'impression devient un vrai réglage |
| N19 ✅ retenue | **Journal photo automatique** : photo à 0 %, 25 %, 50 %, 100 %, rangées avec l'impression, en galerie | Garder la trace, documenter un projet |

**Au-delà de la P1S**
| # | Idée | Pourquoi |
|---|---|---|
| N20 | **Autres imprimantes** : Prusa, Klipper/Creality via Moonraker, avec les mêmes garde-fous | Ne plus dépendre d'une seule marque |
| N21 | **Commande vocale depuis le téléphone** : « Printalk, où en est l'impression ? » | Suivre l'atelier les mains prises |
| N22 ✅ retenue | **Galerie / portfolio** des objets réalisés (photos finales, réglages, coût) + modèles 3D interactifs, versions et journal photo (N19), exportable en page web ou PDF | Montrer ce qu'on fabrique |

### Blender dans Printalk (décision du 2026-11-09)
**Uniquement le Blender de l'ordinateur de l'utilisateur**, pas de Blender sur le serveur.
- Printalk fournit son **propre module complémentaire Blender** (un fichier .zip téléchargeable depuis l'interface et depuis Claude) qui se connecte à Printalk et expose ses fonctions dans Blender (envoyer la sculpture vers Printalk, recevoir la pièce réparée/orientée, la carte de résistance en couleurs, les rendus) et aux outils MCP.
- Si Printalk détecte qu'aucun Blender n'est relié, il le dit et recommande : « c'est mieux d'avoir Blender sur ton ordinateur : installe Blender, puis ce module (.zip) : Édition > Préférences > Modules complémentaires > Installer depuis le disque ». Sans Blender, tout le reste de Printalk fonctionne.
- Les rendus réalistes (N2) passent par ce Blender quand il est relié ; sinon, rendu simplifié côté serveur.

### Idées dans l'univers Bambu Lab — B1 à B12 (à arbitrer)
| # | Idée | Pourquoi |
|---|---|---|
| B1 ✅ retenue | **MakerWorld complet** : chercher, importer un modèle avec ses profils d'impression, comparer avec nos réglages, suivre les remixes | La bibliothèque Bambu à portée de phrase |
| B2 ✅ retenue | **Lecture des puces RFID des bobines Bambu** (via AMS) : inventaire automatique (matière, couleur exacte, lot, poids restant) | Plus aucune saisie de bobine |
| B3 ✅ retenue | **Catalogue officiel des filaments Bambu** (couleurs hex, finitions, températures) pour l'aperçu réaliste et le choix de couleur | Couleurs justes partout |
| B4 | **Plan d'entretien** : graissage des tiges, nettoyage carbone, buse à changer, courroies, selon les heures réelles d'impression ; rappels et guides pas à pas | Une P1S qui dure |
| B5 | **Buses et plaques** : détection/déclaration de la buse (0,2/0,4/0,6/0,8, acier trempé), refus des matières abrasives sur buse laiton, plaque conseillée par matière | Éviter les erreurs coûteuses |
| B6 | **Résultats des inspections natives** (lidar X1, première couche, caméra) remontés dans le journal et le diagnostic d'échec | Exploiter tous les capteurs Bambu |
| B7 | **Modules H2D** : buse double, laser et découpe (gravure, découpe de carton/bois) avec les mêmes garde-fous humains | Toute la gamme Bambu |
| B8 | **AMS 2 Pro / AMS HT** : séchage des bobines piloté, réglage d'humidité par matière | Lien avec N10 |
| B9 ✅ retenue | **Firmware** : suivi des versions, notes de version, alerte si une mise à jour touche le mode LAN/développeur (risque Authorization Control) | Ne pas se faire surprendre |
| B10 ✅ retenue | **Projets MakerWorld multi-plaques** (3MF avec plaques et couleurs) importés tels quels, découpés et lancés plaque par plaque | Les projets populaires en un geste |
| B11 ✅ retenue | **Publication MakerWorld assistée** : fiche, photos de la galerie (N22), profils testés, sans publication automatique : tout ce qui peut être fait et simplifié passe par le MCP, l'humain ne fait que le geste final minimal | Partager ses créations proprement |
| B12 ✅ retenue | **Calibrations natives Bambu** déclenchées et relues (débit dynamique, vibrations) avec historique par buse et par matière | Lien avec S10 |

## Règle d'architecture (2026-11-09)
**Aucune API d'IA dans Printalk.** Tout ce que fait le serveur est du calcul déterministe (géométrie, découpe Bambu Studio, éléments finis, statistiques). L'intelligence conversationnelle vient uniquement du Claude (ou autre client MCP) de l'utilisateur, avec son propre abonnement. Les résumés produits par le serveur sont des textes construits à partir de chiffres, que Claude présente.

## Idées « effet waouh » retenues (2026-11-09)

| # | Fonction | Comment |
|---|---|---|
| W1 | **Sauver une impression ratée à 80 %** | 1) Hauteur saine : dernière couche réussie transmise par l'imprimante + **mesure au pied à coulisse demandée à l'humain** (fait foi). 2) L'humain arase le haut abîmé et donne la nouvelle hauteur ; Printalk **coupe le modèle à cette cote** et garde la partie haute. 3) Alignement au choix : **collier** (la partie haute descend de quelques mm et s'emboîte autour de la base), **gabarit de perçage** imprimé qui épouse la section + goupilles intégrées dans la partie haute, ou **collage à plat** si la surface est grande. 4) Découpe de la partie haute (souvent tête en bas pour une face de collage plane), confirmation humaine habituelle. 5) Conseil de colle selon la matière (CA, époxy pour PETG) et temps de séchage. Pure géométrie (lien S1 couper, N5 logements). |
| W2 | **L'optimiseur de nuit (sans IA)** | Tâche planifiée la nuit sur le serveur : pour chaque pièce de la semaine, essai systématique de dizaines d'orientations et de réglages, notés par des mesures (surplombs et volume de supports, surface de contact, **temps et grammes réels via Bambu Studio**, résistance via le calcul par éléments finis). Garde les meilleures, écrit un résumé chiffré (« orientation 30° : −40 % de temps, même résistance, supports −60 % »), présenté par Claude à l'ouverture de la conversation et visible dans une carte. Bornes de CPU, n'interfère jamais avec une impression ou une découpe en cours. |
