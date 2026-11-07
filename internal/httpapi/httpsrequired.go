package httpapi

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
)

// isHumanPage : pages HTML humaines (isHumanPath sans l'état JSON /api/session, utilisé par la SPA).
func isHumanPage(p string) bool { return isHumanPath(p) && p != "/api/session" }

// firstHTTPSURL renvoie la première URL publique en https ("" si aucune).
func (o Options) firstHTTPSURL() string {
	for _, p := range o.publicURLs() {
		if strings.HasPrefix(strings.ToLower(p), "https://") {
			return strings.TrimRight(p, "/")
		}
	}
	return ""
}

// HTTPSNeededHeader : en-tête de GET /api/session présent quand la SPA est servie en http hors loopback ;
// sa valeur est la première URL https publique (vide si aucune).
const HTTPSNeededHeader = "X-Atelier-Https-Url"

// HTTPSRequiredMessage explique pourquoi une page humaine refuse le http hors localhost.
const HTTPSRequiredMessage = "Cette page exige HTTPS (ou localhost) : en http sur le réseau, le navigateur n'envoie pas ses en-têtes de sécurité et bambud refuse la soumission par prudence."

// httpsRequired répond à une page humaine demandée en http hors loopback : page explicite avec lien https.
// La protection cross-origin n'est jamais assouplie (pas d'acceptation d'Origin: null).
func (o Options) httpsRequired(w http.ResponseWriter, r *http.Request) {
	humanauth.PageHeaders(w)
	w.WriteHeader(http.StatusForbidden)
	link := ""
	if base := o.firstHTTPSURL(); base != "" {
		link = base + r.URL.RequestURI()
	}
	_ = httpsRequiredTpl.Execute(w, map[string]any{"Msg": HTTPSRequiredMessage, "Link": link, "Style": template.CSS(humanauth.Style)})
}

var httpsRequiredTpl = template.Must(template.New("https").Parse(`<!doctype html><html lang="fr"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer">
<title>HTTPS requis</title><style>{{.Style}}</style></head><body><main><h1>HTTPS requis</h1>
<p class="warn">{{.Msg}}</p>
{{if .Link}}<p>Cette page exige HTTPS : ouvrez <a href="{{.Link}}">{{.Link}}</a></p>
{{else}}<p>Aucune URL https n'est configurée (BAMBU_PUBLIC_URL). Exposez bambud en https (par exemple <code>tailscale serve</code>) ou ouvrez cette page depuis le poste de bambud via <code>http://localhost</code>. L'API JSON et /mcp restent utilisables en http avec le jeton.</p>{{end}}
</main></body></html>`))
