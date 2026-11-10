# État du projet — honnête

Date : 2026-11-06 (fin du jalon 9). Rédigé pour ne rien promettre de plus que ce qui a été exécuté.

**Légende des colonnes**

- **Implémenté** : le code existe et est branché.
- **Testé** : un test automatique le prouve (nom du test = preuve). `réel local` = exécuté contre un vrai Bambu Studio installé sur le poste de développement Windows, sans réseau ni imprimante.
- **Simulé** : ne s'exécute qu'contre `printer-sim` (127.0.0.1) ou un faux Studio ; **aucune imprimante réelle n'a jamais été contactée**.
- **Restant** : ce qui n'est pas fait ou pas vérifié.

> Un test passé contre le simulateur prouve que notre code fait ce que **nous avons compris** du protocole, pas que la P1S se comporte ainsi. C'est le rôle de `docs/BRANCHEMENT.md`.

## 0. Interface web : écrans branchés (passe « écrans sans endpoint », 2026-11-06)

Les huit besoins que `web/src/api/pending.ts` déclarait ont été traités ; l'interface n'affiche plus **aucune** donnée de démonstration (`data/demo.ts` supprimé ; la page « Système de design » porte un bandeau « valeurs fictives »).

| Besoin | Serveur (HTTP) | Test Go | Écran |
|---|---|---|---|
| `materials` | `GET /api/materials`, `GET /api/materials/{id}` (internal/engineering : valeur, unité, tolérance, source, date, niveau, domaine ; inconnu = `null`) ; `GET /api/profiles/filaments?machine=` (presets de Bambu Studio) | `TestMaterialsAPI`, `TestListExperimentsMemoryForAndFilamentPresets` | Matériaux > Matériaux |
| `experiments_list` | `GET /api/experiments?model_id=` | `TestListExperimentsMemoryForAndFilamentPresets`, `TestExperimentsListMemoryAndFilaments` | Comparaison sans expérience ouverte |
| `record_result_ui` | `GET /api/print-memory` (+ `POST /api/print-results` déjà exposé) | idem, `TestRecordPrintResultAndComparableMemory` | Comparaison : « Enregistrer l'essai réel », mémoire ; lien depuis l'impression terminée |
| `analyze` | `POST /api/models/{id}/analyze` (unité confirmée, machine) | `TestDownloadExportPartsAndAnalyze` | Fiche modèle : « Unité et fabricabilité » |
| `export_files` | `GET /api/files/{id}/download`, `POST /api/export` (zip en flux) | `TestDownloadExportPartsAndAnalyze`, `TestSafeNameNeverEscapesTheArchive` | Fiche modèle, bibliothèque (sélection) |
| `print_cancel_reconcile` | routes existantes : `POST /api/print-jobs/{id}/cancel` (renvoie `stop_url`, rien n'est arrêté), `POST .../reconcile` (lecture seule) | `TestPrintAndAgentRoutesUsedByTheWebApp` (+ tests d'impression existants) | Impression : « Demander l'arrêt », « Vérifier l'état réel » |
| `windows_agent_ui` | routes existantes : `GET /api/agents`, `/inventory`, `/inventory/select`, `/api/agent/tasks` (liste vide désormais `[]` et non `null`) | `TestPrintAndAgentRoutesUsedByTheWebApp` (+ tests agentlink existants) | écran `/agents` (pastille « Windows ») ; l'appairage reste la page serveur `/agents/pair` |
| `zone_overrides` | `GET /api/models/{id}/parts` (sous-parties stables), `POST /api/profiles/modify` (déjà exposé) | `TestDownloadExportPartsAndAnalyze` (stabilité), `TestModifyProfileValidationDiffAndRevisions` | Préparation : choix de l'objet, réglages manuels avec ancien / nouveau réglage et profil d'origine ; l'expérience part de la révision |

