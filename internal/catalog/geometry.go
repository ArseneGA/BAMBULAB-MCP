package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

func openRegular(p string) (*os.File, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, invalidf("%q n'est pas un fichier", p)
	}
	return f, nil
}

// resolveFile désigne le fichier visé : fichier explicite, sinon le plus récent du modèle.
func (s *Service) resolveFile(ctx context.Context, modelID, fileID string) (contracts.Model, contracts.SourceFile, error) {
	var m contracts.Model
	switch {
	case fileID != "":
		f, err := getFile(ctx, s.db(), fileID)
		if err != nil {
			return m, f, err
		}
		m, err = getModel(ctx, s.db(), f.ModelID)
		return m, f, err
	case modelID != "":
		m, err := getModel(ctx, s.db(), modelID)
		if err != nil {
			return m, contracts.SourceFile{}, err
		}
		lf, ok, err := currentFile(ctx, s.db(), modelID)
		if err != nil {
			return m, contracts.SourceFile{}, err
		}
		if !ok {
			return m, lf, invalidf("le modèle %s n'a aucun fichier", modelID)
		}
		return m, lf, nil
	}
	return m, contracts.SourceFile{}, invalidf("model_id ou file_id requis")
}

// ---- get_preview_asset ----

// PreviewAssetInput désigne le fichier dont on veut l'aperçu.
type PreviewAssetInput struct {
	ModelID string `json:"model_id,omitempty" jsonschema:"modèle (son fichier le plus récent)"`
	FileID  string `json:"file_id,omitempty" jsonschema:"fichier source précis"`
}

