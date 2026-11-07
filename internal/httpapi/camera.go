package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/camera"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/notify"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// registerCamera monte la caméra, la détection d'échec et les notifications. Rien ici ne lance ni n'arrête une impression.
//
//	GET  /api/printers/{id}/camera/snapshot     JPEG (instantané)
//	GET  /api/printers/{id}/camera/stream       flux MJPEG relayé (une seule connexion à la caméra, partagée)
//	GET  /api/camera/photos/{sha256}            JPEG d'une photo enregistrée (jamais un autre blob)
//	GET  /api/print-jobs/{id}/photos            photos rattachées à l'impression (fin, alertes)
//	GET  /api/print-jobs/{id}/analysis          dernier rapport du détecteur + alertes de l'impression
//	GET  /api/camera/settings                   PUT /api/camera/settings (modification partielle)
//	GET  /api/notifications                     canaux SANS secret + lien de saisie       POST /api/notifications/test
//	GET  /setup/notifications  POST ...         page HUMAINE de saisie des secrets (navigateur enregistré)
//	GET  /setup/camera/{id}    POST ...         page HUMAINE : épinglage du certificat de la caméra
func registerCamera(mux *http.ServeMux, c *core.Core, human *humanauth.Service) {
	cam, nv := c.Camera, c.Notify
	if cam == nil || nv == nil {
		return
	}
	nv.RegisterPages(mux, human.Require)
	cam.RegisterPages(mux, human.Require)

	mux.HandleFunc("GET /api/printers/{id}/camera/snapshot", func(w http.ResponseWriter, r *http.Request) {
		p, err := cam.Capture(r.Context(), r.PathValue("id"), "snapshot", "", 2*time.Second)
		if err != nil {
			cameraError(c, w, err)
			return
		}
		serveJPEG(w, p.JPEG)
	})
	mux.HandleFunc("GET /api/printers/{id}/camera/stream", func(w http.ResponseWriter, r *http.Request) {
		st, err := cam.Subscribe(r.Context(), r.PathValue("id"))
		if err != nil {
			cameraError(c, w, err)
			return
		}
		defer st.Stop()
		var first camera.Frame
		select {
		case f, ok := <-st.C:
			if !ok {
				err := st.Err()
				if err == nil {
					err = fmt.Errorf("%w : flux fermé", camera.ErrUnavailable)
				}
				cameraError(c, w, err)
				return
			}
			first = f
		case <-time.After(15 * time.Second):
			cameraError(c, w, fmt.Errorf("%w : aucune image reçue en 15 s", camera.ErrUnavailable))
			return
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		rc := http.NewResponseController(w)
		write := func(f camera.Frame) bool {
			_ = rc.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if _, err := fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(f.JPEG)); err != nil {
				return false
			}
			if _, err := w.Write(f.JPEG); err != nil {
				return false
			}
			if _, err := w.Write([]byte("\r\n")); err != nil {
				return false
			}
			return rc.Flush() == nil
		}
		if !write(first) {
			return
		}
		limit := time.After(2 * time.Hour) // un relais ne reste pas ouvert indéfiniment ; le navigateur se reconnecte
		for {
			select {
			case f, ok := <-st.C:
				if !ok || !write(f) {
					return
				}
			case <-limit:
				return
			case <-r.Context().Done():
				return
			}
		}
	})
	mux.HandleFunc("GET /api/camera/photos/{sha}", func(w http.ResponseWriter, r *http.Request) {
		b, err := cam.OpenPhoto(r.Context(), r.PathValue("sha"))
		if err != nil {
			cameraError(c, w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, max-age=3600")
		serveJPEG(w, b)
	})
	mux.HandleFunc("GET /api/print-jobs/{id}/photos", func(w http.ResponseWriter, r *http.Request) {
		ps, err := cam.PhotosForJob(r.Context(), r.PathValue("id"))
		if err != nil {
			cameraError(c, w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"photos": ps})
	})
	mux.HandleFunc("GET /api/print-jobs/{id}/analysis", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		al, err := cam.Alerts(r.Context(), id, 20)
		if err != nil {
			cameraError(c, w, err)
			return
		}
		out := map[string]any{"alerts": al}
		if rep, at, ok := cam.LastReport(id); ok {
			out["detector_report"], out["detector_report_at"] = rep, at
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /api/camera/settings", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, cam.Settings(r.Context()))
	})
	mux.HandleFunc("PUT /api/camera/settings", func(w http.ResponseWriter, r *http.Request) {
		var in camera.SettingsPatch
		if !decode(w, r, &in) {
			return
		}
		st, err := cam.UpdateSettings(r.Context(), in)
		if err != nil {
			cameraError(c, w, err)
			return
		}
		writeJSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("GET /api/notifications", func(w http.ResponseWriter, r *http.Request) {
		chs, err := nv.ListChannels(r.Context())
		if err != nil {
			cameraError(c, w, err)
			return
		}
		lg, _ := nv.Log(r.Context(), 10)
		writeJSON(w, http.StatusOK, map[string]any{"channels": chs, "setup_url": nv.SetupURL(), "known_events": notify.AllEvents, "recent_log": lg})
	})
	mux.HandleFunc("POST /api/notifications/test", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Channel string `json:"channel"`
		}
		if !decode(w, r, &in) {
			return
		}
		rs, err := nv.Test(r.Context(), in.Channel)
		if err != nil {
			cameraError(c, w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": rs})
	})
}

func serveJPEG(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}

func cameraError(c *core.Core, w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "internal"
	switch {
	case errors.Is(err, camera.ErrNotFound), errors.Is(err, notify.ErrNotFound), errors.Is(err, printing.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, camera.ErrInvalid), errors.Is(err, notify.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid"
	case errors.Is(err, camera.ErrUnavailable):
		status, code = http.StatusServiceUnavailable, "unavailable"
	}
	body := map[string]any{"code": code}
	if status == http.StatusInternalServerError {
		c.Log.Error("erreur API caméra", "err", err)
		body["error"] = "erreur interne"
	} else {
		body["error"] = err.Error()
	}
	writeJSON(w, status, body)
}
