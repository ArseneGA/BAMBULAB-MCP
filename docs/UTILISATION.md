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

### Plusieurs pièces sur un plateau

> **Vous** : Mets les 6 pièces de la presse sur le moins de plateaux possible, matrices à 100 %.
>
> **Claude** (`search_models`, `plan_plate`) : J'ai retrouvé *matrice* (×4) et *poussoir* (×2). Plan : **TIENT** — 6 pièces sur **1 plateau** de la P1S (256 × 256 mm, disposition réelle calculée par Bambu Studio), empreinte 36 % du plateau, hauteur max 30 mm. Matrices à 100 % de remplissage, poussoirs au profil par défaut. (`capture_preview`, mode `plate` : *je te montre le plateau* — image avec la grille de 10 mm, la zone interdite de la P1S et chaque pièce numérotée à sa place, légende avec les réglages de chacune.)
>
> **Vous** : Parfait, découpe.
>
> **Claude** (`execute_plate_slice`) : 1 impression en file (une par plaque). *(plus tard)* Découpé : 7 h 29, 554 g de filament ; par pièce, relu dans le G-code : matrice 133,5 g (100 %), poussoir 9,9 g. Validation OK pour la P1S. Je dépose le fichier ? — puis, comme d'habitude, tu valides **sur la page de confirmation**.

Ce qu'il faut savoir :

