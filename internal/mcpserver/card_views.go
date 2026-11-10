package mcpserver

import "github.com/modelcontextprotocol/go-sdk/mcp"

// Cartes MCP App « même rendu que le site, en plus compact » (web/mcp-app/cards, docs/research/mcp-cards.md).
// Même bundle HTML que les autres vues : la carte est choisie d'après la forme du résultat, l'URI ne sert qu'à lier les outils.
// Aucun outil n'est renommé ni modifié : seul le lien _meta.ui.resourceUri (définition ET résultat, via addTool) est posé, le texte
// de repli de chaque outil reste complet (les hôtes sans vue, dont Claude Code, ne voient que lui).
//
//	ui://bambu/library.html   list_collection                                              (search_models garde ui://bambu/search.html)
//	ui://bambu/compare.html   compare_variants                                             (bouton « Choisir cette variante » = apply_variant)
//	ui://bambu/costs.html     estimate_cost, get_project_costs, get_usage_dashboard
//	ui://bambu/spools.html    list_spools, get_ams_state
//	ui://bambu/prints.html    list_print_history, list_print_queue, get_print_log, list_print_log   (clic : get_print_status)
//	ui://bambu/printers.html  list_printers                                                (clic : get_printer_status)
//	ui://bambu/issues.html    list_issues                                                  (lecture seule, déclarée dans issue_tools.go)
const (
	LibraryViewURI  = "ui://bambu/library.html"
	CompareViewURI  = "ui://bambu/compare.html"
	CostsViewURI    = "ui://bambu/costs.html"
	SpoolsViewURI   = "ui://bambu/spools.html"
	PrintsViewURI   = "ui://bambu/prints.html"
	PrintersViewURI = "ui://bambu/printers.html"
)

func init() {
	for tool, uri := range map[string]string{
		"list_collection":     LibraryViewURI,
		"compare_variants":    CompareViewURI,
		"estimate_cost":       CostsViewURI,
		"get_project_costs":   CostsViewURI,
		"get_usage_dashboard": CostsViewURI,
		"list_spools":         SpoolsViewURI,
		"get_ams_state":       SpoolsViewURI,
		"list_print_history":  PrintsViewURI,
		"list_print_queue":    PrintsViewURI,
		"get_print_log":       PrintsViewURI,
		"list_print_log":      PrintsViewURI,
		"list_printers":       PrintersViewURI,
	} {
		toolViewMeta[tool] = AppToolMeta(uri)
	}
}

// registerCardViews déclare les ressources ui:// des cartes (une ligne dans New()).
func registerCardViews(s *mcp.Server) {
	html, _ := viewHTML()
	addViewResource(s, LibraryViewURI, "library_view", "Bibliothèque",
		"Carte Bibliothèque (list_collection, search_models) : grille de pièces avec dimensions, formats et étiquettes, filtre par format ; choisir une pièce ouvre sa fiche.", html)
	addViewResource(s, CompareViewURI, "compare_view", "Comparaison des variantes",
		"Carte Comparaison (compare_variants) : tableau des variantes (temps, matière, surplomb, avertissements), variante recommandée, « Choisir cette variante » (apply_variant, n'imprime rien).", html)
	addViewResource(s, CostsViewURI, "costs_view", "Coûts",
		"Carte Coûts (estimate_cost, get_project_costs, get_usage_dashboard) : tuiles, graphique par période, détail matière, électricité et usure.", html)
	addViewResource(s, SpoolsViewURI, "spools_view", "Bobines et AMS",
		"Carte Bobines (list_spools, get_ams_state) : couleur réelle, matière, reste en grammes et jauge, alerte de manque, emplacements de l'AMS.", html)
	addViewResource(s, PrintsViewURI, "prints_view", "Impressions",
		"Carte Impressions (list_print_history, list_print_queue, get_print_log, list_print_log) : statut coloré, durée, coût, note ; choisir une impression ouvre son suivi.", html)
	addViewResource(s, PrintersViewURI, "printers_view", "Imprimantes",
		"Carte Imprimantes (list_printers) : état, firmware, AMS, dernière impression ; choisir une machine ouvre son état détaillé.", html)
}
