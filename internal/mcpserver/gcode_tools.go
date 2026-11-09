package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// Visionneuse 3D couche par couche « comme Bambu Studio » : show_gcode_preview (visible du modèle ; ouvre la vue
// ui://bambu/gcode.html, joint une planche-contact PNG et un résumé texte), get_gcode_layers et get_gcode_slice
// (réservés à la vue : résumé par couche, puis trajectoires par tranches de ≤ 12 000 points).

// GcodeSuggestion : phrase ajoutée aux résultats de découpe (et reprise dans les instructions du serveur) pour que le
// modèle propose l'aperçu au lieu de fabriquer ses propres rendus.
const GcodeSuggestion = "Après la découpe, montre le résultat avec show_gcode_preview(print_job_id) : visionneuse 3D couche par couche (types de trajectoires, curseurs de couches, temps et filament par couche) ; ne fabrique pas tes propres rendus."

// ShowGcodePreviewInput : plaque du G-code découpé.
type ShowGcodePreviewInput struct {
	PrintJobID string `json:"print_job_id,omitempty" jsonschema:"impression (ou découpe) dont on veut voir les trajectoires"`
	ArtifactID string `json:"artifact_id,omitempty" jsonschema:"à la place de print_job_id : empreinte sha256 d'un .gcode.3mf"`
	Plate      int    `json:"plate,omitempty" jsonschema:"plaque (défaut : celle de l'impression, sinon 1)"`
	MaxTiles   int    `json:"max_tiles,omitempty" jsonschema:"vignettes de la planche-contact jointe (1 à 16, défaut 9)"`
}

// ShowGcodePreviewOutput : ce que la vue a besoin de connaître (le détail par couche vient de get_gcode_layers).
type ShowGcodePreviewOutput struct {
	Kind           string   `json:"kind" jsonschema:"toujours gcode_preview"`
	ArtifactSHA256 string   `json:"artifact_sha256"`
	Plate          int      `json:"plate"`
	LayersTotal    int      `json:"layers_total"`
	Features       []string `json:"features"`
	TotalTimeS     float64  `json:"total_time_s"`
	TimeSource     string   `json:"time_source" jsonschema:"slicer | estimation"`
	FilamentMM     float64  `json:"filament_mm"`
	SheetLayers    []int    `json:"sheet_layers,omitempty" jsonschema:"couches de la planche-contact jointe"`
	PNGSHA256      string   `json:"png_sha256,omitempty"`
	Simplified     bool     `json:"simplified,omitempty"`
}

