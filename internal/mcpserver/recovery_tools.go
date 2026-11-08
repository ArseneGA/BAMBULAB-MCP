package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// Outils de reprise après incident : relancer un fichier déjà déposé, reprendre après une pause sur erreur, charger la
// bobine externe, lister et nettoyer la carte SD, diagnostiquer la connexion. Les actions à effet (reprise, chargement,
// nettoyage) PASSENT PAR LA VALIDATION D'UN HUMAIN (page /action/{id}, ou son code de lancement dans l'application) : ces
// outils ne font que créer la demande, comme cancel_print pour l'arrêt. Une demande = une seule commande, jamais renvoyée.

// ResumePrintInput : impression à reprendre.
type ResumePrintInput struct {
	PrintJobID string `json:"print_job_id" jsonschema:"impression en pause sur l'imprimante (get_print_status : alert « attend une action à l'écran »)"`
}

// LoadFilamentInput : chargement de la bobine externe.
type LoadFilamentInput struct {
	PrinterID    string `json:"printer_id,omitempty" jsonschema:"imprimante (facultatif s'il n'y en a qu'une)"`
	PrintJobID   string `json:"print_job_id,omitempty" jsonschema:"impression en pause concernée (facultatif ; journalise l'action sur elle)"`
	TemperatureC int    `json:"temperature_c,omitempty" jsonschema:"température de buse pour le chargement, 150 à 320 °C (défaut : consigne actuelle, sinon 220)"`
}

// ListPrinterFilesInput : liste de la carte SD.
type ListPrinterFilesInput struct {
	PrinterID  string `json:"printer_id,omitempty"`
	KeepLatest int    `json:"keep_latest,omitempty" jsonschema:"versions récentes gardées par modèle avant de marquer le reste obsolète (défaut 1)"`
}

// CleanupFilesInput : nettoyage de la carte SD.
type CleanupFilesInput struct {
	PrinterID  string   `json:"printer_id,omitempty"`
	KeepLatest int      `json:"keep_latest,omitempty" jsonschema:"versions récentes gardées par modèle (défaut 1)"`
	Names      []string `json:"names,omitempty" jsonschema:"restreindre à ces fichiers (ils doivent être des candidats : list_printer_files, obsolete=true) ; sinon tous les candidats"`
}

// ApproveActionWithCodeInput : demande d'action visée et code saisi par l'humain.
type ApproveActionWithCodeInput struct {
	ActionID string `json:"action_id" jsonschema:"demande visée (fin de action_url)"`
	Code     string `json:"code" jsonschema:"code de lancement personnel saisi par l'HUMAIN dans la vue ; ne JAMAIS le demander à l'utilisateur dans la conversation"`
}

// ActionCodeOut : résultat d'une validation par code (jamais le code).
type ActionCodeOut struct {
	CodeApprovalOut
	Result *printing.ActionResult `json:"result,omitempty"`
}

func init() { codeTools["validate_action_with_code"] = true }

