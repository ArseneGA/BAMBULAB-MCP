package agentlink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxJSON = 4 << 20

// Handler renvoie le routeur des routes agent (préfixe BasePath inclus). Les
// routes d'administration (appairage, affectation de travaux) ne sont pas ici :
// elles sont montées par internal/httpapi.
func (r *Registry) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+BasePath+"/pair", func(w http.ResponseWriter, req *http.Request) {
		var in PairRequest
		if !readJSON(w, req, &in) {
			return
		}
		resp, err := r.Pair(req.Context(), in)
		reply(w, resp, err, http.StatusOK)
	})
	auth := func(h func(w http.ResponseWriter, req *http.Request, a Agent)) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			a, err := r.Authenticate(req.Context(), req.Header.Get("Authorization"))
			if err != nil {
				writeErr(w, err)
				return
			}
			h(w, req, a)
		}
	}
	mux.HandleFunc("POST "+BasePath+"/hello", auth(func(w http.ResponseWriter, req *http.Request, a Agent) {
		var in HelloRequest
		if !readJSON(w, req, &in) {
			return
		}
		resp, err := r.Hello(req.Context(), a, in)
		reply(w, resp, err, http.StatusOK)
	}))
	mux.HandleFunc("POST "+BasePath+"/heartbeat", auth(func(w http.ResponseWriter, req *http.Request, a Agent) {
		var in HeartbeatRequest
		if !readJSON(w, req, &in) {
			return
		}
		resp, err := r.Heartbeat(req.Context(), a, in)
		reply(w, resp, err, http.StatusOK)
	}))
	mux.HandleFunc("GET "+BasePath+"/tasks/next", auth(func(w http.ResponseWriter, req *http.Request, a Agent) {
		wait := time.Duration(0)
		if s := req.URL.Query().Get("wait"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				wait = time.Duration(n) * time.Second
			}
		}
		t, err := r.NextTask(req.Context(), a, wait)
		if err != nil {
			writeErr(w, err)
			return
		}
		if t == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, t)
	}))
	mux.HandleFunc("POST "+BasePath+"/tasks/{id}/start", auth(func(w http.ResponseWriter, req *http.Request, a Agent) {
		err := r.StartTask(req.Context(), a, req.PathValue("id"))
		reply(w, map[string]string{"state": StateRunning}, err, http.StatusOK)
	}))
	mux.HandleFunc("POST "+BasePath+"/tasks/{id}/progress", auth(func(w http.ResponseWriter, req *http.Request, a Agent) {
		var in ProgressReport
		if !readJSON(w, req, &in) {
			return
		}
		err := r.ReportProgress(req.Context(), a, req.PathValue("id"), in)
		reply(w, map[string]string{"ok": "true"}, err, http.StatusOK)
	}))
	mux.HandleFunc("POST "+BasePath+"/tasks/{id}/result", auth(func(w http.ResponseWriter, req *http.Request, a Agent) {
		var in TaskResult
		if !readJSON(w, req, &in) {
			return
		}
		t, err := r.Complete(req.Context(), a, req.PathValue("id"), in)
		reply(w, map[string]string{"state": t.State}, err, http.StatusOK)
	}))
	mux.HandleFunc("POST "+BasePath+"/inventory", auth(func(w http.ResponseWriter, req *http.Request, a Agent) {
		var in InventoryRequest
		if !readJSON(w, req, &in) {
			return
		}
		resp, err := r.RecordInventory(req.Context(), a, in)
		reply(w, resp, err, http.StatusOK)
	}))
	mux.HandleFunc("POST "+BasePath+"/uploads/begin", auth(func(w http.ResponseWriter, req *http.Request, a Agent) {
		var in UploadBegin
		if !readJSON(w, req, &in) {
			return
		}
		st, err := r.BeginUpload(req.Context(), a, in)
		reply(w, st, err, http.StatusOK)
	}))
	mux.HandleFunc("PUT "+BasePath+"/uploads/{sha}", auth(func(w http.ResponseWriter, req *http.Request, a Agent) {
		start, total, err := parseContentRange(req.Header.Get("Content-Range"))
		if err != nil {
			writeErr(w, errf(http.StatusBadRequest, CodeInvalid, "%v", err))
			return
		}
		st, err := r.PutChunk(req.Context(), a, req.URL.Query().Get("task"), req.PathValue("sha"), start, total, req.Body)
		if err != nil {
			var ae *Error
			if errors.As(err, &ae) {
				w.Header().Set("Upload-Offset", strconv.FormatInt(st.Offset, 10))
			}
			writeErr(w, err)
			return
		}
		w.Header().Set("Upload-Offset", strconv.FormatInt(st.Offset, 10))
		writeJSON(w, http.StatusOK, st)
	}))
	mux.HandleFunc("GET "+BasePath+"/blobs/{sha}", auth(func(w http.ResponseWriter, req *http.Request, a Agent) {
		f, err := r.OpenInput(req.Context(), a, req.PathValue("sha"))
		if err != nil {
			writeErr(w, err)
			return
		}
		defer f.Close()
		st, _ := f.Stat()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Content-SHA256", req.PathValue("sha"))
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, req, "", st.ModTime(), f) // Range géré
	}))
	return mux
}

func parseContentRange(h string) (start, total int64, err error) {
	// "bytes START-END/TOTAL" ; un morceau vide s'écrit "bytes */TOTAL" (start = total).
	rest, ok := strings.CutPrefix(h, "bytes ")
	if !ok {
		return 0, 0, fmt.Errorf("en-tête Content-Range requis (bytes début-fin/total)")
	}
	rng, tot, ok := strings.Cut(rest, "/")
	if !ok {
		return 0, 0, fmt.Errorf("Content-Range invalide")
	}
	total, err = strconv.ParseInt(tot, 10, 64)
	if err != nil || total < 0 {
		return 0, 0, fmt.Errorf("Content-Range : total invalide")
	}
	if rng == "*" {
		return total, total, nil
	}
	a, _, ok := strings.Cut(rng, "-")
	if !ok {
		return 0, 0, fmt.Errorf("Content-Range invalide")
	}
	start, err = strconv.ParseInt(a, 10, 64)
	if err != nil || start < 0 || start > total {
		return 0, 0, fmt.Errorf("Content-Range : début invalide")
	}
	return start, total, nil
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxJSON))
	if err := dec.Decode(v); err != nil {
		writeErr(w, errf(http.StatusBadRequest, CodeInvalid, "corps JSON invalide : %v", err))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError écrit une erreur au format du protocole (exporté pour httpapi).
func WriteError(w http.ResponseWriter, err error) { writeErr(w, err) }

func writeErr(w http.ResponseWriter, err error) {
	var ae *Error
	if errors.As(err, &ae) {
		writeJSON(w, ae.Status, map[string]APIError{"error": {Code: ae.Code, Message: ae.Message}})
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]APIError{"error": {Code: "internal", Message: "erreur interne"}})
}

func reply(w http.ResponseWriter, v any, err error, status int) {
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, status, v)
}

// Reply est l'aide d'httpapi : réponse JSON ou erreur de protocole.
func Reply(w http.ResponseWriter, v any, err error, status int) { reply(w, v, err, status) }
