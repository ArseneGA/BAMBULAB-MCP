package printing

import (
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

const unknownColor = "inconnue"

// FilamentColor : couleur réelle d'un filament de l'impression.
type FilamentColor struct {
	Slot     int    `json:"filament_slot"`
	Tray     string `json:"tray,omitempty"`
	Color    string `json:"color"`  // #RRGGBB, ou « inconnue »
	Source   string `json:"source"` // inventaire / AMS / « non déclarée : déclarer la bobine avec update_spool »
	Material string `json:"material,omitempty"`
}

// clearProfileColors retire des plaques la couleur par défaut du preset (ex. #00AE42, vert Bambu), que le
// G-code et slice_info portent quelle que soit la bobine : ce n'est pas la couleur réelle de la bobine.
func clearProfileColors(sum *ArtifactSummary) {
	if sum == nil {
		return
	}
	for pi := range sum.Plates {
		fs := append([]slicer.FilamentUse(nil), sum.Plates[pi].Filaments...)
		for fi := range fs {
			fs[fi].Color, fs[fi].ColorSource = "", unknownColor+" (la couleur du profil de découpe n'est pas celle de la bobine)"
		}
		sum.Plates[pi].Filaments = fs
	}
}

func trayOf(as []FilamentAssignment, slot int) string {
	for _, a := range as {
		if a.FilamentSlot == slot {
			return a.Tray
		}
	}
	return ""
}

// applyRealColors renseigne la couleur de chaque filament de la plaque imprimée d'après la bobine chargée à
// l'emplacement affecté (jamais d'après le profil). Sans bobine connue : « inconnue ».
func applyRealColors(j *Job, loaded []LoadedSpoolSummary) {
	if j.Summary == nil {
		return
	}
	for pi := range j.Summary.Plates {
		fs := append([]slicer.FilamentUse(nil), j.Summary.Plates[pi].Filaments...)
		for fi := range fs {
			tray := trayOf(j.Assignments, fs[fi].Slot)
			if l, ok := trayColor(loaded, tray); ok {
				fs[fi].Color, fs[fi].ColorSource = strings.ToUpper(l.Color), l.Source
			} else {
				fs[fi].Color, fs[fi].ColorSource = "", unknownColor+" (bobine non déclarée à cet emplacement)"
			}
		}
		j.Summary.Plates[pi].Filaments = fs
	}
}

// colorsFor : couleurs réelles à montrer à l'utilisateur pour la plaque de l'impression.
func (s *Service) colorsFor(j Job) []FilamentColor {
	var out []FilamentColor
	if j.Summary == nil {
		return nil
	}
	loaded := s.loadedSpools(j.PrinterID)
	for _, p := range j.Summary.Plates {
		if p.Index != j.Plate && !(j.Plate == 0 && p.Index == 1) {
			continue
		}
		for _, f := range p.Filaments {
			tray := trayOf(j.Assignments, f.Slot)
			fc := FilamentColor{Slot: f.Slot, Tray: tray, Color: unknownColor, Material: f.Type,
				Source: "non déclarée : déclarer la bobine (update_spool, slot « ext » pour la bobine externe) ou charger l'AMS"}
			if l, ok := trayColor(loaded, tray); ok {
				fc.Color, fc.Source = strings.ToUpper(l.Color), l.Source
				if l.Material != "" {
					fc.Material = l.Material
				}
			}
			out = append(out, fc)
		}
	}
	return out
}
