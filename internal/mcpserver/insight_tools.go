package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
)

// LogIDInput identifie une fiche du journal (ou l'impression correspondante).
type LogIDInput struct {
	ID string `json:"id" jsonschema:"identifiant de la fiche (log_...) ou de l'impression terminée (print job)"`
}

// UsageDashboardInput borne le tableau de bord.
type UsageDashboardInput struct {
	From      string `json:"from,omitempty" jsonschema:"début de période, AAAA-MM-JJ ou RFC 3339 (défaut : 30 jours avant la fin)"`
	To        string `json:"to,omitempty" jsonschema:"fin de période incluse, AAAA-MM-JJ ou RFC 3339 (défaut : maintenant)"`
	Period    string `json:"period,omitempty" jsonschema:"découpage : day | week | month (défaut : selon la durée)"`
	PrinterID string `json:"printer_id,omitempty" jsonschema:"restreindre à une imprimante"`
	NoChart   bool   `json:"no_chart,omitempty" jsonschema:"ne pas joindre l'image du graphe (heures machine par période)"`
}

// ProjectCostsOutput est le coût par projet (collection).
type ProjectCostsOutput struct {
	Label    string             `json:"label"`
	Currency string             `json:"currency"`
	Projects []insights.DashRow `json:"projects"`
}

