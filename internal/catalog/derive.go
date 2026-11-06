package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

const actorDerive = "derive"

// deriveParams : entrée des travaux mesh.analyze, mesh.preview et cad.step_import.
type deriveParams struct {
	// SHA256 : empreinte du fichier source (clé du produit dérivé).
	SHA256 string `json:"sha256"`
	// FromSHA256 : blob à lire si différent (STL issu d'un STEP).
	FromSHA256 string `json:"from_sha256,omitempty"`
	Name       string `json:"name"`
	Unit       string `json:"unit,omitempty"`
}

// AnalysisDoc est l'analyse stockée pour un fichier source. Ce sont des mesures
// géométriques, jamais une certification mécanique.
type AnalysisDoc struct {
	Report       *mesh.ModelReport `json:"report"`
	UnitOverride string            `json:"unit_override,omitempty"`
	ScaleApplied float64           `json:"scale_applied,omitempty"`
	FromSHA256   string            `json:"from_sha256,omitempty"`
	Level        string            `json:"level"`
	AnalyzedAt   time.Time         `json:"analyzed_at"`
}

// PreviewDoc décrit ce que le GLB représente réellement.
type PreviewDoc struct {
	SourceTriangles   int      `json:"source_triangles"`
	ExportedTriangles int      `json:"exported_triangles"`
	Decimated         bool     `json:"decimated"`
	SizeBytes         int64    `json:"size_bytes"`
	Note              string   `json:"note"`
	NotRepresented    []string `json:"not_represented,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
	UnitOverride      string   `json:"unit_override,omitempty"`
}

func (s *Service) loadMesh(p deriveParams) (*mesh.Model, float64, error) {
	sha := p.FromSHA256
	if sha == "" {
		sha = p.SHA256
	}
	f, err := s.d.Blobs.Open(sha)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, s.cfg.MaxFileBytes+1))
	if err != nil {
		return nil, 0, err
	}
	md, err := mesh.Parse(p.Name, data)
	if err != nil {
		return nil, 0, err
	}
	scale := 1.0
	if sc := unitScale(p.Unit); sc != 1 && md.UnitAssumed {
		for i := range md.Objects {
			if md.Objects[i].Mesh != nil {
				md.Objects[i].Mesh = md.Objects[i].Mesh.Transform(mesh.Scaling(sc))
			}
		}
		scale = sc
	}
	return md, scale, nil
}

// analyzeBlob lit, analyse et enregistre l'analyse (état « ok » ou « error »).
func (s *Service) analyzeBlob(ctx context.Context, p deriveParams) (*AnalysisDoc, error) {
	md, scale, err := s.loadMesh(p)
	if err != nil {
		_ = s.setDerived(ctx, p.SHA256, KindAnalysis, StateError, "", nil, "lecture/analyse impossible : "+err.Error())
		return nil, err
	}
	doc := &AnalysisDoc{Report: md.Analyze(mesh.AnalyzeOptions{}), UnitOverride: p.Unit, Level: "calculé", AnalyzedAt: s.d.Store.Now(), FromSHA256: p.FromSHA256}
	if scale != 1 {
		doc.ScaleApplied = scale
	}
	if err := s.setDerived(ctx, p.SHA256, KindAnalysis, StateOK, "", doc, ""); err != nil {
		return nil, err
	}
	if c := doc.Report.Combined; c != nil && len(md.Objects) > 0 {
		if err := s.updateDimensions(ctx, p.SHA256, contracts.Dimensions{X: c.BBox.Size.X, Y: c.BBox.Size.Y, Z: c.BBox.Size.Z}); err != nil {
			s.d.Log.Warn("dimensions non mises à jour", "err", err)
		}
	}
	return doc, nil
}

// updateDimensions met à jour les dimensions des modèles dont le fichier le plus récent a cette empreinte.
func (s *Service) updateDimensions(ctx context.Context, sha string, d contracts.Dimensions) error {
	files, err := queryFiles(ctx, s.db(), `WHERE sha256=?`, sha)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	raw, _ := json.Marshal(d)
	for _, f := range files {
		if seen[f.ModelID] {
			continue
		}
		seen[f.ModelID] = true
		lf, ok, err := currentFile(ctx, s.db(), f.ModelID)
		if err != nil {
			return err
		}
		if ok && lf.SHA256 == sha {
			if _, err := s.db().ExecContext(ctx, `UPDATE models SET dimensions=? WHERE id=?`, string(raw), f.ModelID); err != nil {
				return err
			}
		}
	}
	return nil
}

// previewBlob produit le GLB dérivé (stocké comme blob dérivé, jamais à la place de l'original).
func (s *Service) previewBlob(ctx context.Context, p deriveParams) (*PreviewDoc, string, error) {
	md, _, err := s.loadMesh(p)
	if err != nil {
		_ = s.setDerived(ctx, p.SHA256, KindPreview, StateError, "", nil, "lecture impossible : "+err.Error())
		return nil, "", err
	}
	res, err := md.GLB(mesh.GLBOptions{MaxTriangles: s.cfg.PreviewMaxTriangles})
	if err != nil {
		_ = s.setDerived(ctx, p.SHA256, KindPreview, StateError, "", nil, "export GLB impossible : "+err.Error())
		return nil, "", err
	}
	sha, size, err := s.d.Blobs.Put(bytes.NewReader(res.Data), res.SHA256)
	if err != nil {
		return nil, "", err
	}
	doc := &PreviewDoc{SourceTriangles: res.SourceTriangles, ExportedTriangles: res.ExportedTriangles, Decimated: res.Decimated,
		SizeBytes: size, Note: res.Note, Warnings: md.Warnings, UnitOverride: p.Unit}
	doc.NotRepresented = append(doc.NotRepresented, md.Extras...)
	if md.Format == "3mf" {
		doc.NotRepresented = append(doc.NotRepresented, "matériaux, couleurs, plateaux et réglages du 3MF : le GLB ne montre que la géométrie")
	}
	if err := s.setDerived(ctx, p.SHA256, KindPreview, StateOK, sha, doc, ""); err != nil {
		return nil, "", err
	}
	return doc, sha, nil
}

func (s *Service) runAnalyzeJob(ctx context.Context, jc *jobs.JobCtx) (json.RawMessage, error) {
	var p deriveParams
	if err := json.Unmarshal(jc.Job.Params, &p); err != nil {
		return nil, &jobs.HandlerError{Code: "bad_params", Err: err}
	}
	doc, err := s.analyzeBlob(ctx, p)
	if err != nil {
		return nil, &jobs.HandlerError{Code: "mesh_error", Err: err}
	}
	c := doc.Report.Combined
	return json.Marshal(map[string]any{"sha256": p.SHA256, "triangles": c.Triangles, "watertight": c.Watertight, "warnings": len(doc.Report.Warnings)})
}

func (s *Service) runPreviewJob(ctx context.Context, jc *jobs.JobCtx) (json.RawMessage, error) {
	var p deriveParams
	if err := json.Unmarshal(jc.Job.Params, &p); err != nil {
		return nil, &jobs.HandlerError{Code: "bad_params", Err: err}
	}
	doc, sha, err := s.previewBlob(ctx, p)
	if err != nil {
		return nil, &jobs.HandlerError{Code: "mesh_error", Err: err}
	}
	return json.Marshal(map[string]any{"sha256": p.SHA256, "glb_sha256": sha, "decimated": doc.Decimated})
}

// enqueueDerive met un travail dérivé en file. La clé d'idempotence évite les doublons.
func (s *Service) enqueueDerive(ctx context.Context, kind string, class contracts.JobClass, p deriveParams, force bool) (contracts.Job, error) {
	key := fmt.Sprintf("%s:%s:%s:%s", kind, p.SHA256, p.FromSHA256, p.Unit)
	if force {
		key += ":" + contracts.NewID("f")
	}
	params, _ := json.Marshal(p)
	j, _, err := s.d.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: kind, Class: class, Params: params, IdempotencyKey: key})
	return j, err
}

// ensureDerived met en file ce qui manque pour un fichier copié : analyse + aperçu
// (maillages) ou import STEP. Idempotent : sert aussi à la reprise après interruption.
func (s *Service) ensureDerived(ctx context.Context, f contracts.SourceFile) {
	if f.Availability != contracts.AvailableLinux || !s.d.Blobs.Has(f.SHA256) {
		return
	}
	p := deriveParams{SHA256: f.SHA256, Name: f.Name, Unit: f.Unit}
	need := func(kind string) bool {
		d, ok, err := getDerived(ctx, s.db(), f.SHA256, kind)
		if err != nil {
			return false
		}
		return !ok || d.State == StatePending
	}
	enqueue := func(derivedKind, jobKind string, class contracts.JobClass) {
		if !need(derivedKind) {
			return
		}
		if _, err := s.markPending(ctx, f.SHA256, derivedKind); err != nil {
			s.d.Log.Warn("état en attente non enregistré", "err", err)
			return
		}
		if _, err := s.enqueueDerive(ctx, jobKind, class, p, false); err != nil {
			s.d.Log.Warn("travail dérivé non mis en file", "kind", jobKind, "err", err)
		}
	}
	if f.Format == contracts.FormatSTEP {
		enqueue(KindStepSTL, JobStepImport, contracts.ClassHeavy)
		return
	}
	enqueue(KindAnalysis, JobAnalyze, contracts.ClassLight)
	enqueue(KindPreview, JobPreview, contracts.ClassLight)
}

// ---- worker géométrie (STEP) ----

// WorkerError : erreur métier renvoyée par le worker Python.
type WorkerError struct {
	Code    string
	Message string
}

func (e *WorkerError) Error() string { return e.Code + " : " + e.Message }

// WorkerFunc exécute une commande du worker géométrie et renvoie son champ result.
type WorkerFunc func(ctx context.Context, command string, req map[string]any) (json.RawMessage, error)

func (s *Service) worker() WorkerFunc {
	if s.cfg.Worker != nil {
		return s.cfg.Worker
	}
	return s.execWorker
}

// CADStatus dit si le backend CAO paraît disponible (uv et projet worker trouvés).
// La présence de CadQuery/OCP n'est constatée qu'à l'exécution.
func (s *Service) CADStatus() (available bool, note string) {
	if s.cfg.Worker != nil {
		return true, "worker injecté"
	}
	uv, dir, err := s.stepBackend()
	if err != nil {
		return false, err.Error()
	}
	return true, fmt.Sprintf("uv=%s, projet=%s (CadQuery/OCP vérifiés à l'exécution)", uv, dir)
}

func (s *Service) runStepJob(ctx context.Context, jc *jobs.JobCtx) (json.RawMessage, error) {
	var p deriveParams
	if err := json.Unmarshal(jc.Job.Params, &p); err != nil {
		return nil, &jobs.HandlerError{Code: "bad_params", Err: err}
	}
	markMissing := func(note string) error {
		_ = s.setDerived(context.Background(), p.SHA256, KindStepSTL, StateBackendMissing, "", nil, note)
		_ = s.setDerived(context.Background(), p.SHA256, KindAnalysis, StateBackendMissing, "", nil, note)
		_ = s.setDerived(context.Background(), p.SHA256, KindPreview, StateBackendMissing, "", nil, note)
		return &jobs.HandlerError{Code: "cad_backend_missing", Err: errors.New(note)}
	}
	if s.cfg.Worker == nil {
		if _, _, err := s.stepBackend(); err != nil {
			return nil, markMissing("backend CAO absent : " + err.Error())
		}
	}
	tmpRoot := filepath.Join(s.d.DataDir, "tmp")
	_ = os.MkdirAll(tmpRoot, 0o755)
	tmp, err := os.MkdirTemp(tmpRoot, "step-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	in := filepath.Join(tmp, "input.step")
	src, err := s.d.Blobs.Open(p.SHA256)
	if err != nil {
		return nil, &jobs.HandlerError{Code: "blob_missing", Err: err}
	}
	dst, err := os.Create(in)
	if err != nil {
		src.Close()
		return nil, err
	}
	_, err = io.Copy(dst, src)
	src.Close()
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	out := filepath.Join(tmp, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	jc.Progress(0.1, "tessellation STEP (worker géométrie)")
	raw, err := s.worker()(ctx, "step_import", map[string]any{"path": in, "out_dir": out})
	if err != nil {
		var we *WorkerError
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case errors.Is(err, ErrBackendMissing):
			return nil, markMissing("backend CAO absent : " + err.Error())
		case errors.As(err, &we) && (we.Code == "step_backend_missing" || we.Code == "cad_backend_missing"):
			return nil, markMissing("backend CAO absent : " + we.Message)
		}
		_ = s.setDerived(context.Background(), p.SHA256, KindStepSTL, StateError, "", nil, err.Error())
		code := "cad_failure"
		if errors.As(err, &we) {
			code = we.Code
		}
		return nil, &jobs.HandlerError{Code: code, Err: err}
	}
	var res struct {
		Files struct {
			STL string `json:"stl"`
			TMF string `json:"3mf"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.Files.STL == "" {
		_ = s.setDerived(context.Background(), p.SHA256, KindStepSTL, StateError, "", nil, "réponse du worker illisible")
		return nil, &jobs.HandlerError{Code: "cad_protocol", Err: fmt.Errorf("réponse du worker sans fichier STL")}
	}
	put := func(p string) (string, error) {
		if !filepath.IsAbs(p) {
			p = filepath.Join(out, p)
		}
		if !within(tmp, p) {
			return "", fmt.Errorf("fichier du worker hors du dossier de travail : %s", p)
		}
		f, err := os.Open(p)
		if err != nil {
			return "", err
		}
		defer f.Close()
		sha, _, err := s.d.Blobs.Put(f, "")
		return sha, err
	}
	stlSHA, err := put(res.Files.STL)
	if err != nil {
		_ = s.setDerived(context.Background(), p.SHA256, KindStepSTL, StateError, "", nil, err.Error())
		return nil, &jobs.HandlerError{Code: "cad_output", Err: err}
	}
	if err := s.setDerived(ctx, p.SHA256, KindStepSTL, StateOK, stlSHA, json.RawMessage(raw), ""); err != nil {
		return nil, err
	}
	if res.Files.TMF != "" {
		if tsha, err := put(res.Files.TMF); err == nil {
			_ = s.setDerived(ctx, p.SHA256, KindStep3MF, StateOK, tsha, nil, "")
		} else {
			jc.Logf("3MF dérivé ignoré : %v", err)
		}
	}
	// Analyse et aperçu du STL dérivé, enregistrés sous l'empreinte du STEP d'origine.
	dp := deriveParams{SHA256: p.SHA256, FromSHA256: stlSHA, Name: "derived.stl", Unit: ""}
	for _, k := range []string{KindAnalysis, KindPreview} {
		_, _ = s.markPending(ctx, p.SHA256, k)
	}
	if _, err := s.enqueueDerive(ctx, JobAnalyze, contracts.ClassLight, dp, false); err != nil {
		jc.Logf("analyse du STL dérivé non mise en file : %v", err)
	}
	if _, err := s.enqueueDerive(ctx, JobPreview, contracts.ClassLight, dp, false); err != nil {
		jc.Logf("aperçu du STL dérivé non mis en file : %v", err)
	}
	return json.Marshal(map[string]any{"sha256": p.SHA256, "stl_sha256": stlSHA})
}
