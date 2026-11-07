package filament

import (
	"context"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// loadedForPrinting donne à la chaîne d'impression les bobines réellement chargées (couleur, matière, source).
// Priorité : saisie de l'utilisateur dans l'inventaire (y compris la bobine externe déclarée, slot « ext »),
// puis lecture de l'AMS. Un emplacement sans bobine connue n'apparaît pas : sa couleur est « inconnue », jamais
// la couleur par défaut d'un profil de découpe.
func (s *Service) loadedForPrinting(ctx context.Context, printerID string) []printing.LoadedSpoolSummary {
	var out []printing.LoadedSpoolSummary
	seen := map[string]bool{}
	if spools, err := s.ListSpools(ctx, ListFilter{PrinterID: printerID}); err == nil {
		for _, sp := range spools {
			if sp.Slot == "" {
				continue
			}
			l := printing.LoadedSpoolSummary{Tray: sp.Slot, Material: sp.Material, Color: sp.ColorHex, Source: "inventaire (saisi par vous)"}
			if sp.Translucent {
				l.Material += " translucide"
			}
			if l.Color == "" && sp.Observed != nil {
				l.Color, l.Source = sp.Observed.Color, "lecture de l'AMS"
			}
			out = append(out, l)
			seen[sp.Slot] = true
		}
	}
	if ams, _, err := s.d.Printing.PrinterAMS(ctx, printerID); err == nil && ams != nil {
		for _, tr := range ams.Trays() {
			if seen[tr.Slot] || tr.Empty || tr.Color == "" {
				continue
			}
			out = append(out, printing.LoadedSpoolSummary{Tray: tr.Slot, Material: tr.Type, Color: tr.Color, Source: "lecture de l'AMS"})
		}
	}
	return out
}
