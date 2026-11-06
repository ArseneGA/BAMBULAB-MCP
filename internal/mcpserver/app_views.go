package mcpserver

import (
	"context"
	"embed"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
)

// Vues MCP Apps du jalon 3 (dossier §14.3). Un seul bundle HTML monofichier
// (web/mcp-app, Vite + vite-plugin-singlefile, Three.js embarqué) est servi sous
// deux URI ; la vue choisit son écran d'après la forme du résultat d'outil :
//
//	ui://bambu/search.html  liée à search_models      (liste de résultats)
//	ui://bambu/model.html   liée à get_model_details  (fiche compacte + viewer 3D)
//
// Le maillage n'est PAS servi par HTTP : l'iframe de l'hôte ne peut pas joindre
// localhost de façon fiable (docs/research/mcp-apps.md §4). La vue le charge par
// tranches via l'outil get_mesh_chunk, réservé à l'application (visibility app).

//go:embed all:ui/views
var viewsFS embed.FS

// URI des vues.
const (
	SearchViewURI = "ui://bambu/search.html"
	ModelViewURI  = "ui://bambu/model.html"
)

// MeshChunkBytes : octets bruts par tranche. En base64, 72 000 octets donnent
// 96 000 caractères (< 100 000, et très en dessous du seuil d'environ 150 000
// caractères au-delà duquel certains hôtes remplacent le résultat par un fichier).
const MeshChunkBytes = 72_000

// MaxMeshBytes : taille maximale d'aperçu servie à la vue par cet outil.
const MaxMeshBytes = 16 << 20

// toolViewMeta lie les outils du catalogue à leur vue (appliqué par addTool).
var toolViewMeta = map[string]mcp.Meta{
	"search_models":     AppToolMeta(SearchViewURI),
	"get_model_details": AppToolMeta(ModelViewURI),
}

// appOnlyMeta : outil appelable par la vue mais caché du modèle.
func appOnlyMeta() mcp.Meta {
	return mcp.Meta{"ui": map[string]any{"visibility": []string{"app"}}}
}

const fallbackViewHTML = `<!doctype html><html lang="fr"><head><meta charset="utf-8"><title>Atelier</title>
<style>:root{color-scheme:light dark}body{font:14px/1.5 system-ui,sans-serif;padding:16px}</style></head>
<body><h1 style="font-size:16px">Vue non construite</h1>
<p>Ce binaire a été compilé sans la vue MCP App. Construire avec <code>scripts/build.ps1 -Task release</code>.
Les résultats restent disponibles en texte dans la conversation.</p></body></html>`

// viewHTML renvoie le bundle monofichier construit, ou la page de repli.
func viewHTML() (html string, built bool) {
	b, err := viewsFS.ReadFile("ui/views/mcp-app.html")
	if err != nil || len(b) == 0 {
		return fallbackViewHTML, false
	}
	return string(b), true
}

// ViewsBuilt indique si le bundle MCP App est embarqué.
func ViewsBuilt() bool { _, ok := viewHTML(); return ok }

func addViewResource(s *mcp.Server, uri, name, title, desc, html string) {
	// CSP vide = aucune connexion ni ressource externe : Three.js est dans le HTML
	// et le maillage arrive par callServerTool.
	uiMeta := mcp.Meta{"ui": map[string]any{
		"csp":           map[string]any{},
		"prefersBorder": true,
	}}
	s.AddResource(&mcp.Resource{
		URI: uri, Name: name, Title: title, Description: desc,
		MIMEType: AppMIMEType, Size: int64(len(html)), Meta: uiMeta,
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		if req.Params.URI != uri {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: uri, MIMEType: AppMIMEType, Text: html, Meta: uiMeta,
		}}}, nil
	})
}

// registerAppViews déclare les ressources ui:// et l'outil réservé à l'application.
func registerAppViews(s *mcp.Server, svc *catalog.Service) {
	html, _ := viewHTML()
	addViewResource(s, SearchViewURI, "search_view", "Résultats de recherche",
		"Liste interactive des modèles trouvés (search_models) ; choisir un modèle ouvre sa fiche.", html)
	addViewResource(s, ModelViewURI, "model_view", "Fiche modèle",
		"Fiche compacte d'un modèle (get_model_details) avec viewer 3D, sélection d'une sous-partie et dimensions.", html)

	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	addTool(s, &mcp.Tool{
		Name: "get_mesh_chunk", Title: "Maillage d'aperçu par tranches",
		Description: "RÉSERVÉ À LA VUE 3D (non destiné au modèle) : renvoie une tranche du GLB d'aperçu en base64 (≤ 100 000 caractères). " +
			"Appeler avec index=0, lire chunk_count, puis les index suivants et concaténer. Pour décrire un modèle, utiliser get_model_details.",
		Annotations: ro,
		Meta:        appOnlyMeta(),
	}, (&meshChunkHandler{svc: svc}).get)
}

