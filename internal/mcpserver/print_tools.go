package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// Outils de la chaîne d'impression (dossier §6 et §10), au-dessus de internal/printing.
// Séquence imposée : préparer -> valider -> envoyer -> demander explicitement -> confirmer (humain) -> lancer.
// Le code d'accès de l'imprimante n'est JAMAIS une entrée d'outil : add_printer renvoie l'URL
// d'une page locale où l'humain le saisit.

// PrintersOut liste les imprimantes.
type PrintersOut struct {
	Printers []printing.PrinterView `json:"printers"`
	Note     string                 `json:"note"`
}

// PrinterModelsOut liste les modèles connus.
type PrinterModelsOut struct {
	Models []contracts.PrinterModel `json:"models"`
	Note   string                   `json:"note"`
}

// AddPrinterOut : imprimante ajoutée et lien de saisie du code.
type AddPrinterOut struct {
	Printer  printing.PrinterView `json:"printer"`
	SetupURL string               `json:"setup_url" jsonschema:"à donner à l'utilisateur : il y saisit le code d'accès LAN lui-même"`
	Note     string               `json:"note"`
}

// PrinterIDInput identifie une imprimante.
type PrinterIDInput struct {
	PrinterID string `json:"printer_id"`
}

// PrintJobIDInput identifie une impression.
type PrintJobIDInput struct {
	PrintJobID string `json:"print_job_id"`
}

// ConfirmationIDInput identifie une confirmation.
type ConfirmationIDInput struct {
	ConfirmationID string `json:"confirmation_id"`
}

// SliceOut : impression créée.
type SliceOut struct {
	Job  printing.Job `json:"job"`
	Note string       `json:"note"`
}

func appOnlyMeta() mcp.Meta { return mcp.Meta{"ui": map[string]any{"visibility": []string{"app"}}} }

