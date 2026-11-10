package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/issues"
)

// Carnet de bord des dysfonctionnements (FB0, internal/issues) : report_issue, list_issues, update_issue, export_issues.
// L'agent consigne spontanément ce qu'il constate d'anormal ; le serveur range, dédoublonne et masque les secrets ;
// l'export Markdown se colle à l'agent qui code. Aucune IA côté serveur, et aucun de ces outils ne lance, n'arrête
// ni ne reprend une impression.

// IssuesViewURI : carte du carnet (liée à list_issues et export_issues).
const IssuesViewURI = "ui://bambu/issues.html"

func init() {
	toolViewMeta["list_issues"] = AppToolMeta(IssuesViewURI)
}

// ExportIssuesInput : filtres de l'export (mêmes valeurs que list_issues).
type ExportIssuesInput struct {
	State    string `json:"state,omitempty" jsonschema:"open (défaut : new + seen) | new | seen | fixed | dismissed | all"`
	Severity string `json:"severity,omitempty" jsonschema:"P0 | P1 | P2 | P3"`
	Tool     string `json:"tool,omitempty" jsonschema:"restreindre à un outil"`
}

// ExportIssuesOutput : le Markdown et le nombre de constats.
type ExportIssuesOutput struct {
	Markdown string `json:"markdown" jsonschema:"texte à coller à l'agent qui code les correctifs"`
	Count    int    `json:"count"`
}

// AddIssueTools enregistre les outils du carnet des dysfonctionnements.
func AddIssueTools(s *mcp.Server, c *core.Core) {
	svc := c.Issues
	if svc == nil {
		return
	}
	html, _ := viewHTML()
	addViewResource(s, IssuesViewURI, "issues_view", "Dysfonctionnements",
		"Carte Dysfonctionnements (list_issues) : constats par gravité et par état, compteur d'occurrences, outil concerné, commit du correctif. Lecture seule.", html)
	f := false
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	write := &mcp.ToolAnnotations{DestructiveHint: &f}
	idem := &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true}

	addTool(s, &mcp.Tool{
		Name: "report_issue", Title: "Signaler un dysfonctionnement",
		Description: "À appeler SPONTANÉMENT, sans qu'on te le demande, dès que tu constates un comportement anormal de Printalk : outil qui refuse à tort, réponse incohérente avec l'état réel, fausse alerte, donnée périmée, réponse trop lourde. " +
			"Consigne le constat dans le carnet de bord : title (le symptôme en une phrase courte, sans identifiant), severity (P0 bloquant ou sécurité, P1 grave, P2 gênant par défaut, P3 détail), tool (l'outil concerné), expected, observed, et si possible print_job_id, printer_id, model_id. " +
			"Le serveur DÉDOUBLONNE (même outil et même titre normalisé : le compteur d'occurrences monte, aucune ligne n'est ajoutée) et MASQUE les secrets (codes de lancement, jetons d'URL, clés, mots de passe) avant stockage ; n'y mets de toute façon aucun secret. " +
			"Ne corrige rien et ne touche à aucune impression : ensuite, continue d'aider l'utilisateur.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in issues.ReportInput) (*mcp.CallToolResult, issues.ReportOutput, error) {
		o, err := svc.Report(ctx, in)
		if err != nil {
			return nil, issues.ReportOutput{}, err
		}
		return textResult(o.Note + " " + issueLine(o.Issue)), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "list_issues", Title: "Carnet des dysfonctionnements",
		Description: "Liste le carnet de bord des dysfonctionnements : les plus graves puis les plus fréquents d'abord. Par défaut les constats ouverts (nouveaux et vus) ; filtres state (open | new | seen | fixed | dismissed | all), severity (P0 à P3), tool. " +
			"Lecture seule. Pour obtenir un texte à coller à l'agent qui code, utiliser export_issues.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in issues.ListQuery) (*mcp.CallToolResult, issues.ListOutput, error) {
		o, err := svc.List(ctx, in)
		if err != nil {
			return nil, issues.ListOutput{}, err
		}
		return textResult(issuesText(o)), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "update_issue", Title: "Changer l'état d'un constat",
		Description: "Change l'état d'un constat du carnet : new -> seen (vu) -> fixed (corrigé, fix_commit OBLIGATOIRE : la référence du commit du correctif) ou dismissed (écarté, ce n'était pas un défaut). " +
			"Peut aussi corriger la gravité (severity). Si un symptôme déjà « fixed » est de nouveau signalé, il repasse à « new » avec une régression comptée.",
		Annotations: idem,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in issues.UpdateInput) (*mcp.CallToolResult, issues.Issue, error) {
		i, err := svc.Update(ctx, in)
		if err != nil {
			return nil, issues.Issue{}, err
		}
		return textResult("Constat mis à jour. " + issueLine(*i)), *i, nil
	})

	addTool(s, &mcp.Tool{
		Name: "export_issues", Title: "Exporter le carnet en Markdown",
		Description: "Rend le carnet (constats ouverts par défaut, mêmes filtres que list_issues) en Markdown, groupé par gravité, avec la consigne de correction : à coller tel quel à l'agent qui code (par exemple dans Claude Code). " +
			"Les secrets ont déjà été retirés. Lecture seule : rien n'est envoyé nulle part.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ExportIssuesInput) (*mcp.CallToolResult, ExportIssuesOutput, error) {
		md, n, err := svc.ExportMarkdown(ctx, issues.ListQuery{State: in.State, Severity: in.Severity, Tool: in.Tool})
		if err != nil {
			return nil, ExportIssuesOutput{}, err
		}
		return textResult(md), ExportIssuesOutput{Markdown: md, Count: n}, nil
	})
}

func issueLine(i issues.Issue) string {
	s := fmt.Sprintf("%s · %s · %s", i.ID, i.Severity, i.Title)
	if i.Tool != "" {
		s += " (" + i.Tool + ")"
	}
	s += fmt.Sprintf(" · ×%d · %s", i.Occurrences, issues.StateLabel(i.State))
	if i.FixCommit != "" {
		s += " (commit " + i.FixCommit + ")"
	}
	return s
}

func issuesText(o *issues.ListOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d constat(s) correspondant aux filtres (nouveaux %d, vus %d, corrigés %d, écartés %d au total).\n",
		o.Total, o.Counts[issues.StateNew], o.Counts[issues.StateSeen], o.Counts[issues.StateFixed], o.Counts[issues.StateDismissed])
	for _, i := range o.Issues {
		b.WriteString("- " + issueLine(i) + "\n")
	}
	if len(o.Issues) < o.Total {
		fmt.Fprintf(&b, "(%d de plus : augmenter limit ou décaler offset.)\n", o.Total-len(o.Issues))
	}
	return b.String()
}
