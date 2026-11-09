package strength

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// JobAssess : type de travail de la file.
const JobAssess = "strength.assess"

const actor = "strength"

// MeshSource lit le maillage d'un modèle (implémenté par *printing.Service).
type MeshSource interface {
	ModelMesh(ctx context.Context, modelID, versionID string) (*mesh.Mesh, printing.ModelMeshRef, error)
	LastSliceSettings(ctx context.Context, modelID string) printing.SliceSettings
}

// Deps : ce que le cœur fournit.
type Deps struct {
	Store    *store.Store
	Blobs    *store.BlobStore
	Queue    *jobs.Queue
	Registry *jobs.Registry
	Meshes   MeshSource
	Log      *slog.Logger
	// SyncWait : attente maximale avant de rendre job_id (défaut 20 s ; le client MCP abandonne à 60 s).
	SyncWait time.Duration
}

// Service : plan, calcul et historique des tests de résistance.
type Service struct{ d Deps }

// New construit le service et enregistre le travail de calcul.
func New(d Deps) (*Service, error) {
	if d.Store == nil || d.Blobs == nil || d.Queue == nil || d.Meshes == nil {
		return nil, errors.New("strength : store, blobs, file de travaux et lecteur de maillage requis")
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	s := &Service{d: d}
	if d.Registry != nil {
		d.Registry.Register(JobAssess, true, s.assessHandler)
	}
	return s, nil
}

func (s *Service) syncWait() time.Duration {
	if s.d.SyncWait > 0 {
		return s.d.SyncWait
	}
	return 20 * time.Second
}

// ---- plan ----

// PlanInput : modèle à préparer.
type PlanInput struct {
	ModelID   string `json:"model_id"`
	VersionID string `json:"version_id,omitempty" jsonschema:"version du modèle (défaut : la plus récente)"`
	Usage     string `json:"usage,omitempty" jsonschema:"à quoi sert la pièce, dit par l'humain (ex. « porte-guitare mural ») : sert à proposer des hypothèses"`
}

// PlanOutput : zones, questions et hypothèses proposées.
type PlanOutput struct {
	ModelID    string        `json:"model_id"`
	VersionID  string        `json:"version_id"`
	Name       string        `json:"name"`
	Zones      []Zone        `json:"zones"`
	Questions  []Question    `json:"questions" jsonschema:"à poser à l'humain une par une, dans l'ordre"`
	Proposal   Proposal      `json:"proposed_assumptions" jsonschema:"hypothèses par défaut justifiées, à annoncer et à faire confirmer"`
	Known      KnownSettings `json:"known_settings"`
	Materials  []Material    `json:"materials" jsonschema:"matières prises en charge, niveau estimation"`
	Note       string        `json:"note"`
	Disclaimer string        `json:"disclaimer"`
	PNGSHA256  string        `json:"png_sha256,omitempty"`
	PNG        []byte        `json:"-"`
	Decimated  bool          `json:"analysis_mesh_decimated,omitempty"`
}

// Plan analyse la géométrie et prépare le questionnaire. Lecture seule (l'image est mise en réserve).
func (s *Service) Plan(ctx context.Context, in PlanInput) (*PlanOutput, error) {
	if strings.TrimSpace(in.ModelID) == "" {
		return nil, errors.New("model_id requis")
	}
	m, ref, err := s.d.Meshes.ModelMesh(ctx, in.ModelID, in.VersionID)
	if err != nil {
		return nil, err
	}
	zs := DetectZones(m)
	if len(zs.Zones) == 0 {
		return nil, errors.New("aucune zone exploitable sur ce maillage (surface trop fragmentée ?)")
	}
	known := KnownSettings{}
	if ss := s.d.Meshes.LastSliceSettings(ctx, in.ModelID); ss.Found {
		known = KnownSettings{Found: true, Filaments: ss.Filaments, Walls: ss.Walls, InfillPct: ss.InfillPct, Source: ss.Source}
		for _, f := range ss.Filaments {
			if mat, ok := LookupMaterial(f); ok {
				known.Material = mat.Key
				break
			}
		}
	}
	hint := in.Usage + " " + ref.Name + " " + strings.Join(ref.Tags, " ")
	var ex *ObjectExample
	if e, ok := MatchExample(hint); ok {
		ex = &e
	}
	out := &PlanOutput{ModelID: in.ModelID, VersionID: ref.VersionID, Name: ref.Name, Zones: zs.Zones, Known: known, Materials: Materials,
		Questions: BuildQuestions(zs, in.Usage, known, ex), Proposal: BuildProposal(zs, hint, ex), Disclaimer: Disclaimer, Decimated: zs.Decimated}
	if known.Found && known.Material != "" {
		out.Proposal.Material = known.Material
	}
	out.Note = "Numérotation des zones stable pour cette version du modèle. Poser les questions UNE par une ; annoncer toute valeur supposée (source « assumed ») et la laisser corriger. Puis assess_strength."
	png, err := PlanImage(zs, ref.Name)
	if err != nil {
		return nil, err
	}
	sha, _, err := s.d.Blobs.Put(bytes.NewReader(png), "")
	if err != nil {
		return nil, err
	}
	out.PNG, out.PNGSHA256 = png, sha
	_ = s.d.Store.Audit(ctx, actor, "strength.plan", "model", in.ModelID, map[string]any{"version_id": ref.VersionID, "zones": len(zs.Zones)})
	return out, nil
}

// ---- calcul ----

// AssessOutput : état et résultat d'un test.
type AssessOutput struct {
	State     string  `json:"state" jsonschema:"done | running"`
	JobID     string  `json:"job_id,omitempty" jsonschema:"suivre avec get_job_status / wait_for_job ; le résultat se relit avec get_strength_report"`
	TestID    string  `json:"test_id,omitempty"`
	Result    *Result `json:"result,omitempty"`
	PNGSHA256 string  `json:"png_sha256,omitempty"`
	Note      string  `json:"note,omitempty"`
	PNG       []byte  `json:"-"`
}

// Assess valide l'entrée (refus sans charge ni hypothèse), met le calcul en file et attend un peu.
func (s *Service) Assess(ctx context.Context, in AssessInput) (*AssessOutput, error) {
	if strings.TrimSpace(in.ModelID) == "" {
		return nil, errors.New("model_id requis")
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	params, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	qj, _, err := s.d.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: JobAssess, Class: contracts.ClassHeavy, Params: params})
	if err != nil {
		return nil, err
	}
	_ = s.d.Store.Audit(ctx, actor, "strength.assess_requested", "model", in.ModelID, map[string]any{"job_id": qj.ID, "loads": len(in.Loads), "supports": len(in.Supports), "material": in.Material})
	deadline := time.Now().Add(s.syncWait())
	for {
		q, gerr := s.d.Queue.Get(ctx, qj.ID)
		if gerr == nil && q.State.Terminal() {
			return s.finish(ctx, q)
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return &AssessOutput{State: "running", JobID: qj.ID, Note: "Le calcul continue en arrière-plan (jusqu'à quelques minutes sur une pièce détaillée) : suivre avec wait_for_job ou get_job_status, puis lire le résultat avec get_strength_report(model_id)."}, nil
		}
		time.Sleep(40 * time.Millisecond)
	}
}

