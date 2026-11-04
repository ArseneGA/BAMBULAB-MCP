# Atelier — prototype D0

Prototype navigable (Vite + React 18 + TypeScript strict) d'un gestionnaire de fichiers 3D et de préparation d'impression pour une Bambu Lab P1S. **Données de démonstration, aucune action réelle** : ni serveur, ni clé API, ni imprimante. Détail de ce qui est simulé : `../docs/D0/SIMULE.md`. Parcours annoté : `../docs/D0/PARCOURS.md`.

## Lancer

```bash
npm install
npm run dev        # http://localhost:5173 (port imposé, strictPort)
npm run build      # contrôle de types + build de production dans dist/
npm run preview    # sert dist/ sur le port 4173
```

## Routes

| Route | Écran |
|---|---|
| `/` | Accueil : les six étapes du parcours |
| `/bibliotheque` | Recherche, collections, filtres |
| `/modele/:id` | Fiche modèle : viewer 3D, fichiers, versions (ex. `/modele/m01`) |
| `/preparation?model=m01` | Sélection d'objet ou de zone, réglages, objectif de finition |
| `/comparaison?model=m01` | Trois variantes, caméra partagée, conflit de révision simulé |
| `/materiaux` | Matériaux et objectifs de finition |
| `/impression` | Envoi, confirmation de lancement, suivi (simulés) |
| `/claude` | Vue compacte dans une conversation simulée (4 tours) |
| `/design` | Système de design |

Le thème clair/sombre se change depuis la barre du haut. Direction artistique : `DESIGN.md`.

## Captures et contrôle

```bash
npx playwright install chromium   # une seule fois
node scripts/capture.mjs          # serveur de dev lancé ; écrit ../docs/D0/captures/
node scripts/audit.mjs            # débordements, troncatures, contrastes, console, canvas 3D
```

Le viewer 3D (three.js) est chargé en différé : seules les routes qui l'affichent (`/modele`, `/preparation`, `/comparaison`, `/claude`) le téléchargent.

## Structure

`src/components` composants du système de design · `src/design` jetons et thème · `src/viewer` viewer 3D (`Viewer3D.tsx`, chargé via `LazyViewer3D.tsx` ; props `camera` et `onCameraChange` pour synchroniser plusieurs viewers) · `src/pages` écrans · `src/data` données de démonstration.