**Ce qui reste dans `pending.ts`** : `zone_overrides`, sous-point « variantes par zone » seulement. Un profil de découpe (machine, process, filament) est global ; Bambu Studio porte les réglages par objet dans le projet 3MF, que ni `modify_slicing_profile` ni la génération de variantes n'écrivent. La sélection d'un objet est donc consignée dans la note de la révision et les réglages valent pour la pièce entière (l'écran le dit).

**Parcours vérifié dans le navigateur** (`web/scripts/e2e.mjs`, Edge via Playwright, vrai `bambud` sur un dossier de données temporaire, vrai Bambu Studio, `printer-sim` en loopback) : import (inventaire puis import), étiquettes avec aperçu, corbeille et restauration, objectif, sous-parties et réglage manuel (un réglage invalide est refusé par le serveur), comparaison de 3 découpes réelles, application d'une variante, matériaux, agent Windows (état vide réel), unité confirmée et fabricabilité, téléchargement d'un original (sha256 vérifié) et export zip, liste des expériences, navigateur enregistré par `bambud enroll`, ajout du simulateur et saisie du code d'accès sur la page locale, découpe, validation, dépôt, page `/confirm` (aucun bouton de lancement avant l'accord), lancement, suivi jusqu'à la fin, retour d'expérience retrouvé dans la mémoire. **23 étapes, `E2E OK`.** Captures : `docs/J3/captures/` (matrice 800 / 1440 px × clair / sombre pour les écrans principaux, et `parcours-*.png`). **Limites** : simulateur seulement, aucune imprimante réelle ; l'arrêt via `/stop` n'est pas rejoué par l'e2e (couvert côté serveur) ; les captures de la vue MCP App n'ont pas été régénérées (aucune modification de cette vue) ; pas d'accès depuis un autre appareil.

## 0 bis. Carnet de bord des dysfonctionnements (FB0, 2026-11-10)

| | |
|---|---|
| Implémenté | Migration 0290 (`issues`), service `internal/issues`, outils `report_issue` / `list_issues` / `update_issue` / `export_issues`, carte lecture seule, API `/api/issues`, page Réglages > Dysfonctionnements, instruction serveur, filtre de secrets `contracts.RedactText` |
| Testé | `internal/issues` (stockage, dédoublonnage, régression, filtres, export), `TestRedactText`, `TestIssueToolsFlow` (MCP de bout en bout), `TestIssuesEndpoints` (HTTP), `TestGuardCoversCatalogRoutes`, tests Vitest de l'API web et de la carte |
| Restant | pas de zone « À faire » dans Accueil ; page non vérifiée visuellement dans un navigateur ; comportement réel de Claude (appel spontané de `report_issue`) non éprouvé |

## 1. Vérifications de ce jalon (résultats)

