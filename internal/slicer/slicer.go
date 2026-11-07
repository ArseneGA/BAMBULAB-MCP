// Package slicer pilote la ligne de commande de Bambu Studio : localisation du
// binaire, écriture de profils complets dans un répertoire de travail isolé,
// lancement sans shell avec timeout et arrêt de l'arbre de processus,
// lecture de result.json, validation de l'archive .gcode.3mf produite.
package slicer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

// Config configure un Slicer.
type Config struct {
	BinaryPath string          // vide : détection (Locate)
	Profiles   *profiles.Store // vide : profils de l'installation du binaire
	WorkRoot   string          // parent des répertoires de travail ; défaut <tmp>/bambu-slicer
	Timeout    time.Duration   // défaut 10 min
	MaxLog     int             // octets conservés par flux (stdout/stderr) ; défaut 256 Kio
	// Wrapper : commande préfixe (ex. ["xvfb-run","-a"] sous Linux headless).
	Wrapper []string
}

// Slicer lance Bambu Studio. Sûr pour un usage concurrent : chaque travail a son répertoire.
type Slicer struct {
	bin      *Binary
	profiles *profiles.Store
	cfg      Config
	trust    *Trust
}

// New localise le binaire et charge les profils.
func New(cfg Config) (*Slicer, error) {
	bin, err := Locate(cfg.BinaryPath)
	if err != nil {
		return nil, err
	}
	st := cfg.Profiles
	if st == nil {
		d := bin.ProfilesDir()
		if d == "" {
			return nil, fmt.Errorf("slicer: profils introuvables près de %s, fournir Config.Profiles", bin.Path)
		}
		if st, err = profiles.Load(d); err != nil {
			return nil, err
		}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Minute
	}
	if cfg.MaxLog <= 0 {
		cfg.MaxLog = 256 << 10
	}
	if cfg.WorkRoot == "" {
		cfg.WorkRoot = filepath.Join(os.TempDir(), "bambu-slicer")
	}
	return &Slicer{bin: bin, profiles: st, cfg: cfg, trust: NewTrust(st)}, nil
}

// Binary retourne l'installation utilisée.
func (s *Slicer) Binary() *Binary { return s.bin }

// Profiles retourne le catalogue de profils.
func (s *Slicer) Profiles() *profiles.Store { return s.profiles }

// Request décrit un travail de slicing.
type Request struct {
	ID    string // identifiant du travail (nom du répertoire) ; généré si vide
	Label string // nom lisible, sert de base au nom de l'artefact

	Model string // chemin du modèle .stl, .obj ou .3mf (copié, jamais modifié)

	// Machine : nom exact du preset machine ("Bambu Lab P1S 0.4 nozzle"). À
	// défaut, Printer ("Bambu Lab P1S") + Nozzle (mm, défaut 0.4).
	Machine string
	Printer string
	Nozzle  float64

	Process   string   // preset process ; défaut : default_print_profile de la machine
	Filaments []string // presets filament, un par emplacement ; défaut : default_filament_profile

	Plate   int    // plaque à trancher (1 par défaut ; 0 non supporté ici, voir SliceAll)
	Arrange *bool  // nil : pas d'option ; true/false : --arrange 1/0
	Orient  bool   // --orient
	BedType string // "Cool Plate", "Textured PEI Plate"… ; défaut : plateau par défaut du modèle d'imprimante

	Overrides []Override
	Timeout   time.Duration // défaut : Config.Timeout

	// Objects : projet multi-objets (plusieurs pièces, plaques et réglages par objet). Quand il est
	// renseigné, Model est ignoré : le 3MF est écrit dans le répertoire de travail une fois les
	// profils résolus (Metadata/project_settings.config en dérive). PlateOrigin donne le décalage
	// monde de la plaque n ; défaut : plaques côte à côte, écart de 20 % de la largeur du plateau.
	Objects     []mesh.ProjectObject
	PlateOrigin func(plate int) (x, y float64)
	// ArrangeOnly : pas de découpe ; Studio dispose les objets (--arrange 1, répartition automatique sur
	// plusieurs plaques) et exporte le projet 3MF disposé (ArtifactPath). Voir Slicer.Arrange.
	ArrangeOnly bool
}

// SliceAll vaut Request.Plate pour trancher toutes les plaques (--slice 0).
const SliceAll = -1

