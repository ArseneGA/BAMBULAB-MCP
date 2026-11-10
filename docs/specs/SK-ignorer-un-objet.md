# SK — Ignorer un objet raté en cours d'impression

Spec pour un agent de code. Lire d'abord `docs/specs/README.md` § 2 (règles communes), `docs/SECURITY.md` (§§ 3, 4, 9, 12, 13, 15), `docs/BACKLOG.md` (règle « aucune API d'IA dans Printalk »), `docs/UX.md`, `docs/SUIVI-PROJET.md`. Idée de l'utilisateur du 2026-11-10 (message vocal transcrit). **Cette spec est de la documentation : aucun code n'a été écrit pour la produire.** Le dépôt a été lu le 2026-11-10 (commit `847f259`) ; l'agent relit `git log` et les fichiers cités avant de coder.

Codes retenus : **SK0 à SK6** (préfixe « SK » = *skip*, ignorer). Aucun conflit dans `docs/BACKLOG.md` ni dans `docs/specs/`.

| Code | Nom | Étape |
|---|---|---|
| SK0 | Essai sur la vraie P1S (commande + confirmation), par ligne de commande humaine | **A, avant tout le reste** |
| SK1 | Objets de chaque impression : identifiants, noms, emprises, connus et persistés | B |
| SK2 | Capacité : le modèle, le firmware et le fichier permettent-ils d'ignorer un objet ? | B |
| SK3 | Demande humaine d'ignorer un ou plusieurs objets (outil, page, carte) | B |
| SK4 | Exécution unique, vérification par l'imprimante, audit et journal | B |
| SK5 | Conséquences sur le suivi : pièces imprimées, grammes et temps économisés, verdict par objet | C |
| SK6 | Aide à désigner l'objet (plateau numéroté à côté de la photo) | C |

## 1. Objectif et valeur

Sur une plaque de plusieurs objets (l'utilisateur imprime par 10), il arrive qu'**un seul objet rate** (décollement, spaghetti local). Aujourd'hui le choix est mauvais : **tout arrêter** (perdre les objets sains) ou **laisser continuer** (gâcher de la matière, risquer que l'objet raté arrache ou salisse les autres). La fonction permet de **ne plus imprimer l'objet raté** et de **laisser les autres finir**, avec un geste humain unique.

Valeur chiffrable : grammes et heures économisés, objets sauvés ; directement utile aux vendeurs et fermes (G2, G5) et aux séries (N3).

## 2. Ce que Bambu permet (recherche du 2026-11-10, sources dans le rapport de recherche résumé ici)

- **Commande MQTT** (confirmée par le code source de Bambu Studio, `DeviceManager.cpp`, `command_task_partskip`, et par OpenBambuAPI) : publiée sur `device/{serial}/request`, QoS 1 :
  `{"print":{"sequence_id":"<n>","command":"skip_objects","obj_list":[130,174]}}`
- **Identifiants** : les `identify_id` de `Metadata/slice_info.config` du `.gcode.3mf` (communautaire, concordant). Noms et boîtes dans `Metadata/plate_N.json`. **Pas** la position dans la liste.
- **Confirmation** : le rapport d'état contient `print.s_obj`, tableau des identifiants déjà ignorés (code source de Studio). **Pas d'accusé dédié** : la preuve est l'apparition des identifiants dans `s_obj`.
- **Condition** : plaque découpée avec l'étiquetage des objets actif (`label_object_enabled`), objets non groupés ; sinon pas de marqueurs et rien à ignorer.
- **Prise en charge officielle connue** : X1C (firmware ≥ 01.09.02.12, Studio ≥ 2.2) et H2D ; wiki cité : séries X1, H2, P2S. **P1P/P1S : via Bambu Handy (cloud) depuis 2023** ; un utilisateur signale en 2026 que, **sur P1S en LAN seul (firmware 1.10), le bouton est absent de Studio** ; aucune réponse officielle. **On ne sait pas si la P1S accepte `skip_objects` en LAN : c'est l'objet de SK0.**
- **Limites** : la tour de purge (nettoyage) ne s'ignore pas ; rien de sourcé sur les supports partagés entre objets ou les objets qui se touchent (à traiter comme un risque, § 6).

