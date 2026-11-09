# G9 — Suivi de projet (projets, pièces, impressions)

Spec pour un agent de code. Lire d'abord `docs/specs/README.md` § 2 (règles communes), `docs/BACKLOG.md` (règle d'architecture « aucune API d'IA dans Printalk » : le serveur ne fait que du **calcul déterministe**, Claude présente), `docs/UTILISATION.md` § 11 (coût, journal), `docs/SECURITY.md` (approbation humaine). Demande de l'utilisateur du 2026-11-09. Module Go `github.com/ArseneGA/BAMBULAB-MCP`, SQLite pur Go, `CGO_ENABLED=0`. Le dépôt a été lu le 2026-11-09 ; l'agent relit `git log` et les fichiers cités avant de coder.

Codes retenus : **PJ1 à PJ13** (préfixe « PJ » = suivi de Projet ; PJ13 ajouté après les réponses de l'utilisateur du 2026-11-09, voir § 11). Aucun conflit dans `docs/BACKLOG.md` : les codes de la demande (M1…M4, P1…P5, I1…I4) sont repris sous PJ pour ne pas entrer en collision avec les `P`/`N`/`S`/`B`/`W` existants.

| Code | Demande | Nom | Vague |
|---|---|---|---|
| PJ1 | M1 | Tableau de bord atelier | 3 |
| PJ2 | M2 | Projet = objet de premier rang (socle) | 1 |
| PJ3 | M3 | Fil chronologique filtrable | 3 |
| PJ4 | M4 | Recherche filtrable | 4 |
| PJ5 | P1 | Fiche pièce | 1 |
| PJ6 | P2 | Rechoisir un profil et relancer (préparation seulement) | 2 |
| PJ7 | P3 | Comparateur de profils d'une pièce | 2 |
| PJ8 | P4 | Arbre version → profils → impressions | 4 |
| PJ9 | P5 | Différence entre deux impressions | 4 |
| PJ10 | I1 | Fiche d'impression | 1 |
| PJ11 | I2 | Verdict en un geste | 1 |
| PJ12 | I3 + I4 | Notes/étiquettes libres ; rattacher une impression faite hors Printalk | 4 |
| PJ13 | (réponse 2) | Déclinaisons d'un assemblage par objectif (rapide / solide / équilibrée…) | 3 |

## 1. Objectif et valeur

Passer de « une liste d'impressions et un catalogue de modèles » à un **suivi de projet** : savoir où en est chaque projet (« 4/6 pièces imprimées »), ce qu'il a coûté, quelle pièce a été imprimée combien de fois avec quel résultat, quel réglage marche vraiment, et ce qui attend un geste de l'humain. Le verdict en un geste en fin d'impression (PJ11) est la **clé de toutes les statistiques** : sans résultat saisi, aucun taux de réussite ni « ton meilleur profil » n'est honnête.

Trois niveaux : **macro** (atelier, projet), **méso** (pièce, profil), **micro** (impression). Tout est utilisable depuis Claude (outil MCP + texte), depuis des cartes MCP App (DA verte `web/shared`) et depuis l'interface web.

## 2. Périmètre / hors périmètre

Dans le périmètre : table de projets, sous-projets et appartenance des pièces ; déclinaisons d'un assemblage par objectif (PJ13) ; journal photo à chaque 10 % (D8) ; statut et avancement ; fiche pièce et fiche impression agrégées depuis l'existant ; verdict, notes, étiquettes ; préparation d'une réimpression ; comparaisons et différences (calcul pur) ; tableau de bord, fil, recherche ; cartes ; outils MCP ; routes HTTP de lecture ; pages web.
Hors périmètre : toute décision automatique (le serveur n'invente aucun verdict ; « ton meilleur » est un classement de chiffres réels avec effectif) ; découpage d'un projet en pièces, nomenclature, notice (**N13**, qui se branche sur PJ2) ; galerie/portfolio (N22) ; commandes clients (G5) ; devis (G2) ; apprentissage du profil maître (N18, qui lira les verdicts de PJ11) ; **tout lancement, arrêt ou reprise d'impression**.

### Ce qui existe déjà en partie (ne pas dupliquer)

| Existant | Ce que c'est | Conséquence pour G9 |
|---|---|---|
| **G5** traçabilité par commande (`internal/orders`) | trace commande → ligne → impression (photo, réglages, bobine, accord humain), coût réel par commande | G9 ne refait pas la trace ; PJ10 réutilise les mêmes lectures (journal, `spool_usage`, audit `print.confirmation_approved`) : extraire une fonction commune plutôt que dupliquer. « Commande » = G5, « projet » = G9 |
| **N19** journal photo automatique (retenu) | photos à 0/25/50/100 % rangées avec l'impression | **G9 étend N19** : décision de l'utilisateur, une photo à **chaque 10 %** (10, 20, … 100 %) au lieu de 0/25/50/100 (voir PJ10 et § 4 D8). PJ10 et PJ5 affichent `camera_photos` (kinds `confirm`, `final`, `alert`, plus `progress` pour les paliers). **N19 reste propriétaire de la capture** ; si N19 n'est pas livré, G9 livre la capture par paliers (même code, une seule implémentation) |
| **N13** mode projet complet (retenu) | idée → pièces, nomenclature, planning, notice | N13 crée des projets et pièces via les outils de PJ2 ; G9 fournit le socle (table, statut, avancement), pas la génération |
| **N18** profil maître par matière (retenu) | apprentissage des réglages | lit le verdict PJ11 ; PJ7 est un classement simple, pas l'apprentissage |
| **N22**, **B11** galerie, publication | photos finales, coût, réglages | lisent la fiche pièce PJ5 |
| **S7** « Refais la même » | relance d'une réussite à l'identique | **PJ6 = S7 étendu** (profil au choix). Une seule fonction `prepare_reprint` ; S7 en est le cas `profile=last_success` ; si S7 est déjà fait, PJ6 l'appelle |
| `list_print_history` (`print_tools.go`), `list_print_log`, `get_print_log`, `update_print_log` (`insight_tools.go`), table `print_logs` (0160) | historique et fiche de journal (`Outcome` success/partial/failed/cancelled, `Defects`, `Rating`, `Comment`, `Photos`, `CameraPhotos`, `PlannedCost`/`ActualCost`, `ResultIDs`) | PJ10/PJ11 les étendent ; aucun outil renommé |
| `record_print_result`, `get_print_memory`, `compare_variants`, `apply_variant`, `experiments`, `applied_variants`, `print_results` (0110) | retours d'expérience par candidat | PJ7 réutilise `print_results` et `slicing_profiles` ; PJ11 n'écrit un `print_result` que si l'impression vient d'un candidat d'expérience (comportement existant, inchangé) ; **PJ13 réutilise `experiments` / `experiment_candidates` / `applied_variants` pour les déclinaisons, sans les dupliquer** |
| `set_manufacturing_goals`, `list_goal_criteria`, `manufacturing_goals`, `goal_sets` (`id, model_id, original_text, doc, created_at`) | objectifs de fabrication structurés (critères : solide, rapide, surface, économe…) | **PJ13 : l'« objectif » d'une déclinaison est un `goal_sets` existant (ou un jeu de critères du catalogue `list_goal_criteria`) ; aucune seconde notion d'objectif** |
| `link_model_versions`, `model_versions` (immuables, `parent_id`) | versions de géométrie | lues par PJ5/PJ8 |
| `get_project_costs`, `insights.Dashboard.ByProject` | coût par **collection** | PJ2 le complète par projet ; la sortie existante reste identique |
| `get_project_state` | état du projet ouvert **dans Studio** (agent) | sans rapport : ne pas confondre avec PJ2 |
| `reprint_uploaded`, `reconcile_print`, `queue_print`, `request_print_confirmation` | relance d'un fichier déjà déposé ; file ; confirmation | PJ6 aboutit à la même confirmation humaine |
| `get_strength_report`, `plan_strength_test`, `strength_tests` (0280) | tests de résistance par version | PJ5 affiche le dernier test par version avec lien |
| `show_atelier` (`ui://bambu/atelier.html`) et cartes `library`, `compare`, `costs`, `spools`, `prints`, `printers`, `strength`, `form`, `print-live` | cartes existantes | PJ1 réutilise `show_atelier` comme point d'entrée ; PJ3 complète puis remplace progressivement la carte `prints` |

## 3. État actuel dans le code (lu le 2026-11-09)

- **Pas de projet de premier rang.** `collections` (0001 : `id, name, parent_id, created_at`) est un dossier hiérarchique ; `models.collection_id` est unique par modèle ; `get_project_costs` et `ByProject` agrègent « par collection ». Aucun statut, aucune quantité.
- Modèles et versions : `models` (+ `tags` JSON, `dimensions`, `deleted_at`), `model_versions` immuables (triggers), `source_files`.
- Profils et expériences : `slicing_profiles`, `experiments`, `experiment_candidates`, `applied_variants`, `print_results` (0110).
- Impressions : `print_jobs`, `print_confirmations` (tables à document JSON), `print_logs` (0160, `doc` JSON `LogEntry`, index `finished_at` et `material_family, printer_id`), `spool_usage` (0180), `camera_photos`, `camera_alerts` (0170), `print_queue` (0180).
- Résistance : `strength_tests` (0280).
- Cartes MCP App : `internal/mcpserver/card_views.go` (constantes `*ViewURI`, `registerCardViews`), `web/mcp-app/cards/*.tsx`, `testkit.tsx`, `fixtures.ts`, `call.ts` ; formulaires `form_tools.go` (outils `submit_*` réservés à l'application) ; confirmation `launch_tools.go` (`approve_print_with_code`) ; palette `web/shared/tokens.css`.
- Garde-fous : `TestNoMCPToolCanApproveAConfirmation`, `noapprove_test.go`, `spec_conformance_test.go`, `secrets_test.go`.
- Dernière migration lue : `0280_strength_tests.sql`.

## 4. Décisions de conception

**D1 — Projet : nouvelle table, pas d'extension de `collections`.** (a) `collections` est un dossier de rangement hiérarchique où un modèle n'est que dans un seul dossier ; un projet doit pouvoir réutiliser une même pièce (une équerre dans deux projets) avec une **quantité voulue** ; (b) mélanger rangement et suivi dans une table utilisée par tout le catalogue et par `internal/export` est le chemin le moins sûr ; (c) la migration reste **additive**. Pont : `projects.collection_id` facultatif (un projet peut « adopter » une collection : ses modèles comptent comme pièces de quantité 1 si aucune ligne `project_parts` ne les cite). `get_project_costs` par collection reste inchangé.
**D2 — Verdict = colonnes additives sur `print_logs`**, pas une table : une impression a un seul verdict courant ; l'historique des changements est dans `audit_log` (existant).
**D3 — Notes et étiquettes : table générique** `entity_notes` (projet ou impression) ; les étiquettes de **modèle** restent dans `models.tags` (outil `tag_models` existant) pour ne pas créer deux systèmes.
**D4 — Impression hors Printalk (PJ12/I4)** : ligne `print_logs` d'origine `external`, niveau « déclaré », sans `print_jobs` ni accord humain ; exclue par défaut des statistiques de fiabilité de machine (option `include_external`).
**D5 — Calcul seulement.** « Meilleur profil », taux de réussite, différences, statut suggéré sont des formules déterministes avec effectif `n` affiché ; sous le seuil `n` (**3 par défaut, réglable** : décision de l'utilisateur, valeur par défaut gardée faute de préférence) impressions par profil : « échantillon trop petit pour conclure », aucun classement présenté comme conclusion.
**D7 — « Imprimée » = l'impression a atteint 100 %, verdict ou pas** (décision de l'utilisateur). Une pièce compte comme imprimée dès qu'une impression liée a atteint 100 % (fin de l'impression, `Outcome` terminé : `success`/`partial`, ou progression 100 % relevée). Le **verdict** (`success|failed|redo`) reste un jugement à part : il ne conditionne **pas** le statut « imprimée », mais qualifie la tentative (réussie, ratée, à refaire) et alimente taux de réussite et « meilleur profil » (PJ7). Les impressions annulées ou arrêtées avant 100 % ne comptent pas. Une impression à 100 % jugée `failed` ou `redo` compte « imprimée » mais est signalée « imprimée, jugée ratée / à refaire » ; l'avancement affiche aussi le nombre d'unités **jugées réussies** à côté du nombre d'unités imprimées.
**D8 — Journal photo à chaque 10 %** (décision de l'utilisateur ; **étend N19**, qui prévoyait 0/25/50/100). Une photo automatique à 10, 20, … 100 % de progression, rangée avec l'impression (`camera_photos`, kind `progress`, champ `percent`), affichée dans la fiche d'impression (PJ10) et la fiche pièce (PJ5). Règles : (a) **échec de capture jamais bloquant** : caméra absente, hors ligne, image vide ou erreur → l'impression continue, le palier est consigné « photo non disponible (raison) » et la fiche l'affiche (distinct de « aucune alerte ») ; (b) **une seule tentative par palier**, au plus une photo par palier, sans rattrapage en rafale ; si un palier est sauté (relevé de progression qui saute de 38 à 52 %), on photographie le palier atteint le plus récent sans doublon ; (c) **aucune commande envoyée à l'imprimante** (lecture de la caméra seulement) ; (d) **stockage** : photos rangées en blobs sha256 (dédoublonnage existant), redimensionnées à une borne (défaut 1280 px de large, JPEG qualité 80, ≈ 80-150 Ko : ≈ 1-1,5 Mo par impression de 10 paliers) ; **rétention réglable** : défaut 90 jours pour les paliers intermédiaires, **les photos 100 % (finale) et celles des impressions jugées `success` d'une pièce de projet sont conservées** ; purge par tâche d'entretien existante, jamais de suppression d'une photo référencée par une galerie (N22) ou un projet ; plafond de volume total réglable (défaut 2 Go) avec purge des plus anciens paliers intermédiaires d'abord et message clair dans le tableau de bord (PJ1) quand le plafond approche.
**D9 — Sous-projets et déclinaisons selon l'objectif (v1)** (décision de l'utilisateur). Un projet contient plusieurs pièces **et peut contenir des sous-projets** (ex. un assemblage = un projet qui regroupe des pièces et d'autres assemblages). Hiérarchie par `projects.parent_id` (additif) avec garde contre les cycles (refus si le parent est un descendant) et profondeur bornée (défaut 4 niveaux). Un projet/assemblage peut avoir plusieurs **déclinaisons selon l'objectif** (impression rapide, solide, équilibrée entre les deux, tout rapide…) : voir PJ13. Les objectifs, jeux de critères, expériences et variantes appliquées **existants** sont réutilisés (`goal_sets`, `experiments`, `applied_variants`), pas recréés.
**D6 — Mesuré / calculé / déclaré** (règle commune 4) : temps réel et grammes consommés = mesuré (imprimante, `spool_usage`) ; coût = calculé ; verdict, raison, note = **déclaré**. Valeur inconnue = « non disponible » + raison.

## 5. Modèle de données (migration additive `<N>_project_tracking.sql`, N = dernier numéro + 10 au moment du codage ; en-tête « G9, additive »)

Nouvelles tables :
```
projects(
  id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'idea',   -- idea | design | printing | done | in_use | abandoned
  collection_id TEXT REFERENCES collections(id),   -- adoption facultative
  parent_id TEXT REFERENCES projects(id),          -- sous-projet / assemblage (D9) ; NULL = projet racine
  due_date TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  closed_at TEXT, archived INTEGER NOT NULL DEFAULT 0, rev INTEGER NOT NULL DEFAULT 1)
project_parts(
  project_id TEXT NOT NULL REFERENCES projects(id), model_id TEXT NOT NULL REFERENCES models(id),
  quantity INTEGER NOT NULL DEFAULT 1 CHECK(quantity BETWEEN 1 AND 1000),
  role TEXT NOT NULL DEFAULT '', position INTEGER NOT NULL DEFAULT 0, added_at TEXT NOT NULL,
  PRIMARY KEY(project_id, model_id))
entity_notes(
  id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK(kind IN ('project','print')),
  entity_id TEXT NOT NULL, text TEXT NOT NULL DEFAULT '', tags TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL)
```
```
project_variants(          -- PJ13 : déclinaison d'un projet/assemblage selon un objectif
  id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id),
  name TEXT NOT NULL,                    -- « Rapide », « Solide », « Équilibrée », « Tout rapide »…
  goal_set_id TEXT REFERENCES goal_sets(id),   -- objectif existant (facultatif) ; sinon `goal_kind`
  goal_kind TEXT NOT NULL DEFAULT 'custom',    -- fast | strong | balanced | all_fast | custom (étiquette d'affichage)
  chosen_at TEXT,                        -- posé quand l'humain « choisit » cette déclinaison (préparation, pas lancement)
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL, rev INTEGER NOT NULL DEFAULT 1)
project_variant_parts(     -- une ligne par pièce de l'ensemble (sous-projets inclus) : le profil adapté à l'objectif
  variant_id TEXT NOT NULL REFERENCES project_variants(id), model_id TEXT NOT NULL REFERENCES models(id),
  profile_id TEXT REFERENCES slicing_profiles(id),     -- profil retenu pour cette pièce
  experiment_candidate_id TEXT REFERENCES experiment_candidates(id),  -- origine (facultatif, réutilise les expériences)
  applied_variant_id TEXT REFERENCES applied_variants(id),            -- variante appliquée existante (facultatif)
  PRIMARY KEY(variant_id, model_id))
```
Colonnes additives sur `camera_photos` (D8) : `percent INTEGER` (10-100, NULL hors paliers), `status TEXT NOT NULL DEFAULT 'ok'` (`ok` | `unavailable`), `reason TEXT NOT NULL DEFAULT ''`.
Index : `project_parts(model_id)`, `entity_notes(kind, entity_id)`, `projects(status, updated_at)`, `projects(parent_id)`, `project_variants(project_id)`, `camera_photos(print_job_id, percent)` (si le nom de colonne diffère, adapter au schéma lu).
**Garde-fous de hiérarchie** (service `internal/projects`, testés) : un projet ne peut pas être son propre ancêtre (refus de tout rattachement qui crée un cycle) ; profondeur maximale réglable (défaut 4) ; un sous-projet appartient à un seul parent ; archiver un parent archive la vue, pas les sous-projets (ils restent consultables).
Les valeurs de `project_variants` (temps, grammes, coût, solidité totaux) **ne sont pas stockées** : elles sont recalculées à la demande depuis les profils/mesures (aucune valeur périmée).

Colonnes ajoutées à `print_logs` (toutes `NOT NULL DEFAULT`, sans effet sur l'existant) : `verdict` (`''` pas encore jugé | `success` | `failed` | `redo`), `fail_reason` (vocabulaire fermé : `stringing|warping|adhesion|spaghetti|layer_shift|under_extrusion|nozzle_clog|other`, détail libre dans la note), `verdict_at`, `verdict_source` (`human_card|human_web|claude_on_request`), `origin` (`printalk|external`, défaut `printalk`), `part_count` (défaut 1).
`Outcome` existant (état technique) **n'est pas écrasé** : `verdict` est le jugement de l'humain. **Le statut « imprimée » (D7) ne dépend que de l'atteinte de 100 %**, pas du verdict. Les statistiques de **réussite** (taux, meilleur profil) utilisent `verdict` quand il existe, sinon `Outcome` avec la mention « non jugé ». Ajouter `reached_100 INTEGER NOT NULL DEFAULT 0` sur `print_logs` (posé à la fin de l'impression à 100 %). Aucun rétro-remplissage (les anciennes impressions terminées `Outcome=success|partial` sont lues « imprimées » par calcul, sans écriture).
**Aucun `DROP`, aucun renommage, aucune donnée migrée.** Un test de migration ouvre une base d'avant. Statuts : transitions libres ; `abandoned` et `in_use` posent `closed_at`. Un statut peut être **suggéré** (calcul), jamais appliqué sans appel explicite.

## 6. Exigences par élément

**Convention des cartes.** Chaque carte est une ressource `ui://bambu/<nom>.html` déclarée dans `registerCardViews` (constantes et liaison outil → carte dans un nouveau fichier `internal/mcpserver/project_views.go`), rendue par `web/mcp-app/cards/<nom>.tsx` (style `web/shared`, aucune couleur codée en dur), avec rendu texte de repli sans MCP Apps. Les écritures faites depuis une carte passent par des outils **réservés à l'application** (`submit_*`, visibilité `app`). Le serveur ne distingue pas un appel de l'application d'un appel du modèle : ces outils doivent donc rester **sans risque** (aucun lancement, aucune suppression de donnée de valeur).

### PJ2 — Projet de premier rang (M2), socle, vague 1
**But.** Regrouper des pièces ; statut ; avancement « 4/6 pièces imprimées » ; coût et temps totaux.
**Données.** Nouvelles : `projects`, `project_parts`. Lues : `models`, `print_logs` (par `model_id`), `spool_usage`, `insights` (coût).
**Calcul** (`internal/projects`, nouveau paquet). Une pièce est « imprimée » quand le nombre d'impressions **ayant atteint 100 %** (D7, verdict ou pas), pondéré par `part_count`, atteint sa quantité. Le verdict est affiché à part : parmi ces impressions, `n` jugées réussies, `n` jugées ratées / à refaire, `n` non jugées. Avancement : `X/Y pièces` et `Z/W unités imprimées` (+ « dont V jugées réussies »). **Sous-projets (D9)** : l'avancement d'un projet agrège récursivement ses pièces **et** celles de ses sous-projets (chaque pièce comptée une fois par chemin d'appartenance, borne de profondeur respectée) ; l'avancement propre à chaque sous-projet reste affiché. Coût total = somme des `ActualCost` réels, `PlannedCost` des tentatives en cours indiqué à part ; temps total = somme des durées réelles. Statut suggéré : `printing` dès qu'une impression est liée, `done` quand tout est satisfait.
**Outils MCP (nouveaux).** `create_project(name, description?, status?, collection_id?, parent_id?, due_date?)`, `update_project(id, …, status?, archived?, parent_id?)` (refus si cycle ou profondeur dépassée, message clair), `add_project_part(project_id, model_id, quantity?, role?)`, `remove_project_part`, `get_project(id)` (projet, pièces, sous-projets, avancement agrégé, coûts, temps, déclinaisons PJ13, prochaine action), `list_projects(status?, parent_id?, limit?)` (arbre par défaut, `parent_id` pour descendre). Pas de suppression définitive (archivage seulement).
**Carte.** `ui://bambu/project.html` : nom, statut en pastille avec texte, barre « 4/6 pièces », coût, temps, pièces avec compteur, sous-projets repliables (avancement propre), liste des déclinaisons (PJ13) ; actions app-only `submit_project_status`, `submit_project_part` ; clic sur une pièce → `get_part_sheet`.
**CA.**
- CA2.1 Projet de 3 pièces (quantités 2, 1, 3), impressions simulées ayant atteint 100 % (2, 1, 1) → `2/3 pièces`, `4/6 unités imprimées`, coût = somme des coûts réels, temps = somme des durées.
- CA2.2 Une impression à 100 % jugée `failed` compte « imprimée » (D7) mais le texte la signale « jugée ratée » et ne l'ajoute pas aux « jugées réussies » ; une impression sans verdict compte imprimée et « non jugée » ; une impression annulée à 60 % ne compte pas.
- CA2.7 Sous-projets : un assemblage A contenant les pièces P1 (×2) et le sous-projet B (pièces P2 ×1, P3 ×1) → l'avancement de A agrège P1, P2, P3 (4 unités) et B affiche le sien (2 unités) ; la même pièce dans A et B est comptée dans chacun.
- CA2.8 Cycle refusé : rattacher A sous son descendant B échoue avec message clair, rien n'est modifié ; profondeur > borne (défaut 4) refusée ; un sous-projet n'a qu'un parent.
- CA2.3 Une même pièce dans deux projets compte dans les deux ; `get_project_costs` renvoie exactement la même sortie qu'avant (non-régression).
- CA2.4 Le statut suggéré `done` ne modifie pas `status` sans `update_project`.
- CA2.5 Une base créée avant la migration s'ouvre ; `projects` est vide ; tous les outils existants répondent comme avant.
- CA2.6 Aucun outil ni route ne supprime définitivement un projet ; `archived` masque de `list_projects` par défaut.

### PJ5 — Fiche pièce (P1), vague 1
**But.** Tout savoir d'une pièce (= un `model`) : aperçu 3D, versions de géométrie, profils (temps, g, €), impressions, compteurs, coût cumulé, lien test de résistance.
**Données.** Existantes en lecture : `models`, `model_versions`, `slicing_profiles` et `experiment_candidates.metrics`, `print_logs` + PJ11, `camera_photos`, `spool_usage`, `strength_tests`. Notes via PJ12.
**Outil.** `get_part_sheet(model_id, include?)` (lecture) : compteur « imprimée 5 fois (100 % atteint, D7), 4 jugées réussies, 1 jugée ratée (raison la plus fréquente : stringing, n = 1) » : « imprimée » sur l'atteinte de 100 %, réussite/échec sur `verdict` (non jugées signalées) ; photos du journal par palier de 10 % (D8) par impression, coût cumulé réel et par pièce réussie, temps cumulé, dernier profil réussi, dernier test de résistance **par version** avec verdict et `assumed`. Complète `get_model_details`, qui garde ses sorties.
**Carte.** `ui://bambu/part.html` : viewer 3D existant (`ModelViewURI`, `get_mesh_chunk`), onglets Versions / Profils / Impressions / Résistance ; par impression : date, profil, résultat, raison, miniature (blob par sha256 via l'API existante) et bandeau de miniatures par palier de 10 % (palier sans photo : « photo non disponible ») ; boutons app-only vers PJ6 et PJ11.
**CA.**
- CA5.1 5 impressions simulées ayant atteint 100 % (4 `success`, 1 `failed` raison `stringing`) → « imprimée 5 fois, 4 jugées réussies » et `{printed:5, success:4, failed:1}` (la ratée compte « imprimée », D7) ; coût cumulé = somme ; une pièce jamais imprimée renvoie des zéros et « jamais imprimée », sans erreur.
- CA5.2 Les versions suivent `parent_id` ; chaque test de résistance est rattaché à **sa** version.
- CA5.3 Les impressions `external` sont listées « déclarée » et ne comptent pas dans le coût « réel mesuré ».
- CA5.4 La carte s'affiche sans photo ni 3D (états vides explicites) ; aucun secret dans la sortie (test de motifs, § 7).

### PJ10 — Fiche d'impression (I1), vague 1
**But.** Une impression = un enregistrement lisible : profil **figé** (réglages effectifs relevés à la découpe), bobine(s) et grammes **réels**, durée prévue vs réelle, photos du journal, alertes caméra, note et verdict.
**Données.** Existantes : `print_logs.doc`, `spool_usage`, `camera_alerts`, audit. Ajoutées : verdict, raison, origine, `reached_100` (§ 5) ; photos par palier de 10 % (D8, colonnes additives de `camera_photos`).
**Journal photo à chaque 10 %** (étend N19). Pendant l'impression, quand la progression franchit 10, 20, … 100 %, le serveur prend **une** photo de la caméra (lecture seule) et la range avec l'impression ; **aucune commande n'est envoyée à l'imprimante**. Échec de capture (caméra absente, hors ligne, image vide, erreur) : **jamais bloquant**, palier consigné `unavailable` + raison, l'impression continue. Volume : ≈ 1-1,5 Mo par impression (blobs sha256, JPEG redimensionné) ; rétention et plafond réglables (D8) ; les photos 100 % et celles des réussites de projet sont conservées. La fiche affiche la planche de paliers (10 %…100 %) ; un palier manquant montre « photo non disponible » avec la raison.
**Outil.** `get_print_sheet(print_job_id)` : extension de `get_print_log` (inchangé) avec alertes caméra (score, instant), bobines (grammes, `estimated`), accord humain lu dans l'audit comme G5, mention « déclarée » si `origin=external` ; niveau de chaque champ affiché.
**Carte.** `ui://bambu/print-sheet.html` : en-tête, bandeau verdict (PJ11), blocs réglages / matière / temps / photos / alertes, bouton « Comparer avec… » (PJ9).
**CA.**
- CA10.1 Le profil affiché est celui relevé à la découpe : si le profil est modifié ensuite, la fiche ne change pas.
- CA10.2 Écart de durée = (réel − prévu) / prévu en % ; sans durée réelle : « non disponible ».
- CA10.3 Alertes caméra listées avec instant et niveau ; sans caméra : « caméra non disponible » (distinct de « aucune alerte »).
- CA10.4 Aucune URL signée longue durée ni secret dans la sortie.
- CA10.5 Impression simulée menée à 100 % avec caméra simulée → 10 photos (10, 20, … 100 %) rangées avec l'impression, visibles dans la fiche d'impression et la fiche pièce, chacune avec son palier.
- CA10.6 Caméra simulée en panne au palier 40 % → l'impression se termine normalement, le palier 40 % est `unavailable` avec raison et la fiche l'affiche « photo non disponible » ; les autres paliers sont présents ; aucune erreur remontée au lancement ni à la file.
- CA10.7 Un palier sauté (progression 38 → 52 %) ne produit ni doublon ni rafale : une photo par palier atteint au plus.
- CA10.8 Rétention : après dépassement de la durée réglée, les paliers intermédiaires sont purgés, la photo 100 % et celles des réussites de projet restent ; plafond de volume respecté et signalé ; une photo référencée par une galerie n'est jamais supprimée.

### PJ11 — Verdict en un geste (I2), vague 1
**But.** À la fin d'une impression, l'humain tape « réussi », « raté + raison » ou « à refaire ».
**Données.** `verdict`, `fail_reason`, `verdict_at`, `verdict_source` ; une entrée `audit_log` à chaque changement.
**Outils.** `set_print_verdict(print_job_id, verdict: success|failed|redo, reason?, note?)` (écriture, idempotent) ; app-only `submit_print_verdict` (même effet depuis la carte) ; `get_pending_verdicts(limit?)` (lecture : terminées sans verdict). Aucun verdict par défaut.
**Carte.** `ui://bambu/verdict.html` : aussi en bandeau de `print-live` en fin d'impression ; trois gros boutons (Réussi / Raté → raison / À refaire), note facultative, modifiable ensuite.
**Règle.** Un verdict n'a **aucun effet** sur l'imprimante, la file ou la confirmation.
**CA.**
- CA11.1 `set_print_verdict` sur une impression simulée `COMPLETED` écrit `verdict`, `verdict_at` et une entrée d'audit ; un second appel modifie le verdict et ajoute une seconde entrée ; `get_part_sheet` reflète le compteur.
- CA11.2 `failed` sans raison accepté (raison `other`) avec avertissement ; raison hors vocabulaire refusée avec la liste.
- CA11.3 Verdict sur une impression non terminée ou inconnue refusé avec un message clair.
- CA11.4 `get_pending_verdicts` ne renvoie que les terminées sans verdict, par date décroissante, bornées.
- CA11.5 Aucun outil G9 n'appelle `Approve`, `Start`, `Stop`, `Resume`, `Cancel` (§ 7).

### PJ6 — Rechoisir un profil et relancer (P2), vague 2
**But.** « Réimprime comme la dernière réussie » ou « avec le profil B » : le serveur **prépare** (profil, découpe, demande de confirmation) ; l'humain confirme par le geste existant.
**RÈGLE ABSOLUE.** **Aucune carte, aucun outil de G9 ne lance, n'arrête ni ne reprend une impression.** Le flux se termine toujours par une **demande de confirmation** (`request_print_confirmation`, page `/confirm` ou carte de lancement avec code personnel, `approve_print_with_code`, tous inchangés). Ni verdict, ni projet, ni priorité n'accélère ou ne contourne l'approbation.
**Outil.** `prepare_reprint(model_id | print_job_id, profile: last_success | profile_id | print_job_id_source, printer_id?, version_id?)` : découpe via le chemin existant (bornes de `execute_headless_slice`), puis demande de confirmation. Rend récapitulatif (profil, matières, temps, coût estimé), avertissements (bobine insuffisante via `check_filament_for_print`, version plus récente, imprimante occupée) et le lien ou la carte de confirmation. Bobine changée depuis la réussite : redécoupe avec la bobine actuelle, et le dit. Aucune réussite : erreur guidant vers PJ7 et PJ11.
**Carte.** `ui://bambu/reprint.html` : choix du profil (liste PJ7 avec effectifs), bouton **« Préparer »** (app-only `submit_reprint_choice` → `prepare_reprint`), résumé, puis le lien existant vers la confirmation. Aucun bouton « Lancer ».
**CA.**
- CA6.1 `prepare_reprint(profile=last_success)` sur une pièce simulée crée une confirmation en attente avec les **mêmes réglages effectifs** que la réussite ; l'imprimante simulée n'a reçu **aucune commande de démarrage** tant que l'approbation humaine n'a pas eu lieu.
- CA6.2 Version plus récente : avertissement explicite, choix laissé ; bobine insuffisante : avertissement visible dans le résumé.
- CA6.3 `TestNoMCPToolCanApproveAConfirmation` étendu à `prepare_reprint`, `submit_reprint_choice` et tous les outils G9 ; aucun nom G9 ne contient `approve|start|launch|stop|resume|cancel`.
- CA6.4 Pièce sans réussite : « aucune impression réussie à reproduire » + outil suivant conseillé.

### PJ7 — Comparateur de profils (P3), vague 2
**But.** Pour une pièce, comparer ses profils sur les résultats réels et dire « ton meilleur » avec son effectif.
**Calcul** (`internal/projects/profilecmp.go`, fonction pure). Par profil (clé = signature des réglages effectifs, `CandidateSignature` existant) : `n`, taux de réussite sur `verdict`, temps et grammes médians réels, coût médian, défauts fréquents. Classement par taux de réussite puis coût, **seulement** si `n ≥ seuil` (défaut 3, réglage `min_sample_size`, borné 2-20) pour au moins deux profils ; sinon « échantillon trop petit pour conclure », aucun « meilleur » désigné. Les `print_results` d'expérience du même contexte (matière, machine) sont joints.
**Outil.** `compare_part_profiles(model_id, material_family?, printer_id?)` (lecture), distinct de `compare_variants` (inchangé).
**Carte.** `ui://bambu/profiles.html` (composants de `compare.tsx`) : tableau avec effectifs, badge « ton meilleur (n = 5) » ; bouton « Réimprimer avec ce profil » → PJ6.
**CA.**
- CA7.1 Profil A : 5 impressions, 5 réussies ; profil B : 4 impressions, 2 réussies → A meilleur, `n` affichés.
- CA7.2 A avec 2 impressions seulement → aucun « meilleur », message d'échantillon trop petit.
- CA7.3 Les non jugées sont exclues du taux et comptées à part.
- CA7.4 Sortie déterministe (égalités départagées par identifiant).
- CA7.5 Le seuil est réglable : avec `min_sample_size=2`, A (2 impressions) redevient classable ; la valeur appliquée est répétée dans la réponse ; valeur hors 2-20 refusée.

### PJ1 — Tableau de bord atelier (M1), vague 3
**But.** Page d'accueil : projets par statut ; chiffres du mois (impressions, taux de réussite, heures machine, grammes, €) ; « ce qui attend un geste ».
**Données.** Existantes : `insights` (nouvelle fonction d'agrégation, l'existante inchangée), `print_logs`, `spools` (manque), `print_confirmations` en attente, `print_queue`, `camera_alerts`. Nouvelles : `projects`.
**« Attend un geste »** (liste calculée) : impressions sans verdict (PJ11), confirmations en attente (lien vers `/confirm`, **sans** bouton d'approbation), bobines sous le seuil, impressions en échec à examiner, projets sans activité depuis N jours (réglable, défaut 14).
**Outil.** `get_workshop_dashboard(month?: AAAA-MM)` (lecture) ; `show_atelier` ouvre la carte.
**Carte.** `ui://bambu/workshop.html`, liée à `get_workshop_dashboard` ; `show_atelier` y mène quand le client gère les cartes, sinon texte.
**CA.**
- CA1.1 Mois simulé de 10 impressions (7 jugées réussies, 1 ratée, 2 non jugées) → « 7/8 jugées, taux 87,5 %, 2 non jugées » (les non jugées n'entrent pas dans le taux et c'est dit).
- CA1.2 Heures machine = somme des durées réelles ; grammes = somme `spool_usage` ; € = somme des coûts réels ; niveaux affichés.
- CA1.3 Chaque catégorie de « attend un geste » est listée avec effectif et lien ; une confirmation en attente n'a qu'un lien vers la page humaine.
- CA1.4 Mois vide : états vides, pas d'erreur ni de division par zéro.

### PJ3 — Fil chronologique filtrable (M3), vague 3
**But.** Une ligne de temps : impressions, verdicts, photos, alertes, notes, changements de statut, bobines, versions.
**Données.** Lecture seule (`print_logs`, `camera_alerts`, `camera_photos`, `audit_log`, `entity_notes`, `model_versions`, `spool_usage`) ; aucune table dédiée.
**Outil.** `get_activity_timeline(project_id?, model_id?, spool_id?, since?, until?, kinds?, cursor?, limit?)` (lecture), ordre décroissant stable.
**Carte.** `ui://bambu/timeline.html` avec filtres projet / pièce / bobine / type.
**CA.**
- CA3.1 Chaque filtre restreint correctement (projet → ses pièces ; bobine → impressions qui l'ont consommée).
- CA3.2 Pagination sans doublon ni trou (250 évènements simulés, `limit` ≤ 200).
- CA3.3 Un évènement sans date est écarté, jamais daté artificiellement.
- CA3.4 Liste blanche de types d'évènements d'audit : aucun jeton ni code ne sort.

### PJ4 — Recherche filtrable (M4), vague 4
**But.** « Pièces PETG imprimées plus de 2 fois », « ratées ce mois », « projets en usage ».
**Outil.** `search_workshop(kind: part|print|project, filters…)` (un seul outil). Filtres fermés et typés : `material_family`, `printer_id`, `verdict`, `fail_reason`, `min_prints`, `max_prints`, `since`, `until`, `tag`, `status`, `project_id`, `has_photo`. **Pas de langage libre côté serveur** : Claude traduit la phrase en filtres (règle « aucune IA »). La réponse répète les filtres réellement appliqués.
**Carte.** Réutilise `library.html` / `timeline.html` avec filtres préremplis (pas de nouvelle ressource).
**CA.**
- CA4.1 « PETG et plus de 2 impressions » filtre exactement les données simulées.
- CA4.2 Filtre inconnu refusé avec la liste.
- CA4.3 `limit` ≤ 200, total exact.
- CA4.4 Requêtes paramétrées (test avec des valeurs contenant `'` et `;`).

### PJ8 — Arbre version → profils → impressions (P4), vague 4
**But.** L'histoire d'une pièce : chaque version, ses profils testés, leurs impressions et verdicts.
**Outil.** `get_part_tree(model_id)` (lecture), calculé depuis `model_versions.parent_id`. **Carte.** `ui://bambu/part-tree.html` (arbre repliable, pastille verdict avec texte, clic → `get_print_sheet`).
**CA.** CA8.1 Trois versions chaînées, deux profils, quatre impressions → arbre aux bons rattachements. CA8.2 Une impression de version inconnue est rangée sous « version non déterminée », jamais perdue. CA8.3 Borne de 500 nœuds avec indication de troncature.

### PJ9 — Différence entre deux impressions (P5), vague 4
**But.** « Qu'est-ce qui a changé entre la réussie et la ratée ? »
**Calcul** (`internal/projects/diff.go`, fonction pure) sur deux fiches PJ10 : profil effectif clé par clé, bobine (marque, couleur), version, imprimante, buse, durée réelle, grammes, alertes, verdict. Sortie : liste ordonnée `{champ, a, b, écart, niveau}` des **différences seulement** + « N champs identiques ». Aucune interprétation causale côté serveur.
**Outil.** `diff_prints(print_job_id_a, print_job_id_b)` (lecture). **Carte.** `ui://bambu/print-diff.html`.
**CA.** CA9.1 Deux fiches différant par une seule clé → exactement une ligne. CA9.2 Fiches identiques → « aucune différence ». CA9.3 Valeur manquante d'un côté → « non disponible », pas de différence inventée. CA9.4 Symétrie (a,b) ↔ (b,a) avec écarts inversés.

### PJ12 — Notes et étiquettes (I3) ; impression hors Printalk (I4), vague 4
**I3.** `add_note(kind: project|print, id, text?, tags?)`, `list_notes`, `delete_note` (l'audit garde la trace) ; étiquettes normalisées (minuscules, ≤ 32 caractères, ≤ 10 par entité) ; pour une **pièce**, les étiquettes sont `models.tags` (`tag_models` existant, exposé dans la fiche). Carte : champ note et étiquettes dans `part.html`, `print-sheet.html`, `project.html` (app-only `submit_note`).
**I4.** `attach_external_print(model_id, printed_at, duration_minutes?, grams?, spool_id?, printer_model?, verdict?, note?, deduct_spool?)` : crée une fiche `print_logs` `origin=external`, niveau **déclaré**, identifiant synthétique `ext_<id>` ; rien n'est envoyé à une imprimante, aucune confirmation créée ; avec `deduct_spool=true` (faux par défaut), une ligne `spool_usage` `estimated=1` annotée « déclaré ». Carte : formulaire existant `form.html` (nouveau type `external_print`, soumis par `submit_external_print`).
**CA.**
- CA12.1 Notes : création, modification, suppression ; bornes (texte ≤ 2000, 10 étiquettes).
- CA12.2 Étiquette hors format : règle de normalisation écrite et testée.
- CA12.3 Impression externe : visible dans fiche pièce et fil avec « déclarée » ; exclue des stats de fiabilité par défaut ; `deduct_spool` faux ne change pas le stock ; date future refusée.
- CA12.4 Aucune commande envoyée à l'imprimante simulée.

### PJ13 — Déclinaisons d'un assemblage par objectif (vague 3, après le socle)
**But.** Un projet ou assemblage (pièces **et** sous-projets, D9) peut avoir plusieurs **déclinaisons selon l'objectif** : « impression rapide », « solide », « équilibrée » (entre les deux), « tout rapide », etc. Chaque déclinaison associe **à chaque pièce de l'ensemble** (sous-projets inclus) le **profil adapté à l'objectif**, avec les **totaux de l'ensemble** : temps, grammes, coût, solidité. Les déclinaisons se comparent **côte à côte** ; l'humain peut **en choisir une** pour **préparer l'impression de tout l'assemblage**, qui s'arrête **toujours à la confirmation humaine** (règle absolue de PJ6).
**Réutilisation (ne rien recréer).** L'objectif d'une déclinaison = un `goal_sets` / critères du catalogue `list_goal_criteria` (`set_manufacturing_goals` existant). Le profil d'une pièce = un `slicing_profiles` obtenu par les chemins existants (`create_experiment` / `experiment_candidates`, `apply_variant` → `applied_variants`, `modify_slicing_profile`), colonne `experiment_candidate_id` / `applied_variant_id` de `project_variant_parts`. Temps, grammes, coût : `estimate_cost` et les métriques de candidats existants ; solidité : `assess_strength` / `get_strength_report` existants. Un profil jamais découpé : valeurs « non disponible » + raison, jamais inventées.
**Calcul** (`internal/projects/variants.go`, fonctions pures, aucun appel de modèle). Par déclinaison : somme des temps, grammes et coûts des profils retenus **× quantité de chaque pièce** (sous-projets agrégés récursivement, même bornes que PJ2) ; solidité de l'ensemble = **le maillon le plus faible** (minimum des indices de solidité des pièces, pièce limitante nommée) ; niveau mesuré / calculé / déclaré par champ (D6). Écarts entre déclinaisons : valeurs absolues et pourcentages vs la plus rapide / la plus solide ; **aucune « meilleure déclinaison » désignée** : l'humain arbitre (le serveur affiche les compromis, y compris « plus rapide mais pièce limitante X plus faible »). Pièce sans profil dans une déclinaison : signalée, totaux marqués « incomplets ».
**Outils MCP (nouveaux).** `create_project_variant(project_id, name, goal_kind?, goal_set_id?)`, `set_variant_part_profile(variant_id, model_id, profile_id | experiment_candidate_id | applied_variant_id)`, `update_project_variant`, `remove_project_variant` (archive logique, aucune donnée de valeur détruite), `compare_project_variants(project_id, variant_ids?)` (lecture, totaux et écarts côte à côte), `prepare_assembly_print(variant_id, printer_id?)` : découpe chaque pièce avec le profil de la déclinaison (bornes de `execute_headless_slice`, via `prepare_reprint` de PJ6, une pièce après l'autre), contrôle les bobines (`check_filament_for_print`) et crée **une demande de confirmation par plaque ou par lot** ; renvoie récapitulatif et liens. Pose `chosen_at` (préparation, pas lancement).
**Carte.** `ui://bambu/variants.html` : colonnes = déclinaisons (Rapide / Solide / Équilibrée…), lignes = pièces (profil retenu) puis ligne de totaux (temps, grammes, coût, solidité + pièce limitante), badges d'écart, états « incomplet » en texte ; bouton **« Préparer cette déclinaison »** (app-only `submit_variant_choice` → `prepare_assembly_print`) ; **aucun bouton « Lancer »** ; liens vers la confirmation existante.
**RÈGLE ABSOLUE** (identique à PJ6). Aucun outil ni aucune carte de PJ13 ne lance, n'arrête ni ne reprend une impression ; `prepare_assembly_print` n'aboutit qu'à des **demandes de confirmation** (`request_print_confirmation`, `approve_print_with_code`, tous inchangés). Choisir une déclinaison n'accélère ni ne contourne l'approbation.
**CA.**
- CA13.1 Assemblage A (pièces P1 ×2, P2 ×1) + sous-projet B (P3 ×3) avec trois déclinaisons « rapide », « solide », « équilibrée » : `compare_project_variants` renvoie, pour chacune, temps/grammes/coût totaux = somme des profils retenus × quantités (sous-projet inclus), et solidité = minimum des pièces avec la pièce limitante nommée ; valeurs identiques à un calcul indépendant du test.
- CA13.2 « Équilibrée » a des totaux compris entre « rapide » et « solide » sur le temps et la solidité lorsque ses profils sont intermédiaires (test sur données simulées) ; aucune « meilleure » déclinaison désignée.
- CA13.3 Une pièce sans profil dans une déclinaison : ligne « non disponible », totaux « incomplets » avec raison ; pas d'erreur.
- CA13.4 La déclinaison réutilise les objets existants : une déclinaison créée depuis un `goal_sets` et un `experiment_candidates` existants ne crée **aucune** nouvelle ligne dans `goal_sets`, `experiments` ni `applied_variants` ; le test le vérifie.
- CA13.5 `prepare_assembly_print` crée des confirmations en attente avec les réglages du profil de **chaque** pièce ; l'imprimante simulée n'a reçu **aucune commande de démarrage** avant approbation humaine ; bobine insuffisante signalée dans le récapitulatif.
- CA13.6 `TestNoMCPToolCanApproveAConfirmation` et `noapprove_test.go` étendus à tous les outils PJ13 ; aucun nom ne contient `approve|start|launch|stop|resume|cancel` (`prepare_assembly_print` autorisé : il prépare) ; aucun appel à `Approve`/`Start`/`Stop`/`Resume`/`Cancel` dans le code PJ13.
- CA13.7 Les totaux ne sont pas stockés : modifier le profil d'une pièce change immédiatement les totaux au calcul suivant.
- CA13.8 Cycle ou profondeur excessive dans les sous-projets d'une déclinaison : refus gardé par PJ2 ; sortie bornée (500 pièces) avec indication de troncature.

## 7. Sécurité et règles non négociables

- **Lancement humain.** Aucun nouvel outil, carte ou route ne lance, n'arrête ni ne reprend une impression (PJ6). Tous les nouveaux outils sont ajoutés à `TestNoMCPToolCanApproveAConfirmation` et `noapprove_test.go` ; aucun nom ne contient `approve|start|launch|stop|resume|cancel` ; un test recherche tout appel à `Approve`/`Start`/`Stop`/`Resume`/`Cancel` dans `internal/projects` et `project_*`.
- **Secrets jamais dans les cartes ni les sorties** : pas de code d'accès d'imprimante, jeton, code de lancement, clé de signature, chemin serveur, URL signée longue durée. Les images passent par les blobs sha256 et les routes existantes (aucune nouvelle URL signée). Test de motifs (`bblp`, `C:\`, `/home/`, `/opt/`, `Bearer`, `access_code`) sur les sorties de chaque outil G9, comme `secrets_test.go`.
- **Migrations additives** uniquement (§ 5) ; test d'ouverture d'une base d'avant.
- **Originaux immuables** : G9 ne modifie aucune version, aucun fichier source, aucun profil ; `prepare_reprint` ne fait que créer une découpe et une demande de confirmation.
- **Aucune IA dans Printalk** : « meilleur », taux, différences, statut suggéré = formules déterministes testées ; aucun appel réseau à un modèle ; aucune cause (« parce que… ») produite côté serveur.
- **Honnêteté statistique** : `n` toujours affiché ; seuil `n ≥ 3` par défaut, réglable ; non jugées comptées à part ; `external` signalées « déclarées ».
- **Entrées validées** : bornes (quantités 1-1000, textes ≤ 2000, `limit` ≤ 200), énumérations fermées, SQL paramétré, références vérifiées (pièce existante, impression terminée avant verdict).
- **Données personnelles** : aucune donnée client dans les projets ; notes libres jamais copiées dans les journaux applicatifs ; la doc avertit de ne pas y saisir de secret.
- **Cartes** : pas de `eval`, pas de ressource externe, CSP existante (`AddAppResource`), repli texte, états en texte et pas seulement en couleur, aucun bouton « Lancer » ou « Approuver ».

## 8. Compatibilité production

Serveur Linux, P1S en direct, Claude Desktop via `mcp-remote` : aucune action de l'utilisateur après mise à jour. Aucun outil renommé ni supprimé ; `get_print_log`, `get_model_details`, `get_project_costs`, `compare_variants`, `record_print_result`, `list_print_history`, `reprint_uploaded`, `link_model_versions`, `get_project_state` gardent leurs entrées/sorties ; champs ajoutés aux fiches **facultatifs** (`omitempty`). `tools/list` conforme (`spec_conformance_test.go`) : chaque outil a `Title`, `Annotations` correctes (lecture seule vs écriture) et un `outputSchema` d'objet. Sans projets, aucun changement visible (liste vide avec état vide explicite). Sans MCP Apps, tout reste disponible en texte.

## 9. Ordre de livraison conseillé

| Vague | Contenu | Pourquoi |
|---|---|---|
| **1** | **PJ2** (projet, socle) + **PJ5** (fiche pièce) + **PJ10** (fiche impression) + **PJ11** (verdict) | sans projet ni verdict, le reste n'a pas de sens ; le verdict produit des données dès le premier jour |
| **2** | **PJ6** (préparer une réimpression) + **PJ7** (comparateur de profils) | exigent des verdicts en base ; PJ6 s'aligne sur S7 |
| **3** | **PJ1** (tableau de bord) + **PJ3** (fil) + **PJ13** (déclinaisons d'un assemblage par objectif) | agrégations utiles seulement avec des données jugées ; PJ13 exige le socle PJ2 (sous-projets), PJ6 (préparation) et PJ7 (profils comparés) |
| **4** | **PJ8**, **PJ9**, **PJ12** (I3 + I4), **PJ4** | confort et finitions ; PJ4 en dernier (il consomme tout) |

Chaque vague est livrable seule, une fusion par vague. **N13** peut démarrer après la vague 1.

## 10. Fichiers probablement touchés

Nouveaux : `internal/store/migrations/<N>_project_tracking.sql`, `internal/projects/*.go` (service, progression, comparaison de profils, différences, fil, recherche) et tests, `internal/mcpserver/project_tools.go`, `project_views.go` (+ tests), `internal/httpapi/projects.go` (routes de lecture), `internal/projects/variants.go` (PJ13), `web/mcp-app/cards/{project,part,print-sheet,verdict,reprint,profiles,variants,workshop,timeline,part-tree,print-diff}.tsx` (+ tests vitest, fixtures dans `fixtures.ts`), `web/src/pages/Projects.tsx`, `web/src/api/projectsTypes.ts`, `docs/SUIVI-PROJET.md`.
Modifiés pour D8 (journal photo à chaque 10 %, étend N19) : le code de capture caméra existant (`camera_photos`, `internal/camera` ou équivalent, relire le dépôt) et la tâche d'entretien (rétention/plafond), en additif, sans changer les kinds existants.
Modifiés (additif, une ligne chacun) : `internal/mcpserver/server.go` (`registerProjectTools`), `card_views.go`, `internal/core/core.go`, `internal/insights/journal.go` (champs `LogEntry` facultatifs), `web/src/main.tsx` et `Shell.tsx` (menu « Projets »), `form_tools.go` (type `external_print`).
Dépendances Go : aucune nouvelle (objectif). Coordination : G5 (lecture du journal et de l'audit), S7, N13, N18.

## 11. Risques, questions ouvertes, taille

Taille : **L** (vague 1 ≈ M+ avec le journal photo par 10 %, 2 ≈ S, 3 ≈ M+ avec PJ13, 4 ≈ M).
Risques : **volume de stockage des photos** (10 par impression : rétention et plafond de D8, à surveiller dans PJ1) ; **PJ13** dépend de la qualité des profils/estimations par pièce (totaux « incomplets » assumés) ; confusion entre « projet » (G9), « commande » (G5) et « projet ouvert dans Studio » (`get_project_state`) : textes et doc les distinguent ; faible taux de verdicts saisis (atténué par `get_pending_verdicts`, le bandeau de fin d'impression, le geste unique) ; avancement faussé par les non jugées (mention explicite) ; définition d'un « profil » (signature des réglages effectifs) à valider sur les profils BBL à `include`.
Questions ouvertes : **aucune** (réponses de l'utilisateur du 2026-11-09 intégrées ci-dessous).
Décisions de l'utilisateur (2026-11-09) :
1. **« Imprimée » = l'impression a atteint 100 %, verdict ou pas** (D7) ; le verdict reste à part et ne conditionne pas le statut. En plus, **journal photo automatique à chaque 10 %** (D8), rangé avec l'impression, affiché dans PJ10 et PJ5 : **étend N19** (0/25/50/100) ; stockage (rétention, taille, plafond) prévu, échec de capture non bloquant.
2. **Sous-projets en v1** (D9) : hiérarchie `parent_id` (garde cycles, profondeur bornée) ; **déclinaisons selon l'objectif** d'un assemblage (rapide, solide, équilibrée, tout rapide…) = **PJ13**, réutilisant `goal_sets`, `experiments`, `applied_variants` ; choix d'une déclinaison → préparation de l'impression de l'assemblage, arrêt à la confirmation humaine.
3. **Seuil `n ≥ 3`** pour « ton meilleur » : pas de réponse, gardé comme **valeur par défaut réglable** (`min_sample_size`, 2-20).

## 12. Consigne finale pour l'agent de code

Travaille dans ton worktree. Avant de rendre la main, fais passer : `go vet ./...` ; `go test -short ./... -count=1` ; `cd web && npm ci && npm run build` ; `cd web && npx vitest run` ; `GOOS=linux CGO_ENABLED=0 go build ./...`. L'imprimante est **toujours simulée** (`internal/printer/sim`, `cmd/printer-sim`). Livre par vague (§ 9) ; PJ13 en vague 3, après PJ2/PJ6/PJ7. N'édite pas `docs/UTILISATION.md`, `docs/ETAT.md` ni `docs/BACKLOG.md` ; écris `docs/SUIVI-PROJET.md`. Commits en français, une ligne finale `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. **Ne pas pousser.**
