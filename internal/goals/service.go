package goals

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Erreurs nommées.
var (
	// ErrStale : la révision attendue n'est plus la révision courante.
	ErrStale = errors.New("révision périmée")
	// ErrIncompatible : base, matériau ou machine incompatibles.
	ErrIncompatible = errors.New("incompatible")
	// ErrNotReady : l'état de l'expérience ne permet pas l'opération.
	ErrNotReady = errors.New("pas prêt")
	// ErrUnavailable : composant nécessaire absent (Bambu Studio, profils).
	ErrUnavailable = errors.New("indisponible")
)

// StaleError précise la révision courante.
type StaleError struct {
	What     string
	Expected string
	Current  string
}

func (e *StaleError) Error() string {
	return fmt.Sprintf("%v : %s attendue %q, courante %q — relire l'état et recommencer", ErrStale, e.What, e.Expected, e.Current)
}
func (e *StaleError) Unwrap() error { return ErrStale }

// JobKind est le type de travail de découpe d'un candidat.
const JobKind = "goals.slice_candidate"

const actor = "goals"

// Config du service.
type Config struct {
	Store    *store.Store
	Blobs    *store.BlobStore
	Queue    *jobs.Queue
	Registry *jobs.Registry // si fourni, le handler de découpe y est enregistré
	Slicer   *slicer.Slicer // nil : génération possible, découpe indisponible
	// SlicerFactory : construction paresseuse du slicer quand Slicer est nil
	// (profils chargés à la première utilisation). Une erreur laisse les outils
	// disponibles ; la découpe échoue alors avec le code slicer_missing.
	SlicerFactory func() (*slicer.Slicer, error)
	Profiles      *profiles.Store // défaut : profils du slicer
	Rules         *Rules          // défaut : règles embarquées

	DefaultCandidates int           // défaut 3
	MaxCandidates     int           // plafond configurable, défaut 6
	MaxFactors        int           // facteurs modifiés par variante, défaut 3
	WorkRoot          string        // dossier de travail des découpes
	SliceTimeout      time.Duration // défaut : celui du slicer
}

// Service est le moteur d'objectifs et d'expériences.
type Service struct {
	cfg   Config
	rules *Rules
	prof  *profiles.Store
	sl    *slicer.Slicer
	slErr error
	once  sync.Once
}

// load résout (une fois) le slicer et les profils.
func (s *Service) load() {
	s.once.Do(func() {
		s.sl = s.cfg.Slicer
		if s.sl == nil && s.cfg.SlicerFactory != nil {
			s.sl, s.slErr = s.cfg.SlicerFactory()
		}
		s.prof = s.cfg.Profiles
		if s.prof == nil && s.sl != nil {
			s.prof = s.sl.Profiles()
		}
	})
}

// New construit le service et enregistre le handler de découpe.
func New(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, errors.New("goals: store requis")
	}
	if cfg.DefaultCandidates <= 0 {
		cfg.DefaultCandidates = 3
	}
	if cfg.MaxCandidates <= 0 {
		cfg.MaxCandidates = 6
	}
	if cfg.MaxFactors <= 0 {
		cfg.MaxFactors = 3
	}
	if cfg.DefaultCandidates > cfg.MaxCandidates {
		cfg.DefaultCandidates = cfg.MaxCandidates
	}
	if cfg.WorkRoot == "" {
		cfg.WorkRoot = filepath.Join(os.TempDir(), "bambu-goals")
	}
	rules := cfg.Rules
	if rules == nil {
		var err error
		if rules, err = LoadRules(); err != nil {
			return nil, err
		}
	}
	s := &Service{cfg: cfg, rules: rules}
	if cfg.Registry != nil {
		cfg.Registry.Register(JobKind, true, s.sliceHandler)
	}
	return s, nil
}

// Rules expose les règles chargées.
func (s *Service) Rules() *Rules { return s.rules }

func (s *Service) needProfiles() error {
	s.load()
	if s.prof == nil {
		cause := ""
		if s.slErr != nil {
			cause = " (" + s.slErr.Error() + ")"
		}
		return fmt.Errorf("%w: slicer_missing : profils Bambu Studio non chargés, installer Bambu Studio ou configurer son dossier%s", ErrUnavailable, cause)
	}
	return nil
}

func (s *Service) now() time.Time { return s.cfg.Store.Now() }

func shaHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ---- objectifs ----

// CriteriaCatalog est la sortie de list_goal_criteria.
type CriteriaCatalog struct {
	RulesVersion string          `json:"rules_version"`
	Note         string          `json:"note"`
	Families     []string        `json:"material_families"`
	FamilyNote   string          `json:"family_note"`
	Criteria     []CriterionInfo `json:"criteria"`
	Usage        string          `json:"usage"`
}

