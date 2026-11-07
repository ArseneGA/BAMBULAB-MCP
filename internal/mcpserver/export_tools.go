package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/export"
)

// Export de projet (docs/BACKLOG.md, point 22) : export_project lance un travail qui construit une archive zip
// partageable ; get_project_export la suit et donne un lien de téléchargement signé à durée limitée.
// Aucune publication : le texte rendu liste ce qu'il reste à faire à la main (MakerWorld / Printables).

// ExportStartOut : réponse de export_project.
type ExportStartOut struct {
	ExportID string             `json:"export_id"`
	JobID    string             `json:"job_id" jsonschema:"suivre avec get_job_status ou get_project_export"`
	State    string             `json:"state" jsonschema:"queued | ready | failed"`
	Plan     export.PlanSummary `json:"plan"`
	Export   *export.Record     `json:"export,omitempty" jsonschema:"présent quand l'archive est déjà prête"`
	Link     *export.Link       `json:"download,omitempty" jsonschema:"lien de téléchargement signé (durée limitée) quand l'archive est prête"`
	Next     []string           `json:"next_steps" jsonschema:"ce qui reste à faire à la main pour publier"`
}

// ExportGetIn : lecture d'un export.
type ExportGetIn struct {
	ExportID string `json:"export_id,omitempty" jsonschema:"export à relire ; omis : liste des exports récents"`
}

// ExportGetOut : état d'un export ou liste.
type ExportGetOut struct {
	Exports  []export.Record `json:"exports"`
	JobState string          `json:"job_state,omitempty"`
	Progress float64         `json:"progress,omitempty"`
	Message  string          `json:"message,omitempty"`
	Link     *export.Link    `json:"download,omitempty"`
	Next     []string        `json:"next_steps,omitempty"`
}

const exportWait = 10 * time.Second

