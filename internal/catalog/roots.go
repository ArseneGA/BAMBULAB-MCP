package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// canonical résout un chemin absolu en chemin canonique (liens symboliques résolus).
func canonical(p string) (string, error) {
	if strings.ContainsRune(p, 0) {
		return "", invalidf("chemin invalide")
	}
	if !filepath.IsAbs(p) {
		return "", invalidf("chemin absolu requis : %q", p)
	}
	return filepath.EvalSymlinks(filepath.Clean(p))
}

// within indique si p est root ou se trouve sous root (chemins canoniques).
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// AddRoot ajoute une racine autorisée (le dossier doit exister).
func (s *Service) AddRoot(p string) error {
	c, err := canonical(p)
	if err != nil {
		return err
	}
	st, err := os.Stat(c)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return invalidf("%q n'est pas un dossier", p)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.roots {
		if r == c {
			return nil
		}
	}
	s.roots = append(s.roots, c)
	return nil
}

// Roots renvoie les racines autorisées (canoniques).
func (s *Service) Roots() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.roots...)
}

// authorize valide un chemin demandé : absolu, existant, et sous une racine
// autorisée APRÈS résolution des liens symboliques (un lien qui s'échappe de la
// racine est refusé). Renvoie le chemin canonique et la racine retenue.
func (s *Service) authorize(p string) (path, root string, err error) {
	roots := s.Roots()
	if len(roots) == 0 {
		return "", "", fmt.Errorf("%w : aucune racine d'import configurée (BAMBU_IMPORT_ROOTS ou --import-root)", ErrNotAllowed)
	}
	c, err := canonical(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("%w : %q", ErrNotFound, p)
		}
		return "", "", err
	}
	for _, r := range roots {
		if within(r, c) {
			return c, r, nil
		}
	}
	return "", "", fmt.Errorf("%w : %q est hors des racines autorisées", ErrNotAllowed, p)
}