| Vérification | Résultat |
|---|---|
| `go vet ./...` | propre |
| `go test -short ./... -count=1` | **vert** (passe « écrans sans endpoint », rejoué le 2026-11-06), 20 paquets avec tests, 370 fonctions de test Go (dont 4 nouvelles dans `internal/httpapi`, 1 dans `internal/goals`, 1 dans `internal/catalog` pour cette passe ; J9 : 7 dans `internal/backup`, 2 dans `cmd/bambud`, 2 dans `cmd/bambu-mcp` ; plus des tests « réels » qui utilisent le Bambu Studio installé sur le poste) |
| Compilation croisée `go build ./...` | OK `linux/amd64`, `linux/arm64`, `windows/amd64` (SQLite pur Go, `CGO_ENABLED=0`) |
| `scripts/release.sh --skip-web --skip-tests` | OK : `bambud`, `bambu-mcp`, `printer-sim` linux amd64+arm64 ; `bambu-mcp.exe`, `bambu-agent.exe` windows/amd64 ; archive du worker CAO (sans `.venv`) ; archive de déploiement ; `SHA256SUMS` au format `sha256sum` |
| `scripts/release.ps1` | **exécuté** (`-SkipWeb -SkipTests`, PowerShell 5.1) : mêmes 10 artefacts que le `.sh`, `SHA256SUMS` généré, archive CAO sans `.venv` |
| `deploy/linux/install.sh`, `uninstall.sh`, `scripts/release.sh` | `bash -n` OK. **shellcheck n'est pas installé sur ce poste et aucune image Docker locale ne le fournit : non exécuté.** Substitut : exécution réelle des deux scripts dans un conteneur Debian jetable (golang:1.27.1, `systemctl` simulé) : 1re installation, 2e installation idempotente (jeton conservé, aucune clé dupliquée, unité identique), `atelier-admin enroll`, `seed-demo` et sauvegarde sous l'utilisateur `atelier`, rotation (5 sauvegardes → 3 conservées), sauvegarde à chaud pendant que `bambud` tourne avec l'environnement généré, restauration en dossier neuf, désinstallation (données, sauvegardes, `/etc/atelier` et utilisateur **intacts**), réinstallation (jeton réutilisé), refus d'un `--backup-dir` dans les données. Bilan : 0 échec. |
| Scripts PowerShell Windows | `install-agent.ps1` : syntaxe vérifiée, **non exécuté** (il crée une tâche planifiée et appaire un vrai agent). `configure-claude-desktop.ps1` : **exécuté** sur un faux `%APPDATA%`/`%LOCALAPPDATA%` : ajout sans écraser un autre serveur, sauvegarde horodatée, jeton hors de la configuration avec droits limités (`icacls`), idempotence, `-Remove`, JSON invalide laissé intact |
| `scripts/smoke.ps1` | **25 contrôles, 0 échec** (voir §2) |
| Worker Python (`uv run pytest` dans `workers/geometry`) | non rejoué dans ce jalon (aucune modification du worker) |
| Test sur Linux réel (Zorin), Bambu Studio AppImage headless sous `xvfb-run` | **non fait** : aucun accès au serveur ; l'AppImage n'a jamais été lancée par ce code. Le prompt de préparation (`docs/SETUP-LINUX-PROMPT.md` étape 6) doit le prouver à l'avance |

## 2. Test de fumée local (`scripts/smoke.ps1`)

Exécuté le 2026-11-06 sous Windows 11. Les `.exe` compilés sont **bloqués par « Accès refusé »** sur ce poste : le script a basculé automatiquement sur `go run` (mode `gorun`). Résultat :

```
[OK] printer-sim démarre (empreinte du certificat lue)
[OK] bambud seed-demo
[OK] GET /health          [OK] /health sans jeton
[OK] API sans jeton refusée (401)          [OK] GET /api/capabilities avec jeton
[OK] bambud enroll affiche un code         [OK] navigateur enregistré via /enroll
[OK] sans cookie : non enregistré          [OK] code à usage unique (rejeu refusé, 403)
[OK] MCP initialize (pont stdio -> bambud, --api-token-file)
[OK] MCP tools/list : 46 outils            [OK] aucun outil d'approbation de lancement
[OK] MCP get_capabilities / inspect_environment / search_models (total=10) / list_printer_models / list_printers (vide)
[OK] bambud backup (service en marche)     [OK] bambud verify --archive
[OK] bambud restore (dossier neuf)         [OK] bambud verify --data-dir (restauré)
[OK] restore refuse un dossier non vide    [OK] restore refuse un service en marche
[OK] /health après tout cela
Résultat : 25 contrôle(s), 0 échec(s) - mode gorun
```

