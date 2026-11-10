# Suivi de projet (G9) : projets de premier rang et verdict en un geste

Ce que livre la vague 1 du chantier G9 (spec : `docs/specs/G9-suivi-de-projet.md`) : **PJ2** (le projet est un objet de premier rang), **PJ11** (le verdict en un geste), **PJ5** (la fiche pièce) et **PJ10** (la fiche d'impression), décrites plus bas. Le journal photo à chaque 10 % (D8) est décrit à part (`docs/SUIVI-PROJET-D8.md`).

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

## PJ5 : la fiche pièce

Une **pièce** est un modèle du catalogue. `get_part_sheet(model_id, include?, limit?)` (lecture seule, carte `ui://bambu/part.html`) rassemble tout ce que le journal sait d'elle :

| Bloc | Contenu |
|---|---|
| `counts` | compteurs D7 et leur phrase : « imprimée 5 fois (100 % atteint), 4 jugées réussies, 1 jugée ratée (raison la plus fréquente : stringing, n = 1) » ; pièce jamais imprimée : zéros, `never_printed` et « jamais imprimée », sans erreur |
| `versions` | versions immuables dans l'ordre de la chaîne `parent_id` (parents avant enfants, `depth`, `is_latest`), chacune avec **son** dernier test de résistance (verdict, coefficient, `assumed`) ou « jamais testée » ; un test sans version identifiable va dans `strength_unassigned` (« version non déterminée ») |
| `profiles` | un **profil testé** = une signature de réglages effectifs relevés à la découpe (`insights.SettingsSignature` : machine, procédé, filaments, buse, surcharges triées, supports, plateau) ; par profil : impressions, imprimées, jugées réussies / ratées / à refaire / non jugées, temps, grammes et coût **médians** sur les impressions mesurées ; tri par nombre d'impressions puis signature (sortie déterministe) |
| `prints` | les plus récentes d'abord (`limit`, défaut 20, max 100 ; les compteurs portent sur toutes) : date, profil, issue, verdict et raison, durée / grammes / coût, niveau (`measured` ou `declared`) et bandeau de paliers à 10 % (voir ci-dessous) |
| `totals` | coût réel cumulé, temps machine, grammes, coût par impression jugée réussie ; **les impressions `external` (déclarées hors Printalk) sont listées « déclarée » et exclues** du coût et du temps mesurés (comptées à part dans `declared_*`) |
| `last_success` | dernier profil réussi (impression Printalk jugée réussie) |
| `projects` | projets qui citent la pièce |

`include` (liste ou « versions,profiles,prints,strength,projects ») restreint les sections servies ; une section inconnue est refusée avec la liste. Complète `get_model_details`, qui garde ses sorties.

**Bandeau de paliers (D8).** Par impression, les dix paliers 10 … 100 % : `ok` (photo rangée, `url` = route existante `/api/camera/photos/{sha256}`), `unavailable` (tentative consignée sans photo : « photo non disponible » + raison) ou `absent` (aucun relevé : palier non atteint, sauté ou impression d'avant le journal photo). Sans aucun relevé, la liste est vide et `note` dit « photo non disponible : … ». La miniature est la photo 100 %, sinon la photo de fin, sinon le dernier palier avec photo. La carte charge une photo à la demande par l'outil réservé aux cartes `get_sheet_photo(sha256)` (il ne sert que des photos de la caméra) : l'iframe de l'hôte n'a pas d'accès réseau.

**Carte Fiche pièce.** Compteurs, onglets Versions / Profils / Impressions / Résistance, bouton « Voir en 3D » (le viewer 3D existant, via `get_model_details`), « Ouvrir la fiche d'impression », « Juger cette impression » (le verdict en un geste, `submit_print_verdict`). **Pas de bouton « Réimprimer »** (PJ6 n'existe pas). La carte Projet ouvre cette fiche quand on clique une pièce.

## PJ10 : la fiche d'impression

`get_print_sheet(print_job_id)` (lecture seule, carte `ui://bambu/print-sheet.html`) est une extension de `get_print_log` (inchangé) pour une impression **terminée** (l'identifiant de l'impression ou de sa fiche de journal convient). Une impression en cours ou inconnue est refusée avec un message clair.

| Bloc | Contenu | Niveau |
|---|---|---|
| `verdict` | jugement de l'humain, raison, date, source, note ; vocabulaire des raisons pour le bandeau | déclaré |
| `profile` | réglages **figés** : relevés à la découpe (le job d'impression garde ses paramètres de découpe) puis copiés dans la fiche de journal ; modifier ensuite un profil ne change pas la fiche | mesuré (déclaré pour une impression `external`) |
| `material` | bobines consommées (`spool_usage`) et grammes ; `estimated` signale une masse non pesée | mesuré, ou calculé quand estimé |
| `duration` | prévue, réelle, écart = (réel − prévu) / prévu en % ; sans durée réelle : « non disponible » et raison dans `unavailable` | mesuré / calculé |
| `cost` | coût prévu et réel ; absent pour une impression déclarée | calculé |
| `photos` | planche de paliers (comme ci-dessus) | mesuré |
| `camera` | `alerts_status` : `listed` (score, instant, niveau), `none` (« aucune alerte ») ou `unavailable` (« caméra non disponible » : on ne peut pas affirmer l'absence d'alerte). La caméra est réputée disponible quand elle a laissé une trace (photo, alerte) ou quand la surveillance est active pour cette imprimante | mesuré |
| `approval` | accord humain de lancement lu dans l'audit (demande, accord, canal) ; jamais de code ni de jeton ; sans objet pour une impression déclarée | mesuré |

Une valeur absente est « non disponible » avec sa raison (champ `unavailable`, jamais un 0 inventé) ; `levels` donne le niveau de chaque bloc. Les impressions `external` (PJ12, plus tard) sont marquées `declared` : pas de coût mesuré, pas de caméra, pas d'accord humain.

**Carte Fiche d'impression.** Bandeau de verdict (le formulaire de la carte Verdict : Réussi / Raté + raison / À refaire, modifiable), réglages figés, matière, durée, coût, planche de paliers, alertes, accord humain. **Pas de bouton « Comparer avec… »** (PJ9 n'existe pas), ni de lancement.

## Routes de lecture (pour les pages web)

`GET /api/models/{id}/sheet?include=…&limit=…` et `GET /api/prints/{id}/sheet` renvoient **exactement** les structures des outils (`internal/sheets/types.go`) ; types, client et jeux d'essai dans `web/src/api/sheets.ts` et `sheets.fixtures.ts`. Pièce ou impression inconnue : 404 ; section inconnue : 400. Aucune route ne lance, n'arrête ni ne reprend d'impression ; aucune URL signée ni secret dans la réponse (tests `secrets_test.go`, `TestSheetEndpoints`).

## Pour la suite (PJ7, PJ9...)

- `insights.Service.PrintCountsByModel(ctx, modelIDs)` rend, par pièce : tentatives, imprimées (et unités, pondérées par `part_count`), jugées réussies / ratées / à refaire / non jugées, déclarées hors Printalk, raison d'échec la plus fréquente avec son effectif, coût et temps réels cumulés, tentatives en cours avec leur coût estimé.
- `projects.DescribeCounts(counts)` en fait la phrase de la fiche pièce : « imprimée 5 fois (100 % atteint), 4 jugées réussies, 1 jugée ratée (raison la plus fréquente : stringing, n = 1) » ; « jamais imprimée » sans impression.
- `projects.Service.ProjectsOfModel(ctx, modelID)` liste les projets qui citent une pièce.
- `LogEntry` (journal) porte désormais, en lecture, `verdict`, `fail_reason`, `verdict_at`, `verdict_source`, `origin`, `part_count`, `reached_100` (champs facultatifs, relus depuis les colonnes de `print_logs`, jamais écrits dans le document de la fiche). Les anciennes fiches d'une impression `COMPLETED` sont lues « imprimées » par calcul, sans rétro-remplissage.
- `insights.SettingsSignature` / `SettingsLabel` (signature et libellé d'un profil) et `insights.Service.LogsForModel` (fiches d'une pièce avec leurs colonnes de suivi, en une requête) sont réutilisables par le comparateur de profils (PJ7) et la différence entre impressions (PJ9) ; les fiches sont assemblées par `internal/sheets` (`Service.PartSheet`, `Service.PrintSheet`).
- Les tables `project_variants` et `project_variant_parts` (PJ13) et `entity_notes` (PJ12) existent déjà, vides ; `get_project` liste les déclinaisons quand il y en aura.

## Limites connues de cette vague

- Le verdict ne se donne que sur une impression terminée au journal ; le journal se complète à la lecture (une impression terminée reçoit sa fiche quand un outil ou une page du journal la lit). `reached_100` est posé à la création de la fiche, d'après l'état final (`COMPLETED`) ou la progression relevée à 100 %.
- Le nombre d'exemplaires par plaque (`part_count`) vaut 1 par défaut : rien ne le renseigne encore automatiquement.
- Cliquer une pièce dans la carte Projet ouvre la fiche pièce (`get_part_sheet`, PJ5) ; la réimpression d'une pièce (PJ6) et la comparaison de deux impressions (PJ9) ne sont pas livrées : les cartes n'ont donc ni bouton « Réimprimer » ni bouton « Comparer avec… ».
- Fiches (PJ5, PJ10) : aucune migration (tout est relu depuis le journal, la caméra, `spool_usage`, l'audit et `strength_tests`). Le profil « figé » est celui du job d'impression relevé à la découpe, copié dans la fiche de journal quand celle-ci est créée (à la première lecture après la fin de l'impression) ; une fiche d'avant cette vague est lue telle quelle. Les impressions ne portent pas encore l'identifiant de la version de géométrie imprimée : l'onglet Impressions n'est donc pas rangé par version (PJ8). La disponibilité de la caméra est déduite (traces laissées ou surveillance active), pas interrogée en direct. Le journal photo n'a été vérifié qu'avec la caméra simulée. Le module d'accord humain n'a pas de lecteur partagé avec G5 (absent de ce dépôt) : la lecture de l'audit vit dans `internal/sheets`.
- Les déclinaisons (PJ13), le tableau de bord (PJ1), le fil (PJ3) et la recherche (PJ4) ne sont pas livrés.
