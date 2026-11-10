# Suivi de projet (G9) : projets de premier rang et verdict en un geste

Ce que livre la vague 1 du chantier G9 (spec : `docs/specs/G9-suivi-de-projet.md`) pour le socle de données : **PJ2** (le projet est un objet de premier rang) et **PJ11** (le verdict en un geste). La fiche pièce (PJ5), la fiche d'impression (PJ10) et le journal photo à chaque 10 % (D8) viennent à part.

Rien n'est à faire à la mise à jour : la migration `0300_project_tracking.sql` est additive (aucun `DROP`, aucun renommage, aucune donnée migrée) ; sans projet, rien ne change à l'écran ni dans les outils existants.

## Les mots

- **Projet** : un regroupement de pièces (modèles du catalogue) avec une quantité voulue. À ne pas confondre avec une **commande** client (G5), ni avec le « projet ouvert dans Studio » (`get_project_state`), ni avec une **collection** (un dossier de rangement ; `get_project_costs` agrège toujours « par collection », inchangé).
- **Sous-projet** (assemblage) : un projet dont `parent_id` est un autre projet. Quatre niveaux au plus ; un sous-projet n'a qu'un parent ; un cycle est refusé.
- **Imprimée** : l'impression a **atteint 100 %**, avec ou sans verdict. Une impression annulée ou arrêtée avant 100 % ne compte pas.
- **Verdict** : le **jugement de l'humain** sur une impression terminée : réussie (`success`), ratée (`failed`, avec une raison) ou à refaire (`redo`). Il qualifie la tentative et nourrit les taux de réussite ; il ne conditionne **pas** « imprimée ».

## Ce qui est calculé, mesuré, déclaré

| Valeur | Niveau |
|---|---|
| Durée réelle, grammes consommés | mesuré (imprimante, bobines) ; les grammes sont estimés quand ils n'ont pas été pesés |
| Coût | calculé (réglages de prix) |
| Verdict, raison, note | déclaré (par l'humain) |
| Avancement, statut suggéré, prochaine étape | calculé (formules déterministes) |

Aucune IA dans Printalk : tout est calcul sur le journal. Une valeur inconnue s'affiche « non disponible » avec sa raison (un projet dont aucune pièce n'a été imprimée n'a ni coût ni temps, pas un « 0 € »).

## Outils (Claude)

| Outil | Rôle |
|---|---|
| `create_project` | crée un projet (`name`, `description?`, `status?`, `collection_id?`, `parent_id?`, `due_date?`) |
| `update_project` | change nom, description, échéance, statut, collection, parent, archivage ; refus clair si cycle ou profondeur dépassée |
| `add_project_part` / `remove_project_part` | ajoute (ou met à jour) / retire une pièce ; quantité 1 à 1000 ; seul le lien est retiré |
| `get_project` | fiche : pièces et compteurs, sous-projets, avancement agrégé, coût et temps, déclinaisons, statut suggéré, prochaine étape |
| `list_projects` | arbre à plat (profondeur dans `depth`), `status` (liste plate), `parent_id` (descendre), `include_archived`, `limit` |
| `set_print_verdict` | verdict d'une impression terminée (`verdict`, `reason?`, `note?`), idempotent, audité |
| `get_pending_verdicts` | impressions terminées (réussies ou en échec) sans verdict, les plus récentes d'abord |

Outils réservés aux cartes (invisibles du modèle) : `submit_print_verdict`, `submit_project_status`, `submit_project_part`.

Raisons d'échec (vocabulaire fermé) : `stringing`, `warping`, `adhesion`, `spaghetti`, `layer_shift`, `under_extrusion`, `nozzle_clog`, `other`. Un échec sans raison est enregistré « other » avec un avertissement ; une raison hors liste est refusée avec la liste.

## Règles à retenir

- **Aucune suppression définitive** d'un projet : `archived=true` seulement. Un projet archivé est masqué de `list_projects` par défaut ; ses sous-projets restent consultables (ils remontent au premier niveau).
- **Le statut suggéré n'est jamais appliqué seul.** `get_project` propose `printing` (une impression est liée) ou `done` (toutes les pièces sont imprimées) ; seul `update_project` (ou le bouton de la carte) change le statut. `abandoned` et `in_use` posent `closed_at`.
- **Un verdict n'a aucun effet** sur l'imprimante, la file ni la confirmation de lancement. Aucun outil, carte ou route de ce chantier ne lance, n'arrête ni ne reprend une impression, ni ne crée ou n'approuve une confirmation (tests de garde-fous : `TestNoMCPToolCanApproveAConfirmation`, `TestG9ToolsConformAndSafe`, `TestPackageNeverStartsStopsResumesOrApproves`).
- **Une même pièce dans deux projets** compte dans les deux. Dans un projet avec sous-projets, chaque pièce est comptée une fois par chemin d'appartenance (une pièce citée dans A et dans son sous-projet B compte deux fois dans l'avancement de A) ; le coût et le temps, eux, comptent chaque pièce distincte une seule fois.
- **Collection adoptée** : si `collection_id` est posé, les modèles de la collection comptent comme pièces de quantité 1, sauf ceux que le projet cite déjà.
- **Notes** : la note d'un verdict est rangée dans `entity_notes` (jamais copiée dans l'audit ni dans les journaux). N'y saisissez aucun secret (mot de passe, code d'accès, jeton).
- Les impressions déclarées hors Printalk (`origin=external`, PJ12, plus tard) seront comptées mais signalées « déclarées » et exclues du coût mesuré.

