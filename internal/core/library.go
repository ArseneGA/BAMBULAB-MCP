package core

import (
	"path/filepath"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/calibration"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/library"
)

// wireLibrary construit la bibliothèque de gabarits et le service de calibration (chantier G).
// La bibliothèque lit les calibrations pour appliquer jeux et compensations mesurés.
func (c *Core) wireLibrary() error {
	cd := calibration.Deps{Store: c.Store, Blobs: c.Blobs, Catalog: c.Catalog, WorkRoot: filepath.Join(c.Cfg.DataDir, "work", "calibration")}
	if c.Goals != nil {
		cd.Profiles = c.Goals
		cd.Ranges = c.Goals // plage de température lue dans le profil filament de base
	}
	cal, err := calibration.New(cd)
	if err != nil {
		return err
	}
	lib, err := library.New(library.Deps{Store: c.Store, Blobs: c.Blobs, Catalog: c.Catalog, Calibrations: cal,
		WorkRoot: filepath.Join(c.Cfg.DataDir, "work", "library")})
	if err != nil {
		return err
	}
	c.Calibration, c.Library = cal, lib
	return nil
}
