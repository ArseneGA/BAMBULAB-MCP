# Ce qui est simulé, hypothèses et limites — prototype D0

## Portée

Le prototype est une **maquette**. Il ne prouve ni la compatibilité MCP Apps (affichage d'un widget dans une conversation, messages hôte/widget, limites du cadre), ni le fonctionnement de Bambu Studio, ni la qualité d'un résultat imprimé. Ces validations techniques restent à faire dans les étapes suivantes du dossier.

## Simulé (aucun vrai calcul ni vrai appel)

- **Données** : modèles, versions, empreintes, matériaux, imprimante, profils sont fictifs (`src/data/demo.ts`, `src/pages/prepData.ts`). Toute valeur inconnue y est `null` et affichée « inconnu ».
- **Géométrie** : les formes (boîte, cylindre, tore, cône, creux) sont **générées** à partir d'un type de forme et des dimensions de démonstration ; **elles ne sont pas lues dans les fichiers STL, STEP ou 3MF**. Les sous-parties sélectionnables sont définies à la main par forme.
- **Aperçu identique entre variantes** : les réglages ne changent pas la forme ; les trois viewers de la comparaison affichent la même géométrie (seule la caméra est partagée). Entre versions d'un modèle, la différence est un rendu simulé (moins de chanfreins/perçages sur les anciennes).
- **Estimations slicer** (temps, matière, couches, volume de supports) : formule de démonstration (`estimate()` dans `prepData.ts`), pas un slicer. Elles sont étiquetées « démo ».
- **Objectifs de finition** : le texte libre est conservé tel quel ; la décomposition en critères dans Matériaux repose sur des **mots-clés**, pas sur une interprétation par un modèle de langage ni sur des connaissances matériaux vérifiées.
- **Matériaux** : compatibilité P1S « déclarée par le fabricant » ou « non démontrée » ; ce sont des étiquettes de démonstration, pas des mesures.
- **Impression** : machine à états pilotée par minuteries. Slicing, validation, transfert, démarrage, couches, températures, perte réseau, échec sont simulés. Aucun clic ne contacte la P1S, le réseau, un serveur Linux ou un agent Windows. La disponibilité de Linux, Windows et P1S dans la barre du haut est une constante.
- **Conflit de révision** : reproduit par un interrupteur, sans second utilisateur ni vrai versionnement.
- **Vue compacte `/claude`** : conversation illustrative inventée ; le cadre « hôte » est une hypothèse de dessin, pas une capture. Panneau latéral et plein écran ne sont pas supposés disponibles.
- **Persistance** : le thème est mémorisé dans `localStorage` ; les réglages de préparation vivent en `sessionStorage` (par modèle, par onglet). Rien d'autre n'est conservé.

## Hypothèses de conception

- Plateau P1S 256 × 256 mm ; la grille du viewer est à 10 mm.
- Une impression exige une autorisation explicite par lancement et une case « plateau vide » : choix de sécurité, non imposé par l'imprimante.
- Aucune variante n'est désignée « meilleure » et aucun score de qualité n'est affiché ; résistance et aspect sont « à vérifier par essai ».
- Polices Instrument Sans et JetBrains Mono chargées depuis Google Fonts sans bloquer l'affichage ; repli sur les polices système si elles sont lentes ou indisponibles.

## Limites connues

- Pas de **mesure** ni de **coupe** dans le viewer ; pas d'affichage des surplombs, des supports ni des couches.
- **Peinture des supports et des coutures non simulée** : le prototype propose des modes (supports, position des coutures) mais pas de peinture de zones ; l'interface le signale.
- Sélection limitée aux sous-parties prédéfinies, pas de sélection libre de faces.
- La caméra partagée de la comparaison ne synchronise pas la sélection de sous-partie ; la bascule garde une seule caméra.
- Viewer en rendu à la demande (`frameloop="demand"`), résolution plafonnée à 1,5. Performances non mesurées sur matériel modeste.
- Responsive vérifié à 800 et 1440 px seulement ; pas d'optimisation téléphone. La navigation du haut est réduite aux icônes sous 1180 px.
- Accessibilité : focus clavier, libellés et contrastes contrôlés automatiquement ; pas d'audit avec lecteur d'écran.
- Testé avec Chromium (Playwright) uniquement.
- three.js (~0,5 Mo minifié, plus ~0,4 Mo de générateur de géométrie) est chargé en différé, seulement sur les écrans qui affichent un viewer.
