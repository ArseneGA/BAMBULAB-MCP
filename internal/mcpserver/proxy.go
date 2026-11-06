package mcpserver

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
)

// Proxy relaie un serveur MCP distant (bambud, MCP Streamable HTTP) vers un
// serveur local exposable sur n'importe quel transport (stdio pour Claude
// Desktop). Les outils et ressources du distant sont énumérés à la connexion
// puis relayés tels quels : schémas, annotations et _meta (donc MCP Apps) inclus.
// Limite connue : la liste est figée à la connexion (pas de relais de
// notifications list_changed) ; relancer le pont après une mise à jour du service.
type Proxy struct {
	Server *mcp.Server
	remote *mcp.ClientSession
}

// Close ferme la session distante.
func (p *Proxy) Close() error { return p.remote.Close() }

// NewProxy se connecte via transport au serveur distant et construit le miroir local.
func NewProxy(ctx context.Context, transport mcp.Transport, log *slog.Logger) (*Proxy, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "bambu-mcp-bridge", Version: core.Version}, nil)
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connexion à bambud : %w", err)
	}
	instructions := ""
	if init := cs.InitializeResult(); init != nil {
		instructions = init.Instructions
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "bambu-mcp", Title: "Atelier Bambu Lab (pont)", Version: core.Version},
		&mcp.ServerOptions{Logger: log, Instructions: instructions})
	p := &Proxy{Server: srv, remote: cs}

	nt, nr := 0, 0
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			cs.Close()
			return nil, fmt.Errorf("liste des outils : %w", err)
		}
		t := *tool
		srv.AddTool(&t, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return cs.CallTool(ctx, &mcp.CallToolParams{Name: req.Params.Name, Arguments: req.Params.Arguments})
		})
		nt++
	}
	for res, err := range cs.Resources(ctx, nil) {
		if err != nil {
			cs.Close()
			return nil, fmt.Errorf("liste des ressources : %w", err)
		}
		r := *res
		srv.AddResource(&r, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: req.Params.URI})
		})
		nr++
	}
	if log != nil {
		log.Info("pont MCP prêt", "outils", nt, "ressources", nr)
	}
	return p, nil
}
