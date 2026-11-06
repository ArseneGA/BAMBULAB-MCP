package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/goals"
)

// ExperimentIDInput identifie une expérience.
type ExperimentIDInput struct {
	ExperimentID string `json:"experiment_id" jsonschema:"identifiant d'expérience, ex. exp_ab12..."`
}

// MemoryInput décrit les conditions pour lesquelles chercher des essais comparables.
type MemoryInput struct {
	ExperimentID   string    `json:"experiment_id,omitempty" jsonschema:"si fourni, les conditions sont celles de cette expérience"`
	MaterialFamily string    `json:"material_family,omitempty" jsonschema:"PLA, PETG... (requis sans experiment_id)"`
	Machine        string    `json:"machine,omitempty" jsonschema:"nom exact du preset machine (requis sans experiment_id)"`
	Filament       string    `json:"filament,omitempty"`
	ModelID        string    `json:"model_id,omitempty"`
	SizeMM         []float64 `json:"size_mm,omitempty" jsonschema:"dimensions du boîtier englobant (3 valeurs) pour juger la similarité de géométrie"`
}

// MemoryOutput liste les essais réutilisables.
type MemoryOutput struct {
	Query MemoryEcho        `json:"query"`
	Hits  []goals.MemoryHit `json:"hits"`
	Note  string            `json:"note"`
}

// MemoryEcho rappelle les conditions utilisées.
type MemoryEcho struct {
	MaterialFamily string     `json:"material_family"`
	Machine        string     `json:"machine"`
	Filament       string     `json:"filament,omitempty"`
	ModelID        string     `json:"model_id,omitempty"`
	DimsSortedMM   [3]float64 `json:"dims_sorted_mm"`
}

