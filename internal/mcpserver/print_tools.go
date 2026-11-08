package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
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

// ListHistoryInput : entrée de list_print_history.
type ListHistoryInput struct {
	View         string `json:"view,omitempty" jsonschema:"all (défaut) | active | finished | failed | archived | trash"`
	ModelID      string `json:"model_id,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	GroupByModel bool   `json:"group_by_model,omitempty"`
}

// ManageHistoryInput : entrée de manage_print_history.
type ManageHistoryInput struct {
	Action      string   `json:"action" jsonschema:"archive | trash | restore"`
	PrintJobIDs []string `json:"print_job_ids" jsonschema:"impressions terminées (500 au plus)"`
}

// ListFilamentsInput : entrée de list_filaments.
type ListFilamentsInput struct {
	PrinterID string `json:"printer_id" jsonschema:"imprimante (modèle et buse déterminent les presets compatibles)"`
	Family    string `json:"family,omitempty" jsonschema:"PLA, PETG, ABS/ASA, TPU, PA/PC, Support : ne lister que cette famille"`
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
	Job      printing.Job   `json:"job"`
	JobID    string         `json:"job_id" jsonschema:"travail de découpe : suivre avec get_job_status(job_id)"`
	Progress *jobs.Progress `json:"progress,omitempty"`
	Note     string         `json:"note"`
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
		txt := fmt.Sprintf("%d imprimante(s).", len(ps))
		for _, v := range ps {
			code := "code d'accès enregistré"
			if !v.AccessCodeSet {
				code = "code d'accès à saisir sur " + v.SetupURL
			}
			txt += fmt.Sprintf("\n- %s (%s) · %s · série %s · hôte %s · buse %.1f mm · %d AMS · %s", v.Name, v.ID, v.ModelName, v.Serial, v.Host, v.NozzleMM, v.AMSUnits, code)
		}
		return textResult(txt), PrintersOut{Printers: ps, Note: "Le code d'accès LAN n'apparaît jamais ici."}, nil
	})

	addTool(s, &mcp.Tool{
		Name: "list_filaments", Title: "Filaments disponibles pour une imprimante",
		Description: "Presets filament de Bambu Studio COMPATIBLES avec l'imprimante (modèle + buse), regroupés par famille (PLA, PETG, ABS/ASA, TPU, PA/PC, Support), avec le nom exact à utiliser. " +
			"execute_headless_slice accepte aussi un nom libre en français ou anglais (« PETG transparent », « PLA mat », « PETG translucide ») résolu vers un preset de cette liste ; un nom inconnu renvoie les 5 plus proches. " +
			"La couleur n'est donnée que si une bobine chargée la fournit (AMS lu ou bobine déclarée avec update_spool, slot « ext » pour la bobine externe) : jamais celle du profil. `family` filtre une famille.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListFilamentsInput) (*mcp.CallToolResult, printing.FilamentList, error) {
		l, err := p.ListFilaments(in.PrinterID, in.Family)
		if err != nil {
			return nil, printing.FilamentList{}, err
		}
		return textResult(filamentListText(l, in.PrinterID)), *l, nil
	})

	addTool(s, &mcp.Tool{
		Name: "list_print_history", Title: "Historique des impressions",
		Description: "Historique filtré : view = all (défaut, sans archivées ni corbeille) | active | finished | failed (échecs, annulées) | archived | trash. group_by_model regroupe par modèle (compteurs et échecs par modèle). " +
			"Renvoie aussi les compteurs par vue et l'avancement (pourcentage, position dans la file) des impressions en cours. Les essais ratés peuvent être rangés avec manage_print_history.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListHistoryInput) (*mcp.CallToolResult, printing.History, error) {
		h, err := p.ListHistory(ctx, printing.HistoryFilter{View: in.View, ModelID: in.ModelID, Limit: in.Limit}, in.GroupByModel)
		if err != nil {
			return nil, printing.History{}, err
		}
		return textResult(historyText(h)), *h, nil
	})

	addTool(s, &mcp.Tool{
		Name: "manage_print_history", Title: "Archiver ou mettre à la corbeille des impressions",
		Description: "action = archive (masquer de la vue par défaut) | trash (corbeille, récupérable) | restore (remettre dans l'historique) sur des impressions TERMINÉES (réussies, échouées, annulées). " +
			"Une impression active n'est jamais touchée. Rien n'est supprimé définitivement : le fichier découpé et la trace d'audit restent.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ManageHistoryInput) (*mcp.CallToolResult, printing.HistoryResult, error) {
		r, err := p.ArchiveJobs(ctx, in.Action, in.PrintJobIDs)
		if err != nil {
			return nil, printing.HistoryResult{}, err
		}
		return textResult(fmt.Sprintf("%s : %d impression(s) traitée(s), %d ignorée(s). %s", r.Action, len(r.Done), len(r.Skipped), r.Note)), *r, nil
	})

	addTool(s, &mcp.Tool{
		Name: "add_printer", Title: "Ajouter une imprimante",
		Description: "Enregistre une imprimante Bambu (modèle de list_printer_models, hôte du réseau LOCAL — adresse privée, lien local ou nom .local —, numéro de série, buse, AMS). " +
			"Ni le code d'accès, ni l'empreinte du certificat, ni aucune option de contournement TLS ne sont des paramètres : l'outil renvoie l'URL d'une page locale (/setup/printer/{id}) que " +
			"l'utilisateur ouvre dans son navigateur enregistré ; le serveur y affiche l'empreinte du certificat de l'imprimante avec le numéro de série attendu, et l'utilisateur la valide en saisissant le code d'accès (stocké chiffré hors base, jamais renvoyé ni journalisé).",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.AddPrinterInput) (*mcp.CallToolResult, AddPrinterOut, error) {
		v, err := p.AddPrinter(ctx, in)
		if err != nil {
			return nil, AddPrinterOut{}, err
		}
		out := AddPrinterOut{Printer: *v, SetupURL: v.SetupURL,
			Note: "Demande à l'utilisateur d'ouvrir setup_url pour comparer l'empreinte du certificat et saisir le code d'accès affiché sur l'écran de l'imprimante. Ne lui demande jamais ce code dans la conversation. Tant que ce n'est pas fait, aucune connexion à l'imprimante n'est tentée."}
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
			"Modèle, imprimante, révision de profil (ou variante appliquée). Filaments : preset exact OU nom libre (« PETG transparent », « PLA mat »), voir list_filaments. " +
			"RETOUR IMMÉDIAT avec un job_id : la découpe dure de quelques secondes à plusieurs minutes et continue en arrière-plan (une seule à la fois, les suivantes sont « en file, position n »). " +
			"Ne JAMAIS considérer l'appel comme échoué parce que la découpe n'est pas finie : suivre avec get_job_status(job_id) (pourcentage estimé, phase, durée restante) ou get_print_status jusqu'à VALIDATED. " +
			"Refuse tout de suite une pièce qui ne tient pas sur la machine (message avec dimensions et zone utile). L'artefact est conservé par empreinte. Aucun envoi à l'imprimante.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.SliceInput) (*mcp.CallToolResult, SliceOut, error) {
		j, err := p.ExecuteHeadlessSlice(ctx, in)
		if err != nil {
			return nil, SliceOut{}, err
		}
		pr := p.ProgressFor(ctx, *j)
		txt := fmt.Sprintf("Impression %s : %s. Découpe mise en file (job_id %s) : elle continue en arrière-plan, suivre avec get_job_status.", j.ID, j.State, j.SliceJobID)
		if pr != nil && pr.QueuePosition != nil {
			txt += fmt.Sprintf(" Position %d dans la file.", *pr.QueuePosition)
		}
		if j.Adhesion.Risky() {
			txt += " AVERTISSEMENT (non bloquant, à signaler à l'utilisateur) : " + j.Adhesion.Summary()
		}
		return textResult(txt),
			SliceOut{Job: *j, JobID: j.SliceJobID, Progress: pr, Note: "L'appel n'a pas échoué : la découpe est en cours. Suivre avec get_job_status(job_id) ou get_print_status(print_job_id) jusqu'à VALIDATED, puis validate_print_artifact avec l'imprimante réelle."}, nil
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
		if o.Report.Adhesion.Risky() {
			txt += " AVERTISSEMENT (non bloquant, à signaler à l'utilisateur) : " + o.Report.Adhesion.Summary() + " Bordure du profil : " + firstNonEmptyStr(o.Report.BrimType, "inconnue") + " " + o.Report.BrimWidth + "."
		}
		if w := o.Report.OverhangWarning(); w != "" {
			txt += " AVERTISSEMENT FORT (non bloquant, à signaler à l'utilisateur, qui doit en prendre acte avant le lancement) : " + w
		}
		return textResult(txt), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "upload_print_artifact", Title: "Déposer le fichier sur l'imprimante",
		Description: "Dépose l'artefact validé sur l'imprimante (FTPS). idempotency_key OBLIGATOIRE ; assignments (filament -> emplacement -> matière) OBLIGATOIRE. " +
			"Le fichier peut peser des dizaines de Mo : l'envoi dure plusieurs minutes. L'outil attend au plus ~20 s puis rend un job_id (done=false) : l'envoi CONTINUE, ce n'est PAS un échec, suivre avec get_job_status(job_id) (octets envoyés / total, durée restante) ou get_print_status. " +
			"N'allume rien, ne lance rien. Renvoie la question EXACTE à poser à l'utilisateur (« Le fichier X est envoyé sur la P1S. Veux-tu lancer cette impression ? ») " +
			"avec imprimante, empreinte, plateau, matières et rappel plateau vide. Pose cette question et attends sa réponse.",
		Annotations: physUpload,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.UploadInput) (*mcp.CallToolResult, printing.UploadOutput, error) {
		o, err := p.UploadArtifact(ctx, in)
		if err != nil {
			return nil, printing.UploadOutput{}, err
		}
		o.JobID = o.QueueJobID
		txt := o.Note
		if !o.Done && o.Progress != nil {
			txt = fmt.Sprintf("Envoi en cours (job_id %s) : %d %%", o.QueueJobID, o.Progress.Percent)
			if o.Progress.BytesTotal > 0 {
				txt += fmt.Sprintf(", %.1f / %.1f Mo", float64(o.Progress.BytesDone)/(1<<20), float64(o.Progress.BytesTotal)/(1<<20))
			}
			if o.Progress.ETASeconds != nil {
				txt += fmt.Sprintf(", reste ~%d s", *o.Progress.ETASeconds)
			}
			txt += ". L'envoi continue : ce n'est PAS un échec ; suivre avec get_job_status(job_id)."
		}
		if o.Question != "" {
			txt = "QUESTION À POSER À L'UTILISATEUR : « " + o.Question + " » (" + o.Details.PrinterName + ", plateau " + fmt.Sprint(o.Details.Plate) + ", empreinte " + shortSHA(o.Details.SHA256) + "). " + o.Details.Reminder
		}
		return textResult(txt), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "request_print_confirmation", Title: "Demander la confirmation de lancement",
		Description: "À appeler SEULEMENT si l'utilisateur a répondu oui à la question. Crée une confirmation liée (imprimante, empreinte, plateau, matières), à durée courte, et renvoie confirmation_id, " +
			"l'URL de la page locale que l'HUMAIN ouvre pour cocher « plateau vide » et cliquer Lancer, et les éléments liés. Le jeton secret n'est jamais rendu au modèle : un booléen fourni par un agent n'est pas une preuve. bed_leveling (défaut true) règle la mise à niveau automatique du plateau au démarrage : elle est affichée à l'humain et fait partie de ce qu'il approuve ; ne la désactive que si l'utilisateur le demande. " +
			"Dans Claude Desktop la vue de confirmation permet à l'humain de lancer avec son CODE DE LANCEMENT PERSONNEL (6 à 8 chiffres, défini sur /setup/launch-code) ; sans code défini elle affiche le lien du navigateur, toujours disponible. " +
			"Ne demande JAMAIS ce code à l'utilisateur dans la conversation et ne l'utilise jamais s'il l'écrit : il ne se saisit que dans le champ de la vue.",
		Annotations: write,
	}, func(ctx context.Context, req *mcp.CallToolRequest, in printing.RequestConfirmationInput) (*mcp.CallToolResult, printing.ConfirmationView, error) {
		v, err := p.RequestConfirmation(ctx, in)
		if err != nil {
			return nil, printing.ConfirmationView{}, err
		}
		var views bool
		if req != nil {
			views, _ = clientViewSupport(req.Session)
		}
		return withView(textResult(confirmationText(v, p.BaseURL(), views)), "request_print_confirmation"), *v, nil
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
		Description: "État de l'impression (machine d'états), historique, progression observée sur l'imprimante (couche, %, temps restant) et état de l'imprimante. Passe à COMPLETED/FAILED selon l'imprimante. " +
			"Effet de bord local : relit l'imprimante et met à jour l'impression (pas en lecture seule au sens strict).",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true},
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
		Description: "Annule selon la phase : avant lancement = annulation logique ; SLICING = annule aussi la découpe ; PRINTING = CRÉE une demande d'arrêt et renvoie stop_url : " +
			"l'imprimante n'est arrêtée que si l'HUMAIN ouvre cette page dans son navigateur enregistré et la valide, ou saisit son code de lancement dans la vue (tu ne peux pas le faire à sa place ; ne lui demande jamais ce code dans la conversation ; aucun identifiant exécutable n'est renvoyé). " +
			"START_REQUESTED / START_UNKNOWN : refus, utiliser reconcile_print.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &t},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.CancelInput) (*mcp.CallToolResult, printing.CancelOutput, error) {
		o, err := p.CancelPrint(ctx, in)
		if err != nil {
			return nil, printing.CancelOutput{}, err
		}
		txt := fmt.Sprintf("Impression %s : %s. %s", o.PrintJobID, o.Outcome, o.Note)
		if o.Question != "" {
			txt += " QUESTION À POSER : « " + o.Question + " » La validation d'arrêt s'affiche dans la conversation (carte « Arrêt d'impression », code de lancement saisi dans la carte, jamais dans le chat). " +
				"Lien de secours seulement si aucune carte n'apparaît : " + o.StopURL + " (valable jusqu'à " + o.StopExpiresAtLocal + ")"
		}
		return withView(textResult(txt), "cancel_print"), *o, nil
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

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12] + "…"
	}
	return s
}