## 3. État actuel dans le code (lu le 2026-11-10)

- **Objets connus à la découpe** : `slicer.ObjectReport` (`internal/slicer/objects.go:46`) lit `slice_info.config` (`identify_id`, `name`), le G-code (`; start printing object, unique label id: …`) et rend `ObjectUse{Name, IdentifyID, ExtrudedMM, Grams, Layers, MaxZ, ExtentX, ExtentY}` (emprise en repère machine). Stocké dans `ArtifactSummary.Objects` (`internal/printing/types.go:157`), calculé par `plates.go:1004` et `plate_preview.go:358`. Erreur si aucun marqueur (`objects.go:183`).
- **Plan de plateau** : `PlanObject{Index, Name, X, Y, W, D, H}` (`internal/printing/plates.go:79-96`) ; noms uniques dans un plan. Aucun lien persistant objet → `identify_id`.
- **Étiquetage** : aucun réglage `label_object_enabled` forcé ni vérifié (seule mention : commentaire `internal/slicer/objects.go:44`, « activé par défaut dans les profils Bambu »).
- **Imprimante** : `internal/printer/bambulan.go` (`Pause/Resume/Stop` l.731-747 via `control()`), `start.go:51`. Liste blanche des accessoires `internal/printer/accessories.go:320` (refuse tout le reste : `skip_objects` y est refusé, c'est voulu). `status.go:68-111` (`toStatus`) **ne lit pas `s_obj`** (le rapport fusionné le contient brut).
- **Simulateur** : `internal/printer/sim/sim.go:448-467` répond « unsupported command » à `skip_objects` ; pas de `s_obj`.
- **Flux humains existants** : arrêt (`cancel_print` → `/stop/{id}`, `printing/start.go:518-538`, `stop.go:22`, `approve_stop_with_code`), actions (`printing/actions.go` : types `resume`, `load_filament`, `cleanup_files`, `RequestResume` l.140, `ExecuteAction` l.261, page l.378 ; `mcpserver/recovery_tools.go`). Règles : SECURITY § 15 (une demande = une commande, consommée avant l'envoi, jamais renvoyée ; trace `requested/approved/sent/acked/verified`).
- **Caméra** : `internal/camera/detect.go` compare à la référence (grille 160×90) ; `Report` n'expose **aucune coordonnée** ; aucune calibration caméra → plateau. **Localiser automatiquement l'objet raté n'est pas possible aujourd'hui** (hors périmètre, § 2.2).

## 4. Périmètre / hors périmètre

**Dans le périmètre** : SK0 à SK6 ; outils MCP, routes HTTP, page serveur de confirmation humaine, carte MCP, fiche impression (PJ10) et Accueil (G10) enrichis ; simulateur ; docs `docs/IGNORER-OBJET.md`.

**Hors périmètre** :
- **Toute décision automatique.** Aucune détection (caméra, HMS, IA) n'ignore un objet d'elle-même ; aucun outil ni carte ne l'exécute sans geste humain sur une page serveur. Le serveur ne « devine » pas l'objet raté.
- Localisation automatique de l'objet par la caméra (calibration caméra → plateau) : idée future (§ 11), pas dans cette spec.
- Réécriture du G-code en cours d'impression, pause/reprise pour « enlever » un objet : **interdit** (fragile, dangereux) ; on utilise la commande native de l'imprimante ou rien.
- « Réintégrer » un objet ignoré : impossible côté imprimante, donc non proposé (l'action est **irréversible**, et la page le dit).
- Imprimantes non Bambu.

## 5. Décisions de conception

**D1 — Commande native seulement.** `skip_objects` avec `obj_list` = `identify_id`. Si la machine ne la prend pas en charge, la fonction est **absente** pour cette machine (avec la raison), jamais simulée par pause + G-code.
**D2 — Geste humain, comme l'arrêt.** Ignorer un objet modifie une impression en cours, de façon irréversible : même niveau d'exigence que l'arrêt (page serveur `/action/{id}` d'un navigateur enregistré, ou code personnel / passkey dans la carte, validité courte, une demande = une commande, consommée avant l'envoi). Le modèle peut **demander** (créer la demande), jamais **approuver**.
**D3 — Identifiants vérifiés, pas devinés.** La demande ne peut viser que des `identify_id` **présents dans le fichier de cette impression** (SK1) et **pas déjà ignorés** (`s_obj`). Interdit d'ignorer **tous** les objets restants (proposer l'arrêt à la place).
**D4 — Preuve par l'imprimante.** Une action réussie = identifiants visibles dans `s_obj` dans un délai borné (défaut 20 s). Sinon : « envoyée, non confirmée », jamais renvoyée automatiquement (SECURITY § 15).
**D5 — Capacité prudente.** Proposée seulement si : fichier étiqueté (marqueurs présents), plaque à ≥ 2 objets, imprimante en impression (pas en préparation, pas en pause d'erreur), modèle et firmware dans la table « pris en charge », **ou** P1S/P1P si SK0 l'a prouvé ; `s_obj` présent dans le rapport est un indice, pas une preuve.
**D6 — Mesuré / calculé / estimé.** Grammes et temps économisés = **estimés** (part restante de l'objet d'après `ObjectUse` × fraction de couches restante) ; objets ignorés = **mesurés** (`s_obj`).

## 6. Exigences par élément

### SK0 — Essai sur la vraie P1S (étape A, avant tout le reste)

**But.** Savoir, avant d'investir, si la P1S en LAN accepte `skip_objects`.
**Livrable minimal** :
- lecture de `s_obj` dans `Status` (champ facultatif `SkippedObjects []int`), visible dans `get_printer_status` / `get_print_status` ;
- un validateur dédié `ValidateSkipObjectsPayload` (entiers > 0, non vide, au plus 64, sans doublon) et une méthode `SkipObjects(ctx, ids []int)` dans le client, **hors** de la liste blanche des accessoires ;
- une **sous-commande humaine** `bambud skip-objects --job <print_job_id> --ids 130,174` (ligne de commande sur le serveur = geste humain, comme `bambud enroll`) qui : vérifie que les identifiants sont dans le fichier de l'impression (SK1 minimal : relecture de `slice_info.config` de l'artefact), refuse si tous les objets seraient ignorés, demande une confirmation interactive (`Ignorer 1 objet (« Pied #3 », id 174) ? Cette action est irréversible [o/N]`), envoie une fois, attend `s_obj` 20 s, écrit l'audit, affiche le résultat ;
- simulateur : `skip_objects` accepté pendant une impression, `s_obj` mis à jour, l'objet ignoré cesse de consommer (pour les tests) ;
- une page « mode opératoire » dans `docs/IGNORER-OBJET.md` pour l'essai humain.
**Mode opératoire de l'essai (humain, sur la P1S)** : découper une plaque de **3 petits objets séparés** (par ex. 3 cubes de 20 mm) avec l'étiquetage actif ; lancer normalement (confirmation humaine) ; vers 20 % de la hauteur, lancer `bambud skip-objects` sur un objet ; observer : `s_obj` le contient-il ? l'objet cesse-t-il d'être imprimé (photo caméra et vue directe) ? les deux autres finissent-ils bien ? Noter le firmware, le résultat et les écarts dans `docs/IGNORER-OBJET.md`.
**Sortie** : *P1S en LAN = pris en charge* (on livre SK1 à SK6 pour la P1S) ou *non pris en charge* (on livre SK1 à SK6 pour les modèles officiellement pris en charge seulement, et la P1S affiche « non pris en charge en réseau local par ce firmware »).
**CA.**
- CA0.1 Le validateur refuse : liste vide, entier ≤ 0, doublon, > 64 éléments, clé en trop ; seul `{"print":{"sequence_id","command":"skip_objects","obj_list"}}` peut sortir (test de forme exacte).
- CA0.2 La sous-commande refuse un identifiant absent du fichier de l'impression, un objet déjà dans `s_obj`, et le cas « tous les objets restants » (message : « utilisez l'arrêt »).
- CA0.3 Avec le simulateur : `s_obj` contient l'identifiant en < 20 s ; statut « vérifié » ; audit `print.skip_requested/approved/sent/verified`.
- CA0.4 Aucun outil MCP ni route JSON ne permet d'envoyer `skip_objects` dans SK0 (seule la ligne de commande humaine) ; test de source.

### SK1 — Objets de chaque impression

**But.** Pour chaque impression : liste des objets avec `identify_id`, nom lisible (nom du plan si la plaque vient de `plan_plate`, sinon nom du fichier), emprise (repère machine), grammes et couches prévus.
**Données.** Réutiliser `ArtifactSummary.Objects` (`ObjectUse`) ; s'assurer qu'il est calculé **pour toute impression** (pas seulement les plaques planifiées), relu depuis l'artefact si absent. Lier `ObjectUse.Name` ↔ `PlanObject.Name` quand un plan existe. Aucune nouvelle table si `ArtifactSummary` suffit ; sinon migration **additive**.
**Étiquetage garanti.** À la découpe faite par Printalk : vérifier que l'étiquetage est actif (lire `label_object_enabled` dans `project_settings.config` ; s'il est explicitement désactivé, l'activer **si la clé est autorisée** par la liste blanche, sinon avertir). `validate_print_artifact` : sur une plaque à ≥ 2 objets **sans** marqueurs, avertissement non bloquant « impossible d'ignorer un objet pendant cette impression ».
**À vérifier au codage** : que l'identifiant du G-code (`unique label id`) et l'`identify_id` de `slice_info.config` sont les mêmes ; sinon, **`slice_info.config` fait foi** (c'est ce qu'envoie Bambu Studio) ; test sur un vrai `.gcode.3mf` produit par Bambu Studio (jeu d'essai du dépôt).
**CA.** CA1.1 Plaque de 3 objets découpée → 3 objets avec `identify_id`, nom, emprise, grammes ; noms du plan repris. CA1.2 Fichier sans marqueurs → liste vide + raison, avertissement à la validation, aucune erreur bloquante. CA1.3 Fichier d'une seule pièce → fonction non proposée.

### SK2 — Capacité

**But.** Répondre honnêtement à « peut-on ignorer un objet sur cette impression, maintenant ? ».
**Calcul** (fonction pure, testée) : `CanSkip(model, firmware, file, state, sk0Result) → {ok, reason}` selon D5. Table des modèles pris en charge dans `internal/printer` (X1/X1C/X1E, H2D/H2S, P2S : « pris en charge selon Bambu » ; P1S/P1P : selon SK0 ; A1/A1 mini : « non confirmé en LAN », désactivé par défaut) ; seuil de firmware connu pour X1C (01.09.02.12) ; inconnu = « non confirmé ». Un réglage administrateur permet de **forcer** l'essai pour un modèle non confirmé (désactivé par défaut, journalisé).
**Exposé dans** : `get_print_status` (`skip.available`, `skip.reason`), `get_capabilities`, la fiche impression.
**CA.** CA2.1 Chaque raison de refus a son message (fichier non étiqueté, une seule pièce, modèle non pris en charge, firmware trop ancien, impression pas en cours, tous les objets déjà ignorés). CA2.2 Sans résultat SK0 positif, la P1S répond « non confirmé en réseau local ».

### SK3 — Demande humaine (outil, page, carte)

**Outil** `request_skip_objects(print_job_id, objects: [identify_id | nom], reason?)` (écriture, **ne fait que créer une demande**) : vérifie SK2 et D3, crée une action `skip_objects` dans `internal/printing/actions.go` (nouveau type, à côté de `resume`), validité **2 minutes** comme le lancement (ou 10 min comme l'arrêt : choisir 2 min, l'impression avance), renvoie le lien de la page et la carte. Nom d'outil sans `approve|start|launch|stop|resume|cancel`.
**Page serveur** `/action/{id}` (gabarit existant) : titre « Ignorer N objet(s) pendant l'impression », **photo actuelle du plateau** (rafraîchissable) **à côté du plateau numéroté** (SK6), liste des objets visés (nom, numéro, part restante estimée), objets restants qui continueront, **avertissements** (irréversible ; la tour de purge continue ; objets qui se touchent ou supports partagés possibles : l'objet ignoré peut laisser un support orphelin), case à cocher « J'ai vérifié sur la photo que c'est le bon objet », validation par le navigateur enregistré, ou code personnel / passkey (mêmes règles que l'arrêt : verrou 3 échecs, délai 3 s).
**Carte MCP** `ui://bambu/skip.html` : même contenu, champ du code personnel, outil app-only `approve_skip_with_code` (même mécanisme que `approve_stop_with_code`, inapprouvable par le modèle : `TestNoMCPToolCanApproveAConfirmation` étendu).
**Web** : bouton « Ignorer un objet… » dans la fiche impression (`/impressions/:id`) et dans la carte « En direct » de l'Accueil **seulement si SK2 dit oui** ; il ouvre un choix d'objet puis **mène à la page serveur** (lien), jamais d'envoi direct.
**CA.** CA3.1 Le modèle peut créer la demande mais aucun chemin MCP ni JSON ne l'approuve (tests de garde existants étendus). CA3.2 Demande expirée, déjà consommée, ou impression terminée entre-temps : refus clair, rien n'est envoyé. CA3.3 La page contient tous les éléments listés (test de gabarit) et aucun secret. CA3.4 Viser un objet inconnu, déjà ignoré, ou tous les objets restants : refus à la création **et** à l'exécution.

### SK4 — Exécution, vérification, audit, journal

`ExecuteAction` pour `skip_objects` : consomme la demande **avant** l'envoi, revérifie SK2/D3 (état courant), envoie **une fois**, attend `s_obj` (défaut 20 s), statut `verified` / `sent_unconfirmed` / `refused`. Audit : `print.skip_requested`, `print.skip_approved`, `print.skip_sent`, `print.skip_verified` (identifiants et noms, jamais de secret). Événement dans l'historique de l'impression (`job.events`, comme FB14). Un `s_obj` qui change **sans** demande de Printalk (geste fait depuis Handy ou l'écran) est inscrit « hors Printalk » (FB14).
**CA.** CA4.1 Simulateur : demande approuvée → `s_obj` mis à jour → `verified`, objet ignoré visible dans `get_print_status` et la fiche. CA4.2 Simulateur qui n'applique pas la commande → `sent_unconfirmed` après le délai, aucune nouvelle tentative. CA4.3 `s_obj` modifié par un autre client → événement « hors Printalk ». CA4.4 Aucune pause, reprise, arrêt ni G-code n'est envoyé par ce chemin (test : seule la commande `skip_objects` sort).

### SK5 — Conséquences sur le suivi (G9)

- **Unités imprimées** (D7 de G9) : une impression qui finit à 100 % avec `k` objets ignorés sur `n` compte **n − k** unités imprimées (champ `part_count` du journal et compteurs de la fiche pièce/projet) ; les objets ignorés sont listés à part.
- **Verdict** : le verdict PJ11 reste par impression ; la fiche impression montre les objets ignorés avec la raison donnée.
- **Économie estimée** : grammes et temps non imprimés de chaque objet ignoré = estimé (D6), affichés dans la fiche impression et les chiffres du mois (« matière économisée »).
**CA.** CA5.1 Plaque de 10, 1 ignoré à 30 %, fin à 100 % → « 9 unités imprimées, 1 ignorée », grammes économisés ≈ 70 % de l'objet (estimé). CA5.2 Les statistiques de réussite ne comptent pas l'objet ignoré comme réussi.

### SK6 — Aide à désigner l'objet

**But.** Que l'humain (aidé de Claude dans la conversation) désigne le bon objet sans erreur.
**v1 (cette spec)** : une image du **plateau numéroté** générée par le serveur à partir des emprises SK1 (rectangles numérotés 1…n avec le nom, vue de dessus, même orientation que la caméra annoncée dans la doc), affichée **à côté** de la photo actuelle de la caméra sur la page, la carte et la fiche. Claude, dans la conversation, peut regarder les deux images (`get_camera_snapshot` + l'image numérotée) et **proposer** un numéro ; c'est l'humain qui confirme sur la page. Aucun calcul de localisation côté serveur.
**CA.** CA6.1 L'image numérotée correspond aux emprises (test sur données simulées). CA6.2 Sans emprises : liste textuelle seulement, pas d'erreur.

## 7. Sécurité et règles non négociables

- Aucun lancement, arrêt, pause ou reprise ajouté ; aucune réécriture de G-code ; aucune action automatique (ni par détection, ni par horaire, ni par Claude).
- `skip_objects` sort **uniquement** par `SkipObjects` (validateur dédié), appelé **uniquement** depuis la sous-commande humaine (SK0) et `ExecuteAction` après approbation humaine (SK3/SK4) ; test de source qui l'impose.
- Une demande = une commande, consommée avant l'envoi, jamais renvoyée (SECURITY § 15) ; validité 2 minutes ; liée à l'impression et aux identifiants.
- Page et carte sans secret ; images par les routes existantes ; aucun chemin serveur.
- Aucune API d'IA ; capacité, économies, image numérotée = calculs déterministes.
- Nouvelle section `docs/SECURITY.md` (par l'orchestrateur à la fusion) : « Ignorer un objet » : pouvoir nouveau, irréversible, même niveau que l'arrêt.

## 8. Compatibilité production

Serveur Linux, P1S en LAN + Développeur, Claude Desktop via `mcp-remote` : aucune action après mise à jour. Si SK0 est négatif, la P1S affiche « non pris en charge » et rien d'autre ne change. Outils existants inchangés ; champs ajoutés facultatifs ; migration additive seulement si nécessaire ; `tools/list` conforme.

## 9. Ordre de livraison

| Étape | Contenu | Condition |
|---|---|---|
| **A** | SK0 (lecture `s_obj`, validateur, `SkipObjects`, sous-commande humaine, simulateur, mode opératoire) | — |
| *essai humain* | l'utilisateur fait l'essai sur la P1S (§ 6 SK0) | après la mise à jour du serveur |
| **B** | SK1, SK2, SK3, SK4 | après l'essai (P1S incluse ou non selon le résultat) |
| **C** | SK5, SK6 | après B |

## 10. Fichiers probablement touchés

Nouveaux : `internal/printer/skip.go` (+ tests), `internal/printer/sim/skip.go`, `internal/printing/skip.go` (+ tests), `cmd/bambud/skip.go` (sous-commande), `internal/mcpserver/skip_tools.go`, `web/mcp-app/cards/skip.tsx` (+ test), `docs/IGNORER-OBJET.md`.
Modifiés (additif) : `internal/printer/status.go` (`s_obj`), `internal/printing/actions.go` (type `skip_objects`), `pages.go` (gabarit), `internal/slicer/objects.go` (relecture si besoin), `internal/printing/validate.go` (avertissement), `internal/mcpserver/server.go` (une ligne), garde-fous (`noapprove_test.go`, `spec_conformance_test.go`, `secrets_test.go`), `web/src/pages/impressions/PrintSheet.tsx` et `web/src/pages/dashboard/LiveCard.tsx` (lien vers la demande).

## 11. Risques, questions ouvertes, taille

Taille : **M** (A ≈ S, B ≈ M, C ≈ S).
Risques : **P1S en LAN non prise en charge** (SK0 le tranche) ; identifiants G-code ≠ `identify_id` (SK1 le vérifie) ; supports partagés ou objets collés : l'objet ignoré peut laisser des supports ou abîmer un voisin (avertissement, pas de calcul) ; tour de purge non ignorable ; erreur de désignation humaine (atténuée par SK6 et la case « j'ai vérifié »).
Idée future (hors spec) : calibration caméra → plateau (4 coins du plateau sur la photo de référence) pour que la caméra **signale** dans quelle zone apparaît un défaut et quel objet s'y trouve ; toujours une suggestion, jamais une action.
Questions ouvertes pour l'utilisateur : **aucune bloquante**. À confirmer à l'usage : délai de validité de la demande (2 min proposé) et le seuil « au plus 64 objets ».

## 12. Consigne finale pour l'agent de code

Travaille dans ton worktree. Livre **l'étape A seule d'abord** et rends la main : l'étape B dépend de l'essai humain sur la P1S. Avant de rendre : `go vet ./...` ; `go test -short ./... -count=1` ; `GOOS=linux CGO_ENABLED=0 go build ./...` ; si `web/` est touché : `cd web && npm ci && npm run build && npx vitest run`. L'imprimante est **toujours simulée** dans les tests. N'édite pas `docs/UTILISATION.md`, `docs/ETAT.md` ni `docs/BACKLOG.md` ; écris `docs/IGNORER-OBJET.md`. Commits en français, une ligne finale `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. **Ne pas pousser.**
