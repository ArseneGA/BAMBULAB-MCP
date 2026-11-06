package printing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// SliceInput est l'entrée de execute_headless_slice.
type SliceInput struct {
	ModelID         string   `json:"model_id" jsonschema:"modèle à découper"`
	VersionID       string   `json:"version_id,omitempty" jsonschema:"révision du modèle (défaut : la plus récente)"`
	PrinterID       string   `json:"printer_id" jsonschema:"imprimante cible (modèle et buse déterminent le profil machine)"`
	ProfileRevision string   `json:"profile_revision,omitempty" jsonschema:"révision de profil (modify_slicing_profile / apply_variant) ; défaut : la variante appliquée au modèle, sinon les profils par défaut de la machine"`
	Process         string   `json:"process,omitempty" jsonschema:"preset process (ignoré avec une révision de profil)"`
	Filaments       []string `json:"filaments,omitempty" jsonschema:"presets filament, un par emplacement (ignoré avec une révision de profil)"`
	Plate           int      `json:"plate,omitempty" jsonschema:"plaque (défaut 1)"`
	IdempotencyKey  string   `json:"idempotency_key,omitempty" jsonschema:"un doublon de requête renvoie la même impression"`
}

var nameClean = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func cleanStem(s string) string {
	s = strings.TrimSuffix(s, filepath.Ext(s))
	s = strings.Trim(nameClean.ReplaceAllString(s, "_"), "_")
	if s == "" {
		return "model"
	}
	if len(s) > 48 {
		s = s[:48]
	}
	return s
}

// sourceFor choisit la version et le fichier découpable (STL/OBJ/3MF copié sur le stockage).
func (s *Service) sourceFor(ctx context.Context, modelID, versionID string) (contracts.ModelVersion, contracts.SourceFile, error) {
	var ver contracts.ModelVersion
	if _, err := s.d.Store.GetModel(ctx, modelID); err != nil {
		return ver, contracts.SourceFile{}, fmt.Errorf("%w : modèle %q", ErrNotFound, modelID)
	}
	vs, err := s.d.Store.ListVersions(ctx, modelID)
	if err != nil {
		return ver, contracts.SourceFile{}, err
	}
	files, err := s.d.Store.ListSourceFiles(ctx, modelID)
	if err != nil {
		return ver, contracts.SourceFile{}, err
	}
	byID := map[string]contracts.SourceFile{}
	for _, f := range files {
		byID[f.ID] = f
	}
	if versionID != "" {
		found := false
		for _, v := range vs {
			if v.ID == versionID {
				ver, found = v, true
			}
		}
		if !found {
			return ver, contracts.SourceFile{}, fmt.Errorf("%w : version %q du modèle %q", ErrNotFound, versionID, modelID)
		}
	} else if len(vs) > 0 {
		ver = vs[0]
	}
	pick := func(ids []string) (contracts.SourceFile, bool) {
		for _, id := range ids {
			f, ok := byID[id]
			if !ok || f.Availability != contracts.AvailableLinux {
				continue
			}
			switch f.Format {
			case contracts.FormatSTL, contracts.FormatOBJ, contracts.Format3MF:
				if s.d.Blobs.Has(f.SHA256) {
					return f, true
				}
			}
		}
		return contracts.SourceFile{}, false
	}
	if f, ok := pick(ver.SourceFileIDs); ok {
		return ver, f, nil
	}
	if versionID == "" {
		var ids []string
		for i := len(files) - 1; i >= 0; i-- {
			ids = append(ids, files[i].ID)
		}
		if f, ok := pick(ids); ok {
			return ver, f, nil
		}
	}
	return ver, contracts.SourceFile{}, fmt.Errorf("%w : aucun fichier STL/OBJ/3MF disponible sur le stockage pour %s (un STEP doit d'abord être converti en maillage)", ErrIncompatible, modelID)
}

