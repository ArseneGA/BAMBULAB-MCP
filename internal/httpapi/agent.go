package httpapi

import (
	"encoding/json"
	"net"
	"net/http"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
)

// registerAgent monte le protocole agent (/agent/v1/*, authentifié par secret
// d'agent) et l'administration des agents (/api/agents*, /api/agent/tasks*).
//
//	POST   /api/agents/pairing               jeton d'appairage à usage unique (requête locale uniquement)
//	GET    /api/agents                       agents, disponibilité, dernière vue
//	DELETE /api/agents/{id}                  révocation
//	GET    /api/agents/{id}/inventory        inventaire Windows (état windows_only / copié)
//	POST   /api/agents/{id}/inventory/select demande de copie contrôlée (crée un travail)
//	POST   /api/agent/tasks                  confie un travail à l'agent (executor windows-agent)
//	GET    /api/agent/tasks[/{id}]           suivi ; POST .../cancel ; POST .../requeue
func registerAgent(mux *http.ServeMux, c *core.Core) {
	reg, err := agentlink.For(c)
	if err != nil {
		c.Log.Error("registre des agents indisponible", "err", err)
		return
	}
	mux.Handle(agentlink.BasePath+"/", reg.Handler())

	mux.HandleFunc("POST /api/agents/pairing", func(w http.ResponseWriter, r *http.Request) {
		// Le jeton donne accès au service : seule une requête locale peut en créer un.
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "l'appairage ne peut être initié que depuis la machine du service"})
			return
		}
		tok, exp, err := reg.NewPairing(r.Context())
		if err != nil {
			agentlink.WriteError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"pairing_token": tok, "expires_at": exp,
			"instructions": "sur le poste Windows : bambu-agent pair --server http://<hôte>:<port> --token <pairing_token> ; usage unique"})
	})
	mux.HandleFunc("GET /api/agents", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"agents": reg.Status(r.Context())})
	})
	mux.HandleFunc("DELETE /api/agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		agentlink.Reply(w, map[string]string{"status": "revoked"}, reg.Revoke(r.Context(), r.PathValue("id")), http.StatusOK)
	})
	mux.HandleFunc("GET /api/agents/{id}/inventory", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		items, sum, err := reg.ListInventory(r.Context(), r.PathValue("id"), q.Get("root"), q.Get("state"), atoi(q.Get("limit")), atoi(q.Get("offset")))
		agentlink.Reply(w, map[string]any{"summary": sum, "items": items}, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/agents/{id}/inventory/select", func(w http.ResponseWriter, r *http.Request) {
		var in agentlink.SelectInput
		if !decodeBody(w, r, &in) {
			return
		}
		res, err := reg.SelectForUpload(r.Context(), r.PathValue("id"), in)
		agentlink.Reply(w, res, err, http.StatusAccepted)
	})
	mux.HandleFunc("POST /api/agent/tasks", func(w http.ResponseWriter, r *http.Request) {
		var in agentlink.CreateTaskInput
		if !decodeBody(w, r, &in) {
			return
		}
		t, err := reg.CreateTask(r.Context(), in)
		agentlink.Reply(w, t, err, http.StatusAccepted)
	})
	mux.HandleFunc("GET /api/agent/tasks", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		ts, err := reg.ListTasks(r.Context(), q.Get("agent"), q.Get("state"), atoi(q.Get("limit")))
		agentlink.Reply(w, map[string]any{"tasks": ts}, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/agent/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		t, err := reg.GetTask(r.Context(), r.PathValue("id"))
		agentlink.Reply(w, t, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/agent/tasks/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		t, err := reg.CancelTask(r.Context(), r.PathValue("id"))
		agentlink.Reply(w, t, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/agent/tasks/{id}/requeue", func(w http.ResponseWriter, r *http.Request) {
		t, err := reg.Requeue(r.Context(), r.PathValue("id"))
		agentlink.Reply(w, t, err, http.StatusAccepted)
	})
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "corps JSON invalide : " + err.Error()})
		return false
	}
	return true
}