// AddRecoveryTools enregistre les outils de reprise après incident (une ligne dans New()).
func AddRecoveryTools(s *mcp.Server, p *printing.Service) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	f, t := false, true
	phys := &mcp.ToolAnnotations{DestructiveHint: &t, OpenWorldHint: &t}
	write := &mcp.ToolAnnotations{DestructiveHint: &f, OpenWorldHint: &t}

	addTool(s, &mcp.Tool{
		Name: "reprint_uploaded", Title: "Relancer un fichier déjà déposé",
		Description: "Recrée une confirmation de lancement pour un fichier DÉJÀ déposé sur la carte de l'imprimante (après un abandon, un échec, un arrêt), sans redécouper ni renvoyer. " +
			"Vérifie par FTPS que le fichier est toujours là avec la bonne taille et le bon sha256 (relecture complète ; quick=true : présence et taille seulement), que l'imprimante est au repos et lançable, " +
			"réconcilie d'abord une impression restée PRINTING alors que l'imprimante est arrêtée, puis crée une NOUVELLE impression et sa confirmation (l'historique de l'échec reste). " +
			"Même approbation humaine que request_print_confirmation (page /confirm ou code de lancement dans la carte) : l'humain atteste que le plateau est vide ; rien n'est lancé ici. " +
			"Refus avec consigne précise si le fichier a disparu ou changé (redéposer avec upload_print_artifact), si l'imprimante est occupée ou en erreur. À appeler seulement si l'utilisateur veut relancer.",
		Annotations: write,
	}, func(ctx context.Context, req *mcp.CallToolRequest, in printing.ReprintInput) (*mcp.CallToolResult, printing.ReprintOutput, error) {
		o, err := p.ReprintUploaded(ctx, in)
		if err != nil {
			return nil, printing.ReprintOutput{}, err
		}
		var views bool
		if req != nil {
			views, _ = clientViewSupport(req.Session)
		}
		txt := "Relance du fichier déjà déposé (impression " + o.PrintJobID + ", source " + o.SourcePrintJobID + ") : " + o.Check.CheckedMsg + ". "
		if o.Reconciled != "" {
			txt += "Réconciliation : " + o.Reconciled + ". "
		}
		txt += confirmationText(o.Confirmation, p.BaseURL(), views)
		return withView(textResult(txt), "request_print_confirmation"), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "resume_print", Title: "Reprendre une impression en pause",
		Description: "Crée la demande de REPRISE d'une impression que l'imprimante a mise en pause (erreur de filament, bobine externe vide, porte ouverte…). " +
			"La reprise n'est envoyée que si l'HUMAIN valide sur action_url (navigateur enregistré, passkey) ou saisit son code de lancement dans la vue : tu ne peux pas la faire à sa place, ne lui demande jamais ce code dans la conversation. " +
			"Une seule commande, jamais renvoyée automatiquement. Avant : vérifie avec lui que la cause de la pause est réglée (get_print_status > alert, printer.error_details). Aucune carte pour cette action : donne-lui action_url.",
		Annotations: phys,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ResumePrintInput) (*mcp.CallToolResult, printing.ActionOutput, error) {
		o, err := p.RequestResume(ctx, in.PrintJobID)
		if err != nil {
			return nil, printing.ActionOutput{}, err
		}
		return textResult(actionText(o)), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "load_filament", Title: "Charger la bobine externe",
		Description: "Crée la demande de CHARGEMENT du filament de la bobine externe (hors AMS) : la buse chauffe à temperature_c puis l'imprimante charge. Refusé pendant l'impression (attendre la pause). " +
			"Même validation humaine que la reprise (action_url ou code de lancement dans la vue), une seule commande, jamais renvoyée. Ensuite, resume_print pour reprendre. Aucune carte pour cette action : donne action_url à l'utilisateur.",
		Annotations: phys,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in LoadFilamentInput) (*mcp.CallToolResult, printing.ActionOutput, error) {
		o, err := p.RequestLoadFilament(ctx, in.PrinterID, in.PrintJobID, in.TemperatureC)
		if err != nil {
			return nil, printing.ActionOutput{}, err
		}
		return textResult(actionText(o)), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "diagnose_connection", Title: "Diagnostiquer la connexion à l'imprimante",
		Description: "Lecture seule : résolution du nom, tentative TCP (ping sans privilèges) sur 8883 (MQTT), 990 (FTPS) et 6000 (caméra), entrée ARP, session MQTT de bambud (dernière connexion, erreurs, prochain essai) " +
			"et diagnostic en français : réseau, service ou imprimante. À utiliser quand l'imprimante est injoignable (« no route to host », hors ligne après un redémarrage).",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in PrinterIDInput) (*mcp.CallToolResult, printing.ConnectionDiagnosis, error) {
		d, err := p.DiagnoseConnection(ctx, in.PrinterID)
		if err != nil {
			return nil, printing.ConnectionDiagnosis{}, err
		}
		var ports []string
		for _, pr := range d.Report.Ports {
			st := "fermé"
			if pr.Open {
				st = "ouvert"
			}
			ports = append(ports, fmt.Sprintf("%d %s", pr.Port, st))
		}
		return textResult(fmt.Sprintf("%s (%s) : %s Ports : %s.", d.PrinterName, d.Host, d.Summary, strings.Join(ports, ", "))), *d, nil
	})

	addTool(s, &mcp.Tool{
		Name: "list_printer_files", Title: "Fichiers de la carte SD de l'imprimante",
		Description: "Lecture seule (FTPS) : fichiers de la carte SD, avec l'impression, le modèle et le profil de découpe connus (par le nom portant l'empreinte courte et le sha256), " +
			"ceux qui servent encore (jamais supprimables), ceux qui sont relançables (reprint_uploaded) et les versions obsolètes candidates au nettoyage (cleanup_printer_files).",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListPrinterFilesInput) (*mcp.CallToolResult, printing.FileList, error) {
		l, err := p.ListPrinterFiles(ctx, in.PrinterID, in.KeepLatest)
		if err != nil {
			return nil, printing.FileList{}, err
		}
		txt := fmt.Sprintf("%d fichier(s) sur la carte (%.1f Mo), %d obsolète(s).", l.Total, float64(l.Bytes)/1e6, l.Obsolete)
		for _, f := range l.Files {
			flag := ""
			switch {
			case f.InUse:
				flag = " [en usage]"
			case f.Obsolete:
				flag = " [obsolète]"
			}
			txt += fmt.Sprintf("\n- %s (%.1f Mo)%s", f.Name, float64(f.Size)/1e6, flag)
			if f.ModelName != "" {
				txt += " · modèle " + f.ModelName
			}
			if len(f.PrintJobIDs) > 0 {
				txt += " · impression " + f.PrintJobIDs[0] + " (" + f.LastJobState + ")"
			}
		}
		return textResult(txt), *l, nil
	})

	addTool(s, &mcp.Tool{
		Name: "cleanup_printer_files", Title: "Nettoyer les fichiers obsolètes de la carte SD",
		Description: "Crée la demande de SUPPRESSION des versions obsolètes déposées par bambud sur la carte SD (jamais un fichier en usage, ni un fichier que bambud n'a pas déposé) et renvoie la liste EXACTE des fichiers. " +
			"Rien n'est supprimé tant que l'HUMAIN n'a pas validé sur action_url (navigateur enregistré) ou saisi son code de lancement dans la vue ; la page montre la liste, le résultat dit ce qui a été supprimé. Aucune carte pour cette action : donne action_url.",
		Annotations: phys,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CleanupFilesInput) (*mcp.CallToolResult, printing.ActionOutput, error) {
		o, err := p.RequestCleanup(ctx, in.PrinterID, in.KeepLatest, in.Names)
		if err != nil {
			return nil, printing.ActionOutput{}, err
		}
		return textResult(actionText(o)), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "validate_action_with_code", Title: "Valider une action (code de lancement)",
		Description: launchToolWarning + " Valide la reprise, le chargement ou le nettoyage visé par la demande d'action.",
		Annotations: phys, Meta: appOnlyMeta(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ApproveActionWithCodeInput) (*mcp.CallToolResult, ActionCodeOut, error) {
		res, err := p.ActionWithCode(ctx, in.ActionID, in.Code)
		if err != nil {
			if prob := printing.LaunchCodeProblem(err); prob != "" {
				st, _ := p.LaunchCodeStatus(ctx)
				out := ActionCodeOut{CodeApprovalOut: CodeApprovalOut{Problem: prob, AttemptsLeft: st.AttemptsLeft, Locked: st.Locked, Message: err.Error()}}
				return textResult("Non validé : " + err.Error()), out, nil
			}
			return nil, ActionCodeOut{}, err
		}
		out := ActionCodeOut{CodeApprovalOut: CodeApprovalOut{Approved: true, AttemptsLeft: printing.LaunchCodeAttempts, Message: res.Message}, Result: res}
		return textResult(res.Message), out, nil
	})
}

func actionText(o *printing.ActionOutput) string {
	txt := fmt.Sprintf("Demande %s créée (%s). QUESTION À POSER : « %s » Rien n'est fait tant que l'humain n'a pas validé. Aucune carte n'accompagne cette action : donne-lui ce lien (navigateur enregistré) : %s (valable jusqu'à %s). Ne lui demande jamais de code dans la conversation.",
		o.ActionID, o.Kind, o.Question, o.ActionURL, o.ExpiresAtLocal)
	for _, d := range o.Details {
		txt += "\n- " + d
	}
	return txt
}
