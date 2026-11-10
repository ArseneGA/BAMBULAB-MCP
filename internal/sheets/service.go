package sheets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/camera"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/projects"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// ErrInvalid : entrée refusée (HTTP 400).
var ErrInvalid = errors.New("entrée invalide")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w : %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Sections servies par la fiche pièce (paramètre include).
var Sections = []string{"versions", "profiles", "prints", "strength", "projects"}

// Camera : ce que les fiches lisent de la caméra (camera.Service). Lecture seule.
type Camera interface {
	ProgressPhotos(ctx context.Context, jobID string) ([]camera.ProgressPhoto, error)
	Alerts(ctx context.Context, jobID string, limit int) ([]camera.Alert, error)
	Settings(ctx context.Context) camera.Settings
}

// Config du service. Camera est facultatif (nil : « caméra non disponible »).
type Config struct {
	Store    *store.Store
	Insights *insights.Service
	Projects *projects.Service // facultatif
	Camera   Camera            // facultatif
}

// Service assemble les fiches.
type Service struct{ cfg Config }

// New crée le service.
func New(cfg Config) (*Service, error) {
	if cfg.Store == nil || cfg.Insights == nil {
		return nil, errors.New("sheets : store et insights requis")
	}
	return &Service{cfg: cfg}, nil
}

// PartSheetInput : la demande de fiche pièce.
type PartSheetInput struct {
	ModelID string   `json:"model_id" jsonschema:"identifiant de la pièce (mdl_...)"`
	Include []string `json:"include,omitempty" jsonschema:"sections à servir : versions, profiles, prints, strength, projects (défaut : toutes)"`
	Limit   int      `json:"limit,omitempty" jsonschema:"nombre maximal d'impressions listées (défaut 20, max 100) ; les compteurs portent sur toutes"`
}

// PrintSheetInput : la demande de fiche d'impression.
type PrintSheetInput struct {
	PrintJobID string `json:"print_job_id" jsonschema:"identifiant de l'impression (ou de sa fiche de journal)"`
}

func cleanInclude(in []string) ([]string, error) {
	if len(in) == 0 {
		return append([]string(nil), Sections...), nil
	}
	seen := map[string]bool{}
	var out []string
	for _, raw := range in {
		for _, s := range strings.Split(raw, ",") {
			s = strings.ToLower(strings.TrimSpace(s))
			if s == "" || seen[s] {
				continue
			}
			ok := false
			for _, k := range Sections {
				ok = ok || k == s
			}
			if !ok {
				return nil, invalid("section %q inconnue (%s)", s, strings.Join(Sections, ", "))
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return append([]string(nil), Sections...), nil
	}
	return out, nil
}
