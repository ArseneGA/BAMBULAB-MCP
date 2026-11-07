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
| **Agent Windows** (pastille « Windows ») | État des agents, inventaire des fichiers encore « Windows seulement », **demande de copie** vers le serveur (taille et empreinte vérifiées), suivi des travaux. L'appairage se fait sur `/agents/pair` (navigateur enregistré). L'agent ne peut ni lancer ni arrêter une impression. |

Ce que l'interface ne fait pas : approuver un lancement ou un arrêt (pages serveur seulement), saisir le code d'accès de l'imprimante (page `/setup`), joindre des photos à un retour d'expérience, renommer ou déplacer physiquement des fichiers (le catalogue seul est modifié).

## 11. Caméra, détection d'échec et notifications (chantier D)

**Caméra (P1, P1S, A1).** « Montre-moi le plateau » : `get_camera_snapshot` renvoie une image que Claude voit (flux JPEG de l'imprimante, port 6000, avec le code d'accès déjà enregistré). Le X1, le P2 et le H2 exposent du RTSP : non pris en charge (l'outil le dit). Sur la P1S, activer « LAN liveview » dans les réglages réseau de l'écran. Sur la page Impression, la carte de l'imprimante a un bouton Instantané et un flux en direct (relayé par le serveur : une seule connexion à la caméra, quel que soit le nombre de spectateurs).

**Photo sur la page de confirmation.** À l'ouverture de `/confirm/{id}`, la page montre une photo du plateau horodatée. Elle aide à vérifier qu'il est vide ; elle ne remplace pas votre case « plateau vide ». Caméra indisponible : la page le dit, sans photo périmée. Cette photo sert aussi d'image de référence à la détection.

**Détection d'échec.** Pendant une impression, un instantané est analysé toutes les 60 s (réglable) par un détecteur local, sans modèle appris : masse de filaments hors de la colonne de la pièce (spaghetti), empreinte apprise qui se vide (pièce décollée), couche qui n'avance plus, erreurs HMS, mouvement brutal de la scène. Score de 0 à 1, chaque signal est expliqué. Au-dessus du seuil (0,6 par défaut) sur 2 analyses consécutives : **alerte** (photo + lien vers la page d'arrêt). **Aucun arrêt automatique** : l'arrêt reste la page humaine `/stop`. `analyze_print_frame` renvoie la dernière image à Claude pour qu'il la juge lui-même ; `configure_failure_detection` règle activation, intervalle, seuil, sensibilité, persistance. Une photo de fin d'impression est rattachée à l'impression (`GET /api/print-jobs/{id}/photos`).

**Notifications.** `configure_notifications` crée des canaux **sans secret** : `ntfy`, `webhook` (JSON signé HMAC-SHA256, en-têtes `X-Bambu-Timestamp` et `X-Bambu-Signature: sha256=hex(HMAC(clé, horodatage + "." + corps))`) et `email` (SMTP, STARTTLS ou TLS implicite, jamais en clair). Les secrets (URL du sujet ntfy, URL et clé du webhook, mot de passe SMTP) se saisissent par **vous** sur `/setup/notifications` (navigateur enregistré) ou par variable d'environnement `BAMBU_NOTIFY_<CANAL>_<CHAMP>` (ou `…_FILE` vers un fichier) ; Claude ne les voit jamais. Évènements : `print_completed`, `print_failed`, `detection_alert`, `start_unknown`, `printer_offline`, `queue_event` (file d'attente). `test_notification` vérifie un canal. Le contenu d'une notification ne contient jamais de secret.
