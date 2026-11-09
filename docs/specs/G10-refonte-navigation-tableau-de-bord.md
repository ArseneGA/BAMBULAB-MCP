# G10 — Refonte de la navigation et vrai tableau de bord (interface web)

Spec pour un agent de code. Lire d'abord `docs/specs/README.md` § 2 (règles communes), `docs/specs/G9-suivi-de-projet.md` (en entier : G10 est le cadre qui accueille projets, fiche pièce, fiche impression, verdict, tableau de bord PJ1, fil PJ3, recherche PJ4), `docs/SECURITY.md` (approbation humaine), `web/shared/README.md` (socle de design), `docs/BACKLOG.md` (règle « aucune API d'IA dans Printalk »). Demande de l'utilisateur du 2026-11-09. Le dépôt a été lu le 2026-11-09 ; l'agent relit `git log` et les fichiers cités avant de coder. **Cette spec est de la documentation : aucun code n'a été écrit pour la produire.**

Codes retenus : **UX1 à UX12** (préfixe « UX » = expérience / navigation). Aucun conflit dans `docs/BACKLOG.md` ni dans `docs/` (recherche faite le 2026-11-09 ; `UX` n'est utilisé nulle part).

**Version de G9 prise en compte** (relue le 2026-11-09, après les réponses de l'utilisateur) : codes **PJ1 à PJ13** ; **sous-projets** (`projects.parent_id`, D9) ; **déclinaisons d'un assemblage par objectif** (PJ13, carte `variants.html`, « Préparer cette déclinaison », arrêt à la confirmation humaine) ; **« imprimée » = l'impression a atteint 100 %, verdict ou pas** (D7) ; **journal photo à chaque 10 %** (D8, planche de paliers dans la fiche impression) ; seuil « ton meilleur » **réglable** (`min_sample_size`, défaut 3). Si G9 évolue encore, c'est G9 qui fait foi pour la logique ; G10 ne fixe que la coque et la présentation.

| Code | Nom | Vague G10 | Vague G9 qu'elle suit |
|---|---|---|---|
| UX1 | Socle de navigation (table de navigation unique, fil d'Ariane, en-tête de page, barre de chargement, page « introuvable ») | U1 | avant la vague 1 (G9 y branche « Projets ») |
| UX2 | Nouvelle arborescence et redirections de toutes les anciennes URL | U1 | idem |
| UX3 | Navigation mobile (barre basse, feuille « Plus ») | U1 | idem |
| UX4 | Vocabulaire sans jargon et libellés | U1, continu | — |
| UX5 | États vides, chargements visibles, erreurs lisibles | U1 puis U2 | — |
| UX6 | Tableau de bord « Accueil » (v1 puis complet) | U2 (v1), U3 (complet) | v1 après vague 1 (verdict) ; complet avec PJ1 (vague 3) |
| UX7 | Impressions : à faire / en cours / terminées / bilan, et fiche impression | U2 | vague 1 (PJ10, PJ11) |
| UX8 | Regroupements Matières, Imprimantes, Bibliothèque, Réglages | U2 | — |
| UX9 | Recherche globale | U2 (v1), U4 (filtres PJ4) | vague 4 (PJ4) |
| UX10 | Pages serveur (connexion, confirmation, consentement OAuth, passkeys…) alignées sur la charte | U3 | — |
| UX11 | Cartes MCP et pages web : composants partagés `@shared` | U3 | vagues 1 à 3 (cartes G9) |
| UX12 | Accessibilité, mode sombre, recette visuelle | transversal, recette U4 | — |

## 1. Objectif et valeur

Aujourd'hui l'interface est un **inventaire de fonctions** (11 entrées de menu, 19 routes, 5 onglets « Coûts », 6 onglets « Impressions ») rangé selon l'histoire du développement. Un maker qui arrive doit deviner où est « ma dernière impression » ou « où en est mon projet ». Avec G9, l'interface doit devenir **un atelier organisé autour de ce que fait l'humain** : (1) voir ce qui se passe maintenant, (2) savoir ce qui attend un geste de lui, (3) retrouver un projet, une pièce, une impression, (4) régler. Objectifs mesurables :

- **7 entrées de premier niveau** (au lieu de 11 + un groupe « Plus »), chacune compréhensible sans explication ;
- **Aucune fonction perdue**, **aucune URL cassée** (table de redirections § 4.3) ;
- Un **tableau de bord** qui répond en 5 secondes à : « mon imprimante fait quoi ? », « qu'est-ce que je dois faire ? », « où en sont mes projets ? » ;
- Le **verdict d'une impression** donné en un geste depuis l'accueil (alimente G9) ;
- **Mobile utilisable** (le P1S se surveille depuis un téléphone) et **mode sombre** soigné ;
- Mêmes composants et mêmes mots dans le site, les pages serveur et les cartes affichées dans Claude.

## 2. Périmètre / hors périmètre

Dans le périmètre : arborescence, menu, routes et redirections, en-têtes de page, fil d'Ariane, recherche globale, tableau de bord, regroupement des écrans existants en onglets, états vides/chargement/erreur, vocabulaire, accessibilité, mode sombre, harmonisation visuelle des pages serveur (`internal/humanauth/theme.go` et gabarits), composants partagés site ↔ cartes, tests.
Hors périmètre : toute logique métier nouvelle (G9 la fournit : projets, verdict, fiches, fil, recherche) ; changement du **flux d'approbation** (§ 7) ; nouvelle marque ou nouveau logo (DA verte inchangée, `docs/logo`, `docs/maquette`) ; refonte de l'API HTTP existante ; application mobile native ; **toute API d'IA**.

## 3. Inventaire de l'existant (lu le 2026-11-09)

### 3.1 Coque et navigation (`web/src/Shell.tsx`, `main.tsx`, `shell.css`)

Barre latérale vitrée en 3 groupes : **principal** (Accueil, Bibliothèque + compteur, Préparer, Impressions), **« Atelier »** (Imprimantes, Bobines, Coûts), **« Plus »** (Gabarits, Matériaux, Corbeille, Réglages). Pied : trois pastilles d'état (service, Windows, CAO), « Enregistrer ce navigateur », « Réseau local / bambud <version> », bascule de thème. Titre d'onglet via `TITLES`. Pas de recherche globale, pas de fil d'Ariane commun (deux fils écrits à la main dans `Model.tsx` et `Compare.tsx`), pas de page « introuvable » (`path="*"` renvoie silencieusement à `/`), repli de chargement plein écran « Chargement de l'écran » pour les écrans différés, `Boundary` qui propose de recharger. Mobile (< 760 px) : la barre latérale devient une barre **haute collante, icônes seules** (le libellé n'apparaît que pour l'écran actif), pas de zone tactile de 44 px garantie, pastilles d'état et version masquées.

### 3.2 Écrans web (tous)

| Écran (route, fichier) | Ce qu'il montre | Actions | Défauts d'ergonomie visibles dans le code |
|---|---|---|---|
| **Accueil** `/` (`Home.tsx`) | « Bonjour » + date ; 4 chiffres du mois (impressions, filament, coût, taux de réussite) ; imprimante en direct + photo du plateau ; file d'attente ; travaux en cours ; « Prêtes à lancer » ; 4 dernières impressions ; 5 modèles récents ; « Parcours » (6 raccourcis numérotés) | Importer un modèle ; « Lancer » (ouvre la page de confirmation) ; photo à la demande ; Tout voir | 9 blocs empilés **sans priorité** ; « File d'attente » et « Prêtes à lancer » **se recoupent** (deux listes avec « Lancer ») ; « Parcours » **double le menu** ; aucun projet, aucun verdict ; « Taux de réussite » sur `Outcome` technique, sans effectif des non jugées ; une seule imprimante affichée (la première ou celle qui imprime) |
| **Bibliothèque** `/bibliotheque` (`Library.tsx`) | recherche, arbre de collections, grille/liste, 4 filtres (format, tri, disponibilité, affichage), import | importer, ouvrir, filtrer | surtitre « Espace » sans sens ; 4 contrôles de filtre au même niveau ; état dans l'URL (`q c f a s t p`) : **à conserver** |
| **Fiche modèle** `/modele/:id` (`Model.tsx`, `ModelTools.tsx`, `ExportDialog.tsx`) | aperçu 3D ; 8 panneaux empilés : Dimensions, Analyse géométrique, Sous-parties, Fichiers associés, Versions (immuables), Provenance et licence, Modifier, Journal | Comparer, Préparer, restaurer, exporter, modifier | **page fourre-tout** (8 panneaux, aucun regroupement) ; aucune impression ni coût de la pièce ; « Comparer » ouvre « la dernière expérience » sans dire laquelle ; fil d'Ariane écrit à la main |
| **Préparation** `/preparation` (`Prepare.tsx`, `PrepareParts.tsx`, `PlatePanel.tsx`) | choix du modèle puis objectif de finition, critères, plateau | préparer, découper | étape d'un parcours **présentée comme une destination de premier niveau** ; sous-menu propre `PrepareNav` (Objectif / Variantes) ; paramètre `?model=` |
| **Comparaison** `/comparaison` (`Compare.tsx`, `RecordResult.tsx`) | expériences de découpe, variantes, mesures, enregistrement du résultat réel | rouvrir, enregistrer | **absente du menu** (accessible depuis la fiche modèle, l'accueil, PrepareNav) ; mot « Comparaison » vs « Variantes » vs « Expérience » (3 mots, 1 notion) ; paramètre `?experiment=` |
| **Impressions** `/impressions` (`Print.tsx`, `CompletedBox.tsx`, `UploadLauncher.tsx`) | formulaire « nouveau travail » ; historique à **6 onglets** (Toutes, En cours, Terminées, Échecs, Archivées, Corbeille) + « grouper par modèle » ; détail d'un travail en bas de page avec confirmation | créer un travail, découper, déposer, archiver, corbeille, lancer (via confirmation) | **création + historique + détail sur une seule page** ; détail non adressable par URL (état local `jid`) ; un 2e concept de « Corbeille » (impressions) en plus de celle des modèles ; libellés techniques d'état (`UPLOADED`→« déposée sur l'imprimante », `START_UNKNOWN`) ; pas de verdict |
| **Imprimantes** `/imprimantes` (`Printers.tsx`, `CameraPanel.tsx`) | cartes : état, modèle, numéro de série, buse, AMS, firmware, code d'accès (enregistré / à saisir), caméra, progression | ajouter, saisir le code (page serveur), caméra | jargon (« Firmware », « Numéro de série » au même plan que l'état) ; renvoie vers « Bobines » dans une phrase ; **les modèles de machines connus sont ailleurs** (Matériaux) |
| **Bobines** `/bobines` et **File** `/file` (`Filament.tsx`, même composant) | titre « Bobines, couleurs et file d'attente » ; 3 onglets : Bobines et AMS, Multicolore, **File d'attente** | inscrire une bobine, affecter AMS, mettre en file | la **file d'attente** (des impressions) est rangée sous la matière ; `/file` est une 2e URL du même écran, **absente du menu** ; « Espace » en surtitre |
| **Coûts** `/couts` (`Insights.tsx` + 5 fichiers) | 5 onglets : Vue d'ensemble, Usage, **Journal**, **Prix et coût**, **Post-traitement** | régler tarifs, filtrer le journal, estimer un coût | **onglets fourre-tout** : le Journal **double** l'historique des impressions et les « Dernières impressions » de l'accueil ; « Prix et coût » est un **réglage** (tarifs, puissances) rangé dans un rapport ; « Post-traitement » n'a aucun rapport avec les coûts ; paramètre `?onglet=` |
| **Matériaux** `/materiaux` (`Materials.tsx`) | 2 onglets : Machines (modèles Bambu connus, parc), Matériaux (fiches techniques) | consulter | les **machines** dupliquent « Imprimantes » ; rangé dans « Plus » ; « Matériaux » vs « Bobines » vs « matières » (3 mots) |
| **Gabarits** `/gabarits` (`Gabarits.tsx`) | 2 onglets : Gabarits (pièces paramétriques), Calibration | créer une pièce, générer un jeu de calibration | **2 fonctions sans rapport** (créer une pièce / calibrer une imprimante) ; titre en `h1.page-title` au lieu de l'en-tête commun ; dans « Plus » |
| **Corbeille** `/corbeille` (`Trash.tsx`) | 2 onglets : Corbeille (modèles), Doublons exacts | restaurer, fusionner | dans « Plus » alors qu'elle dépend de la Bibliothèque |
| **Agent Windows** `/agents` (`Agents.tsx`) | agents appairés, dossiers indexés, copies, travaux | appairer (page serveur `/agents/pair`), copier | **absent du menu** : accessible par un lien des Réglages ou la pastille d'état ; réservé à une minorité d'utilisateurs, au même niveau que le reste |
| **Réglages** `/reglages` (`Settings.tsx`) | apparence (thème), état du service, **5 liens** vers des écrans annexes | thème, enregistrer le navigateur | **page-menu** (liste de liens vers des écrans qui sont déjà dans « Plus ») ; ni tarifs, ni sécurité (code de lancement, passkeys, notifications) : ceux-ci sont des **pages serveur** introuvables depuis l'interface |
| **Design** `/design` (`Design.tsx`) | jetons et composants | — | page de développement, **exposée en production** et listée dans les Réglages |
| Redirections existantes | `/modele`→`/bibliotheque`, `/impression`→`/impressions`, `/tableau-de-bord`→`/couts`, `*`→`/` | | à **conserver** (§ 4.3) |

### 3.3 Pages serveur (gabarit `internal/humanauth/theme.go`, `Layout`)

| Page | Fichier | Rôle | Défauts visibles |
|---|---|---|---|
| `/enroll` | `humanauth/web.go` | enregistrer ce navigateur (code à usage unique du terminal) | page isolée, sans lien « retour à Printalk » ; texte technique |
| `/confirm/{id}` | `printing/pages.go` | **approbation humaine du lancement** : réglages, températures lues dans le G-code, photo du plateau, case « plateau vide », 2 minutes, passkey ou code personnel | libellés « G-code », « slicer », « Empreinte » ; aucun fil d'Ariane ni lien vers la fiche de l'impression ; **à ne pas alléger** (§ 7) |
| `/stop/{id}`, `/action/{id}` | `printing/pages.go`, `actions.go` | arrêt / actions sur l'impression en cours, confirmées par l'humain | idem |
| `/setup/printer/{id}`, `/setup/camera/{id}` | `printing/pages.go`, `camera/pages.go` | code d'accès LAN, certificat de caméra | ouverts dans un nouvel onglet depuis Imprimantes, sans retour |
| `/setup/launch-code`, `/setup/passkeys`, `/setup/notifications` | `humanauth`, `notify` | code de lancement personnel, passkeys, secrets des canaux de notification | **aucun lien depuis l'interface** (introuvables si on ne connaît pas l'URL) |
| `/agents/pair` | `httpapi/pairpage.go` | appairer un agent Windows | lié depuis Agents seulement |
| `/oauth/consent/{id}` | `httpapi/oauth.go` | consentement OAuth du connecteur distant | texte de consentement à garder exact |
| `/health`, « HTTPS requis », « Interface non construite », `/about`, `/privacy` | `httpapi`, `webui` | état, avertissements, pages publiques | gabarit commun, mêmes réserves |
| **Style** | `theme.go` (`Style`, jetons recopiés : `--ok-text`, `--warn-line`…) | feuille de style **dupliquée** à la main : palette de `web/shared/tokens.css` recopiée, boutons à 34 px, rayon 22 px | **dérive possible** entre site, pages serveur et cartes (aucun test de parité) |

### 3.4 Cartes MCP (`internal/mcpserver/card_views.go`, `web/mcp-app/cards/*.tsx`)

`library`, `compare`, `costs`, `spools`, `prints`, `printers` (cartes « compactes », `lite.css`), `atelier` (`show_atelier`, `internal/mcpserver/ui/atelier.html`), `model` (+ viewer 3D), `print-confirm`, `progress`, `print-live`, `print-stop`, `layers`, `gcode`, `drop`, `strength`, `form`, `search` ; G9 ajoute `project`, `part`, `print-sheet`, `verdict`, `reprint`, `profiles`, `variants`, `workshop`, `timeline`, `part-tree`, `print-diff`. Constat : les cartes réutilisent `web/shared` (`Card`, `Badge`, `ProgressBar`…), mais les **écrans du site** (`Home.tsx`, `Printers.tsx`, `Print.tsx`) écrivent leurs propres pastilles (`statePill`), tuiles de chiffres (`.stat`), lignes de liste (`.rowlist`) et libellés d'état (`STATE_LABEL`, `QSTATE`, `STATUS`, `OUTCOME` : **6 tables de libellés** dans 6 fichiers). Résultat : le même état n'a pas toujours le même mot dans le site et dans une carte.

### 3.5 Synthèse des défauts

1. Trop d'entrées (11) et un groupe « Plus » qui cache des fonctions courantes.
2. Écrans **hors menu** (Comparaison, File, Agents, Design) et pages serveur **introuvables** (sécurité).
3. **Onglets fourre-tout** (Coûts ×5, Impressions ×6, Gabarits, Matériaux, Corbeille).
4. **Doublons** : journal ×3 (accueil, Impressions, Coûts), file ×2 (accueil), machines ×2, corbeille ×2, raccourcis ≈ menu.
5. Pas de **projet**, pas de **verdict**, pas de **fiche** d'impression adressable.
6. Jargon (G-code, slicer, firmware, AMS, empreinte, HMS, bambud, CAO, backend) et 3 mots pour une notion (matières / matériaux / bobines ; comparaison / variantes / expérience).
7. Mobile : icônes seules, pas de recherche, pas de zone tactile garantie.
8. Trois dialectes visuels (site, pages serveur, cartes) sans test de parité.

## 4. Nouvelle architecture d'information

### 4.1 Principe

Organiser par **objet que l'humain reconnaît** (un projet, un modèle, une impression, une imprimante, une bobine), pas par fonction technique. Une **étape d'un parcours** (préparer, comparer des variantes, découper) n'est **pas** une destination : c'est une action sur un objet (« Préparer » depuis la fiche du modèle) ou un assistant (« Nouvelle impression »). Un **réglage** va dans Réglages, jamais dans un rapport.

### 4.2 Premier niveau : 7 entrées

| # | Entrée | Contenu | Pourquoi |
|---|---|---|---|
| 1 | **Accueil** | tableau de bord (§ 5) | le point de départ unique |
| 2 | **Projets** | liste en **arbre** (projets et sous-projets, D9), fiche projet (G9 PJ2) à onglets **Pièces · Déclinaisons (PJ13) · Activité (PJ3) · Notes**, avancement « 4/6 pièces imprimées » | nouvelle entrée de premier rang, demandée par G9 |
| 3 | **Bibliothèque** | modèles et collections, fiche du modèle (= fiche pièce G9 PJ5), import ; sous-vues : **Gabarits**, **Corbeille et doublons** | tout ce qui est un fichier ou une pièce |
| 4 | **Impressions** | **À faire**, **En cours et en file**, **Terminées**, **Bilan** ; fiche impression (G9 PJ10) ; assistant « Nouvelle impression » | tout ce qui est une impression, du dépôt au verdict |
| 5 | **Imprimantes** | mes imprimantes (état, caméra, code d'accès), **modèles connus** (ex-Matériaux › Machines), **Calibration** (ex-Gabarits › Calibration) | tout ce qui est une machine |
| 6 | **Matières** | **Bobines et AMS**, **Multicolore**, **Fiches matières** (ex-Matériaux › Matériaux), **Après impression** (ex-Coûts › Post-traitement) | tout ce qui est de la matière |
| 7 | **Réglages** | Apparence, **Tarifs** (ex-Coûts › Prix et coût), **Suivi** (seuil « projet dormant », seuil d'échantillon `min_sample_size`, conservation des photos de paliers : réglages de G9), **Sécurité** (ex-pages serveur introuvables), **Agent Windows**, **Avancé** (service, design) | tout ce qui se règle une fois |

Ajustements par rapport à la liste proposée par la demande (Accueil, Projets, Bibliothèque, Imprimantes, Matières, Historique, Réglages) : **« Historique » devient « Impressions »**, car l'écran contient aussi ce qui est à faire et en cours (une page « Historique » ne porterait pas la file ni les verdicts à donner) ; « Coûts » **disparaît comme entrée** : son contenu se répartit entre **Bilan** (Impressions), **Tarifs** (Réglages) et **Après impression** (Matières). Pas de « Préparer » ni « Comparer » dans le menu.

Règles : 7 entrées maximum au premier niveau (la liste n'en reçoit une 8e qu'après retrait d'une autre) ; chaque page a **au plus 4 onglets** ; un onglet est une **vue du même objet** (jamais une fonction étrangère) ; l'onglet actif est dans l'URL (`?vue=`), ce qui permet le lien direct et le retour arrière ; l'ordre des entrées ne change jamais selon l'état.

### 4.3 Table ancien → nouveau (rien de perdu, aucune URL cassée)

Toutes les anciennes routes **restent valides indéfiniment** : une redirection côté client (`<Navigate replace>`) qui **conserve les paramètres** de la requête. Un test itère sur cette table.

| Ancienne URL | Nouvelle destination | Remarque |
|---|---|---|
| `/` | `/` | tableau de bord refondu |
| `/bibliotheque` (+ `?q c f a s t p`) | `/bibliotheque` (mêmes paramètres) | inchangé |
| `/modele` | `/bibliotheque` | redirection existante conservée |
| `/modele/:id` | `/modele/:id` | URL canonique conservée (liens existants) ; onglets `?vue=apercu\|fichiers\|impressions\|profils\|infos` |
| `/corbeille` | `/bibliotheque/corbeille` | onglets Corbeille / Doublons exacts |
| `/gabarits` | `/bibliotheque/gabarits` | seul l'onglet Gabarits |
| `/gabarits` (onglet Calibration) | `/imprimantes/calibration` | l'onglet était un état local : `/gabarits` va aux Gabarits, la calibration est annoncée en bandeau d'une version |
| `/preparation?model=ID` | `/modele/ID/preparer` | assistant « Préparer » sous le modèle |
| `/preparation` (sans modèle) | `/impressions/nouvelle` | l'assistant commence par « Choisir un modèle » (`PickModel` actuel) |
| `/comparaison?experiment=ID` | `/modele/<id du modèle de l'expérience>/essais/ID` | composant `LegacyCompare` qui lit l'expérience puis redirige (repli : `/bibliotheque`) |
| `/comparaison` | `/bibliotheque` | repli ; la liste des essais est dans la fiche du modèle |
| `/impressions` | `/impressions` | `?vue=a-faire\|en-cours\|terminees\|bilan` ; défaut **À faire** s'il y a quelque chose, sinon **En cours** |
| `/impression` | `/impressions` | existante conservée |
| `/impressions` + travail ouvert en bas de page | `/impressions/:id` | le détail devient adressable (G9 PJ10) |
| `/file` | `/impressions?vue=en-cours` | |
| `/imprimantes` | `/imprimantes` | |
| `/bobines` | `/matieres` | vue Bobines et AMS |
| `/materiaux` | `/matieres?vue=fiches` | les **machines** de cet écran sont dans `/imprimantes?vue=modeles` |
| `/couts` | `/impressions?vue=bilan` | |
| `/couts?onglet=dashboard` | `/impressions?vue=bilan#usage` | |
| `/couts?onglet=journal` | `/impressions?vue=terminees` | le journal est la liste des terminées + fiche impression |
| `/couts?onglet=costs` | `/reglages/tarifs` | |
| `/couts?onglet=post` | `/matieres?vue=apres-impression` | |
| `/tableau-de-bord` | `/impressions?vue=bilan` | existante (`→/couts`) : chaîne raccourcie |
| `/agents` | `/reglages/agent-windows` | |
| `/design` | `/reglages/avance/design` | page conservée, sortie du menu |
| `*` (inconnu) | **page « Introuvable »** avec recherche et liens vers les 7 entrées | ne renvoie plus silencieusement à `/` |

Pages **serveur** : leurs URL **ne changent pas** (`/enroll`, `/confirm/{id}`, `/stop/{id}`, `/action/{id}`, `/setup/*`, `/agents/pair`, `/oauth/*`, `/health`, `/about`, `/privacy`) ; les liens et courriels de notification déjà envoyés restent valides. Aucune route SPA nouvelle ne doit commencer par `/setup`, `/confirm`, `/stop`, `/action`, `/enroll`, `/oauth`, `/api`, `/mcp`, `/health` : test de non-collision (§ 9).

### 4.4 Arborescence cible (profondeur ≤ 3, tout atteignable en 2 clics depuis le menu)

```
Accueil
Projets            /projets (arbre)               › /projets/:id (Pièces · Déclinaisons · Activité · Notes) › sous-projet /projets/:id › pièce → /modele/:id
Bibliothèque       /bibliotheque                  › /modele/:id (Aperçu · Fichiers et versions · Impressions · Profils · Infos)
                   /bibliotheque/gabarits · /bibliotheque/corbeille       › /modele/:id/preparer · /modele/:id/essais/:exp
Impressions        /impressions (À faire · En cours et en file · Terminées · Bilan)
                   /impressions/nouvelle · /impressions/:id
Imprimantes        /imprimantes (Mes imprimantes · Modèles connus · Calibration)
Matières           /matieres (Bobines et AMS · Multicolore · Fiches matières · Après impression)
Réglages           /reglages (Apparence) · /reglages/tarifs · /reglages/securite · /reglages/agent-windows · /reglages/avance
```

La **fiche du modèle** passe de 8 panneaux empilés à **4 onglets + en-tête** : **Aperçu** (3D, dimensions, analyse), **Fichiers et versions**, **Impressions** (compteurs, coût cumulé, verdicts : G9 PJ5/PJ8), **Profils** (PJ7, essais de réglages), **Infos** (provenance et licence, étiquettes, notes, journal). Les panneaux « Modifier » et « Sous-parties » vont dans Fichiers et versions. En-tête fixe : nom, étiquettes, **une** action principale (« Préparer »), menu « … » (exporter, corbeille).

**Vocabulaire des objets** : « **Modèle** » dans la Bibliothèque (ce qu'on importe) ; « **pièce** » seulement dans un projet (un modèle avec une quantité voulue). C'est le même objet (G9 : pièce = `model`) ; la fiche s'intitule « Modèle » dans la Bibliothèque et « Pièce : <nom> » depuis un projet. L'URL reste `/modele/:id`.

### 4.5 Navigation, repères, fil d'Ariane

- **Table de navigation unique** `web/src/nav.ts` (entrées, icônes, routes, parents, titres d'onglet, mots-clés de recherche). Le menu, la navigation mobile, le fil d'Ariane, `TITLES` et la recherche la lisent ; **G9 et les chantiers futurs ajoutent une ligne ici**, pas dans `Shell.tsx`.
- **Barre latérale** (≥ 761 px) : logo, 7 entrées avec icône **et** libellé, compteurs discrets (Projets en cours, Impressions « à faire » en pastille), bascule de thème, pied réduit à une ligne d'état (« Tout fonctionne » / « 1 problème » → Réglages › Avancé ; le détail des 3 pastilles y est). La version est dans Réglages › Avancé, plus dans la barre. Le mot « bambud » n'apparaît plus dans l'interface (« Printalk »).
- **Fil d'Ariane** : composant partagé `Breadcrumb` dans l'en-tête de page, visible dès la profondeur 2 (ex. *Projets › Étagère salon › Équerre*, *Bibliothèque › Atelier › Support*, *Impressions › 9 nov. · Équerre*). Le dernier élément n'est pas un lien (`aria-current="page"`). Sur mobile : un lien « ← Parent » à la place.
- **Retour depuis les pages serveur** : chaque page serveur affiche un lien « ← Retour à Printalk » (vers la page d'où l'on vient quand le paramètre `next` est sûr, sinon `/`), et la confirmation propose « Voir la fiche de l'impression ».
- **En-tête de page standard** `PageHeader` (titre `h1`, phrase d'aide **d'une ligne**, **une action principale** à droite, onglets en dessous). Suppression du surtitre « Espace » et de `h1.page-title`.
- **Une action principale par écran** (bouton plein) ; les autres sont secondaires ou dans un menu « … ».

| Écran | Action principale |
|---|---|
| Accueil | selon l'état : « Vérifier et lancer » (confirmation en attente) ; sinon « Nouvelle impression » |
| Projets / fiche projet | « Nouveau projet » / « Ajouter une pièce » (onglet Déclinaisons : « Préparer cette déclinaison », qui **prépare** et mène à la confirmation, jamais ne lance) |
| Bibliothèque | « Importer » |
| Fiche modèle | « Préparer » |
| Impressions | « Nouvelle impression » |
| Fiche impression | « Donner le verdict » (si non jugée) sinon « Refaire » (prépare, PJ6) |
| Imprimantes | « Ajouter une imprimante » |
| Matières | « Inscrire une bobine » |
| Réglages | aucune (enregistrement automatique + confirmation discrète) |

### 4.6 Navigation mobile (< 761 px)

**Barre basse fixe** de 5 emplacements, 56 px de haut, zones ≥ 44 × 44 px, icône **+ libellé toujours visibles** : Accueil, Projets, Bibliothèque, Impressions, **Plus**. « Plus » ouvre une **feuille** (dialogue modal accessible) : Imprimantes, Matières, Réglages, état du service, thème. En haut : titre de page, bouton **recherche** (loupe), fil d'Ariane réduit à « ← Parent ». La barre haute actuelle (icônes seules, collante) disparaît. Les pastilles « à faire » restent visibles sur « Accueil » et « Impressions ». Zone sûre (`env(safe-area-inset-bottom)`) respectée ; le contenu garde une marge basse égale à la barre. Les tableaux denses deviennent des **listes de cartes** (pas de défilement horizontal de page).

### 4.7 Recherche globale (UX9)

- Champ **dans l'en-tête** (≥ 761 px) ou **loupe** (mobile) ; raccourcis **`/`** et **Ctrl/Cmd + K** ouvrent un panneau de recherche (focus piégé, `Échap` ferme, flèches + `Entrée`).
- **v1 (vague U2)** : recherche **par texte** sur ce qui existe déjà (`api.models` avec `q`, journal des impressions, liste des projets, imprimantes, bobines, pages de la table `nav.ts`), résultats **groupés** : Modèles · Projets · Impressions · Imprimantes · Matières · Pages. Pas de requête libre côté serveur au-delà des routes de lecture existantes (à vérifier au code : paramètre de recherche de chaque route ; sinon filtrer côté client sur la liste déjà chargée, bornée à 200 éléments, avec mention « résultats limités »).
- **v2 (vague U4)** : après PJ4 (`search_workshop`), le panneau propose les **filtres typés** (matière, verdict, période, projet, étiquette) ; la saisie reste du texte et des **filtres choisis** : **aucune interprétation de phrase par le serveur** (règle « aucune IA » ; la phrase en langage naturel reste le rôle de Claude dans la conversation).
- État vide : « Aucun résultat pour « … » » + suggestions (« Importer un modèle », « Voir toutes les impressions »).

### 4.8 États vides, chargements visibles, erreurs lisibles (UX5)

- **Chargement** : (1) une **barre de progression fine** en haut de l'écran à chaque changement de route et chaque chargement différé (remplace le plein écran « Chargement de l'écran ») ; (2) des **squelettes** par bloc (pas un spinner global) pour que la page ne saute pas ; (3) pour les opérations longues (import, découpe, dépôt, export, aperçu 3D) la **`ProgressBar` partagée** avec phase en clair et reste estimé, **indéterminée** quand on ne sait pas (jamais de pourcentage inventé) ; (4) `aria-busy` et zone `role="status"` annoncent l'avancement ; (5) un indicateur « Mis à jour à 14:32 » sur les blocs en direct.
- **États vides** : toujours **trois éléments** : ce que c'est, pourquoi c'est vide, **une** action (bouton) ; ex. Projets : « Aucun projet. Un projet regroupe les pièces d'un même objet et suit son avancement. [Créer un projet] ». Aucun état vide n'est un simple « — ».
- **Erreurs** : trois parties : **ce qui s'est passé** (en clair), **ce que ça change**, **ce qu'on peut faire** (« Réessayer », lien) ; détail technique replié (« Détails techniques »). Bandeau global existant (`ServerBanner`, `HttpsBanner`) conservé ; **mode hors ligne** lisible (service injoignable : les données déjà affichées restent, marquées « Dernière mise à jour… »). Une erreur d'un bloc **n'efface pas** le reste de la page (le tableau de bord montre chaque bloc indépendamment). Jamais de trace, de chemin serveur ni de secret dans un message.
- **Page « Introuvable »** et erreur de chunk : message + « Recharger » + retour Accueil.

## 5. Le tableau de bord (Accueil, UX6)

### 5.1 Priorités visuelles

Ordre **fixe**, de haut en bas (une colonne sur mobile ; sur ≥ 1100 px, grille 12 colonnes : blocs 1 et 2 en pleine largeur ou 8/4) :

| # | Bloc | Contenu précis | Données (existant → G9) | Vide / dégradé | Action |
|---|---|---|---|---|---|
| 0 | **Salutation** (une ligne) | « Bonjour », date ; si impression : « 1 impression en cours, fin vers 16:40 » | `jobs`, `printer status` | « Tout est calme. » | — |
| 1 | **À faire** (affiché **seulement s'il y a quelque chose**) | liste priorisée, max 5 lignes + « Voir tout (n) » : **(a)** impression à **confirmer** (lien « Vérifier et lancer » vers `/confirm/{id}`) ; **(b)** impression en **échec ou alerte caméra** à examiner ; **(c)** **verdict à donner** (3 boutons inline : Réussi · Raté · À refaire ; « Raté » demande la raison en puces) ; **(d)** **bobine basse** requise par une impression en file ; **(e)** **projet dormant** (> N jours, N = 14 réglable). Chaque ligne : pictogramme **+ texte** (jamais la couleur seule), nom, âge | `UPLOADED/AWAITING_PRINT_CONFIRMATION` (existant), journal + `camera_alerts` (existant), `get_pending_verdicts` (PJ11), `spools` + file (existant), `projects` (PJ2) | bloc **absent** ; sinon « Rien n'attend. » | selon la ligne |
| 2 | **En direct** | **une carte par imprimante** : nom, état en mots (« Imprime », « Prête », « Pause », « Injoignable »), **photo du plateau** (instantané du serveur), **barre de progression**, **couche x/y**, temps restant, **fin estimée**, nom de l'impression + lien vers sa fiche, buse/plateau (valeurs et cibles), alertes de l'imprimante comptées en clair. Imprimante qui imprime : carte **agrandie** (2/3 de la largeur), les autres **compactes** (une ligne). Aucune imprimante : état vide « Ajouter une imprimante » | `usePrinterStatus` (5 s), `camera/snapshot` (existants) | « Imprimante injoignable » + « Dernier relevé à… » ; pas de photo : message existant | **liens seulement** vers la page serveur d'arrêt (§ 7) ; « Voir la fiche » |
| 3 | **Projets en cours** | 3 cartes max, **projets racines** (statut « En impression » en premier, puis les plus récents) : nom, **barre « 4/6 pièces imprimées »** (définition de G9 D7 : impression ayant atteint 100 %, + « dont 3 jugées réussies » et « non jugées » si > 0 ; agrégée sur les sous-projets, chaque sous-projet repliable avec sa propre barre), coût à date, **prochaine action** (« Imprimer 2 × support »), mention « 3 déclinaisons » si PJ13 ; pastille de statut (mot + forme) ; lien « Tous les projets » | PJ2 `get_project`/`list_projects` | « Aucun projet en cours. [Créer un projet] » ; **sans G9** : bloc masqué | ouvrir le projet |
| 4 | **Dernières impressions** | 5 lignes : miniature, nom, durée, matière, **résultat en mots**, **verdict** (bouton ou pastille ; modifiable) ; « Tout voir » → Impressions › Terminées | `print_logs` + `verdict` (PJ11) | « Le journal se remplit à la fin de chaque impression. » | donner / changer le verdict |
| 5 | **Chiffres du mois** | 4 tuiles : **Impressions** (n), **Taux de réussite** (« 7/8 jugées · 2 non jugées » : les non jugées sont dites, jamais comptées dans le taux), **Heures machine**, **Matière et coût** (g, kg, €) ; chaque tuile porte son **niveau** (mesuré / calculé / estimé) en infobulle texte ; lien « Bilan » | `insights` (existant), `get_workshop_dashboard` (PJ1) | mois vide : tuiles à « 0 » avec phrase, pas de division par zéro | ouvrir le bilan |
| 6 | **Bobines basses** | bandeau de pastilles (couleur + nom + « il reste ~60 g ») sous le seuil ; vide = bloc absent | `spools` | absent | ouvrir Matières |
| 7 | **Raccourcis** | uniquement **Nouvelle impression** et **Importer un modèle** (boutons) ; le « Parcours » en 6 cartes est **supprimé** (doublon du menu) ; **premier lancement** (aucune imprimante ni modèle) : 3 étapes « Ajouter une imprimante · Importer un modèle · Préparer une impression » | — | — | — |

Retraits par rapport à l'accueil actuel : « Prêtes à lancer » et « File d'attente » (fusionnées dans **À faire** et dans Impressions › En cours et en file) ; « Récemment modifiés » (devenu la recherche et la Bibliothèque) ; « Parcours ».

### 5.2 Comportement pendant une impression

- **Hiérarchie** : le bloc **En direct** devient le **héros** de la page (taille, première position après « À faire »). Le reste reste visible, plus bas.
- **Fraîcheur** : état imprimante toutes les 5 s, travaux toutes les 4 s **seulement quand la page est visible** (Page Visibility : pause quand l'onglet est caché, reprise immédiate au retour) ; le **titre de l'onglet** devient « 42 % · Équerre · Printalk » pendant l'impression.
- **Photo du plateau** : rafraîchie **à la demande** (bouton « Photo »), comme aujourd'hui (le flux n'est jamais ouvert en arrière-plan). Le rafraîchissement automatique (30 s, onglet visible seulement) est **proposé en option désactivée par défaut** (question ouverte 2).
- **Alerte** (alerte caméra, échec, imprimante injoignable en pleine impression) : bandeau rouge **en tête** (texte + pictogramme) avec le lien « Ouvrir la page d'arrêt » qui mène à `/stop/{id}` (page serveur existante, confirmation humaine). **Aucun bouton de la page n'arrête quoi que ce soit.**
- **Photos de paliers** (G9 D8, une par 10 %) : la carte En direct montre la **dernière photo de palier** avec son pourcentage ; un palier sans photo s'écrit « photo non disponible » (jamais un trou muet ni une erreur) ; la planche complète est dans la fiche impression.
- **Fin d'impression** : le bloc En direct se transforme en « **Terminée : comment s'est passée l'impression ?** » avec le verdict en un geste (PJ11) et reste jusqu'au verdict ou jusqu'à « Plus tard » (qui met l'élément dans **À faire**). Un toast discret « Verdict enregistré · Annuler » (8 s) permet de corriger une erreur de tap (question ouverte 4).
- **Plusieurs imprimantes** : toutes visibles (carte compacte si au repos) ; l'ordre est stable (imprimante qui imprime d'abord, puis par nom).
- **Dégradé** : si le service est injoignable, les blocs gardent leur dernière valeur avec « Dernière mise à jour à… » et un seul bandeau d'erreur ; rien ne clignote.

## 6. Principes d'ergonomie

1. **Vocabulaire sans jargon** (UX4). Glossaire appliqué dans tout le site, les pages serveur et les cartes ; le terme technique reste disponible en infobulle ou « Détails techniques ».

| Aujourd'hui | Demain | Remarque |
|---|---|---|
| « bambud », « Service bambud 1.x » | « Printalk » | version dans Réglages › Avancé |
| « Découpe » / « slicing » / « slicer » | « Préparation de l'impression (découpe) » | |
| « G-code », « fichier G-code » | « fichier d'impression (G-code) » | |
| « Empreinte (sha256) » | « Identifiant du fichier » + détail replié | sur /confirm : garder, **à côté** d'un libellé en clair |
| « HMS », « Alertes HMS » | « Alertes de l'imprimante » | |
| « Firmware » | « Version du logiciel de l'imprimante » | |
| « CAO (STEP) », « Backend CAO absent » | « Fichiers STEP : conversion disponible / non installée » | |
| « Matériaux » / « Bobines » / « matière » | **« Matières »** (stock : bobines ; fiches : fiches matières) | un seul mot de premier niveau |
| « Comparaison » / « Variantes » / « Expérience » | **« Essais de réglages »** (un essai a des variantes) | |
| « Gabarits » | « Pièces sur mesure (gabarits) » | |
| « Journal » (impressions) | « Impressions terminées » + fiche | |
| « UPLOADED », « START_UNKNOWN », « déposée sur l'imprimante » | « Envoyée à l'imprimante », « Lancement à vérifier » | **une seule table** `web/shared/labels.ts` pour site, serveur et cartes |
| « Lancer » (bouton qui ouvre la confirmation) | « **Vérifier et lancer** » + pictogramme bouclier | le bouton **n'est** qu'un lien : le libellé le dit |
| « Jobs », « Travaux en cours » | « Préparations en cours » | |
| « Espace » (surtitre) | supprimé | |

2. **Une action principale par écran** (§ 4.5) ; boutons destructifs jamais en primaire ; suppression = corbeille (récupérable), jamais définitive depuis ces écrans.
3. **Profondeur ≤ 3**, **2 clics** pour toute fonction depuis le menu ; test de graphe (§ 9).
4. **Honnêteté des chiffres** (règle G9) : tout pourcentage affiche son effectif `n` ; les « non jugées » sont comptées à part ; une valeur inconnue = « non disponible » + raison, jamais « 0 » ni « — » muet ; niveau mesuré / calculé / estimé visible en texte.
5. **Accessibilité** (UX12) : contraste **AA** (jetons de `tokens.css` déjà documentés ; test sur les nouveaux couples) ; **cibles tactiles ≥ 44 px** quand `pointer: coarse` (la hauteur de contrôle de 34 px reste pour la souris) ; **tout au clavier** (menu, onglets en flèches, recherche `/` et Ctrl/Cmd+K, feuille « Plus », dialogues piégés) ; **focus déplacé** vers le titre `h1` à chaque changement de route (aujourd'hui seul `scrollTo(0,0)`) ; lien d'évitement conservé ; repères (`nav`, `main`, `aside`) étiquetés ; état **jamais** par la couleur seule (`Badge`, `StatusDot`) ; `prefers-reduced-motion` coupe halos, flous animés, défilements doux ; zoom 200 % sans perte ; textes ≥ 14 px (15 px actuel), `lang="fr"`.
6. **Mode sombre** soigné : suit le système par défaut (déjà), bascule mémorisée ; **aucune couleur codée en dur** dans les nouveaux CSS (jetons seulement) ; photos et miniatures sur fond neutre ; contrastes vérifiés dans les **deux** thèmes ; captures clair + sombre à 375, 768 et 1440 px (UX12). Effet « verre » (flou) avec **repli opaque** (existant : `@supports`) conservé ; pas d'information portée par le flou.
7. **Cohérence cartes MCP ↔ pages web** (UX11) : un composant = une seule définition dans `web/shared/components` (nouveaux : `StatusPill`, `StatTile`, `PrinterLiveCard`, `ProjectProgress`, `VerdictBar`, `Breadcrumb`, `PageHeader`, `EmptyState` étendu) ; `web/src/pages/*` et `web/mcp-app/cards/*` **importent** ces composants et la table de libellés `labels.ts` ; plus de `statePill`, `STATE_LABEL`, `QSTATE`, `STATUS`, `OUTCOME` locaux. Test : une carte et une page rendues avec la même donnée affichent **le même libellé** d'état.
8. **Performance et simplicité** : écrans différés conservés (`lazy`), aucune dépendance front nouvelle (objectif), aucun appel réseau externe, aucune police distante (DM Sans embarquée).

## 7. Règles non négociables

**Approbation humaine du lancement : état actuel vérifié le 2026-11-09, à ne pas affaiblir.**
- `Approve` (`internal/printing/confirm.go`) n'est appelée que par la **page serveur `/confirm/{id}`** d'un **navigateur enregistré** : case « plateau vide », nonce, **validité 2 minutes**, passkey exigible ; **aucun outil MCP ni route JSON** ne l'appelle (`noapprove_test.go`, `TestNoMCPToolCanApproveAConfirmation`).
- Dans le site, le bouton **« Lancer »** (`LaunchButton`, `Print.tsx`) **ne lance rien** : il demande une confirmation (`api.requestConfirmation`) puis **redirige** vers `/confirm/{id}`. L'appel `api.startPrint` n'existe que dans `ConfirmBox` (`Print.tsx`), **après** que la page serveur a enregistré l'accord, et le serveur **refuse** sinon ; l'autorisation est à usage unique. L'arrêt passe par `stop_url` (page `/stop/{id}`).
- **Donc, pour G10 :**
  1. **Aucun bouton, carte, raccourci clavier ou widget du tableau de bord ne lance, n'arrête ni ne reprend une impression.** « Vérifier et lancer » est un **lien** (`<a href="/confirm/…">`) ; « Ouvrir la page d'arrêt » aussi. L'Accueil n'importe **ni** `api.startPrint` **ni** un appel d'arrêt.
  2. `api.startPrint` reste appelé **dans un seul composant** (`ConfirmBox`, déplacé avec la fiche impression en UX7 **sans changement de logique**) ; un test échoue si un autre fichier du site l'appelle.
  3. **Le verdict (PJ11), le projet, la priorité, la recherche n'accélèrent ni ne contournent** l'approbation ; un verdict n'a **aucun effet** sur l'imprimante.
  4. La refonte visuelle de `/confirm`, `/stop`, `/action` (UX10) **ne retire aucun élément** : photo, températures, anomalies G-code, alertes matière, risque de décollement, surplombs, case « plateau vide », mention des 2 minutes, passkey, nonce, « Reprendre la photo ». Test de gabarit : chaque élément de sécurité est présent (extension de `internal/printing` tests de page).
  5. Les pages serveur ne sont **jamais** intégrées dans une `iframe` de l'application ni approuvables depuis une carte.
- **Aucun secret affiché** : ni code d'accès d'imprimante (la page Imprimantes montre « enregistré / à saisir » seulement), ni code de lancement, ni jeton, ni clé de signature, ni chemin serveur, ni URL signée longue durée ; messages d'erreur sans trace. Les **liens « Sécurité »** de Réglages **ne montrent aucun secret** : ils ouvrent les pages serveur existantes. Test de motifs (`bblp`, `C:\`, `/home/`, `/opt/`, `Bearer`, `access_code`) sur le rendu de chaque écran avec des données piégées.
- **Aucune API d'IA** : la recherche est textuelle + filtres typés ; « à faire », « meilleur », taux, statut suggéré sont des calculs déterministes ; aucun appel réseau à un modèle, aucune interprétation de phrase côté serveur.
- **Migrations additives seulement** : G10 n'ajoute **aucune** migration par défaut. Si un réglage d'interface doit être mémorisé côté serveur (seuil « projet dormant », vue par défaut), il utilise la table de réglages existante ou, à défaut, une colonne/table **additive** (jamais de `DROP`, de renommage ni de donnée migrée) ; le reste (thème, vue, onglet) est mémorisé dans le navigateur.
- **Ne pas casser l'installation de production** : serveur Linux, P1S en direct, Claude Desktop via `mcp-remote`, navigateur déjà enregistré. **Toutes les URL existantes redirigent** (§ 4.3) ; les URL serveur ne changent pas ; **aucune action** de l'utilisateur après mise à jour ; l'API HTTP (`/api/*`) et les outils MCP gardent leurs entrées et sorties (G10 est un travail d'interface) ; le binaire intègre toujours le build web (`internal/webui`, `go:embed`), le repli « interface non construite » reste.
- **Originaux immuables, corbeille récupérable** : aucune action de l'interface ne supprime définitivement ; archiver / mettre à la corbeille restent des opérations existantes.
- **Cartes** : pas de `eval`, pas de ressource externe, CSP existante, repli texte, aucun bouton « Lancer » ou « Approuver ».

## 8. Compatibilité et dépendances avec G9

- **G9 reste propriétaire de la logique et des données** (projets, verdict, fiches, fil, recherche) ; **G10 est propriétaire de la coque** (menu, routes, en-têtes, tableau de bord, composants de navigation). G9 § 10 prévoit une ligne dans `Shell.tsx` et `main.tsx` ; **tant que G10 UX1 n'est pas fusionnée**, G9 ajoute **un seul** `NavItem` « Projets » et une `<Route>` ; **après UX1**, G9 et tout futur chantier n'ajoutent qu'une ligne dans `web/src/nav.ts`.
- **Tant que G9 n'est pas livrée**, l'interface G10 fonctionne : les blocs qui dépendent de PJ2/PJ10/PJ11 (Projets, verdict, « non jugées ») sont **masqués** ou affichent leur état vide ; le tableau de bord v1 (U2) se compose des routes existantes comme `Home.tsx` aujourd'hui.
- **Une seule source de données** : les pages web lisent les **mêmes routes de lecture** que les cartes (`internal/httpapi/projects.go` de G9, existantes pour le reste) ; pas de calcul dupliqué côté front (taux, avancement, coût = valeurs du serveur).
- `show_atelier` et la carte `workshop.html` (PJ1) affichent le **même contenu logique** que l'Accueil (mêmes blocs, mêmes mots, mêmes priorités, version compacte) ; `ui/atelier.html` actuel reste le repli.

## 9. Critères d'acceptation (testables)

**Navigation (UX1, UX2, UX3)**
- CA-N1 `web/src/nav.ts` déclare **exactement 7** entrées de premier niveau, dans l'ordre Accueil, Projets, Bibliothèque, Impressions, Imprimantes, Matières, Réglages ; le menu rendu (desktop) et la barre basse (mobile : 4 entrées + « Plus ») les reflètent ; test vitest.
- CA-N2 **Redirections** : pour **chaque** ligne de la table § 4.3, un test vitest (routeur en mémoire) vérifie l'URL finale **et** la conservation des paramètres (`/bibliotheque?q=équerre&f=stl`, `/couts?onglet=journal`, `/preparation?model=X`).
- CA-N3 **Aucune route orpheline** : toute route de `nav.ts` a un parent et est atteignable en ≤ 2 clics depuis le menu ; test sur le graphe déclaré. Comparaison, File, Agents, Design **ne sont plus hors menu**.
- CA-N4 **Non-collision** : aucune route SPA ne commence par un préfixe réservé du serveur (`/setup`, `/confirm`, `/stop`, `/action`, `/enroll`, `/oauth`, `/api`, `/mcp`, `/health`, `/agents/pair`) ; test Go + vitest sur la liste partagée.
- CA-N5 Une route inconnue affiche « Introuvable » (statut 200 SPA, `h1`, recherche, 7 liens), **ne redirige plus** vers `/`.
- CA-N6 À 375 px : barre basse visible, 5 emplacements, **libellés visibles**, cibles ≥ 44 px (test de taille calculée), aucune barre de défilement horizontale de la page, feuille « Plus » ouverte/fermée au clavier (Échap, focus rendu).
- CA-N7 Fil d'Ariane : présent dès la profondeur 2, dernier élément `aria-current="page"`, remplacé par « ← Parent » sous 761 px ; les fils écrits à la main de `Model.tsx` et `Compare.tsx` sont supprimés.
- CA-N8 Les onglets ont l'état dans l'URL (`?vue=`) : recharger, partager le lien ou revenir en arrière conserve l'onglet ; ≤ 4 onglets par page (test).

**Tableau de bord (UX6)**
- CA-T1 Rendu avec des données simulées : blocs dans l'**ordre** § 5.1 ; « À faire » **absent** si rien n'attend, présent avec effectifs sinon (une impression à confirmer, une sans verdict, une bobine basse, un projet dormant).
- CA-T2 Une confirmation en attente n'offre qu'un **lien** `a[href^="/confirm/"]` ; le test vérifie **qu'aucune requête non-GET** hors verdict (`/verdict`) n'est émise par le rendu et les clics de l'Accueil (espion sur `fetch`) et que `startPrint` n'est pas importé par l'Accueil.
- CA-T3 Mois simulé de 10 impressions (7 jugées réussies, 1 ratée, 2 non jugées) → tuile « 7/8 jugées · 2 non jugées » (cohérent avec CA1.1 de G9) ; mois vide → tuiles à zéro, aucun `NaN`/division par zéro.
- CA-T4 Impression simulée en cours : carte En direct agrandie, progression, couche, fin estimée, titre d'onglet « NN % · … » ; onglet caché → les sondages s'arrêtent (horloge simulée) et reprennent au retour.
- CA-T5 Impression terminée simulée : le bloc propose le verdict ; un tap sur « Réussi » écrit via PJ11 et affiche « Annuler » 8 s ; « Raté » demande la raison.
- CA-T6 Service injoignable simulé : chaque bloc garde sa dernière valeur, « Dernière mise à jour à… », **un seul** bandeau d'erreur ; un bloc en erreur n'efface pas les autres.
- CA-T7 Sans G9 (routes de projets absentes → 404) : l'Accueil s'affiche sans erreur, sans bloc Projets ni verdict.

**Impressions, regroupements, recherche (UX7, UX8, UX9)**
- CA-I1 `/impressions/:id` affiche la fiche (PJ10) d'une impression, avec la **planche des photos de paliers à 10 %** (palier manquant : « photo non disponible » + raison) et est **adressable** ; archivage et corbeille d'une impression restent possibles ; `ConfirmBox` inchangé.
- CA-I2 `api.startPrint` n'est référencé que dans le composant de confirmation (test de source).
- CA-I3 Les 5 anciens onglets « Coûts » aboutissent à leur nouvelle place (Bilan, Terminées, Tarifs, Après impression) avec le **même contenu** ; aucune donnée ni réglage perdu (tests de rendu comparant les titres de panneau).
- CA-I4 Réglages › Sécurité contient les liens vers `/setup/launch-code`, `/setup/passkeys`, `/setup/notifications`, `/enroll`, `/agents/pair` ; aucun secret rendu.
- CA-P1 Projets : un assemblage simulé (pièces + sous-projet) s'affiche en arbre repliable, avancement agrégé et avancement propre du sous-projet ; une impression à 100 % jugée ratée compte « imprimée » **et** est signalée « jugée ratée » ; l'onglet Déclinaisons affiche la comparaison PJ13 ; « Préparer cette déclinaison » n'émet aucune requête de lancement (seulement `prepare_assembly_print` et le lien vers `/confirm/…`) ; sans G9, l'entrée Projets affiche son état vide ou est masquée.
- CA-P2 Réglages › Suivi : `min_sample_size` borné 2-20 (valeur hors bornes refusée avec message), seuil « projet dormant » > 0 ; l'enregistrement est visible (toast) et la valeur appliquée est affichée dans les écrans qui l'utilisent (« ton meilleur, n ≥ 3 »).
- CA-R1 `Ctrl+K` et `/` ouvrent la recherche ; résultats groupés ; `Échap` ferme et **rend le focus** ; saisie contenant `'`, `;`, `<script>` : échappée côté affichage, paramètres encodés, aucune erreur.
- CA-R2 Recherche sans résultat : message + 2 suggestions ; recherche vide : récents.

**Vocabulaire, cohérence, accessibilité, thème (UX4, UX5, UX10, UX11, UX12)**
- CA-V1 Aucun des termes interdits (`bambud`, `Firmware`, `HMS`, `Backend`, `START_UNKNOWN`, `UPLOADED`, « Espace ») n'apparaît dans le texte rendu des écrans (test sur le DOM) ; exceptions listées (Détails techniques, pages `Design`/Avancé).
- CA-V2 Une seule table de libellés `labels.ts` ; les tables locales (`STATE_LABEL`, `QSTATE`, `STATUS`, `OUTCOME`, `statePill`) sont supprimées ; test : même donnée → même libellé dans une page et dans la carte correspondante.
- CA-S1 Gabarit serveur : `/confirm`, `/stop`, `/action` contiennent **tous** les éléments de sécurité listés § 7 point 4 (test de page existant étendu) ; lien « ← Retour à Printalk » présent sur chaque page serveur ; `next` non sûr (`//x`, `\`, > 300 car.) ignoré.
- CA-S2 **Parité de jetons** : test Go qui lit `web/shared/tokens.css` et `theme.go` et échoue si une variable commune (couleurs de texte, de statut, de bordure ; clair et sombre) diverge.
- CA-A1 États vides (3 éléments), erreurs (3 parties + détails repliés) et squelettes testés sur chaque écran principal ; la barre de chargement de route apparaît lors d'un chargement différé simulé.
- CA-A2 Accessibilité : un seul `h1` par écran ; repères étiquetés ; focus sur le `h1` après navigation ; ordre de tabulation logique ; onglets pilotés aux flèches ; contraste AA sur les nouveaux couples de jetons (test de calcul) ; `prefers-reduced-motion` désactive halos et animations (test CSS). Un contrôle automatique d'accessibilité (axe) n'est ajouté que si sa licence est compatible (MIT/Apache-2.0) — **à vérifier** ; sinon, liste de contrôle manuelle dans la recette.
- CA-A3 Mode sombre : aucune **nouvelle** couleur codée en dur dans `web/src/**/*.css` ni `web/shared/**/*.css` (test de comptage : le nombre d'occurrences d'hexadécimaux hors `tokens.css` **ne peut que baisser**) ; captures clair/sombre à 375, 768, 1440 px de l'Accueil, d'Impressions, de la fiche modèle et de la confirmation, rangées dans `docs/ux/captures/`.
- CA-A4 Secrets : rendu de chaque écran avec des données contenant `access_code`, `Bearer abc`, `C:\Users\x`, `/opt/atelier` : **aucune** de ces valeurs dans le DOM.

## 10. Ordre de livraison en vagues (articulé avec G9)

| Vague G10 | Contenu | Se place par rapport à G9 | Pourquoi |
|---|---|---|---|
| **U1** | **UX1** (socle `nav.ts`, `PageHeader`, `Breadcrumb`, barre de chargement, « Introuvable »), **UX2** (7 entrées + toutes les redirections), **UX3** (barre basse mobile), **UX4** (glossaire, `labels.ts`), début de **UX5** | **juste avant ou avec la vague 1 de G9** | G9 y branche « Projets », la fiche pièce et la fiche impression : mieux vaut leur donner d'emblée le bon cadre ; aucune logique métier, risque faible, **les redirections protègent la production** |
| **U2** | **UX6 v1** (Accueil : À faire, En direct, Impressions récentes, Chiffres ; verdict inline), **UX7** (Impressions en 4 vues, `/impressions/:id`, fiche impression), **UX8** (Matières, Imprimantes, Bibliothèque, Réglages regroupés), **UX9 v1** (recherche texte) | **après la vague 1 de G9** (verdict PJ11, fiche PJ10, projets PJ2) | l'Accueil et « À faire » n'ont de sens qu'avec les verdicts et les projets |
| **U3** | **UX6 complet** (Projets en cours, chiffres PJ1, bobines basses, titre d'onglet, alertes), onglet **Déclinaisons** de la fiche projet (**PJ13**, carte `variants.html` alignée), Réglages › Suivi, **UX10** (pages serveur), **UX11** (composants partagés, cartes `workshop`/`project`/`part` alignées), fil d'activité PJ3 intégré (page « Activité » en onglet de Projets et de la fiche impression) | **avec la vague 3 de G9** (PJ1, PJ3) | le vrai tableau de bord lit les agrégations de PJ1 ; les pages serveur se retouchent en dernier, avec les tests de sécurité |
| **U4** | **UX9 v2** (filtres PJ4), arbre PJ8 et différence PJ9 intégrés aux fiches, **UX12** recette (captures, accessibilité), nettoyage des CSS devenus morts | **avec la vague 4 de G9** | confort et finitions, recette finale |

Chaque vague est livrable seule, une fusion par vague, **sans casser l'existant** (les redirections de U1 sont en place avant tout déplacement d'écran). U1 peut démarrer **sans attendre G9** ; si G9 vague 1 est déjà fusionnée, U1 reprend sa ligne de menu « Projets ». Règle de conflit : un seul chantier à la fois modifie `Shell.tsx`, `main.tsx` et `nav.ts` ; l'orchestrateur fusionne dans l'ordre U1 → G9 v1 → U2 → G9 v2/v3 → U3 → G9 v4 → U4 (G9 v2 PJ6/PJ7 n'introduit que des écrans dans la fiche modèle).

## 11. Fichiers probablement touchés

Nouveaux : `web/src/nav.ts` (+ test), `web/shared/labels.ts`, `web/shared/components/{PageHeader,Breadcrumb,StatusPill,StatTile,PrinterLiveCard,ProjectProgress,VerdictBar,PhotoStrip,NavProgress,MobileTabBar}.tsx` (+ tests vitest), `web/src/pages/{Dashboard,NotFound,SearchPanel,LegacyRedirects}.tsx`, `web/src/pages/impressions/{Impressions,PrintSheet,NewPrint}.tsx`, `web/src/pages/settings/{Appearance,Tariffs,Security,AdvancedPage}.tsx`, `web/src/pages/model/{ModelTabs}.tsx`, `docs/UX.md` (guide d'interface, vocabulaire, règles), `docs/ux/captures/`.
Modifiés : `web/src/Shell.tsx` (lit `nav.ts`, barre basse, recherche), `web/src/main.tsx` (routes + redirections), `web/src/shell.css`, `web/src/pages/{Home,Library,Model,Prepare,Compare,Print,Printers,Filament,Insights*,Materials,Gabarits,Trash,Agents,Settings,Design}.tsx` (déplacés ou découpés, **logique conservée**), `web/src/pages/common.tsx` (`Async` : squelettes, erreurs en 3 parties), `web/shared/components/{ui,kit}.tsx` et `*.css`, `web/shared/tokens.css` (cibles tactiles `pointer: coarse` seulement), `web/mcp-app/cards/*.tsx` (import des composants partagés), `internal/humanauth/theme.go` (parité de jetons, liens de retour, vocabulaire), `internal/printing/pages.go` et `actions.go` (libellés et lien de retour, **sans retirer un élément de sécurité**), `internal/httpapi/oauth.go`, `pairpage.go`, `internal/camera/pages.go`, `internal/notify/pages.go` (gabarits), `internal/webui/webui_test.go` (routes SPA et non-collision), tests de pages `internal/*/pages_test.go`.
Aucune dépendance Go nouvelle ; aucune dépendance npm nouvelle (objectif ; `lucide-react` déjà là).

## 12. Risques, questions ouvertes, taille

Taille : **L** (U1 ≈ M, U2 ≈ M, U3 ≈ M, U4 ≈ S).
Risques :
- **Habitudes** de l'utilisateur de production (menu déplacé) : atténué par les redirections, un bandeau d'une version « Le menu a changé : Coûts est maintenant dans Impressions › Bilan » (fermable, mémorisé dans le navigateur) et `docs/UX.md`.
- **Régression de la confirmation** en retouchant les pages serveur : atténué par la règle § 7 point 4 et les tests de gabarit ; UX10 est livrée **en dernier** et sans changement de logique.
- **Conflits de fichiers** avec G9 (`Shell.tsx`, `main.tsx`) : atténué par `nav.ts` et l'ordre de fusion § 10.
- **Fiche modèle** (292 lignes) à découper : refactoring sans changement de comportement, tests existants conservés.
- **Faible taux de verdicts** : le tap direct sur l'Accueil, « À faire » et le titre d'onglet visent à le relever, sans jamais forcer.

Questions ouvertes pour l'utilisateur (4) :
1. **Les 7 entrées** (Accueil, Projets, Bibliothèque, Impressions, Imprimantes, Matières, Réglages) vous conviennent-elles, notamment « Impressions » à la place de « Historique » et la disparition de l'entrée « Coûts » (répartie en Bilan, Tarifs, Après impression) ?
2. **Photo du plateau** : l'actualiser automatiquement toutes les 30 s pendant une impression (onglet visible seulement) ou garder uniquement le bouton « Photo » comme aujourd'hui (charge sur la caméra de la P1S) ? Recommandé : bouton seul par défaut, option activable.
3. **Mobile** : barre basse à 5 emplacements (recommandé) ou menu « hamburger » classique ?
4. **Verdict sur l'Accueil** : un seul tap enregistre directement avec « Annuler » pendant 8 s (recommandé), ou toujours une confirmation avant d'enregistrer ?

## 13. Consigne finale pour l'agent de code

Travaille dans ton worktree. Avant de rendre la main, fais passer : `go vet ./...` ; `go test -short ./... -count=1` ; `cd web && npm ci && npm run build` ; `cd web && npx vitest run` ; `GOOS=linux CGO_ENABLED=0 go build ./...`. L'imprimante est **toujours simulée** (`internal/printer/sim`, `cmd/printer-sim`). Vérifie à la main, dans le navigateur, l'Accueil et la confirmation à 375 / 768 / 1440 px en clair et en sombre. Livre par vague (§ 10), une fusion par vague. N'édite pas `docs/UTILISATION.md`, `docs/ETAT.md` ni `docs/BACKLOG.md` ; écris `docs/UX.md`. Commits en français, une ligne finale `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. **Ne pas pousser.**
