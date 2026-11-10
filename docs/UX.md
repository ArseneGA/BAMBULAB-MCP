# Guide d'interface de Printalk (G10 UX)

Ce guide dit **où est chaque chose**, **quels mots on emploie** et **les règles à suivre** pour ajouter ou modifier un écran. Il accompagne la spec `docs/specs/G10-refonte-navigation-tableau-de-bord.md` ; en cas de doute, la spec fait foi. État décrit ici : **vague U1** (coque, navigation, vocabulaire, début des états vides et erreurs). Le tableau de bord complet, la fiche impression, la recherche globale et la fiche modèle à onglets viennent dans les vagues U2 et suivantes.

## 1. Le menu : 7 entrées, dans cet ordre

| Entrée | Chemin | Contenu |
|---|---|---|
| Accueil | `/` | le tableau de bord (refonte en vague U2) |
| Projets | `/projets` | les projets et leur avancement (G9) ; page d'état vide tant que G9 n'est pas fusionnée |
| Bibliothèque | `/bibliotheque` | modèles et collections, import ; sous-vues **Pièces sur mesure** (`/bibliotheque/gabarits`) et **Corbeille et doublons** (`/bibliotheque/corbeille`) ; fiche d'un modèle `/modele/:id` |
| Impressions | `/impressions` | vues `?vue=` : **À faire**, **En cours et en file**, **Terminées**, **Bilan** ; assistant `/impressions/nouvelle` |
| Imprimantes | `/imprimantes` | vues `?vue=` : **Mes imprimantes**, **Modèles connus**, **Calibration** |
| Matières | `/matieres` | vues `?vue=` : **Bobines et AMS**, **Multicolore**, **Fiches matières**, **Après impression** |
| Réglages | `/reglages` | **Apparence**, **Tarifs** (`/reglages/tarifs`), **Sécurité** (`/reglages/securite`), **Agent Windows** (`/reglages/agent-windows`), **Avancé** (`/reglages/avance`, avec Dysfonctionnements et Système de design) |

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
