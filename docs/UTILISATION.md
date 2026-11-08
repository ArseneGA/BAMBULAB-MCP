# Utiliser l'Atelier avec Claude

Vous parlez normalement ; Claude choisit les outils. Les noms d'outils entre parenthèses ne sont là que pour comprendre ce qui se passe (vous n'avez pas à les connaître). Les réponses ci-dessous sont **illustratives** : elles montrent la forme et les garde-fous, pas des sorties enregistrées.

Trois règles à garder en tête :

1. **Rien ne part sur l'imprimante sans votre accord**, donné soit par un clic sur une page web depuis un navigateur enregistré, soit dans la vue de Claude avec votre **code de lancement personnel** (§ 4 bis). Claude prépare, dépose, demande ; il ne peut pas approuver.
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
> **Claude** (`request_print_confirmation`) : La carte de confirmation est affichée ci-dessus (fichier, imprimante, empreinte, plateau, matière, températures, photo du plateau) : cochez « plateau vide » et saisissez votre code de lancement. Je ne peux pas le faire à votre place. *(Sans code défini, la carte vous invite à le définir sur `/setup/launch-code` et propose le lien du navigateur `…/confirm/…`.)*
>
> *(vous cliquez « Lancer l'impression » dans la carte, ou sur la page du navigateur : **le serveur lance l'impression tout seul**, vous n'avez rien à dire à Claude)*
>
> **Claude** (`get_print_status`) : Lancé : préparation en cours (chauffe, nivellement), puis impression. Couche 12/240, 5 %, 1 h 07 restantes.

Si ça se passe mal :

- **« Liaison perdue après la commande »** (`start_unknown`) : Claude **ne relance pas**. Il lit l'état réel de l'imprimante (`reconcile_print`) et vous dit si l'impression a démarré ou non.
- **Arrêter** : au bouton de l'imprimante, toujours. Via Claude : « Arrête l'impression » → `cancel_print` crée une demande d'arrêt et vous donne un lien `…/stop/…` à valider dans le navigateur enregistré, ou une vue d'arrêt où vous saisissez votre code de lancement (§ 4 bis).
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

## 4 bis. Lancer et arrêter sans quitter Claude (code de lancement personnel)

Dans Claude Desktop, la confirmation s'affiche **dans la conversation** : résumé (fichier, imprimante, empreinte, plateau, matières), températures lues dans le G-code et « G-code de démarrage : profil officiel ✓ », photo du plateau si la caméra est configurée, case « le plateau est vide et dégagé », champ **code de lancement** et bouton « Lancer l'impression ». L'arrêt (« Arrête l'impression ») affiche la même chose avec un bouton « Arrêter l'impression ».

