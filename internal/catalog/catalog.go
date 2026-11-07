// Package catalog est le catalogue documentaire du service (dossier §14.2 et
// §15.4) : import non destructif de fichiers 3D, scan en lecture seule,
// collections, modèles logiques, fichiers sources, versions immuables,
// doublons exacts, opérations avec aperçu, corbeille, produits dérivés
// (analyse, aperçu GLB, sorties STEP).
//
// Principes : les originaux ne sont jamais déplacés ni modifiés ; les fichiers
// sont copiés dans le magasin de blobs adressé par contenu ; un produit dérivé
// (GLB, STL issu de STEP) ne remplace jamais un original ; toute mutation écrit
// son entrée d'audit dans la même transaction.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Erreurs sentinelles (utiliser errors.Is).
var (
	// ErrNotAllowed : chemin hors des racines autorisées.
	ErrNotAllowed = errors.New("chemin non autorisé")
	// ErrInvalid : entrée invalide.
	ErrInvalid = errors.New("entrée invalide")
	// ErrConflict : l'état a changé depuis l'aperçu, ou jeton déjà utilisé / expiré.
	ErrConflict = errors.New("conflit")
	// ErrNotFound : entité inconnue.
	ErrNotFound = store.ErrNotFound
	// ErrBackendMissing : le backend CAO (uv + worker géométrie) est absent.
	ErrBackendMissing = errors.New("backend CAO absent")
)

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w : %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Noms des travaux de la file.
const (
	JobImport     = "catalog.import"
	JobAnalyze    = "mesh.analyze"
	JobPreview    = "mesh.preview"
	JobStepImport = "cad.step_import"
)

// Genres de produits dérivés.
const (
	KindAnalysis = "analysis"
	KindPreview  = "preview_glb"
	KindStepSTL  = "step_stl"
	KindStep3MF  = "step_3mf"
)

// États d'un produit dérivé.
const (
	StateOK             = "ok"
	StatePending        = "pending"
	StateError          = "error"
	StateBackendMissing = "backend_missing"
)

// Config du catalogue.
type Config struct {
	// ImportRoots : racines autorisées pour l'import et le scan. Vide = tout import est refusé.
	ImportRoots []string
	// UVPath : exécutable uv (vide = recherche dans le PATH).
	UVPath string
	// WorkerDir : dossier du projet Python du worker géométrie (contient pyproject.toml).
	// Vide = ./workers/geometry puis à côté de l'exécutable.
	WorkerDir string
	// StepTimeout : durée maximale d'un import STEP (défaut 10 min).
	StepTimeout time.Duration
	// Worker remplace l'exécution du worker Python (tests).
	Worker WorkerFunc
	// BaseURL : base des URL signées renvoyées aux clients (défaut http://127.0.0.1:7878).
	BaseURL string
	// AssetTTL : validité des URL signées (défaut 1 h).
	AssetTTL time.Duration
	// PreviewMaxTriangles : au-delà, l'aperçu GLB est décimé (défaut 200 000).
	PreviewMaxTriangles int
	// MaxFileBytes : taille maximale d'un fichier importable (défaut 2 Gio).
	MaxFileBytes int64
	// PreviewTTL : validité d'un aperçu d'opérations (défaut 15 min).
	PreviewTTL time.Duration
}

func (c *Config) defaults() {
	if c.StepTimeout <= 0 {
		c.StepTimeout = 10 * time.Minute
	}
	if c.BaseURL == "" {
		c.BaseURL = "http://127.0.0.1:7878"
	}
	if c.AssetTTL <= 0 {
		c.AssetTTL = time.Hour
	}
	if c.PreviewMaxTriangles <= 0 {
		c.PreviewMaxTriangles = 200_000
	}
	if c.MaxFileBytes <= 0 {
		c.MaxFileBytes = 2 << 30
	}
	if c.PreviewTTL <= 0 {
		c.PreviewTTL = 15 * time.Minute
	}
}

// Deps : ce que le catalogue reçoit du cœur.
type Deps struct {
	Store    *store.Store
	Blobs    *store.BlobStore
	Queue    *jobs.Queue
	Registry *jobs.Registry
	Log      *slog.Logger
	DataDir  string
}

// Service est le catalogue.
type Service struct {
	d   Deps
	cfg Config

	mu      sync.RWMutex
	roots   []string // canoniques
	baseURL string
	key     []byte                 // clé HMAC des URL signées
	cad     cadHealth              // vérification réelle du backend CAO (ping)
	profFn  func() *profiles.Store // profils Bambu Studio (cibles machine, seuil de surplomb) ; nil = repli nominal

	// testHook est appelé après chaque fichier d'un import (tests d'interruption).
	testHook func(done int)
}

// New crée le catalogue et enregistre ses travaux (rejouables, idempotents).
func New(d Deps, cfg Config) (*Service, error) {
	cfg.defaults()
	if d.Log == nil {
		d.Log = slog.Default()
	}
	s := &Service{d: d, cfg: cfg, baseURL: strings.TrimRight(cfg.BaseURL, "/")}
	for _, r := range cfg.ImportRoots {
		if err := checkRootAgainstDataDir(r, d.DataDir); err != nil {
			return nil, fmt.Errorf("racine d'import %q : %w", r, err)
		}
		if err := s.AddRoot(r); err != nil {
			return nil, fmt.Errorf("racine d'import %q : %w", r, err)
		}
	}
	key, err := loadOrCreateKey(filepath.Join(d.DataDir, "assets.key"))
	if err != nil {
		return nil, err
	}
	s.key = key
	if d.Registry != nil {
		d.Registry.Register(JobImport, true, s.runImportJob)
		d.Registry.Register(JobAnalyze, true, s.runAnalyzeJob)
		d.Registry.Register(JobPreview, true, s.runPreviewJob)
		d.Registry.Register(JobStepImport, true, s.runStepJob)
	}
	return s, nil
}

// SetBaseURL fixe la base des URL signées (adresse d'écoute réelle de bambud).
func (s *Service) SetBaseURL(u string) {
	s.mu.Lock()
	s.baseURL = strings.TrimRight(u, "/")
	s.mu.Unlock()
}

// BaseURL renvoie la base des URL signées.
func (s *Service) BaseURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.baseURL
}

// WaitJob attend la fin d'un travail (ou timeout) et renvoie son état courant.
func (s *Service) WaitJob(ctx context.Context, id string, timeout time.Duration) (state string, done bool, err error) {
	deadline := time.Now().Add(timeout)
	for {
		j, err := s.d.Queue.Get(ctx, id)
		if err != nil {
			return "", false, err
		}
		if j.State.Terminal() {
			return string(j.State), true, nil
		}
		if time.Now().After(deadline) {
			return string(j.State), false, nil
		}
		select {
		case <-ctx.Done():
			return string(j.State), false, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// Idle attend qu'aucun travail ne soit en file ou en cours (démo, tests).
func (s *Service) Idle(ctx context.Context, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		n := 0
		for _, st := range []contracts.JobState{contracts.JobQueued, contracts.JobRunning} {
			js, err := s.d.Queue.List(ctx, st, 1)
			if err == nil {
				n += len(js)
			}
		}
		if n == 0 {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
}
