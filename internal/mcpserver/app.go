package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Helper MCP Apps (extension MCP, spec 2026-01-26), émis à la main selon
// docs/research/mcp-apps.md : le SDK Go n'a pas d'API dédiée et on ne dépend
// d'aucun SDK TypeScript.

// AppMIMEType est le type MIME d'une ressource MCP App.
const AppMIMEType = "text/html;profile=mcp-app"

// CSP liste les origines autorisées pour la vue. Tout vide = aucune connexion
// externe (défaut sûr). Pour l'atelier, toute I/O passe par callServerTool :
// aucun domaine n'est nécessaire, et Three.js est embarqué dans le HTML.
type CSP struct {
	ConnectDomains  []string `json:"connectDomains,omitempty"`
	ResourceDomains []string `json:"resourceDomains,omitempty"`
	FrameDomains    []string `json:"frameDomains,omitempty"`
	BaseURIDomains  []string `json:"baseUriDomains,omitempty"`
}

// AppResource décrit une ressource UI.
type AppResource struct {
	URI         string // ui://…
	Name        string
	Title       string
	Description string
	HTML        string
	CSP         CSP
}

// AppToolMeta renvoie le _meta d'un outil lié à une vue. visibility vide =
// modèle et application ; ["app"] cache l'outil au modèle (appelable par la vue).
// La clé plate historique "ui/resourceUri" est émise en plus, comme le fait le
// helper officiel TypeScript.
func AppToolMeta(resourceURI string, visibility ...string) mcp.Meta {
	ui := map[string]any{"resourceUri": resourceURI}
	if len(visibility) > 0 {
		ui["visibility"] = visibility
	}
	return mcp.Meta{"ui": ui, "ui/resourceUri": resourceURI}
}

// AddAppResource enregistre la ressource ui:// avec le bon MIME et sa CSP.
func AddAppResource(s *mcp.Server, r AppResource) {
	uiMeta := mcp.Meta{"ui": map[string]any{"csp": r.CSP}}
	s.AddResource(&mcp.Resource{
		URI: r.URI, Name: r.Name, Title: r.Title, Description: r.Description,
		MIMEType: AppMIMEType, Size: int64(len(r.HTML)),
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		if req.Params.URI != r.URI {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: r.URI, MIMEType: AppMIMEType, Text: r.HTML, Meta: uiMeta,
		}}}, nil
	})
}
