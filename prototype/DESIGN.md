# Direction artistique — « Atelier »

**Identité.** Un établi, pas un tableau de bord : fond papier gris chaud en clair, graphite chaud en sombre, panneaux plats séparés par des filets de 1 px, rayons de 2 à 6 px, aucune ombre hors éléments flottants. La densité vient de la grille de 4 px et de contrôles de 32 px, pas de cartes empilées.

**Typographie.** *Instrument Sans* (grotesque nette, bonne lisibilité à 13 px) pour l'interface ; *JetBrains Mono* pour tout ce qui se mesure : cotes en mm, hauteurs de couche, températures, empreintes, versions. La police mono signale « valeur exacte », la grotesque « libellé ». Repli système (`Segoe UI`, `Cascadia Mono`) si les polices Google ne se chargent pas.

**Couleur.** Un seul accent fonctionnel, l'orange de sécurité d'atelier : `#B83C08` en clair, `#FF8A4C` en sombre. Il marque la sélection, l'action primaire et le focus, rien d'autre. Les états (vert `#2F6F3E`, ambre `#8A5A00`, rouge `#B3261E` en clair) sont sémantiques et toujours doublés d'une forme et d'un texte : disque plein, disque vide, losange, triangle. Neutres chauds : clair `#ECEBE6` / `#F6F5F1` / filets `#C4C0B5`, texte `#1A1916` ; sombre `#121211` / `#1B1A18` / filets `#3B3832`, texte `#ECE9E2`.

**Mouvement.** Transitions de 90 à 240 ms, uniquement pour expliquer un changement d'état ; tout tombe à 0 ms avec `prefers-reduced-motion`.

**Interdits respectés.** Pas de dégradé, de néon, de verre dépoli, de blob, d'emoji ni de faux indicateur. Icônes : `lucide-react` seul, trait 2 px.

**Accessibilité.** Focus clavier = anneau de 2 px en accent sur écart de 2 px, présent sur tous les contrôles ; textes secondaires à plus de 4,5:1 sur leur fond dans les deux thèmes.
