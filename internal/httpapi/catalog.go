package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
)

const maxBody = 4 << 20

// registerCatalog ajoute les routes du catalogue (web app) :
//
//	GET  /api/models                 recherche paginée (q, tag, collection, format, availability, trashed, sort, desc, limit, offset)
//	GET  /api/models/{id}            fiche
//	GET  /api/collections[/{id}]     collections (effectifs) / contenu
//	GET  /api/assets/{sha}.glb       aperçu GLB (URL signée, ETag, Range)
//	POST /api/import  /api/scan      import non destructif (job) / inventaire lecture seule
//	POST /api/operations/preview     aperçu d'opérations (jeton)
//	POST /api/operations/apply       application avec jeton
//	GET  /api/trash, POST /api/trash/restore
//	POST /api/tags, GET /api/duplicates
func registerCatalog(mux *http.ServeMux, c *core.Core) {
	svc := c.Catalog

	mux.HandleFunc("GET /api/models", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		sq := catalog.SearchQuery{
			Text: q.Get("q"), Tags: q["tag"], Collection: q.Get("collection"), Format: q.Get("format"),
			Availability: q.Get("availability"), Sort: q.Get("sort"), Descending: truthy(q.Get("desc")),
			Limit: atoi(q.Get("limit")), Offset: atoi(q.Get("offset")),
		}
		switch q.Get("trashed") {
		case "include":
			sq.IncludeTrashed = true
		case "only":
			sq.OnlyTrashed = true
		}
		res, err := svc.Search(r.Context(), sq)
		reply(c, w, res, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/models/{id}", func(w http.ResponseWriter, r *http.Request) {
		d, err := svc.GetModelDetails(r.Context(), r.PathValue("id"))
		reply(c, w, d, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/collections", func(w http.ResponseWriter, r *http.Request) {
		cols, err := svc.ListCollections(r.Context())
		reply(c, w, map[string]any{"collections": cols}, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/collections/{id}", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		l, err := svc.ListCollection(r.Context(), catalog.ListCollectionInput{
			Collection: r.PathValue("id"), Limit: atoi(q.Get("limit")), Offset: atoi(q.Get("offset")), Recursive: truthy(q.Get("recursive"))})
		reply(c, w, l, err, http.StatusOK)
	})

	mux.HandleFunc("GET /api/assets/{file}", func(w http.ResponseWriter, r *http.Request) {
		sha, ok := strings.CutSuffix(r.PathValue("file"), ".glb")
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "ressource introuvable"})
			return
		}
		q := r.URL.Query()
		if !svc.VerifyAssetSignature(sha, q.Get("exp"), q.Get("sig")) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "URL d'aperçu invalide ou expirée : redemander get_preview_asset"})
			return
		}
		f, err := svc.OpenPreviewAsset(r.Context(), sha)
		if errors.Is(err, catalog.ErrAssetUnknown) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "aperçu introuvable"})
			return
		}
		if err != nil {
			c.Log.Error("ouverture de l'aperçu", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "erreur interne"})
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "erreur interne"})
			return
		}
		h := w.Header()
		h.Set("Content-Type", "model/gltf-binary")
		h.Set("ETag", `"`+sha+`"`)
		h.Set("Cache-Control", "private, max-age=300")
		h.Set("X-Content-Type-Options", "nosniff")
		// L'URL est une capacité signée : lisible depuis l'iframe isolée d'un hôte MCP Apps.
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Cross-Origin-Resource-Policy", "cross-origin")
		http.ServeContent(w, r, "model.glb", st.ModTime(), f)
	})

	mux.HandleFunc("POST /api/import", func(w http.ResponseWriter, r *http.Request) {
		var in catalog.ImportInput
		if !decode(w, r, &in) {
			return
		}
		res, err := svc.StartImportWait(r.Context(), in)
		status := http.StatusAccepted
		if res != nil && res.Done {
			status = http.StatusOK
		}
		reply(c, w, res, err, status)
	})
	mux.HandleFunc("POST /api/scan", func(w http.ResponseWriter, r *http.Request) {
		var in catalog.ScanInput
		if !decode(w, r, &in) {
			return
		}
		res, err := svc.ScanFolder(r.Context(), in)
		reply(c, w, res, err, http.StatusOK)
	})

	mux.HandleFunc("POST /api/operations/preview", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Operations []catalog.Operation `json:"operations"`
		}
		if !decode(w, r, &in) {
			return
		}
		res, err := svc.PreviewOperations(r.Context(), in.Operations)
		reply(c, w, res, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/operations/apply", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Token string `json:"token"`
		}
		if !decode(w, r, &in) {
			return
		}
		res, err := svc.ApplyOperations(r.Context(), in.Token)
		reply(c, w, res, err, http.StatusOK)
	})

	mux.HandleFunc("GET /api/trash", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		res, err := svc.Search(r.Context(), catalog.SearchQuery{OnlyTrashed: true, Sort: "updated", Descending: true,
			Limit: atoi(q.Get("limit")), Offset: atoi(q.Get("offset"))})
		reply(c, w, res, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/trash/restore", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ModelIDs []string `json:"model_ids"`
		}
		if !decode(w, r, &in) {
			return
		}
		res, err := svc.RestoreFromTrash(r.Context(), in.ModelIDs)
		reply(c, w, res, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/tags", func(w http.ResponseWriter, r *http.Request) {
		var in catalog.TagInput
		if !decode(w, r, &in) {
			return
		}
		res, err := svc.TagModels(r.Context(), in)
		reply(c, w, res, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/duplicates", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		res, err := svc.FindExactDuplicates(r.Context(), catalog.DuplicatesInput{SHA256: q.Get("sha256"), Path: q.Get("path"),
			IncludeTrashed: truthy(q.Get("trashed")), Limit: atoi(q.Get("limit")), Offset: atoi(q.Get("offset"))})
		reply(c, w, res, err, http.StatusOK)
	})
}

func truthy(s string) bool {
	return s == "1" || strings.EqualFold(s, "true") || strings.EqualFold(s, "yes")
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "corps JSON invalide : " + err.Error(), "code": "invalid"})
		return false
	}
	return true
}

// reply écrit la réponse ou l'erreur traduite en statut HTTP.
func reply(c *core.Core, w http.ResponseWriter, v any, err error, ok int) {
	if err == nil {
		writeJSON(w, ok, v)
		return
	}
	status, code := http.StatusInternalServerError, "internal"
	switch {
	case errors.Is(err, catalog.ErrNotAllowed):
		status, code = http.StatusForbidden, "not_allowed"
	case errors.Is(err, catalog.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, catalog.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid"
	case errors.Is(err, catalog.ErrConflict):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, catalog.ErrBackendMissing):
		status, code = http.StatusServiceUnavailable, "backend_missing"
	}
	if status == http.StatusInternalServerError {
		c.Log.Error("erreur API catalogue", "err", err)
		writeJSON(w, status, map[string]string{"error": "erreur interne", "code": code})
		return
	}
	writeJSON(w, status, map[string]string{"error": err.Error(), "code": code})
}
