package printing_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

// De bout en bout par MCP : show_gcode_preview (texte + planche PNG + lien de vue), get_gcode_layers et get_gcode_slice
// (outils réservés à la vue, enchaînés jusqu'à la fin), instructions du serveur.
func TestShowGcodePreviewOverMCP(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()

	gb, err := os.ReadFile(e2eGcode(t, 12, false))
	if err != nil {
		t.Fatal(err)
	}
	art, _, err := r.c.Blobs.Put(bytes.NewReader(gb), "")
	if err != nil {
		t.Fatal(err)
	}

	// les instructions du serveur disent d'appeler l'aperçu après chaque découpe
	if ins := r.cs.InitializeResult().Instructions; !strings.Contains(ins, "show_gcode_preview") || !strings.Contains(ins, "create_upload_link") {
		t.Fatalf("instructions du serveur : %q", ins)
	}

	res, err := r.cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "show_gcode_preview", Arguments: map[string]any{"artifact_id": art, "max_tiles": 4}})
	if err != nil || res.IsError {
		t.Fatalf("show_gcode_preview : %v %+v", err, res)
	}
	firstImage(t, res) // repli sans vue : planche PNG
	txt := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(txt, "12 couches") || !strings.Contains(txt, "Outer wall") || !strings.Contains(txt, "planche-contact") {
		t.Fatalf("texte : %s", txt)
	}
	st, _ := res.StructuredContent.(map[string]any)
	if st["kind"] != "gcode_preview" || st["layers_total"].(float64) != 12 || st["artifact_sha256"] != art {
		t.Fatalf("structuré : %v", st)
	}
	if ui, _ := res.Meta["ui"].(map[string]any); ui["resourceUri"] != mcpserver.GcodeViewURI {
		t.Fatalf("résultat sans lien de vue : %v", res.Meta)
	}

	// outils de la vue : visibilité « app » seulement, résumé par couche puis tranches jusqu'à la fin
	lt, err := r.cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range lt.Tools {
		switch tl.Name {
		case "get_gcode_layers", "get_gcode_slice":
			if vis, _ := tl.Meta["ui"].(map[string]any)["visibility"].([]any); len(vis) != 1 || vis[0] != "app" {
				t.Errorf("%s doit être réservé à la vue : %v", tl.Name, tl.Meta)
			}
		case "show_gcode_preview":
			if ui, _ := tl.Meta["ui"].(map[string]any); ui["resourceUri"] != mcpserver.GcodeViewURI {
				t.Errorf("show_gcode_preview : %v", tl.Meta)
			}
			if !strings.Contains(tl.Description, "APRÈS CHAQUE DÉCOUPE") {
				t.Errorf("description : %q", tl.Description)
			}
		}
	}
	idx := r.call("get_gcode_layers", map[string]any{"artifact_id": art})
	if z, _ := idx["z"].([]any); len(z) != 12 || z[11].(float64) != 2.4 {
		t.Fatalf("get_gcode_layers : %v", idx)
	}
	if idx["max_total_points"].(float64) != 2_000_000 || idx["slice_points"].(float64) != 12000 {
		t.Fatalf("budgets : %v", idx)
	}
	seen, next := 0, 1
	for next != 0 && seen < 20 {
		sl := r.call("get_gcode_slice", map[string]any{"artifact_id": art, "from_layer": next})
		ls, _ := sl["layers"].([]any)
		seen += len(ls)
		next = int(sl["next"].(float64))
	}
	if seen != 12 || next != 0 {
		t.Fatalf("tranches : %d couches vues, next=%d", seen, next)
	}
	if r.callErr("get_gcode_slice", map[string]any{"artifact_id": art, "from_layer": 99}) == "" {
		t.Fatal("couche hors plage : erreur attendue")
	}
	if r.callErr("show_gcode_preview", map[string]any{}) == "" {
		t.Fatal("sans artefact : erreur attendue")
	}
}
