# Guide d'interface de Printalk (G10 UX)

Ce guide dit **où est chaque chose**, **quels mots on emploie** et **les règles à suivre** pour ajouter ou modifier un écran. Il accompagne la spec `docs/specs/G10-refonte-navigation-tableau-de-bord.md` ; en cas de doute, la spec fait foi. État décrit ici : **vagues U1** (coque, navigation, vocabulaire, début des états vides et erreurs) **et U2** (Accueil refondu, fiche impression adressable, fiche modèle à onglets, recherche globale, Réglages › Suivi ; § 8). Restent pour la suite : la version complète de l'Accueil (chiffres PJ1, fil d'activité), la page Projets à onglets, l'alignement des pages du serveur et des cartes (U3), les filtres de recherche et la recette visuelle (U4).

## 1. Le menu : 7 entrées, dans cet ordre

| Entrée | Chemin | Contenu |
|---|---|---|
| Accueil | `/` | le tableau de bord (§ 8) |
| Projets | `/projets` | les projets et leur avancement (G9) ; page d'état vide tant que G9 n'est pas fusionnée |
| Bibliothèque | `/bibliotheque` | modèles et collections, import ; sous-vues **Pièces sur mesure** (`/bibliotheque/gabarits`) et **Corbeille et doublons** (`/bibliotheque/corbeille`) ; fiche d'un modèle `/modele/:id` |
| Impressions | `/impressions` | vues `?vue=` : **À faire**, **En cours et en file**, **Terminées**, **Bilan** ; assistant `/impressions/nouvelle` ; fiche d'une impression `/impressions/:id` |
| Imprimantes | `/imprimantes` | vues `?vue=` : **Mes imprimantes**, **Modèles connus**, **Calibration** |
| Matières | `/matieres` | vues `?vue=` : **Bobines et AMS**, **Multicolore**, **Fiches matières**, **Après impression** |
| Réglages | `/reglages` | **Apparence**, **Tarifs** (`/reglages/tarifs`), **Suivi** (`/reglages/suivi`), **Sécurité** (`/reglages/securite`), **Agent Windows** (`/reglages/agent-windows`), **Avancé** (`/reglages/avance`, avec Dysfonctionnements et Système de design) |

Où est passé… : **Coûts** → Impressions › Bilan (chiffres), Réglages › Tarifs (prix), Matières › Après impression (finition) ; **Journal** → Impressions › Terminées ; **File d'attente** → Impressions › En cours et en file ; **Bobines** et **Matériaux** → Matières ; **Préparer** → bouton « Préparer » de la fiche d'un modèle (`/modele/:id/preparer`) ou « Nouvelle impression » ; **Comparaison / variantes** → « Essais de réglages » de la fiche d'un modèle (`/modele/:id/essais`) ; **Gabarits** → Bibliothèque › Pièces sur mesure ; la **calibration** → Imprimantes › Calibration ; **Corbeille** → Bibliothèque › Corbeille et doublons ; **Agent Windows** → Réglages.

Un bandeau fermable « Le menu a changé » le rappelle à la première visite (mémorisé dans le navigateur : clé `printalk.menu-v2-vu`).

### Anciennes URL : toutes redirigées

Aucune ancienne adresse n'est cassée ; une redirection côté client conserve les paramètres de la requête. La table est dans `web/src/redirects.ts` (testée ligne à ligne) : `/modele`, `/corbeille`, `/gabarits`, `/preparation` (avec ou sans `?model=`), `/comparaison` (avec ou sans `?experiment=`), `/impression`, `/file`, `/bobines`, `/materiaux`, `/couts` (et ses `?onglet=`), `/tableau-de-bord`, `/agents`, `/design`. Une adresse inconnue affiche la page **Introuvable** (statut 200 du repli SPA, un h1, une recherche, les 7 sections).

### Pages du serveur : adresses inchangées

`/enroll`, `/confirm/{id}`, `/stop/{id}`, `/action/{id}`, `/setup/*`, `/agents/pair`, `/oauth/*`, `/health`, `/about`, `/privacy`, `/api/*`, `/mcp` ne changent pas (liens et courriels déjà envoyés restent valides). Aucune route de l'interface ne peut commencer par ces préfixes : la liste est `web/reserved-routes.json`, lue par le test vitest `nav.test.ts` et par le test Go `internal/webui/routes_test.go`.

## 2. Ajouter un écran

