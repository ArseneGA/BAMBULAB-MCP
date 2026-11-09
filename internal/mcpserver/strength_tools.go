package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/strength"
)

// Test de résistance : plan_strength_test (zones numérotées + questions + hypothèses proposées), assess_strength
// (calcul par éléments finis, carte de contraintes), get_strength_report (historique). Voir internal/strength.

// StrengthViewURI : vue de la carte de contraintes (liée à assess_strength et get_strength_report).
const StrengthViewURI = "ui://bambu/strength.html"

func init() {
	toolViewMeta["assess_strength"] = AppToolMeta(StrengthViewURI)
	toolViewMeta["get_strength_report"] = AppToolMeta(StrengthViewURI)
}

// AddStrengthTools enregistre les outils du test de résistance.
func AddStrengthTools(s *mcp.Server, c *core.Core) {
	svc := c.Strength
	if svc == nil {
		return
	}
	html, _ := viewHTML()
	addViewResource(s, StrengthViewURI, "strength_view", "Test de résistance",
		"Carte de contraintes (MPa) d'une pièce, verdict, marge de sécurité, charges et appuis, recommandations (assess_strength, get_strength_report).", html)
	f := false
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	add := &mcp.ToolAnnotations{DestructiveHint: &f}

	addTool(s, &mcp.Tool{
		Name: "plan_strength_test", Title: "Préparer un test de résistance",
		Description: "À appeler quand une pièce va porter une charge (support, crochet, bras, étagère...) : analyse la géométrie et renvoie une IMAGE avec des ZONES NUMÉROTÉES (appuis et zones de charge probables), " +
			"la liste des QUESTIONS à poser à l'humain une par une en français simple (à quoi sert la pièce, quoi pèse dessus et combien, où et dans quelle direction, comment elle est tenue, charge permanente ou chocs, matière et réglages, orientation d'impression), " +
			"et une proposition d'HYPOTHÈSES par défaut justifiée (masses courantes : guitare, étagère, écran, vélo, outil...). Ne jamais inventer une charge en silence : poser les questions, ou annoncer l'hypothèse et la faire confirmer. " +
			"Ensuite appeler assess_strength. Calcul simplifié, ne remplace pas un essai réel.",
		Annotations: add,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in strength.PlanInput) (*mcp.CallToolResult, strength.PlanOutput, error) {
		o, err := svc.Plan(ctx, in)
		if err != nil {
			return nil, strength.PlanOutput{}, err
		}
		res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: planText(o)}}}
		if len(o.PNG) > 0 {
			res.Content = append(res.Content, &mcp.ImageContent{Data: o.PNG, MIMEType: "image/png"})
		}
		return res, *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "assess_strength", Title: "Test de résistance",
		Description: "VRAI calcul par éléments finis (élasticité linéaire, Go pur) d'une pièce sous charge : voxélisation du maillage (≤ 150 000 cubes), contraintes de von Mises, déplacement, coefficient de sécurité, avec les propriétés réduites pour la matière (PLA, PETG, ABS, ASA, PA, TPU), les parois, le remplissage, la liaison entre couches (direction d'impression) et le fluage. " +
			"Entrées : loads (zone ou point, force_N ou mass_kg, direction), supports (zone, fixed | resting), material, infill, walls, layer_orientation, safety_factor (défaut 2), load_kind (static | impact), duration. " +
			"Chaque charge et chaque appui porte `source` : user (donné par l'humain) ou assumed (hypothèse que tu as ANNONCÉE à l'humain) ; le verdict dit alors qu'il repose sur des hypothèses. Refuse sans charge ni appui : appeler d'abord plan_strength_test. " +
			"Renvoie une carte de contraintes PNG (MPa, charges et appuis annotés), le verdict (« tient avec une marge de ×3,2 » / « risque de casse »), le point critique, le déplacement max et des recommandations. " +
			"Peut durer jusqu'à quelques minutes : un job_id est alors rendu (wait_for_job), le résultat se relit avec get_strength_report. Calcul simplifié : ne remplace pas un essai réel.",
		Annotations: add,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in strength.AssessInput) (*mcp.CallToolResult, strength.AssessOutput, error) {
		o, err := svc.Assess(ctx, in)
		if err != nil {
			return nil, strength.AssessOutput{}, err
		}
		if o.State != "done" {
			return textResult(o.Note + " Travail " + o.JobID + "."), *o, nil
		}
		res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: assessText(o.Result, o.TestID)}}}
		if len(o.PNG) > 0 {
			res.Content = append(res.Content, &mcp.ImageContent{Data: o.PNG, MIMEType: "image/png"})
		}
		return res, *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_strength_report", Title: "Historique des tests de résistance",
		Description: "Relit les tests de résistance d'un modèle (rattachés à la version testée) : liste du plus récent au plus ancien, et le détail du dernier (ou de test_id / job_id) avec sa carte de contraintes, ses hypothèses et ce qui est mesuré, calculé ou estimé.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in strength.ReportInput) (*mcp.CallToolResult, strength.ReportOutput, error) {
		o, err := svc.Report(ctx, in)
		if err != nil {
			return nil, strength.ReportOutput{}, err
		}
		if o.Detail == nil || o.Detail.Result == nil {
			return textResult(fmt.Sprintf("Aucun test de résistance pour le modèle %s. Lancer plan_strength_test puis assess_strength.", o.ModelID)), *o, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d test(s) pour le modèle %s :\n", o.Total, o.ModelID)
		for _, t := range o.Tests {
			hyp := ""
			if t.Assumed {
				hyp = ", sur hypothèses"
			}
			fmt.Fprintf(&b, "- %s (%s) version %s : %s, marge ×%.1f, %.1f MPa, %s%s\n", t.ID, t.CreatedAt.Local().Format("02/01/2006 15:04"), t.VersionID, t.Verdict, t.Safety, t.MaxStress, t.Material, hyp)
		}
		b.WriteString("\nDétail du test " + o.Detail.ID + " :\n" + assessText(o.Detail.Result, o.Detail.ID))
		res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}}
		if len(o.PNG) > 0 {
			res.Content = append(res.Content, &mcp.ImageContent{Data: o.PNG, MIMEType: "image/png"})
		}
		return res, *o, nil
	})
}

