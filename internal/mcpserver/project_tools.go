package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/projects"
)

// Suivi de projet (G9, PJ2) et verdict en un geste (PJ11) : create_project, update_project, add_project_part,
// remove_project_part, get_project, list_projects ; set_print_verdict, get_pending_verdicts (et les outils réservés à la
// carte : submit_print_verdict, submit_project_status, submit_project_part).
//
// RÈGLE ABSOLUE : aucun de ces outils ne lance, n'arrête ni ne reprend une impression, ne crée ni n'approuve de
// confirmation. Un verdict n'a AUCUN effet sur l'imprimante, la file ou la confirmation. Aucune IA : tout est calcul
// déterministe sur le journal. Les cartes (project_views.go) ne portent aucun bouton de lancement.

// ProjectIDInput identifie un projet.
type ProjectIDInput struct {
	ID string `json:"id" jsonschema:"identifiant du projet (prj_...)"`
}

// RemovePartInput : retirer une pièce d'un projet.
type RemovePartInput struct {
	ProjectID string `json:"project_id"`
	ModelID   string `json:"model_id" jsonschema:"pièce à retirer du projet (le modèle et ses impressions restent intacts)"`
}

// PendingVerdictsInput : borne de get_pending_verdicts.
type PendingVerdictsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"nombre maximal d'impressions (défaut 20, max 200)"`
}

// ProjectStatusInput : changement de statut depuis la carte.
type ProjectStatusInput struct {
	ProjectID string `json:"project_id"`
	Status    string `json:"status" jsonschema:"idea | design | printing | done | in_use | abandoned"`
}

// VerdictFormInput : verdict donné depuis la carte (même champs que set_print_verdict).
type VerdictFormInput struct {
	PrintJobID string `json:"print_job_id"`
	Verdict    string `json:"verdict" jsonschema:"success | failed | redo"`
	Reason     string `json:"reason,omitempty"`
	Note       string `json:"note,omitempty"`
}

func registerProjectTools(s *mcp.Server, c *core.Core) {
	svc, ins := c.Projects, c.Insights
	html, _ := viewHTML()
	f := false
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	write := &mcp.ToolAnnotations{DestructiveHint: &f}
	idem := &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true}

	if svc != nil {
		addViewResource(s, ProjectViewURI, "project_view", "Projet",
			"Carte Projet (get_project, list_projects) : statut, avancement « 4/6 pièces imprimées », coût et temps réels, pièces avec compteur, sous-projets repliables. Ne lance, n'arrête ni ne reprend aucune impression.", html)

		addTool(s, &mcp.Tool{
			Name: "create_project", Title: "Créer un projet",
			Description: "Crée un projet de suivi (à ne pas confondre avec une commande client, ni avec le projet ouvert dans Studio : get_project_state). Un projet regroupe des pièces du catalogue avec une quantité voulue (add_project_part) et peut contenir des sous-projets (parent_id : un assemblage, 4 niveaux au plus). " +
				"Statut idea par défaut. Ne lance aucune impression.",
			Annotations: write,
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in projects.CreateInput) (*mcp.CallToolResult, projects.Project, error) {
			p, err := svc.Create(ctx, in)
			if err != nil {
				return nil, projects.Project{}, err
			}
			return textResult("Projet créé : " + projectLine(*p)), *p, nil
		})

		addTool(s, &mcp.Tool{
			Name: "update_project", Title: "Modifier un projet",
			Description: "Modifie le nom, la description, l'échéance, le statut, la collection adoptée, le parent ou l'archivage d'un projet (les champs omis ne changent pas). Le statut suggéré par get_project n'est JAMAIS appliqué seul : c'est cet appel qui change le statut. " +
				"Rattacher un projet sous son propre descendant est refusé (cycle), comme dépasser 4 niveaux. Aucune suppression définitive : archived=true seulement. Ne lance aucune impression.",
			Annotations: idem,
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in projects.UpdateInput) (*mcp.CallToolResult, projects.Project, error) {
			p, err := svc.Update(ctx, in)
			if err != nil {
				return nil, projects.Project{}, err
			}
			return textResult("Projet mis à jour : " + projectLine(*p)), *p, nil
		})

		addTool(s, &mcp.Tool{
			Name: "add_project_part", Title: "Ajouter une pièce à un projet",
			Description: "Ajoute une pièce (model_id du catalogue) à un projet avec la quantité voulue (1 à 1000, défaut 1) et un rôle libre ; si la pièce y est déjà, met à jour sa quantité ou son rôle. Une même pièce peut figurer dans plusieurs projets. Ne lance aucune impression.",
			Annotations: idem,
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in projects.PartInput) (*mcp.CallToolResult, projects.Project, error) {
			p, err := svc.AddPart(ctx, in)
			if err != nil {
				return nil, projects.Project{}, err
			}
			return textResult("Pièce enregistrée dans le projet : " + projectLine(*p) + ". get_project donne l'avancement."), *p, nil
		})

		addTool(s, &mcp.Tool{
			Name: "remove_project_part", Title: "Retirer une pièce d'un projet",
			Description: "Retire une pièce d'un projet : seul le lien est supprimé, le modèle, ses fichiers et ses impressions restent intacts. Ne lance aucune impression.",
			Annotations: idem,
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in RemovePartInput) (*mcp.CallToolResult, projects.Project, error) {
			p, err := svc.RemovePart(ctx, in.ProjectID, in.ModelID)
			if err != nil {
				return nil, projects.Project{}, err
			}
			return textResult("Pièce retirée du projet : " + projectLine(*p)), *p, nil
		})

		addTool(s, &mcp.Tool{
			Name: "get_project", Title: "Fiche d'un projet",
			Description: "Fiche d'un projet : statut, pièces avec leur compteur (« 2/3 imprimée ; 1 jugée réussie, 1 non jugée »), sous-projets avec leur avancement propre, avancement agrégé « 4/6 pièces imprimées », coût et temps réels cumulés, déclinaisons, statut suggéré et prochaine étape. " +
				"« Imprimée » = l'impression a atteint 100 %, verdict ou pas ; le verdict (réussie, ratée, à refaire) est affiché à part (set_print_verdict). Coût et temps absents = « non disponibles » avec la raison. Lecture seule : calcul déterministe, aucune IA, aucun lancement.",
			Annotations: ro, Meta: AppToolMeta(ProjectViewURI),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in ProjectIDInput) (*mcp.CallToolResult, projects.Detail, error) {
			d, err := svc.Get(ctx, in.ID)
			if err != nil {
				return nil, projects.Detail{}, err
			}
			return textResult(projectDetailText(d)), *d, nil
		})

		addTool(s, &mcp.Tool{
			Name: "list_projects", Title: "Lister les projets",
			Description: "Liste les projets en arbre (projets racine avec leurs sous-projets imbriqués et leur avancement). status : liste plate de tous les projets de ce statut ; parent_id : descendre dans les sous-projets d'un projet ; les projets archivés sont masqués par défaut (include_archived). " +
				"Lecture seule. Sans projet : renvoie une liste vide avec l'état vide expliqué.",
			Annotations: ro, Meta: AppToolMeta(ProjectViewURI),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in projects.ListQuery) (*mcp.CallToolResult, projects.ListOutput, error) {
			o, err := svc.List(ctx, in)
			if err != nil {
				return nil, projects.ListOutput{}, err
			}
			return textResult(projectListText(o)), *o, nil
		})

		// ---- outils réservés à la carte Projet (non destinés au modèle) ----
		addTool(s, &mcp.Tool{
			Name: "submit_project_status", Title: "Statut du projet (carte)",
			Description: "RÉSERVÉ À LA CARTE PROJET (non destiné au modèle) : change le statut d'un projet (comme update_project) et renvoie la fiche à jour. Ne lance, n'arrête ni ne reprend aucune impression.",
			Annotations: idem, Meta: appOnlyMeta(),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in ProjectStatusInput) (*mcp.CallToolResult, projects.Detail, error) {
			if _, err := svc.Update(ctx, projects.UpdateInput{ID: in.ProjectID, Status: in.Status}); err != nil {
				return nil, projects.Detail{}, err
			}
			d, err := svc.Get(ctx, in.ProjectID)
			if err != nil {
				return nil, projects.Detail{}, err
			}
			return textResult(projectDetailText(d)), *d, nil
		})
		addTool(s, &mcp.Tool{
			Name: "submit_project_part", Title: "Pièce du projet (carte)",
			Description: "RÉSERVÉ À LA CARTE PROJET (non destiné au modèle) : ajoute une pièce ou change sa quantité (comme add_project_part) et renvoie la fiche à jour. Ne lance aucune impression.",
			Annotations: idem, Meta: appOnlyMeta(),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in projects.PartInput) (*mcp.CallToolResult, projects.Detail, error) {
			if _, err := svc.AddPart(ctx, in); err != nil {
				return nil, projects.Detail{}, err
			}
			d, err := svc.Get(ctx, in.ProjectID)
			if err != nil {
				return nil, projects.Detail{}, err
			}
			return textResult(projectDetailText(d)), *d, nil
		})
	}

	if ins != nil {
		addViewResource(s, VerdictViewURI, "verdict_view", "Verdict d'impression",
			"Carte Verdict (get_pending_verdicts, set_print_verdict) : trois gros boutons Réussi / Raté (raison) / À refaire pour les impressions terminées, note facultative, modifiable ensuite. Un verdict n'a aucun effet sur l'imprimante, la file ni la confirmation.", html)

		addTool(s, &mcp.Tool{
			Name: "set_print_verdict", Title: "Donner le verdict d'une impression",
			Description: "Enregistre le JUGEMENT de l'humain sur une impression terminée : verdict success (réussie), failed (ratée) ou redo (à refaire), avec reason pour un échec (stringing, warping, adhesion, spaghetti, layer_shift, under_extrusion, nozzle_clog, other ; « other » par défaut avec avertissement) et note libre (2000 caractères, sans secret). " +
				"À appeler SEULEMENT quand l'utilisateur dit lui-même comment l'impression s'est passée : n'invente JAMAIS un verdict, aucun verdict par défaut. Idempotent ; un changement ajoute une entrée d'audit. Refusé si l'impression est inconnue ou pas terminée. " +
				"Le verdict ne change pas « imprimée » (100 % atteint) : il alimente les taux de réussite. Il n'a AUCUN effet sur l'imprimante, la file ni la confirmation de lancement.",
			Annotations: idem, Meta: AppToolMeta(VerdictViewURI),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in insights.SetVerdictInput) (*mcp.CallToolResult, insights.VerdictOutput, error) {
			in.Source = insights.SourceClaude
			o, err := ins.SetVerdict(ctx, in)
			if err != nil {
				return nil, insights.VerdictOutput{}, err
			}
			return textResult(verdictText(o)), *o, nil
		})
		addTool(s, &mcp.Tool{
			Name: "submit_print_verdict", Title: "Verdict d'impression (carte)",
			Description: "RÉSERVÉ À LA CARTE VERDICT (non destiné au modèle) : même effet et même validation que set_print_verdict, source « carte ». Ne lance, n'arrête ni ne reprend aucune impression et ne touche ni à la file ni à la confirmation.",
			Annotations: idem, Meta: appOnlyMeta(),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in VerdictFormInput) (*mcp.CallToolResult, insights.VerdictOutput, error) {
			o, err := ins.SetVerdict(ctx, insights.SetVerdictInput{PrintJobID: in.PrintJobID, Verdict: in.Verdict, Reason: in.Reason, Note: in.Note, Source: insights.SourceHumanCard})
			if err != nil {
				return nil, insights.VerdictOutput{}, err
			}
			return textResult(verdictText(o)), *o, nil
		})
		addTool(s, &mcp.Tool{
			Name: "get_pending_verdicts", Title: "Impressions à juger",
			Description: "Impressions terminées (COMPLETED ou FAILED) qui n'ont pas encore de verdict, les plus récentes d'abord (limit, défaut 20, max 200). Lecture seule. À proposer à l'utilisateur après une impression : il répond « réussie », « ratée » (et pourquoi) ou « à refaire », puis set_print_verdict. Sans verdict, aucun taux de réussite n'est honnête.",
			Annotations: ro, Meta: AppToolMeta(VerdictViewURI),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in PendingVerdictsInput) (*mcp.CallToolResult, insights.PendingOutput, error) {
			o, err := ins.PendingVerdicts(ctx, in.Limit)
			if err != nil {
				return nil, insights.PendingOutput{}, err
			}
			return textResult(pendingText(o)), *o, nil
		})
	}
}

func projectLine(p projects.Project) string {
	s := fmt.Sprintf("%s · %s · %s", p.ID, p.Name, projects.StatusLabel(p.Status))
	if p.ParentID != "" {
		s += " · sous-projet de " + p.ParentID
	}
	if p.Archived {
		s += " · archivé"
	}
	return s
}

func projectDetailText(d *projects.Detail) string {
	var b strings.Builder
	b.WriteString(d.Note + "\n")
	if len(d.Path) > 0 {
		names := make([]string, len(d.Path))
		for i, p := range d.Path {
			names[i] = p.Name
		}
		b.WriteString("Chemin : " + strings.Join(names, " > ") + "\n")
	}
	if len(d.Parts) > 0 {
		b.WriteString("Pièces :\n")
		for _, p := range d.Parts {
			n := p.ModelName
			if n == "" {
				n = p.ModelID
			}
			b.WriteString("- " + n + " (" + p.ModelID + ") : " + p.Text)
			if p.Implicit {
				b.WriteString(" [adoptée avec la collection]")
			}
			b.WriteString("\n")
		}
	}
	for _, sp := range d.SubProjects {
		fmt.Fprintf(&b, "%s- Sous-projet « %s » (%s, %s) : %s\n", strings.Repeat("  ", sp.Depth-1), sp.Name, sp.ID, projects.StatusLabel(sp.Status), sp.Progress.Text)
	}
	if d.Totals.Available {
		fmt.Fprintf(&b, "Coût réel (calculé) : %.2f %s ; temps réel (mesuré) : %s ; matière : %.0f g (pesée ou estimation selon la fiche).\n", d.Totals.ActualCost, d.Totals.Currency, hms(d.Totals.ActualSeconds), d.Totals.ActualGrams)
	}
	if d.Totals.InProgress > 0 {
		fmt.Fprintf(&b, "%d tentative(s) en cours, coût estimé à part : %.2f.\n", d.Totals.InProgress, d.Totals.PlannedCost)
	}
	for _, v := range d.Variants {
		b.WriteString("Déclinaison : " + v.Name + " (" + v.GoalKind + ")\n")
	}
	return b.String()
}

func hms(sec float64) string {
	m := int(sec/60 + 0.5)
	if m < 60 {
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%d h %02d", m/60, m%60)
}

func projectListText(o *projects.ListOutput) string {
	var b strings.Builder
	b.WriteString(o.Note + "\n")
	for _, it := range o.Projects {
		fmt.Fprintf(&b, "%s- %s (%s) · %s · %s", strings.Repeat("  ", it.Depth), it.Project.Name, it.Project.ID, projects.StatusLabel(it.Project.Status), it.Progress.Text)
		if it.SuggestedStatus != "" {
			b.WriteString(" · statut suggéré : " + projects.StatusLabel(it.SuggestedStatus))
		}
		if it.Project.Archived {
			b.WriteString(" · archivé")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func verdictText(o *insights.VerdictOutput) string {
	t := o.Note
	for _, w := range o.Warnings {
		t += " " + w
	}
	return t
}

func pendingText(o *insights.PendingOutput) string {
	var b strings.Builder
	b.WriteString(o.Note + "\n")
	for _, p := range o.Pending {
		n := p.ModelName
		if n == "" {
			n = p.Name
		}
		fmt.Fprintf(&b, "- %s · %s · %s · terminée le %s (%s)\n", p.PrintJobID, n, p.State, p.FinishedAt.Format("02/01/2006 15:04"), p.MaterialFamily)
	}
	if len(o.Pending) > 0 {
		b.WriteString("Demander à l'utilisateur : réussie, ratée (pourquoi : " + reasonIDs() + ") ou à refaire ? Puis set_print_verdict. Ne jamais deviner.\n")
	}
	return b.String()
}

func reasonIDs() string {
	ids := make([]string, len(insights.FailReasons))
	for i, r := range insights.FailReasons {
		ids[i] = r.ID
	}
	return strings.Join(ids, ", ")
}
