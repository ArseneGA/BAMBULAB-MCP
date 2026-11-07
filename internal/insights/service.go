// Package insights regroupe le coût d'une impression (estimation puis coût réel), le journal des
// impressions (fiche, photo, note, défauts, ajustements proposés), le tableau de bord d'usage de la machine
// et les conseils de post-traitement. Il ne lit que le magasin (print_jobs, printers, models, print_results)
// et n'écrit que ses propres tables (insight_settings, print_logs) : il ne dépend pas de la chaîne
// d'impression et ne peut ni lancer ni arrêter une impression.
//
// Tout chiffre de coût est une ESTIMATION : les hypothèses sont listées avec chaque résultat.
package insights

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/filament"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/goals"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// ErrInvalid : entrée invalide (traduite en 400 / erreur d'outil).
var ErrInvalid = errors.New("entrée invalide")

// ErrNotFound : fiche ou impression introuvable.
var ErrNotFound = store.ErrNotFound

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w : %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// ResultRecorder est la part du moteur d'objectifs dont le journal se sert pour alimenter la mémoire
// des essais (get_print_memory). Facultatif.
type ResultRecorder interface {
	RecordResult(ctx context.Context, in goals.ResultInput) (*goals.PrintResult, error)
}

// SpoolPricer est la part du service des bobines (internal/filament) dont le coût se sert : prix/kg de la bobine
// chargée dans un emplacement et consommation réellement décomptée.
type SpoolPricer interface {
	PriceForSlot(ctx context.Context, printerID, slot string) (filament.SlotPrice, bool)
	UsageOf(ctx context.Context, printJobID string) ([]filament.Usage, error)
}

// Config câble le service.
type Config struct {
	Store *store.Store
	Blobs *store.BlobStore
	// Goals : alimente la mémoire existante quand une fiche est liée à une expérience (nil : pas de lien).
	Goals ResultRecorder
	// Spools : prix des bobines inventoriées (facultatif, voir SetSpools).
	Spools SpoolPricer
	// Now est injectable pour les tests.
	Now func() time.Time
}

// Service est le point d'entrée du paquet.
type Service struct {
	cfg     Config
	spoolMu sync.Mutex // protège cfg.Spools seul (mu est tenu pendant les calculs)
	mu      sync.Mutex // sérialise les lectures-modifications-écritures des fiches
}

// New construit le service (la base doit être migrée : tables insight_settings et print_logs).
func New(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, errors.New("insights : magasin requis")
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{cfg: cfg}, nil
}

// SetSpools branche l'inventaire des bobines (prix/kg réel et consommation décomptée) ; à appeler avant de servir.
func (s *Service) SetSpools(sp SpoolPricer) {
	s.spoolMu.Lock()
	s.cfg.Spools = sp
	s.spoolMu.Unlock()
}

func (s *Service) spools() SpoolPricer {
	s.spoolMu.Lock()
	defer s.spoolMu.Unlock()
	return s.cfg.Spools
}

// MaterialCost chiffre une masse de matière avec les réglages de prix (famille de la matière, sinon « other ») :
// c'est la fonction que internal/filament utilise pour le coût de purge (SetCostFunc).
func (s *Service) MaterialCost(ctx context.Context, material string, grams float64) (float64, bool) {
	set, err := s.GetSettings(ctx)
	if err != nil || grams < 0 {
		return 0, false
	}
	fam := MaterialFamily(material)
	k, price := familyPrice(set, fam)
	if k == "" {
		price = set.FilamentPerKg["other"]
	}
	return round2(grams / 1000 * price), true
}

func (s *Service) now() time.Time { return s.cfg.Now().UTC() }

const actor = "insights"

// ---- familles de matière ----

// MaterialFamily normalise un libellé de matière ou de preset filament vers une famille
// (PLA, PETG, ABS, ASA, TPU, PC, PA, PVA, PET, PLA-CF, PETG-CF, PA-CF) ; "" si inconnue.
func MaterialFamily(label string) string {
	norm := strings.NewReplacer("@", " ", "-", " ", "_", " ", "/", " ", "+", " ", ".", " ", ",", " ").Replace(strings.ToLower(label))
	fam := ""
	cf := false
	for _, t := range strings.Fields(norm) {
		if t == "cf" || t == "gf" {
			cf = true
		}
		if len(t) > 2 && strings.HasSuffix(t, "cf") {
			cf = true
			t = strings.TrimSuffix(t, "cf")
		}
		if fam != "" {
			continue
		}
		switch {
		case strings.HasPrefix(t, "petg"):
			fam = "PETG"
		case t == "pla":
			fam = "PLA"
		case t == "abs":
			fam = "ABS"
		case t == "asa":
			fam = "ASA"
		case t == "tpu" || t == "tpe":
			fam = "TPU"
		case t == "pc" || t == "polycarbonate":
			fam = "PC"
		case t == "pet":
			fam = "PET"
		case t == "pa" || t == "pa6" || t == "pa12" || t == "paht" || t == "nylon":
			fam = "PA"
		case t == "pva" || t == "bvoh":
			fam = "PVA"
		}
	}
	if fam == "" {
		return ""
	}
	if cf {
		switch fam {
		case "PLA", "PETG", "PA":
			return fam + "-CF"
		}
	}
	return fam
}

// baseFamily retire le suffixe -CF.
func baseFamily(f string) string { return strings.TrimSuffix(f, "-CF") }