// AddExportTools enregistre les outils d'export. Fonction séparée : une seule ligne dans New().
func AddExportTools(s *mcp.Server, c *core.Core) {
	svc := c.Export
	catalogWait := c.Catalog.WaitJob
	f := false

	addTool(s, &mcp.Tool{
		Name: "export_project", Title: "Exporter un projet (archive zip partageable)",
		Description: "Prépare l'archive zip d'un projet (collection_id OU model_ids) : originaux STL/STEP/3MF/OBJ des dernières versions (ou versions choisies, sha256 vérifiés), " +
			"sources paramétriques, profils de découpe effectifs (uniquement les réglages modifiés + préréglage officiel, jamais de G-code), README.md en français " +
			"(pièces, quantités, matières, réglages, temps/masse/coût estimés, notice d'assemblage, quincaillerie, licence) et manifest.json. " +
			"Options : include_gcode_3mf (désactivé par défaut : fichiers propres à une imprimante), include_photos, license (défaut « non précisée »), assembly_notes, hardware, quantities, versions. " +
			"Aucun secret, aucune donnée d'imprimante, aucun chemin du serveur dans l'archive. Travail asynchrone (streaming, taille bornée) : rend un lien de téléchargement signé à durée limitée, " +
			"redemandable avec get_project_export. Ne publie rien : la réponse liste ce qu'il reste à faire à la main sur MakerWorld ou Printables.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &f},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in export.Input) (*mcp.CallToolResult, ExportStartOut, error) {
		st, err := svc.Start(ctx, in)
		if err != nil {
			return nil, ExportStartOut{}, err
		}
		out := ExportStartOut{ExportID: st.ExportID, JobID: st.JobID, State: export.StateQueued, Plan: st.Plan, Next: export.PublishSteps}
		_, _, _ = catalogWait(ctx, st.JobID, exportWait)
		rec, err := svc.Get(ctx, st.ExportID)
		if err != nil {
			return nil, ExportStartOut{}, err
		}
		out.State = rec.State
		var sb strings.Builder
		fmt.Fprintf(&sb, "Export « %s » : %d pièce(s), %d fichier(s) d'origine, %s décompressés.\n", st.Plan.Title, len(st.Plan.Parts), st.Plan.Files, humanSize(st.Plan.TotalBytes))
		switch rec.State {
		case export.StateReady:
			out.Export = &rec
			if out.Link, err = svc.Link(ctx, rec.ID); err != nil {
				return nil, ExportStartOut{}, err
			}
			fmt.Fprintf(&sb, "Archive prête (%s, %d entrées). Téléchargement (valable jusqu'à %s) : %s\n", humanSize(rec.SizeBytes), rec.Summary.Files, out.Link.ExpiresAt.Local().Format("15:04"), out.Link.URL)
		case export.StateFailed:
			return nil, ExportStartOut{}, fmt.Errorf("l'export a échoué : %s", rec.Error)
		default:
			fmt.Fprintf(&sb, "Archive en préparation (travail %s) : relire avec get_project_export(export_id=\"%s\").\n", st.JobID, st.ExportID)
		}
		writeWarnings(&sb, st.Plan.Warnings, st.Plan.Skipped)
		if rec.Summary != nil {
			writeWarnings(&sb, rec.Summary.Warnings, rec.Summary.Skipped)
		}
		sb.WriteString("\nÀ faire à la main pour publier (rien n'est publié automatiquement) :\n")
		for i, n := range export.PublishSteps {
			fmt.Fprintf(&sb, "%d. %s\n", i+1, n)
		}
		return textResult(sb.String()), out, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_project_export", Title: "Suivre un export de projet",
		Description: "État d'un export (export_id) : avancement du travail et, quand l'archive est prête, un lien de téléchargement signé à durée limitée (un nouveau lien à chaque appel). Sans export_id : liste des exports récents. " +
			"Une archive est conservée 24 h sur le serveur ; ensuite relancer export_project.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ExportGetIn) (*mcp.CallToolResult, ExportGetOut, error) {
		if in.ExportID == "" {
			recs, err := svc.List(ctx, 30)
			if err != nil {
				return nil, ExportGetOut{}, err
			}
			return textResult(fmt.Sprintf("%d export(s) de projet.", len(recs))), ExportGetOut{Exports: recs}, nil
		}
		rec, err := svc.Get(ctx, in.ExportID)
		if err != nil {
			return nil, ExportGetOut{}, err
		}
		out := ExportGetOut{Exports: []export.Record{rec}}
		if rec.JobID != "" {
			if j, err := c.Queue.Get(ctx, rec.JobID); err == nil {
				out.JobState, out.Progress, out.Message = string(j.State), j.Progress, j.Message
			}
		}
		var sb strings.Builder
		switch rec.State {
		case export.StateReady:
			if out.Link, err = svc.Link(ctx, rec.ID); err != nil {
				return nil, ExportGetOut{}, err
			}
			out.Next = export.PublishSteps
			fmt.Fprintf(&sb, "Archive prête : %s (%s). Téléchargement (valable jusqu'à %s) : %s\n", rec.FileName, humanSize(rec.SizeBytes), out.Link.ExpiresAt.Local().Format("15:04"), out.Link.URL)
		case export.StateFailed:
			fmt.Fprintf(&sb, "L'export a échoué : %s\n", rec.Error)
		case export.StateExpired:
			sb.WriteString("L'archive a expiré (24 h) : relancer export_project.\n")
		default:
			fmt.Fprintf(&sb, "En préparation (%s, %.0f %%) %s\n", out.JobState, out.Progress*100, out.Message)
		}
		return textResult(sb.String()), out, nil
	})
}

func writeWarnings(sb *strings.Builder, warns []string, skipped []export.Skip) {
	for _, w := range warns {
		fmt.Fprintf(sb, "Avertissement : %s\n", w)
	}
	for _, k := range skipped {
		fmt.Fprintf(sb, "Laissé de côté : %s — %s\n", k.Name, k.Reason)
	}
}

func humanSize(n int64) string {
	const k = 1024.0
	f := float64(n)
	switch {
	case f >= k*k*k:
		return fmt.Sprintf("%.1f Gio", f/(k*k*k))
	case f >= k*k:
		return fmt.Sprintf("%.1f Mio", f/(k*k))
	case f >= k:
		return fmt.Sprintf("%.1f Kio", f/k)
	}
	return fmt.Sprintf("%d o", n)
}
