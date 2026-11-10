// Package mcpserver expose le cœur via le SDK MCP officiel (Go) : outils,
// ressources MCP Apps. Les logs ne vont JAMAIS sur stdout (transport stdio).
package mcpserver

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/issues"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/strength"
)

//go:embed ui/atelier.html
var atelierHTML string

// AtelierURI est la ressource UI de démonstration.
const AtelierURI = "ui://bambu/atelier.html"

const actor = "mcp"

// New construit le serveur MCP au-dessus du cœur.
func New(c *core.Core) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "bambu-mcp", Title: "Atelier Bambu Lab", Version: core.Version},
		&mcp.ServerOptions{
			Logger: c.Log,
			InitializedHandler: func(_ context.Context, req *mcp.InitializedRequest) {
				if c.Log != nil {
					logClientInitialized(c.Log, req) // preuve : le client a-t-il annoncé les vues MCP Apps ?
				}
			},
			Instructions: "Atelier 3D pour imprimantes Bambu Lab. Les travaux longs renvoient un job_id : " +
				"suivre avec get_job_status. Aucune impression ne démarre sans confirmation explicite. " +
				"La confirmation de lancement (request_print_confirmation) et la validation d'arrêt (cancel_print) s'affichent dans la conversation, sous forme de carte : ne renvoie pas l'utilisateur vers un lien tant qu'il ne dit pas ne voir aucune carte. " +
				"Ne demande JAMAIS à l'utilisateur son code de lancement personnel dans la conversation : il le saisit seulement dans la vue de confirmation. " +
				GcodeSuggestion + " " +
				"Pour montrer une pièce importée ou envoyée : get_model_details (viewer 3D) ; ne fabrique pas tes propres rendus. " +
				"Pour envoyer un fichier créé dans ton espace de code : create_upload_link (une commande curl à exécuter) de préférence, sinon upload_file par morceaux de 75000 octets ; l'utilisateur peut aussi le déposer lui-même avec show_file_drop. " +
				"Pour toute question d'avancement (où en est l'impression, combien de temps, progression), appeler get_print_status ou get_printer_status : la carte de suivi s'affiche. " +
				strength.InstructionText + " " +
				issues.InstructionText,
		})
	if mw := mcpLogMiddleware(c.Log); mw != nil {
		s.AddReceivingMiddleware(mw) // journal INFO des requêtes MCP (mcplog.go) ; BAMBU_LOG_MCP=off pour le couper
	}
	s.AddReceivingMiddleware(strengthHintMiddleware(c)) // propose un test de résistance quand la pièce semble porteuse (strength_tools.go)
	h := &handlers{c: c}
	roAnn := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

	addTool(s, &mcp.Tool{
		Name: "inspect_environment", Title: "Inspecter l'environnement",
		Description: "Versions, exécutables détectés (Bambu Studio, Python), droits d'écriture et éléments manquants. Aucune donnée sensible.",
		Annotations: roAnn,
	}, h.inspectEnvironment)

	addTool(s, &mcp.Tool{
		Name: "get_capabilities", Title: "Capacités du service",
		Description: "Fonctions réellement disponibles et leurs limites (une fonction non implémentée est annoncée indisponible).",
		Annotations: roAnn,
	}, h.getCapabilities)

	addTool(s, &mcp.Tool{
		Name: "get_job_status", Title: "État d'un travail",
		Description: "État, progression RÉELLE (progress_detail : percent, phase, octets envoyés / total, durée restante, position dans la file), journaux bornés, résultat ou erreur d'un travail. " +
			"À utiliser pour suivre toute opération longue (découpe, dépôt, import) : un outil qui rend un job_id sans avoir fini N'A PAS échoué, il continue en arrière-plan ; rappeler get_job_status toutes les 10 à 30 s jusqu'à succeeded / failed.",
		Annotations: roAnn,
	}, h.getJobStatus)

	f := false
	addTool(s, &mcp.Tool{
		Name: "cancel_job", Title: "Annuler un travail",
		Description: "Annule un travail selon sa phase : en file = immédiat ; en cours = demande relayée au travail (état final ensuite) ; terminé = sans effet.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true},
	}, h.cancelJob)

	AddAppResource(s, AppResource{
		URI: AtelierURI, Name: "atelier", Title: "Atelier (démonstration)",
		Description: "Vue de démonstration MCP Apps du jalon 1.", HTML: atelierHTML,
		// CSP vide : aucune connexion externe, tout passe par les outils.
	})
	addTool(s, &mcp.Tool{
		Name: "show_atelier", Title: "Ouvrir l'atelier",
		Description: "Affiche la vue Atelier (démonstration technique) dans l'interface de Claude quand l'hôte gère MCP Apps ; renvoie aussi un résumé texte. Ce n'est PAS un viewer : pour voir un modèle, utiliser get_model_details (viewer 3D) ou capture_preview(model_id, views=[all]) ; pour une couche ou la couche 1, capture_preview (layers, first_layer) ou show_layers.",
		Annotations: roAnn,
		Meta:        AppToolMeta(AtelierURI),
	}, h.showAtelier)
	AddWaitTools(s, c) // attente bloquante : wait_for_job, wait_for_print_change (wait_tools.go)
	registerCatalogTools(s, &catalogHandlers{svc: c.Catalog})
	registerUploadTools(s, c)
	registerLibraryTools(s, c)
	registerAppViews(s, c.Catalog) // jalon 3 : vues ui://bambu/*, get_mesh_chunk (app_views.go)
	registerCardViews(s)           // cartes bibliothèque, comparaison, coûts, bobines, impressions, imprimantes (card_views.go)
	registerAgentTools(s, c)
	if c.Goals != nil {
		AddGoalTools(s, c.Goals)
	}
	if c.Insights != nil {
		AddInsightTools(s, c.Insights, c.Filament) // coût, journal, tableau de bord, post-traitement (insight_tools.go)
	}
	if c.Printing != nil {
		AddPrintTools(s, c.Printing, c.Camera) // chaîne d'impression (print_tools.go)
		AddPlateTools(s, c.Printing)           // plateau multi-pièces (plate_tools.go)
		AddLayerTools(s, c.Printing)           // visionneuse de trajectoires : show_layers, get_layer_paths (layer_tools.go)
		AddGcodeTools(s, c.Printing)           // visionneuse 3D couche par couche : show_gcode_preview, get_gcode_layers, get_gcode_slice (gcode_tools.go)
		AddLaunchTools(s, c)                   // approbation par code de lancement depuis la vue (launch_tools.go)
		AddLiveTools(s, c)                     // carte de suivi en direct : image caméra partagée, état (live_tools.go)
		AddRecoveryTools(s, c.Printing)        // relance, reprise, chargement, fichiers de la carte, diagnostic (recovery_tools.go)
		AddAccessoryTools(s, c.Printing)       // lumières, ventilateurs, vitesse de l'impression en cours (accessory_tools.go)
		AddStrengthTools(s, c)                 // test de résistance : plan_strength_test, assess_strength, get_strength_report (strength_tools.go)
	}
	if c.Camera != nil && c.Notify != nil {
		AddCameraTools(s, c) // caméra, détection d'échec, notifications (camera_tools.go)
	}
	if c.Filament != nil {
		registerAMSTools(s, c.Filament) // bobines, AMS, multicolore, file d'attente (ams_tools.go)
	}
	registerFilamentCatalogTools(s) // catalogue officiel des filaments Bambu, embarqué, lecture seule (filcatalog_tools.go)
	AddFormTools(s, c)              // cartes-formulaires : vue ui://bambu/form.html et outils de soumission réservés à l'application (form_tools.go)
	AddIssueTools(s, c)             // carnet de bord des dysfonctionnements : report_issue, list_issues, update_issue, export_issues (issue_tools.go)
	if c.Export != nil {
		AddExportTools(s, c) // export de projet : archive zip partageable (export_tools.go)
	}
	return s
}

