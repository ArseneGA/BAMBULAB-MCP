package httpapi

import (
	"html/template"
	"net/http"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/localtime"
)

var pairTpl = template.Must(template.New("pair").Parse(`<!doctype html><html lang="fr"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Appairer un agent Windows</title>
<style>` + humanauth.Style + `</style></head><body><main><h1>Appairer un agent Windows</h1>
{{if .Token}}<p class="ok">Jeton d'appairage à usage unique (valable jusqu'à {{.Expires}}). Il n'est affiché qu'une fois et n'est jamais transmis à Claude.</p>
<dl><dt>Jeton</dt><dd><code>{{.Token}}</code></dd></dl>
<p>Sur le poste Windows : <code>bambu-agent pair --server {{.Server}} --token &lt;jeton&gt;</code></p>
{{else}}<p>Crée un jeton d'appairage à usage unique pour <code>bambu-agent pair</code>. Un agent appairé peut inventorier les dossiers Windows autorisés et transférer des fichiers vers ce service ; il ne peut ni lancer ni arrêter une impression.</p>
<form method="post"><button type="submit">Créer un jeton</button></form>{{end}}
</main></body></html>`))

// registerPairPage monte GET/POST /agents/pair : le jeton d'appairage ne se crée que depuis un navigateur
// enregistré (cookie) ou par la CLI humaine (bambud agent pair), jamais par un appel JSON non authentifié.
func registerPairPage(mux *http.ServeMux, c *core.Core, reg *agentlink.Registry, human *humanauth.Service) {
	mux.Handle("GET /agents/pair", human.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		humanauth.PageHeaders(w)
		_ = pairTpl.Execute(w, map[string]any{})
	})))
	mux.Handle("POST /agents/pair", human.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, exp, err := reg.NewPairing(r.Context())
		if err != nil {
			c.Log.Error("appairage", "err", err)
			http.Error(w, "erreur interne", http.StatusInternalServerError)
			return
		}
		humanauth.PageHeaders(w)
		_ = pairTpl.Execute(w, map[string]any{"Token": tok, "Expires": localtime.Page(exp), "Server": c.Catalog.BaseURL()})
	})))
}