var decimalDot = regexp.MustCompile(`(\d)\.(\d)`)

// fr met la virgule décimale française dans un texte destiné à l'humain (les nombres structurés restent en JSON).
func fr(s string) string { return decimalDot.ReplaceAllString(s, "$1,$2") }

func planText(o *strength.PlanOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Test de résistance de « %s » (version %s) : %d zone(s) candidates numérotées sur l'image jointe.\n", o.Name, o.VersionID, len(o.Zones))
	for _, z := range o.Zones {
		b.WriteString("- " + z.Describe() + " [" + strings.Join(z.Candidates, ", ") + "]\n")
	}
	b.WriteString("\nMontre l'image à l'humain, puis pose ces questions UNE par une, en français simple (jamais toutes d'un coup) :\n")
	for _, q := range o.Questions {
		fmt.Fprintf(&b, "%d. %s", q.Order, q.Ask)
		if len(q.Examples) > 0 {
			b.WriteString(" Exemples : " + strings.Join(q.Examples, " ; ") + ".")
		}
		if q.Default != "" {
			b.WriteString(" [s'il ne sait pas, hypothèse à annoncer : " + q.Default + "]")
		}
		b.WriteString("\n")
	}
	if o.Known.Found {
		fmt.Fprintf(&b, "\nRéglages relus dans une découpe existante (%s) : matière %s, parois %d, remplissage %.0f %% (0 = inconnu).\n", o.Known.Source, o.Known.Material, o.Known.Walls, o.Known.InfillPct)
	}
	b.WriteString("\nHYPOTHÈSES PROPOSÉES (à annoncer à l'humain et à lui laisser corriger ; source « assumed ») : " + o.Proposal.Statement + "\n")
	for _, w := range o.Proposal.Why {
		b.WriteString("  * " + w + "\n")
	}
	if len(o.Proposal.Loads) > 0 || len(o.Proposal.Supports) > 0 {
		pj, _ := json.Marshal(map[string]any{"loads": o.Proposal.Loads, "supports": o.Proposal.Supports})
		b.WriteString("Appel assess_strength prêt à confirmer : " + string(pj) + "\n")
	}
	b.WriteString("\n" + strength.Disclaimer)
	return fr(b.String())
}