// AddGoalTools enregistre les outils du moteur d'objectifs et d'expériences :
// list_goal_criteria, set_manufacturing_goals, modify_slicing_profile,
// create_experiment, compare_variants, apply_variant, record_print_result,
// get_print_memory. Fonction d'enregistrement séparée : l'appelant (serveur
// principal) décide de l'activer quand le service est disponible.
func AddGoalTools(s *mcp.Server, svc *goals.Service) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	f := false
	write := &mcp.ToolAnnotations{DestructiveHint: &f}

	addTool(s, &mcp.Tool{
		Name: "list_goal_criteria", Title: "Catalogue des critères d'objectif",
		Description: "Critères connus (aspect de surface, résistance avec direction d'effort, précision, souplesse, tenue thermique/extérieure/chimique, planéité, vitesse, économie de matière, transparence...), " +
			"leurs paramètres, ce qui les rend évaluables (slicer, calcul, géométrie) ou non (essai physique), leurs leviers de réglage et leurs compromis. À lire avant set_manufacturing_goals.",
		Annotations: ro,
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, goals.CriteriaCatalog, error) {
		c := svc.ListCriteria()
		return textResult(fmt.Sprintf("%d critères connus (règles %s).", len(c.Criteria), c.RulesVersion)), c, nil
	})

	addTool(s, &mcp.Tool{
		Name: "set_manufacturing_goals", Title: "Enregistrer les objectifs de fabrication",
		Description: "Enregistre un objectif structuré. Le modèle (Claude) extrait lui-même les critères du texte libre (voir list_goal_criteria) ; " +
			"le service valide, garde le texte original, renvoie les ambiguïtés qui changent le choix (ex. direction d'effort inconnue, priorités égales sur des critères opposés), " +
			"les conflits avec compromis proposés et les critères non évaluables (à vérifier par essai). Un critère inconnu est accepté comme custom, non évaluable.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in goals.GoalInput) (*mcp.CallToolResult, goals.Goal, error) {
		g, err := svc.SetGoals(ctx, in)
		if err != nil {
			return nil, goals.Goal{}, err
		}
		return textResult(fmt.Sprintf("Objectif %s : %d critère(s), %d ambiguïté(s), %d conflit(s), %d non évaluable(s).",
			g.ID, len(g.Criteria), len(g.Ambiguities), len(g.Conflicts), len(g.NonEvaluable))), *g, nil
	})

	addTool(s, &mcp.Tool{
		Name: "modify_slicing_profile", Title: "Modifier un profil de découpe",
		Description: "Applique des modifications typées (clé existante du profil Bambu Studio, valeur du même type, cible machine/process/filament) sur une base, " +
			"valide contre le profil résolu et renvoie le profil complet (process et filaments ; machine : clés modifiées), la provenance de chaque clé et la différence. " +
			"base_revision doit être la tête de sa lignée, sinon refus (révision périmée). Les clés d'identité de la machine sont protégées.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in goals.ModifyInput) (*mcp.CallToolResult, goals.ModifyOutput, error) {
		o, err := svc.ModifyProfile(ctx, in)
		if err != nil {
			return nil, goals.ModifyOutput{}, err
		}
		return textResult(fmt.Sprintf("Révision de profil %s : %d différence(s).", o.Revision, len(o.Diff))), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "create_experiment", Title: "Créer une expérience de découpe",
		Description: "Fige la révision du modèle et la base (machine, process, filament, empreintes des profils), génère une référence et N variantes bornées et justifiées " +
			"(défaut 3, plafond configurable ; peu de facteurs par variante) et met leur découpe en file (un travail lourd à la fois). Aucun paramètre d'un autre matériau n'est appliqué. " +
			"Suivre avec get_job_status puis compare_variants.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in goals.CreateExperimentInput) (*mcp.CallToolResult, goals.Experiment, error) {
		x, err := svc.CreateExperiment(ctx, in)
		if err != nil {
			return nil, goals.Experiment{}, err
		}
		return textResult(fmt.Sprintf("Expérience %s sur la révision %s : %d candidat(s) (référence comprise), découpes en file.", x.ID, x.ModelVersionID, len(x.Candidates))), *x, nil
	})

	addTool(s, &mcp.Tool{
		Name: "compare_variants", Title: "Comparer les variantes",
		Description: "Tableau des mesures réellement disponibles par candidat (temps, matière, couches, supports si lisibles) étiquetées « estimation slicer », " +
			"calculs d'ingénierie étiquetés (ex. facteur de sécurité selon l'orientation), seuils vérifiés, classements par critère mesurable, et critères à vérifier par essai. " +
			"Aucun gagnant ni score global.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ExperimentIDInput) (*mcp.CallToolResult, goals.CompareOutput, error) {
		c, err := svc.CompareVariants(ctx, in.ExperimentID)
		if err != nil {
			return nil, goals.CompareOutput{}, err
		}
		txt := fmt.Sprintf("Expérience %s : %d candidat(s), %d classement(s) par critère mesurable, %d critère(s) à vérifier par essai. Aucun gagnant désigné.",
			c.ExperimentID, len(c.Candidates), len(c.Rankings), len(c.NotEvaluable))
		if !c.Complete {
			txt += fmt.Sprintf(" Découpes en attente : %d.", len(c.Pending))
		}
		return textResult(txt), *c, nil
	})

	addTool(s, &mcp.Tool{
		Name: "apply_variant", Title: "Appliquer une variante",
		Description: "Fixe la variante choisie (découpée avec succès) comme profil du modèle. expected_revision doit être la révision du modèle figée dans l'expérience ET la plus récente du modèle ; " +
			"sinon refus (révision périmée) et la révision courante est indiquée. N'imprime rien.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in goals.ApplyInput) (*mcp.CallToolResult, goals.ApplyOutput, error) {
		o, err := svc.ApplyVariant(ctx, in)
		if err != nil {
			return nil, goals.ApplyOutput{}, err
		}
		return textResult(fmt.Sprintf("Variante %s appliquée au modèle %s (révision de profil %s).", o.CandidateID, o.ModelID, o.ProfileRevision)), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "record_print_result", Title: "Enregistrer un résultat d'impression",
		Description: "Lie un essai réel à l'expérience, au candidat et aux réglages réels (écarts avec le plan), avec photos (références de blobs sha256), mesures et observations. " +
			"Alimente une mémoire réutilisée seulement pour la même famille de matériau, la même machine et une géométrie similaire.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in goals.ResultInput) (*mcp.CallToolResult, goals.PrintResult, error) {
		r, err := svc.RecordResult(ctx, in)
		if err != nil {
			return nil, goals.PrintResult{}, err
		}
		return textResult(fmt.Sprintf("Résultat %s enregistré (%s), %d écart(s) de réglage.", r.ID, r.Outcome, len(r.Deviations))), *r, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_print_memory", Title: "Essais antérieurs comparables",
		Description: "Résultats d'impression antérieurs réutilisables dans les conditions données (même famille de matériau et même machine, géométrie similaire), avec la qualité de la comparaison.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in MemoryInput) (*mcp.CallToolResult, MemoryOutput, error) {
		q := goals.MemoryQuery{MaterialFamily: in.MaterialFamily, Machine: in.Machine, Filament: in.Filament, ModelID: in.ModelID}
		if in.ExperimentID != "" {
			x, err := svc.GetExperiment(ctx, in.ExperimentID)
			if err != nil {
				return nil, MemoryOutput{}, err
			}
			q = goals.MemoryQuery{MaterialFamily: x.Details.Base.Family, Machine: x.Details.Base.Machine, Filament: x.Details.Base.Filaments[0],
				ModelID: x.ModelID, DimsSortedMM: x.Details.Geometry.DimsSortedMM}
		} else if len(in.SizeMM) == 3 {
			d := [3]float64{in.SizeMM[0], in.SizeMM[1], in.SizeMM[2]}
			for i := 0; i < 3; i++ {
				for j := i + 1; j < 3; j++ {
					if d[j] > d[i] {
						d[i], d[j] = d[j], d[i]
					}
				}
			}
			q.DimsSortedMM = d
		}
		hits, err := svc.Memory(ctx, q, 20)
		if err != nil {
			return nil, MemoryOutput{}, err
		}
		if hits == nil {
			hits = []goals.MemoryHit{}
		}
		out := MemoryOutput{Query: MemoryEcho{MaterialFamily: q.MaterialFamily, Machine: q.Machine, Filament: q.Filament, ModelID: q.ModelID, DimsSortedMM: q.DimsSortedMM},
			Hits: hits, Note: "Seuls les essais de même famille de matériau et même machine, de géométrie similaire (±25 %) ou du même modèle, sont renvoyés ; la colonne comparability dit à quel point ils sont comparables."}
		return textResult(fmt.Sprintf("%d essai(s) comparable(s).", len(hits))), out, nil
	})
}
