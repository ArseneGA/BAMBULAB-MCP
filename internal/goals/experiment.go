package goals

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// SourceRef identifie le fichier source figé.
type SourceRef struct {
	FileID    string `json:"file_id"`
	Name      string `json:"name"`
	Format    string `json:"format"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

// Budget borne une expérience.
type Budget struct {
	MaxCandidates       int `json:"max_candidates,omitempty" jsonschema:"nombre maximal de variantes (hors référence)"`
	SliceTimeoutSeconds int `json:"slice_timeout_seconds,omitempty" jsonschema:"délai maximal par découpe"`
}

// Geometry est la signature géométrique du modèle figé (indépendante de l'orientation).
type Geometry struct {
	SizeMM        [3]float64 `json:"size_mm" jsonschema:"dimensions du boîtier englobant dans le repère du modèle"`
	DimsSortedMM  [3]float64 `json:"dims_sorted_mm" jsonschema:"dimensions triées, utilisées pour juger la similarité"`
	VolumeMM3     float64    `json:"volume_mm3,omitempty"`
	TriangleCount int        `json:"triangles"`
}

// ExperimentDoc est le contenu figé d'une expérience.
type ExperimentDoc struct {
	Source              SourceRef   `json:"source"`
	Base                BaseDoc     `json:"base"`
	BaseProfileRevision string      `json:"base_profile_revision,omitempty"`
	Budget              Budget      `json:"budget"`
	RequestedCount      int         `json:"requested_candidates"`
	MaxFactors          int         `json:"max_factors"`
	RulesVersion        string      `json:"rules_version"`
	Geometry            Geometry    `json:"geometry"`
	Skipped             []Skipped   `json:"skipped_levers"`
	Notes               []string    `json:"notes"`
	Prior               []MemoryHit `json:"prior_results,omitempty"`
}

// CandidateView est un candidat avec son état.
type CandidateView struct {
	Candidate
	State       string       `json:"state" jsonschema:"pending | queued | running | succeeded | failed | cancelled"`
	JobID       string       `json:"job_id,omitempty"`
	Error       string       `json:"error,omitempty"`
	ArtifactSHA string       `json:"artifact_sha256,omitempty"`
	Metrics     *CandMetrics `json:"metrics,omitempty"`
}

// Experiment est l'expérience complète.
type Experiment struct {
	ID             string          `json:"id"`
	GoalID         string          `json:"goal_id,omitempty"`
	ModelID        string          `json:"model_id"`
	ModelVersionID string          `json:"model_version_id" jsonschema:"révision figée du modèle"`
	CreatedAt      time.Time       `json:"created_at"`
	Details        ExperimentDoc   `json:"details"`
	Candidates     []CandidateView `json:"candidates"`
}

// CustomCandidate est une variante fournie par l'appelant (validée comme les autres).
type CustomCandidate struct {
	Label     string            `json:"label"`
	Rationale string            `json:"rationale,omitempty"`
	Overrides []slicer.Override `json:"overrides"`
}

// CreateExperimentInput est l'entrée de create_experiment.
type CreateExperimentInput struct {
	ModelID               string            `json:"model_id"`
	ModelVersionID        string            `json:"model_version_id,omitempty" jsonschema:"version à figer ; défaut : la plus récente"`
	ExpectedModelRevision string            `json:"expected_model_revision,omitempty" jsonschema:"si fournie, refus quand la version la plus récente du modèle est autre"`
	GoalID                string            `json:"goal_id"`
	Base                  BaseSpec          `json:"base"`
	BaseProfileRevision   string            `json:"base_profile_revision,omitempty" jsonschema:"révision issue de modify_slicing_profile ; sa machine, son process, ses filaments et ses surcharges servent de base"`
	Candidates            int               `json:"candidates,omitempty" jsonschema:"nombre de variantes à générer (défaut 3, plafond configurable)"`
	Budget                Budget            `json:"budget,omitempty"`
	Custom                []CustomCandidate `json:"custom_candidates,omitempty" jsonschema:"variantes proposées par l'appelant, validées contre les profils"`
}

// sliceableSource retourne le fichier source découpable de la version.
func (s *Service) sliceableSource(ctx context.Context, v contracts.ModelVersion) (contracts.SourceFile, error) {
	files, err := s.cfg.Store.ListSourceFiles(ctx, v.ModelID)
	if err != nil {
		return contracts.SourceFile{}, err
	}
	byID := map[string]contracts.SourceFile{}
	for _, f := range files {
		byID[f.ID] = f
	}
	var sawStep bool
	for _, id := range v.SourceFileIDs {
		f, ok := byID[id]
		if !ok {
			continue
		}
		switch f.Format {
		case contracts.FormatSTL, contracts.FormatOBJ, contracts.Format3MF:
			if f.Availability != contracts.AvailableLinux {
				continue
			}
			if s.cfg.Blobs == nil || !s.cfg.Blobs.Has(f.SHA256) {
				return f, fmt.Errorf("%w: contenu du fichier %q absent du stockage", ErrUnavailable, f.Name)
			}
			return f, nil
		case contracts.FormatSTEP:
			sawStep = true
		}
	}
	if sawStep {
		return contracts.SourceFile{}, fmt.Errorf("%w: la version %s ne contient que du STEP, non découpable directement : exporter d'abord un maillage (STL/3MF)", ErrNotReady, v.ID)
	}
	return contracts.SourceFile{}, fmt.Errorf("%w: aucun fichier STL/OBJ/3MF disponible dans la version %s", ErrNotReady, v.ID)
}

func (s *Service) readBlob(sha string) ([]byte, error) {
	f, err := s.cfg.Blobs.Open(sha)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// CreateExperiment fige le modèle et la base, génère les candidats et met les
// découpes en file (une lourde à la fois, via la file de travaux).
func (s *Service) CreateExperiment(ctx context.Context, in CreateExperimentInput) (*Experiment, error) {
	if in.ModelID == "" || in.GoalID == "" {
		return nil, invalid("model_id et goal_id sont requis")
	}
	if err := s.needProfiles(); err != nil {
		return nil, err
	}
	model, err := s.cfg.Store.GetModel(ctx, in.ModelID)
	if err != nil {
		return nil, fmt.Errorf("modèle %q : %w", in.ModelID, err)
	}
	if model.DeletedAt != nil {
		return nil, fmt.Errorf("%w: le modèle %q est dans la corbeille", ErrNotReady, in.ModelID)
	}
	versions, err := s.cfg.Store.ListVersions(ctx, in.ModelID)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("%w: le modèle %q n'a aucune version", ErrNotReady, in.ModelID)
	}
	latest := versions[0]
	if in.ExpectedModelRevision != "" && in.ExpectedModelRevision != latest.ID {
		return nil, &StaleError{What: "révision du modèle", Expected: in.ExpectedModelRevision, Current: latest.ID}
	}
	ver := latest
	if in.ModelVersionID != "" {
		found := false
		for _, v := range versions {
			if v.ID == in.ModelVersionID {
				ver, found = v, true
			}
		}
		if !found {
			return nil, invalid("version %q inconnue pour le modèle %q", in.ModelVersionID, in.ModelID)
		}
	}
	goal, err := s.GetGoal(ctx, in.GoalID)
	if err != nil {
		return nil, err
	}
	// base : éventuellement depuis une révision de profil
	base, baseOvs := in.Base, []slicer.Override(nil)
	if in.BaseProfileRevision != "" {
		pr, err := s.loadProfile(ctx, in.BaseProfileRevision)
		if err != nil {
			return nil, err
		}
		base = BaseSpec{Machine: pr.Machine, Process: pr.Process, Filaments: pr.Filaments}
		baseOvs = pr.Overrides
	}
	rb, err := s.resolveBase(base, goal.MaterialFam, baseOvs)
	if err != nil {
		return nil, err
	}
	src, err := s.sliceableSource(ctx, ver)
	if err != nil {
		return nil, err
	}
	data, err := s.readBlob(src.SHA256)
	if err != nil {
		return nil, fmt.Errorf("lecture du modèle : %w", err)
	}
	md, err := mesh.Parse(src.Name, data)
	if err != nil {
		return nil, fmt.Errorf("lecture de la géométrie : %w", err)
	}
	m := md.Combined()
	geo := Geometry{TriangleCount: len(m.T)}
	if len(m.T) > 0 {
		b := m.Bounds()
		geo.SizeMM = [3]float64{b.Size.X, b.Size.Y, b.Size.Z}
		d := []float64{b.Size.X, b.Size.Y, b.Size.Z}
		sort.Sort(sort.Reverse(sort.Float64Slice(d)))
		geo.DimsSortedMM = [3]float64{d[0], d[1], d[2]}
		geo.VolumeMM3 = math.Abs(mesh.Analyze(m, mesh.AnalyzeOptions{}).Volume)
	}
	// Cible lue dans le preset machine (volume, zones d'exclusion, volume par buse) ; repli nominal marqué sinon.
	bed, terr := s.prof.TargetForMachine(rb.doc.Machine)
	if terr != nil {
		bed, _ = mesh.NominalTarget("p1s")
		bed.Name = rb.doc.Machine
	}
	if rb.doc.Nozzle > 0 {
		bed.Nozzle = rb.doc.Nozzle
	}

	count := in.Candidates
	if count <= 0 {
		count = s.cfg.DefaultCandidates
	}
	var notes []string
	if in.Budget.MaxCandidates > 0 && count > in.Budget.MaxCandidates {
		notes = append(notes, fmt.Sprintf("nombre de variantes ramené de %d à %d par le budget", count, in.Budget.MaxCandidates))
		count = in.Budget.MaxCandidates
	}
	if count > s.cfg.MaxCandidates {
		notes = append(notes, fmt.Sprintf("nombre de variantes ramené de %d à %d (plafond du service)", count, s.cfg.MaxCandidates))
		count = s.cfg.MaxCandidates
	}
	gen, err := Generate(GenInput{
		Goal: goal, Rules: s.rules, Process: rb.process, Filament: rb.filament, Family: rb.doc.Family,
		Nozzle: rb.doc.Nozzle, Count: count, MaxFactors: s.cfg.MaxFactors, Mesh: m, Bed: bed,
	})
	if err != nil {
		return nil, err
	}
	cands := gen.Candidates
	for _, cc := range in.Custom {
		if strings.TrimSpace(cc.Label) == "" || len(cc.Overrides) == 0 {
			return nil, invalid("variante personnalisée : label et surcharges requis")
		}
		all := append(append([]slicer.Override{}, baseOvs...), cc.Overrides...)
		if _, _, _, _, err := slicer.ResolveWithOverrides(s.prof, rb.doc.Machine, rb.doc.Process, rb.doc.Filaments, all); err != nil {
			return nil, fmt.Errorf("%w: variante %q : %v", ErrInvalid, cc.Label, err)
		}
		cands = append(cands, Candidate{
			Index: len(cands), Kind: "variant", Label: cc.Label, Rationale: orDefault(cc.Rationale, "Variante fournie par l'appelant."),
			Overrides: cc.Overrides, Changes: []Change{}, Factors: len(cc.Overrides), Tradeoffs: []string{},
			Notes: []string{"variante personnalisée : valeurs validées contre les profils, justification non vérifiée par les règles"},
		})
	}
	notes = append(notes, gen.Notes...)

	expID := contracts.NewID("exp")
	doc := ExperimentDoc{
		Source: SourceRef{FileID: src.ID, Name: src.Name, Format: string(src.Format), SHA256: src.SHA256, SizeBytes: src.SizeBytes},
		Base:   rb.doc, BaseProfileRevision: in.BaseProfileRevision, Budget: in.Budget, RequestedCount: count, MaxFactors: s.cfg.MaxFactors,
		RulesVersion: s.rules.Version, Geometry: geo, Skipped: gen.Skipped, Notes: notes,
	}
	// mémoire : essais antérieurs comparables (même famille, même machine, géométrie proche)
	doc.Prior = s.memoryFor(ctx, MemoryQuery{
		MaterialFamily: rb.doc.Family, Machine: rb.doc.Machine, Filament: rb.doc.Filaments[0], ModelID: in.ModelID, DimsSortedMM: geo.DimsSortedMM,
	}, 8)
	for i := range cands {
		cands[i].ID = fmt.Sprintf("%s_c%d", expID, cands[i].Index)
		for _, h := range doc.Prior {
			if h.CandidateSignature != "" && h.CandidateSignature == candSig(cands[i]) && cands[i].Kind == "variant" {
				cands[i].Notes = append(cands[i].Notes, fmt.Sprintf("déjà essayé en conditions comparables (%s) : résultat « %s »", h.Comparability, h.Outcome))
			}
		}
	}
	docJSON, _ := json.Marshal(doc)
	createdAt := s.now()
	err = s.cfg.Store.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO experiments(id,goal_id,model_id,model_version_id,doc,created_at) VALUES(?,?,?,?,?,?)`,
			expID, goal.ID, in.ModelID, ver.ID, string(docJSON), store.FormatTime(createdAt)); err != nil {
			return err
		}
		for _, c := range cands {
			spec, _ := json.Marshal(c)
			if _, err := tx.ExecContext(ctx, `INSERT INTO experiment_candidates(id,experiment_id,idx,label,spec,state,updated_at) VALUES(?,?,?,?,?,'pending',?)`,
				c.ID, expID, c.Index, c.Label, string(spec), store.FormatTime(createdAt)); err != nil {
				return err
			}
		}
		return s.cfg.Store.AuditTx(ctx, tx, actor, "experiment.create", "experiment", expID,
			map[string]any{"model_id": in.ModelID, "model_version": ver.ID, "candidates": len(cands)})
	})
	if err != nil {
		return nil, err
	}
	// mise en file (hors transaction : une seule connexion SQLite)
	if s.cfg.Queue != nil {
		for _, c := range cands {
			p, _ := json.Marshal(sliceParams{ExperimentID: expID, CandidateID: c.ID})
			j, _, err := s.cfg.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: JobKind, Class: contracts.ClassHeavy, Params: p, IdempotencyKey: "goals:" + c.ID})
			if err != nil {
				return nil, fmt.Errorf("mise en file de %s : %w", c.ID, err)
			}
			if _, err := s.cfg.Store.DB().ExecContext(ctx, `UPDATE experiment_candidates SET job_id=?, state='queued', updated_at=? WHERE id=?`,
				j.ID, store.FormatTime(s.now()), c.ID); err != nil {
				return nil, err
			}
		}
	}
	return s.GetExperiment(ctx, expID)
}