// EffectiveProfile documente un profil effectivement utilisé.
type EffectiveProfile struct {
	Kind   string            `json:"kind"`
	Name   string            `json:"name"`
	Chain  []string          `json:"chain"` // feuille d'abord
	Files  []string          `json:"files"` // fichiers sources de la chaîne
	File   string            `json:"file"`  // JSON complet écrit pour le CLI (relatif au répertoire de travail)
	SHA256 string            `json:"sha256"`
	Keys   int               `json:"keys"`
	Source map[string]string `json:"source"` // clé -> profil d'origine, "override" si surchargé
}

// Result est le résultat complet d'un slicing réussi (ou, en cas d'échec,
// l'état partiel utile au diagnostic : commande, journaux, durée).
type Result struct {
	JobID   string `json:"job_id"`
	WorkDir string `json:"work_dir"`

	InputPath   string `json:"input_path"` // copie immuable
	InputSHA256 string `json:"input_sha256"`

	ArtifactPath   string `json:"artifact_path,omitempty"`
	ArtifactSHA256 string `json:"artifact_sha256,omitempty"`
	ArtifactSize   int64  `json:"artifact_size,omitempty"`

	BinaryPath    string        `json:"binary_path"`
	BinaryVersion string        `json:"binary_version"`
	StudioVersion string        `json:"studio_version,omitempty"` // lue dans l'archive
	Command       []string      `json:"command"`
	Duration      time.Duration `json:"duration"`
	ExitCode      int           `json:"exit_code"`
	PID           int           `json:"pid,omitempty"`
	ReturnCode    int           `json:"return_code"` // result.json

	Plates       []PlateResult `json:"plates,omitempty"`
	TotalSeconds float64       `json:"total_seconds"`
	TotalGrams   float64       `json:"total_grams"`

	Machine   EffectiveProfile   `json:"machine"`
	Process   EffectiveProfile   `json:"process"`
	Filaments []EffectiveProfile `json:"filaments"`
	Overrides []AppliedOverride  `json:"overrides,omitempty"`
	BedType   string             `json:"bed_type,omitempty"`
	BedX      float64            `json:"bed_x_mm,omitempty"` // étendue du plateau de la machine
	BedY      float64            `json:"bed_y_mm,omitempty"`

	Archive    *ArchiveInfo    `json:"archive,omitempty"`
	Validation *Validation     `json:"validation,omitempty"`
	StudioJSON json.RawMessage `json:"studio_result,omitempty"`

	Stdout       string   `json:"stdout"`
	Stderr       string   `json:"stderr"`
	LogTruncated bool     `json:"log_truncated,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

// Cleanup supprime le répertoire de travail.
func (r *Result) Cleanup() error {
	if r == nil || r.WorkDir == "" {
		return nil
	}
	return os.RemoveAll(r.WorkDir)
}

// studioResult est le result.json écrit par Bambu Studio.
type studioResult struct {
	ReturnCode   int     `json:"return_code"`
	ErrorString  string  `json:"error_string"`
	PlateIndex   int     `json:"plate_index"`
	LayerHeight  float64 `json:"layer_height"`
	SlicedPlates []struct {
		ID        int `json:"id"`
		Filaments []struct {
			ID         int     `json:"id"`
			FilamentID string  `json:"filament_id"`
			TotalUsedG float64 `json:"total_used_g"`
			MainUsedG  float64 `json:"main_used_g"`
		} `json:"filaments"`
	} `json:"sliced_plates"`
}

type studioPlate = struct {
	ID        int `json:"id"`
	Filaments []struct {
		ID         int     `json:"id"`
		FilamentID string  `json:"filament_id"`
		TotalUsedG float64 `json:"total_used_g"`
		MainUsedG  float64 `json:"main_used_g"`
	} `json:"filaments"`
}

func (r *studioResult) plate(i int) *studioPlate {
	for k := range r.SlicedPlates {
		if r.SlicedPlates[k].ID == i {
			return &r.SlicedPlates[k]
		}
	}
	return nil
}

var safeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitize(s, def string) string {
	s = strings.Trim(safeName.ReplaceAllString(s, "_"), "._-")
	if s == "" {
		return def
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}

// Slice tranche un modèle. En cas d'erreur, le *Result partiel (journaux,
// commande, durée) est retourné avec l'erreur quand le processus a été lancé.
func (s *Slicer) Slice(ctx context.Context, req Request) (*Result, error) {
	// ---- 1. résolution des profils ----
	machineName := req.Machine
	if machineName == "" {
		if req.Printer == "" {
			return nil, fmt.Errorf("%w: Machine ou Printer requis", ErrInvalidRequest)
		}
		nz := req.Nozzle
		if nz == 0 {
			nz = 0.4
		}
		var err error
		if machineName, err = s.profiles.MachineProfile(req.Printer, nz); err != nil {
			return nil, err
		}
	}
	rm, err := s.profiles.Resolve(profiles.KindMachine, machineName)
	if err != nil {
		return nil, err
	}
	var pickNotes []string
	processName := req.Process
	if processName == "" {
		// le défaut déclaré par la machine est souvent hérité d'un X1C : on préfère le preset de la machine
		if c, perr := s.profiles.PickProcess(machineName); perr == nil {
			processName = c.Name
			if c.Note != "" {
				pickNotes = append(pickNotes, "process "+c.Name+" : "+c.Note)
			}
		} else {
			processName, _ = rm.String("default_print_profile")
		}
	}
	if processName == "" {
		return nil, fmt.Errorf("%w: aucun process fourni et la machine n'en déclare pas par défaut", ErrInvalidRequest)
	}
	filNames := req.Filaments
	if len(filNames) == 0 {
		if c, perr := s.profiles.PickFilament(machineName); perr == nil {
			filNames = []string{c.Name}
			if c.Note != "" {
				pickNotes = append(pickNotes, "filament "+c.Name+" : "+c.Note)
			}
		} else {
			filNames = rm.Strings("default_filament_profile")
			if len(filNames) > 1 {
				filNames = filNames[:1]
			}
		}
	}
	if len(filNames) == 0 {
		return nil, fmt.Errorf("%w: aucun filament fourni et la machine n'en déclare pas par défaut", ErrInvalidRequest)
	}
	rp, err := s.profiles.Resolve(profiles.KindProcess, processName)
	if err != nil {
		return nil, err
	}
	if ok, err := s.profiles.IsCompatible(profiles.KindProcess, processName, machineName); err != nil || !ok {
		return nil, fmt.Errorf("%w: process %q avec %q (%v)", ErrIncompatible, processName, machineName, err)
	}
	var rfs []*profiles.Resolved
	for _, fn := range filNames {
		rf, err := s.profiles.Resolve(profiles.KindFilament, fn)
		if err != nil {
			return nil, err
		}
		if ok, err := s.profiles.IsCompatible(profiles.KindFilament, fn, machineName); err != nil || !ok {
			return nil, fmt.Errorf("%w: filament %q avec %q (%v)", ErrIncompatible, fn, machineName, err)
		}
		rfs = append(rfs, rf)
	}
	// copies privées, puis surcharges validées
	cm, cp := rm.Clone(), rp.Clone()
	cfs := make([]*profiles.Resolved, len(rfs))
	for i, rf := range rfs {
		cfs[i] = rf.Clone()
	}
	applied, err := applyOverrides(s.profiles, cm, cp, cfs, req.Overrides)
	if err != nil {
		return nil, err
	}
	// réglages par objet / par plage de hauteur : même liste blanche et mêmes bornes que les surcharges
	for _, o := range req.Objects {
		if err := CheckObjectSettings(o.Settings, cm); err != nil {
			return nil, fmt.Errorf("objet %q : %w", o.Name, err)
		}
		for _, r := range o.Ranges {
			if err := CheckObjectSettings(r.Settings, cm); err != nil {
				return nil, fmt.Errorf("objet %q, plage %.2f-%.2f mm : %w", o.Name, r.MinZ, r.MaxZ, err)
			}
		}
	}
	nozzle := 0.0
	if nz := cm.Strings("nozzle_diameter"); len(nz) > 0 {
		nozzle = parseF(nz[0])
	}
	if req.Nozzle > 0 && math.Abs(req.Nozzle-nozzle) > 1e-6 {
		return nil, fmt.Errorf("%w: buse demandée %g mm mais la machine %q a une buse de %g mm", ErrInvalidRequest, req.Nozzle, machineName, nozzle)
	}
	variant, err := s.variantOf(cm)
	if err != nil {
		return nil, err
	}

	// ---- 2. répertoire de travail et entrées ----
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCanceled, err)
	}
	ext := ".3mf"
	if len(req.Objects) == 0 {
		st, err := os.Stat(req.Model)
		if err != nil {
			return nil, fmt.Errorf("%w: modèle: %v", ErrInvalidRequest, err)
		}
		if st.IsDir() {
			return nil, fmt.Errorf("%w: le modèle est un répertoire", ErrInvalidRequest)
		}
		ext = strings.ToLower(filepath.Ext(req.Model))
		switch ext {
		case ".stl", ".3mf", ".obj":
		default:
			return nil, fmt.Errorf("%w: extension %q non supportée (stl, obj, 3mf)", ErrInvalidRequest, ext)
		}
	}
	id := sanitize(req.ID, "")
	if id == "" {
		id = newID()
	}
	work, err := filepath.Abs(filepath.Join(s.cfg.WorkRoot, id))
	if err != nil {
		return nil, err
	}
	for _, d := range []string{"profiles", "out"} {
		if err := os.MkdirAll(filepath.Join(work, d), 0o755); err != nil {
			return nil, err
		}
	}
	res := &Result{JobID: id, WorkDir: work, BinaryPath: s.bin.Path, BinaryVersion: s.bin.Version, ReturnCode: 1, BedX: variant.BedX, BedY: variant.BedY,
		Warnings: pickNotes}

	if len(req.Objects) > 0 {
		pm, err := writeProject(work, req.Objects, req.PlateOrigin, variant, cm, cp, cfs)
		if err != nil {
			return res, err
		}
		req.Model = pm
	}
	base := sanitize(strings.TrimSuffix(filepath.Base(req.Model), filepath.Ext(req.Model)), "model")
	inputRel := base + ext
	inputAbs := filepath.Join(work, filepath.FromSlash(inputRel))
	sum, err := copyFileSHA(req.Model, inputAbs)
	if err != nil {
		return res, fmt.Errorf("copie du modèle: %w", err)
	}
	_ = os.Chmod(inputAbs, 0o444)
	res.InputPath, res.InputSHA256 = inputAbs, sum

	var inputVolume float64
	var partSizes []mesh.Vec3       // dimensions des pièces de la plaque, pour expliquer un refus de placement
	for _, o := range req.Objects { // projet : volume des seuls objets de la plaque tranchée
		if (req.Plate > 0 && o.Plate != req.Plate) || o.Mesh == nil {
			continue
		}
		inputVolume += math.Abs(mesh.Analyze(o.Mesh, mesh.AnalyzeOptions{}).Volume)
		partSizes = append(partSizes, o.Mesh.Bounds().Size)
	}
	if m, err := mesh.ReadFile(inputAbs); err == nil {
		if len(req.Objects) == 0 {
			partSizes = append(partSizes, m.Combined().Bounds().Size)
		}
		if v := math.Abs(mesh.Analyze(m.Combined(), mesh.AnalyzeOptions{}).Volume); v > 0 && inputVolume == 0 {
			inputVolume = v
		}
		if len(m.Objects) == 0 || (len(m.Objects) == 1 && m.Objects[0].Mesh != nil && len(m.Objects[0].Mesh.T) == 0) {
			return res, fmt.Errorf("%w: le modèle ne contient aucune géométrie", ErrInvalidRequest)
		}
	} else {
		res.Warnings = append(res.Warnings, "modèle non lu par internal/mesh: "+err.Error())
	}

	write := func(kind string, r *profiles.Resolved, file string) (EffectiveProfile, error) {
		b, err := r.JSON()
		if err != nil {
			return EffectiveProfile{}, err
		}
		if err := os.WriteFile(filepath.Join(work, filepath.FromSlash(file)), b, 0o644); err != nil {
			return EffectiveProfile{}, err
		}
		h := sha256.Sum256(b)
		return EffectiveProfile{Kind: kind, Name: r.Name, Chain: r.Chain, Files: r.Files, File: file,
			SHA256: hex.EncodeToString(h[:]), Keys: len(r.Config), Source: r.Source}, nil
	}
	if res.Machine, err = write("machine", cm, "profiles/machine.json"); err != nil {
		return res, err
	}
	if res.Process, err = write("process", cp, "profiles/process.json"); err != nil {
		return res, err
	}
	var filFiles []string
	for i, cf := range cfs {
		f := fmt.Sprintf("profiles/filament%d.json", i+1)
		ep, err := write("filament", cf, f)
		if err != nil {
			return res, err
		}
		res.Filaments = append(res.Filaments, ep)
		filFiles = append(filFiles, f)
	}
	res.Overrides = applied

	// ---- 3. commande ----
	plate := req.Plate
	sliceArg := "1"
	switch {
	case plate == SliceAll:
		sliceArg = "0"
	case plate > 0:
		sliceArg = fmt.Sprint(plate)
	}
	artifactName := sanitize(req.Label, base) + ".gcode.3mf"
	args := append([]string(nil), s.bin.Args...)
	if req.ArrangeOnly {
		artifactName = "arranged.3mf"
		on := true
		req.Arrange = &on
	} else {
		args = append(args, "--slice", sliceArg)
	}
	args = append(args,
		"--load-settings", "profiles/machine.json;profiles/process.json",
		"--load-filaments", strings.Join(filFiles, ";"),
		"--allow-newer-file",
		"--outputdir", "out",
		"--export-3mf", artifactName,
	)
	bed := req.BedType
	if bed == "" {
		if p, err := s.profiles.Printer(variantModel(cm)); err == nil {
			bed = p.DefaultBedType
		}
	}
	if bed != "" {
		args = append(args, "--curr-bed-type", bed)
	}
	res.BedType = bed
	if req.Arrange != nil {
		if *req.Arrange {
			args = append(args, "--arrange", "1")
		} else {
			args = append(args, "--arrange", "0")
		}
	}
	if req.Orient {
		args = append(args, "--orient", "1")
	}
	args = append(args, inputRel)
	res.Command = append([]string{s.bin.Path}, args...)

	// ---- 4. exécution ----
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = s.cfg.Timeout
	}
	runErr := s.run(ctx, timeout, work, args, res)

	// ---- 5. lecture de result.json ----
	var sr *studioResult
	if b, err := os.ReadFile(filepath.Join(work, "out", "result.json")); err == nil {
		res.StudioJSON = json.RawMessage(b)
		var x studioResult
		if err := json.Unmarshal(b, &x); err == nil {
			sr = &x
			res.ReturnCode = x.ReturnCode
		}
	}
	if runErr != nil {
		return res, runErr
	}
	if sr == nil {
		// Sans result.json (ex. option inconnue), le code de sortie 32 bits signé
		// porte encore le code Studio : 4294967294 sous Windows pour -2.
		if code := int(int32(uint32(res.ExitCode))); code < 0 {
			if _, known := studioCodes[code]; known {
				return res, newStudioError(code, "pas de result.json, code lu sur le code de sortie du processus")
			}
		}
		if u := uint64(res.ExitCode); res.ExitCode == -1 || (u >= 0xC0000000 && u <= 0xFFFFFFFF) {
			return res, fmt.Errorf("%w (code de sortie %#x, aucun result.json, stderr: %s)", ErrStudioCrashed, u&0xFFFFFFFF, tail(res.Stderr, 300))
		}
		return res, fmt.Errorf("%w (code de sortie %d, stderr: %s)", ErrNoResult, res.ExitCode, tail(res.Stderr, 300))
	}
	if sr.ReturnCode != 0 {
		se := newStudioError(sr.ReturnCode, sr.ErrorString)
		if ex := explainPlacement(se, variant.MeshTarget(variantModel(cm)), partSizes); ex != nil {
			return res, ex
		}
		return res, se
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("%w: code de sortie %d alors que result.json annonce un succès", ErrInvalidOutput, res.ExitCode)
	}

	if req.ArrangeOnly {
		res.ArtifactPath = filepath.Join(work, "out", artifactName)
		if _, err := os.Stat(res.ArtifactPath); err != nil {
			return res, fmt.Errorf("%w: projet disposé absent: %v", ErrInvalidOutput, err)
		}
		return res, nil
	}

	// ---- 6. validation de l'archive ----
	artifact := filepath.Join(work, "out", artifactName)
	fi, err := os.Stat(artifact)
	if err != nil {
		return res, fmt.Errorf("%w: archive absente: %v", ErrInvalidOutput, err)
	}
	res.ArtifactPath, res.ArtifactSize = artifact, fi.Size()
	if res.ArtifactSHA256, err = fileSHA(artifact); err != nil {
		return res, err
	}
	pm, _ := cm.String("printer_model")
	exp := Expect{
		LayerHeight: parseF(firstStr(cp, "layer_height")), FirstLayerHeight: parseF(firstStr(cp, "initial_layer_print_height")),
		PrinterModel: pm, MachineName: machineName, Nozzle: nozzle, ProcessName: processName,
		Filaments: filNames, BedArea: variant.BedArea, PrintableZ: variant.PrintableZ,
		InputVolume: inputVolume, InputIsMesh: ext != ".3mf", BinaryVersion: s.bin.Version, Studio: sr, Trust: s.trust,
	}
	if plate > 0 {
		exp.Plate = plate
	} else if plate == 0 {
		exp.Plate = 1
	}
	info, val, verr := ValidateArchive(artifact, exp)
	res.Archive, res.Validation = info, val
	if info != nil {
		res.StudioVersion = info.StudioVersion
		res.Plates = info.Plates
		for _, p := range info.Plates {
			res.TotalSeconds += p.PredictedSeconds
			res.TotalGrams += p.WeightGrams
		}
	}
	if val != nil {
		for _, c := range val.Checks {
			if !c.OK && c.Level == "warn" {
				res.Warnings = append(res.Warnings, c.Name+": "+c.Detail)
			}
		}
	}
	if verr != nil {
		return res, verr
	}
	return res, nil
}

func variantModel(r *profiles.Resolved) string {
	m, _ := r.String("printer_model")
	return m
}

func (s *Slicer) variantOf(r *profiles.Resolved) (profiles.Variant, error) {
	m := variantModel(r)
	p, err := s.profiles.Printer(m)
	if err != nil {
		return profiles.Variant{}, err
	}
	for _, v := range p.Variants {
		if v.Profile == r.Name {
			return v, nil
		}
	}
	return profiles.Variant{}, fmt.Errorf("%w: machine %q absente des variantes de %q", profiles.ErrNotFound, r.Name, m)
}

// run lance le processus et en capture les sorties bornées. Les erreurs de
// délai et d'annulation sont nommées ; l'arbre de processus est arrêté.
func (s *Slicer) run(parent context.Context, timeout time.Duration, dir string, args []string, res *Result) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	exe := s.bin.Path
	// un wrapper (xvfb-run…) devient la commande ; le binaire passe en argument
	var cmd *exec.Cmd
	if len(s.cfg.Wrapper) > 0 {
		full := append(append([]string(nil), s.cfg.Wrapper...), exe)
		full = append(full, args...)
		cmd = exec.CommandContext(ctx, full[0], full[1:]...)
		res.Command = full
	} else {
		cmd = exec.CommandContext(ctx, exe, args...)
	}
	cmd.Dir = dir
	cmd.Stdin = nil
	var stdout, stderr = &boundedBuffer{max: s.cfg.MaxLog}, &boundedBuffer{max: s.cfg.MaxLog}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	prepare(cmd)
	var killer *treeKiller
	cmd.Cancel = func() error {
		if killer != nil {
			killer.kill()
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 10 * time.Second

	start := time.Now()
	if err := cmd.Start(); err != nil {
		res.Duration = time.Since(start)
		return fmt.Errorf("lancement de %s: %w", exe, err)
	}
	res.PID = cmd.Process.Pid
	killer = attach(cmd)
	defer killer.close()
	werr := cmd.Wait()
	res.Duration = time.Since(start)
	res.Stdout, res.Stderr = stdout.String(), stderr.String()
	res.LogTruncated = stdout.dropped > 0 || stderr.dropped > 0
	_ = os.WriteFile(filepath.Join(dir, "stdout.log"), []byte(res.Stdout), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "stderr.log"), []byte(res.Stderr), 0o644)
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	// même si Wait a retourné, on s'assure qu'aucun descendant ne survit
	if ctx.Err() != nil {
		killer.kill()
	}
	switch {
	case parent.Err() != nil:
		return fmt.Errorf("%w: %w", ErrCanceled, parent.Err())
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("%w (%s)", ErrTimeout, timeout)
	}
	var ee *exec.ExitError
	if werr != nil && !errors.As(werr, &ee) {
		return fmt.Errorf("exécution: %w", werr)
	}
	return nil
}

// ---- utilitaires ----

type boundedBuffer struct {
	max     int
	buf     bytes.Buffer
	dropped int64
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	room := b.max - b.buf.Len()
	if room > len(p) {
		room = len(p)
	}
	if room > 0 {
		b.buf.Write(p[:room])
	}
	b.dropped += int64(len(p) - max(room, 0))
	return len(p), nil
}

func (b *boundedBuffer) String() string { return b.buf.String() }

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

func copyFileSHA(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileSHA(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func firstStr(r *profiles.Resolved, key string) string {
	v, _ := r.String(key)
	return v
}
