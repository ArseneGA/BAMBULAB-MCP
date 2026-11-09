# Cartes MCP App (bibliothèque, comparaison, coûts, bobines, impressions, imprimantes)

Besoin : « le front affiché dans Claude doit être dans la même DA que le site ; presque tout en carte ». Six cartes reprennent le rendu vert des écrans du site (Bibliothèque, Préparer, Coûts, Bobines, Impressions, Imprimantes), en plus compact. Code : `web/mcp-app/cards/`, liaisons Go : `internal/mcpserver/card_views.go`.

## Liaisons outil -> carte

Le lien `_meta.ui.resourceUri` (clé imbriquée et clé plate) est posé par `addTool` sur la définition (`tools/list`) ET sur le résultat, sans toucher aux handlers : aucun outil renommé, texte de repli inchangé (Claude Code et les hôtes sans vue ne voient que lui). Toutes les URI servent le même bundle HTML ; la carte est choisie par la FORME du `structuredContent` (`classifyCard`, formes disjointes des autres vues).

| Carte | URI | Outils liés | Interactions |
|---|---|---|---|
| Bibliothèque | `ui://bambu/search.html` (search_models, existante), `ui://bambu/library.html` | `search_models`, `list_collection` | filtre par format ; clic pièce = `get_model_details` (fiche + viewer 3D existants) ; sous-collection = `list_collection` |
| Comparaison | `ui://bambu/compare.html` | `compare_variants` | « Choisir cette variante » = `apply_variant` (fixe le profil, n'imprime rien) ; « Actualiser » = `compare_variants` |
| Coûts | `ui://bambu/costs.html` | `estimate_cost`, `get_project_costs`, `get_usage_dashboard` | bascule coût / heures / matière du graphique |
| Bobines | `ui://bambu/spools.html` | `list_spools`, `get_ams_state` | lecture seule |
| Impressions | `ui://bambu/prints.html` | `list_print_history`, `list_print_queue`, `get_print_log`, `list_print_log` | clic = `get_print_status` (carte de suivi) |
| Imprimantes | `ui://bambu/printers.html` | `list_printers` | état relu par `get_printer_status` à l'ouverture ; clic = `get_printer_status` |

`list_print_log` (journal) n'était pas dans la demande : il est lié à la carte Impressions parce qu'il rend les mêmes fiches (note, coût).

Navigation : une carte qui ouvre un autre résultat (fiche, suivi, état) passe par `main.tsx` (`navigate`), qui empile la vue courante et affiche « Retour ». Le résultat ouvert est classé comme tout résultat d'outil.

## Point de fusion avec le socle design partagé (`web/shared`)

Les cartes n'utilisent pas encore le socle : elles s'appuient sur des composants locaux minces, tous préfixés **`Lite`** (`web/mcp-app/cards/lite.tsx` + `lite.css`, classes `lc-*`), aux mêmes jetons (#16a06b, #3ee39c, #095a40, fond #f4f7f5 / #0f1714, DM Sans avec repli système). À la fusion, remplacer les imports de `./lite` :

`LiteCard` -> Card · `LiteButton` -> Button · `LiteBadge` -> Badge · `LiteGauge` -> ProgressBar · `LiteTable` -> Table · `LiteStat`/`LiteStats` -> tuile de chiffre · `LiteState` -> états vide / chargement / erreur · `LiteSwatch` (pastille de couleur réelle), `LiteBox` (silhouette) et `LiteBars` (barres SVG, dans `costs.tsx`) n'ont pas d'équivalent attendu et restent.

Les vues (`library`, `compare`, `costs`, `spools`, `prints`, `printers`) n'importent que ces noms ; `lite.css` est la seule feuille de style des cartes. Thème : `data-theme` posé par `main.tsx` d'après l'hôte, repli `prefers-color-scheme`.

## Choix et limites

- **Vignettes** : l'iframe de l'hôte ne charge pas l'aperçu (CSP vide, pas de réseau) ; la bibliothèque affiche donc une silhouette isométrique aux proportions réelles (x, y, z) et à la teinte dérivée du nom, signalée comme telle dans la carte. Ce n'est pas un rendu de la pièce ; la vraie 3D est dans la fiche (clic).
- **Comparaison** : `compare_variants` ne désigne aucun gagnant. « Recommandée » = variante première sur le plus de critères mesurés (classements du serveur), hors violation d'un seuil non négociable et hors échec de découpe ; en cas d'égalité, aucune. Les colonnes coût, translucidité et rigidité n'apparaissent que si une mesure de ce nom (`cost`, `translu*`, `rigid*`) existe dans les métriques : rien n'est inventé.
- **Coûts** : `get_usage_dashboard` ne ventile pas électricité et usure (seulement `estimate_cost` le fait) ; le détail du tableau de bord est par matière et par imprimante.
- **Imprimantes** : ni code d'accès, ni numéro de série, ni adresse réseau n'est affiché ; l'état est lu une fois à l'ouverture (imprimante sans code d'accès non interrogée).
- **Bundle** : un seul HTML, 1 148 Kio, sous le seuil de 1,5 Mio : pas de découpe par vue.
- **Non vérifié ici** : l'affichage réel dans Claude Desktop. Contrôle visuel fait avec un hôte JSON-RPC minimal (clair/sombre, 720 et 375 px) ; tests vitest (`web/mcp-app/cards/*.test.tsx`) et Go (`card_views_test.go`).
