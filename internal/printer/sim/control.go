package sim

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// controlHandle : API HTTP locale de contrôle (127.0.0.1 uniquement).
//
//	GET  /state                      état courant et pannes actives
//	POST /faults/{name}?on=true|false  active/désactive une panne
//	POST /reset                      désactive toutes les pannes
type controlHandle struct{ srv *http.Server }

func (h *controlHandle) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = h.srv.Shutdown(ctx)
}

func (s *Sim) startControl() error {
	ln, err := net.Listen("tcp", net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.ControlPort)))
	if err != nil {
		return err
	}
	s.ctlAddr = ln.Addr().String()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /state", func(w http.ResponseWriter, r *http.Request) {
		f := s.Faults()
		sort.Strings(f)
		s.mu.Lock()
		out := map[string]any{
			"model": s.cfg.Model, "serial": s.cfg.Serial, "gcode_state": s.p.gcodeState,
			"percent": s.p.percent, "layer": s.p.layer, "faults": f, "start_commands": len(s.starts),
		}
		s.mu.Unlock()
		out["mqtt_clients"] = s.ConnectedClients()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("POST /faults/{name}", func(w http.ResponseWriter, r *http.Request) {
		on := r.URL.Query().Get("on") != "false"
		if err := s.SetFault(r.PathValue("name"), on); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /reset", func(w http.ResponseWriter, r *http.Request) {
		for _, f := range s.Faults() {
			_ = s.SetFault(f, false)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	s.ctl = &controlHandle{srv: srv}
	go func() { _ = srv.Serve(ln) }()
	return nil
}