// AddInsightTools enregistre les outils du chantier « coût, journal, tableau de bord, post-traitement » :
// estimate_cost, get_cost_settings, set_cost_settings, list_print_log, get_print_log, update_print_log,
// suggest_print_adjustments, get_usage_dashboard, get_project_costs, post_processing_advice.
// Aucun de ces outils ne lance ni n'arrête d'impression.
func AddInsightTools(s *mcp.Server, svc *insights.Service) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	f := false
	write := &mcp.ToolAnnotations{DestructiveHint: &f}
	idem := &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true}

	addTool(s, &mcp.Tool{
		Name: "estimate_cost", Title: "Estimer le coût d'une impression",
		Description: "Coût d'une impression = filament (masse par emplacement × prix/kg de la bobine ou de la famille de matière) + électricité (puissance moyenne du modèle d'imprimante par phase : chauffe puis impression, × durée × prix du kWh) " +
			"+ usure (amortissement machine et buse par heure, 0 par défaut) ; temps machine en heures. Sur un artefact découpé (print_job_id : masses et durée lues dans l'archive) ou sur un plan saisi " +
			"(printer_model, duration_minutes, filaments). Pour une impression terminée, renvoie aussi le coût RÉEL estimé (durée observée). TOUJOURS une estimation : hypothèses listées, " +
			"puissances et prix par défaut INDICATIFS tant que set_cost_settings n'a pas été utilisé.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in insights.EstimateInput) (*mcp.CallToolResult, insights.EstimateOutput, error) {
		o, err := svc.EstimateCost(ctx, in)
		if err != nil {
			return nil, insights.EstimateOutput{}, err
		}
		txt := o.Estimate.Text()
		if o.Actual != nil {
			txt += "\n\n" + o.Actual.Text()
		}
		return textResult(txt), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_cost_settings", Title: "Réglages de prix",
		Description: "Prix du kWh, prix du kg par famille de matière et par bobine, usure par heure, puissances mesurées. Indique quels prix sont encore des valeurs par défaut indicatives. Aucun secret.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, insights.Settings, error) {
		st, err := svc.GetSettings(ctx)
		if err != nil {
			return nil, st, err
		}
		return textResult(fmt.Sprintf("Devise %s, kWh à %.4g, %d prix de famille (%d encore par défaut indicatif).", st.Currency, st.ElectricityPerKWh, len(st.FilamentPerKg), len(st.DefaultedPrices))), st, nil
	})

	addTool(s, &mcp.Tool{
		Name: "set_cost_settings", Title: "Modifier les réglages de prix",
		Description: "Modifie une partie des réglages de coût (fusion). Dans les dictionnaires, la valeur 0 supprime l'entrée (retour à la valeur par défaut). " +
			"apply_to_history=true recalcule le coût réel de toutes les fiches du journal avec ces prix. Aucun secret ici : des prix, des watts, une devise.",
		Annotations: idem,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in insights.SettingsPatch) (*mcp.CallToolResult, insights.Settings, error) {
		st, err := svc.SetSettings(ctx, in)
		if err != nil {
			return nil, st, err
		}
		return textResult(fmt.Sprintf("Réglages enregistrés : devise %s, kWh à %.4g, usure machine %.2f/h, buse %.2f/h.", st.Currency, st.ElectricityPerKWh, st.MachineWearPerHr, st.NozzleWearPerHr)), st, nil
	})

	addTool(s, &mcp.Tool{
		Name: "list_print_log", Title: "Journal des impressions",
		Description: "Fiches du journal, les plus récentes d'abord : une par impression terminée, échouée ou annulée (créée à la demande), avec réglages effectifs, durée et masse estimées vs réelles, coût, " +
			"photo, note 1 à 5, commentaire et défauts. Filtres : imprimante, famille de matière, issue, modèle.",
		Annotations: idem,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in insights.ListQuery) (*mcp.CallToolResult, insights.ListOutput, error) {
		o, err := svc.ListLogs(ctx, in)
		if err != nil {
			return nil, insights.ListOutput{}, err
		}
		return textResult(fmt.Sprintf("%d fiche(s) au total, %d renvoyée(s)%s.", o.Total, len(o.Entries), createdNote(o.Created))), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_print_log", Title: "Fiche d'une impression",
		Description: "Une fiche du journal par son identifiant ou par celui de l'impression : réglages effectifs, écarts de durée et de masse, coût estimé et réel, photos, note, défauts.",
		Annotations: idem,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in LogIDInput) (*mcp.CallToolResult, insights.LogEntry, error) {
		e, err := svc.GetLog(ctx, in.ID)
		if err != nil {
			return nil, insights.LogEntry{}, err
		}
		return textResult(logLine(e)), *e, nil
	})

	addTool(s, &mcp.Tool{
		Name: "update_print_log", Title: "Compléter la fiche d'une impression",
		Description: "Note (1 à 5), commentaire, défauts cochables (warping, stringing, layer_lines, delamination, adhesion, dimensions, elephant_foot, under_extrusion, over_extrusion, blobs, supports, overhang, layer_shift, surface, other), " +
			"issue (success|partial|failed|cancelled), masse pesée, durée réelle, photos (références de blobs). Avec experiment_id et candidate_id, verse aussi le retour dans la mémoire des essais (get_print_memory) ; " +
			"les ajustements possibles se demandent ensuite avec suggest_print_adjustments. Ne change aucun réglage.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in insights.UpdateInput) (*mcp.CallToolResult, insights.LogEntry, error) {
		e, err := svc.UpdateLog(ctx, in)
		if err != nil {
			return nil, insights.LogEntry{}, err
		}
		return textResult("Fiche mise à jour. " + logLine(e)), *e, nil
	})

	addTool(s, &mcp.Tool{
		Name: "suggest_print_adjustments", Title: "Ajustements proposés pour la prochaine impression",
		Description: "Propose des changements de réglages concrets (clé du profil, sens, ordre de grandeur, justification, compromis) d'après les défauts cochés sur une fiche ou demandés et ceux qui reviennent " +
			"dans les fiches comparables (même famille de matière, même machine). Règles explicites de niveau heuristique ; JAMAIS appliquées automatiquement : à passer à modify_slicing_profile ou create_experiment, un changement à la fois. " +
			"Indique aussi la meilleure fiche comparable (bien notée) comme base de départ.",
		Annotations: idem,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in insights.AdjustInput) (*mcp.CallToolResult, insights.AdjustOutput, error) {
		o, err := svc.SuggestAdjustments(ctx, in)
		if err != nil {
			return nil, insights.AdjustOutput{}, err
		}
		return textResult(fmt.Sprintf("%s : %d défaut(s) traité(s) sur %d fiche(s) comparable(s). Aucun réglage appliqué.", o.MaterialFamily, len(o.Proposals), o.ComparableLogs)), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_usage_dashboard", Title: "Tableau de bord d'usage de la machine",
		Description: "Heures machine, nombre d'impressions, taux de réussite, filament consommé par matière, coût cumulé ESTIMÉ, par période et par imprimante et par projet (collection) ; " +
			"un graphe PNG des heures machine par période est joint sauf no_chart. Les coûts sont des estimations (voir estimate_cost).",
		Annotations: idem,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in UsageDashboardInput) (*mcp.CallToolResult, insights.Dashboard, error) {
		d, err := svc.GetDashboard(ctx, insights.DashboardQuery{From: in.From, To: in.To, Period: in.Period, PrinterID: in.PrinterID})
		if err != nil {
			return nil, insights.Dashboard{}, err
		}
		res := textResult(d.Text())
		if !in.NoChart && d.Totals.Prints > 0 {
			if png, err := insights.RenderChart(d); err == nil {
				res.Content = append(res.Content, &mcp.ImageContent{Data: png, MIMEType: "image/png"})
			}
		}
		return res, *d, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_project_costs", Title: "Coût par projet",
		Description: "Coût réel ESTIMÉ des impressions terminées par projet (collection de la bibliothèque) sur une période, plus l'ESTIMATION des impressions préparées pas encore terminées.",
		Annotations: idem,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in UsageDashboardInput) (*mcp.CallToolResult, ProjectCostsOutput, error) {
		d, err := svc.GetDashboard(ctx, insights.DashboardQuery{From: in.From, To: in.To, Period: in.Period, PrinterID: in.PrinterID})
		if err != nil {
			return nil, ProjectCostsOutput{}, err
		}
		out := ProjectCostsOutput{Label: d.Label, Currency: d.Currency, Projects: d.ByProject}
		return textResult(fmt.Sprintf("%d projet(s) sur la période. %s", len(out.Projects), d.Label)), out, nil
	})

	addTool(s, &mcp.Tool{
		Name: "post_processing_advice", Title: "Conseils de post-traitement",
		Description: "Étapes ordonnées de finition d'une pièce selon la matière, la finition voulue (lisse, mat, peinte, brillante, transparente, recuit, collage, inserts) et la géométrie/l'usage : " +
			"ponçage (grains), apprêt, lissage chimique quand il est pertinent (avec ses dangers), peinture, vernis/résine, recuit, inserts thermo-sertis (diamètres de trou), collage. " +
			"Données embarquées et versionnées (niveau heuristique, sources citées) ; dit honnêtement ce qui n'est pas possible (ex. PETG et polycarbonate FDM restent translucides, pas transparents).",
		Annotations: ro,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in insights.PostProcessInput) (*mcp.CallToolResult, insights.PostProcessAdvice, error) {
		a, err := insights.PostProcessingAdvice(in)
		if err != nil {
			return nil, insights.PostProcessAdvice{}, err
		}
		return textResult(fmt.Sprintf("%s : %d étape(s) conseillée(s), %d technique(s) déconseillée(s). Niveau %s : essayer sur une pièce témoin.", a.Family, len(a.Steps), len(a.NotRecommended), a.Level)), *a, nil
	})
}

func createdNote(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" ; %d fiche(s) créée(s) à l'instant pour des impressions terminées", n)
}

func logLine(e *insights.LogEntry) string {
	s := fmt.Sprintf("Fiche %s (impression %s) : %s", e.ID, e.PrintJobID, e.Outcome)
	if e.Rating > 0 {
		s += fmt.Sprintf(", note %d/5", e.Rating)
	}
	if len(e.Defects) > 0 {
		s += fmt.Sprintf(", défauts %v", e.Defects)
	}
	if e.ActualCost != nil {
		s += fmt.Sprintf(", coût réel estimé %.2f %s", e.ActualCost.Total, e.ActualCost.Currency)
	}
	return s + "."
}