// MeshChunkInput désigne la tranche demandée.
type MeshChunkInput struct {
	ModelID string `json:"model_id" jsonschema:"identifiant du modèle (son fichier le plus récent)"`
	FileID  string `json:"file_id,omitempty" jsonschema:"fichier source précis (sinon le plus récent du modèle)"`
	Index   int    `json:"index,omitempty" jsonschema:"numéro de tranche à partir de 0"`
}

// MeshChunk est une tranche du GLB d'aperçu.
type MeshChunk struct {
	ModelID    string `json:"model_id"`
	FileID     string `json:"file_id"`
	SHA256     string `json:"sha256" jsonschema:"empreinte du GLB dérivé complet (identique pour toutes les tranches)"`
	MIME       string `json:"mime"`
	SizeBytes  int64  `json:"size_bytes" jsonschema:"taille du GLB complet en octets"`
	ChunkChars int    `json:"chunk_chars" jsonschema:"taille maximale d'une tranche en caractères base64"`
	ChunkCount int    `json:"chunk_count"`
	Index      int    `json:"index"`
	Encoding   string `json:"encoding" jsonschema:"toujours base64"`
	Data       string `json:"data" jsonschema:"octets de la tranche en base64 standard"`
	Triangles  int    `json:"triangles,omitempty"`
	Decimated  bool   `json:"decimated,omitempty" jsonschema:"aperçu simplifié : jamais utilisé pour fabriquer"`
}

type meshChunkHandler struct{ svc *catalog.Service }

func (h *meshChunkHandler) get(ctx context.Context, _ *mcp.CallToolRequest, in MeshChunkInput) (*mcp.CallToolResult, MeshChunk, error) {
	if in.Index < 0 {
		return nil, MeshChunk{}, fmt.Errorf("index négatif")
	}
	pa, err := h.svc.GetPreviewAsset(ctx, catalog.PreviewAssetInput{ModelID: in.ModelID, FileID: in.FileID})
	if err != nil {
		return nil, MeshChunk{}, err
	}
	if pa.Preview.State != catalog.PreviewReady {
		return nil, MeshChunk{}, fmt.Errorf("aperçu %s : %s", pa.Preview.State, pa.Note)
	}
	f, err := h.svc.OpenPreviewAsset(ctx, pa.Preview.SHA256)
	if err != nil {
		return nil, MeshChunk{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, MeshChunk{}, err
	}
	size := st.Size()
	if size > MaxMeshBytes {
		return nil, MeshChunk{}, fmt.Errorf("aperçu trop volumineux pour la vue (%d octets, maximum %d) : utiliser l'interface web", size, int64(MaxMeshBytes))
	}
	count := int((size + MeshChunkBytes - 1) / MeshChunkBytes)
	if count == 0 {
		count = 1
	}
	if in.Index >= count {
		return nil, MeshChunk{}, fmt.Errorf("index %d hors limites (chunk_count=%d)", in.Index, count)
	}
	off := int64(in.Index) * MeshChunkBytes
	buf := make([]byte, min(int64(MeshChunkBytes), size-off))
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return nil, MeshChunk{}, err
	}
	out := MeshChunk{
		ModelID: pa.ModelID, FileID: pa.FileID, SHA256: pa.Preview.SHA256, MIME: pa.MIME,
		SizeBytes: size, ChunkChars: base64.StdEncoding.EncodedLen(MeshChunkBytes), ChunkCount: count, Index: in.Index,
		Encoding: "base64", Data: base64.StdEncoding.EncodeToString(buf), Triangles: pa.Triangles, Decimated: pa.Decimated,
	}
	return textResult(fmt.Sprintf("Tranche %d/%d du GLB d'aperçu (%d octets au total).", in.Index+1, count, size)), out, nil
}
