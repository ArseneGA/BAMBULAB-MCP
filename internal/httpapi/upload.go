package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	registerUploadLink(mux, c)

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

// isUploadLinkRequest : PUT ou POST /upload/<jeton> à la forme exacte d'un jeton. C'est le SEUL chemin d'envoi ouvert
// sur la surface publique et il n'a pas d'autre authentification que son jeton à usage unique.
func isUploadLinkRequest(r *http.Request) bool {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		return false
	}
	t, ok := strings.CutPrefix(r.URL.Path, "/upload/")
	return ok && catalog.ValidUploadToken(t)
}

// uploadLinkWindow : durée maximale de réception du corps (le lien lui-même expire en 10 minutes).
const uploadLinkWindow = 15 * time.Minute

// registerUploadLink : réception d'un fichier par lien à usage unique (create_upload_link) :
//
//	PUT|POST /upload/{token}   corps = fichier entier (curl -T) ; réponse JSON {file, note}
//
// Le jeton aléatoire (256 bits, 10 min, une seule fois) tient lieu d'authentification : le garde le laisse passer et la
// surface publique l'autorise seul (public.go). Le service contrôle taille, type, signature et empreinte, puis importe
// le fichier comme import_model. Rien n'est lu d'autre que le corps ; les erreurs ne révèlent rien du jeton.
func registerUploadLink(mux *http.ServeMux, c *core.Core) {
	h := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		rc := http.NewResponseController(w)
		_ = rc.SetReadDeadline(time.Now().Add(uploadLinkWindow))
		body := http.MaxBytesReader(w, r.Body, 4<<30) // garde-fou ; la limite exacte est celle du lien
		res, err := c.Catalog.UploadLinkReceive(r.Context(), r.PathValue("token"), body, r.ContentLength)
		if errors.Is(err, catalog.ErrUploadTooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": err.Error(), "code": "too_large"})
			return
		}
		reply(c, w, res, err, http.StatusOK)
	}
	mux.HandleFunc("PUT /upload/{token}", h)
	mux.HandleFunc("POST /upload/{token}", h)
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