func (s *Service) finish(ctx context.Context, q contracts.Job) (*AssessOutput, error) {
	switch q.State {
	case contracts.JobSucceeded:
		var r struct {
			TestID string `json:"test_id"`
		}
		_ = json.Unmarshal(q.Result, &r)
		rec, err := s.GetTest(ctx, r.TestID)
		if err != nil {
			return nil, err
		}
		out := &AssessOutput{State: "done", JobID: q.ID, TestID: rec.ID, Result: rec.Result, PNGSHA256: rec.PNGSHA256}
		out.PNG = s.pngOf(rec.PNGSHA256)
		return out, nil
	case contracts.JobFailed:
		msg := "calcul en échec"
		if q.Error != nil {
			msg = q.Error.Message
		}
		return nil, errors.New(msg)
	}
	return nil, fmt.Errorf("calcul %s (%s)", q.State, q.ID)
}

func (s *Service) pngOf(sha string) []byte {
	if sha == "" || !s.d.Blobs.Has(sha) {
		return nil
	}
	f, err := s.d.Blobs.Open(sha)
	if err != nil {
		return nil
	}
	defer f.Close()
	var b bytes.Buffer
	if _, err := b.ReadFrom(f); err != nil {
		return nil
	}
	return b.Bytes()
}

func (s *Service) assessHandler(ctx context.Context, jc *jobs.JobCtx) (json.RawMessage, error) {
	var in AssessInput
	if err := json.Unmarshal(jc.Job.Params, &in); err != nil {
		return nil, &jobs.HandlerError{Code: "bad_params", Err: err}
	}
	jc.Progress(0.01, "lecture du maillage")
	m, ref, err := s.d.Meshes.ModelMesh(ctx, in.ModelID, in.VersionID)
	if err != nil {
		return nil, &jobs.HandlerError{Code: "model", Err: err}
	}
	in.VersionID = ref.VersionID
	an, err := Run(ctx, m, in, func(p float64, msg string) { jc.Progress(p, msg) })
	if err != nil {
		code := "compute"
		if errors.Is(err, context.Canceled) {
			code = "canceled"
		}
		return nil, &jobs.HandlerError{Code: code, Err: err}
	}
	an.Result.ModelID, an.Result.VersionID = in.ModelID, ref.VersionID
	jc.Progress(0.94, "rendu de la carte de contraintes")
	png, err := StressImage(an, ref.Name)
	if err != nil {
		return nil, &jobs.HandlerError{Code: "render", Err: err}
	}
	sha, _, err := s.d.Blobs.Put(bytes.NewReader(png), "")
	if err != nil {
		return nil, &jobs.HandlerError{Code: "store", Err: err}
	}
	id, err := s.save(ctx, jc.Job.ID, in, an.Result, sha)
	if err != nil {
		return nil, &jobs.HandlerError{Code: "store", Err: err}
	}
	_ = s.d.Store.Audit(ctx, actor, "strength.assess_done", "model", in.ModelID, map[string]any{
		"test_id": id, "version_id": ref.VersionID, "verdict": an.Result.Verdict, "assumed": an.Result.BasedOnAssumed})
	return json.Marshal(map[string]string{"test_id": id})
}

