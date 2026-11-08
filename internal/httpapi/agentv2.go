package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentv2"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// v2Auth branche l'authentification et l'appairage du registre existant (agentlink, protocole v1) sur le protocole
// /agent/v2 : même jeton d'appairage à usage unique, même secret d'agent « <agent_id>.<secret> » (comparé en temps
// constant, agent révocable).
type v2Auth struct{ reg *agentlink.Registry }

func v2err(err error) error {
	var ae *agentlink.Error
	if errors.As(err, &ae) {
		return &agentv2.Error{Status: ae.Status, Code: ae.Code, Message: ae.Message}
	}
	return err
}

func (a v2Auth) Authenticate(ctx context.Context, bearer string) (string, error) {
	ag, err := a.reg.Authenticate(ctx, bearer)
	if err != nil {
		return "", v2err(err)
	}
	return ag.ID, nil
}

func (a v2Auth) Pair(ctx context.Context, req agentv2.PairRequest) (string, string, error) {
	resp, err := a.reg.Pair(ctx, agentlink.PairRequest{Token: req.Token, Name: req.Name, Host: req.Host, AgentVersion: req.AgentVersion,
		Protocol: agentlink.ProtocolVersion}) // l'appairage est celui de v1 : même registre, mêmes jetons
	if err != nil {
		return "", "", v2err(err)
	}
	return resp.AgentID, resp.Secret, nil
}

// registerAgentV2 monte le protocole /agent/v2/* (authentifié par secret d'agent, comme v1) et le réglage du
// transport d'une imprimante.
//
//	/agent/v2/*                       protocole de l'agent local (appairage, long-poll d'ordres, télémétrie, fichiers)
//	PUT /api/printers/{id}/link       {"link":"direct"|"agent","agent_id":"agt_…"} : transport de l'imprimante
//	GET /api/agent-v2/server-key      clé publique de signature (identifiant court) à comparer à celle affichée par l'agent
func registerAgentV2(mux *http.ServeMux, c *core.Core, reg *agentlink.Registry) {
	if c.AgentV2 == nil {
		return
	}
	mux.Handle(agentv2.BasePath+"/", c.AgentV2.Handler(v2Auth{reg}))
	mux.HandleFunc("GET /api/agent-v2/server-key", func(w http.ResponseWriter, r *http.Request) {
		key, id, err := c.AgentV2.ServerKey()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "clé de signature indisponible"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"public_key": key, "key_id": id, "algorithm": "ed25519"})
	})
	if c.Printing == nil {
		return
	}
	mux.HandleFunc("PUT /api/printers/{id}/link", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Link    string `json:"link"`
			AgentID string `json:"agent_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Link == string(printer.LinkAgent) {
			ag, err := reg.GetAgent(r.Context(), in.AgentID)
			if err != nil || ag.Revoked {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent inconnu ou révoqué (voir GET /api/agents)"})
				return
			}
		}
		v, err := c.Printing.SetPrinterLink(r.Context(), r.PathValue("id"), in.Link, in.AgentID)
		printingReply(c, w, v, err, http.StatusOK)
	})
}