1. **Une ligne dans `web/src/nav.ts`** (table `ROUTES` : chemin, titre, parent, mots-clés, onglets éventuels). Une entrée de premier niveau n'est ajoutée qu'après le retrait d'une autre (7 au plus).
2. Une `<Route>` dans `web/src/main.tsx` (le test `nav.test.ts` échoue si les deux divergent).
3. L'écran commence par `<PageHeader title help actions below />` : fil d'Ariane (dès la profondeur 2, déduit de `nav.ts` ou fourni avec des noms propres), **un seul h1**, une phrase d'aide d'une ligne, **une seule action principale** (bouton plein), onglets ou navigation de section en dessous.
4. Onglets : déclarés dans `nav.ts` (≤ 4), lus avec `useTab('/chemin')` (`web/src/tabs.ts`) : l'état est dans l'URL (`?vue=`), donc lien direct et retour arrière fonctionnent ; les flèches changent d'onglet. Au-delà de 4 vues, on utilise une navigation de section (`SectionNav`, une URL par sous-page), comme Réglages.
5. Une section ne mélange pas des fonctions étrangères : un onglet est une **vue du même objet**. Un **réglage** va dans Réglages, jamais dans un rapport.

Composants de navigation : `web/src/components/{PageHeader,Breadcrumb,SectionNav,MobileTabBar,NavProgress}.tsx` (dépendent du routeur : ils vivent dans `web/src`, pas dans le socle partagé `web/shared`, qui sert aussi aux cartes affichées dans Claude).

## 3. Mobile (moins de 761 px)

La barre latérale disparaît. **Barre basse fixe** de 5 emplacements (56 px de haut, zone sûre `env(safe-area-inset-bottom)`) : Accueil, Projets, Bibliothèque, Impressions, **Plus** ; icône **et** libellé toujours visibles, cibles ≥ 44 × 44 px. « Plus » ouvre une **feuille** modale (Imprimantes, Matières, Réglages, état du service, thème) : `Échap` ou « Fermer » la ferme et le focus revient au bouton « Plus ». Le fil d'Ariane devient un lien « ← Parent ». Le contenu garde une marge basse égale à la barre. Sur écran tactile (`pointer: coarse`), `--control-h` passe à 44 px (`web/shared/tokens.css`).

## 4. Vocabulaire (UX4)

Une seule table de libellés : **`web/shared/labels.ts`**. Le site, les cartes de suivi (`web/mcp-app/live.tsx`, `progress.tsx`) et les futures pages lisent la même ; il n'existe plus de `STATE_LABEL`, `QSTATE`, `STATUS`, `OUTCOME` locaux ni de `statePill` (tests dans `vocabulary.test.ts`). Pour un nouvel état : une ligne dans `labels.ts`, jamais un mot écrit dans un écran.

| Ne plus écrire | Écrire |
|---|---|
| bambud | Printalk |
| slicer, découpe | Préparation de l'impression (découpe) |
| G-code | fichier d'impression (G-code) |
| empreinte (sha256) | identifiant du fichier (détail replié) |
| HMS | alertes de l'imprimante |
| Firmware | version du logiciel de l'imprimante |
| Backend CAO | conversion des fichiers STEP (disponible / non installée) |
| Matériaux, Bobines (au premier niveau) | Matières |
| Comparaison, Variantes, Expérience | Essais de réglages (un essai a des variantes) |
| Gabarits | Pièces sur mesure (gabarits) |
| Journal | Impressions terminées |
| UPLOADED, START_UNKNOWN | Envoyée à l'imprimante, Lancement à vérifier |
| Lancer (bouton qui ouvre la confirmation) | Vérifier et lancer (+ bouclier) : c'est un lien vers la page de confirmation du serveur |
| Jobs, Travaux en cours | Préparations en cours |
| surtitre « Espace » | supprimé |

Le terme technique reste possible dans un bloc replié « Détails techniques » (exception testée) et dans la page de spécimen « Système de design ». Les cartes affichées dans Claude gardent quelques termes techniques (alignement en vague U3, UX11).

## 5. États : vide, chargement, erreur (UX5)

