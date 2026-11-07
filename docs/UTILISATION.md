# Utiliser l'Atelier avec Claude

Vous parlez normalement ; Claude choisit les outils. Les noms d'outils entre parenthèses ne sont là que pour comprendre ce qui se passe (vous n'avez pas à les connaître). Les réponses ci-dessous sont **illustratives** : elles montrent la forme et les garde-fous, pas des sorties enregistrées.

Trois règles à garder en tête :

1. **Rien ne part sur l'imprimante sans votre clic** sur une page web, depuis un navigateur enregistré. Claude prépare, dépose, demande ; il ne peut pas approuver.
2. **Les originaux ne bougent pas.** Toute modification crée une nouvelle version ; vous pouvez revenir en arrière.
3. **Claude distingue le mesuré, le calculé et le supposé.** Un critère qu'on ne peut pas évaluer (aspect de surface, tenue réelle) est signalé comme « à essayer », pas noté.

---

## 1. « Retrouve ma pince »

> **Vous** : Retrouve ma pince.
>
> **Claude** (`search_models` : texte « pince ») : J'ai trouvé 3 modèles : *Pince serre-joint v2* (3MF, collection Atelier/Outils), *Pince 40 mm* (STL, Atelier/Outils/Anciens), *Pince cassée* (dans la corbeille). Je te montre la première ? (`get_preview_asset` affiche l'aperçu 3D dans la conversation.)
>
> **Vous** : Oui, et dis-moi si j'en ai des doublons.
>
> **Claude** (`get_model_details`, `find_exact_duplicates`) : *Pince serre-joint v2* a 2 versions ; la v1 est identique au fichier *Pince 40 mm* (même empreinte) : c'est un doublon exact, pas une pièce différente. Aucun fichier supprimé.

Ce qu'il faut savoir :