// addTool pose les schémas depuis contracts (source unique, voir contracts/schema.go).
func addTool[In, Out any](s *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	t.InputSchema = contracts.MustSchemaFor[In]()
	t.OutputSchema = objectOnly(contracts.MustSchemaFor[Out]())
	if m, ok := toolViewMeta[t.Name]; ok {
		if t.Meta == nil {
			t.Meta = m // lien outil -> vue MCP App (app_views.go)
		}
		h = withViewMeta(m, h) // le résultat porte aussi le lien (certains hôtes le lisent dans le résultat)
	}
	mcp.AddTool(s, t, h)
}

// withViewMeta ajoute le lien vers la vue au résultat d'un outil lié à une vue, sans toucher à son contenu : les
// handlers n'ont pas à appeler withView (le contenu texte reste la seule chose lue par le modèle).
func withViewMeta[In, Out any](meta mcp.Meta, h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		res, out, err := h(ctx, req, in)
		if res != nil && err == nil && res.Meta == nil {
			res.Meta = mcp.Meta{}
			for k, v := range meta {
				res.Meta[k] = v
			}
		}
		return res, out, err
	}
}

// objectOnly ramène un schéma de sortie à `type: "object"` exact. Une sortie en
// pointeur (*T) est générée avec types ["null","object"], que la spec MCP et le SDK
// TypeScript (Claude Desktop) refusent : tout tools/list est alors rejeté. Le
// handler ne renvoie jamais nil en succès, donc "null" n'est pas une sortie valide.
func objectOnly(sc *jsonschema.Schema) *jsonschema.Schema {
	if sc != nil && sc.Type != "object" {
		for _, ty := range sc.Types {
			if ty == "object" {
				sc.Type, sc.Types = "object", nil
				break
			}
		}
	}
	return sc
}