// AddGcodeTools enregistre les outils de la visionneuse 3D du G-code. Lecture seule.
func AddGcodeTools(s *mcp.Server, p *printing.Service) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	addTool(s, &mcp.Tool{
		Name: "show_gcode_preview", Title: "Aperçu 3D de la découpe",
		Description: "À APPELER APRÈS CHAQUE DÉCOUPE (execute_headless_slice, execute_plate_slice) et chaque fois qu'on veut voir comment la pièce sera imprimée : ouvre dans la conversation une visionneuse 3D couche par couche comme Bambu Studio " +
			"(trajectoires réelles du G-code, couleurs par type : parois, remplissage, ponts, surplombs, supports, bordure, tour d'essuyage ; curseur de couches, lecture, vues iso/dessus/face, temps et filament par couche). " +
			"Joint aussi un résumé texte (couches, durée, filament) et une planche-contact PNG : sans vue, ils suffisent. Ne fabrique pas tes propres rendus. " +
			"Accepte print_job_id (la plaque de l'impression) ou artifact_id (empreinte d'un .gcode.3mf). Autres aperçus : capture_preview(layers, first_layer).",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ShowGcodePreviewInput) (*mcp.CallToolResult, ShowGcodePreviewOutput, error) {
		gi := printing.GcodeInput{PrintJobID: in.PrintJobID, ArtifactID: in.ArtifactID, Plate: in.Plate}
		idx, err := p.GcodeIndex(ctx, gi)
		if err != nil {
			return nil, ShowGcodePreviewOutput{}, err
		}
		out := ShowGcodePreviewOutput{Kind: "gcode_preview", ArtifactSHA256: idx.ArtifactSHA256, Plate: idx.Plate, LayersTotal: idx.LayersTotal, Features: idx.Features,
			TotalTimeS: idx.TotalTimeS, TimeSource: idx.TimeSource, FilamentMM: idx.FilamentMM, Simplified: idx.Simplified}
		txt := fmt.Sprintf("Plaque %d : %s. La visionneuse 3D couche par couche s'ouvre dans la conversation si l'hôte affiche les vues (types : %s).",
			idx.Plate, idx.SummaryLine(), strings.Join(visibleFeatures(idx.Features), ", "))
		res := &mcp.CallToolResult{}
		pi := printing.PreviewInput{PrintJobID: in.PrintJobID, ArtifactID: in.ArtifactID, Plate: in.Plate, Mode: "layers", MaxTiles: in.MaxTiles}
		if pi.MaxTiles == 0 {
			pi.MaxTiles = 9
		}
		if o, perr := p.CapturePreview(ctx, pi); perr == nil {
			out.PNGSHA256, out.SheetLayers = o.PNGSHA256, o.Layers
			txt += fmt.Sprintf(" La planche-contact jointe montre les couches %s (repli sans vue).", joinLayerNums(o.Layers))
			res.Content = []mcp.Content{&mcp.TextContent{Text: txt}, &mcp.ImageContent{Data: o.PNG, MIMEType: o.MIME}}
		} else {
			txt += " (Planche-contact indisponible : " + perr.Error() + ".)"
			res.Content = []mcp.Content{&mcp.TextContent{Text: txt}}
		}
		if idx.TimeSource != "slicer" {
			res.Content[0].(*mcp.TextContent).Text += " Durée estimée d'après les vitesses programmées (sans accélérations) : l'estimation du slicer fait foi."
		}
		return res, out, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_gcode_layers", Title: "Résumé par couche du G-code",
		Description: "RÉSERVÉ À LA VUE (non destiné au modèle) : hauteur Z, durée et longueur de filament de chaque couche, types de trajectoires et budgets de points d'une plaque découpée. Pour montrer la découpe : show_gcode_preview.",
		Annotations: ro,
		Meta:        appOnlyMeta(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.GcodeInput) (*mcp.CallToolResult, printing.GcodeIndexOutput, error) {
		o, err := p.GcodeIndex(ctx, in)
		if err != nil {
			return nil, printing.GcodeIndexOutput{}, err
		}
		return textResult(fmt.Sprintf("Plaque %d : %s.", o.Plate, o.SummaryLine())), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_gcode_slice", Title: "Trajectoires du G-code par tranches",
		Description: "RÉSERVÉ À LA VUE (non destiné au modèle) : couches consécutives à partir de from_layer (≤ 12 000 points par réponse, coordonnées entières de 0,05 mm, points codés en différences), avec next = première couche de la tranche suivante (0 = terminé). Pour montrer la découpe : show_gcode_preview.",
		Annotations: ro,
		Meta:        appOnlyMeta(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.GcodeSliceInput) (*mcp.CallToolResult, printing.GcodeSliceOutput, error) {
		o, err := p.GcodeSlice(ctx, in)
		if err != nil {
			return nil, printing.GcodeSliceOutput{}, err
		}
		return textResult(fmt.Sprintf("Couches %d à %d sur %d : %d points.", o.From, o.From+len(o.Layers)-1, o.LayersTotal, o.Points)), *o, nil
	})
}

func visibleFeatures(f []string) []string {
	out := make([]string, 0, len(f))
	for _, n := range f {
		if n != printing.TravelFeature {
			out = append(out, n)
		}
	}
	return out
}
