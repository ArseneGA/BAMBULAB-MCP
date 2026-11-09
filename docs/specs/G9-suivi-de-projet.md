# G9 — Suivi de projet (projets, pièces, impressions)

Spec pour un agent de code. Lire d'abord `docs/specs/README.md` § 2 (règles communes), `docs/BACKLOG.md` (règle d'architecture « aucune API d'IA dans Printalk » : le serveur ne fait que du **calcul déterministe**, Claude présente), `docs/UTILISATION.md` § 11 (coût, journal), `docs/SECURITY.md` (approbation humaine). Demande de l'utilisateur du 2026-11-09. Module Go `github.com/ArseneGA/BAMBULAB-MCP`, SQLite pur Go, `CGO_ENABLED=0`. Le dépôt a été lu le 2026-11-09 ; l'agent relit `git log` et les fichiers cités avant de coder.

Codes retenus : **PJ1 à PJ12** (préfixe « PJ » = suivi de Projet). Aucun conflit dans `docs/BACKLOG.md` : les codes de la demande (M1…M4, P1…P5, I1…I4) sont repris sous PJ pour ne pas entrer en collision avec les `P`/`N`/`S`/`B`/`W` existants.

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

## 1. Objectif et valeur

Passer de « une liste d'impressions et un catalogue de modèles » à un **suivi de projet** : savoir où en est chaque projet (« 4/6 pièces imprimées »), ce qu'il a coûté, quelle pièce a été imprimée combien de fois avec quel résultat, quel réglage marche vraiment, et ce qui attend un geste de l'humain. Le verdict en un geste en fin d'impression (PJ11) est la **clé de toutes les statistiques** : sans résultat saisi, aucun taux de réussite ni « ton meilleur profil » n'est honnête.

Trois niveaux : **macro** (atelier, projet), **méso** (pièce, profil), **micro** (impression). Tout est utilisable depuis Claude (outil MCP + texte), depuis des cartes MCP App (DA verte `web/shared`) et depuis l'interface web.

## 2. Périmètre / hors périmètre

Dans le périmètre : table de projets et appartenance des pièces ; statut et avancement ; fiche pièce et fiche impression agrégées depuis l'existant ; verdict, notes, étiquettes ; préparation d'une réimpression ; comparaisons et différences (calcul pur) ; tableau de bord, fil, recherche ; cartes ; outils MCP ; routes HTTP de lecture ; pages web.
Hors périmètre : toute décision automatique (le serveur n'invente aucun verdict ; « ton meilleur » est un classement de chiffres réels avec effectif) ; découpage d'un projet en pièces, nomenclature, notice (**N13**, qui se branche sur PJ2) ; galerie/portfolio (N22) ; commandes clients (G5) ; devis (G2) ; apprentissage du profil maître (N18, qui lira les verdicts de PJ11) ; **tout lancement, arrêt ou reprise d'impression**.

### Ce qui existe déjà en partie (ne pas dupliquer)

| Existant | Ce que c'est | Conséquence pour G9 |
|---|---|---|
| **G5** traçabilité par commande (`internal/orders`) | trace commande → ligne → impression (photo, réglages, bobine, accord humain), coût réel par commande | G9 ne refait pas la trace ; PJ10 réutilise les mêmes lectures (journal, `spool_usage`, audit `print.confirmation_approved`) : extraire une fonction commune plutôt que dupliquer. « Commande » = G5, « projet » = G9 |
| **N19** journal photo automatique (retenu) | photos à 0/25/50/100 % rangées avec l'impression | PJ10 affiche `camera_photos` (kinds `confirm`, `final`, `alert` ; les paliers N19 s'y ajoutent sans changement) ; G9 ne prend aucune photo |
| **N13** mode projet complet (retenu) | idée → pièces, nomenclature, planning, notice | N13 crée des projets et pièces via les outils de PJ2 ; G9 fournit le socle (table, statut, avancement), pas la génération |
| **N18** profil maître par matière (retenu) | apprentissage des réglages | lit le verdict PJ11 ; PJ7 est un classement simple, pas l'apprentissage |
| **N22**, **B11** galerie, publication | photos finales, coût, réglages | lisent la fiche pièce PJ5 |
| **S7** « Refais la même » | relance d'une réussite à l'identique | **PJ6 = S7 étendu** (profil au choix). Une seule fonction `prepare_reprint` ; S7 en est le cas `profile=last_success` ; si S7 est déjà fait, PJ6 l'appelle |
| `list_print_history` (`print_tools.go`), `list_print_log`, `get_print_log`, `update_print_log` (`insight_tools.go`), table `print_logs` (0160) | historique et fiche de journal (`Outcome` success/partial/failed/cancelled, `Defects`, `Rating`, `Comment`, `Photos`, `CameraPhotos`, `PlannedCost`/`ActualCost`, `ResultIDs`) | PJ10/PJ11 les étendent ; aucun outil renommé |
| `record_print_result`, `get_print_memory`, `compare_variants`, `apply_variant`, `experiments`, `applied_variants`, `print_results` (0110) | retours d'expérience par candidat | PJ7 réutilise `print_results` et `slicing_profiles` ; PJ11 n'écrit un `print_result` que si l'impression vient d'un candidat d'expérience (comportement existant, inchangé) |
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
**D5 — Calcul seulement.** « Meilleur profil », taux de réussite, différences, statut suggéré sont des formules déterministes avec effectif `n` affiché ; sous `n = 3` impressions par profil : « échantillon trop petit pour conclure », aucun classement présenté comme conclusion.
**D6 — Mesuré / calculé / déclaré** (règle commune 4) : temps réel et grammes consommés = mesuré (imprimante, `spool_usage`) ; coût = calculé ; verdict, raison, note = **déclaré**. Valeur inconnue = « non disponible » + raison.

