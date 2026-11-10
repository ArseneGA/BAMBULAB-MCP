package mcpserver

// Cartes du suivi de projet (G9) : même bundle HTML que les autres cartes, la carte est choisie d'après la forme du
// résultat (web/mcp-app/cards/project.tsx, verdict.tsx) ; l'URI ne sert qu'à lier les outils. Aucune carte ne porte de
// bouton « Lancer » ou « Approuver » : seuls des gestes sans risque (statut, quantité, verdict) passent par des outils
// réservés à l'application (submit_*).
//
//	ui://bambu/project.html  get_project, list_projects                    (submit_project_status, submit_project_part)
//	ui://bambu/verdict.html  get_pending_verdicts, set_print_verdict       (submit_print_verdict)
//	ui://bambu/part.html         get_part_sheet                                (miniatures : get_sheet_photo, réservé à la carte)
//	ui://bambu/print-sheet.html  get_print_sheet                               (bandeau de verdict : submit_print_verdict)
const (
	ProjectViewURI    = "ui://bambu/project.html"
	VerdictViewURI    = "ui://bambu/verdict.html"
	PartViewURI       = "ui://bambu/part.html"
	PrintSheetViewURI = "ui://bambu/print-sheet.html"
)