// PreviewAsset : référence à l'aperçu, sans le contenu (pas de base64 géant).
type PreviewAsset struct {
	ModelID   string     `json:"model_id"`
	FileID    string     `json:"file_id"`
	Preview   PreviewRef `json:"preview"`
	MIME      string     `json:"mime,omitempty"`
	SizeBytes int64      `json:"size_bytes,omitempty"`
	Triangles int        `json:"triangles,omitempty"`
	Decimated bool       `json:"decimated,omitempty" jsonschema:"l'aperçu est simplifié : il ne sert jamais à fabriquer"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Note      string     `json:"note"`
}

// GetPreviewAsset renvoie la référence de l'aperçu GLB et une URL HTTP locale signée.
func (s *Service) GetPreviewAsset(ctx context.Context, in PreviewAssetInput) (*PreviewAsset, error) {
	m, f, err := s.resolveFile(ctx, in.ModelID, in.FileID)
	if err != nil {
		return nil, err
	}
	out := &PreviewAsset{ModelID: m.ID, FileID: f.ID}
	ref, err := s.previewRef(ctx, f)
	if err != nil {
		return nil, err
	}
	if ref.State == PreviewNone {
		s.ensureDerived(ctx, f)
		if ref, err = s.previewRef(ctx, f); err != nil {
			return nil, err
		}
	}
	out.Preview = ref
	switch ref.State {
	case PreviewReady:
		d, _, err := getDerived(ctx, s.db(), f.SHA256, KindPreview)
		if err != nil {
			return nil, err
		}
		var pd PreviewDoc
		_ = json.Unmarshal(d.Doc, &pd)
		_, exp := s.SignedAssetURL(d.BlobSHA)
		out.MIME, out.SizeBytes, out.Triangles, out.Decimated, out.ExpiresAt = "model/gltf-binary", pd.SizeBytes, pd.ExportedTriangles, pd.Decimated, &exp
		out.Note = "Aperçu de visualisation uniquement (GLB dérivé) ; l'original reste la référence de fabrication. " + pd.Note
		if len(pd.NotRepresented) > 0 {
			out.Note += " Non représenté : " + strings.Join(pd.NotRepresented, " ; ") + "."
		}
	case PreviewPending:
		out.Note = "L'aperçu est en cours de calcul : réessayer dans quelques secondes."
	case PreviewWindowsOnly:
		out.Note = "Fichier disponible uniquement sur Windows : importer avec copie pour obtenir un aperçu."
	case PreviewBackendMissing:
		out.Note = "Backend CAO absent : l'aperçu d'un fichier STEP nécessite uv et le worker géométrie (voir get_capabilities)."
	default:
		out.Note = "Aucun aperçu disponible. " + ref.Note
	}
	return out, nil
}

// ---- analyze_geometry ----

// AnalyzeInput : analyse géométrique d'un modèle ou d'un fichier.
type AnalyzeInput struct {
	ModelID     string  `json:"model_id,omitempty" jsonschema:"modèle (son fichier le plus récent)"`
	FileID      string  `json:"file_id,omitempty" jsonschema:"fichier source précis"`
	Unit        string  `json:"unit,omitempty" jsonschema:"unité confirmée des coordonnées pour un STL/OBJ : mm | cm | m | inch ; mémorisée sur le fichier et force le recalcul"`
	Machine     string  `json:"machine,omitempty" jsonschema:"cible machine pour la fabricabilité : tout modèle Bambu des profils installés (p1s par défaut, h2d, h2s, x1e, p2s, a1, a1mini, x1c…) ; volume, buse et zones d'exclusion lus dans les profils Bambu Studio (double buse : par buse), repli nominal signalé si les profils sont absents"`
	Nozzle      float64 `json:"nozzle_mm,omitempty" jsonschema:"diamètre de buse en mm (défaut 0.4)"`
	Process     string  `json:"process,omitempty" jsonschema:"preset process Bambu dont lire support_threshold_angle (seuil de surplomb) ; défaut : process par défaut de la machine"`
	OverhangDeg float64 `json:"overhang_angle_deg,omitempty" jsonschema:"seuil de surplomb explicite, en degrés depuis la verticale (0 = paroi, 90 = plafond) ; prime sur le profil"`
	Recompute   bool    `json:"recompute,omitempty" jsonschema:"ignorer l'analyse stockée"`
	SkipFab     bool    `json:"skip_fabricability,omitempty" jsonschema:"ne pas calculer surplombs / épaisseurs / orientations (heuristiques)"`
}

// AnalyzeOutput : mesures géométriques et heuristiques de fabricabilité.
type AnalyzeOutput struct {
	ModelID       string          `json:"model_id"`
	FileID        string          `json:"file_id"`
	FileName      string          `json:"file_name"`
	State         string          `json:"state" jsonschema:"ok | pending | backend_missing | error"`
	Note          string          `json:"note,omitempty"`
	Cached        bool            `json:"cached,omitempty"`
	UnitApplied   string          `json:"unit_applied,omitempty"`
	Summary       AnalysisSummary `json:"summary"`
	Analysis      json.RawMessage `json:"analysis,omitempty" jsonschema:"rapport complet : par objet et ensemble (mesures géométriques, niveau « calculé »)"`
	Machine       string          `json:"machine,omitempty"`
	Fabricability json.RawMessage `json:"fabricability,omitempty" jsonschema:"surplombs, épaisseurs, orientations : HEURISTIQUES, jamais une garantie d'imprimabilité ni de résistance"`
	Caveats       []string        `json:"caveats"`
}

// AnalyzeGeometry renvoie l'analyse (stockée ou recalculée) et la fabricabilité heuristique.
func (s *Service) AnalyzeGeometry(ctx context.Context, in AnalyzeInput) (*AnalyzeOutput, error) {
	target, machine, err := s.machineTarget(in.Machine, in.Nozzle)
	if err != nil {
		return nil, err
	}
	unit, err := normalizeUnit(in.Unit)
	if err != nil {
		return nil, err
	}
	m, f, err := s.resolveFile(ctx, in.ModelID, in.FileID)
	if err != nil {
		return nil, err
	}
	out := &AnalyzeOutput{ModelID: m.ID, FileID: f.ID, FileName: f.Name, Machine: machine, Caveats: []string{}}
	if f.Availability != contracts.AvailableLinux {
		out.State, out.Note = StateError, "fichier disponible uniquement sur Windows (non copié) : importer avec copie pour l'analyser"
		out.Summary = AnalysisSummary{State: "none", Note: out.Note}
		return out, nil
	}
	p := deriveParams{SHA256: f.SHA256, Name: f.Name, Unit: f.Unit}
	if f.Format == contracts.FormatSTEP {
		sd, ok, err := getDerived(ctx, s.db(), f.SHA256, KindStepSTL)
		if err != nil {
			return nil, err
		}
		switch {
		case !ok:
			s.ensureDerived(ctx, f)
			out.State, out.Note = StatePending, "import STEP en file : réessayer plus tard (get_job_status)"
			out.Summary = AnalysisSummary{State: StatePending}
			return out, nil
		case sd.State == StateBackendMissing:
			out.State, out.Note = StateBackendMissing, "backend CAO absent : "+sd.Note
			out.Summary = AnalysisSummary{State: StateBackendMissing, Note: sd.Note}
			return out, nil
		case sd.State != StateOK:
			out.State, out.Note = sd.State, sd.Note
			out.Summary = AnalysisSummary{State: sd.State, Note: sd.Note}
			return out, nil
		}
		p.FromSHA256, p.Name, p.Unit = sd.BlobSHA, "derived.stl", ""
		out.Caveats = append(out.Caveats, "STEP : l'analyse porte sur le maillage tessellé issu du worker CAO (tolérance contrôlée), pas sur la géométrie exacte")
	} else if unit != "" && unit != f.Unit {
		if err := s.setFileUnit(ctx, f, unit); err != nil {
			return nil, err
		}
		p.Unit, in.Recompute = unit, true
		// L'aperçu doit suivre l'échelle : recalcul en arrière-plan sous une nouvelle clé.
		_ = s.setDerived(ctx, f.SHA256, KindPreview, StatePending, "", nil, "")
		_, _ = s.enqueueDerive(ctx, JobPreview, contracts.ClassLight, p, true)
	}
	out.UnitApplied = p.Unit

	var doc AnalysisDoc
	d, ok, err := getDerived(ctx, s.db(), f.SHA256, KindAnalysis)
	if err != nil {
		return nil, err
	}
	if ok && d.State == StateOK && !in.Recompute && json.Unmarshal(d.Doc, &doc) == nil && doc.Report != nil {
		out.Cached = true
	} else {
		fresh, err := s.analyzeBlob(ctx, p)
		if err != nil {
			out.State, out.Note = StateError, "analyse impossible : "+err.Error()
			out.Summary = AnalysisSummary{State: StateError, Note: out.Note}
			return out, nil
		}
		doc = *fresh
	}
	out.State = StateOK
	out.Analysis, _ = json.Marshal(doc.Report)
	if sum, err := s.analysisSummary(ctx, f); err == nil {
		out.Summary = sum
	}
	if doc.Report.UnitAssumed {
		out.Caveats = append(out.Caveats, doc.Report.UnitNote)
	}
	out.Caveats = append(out.Caveats, "Les mesures (dimensions, volume, arêtes ouvertes) sont géométriques ; la résistance mécanique ne se déduit pas d'un maillage.")
	if !in.SkipFab {
		md, _, err := s.loadMesh(p)
		if err == nil {
			fo := mesh.FabOptions{Target: target, OverhangAngleDeg: in.OverhangDeg}
			if note := s.applyProcessThreshold(&fo, target, in.Process); note != "" {
				out.Caveats = append(out.Caveats, note)
			}
			rep, ferr := mesh.Fabricability(md.Combined(), fo)
			if ferr == nil {
				out.Fabricability, _ = json.Marshal(rep)
				out.Caveats = append(out.Caveats, fmt.Sprintf("Fabricabilité = heuristiques (surplombs, épaisseur par rayons, orientations) pour %s (volumes : %s).", target.Name, target.Source))
			} else {
				out.Caveats = append(out.Caveats, "fabricabilité non calculée : "+ferr.Error())
			}
		}
	}
	return out, nil
}

// setFileUnit mémorise l'unité confirmée sur un fichier (annotation, journalisée).
func (s *Service) setFileUnit(ctx context.Context, f contracts.SourceFile, unit string) error {
	return s.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE source_files SET unit=? WHERE id=?`, unit, f.ID); err != nil {
			return err
		}
		return s.d.Store.AuditTx(ctx, tx, "mcp", "source_file.set_unit", "source_file", f.ID, map[string]any{"before": f.Unit, "after": unit})
	})
}

