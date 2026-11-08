package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// Visionneuse de trajectoires par couche : show_layers (visible du modèle, ouvre la vue ui://bambu/layers.html et
// renvoie AUSSI une planche-contact PNG, donc utile sans vue) et get_layer_paths (réservé à la vue : segments d'une couche).

// ShowLayersInput : G-code à parcourir.
type ShowLayersInput struct {
	PrintJobID string `json:"print_job_id,omitempty" jsonschema:"impression (ou découpe) dont on veut voir les couches"`
	ArtifactID string `json:"artifact_id,omitempty" jsonschema:"à la place de print_job_id : empreinte sha256 d'un .gcode.3mf"`
	Plate      int    `json:"plate,omitempty" jsonschema:"plaque (défaut : celle de l'impression, sinon 1)"`
	MaxTiles   int    `json:"max_tiles,omitempty" jsonschema:"vignettes de la planche-contact jointe (1 à 16, défaut 9)"`
}

// ShowLayersOutput : ce que la vue a besoin de connaître, et ce que montre la planche.
type ShowLayersOutput struct {
	ArtifactSHA256 string `json:"artifact_sha256"`
	Plate          int    `json:"plate"`
	LayersTotal    int    `json:"layers_total"`
	SheetLayers    []int  `json:"sheet_layers" jsonschema:"couches de la planche-contact jointe"`
	PNGSHA256      string `json:"png_sha256,omitempty"`
}

// AddLayerTools enregistre les outils de la visionneuse de trajectoires. Lecture seule.
func AddLayerTools(s *mcp.Server, p *printing.Service) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	// la ressource ui://bambu/layers.html est déclarée par registerAppViews (même bundle que les autres vues)
	addTool(s, &mcp.Tool{
		Name: "show_layers", Title: "Trajectoires par couche",
		Description: "Ouvre une visionneuse des trajectoires d'extrusion du G-code avec un curseur de couche (dans Claude quand l'hôte affiche les vues) et joint une planche-contact PNG de couches réparties de la première à la dernière : " +
			"c'est le moyen d'inspecter toutes les couches SANS 20 appels. Sans vue, la planche suffit. Autres aperçus : capture_preview(layers=[…] ou every_n, format=gif) ; contact réel avec le plateau : capture_preview(mode=first_layer).",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ShowLayersInput) (*mcp.CallToolResult, ShowLayersOutput, error) {
		pi := printing.PreviewInput{PrintJobID: in.PrintJobID, ArtifactID: in.ArtifactID, Plate: in.Plate, Mode: "layers", MaxTiles: in.MaxTiles}
		if pi.MaxTiles == 0 {
			pi.MaxTiles = 9
		}
		o, err := p.CapturePreview(ctx, pi)
		if err != nil {
			return nil, ShowLayersOutput{}, err
		}
		out := ShowLayersOutput{PNGSHA256: o.PNGSHA256, SheetLayers: o.Layers}
		out.ArtifactSHA256, _ = o.Render["artifact_sha256"].(string)
		out.Plate, _ = o.Render["plate"].(int)
		out.LayersTotal, _ = o.Render["layers_total"].(int)
		txt := fmt.Sprintf("%d couche(s) au total (plaque %d). La visionneuse s'ouvre dans la conversation si l'hôte affiche les vues ; la planche-contact jointe montre les couches %s.\n%s",
			out.LayersTotal, out.Plate, joinLayerNums(o.Layers), strings.Join(o.Legend, "\n"))
		res := &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: txt},
			&mcp.ImageContent{Data: o.PNG, MIMEType: o.MIME},
		}}
		return res, out, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_layer_paths", Title: "Trajectoires d'une couche",
		Description: "RÉSERVÉ À LA VUE (non destiné au modèle) : polylignes d'extrusion d'une couche du G-code, par type, en entiers de 0,05 mm, bornées en taille. Pour montrer des couches au modèle ou à l'utilisateur : show_layers ou capture_preview.",
		Annotations: ro,
		Meta:        appOnlyMeta(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in printing.LayerPathsInput) (*mcp.CallToolResult, printing.LayerPathsOutput, error) {
		o, err := p.LayerPaths(ctx, in)
		if err != nil {
			return nil, printing.LayerPathsOutput{}, err
		}
		return textResult(fmt.Sprintf("Couche %d/%d : %d segments, %d points.", o.Layer, o.LayersTotal, o.Segments, o.Points)), *o, nil
	})
}

func joinLayerNums(a []int) string {
	p := make([]string, len(a))
	for i, v := range a {
		p[i] = fmt.Sprint(v)
	}
	return strings.Join(p, ", ")
}