func orDefault(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

// GetExperiment relit l'expérience et l'état de ses candidats (rafraîchi depuis la file).
func (s *Service) GetExperiment(ctx context.Context, id string) (*Experiment, error) {
	var e Experiment
	var doc, at string
	var goal sql.NullString
	err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT id,goal_id,model_id,model_version_id,doc,created_at FROM experiments WHERE id=?`, id).
		Scan(&e.ID, &goal, &e.ModelID, &e.ModelVersionID, &doc, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("expérience %q : %w", id, store.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	e.GoalID = goal.String
	e.CreatedAt, _ = store.ParseTime(at)
	if err := json.Unmarshal([]byte(doc), &e.Details); err != nil {
		return nil, err
	}
	rows, err := s.cfg.Store.DB().QueryContext(ctx,
		`SELECT id,spec,job_id,state,metrics,artifact_sha,error FROM experiment_candidates WHERE experiment_id=? ORDER BY idx`, id)
	if err != nil {
		return nil, err
	}
	type stale struct{ id, job string }
	var maybeStale []stale
	for rows.Next() {
		var cv CandidateView
		var spec string
		var job, metrics, art, errMsg sql.NullString
		if err := rows.Scan(&cv.ID, &spec, &job, &cv.State, &metrics, &art, &errMsg); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(spec), &cv.Candidate); err != nil {
			rows.Close()
			return nil, err
		}
		cv.JobID, cv.ArtifactSHA, cv.Error = job.String, art.String, errMsg.String
		if metrics.Valid {
			var m CandMetrics
			if json.Unmarshal([]byte(metrics.String), &m) == nil {
				cv.Metrics = &m
			}
		}
		if (cv.State == "queued" || cv.State == "running") && cv.JobID != "" {
			maybeStale = append(maybeStale, stale{cv.ID, cv.JobID})
		}
		e.Candidates = append(e.Candidates, cv)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Réconciliation avec la file : un travail terminé sans mise à jour du candidat.
	if s.cfg.Queue != nil {
		for _, m := range maybeStale {
			j, err := s.cfg.Queue.Get(ctx, m.job)
			if err != nil || !j.State.Terminal() || j.State == contracts.JobSucceeded {
				continue
			}
			msg := "travail terminé sans résultat (" + string(j.State) + ")"
			if j.Error != nil {
				msg = j.Error.Code + " : " + j.Error.Message
			}
			state := "failed"
			if j.State == contracts.JobCancelled {
				state = "cancelled"
			}
			_, _ = s.cfg.Store.DB().ExecContext(ctx, `UPDATE experiment_candidates SET state=?, error=?, updated_at=? WHERE id=? AND state IN ('queued','running')`,
				state, msg, store.FormatTime(s.now()), m.id)
			for i := range e.Candidates {
				if e.Candidates[i].ID == m.id && (e.Candidates[i].State == "queued" || e.Candidates[i].State == "running") {
					e.Candidates[i].State, e.Candidates[i].Error = state, msg
				}
			}
		}
	}
	return &e, nil
}
