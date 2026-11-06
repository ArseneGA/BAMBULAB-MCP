# J7b — chaîne d'impression de bout en bout (`internal/printing`)

Livrables : `internal/printing` (service), `internal/mcpserver/print_tools.go` (17 outils MCP, une ligne dans `New`), `internal/httpapi/printing.go` (routes + pages, une ligne dans `NewHandler`), branchement dans `internal/core` (`Core.Printing`), deux petites exportations (`catalog.RunWorker/EnsureDerived/RegisterGenerated`, `goals.GetProfileRevision`).
Sources : `docs/DOSSIER.md` §1, §6, §10 ; J1 (machine d'états, `ConsumeConfirmation`, kinds physiques), J5 (slicer, archive), J6 (variantes), J7 (adaptateur, simulateur).
**Aucune imprimante réelle n'a été contactée** : tout est validé contre `internal/printer/sim` (127.0.0.1) ; un mock ne vaut pas un essai matériel.

## 1. Séquence imposée (et ce qui la garantit)

`execute_headless_slice` (DRAFT → PREPARED → SLICING → VALIDATED) → `validate_print_artifact` → `upload_print_artifact` (UPLOADING → UPLOADED → AWAITING_PRINT_CONFIRMATION) → question posée à l'utilisateur → `request_print_confirmation` → **l'humain confirme sur une page web locale** → `start_print` (START_REQUESTED → PRINTING observé, ou START_UNKNOWN) → suivi (COMPLETED / FAILED).

| Règle | Mécanisme |
|---|---|
| L'envoi ne chauffe ni ne déplace rien | le dépôt n'émet que FTPS ; test : aucune commande `project_file` reçue par le simulateur après dépôt, état `IDLE` |
| Une autorisation ne vaut pas pour la suivante | la confirmation est liée au `PrintJob` (`ConsumeConfirmation(..., printJobID)`), à l'imprimante, à l'empreinte, au plateau, aux matières ; usage unique en base ; test : 2e impression du même fichier refusée avec l'ancienne confirmation |
| Pas de lancement sans confirmation consommée atomiquement | le `ProofVerifier` de l'adaptateur appelle `store.ConsumeConfirmation` **juste avant la publication MQTT**, après les contrôles d'état de l'imprimante : un refus d'état (`ErrNotReady`…) ne brûle donc pas la confirmation ; test : 5 `start_print` concurrents → 1 seule commande |
| `kind` physique, idempotent, jamais rejoué | `upload_print_artifact` et `start_print` passent par la file (`jobs.physicalKind` : clé d'idempotence obligatoire ; `start_print` est `neverReplay`) ; un travail `start_print` interrompu devient `unknown`, jamais relancé |
| Résultat incertain | START_UNKNOWN : `start_print` refuse, `cancel_print` refuse, seul `reconcile_print` agit (lecture d'état, jamais de commande de lancement) |
| Redémarrage du service pendant un lancement | `Recover` : START_REQUESTED → START_UNKNOWN ; PRINTING → suivi repris |
| Plateau vide | l'imprimante ne peut pas le savoir : attestation humaine obligatoire (case à cocher) ; `get_printer_status` répond toujours `plate_empty: "inconnu"` |

## 2. Mécanisme de confirmation choisi

Dossier §10 : « un simple booléen inventé par un agent n'est pas une preuve suffisante ». Choix :

1. `request_print_confirmation` crée une `PrintConfirmation` (jeton 256 bits, TTL 10 min par défaut, max 24 h). Le **jeton reste dans la mémoire du service** : il n'est ni renvoyé au modèle, ni stocké en clair (la base ne garde que son hachage ; test : absent de tous les fichiers de données et de la réponse). Le modèle reçoit `confirmation_id`, `confirm_url` et les éléments liés (fichier, imprimante, empreinte, plateau, matières, durée estimée).
2. **Canal humain principal : page `/confirm/{id}`** (HTML serveur sans JavaScript). L'humain voit le résumé, coche « le plateau est vide et dégagé », clique « Lancer l'impression ». Le formulaire porte un nonce à usage unique ; en-têtes `X-Frame-Options: DENY`, CSP `frame-ancestors 'none'`, `no-store` ; protection cross-origin du garde HTTP.
3. **Canal secondaire : outil MCP `confirm_print_from_app`** (et `get_print_confirmation_view`), marqués `_meta.ui.visibility: ["app"]` pour que l'hôte les cache au modèle. Même effet (approbation humaine enregistrée), même refus sans `plate_clear`.
4. `start_print` ne reçoit que `confirmation_id` : il refuse tant que l'humain n'a pas approuvé, puis le service **consomme lui-même** la confirmation avec le jeton gardé en mémoire. Aucune route JSON ne permet d'approuver.

**Limites assumées** (à lire avant de se fier à ce mécanisme) :
- Ce n'est pas une preuve cryptographique de présence humaine : c'est une barrière qui sort la décision du canal que contrôle le modèle. Un agent qui disposerait lui-même d'un client HTTP local (hors MCP) pourrait lire la page puis soumettre le formulaire ; la mise en œuvre de `visibility:["app"]` dépend de l'hôte (Claude Desktop non qualifié ici).
- Le jeton n'existe qu'en mémoire : un redémarrage du service entre la demande et l'approbation oblige à refaire `request_print_confirmation` (comportement voulu).
- Avec `--api-token` (écoute hors loopback) le garde exige `Authorization: Bearer` sur **toutes** les routes, pages comprises : un navigateur ne peut pas l'envoyer. Hors loopback, mettre un proxy qui ajoute l'en-tête ; en loopback (cas par défaut) rien à faire.

## 3. Registre d'imprimantes et secrets

- `list_printer_models` : modèles semés **depuis les profils de Bambu Studio** (`printing.SeedPrinterModels`, remplace le TODO de `contracts.SeedPrinterModels`) : volume d'impression, buses, plateaux, source. Tout le reste (caisson, AMS, températures, caméra) reste **nil = inconnu**.
- `add_printer` : modèle, hôte, série, buse, nombre d'AMS (déclaré), ports, empreinte de certificat (non secrète). **Aucun paramètre pour le code d'accès** (test : absent du schéma d'entrée ; un champ `access_code` dans le JSON HTTP est refusé). L'outil renvoie `setup_url` = `/setup/printer/{id}` : page HTML où l'humain saisit le code.
- Coffre : `FileSecretStore`, AES-256-GCM (l'identifiant d'imprimante est la donnée authentifiée : une entrée copiée sous un autre identifiant ne se déchiffre pas), fichier `secrets/printers.secrets` ; clé maître `secrets/master.key` **enveloppée par DPAPI sous Windows** (liée au compte), sinon fichier 0600 + avertissement journalisé. Pas de trousseau OS (pas de dépendance cgo). Le code n'est ni dans la base, ni dans l'audit, ni dans les journaux, ni dans les réponses ; `accessCodeProvider` masque `%v/%s/%#v`.
- `get_printer_status` : état normalisé + **fraîcheur** (`age_seconds`, `stale`, `connected`) ; imprimante sans code ou injoignable → `offline` avec la raison, sans erreur d'outil.
- `AllowStartFromFinish` (par imprimante, faux par défaut) : J7 §5 point 8 non validé sur matériel. Quand il est vrai, le service **vérifie en plus** qu'une activité de CE fichier a été observée après la commande (sinon START_UNKNOWN) et n'attribue un `FINISH` à l'impression qu'après avoir vu une activité (`seen_active`), car l'adaptateur peut prendre le `FINISH` précédent pour un succès.

## 4. Outils MCP ajoutés (17)

`list_printer_models`, `list_printers`, `add_printer`, `get_printer_status`, `execute_headless_slice`, `validate_print_artifact`, `upload_print_artifact`, `request_print_confirmation`, `start_print`, `get_print_status`, `cancel_print`, `reconcile_print`, `capture_preview`, `create_parametric_model`, `modify_geometry`, et les deux outils d'application `get_print_confirmation_view`, `confirm_print_from_app`. `cancel_job` (existant) aiguille un identifiant `pj_…` vers `cancel_print`.

Routes HTTP équivalentes (`/api/printers`, `/api/print-jobs…`, `/api/confirmations/{id}`, `/api/previews`, `/api/models/parametric`, `/api/models/modify-geometry`) et pages `/setup/printer/{id}`, `/confirm/{id}`.

- **`execute_headless_slice`** : modèle + version + imprimante (+ révision de profil ; sinon la variante appliquée par `apply_variant`, sinon profils par défaut de la machine). Travail `printing.slice` (heavy, rejouable, tolérant à la reprise). Orientation de la variante appliquée au maillage (comme `goals`). Artefact conservé par empreinte.
- **`validate_print_artifact`** : `slicer.ValidateArchive` (blocage sur échec d'un contrôle d'archive) **et** compatibilité avec l'imprimante réelle : modèle d'imprimante, buse, plaque demandée, hauteur/volume, type de plateau (avertissement), matières : emplacements AMS déclarés, doublons, famille de matière du catalogue vs type du fichier découpé, buse minimale ; matière absente du catalogue = avertissement (« non vérifiée »), jamais acceptée silencieusement. Accepte `print_job_id` ou `artifact_id` (par ex. artefact d'une variante : une impression VALIDATED est alors créée, historique « artefact existant »).
- **`upload_print_artifact`** : `idempotency_key` et `assignments` (filament → emplacement `ams1:1..ams4:4`/`ext` → matière) obligatoires ; renvoie `question` = « Le fichier X est envoyé sur la P1S. Veux-tu lancer cette impression ? » (nom court du modèle) avec imprimante, empreinte, plateau, matières, durée/masse estimées et rappel plateau vide. `ams_mapping` dérivé des affectations (position = filament − 1, valeur = unité×4 + bac, 254 = externe ; supposition à valider, cf. J7 §4). La liaison de la confirmation encode `f{n}>{emplacement}` : changer l'affectation invalide la confirmation.
- **`start_print`** : vérifications dans l'ordre : clé, impression en AWAITING, confirmation = celle de l'impression, non expirée/utilisée, approbation humaine, réservation de l'imprimante (une seule impression active), état **frais et idle** (ou finished si autorisé) sans erreur ; puis travail `start_print` → adaptateur (accusé de transport / acceptation / état observé distingués). Issues : `printing`, `start_unknown`, `rejected` (FAILED), `not_sent` (rien envoyé, confirmation intacte), `replayed`.
- **`get_print_status`** / suivi : abonnement de l'adaptateur + relevé périodique ; FINISH → COMPLETED, FAILED → FAILED, avec avancement (couche, %, restant) ; ne conclut que si le rapport désigne bien le fichier.
- **`cancel_print`** : avant lancement = annulation logique (la confirmation en attente cesse de valoir) ; SLICING = annule aussi la découpe ; PRINTING = arrêt imprimante après une **confirmation séparée** (premier appel → `stop_confirmation_id` + question ; second appel avec cet identifiant ; plus faible que le lancement par choix : arrêter est une action de sûreté) ; START_REQUESTED/UNKNOWN = refus.
- **`reconcile_print`** : `started` → PRINTING + suivi ; `not_started` → UPLOADED (nouvelle confirmation requise) ; `unknown` → reste START_UNKNOWN.
- **`capture_preview`** : PNG Go pur (z-buffer logiciel, suréchantillonnage 2×, police bitmap 5×7 intégrée, aucune dépendance). `model_id` : vue isométrique orthographique, Lambert, géométrie seule. `artifact_id`/`print_job_id` + `layer` : tracé 2D des segments extrudés d'une couche du G-code (couleur par `FEATURE`, contour du plateau si l'imprimante est connue). La légende donne les paramètres de rendu et dit **ce qui n'est pas représenté** (matériau, supports, déplacements à vide, rétractions, temps, défauts de maillage). Le PNG est conservé (`png_sha256`, route `GET /api/previews/{sha256}`).
- **`create_parametric_model`** : worker Python (`parametric`), sorties confinées dans un dossier de travail, enregistrées au catalogue (STEP + STL, version v1, provenance « génération paramétrique », hypothèses du worker). Échoue clairement (`cad_backend_missing`) si `uv`/CadQuery manquent ; testé avec un worker injecté.
- **`modify_geometry`** : opérations typées `scale` (facteur ou x,y,z, > 0), `rotate`, `mirror`, `translate`, `center_on_bed` (imprimante ou dimensions), `lay_flat` (plus grande face plane d'appui), `cut_plane` (plan axe = offset, garde au-dessus/dessous, **capuchon** par triangulation si la section est faite de contours simples non imbriqués ; sinon coupe ouverte + avertissement explicite). `base_version_id` périmée = refus. Résultat : **nouvelle version** (STL, parent = base), original intact, matrices appliquées, différences (boîte, volume, étanchéité, triangles). Échelle/rotation/miroir se font autour du centre de la boîte englobante.

## 5. Tests

`go test ./internal/printing` : 22 fonctions de test (dont tables), ~60 s.
- Unitaires : coffre chiffré (rien en clair sur disque, liaison à l'identifiant, masquage `fmt`), `parseTray`/`buildAMS`, compatibilité matières, opérations de géométrie et coupes (cube, pièce percée, trois axes), pose à plat, rendu PNG (iso et couche), jeton jamais persisté ni renvoyé, invalidation de la confirmation par changement de plateau/matière/emplacement/empreinte/imprimante, remplacement d'une confirmation par la suivante, expiration, reprise après arrêt, garde-fous de la machine d'états, semis des modèles depuis les profils de test.
- Bout en bout en processus (cœur réel + MCP en mémoire + HTTP + adaptateur Bambu réel ↔ simulateur) : chaîne complète en artefact factice (dépôt sans effet, question exacte, refus sans confirmation, refus avant approbation humaine, formulaire sans nonce/sans plateau vide/cross-origin refusés, lancement, PRINTING → COMPLETED, rejeu sans relance, 2e impression avec ancienne confirmation refusée, perte de liaison après lancement depuis FINISH, perte de liaison depuis IDLE → START_UNKNOWN → refus de relance → réconciliation, annulations par phase) ; 5 lancements concurrents → 1 commande ; routes HTTP et en-têtes de la page ; outils géométrie ; découpe sans Studio → `slicer_missing` et idempotence.
- **Chaîne réelle** `TestRealStudioFullChain` (ignorée sans Bambu Studio ou avec `-short`) : seed d'un cube 20 mm → objectifs → expérience (2 variantes) → variante appliquée → **découpe réelle Bambu Studio 02.08** → validation d'archive réelle + compatibilité P1S (modèle semé depuis les profils) → aperçus modèle et couche 20 du G-code réel → dépôt sur le simulateur → confirmation humaine → lancement → COMPLETED, empreinte inchangée. Vert sur ce poste (≈ 34 s).
- `internal/mcpserver` : le compte exact d'outils (27) devient un minimum (44) pour ne pas casser à chaque chantier parallèle.

## 6. Limites et restes à faire

- **Jamais essayé sur la vraie P1S** : `url` de `project_file`, `ams_mapping`, comportement depuis `FINISH`, cadence des rapports… restent les hypothèses de J7 §4–5. La procédure de J7 §6 s'applique, avec en plus : saisir le code via `/setup/printer/{id}` (jamais dans la conversation), épingler l'empreinte du certificat (`pinned_sha256`), tester la page `/confirm/{id}` avant tout lancement.
- Le service ne connaît ni le type de buse (durcie ou non) ni le contenu réel de l'AMS (la télémétrie AMS n'est pas lue) : les affectations sont **déclaratives** et vérifiées contre ce que l'utilisateur déclare, pas contre l'imprimante.
- Suivi : si le service redémarre en PRINTING, le suivi reprend ; un `FINISH` du même fichier vu sans activité préalable n'est pas attribué (l'impression reste PRINTING avec une note) faute de pouvoir distinguer deux tirages du même fichier.
- Une impression par imprimante à la fois (réservation) ; pas de file d'impressions.
- `capture_preview` : rasterisation CPU sans anticrénelage de texte avancé ni éclairage au-delà de Lambert ; les très gros maillages (> 1 M de triangles) sont lents (pas de décimation).
- `modify_geometry` : pas de capuchon pour les sections creuses ; un 3MF d'origine donne un STL (métadonnées Bambu non reprises) ; `lay_flat` ne choisit pas l'orientation de lacet ; les supports/coutures peints ne sont pas transportés (dossier §6, dernier paragraphe).
- `create_parametric_model` n'a été exercé qu'avec un worker injecté (ni `uv` ni CadQuery vérifiés ici).
- La page de confirmation hors loopback : voir §2, dernière puce.
- Un répertoire vide `C:\ARSENE~1` a été créé par erreur à la racine du disque pendant le développement (chemin mal formé) ; il est inoffensif et à supprimer à la main.