- **Chargement** : barre fine indéterminée en haut de l'écran à chaque changement de route et pendant le chargement d'un écran différé (`NavProgress`, `RouteFallback`) ; **squelettes** (`Skeleton`) par bloc à la place du plein écran « Chargement de l'écran » ; jamais de pourcentage inventé (`ProgressBar` indéterminée sans valeur).
- **État vide** (`EmptyState`) : toujours **trois éléments** : ce que c'est (titre), pourquoi c'est vide (texte), **une** action (bouton ou lien). Jamais un simple « — ».
- **Erreur** (`ErrorState`, utilisé par `Async`) : **trois parties** : ce qui s'est passé (titre + texte en clair), ce que ça change (`impact`), ce qu'on peut faire (« Réessayer », lien) ; le détail technique est replié (« Détails techniques »). Jamais de trace, de chemin serveur ni de secret. Une erreur d'un bloc n'efface pas le reste de la page ; l'indisponibilité du service est un bandeau unique.
- **Focus** : à chaque navigation, le focus va au h1 de la nouvelle page (lecteur d'écran et clavier). Lien d'évitement « Aller au contenu » conservé.

## 6. Règles non négociables (rappel, spec § 7)

- **Aucun bouton, carte ni raccourci du site ne lance, n'arrête ni ne reprend une impression.** « Vérifier et lancer » mène à `/confirm/{id}` (page du serveur, navigateur enregistré, 2 minutes, case « plateau vide », passkey). `api.startPrint` n'est appelé que dans l'écran des impressions, après l'accord enregistré par la page serveur (test de source dans `vocabulary.test.ts`).
- Aucun secret affiché : Réglages › Sécurité n'ouvre que les pages serveur (`/setup/launch-code`, `/setup/passkeys`, `/setup/notifications`, `/enroll`, `/agents/pair`).
- Aucune API d'IA, aucune migration, aucune dépendance npm nouvelle ; suppression = corbeille (récupérable).
- Couleurs uniquement par jetons (`var(--…)`) : pas de couleur codée en dur dans `shell.css` (test Go `css_test.go`). Thème clair et sombre soignés ; `prefers-reduced-motion` coupe halos et animations.

## 7. Tests qui gardent ce guide

`web/src/nav.test.ts` (7 entrées, graphe, non-collision, onglets), `redirects.test.tsx` (chaque ancienne URL, paramètres conservés, page Introuvable), `shell.test.tsx` (menu rendu, barre basse, feuille « Plus », fil d'Ariane, onglets, bandeau), `vocabulary.test.ts` (termes interdits, table unique, lancement humain), `web/shared/components/ui.test.tsx` (onglets au clavier, états), `internal/webui/routes_test.go` et `css_test.go` (non-collision serveur, cibles de 44 px, marge de la barre, mouvement réduit).

## 8. Vague U2 : Accueil, Impressions, recherche, fiche modèle

### Accueil (`/`, `web/src/pages/Home.tsx`, blocs dans `web/src/pages/dashboard/`)

Ordre fixe, un bloc indépendant par lecture : salutation (« 1 impression en cours, fin vers 18:08 » ou « Tout est calme. ») ; bandeau d'alerte ; **À faire** (absent s'il n'y a rien) ; **En direct** ; **Projets en cours** ; **Dernières impressions** ; **Chiffres du mois** ; **Bobines basses** (absent s'il n'y en a pas) ; raccourcis (ou les trois étapes du premier lancement quand il n'y a ni imprimante ni modèle). Retirés : « Parcours », « Prêtes à lancer », « File d'attente », « Récemment modifiés ».

- **À faire** (5 lignes, « Voir tout (n) » au-delà) : impression à confirmer (un **lien** `<a href="/confirm/…">` « Vérifier et lancer » ; sans confirmation déjà demandée, un lien vers la fiche), échec ou alerte de la caméra, **verdict à donner** (trois boutons en ligne ; « Raté » demande la raison en puces ; « Annuler » 8 s), bobine basse requise par une impression en file, projet dormant. Chaque ligne : pictogramme **et** texte.
- **Verdict en un tap** : `POST /api/prints/{id}/verdict`, puis une barre « Verdict enregistré · Annuler » pendant 8 s. **Annuler** restaure le verdict précédent, ou le retire (`DELETE /api/prints/{id}/verdict`, route additive ajoutée en U2 ; audit `insights.verdict.cleared` : l'impression redevient non jugée, la note éventuelle est conservée, aucun effet sur l'imprimante). C'est la **seule** écriture de l'Accueil.
- **En direct** : une carte par imprimante (celle qui imprime d'abord, agrandie ; les autres sur une ligne). Progression, couche, temps restant, fin estimée, températures, alertes de l'imprimante comptées. **Photo du plateau à la demande** (bouton « Photo ») ; l'actualisation toutes les 30 s est une option **désactivée par défaut** (mémorisée dans ce navigateur, seulement pendant une impression et onglet visible). Dernière **photo de palier** avec son pourcentage, ou « photo non disponible ». Titre d'onglet « 42 % · Équerre · Printalk » pendant l'impression. Les lectures (état 5 s, travaux 4 s, alertes et photos 30 s) **s'arrêtent quand l'onglet est caché** et reprennent au retour (`useVisiblePolling`). Aucun bouton n'arrête ni ne reprend : l'alerte renvoie à la fiche de l'impression, où « Demander l'arrêt » mène à la page d'arrêt du serveur.
- **Chiffres du mois** : « 7/8 jugées · 2 non jugées » (calculé sur le journal du mois : imprimée = 100 % atteint ; les non jugées sont dites, jamais comptées dans le taux ; aucune division par zéro). Chaque tuile porte son niveau en texte (mesuré, calculé, estimé). Limite : le journal est lu sur ses 200 dernières fiches (la route ne filtre pas par date).
- **Hors ligne** : chaque bloc garde sa dernière valeur avec « Dernière mise à jour à… » ; un bloc sans donnée dit « Indisponible pour l'instant » (le bandeau d'erreur est unique, celui de la coque). **Sans projets** (routes absentes) : ni bloc Projets ni verdicts, sans erreur.

### Impressions

Les quatre vues (`?vue=`) viennent de U1. Chaque ligne mène à **`/impressions/:id`**, la fiche (`web/src/pages/impressions/PrintSheet.tsx`, lecture `GET /api/prints/{id}/sheet`) : en-tête et fil d'Ariane, **verdict** (boutons en ligne), **réglages figés**, **matière** (bobines), **temps prévu et réel** et coût, **planche des 10 paliers** (« photo non disponible » + raison pour un palier manquant), alertes de la caméra (« non disponible » distingué de « aucune alerte »), accord de lancement ; puis le suivi du travail (`JobDetail`, confirmation sur la page du serveur) et Archiver / Corbeille / Restaurer (récupérables). Action principale : « Donner le verdict » si non jugée, sinon « Refaire » (mène à la préparation). Un ancien lien `/impressions?job=ID` redirige vers la fiche. **`api.startPrint` n'est appelé que dans `Print.tsx`** (composant de confirmation, inchangé ; test de source dans `vocabulary.test.ts`).

### Recherche globale

« Rechercher » (barre latérale, loupe sur mobile), `/` hors champ de saisie, Ctrl/Cmd + K. Panneau modal : flèches, Entrée, Échap (le focus revient où il était). Résultats groupés : Modèles (paramètre `q` du serveur), Projets, Impressions, Imprimantes, Matières (listes déjà servies, filtrées côté client, **bornées à 200 éléments** avec la mention « résultats limités »), Pages (table `nav.ts`). Recherche vide : les récents. Sans résultat : message et deux suggestions. Texte seulement, **aucune interprétation de phrase, aucune IA** ; la saisie est échappée à l'affichage et encodée dans les paramètres.

### Fiche modèle à 4 onglets

`/modele/:id?vue=` : **Aperçu** (3D, dimensions, analyse, sous-parties), **Fichiers et versions** (fichiers, versions, provenance et licence, modifications, journal), **Impressions** (compteurs, coût cumulé, versions et résistance, liste liée aux fiches ; `GET /api/models/{id}/sheet`), **Profils** (réglages réellement utilisés, effectifs). **Choix** : la rubrique « Infos » est fusionnée dans « Fichiers et versions » pour respecter « au plus 4 onglets » (`?vue=infos` y mène) ; « Sous-parties » reste dans Aperçu, car elle surligne le viewer 3D. En-tête : une action principale « Préparer » et un menu « … » (Essais de réglages, mettre à la corbeille).

### Réglages › Suivi (`/reglages/suivi`)

Seuil « **projet dormant** » (entier de 1 à 3650 jours, défaut 14 ; mémorisé **dans ce navigateur**, aucune route serveur : l'Accueil l'applique dans « À faire ») et **photos de paliers** (réglages existants `progress_photos`, `progress_retention_days`, `progress_cap_mb`, via `GET/PUT /api/camera/settings`). Valeur hors bornes refusée avec un message ; l'enregistrement est confirmé par un message et la valeur appliquée est affichée. Le seuil d'échantillon `min_sample_size` (« ton meilleur ») n'a pas d'API : il viendra avec PJ7.

### Tests de la vague U2

`web/src/pages/dashboard/dashboard.test.tsx` (CA-T1 à CA-T7 : ordre, « À faire », lien de confirmation, aucune écriture hors verdict, verdict et Annuler, impression en cours, visibilité de l'onglet, hors ligne, sans projets), `web/src/pages/u2.test.tsx` (recherche CA-R1 et CA-R2, fiche impression CA-I1, fiche modèle, Réglages › Suivi CA-P2), `internal/insights/verdict_clear_test.go` et `internal/httpapi/projects_test.go` (retrait du verdict).