// ---- historique ----

// TestRecord : un test enregistré.
type TestRecord struct {
	ID        string       `json:"test_id"`
	ModelID   string       `json:"model_id"`
	VersionID string       `json:"version_id"`
	JobID     string       `json:"job_id,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
	Material  string       `json:"material"`
	Verdict   string       `json:"verdict"`
	Safety    float64      `json:"safety_factor"`
	MaxStress float64      `json:"max_stress_mpa"`
	MaxDispMM float64      `json:"max_displacement_mm"`
	Assumed   bool         `json:"based_on_assumptions"`
	PNGSHA256 string       `json:"png_sha256,omitempty"`
	Input     *AssessInput `json:"input,omitempty"`
	Result    *Result      `json:"result,omitempty"`
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "st_" + hex.EncodeToString(b)
}

func (s *Service) save(ctx context.Context, jobID string, in AssessInput, r *Result, png string) (string, error) {
	id := newID()
	ib, _ := json.Marshal(in)
	rb, _ := json.Marshal(r)
	assumed := 0
	if r.BasedOnAssumed {
		assumed = 1
	}
	_, err := s.d.Store.DB().ExecContext(ctx, `INSERT INTO strength_tests
		(id, model_id, version_id, job_id, created_at, material, verdict, safety_factor, max_stress_mpa, max_disp_mm, assumed, input_json, result_json, png_sha256)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, in.ModelID, r.VersionID, jobID, store.FormatTime(s.d.Store.Now()), r.Material.Key, r.Verdict, r.SafetyAchieved, r.MaxVonMisesMPa, r.MaxDisplacementMM, assumed, string(ib), string(rb), png)
	return id, err
}

// GetTest relit un test.
func (s *Service) GetTest(ctx context.Context, id string) (*TestRecord, error) {
	return s.scan(s.d.Store.DB().QueryRowContext(ctx, `SELECT id, model_id, version_id, job_id, created_at, material, verdict, safety_factor,
		max_stress_mpa, max_disp_mm, assumed, input_json, result_json, png_sha256 FROM strength_tests WHERE id=?`, id), true)
}

type rowScanner interface{ Scan(dest ...any) error }

func (s *Service) scan(r rowScanner, full bool) (*TestRecord, error) {
	var t TestRecord
	var created, ib, rb string
	var assumed int
	if err := r.Scan(&t.ID, &t.ModelID, &t.VersionID, &t.JobID, &created, &t.Material, &t.Verdict, &t.Safety, &t.MaxStress, &t.MaxDispMM, &assumed, &ib, &rb, &t.PNGSHA256); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("test de résistance introuvable")
		}
		return nil, err
	}
	t.CreatedAt, _ = store.ParseTime(created)
	t.Assumed = assumed == 1
	if full {
		var in AssessInput
		var res Result
		if json.Unmarshal([]byte(ib), &in) == nil {
			t.Input = &in
		}
		if json.Unmarshal([]byte(rb), &res) == nil {
			t.Result = &res
		}
	}
	return &t, nil
}

