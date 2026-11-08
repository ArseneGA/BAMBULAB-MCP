package goals

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Origin dit d'où vient une révision de profil appliquée.
type Origin struct {
	ExperimentID string `json:"experiment_id"`
	CandidateID  string `json:"candidate_id"`
}

// ProfileRev est une révision immuable de profil de découpe.
type ProfileRev struct {
	ID          string            `json:"id"`
	ParentID    string            `json:"parent_id,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	Machine     string            `json:"machine"`
	Process     string            `json:"process"`
	Filaments   []string          `json:"filaments"`
	Overrides   []slicer.Override `json:"overrides" jsonschema:"surcharges cumulées par rapport aux profils de l'installation"`
	Orientation *OrientationSpec  `json:"orientation,omitempty"`
	Origin      *Origin           `json:"origin,omitempty"`
	Note        string            `json:"note,omitempty"`
}

func (s *Service) insertProfile(ctx context.Context, tx *sql.Tx, r *ProfileRev) error {
	r.ID = contracts.NewID("prof")
	r.CreatedAt = s.now()
	doc, _ := json.Marshal(r)
	_, err := tx.ExecContext(ctx, `INSERT INTO slicing_profiles(id,parent_id,doc,created_at) VALUES(?,?,?,?)`,
		r.ID, nullIfEmpty(r.ParentID), string(doc), store.FormatTime(r.CreatedAt))
	return err
}

func (s *Service) loadProfile(ctx context.Context, id string) (*ProfileRev, error) {
	var doc string
	err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT doc FROM slicing_profiles WHERE id=?`, id).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("révision de profil %q : %w", id, store.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var r ProfileRev
	if err := json.Unmarshal([]byte(doc), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// profileHead suit les enfants jusqu'à la tête de la lignée.
func (s *Service) profileHead(ctx context.Context, id string) (string, error) {
	cur := id
	for i := 0; i < 1000; i++ {
		var child string
		err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT id FROM slicing_profiles WHERE parent_id=? ORDER BY created_at DESC, id DESC LIMIT 1`, cur).Scan(&child)
		if errors.Is(err, sql.ErrNoRows) {
			return cur, nil
		}
		if err != nil {
			return "", err
		}
		cur = child
	}
	return cur, nil
}

func mergeOverrides(base, add []slicer.Override) []slicer.Override {
	out := append([]slicer.Override{}, base...)
	for _, o := range add {
		replaced := false
		for i := range out {
			if out[i].Key == o.Key && out[i].Target == o.Target {
				out[i] = o
				replaced = true
			}
		}
		if !replaced {
			out = append(out, o)
		}
	}
	return out
}

// ProfileView est un profil complet avec sa provenance.
type ProfileView struct {
	Name   string            `json:"name"`
	Chain  []string          `json:"inherits_chain"`
	Config map[string]any    `json:"config" jsonschema:"configuration complète (machine : seulement les clés modifiées)"`
	Source map[string]string `json:"provenance" jsonschema:"clé -> profil d'origine, « override » si surchargée"`
}

// DiffEntry est une différence entre deux révisions.
type DiffEntry struct {
	Target    string `json:"target"`
	Key       string `json:"key"`
	Old       any    `json:"old"`
	New       any    `json:"new"`
	OldSource string `json:"old_source,omitempty"`
	NewSource string `json:"new_source,omitempty"`
}

// ModifyInput est l'entrée de modify_slicing_profile.
type ModifyInput struct {
	BaseRevision  string            `json:"base_revision,omitempty" jsonschema:"révision de profil à modifier ; doit être la tête de sa lignée (sinon refus)"`
	Base          *BaseSpec         `json:"base,omitempty" jsonschema:"profils de départ quand il n'y a pas encore de révision"`
	Modifications []slicer.Override `json:"modifications" jsonschema:"surcharges typées : clé existante du profil, valeur du même type, cible machine/process/filament"`
	Note          string            `json:"note,omitempty"`
	AllowBranch   bool              `json:"allow_branch,omitempty" jsonschema:"autorise une branche depuis une révision déjà prolongée"`
}

// ModifyOutput est le profil complet validé.
type ModifyOutput struct {
	Revision       string                   `json:"revision"`
	ParentRevision string                   `json:"parent_revision,omitempty"`
	Machine        ProfileView              `json:"machine"`
	Process        ProfileView              `json:"process"`
	Filaments      []ProfileView            `json:"filaments"`
	Overrides      []slicer.Override        `json:"overrides"`
	Applied        []slicer.AppliedOverride `json:"applied"`
	Diff           []DiffEntry              `json:"diff" jsonschema:"différences avec la révision de base (ou avec les profils d'origine pour une première révision)"`
	Effective      []EffectiveSetting       `json:"effective_settings" jsonschema:"réglages d'adhérence et de supports EFFECTIFS après modification (brim_type, brim_width, raft_layers, enable_support, support_type, curr_bed_type), avec leur origine : surcharge, profil ou défaut de Bambu Studio ; jamais null"`
	Notes          []string                 `json:"notes,omitempty"`
}

// EffectiveSetting est la valeur effective d'un réglage et son origine.
type EffectiveSetting struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source" jsonschema:"surcharge | profil | défaut Bambu Studio | inconnue"`
}

// effectiveKeys : réglages qui décident de l'adhérence et des supports, toujours résolus pour l'affichage.
var effectiveKeys = []string{"brim_type", "brim_width", "raft_layers", "enable_support", "support_type", "support_threshold_angle", "curr_bed_type"}

// effectiveSettings résout la valeur effective de chaque réglage clé (défaut de Bambu Studio compris).
func effectiveSettings(p *profiles.Resolved) []EffectiveSetting {
	out := make([]EffectiveSetting, 0, len(effectiveKeys))
	for _, k := range effectiveKeys {
		v, src := slicer.Effective(p, k)
		out = append(out, EffectiveSetting{Key: k, Value: v, Source: src})
	}
	return out
}

func view(r *profiles.Resolved) ProfileView {
	return ProfileView{Name: r.Name, Chain: r.Chain, Config: r.Config, Source: r.Source}
}

func diffResolved(target string, oldR, newR *profiles.Resolved) []DiffEntry {
	var out []DiffEntry
	keys := map[string]bool{}
	for k := range oldR.Config {
		keys[k] = true
	}
	for k := range newR.Config {
		keys[k] = true
	}
	var ks []string
	for k := range keys {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		if !reflect.DeepEqual(oldR.Config[k], newR.Config[k]) {
			d := DiffEntry{Target: target, Key: k, Old: oldR.Config[k], New: newR.Config[k], OldSource: oldR.Source[k], NewSource: newR.Source[k]}
			// clé absente du profil : sa valeur effective est celle de Bambu Studio, pas « null »
			if _, ok := oldR.Config[k]; !ok {
				if def, ok := slicer.BambuDefault(k); ok {
					d.Old, d.OldSource = def, slicer.SourceDefault
				}
			}
			out = append(out, d)
		}
	}
	return out
}

// ModifyProfile valide des modifications typées contre le profil résolu et
// enregistre une nouvelle révision immuable.
func (s *Service) ModifyProfile(ctx context.Context, in ModifyInput) (*ModifyOutput, error) {
	if err := s.needProfiles(); err != nil {
		return nil, err
	}
	if len(in.Modifications) == 0 {
		return nil, invalid("modifications vide : rien à modifier")
	}
	var parent *ProfileRev
	var spec BaseSpec
	switch {
	case in.BaseRevision != "":
		p, err := s.loadProfile(ctx, in.BaseRevision)
		if err != nil {
			return nil, err
		}
		head, err := s.profileHead(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		if head != p.ID && !in.AllowBranch {
			return nil, &StaleError{What: "révision de profil (déjà prolongée)", Expected: p.ID, Current: head}
		}
		parent = p
		spec = BaseSpec{Machine: p.Machine, Process: p.Process, Filaments: p.Filaments}
	case in.Base != nil:
		spec = *in.Base
	default:
		return nil, invalid("base_revision ou base requis")
	}
	rb, err := s.resolveBase(spec, "", nil)
	if err != nil {
		return nil, err
	}
	var parentOvs []slicer.Override
	if parent != nil {
		parentOvs = parent.Overrides
	}
	merged := mergeOverrides(parentOvs, in.Modifications)
	nm, np, nfs, applied, err := slicer.ResolveWithOverrides(s.prof, rb.doc.Machine, rb.doc.Process, rb.doc.Filaments, merged)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	om, op, ofs, _, err := slicer.ResolveWithOverrides(s.prof, rb.doc.Machine, rb.doc.Process, rb.doc.Filaments, parentOvs)
	if err != nil {
		return nil, err
	}
	rev := &ProfileRev{Machine: rb.doc.Machine, Process: rb.doc.Process, Filaments: rb.doc.Filaments, Overrides: merged, Note: in.Note}
	if parent != nil {
		rev.ParentID, rev.Orientation = parent.ID, parent.Orientation
	}
	err = s.cfg.Store.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.insertProfile(ctx, tx, rev); err != nil {
			return err
		}
		return s.cfg.Store.AuditTx(ctx, tx, actor, "profile.modify", "slicing_profile", rev.ID, map[string]any{"parent": rev.ParentID, "changes": len(in.Modifications)})
	})
	if err != nil {
		return nil, err
	}
	out := &ModifyOutput{Revision: rev.ID, ParentRevision: rev.ParentID, Process: view(np), Overrides: merged, Applied: applied, Diff: []DiffEntry{},
		Effective: effectiveSettings(np)}
	// machine : seulement les clés modifiées (le profil complet fait des centaines de clés dont des scripts G-code)
	mv := ProfileView{Name: nm.Name, Chain: nm.Chain, Config: map[string]any{}, Source: map[string]string{}}
	for _, d := range diffResolved("machine", om, nm) {
		mv.Config[d.Key], mv.Source[d.Key] = d.New, d.NewSource
	}
	out.Machine = mv
	out.Diff = append(out.Diff, diffResolved("machine", om, nm)...)
	out.Diff = append(out.Diff, diffResolved("process", op, np)...)
	for i := range nfs {
		out.Filaments = append(out.Filaments, view(nfs[i]))
		out.Diff = append(out.Diff, diffResolved("filament", ofs[i], nfs[i])...)
	}
	out.Notes = append([]string{"profil machine : seules les clés modifiées sont listées (le profil complet est écrit au moment de la découpe)"}, rb.notes...)
	if parent == nil {
		out.Notes = append(out.Notes, "première révision : la différence est calculée par rapport aux profils d'origine de l'installation")
	}
	return out, nil
}

// ApplyInput est l'entrée de apply_variant.
type ApplyInput struct {
	ExperimentID     string `json:"experiment_id"`
	CandidateID      string `json:"candidate_id"`
	ExpectedRevision string `json:"expected_revision" jsonschema:"révision du modèle sur laquelle l'utilisateur raisonne ; refus si le modèle ou l'expérience portent une autre révision"`
}

// ApplyOutput décrit l'application.
type ApplyOutput struct {
	ProfileRevision string            `json:"profile_revision"`
	ModelID         string            `json:"model_id"`
	ModelVersionID  string            `json:"model_version_id"`
	ExperimentID    string            `json:"experiment_id"`
	CandidateID     string            `json:"candidate_id"`
	Label           string            `json:"label"`
	Overrides       []slicer.Override `json:"overrides"`
	Orientation     *OrientationSpec  `json:"orientation,omitempty"`
	Notes           []string          `json:"notes"`
	AppliedAt       time.Time         `json:"applied_at"`
}

// ApplyVariant fixe la variante choisie comme profil du modèle, si la révision n'a pas bougé.
func (s *Service) ApplyVariant(ctx context.Context, in ApplyInput) (*ApplyOutput, error) {
	if in.ExperimentID == "" || in.CandidateID == "" {
		return nil, invalid("experiment_id et candidate_id requis")
	}
	if in.ExpectedRevision == "" {
		return nil, invalid("expected_revision requis")
	}
	exp, err := s.GetExperiment(ctx, in.ExperimentID)
	if err != nil {
		return nil, err
	}
	var cand *CandidateView
	for i := range exp.Candidates {
		if exp.Candidates[i].ID == in.CandidateID {
			cand = &exp.Candidates[i]
		}
	}
	if cand == nil {
		return nil, fmt.Errorf("candidat %q de l'expérience %q : %w", in.CandidateID, in.ExperimentID, store.ErrNotFound)
	}
	if cand.State != "succeeded" {
		return nil, fmt.Errorf("%w: le candidat %s est « %s » ; seule une variante découpée avec succès peut être appliquée", ErrNotReady, cand.ID, cand.State)
	}
	var out *ApplyOutput
	err = s.cfg.Store.Tx(ctx, func(tx *sql.Tx) error {
		var latest string
		err := tx.QueryRowContext(ctx, `SELECT id FROM model_versions WHERE model_id=? ORDER BY created_at DESC, id LIMIT 1`, exp.ModelID).Scan(&latest)
		if err != nil {
			return err
		}
		if in.ExpectedRevision != exp.ModelVersionID {
			return &StaleError{What: "révision du modèle figée dans l'expérience", Expected: in.ExpectedRevision, Current: exp.ModelVersionID}
		}
		if latest != exp.ModelVersionID {
			return &StaleError{What: "révision du modèle (modifié depuis l'expérience)", Expected: exp.ModelVersionID, Current: latest}
		}
		rev := &ProfileRev{
			ParentID: exp.Details.BaseProfileRevision, Machine: exp.Details.Base.Machine, Process: exp.Details.Base.Process,
			Filaments: exp.Details.Base.Filaments, Overrides: mergeOverrides(exp.Details.Base.Overrides, cand.Overrides),
			Orientation: cand.Orientation, Origin: &Origin{ExperimentID: exp.ID, CandidateID: cand.ID},
			Note: "variante « " + cand.Label + " »",
		}
		if err := s.insertProfile(ctx, tx, rev); err != nil {
			return err
		}
		avID := contracts.NewID("appl")
		at := s.now()
		if _, err := tx.ExecContext(ctx, `INSERT INTO applied_variants(id,model_id,model_version_id,experiment_id,candidate_id,profile_id,applied_at) VALUES(?,?,?,?,?,?,?)`,
			avID, exp.ModelID, exp.ModelVersionID, exp.ID, cand.ID, rev.ID, store.FormatTime(at)); err != nil {
			return err
		}
		out = &ApplyOutput{ProfileRevision: rev.ID, ModelID: exp.ModelID, ModelVersionID: exp.ModelVersionID, ExperimentID: exp.ID, CandidateID: cand.ID,
			Label: cand.Label, Overrides: rev.Overrides, Orientation: cand.Orientation, AppliedAt: at, Notes: []string{}}
		return s.cfg.Store.AuditTx(ctx, tx, actor, "variant.apply", "model", exp.ModelID, map[string]any{"experiment": exp.ID, "candidate": cand.ID, "profile": rev.ID})
	})
	if err != nil {
		return nil, err
	}
	if cand.Orientation != nil {
		out.Notes = append(out.Notes, "L'orientation proposée est enregistrée dans la révision de profil mais n'a pas modifié le modèle (les versions de modèle sont immuables) : l'appliquer à la géométrie lors de la prochaine mise en plateau.")
	}
	out.Notes = append(out.Notes, "Aucune impression n'est lancée : le démarrage reste soumis à confirmation explicite.")
	return out, nil
}

// AppliedFor renvoie la dernière variante appliquée à un modèle (nil si aucune).
func (s *Service) AppliedFor(ctx context.Context, modelID string) (*ProfileRev, error) {
	var pid string
	err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT profile_id FROM applied_variants WHERE model_id=? ORDER BY applied_at DESC, id DESC LIMIT 1`, modelID).Scan(&pid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.loadProfile(ctx, pid)
}

// variantBed lit la surface imprimable d'un preset machine.
type bedInfo struct{ BedX, BedY, PrintableZ float64 }

func (s *Service) variantFor(machine string) (bedInfo, error) {
	var b bedInfo
	rm, err := s.prof.Resolve(profiles.KindMachine, machine)
	if err != nil {
		return b, err
	}
	minX, minY, maxX, maxY := 1e18, 1e18, -1e18, -1e18
	n := 0
	for _, p := range rm.Strings("printable_area") {
		var x, y float64
		if _, err := fmt.Sscanf(p, "%gx%g", &x, &y); err != nil {
			continue
		}
		minX, maxX, minY, maxY = minF(minX, x), maxF(maxX, x), minF(minY, y), maxF(maxY, y)
		n++
	}
	if n < 3 {
		return b, errors.New("printable_area illisible")
	}
	b.BedX, b.BedY = maxX-minX, maxY-minY
	if z, _, ok := parseNum(firstStr(rm.Config["printable_height"])); ok {
		b.PrintableZ = z
	} else {
		b.PrintableZ = 250
	}
	return b, nil
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

var _ = mesh.Identity