## 5. Modèle de données (migration additive `<N>_project_tracking.sql`, N = dernier numéro + 10 au moment du codage ; en-tête « G9, additive »)

Nouvelles tables :
```
projects(
  id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'idea',   -- idea | design | printing | done | in_use | abandoned
  collection_id TEXT REFERENCES collections(id),   -- adoption facultative
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
Index : `project_parts(model_id)`, `entity_notes(kind, entity_id)`, `projects(status, updated_at)`.

Colonnes ajoutées à `print_logs` (toutes `NOT NULL DEFAULT`, sans effet sur l'existant) : `verdict` (`''` pas encore jugé | `success` | `failed` | `redo`), `fail_reason` (vocabulaire fermé : `stringing|warping|adhesion|spaghetti|layer_shift|under_extrusion|nozzle_clog|other`, détail libre dans la note), `verdict_at`, `verdict_source` (`human_card|human_web|claude_on_request`), `origin` (`printalk|external`, défaut `printalk`), `part_count` (défaut 1).
`Outcome` existant (état technique) **n'est pas écrasé** : `verdict` est le jugement de l'humain. Les statistiques utilisent `verdict` quand il existe, sinon `Outcome` avec la mention « non jugé ». Aucun rétro-remplissage.
**Aucun `DROP`, aucun renommage, aucune donnée migrée.** Un test de migration ouvre une base d'avant. Statuts : transitions libres ; `abandoned` et `in_use` posent `closed_at`. Un statut peut être **suggéré** (calcul), jamais appliqué sans appel explicite.

## 6. Exigences par élément

**Convention des cartes.** Chaque carte est une ressource `ui://bambu/<nom>.html` déclarée dans `registerCardViews` (constantes et liaison outil → carte dans un nouveau fichier `internal/mcpserver/project_views.go`), rendue par `web/mcp-app/cards/<nom>.tsx` (style `web/shared`, aucune couleur codée en dur), avec rendu texte de repli sans MCP Apps. Les écritures faites depuis une carte passent par des outils **réservés à l'application** (`submit_*`, visibilité `app`). Le serveur ne distingue pas un appel de l'application d'un appel du modèle : ces outils doivent donc rester **sans risque** (aucun lancement, aucune suppression de donnée de valeur).