// ReportInput : relecture de l'historique.
type ReportInput struct {
	ModelID   string `json:"model_id"`
	VersionID string `json:"version_id,omitempty" jsonschema:"limiter à une version du modèle"`
	TestID    string `json:"test_id,omitempty" jsonschema:"un test précis (défaut : le plus récent)"`
	JobID     string `json:"job_id,omitempty" jsonschema:"le test issu de ce travail de calcul"`
	Limit     int    `json:"limit,omitempty" jsonschema:"nombre de tests listés (défaut 10, max 50)"`
}

// ReportOutput : liste des tests d'un modèle et détail de l'un d'eux.
type ReportOutput struct {
	ModelID string        `json:"model_id"`
	Total   int           `json:"total"`
	Tests   []*TestRecord `json:"tests" jsonschema:"résumés, du plus récent au plus ancien"`
	Detail  *TestRecord   `json:"detail,omitempty"`
	PNG     []byte        `json:"-"`
}

// Report liste les tests d'un modèle et détaille le demandé (ou le plus récent).
func (s *Service) Report(ctx context.Context, in ReportInput) (*ReportOutput, error) {
	if in.TestID == "" && in.ModelID == "" && in.JobID == "" {
		return nil, errors.New("model_id (ou test_id, job_id) requis")
	}
	if in.Limit <= 0 {
		in.Limit = 10
	}
	if in.Limit > 50 {
		in.Limit = 50
	}
	out := &ReportOutput{ModelID: in.ModelID, Tests: []*TestRecord{}}
	q := `SELECT id, model_id, version_id, job_id, created_at, material, verdict, safety_factor, max_stress_mpa, max_disp_mm, assumed, input_json, result_json, png_sha256 FROM strength_tests WHERE 1=1`
	var args []any
	if in.ModelID != "" {
		q += ` AND model_id=?`
		args = append(args, in.ModelID)
	}
	if in.VersionID != "" {
		q += ` AND version_id=?`
		args = append(args, in.VersionID)
	}
	var total int
	_ = s.d.Store.DB().QueryRowContext(ctx, strings.Replace(q, "SELECT id, model_id, version_id, job_id, created_at, material, verdict, safety_factor, max_stress_mpa, max_disp_mm, assumed, input_json, result_json, png_sha256", "SELECT COUNT(*)", 1), args...).Scan(&total)
	out.Total = total
	rows, err := s.d.Store.DB().QueryContext(ctx, q+` ORDER BY created_at DESC, rowid DESC LIMIT ?`, append(args, in.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		t, err := s.scan(rows, false)
		if err != nil {
			return nil, err
		}
		out.Tests = append(out.Tests, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var want *TestRecord
	switch {
	case in.TestID != "":
		want, err = s.GetTest(ctx, in.TestID)
		if err != nil {
			return nil, err
		}
	case in.JobID != "":
		want, err = s.scan(s.d.Store.DB().QueryRowContext(ctx, `SELECT id, model_id, version_id, job_id, created_at, material, verdict, safety_factor,
			max_stress_mpa, max_disp_mm, assumed, input_json, result_json, png_sha256 FROM strength_tests WHERE job_id=?`, in.JobID), true)
		if err != nil {
			return nil, errors.New("aucun test pour ce travail (calcul encore en cours, ou échoué : get_job_status)")
		}
	case len(out.Tests) > 0:
		want, err = s.GetTest(ctx, out.Tests[0].ID)
		if err != nil {
			return nil, err
		}
	}
	if want != nil {
		out.Detail = want
		out.PNG = s.pngOf(want.PNGSHA256)
		if out.ModelID == "" {
			out.ModelID = want.ModelID
		}
	}
	return out, nil
}

// HasTest : un test existe déjà pour ce modèle (inutile de re-proposer).
func (s *Service) HasTest(ctx context.Context, modelID string) bool {
	var n int
	_ = s.d.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM strength_tests WHERE model_id=?`, modelID).Scan(&n)
	return n > 0
}
