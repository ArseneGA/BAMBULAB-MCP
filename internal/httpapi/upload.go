package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
)

// beginUpload : corps de POST /api/uploads.
type beginUpload struct {
	Filename  string `json:"filename"`
	TotalSize int64  `json:"total_size"`
	SHA256    string `json:"sha256,omitempty"`
	catalog.UploadMeta
}

// registerUpload ajoute l'envoi de fichiers par morceaux (interface web) et l'import par lien :
//
//	POST   /api/uploads              ouvre une session {filename,total_size,sha256?,collection,tags,...}
//	PUT    /api/uploads/{id}?offset= morceau binaire brut (8 Mio max) à l'offset donné
//	GET    /api/uploads/{id}         état (reprise)
//	POST   /api/uploads/{id}/finish  contrôles + import au catalogue
//	DELETE /api/uploads/{id}         abandon
//	POST   /api/import-url           import par lien (domaines autorisés)
//
// Authentification et CSRF : celles de /api/* (Bearer, ou cookie d'un navigateur enregistré + en-tête
// X-Atelier-Request, voir guard.go). Le client ne fournit jamais de chemin.
func registerUpload(mux *http.ServeMux, c *core.Core) {
	svc := c.Catalog

	mux.HandleFunc("POST /api/uploads", func(w http.ResponseWriter, r *http.Request) {
		var in beginUpload
		if !decode(w, r, &in) {
			return
		}
		st, err := svc.UploadBegin(r.Context(), in.Filename, in.TotalSize, in.SHA256, in.UploadMeta)
		reply(c, w, st, err, http.StatusCreated)
	})
	mux.HandleFunc("PUT /api/uploads/{id}", func(w http.ResponseWriter, r *http.Request) {
		off, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "offset (octets) requis", "code": "invalid"})
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, catalog.MaxChunkBytes))
		if err != nil {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "morceau trop grand (max 8 Mio)", "code": "too_large"})
			return
		}
		st, err := svc.UploadWrite(r.Context(), r.PathValue("id"), off, data)
		replyUpload(c, w, st, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/uploads/{id}", func(w http.ResponseWriter, r *http.Request) {
		st, err := svc.UploadStatus(r.Context(), r.PathValue("id"))
		reply(c, w, st, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/uploads/{id}/finish", func(w http.ResponseWriter, r *http.Request) {
		res, err := svc.UploadFinish(r.Context(), r.PathValue("id"))
		replyUpload(c, w, res, err, http.StatusOK)
	})
	mux.HandleFunc("DELETE /api/uploads/{id}", func(w http.ResponseWriter, r *http.Request) {
		err := svc.UploadCancel(r.PathValue("id"))
		reply(c, w, map[string]bool{"canceled": true}, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/import-url", func(w http.ResponseWriter, r *http.Request) {
		var in catalog.URLImportInput
		if !decode(w, r, &in) {
			return
		}
		res, err := svc.ImportFromURL(r.Context(), in)
		reply(c, w, res, err, http.StatusOK)
	})
}

// replyUpload : comme reply, mais un décalage d'offset renvoie aussi l'état (next_offset) pour la reprise.
func replyUpload(c *core.Core, w http.ResponseWriter, v any, err error, ok int) {
	var mm *catalog.UploadChunkMismatchError
	if errors.As(err, &mm) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "offset_mismatch", "status": mm.Status})
		return
	}
	reply(c, w, v, err, ok)
}