func assessText(r *strength.Result, testID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "TEST DE RÉSISTANCE %s — modèle %s, version %s, matière %s (remplissage %s, %s parois, couches perpendiculaires à l'axe %s).\n", testID, r.ModelID, r.VersionID, r.Material.Label,
		inputOf(r, "remplissage"), inputOf(r, "parois"), r.LayerAxisName)
	b.WriteString("VERDICT : " + r.VerdictTxt + "\n")
	fmt.Fprintf(&b, "Contrainte maximale (von Mises) %.1f MPa au point (%.0f ; %.0f ; %.0f) mm. Point critique (contrainte / résistance la plus élevée, %.0f %% de la limite) %s, von Mises %.1f MPa ; limite admissible estimée %.1f MPa dans le plan, %.1f MPa entre couches ; critère limitant : %s ; coefficient de sécurité obtenu ×%.2f pour ×%.1f visé.\n",
		r.MaxVonMisesMPa, r.MaxVMPoint[0], r.MaxVMPoint[1], r.MaxVMPoint[2], 100*r.MaxUtilization, r.CriticalNote, r.CriticalStressMPa, r.AllowableXYMPa, r.AllowableZMPa,
		strings.ReplaceAll(r.LimitingCriteria, "_", " "), r.SafetyAchieved, r.SafetyRequired)
	fmt.Fprintf(&b, "Déplacement maximal %.2f mm (au point %.0f ; %.0f ; %.0f).", r.MaxDisplacementMM, r.MaxDispPoint[0], r.MaxDispPoint[1], r.MaxDispPoint[2])
	if r.LocalThickMM > 0 {
		fmt.Fprintf(&b, " Épaisseur locale au point critique ≈ %.1f mm.", r.LocalThickMM)
	}
	b.WriteString("\n")
	if len(r.Orientations) > 0 {
		fmt.Fprintf(&b, "Marge selon l'axe perpendiculaire aux couches : x ×%.1f, y ×%.1f, z ×%.1f (même champ de contraintes).\n", r.Orientations["x"], r.Orientations["y"], r.Orientations["z"])
	}
	if len(r.Recommendations) > 0 {
		b.WriteString("RECOMMANDATIONS :\n")
		for _, x := range r.Recommendations {
			b.WriteString("- " + x + "\n")
		}
	}
	if r.BasedOnAssumed {
		b.WriteString("ENTRÉES SUPPOSÉES (à confirmer ou corriger avec l'humain) :\n")
		for _, m := range r.Inputs {
			if m.Source != "user" {
				fmt.Fprintf(&b, "- %s = %s [%s]\n", m.Name, m.Value, map[string]string{"assumed": "hypothèse annoncée", "default": "valeur par défaut"}[m.Source])
			}
		}
	}
	for _, w := range r.Warnings {
		b.WriteString("Attention : " + w + "\n")
	}
	b.WriteString("MESURÉ : " + strings.Join(r.Measured, " ; ") + "\n")
	b.WriteString("CALCULÉ : " + strings.Join(r.Calculated, " ; ") + "\n")
	b.WriteString("ESTIMÉ : " + strings.Join(r.Estimated, " ; ") + "\n")
	fmt.Fprintf(&b, "Maillage : %d cubes de %.2f mm, %d itérations, %.1f s. ", r.Mesh.Elements, r.Mesh.VoxelMM, r.Solver.Iterations, r.TotalSeconds)
	b.WriteString(r.Disclaimer)
	return fr(b.String())
}

func inputOf(r *strength.Result, name string) string {
	for _, m := range r.Inputs {
		if m.Name == name {
			return m.Value
		}
	}
	return "?"
}

// ---- proposition au bon moment ----

// suggestTools : outils dont le résultat décrit une pièce créée, modifiée, importée ou analysée.
var suggestTools = map[string]bool{
	"create_from_template": true, "create_parametric_model": true, "modify_geometry": true, "import_model": true, "analyze_geometry": true,
}

// strengthHintMiddleware ajoute au texte rendu au modèle la suggestion d'un test de résistance quand la pièce semble
// porteuse (strength.LooksLoadBearing : gabarit, nom, étiquettes, demande) ; une seule fois par modèle et par serveur,
// jamais si un test existe déjà, jamais sur erreur ni analyse non valide (maillage non étanche).
func strengthHintMiddleware(c *core.Core) mcp.Middleware {
	var seen sync.Map
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			if err != nil || method != "tools/call" || c.Strength == nil {
				return res, err
			}
			cr, ok := res.(*mcp.CallToolResult)
			p, pok := req.GetParams().(*mcp.CallToolParamsRaw)
			if !ok || cr == nil || cr.IsError || !pok || p == nil || !suggestTools[p.Name] {
				return res, err
			}
			type meshSum struct {
				OpenEdges   int `json:"open_edges"`
				NonManifold int `json:"non_manifold_edges"`
			}
			var out struct {
				ModelID  string `json:"model_id"`
				Template string `json:"template"`
				State    string `json:"state"`
				File     *struct {
					ModelID string `json:"model_id"`
				} `json:"file"`
				Summary  *meshSum `json:"summary"`
				Analysis *meshSum `json:"analysis"`
			}
			if b, e := json.Marshal(cr.StructuredContent); e == nil {
				_ = json.Unmarshal(b, &out)
			}
			if out.ModelID == "" && out.File != nil {
				out.ModelID = out.File.ModelID
			}
			bad := false // « validée » : maillage fermé (aucune arête ouverte ni non manifold)
			for _, ms := range []*meshSum{out.Summary, out.Analysis} {
				if ms != nil && (ms.OpenEdges > 0 || ms.NonManifold > 0) {
					bad = true
				}
			}
			if out.ModelID == "" || bad || (out.State != "" && out.State != "ok" && out.State != "succeeded") {
				return res, err
			}
			texts := []string{out.Template, string(p.Arguments)}
			if m, e := c.Store.GetModel(ctx, out.ModelID); e == nil {
				texts = append(texts, m.Name, strings.Join(m.Tags, " "), m.Description)
			}
			if !strength.LooksLoadBearing(texts...) || c.Strength.HasTest(ctx, out.ModelID) {
				return res, err
			}
			if _, dup := seen.LoadOrStore(out.ModelID, true); dup {
				return res, err
			}
			for _, ct := range cr.Content {
				if t, ok := ct.(*mcp.TextContent); ok {
					t.Text += "\n\n" + strength.SuggestionText + " (modèle " + out.ModelID + ")"
					break
				}
			}
			return res, err
		}
	}
}
