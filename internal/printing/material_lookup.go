package printing

import (
	"context"
	"errors"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// lookupMaterial retrouve la matière d'une affectation. Ordre :
//  1. le catalogue de matières du service (identifiant exact, puis nom normalisé : suffixe « @BBL … » retiré,
//     casse et séparateurs ignorés) ;
//  2. un préréglage filament OFFICIEL de Bambu Studio (nom exact « … @BBL X1C », ligne de produit sans suffixe, ou
//     nom libre résolu pour la machine) : un nom officiel n'est jamais « absent du catalogue ».
//
// known est faux seulement si rien ne correspond ; err n'est non nul que pour une vraie erreur de lecture.
func (s *Service) lookupMaterial(ctx context.Context, id, printerModel string, nozzle float64) (mat contracts.Material, known bool, err error) {
	id = strings.TrimSpace(id)
	try := func(key string) (bool, error) {
		if key == "" {
			return false, nil
		}
		if gerr := s.d.Store.GetDoc(ctx, "materials", key, &mat); gerr != nil {
			if errors.Is(gerr, store.ErrNotFound) {
				return false, nil
			}
			return false, gerr
		}
		return true, nil
	}
	base := strings.TrimSpace(profiles.PresetBase(id))
	for _, k := range []string{id, base, strings.ToLower(base), strings.ReplaceAll(strings.ToLower(base), " ", "-"), strings.ReplaceAll(strings.ToLower(base), " ", "_")} {
		ok, gerr := try(k)
		if gerr != nil {
			return mat, false, gerr
		}
		if ok {
			return mat, true, nil
		}
	}
	// préréglage officiel de Bambu Studio
	if ps, machine, merr := s.machinePreset(printerModel, nozzle); merr == nil {
		if r, rerr := ps.ResolveFilament(machine, id); rerr == nil {
			return contracts.Material{ID: id, Family: r.Entry.Type, Compatibility: contracts.CompatUnknown}, true, nil
		}
	}
	return contracts.Material{}, false, nil
}
