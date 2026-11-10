package core

import (
	"os"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/export"
)

// wireExport construit le service d'export de projet (internal/export) et sa purge périodique.
// Appelé une fois, depuis Open, après les services dont il lit les données.
func (c *Core) wireExport() error {
	// Durée de validité des liens de téléchargement (FB13) : BAMBU_EXPORT_LINK_TTL, entre 1 min et 24 h ; une valeur
	// invalide est refusée au démarrage plutôt que corrigée en silence.
	linkTTL, err := export.ParseLinkTTL(os.Getenv("BAMBU_EXPORT_LINK_TTL"))
	if err != nil {
		return err
	}
	svc, err := export.New(export.Deps{
		LinkTTL: linkTTL,
		Store:   c.Store, Blobs: c.Blobs, Queue: c.Queue, Registry: c.Registry, Catalog: c.Catalog,
		Goals: c.Goals, Insights: c.Insights, Log: c.Log, DataDir: c.Cfg.DataDir, Version: Version,
	})
	if err != nil {
		return err
	}
	c.Export = svc
	c.AddRunHook(svc.Run)
	return nil
}