### PJ2 — Projet de premier rang (M2), socle, vague 1
**But.** Regrouper des pièces ; statut ; avancement « 4/6 pièces imprimées » ; coût et temps totaux.
**Données.** Nouvelles : `projects`, `project_parts`. Lues : `models`, `print_logs` (par `model_id`), `spool_usage`, `insights` (coût).
**Calcul** (`internal/projects`, nouveau paquet). Une pièce est « imprimée » quand le nombre d'impressions réussies (`verdict=success`, sinon `Outcome=success` marqué « non jugée »), pondéré par `part_count`, atteint sa quantité. Avancement : `X/Y pièces` et `Z/W unités`. Coût total = somme des `ActualCost` réels, `PlannedCost` des tentatives en cours indiqué à part ; temps total = somme des durées réelles. Statut suggéré : `printing` dès qu'une impression est liée, `done` quand tout est satisfait.
**Outils MCP (nouveaux).** `create_project(name, description?, status?, collection_id?, due_date?)`, `update_project(id, …, status?, archived?)`, `add_project_part(project_id, model_id, quantity?, role?)`, `remove_project_part`, `get_project(id)` (projet, pièces, avancement, coûts, temps, prochaine action), `list_projects(status?, limit?)`. Pas de suppression définitive (archivage seulement).
**Carte.** `ui://bambu/project.html` : nom, statut en pastille avec texte, barre « 4/6 pièces », coût, temps, pièces avec compteur ; actions app-only `submit_project_status`, `submit_project_part` ; clic sur une pièce → `get_part_sheet`.
**CA.**
- CA2.1 Projet de 3 pièces (quantités 2, 1, 3), impressions simulées réussies (2, 1, 1) → `2/3 pièces`, `4/6 unités`, coût = somme des coûts réels, temps = somme des durées.
- CA2.2 Une impression `failed` ne compte pas ; une impression sans verdict compte « non jugée » et le texte le signale.
- CA2.3 Une même pièce dans deux projets compte dans les deux ; `get_project_costs` renvoie exactement la même sortie qu'avant (non-régression).
- CA2.4 Le statut suggéré `done` ne modifie pas `status` sans `update_project`.
- CA2.5 Une base créée avant la migration s'ouvre ; `projects` est vide ; tous les outils existants répondent comme avant.
- CA2.6 Aucun outil ni route ne supprime définitivement un projet ; `archived` masque de `list_projects` par défaut.

