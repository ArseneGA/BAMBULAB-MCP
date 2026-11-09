package mcpserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

// cardLinks : outil -> carte (docs/research/mcp-cards.md). search_models garde sa vue historique (même bundle, la carte
// Bibliothèque est choisie d'après la forme du résultat).
var cardLinks = map[string]string{
	"search_models":       mcpserver.SearchViewURI,
	"list_collection":     mcpserver.LibraryViewURI,
	"compare_variants":    mcpserver.CompareViewURI,
	"estimate_cost":       mcpserver.CostsViewURI,
	"get_project_costs":   mcpserver.CostsViewURI,
	"get_usage_dashboard": mcpserver.CostsViewURI,
	"list_spools":         mcpserver.SpoolsViewURI,
	"get_ams_state":       mcpserver.SpoolsViewURI,
	"list_print_history":  mcpserver.PrintsViewURI,
	"list_print_queue":    mcpserver.PrintsViewURI,
	"get_print_log":       mcpserver.PrintsViewURI,
	"list_print_log":      mcpserver.PrintsViewURI,
	"list_printers":       mcpserver.PrintersViewURI,
}

// Chaque outil lié à une carte porte le lien dans tools/list (clé imbriquée ET clé plate), reste visible du modèle, garde son nom,
// sa description et un outputSchema de type « object » ; apply_variant (bouton de la comparaison) existe et n'est pas lié à une vue.
func TestCardToolLinks(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	tl, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	for _, tt := range tl.Tools {
		tools[tt.Name] = tt
	}
	for name, uri := range cardLinks {
		tt := tools[name]
		if tt == nil {
			t.Fatalf("outil %s absent (aucun outil ne doit être renommé)", name)
		}
		if ui := metaUI(t, tt.Meta); ui["resourceUri"] != uri || tt.Meta["ui/resourceUri"] != uri {
			t.Errorf("%s : lien de vue %v, attendu %s", name, tt.Meta, uri)
		}
		if vis, _ := metaUI(t, tt.Meta)["visibility"].([]any); len(vis) != 0 {
			t.Errorf("%s doit rester visible du modèle : %v", name, tt.Meta)
		}
		if strings.TrimSpace(tt.Description) == "" || tt.Title == "" {
			t.Errorf("%s : titre ou description vide", name)
		}
		if tt.OutputSchema == nil {
			t.Errorf("%s : outputSchema absent (la carte lit structuredContent)", name)
		} else if ty, ok := schemaType(t, tt.OutputSchema); !ok || ty != "object" {
			t.Errorf("%s : outputSchema.type = %v, attendu \"object\"", name, ty)
		}
	}
	av := tools["apply_variant"]
	if av == nil {
		t.Fatal("apply_variant absent : le bouton « Choisir cette variante » l'appelle")
	}
	if _, linked := av.Meta["ui"]; linked {
		t.Fatalf("apply_variant n'ouvre aucune vue : %v", av.Meta)
	}
	if ps := tools["start_print"]; ps != nil {
		if _, linked := ps.Meta["ui"]; linked {
			t.Fatalf("start_print ne doit être lié à aucune carte : %v", ps.Meta)
		}
	}
}

// Les six ressources ui:// des cartes : MIME MCP Apps, CSP vide (aucune origine externe), HTML non vide ; même bundle que les autres vues.
func TestCardViewResources(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	base, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpserver.SearchViewURI})
	if err != nil {
		t.Fatal(err)
	}
	for _, uri := range []string{mcpserver.LibraryViewURI, mcpserver.CompareViewURI, mcpserver.CostsViewURI, mcpserver.SpoolsViewURI, mcpserver.PrintsViewURI, mcpserver.PrintersViewURI} {
		rr, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatalf("%s : %v", uri, err)
		}
		if len(rr.Contents) != 1 {
			t.Fatalf("%s : %d contenus", uri, len(rr.Contents))
		}
		ct := rr.Contents[0]
		if ct.MIMEType != "text/html;profile=mcp-app" || ct.URI != uri {
			t.Fatalf("%s : MIME %q URI %q", uri, ct.MIMEType, ct.URI)
		}
		if !strings.Contains(strings.ToLower(ct.Text), "<html") || ct.Text != base.Contents[0].Text {
			t.Fatalf("%s : HTML différent du bundle commun (%d octets)", uri, len(ct.Text))
		}
		if csp, _ := metaUI(t, ct.Meta)["csp"].(map[string]any); len(csp) != 0 {
			t.Fatalf("%s : CSP non vide %v", uri, csp)
		}
	}
	lr, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range lr.Resources {
		seen[r.URI] = true
	}
	for _, uri := range cardLinks {
		if !seen[uri] {
			t.Errorf("ressource %s non listée", uri)
		}
	}
}

// Les résultats portent le lien de la carte dans _meta (meta_ui) en plus d'un structuredContent objet ET d'un texte de repli non vide.
func TestCardToolResultsCarryViewMeta(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	checked := 0
	for _, name := range []string{"list_printers", "list_spools", "list_print_queue", "list_print_history", "get_usage_dashboard", "get_project_costs", "list_print_log", "search_models"} {
		args := map[string]any{}
		if name == "get_usage_dashboard" {
			args["no_chart"] = true
		}
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Logf("%s non vérifié : %v", name, err)
			continue // outil indisponible dans ce montage minimal
		}
		checked++
		if ui, _ := res.Meta["ui"].(map[string]any); ui["resourceUri"] != cardLinks[name] || res.Meta["ui/resourceUri"] != cardLinks[name] {
			t.Errorf("%s : _meta du résultat %v, attendu le lien %s", name, res.Meta, cardLinks[name])
		}
		if _, ok := res.StructuredContent.(map[string]any); !ok {
			t.Errorf("%s : structuredContent n'est pas un objet : %T", name, res.StructuredContent)
		}
		text := ""
		for _, b := range res.Content {
			if tc, ok := b.(*mcp.TextContent); ok {
				text += tc.Text
			}
		}
		if strings.TrimSpace(text) == "" {
			t.Errorf("%s : texte de repli vide", name)
		}
	}
	if checked < 4 {
		t.Fatalf("trop peu d'outils vérifiés (%d) : le montage de test ne les expose plus ?", checked)
	}
}
