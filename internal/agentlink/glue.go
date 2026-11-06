package agentlink

import (
	"sync"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
)

var (
	forMu sync.Mutex
	forBy = map[*core.Core]*Registry{}
)

// For renvoie le registre d'agents du cœur (créé au premier appel) et branche sa
// disponibilité sur Core.Capabilities() (get_capabilities, /api/capabilities).
// À appeler avant que le service ne serve des requêtes (httpapi.NewHandler le fait).
func For(c *core.Core) (*Registry, error) {
	forMu.Lock()
	defer forMu.Unlock()
	if r, ok := forBy[c]; ok {
		return r, nil
	}
	r, err := New(Deps{Store: c.Store, Blobs: c.Blobs, Catalog: c.Catalog, Log: c.Log, DataDir: c.Cfg.DataDir}, Config{})
	if err != nil {
		return nil, err
	}
	forBy[c] = r
	c.CapabilityHooks = append(c.CapabilityHooks, r.ApplyCapabilities)
	return r, nil
}
