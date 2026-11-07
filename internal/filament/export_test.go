package filament

import (
	"context"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// SetRFIDForTest rattache une étiquette RFID à une bobine (aucune API publique : l'identifiant vient de l'AMS).
func (s *Service) SetRFIDForTest(id, uuid string) error {
	_, err := s.mutate(context.Background(), id, func(sp *Spool) bool { sp.RFIDUUID = uuid; return true })
	return err
}

// printingJob : impression minimale pour les tests unitaires.
func printingJob(plates []slicer.PlateResult) printing.Job {
	j := printing.Job{Plate: 1}
	if plates != nil {
		j.Summary = &printing.ArtifactSummary{Plates: plates}
	}
	return j
}
