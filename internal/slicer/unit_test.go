package slicer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

const (
	tMachine  = "Test T1 0.4 nozzle"
	tProcess  = "0.20mm Std @TST"
	tFilament = "Test PLA"
)

func fixtureStore(t *testing.T) *profiles.Store {
	t.Helper()
	st, err := profiles.Load(filepath.Join("..", "profiles", "testdata", "vendor"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// fakeSlicer construit un Slicer dont le « binaire » est le binaire de test en mode faux Studio.
func fakeSlicer(t *testing.T, mode string, timeout time.Duration) *Slicer {
	t.Helper()
	t.Setenv("FAKE_STUDIO", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{BinaryPath: exe, Profiles: fixtureStore(t), WorkRoot: t.TempDir(), Timeout: timeout, MaxLog: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func cubeFile(t *testing.T) string {
	t.Helper()
	b, err := mesh.STLBytes(mesh.Cube(20))
	if err != nil {
		t.Fatal(err)
	}
	return writeTemp(t, "cube.stl", b)
}

func fakeReq(t *testing.T) Request {
	return Request{Model: cubeFile(t), Machine: tMachine, Process: tProcess, Filaments: []string{tFilament}, Label: "essai"}
}

func TestFakeSuccess(t *testing.T) {
	s := fakeSlicer(t, "ok", time.Minute)
	if s.Binary().Version != "02.08.02.61" {
		t.Fatalf("version détectée: %q", s.Binary().Version)
	}
	res, err := s.Slice(context.Background(), fakeReq(t))
	if err != nil {
		t.Fatalf("%v\n%+v", err, res.Validation)
	}
	if !res.Validation.OK || len(res.Plates) != 1 {
		t.Fatalf("%+v", res.Validation)
	}
	p := res.Plates[0]
	if p.Layers != 2 || p.PredictedSeconds != 90 || p.WeightGrams != 1 || p.EstimatedTime != "1m 30s" || p.ExtrudeMoves != 2 {
		t.Fatalf("plaque: %+v", p)
	}
	if res.TotalSeconds != 90 || res.TotalGrams != 1 {
		t.Fatalf("totaux %v %v", res.TotalSeconds, res.TotalGrams)
	}
	// la purge de démarrage (Y200 hors zone) ne compte pas dans les trajectoires
	if p.ExtentY[1] > 60 {
		t.Fatalf("étendue Y: %v", p.ExtentY)
	}
	if filepath.Base(res.ArtifactPath) != "essai.gcode.3mf" || len(res.ArtifactSHA256) != 64 {
		t.Fatalf("artefact: %s %s", res.ArtifactPath, res.ArtifactSHA256)
	}
	// l'entrée est copiée et protégée en écriture, l'original est intact
	if sum, _ := fileSHA(res.InputPath); sum != res.InputSHA256 {
		t.Fatal("copie d'entrée altérée")
	}
	if len(res.Machine.Chain) != 3 || res.Machine.Source["printable_area"] != "fdm_tst_common" {
		t.Fatalf("provenance: %+v", res.Machine)
	}
	// les profils complets ont bien été écrits pour le CLI
	for _, f := range []string{res.Machine.File, res.Process.File, res.Filaments[0].File} {
		if _, err := os.Stat(filepath.Join(res.WorkDir, filepath.FromSlash(f))); err != nil {
			t.Fatal(err)
		}
	}
	joined := strings.Join(res.Command, " ")
	for _, want := range []string{"--slice 1", "--load-settings profiles/machine.json;profiles/process.json", "--load-filaments profiles/filament1.json", "--export-3mf essai.gcode.3mf", "--curr-bed-type Smooth Plate", "--allow-newer-file"} {
		if !strings.Contains(joined, want) {
			t.Errorf("commande sans %q: %s", want, joined)
		}
	}
	if err := res.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(res.WorkDir); !os.IsNotExist(err) {
		t.Fatal("Cleanup n'a pas supprimé le répertoire")
	}
}

func TestFakeStudioErrorCodes(t *testing.T) {
	cases := map[int]error{
		-17:  ErrStudioIncompatibleProc,
		-50:  ErrStudioEmptyPlate,
		-52:  ErrStudioObjectsOutside,
		-100: ErrStudioSlicing,
		-3:   ErrStudioFileNotFound,
		-63:  ErrStudioCollision,
		-77:  ErrStudioUnknown,
	}
	for code, want := range cases {
		s := fakeSlicer(t, "err:"+strconv.Itoa(code), time.Minute)
		res, err := s.Slice(context.Background(), fakeReq(t))
		if !errors.Is(err, want) {
			t.Errorf("code %d: %v, attendu %v", code, err, want)
			continue
		}
		var se *StudioError
		if !errors.As(err, &se) || se.Code != code || se.Message != "échec simulé" {
			t.Errorf("code %d: %+v", code, se)
		}
		if res.ReturnCode != code || res.ArtifactPath != "" {
			t.Errorf("code %d: résultat %+v", code, res)
		}
	}
}

func TestFakeNoResultJSON(t *testing.T) {
	s := fakeSlicer(t, "noresult", time.Minute)
	if _, err := s.Slice(context.Background(), fakeReq(t)); !errors.Is(err, ErrNoResult) {
		t.Fatalf("%v", err)
	}
}

func TestFakeCrashIsNamed(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("code de sortie NTSTATUS propre à Windows")
	}
	s := fakeSlicer(t, "crash", time.Minute)
	if _, err := s.Slice(context.Background(), fakeReq(t)); !errors.Is(err, ErrStudioCrashed) {
		t.Fatalf("%v", err)
	}
}

func TestFakeInvalidOutputs(t *testing.T) {
	cases := map[string]string{
		"garbage":      "archive_open",
		"badmd5":       "gcode_md5",
		"wrongmachine": "printer_model",
	}
	for mode, check := range cases {
		s := fakeSlicer(t, mode, time.Minute)
		res, err := s.Slice(context.Background(), fakeReq(t))
		if !errors.Is(err, ErrInvalidOutput) {
			t.Errorf("%s: %v", mode, err)
			continue
		}
		found := false
		for _, c := range res.Validation.Failed() {
			if strings.HasSuffix(c.Name, check) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: contrôle %q non signalé: %+v", mode, check, res.Validation.Checks)
		}
	}
}

func TestFakeTimeoutKillsTree(t *testing.T) {
	s := fakeSlicer(t, "hang", 500*time.Millisecond)
	t0 := time.Now()
	res, err := s.Slice(context.Background(), fakeReq(t))
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("%v", err)
	}
	if d := time.Since(t0); d > 8*time.Second {
		t.Errorf("trop long: %v", d)
	}
	waitGone(t, res.PID)
	b, err := os.ReadFile(filepath.Join(res.WorkDir, "out", "child.pid"))
	if err != nil {
		t.Fatalf("l'enfant n'a pas démarré: %v", err)
	}
	child, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	waitGone(t, child)
}

func TestFakeCancelKillsTree(t *testing.T) {
	s := fakeSlicer(t, "hang", time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(700 * time.Millisecond); cancel() }()
	res, err := s.Slice(ctx, fakeReq(t))
	if !errors.Is(err, ErrCanceled) || !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	waitGone(t, res.PID)
	b, err := os.ReadFile(filepath.Join(res.WorkDir, "out", "child.pid"))
	if err != nil {
		t.Fatalf("l'enfant n'a pas démarré: %v", err)
	}
	child, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	waitGone(t, child)
}

func TestFakeCanceledBeforeStart(t *testing.T) {
	s := fakeSlicer(t, "ok", time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Slice(ctx, fakeReq(t)); !errors.Is(err, ErrCanceled) {
		t.Fatalf("%v", err)
	}
}

func TestFakeLogsAreBounded(t *testing.T) {
	s := fakeSlicer(t, "spam", time.Minute)
	res, err := s.Slice(context.Background(), fakeReq(t))
	if err == nil {
		t.Fatal("échec attendu")
	}
	if len(res.Stdout) != 64<<10 || len(res.Stderr) != 64<<10 || !res.LogTruncated {
		t.Fatalf("journaux non bornés: %d / %d truncated=%v", len(res.Stdout), len(res.Stderr), res.LogTruncated)
	}
}

func TestRequestValidation(t *testing.T) {
	s := fakeSlicer(t, "ok", time.Minute)
	base := fakeReq(t)
	mut := func(f func(r *Request)) Request { r := base; f(&r); return r }
	cases := map[string]struct {
		req  Request
		want error
	}{
		"ni machine ni imprimante": {mut(func(r *Request) { r.Machine = "" }), ErrInvalidRequest},
		"modèle absent":            {mut(func(r *Request) { r.Model = filepath.Join(t.TempDir(), "x.stl") }), ErrInvalidRequest},
		"extension":                {mut(func(r *Request) { r.Model = writeTemp(t, "a.step", []byte("x")) }), ErrInvalidRequest},
		"buse incohérente":         {mut(func(r *Request) { r.Nozzle = 0.6 }), ErrInvalidRequest},
		"machine inconnue":         {mut(func(r *Request) { r.Machine = "Nope" }), profiles.ErrNotFound},
		"process incompatible":     {mut(func(r *Request) { r.Process = "0.30mm Wide @TST" }), ErrIncompatible},
		"filament inconnu":         {mut(func(r *Request) { r.Filaments = []string{"Nope"} }), profiles.ErrNotFound},
		"surcharge inconnue":       {mut(func(r *Request) { r.Overrides = []Override{{Key: "zzz", Value: 1}} }), ErrOverride},
	}
	for name, c := range cases {
		res, err := s.Slice(context.Background(), c.req)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, attendu %v", name, err, c.want)
		}
		if res != nil && res.PID != 0 {
			t.Errorf("%s: le processus a été lancé", name)
		}
	}
	// printer + nozzle + défauts de la machine
	r := Request{Model: base.Model, Printer: "Test T1", Nozzle: 0.4}
	res, err := s.Slice(context.Background(), r)
	if err != nil {
		t.Fatalf("défauts: %v", err)
	}
	if res.Machine.Name != tMachine || res.Process.Name != tProcess || res.Filaments[0].Name != tFilament {
		t.Fatalf("défauts: %s / %s / %s", res.Machine.Name, res.Process.Name, res.Filaments[0].Name)
	}
}

func TestOverrides(t *testing.T) {
	st := fixtureStore(t)
	get := func(k profiles.Kind, n string) *profiles.Resolved {
		r, err := st.Resolve(k, n)
		if err != nil {
			t.Fatal(err)
		}
		return r.Clone()
	}
	m, p, f := get(profiles.KindMachine, tMachine), get(profiles.KindProcess, tProcess), get(profiles.KindFilament, tFilament)

	ok := []Override{
		{Key: "layer_height", Value: 0.28},             // nombre
		{Key: "sparse_infill_density", Value: "25%"},   // pourcentage
		{Key: "wall_loops", Value: 4},                  // entier
		{Key: "bridge_speed", Value: 40},               // diffusion sur la liste [50,50]
		{Key: "nozzle_temperature", Value: []int{230}}, // filament, liste
		{Key: "retraction_length", Value: "1.5", Target: "machine"},
	}
	ap, err := applyOverrides(m, p, []*profiles.Resolved{f}, ok)
	if err != nil {
		t.Fatal(err)
	}
	if len(ap) != 6 {
		t.Fatalf("appliquées: %d", len(ap))
	}
	if v, _ := p.String("layer_height"); v != "0.28" {
		t.Errorf("layer_height %q", v)
	}
	if bs := p.Strings("bridge_speed"); len(bs) != 2 || bs[0] != "40" || bs[1] != "40" {
		t.Errorf("bridge_speed %v", bs)
	}
	if v, _ := f.String("nozzle_temperature"); v != "230" {
		t.Errorf("nozzle_temperature %q", v)
	}
	if p.Source["wall_loops"] != "override" || m.Source["retraction_length"] != "override" {
		t.Error("provenance non marquée")
	}

	m, p, f = get(profiles.KindMachine, tMachine), get(profiles.KindProcess, tProcess), get(profiles.KindFilament, tFilament)
	bad := map[string]Override{
		"clé inconnue":          {Key: "nope", Value: 1},
		"clé vide":              {Key: " ", Value: 1},
		"clé protégée":          {Key: "nozzle_diameter", Value: 0.6},
		"nom protégé":           {Key: "name", Value: "x"},
		"texte pour nombre":     {Key: "layer_height", Value: "epais"},
		"booléen pour texte":    {Key: "gcode_flavor", Value: true},
		"valeur nil":            {Key: "layer_height", Value: nil},
		"liste vide":            {Key: "layer_height", Value: []string{}},
		"scalaire multiple":     {Key: "layer_height", Value: []float64{0.1, 0.2}},
		"liste mauvaise taille": {Key: "bridge_speed", Value: []int{1, 2, 3}},
		"cible inconnue":        {Key: "layer_height", Value: 0.2, Target: "bed"},
		"mauvaise cible":        {Key: "layer_height", Value: 0.2, Target: "machine"},
		"type non supporté":     {Key: "layer_height", Value: struct{}{}},
	}
	for name, o := range bad {
		if _, err := applyOverrides(m, p, []*profiles.Resolved{f}, []Override{o}); !errors.Is(err, ErrOverride) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// après rejet, rien n'a été modifié
	if v, _ := p.String("layer_height"); v != "0.2" {
		t.Errorf("config altérée par un rejet: %q", v)
	}
}

func TestValidateArchiveBranches(t *testing.T) {
	st := fixtureStore(t)
	v := mustVariant(t, st)
	exp := Expect{PrinterModel: "Test T1", MachineName: tMachine, Nozzle: 0.4, ProcessName: tProcess,
		Filaments: []string{tFilament}, BedArea: v.BedArea, PrintableZ: v.PrintableZ, Plate: 1}

	run := func(name string, b []byte, e Expect, wantFail string) {
		t.Helper()
		_, val, err := ValidateArchive(writeTemp(t, "a.gcode.3mf", b), e)
		if wantFail == "" {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			return
		}
		if !errors.Is(err, ErrInvalidOutput) {
			t.Errorf("%s: erreur attendue, reçu %v", name, err)
			return
		}
		for _, c := range val.Failed() {
			if strings.HasSuffix(c.Name, wantFail) {
				return
			}
		}
		t.Errorf("%s: contrôle %q non en échec: %+v", name, wantFail, val.Failed())
	}
	run("valide", buildArchive(archiveOpts{}), exp, "")
	run("non zip", []byte("PK nope"), exp, "archive_open")
	run("md5", buildArchive(archiveOpts{badMD5: true}), exp, "gcode_md5")
	run("sans gcode", buildArchive(archiveOpts{noGcode: true}), exp, "gcode_present")
	run("autre imprimante", buildArchive(archiveOpts{printerModel: "Test T2"}), exp, "printer_model")
	run("autre machine", buildArchive(archiveOpts{machine: "Test T1 0.6 nozzle"}), exp, "machine_profile")
	run("autre process", buildArchive(archiveOpts{process: "0.30mm Wide @TST"}), exp, "process_profile")
	run("autre buse", buildArchive(archiveOpts{nozzle: "0.6"}), exp, "nozzle")
	run("autre filament", buildArchive(archiveOpts{filaments: []string{"Test Generic"}}), exp, "filament_profiles")
	run("aucune plaque", buildArchive(archiveOpts{skipSlice: true}), exp, "slice_info")
	run("plaque 2 demandée", buildArchive(archiveOpts{}), Expect{Plate: 2}, "requested_plate")
	run("hors plateau", buildArchive(archiveOpts{gcodeExtra: "G1 X500 Y20 E1\n"}), exp, "toolpath_in_bed")
	run("trop haut", buildArchive(archiveOpts{gcodeExtra: "G1 Z999\nG1 X10 Y10 E1\n"}), exp, "toolpath_height")
	run("poids incohérent", buildArchive(archiveOpts{weightG: "0"}), exp, "weight")
	// version du binaire différente : avertissement seulement
	_, val, err := ValidateArchive(writeTemp(t, "a.gcode.3mf", buildArchive(archiveOpts{})), Expect{BinaryVersion: "02.09.01.70"})
	if err != nil {
		t.Fatal(err)
	}
	warned := false
	for _, c := range val.Checks {
		if c.Name == "studio_version" && !c.OK && c.Level == "warn" {
			warned = true
		}
	}
	if !warned {
		t.Error("écart de version non signalé")
	}
}

func mustVariant(t *testing.T, st *profiles.Store) profiles.Variant {
	t.Helper()
	p, err := st.Printer("Test T1")
	if err != nil {
		t.Fatal(err)
	}
	return p.Variants[0]
}

func TestErrorMapping(t *testing.T) {
	e := newStudioError(-17, "x")
	if !errors.Is(e, ErrStudioIncompatibleProc) || !strings.Contains(e.Error(), "-17") {
		t.Fatal(e)
	}
	if !errors.Is(newStudioError(-104, ""), ErrStudioGcodePath) || !errors.Is(newStudioError(-1234, ""), ErrStudioUnknown) {
		t.Fatal("mapping")
	}
}

func TestSanitizeAndDuration(t *testing.T) {
	if got := sanitize("Pièce n°1/../x", "d"); strings.ContainsAny(got, `/\ è°`) || got == "d" {
		t.Fatalf("sanitize: %q", got)
	}
	if got := sanitize("///", "def"); got != "def" {
		t.Fatalf("%q", got)
	}
	if d := parseDuration("1d 2h 3m 4s"); d != 86400+7200+180+4 {
		t.Fatalf("%v", d)
	}
}

func TestLocateMissing(t *testing.T) {
	t.Setenv("BAMBU_STUDIO_BIN", "")
	_, err := Locate(filepath.Join(t.TempDir(), "absent.exe"))
	// en l'absence de toute installation la recherche échoue ; sinon elle trouve l'installation locale
	if err != nil && !errors.Is(err, ErrBinaryNotFound) {
		t.Fatal(err)
	}
}
