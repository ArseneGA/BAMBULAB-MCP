package mcpserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

// Les descriptions orientent Claude vers le viewer 3D pour « montre-moi en 3D », et la ressource lue renvoie le MIME MCP Apps.
func TestViewerToolsPreferredForShowMeIn3D(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	tl, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, tt := range tl.Tools {
		if tt.Name == "search_models" || tt.Name == "get_model_details" {
			seen++
			if !strings.Contains(tt.Description, "3D") || !strings.Contains(tt.Description, "viewer") {
				t.Errorf("%s : la description doit orienter vers le viewer 3D : %q", tt.Name, tt.Description)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("outils vus : %d", seen)
	}
	rr, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpserver.ModelViewURI})
	if err != nil || len(rr.Contents) != 1 || rr.Contents[0].MIMEType != "text/html;profile=mcp-app" || rr.Contents[0].Text == "" {
		t.Fatalf("lecture de la vue : %v %+v", err, rr)
	}
}