Ce que ce test **ne couvre pas** : le simulateur d'imprimante démarre mais n'est pas relié à `bambud` dans ce script (la chaîne d'impression complète contre le simulateur est dans `TestEndToEndChainWithSimulator`) ; pas d'interface web ; pas de worker CAO (`--no-cad`).

## 3. Contrats MCP du dossier §6

| Outil | Implémenté | Testé (preuve) | Simulé / non vérifié sur matériel | Restant |
|---|---|---|---|---|
| `inspect_environment` | oui | `TestEnvironmentAndCapabilities`, smoke | – | – |
| `get_capabilities` | oui (modes `linux_seul` / `linux_renfort_windows`) | `TestEnvironmentAndCapabilities`, `TestAgentAvailabilityInCapabilities`, `TestCapabilitiesAnnounced`, smoke | – | – |
| `create_parametric_model` | oui, 5 gabarits (pince, équerre, boîte, entretoise, crochet) via worker CadQuery | `TestParametricAndModifyGeometryTools` (worker injecté), `TestRealWorkerParametric` (réel, ignoré si `uv`/CadQuery absents), `TestStepBackendMissing` | CadQuery non vérifié sur le serveur Linux | formes libres ; contraintes non gabarit |
| `import_model` / `import_files` / `scan_folder` | oui (STL, OBJ, 3MF, STEP via worker) | `TestImportRealFormats`, `TestScanFolderReadOnly`, `TestIncrementalReimport`, `TestResumeAfterInterruption`, `TestWrongMagicRejected`, `TestPathsOutsideRootsRefused`, `TestStepImportWithWorker` | – | – |
| `get_project_state` | oui (état **enregistré**, via l'agent Windows) | `TestProjectStateAndOpenInStudio` (Studio simulé), `TestReadProject`, `TestReadRealProject` (projet réel fourni par `AGENT_REAL_3MF`), `TestWindowTitleAndRecentProjects` | titre/fichier de la fenêtre mesurés sur Studio 02.08.02.61 ; ligne de commande d'un vrai Studio non rejouée | modifications non enregistrées invisibles ; projet jamais enregistré : chemin inconnu |
| `analyze_geometry` | oui (Go : étanchéité, volume, parois minces, surplombs, orientation) | `TestAnalyzeGeometryUnitAndFab`, `TestCubeAnalysis`, `TestThinWalls`, `TestOverhangT`, `TestOrientationT`, `TestDefects` | – | pas d'analyse d'une pièce multi-matériaux |
| `set_manufacturing_goals` | oui (critères, contradictions, évaluabilité) | `TestSetGoalsPersistsAndRoundTrips`, `TestGoalContradictoryCriteria`, `TestGoalAmbiguousLoadDirection`, `TestGoalUnknownCriterionIsCustom` | – | – |
| `modify_geometry` | oui (scale, rotate, mirror, translate, center_on_bed, lay_flat, cut_plane) | `TestGeometryOps`, `TestLayFlat`, `TestCutPlaneCapsAndHoles` | – | coupe avec capuchon limitée aux sections simples (sinon avertissement) ; pas de supports/coutures peints |
| `modify_slicing_profile` | oui (clés existantes de Studio, types vérifiés, révisions) | `TestModifyProfileValidationDiffAndRevisions`, `TestRulesKeysExistInRealProfiles`, `TestRealLeverValuesAcceptedByStudioAndSupports` (réel local) | – | – |
| `modify_open_project` | répond `not_supported` + alternative (modifier une copie puis `open_in_studio`) | `TestSessionToolsWithoutAndWithAgent` | **impossible** sans passerelle C++ dans le processus de Studio (J8 §4) | passerelle C++ non entreprise (dossier §3 exige un audit du dépôt Studio et une compilation de référence) |
| `create_experiment` | oui (figeage, référence + variantes bornées, découpes séquentielles) | `TestCreateExperimentRefusals`, `TestGenerateThreeVariants`, `TestRealExperimentCube` (réel local), `TestRealOrientationCandidateIsSliced` | – | – |
| `execute_headless_slice` | oui (Bambu Studio CLI, sous-processus, `result.json`) | `TestRealP1SCube`, `TestRealP1SCylinder`, `TestRealA1MiniCubeAndCylinder`, `TestRealStudioFullChain` (réel local Windows) ; `TestFake*` ; `TestSliceWithoutStudioFailsClearlyAndIsIdempotent` | **Linux headless (AppImage + xvfb) jamais exécuté par ce code** ; wrapper `xvfb-run` ajouté en J9 (`TestStudioWrapperDefaults` teste le choix, pas Studio) | qualification Linux/Windows des sorties sur un corpus commun |
| `get_job_status` / `cancel_job` | oui | `TestIntegrationJobLifecycleThroughMCP`, `TestGetJobStatusRedactsSecrets`, `TestCancelQueuedAndRunning`, `TestFakeCancelKillsTree` | – | – |
| `compare_variants` | oui (mesures réelles étiquetées « estimation slicer », classement seulement sur le mesurable) | `TestRankingsOnlyForMeasurable`, `TestEngineeringOrientationChangesSafetyFactor`, `TestEngineeringRefusedForTPUShownOnCandidate` | résistance = calcul à hypothèses, pas une mesure | corrélation estimations ↔ pièces réelles : aucune donnée |
| `capture_preview` | oui (PNG Go pur : iso d'un modèle, tracé 2D d'une couche) | `TestRenderIsoPNG`, `TestRenderLayer`, `TestDrawTextAndLegendWrap`, `TestPreviewRouteServesOnlyRegisteredPreviews` | – | pas de rendu des supports/rétractions |
| `apply_variant` | oui (garde de révision) | `TestApplyVariantStaleRevision` | – | – |
| `validate_print_artifact` | oui (archive + compatibilité imprimante/matières) | `TestValidateArchiveBranches`, `TestCheckAssignments`, `TestEndToEndChainWithSimulator` | matières AMS : déclaratives (rien ne les lit sur la machine) | – |
| `upload_print_artifact` | oui (FTPS implicite, sha256 vérifié, idempotent, **sans lancer**) | `TestUploadIdempotentAndVerified`, `TestUploadRejectsWrongShaOrSize`, `TestUploadReplayWithDifferentParamsRefused` | **simulé** : chemin distant, reprise de session TLS, EPSV non vérifiés sur la vraie P1S (J7 §5 pts 3, 7) | essai matériel |
| `get_printer_status` | oui (état normalisé + fraîcheur) | `TestStatusIdleAndFirmware`, `TestPartialReportsMerge`, `TestNormalizeGcodeState`, `TestHMSPauseSurfaced` | **simulé** : formats réels de `hms`/`print_error`/`total_layer_num`, cadence des rapports (J7 §5 pts 4–6) | essai matériel |
| `start_print` | oui (accord humain lié et expirant, réservation, état frais et idle, consommation atomique juste avant MQTT) | `TestConcurrentStartsSendOneCommand`, `TestStartRefusals`, `TestApprovalExpiresTwoMinutesAfterTheClick`, `TestApprovalInvalidatedAfterNotSent`, `TestConsumeOKThenTransitionFailsAbortsBeforeSend`, `TestStartIdempotencyKeyIsComposedAndParamsChecked`, `TestRecoverTurnsInterruptedStartIntoUnknown`, `TestNoMCPToolCanApproveAConfirmation`, `TestConfirmationInvalidatedWhenBindingChanges` | **simulé** : forme réelle de `project_file`, réponse de l'imprimante, `ams_mapping` (supposé), nouveau lancement depuis FINISH | **premier lancement réel : à faire** (BRANCHEMENT étape 8) |
| `reconcile_print` | oui (lecture seule, jamais de commande de lancement) | `TestConnectionLostAfterStartThenReconcile`, `TestIgnoredStartReconcilesToNotStarted`, `TestReconcileWaitsForSettle`, `TestReconcileNotStartedNeedsReferenceTime` | **simulé** (pannes `drop_after_start`, `ignore_start`) | perte de lien réelle non essayée |
| `cancel_print` | oui : PRINTING → demande d'arrêt + `stop_url` validée par l'humain | `TestCancelPrintingOnlyCreatesAStopRequest`, `TestPagesRequireEnrolledBrowser`, `TestStopsPagesAndConsumedAreExpiredAndPurged` | **simulé** : Pause/Stop réels non essayés | – |
| `record_print_result` / `get_print_memory` | oui | `TestRecordPrintResultAndComparableMemory` | – | aucun retour d'essai réel enregistré |
| `add_printer` / `list_printers` / `list_printer_models` | oui ; hôte limité au LAN ; empreinte du certificat lue par le serveur et épinglée sur `/setup` ; code d'accès hors MCP | `TestAddPrinterToolHasNoBypassInputs`, `TestCheckPrinterHostPolicy`, `TestSetupPagePinsTheDisplayedFingerprint`, `TestHostChangeForcesRepin`, `TestSetupPageWarnsOnSerialMismatch`, `TestFileSecretStoreEncryptedAndBound`, `TestSeedPrinterModelsFromProfiles`, `TestFetchCertInfoDescribesTheCertificate` | **le certificat réel de la P1S n'a jamais été lu** (CN = série supposé) | essai matériel |
| `open_in_studio` | oui (par l'agent Windows) | `TestProjectStateAndOpenInStudio` | ouverture réelle essayée une fois (J8 §4) ; seconde instance non mesurée | – |

Non listés au dossier mais livrés : `list_goal_criteria`, `search_models`, `list_collection`, `get_model_details`, `tag_models`, `link_model_versions`, `find_exact_duplicates`, `preview_file_operations`, `apply_file_operations`, `restore_from_trash`, `get_preview_asset`, `get_print_confirmation_view`, `show_atelier`/`atelier` (vue de démonstration). Le pont expose 46 outils (smoke).

## 4. Gestionnaire de fichiers 3D (dossier §14.2)

| Capacité | Implémenté | Testé (preuve) | Restant / limites |
|---|---|---|---|
| Import, scan de dossiers (lecture seule), index incrémental, reprise | oui | `TestImportRealFormats`, `TestScanFolderReadOnly`, `TestIncrementalReimport`, `TestResumeAfterInterruption` | – |
| Collections, tags, recherche paginée | oui | `TestSearchFiltersAndPagination`, `TestNormTagsAndUnits` | – |
| Renommage / déplacement maîtrisés (aperçu obligatoire, blobs intacts) | oui | `TestOperationsRequirePreview`, `TestRenameFileKeepsBlob` | – |
| Doublons exacts par empreinte | oui | `TestExactDuplicates` | similarité géométrique : **non implémentée** (dossier : fonction ultérieure) |
| Versions immuables, liens entre versions, provenance | oui | `TestLinkVersions`, `TestSourceFilesAndVersionsImmutable`, `TestCatalogAuditAndImmutability` | – |
| Corbeille et restauration | oui | `TestOperationsRequirePreview` (suppression = corbeille), `TestCatalogToolsEndToEnd` | – |
| Journal d'audit des mutations | oui | `TestMutationRollbackLeavesNoAudit`, `TestCatalogAuditAndImmutability` | – |
| Vignettes / aperçus GLB en arrière-plan, URL signées | oui | `TestSignedAssetsServeOnlyPreviews` | vues 3D de l'interface : chantier `web/` |
| Limites d'extraction, validation des formats | oui (zip-bomb, composants explosifs, magic) | `TestThreeMFZipBombEntry`, `TestThreeMFExplosiveComponentsFailFast`, `TestWrongMagicRejected`, `TestReadFileTooLarge` | – |
| **Téléchargement d'un original et export groupé (zip en flux)** | oui (`GET /api/files/{id}/download`, `POST /api/export`) | `TestDownloadExportPartsAndAnalyze` (octets et sha256 identiques, archive valide avec manifeste, refus de fichier inconnu / de modèle inconnu / de liste vide), `TestSafeNameNeverEscapesTheArchive` | seuls les originaux copiés dans le stockage par empreintes sont servis (jamais un chemin du disque) ; les fichiers « Windows seulement » sont listés comme laissés de côté ; 200 modèles maximum par archive ; renommage / déplacement **physiques** volontairement absents (catalogue seulement) |
| Sous-parties stables d'un modèle (objets du fichier et corps connexes) | oui (`GET /api/models/{id}/parts`) | `TestDownloadExportPartsAndAnalyze` (identifiants identiques d'un appel à l'autre) | dérivées de l'analyse stockée ; un STL n'a qu'un objet |
| Analyse avec unité confirmée / fabricabilité par machine par HTTP | oui (`POST /api/models/{id}/analyze`) | `TestDownloadExportPartsAndAnalyze` (cm ⇒ dimensions recalculées, unité invalide refusée, modèle inconnu 404) | fabricabilité = heuristiques |
| Sauvegarde fichiers **et** base + essai de restauration | oui (J9) | `TestBackupWhileJobRunningThenRestoreInFreshDir`, `TestVerifyDataDirDetectsCorruptAndMissingBlobs`, `TestRestoreRefusesNonEmptyDirUnlessForce`, `TestCorruptedAndTruncatedArchives`, `TestHostileArchiveEntriesRefused`, `TestManifestHashesAndTamperedTarDetected`, `TestExcludeSecrets`, `TestBackupRestoreVerifyCommands`, smoke | pas de format `.tar.zst` (aucune dépendance zstd : `.tar.gz` ou `.tar`) ; archive non chiffrée (elle contient le coffre chiffré **et** sa clé : protéger le support) ; clé maître sous Windows liée à DPAPI : une archive Windows ne se restaure pas sur un autre compte/poste (non concerné sur Linux : clé fichier 0600) |

## 5. Modes d'exécution (dossier §15)

| Fonction | Implémenté | Testé | Limites |
|---|---|---|---|
| Linux seul (préparer, découper, comparer, déposer sans Windows) | oui, sans dépendance cachée à Windows | chaîne `TestEndToEndChainWithSimulator` (sans agent) ; `get_capabilities` mode `linux_seul` (smoke) | **non démontré sur le vrai serveur Linux ni avec Windows réellement éteint** (BRANCHEMENT étape 10) |
| Agent Windows : appairage, protocole, inventaire, envoi vérifié, slicing de renfort | oui | `TestPairingOneShotAndSecretNotStored`, `TestUploadResumeAndVerification`, `TestInventoryUploadImportEndToEnd`, `TestSliceReinforcement`, `TestSliceReinforcementReal` (réel local) | service Windows implémenté mais **non exécuté** (élévation) ; DPAPI « user » lié au compte |
| Perte de l'agent sans seconde impression | oui (`unknown`, réconciliation, travaux physiques refusés) | `TestAgentLostMakesTaskUnknownThenReconciles`, `TestPhysicalTaskIsNeverRequeued`, `TestAgentLostDuringTaskThenRestartReconciles` | – |
| Fichiers « disponibles uniquement sur Windows » marqués comme tels | oui | `TestWindowsOnlyThenCopy`, `TestInventoryUploadImportEndToEnd` | – |
| Qualification des sorties Linux/Windows sur un corpus commun | **non faite** | – | aucun slicer Linux qualifié |
| Web app accessible depuis un autre appareil | service prêt (API, pages humaines, cookie, garde Host) et **interface livrée** (`web/`, embarquée dans `bambud`, voir §0) | `TestHumanPagesNeedCookieAPINeedsBearer`, `TestEnrollFlowAndCookieFlags`, parcours `web/scripts/e2e.mjs` | accès depuis un autre appareil du LAN **non essayé** (un seul poste, loopback) ; HTTP clair |
| Conversation Claude depuis un autre appareil (connecteur MCP distant authentifié) | `/mcp` Streamable HTTP + Bearer existe | `TestProxyBridge`, `TestAPIToken`, smoke | exposition hors LAN (HTTPS, publication, accès depuis le cloud) **non qualifiée, non configurée** ; un VPN de téléphone n'est pas une connectivité cloud |
| Vues MCP App dans Claude Desktop | une vue de démonstration (`ui://bambu/atelier.html`) et `get_print_confirmation_view` en lecture seule | `TestIntegrationToolsAndResource` | contenu final = chantier `web/` ; rendu dans Claude Desktop **non qualifié** ; `visibility:["app"]` dépend de l'hôte |

## 6. Sécurité (modèle : `docs/SECURITY.md`)

Implémenté et testé : aucun outil MCP n'approuve (`TestNoMCPToolCanApproveAConfirmation`), accord humain par navigateur enregistré (`TestHumanPagesNeedCookieAPINeedsBearer`, `TestPostNeedsSameOriginNavigation`), code à usage unique et verrou (`TestCodeFormatAndSingleUse`, `TestLockoutAfterRepeatedFailures`), jeton jamais persisté ni renvoyé (`TestConfirmationTokenNeverPersistedNorReturned`), secrets absents des sorties (`TestSecretNeverPrinted`, `TestJobEndpointRedactsSecrets`, `TestEnqueueRejectsSecretsInParams`), garde Host et cross-origin (`TestHostValidation`, `TestCrossOriginProtection`). J9 ajoute : jeton du pont lisible depuis un fichier (`TestResolveTokenFromFile`, `TestTokenFileIsSentAsBearerAndNotLogged`), restauration qui refuse chemins hostiles (`TestHostileArchiveEntriesRefused`), atelier-admin qui n'affiche jamais le jeton d'API.

Limites assumées : processus local avec les droits de l'utilisateur hors périmètre ; HTTP clair sur le LAN (pas de TLS natif) ; pas de rotation du jeton d'API.

## 7. Livrables du jalon 9

| Livrable | État |
|---|---|
| `bambud backup` / `restore` / `verify` | fait, testé (§4) |
| `scripts/release.sh` / `.ps1` | fait ; `.sh` et `.ps1` exécutés (sans l'étape web : `web/` est absent de cette copie, l'étape est prévue — `npm ci`, `npm run build`, `npm run build:mcp-app` s'il existe — mais **non exercée**) |
| `deploy/linux/install.sh` / `uninstall.sh` | fait ; exécutés en conteneur Debian (systemctl simulé) ; **pas sur Zorin réel, pas de vrai systemd** (les directives de durcissement `ProtectSystem=strict`, `ReadWritePaths` n'ont donc **pas** été éprouvées : à contrôler au premier démarrage réel, `journalctl -u atelier`) |
| Wrapper `xvfb-run` du slicer | exposé : `--studio-wrapper` / `BAMBU_STUDIO_WRAPPER`, défaut `xvfb-run -a` sous Linux sans écran si présent ; `--studio-bin` ajouté (avant : variable d'environnement seulement) |
| `deploy/windows/install-agent.ps1`, `configure-claude-desktop.ps1`, `claude-desktop.md` | faits ; `--api-token-file` implémenté dans `cmd/bambu-mcp` (testé) |
| README, INSTALL, BRANCHEMENT, UTILISATION, ETAT | faits |
| `scripts/smoke.ps1` | fait, 25/25 |

## 8. Ce qui reste (par ordre d'importance)

1. **Tout l'essai matériel** (BRANCHEMENT) : certificat réel, état réel, FTPS, `project_file`, `ams_mapping`, reprise de session TLS, premier lancement, arrêt, perte réseau. Les 9 points de J7 §5 restent ouverts.
2. **Preuve sur le vrai serveur Linux** : Bambu Studio AppImage headless sous `xvfb-run`, CadQuery, unité systemd durcie, timer de sauvegarde. Le prompt de préparation couvre le slicing ; le reste se constate à l'installation.
3. **Interface web** : parcours complet vérifié contre le simulateur (§0). Reste : variantes **par zone** et réglages limités à un objet (`web/src/api/pending.ts`, entrée `zone_overrides`) ; photos d'un retour d'expérience ; accès depuis un autre appareil ; rendu des vues MCP App dans Claude Desktop (non qualifié).
4. **Accès conversationnel hors Windows** : connecteur MCP distant authentifié, HTTPS, exposition : à qualifier puis configurer.
5. **Qualification Linux/Windows des sorties de slicing** sur corpus commun.
6. **Modification en direct du projet ouvert dans Studio** : exige une passerelle C++ (hors périmètre) ; l'alternative « copie + ouverture » est livrée.
7. Similarité géométrique ; supports et coutures peints ; format `.tar.zst` ; chiffrement de l'archive de sauvegarde ; rotation du jeton d'API ; TLS natif ; mises à jour automatiques (aujourd'hui : relancer `install.sh`).
