package goals

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Étiquettes de provenance des mesures.
const (
	LabelSlicer      = "estimation slicer"
	LabelGeometry    = "mesure géométrique"
	LabelEngineering = "calcul engineering"
	LabelGcode       = "lecture du G-code"
)

// Metric est une mesure étiquetée par sa provenance.
type Metric struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
	Label string  `json:"label" jsonschema:"provenance : estimation slicer, mesure géométrique, lecture du G-code"`
	Note  string  `json:"note,omitempty"`
}

// CandMetrics est ce qui a été réellement relevé pour un candidat.
type CandMetrics struct {
	Values        map[string]Metric `json:"values"`
	SupportUsed   bool              `json:"support_used"`
	Effective     map[string]any    `json:"effective_settings" jsonschema:"valeurs effectives des réglages touchés par les règles (process et filament)"`
	StudioVersion string            `json:"studio_version,omitempty"`
	Warnings      []string          `json:"warnings,omitempty"`
	Unavailable   []string          `json:"unavailable,omitempty" jsonschema:"mesures qui n'ont pas pu être lues, avec la raison"`
	DurationSec   float64           `json:"slice_duration_s,omitempty"`
	ProfileSHA    map[string]string `json:"effective_profile_sha256,omitempty"`
}

type sliceParams struct {
	ExperimentID string `json:"experiment_id"`
	CandidateID  string `json:"candidate_id"`
}

func (s *Service) setCandidate(ctx context.Context, id, state, errMsg string, m *CandMetrics, art string) {
	var mj any
	if m != nil {
		b, _ := json.Marshal(m)
		mj = string(b)
	}
	_, _ = s.cfg.Store.DB().ExecContext(ctx,
		`UPDATE experiment_candidates SET state=?, error=NULLIF(?,''), metrics=COALESCE(?,metrics), artifact_sha=COALESCE(NULLIF(?,''),artifact_sha), updated_at=? WHERE id=?`,
		state, errMsg, mj, art, store.FormatTime(s.now()), id)
}