// ---- import_model ----

// ImportModelInput : import d'un seul fichier avec rapport.
type ImportModelInput struct {
	Path       string   `json:"path" jsonschema:"fichier absolu sous une racine d'import autorisée"`
	Units      string   `json:"units,omitempty" jsonschema:"unité des coordonnées si le fichier n'en porte pas : mm | cm | m | inch"`
	Name       string   `json:"name,omitempty" jsonschema:"nom du modèle (défaut : nom du fichier)"`
	Collection string   `json:"collection,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	License    string   `json:"license,omitempty"`
	Source     string   `json:"source,omitempty"`
	WaitSecs   int      `json:"wait_seconds,omitempty" jsonschema:"attendre la fin de l'import jusqu'à ce délai (défaut 30, max 120)"`
}

// ImportModelOutput : identifiant du modèle et rapport d'import.
type ImportModelOutput struct {
	JobID    string           `json:"job_id"`
	State    string           `json:"state"`
	Done     bool             `json:"done"`
	File     *FileResult      `json:"file,omitempty"`
	Analysis *AnalysisSummary `json:"analysis,omitempty"`
	Note     string           `json:"note"`
}

// ImportModel importe un fichier (copie vérifiée) et attend son rapport.
func (s *Service) ImportModel(ctx context.Context, in ImportModelInput) (*ImportModelOutput, error) {
	abs, _, err := s.authorize(in.Path)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(abs); err != nil || st.IsDir() {
		return nil, invalidf("import_model attend un fichier ; utiliser import_files pour un dossier")
	}
	j, err := s.StartImport(ctx, ImportInput{Paths: []string{abs}, Unit: in.Units, Name: in.Name, Collection: in.Collection,
		Tags: in.Tags, License: in.License, Source: in.Source})
	if err != nil {
		return nil, err
	}
	wait := in.WaitSecs
	if wait <= 0 {
		wait = 30
	}
	if wait > 120 {
		wait = 120
	}
	state, done, err := s.WaitJob(ctx, j.ID, time.Duration(wait)*time.Second)
	if err != nil {
		return nil, err
	}
	out := &ImportModelOutput{JobID: j.ID, State: state, Done: done}
	if !done {
		out.Note = "Import en cours : suivre avec get_job_status(job_id)."
		return out, nil
	}
	fin, err := s.d.Queue.Get(ctx, j.ID)
	if err != nil {
		return nil, err
	}
	if fin.State != contracts.JobSucceeded {
		if fin.Error != nil {
			out.Note = fin.Error.Code + " : " + fin.Error.Message
		}
		return out, nil
	}
	var rep ImportReport
	if json.Unmarshal(fin.Result, &rep) == nil && len(rep.Files) > 0 {
		out.File = &rep.Files[0]
		out.Note = "Import terminé (" + rep.Files[0].Status + "). L'analyse et l'aperçu GLB sont calculés en arrière-plan : get_model_details pour suivre."
		if rep.Files[0].ModelID != "" {
			if d, err := s.GetModelDetails(ctx, rep.Files[0].ModelID); err == nil {
				out.Analysis = &d.Analysis
			}
		}
	}
	return out, nil
}
