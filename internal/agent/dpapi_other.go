//go:build !windows

package agent

type noProtector struct{}

// NewProtector : aucun protecteur hors Windows (l'agent est un binaire Windows).
func NewProtector() Protector { return noProtector{} }

func (noProtector) Protect([]byte, bool) ([]byte, error)   { return nil, ErrProtectUnsupported }
func (noProtector) Unprotect([]byte, bool) ([]byte, error) { return nil, ErrProtectUnsupported }