type handlers struct{ c *core.Core }

// ---- entrées/sorties ----

// Empty est l'entrée des outils sans paramètre.
type Empty struct{}

// GetCapabilitiesInput : session Studio facultative (réservée aux jalons suivants).
type GetCapabilitiesInput struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"session Bambu Studio éventuelle ; ignorée tant qu'aucun agent Windows n'est connecté"`
}

// JobIDInput identifie un travail.
type JobIDInput struct {
	JobID string `json:"job_id" jsonschema:"identifiant du travail, ex. job_ab12..."`
}

// GetJobStatusInput ajoute la taille de la fenêtre de journaux.
type GetJobStatusInput struct {
	JobID   string `json:"job_id" jsonschema:"identifiant du travail, ex. job_ab12..."`
	LogTail int    `json:"log_tail,omitempty" jsonschema:"nombre de lignes de journal à renvoyer (défaut 50, max 200)"`
}

// CancelJobOutput décrit l'issue d'une demande d'annulation.
type CancelJobOutput struct {
	JobID   string             `json:"job_id"`
	Outcome string             `json:"outcome" jsonschema:"cancelled | cancel_requested | already_finished"`
	State   contracts.JobState `json:"state"`
	Note    string             `json:"note"`
}

// AtelierSummary est le contenu structuré de show_atelier.
type AtelierSummary struct {
	Title        string `json:"title"`
	ServiceReady bool   `json:"service_ready"`
	ActiveJobs   int    `json:"active_jobs"`
	Demo         bool   `json:"demo" jsonschema:"toujours vrai au jalon 1 : aucune donnée réelle"`
}

// ---- outils ----

func (h *handlers) inspectEnvironment(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, contracts.EnvironmentReport, error) {
	rep, err := h.c.Environment(ctx)
	if err != nil {
		return nil, rep, err
	}
	txt := fmt.Sprintf("Service %s, %s/%s, schéma v%d. Manquants : %d.", rep.ServiceVersion, rep.OS, rep.Arch, rep.SchemaVersion, len(rep.Missing))
	return textResult(txt), rep, nil
}

func (h *handlers) getCapabilities(_ context.Context, _ *mcp.CallToolRequest, _ GetCapabilitiesInput) (*mcp.CallToolResult, contracts.Capabilities, error) {
	cp := h.c.Capabilities()
	n := 0
	for _, f := range cp.Features {
		if f.Available {
			n++
		}
	}
	return textResult(fmt.Sprintf("Mode %s, %d/%d fonctions disponibles.", cp.Mode, n, len(cp.Features))), cp, nil
}

// JobStatusOut : le travail et son avancement lisible (pourcentage, phase, octets, durée restante, position).
type JobStatusOut struct {
	contracts.Job
	Detail jobs.Progress `json:"progress_detail" jsonschema:"avancement : percent, phase, bytes_done/bytes_total, eta_seconds, queue_position (travail en file), stalled"`
}

func (h *handlers) getJobStatus(ctx context.Context, _ *mcp.CallToolRequest, in GetJobStatusInput) (*mcp.CallToolResult, JobStatusOut, error) {
	raw, err := h.c.Queue.Get(ctx, in.JobID)
	if err != nil {
		return nil, JobStatusOut{Job: raw}, err
	}
	out, txt := h.jobStatusOf(ctx, raw, in.LogTail)
	return textResult(txt), out, nil
}

// jobStatusOf compose l'état d'un travail (sans secret) et son texte : la ligne de barre « Dépôt sur la P1S :
// ███████░░░ 72 % (21,6 / 30 Mo, ~1 min) » est celle de jobs.Progress.Text, identique à ce que lisent les clients.
func (h *handlers) jobStatusOf(ctx context.Context, raw contracts.Job, logTail int) (JobStatusOut, string) {
	prog := h.c.Queue.ProgressOf(ctx, raw)
	j := raw.Redacted() // jamais de champs secrets vers le client
	tail := logTail
	if tail <= 0 {
		tail = 50
	}
	if tail > 200 {
		tail = 200
	}
	if len(j.Logs) > tail {
		j.Logs = j.Logs[len(j.Logs)-tail:]
	}
	txt := fmt.Sprintf("Travail %s (%s) : %s. %s", j.ID, j.Kind, j.State, prog.Text)
	if prog.Estimated {
		txt += " (Bambu Studio ne publie pas de progression : pourcentage estimé par le temps)"
	}
	if j.State == contracts.JobFailed && j.Error != nil {
		txt += " Erreur : " + j.Error.Message
	}
	return JobStatusOut{Job: j, Detail: prog}, txt + "."
}

func (h *handlers) cancelJob(ctx context.Context, _ *mcp.CallToolRequest, in JobIDInput) (*mcp.CallToolResult, CancelJobOutput, error) {
	out := CancelJobOutput{JobID: in.JobID}
	if strings.HasPrefix(in.JobID, "pj_") && h.c.Printing != nil {
		// Identifiant d'impression : annulation adaptée à la phase (voir printing.CancelPrint)
		o, err := h.c.Printing.CancelPrint(ctx, printing.CancelInput{PrintJobID: in.JobID})
		if err != nil {
			return nil, out, err
		}
		out.Outcome, out.State, out.Note = o.Outcome, contracts.JobState(o.State), o.Note
		if o.Question != "" {
			out.Note += " " + o.Question + " Lien à donner à l'utilisateur (lui seul peut valider l'arrêt) : " + o.StopURL + " (valable jusqu'à " + o.StopExpiresAtLocal + ")"
		}
		return textResult(out.Note), out, nil
	}
	oc, err := h.c.Queue.RequestCancel(ctx, in.JobID)
	if err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			return nil, out, fmt.Errorf("travail %q introuvable", in.JobID)
		}
		return nil, out, err
	}
	j, err := h.c.Queue.Get(ctx, in.JobID)
	if err != nil {
		return nil, out, err
	}
	out.Outcome, out.State = string(oc), j.State
	switch oc {
	case jobs.CancelledNow:
		out.Note = "Le travail attendait en file : annulé immédiatement."
	case jobs.CancelRequested:
		out.Note = "Annulation demandée : le travail s'arrête à son prochain point de contrôle. Suivre avec get_job_status ; l'état final est « cancelled » (ou « succeeded » s'il était déjà fini)."
	default:
		out.Note = "Le travail était déjà terminé : rien n'a été modifié."
	}
	h.c.Store.Audit(ctx, actor, "job.cancel_tool", "job", in.JobID, map[string]any{"outcome": out.Outcome}) //nolint:errcheck // l'audit détaillé est déjà écrit par la file
	return textResult(out.Note), out, nil
}

func (h *handlers) showAtelier(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, AtelierSummary, error) {
	active := 0
	for _, st := range []contracts.JobState{contracts.JobQueued, contracts.JobRunning} {
		js, err := h.c.Queue.List(ctx, st, 500)
		if err != nil {
			return nil, AtelierSummary{}, err
		}
		active += len(js)
	}
	sum := AtelierSummary{Title: "Atelier 3D (démonstration)", ServiceReady: true, ActiveJobs: active, Demo: true}
	return textResult(fmt.Sprintf("Atelier (démonstration) : service prêt, %d travail(aux) actif(s). Vue disponible dans les hôtes MCP Apps.", active)), sum, nil
}

// withView recopie le lien vers la vue MCP App dans le _meta du RÉSULTAT (en plus de la définition de l'outil dans
// tools/list, seule exigée par la spec) : certains hôtes lisent l'un, d'autres l'autre.
func withView(r *mcp.CallToolResult, tool string) *mcp.CallToolResult {
	if m, ok := toolViewMeta[tool]; ok {
		r.Meta = mcp.Meta{}
		for k, v := range m {
			r.Meta[k] = v
		}
	}
	return r
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