func sanitizeName(s string) string {
	s = strings.TrimSuffix(s, filepath.Ext(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "model"
	}
	return b.String()
}

// sliceHandler exécute la découpe d'un candidat (travail lourd de la file).
func (s *Service) sliceHandler(ctx context.Context, jc *jobs.JobCtx) (json.RawMessage, error) {
	var p sliceParams
	if err := json.Unmarshal(jc.Job.Params, &p); err != nil {
		return nil, &jobs.HandlerError{Code: "bad_params", Err: err}
	}
	s.load()
	if s.sl == nil {
		cause := "Bambu Studio non installé ou non configuré"
		if s.slErr != nil {
			cause = s.slErr.Error()
		}
		s.setCandidate(context.Background(), p.CandidateID, "failed", "slicer_missing : "+cause, nil, "")
		return nil, &jobs.HandlerError{Code: "slicer_missing", Err: fmt.Errorf("%w: %s", ErrUnavailable, cause)}
	}
	exp, err := s.GetExperiment(ctx, p.ExperimentID)
	if err != nil {
		return nil, &jobs.HandlerError{Code: "not_found", Err: err}
	}
	var cand *CandidateView
	for i := range exp.Candidates {
		if exp.Candidates[i].ID == p.CandidateID {
			cand = &exp.Candidates[i]
		}
	}
	if cand == nil {
		return nil, &jobs.HandlerError{Code: "not_found", Err: fmt.Errorf("candidat %q introuvable", p.CandidateID)}
	}
	s.setCandidate(ctx, cand.ID, "running", "", nil, "")
	fail := func(code string, err error) (json.RawMessage, error) {
		st := "failed"
		if errors.Is(err, slicer.ErrCanceled) || errors.Is(err, context.Canceled) {
			st = "cancelled"
		}
		s.setCandidate(context.Background(), cand.ID, st, code+" : "+err.Error(), nil, "")
		return nil, &jobs.HandlerError{Code: code, Err: err}
	}
	if err := s.verifyDigests(exp.Details.Base); err != nil {
		return fail("profiles_changed", err)
	}
	jc.Progress(0.1, "préparation du modèle")
	work := filepath.Join(s.cfg.WorkRoot, sanitizeName(cand.ID))
	if err := os.MkdirAll(work, 0o755); err != nil {
		return fail("workdir", err)
	}
	defer os.RemoveAll(work)
	src := exp.Details.Source
	data, err := s.readBlob(src.SHA256)
	if err != nil {
		return fail("model_unreadable", err)
	}
	if got := shaHex(data); got != src.SHA256 {
		return fail("model_corrupt", fmt.Errorf("empreinte du modèle %s ≠ %s attendue", got, src.SHA256))
	}
	in := filepath.Join(work, sanitizeName(src.Name)+"."+src.Format)
	var arrange *bool
	if cand.Orientation != nil {
		md, err := mesh.Parse(src.Name, data)
		if err != nil {
			return fail("model_unreadable", err)
		}
		m := md.Combined().Transform(mesh.Matrix(cand.Orientation.Rotation))
		b := m.Bounds()
		m = m.Transform(mesh.Translation(-b.Min.X, -b.Min.Y, -b.Min.Z))
		stl, err := mesh.STLBytes(m)
		if err != nil {
			return fail("orientation", err)
		}
		in = filepath.Join(work, sanitizeName(src.Name)+"_oriente.stl")
		data = stl
		t := true
		arrange = &t
	}
	if err := os.WriteFile(in, data, 0o644); err != nil {
		return fail("workdir", err)
	}
	ovs := append(append([]slicer.Override{}, exp.Details.Base.Overrides...), cand.Overrides...)
	timeout := s.cfg.SliceTimeout
	if exp.Details.Budget.SliceTimeoutSeconds > 0 {
		timeout = time.Duration(exp.Details.Budget.SliceTimeoutSeconds) * time.Second
	}
	jc.Progress(0.2, "découpe par Bambu Studio")
	res, err := s.sl.Slice(ctx, slicer.Request{
		ID: sanitizeName(cand.ID), Label: sanitizeName(cand.ID), Model: in,
		Machine: exp.Details.Base.Machine, Process: exp.Details.Base.Process, Filaments: exp.Details.Base.Filaments,
		Arrange: arrange, Overrides: ovs, Timeout: timeout,
	})
	if res != nil && os.Getenv("BAMBU_KEEP_WORK") == "" {
		defer res.Cleanup()
	}
	if err != nil {
		code := "slice_failed"
		switch {
		case errors.Is(err, slicer.ErrCanceled):
			code = "canceled"
		case errors.Is(err, slicer.ErrTimeout):
			code = "timeout"
		case errors.Is(err, slicer.ErrOverride):
			code = "override_rejected"
		}
		return fail(code, err)
	}
	jc.Progress(0.8, "relevé des mesures")
	metrics := s.collectMetrics(res, exp, cand, ovs)
	art := ""
	if res.ArtifactPath != "" && s.cfg.Blobs != nil {
		if f, err := os.Open(res.ArtifactPath); err == nil {
			sha, _, perr := s.cfg.Blobs.Put(f, "")
			f.Close()
			if perr == nil {
				art = sha
			} else {
				metrics.Unavailable = append(metrics.Unavailable, "archive gcode.3mf non conservée : "+perr.Error())
			}
		}
	}
	s.setCandidate(context.Background(), cand.ID, "succeeded", "", metrics, art)
	_ = s.cfg.Store.Audit(context.Background(), actor, "candidate.sliced", "experiment", exp.ID, map[string]any{"candidate": cand.ID, "artifact": art})
	jc.Progress(1, "terminé")
	out, _ := json.Marshal(map[string]any{"candidate_id": cand.ID, "artifact_sha256": art, "metrics": metrics})
	return out, nil
}

// leverKeysFor liste les clés touchées par les règles, pour consigner les valeurs effectives.
func (s *Service) leverKeysFor() (process, filament []string) {
	k := s.rules.LeverKeys()
	return k["process"], k["filament"]
}

func (s *Service) collectMetrics(res *slicer.Result, exp *Experiment, cand *CandidateView, ovs []slicer.Override) *CandMetrics {
	m := &CandMetrics{Values: map[string]Metric{}, Effective: map[string]any{}, StudioVersion: res.StudioVersion,
		Warnings: res.Warnings, DurationSec: res.Duration.Seconds(), ProfileSHA: map[string]string{}}
	m.ProfileSHA["process"] = res.Process.SHA256
	m.ProfileSHA["machine"] = res.Machine.SHA256
	for i, f := range res.Filaments {
		m.ProfileSHA[fmt.Sprintf("filament%d", i+1)] = f.SHA256
	}
	if v := res.Validation; v != nil {
		for _, c := range v.Checks {
			if c.OK || c.Level != "warn" {
				continue
			}
			w := c.Name + " : " + c.Detail
			m.Warnings = append(m.Warnings, w)
		}
	}
	put := func(k string, v float64, unit, label, note string) {
		m.Values[k] = Metric{Value: round4(v), Unit: unit, Label: label, Note: note}
	}
	put("print_time_s", res.TotalSeconds, "s", LabelSlicer, "temps estimé par Bambu Studio, hors préchauffage et temps réels de machine")
	put("filament_g", res.TotalGrams, "g", LabelSlicer, "masse estimée par Bambu Studio")
	var layers int
	var lengthM, maxZ float64
	for _, p := range res.Plates {
		layers += p.Layers
		maxZ = math.Max(maxZ, p.MaxZ)
		for _, f := range p.Filaments {
			lengthM += f.Meters
		}
		if p.SupportUsed {
			m.SupportUsed = true
		}
	}
	put("layers", float64(layers), "couches", LabelSlicer, "")
	put("filament_m", lengthM, "m", LabelSlicer, "longueur de filament estimée")
	put("height_mm", maxZ, "mm", LabelGcode, "Z maximal du G-code")
	// volume de supports : lecture du G-code
	if sg, err := s.supportGrams(res, exp); err == nil {
		put("support_g", sg, "g", LabelGcode, "extrusion cumulée des sections « Support » du G-code (densité et diamètre du profil filament) ; ordre de grandeur")
	} else {
		m.Unavailable = append(m.Unavailable, "support_g : "+err.Error())
	}
	if cand.Orientation != nil {
		put("bed_contact_mm2", cand.Orientation.BedContactMM2, "mm²", LabelGeometry, "heuristique de internal/mesh")
		put("overhang_fraction", cand.Orientation.OverhangFraction, "", LabelGeometry, "part de la surface en surplomb au seuil de 45°, heuristique")
	}
	// valeurs effectives des réglages touchés par les règles
	if s.prof != nil {
		_, p, fs, _, err := slicer.ResolveWithOverrides(s.prof, exp.Details.Base.Machine, exp.Details.Base.Process, exp.Details.Base.Filaments, ovs)
		if err == nil {
			pk, fk := s.leverKeysFor()
			for _, k := range pk {
				if v, ok := p.Config[k]; ok {
					m.Effective["process."+k] = v
				}
			}
			for _, k := range fk {
				if v, ok := fs[0].Config[k]; ok {
					m.Effective["filament."+k] = v
				}
			}
			// adhérence et supports EFFECTIFS (défaut de Bambu Studio compris) : jamais absents du résumé
			for _, ek := range effectiveKeys {
				if _, have := m.Effective["process."+ek]; !have {
					v, _ := slicer.Effective(p, ek)
					m.Effective["process."+ek] = v
				}
			}
			if lh, _, ok := parseNum(firstStr(p.Config["layer_height"])); ok {
				put("layer_height_mm", lh, "mm", LabelSlicer, "réglage effectif, indicateur d'aspect de surface")
			}
		}
	}
	return m
}

func firstStr(v any) string {
	if l := strsOf(v); len(l) > 0 {
		return l[0]
	}
	return ""
}

func round4(f float64) float64 { return math.Round(f*10000) / 10000 }

var (
	featureRe = regexp.MustCompile(`^;\s*FEATURE:\s*(.+?)\s*$`)
	eRe       = regexp.MustCompile(`\sE(-?\d*\.?\d+)`)
)

// supportGrams somme l'extrusion des sections de support du G-code de la plaque 1.
func (s *Service) supportGrams(res *slicer.Result, exp *Experiment) (float64, error) {
	path := ""
	for _, c := range []string{"plate_1.gcode"} {
		cand := filepath.Join(res.WorkDir, "out", c)
		if _, err := os.Stat(cand); err == nil {
			path = cand
		}
	}
	if path == "" {
		return 0, errors.New("G-code de la plaque 1 introuvable dans le répertoire de découpe")
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	dia, dens := 1.75, 1.26
	if s.prof != nil {
		if rf, err := s.prof.Resolve(profiles.KindFilament, exp.Details.Base.Filaments[0]); err == nil {
			if v, _, ok := parseNum(firstStr(rf.Config["filament_diameter"])); ok && v > 0 {
				dia = v
			}
			if v, _, ok := parseNum(firstStr(rf.Config["filament_density"])); ok && v > 0 {
				dens = v
			}
		}
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	inSupport, relative := false, false
	var mm float64
	for sc.Scan() {
		line := sc.Text()
		if len(line) > 0 && line[0] == ';' {
			if m := featureRe.FindStringSubmatch(line); m != nil {
				inSupport = strings.HasPrefix(strings.ToLower(m[1]), "support")
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "M83"):
			relative = true
		case strings.HasPrefix(line, "M82"):
			relative = false
		}
		if !inSupport || !relative || !(strings.HasPrefix(line, "G1 ") || strings.HasPrefix(line, "G2 ") || strings.HasPrefix(line, "G3 ")) || !strings.Contains(line, " X") {
			continue
		}
		if m := eRe.FindStringSubmatch(line); m != nil {
			if e, err := strconv.ParseFloat(m[1], 64); err == nil && e > 0 {
				mm += e
			}
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	area := math.Pi * dia * dia / 4 // mm²
	return mm * area * dens / 1000, nil
}

var _ = contracts.JobFailed