### PJ5 — Fiche pièce (P1), vague 1
**But.** Tout savoir d'une pièce (= un `model`) : aperçu 3D, versions de géométrie, profils (temps, g, €), impressions, compteurs, coût cumulé, lien test de résistance.
**Données.** Existantes en lecture : `models`, `model_versions`, `slicing_profiles` et `experiment_candidates.metrics`, `print_logs` + PJ11, `camera_photos`, `spool_usage`, `strength_tests`. Notes via PJ12.
**Outil.** `get_part_sheet(model_id, include?)` (lecture) : compteur « imprimée 5 fois, 4 réussies, 1 ratée (raison la plus fréquente : stringing, n = 1) » sur `verdict` (non jugées signalées), coût cumulé réel et par pièce réussie, temps cumulé, dernier profil réussi, dernier test de résistance **par version** avec verdict et `assumed`. Complète `get_model_details`, qui garde ses sorties.
**Carte.** `ui://bambu/part.html` : viewer 3D existant (`ModelViewURI`, `get_mesh_chunk`), onglets Versions / Profils / Impressions / Résistance ; par impression : date, profil, résultat, raison, miniature (blob par sha256 via l'API existante) ; boutons app-only vers PJ6 et PJ11.
**CA.**
- CA5.1 5 impressions simulées (4 `success`, 1 `failed` raison `stringing`) → « imprimée 5 fois, 4 réussies » et `{printed:5, success:4, failed:1}` ; coût cumulé = somme ; une pièce jamais imprimée renvoie des zéros et « jamais imprimée », sans erreur.
- CA5.2 Les versions suivent `parent_id` ; chaque test de résistance est rattaché à **sa** version.
- CA5.3 Les impressions `external` sont listées « déclarée » et ne comptent pas dans le coût « réel mesuré ».
- CA5.4 La carte s'affiche sans photo ni 3D (états vides explicites) ; aucun secret dans la sortie (test de motifs, § 7).

### PJ10 — Fiche d'impression (I1), vague 1
**But.** Une impression = un enregistrement lisible : profil **figé** (réglages effectifs relevés à la découpe), bobine(s) et grammes **réels**, durée prévue vs réelle, photos du journal, alertes caméra, note et verdict.
**Données.** Existantes : `print_logs.doc`, `spool_usage`, `camera_alerts`, audit. Ajoutées : verdict, raison, origine (§ 5).
**Outil.** `get_print_sheet(print_job_id)` : extension de `get_print_log` (inchangé) avec alertes caméra (score, instant), bobines (grammes, `estimated`), accord humain lu dans l'audit comme G5, mention « déclarée » si `origin=external` ; niveau de chaque champ affiché.
**Carte.** `ui://bambu/print-sheet.html` : en-tête, bandeau verdict (PJ11), blocs réglages / matière / temps / photos / alertes, bouton « Comparer avec… » (PJ9).
**CA.**
- CA10.1 Le profil affiché est celui relevé à la découpe : si le profil est modifié ensuite, la fiche ne change pas.
- CA10.2 Écart de durée = (réel − prévu) / prévu en % ; sans durée réelle : « non disponible ».
- CA10.3 Alertes caméra listées avec instant et niveau ; sans caméra : « caméra non disponible » (distinct de « aucune alerte »).
- CA10.4 Aucune URL signée longue durée ni secret dans la sortie.

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
**Calcul** (`internal/projects/profilecmp.go`, fonction pure). Par profil (clé = signature des réglages effectifs, `CandidateSignature` existant) : `n`, taux de réussite sur `verdict`, temps et grammes médians réels, coût médian, défauts fréquents. Classement par taux de réussite puis coût, **seulement** si `n ≥ 3` pour au moins deux profils ; sinon « échantillon trop petit pour conclure », aucun « meilleur » désigné. Les `print_results` d'expérience du même contexte (matière, machine) sont joints.
**Outil.** `compare_part_profiles(model_id, material_family?, printer_id?)` (lecture), distinct de `compare_variants` (inchangé).
**Carte.** `ui://bambu/profiles.html` (composants de `compare.tsx`) : tableau avec effectifs, badge « ton meilleur (n = 5) » ; bouton « Réimprimer avec ce profil » → PJ6.
**CA.**
- CA7.1 Profil A : 5 impressions, 5 réussies ; profil B : 4 impressions, 2 réussies → A meilleur, `n` affichés.
- CA7.2 A avec 2 impressions seulement → aucun « meilleur », message d'échantillon trop petit.
- CA7.3 Les non jugées sont exclues du taux et comptées à part.
- CA7.4 Sortie déterministe (égalités départagées par identifiant).

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

## 7. Sécurité et règles non négociables

- **Lancement humain.** Aucun nouvel outil, carte ou route ne lance, n'arrête ni ne reprend une impression (PJ6). Tous les nouveaux outils sont ajoutés à `TestNoMCPToolCanApproveAConfirmation` et `noapprove_test.go` ; aucun nom ne contient `approve|start|launch|stop|resume|cancel` ; un test recherche tout appel à `Approve`/`Start`/`Stop`/`Resume`/`Cancel` dans `internal/projects` et `project_*`.
- **Secrets jamais dans les cartes ni les sorties** : pas de code d'accès d'imprimante, jeton, code de lancement, clé de signature, chemin serveur, URL signée longue durée. Les images passent par les blobs sha256 et les routes existantes (aucune nouvelle URL signée). Test de motifs (`bblp`, `C:\`, `/home/`, `/opt/`, `Bearer`, `access_code`) sur les sorties de chaque outil G9, comme `secrets_test.go`.
- **Migrations additives** uniquement (§ 5) ; test d'ouverture d'une base d'avant.
- **Originaux immuables** : G9 ne modifie aucune version, aucun fichier source, aucun profil ; `prepare_reprint` ne fait que créer une découpe et une demande de confirmation.
- **Aucune IA dans Printalk** : « meilleur », taux, différences, statut suggéré = formules déterministes testées ; aucun appel réseau à un modèle ; aucune cause (« parce que… ») produite côté serveur.
- **Honnêteté statistique** : `n` toujours affiché ; seuil `n ≥ 3` ; non jugées comptées à part ; `external` signalées « déclarées ».
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
| **3** | **PJ1** (tableau de bord) + **PJ3** (fil) | agrégations utiles seulement avec des données jugées |
| **4** | **PJ8**, **PJ9**, **PJ12** (I3 + I4), **PJ4** | confort et finitions ; PJ4 en dernier (il consomme tout) |

Chaque vague est livrable seule, une fusion par vague. **N13** peut démarrer après la vague 1.

## 10. Fichiers probablement touchés

Nouveaux : `internal/store/migrations/<N>_project_tracking.sql`, `internal/projects/*.go` (service, progression, comparaison de profils, différences, fil, recherche) et tests, `internal/mcpserver/project_tools.go`, `project_views.go` (+ tests), `internal/httpapi/projects.go` (routes de lecture), `web/mcp-app/cards/{project,part,print-sheet,verdict,reprint,profiles,workshop,timeline,part-tree,print-diff}.tsx` (+ tests vitest, fixtures dans `fixtures.ts`), `web/src/pages/Projects.tsx`, `web/src/api/projectsTypes.ts`, `docs/SUIVI-PROJET.md`.
Modifiés (additif, une ligne chacun) : `internal/mcpserver/server.go` (`registerProjectTools`), `card_views.go`, `internal/core/core.go`, `internal/insights/journal.go` (champs `LogEntry` facultatifs), `web/src/main.tsx` et `Shell.tsx` (menu « Projets »), `form_tools.go` (type `external_print`).
Dépendances Go : aucune nouvelle (objectif). Coordination : G5 (lecture du journal et de l'audit), S7, N13, N18.

## 11. Risques, questions ouvertes, taille

Taille : **L** (vague 1 ≈ M, 2 ≈ S, 3 ≈ M, 4 ≈ M).
Risques : confusion entre « projet » (G9), « commande » (G5) et « projet ouvert dans Studio » (`get_project_state`) : textes et doc les distinguent ; faible taux de verdicts saisis (atténué par `get_pending_verdicts`, le bandeau de fin d'impression, le geste unique) ; avancement faussé par les non jugées (mention explicite) ; définition d'un « profil » (signature des réglages effectifs) à valider sur les profils BBL à `include`.
Questions ouvertes pour l'utilisateur :
1. Une pièce compte-t-elle « imprimée » dès `Outcome=success` sans verdict (recommandé : oui, marquée « non jugée ») ou seulement après verdict ?
2. Sous-projets (assemblages) en v1 ? (non retenu : liste plate de pièces.)
3. Seuil `n ≥ 3` pour « ton meilleur » : à confirmer.

## 12. Consigne finale pour l'agent de code

Travaille dans ton worktree. Avant de rendre la main, fais passer : `go vet ./...` ; `go test -short ./... -count=1` ; `cd web && npm ci && npm run build` ; `cd web && npx vitest run` ; `GOOS=linux CGO_ENABLED=0 go build ./...`. L'imprimante est **toujours simulée** (`internal/printer/sim`, `cmd/printer-sim`). Livre par vague (§ 9). N'édite pas `docs/UTILISATION.md`, `docs/ETAT.md` ni `docs/BACKLOG.md` ; écris `docs/SUIVI-PROJET.md`. Commits en français, une ligne finale `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. **Ne pas pousser.**