1. **Définir son code une fois** : dans un navigateur enregistré, ouvrez `http://…:7878/setup/launch-code` et choisissez un code de **6 à 8 chiffres** (pas de suite ni de chiffres identiques). Il est stocké haché (argon2id), jamais affiché, jamais renvoyé, jamais journalisé. Changez-le ou révoquez-le sur la même page.
2. **Lancer** : dans la vue, cochez « plateau vide », tapez votre code, cliquez « Lancer l'impression ». L'accord vaut pour **cette impression** et **déclenche directement le démarrage** côté serveur (une seule commande, jamais renvoyée) : la carte et la page `/confirm` affichent « Impression lancée » (ou l'erreur, par exemple imprimante pas prête : rien n'est parti et l'accord reste valable **2 minutes à partir de votre clic**, Claude peut alors relancer avec `start_print`). Vous n'avez **rien à dire à Claude** : il suit l'état avec `get_print_status`. Si Claude appelle quand même `start_print`, il constate « déjà lancé » sans rien renvoyer.
3. **Trois mauvais codes verrouillent tout** (même le bon code est alors refusé, pour le lancement comme pour l'arrêt). Deux essais doivent être espacés de quelques secondes. Déverrouillage : bouton « Déverrouiller » de la page `/setup/launch-code`, ou `bambud launch-code --unlock`.
4. **Code oublié** : `bambud launch-code --reset` supprime le code (puis on en définit un autre sur la page). `bambud launch-code` seul affiche l'état (défini, verrouillé).
5. **Sans code défini** (ou verrouillé), la vue affiche le **lien du navigateur** (`/confirm/…`, `/stop/…`), comme avant : il reste toujours disponible, y compris quand le code fonctionne.

> **Ne tapez JAMAIS ce code dans la conversation avec Claude.** Il se saisit uniquement dans le champ de la vue. Claude a pour consigne de ne jamais vous le demander ; s'il le fait quand même, ne répondez pas, et si vous l'avez écrit dans la conversation, changez-le tout de suite sur `/setup/launch-code`. Le serveur ne peut pas empêcher cette erreur (voir `docs/SECURITY.md` § 12).

Claude Code (terminal) n'affiche pas les vues : utilisez le lien du navigateur.

**Ce que la confirmation montre et approuve (mise à jour 2026-11-08)**

- **Mise à niveau du plateau** : « oui (automatique au démarrage) » par défaut ; `request_print_confirmation(bed_leveling=false)` la désactive, et la page comme la carte l'affichent (« non (désactivée pour cette impression) »). Ce réglage fait **partie de la liaison que vous approuvez** : le changer impose une nouvelle confirmation. La commande `project_file` envoie `bed_leveling` **et** `bed_levelling` (les deux orthographes), les autres options (calibration du débit, vibrations) gardent leur valeur par défaut.
- **Risque de décollement** : si la pièce est haute pour sa surface de contact au plateau (hauteur / √aire de contact ≥ 4 modéré, ≥ 6 élevé ; îlots de contact de moins de 100 mm² sous une pièce de plus de 40 mm) **et que le profil tranché n'a pas de vraie bordure** (outer/inner, ≥ 3 mm), un avertissement **non bloquant** apparaît sur `/confirm`, dans la carte et dans le texte de Claude : ajoutez `brim_type = outer_only` et `brim_width = 5 à 8` aux surcharges de découpe, puis redécoupez. Il est aussi donné par `analyze_geometry`, à la découpe et par `validate_print_artifact`. Heuristique tirée de la géométrie seule : elle ne remplace pas votre jugement (matière, chambre à 5 °C en PETG…).
- **Risque de décollement, îlots minuscules** : un îlot de contact de moins de **50 mm²** sous une pièce de plus de 40 mm est à lui seul un risque **élevé** (cas réel : 4 plots de ≈ 3 mm de diamètre sous une pièce de 204 mm). L'alerte n'est levée que par une **bordure ≥ 3 mm** (`outer_only`/`inner_only`/`outer_and_inner`) **ou un radeau** (`raft_layers` ≥ 1) réellement tranchés ; `auto_brim` ne suffit pas.
- **Surplombs sans supports** : à la validation (`validate_print_artifact`), les surplombs de la pièce (seuil d'angle du profil, `support_threshold_angle`) sont croisés avec le réglage de supports **effectif** de l'archive. Au-delà de **200 mm² cumulés ou d'une zone de 100 mm²** sans supports, un avertissement **fort** (non bloquant) s'affiche sur `/confirm` (« Surplombs sans supports »), dans la liaison (`overhang_warnings`) et dans le texte de Claude : activez `enable_support`, réorientez, ou acceptez-le en connaissance de cause. `compare_variants` ajoute la colonne **`unsupported_overhang_mm2`** (surface en surplomb non supportée) et, par variante, un champ `warnings` : « ventilation réduite : surplombs dégradés » (ventilateurs en baisse), température de buse relevée.
- **Photo du plateau** : l'âge est affiché (« il y a 12 s »). Un rechargement de `/confirm` reprend une photo si la dernière date de plus de 15 s ; le bouton **« Reprendre la photo »** (formulaire POST protégé comme le reste de la page : navigateur enregistré, nonce) en prend une nouvelle tout de suite, et la carte a le même bouton.
- **Heures** : toutes les heures affichées aux humains (pages, carte, texte de Claude) sont dans votre fuseau, réglé par `BAMBU_TIMEZONE` (par exemple `Europe/Paris`, voir `docs/DEPLOY-LINUX.md`), avec la date au format français. Les champs `expires_at` des réponses restent en RFC 3339 ; `expires_at_local` en donne la version lisible.

**Si la carte n'apparaît pas dans Claude Desktop (vous voyez un tableau « Requête / Réponse » avec `binding`, `safety`…)**

Ce tableau est l'affichage ordinaire d'un appel d'outil : il prouve seulement que l'hôte n'a **pas monté la vue**. Ce qui est **vérifié** (code, tests, spécification, SDK `@modelcontextprotocol/ext-apps` 2.0.4) :

- le serveur annonce la vue exactement comme le fait l'assistant officiel `registerAppTool` : `_meta.ui.resourceUri` **et** la clé historique `_meta["ui/resourceUri"]` sur la définition de `request_print_confirmation` (`tools/list`) et dans son résultat, ressource `ui://bambu/print-confirm.html` en `text/html;profile=mcp-app` (`internal/mcpserver/app.go`, tests `TestToolsListConformsToMCPSpec` et `confirm_view_chain_test.go`) ;
- la spécification fait annoncer la prise en charge **par le client**, dans `initialize` : `capabilities.extensions["io.modelcontextprotocol/ui"].mimeTypes` contenant `text/html;profile=mcp-app`. Côté serveur c'est **facultatif** (le SDK ne l'impose pas : `getUiCapability` ne sert qu'à choisir un repli texte) ; bambud ne refuse ni ne change rien selon cette annonce ;
- la documentation d'Anthropic montre des serveurs **locaux** (`npx … --stdio` dans `claude_desktop_config.json`) qui affichent des vues dans Claude Desktop : un serveur local n'est donc pas exclu en principe. Seul `_meta.ui.domain` est indisponible pour un serveur local (inutile ici).

Ce qui a été ajouté : (1) bambud **journalise** à chaque connexion ce que le client a annoncé (`client MCP initialisé … ui_extension=true|false extensions=…`) : lisez-le dans les journaux du serveur (`journalctl -u atelier -g "client MCP"`) ; (2) quand le client n'annonce pas les vues, le texte du résultat est court et donne **le lien de confirmation et le résumé** (fichier, imprimante, plateau, mise à niveau, avertissements) ; le contenu structuré ne contient plus les consignes longues et reste léger ; (3) rien d'autre ne change côté sécurité.

**Non prouvé (hypothèses)** : pourquoi Claude Desktop ne monte pas la vue derrière `mcp-remote`. Pistes : le relais ne transmet pas l'annonce d'extension du client (le journal du point (1) le dira) ; une version de Claude Desktop ou de `mcp-remote` qui n'affiche les vues que pour certains connecteurs. Pour trancher, dans l'ordre : (a) vérifiez `/health` (`mcp_app_views: true`), Claude Desktop et `npx -y mcp-remote@latest` à jour, et acceptez « Autoriser / Toujours autoriser » ; (b) ajoutez à `claude_desktop_config.json` l'exemple officiel `{"command":"npx","args":["-y","@modelcontextprotocol/server-map","--stdio"]}` : s'il affiche sa vue, Desktop sait rendre des vues d'un serveur stdio et le problème est sur le chemin bambud/`mcp-remote` ; sinon c'est Desktop (version, réglage) ; (c) **connectez l'atelier en connecteur distant** (ci-dessous, `docs/OAUTH.md`) : c'est le chemin documenté pour un serveur distant (la documentation présente mcp-remote comme un moyen de le tester), à essayer si (b) désigne le chemin mcp-remote. Dans tous les cas la page `/confirm/…` fait exactement la même chose, photo du plateau comprise (c'est le repli, et il suffit).

**Avoir les vues : brancher l'atelier comme connecteur distant** (à essayer si (b) montre que le chemin `mcp-remote` est en cause ; non éprouvé contre un vrai claude.ai) : exposez `https://<nom>.ts.net/mcp` avec Tailscale **Funnel** (seulement `/mcp`, `/.well-known/*`, `/oauth/*`, voir `docs/OAUTH.md` § 3), puis Claude > Paramètres > Connecteurs > Ajouter un connecteur personnalisé, URL `https://<nom>.ts.net/mcp`, ID et secret **vides** ; le navigateur s'ouvre sur bambud pour autoriser (OAuth intégré, `bambud enroll` d'abord). Retirez ensuite l'entrée `mcp-remote` de `claude_desktop_config.json` pour ne pas avoir deux serveurs « Atelier ». Le Funnel rend le service joignable depuis Internet : le Bearer / OAuth reste la seule barrière, les pages humaines (`/confirm`, `/setup`) ne doivent PAS être exposées. Photo du plateau : sur la page navigateur c'est une image du même site ; dans la carte, une image réduite jointe au résultat (aucun domaine externe). Durées : la confirmation vit **10 minutes** depuis la demande ; l'accord de **2 minutes** ne court qu'à partir de votre clic.


---

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



## 7 bis. Analyser, réparer un maillage et choisir la machine (qualité 3D)

**Machine d'analyse.** `analyze_geometry` (paramètre `machine`, puis `nozzle_mm`) lit le volume utile, la buse et les **zones d'exclusion** dans les profils Bambu Studio installés : toutes les imprimantes de l'installation sont acceptées (`p1s`, `p1p`, `x1c`, `x1e`, `a1`, `a1mini`, `h2d`, `h2s`, `p2s`…, ou le nom complet « Bambu Lab H2D »). Sur une **double buse (H2D)**, la pièce est comparée au volume de **chaque buse** (zone gauche 325 × 320 × 320, zone droite décalée de 25 mm) ; un avertissement dit quand elle tient avec une seule buse mais pas dans la zone commune (donc pas de multi-matière entre buses). Si les profils ne sont pas lisibles, un repli **nominal** (valeurs génériques, sans exclusions) est utilisé et le rapport le dit (`target.source = "nominal"`, avertissement). Une machine inconnue est refusée avec la liste des modèles.

**Seuil de surplomb.** Il vient du preset process : `support_threshold_angle` (pente depuis l'horizontale ; 30° dans Bambu Studio, soit un surplomb à partir de 60° depuis la verticale), celui du process par défaut de la machine ou celui nommé par `process`. `overhang_angle_deg` (depuis la verticale) prime sur le profil. Le rapport donne le seuil dans les deux conventions et sa source ; sans profil, 45° par défaut est signalé.

**Épaisseur des parois.** Plusieurs rayons par point (axe −normale et 4 rayons à 25° autour), points tirés **au prorata de l'aire**, seule une face qui regarde la nôtre compte. La confiance (`low | medium | high`) tient compte de la part de rayons qui aboutissent et de leur accord (`ray_agreement`). Reste une **heuristique**, pas une mesure de conception ; elle sous-estime près des arêtes vives.

**Défauts signalés par l'analyse** : arêtes ouvertes, non manifold, normales incohérentes, triangles dégénérés, **triangles dupliqués** (compteur dédié), **éclats flottants** (composante de volume < 0,1 % de la pièce principale), **auto-intersections** (paires de triangles qui se traversent, hors voisins d'arête ; régions et nombre ; contrôle borné en temps : `complete = false` veut dire « au moins ce nombre »). Gros maillage (> 1 M triangles) : la notation des orientations travaille sur un échantillon, annoncé dans `sampling`.

**Réparer** : « Répare ce modèle » → `repair_mesh` (ou `POST /api/models/{id}/repair`). Réparation minimale : soudure des sommets, suppression des triangles dégénérés et dupliqués, orientation cohérente des normales (et inversion d'un maillage dont toutes les normales regardent vers l'intérieur), comblement des petits trous (`max_hole_edges`, 32 par défaut), suppression **facultative** des petits composants flottants (`remove_floating`, jamais par défaut). La réponse donne le rapport **avant / après** (étanchéité, volume, nombre de défauts, actions) et crée une **NOUVELLE version** (STL en mm) ; l'original et les versions précédentes ne sont jamais modifiés. `dry_run` ne fait que le rapport ; s'il n'y a rien à réparer, aucune version n'est créée. **Limites** : les arêtes non manifold, les auto-intersections et les grands trous ne sont pas réparés (signalés) ; le comblement est plan (ear clipping), il ne reconstruit pas une surface courbe ; un 3MF devient un STL (métadonnées de projet non reprises) ; un STEP n'est pas concerné.

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
| `hinge_print_in_place` | charnière dont l'axe s'imprime dans les nœuds de l'autre lame (2 pièces mobiles, aucun assemblage) ; **axe et perçage en goutte** à 45° (`teardrop`, défaut) : s'imprime à plat sans support | jeu radial/axial (jeu glissant mesuré si calibré, sinon 0,75 × jeu « libre ») |
| `hinge_pin` | charnière à nœuds alternés et axe séparé (tête) | jeu diamétral du perçage (ajustement glissant, croît légèrement avec le diamètre de l'axe) |
| `snap_clip` | clip cantilever à rampe d'insertion + récepteur à fenêtre | **SnapFit** : déformation, déformation admissible du matériau, flèche max, force d'insertion |
| `enclosure` | boîtier à angles arrondis, couvercle **vissé** (4 plots d'inserts à chaud, fraisurage) ou **clipsé** (ergots) | trou/profondeur/plot d'insert pour M2…M5 (`ScrewBoss`), jeu de couvercle, longueur de vis |
| `wall_mount` | plaque murale à vis fraisées ou **trous de serrure** | diamètre de vis compensé |
| `spur_gear`, `gear_pair` | engrenage droit en développante (module, dents, angle de pression, alésage, clavette, moyeu) : 24 points par flanc et **congé de pied 0,38·m** (enveloppe de la crémaillère) ; paire à **entraxe calculé** m·(z₁+z₂)/2 avec jeu de flanc | alésage compensé, rapport de conduite, **avertissements de sous-taillage (z < 2/sin²α, 17,1 à 20°) et d'interférence de développante** |
| `thread` | vis hexagonale / écrou métriques M3…M12 imprimables (hélice exacte, **profil ISO 68-1 à 60°**, **chanfrein d'entrée** sur la vis et l'écrou) ou plot pour insert | jeu de l'écrou (0,2 mm RADIAL ≈ 0,23 mm de jeu axial entre flancs), dimensions d'insert |
| `washer`, `handle`, `hook_reinforced`, `mounting_plate` | rondelle chanfreinée, poignée en U, crochet à congé et nervures, plaque perforée (grille ou quinconce) | perçages compensés |

**Exemples à dire à Claude**
- « Quels gabarits as-tu pour faire des charnières ? » → `list_part_templates` (filtre `category: mécanisme`), puis `describe_part_template` pour les paramètres et leurs bornes.
- « Fais-moi une charnière imprimée en place de 50 mm avec 7 nœuds, pour mon P1S en PLA. » → `create_from_template` avec `printer`, `material: PLA`, `nozzle_diameter: 0.4`. La réponse indique le jeu utilisé et sa provenance (« calibré le … » ou valeurs génériques), les hypothèses (axe fusionné avec la lame A, pas de bordure reliant les lames...) et vérifie que chaque pièce est étanche.
- « Un boîtier 80 × 50 × 30 avec couvercle vissé, vis M4, en PETG. » → les trous d'inserts (Ø5,6 × 9,6 mm), plots et longueur de vis viennent de `ScrewBoss`.
- « Un clip qui tienne 200 cycles en PETG, retour de 1,5 mm. » → `snap_clip` ; le résultat contient la déformation calculée et un avertissement si elle dépasse l'admissible du matériau.
- « Une paire d'engrenages 12/36 dents module 1,5, alésage 5. » → entraxe, rapport de réduction, rapport de conduite.
- « Garde ce collier comme "Collier tube 32". » → `save_as_template` : variante nommée, réutilisable ensuite comme `template` (seuls les paramètres demandés sont figés ; les jeux restent recalculés avec la calibration du moment, `frozen: true` fige aussi les valeurs calculées).
- « Refais le support avec 2 mm d'épaisseur de plus » → `create_from_template` avec `model_id` et `base_version_id` : **nouvelle version** du même modèle (refus si la version de départ est périmée).

**Qualité d'impression des pièces mécaniques (ce qui est vérifié, et ce qui ne l'est pas)**
- *Filetages* : maillage exporté fin (flèche 0,02 mm, uniquement pour les pièces filetées ; les autres gabarits restent à 0,02 mm / 0,1 rad génériques mais sans hélice). Hauteur de filet mesurée sur le STL exporté : 103,7 % (M3), 103,2 % (M4), 102,5 % (M6) de la théorie 0,6134·P. Le fond de filet est plat (pas de congé ISO). Un fichier STL de M3 sur 14 mm pèse environ 14 Mo (nombre de triangles dû à l'hélice) ; M6 environ 4 Mo.
- *Engrenages* : le pied est le résultat exact de la crémaillère de référence (tête arrondie à 0,38·m), sous-taillage compris ; en dessous de 17 dents à 20° le sous-taillage est signalé (« léger » ou « fort »), de même que l'interférence de développante d'une paire. Pas de déport de profil.
- *Charnière en place* : avec `teardrop`, aucune paroi ne dépasse 45° de la verticale (aire de surplomb mesurée sur le maillage : 0 mm², contre 50 mm² avec l'axe rond). Si la paroi des nœuds est trop fine pour loger la pointe, l'axe rond est conservé avec un avertissement. Le jeu `clearance` reste celui de l'ingénierie : jeu glissant mesuré par la calibration quand elle existe.
- *Jeu d'ajustement et dimension* : les jeux `fit:*` des gabarits dont la dimension est connue (collier : diamètre du tube ; charnière à axe : diamètre de l'axe) croissent légèrement avec elle : −0,04 mm à 3 mm, 0 à 10 mm, +0,05 mm à 30 mm, +0,15 mm à 100 mm (loi en ∛D de l'unité de tolérance ISO 286-1 pour la forme, amplitude **heuristique** à calibrer). La calibration est mesurée à 10 mm (languette du peigne) et extrapolée par la même loi (niveau « calculé » hors de 10 mm).

Chaque appel renvoie `auto` (paramètres calculés : valeur nominale, valeur utilisée, niveau de confiance, origine), `calcs` (calculs détaillés), `calibration` (appliquée ou non) et `warnings`. Les gabarits sont **versionnés** : la version du gabarit et les paramètres exacts sont conservés avec chaque version de modèle. Interface web : écran **Gabarits** (formulaire généré depuis le schéma, vignettes, résultat détaillé, enregistrement en variante).

## 13. Calibration d'une machine (pièces de test et mesures)

Les valeurs génériques (jeu glissant 0,25 mm, trou −0,15 mm...) sont des ordres de grandeur. Pour un couple **imprimante + buse + matériau (+ bobine)** précis, on imprime des pièces de calibration et on **mesure** :

| Pièce (`kinds`) | À faire | Clé de `record_calibration` |
|---|---|---|
| `fit_comb` | encoches de 10 mm + 0,1 → 0,6 mm (1 à 6 points) et une languette : trouver le plus petit jeu où elle **coulisse** (et celui où elle entre en force) | `fit_slides_gap_mm`, `fit_snug_gap_mm` |
| `hole_shaft` | plaque à trous verticaux, bloc à trous horizontaux et arbres de Ø3 à Ø10 : mesurer au pied à coulisse | `holes[] {nominal, measured, axis}`, `shafts[]` |
| `shrink_cube` | cube creux de 50 mm : mesurer X, Y, Z (à mi-hauteur, hors pied d'éléphant) — **une seule taille : échelle et décalage de contour restent confondus** | `cube_mm: [X, Y, Z]` |
| `bridge_overhang` | ponts de 10 à 50 mm et surplombs de 30° à 70° | `bridge_max_span_mm`, `overhang_max_deg` |
| `temperature` | une petite pièce par température (**5 valeurs réparties sur `nozzle_temperature_range_low/high` du profil filament de `base`**, sinon sur la plage du matériau — PLA 190-240, PETG 220-260, ABS/ASA 240-280, TPU 200-250, PC 260-290, PA 260-300 °C, lues dans les profils de base de Bambu Studio), ou une tour (`temperature_mode: tower`) avec changements de température à la couche. La plage utilisée et son origine sont dans `temperature_range` | `best_temp_c` |
| `shrink_sizes` | **deux tubes carrés (20 et 80 mm par défaut)**, mêmes réglages : mesurer X, Y, Z extérieurs (et le côté intérieur affiché par la pièce) de chaque taille, à mi-hauteur | `shrink_sizes[] {nominal_mm, x_mm, y_mm, z_mm, inner_nominal_mm, inner_x_mm, inner_y_mm}` |
| `flow_ratio` | une pièce par valeur de `filament_flow_ratio` (défaut 0,94 → 1,02) : vase Ø40 mm en mode spirale, donc **paroi d'une seule ligne** ; mesurer son épaisseur au pied à coulisse en 4 points | `flow {series[{ratio, wall_mm}], target_wall_mm}` (régression, ex. cible 0,42 mm) ou `best_flow_ratio` |
| `retraction` | une pièce par rétraction (défaut 0,2 → 1,4 mm) : deux tours de Ø6 × 30 mm ; compter les fils entre les tours | `best_retraction_mm` (la plus courte sans fils) |
| `volumetric_speed` | une pièce par débit maximal (défaut 8 → 24 mm³/s) : vase Ø30 mm dont la vitesse de paroi dépasse la limite testée ; chercher trous, bandes mates, claquements | `best_max_volumetric_speed` (bambud en propose **90 %**) |
| `first_layer` | une pièce par valeur de `first_layer_param` (`height`, `speed` ou `temperature`) : plaque 50 × 50 × 0,6 mm ; lignes jointives et lisses = bon écrasement | `first_layer {param, value}` |
| `pressure_advance` | une pièce par K (défaut 0 → 0,08) : carré à angles vifs. **Aucune clé de profil BBL** (ni `pressure_advance` ni `enable_pressure_advance`) : K se règle à la main dans Bambu Studio | `best_pressure_advance` |

Toutes ces séries sont des **petites pièces, une par valeur**, avec — quand `base` est fourni — une **révision de profil bornée** par pièce (`filament_flow_ratio` [0,85 ; 1,10], `retraction_length` [0 ; 3] mm, `filament_max_volumetric_speed` [1 ; 40] mm³/s, `initial_layer_print_height`, `initial_layer_speed`, `nozzle_temperature_initial_layer`). Elles n'utilisent **aucun G-code personnalisé** (les surcharges `*_gcode` sont interdites par sécurité) ; les clés sont vérifiées dans les profils Bambu Studio 02.08 installés. Seuls les cinq premiers `kinds` sont générés par défaut.

**Exemples à dire à Claude**
- « Génère les pièces de calibration pour mon P1S, buse 0,4, PETG HF noir. » → `generate_calibration_set` : les pièces entrent au catalogue (étiquette *calibration*) ; avec `base` (preset machine/process/filament) Claude crée aussi une **révision de profil découpable par température**. Réponse : quoi mesurer pour chaque pièce et un squelette de mesures.
- « Mes mesures : ça coulisse à 0,3 ; les trous de 4/6/8 mesurent 3,86/5,85/7,84 ; cube 49,8 × 49,9 × 50,0 ; meilleure température 240. » → `record_calibration` : jeu effectif, **droite de compensation de trou** (décalage = a + b·d), facteurs de retrait ×1,004 / ×1,000, température. Les mesures implausibles (unité, trou bouché, cube à ±10 %) sont refusées.
- « Génère les tubes de retrait de 20 et 80 mm pour mon PLA. » puis « Mes mesures : 20 mm → 20,12 × 20,14, 80 mm → 79,94 × 79,96, intérieurs 11,80 et 67,64. » → `shrink_sizes` : **facteur d'échelle** (ici ×1,003) **et décalage de contour séparés**, avec `xy_contour_compensation` et `xy_hole_compensation` PROPOSÉS (par côté, bornés à ±0,3 mm). Préférer ces décalages à une mise à l'échelle globale ; rien n'est appliqué automatiquement.
- « Fais la série de débit : 0,94, 0,96, 0,98, 1,00, 1,02. » puis « Parois : 0,38 ; 0,39 ; 0,41 ; 0,42 ; 0,44, cible 0,42. » → `flow_ratio` puis `record_calibration` avec `flow` : débit retenu par régression.
- « Quelle calibration utilises-tu pour mon P1S en PETG ? » → `get_calibration` : valeurs, provenance (« calibré le 2026-11-07 (p1s, buse 0.4, PETG, …) »), clés utilisées, historique.

**Priorité** : les corrections mesurées priment sur les valeurs génériques dans `create_from_template` (jeux, trous) et dans les calculs d'ingénierie (`FitClearanceCal`, `HoleCompensationCal`...), avec leur provenance. Résolution du plus général au plus spécifique, champ par champ : (buse, matériau) → + bobine → + imprimante → + imprimante et bobine. Une autre buse, un autre matériau : aucune calibration (valeurs génériques). Les mesures sont conservées dans un journal immuable ; refaire la calibration met à jour la valeur active. Interface web : onglet **Calibration** de l'écran Gabarits.

**Valeurs retenues et profils** : les valeurs d'extrusion (débit, rétraction, débit volumétrique, première couche, décalages de contour et de trou) sont stockées pour (imprimante, buse, matériau, bobine) et renvoyées par `record_calibration` et `get_calibration` dans le champ **`proposals`** (`overrides` : clé BBL, valeur, cible ; `manual` : réglages sans clé de profil). Elles sont **proposées, jamais appliquées automatiquement** : après accord, les passer à `modify_slicing_profile` (champ `modifications`). Seules les clés de la liste blanche (§ 13) sont proposées : `filament_retraction_length` n'en fait pas partie, donc si le profil filament renseigne sa propre rétraction (autre chose que « nil »), elle prime sur `retraction_length` de la machine et se règle dans Bambu Studio. La largeur de ligne de première couche (`initial_layer_line_width`) n'est pas surchargeable non plus : pas de série dédiée.

**Calibrations de Bambu Studio** : Bambu Studio possède ses propres étalonnages (menu Calibration : *Flow Dynamics*, *Flow Rate*, débit volumétrique max, température, rétraction...). Ils passent par l'**interface graphique** et envoient leurs réglages à l'imprimante ; bambud ne peut ni les piloter ni relire leurs résultats (le binaire installé n'affiche aucune aide en ligne de commande et aucune option d'étalonnage n'est utilisée par bambud). L'avance de pression (K) est le cas typique : les profils Bambu n'exposent pas la clé, donc seul *Flow Dynamics* ou un réglage manuel dans Studio l'applique. Les séries de bambud servent quand on veut des pièces au catalogue, des profils découpables reproductibles et un historique par bobine.

**Limites** : le facteur de retrait est calculé et affiché mais pas appliqué automatiquement aux gabarits (le trou et l'arbre rétrécissent ensemble ; une mise à l'échelle globale fausserait les compensations) — l'appliquer au besoin avec `modify_geometry` (échelle) et préférer les décalages de contour/trou issus de `shrink_sizes`. Avec seulement deux tailles on ne vérifie pas la linéarité (une troisième taille le permet). La tour de température demande d'insérer les changements `M104` à la main dans Bambu Studio (hauteurs fournies) ; la série de pièces est plus simple et se découpe avec les profils créés. Les valeurs retenues viennent de lectures humaines (pied à coulisse, œil) : elles valent pour cette bobine, cette buse et cette imprimante. Les filetages M3/M4 sont limités par la résolution FDM (avertissement) ; la poignée et le support mural demandent un essai de charge réel.

## 14. Exporter un projet (archive à partager)

Pour donner ou publier un projet (une collection, ou quelques modèles) : « Exporte le projet *Support téléphone* avec la licence CC BY-NC 4.0, quantité 2 pour la base, et joins mes photos » (`export_project`, puis `get_project_export` pour redemander le lien). Sur le web : **Bibliothèque → choisir une collection → « Exporter le projet »** (formulaire : titre, description, licence, notice d'assemblage, quincaillerie, options). Rien n'est publié : vous obtenez un **zip** à télécharger.

**Contenu de l'archive**

| Entrée | Rôle |
|---|---|
| `README.md` | en français : description, tableau des pièces (quantités, matière, dimensions), réglages par pièce, temps / masse / coût **estimés** si une découpe existe, notice d'assemblage et quincaillerie (champs libres), licence (défaut « non précisée »), photos |
| `files/<pièce>/…` | fichiers sources **d'origine** (STL, STEP, 3MF, OBJ) de la dernière version (ou de la version choisie par pièce), copiés tels quels ; leur sha256 est recalculé à l'écriture et comparé au catalogue |
| `sources/<pièce>/parametrique.json` | pour une pièce générée par un gabarit : gabarit, version, paramètres demandés et résolus, calculs, calibration utilisée |
| `profiles/<pièce>.json` | réglages de découpe effectifs : **uniquement les clés surchargées** (révision de profil appliquée, lignée comprise) et la référence aux préréglages officiels de Bambu Studio ; **jamais de G-code** |
| `printer-specific/<pièce>/….gcode.3mf` | **en option, désactivé par défaut** : seulement les archives découpées *validées* ; spécifiques à une imprimante, une buse et des réglages |
| `images/<pièce>/photo-NN.jpg` | en option : photos de fin d'impression du journal (JPEG sans EXIF ni GPS ; photos d'alerte d'échec exclues) |
| `manifest.json` | versions, sha256 et taille de chaque fichier, provenance (source, auteur, URL), date, avertissements, éléments laissés de côté |

La structure (`files/`, `images/`, `README.md`) correspond à ce que demandent les dépôts MakerWorld et Printables.

**Garanties**

- **Aucun secret ni donnée d'imprimante** : pas d'adresse, de numéro de série ni de code d'accès, et **aucun chemin du serveur ou de votre disque** (le chemin d'origine d'un import n'est jamais exporté). Les textes générés (README, JSON) passent par un filtre qui remplace ces valeurs, y compris si vous les avez collées dans une description. Un fichier `.3mf` (d'origine ou découpé) qui contient l'adresse ou le numéro de série d'une imprimante déclarée, ou un chemin du serveur, **n'est pas joint** : il est signalé dans `manifest.json` et dans la réponse.
- **Streaming et taille bornée** : l'archive est construite par un travail asynchrone, fichier par fichier, sans jamais charger un fichier entier en mémoire. Limite par défaut 1 Gio décompressé (réglable jusqu'à 4 Gio avec `max_bytes`), 200 pièces au plus ; un dépassement est refusé **avant** d'écrire quoi que ce soit.
- **Téléchargement par lien signé à durée limitée** : `/api/exports/<id>.zip?exp=…&sig=…` valable **10 minutes**, lisible sans jeton (comme les aperçus 3D) mais impossible à modifier ou à réutiliser pour un autre export. Redemandez un lien à tout moment (`get_project_export`, ou le bouton). L'archive reste **24 h** sur le serveur (20 archives au plus), puis expire ; le dossier `exports/` du dossier de données n'est pas sauvegardé.
- Si une pièce vient d'un site tiers (provenance avec URL), un avertissement rappelle de vérifier sa licence avant de la republier.

**Ce qu'il reste à faire à la main pour publier** (aucune publication automatique, volontairement) :

1. Relire et compléter le `README.md` (description, photos, notice d'assemblage).
2. Choisir la licence si elle est « non précisée » ; vérifier le droit de republier les pièces issues d'ailleurs.
3. Créer la page du projet vous-même sur MakerWorld ou Printables, puis y déposer le contenu de `files/`, les images de `images/` et le texte du README.
4. Renseigner dans le formulaire du site la catégorie, les étiquettes, l'image de couverture et les réglages conseillés (les `profiles/*.json` listent seulement ce qui diffère des préréglages officiels).
5. Pour MakerWorld, joindre de préférence un 3MF de projet enregistré depuis Bambu Studio (plateau et réglages inclus) : l'archive ne le remplace pas.
6. Ne publier les `.gcode.3mf` que s'ils sont voulus (propres à une imprimante).
7. Vérifier une dernière fois les photos (arrière-plan, informations personnelles).

**Limites** : les fichiers dont l'original n'est copié que sur Windows ne sont pas exportables (signalés, laissés de côté) ; les estimations de temps, de masse et de coût viennent de la dernière découpe enregistrée de la pièce (sans découpe, ces lignes sont absentes) et ne sont pas une facture ; les photos PNG et WebP sont jointes telles quelles (seul l'EXIF des JPEG est retiré).

---

## 13. Clés de profil que Claude peut modifier (surcharges)

`modify_slicing_profile`, les variantes (`apply_variant`, `create_experiment`), la calibration et `execute_headless_slice` n'acceptent que les clés ci-dessous, avec leurs bornes. **Tout paramètre de G-code (`*_gcode`, `post_process`) et toute clé hors liste sont refusés** : le G-code de démarrage et de fin reste celui du profil officiel de Bambu Studio. Une valeur hors bornes est refusée avec la borne et sa source (« `nozzle_temperature` = 280 hors bornes [190 ; 240] (nozzle_temperature_range_high du filament) »). La liste complète et ses bornes vivent dans `slicer.OverridableKeys()`.

| Famille | Clés | Bornes |
|---|---|---|
| Hauteur de couche | `layer_height`, `initial_layer_print_height` | 0,04 mm → diamètre de buse |
| Parois et coques | `wall_loops` (1–50), `top_shell_layers`, `bottom_shell_layers` (0–50), `top_shell_thickness`, `bottom_shell_thickness` (0–20 mm), `wall_generator`, `only_one_wall_top`, `detect_thin_wall` | statiques |
| Remplissage | `sparse_infill_density` (0–100 %), `sparse_infill_pattern` (18 motifs), `top_surface_pattern`, `bottom_surface_pattern`, `infill_direction` | statiques / énumérations |
| Couture et surface | `seam_position` (nearest, aligned, aligned_back, back, random), `ironing_type`, `ironing_flow`, `ironing_spacing`, `ironing_speed`, `fuzzy_skin*`, `elefant_foot_compensation`, `xy_contour_compensation`, `xy_hole_compensation`, `spiral_mode`, `adaptive_layer_height`, `enable_arc_fitting` | statiques / énumérations |
| Adhérence | `brim_type`, `brim_width` (0–30), `brim_object_gap`, `skirt_loops`, `skirt_distance`, `skirt_height`, `raft_layers`, `draft_shield`, `enable_prime_tower` | statiques |
| Supports | `enable_support`, `support_type`, `support_style`, `support_base_pattern`, `support_threshold_angle` (0–90°), `support_on_build_plate_only`, `support_*_z_distance`, `support_object_xy_distance`, `support_interface_*` | statiques |
| Vitesses (mm/s) | `outer_wall_speed`, `inner_wall_speed`, `sparse_infill_speed`, `internal_solid_infill_speed`, `top_surface_speed`, `initial_layer_speed`, `initial_layer_infill_speed`, `travel_speed`, `bridge_speed`, `gap_infill_speed`, `support_speed`, `support_interface_speed`, `small_perimeter_speed`, `overhang_*_speed` | ≤ `machine_max_speed_x/y` du profil machine (P1S : 500) |
| Accélérations (mm/s²) | `default_acceleration`, `initial_layer_acceleration`, `outer_wall_acceleration`, `inner_wall_acceleration`, `top_surface_acceleration`, `sparse_infill_acceleration`, `bridge_acceleration` ; `travel_acceleration`, `initial_layer_travel_acceleration` | ≤ `machine_max_acceleration_extruding` (P1S : 20000) ; déplacement ≤ `…_travel` (9000) |
| Températures de buse (filament) | `nozzle_temperature`, `nozzle_temperature_initial_layer` | de `nozzle_temperature_range_low` à `min(nozzle_temperature_range_high, maximum du modèle)` (P1S/A1/A1 mini : 300 °C) |
| Températures de plateau (filament) | `hot_plate_temp`, `cool_plate_temp`, `eng_plate_temp`, `textured_plate_temp`, `supertack_plate_temp` et leurs `_initial_layer` | ≤ limite du modèle (P1S 100 °C, A1 mini 80 °C) |
| Débit et refroidissement (filament) | `filament_flow_ratio` (0,5–1,5), `filament_max_volumetric_speed`, `fan_min_speed`, `fan_max_speed`, `overhang_fan_speed`, `additional_cooling_fan_speed`, `during_print_exhaust_fan_speed`, `complete_print_exhaust_fan_speed` (0–100 %), `close_fan_the_first_x_layers`, `full_fan_speed_layer`, `fan_cooling_layer_time`, `slow_down_min_speed`, `slow_down_for_layer_cooling`, `reduce_fan_stop_start_freq` | statiques |
| Rétraction (machine) | `retraction_length` (0–15), `retraction_speed`, `deretraction_speed`, `retraction_minimum_travel`, `retract_restart_extra`, `z_hop`, `wipe_distance`, `retract_when_changing_layer`, `wipe` | statiques |

- Une clé valide mais absente du profil résolu (par exemple `brim_type`) est ajoutée au process avec le type de la liste blanche ; les autres clés doivent exister dans le profil. Sa valeur **avant** la surcharge est le défaut de Bambu Studio (`brim_type` : `auto_brim`, `raft_layers` : 0…), jamais `null` : `modify_slicing_profile` renvoie aussi `effective_settings` (`brim_type`, `brim_width`, `raft_layers`, `enable_support`, `support_type`, `support_threshold_angle`, `curr_bed_type`) avec l'origine de chaque valeur (surcharge, profil, défaut Bambu Studio), et le résumé de découpe reprend les valeurs réellement tranchées.
- `curr_bed_type` (plateau installé) : `Cool Plate`, `Engineering Plate`, `High Temp Plate`, `Textured PEI Plate`, `Supertack Plate`. Il est refusé si le filament déclare 0 °C pour ce plateau (`cool_plate_temp`, `eng_plate_temp`, `hot_plate_temp`, `textured_plate_temp`, `supertack_plate_temp`) ; le message donne les plateaux prévus par la matière.
- `sparse_infill_density` accepte `15` ou `15%` (écrit `15%`). Un booléen s'écrit `true`/`false`/`0`/`1`.
- Les réglages **par pièce** d'un plan multi-pièces (`plan_plate`) sont limités à : densité, motif de remplissage, parois, hauteur de couche, couches du dessus/dessous, bordure (type, largeur) et supports ; mêmes bornes (hauteur de couche ≤ buse, vérifiée au tranchage).
- Ce que ça change pour vous : Claude ne peut plus, même involontairement, changer ce que l'imprimante exécute au démarrage ou à la fin, ni demander une température que le matériau ou la machine ne supporte pas. Le résumé de confirmation (`/confirm`) affiche les températures réellement lues dans le G-code (voir `docs/SECURITY.md` § 11).

## 14. Retours des premiers essais sur la P1S (ce qui a changé)

- **Pièces larges.** La P1S exclut un coin (0-18 × 0-28 mm) : la zone sûre est **238 × 228 mm**. Claude refuse tout de suite une pièce trop grande (« pièce hors zone imprimable : dimensions…, zone utile… »), tourne seul de 90° une pièce qui n'y tient que dans l'autre sens, et signale une pièce à la limite. Le message d'erreur anglais de Bambu Studio (-50) n'apparaît plus tel quel.
- **Opérations longues.** Découper, envoyer un gros fichier ou importer peut durer des minutes : Claude reçoit un `job_id` au bout de ~20 s et suit avec `get_job_status` (pourcentage, phase, octets envoyés, temps restant, position dans la file). Un appel qui rend un `job_id` n'a pas échoué. Pour la découpe, le pourcentage est une estimation par le temps.
- **Lancement.** Une P1S peut mettre plus de 30 s à accuser réception d'un gros fichier. Le service attend plus longtemps, ne renvoie **jamais** la commande, et fait passer seul un lancement « incertain » en « en cours d'impression » dès que l'imprimante montre ce fichier. Seul `reconcile_print` conclut « pas démarré ».
- **Lien d'arrêt.** Les liens `/stop/…` ne contiennent plus que des caractères alphanumériques ; les anciens liens dont le dernier `_` a disparu fonctionnent encore. Un lien qui ne marche pas dit pourquoi : expiré, inconnu, impression terminée ou déjà utilisé.
- **Matières.** « Quelles matières pour ma P1S ? » → `list_filaments` (groupées par famille). On peut écrire « PETG transparent », « PLA mat », « PETG translucide » : le service choisit le preset installé et compatible, signale un repli, ou propose les 5 noms les plus proches. Les profils par défaut préfèrent le preset de la machine ; un preset dessiné pour une autre machine (le X1C pour la P1S, seul déclaré compatible par Bambu) est signalé.
- **Noms de filament sans suffixe.** « Bambu PETG Translucent » (ou avec le suffixe d'une autre machine, `@BBL X1C`) est résolu vers le préréglage compatible avec la machine choisie (`create_experiment`, `modify_slicing_profile`, `execute_headless_slice`), avec une note ; un nom ambigu dit lequel est retenu. `material_known` ne signale plus « absent du catalogue » pour un préréglage officiel Bambu (suffixe `@BBL …` normalisé). Dans `create_experiment`, un levier écarté dit quel paramètre du critère manque et la valeur attendue ; une variante personnalisée liste ses changements réels (`changes`).
- **Listes lisibles par Claude.** `list_filaments`, `get_model_details`, `list_collection`, `list_print_history` et `list_spools` listent leurs éléments (noms + identifiants, 20 lignes au plus) dans leur texte, pas seulement des compteurs.
- **Couleur.** Une seule couleur effective par filament, avec sa source (`color` + `color_source` : « AMS », « bobine déclarée », ou vide pour le profil) : après validation comme après dépôt, c'est celle de la bobine réellement chargée ; la couleur du preset reste lisible dans `profile_color`. La couleur annoncée est celle de la bobine **chargée** (AMS lu, ou inventaire). Pour la bobine externe, déclarez-la : « la bobine externe est du PETG translucide bleu clair » → `update_spool` avec `slot=ext`, `material`, `color_hex` et `translucent`. Sans cela, la couleur est « inconnue » (jamais le vert par défaut du profil).
- **Écran Impression.** Onglets Toutes / Actives / Terminées / Échecs / Archivées / Corbeille, regroupement par modèle, boutons Archiver / Corbeille / Restaurer (récupérable), « Archiver les n échecs », « en file (position n) » ou « découpe en cours · x % » ; après un arrêt demandé par vous l'imprimante s'affiche « arrêtée (annulée par vous) », et un « failed » Bambu est expliqué (annulation ou erreur, codes HMS). Une impression dont le travail de découpe a disparu est fermée avec la raison.

## 15. Lancer après une annulation, options, étapes réelles, attente et barre de progression

- **Après une annulation.** La P1S reste en `FAILED` jusqu'à son redémarrage ; l'Atelier lance quand même depuis `FAILED` ou `FINISH` (comme Bambu Studio) si l'état est frais, sans alerte HMS ni erreur en cours. Avant d'émettre une confirmation, `request_print_confirmation` vérifie que l'imprimante est lançable et sinon refuse avec la consigne précise (« l'imprimante signale une erreur HMS 0300_… ; acquittez-la sur l'écran ou redémarrez »). `get_printer_status` rend `launchable`, `launch_blocker`, `stage` / `stage_name` et `error_details` (print_error et HMS décodés). La table des codes Bambu est **partielle** (sources : wiki Bambu Lab, ha-bambulab, OpenBambuAPI) : un code absent est rendu « code X (inconnu) » avec un lien vers le wiki, jamais deviné.
- **Options de démarrage.** `request_print_confirmation` accepte `bed_leveling` (défaut vrai), `flow_cali` (défaut vrai), `vibration_cali` (défaut vrai) et `timelapse` (défaut faux) : ce sont les valeurs envoyées jusqu'ici. Elles sont affichées sur `/confirm` et dans le contenu structuré de la vue (`binding.bed_leveling`, `flow_cali`, `vibration_cali`, `timelapse`) et font partie de ce que l'humain approuve.
- **Ce que l'imprimante a réellement fait.** `get_print_status` rend `stage_report` : les étapes vues pour CETTE impression d'après l'étape courante de l'imprimante (`stg_cur`), avec l'heure locale (« Mise à niveau du plateau : effectuée à 21:05 »), et `bed_leveling` / `flow_cali` / `vibration_cali` = `performed` | `not_observed` | `not_requested` | `unknown`. Une étape trop brève pour être vue donne `not_observed` : ce n'est pas une preuve d'absence.
- **Lire le G-code.** `read_gcode_excerpt(artifact_id | print_job_id, plate?, head_lines?, grep?, max_lines?)` (lecture seule, bornée) : par exemple `grep: "G29|M622|(?i); leveling"`.
- **Attendre sans boucler.** `wait_for_job(job_id, timeout_seconds ≤ 60, défaut 25)` rend dès que l'état du travail change, ou à l'échéance (`wait.timed_out`, le travail continue). `wait_for_print_change(print_job_id, timeout_seconds)` fait de même pour une impression (état de l'impression, de l'imprimante ou étape) ; `get_print_status(wait_seconds)` aussi.

### Champs de progression (stables, pour dessiner une barre)

Tout travail long (découpe, dépôt FTPS, import, export) expose `progress_detail` dans `get_job_status` / `wait_for_job` (et `progress` dans `upload_print_artifact`, `get_print_status`) :

| Champ | Type | Sens |
|---|---|---|
| `percent` | entier 0..100 | avancement (octets envoyés / total pour un dépôt ; temps écoulé / attendu, **estimé**, pour une découpe : `estimated` vrai) |
| `done_bytes`, `total_bytes` | entiers | octets transférés / à transférer (0 si sans objet) |
| `eta_seconds` | entier, facultatif | durée restante estimée |
| `phase_code` | chaîne | phase STABLE : `queued`, `slicing`, `uploading`, `starting`, `importing`, `analyzing`, `rendering`, `converting`, `exporting`, `printing`, `running`, `done`, `failed`, `cancelled`, `unknown` |
| `phase_label` | chaîne | libellé lisible (« Dépôt sur la P1S ») |
| `phase` | chaîne | libellé libre historique de la phase (toujours présent) |
| `bar` | chaîne | barre texte de 10 cases, ex. `███████░░░` |
| `text` | chaîne | ligne lisible : `Dépôt sur la P1S : ███████░░░ 72 % (21,6 / 30 Mo, ~1 min)` |
| `queue_position`, `stalled`, `elapsed_seconds`, `message` | | position en file, travail sans signe de vie, temps écoulé, étape courante |

`get_upload_status` / `upload_file` (envoi d'un fichier vers l'Atelier) rendent les mêmes `percent`, `done_bytes`, `total_bytes`, `phase` (`receiving` | `complete`), `phase_label`, `bar`, `text`.

**Notifications.** Si le client fournit un `progressToken` (`_meta.progressToken` de `tools/call`), `wait_for_job`, `wait_for_print_change`, `get_print_status(wait_seconds)`, `upload_print_artifact` et `start_print` émettent des `notifications/progress` : `progress` = pourcentage (strictement croissant), `total` = 100, `message` = la ligne `text`. Sans jeton, rien n'est émis : le résultat porte la même information.

## 16. Quand l'imprimante s'arrête, plante ou redémarre en plein travail

- **L'impression reste « en cours » alors que l'imprimante est arrêtée ?** Plus maintenant : dès que l'imprimante revient au repos après avoir travaillé, ne montre plus ce fichier, ou redémarre, l'impression passe en **échec** avec la cause en français (« 0300_0D00_0002_0005 (grave) : mise à l'origine du plateau… ») et l'heure de chaque étape dans `get_print_status` (`causes`, `job.events`).
- **Relancer le même fichier sans tout refaire** : « relance le fichier déjà déposé » → `reprint_uploaded(print_job_id)`. Claude vérifie que le fichier est toujours sur la carte (taille et sha256) et que l'imprimante est au repos, puis vous donne la carte de confirmation habituelle (plateau vide). Rien n'est redécoupé ni renvoyé. Si l'imprimante affiche encore une alerte, Claude vous dit de l'acquitter ou de la redémarrer.
- **L'arrêt n'a pas l'air d'avoir marché ?** Tout est journalisé (demande, validation, commande, accusé, vérification). Si l'imprimante a coupé les chauffes mais affiche encore RUNNING, l'impression est bien marquée annulée et Claude vous conseille de **redémarrer l'imprimante**.
- **Pause sur erreur** (ex. bobine externe sans filament) : `get_print_status` dit « attend une action à l'écran : <texte HMS> » et vous recevez la notification « print_attention » si un canal est configuré. Chargez le filament à l'écran, ou demandez `load_filament` (lien de validation, un clic), puis `resume_print` (lien de validation). **Claude ne reprend jamais seul.**
- **« Bloquée »** : si la couche, le pourcentage et le temps restant ne bougent plus depuis 5 minutes, ou si le plateau a une consigne de 0 °C en pleine impression, `get_print_status` dit « bloquée : … ». C'est une alerte : regardez l'imprimante ; `cancel_print` (validé par vous) si besoin.
- **Imprimante injoignable** (« no route to host » après un redémarrage) : `diagnose_connection` dit si c'est le **réseau** (aucun port ne répond), le **service** (MQTT fermé, code d'accès changé) ou l'**imprimante** (elle répond mais ses services n'écoutent pas), avec la dernière connexion réussie. L'Atelier se reconnecte seul (2 s, puis jusqu'à 60 s) et reprend dès qu'un port répond.
- **Carte SD** : `list_printer_files` (qui est quoi : impression, modèle, profil ; ce qui est en usage ; ce qui est obsolète) puis `cleanup_printer_files` : vous validez sur un lien qui liste **exactement** les fichiers supprimés. Les nouveaux dépôts ont un nom lisible (`porte_guitare_supports_v3-<empreinte>.gcode.3mf`).
- **Liens de validation** : `/action/{id}` (reprise, chargement, nettoyage) valable 10 minutes, utilisable une fois, comme `/stop/{id}`. Le code de lancement personnel peut aussi les valider depuis la vue (outil réservé `validate_action_with_code`) ; il n'y a pas encore de carte dédiée dans la conversation : Claude vous donne le lien.
- **À savoir** : la table des codes HMS est écrite de mémoire et marquée « à vérifier » ; un code inconnu renvoie vers le wiki Bambu. Le chargement de la bobine externe (commande `ams_change_filament`) n'est pas encore éprouvé sur la vraie P1S.
