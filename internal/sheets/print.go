package sheets

import "context"

// PrintSheet assemble la fiche d'impression (types.go). Implémentation à venir : le contrat est figé.
func (s *Service) PrintSheet(ctx context.Context, in PrintSheetInput) (*PrintSheet, error) {
	return nil, invalid("fiche d'impression : non implémentée")
}