// ExecuteHeadlessSlice crée l'impression (DRAFT -> PREPARED) et met la découpe en file.
func (s *Service) ExecuteHeadlessSlice(ctx context.Context, in SliceInput) (*Job, error) {
	if in.ModelID == "" || in.PrinterID == "" {
		return nil, invalidf("model_id et printer_id requis")
	}
	if in.Plate < 0 || in.Plate > 64 {
		return nil, invalidf("plaque hors plage")
	}
	if in.Plate == 0 {
		in.Plate = 1
	}
	rec, err := s.GetPrinterRecord(ctx, in.PrinterID)
	if err != nil {
		return nil, err
	}
	model, err := s.GetPrinterModel(ctx, rec.ModelID)
	if err != nil {
		return nil, err
	}
	ver, src, err := s.sourceFor(ctx, in.ModelID, in.VersionID)
	if err != nil {
		return nil, err
	}
	spec := &SliceSpec{Printer: model.Name, Nozzle: rec.NozzleMM, Process: in.Process, Filaments: in.Filaments,
		SourceSHA: src.SHA256, SourceName: src.Name, SourceFmt: string(src.Format), Plate: in.Plate}
	revID := in.ProfileRevision
	if revID == "" && s.d.Goals != nil {
		if applied, _ := s.d.Goals.AppliedFor(ctx, in.ModelID); applied != nil {
			revID = applied.ID
		}
	}
	if revID != "" {
		if s.d.Goals == nil {
			return nil, fmt.Errorf("%w : moteur d'objectifs absent", ErrUnavailable)
		}
		rev, err := s.d.Goals.GetProfileRevision(ctx, revID)
		if err != nil {
			return nil, fmt.Errorf("%w : révision de profil %q", ErrNotFound, revID)
		}
		spec.Machine, spec.Process, spec.Filaments, spec.Overrides, spec.Orientation = rev.Machine, rev.Process, rev.Filaments, rev.Overrides, rev.Orientation
	}
	name := in.ModelID
	if m, err := s.d.Store.GetModel(ctx, in.ModelID); err == nil {
		name = m.Name
	}
	j, err := s.createJob(ctx, Job{PrinterID: in.PrinterID, ModelID: in.ModelID, VersionID: ver.ID, ProfileRevision: revID,
		Plate: in.Plate, Name: cleanStem(name) + ".gcode.3mf", Slice: spec})
	if err != nil {
		return nil, err
	}
	if j, err = s.transition(ctx, j.ID, contracts.EvPrepare, "préparation de la découpe", nil); err != nil {
		return nil, err
	}
	params, _ := json.Marshal(map[string]string{"print_job_id": j.ID})
	ikey := ""
	if k := strings.TrimSpace(in.IdempotencyKey); k != "" {
		ikey = in.ModelID + ":" + in.PrinterID + ":slice:" + k // l'impression n'existe pas encore : portée modèle + imprimante
	}
	qj, created, err := s.d.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: JobSlice, Class: contracts.ClassHeavy, Params: params, IdempotencyKey: ikey})
	if err != nil {
		_, _ = s.transition(ctx, j.ID, contracts.EvCancel, "mise en file impossible : "+err.Error(), nil)
		return nil, err
	}
	if !created { // doublon : renvoyer l'impression d'origine, annuler la nôtre
		_, _ = s.transition(ctx, j.ID, contracts.EvCancel, "doublon de la clé d'idempotence "+in.IdempotencyKey, nil)
		var p struct {
			ID string `json:"print_job_id"`
		}
		_ = json.Unmarshal(qj.Params, &p)
		orig, err := s.GetJob(ctx, p.ID)
		if err == nil && (orig.ModelID != j.ModelID || orig.PrinterID != j.PrinterID || orig.VersionID != j.VersionID || orig.Plate != j.Plate || orig.ProfileRevision != j.ProfileRevision) {
			return nil, fmt.Errorf("%w : execute_headless_slice avec cette clé a déjà été demandé avec d'autres paramètres", ErrReplayParams)
		}
		return &orig, err
	}
	out, err := s.update(ctx, j.ID, func(x *Job) error { x.SliceJobID = qj.ID; return nil })
	return &out, err
}

func (s *Service) failJob(ctx context.Context, id, code string, cause error) {
	msg := code + " : " + cause.Error()
	if _, err := s.transition(ctx, id, contracts.EvFail, msg, func(j *Job) error { j.LastError = msg; return nil }); err != nil {
		s.log.Warn("impression non passée en échec", "print_job", id, "err", err)
	}
}