// AddPrintTools enregistre les outils d'impression. Fonction séparée : une seule ligne dans New().
func AddPrintTools(s *mcp.Server, p *printing.Service) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	f, t := false, true
	write := &mcp.ToolAnnotations{DestructiveHint: &f}
	// Effet physique : le dépôt est idempotent ; le lancement ne l'est que par sa clé et n'est jamais rejoué.
	physUpload := &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true, OpenWorldHint: &t}
	physStart := &mcp.ToolAnnotations{DestructiveHint: &t, OpenWorldHint: &t}

	addTool(s, &mcp.Tool{
		Name: "list_printer_models", Title: "Modèles d'imprimantes Bambu",
		Description: "Modèles Bambu Lab connus (volume, buses, plateaux) semés depuis les profils de Bambu Studio. Une valeur inconnue est absente, jamais devinée.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, PrinterModelsOut, error) {
		ms, err := p.ListPrinterModels(ctx)
		if err != nil {
			return nil, PrinterModelsOut{}, err
		}
		note := fmt.Sprintf("%d modèle(s).", len(ms))
		if len(ms) == 0 {
			note = "Aucun modèle : les profils de Bambu Studio ne sont pas chargés (Bambu Studio absent ?)."
		}
		return textResult(note), PrinterModelsOut{Models: ms, Note: note}, nil
	})

	addTool(s, &mcp.Tool{
		Name: "list_printers", Title: "Imprimantes du parc",
		Description: "Imprimantes enregistrées (modèle, hôte, série, buse, AMS déclarés) et si un code d'accès est enregistré. Le code lui-même n'est jamais renvoyé.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, PrintersOut, error) {
		ps, err := p.ListPrinters(ctx)
		if err != nil {
			return nil, PrintersOut{}, err
		}
		return textResult(fmt.Sprintf("%d imprimante(s).", len(ps))), PrintersOut{Printers: ps, Note: "Le code d'accès LAN n'apparaît jamais ici."}, nil
	})

	addTool(s, &mcp.Tool{
		Name: "add_printer", Title: "Ajouter une imprimante",
		Description: "Enregistre une imprimante Bambu (modèle de list_printer_models, hôte, numéro de série, buse, AMS). Le code d'accès n'est PAS un paramètre : " +
			"l'outil renvoie l'URL d'une page locale (/setup/printer/{id}) que l'utilisateur ouvre pour le saisir ; il est stocké chiffré hors base et n'est jamais renvoyé ni journalisé.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.AddPrinterInput) (*mcp.CallToolResult, AddPrinterOut, error) {
		v, err := p.AddPrinter(ctx, in)
		if err != nil {
			return nil, AddPrinterOut{}, err
		}
		out := AddPrinterOut{Printer: *v, SetupURL: v.SetupURL,
			Note: "Demande à l'utilisateur d'ouvrir setup_url pour saisir le code d'accès affiché sur l'écran de l'imprimante. Ne lui demande jamais ce code dans la conversation."}
		return textResult(fmt.Sprintf("Imprimante %s (%s) enregistrée sous %s. Page de saisie du code d'accès : %s", v.Name, v.ModelName, v.ID, v.SetupURL)), out, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_printer_status", Title: "État de l'imprimante",
		Description: "État normalisé lu via l'adaptateur (idle, preparing, printing...), progression, températures, erreurs, FRAÎCHEUR (âge de la dernière information, stale). " +
			"Un état idle ne prouve pas que le plateau est vide.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in PrinterIDInput) (*mcp.CallToolResult, printing.PrinterStatus, error) {
		st, err := p.GetPrinterStatus(ctx, in.PrinterID)
		if err != nil {
			return nil, printing.PrinterStatus{}, err
		}
		age := "âge inconnu"
		if st.AgeSeconds != nil {
			age = fmt.Sprintf("info vieille de %d s", *st.AgeSeconds)
		}
		return textResult(fmt.Sprintf("Imprimante %s : %s, %s%s.", in.PrinterID, st.State, age, map[bool]string{true: " (périmée)", false: ""}[st.Stale])), *st, nil
	})

	addTool(s, &mcp.Tool{
		Name: "execute_headless_slice", Title: "Découper pour une imprimante",
		Description: "Crée une impression (DRAFT -> PREPARED), puis met en file la découpe par Bambu Studio headless (SLICING -> VALIDATED après validation de l'archive). " +
			"Modèle, imprimante, révision de profil (ou variante appliquée). Suivre avec get_print_status ; l'artefact est conservé par empreinte. Aucun envoi à l'imprimante.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.SliceInput) (*mcp.CallToolResult, SliceOut, error) {
		j, err := p.ExecuteHeadlessSlice(ctx, in)
		if err != nil {
			return nil, SliceOut{}, err
		}
		return textResult(fmt.Sprintf("Impression %s : %s. Découpe en file (travail %s).", j.ID, j.State, j.SliceJobID)),
			SliceOut{Job: *j, Note: "Suivre avec get_print_status(print_job_id) jusqu'à VALIDATED, puis validate_print_artifact avec l'imprimante réelle."}, nil
	})

	addTool(s, &mcp.Tool{
		Name: "validate_print_artifact", Title: "Valider un artefact pour une imprimante",
		Description: "Rapport : validation d'archive (structure, G-code, trajectoires) ET compatibilité avec l'imprimante réelle (modèle, buse, volume, plateau, matières/AMS déclarés). " +
			"Accepte print_job_id, ou artifact_id (empreinte d'un .gcode.3mf du stockage, ex. une variante). Les contrôles bloquants empêchent le dépôt.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.ValidateInput) (*mcp.CallToolResult, printing.ValidateOutput, error) {
		o, err := p.ValidateArtifact(ctx, in)
		if err != nil {
			return nil, printing.ValidateOutput{}, err
		}
		txt := fmt.Sprintf("Impression %s (%s) : validation %s — %d bloquant(s), %d avertissement(s).", o.PrintJobID, o.State, map[bool]string{true: "RÉUSSIE", false: "EN ÉCHEC"}[o.Report.OK], len(o.Report.Blocking), len(o.Report.Warnings))
		if len(o.Report.Blocking) > 0 {
			txt += " " + strings.Join(o.Report.Blocking, " ; ")
		}
		return textResult(txt), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "upload_print_artifact", Title: "Déposer le fichier sur l'imprimante",
		Description: "Dépose l'artefact validé sur l'imprimante (FTPS). idempotency_key OBLIGATOIRE ; assignments (filament -> emplacement -> matière) OBLIGATOIRE. " +
			"N'allume rien, ne lance rien. Renvoie la question EXACTE à poser à l'utilisateur (« Le fichier X est envoyé sur la P1S. Veux-tu lancer cette impression ? ») " +
			"avec imprimante, empreinte, plateau, matières et rappel plateau vide. Pose cette question et attends sa réponse.",
		Annotations: physUpload,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.UploadInput) (*mcp.CallToolResult, printing.UploadOutput, error) {
		o, err := p.UploadArtifact(ctx, in)
		if err != nil {
			return nil, printing.UploadOutput{}, err
		}
		txt := o.Note
		if o.Question != "" {
			txt = "QUESTION À POSER À L'UTILISATEUR : « " + o.Question + " » (" + o.Details.PrinterName + ", plateau " + fmt.Sprint(o.Details.Plate) + ", empreinte " + shortSHA(o.Details.SHA256) + "). " + o.Details.Reminder
		}
		return textResult(txt), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "request_print_confirmation", Title: "Demander la confirmation de lancement",
		Description: "À appeler SEULEMENT si l'utilisateur a répondu oui à la question. Crée une confirmation liée (imprimante, empreinte, plateau, matières), à durée courte, et renvoie confirmation_id, " +
			"l'URL de la page locale que l'HUMAIN ouvre pour cocher « plateau vide » et cliquer Lancer, et les éléments liés. Le jeton secret n'est jamais rendu au modèle : un booléen fourni par un agent n'est pas une preuve.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.RequestConfirmationInput) (*mcp.CallToolResult, printing.ConfirmationView, error) {
		v, err := p.RequestConfirmation(ctx, in)
		if err != nil {
			return nil, printing.ConfirmationView{}, err
		}
		b := v.Binding
		return textResult(fmt.Sprintf("Confirmation %s créée pour %s sur %s (plateau %d, empreinte %s), valable jusqu'à %s. L'utilisateur doit ouvrir %s, vérifier, cocher « plateau vide » et cliquer Lancer. Ensuite seulement : start_print.",
			v.ConfirmationID, b.File, b.PrinterName, b.Plate, shortSHA(b.SHA256), v.ExpiresAt.Format("15:04:05"), v.ConfirmURL)), *v, nil
	})

	addTool(s, &mcp.Tool{
		Name: "start_print", Title: "Lancer l'impression",
		Description: "Lance l'impression déposée. Refuse sans confirmation_id approuvée PAR L'HUMAIN et liée à CETTE impression ; la confirmation est consommée atomiquement, une seule fois ; " +
			"état de l'imprimante vérifié frais et idle ; idempotency_key obligatoire ; le lancement n'est jamais rejoué. START_REQUESTED -> PRINTING observé, ou START_UNKNOWN en cas de doute : " +
			"alors reconcile_print, JAMAIS de relance. Une autorisation ne vaut pas pour l'impression suivante.",
		Annotations: physStart,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.StartInput) (*mcp.CallToolResult, printing.StartOutput, error) {
		o, err := p.StartPrint(ctx, in)
		if err != nil {
			return nil, printing.StartOutput{}, err
		}
		return textResult(fmt.Sprintf("Impression %s : %s (%s). %s", o.PrintJobID, o.State, o.Outcome, o.Note)), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_print_status", Title: "Suivi d'une impression",
		Description: "État de l'impression (machine d'états), historique, progression observée sur l'imprimante (couche, %, temps restant) et état de l'imprimante. Passe à COMPLETED/FAILED selon l'imprimante.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in PrintJobIDInput) (*mcp.CallToolResult, printing.PrintStatus, error) {
		st, err := p.GetPrintStatus(ctx, in.PrintJobID)
		if err != nil {
			return nil, printing.PrintStatus{}, err
		}
		txt := fmt.Sprintf("Impression %s : %s.", st.Job.ID, st.Job.State)
		if st.Job.Print != nil {
			txt += fmt.Sprintf(" Imprimante : %s, %d %%, couche %d/%d.", st.Job.Print.PrinterState, st.Job.Print.Percent, st.Job.Print.Layer, st.Job.Print.TotalLayers)
		}
		if st.Job.LastError != "" {
			txt += " Dernière erreur : " + st.Job.LastError
		}
		if st.Job.Next != "" {
			txt += " Suite : " + st.Job.Next + "."
		}
		return textResult(txt), *st, nil
	})

	addTool(s, &mcp.Tool{
		Name: "cancel_print", Title: "Annuler une impression",
		Description: "Annule selon la phase : avant lancement = annulation logique ; SLICING = annule aussi la découpe ; PRINTING = arrêt de l'imprimante après une confirmation séparée " +
			"(premier appel -> stop_confirmation_id et question ; second appel avec stop_confirmation_id une fois l'utilisateur d'accord). START_REQUESTED / START_UNKNOWN : refus, utiliser reconcile_print.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &t},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.CancelInput) (*mcp.CallToolResult, printing.CancelOutput, error) {
		o, err := p.CancelPrint(ctx, in)
		if err != nil {
			return nil, printing.CancelOutput{}, err
		}
		txt := fmt.Sprintf("Impression %s : %s. %s", o.PrintJobID, o.Outcome, o.Note)
		if o.Question != "" {
			txt += " QUESTION À POSER : « " + o.Question + " »"
		}
		return textResult(txt), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "reconcile_print", Title: "Réconcilier un lancement incertain",
		Description: "Après START_UNKNOWN (perte de lien après la commande) : lit l'état réel de l'imprimante sans JAMAIS renvoyer de commande de lancement. started -> PRINTING ; not_started -> UPLOADED " +
			"(nouvelle confirmation humaine requise) ; unknown -> reste START_UNKNOWN.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in PrintJobIDInput) (*mcp.CallToolResult, printing.ReconcileOutput, error) {
		o, err := p.Reconcile(ctx, in.PrintJobID)
		if err != nil {
			return nil, printing.ReconcileOutput{}, err
		}
		return textResult(fmt.Sprintf("Réconciliation de %s : %s (%s) -> %s. %s", o.PrintJobID, o.Outcome, o.Reason, o.State, o.Note)), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "capture_preview", Title: "Aperçu image",
		Description: "PNG rendu côté serveur (Go pur, sans GPU) : vue isométrique orthographique d'un maillage (model_id) OU tracé 2D des trajectoires extrudées d'une couche du G-code (artifact_id / print_job_id, layer). " +
			"La légende donne les paramètres de rendu et dit explicitement ce qui est représenté ou non ; un aperçu ne démontre ni résistance, ni aspect, ni étanchéité.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.PreviewInput) (*mcp.CallToolResult, printing.PreviewOutput, error) {
		o, err := p.CapturePreview(ctx, in)
		if err != nil {
			return nil, printing.PreviewOutput{}, err
		}
		res := &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: strings.Join(o.Legend, "\n") + "\nNon représenté : " + strings.Join(o.NotRepresented, " ; ")},
			&mcp.ImageContent{Data: o.PNG, MIMEType: "image/png"},
		}}
		return res, *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "create_parametric_model", Title: "Créer un modèle paramétrique",
		Description: "Génère un modèle depuis un gabarit du worker CAO (clamp, bracket, box, spacer, hook) avec des dimensions en mm, l'enregistre au catalogue (STEP + STL, version v1) " +
			"et renvoie les hypothèses du worker, ses avertissements et les mesures calculées. Échoue clairement si le backend CAO est absent.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.CreateParametricInput) (*mcp.CallToolResult, printing.CreateParametricOutput, error) {
		o, err := p.CreateParametricModel(ctx, in)
		if err != nil {
			return nil, printing.CreateParametricOutput{}, err
		}
		return textResult(fmt.Sprintf("Modèle %s créé (version %s, gabarit %s) ; %d hypothèse(s), %d avertissement(s).", o.ModelID, o.VersionID, o.Template, len(o.Assumptions), len(o.Warnings))), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "modify_geometry", Title: "Modifier la géométrie",
		Description: "Opérations typées sur le maillage : scale, rotate, mirror, translate, center_on_bed, lay_flat (pose à plat), cut_plane (coupe plane, capuchon si section simple). " +
			"Produit une NOUVELLE version du modèle (jamais d'écrasement) et les différences (boîte englobante, volume, étanchéité). base_version_id périmée = refus.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.ModifyGeometryInput) (*mcp.CallToolResult, printing.ModifyGeometryOutput, error) {
		o, err := p.ModifyGeometry(ctx, in)
		if err != nil {
			return nil, printing.ModifyGeometryOutput{}, err
		}
		a := o.Diff.After.SizeMM
		return textResult(fmt.Sprintf("Version %s créée (parent %s) : %d opération(s), boîte %.1f x %.1f x %.1f mm, volume x%.3f, %d avertissement(s).",
			o.VersionID, o.ParentVersionID, len(o.Applied), a[0], a[1], a[2], o.Diff.VolumeRatio, len(o.Warnings))), *o, nil
	})

	// ---- vue de l'application MCP : LECTURE SEULE. Aucun outil MCP n'approuve un lancement ni un arrêt : l'accord humain ne passe que par les pages web (cookie de navigateur enregistré). ----
	addTool(s, &mcp.Tool{
		Name: "get_print_confirmation_view", Title: "Confirmation (vue application)",
		Description: "Réservé à l'application, lecture seule : éléments liés d'une confirmation à afficher à l'humain (ni jeton, ni nonce, aucun moyen d'approuver).",
		Annotations: ro, Meta: appOnlyMeta(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ConfirmationIDInput) (*mcp.CallToolResult, printing.ConfirmationView, error) {
		v, err := p.GetConfirmation(ctx, in.ConfirmationID)
		if err != nil {
			return nil, printing.ConfirmationView{}, err
		}
		return textResult("Confirmation " + v.ConfirmationID), *v, nil
	})
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12] + "…"
	}
	return s
}
