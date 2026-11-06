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

// checkRootAgainstDataDir refuse une racine d'import configurée qui contient le dossier de données, ou qui
// s'y trouve : importer puis télécharger (GET /api/files/{id}/download) exposerait la base, les blobs et
// secrets/master.key. Les chemins sont comparés après résolution des liens symboliques.
func checkRootAgainstDataDir(root, dataDir string) error {
	if dataDir == "" {
		return nil
	}
	c, err := canonical(root)
	if err != nil {
		return nil // inexistante ou relative : AddRoot s'en plaint
	}
	d, err := filepath.Abs(dataDir)
	if err != nil {
		return nil
	}
	if r, err := filepath.EvalSymlinks(d); err == nil {
		d = r
	}
	if within(c, d) {
		return fmt.Errorf("%w : contient le dossier de données %q (base, blobs, secrets) ; choisir un dossier de fichiers 3D distinct", ErrNotAllowed, d)
	}
	if within(d, c) {
		return fmt.Errorf("%w : se trouve dans le dossier de données %q (base, blobs, secrets) ; choisir un dossier distinct", ErrNotAllowed, d)
	}
	return nil
}

// WorkerBackend renvoie l'exécutable uv et le projet du worker géométrie réellement retenus (ErrBackendMissing
// sinon) : ce que le service utilise, pas une hypothèse.
func (s *Service) WorkerBackend() (uv, dir string, err error) { return s.stepBackend() }

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
