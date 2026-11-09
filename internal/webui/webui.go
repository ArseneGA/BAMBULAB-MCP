// Package webui embarque l'interface web (React/Vite, dossier web/) dans le
// binaire bambud et la sert avec un repli SPA. Le contenu de assets/ est produit
// par `scripts/build.ps1` (ou `make build`) ; sans build, bambud démarre quand
// même et affiche une page « interface non construite ».
package webui

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
)

//go:embed all:assets
var embedded embed.FS

// Built indique si l'interface a été construite (index.html embarqué).
func Built() bool { return builtIn(assetsFS()) }

func assetsFS() fs.FS {
	sub, err := fs.Sub(embedded, "assets")
	if err != nil {
		return nil
	}
	return sub
}

func builtIn(f fs.FS) bool {
	if f == nil {
		return false
	}
	st, err := fs.Stat(f, "index.html")
	return err == nil && !st.IsDir()
}

// Handler sert l'interface embarquée.
func Handler() http.Handler { return handlerFor(assetsFS()) }

// reserved : préfixes qui ne doivent jamais recevoir le repli HTML.
func reserved(p string) bool {
	for _, pre := range []string{"/api/", "/mcp", "/health"} {
		if p == strings.TrimSuffix(pre, "/") || strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

func handlerFor(f fs.FS) http.Handler {
	built := builtIn(f)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reserved(r.URL.Path) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			// /health n'existe qu'en GET : le routeur répondait 405, le repli ne doit pas le changer en 404.
			if r.URL.Path == "/health" && r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				w.WriteHeader(http.StatusMethodNotAllowed)
				_, _ = w.Write([]byte(`{"error":"méthode non autorisée","code":"method_not_allowed"}` + "\n"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"ressource introuvable","code":"not_found"}` + "\n"))
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "méthode non autorisée", http.StatusMethodNotAllowed)
			return
		}
		if !built {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(notBuiltPage))
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if st, err := fs.Stat(f, name); err != nil || st.IsDir() {
			// Un chemin avec extension introuvable est un vrai 404 (jamais du HTML à la place d'un script).
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
			name = "index.html" // route gérée côté client
		}
		b, err := fs.ReadFile(f, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		if name == "index.html" {
			h.Set("Cache-Control", "no-cache")
		} else if strings.HasPrefix(name, "assets/") {
			h.Set("Cache-Control", "public, max-age=31536000, immutable") // noms hachés par Vite
		}
		h.Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
	})
}

// notBuiltTpl : page humaine « interface non construite », dans le gabarit commun (humanauth.Layout).
var notBuiltTpl = template.Must(template.New("nb").Parse(humanauth.Layout("Interface non construite", "", "") + `{{template "head" .}}
<h1>Interface non construite</h1>
<p>Le service <code>bambud</code> fonctionne, mais le binaire a été compilé sans l'interface web.</p>
<p>Construire l'interface puis le binaire : <code>scripts/build.ps1</code> (Windows) ou <code>make build</code>.</p>
<p class="dim">L'API (<code>/api</code>) et le serveur MCP (<code>/mcp</code>) restent disponibles.</p>
{{template "foot"}}`))

var notBuiltPage = func() string {
	var b strings.Builder
	_ = notBuiltTpl.Execute(&b, nil)
	return b.String()
}()
