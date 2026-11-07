package core

import (
	"github.com/ArseneGA/BAMBULAB-MCP/internal/export"
)

// wireExport construit le service d'export de projet (internal/export) et sa purge périodique.
// Appelé une fois, depuis Open, après les services dont il lit les données.
func (c *Core) wireExport() error {
	svc, err := export.New(export.Deps{
		Store: c.Store, Blobs: c.Blobs, Queue: c.Queue, Registry: c.Registry, Catalog: c.Catalog,
		Goals: c.Goals, Insights: c.Insights, Log: c.Log, DataDir: c.Cfg.DataDir, Version: Version,
	})
	if err != nil {
		return err
	}
	c.Export = svc
	c.AddRunHook(svc.Run)
	return nil
}