- Un modèle encore **« disponible uniquement sur Windows »** (indexé par l'agent mais pas copié) est signalé comme tel : il n'est pas disponible quand le PC est éteint. Demandez « copie-le sur le serveur » (travail d'envoi avec empreinte vérifiée).
- Ranger : « mets ces 5 pièces dans Outils/Serre-joints et étiquette-les `PLA` » → Claude **propose d'abord un aperçu** des opérations (`preview_file_operations`) et n'applique (`apply_file_operations`) qu'après votre accord. Les suppressions vont à la corbeille et se restaurent (`restore_from_trash`).

## 2. « Fais-moi un support pour… »

> **Vous** : Fais-moi un support en L de 40 mm de côté pour fixer une petite carte, en PLA.
>
> **Claude** : Je peux le générer à partir du gabarit *équerre* (`bracket`). Il me manque l'épaisseur de la tôle, le diamètre des vis et leur entraxe. Tu veux 3 mm, vis M3, entraxe 30 mm ?
>
> **Vous** : Oui.
>
> **Claude** (`create_parametric_model`) : Modèle créé et rangé au catalogue (STEP + STL, version v1). Hypothèses du générateur : trous de 3,4 mm pour M3, congé de 2 mm, pas de renfort. (`capture_preview` : image isométrique.) Je le pose à plat pour l'impression ? (`modify_geometry` : `lay_flat`, nouvelle version, l'original reste.)

Limites honnêtes :

- Les gabarits paramétriques actuels : `clamp` (pince), `bracket` (équerre), `box` (boîte), `spacer` (entretoise), `hook` (crochet). Pour une forme libre, importez un fichier ; Claude ne dessine pas de CAO arbitraire.
- Le worker CAO doit être installé (`uv sync --extra cad`) ; sinon Claude répond `cad_backend_missing` et le dit.
- Les calculs de résistance (poutre en flexion, bossages à vis, ajustements, emboîtements) viennent d'un module de calcul à **domaine de validité explicite** ; chaque résultat dit s'il est mesuré, calculé ou heuristique.

## 3. « Compare 3 variantes »

> **Vous** : Cette équerre doit être solide, vissée de haut en bas, et jolie. Compare trois façons de l'imprimer.
>
> **Claude** (`list_goal_criteria`, `set_manufacturing_goals`) : J'ai noté deux critères : *résistance* (direction de l'effort : axe Z, d'après ta phrase — tu confirmes ?) et *aspect de surface*. Attention : ils tirent dans des directions opposées sur l'épaisseur de couche ; lequel prime ?
>
> **Vous** : La résistance.
>
> **Claude** (`create_experiment`) : J'ai figé la version v1 du modèle et le profil de base (P1S 0,4, PLA), et lancé 4 découpes indépendantes : une **référence** et trois variantes justifiées (orientation, parois/remplissage, hauteur de couche). Elles tournent une à une (un seul travail lourd à la fois).
>
> *(plus tard — `get_job_status`, `compare_variants`)*
>
> **Claude** : Tableau des mesures réellement disponibles, étiquetées « estimation slicer » : temps, masse de filament, nombre de couches, supports. Classement uniquement sur ce qui se mesure (temps, matière). Sur la résistance, je ne peux que comparer des facteurs calculés (module d'ingénierie, hypothèses affichées) ; l'aspect de surface est **non évaluable** : il faut un essai physique, je te propose de n'imprimer que les deux premières.
>
> **Vous** : Prends la variante B.
>
> **Claude** (`apply_variant`) : Variante B appliquée comme profil du modèle (je vérifie que le modèle n'a pas changé depuis la comparaison).

Ensuite, quand vous avez imprimé : « La B est sortie un peu gauchie dans un coin » → `record_print_result` lie votre retour aux réglages réels ; la prochaine fois, `get_print_memory` ressort les essais comparables (même matière, même machine, géométrie proche).

Pour affiner un profil à la main : « Passe les parois à 4 » → `modify_slicing_profile` (clés existantes de Bambu Studio, valeur du bon type, révision et différences affichées).

## 4. « Envoie et lance »

> **Vous** : Envoie la variante B sur la P1S.
>
> **Claude** (`execute_headless_slice` → `validate_print_artifact`) : Découpé par Bambu Studio : 1 h 12, 38 g (estimation slicer). Validation : archive correcte, compatible P1S buse 0,4. Matière : le fichier attend du PLA ; tu as quoi dans l'AMS ?
>
> **Vous** : Du PLA noir en ams1 emplacement 1.
>
> **Claude** (`upload_print_artifact`, avec une clé d'idempotence) : *Le fichier equerre_B.gcode.3mf est envoyé sur la P1S. Veux-tu lancer cette impression ?* (L'imprimante n'a ni chauffé ni bougé.)
>
> **Vous** : Oui, lance.
>
> **Claude** (`request_print_confirmation`) : Voici la page à ouvrir **dans ton navigateur enregistré** : `http://…:7878/confirm/…`. Elle liste le fichier, l'imprimante, l'empreinte, le plateau et la matière. Tu y coches « le plateau est vide et dégagé » puis tu cliques « Lancer l'impression ». Je ne peux pas le faire à ta place.
>
> *(vous cliquez ; votre accord vaut 2 minutes)*
>
> **Vous** : C'est fait.
>
> **Claude** (`start_print`, puis `get_print_status`) : Lancé : préparation en cours (chauffe, nivellement), puis impression. Couche 12/240, 5 %, 1 h 07 restantes.

Si ça se passe mal :

- **« Liaison perdue après la commande »** (`start_unknown`) : Claude **ne relance pas**. Il lit l'état réel de l'imprimante (`reconcile_print`) et vous dit si l'impression a démarré ou non.
- **Arrêter** : au bouton de l'imprimante, toujours. Via Claude : « Arrête l'impression » → `cancel_print` crée une demande d'arrêt et vous donne un lien `…/stop/…` à valider dans le navigateur enregistré.
- **Changer d'avis** sur la matière ou l'imprimante après avoir été interrogé : la confirmation devient invalide, il faut en redemander une.
- **Deux impressions de suite avec le même accord** : impossible, chaque accord est à usage unique et lié à *cette* impression.

## 5. Depuis le PC Windows (renfort)

- **Importer mes dossiers 3D** : « Inventorie D:\3D » (agent, lecture seule) → « Copie tout sur le serveur » : chaque fichier est copié avec empreinte vérifiée ; les originaux Windows ne sont jamais touchés ; un fichier modifié entre-temps n'est pas envoyé.
- **Voir le projet ouvert dans Bambu Studio** : `get_project_state` lit l'état **enregistré** du projet (objets, plateaux, réglages) et le titre de fenêtre ; les modifications non enregistrées sont invisibles.
- **Ouvrir le résultat dans Studio** : « Ouvre la variante B dans Bambu Studio » → `open_in_studio` (copie vérifiée, ouverture sur le PC).
- **Modifier le projet ouvert en direct** : `modify_open_project` répond *non pris en charge* — il n'existe pas d'API publique pour piloter la session ; la voie livrée est de modifier une **copie** puis de l'ouvrir.
- Si le PC est éteint, Claude Desktop l'est aussi : utilisez l'interface web depuis un autre appareil pour suivre et gérer les travaux.

## 6. Ce que Claude refusera ou signalera

| Demande | Réponse |
|---|---|
| « Lance sans me redemander » | Impossible : l'accord se donne sur la page web, jamais dans la conversation |
| « Voici le code d'accès de l'imprimante : … » | Il ne le saisit nulle part : Claude vous renvoie vers la page `/setup` (ne le tapez pas dans le chat) |
| « Imprime avec un matériau qui n'est pas dans le catalogue » | Avertissement « non vérifié », jamais accepté en silence |
| « Garantis que ce sera solide » | Il distingue ce qui est calculé (avec hypothèses) de ce qui demande un essai physique |
| « Supprime les doublons » | Il les liste et propose ; jamais de suppression automatique |
| Critère sans mesure (transparence, toucher…) | Marqué *non évaluable* avec l'essai à prévoir |

Chaque action qui modifie quelque chose est enregistrée dans un journal d'audit (qui, quoi, quand).

---

## 7. L'interface web (sans passer par Claude)

L'interface est servie par `bambud` lui-même (même adresse que l'API, par exemple `http://127.0.0.1:7878/`), en thème clair ou sombre, utilisable de 800 à 1440 px. Elle n'affiche **que des données réelles du serveur** : un écran vide dit qu'il est vide, une valeur inconnue s'affiche « inconnu », un service injoignable est signalé par un bandeau. Les pastilles du haut montrent le service, l'agent Windows (clic : écran de l'agent) et le backend CAO ; « Enregistrer ce navigateur » apparaît tant que le navigateur n'est pas enregistré (`bambud enroll`, puis `/enroll`).

| Écran | Ce qu'on y fait |
|---|---|
| **Bibliothèque** | Recherche, filtres, import non destructif (inventaire d'abord), sélection groupée : étiquettes, déplacement, corbeille (toujours avec aperçu des changements), **export en archive zip** des originaux copiés (manifeste d'empreintes inclus). |
| **Fiche modèle** | Viewer 3D du vrai fichier, sous-parties, analyse géométrique. **Unité et fabricabilité** : confirmer l'unité d'un STL/OBJ (l'analyse est recalculée, l'unité est mémorisée sur le fichier) et évaluer la fabricabilité pour une machine (heuristique, jamais une garantie). **Télécharger** l'original (l'empreinte sha256 est annoncée) ; **Exporter (zip)**. |
| **Préparation** | Objectif de finition traduit en critères (le serveur signale ambiguïtés, compromis, critères à vérifier par essai) ; **objet ou sous-partie** du modèle (identifiants stables) ; **réglages manuels** validés contre les profils réels de Bambu Studio, avec l'ancien et le nouveau réglage et le profil d'origine ; création de l'expérience (les variantes partent de votre révision). *Limite : les réglages valent pour la pièce entière, pas pour une zone (voir `docs/ETAT.md`).* |
| **Comparaison** | Sans expérience ouverte : la **liste des expériences passées**. Sinon : tableau des variantes (mesures étiquetées « estimation slicer » ou « lecture du G-code »), classement par critère mesurable, application d'une variante (refusée si le modèle a changé), **mémoire des essais comparables** et **« Enregistrer l'essai réel »** (réussite, observations, mesures, réglages réellement utilisés ; les écarts avec le plan sont consignés). |
| **Matériaux** | Modèles d'imprimantes Bambu connus, parc enregistré, **fiches matériaux** issues des fiches techniques du fabricant (valeur, unité, tolérance, source, date, niveau de confiance, domaine de validité ; une propriété sans valeur est « inconnue »), presets filament de Bambu Studio compatibles avec une machine. |
| **Impression** | Ajout d'imprimante, découpe, validation, dépôt, demande de confirmation. *Le lancement se décide sur la page `/confirm` du serveur, jamais dans l'interface.* Pendant l'impression : avancement ; « Demander l'arrêt » ouvre la page `/stop` (rien n'est arrêté avant votre validation). Lancement incertain : **« Vérifier l'état réel »** lit l'imprimante sans jamais relancer. Impression terminée : lien vers le formulaire de retour d'expérience. |
| **Tableau de bord** | Quatre onglets : **Usage** (heures machine, nombre d'impressions, taux de réussite, filament par matière, coût cumulé estimé, par période, imprimante et projet) ; **Journal** (la fiche de chaque impression terminée : réglages effectifs, durée et masse estimées/réelles, note de 1 à 5, défauts à cocher, commentaire, **photos** JPEG/PNG/WebP, ajustements proposés) ; **Prix et coût** (prix du kWh, du kg par matière, usure, puissances mesurées, estimateur) ; **Post-traitement** (ponçage, apprêt, lissage, vernis, recuit, inserts, collage). |
| **Agent Windows** (pastille « Windows ») | État des agents, inventaire des fichiers encore « Windows seulement », **demande de copie** vers le serveur (taille et empreinte vérifiées), suivi des travaux. L'appairage se fait sur `/agents/pair` (navigateur enregistré). L'agent ne peut ni lancer ni arrêter une impression. |

Ce que l'interface ne fait pas : approuver un lancement ou un arrêt (pages serveur seulement), saisir le code d'accès de l'imprimante (page `/setup`), renommer ou déplacer physiquement des fichiers (le catalogue seul est modifié).

---

## 8. Coût, journal des impressions, tableau de bord, finition

Tout est disponible dans Claude (outils) et dans l'interface web (écran **Tableau de bord**). Aucun de ces outils ne lance ni n'arrête d'impression.

### Combien coûte cette impression ?

> « Combien va coûter l'impression 3 de la plaque 1 ? » → `estimate_cost` avec `print_job_id`.
> « Et pour 180 g de PETG pendant 5 h sur la X1C ? » → `estimate_cost` avec `printer_model`, `duration_minutes`, `filaments`.

Le coût est la somme de trois lignes :

- **Filament** : masse de chaque emplacement × prix du kg. Le prix est, dans l'ordre : celui que vous donnez pour cet appel, celui de la bobine (`spool_per_kg`, clé = libellé de la bobine), celui de la famille de matière (PLA, PETG, ABS, ASA, TPU, PC, PA, PA-CF…), sinon une valeur par défaut **indicative**.
- **Électricité** : (puissance de chauffe × durée de chauffe + puissance d'impression × durée restante) × prix du kWh. La puissance dépend du modèle d'imprimante (P1S, P1P, X1C, X1E, A1, A1 mini, H2D, H2S, P2S) et de la matière (facteur plus fort pour ABS, ASA, PC, PA : plateau et enceinte plus chauds ; chauffe plus longue). **Ces puissances sont des ordres de grandeur non mesurés** : remplacez-les par vos relevés de prise mesurante (`power_overrides`).
- **Usure** : heures machine × amortissement de la machine + usure de la buse (réglages, **0 par défaut**).

Pour une impression terminée, la réponse contient aussi le **coût réel estimé** : durée observée par le service (début et fin de l'impression), masse déclarée par vous (pesée) ou, à défaut, masse prévue (au prorata de l'avancement si l'impression a été interrompue).

Réglez les prix une fois :

> « Mon kWh est à 0,21 € et mon PLA à 17 €/kg, recalcule l'historique. » → `set_cost_settings` (`apply_to_history`).

**Toujours une estimation.** Chaque réponse porte le mot ESTIMATION et la liste de ses hypothèses (prix par défaut, puissances indicatives, durée du slicer supposée chauffe comprise, purges et ratés non comptés, pas de temps humain ni de consommables). Les prix encore à leur valeur par défaut sont signalés (`defaulted_prices`).

**Par projet** (collection de la bibliothèque) : `get_project_costs` (ou le tableau « Par projet » du tableau de bord) additionne le coût réel estimé des impressions terminées et l'estimation des impressions préparées pas encore terminées.

### Le journal des impressions

Chaque impression terminée, échouée ou annulée a une **fiche** (créée à la demande, la première fois que vous ouvrez le journal ou que Claude le lit). Elle garde les réglages effectifs (profil, buse, surcharges, orientation), la durée et la masse estimées et réelles, le coût, des photos, une note de 1 à 5, un commentaire et des défauts à cocher (gauchissement, fils, couches visibles, décollement de couches, adhérence, dimensions, patte d'éléphant, sous/sur-extrusion, boutons, supports, surplombs, décalage de couches, surface, autre).

> « Note l'impression 3 : 4 sur 5, un peu de fils, 52 g pesés. » → `update_print_log`.
> « Montre le journal des impressions en PETG ratées. » → `list_print_log`.

- **Photos** : depuis l'interface (« Ajouter une photo » sur la fiche) ou par référence de blob (`photo_blobs`). Seuls JPEG, PNG et WebP sont acceptés (12 Mio au plus, 8 photos par fiche) ; un SVG est refusé.
- **`record_print_result` est étendu sans changer** : il accepte en plus `print_job_id`, `rating`, `defects`, `actual_mass_g` et `actual_duration_minutes`, et rattache alors le retour à la fiche. Les appels existants se comportent comme avant.
- **Mémoire des essais** : avec `experiment_id` et `candidate_id`, `update_print_log` verse le retour de la fiche dans la mémoire existante (`get_print_memory`), qui ne réutilise les essais que pour la même matière, la même machine et une géométrie similaire.

### Ajustements proposés pour la prochaine impression

> « La dernière est pleine de fils, que changer ? » → `suggest_print_adjustments` avec la fiche.

Des **règles explicites de niveau heuristique** (fichier `internal/insights/data/adjustments.json`, avec sources) associent chaque défaut à des changements de réglages : clé du profil Bambu Studio, sens, ordre de grandeur à partir de la valeur actuelle, justification et compromis. Un défaut est traité s'il est coché sur la fiche, demandé, ou **récurrent** (au moins deux fiches comparables : même matière, même machine). La meilleure fiche comparable (réussie, notée 4 ou 5) est indiquée comme base de départ.

**Jamais automatique** : rien n'est appliqué. Changez un réglage à la fois (`modify_slicing_profile`, `create_experiment`), refaites une pièce comparable, puis notez le résultat.

### Tableau de bord

> « Où en est ma machine ce mois-ci ? » → `get_usage_dashboard` (texte + image du graphe des heures machine par période).

Heures machine, nombre d'impressions, **taux de réussite** (réussies ÷ réussies + partielles + ratées ; les annulations par vous sont comptées à part), filament consommé par matière, coût cumulé **estimé**, ventilés par période (jour, semaine ou mois), imprimante et projet. Le statut « réussie » vient de la fin de l'impression et se corrige dans la fiche.

### Conseils de post-traitement

> « Je veux une surface lisse sur ma pièce en ABS. » / « Des inserts M3 dans du PETG. » → `post_processing_advice` (matière, finition, géométrie, usage, tailles d'inserts).

Réponse : des **étapes ordonnées** (préparation, ponçage avec les grains, apprêt, lissage chimique quand il est pertinent et avec ses dangers, peinture, vernis ou résine, recuit, inserts thermo-sertis avec les diamètres de trou de `internal/engineering`, collage), les techniques **déconseillées pour cette matière et pourquoi**, les dangers (acétone, poussières de fibres de carbone, résines) et des avertissements selon l'usage (extérieur, chaleur, cotes serrées, contact alimentaire, parois fines). Les données sont embarquées et versionnées (`internal/insights/data/postprocess.json`), de niveau heuristique, avec leurs sources : à essayer sur une pièce témoin.

Honnêteté : le PETG et le polycarbonate imprimés en FDM restent **translucides**, pas transparents ; le PLA ne se lisse pas à l'acétone.