// sliceHandler : PREPARED -> SLICING -> VALIDATED (ou FAILED).
func (s *Service) sliceHandler(ctx context.Context, jc *jobs.JobCtx) (json.RawMessage, error) {
	var p struct {
		ID string `json:"print_job_id"`
	}
	if err := json.Unmarshal(jc.Job.Params, &p); err != nil {
		return nil, &jobs.HandlerError{Code: "bad_params", Err: err}
	}
	bg := context.Background()
	j, err := s.GetJob(bg, p.ID)
	if err != nil {
		return nil, &jobs.HandlerError{Code: "not_found", Err: err}
	}
	switch j.State {
	case contracts.PrintPrepared:
		if j, err = s.transition(bg, j.ID, contracts.EvStartSlice, "découpe démarrée", nil); err != nil {
			return nil, &jobs.HandlerError{Code: "state", Err: err}
		}
	case contracts.PrintSlicing: // reprise après interruption : on refait la découpe
	default:
		return nil, &jobs.HandlerError{Code: "state", Err: fmt.Errorf("%w : %s", ErrState, j.State)}
	}
	fail := func(code string, err error) (json.RawMessage, error) {
		if !errors.Is(err, slicer.ErrCanceled) && !errors.Is(err, context.Canceled) {
			s.failJob(bg, j.ID, code, err)
		}
		return nil, &jobs.HandlerError{Code: code, Err: err}
	}
	sl, err := s.slicerInst()
	if err != nil {
		return fail("slicer_missing", err)
	}
	spec := j.Slice
	if spec == nil {
		return fail("bad_job", errors.New("paramètres de découpe absents"))
	}
	jc.Progress(0.1, "préparation du modèle")
	f, err := s.d.Blobs.Open(spec.SourceSHA)
	if err != nil {
		return fail("model_unreadable", err)
	}
	data, err := readAll(f, 512<<20)
	f.Close()
	if err != nil {
		return fail("model_unreadable", err)
	}
	if got := mesh.SHA256Hex(data); got != spec.SourceSHA {
		return fail("model_corrupt", fmt.Errorf("empreinte %s ≠ %s", got, spec.SourceSHA))
	}
	work, err := os.MkdirTemp(s.workRoot(), "pj-")
	if err != nil {
		return fail("workdir", err)
	}
	defer os.RemoveAll(work)
	in := filepath.Join(work, cleanStem(spec.SourceName)+"."+spec.SourceFmt)
	var arrange *bool
	if spec.Orientation != nil {
		md, err := mesh.Parse(spec.SourceName, data)
		if err != nil {
			return fail("model_unreadable", err)
		}
		m := md.Combined().Transform(mesh.Matrix(spec.Orientation.Rotation))
		b := m.Bounds()
		m = m.Transform(mesh.Translation(-b.Min.X, -b.Min.Y, -b.Min.Z))
		stl, err := mesh.STLBytes(m)
		if err != nil {
			return fail("orientation", err)
		}
		in, data = filepath.Join(work, cleanStem(spec.SourceName)+"_oriente.stl"), stl
		t := true
		arrange = &t
	}
	if err := os.WriteFile(in, data, 0o644); err != nil {
		return fail("workdir", err)
	}
	jc.Progress(0.2, "découpe par Bambu Studio")
	req := slicer.Request{ID: cleanStem(j.ID), Label: strings.TrimSuffix(j.Name, ".gcode.3mf"), Model: in, Machine: spec.Machine,
		Process: spec.Process, Filaments: spec.Filaments, Plate: spec.Plate, Arrange: arrange, Overrides: spec.Overrides}
	if spec.Machine == "" {
		req.Printer, req.Nozzle = spec.Printer, spec.Nozzle
	}
	res, err := sl.Slice(ctx, req)
	if res != nil {
		defer res.Cleanup()
	}
	if err != nil {
		code := "slice_failed"
		switch {
		case errors.Is(err, slicer.ErrCanceled):
			code = "canceled"
		case errors.Is(err, slicer.ErrTimeout):
			code = "timeout"
		case errors.Is(err, slicer.ErrIncompatible):
			code = "incompatible"
		}
		return fail(code, err)
	}
	if res.Validation != nil && !res.Validation.OK {
		return fail("invalid_output", fmt.Errorf("validation de l'archive en échec : %v", res.Validation.Failed()))
	}
	jc.Progress(0.85, "conservation de l'artefact")
	af, err := os.Open(res.ArtifactPath)
	if err != nil {
		return fail("artifact", err)
	}
	sha, size, err := s.d.Blobs.Put(af, "")
	af.Close()
	if err != nil {
		return fail("artifact", err)
	}
	sum := &ArtifactSummary{Plates: res.Plates, StudioVersion: res.StudioVersion, MachinePreset: res.Machine.Name, Process: res.Process.Name}
	for _, fp := range res.Filaments {
		sum.Filaments = append(sum.Filaments, fp.Name)
	}
	if res.Archive != nil {
		sum.PrinterModel, sum.BedType, sum.Nozzles = res.Archive.PrinterModel, res.Archive.BedType, res.Archive.Nozzles
	}
	base := filepath.Base(res.ArtifactPath)
	_, err = s.transition(bg, j.ID, contracts.EvSliceOK, "artefact "+sha[:12]+" validé par l'archive", func(x *Job) error {
		x.ArtifactSHA256, x.ArtifactSize, x.Summary, x.Name = sha, size, sum, base
		return nil
	})
	if err != nil {
		return nil, &jobs.HandlerError{Code: "state", Err: err}
	}
	_ = s.d.Store.Audit(bg, actor, "print.sliced", "print_job", j.ID, map[string]any{"artifact": sha})
	jc.Progress(1, "terminé")
	out, _ := json.Marshal(map[string]any{"print_job_id": j.ID, "artifact_sha256": sha})
	return out, nil
}

func (s *Service) workRoot() string {
	d := filepath.Join(s.d.DataDir, "work", "printing")
	if s.d.DataDir == "" {
		d = filepath.Join(os.TempDir(), "bambu-printing")
	}
	_ = os.MkdirAll(d, 0o755)
	return d
}

func readAll(f *os.File, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("fichier trop volumineux (plus de %d octets)", max)
	}
	return b, nil
}

var _ = store.ErrNotFound
var _ = time.Second