// ListCriteria renvoie le catalogue pour guider l'extraction des critères.
func (s *Service) ListCriteria() CriteriaCatalog {
	return CriteriaCatalog{
		RulesVersion: s.rules.Version, Note: s.rules.Note, Families: s.rules.Families, FamilyNote: s.rules.FamilyNote,
		Criteria: s.rules.Catalog(),
		Usage: "Extraire du texte de l'utilisateur un critère par exigence (type du catalogue, priorité 1-5, non_negotiable, seuil, zone, params), " +
			"garder la formulation d'origine dans text, puis appeler set_manufacturing_goals. Un type absent du catalogue est accepté comme custom, non évaluable.",
	}
}

// SetGoals valide et enregistre un objectif structuré.
func (s *Service) SetGoals(ctx context.Context, in GoalInput) (*Goal, error) {
	g, err := BuildGoal(s.rules, in)
	if err != nil {
		return nil, err
	}
	if in.ModelID != "" {
		if _, err := s.cfg.Store.GetModel(ctx, in.ModelID); err != nil {
			return nil, fmt.Errorf("modèle %q : %w", in.ModelID, err)
		}
	}
	g.ID = contracts.NewID("goal")
	g.CreatedAt = s.now()
	doc, _ := json.Marshal(g)
	err = s.cfg.Store.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO goal_sets(id,model_id,original_text,doc,created_at) VALUES(?,?,?,?,?)`,
			g.ID, nullIfEmpty(in.ModelID), g.OriginalText, string(doc), store.FormatTime(g.CreatedAt)); err != nil {
			return err
		}
		return s.cfg.Store.AuditTx(ctx, tx, actor, "goal.set", "goal", g.ID, map[string]any{"criteria": len(g.Criteria)})
	})
	if err != nil {
		return nil, err
	}
	return g, nil
}

// GetGoal relit un objectif.
func (s *Service) GetGoal(ctx context.Context, id string) (*Goal, error) {
	var doc string
	err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT doc FROM goal_sets WHERE id=?`, id).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("objectif %q : %w", id, store.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var g Goal
	if err := json.Unmarshal([]byte(doc), &g); err != nil {
		return nil, err
	}
	return &g, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- base figée ----

// BaseSpec désigne la machine et les profils de départ.
type BaseSpec struct {
	Machine   string   `json:"machine,omitempty" jsonschema:"nom exact du preset machine, ex. Bambu Lab P1S 0.4 nozzle"`
	Printer   string   `json:"printer,omitempty" jsonschema:"modèle d'imprimante, ex. Bambu Lab P1S (avec nozzle)"`
	Nozzle    float64  `json:"nozzle,omitempty" jsonschema:"diamètre de buse en mm, défaut 0,4"`
	Process   string   `json:"process,omitempty" jsonschema:"preset process ; défaut : celui de la machine"`
	Filaments []string `json:"filaments,omitempty" jsonschema:"presets filament, un par emplacement ; défaut : selon le matériau de l'objectif ou celui de la machine"`
}

// BaseDoc est la base résolue et figée (noms + empreintes des profils).
type BaseDoc struct {
	Machine       string            `json:"machine"`
	Process       string            `json:"process"`
	Filaments     []string          `json:"filaments"`
	Family        string            `json:"material_family,omitempty"`
	Nozzle        float64           `json:"nozzle_mm"`
	ProfilesStore string            `json:"profiles_version,omitempty"`
	Digests       map[string]string `json:"digests" jsonschema:"empreinte sha256 du JSON complet de chaque profil résolu (hors surcharges)"`
	Overrides     []slicer.Override `json:"base_overrides,omitempty" jsonschema:"surcharges communes à tous les candidats (révision de profil de base)"`
}

type resolvedBase struct {
	doc      BaseDoc
	machine  *profiles.Resolved
	process  *profiles.Resolved // avec surcharges de base
	filament *profiles.Resolved // premier filament, avec surcharges de base
}

func digestOf(r *profiles.Resolved) string {
	b, _ := r.JSON()
	return shaHex(b)
}

func familyOf(f *profiles.Resolved) string {
	t, _ := f.String("filament_type")
	return strings.ToUpper(strings.TrimSpace(t))
}

// resolveBase résout la base. fam (famille visée par l'objectif) est vérifiée
// contre le filament de base : jamais de transfert silencieux entre matériaux.
func (s *Service) resolveBase(spec BaseSpec, fam string, baseOvs []slicer.Override) (*resolvedBase, error) {
	if err := s.needProfiles(); err != nil {
		return nil, err
	}
	machine := spec.Machine
	if machine == "" {
		if spec.Printer == "" {
			return nil, invalid("base : machine ou printer requis")
		}
		nz := spec.Nozzle
		if nz == 0 {
			nz = 0.4
		}
		var err error
		if machine, err = s.prof.MachineProfile(spec.Printer, nz); err != nil {
			return nil, err
		}
	}
	rm, err := s.prof.Resolve(profiles.KindMachine, machine)
	if err != nil {
		return nil, err
	}
	process := spec.Process
	if process == "" {
		process, _ = rm.String("default_print_profile")
	}
	if process == "" {
		return nil, invalid("base : aucun process fourni et la machine n'en déclare pas par défaut")
	}
	fils := spec.Filaments
	if len(fils) == 0 {
		if fam != "" {
			f, err := s.pickFilament(machine, fam)
			if err != nil {
				return nil, err
			}
			fils = []string{f}
		} else {
			fils = rm.Strings("default_filament_profile")
			if len(fils) > 1 {
				fils = fils[:1]
			}
		}
	}
	if len(fils) == 0 {
		return nil, invalid("base : aucun filament fourni et la machine n'en déclare pas par défaut")
	}
	if ok, err := s.prof.IsCompatible(profiles.KindProcess, process, machine); err != nil || !ok {
		return nil, fmt.Errorf("%w: process %q avec %q (%v)", ErrIncompatible, process, machine, err)
	}
	for _, f := range fils {
		if ok, err := s.prof.IsCompatible(profiles.KindFilament, f, machine); err != nil || !ok {
			return nil, fmt.Errorf("%w: filament %q avec %q (%v)", ErrIncompatible, f, machine, err)
		}
	}
	m, p, fs, _, err := slicer.ResolveWithOverrides(s.prof, machine, process, fils, baseOvs)
	if err != nil {
		return nil, fmt.Errorf("%w: surcharges de base : %v", ErrInvalid, err)
	}
	family := familyOf(fs[0])
	if fam != "" && family != fam {
		return nil, fmt.Errorf("%w: l'objectif vise du %s mais le filament de base %q est du %s ; aucun paramètre n'est transféré d'un matériau à un autre", ErrIncompatible, fam, fils[0], family)
	}
	nozzle := 0.4
	if nz := m.Strings("nozzle_diameter"); len(nz) > 0 {
		if v, _, ok := parseNum(nz[0]); ok {
			nozzle = v
		}
	}
	rb := &resolvedBase{machine: m, process: p, filament: fs[0]}
	rb.doc = BaseDoc{Machine: machine, Process: process, Filaments: fils, Family: family, Nozzle: nozzle, ProfilesStore: s.prof.Version,
		Digests: map[string]string{}, Overrides: baseOvs}
	rb.doc.Digests["machine:"+machine] = digestOf(rm)
	rp, _ := s.prof.Resolve(profiles.KindProcess, process)
	rb.doc.Digests["process:"+process] = digestOf(rp)
	for _, f := range fils {
		rf, _ := s.prof.Resolve(profiles.KindFilament, f)
		rb.doc.Digests["filament:"+f] = digestOf(rf)
	}
	return rb, nil
}

// pickFilament choisit, de façon déterministe, un filament Bambu compatible de la famille.
func (s *Service) pickFilament(machine, fam string) (string, error) {
	names, err := s.prof.CompatibleFilaments(machine)
	if err != nil {
		return "", err
	}
	sort.Strings(names)
	var best string
	for _, n := range names {
		r, err := s.prof.Resolve(profiles.KindFilament, n)
		if err != nil || familyOf(r) != fam {
			continue
		}
		if v, _ := r.String("filament_vendor"); v != "Bambu Lab" {
			continue
		}
		if strings.Contains(n, " Basic ") || strings.HasPrefix(n, "Bambu "+fam+" @") || strings.HasPrefix(n, "Bambu "+fam+" Basic") {
			return n, nil
		}
		if best == "" {
			best = n
		}
	}
	if best == "" {
		return "", fmt.Errorf("%w: aucun filament Bambu %s compatible avec %q dans les profils ; préciser base.filaments", ErrIncompatible, fam, machine)
	}
	return best, nil
}

// verifyDigests recontrôle que les profils n'ont pas changé depuis la figeage.
func (s *Service) verifyDigests(b BaseDoc) error {
	check := func(kind profiles.Kind, name string) error {
		r, err := s.prof.Resolve(kind, name)
		if err != nil {
			return err
		}
		if d := digestOf(r); d != b.Digests[string(kind)+":"+name] {
			return fmt.Errorf("%w: le profil %s %q a changé depuis la création de l'expérience (empreinte différente) : recréer l'expérience", ErrStale, kind, name)
		}
		return nil
	}
	if err := check(profiles.KindMachine, b.Machine); err != nil {
		return err
	}
	if err := check(profiles.KindProcess, b.Process); err != nil {
		return err
	}
	for _, f := range b.Filaments {
		if err := check(profiles.KindFilament, f); err != nil {
			return err
		}
	}
	return nil
}