- **Réglages par pièce** : remplissage (%), motif de remplissage, parois, hauteur de couche, couches du dessus/dessous, bordure, supports ; et des **plages de hauteur** (ex. couches de 0,1 mm jusqu'à 5 mm). Ils sont écrits dans le 3MF et **relus par Bambu Studio** : le résumé de chaque plaque donne la matière et le nombre de couches de chaque pièce, lus dans le G-code, pour que vous voyiez que « 100 % » a bien pesé plus lourd que « 15 % ».
- **Ça tient ou pas, avant de découper** : `plan_plate` compare l'empreinte et la hauteur de chaque pièce au plateau réel de l'imprimante (profils Bambu, zones interdites comprises) et dit « NE TIENT PAS : *pièce* — empreinte 400 × 20 mm > plateau 256 × 256 mm ». Un plan qui ne tient pas ne peut pas être découpé.
- **Plusieurs plaques** : si tout ne tient pas sur un plateau, Bambu Studio répartit sur autant de plaques que nécessaire. Chaque plaque est **une impression à part** : elle se valide, se dépose et se confirme séparément (un accord humain par plaque ; l'empreinte et le numéro de plaque sont ceux de la confirmation).
- « Ne mélange pas des réglages différents sur une même plaque » → l'option `group_by_settings` regroupe les pièces par jeu de réglages.
- Sans Bambu Studio, le rangement est **estimé** par rectangles englobants (prudent, annoncé comme tel) ; la disposition exacte se fait alors à la découpe.
- L'aperçu du plateau (`capture_preview`, mode `plate`) marche avant découpe (depuis le plan) et après (depuis le 3MF découpé : positions et orientations réelles). Mode `layers` : une couche du G-code.
- Limites : la pièce est posée comme dans sa version du catalogue (orientez-la avant avec « pose-la à plat ») ; la jupe, la bordure et le temps **par pièce** ne sont pas dessinés (le temps est celui de la plaque).

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
| **Gabarits** | Catalogue de pièces paramétriques (vignettes, schéma de paramètres, jeux et compensations calculés, calibration appliquée), création d'un modèle ou d'une nouvelle version, enregistrement de variantes. Onglet **Calibration** : génération des pièces de test, saisie des mesures, calibration active. |
| **Agent Windows** (pastille « Windows ») | État des agents, inventaire des fichiers encore « Windows seulement », **demande de copie** vers le serveur (taille et empreinte vérifiées), suivi des travaux. L'appairage se fait sur `/agents/pair` (navigateur enregistré). L'agent ne peut ni lancer ni arrêter une impression. |

Ce que l'interface ne fait pas : approuver un lancement ou un arrêt (pages serveur seulement), saisir le code d'accès de l'imprimante (page `/setup`), renommer ou déplacer physiquement des fichiers (le catalogue seul est modifié).



## 8. Caméra, détection d'échec et notifications (chantier D)

**Caméra (P1, P1S, A1).** « Montre-moi le plateau » : `get_camera_snapshot` renvoie une image que Claude voit (flux JPEG de l'imprimante, port 6000, avec le code d'accès déjà enregistré). Le X1, le P2 et le H2 exposent du RTSP : non pris en charge (l'outil le dit). Sur la P1S, activer « LAN liveview » dans les réglages réseau de l'écran. Sur la page Impression, la carte de l'imprimante a un bouton Instantané et un flux en direct (relayé par le serveur : une seule connexion à la caméra, quel que soit le nombre de spectateurs). **Un instantané à la demande n'est jamais conservé** : l'image est renvoyée puis oubliée ; seules les photos de fin d'impression, d'alerte et de confirmation sont enregistrées. Les instantanés et les ouvertures de flux sont limités en débit par imprimante (erreur 429 au-delà), les images de plus de 4096 x 4096 sont refusées, et les photos d'alerte plus vieilles que `alert_photo_retention_days` (90 jours par défaut, 0 = illimité, réglable avec `configure_failure_detection`) sont supprimées ; les photos de fin restent.

**Photo sur la page de confirmation.** À l'ouverture de `/confirm/{id}`, la page montre une photo du plateau horodatée. Elle aide à vérifier qu'il est vide ; elle ne remplace pas votre case « plateau vide ». Caméra indisponible : la page le dit, sans photo périmée. Cette photo sert aussi d'image de référence à la détection.

**Détection d'échec.** Pendant une impression, un instantané est analysé toutes les 60 s (réglable) par un détecteur local, sans modèle appris : masse de filaments hors de la colonne de la pièce (spaghetti), empreinte apprise qui se vide (pièce décollée), couche qui n'avance plus, erreurs HMS, mouvement brutal de la scène. Score de 0 à 1, chaque signal est expliqué. Au-dessus du seuil (0,6 par défaut) sur 2 analyses consécutives : **alerte** (photo + lien vers la page d'arrêt). **Aucun arrêt automatique** : l'arrêt reste la page humaine `/stop`. `analyze_print_frame` renvoie la dernière image à Claude pour qu'il la juge lui-même ; `configure_failure_detection` règle activation, intervalle, seuil, sensibilité, persistance. Une photo de fin d'impression est rattachée à l'impression (`GET /api/print-jobs/{id}/photos`).

**Notifications.** Types de canaux : `ntfy`, `webhook` (JSON signé HMAC-SHA256, en-têtes `X-Bambu-Timestamp` et `X-Bambu-Signature: sha256=hex(HMAC(clé, horodatage + "." + corps))`) et `email` (SMTP, STARTTLS ou TLS implicite, jamais en clair). **Vous** créez les canaux et réglez leur destination (hôte, port et utilisateur SMTP, destinataires, URL) ainsi que leurs secrets (URL du sujet ntfy, URL et clé du webhook, mot de passe SMTP) sur la page `/setup/notifications` (navigateur enregistré), ou par variable d'environnement `BAMBU_NOTIFY_<CANAL>_<CHAMP>` (ou `…_FILE` vers un fichier) ; Claude ne les voit jamais. `configure_notifications` ne sert qu'à **lister** les canaux et à **activer ou désactiver** un canal existant et choisir ses évènements : il ne peut ni créer, ni supprimer, ni rediriger un canal. Changer l'hôte, le port ou l'utilisateur SMTP efface le mot de passe enregistré. Par défaut, les adresses locales, privées ou de lien local et les ports SMTP hors 25, 465, 587 et 2525 sont refusés ; `BAMBU_NOTIFY_ALLOW_PRIVATE=1` (côté serveur) les permet pour un ntfy ou un SMTP de votre réseau. Évènements : `print_completed`, `print_failed`, `detection_alert`, `start_unknown`, `printer_offline`, `queue_event` (file d'attente). `test_notification` vérifie un canal. Le contenu d'une notification ne contient jamais de secret.

## 9. Envoyer un fichier à l'Atelier (upload) et importer depuis un lien

Le service ne lit pas le disque de l'utilisateur. Un fichier que **Claude vient de générer** (script OpenSCAD/CadQuery, maillage, 3MF modifié) entre au catalogue avec l'outil `upload_file` ; un fichier **sur votre PC** se dépose par glisser-déposer dans la Bibliothèque.

**Exemples à dire à Claude**
- « Génère un support de caméra en STL puis envoie-le dans la collection *Photo* avec l'étiquette *prototype*. » Claude calcule la taille et le sha256, découpe en morceaux base64 de 75 000 octets (≤ 100 000 caractères) et enchaîne les appels `upload_file` ; le dernier répond `status: complete` avec le modèle créé.
- « Renvoie la version corrigée de `support.stl` » : même nom dans la même collection = **nouvelle version** du modèle (l'ancienne reste disponible). Même contenu qu'un fichier déjà présent = **doublon**, rien n'est ajouté.
- « Qu'est-ce que je peux envoyer ? » : `get_capabilities` (section `upload`) donne les dossiers d'import du serveur, la taille maximale (globale et par format), la taille de morceau et les domaines autorisés pour les liens.
- « Importe ce modèle : https://www.printables.com/model/… » : outil `import_from_url`. La provenance (site, URL, auteur) et la licence, quand la page les expose, sont enregistrées. Si le site exige une connexion, Claude reçoit la marche à suivre : télécharger le fichier dans votre navigateur connecté, puis le déposer (ou `upload_file`).

**Ce que le service contrôle** : nom sans chemin (jamais de dossier fourni par le client), taille annoncée et maximale par format, signature (STL binaire/ASCII, OBJ texte, 3MF = zip avec `3D/3dmodel.model`, STEP `ISO-10303-21`), empreinte sha256 **recalculée** (un envoi dont l'empreinte diffère est refusé et rien n'est stocké), puis stockage dans le magasin de blobs. Reprise : `get_upload_status` donne `next_offset` ; un morceau rejoué est ignoré. Une session inactive expire (`BAMBU_UPLOAD_TTL`, 1 h par défaut), une session ne dépasse jamais sa durée de vie absolue (`BAMBU_UPLOAD_MAX_LIFETIME`, 6 h par défaut) et le total des octets annoncés par les envois en cours est plafonné (`BAMBU_UPLOAD_INFLIGHT_MB`, 2 048 Mio par défaut) ; les octets temporaires sont purgés ; `cancel_upload` l'abandonne.

**Glisser-déposer** : dans la Bibliothèque, déposez des fichiers n'importe où sur la page ou cliquez « Envoyer des fichiers » : collection, étiquettes et licence optionnelles, barre de progression par fichier, résultat (ajouté, nouvelle version, doublon) avec lien vers la fiche. Le même dialogue propose « Depuis un lien ».

**Limites** : 512 Mo par fichier par défaut (STEP : 256 Mo), 8 envois simultanés. L'import par lien n'accepte que HTTPS et les domaines autorisés (par défaut `makerworld.com`, `bblmw.com` — CDN de fichiers de MakerWorld —, `printables.com`), sans redirection hors liste ni adresse privée. MakerWorld et Printables exigent presque toujours une connexion pour télécharger : le téléchargement manuel puis l'envoi reste le cas courant. Les domaines CDN par défaut sont ceux connus des deux sites et n'ont pas pu être vérifiés ici avec un vrai lien : ajuster `BAMBU_URL_IMPORT_ALLOW` si un téléchargement réel est refusé pour son domaine.

---

## 10. Bobines, AMS, couleurs et file d'attente

Outils : `list_spools`, `update_spool`, `get_ams_state`, `check_filament_for_print`, `set_filament_margin`, `plan_colors`, `queue_print`, `list_print_queue`, `reorder_queue`, `remove_from_queue`. Écran web : **Bobines et file** (onglets Bobines et AMS, Multicolore, File d'attente).

### Bobines et AMS

> **Vous** : J'ai chargé une bobine de PETG rouge Prusament, 850 g, à 30 €/kg, dans l'emplacement 3 de l'AMS.
>
> **Claude** (`update_spool`, puis `get_ams_state`) : Inscrite en `ams1:3`. L'AMS lit bien du PETG #D01010 ; il ne donne pas de pourcentage restant pour cette bobine (pas d'étiquette Bambu), je suis donc ton poids saisi et le décompte après chaque impression.

- Une bobine : matière, marque, couleur, diamètre, poids net et restant, prix/kg, dates d'ouverture et de séchage, emplacement.
- **Le restant se met à jour seul** : lecture de l'AMS (bobines Bambu à étiquette RFID) et décompte de la masse du fichier à la fin de chaque impression (au prorata de l'avancement si elle est interrompue, signalé « estimation »). **Ce que vous saisissez n'est jamais écrasé** par l'AMS (le champ `remaining_source` dit d'où vient la valeur : `human`, `ams`, `consumption`, `default`). Une divergence est signalée, pas corrigée.
- `get_ams_state` n'envoie aucune commande à l'imprimante : il lit son rapport MQTT habituel. Il rapproche chaque emplacement de l'inventaire : bobine inscrite, **lecture différente** (vous avez rechargé autre chose), **bobine proposée** (même matière, couleur proche : à confirmer par `update_spool`), emplacement non inscrit. Une bobine rangée dont l'étiquette RFID est celle d'un bac y est chargée d'office si l'emplacement est libre.
- **Alerte avant l'envoi** : `check_filament_for_print` (et la validation du fichier, donc avant le dépôt) compare la masse que l'impression va extruder par filament, **purge comprise**, au restant de chaque bobine, avec une marge réglable (`set_filament_margin`, 10 % + 5 g par défaut). Verdict par filament : `ok`, `marginal`, `insufficient` (il manque X g, bobines de remplacement proposées), `unknown`. L'alerte s'affiche aussi sur la page de confirmation ; elle ne bloque rien (votre inventaire peut être faux) : la décision reste la vôtre.
- Le prix/kg d'une bobine est lisible par le calcul de coût : `GET /api/filament/price?printer_id=&slot=` ou `GET /api/spools/{id}/price` (côté Go : `filament.Service.PriceForSlot` / `PriceForSpool`, et `SetCostFunc` pour brancher le calcul de coût du chantier C). La consommation décomptée garde le prix/kg au moment de l'impression (`GET /api/spools/usage?print_job_id=`).

### Multicolore

> **Vous** : Le socle en noir, le capot en blanc.
>
> **Claude** (`plan_colors`) : Filament 1 (socle) → `ams1:2` PLA noir, filament 2 (capot) → `ams1:1` PLA blanc, ΔE 0 pour les deux. `ams_mapping` = [1, 0, −1, −1, −1]. **Purge mesurée dans le G-code : 20,9 g jetés sur 26,2 g (80 %)**, 50 changements de filament, coût de la matière jetée 0,53 €. Les deux pièces changent de couleur à chaque couche : si tu peux les regrouper par couleur, la purge baisse beaucoup.

- `plan_colors(print_job_id, colors?, fixed?)` : `colors` = couleur voulue par filament (pièce → couleur) ; `fixed` impose certains emplacements. Il renvoie les `assignments` à passer tels quels à `upload_print_artifact`, l'`ams_mapping`, les filaments sans bobine convenable (« charger du PLA vert »), la suffisance des bobines proposées et la purge.
- **Purge réelle** : lue dans le G-code produit par Bambu Studio (tour de purge et purge de buse, par filament), recoupée avec l'en-tête du fichier et la matrice de volumes de purge du profil. Le coût utilise le prix/kg des bobines proposées (ou le calcul de coût du chantier C quand il est branché).
- **La confirmation lie l'affectation matière** : la page `/confirm` montre « filament 1 → ams1:2 ». Si l'affectation change après l'accord, le lancement est refusé et il faut une nouvelle confirmation (test `TestConfirmationBindsMaterialAssignment`).
- Un 3MF multi-pièces doit venir de Bambu Studio (un 3MF purement synthétique fait planter Studio, voir le test `TestReal3MFSyntheticCrashIsNamed`) ; chaque pièce se rattache à un filament par la clé `extruder` de l'objet.

### File d'attente

> **Vous** : Mets les trois pièces en file.
>
> **Claude** (`queue_print` ×3) : En file : 1, 2, 3. La 1 est envoyée sur l'imprimante ; quand elle sera prête je te donne le lien de confirmation. **Aucune ne démarre sans ton accord, y compris la 2 et la 3 après la fin de la 1.**

- La file **prépare** : elle attend la découpe, dépose le fichier de l'élément de tête quand l'imprimante est libre, demande la confirmation et publie « Impression suivante prête : vider le plateau, puis ouvrir le lien ». Elle **ne lance jamais** : chaque impression exige votre clic sur sa page de confirmation (voir `docs/SECURITY.md` §9).
- `reorder_queue` place des éléments en tête ; `remove_from_queue` retire un élément (rien n'est supprimé ni arrêté, et un élément lancé ne se retire pas : `cancel_print`). Un lien de confirmation expire au bout de 10 minutes : le bouton « Nouveau lien » (ou `request_print_confirmation`) en redonne un.
- Événements internes publiés pour les notifications (chantier D) : `queue_next_ready`, `queue_item_failed`, `spool_low` (`filament.Service.Subscribe`).

### À vérifier sur la vraie P1S

Le simulateur et les tests suivent le format décrit par la communauté (`docs/research/bambu-lan-protocol.md` §3) ; ces champs n'ont **pas** été vus sur le matériel :

- `print.ams.ams[].tray[]` : `tray_type`, `tray_color` (RRGGBBAA), `remain` (le pourcentage est-il fourni, et pour quelles bobines ? `-1` si inconnu), `tray_uuid` (zéros = pas d'étiquette), `tray_weight`, `tray_sub_brands`, `tray_diameter` ;
- `print.ams.tray_now` ou `print.tray_now` : valeur `"255"` (aucun), `"254"` (externe), sinon `unité×4 + bac` ;
- `print.vt_tray` pour la bobine externe ;
- `ams.ams[].humidity` (indice 1 à 5) ;
- que les rapports partiels de la P1 renvoient l'objet `ams.ams` complet (le fusionneur remplace les tableaux) ;
- un AMS lite (A1) ou un AMS HT (identifiants ≥ 128) n'est pas géré (ignoré sans erreur).

### Limites

- Pas de fusion automatique de deux bobines identiques : une bobine physique = une ligne d'inventaire.
- La purge est mesurée sur la plaque demandée ; un changement de réglage (ordre des couleurs, tour de purge, volumes de purge) demande de re-découper.
- La lecture du G-code retrouve la longueur extrudée annoncée par l'en-tête à quelques pourcents près (écart de 3,8 % sur le cas de test à deux cubes) ; l'écart est affiché (`deviation_percent`).
- Aucune imprimante réelle n'a été contactée pour ces fonctions.

---

## 11. Coût, journal des impressions, tableau de bord, finition

Tout est disponible dans Claude (outils) et dans l'interface web (écran **Tableau de bord**). Aucun de ces outils ne lance ni n'arrête d'impression.

### Combien coûte cette impression ?

> « Combien va coûter l'impression 3 de la plaque 1 ? » → `estimate_cost` avec `print_job_id`.
> « Et pour 180 g de PETG pendant 5 h sur la X1C ? » → `estimate_cost` avec `printer_model`, `duration_minutes`, `filaments`.

Le coût est la somme de trois lignes :

- **Filament** : masse de chaque emplacement × prix du kg. Le prix est, dans l'ordre : celui que vous donnez pour cet appel, le **prix réel de la bobine chargée** dans l'emplacement (inventaire des bobines, prix au kg saisi avec la bobine ; la réponse l'indique : « bobine de l'inventaire »), celui du réglage de la bobine (`spool_per_kg`, clé = libellé de la bobine), celui de la famille de matière (PLA, PETG, ABS, ASA, TPU, PC, PA, PA-CF…), sinon une valeur par défaut **indicative**.
- **Électricité** : (puissance de chauffe × durée de chauffe + puissance d'impression × durée restante) × prix du kWh. La puissance dépend du modèle d'imprimante (P1S, P1P, X1C, X1E, A1, A1 mini, H2D, H2S, P2S) et de la matière (facteur plus fort pour ABS, ASA, PC, PA : plateau et enceinte plus chauds ; chauffe plus longue). **Ces puissances sont des ordres de grandeur non mesurés** : remplacez-les par vos relevés de prise mesurante (`power_overrides`).
- **Usure** : heures machine × amortissement de la machine + usure de la buse (réglages, **0 par défaut**).

Pour une impression terminée, la réponse contient aussi le **coût réel estimé** : durée observée par le service (début et fin de l'impression), masse déclarée par vous (pesée) ou, à défaut, masse réellement décomptée dans l'inventaire des bobines, sinon masse prévue (au prorata de l'avancement si l'impression a été interrompue).

Réglez les prix une fois :

> « Mon kWh est à 0,21 € et mon PLA à 17 €/kg, recalcule l'historique. » → `set_cost_settings` (`apply_to_history`).

**Toujours une estimation.** Chaque réponse porte le mot ESTIMATION et la liste de ses hypothèses (prix par défaut, puissances indicatives, durée du slicer supposée chauffe comprise, purges et ratés non comptés, pas de temps humain ni de consommables). Les prix encore à leur valeur par défaut sont signalés (`defaulted_prices`).

**Par projet** (collection de la bibliothèque) : `get_project_costs` (ou le tableau « Par projet » du tableau de bord) additionne le coût réel estimé des impressions terminées et l'estimation des impressions préparées pas encore terminées.

### Le journal des impressions

Chaque impression terminée, échouée ou annulée a une **fiche** (créée à la demande, la première fois que vous ouvrez le journal ou que Claude le lit). Elle garde les réglages effectifs (profil, buse, surcharges, orientation), la durée et la masse estimées et réelles, le coût, des photos (les vôtres, et celles de la caméra rattachées à l'impression : photo de fin et alertes, lues dans la caméra sans être copiées dans la fiche ; une photo d'alerte purgée disparaît aussi du journal), une note de 1 à 5, un commentaire et des défauts à cocher (gauchissement, fils, couches visibles, décollement de couches, adhérence, dimensions, patte d'éléphant, sous/sur-extrusion, boutons, supports, surplombs, décalage de couches, surface, autre).

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

## 12. Gabarits paramétriques (bibliothèque extensible)

La bibliothèque propose des **pièces prêtes à paramétrer** : chacune a un schéma de paramètres typé (unité, bornes, défaut), une description, une version, une vignette et les calculs d'ingénierie associés. Les paramètres « auto » (jeux d'ajustement, diamètres de trous, trous d'inserts) sont **calculés par le moteur d'ingénierie**, avec la **calibration de votre machine** quand elle existe (sinon valeurs génériques signalées comme telles).

| Gabarit | Ce qu'il produit | Calculs associés |
|---|---|---|
| `clamp`, `bracket`, `box`, `spacer`, `hook` | collier, équerre, boîte à couvercle, entretoise, crochet (gabarits d'origine) | jeux, trous de vis |
| `hinge_print_in_place` | charnière dont l'axe s'imprime dans les nœuds de l'autre lame (2 pièces mobiles, aucun assemblage) | jeu radial/axial (jeu glissant mesuré si calibré, sinon 0,75 × jeu « libre ») |
| `hinge_pin` | charnière à nœuds alternés et axe séparé (tête) | jeu diamétral du perçage (ajustement glissant) |
| `snap_clip` | clip cantilever à rampe d'insertion + récepteur à fenêtre | **SnapFit** : déformation, déformation admissible du matériau, flèche max, force d'insertion |
| `enclosure` | boîtier à angles arrondis, couvercle **vissé** (4 plots d'inserts à chaud, fraisurage) ou **clipsé** (ergots) | trou/profondeur/plot d'insert pour M2…M5 (`ScrewBoss`), jeu de couvercle, longueur de vis |
| `wall_mount` | plaque murale à vis fraisées ou **trous de serrure** | diamètre de vis compensé |
| `spur_gear`, `gear_pair` | engrenage droit en développante (module, dents, angle de pression, alésage, clavette, moyeu) ; paire à **entraxe calculé** m·(z₁+z₂)/2 avec jeu de flanc | alésage compensé, rapport de conduite, avertissement de sous-taillage |
| `thread` | vis hexagonale / écrou métriques M3…M12 imprimables (hélice exacte, jeu sur le filet) ou plot pour insert | jeu de l'écrou, dimensions d'insert |
| `washer`, `handle`, `hook_reinforced`, `mounting_plate` | rondelle chanfreinée, poignée en U, crochet à congé et nervures, plaque perforée (grille ou quinconce) | perçages compensés |

**Exemples à dire à Claude**
- « Quels gabarits as-tu pour faire des charnières ? » → `list_part_templates` (filtre `category: mécanisme`), puis `describe_part_template` pour les paramètres et leurs bornes.
- « Fais-moi une charnière imprimée en place de 50 mm avec 7 nœuds, pour mon P1S en PLA. » → `create_from_template` avec `printer`, `material: PLA`, `nozzle_diameter: 0.4`. La réponse indique le jeu utilisé et sa provenance (« calibré le … » ou valeurs génériques), les hypothèses (axe fusionné avec la lame A, pas de bordure reliant les lames...) et vérifie que chaque pièce est étanche.
- « Un boîtier 80 × 50 × 30 avec couvercle vissé, vis M4, en PETG. » → les trous d'inserts (Ø5,6 × 9,6 mm), plots et longueur de vis viennent de `ScrewBoss`.
- « Un clip qui tienne 200 cycles en PETG, retour de 1,5 mm. » → `snap_clip` ; le résultat contient la déformation calculée et un avertissement si elle dépasse l'admissible du matériau.
- « Une paire d'engrenages 12/36 dents module 1,5, alésage 5. » → entraxe, rapport de réduction, rapport de conduite.
- « Garde ce collier comme "Collier tube 32". » → `save_as_template` : variante nommée, réutilisable ensuite comme `template` (seuls les paramètres demandés sont figés ; les jeux restent recalculés avec la calibration du moment, `frozen: true` fige aussi les valeurs calculées).
- « Refais le support avec 2 mm d'épaisseur de plus » → `create_from_template` avec `model_id` et `base_version_id` : **nouvelle version** du même modèle (refus si la version de départ est périmée).

Chaque appel renvoie `auto` (paramètres calculés : valeur nominale, valeur utilisée, niveau de confiance, origine), `calcs` (calculs détaillés), `calibration` (appliquée ou non) et `warnings`. Les gabarits sont **versionnés** : la version du gabarit et les paramètres exacts sont conservés avec chaque version de modèle. Interface web : écran **Gabarits** (formulaire généré depuis le schéma, vignettes, résultat détaillé, enregistrement en variante).

## 13. Calibration d'une machine (pièces de test et mesures)

Les valeurs génériques (jeu glissant 0,25 mm, trou −0,15 mm...) sont des ordres de grandeur. Pour un couple **imprimante + buse + matériau (+ bobine)** précis, on imprime des pièces de calibration et on **mesure** :

| Pièce (`kinds`) | À faire | Clé de `record_calibration` |
|---|---|---|
| `fit_comb` | encoches de 10 mm + 0,1 → 0,6 mm (1 à 6 points) et une languette : trouver le plus petit jeu où elle **coulisse** (et celui où elle entre en force) | `fit_slides_gap_mm`, `fit_snug_gap_mm` |
| `hole_shaft` | plaque à trous verticaux, bloc à trous horizontaux et arbres de Ø3 à Ø10 : mesurer au pied à coulisse | `holes[] {nominal, measured, axis}`, `shafts[]` |
| `shrink_cube` | cube creux de 50 mm : mesurer X, Y, Z (à mi-hauteur, hors pied d'éléphant) | `cube_mm: [X, Y, Z]` |
| `bridge_overhang` | ponts de 10 à 50 mm et surplombs de 30° à 70° | `bridge_max_span_mm`, `overhang_max_deg` |
| `temperature` | une petite pièce par température (défaut : 5 valeurs autour de la plage du matériau), ou une tour (`temperature_mode: tower`) avec changements de température à la couche | `best_temp_c` |

**Exemples à dire à Claude**
- « Génère les pièces de calibration pour mon P1S, buse 0,4, PETG HF noir. » → `generate_calibration_set` : les pièces entrent au catalogue (étiquette *calibration*) ; avec `base` (preset machine/process/filament) Claude crée aussi une **révision de profil découpable par température**. Réponse : quoi mesurer pour chaque pièce et un squelette de mesures.
- « Mes mesures : ça coulisse à 0,3 ; les trous de 4/6/8 mesurent 3,86/5,85/7,84 ; cube 49,8 × 49,9 × 50,0 ; meilleure température 240. » → `record_calibration` : jeu effectif, **droite de compensation de trou** (décalage = a + b·d), facteurs de retrait ×1,004 / ×1,000, température. Les mesures implausibles (unité, trou bouché, cube à ±10 %) sont refusées.
- « Quelle calibration utilises-tu pour mon P1S en PETG ? » → `get_calibration` : valeurs, provenance (« calibré le 2026-11-07 (p1s, buse 0.4, PETG, …) »), clés utilisées, historique.

**Priorité** : les corrections mesurées priment sur les valeurs génériques dans `create_from_template` (jeux, trous) et dans les calculs d'ingénierie (`FitClearanceCal`, `HoleCompensationCal`...), avec leur provenance. Résolution du plus général au plus spécifique, champ par champ : (buse, matériau) → + bobine → + imprimante → + imprimante et bobine. Une autre buse, un autre matériau : aucune calibration (valeurs génériques). Les mesures sont conservées dans un journal immuable ; refaire la calibration met à jour la valeur active. Interface web : onglet **Calibration** de l'écran Gabarits.

**Limites** : le facteur de retrait est calculé et affiché mais pas appliqué automatiquement aux gabarits (le trou et l'arbre rétrécissent ensemble ; une mise à l'échelle globale fausserait les compensations) — l'appliquer au besoin avec `modify_geometry` (échelle). La tour de température demande d'insérer les changements `M104` à la main dans Bambu Studio (hauteurs fournies) ; la série de pièces est plus simple et se découpe avec les profils créés. Les filetages M3/M4 sont limités par la résolution FDM (avertissement) ; la poignée et le support mural demandent un essai de charge réel.