## Où le trouver

- **Dans Claude** : « où en est le projet Étagère ? » (`get_project`, carte `ui://bambu/project.html`) ; « qu'est-ce que je dois juger ? » (`get_pending_verdicts`, carte `ui://bambu/verdict.html` avec les trois gros boutons). En fin d'impression, la carte de suivi (`print-live`) affiche un bandeau « Comment s'est passée l'impression ? » avec les mêmes gestes.
- **Dans l'interface web** : entrée **Projets** du menu Atelier : arbre des projets, fiche, et liste « Impressions à juger » (trois boutons ; « Raté » et « À refaire » demandent la raison). Routes : `GET /api/projects`, `GET /api/projects/{id}`, `GET /api/verdicts/pending`, `POST /api/prints/{id}/verdict`.

## Pour la suite (PJ5, PJ7, PJ10...)

- `insights.Service.PrintCountsByModel(ctx, modelIDs)` rend, par pièce : tentatives, imprimées (et unités, pondérées par `part_count`), jugées réussies / ratées / à refaire / non jugées, déclarées hors Printalk, raison d'échec la plus fréquente avec son effectif, coût et temps réels cumulés, tentatives en cours avec leur coût estimé.
- `projects.DescribeCounts(counts)` en fait la phrase de la fiche pièce : « imprimée 5 fois (100 % atteint), 4 jugées réussies, 1 jugée ratée (raison la plus fréquente : stringing, n = 1) » ; « jamais imprimée » sans impression.
- `projects.Service.ProjectsOfModel(ctx, modelID)` liste les projets qui citent une pièce.
- `LogEntry` (journal) porte désormais, en lecture, `verdict`, `fail_reason`, `verdict_at`, `verdict_source`, `origin`, `part_count`, `reached_100` (champs facultatifs, relus depuis les colonnes de `print_logs`, jamais écrits dans le document de la fiche). Les anciennes fiches d'une impression `COMPLETED` sont lues « imprimées » par calcul, sans rétro-remplissage.
- Les tables `project_variants` et `project_variant_parts` (PJ13) et `entity_notes` (PJ12) existent déjà, vides ; `get_project` liste les déclinaisons quand il y en aura.

## Limites connues de cette vague

- Le verdict ne se donne que sur une impression terminée au journal ; le journal se complète à la lecture (une impression terminée reçoit sa fiche quand un outil ou une page du journal la lit). `reached_100` est posé à la création de la fiche, d'après l'état final (`COMPLETED`) ou la progression relevée à 100 %.
- Le nombre d'exemplaires par plaque (`part_count`) vaut 1 par défaut : rien ne le renseigne encore automatiquement.
- Les cartes Projet et Verdict ne rendent pas la fiche pièce (PJ5) : cliquer une pièce ouvre `get_model_details`.
- Les déclinaisons (PJ13), le tableau de bord (PJ1), le fil (PJ3) et la recherche (PJ4) ne sont pas livrés.
