package goals

import "context"

// GetProfileRevision relit une révision de profil de découpe (immuable) : machine,
// process, filaments, surcharges cumulées, orientation. Utilisé par internal/printing
// pour découper un modèle avec la variante appliquée.
func (s *Service) GetProfileRevision(ctx context.Context, id string) (*ProfileRev, error) {
	return s.loadProfile(ctx, id)
}
