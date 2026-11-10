package httpapi

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
)

// Routes LOCALES et RÉSERVÉES de la sous-commande humaine `bambud skip-objects` (SK0, docs/specs/SK-ignorer-un-objet.md).
//
//	POST /api/system/skip-objects/list      {job}                    lecture : objets de l'impression, aucun envoi
//	POST /api/system/skip-objects/prepare   {job, ids}               contrôle D3 + jeton de confirmation à usage unique
//	POST /api/system/skip-objects/execute   {confirm_token, wait_s}  envoi unique de skip_objects, vérifié par s_obj
//
// Pourquoi une route et pas une 2e connexion MQTT depuis la ligne de commande : le service tient DÉJÀ la session MQTT de
// l'imprimante (reconnexion, rapport fusionné, s_obj lu en continu) et seul lui possède le code d'accès, l'empreinte TLS
// et le suivi de l'impression ; ouvrir une seconde connexion depuis un autre processus dupliquerait les secrets, risquerait
// un conflit sur une imprimante qui limite ses clients et contournerait l'audit et le journal de l'impression.
//
// Ces routes ne sont PAS une API pour le modèle ni pour le navigateur : elles exigent une connexion TCP directe depuis le
// loopback (aucun en-tête de proxy, comme /api/system/busy), aucun en-tête Origin ni Sec-Fetch-* (un navigateur en envoie
// toujours sur un POST ; la sous-commande, jamais), un corps application/json, et le jeton d'API (Bearer) dès qu'il est
// configuré (le cookie d'un navigateur enregistré ne suffit PAS). Elles ne sont pas montées sur la surface publique
// (--public-listen) et aucun outil MCP ne les appelle (test de source).
func registerSkipLocal(mux *http.ServeMux, c *core.Core, opts Options) {
	p := c.Printing
	if p == nil {
		return
	}
	reserved := func(h func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !localDirectRequest(r) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "réservé à la ligne de commande du serveur (connexion directe en loopback)", "code": "local_only"})
				return
			}
			if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" || r.Header.Get("Sec-Fetch-Mode") != "" {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "réservé à la ligne de commande : refusé à un navigateur", "code": "local_only"})
				return
			}
			if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
				writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type application/json requis", "code": "invalid"})
				return
			}
			if opts.APIToken != "" {
				got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare([]byte(got), []byte(opts.APIToken)) != 1 {
					writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "jeton d'API (Bearer) requis : BAMBU_API_TOKEN ou --api-token", "code": "unauthorized"})
					return
				}
			}
			h(w, r)
		}
	}
	mux.HandleFunc("POST /api/system/skip-objects/list", reserved(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Job string `json:"job"`
		}
		if !decode(w, r, &in) {
			return
		}
		v, err := p.SkipObjectsList(r.Context(), in.Job)
		printingReply(c, w, v, err, http.StatusOK)
	}))
	mux.HandleFunc("POST /api/system/skip-objects/prepare", reserved(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Job string `json:"job"`
			IDs []int  `json:"ids"`
		}
		if !decode(w, r, &in) {
			return
		}
		v, err := p.SkipObjectsPrepare(r.Context(), in.Job, in.IDs)
		printingReply(c, w, v, err, http.StatusOK)
	}))
	mux.HandleFunc("POST /api/system/skip-objects/execute", reserved(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ConfirmToken string  `json:"confirm_token"`
			WaitSeconds  float64 `json:"wait_s"`
		}
		if !decode(w, r, &in) {
			return
		}
		v, err := p.SkipObjectsExecute(r.Context(), in.ConfirmToken, time.Duration(in.WaitSeconds*float64(time.Second)))
		printingReply(c, w, v, err, http.StatusOK)
	}))
}
