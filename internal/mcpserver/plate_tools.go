package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// Plusieurs pièces sur un plateau (chantier B) : plan_plate -> get_plate_plan -> execute_plate_slice. L'aperçu du
// plateau passe par capture_preview (mode « plate »). Aucun de ces outils n'envoie quoi que ce soit à l'imprimante :
// chaque plaque devient une impression ordinaire (validation, dépôt, accord humain un par un).

// PlatePlanOut : plan et rapport.
type PlatePlanOut struct {
	Plan         printing.PlatePlan `json:"plan"`
	SettingsKeys []string           `json:"object_setting_keys" jsonschema:"clés de réglages par pièce acceptées"`
	Note         string             `json:"note"`
}

// PlatePlansOut : liste de plans.
type PlatePlansOut struct {
	Plans []printing.PlatePlan `json:"plans"`
}

// PlanIDInput identifie un plan.
type PlanIDInput struct {
	PlanID string `json:"plan_id,omitempty" jsonschema:"plan à relire ; omis : liste des plans récents"`
}

// AddPlateTools enregistre les outils de plateau multi-pièces. Fonction séparée : une seule ligne dans New().
func AddPlateTools(s *mcp.Server, p *printing.Service) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	f := false
	write := &mcp.ToolAnnotations{DestructiveHint: &f}

	addTool(s, &mcp.Tool{
		Name: "plan_plate", Title: "Composer un plateau de plusieurs pièces",
		Description: "Pose plusieurs pièces (model_id, quantité, réglages PAR PIÈCE : remplissage, parois, hauteur de couche, bordure, supports ; plages de hauteur) sur les plateaux d'une imprimante. " +
			"Bambu Studio dispose les pièces (--arrange 1) et les répartit sur autant de plaques que nécessaire ; sans Studio, un rangement estimé par rectangles englobants. " +
			"Rend AVANT toute découpe un rapport « tient / ne tient pas » : empreinte au sol et hauteur contre le plateau réel (profil machine, zones interdites), pièces par plaque, occupation. " +
			"Ne découpe rien, n'envoie rien. Voir la disposition avec capture_preview(mode=plate, plan_id), puis découper avec execute_plate_slice.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.PlanPlateInput) (*mcp.CallToolResult, PlatePlanOut, error) {
		pl, err := p.PlanPlate(ctx, in)
		if err != nil {
			return nil, PlatePlanOut{}, err
		}
		note := "Plan " + pl.ID + " : voir l'aperçu avec capture_preview(mode=\"plate\", plan_id=\"" + pl.ID + "\", plate=1)."
		if pl.Fits {
			note += " Pour découper : execute_plate_slice(plan_id)."
		} else {
			note += " Le plan ne tient pas : retirer ou réduire les pièces signalées, ou changer d'imprimante, puis relancer plan_plate."
		}
		return textResult(strings.Join(pl.Report, "\n")), PlatePlanOut{Plan: *pl, SettingsKeys: printing.ObjectSettingKeys, Note: note}, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_plate_plan", Title: "Relire un plan de plateau",
		Description: "Un plan de plateau (pièces, plaques, positions, rapport, impressions créées) ou, sans plan_id, la liste des plans récents.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in PlanIDInput) (*mcp.CallToolResult, PlatePlansOut, error) {
		if in.PlanID == "" {
			ps, err := p.ListPlatePlans(ctx, 50)
			if err != nil {
				return nil, PlatePlansOut{}, err
			}
			return textResult(fmt.Sprintf("%d plan(s) de plateau.", len(ps))), PlatePlansOut{Plans: ps}, nil
		}
		pl, err := p.GetPlatePlan(ctx, in.PlanID)
		if err != nil {
			return nil, PlatePlansOut{}, err
		}
		return textResult(strings.Join(pl.Report, "\n")), PlatePlansOut{Plans: []printing.PlatePlan{*pl}}, nil
	})

	addTool(s, &mcp.Tool{
		Name: "execute_plate_slice", Title: "Découper un plan de plateau",
		Description: "Découpe un plan qui tient : UNE impression (PrintJob) par plaque, chacune tranchée par Bambu Studio headless à partir d'un 3MF multi-pièces qui porte les réglages par pièce. " +
			"L'archive de chaque impression ne contient que sa plaque ; son numéro et son empreinte sont ce que la confirmation humaine lie, comme pour toute impression. " +
			"La plaque est refusée si l'archive ne porte pas exactement les pièces du plan. Le résumé donne la matière et les couches de chaque pièce lues dans le G-code. " +
			"RETOUR IMMÉDIAT : chaque plaque est découpée en arrière-plan (une à la fois) ; ne pas considérer l'appel comme échoué, suivre avec get_job_status(slice_job_id de chaque impression) ou get_print_status. " +
			"Ensuite : show_gcode_preview(print_job_id) pour montrer la découpe, validate_print_artifact, upload_print_artifact et la confirmation humaine, plaque par plaque.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.PlateSliceInput) (*mcp.CallToolResult, printing.PlateSliceOutput, error) {
		o, err := p.ExecutePlateSlice(ctx, in)
		if err != nil {
			return nil, printing.PlateSliceOutput{}, err
		}
		var ids []string
		for _, j := range o.Jobs {
			ids = append(ids, fmt.Sprintf("plaque %d = %s", j.Plate, j.ID))
		}
		return textResult(fmt.Sprintf("Plan %s : %d impression(s) en file (%s). Une fois découpées : %s", o.Plan.ID, len(o.Jobs), strings.Join(ids, ", "), GcodeSuggestion)), *o, nil
	})
}
