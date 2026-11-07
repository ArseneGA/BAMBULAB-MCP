package library

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/engineering"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func ctx() context.Context { return context.Background() }

func near(t *testing.T, got, want, tol float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s : %v, attendu %v ± %v", what, got, want, tol)
	}
}

func TestListAndDescribe(t *testing.T) {
	r := newRig(t, nil)
	o, err := r.svc.List(ctx(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Templates) != 5 || o.Templates[0].ID != "washer" || o.Templates[0].Kind != "builtin" || !strings.HasSuffix(o.Templates[0].ThumbnailURL, "/washer/thumbnail.svg") {
		t.Fatalf("liste inattendue : %+v", o.Templates)
	}
	if len(o.Categories) != 3 {
		t.Fatalf("catégories : %v", o.Categories)
	}
	f, _ := r.svc.List(ctx(), "mécanisme")
	if len(f.Templates) != 3 {
		t.Fatalf("filtre catégorie : %d", len(f.Templates))
	}
	d, err := r.svc.Describe(ctx(), "washer")
	if err != nil || len(d.Params) != 3 || d.Params[1].Auto != "hole:vertical" || *d.Params[0].Min != 3 {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := r.svc.Describe(ctx(), "inconnu"); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "washer") {
		t.Fatalf("gabarit inconnu : %v", err)
	}
	// le catalogue du worker n'est interrogé qu'une fois
	if n := r.fc.count("library_list"); n != 1 {
		t.Fatalf("library_list appelé %d fois", n)
	}
}

func TestWorkerUnavailableIsReported(t *testing.T) {
	r := newRig(t, nil)
	r.svc.d.Catalog = nil
	if _, err := r.svc.List(ctx(), ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("%v", err)
	}
}

func TestCreateCompensatesHolesGenerically(t *testing.T) {
	r := newRig(t, nil)
	o, err := r.svc.Create(ctx(), CreateInput{Template: "washer", Params: map[string]any{"inner_diameter": 8.0}, Name: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := engineering.HoleCompensation(engineering.HoleParams{Diameter: 8, Axis: "vertical", Nozzle: 0.4, Material: "PLA"})
	got := sentParams(r)["inner_diameter"].(float64)
	near(t, got, want.Value, 1e-9, "diamètre envoyé au worker")
	if got <= 8.1 || got > 8.4 {
		t.Fatalf("compensation générique implausible : %v", got)
	}
	if len(o.Auto) != 1 || o.Auto[0].Nominal != 8.0 || o.Auto[0].Level != "heuristic" || !strings.Contains(o.Auto[0].Source, "générique") {
		t.Fatalf("auto : %+v", o.Auto)
	}
	if o.Calibration.Applied || !strings.Contains(o.Calibration.Provenance, "génériques") {
		t.Fatalf("calibration : %+v", o.Calibration)
	}
	if o.Requested["inner_diameter"] != 8.0 {
		t.Fatalf("requested : %v", o.Requested)
	}
	if len(o.Files) != 2 || o.ModelID == "" || o.VersionID == "" || o.ThumbnailURL == "" {
		t.Fatalf("sortie : %+v", o)
	}
	if n := len(o.Calcs); n != 1 || o.Calcs[0].Name != "hole_compensation" {
		t.Fatalf("calculs : %+v", o.Calcs)
	}
	// compensate_holes=false : valeur nominale transmise telle quelle
	off := false
	if _, err := r.svc.Create(ctx(), CreateInput{Template: "washer", Params: map[string]any{"inner_diameter": 8.0}, CompensateHoles: &off}); err != nil {
		t.Fatal(err)
	}
	if sentParams(r)["inner_diameter"].(float64) != 8.0 {
		t.Fatalf("compensation désactivée ignorée : %v", sentParams(r))
	}
}

func TestCreateUsesCalibrationInPriority(t *testing.T) {
	cal := &engineering.Calibration{Provenance: "calibré le 2026-11-07 (P1S, buse 0.4, PLA)", CalibratedAt: "2026-11-07",
		SlidesGapMM: f64(0.35), HoleVertical: &engineering.LinearOffset{Offset: 0.2, Slope: 0, N: 5}}
	r := newRig(t, cal)
	o, err := r.svc.Create(ctx(), CreateInput{Template: "washer", Params: map[string]any{"inner_diameter": 8.0}, Printer: "P1S", Filament: "Bambu PLA Basic"})
	if err != nil {
		t.Fatal(err)
	}
	near(t, sentParams(r)["inner_diameter"].(float64), 8.2, 1e-9, "trou calibré")
	if !o.Calibration.Applied || !strings.Contains(o.Calibration.Provenance, "calibré le 2026-11-07") || o.Auto[0].Level != "measured" || !strings.Contains(o.Auto[0].Source, "calibration") {
		t.Fatalf("provenance : %+v %+v", o.Calibration, o.Auto)
	}
	if len(r.cal.got) == 0 || r.cal.got[0].Printer != "p1s" || r.cal.got[0].Material != "PLA" || r.cal.got[0].NozzleMM != 0.4 || r.cal.got[0].Filament != "bambu pla basic" {
		t.Fatalf("clé de calibration : %+v", r.cal.got)
	}
	// jeu d'ajustement glissant : mesure plutôt que 0,25 générique
	h, err := r.svc.Create(ctx(), CreateInput{Template: "hinge_pin"})
	if err != nil {
		t.Fatal(err)
	}
	near(t, sentParams(r)["hole_clearance"].(float64), 0.35, 1e-9, "jeu glissant calibré")
	if h.Calcs[0].Result.Level != engineering.LevelMeasured {
		t.Fatalf("niveau : %v", h.Calcs[0].Result.Level)
	}
	// pièce imprimée en place : jeu glissant mesuré
	if _, err := r.svc.Create(ctx(), CreateInput{Template: "hinge_print_in_place"}); err != nil {
		t.Fatal(err)
	}
	near(t, sentParams(r)["clearance"].(float64), 0.35, 1e-9, "jeu imprimé en place calibré")
}

func TestCreateGenericFitAndPIPGap(t *testing.T) {
	r := newRig(t, nil)
	o, err := r.svc.Create(ctx(), CreateInput{Template: "hinge_pin"})
	if err != nil {
		t.Fatal(err)
	}
	near(t, sentParams(r)["hole_clearance"].(float64), 0.25, 1e-9, "jeu glissant générique (buse 0,4)")
	if o.Calcs[0].Name != "fit_clearance" || o.Calcs[0].Result.Level != engineering.LevelHeuristic {
		t.Fatalf("%+v", o.Calcs)
	}
	// une valeur fournie n'est jamais écrasée
	if _, err := r.svc.Create(ctx(), CreateInput{Template: "hinge_pin", Params: map[string]any{"hole_clearance": 0.5}}); err != nil {
		t.Fatal(err)
	}
	if sentParams(r)["hole_clearance"].(float64) != 0.5 {
		t.Fatalf("valeur fournie écrasée : %v", sentParams(r))
	}
	p, err := r.svc.Create(ctx(), CreateInput{Template: "hinge_print_in_place"})
	if err != nil {
		t.Fatal(err)
	}
	near(t, sentParams(r)["clearance"].(float64), 0.3, 1e-9, "jeu print-in-place générique")
	if len(p.Calcs[0].Result.Warnings) == 0 {
		t.Fatal("un avertissement d'essai est attendu sans calibration")
	}
	// la buse change le jeu (0,25 mm par mm d'écart)
	if _, err := r.svc.Create(ctx(), CreateInput{Template: "hinge_pin", NozzleMM: 0.6}); err != nil {
		t.Fatal(err)
	}
	near(t, sentParams(r)["hole_clearance"].(float64), 0.30, 1e-9, "jeu glissant buse 0,6")
}

func TestCreateEnclosureInsertRules(t *testing.T) {
	r := newRig(t, nil)
	o, err := r.svc.Create(ctx(), CreateInput{Template: "enclosure", Params: map[string]any{"screw_size": "M4"}})
	if err != nil {
		t.Fatal(err)
	}
	p := sentParams(r)
	near(t, p["insert_hole_diameter"].(float64), 5.6, 1e-9, "trou d'insert M4")
	near(t, p["insert_depth"].(float64), 9.6, 1e-9, "profondeur d'insert M4")
	near(t, p["boss_diameter"].(float64), 8.8, 1e-9, "plot M4")
	near(t, p["lid_clearance"].(float64), 0.2, 1e-9, "jeu de couvercle par côté")
	names := map[string]int{}
	for _, c := range o.Calcs {
		names[c.Name]++
	}
	if names["screw_boss"] != 1 || names["fit_clearance"] != 1 {
		t.Fatalf("calculs : %v", names)
	}
	// mode clip : pas d'insert
	if _, err := r.svc.Create(ctx(), CreateInput{Template: "enclosure", Params: map[string]any{"lid_mode": "clip"}}); err != nil {
		t.Fatal(err)
	}
	if _, has := sentParams(r)["insert_hole_diameter"]; has {
		t.Fatalf("insert calculé en mode clip : %v", sentParams(r))
	}
	// valeur d'insert fournie : conservée
	if _, err := r.svc.Create(ctx(), CreateInput{Template: "enclosure", Params: map[string]any{"insert_hole_diameter": 4.3}}); err != nil {
		t.Fatal(err)
	}
	if sentParams(r)["insert_hole_diameter"].(float64) != 4.3 {
		t.Fatal("insert fourni écrasé")
	}
}

func TestCreateSnapClipRunsSnapFit(t *testing.T) {
	r := newRig(t, nil)
	o, err := r.svc.Create(ctx(), CreateInput{Template: "snap_clip", Material: "PETG"})
	if err != nil {
		t.Fatal(err)
	}
	var snap *Calc
	for i := range o.Calcs {
		if o.Calcs[i].Name == "snap_fit" {
			snap = &o.Calcs[i]
		}
	}
	if snap == nil || snap.Extra["ok"] != true {
		t.Fatalf("snap_fit : %+v", o.Calcs)
	}
	near(t, snap.Result.Value, 1.5*1.6*1.2/(18*18), 1e-9, "déformation du bras")
	// bras trop court pour le retour demandé : avertissement
	o, err = r.svc.Create(ctx(), CreateInput{Template: "snap_clip", Params: map[string]any{"beam_length": 8.0, "hook_depth": 3.0}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(o.Warnings, " "), "SnapFit") {
		t.Fatalf("avertissement attendu : %v", o.Warnings)
	}
	// matériau inconnu : pas de calcul mais pas d'échec
	o, err = r.svc.Create(ctx(), CreateInput{Template: "snap_clip", Material: "PLA soie rare"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(o.Warnings, " "), "inconnu") {
		t.Fatalf("matériau inconnu : %v", o.Warnings)
	}
}

func TestCreateValidation(t *testing.T) {
	r := newRig(t, nil)
	for name, in := range map[string]CreateInput{
		"template vide":   {},
		"gabarit inconnu": {Template: "nope"},
		"param inconnu":   {Template: "washer", Params: map[string]any{"zzz": 1}},
		"nom invalide":    {Template: "washer", Name: "../x"},
		"model_id sans v": {Template: "washer", ModelID: "mdl_x"},
	} {
		if _, err := r.svc.Create(ctx(), in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s : %v", name, err)
		}
	}
	if r.fc.count("library_build") != 0 {
		t.Fatal("le worker ne doit pas être appelé pour une entrée invalide")
	}
}

func TestCreateNewVersionAndStaleBase(t *testing.T) {
	r := newRig(t, nil)
	a, err := r.svc.Create(ctx(), CreateInput{Template: "washer", Name: "rond"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.svc.Create(ctx(), CreateInput{Template: "washer", Params: map[string]any{"thickness": 3.0}, ModelID: a.ModelID, BaseVersionID: a.VersionID})
	if err != nil {
		t.Fatal(err)
	}
	if b.ModelID != a.ModelID || b.VersionID == a.VersionID {
		t.Fatalf("nouvelle version attendue : %+v %+v", a, b)
	}
	// repartir de la v1 alors que la v2 existe : refus
	if _, err := r.svc.Create(ctx(), CreateInput{Template: "washer", ModelID: a.ModelID, BaseVersionID: a.VersionID}); !errors.Is(err, ErrConflict) {
		t.Fatalf("version périmée : %v", err)
	}
	if _, err := r.svc.Create(ctx(), CreateInput{Template: "washer", ModelID: a.ModelID, BaseVersionID: "ver_inconnue"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("version étrangère : %v", err)
	}
}

func TestWorkerInvalidParamsMapsToInvalid(t *testing.T) {
	r := newRig(t, nil)
	r.fc.failBuild = &catalog.WorkerError{Code: "invalid_params", Message: "thickness doit être >= 0.4"}
	if _, err := r.svc.Create(ctx(), CreateInput{Template: "washer"}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "thickness") {
		t.Fatalf("%v", err)
	}
	r.fc.failBuild = &catalog.WorkerError{Code: "cad_failure", Message: "boom"}
	if _, err := r.svc.Create(ctx(), CreateInput{Template: "washer"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("%v", err)
	}
}

func TestSaveAsTemplateAndReuse(t *testing.T) {
	r := newRig(t, nil)
	a, err := r.svc.Create(ctx(), CreateInput{Template: "washer", Name: "w", Params: map[string]any{"outer_diameter": 24.0, "inner_diameter": 8.0}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.svc.SaveAs(ctx(), SaveInput{ModelID: a.ModelID, Name: "Rondelle large M8", Description: "pour M8"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Variant.ID != "rondelle-large-m8" || s.Variant.Kind != "variant" || s.Variant.BaseTemplate != "washer" {
		t.Fatalf("variante : %+v", s.Variant)
	}
	// seuls les paramètres demandés sont figés : pas la valeur compensée
	if s.Variant.Fixed["inner_diameter"] != 8.0 || s.Variant.Fixed["outer_diameter"] != 24.0 {
		t.Fatalf("fixed : %v", s.Variant.Fixed)
	}
	if s.Variant.Params[0].Default != 24.0 {
		t.Fatalf("défauts de la variante : %+v", s.Variant.Params)
	}
	l, _ := r.svc.List(ctx(), "")
	if len(l.Templates) != 6 || l.Templates[5].ID != "rondelle-large-m8" {
		t.Fatalf("liste avec variante : %d", len(l.Templates))
	}
	// réutilisation : paramètres figés + surcharge
	o, err := r.svc.Create(ctx(), CreateInput{Template: "rondelle-large-m8", Params: map[string]any{"thickness": 3.5}})
	if err != nil {
		t.Fatal(err)
	}
	p := sentParams(r)
	if r.fc.last("library_build")["template"] != "washer" || p["outer_diameter"].(float64) != 24 || p["thickness"].(float64) != 3.5 || p["inner_diameter"].(float64) <= 8.1 {
		t.Fatalf("variante mal appliquée : %v", p)
	}
	if o.Variant != "rondelle-large-m8" || o.Template != "washer" {
		t.Fatalf("sortie : %+v", o)
	}
	// doublon, gabarit intégré, remplacement
	if _, err := r.svc.SaveAs(ctx(), SaveInput{ModelID: a.ModelID, Name: "rondelle large m8"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("doublon : %v", err)
	}
	if _, err := r.svc.SaveAs(ctx(), SaveInput{ModelID: a.ModelID, Name: "washer"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("nom de gabarit intégré : %v", err)
	}
	if _, err := r.svc.SaveAs(ctx(), SaveInput{ModelID: a.ModelID, Name: "rondelle large m8", Replace: true, Frozen: true}); err != nil {
		t.Fatalf("remplacement : %v", err)
	}
	d, _ := r.svc.Describe(ctx(), "rondelle-large-m8")
	if d.Fixed["inner_diameter"].(float64) <= 8.1 { // figé : valeur compensée
		t.Fatalf("valeurs figées attendues : %v", d.Fixed)
	}
	// modèle non généré par un gabarit
	if _, err := r.svc.SaveAs(ctx(), SaveInput{ModelID: "mdl_inconnu", Name: "x"}); err == nil {
		t.Fatal("erreur attendue")
	}
	if _, err := r.svc.SaveAs(ctx(), SaveInput{Name: "x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
}

func TestSaveAsFromLegacyParametricModel(t *testing.T) {
	r := newRig(t, nil)
	m, _, _, _ := r.fc.RegisterGenerated(ctx(), "x", contractsModel("ancien", `gabarit washer ; paramètres {"outer_diameter":30,"inner_diameter":10}`), "", "v1", "", nil)
	s, err := r.svc.SaveAs(ctx(), SaveInput{ModelID: m.ID, Name: "Ancienne rondelle"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Variant.Fixed["outer_diameter"].(float64) != 30 {
		t.Fatalf("%+v", s.Variant.Fixed)
	}
}

func TestThumbnails(t *testing.T) {
	r := newRig(t, nil)
	b1, err := r.svc.Thumbnail(ctx(), "washer", nil)
	if err != nil || !strings.HasPrefix(string(b1), "<svg") {
		t.Fatalf("%q %v", b1, err)
	}
	if _, err := r.svc.Thumbnail(ctx(), "washer", nil); err != nil {
		t.Fatal(err)
	}
	if n := r.fc.count("library_thumbnail"); n != 1 {
		t.Fatalf("vignette par défaut non mise en cache (%d appels)", n)
	}
	if _, err := r.svc.Thumbnail(ctx(), "washer", map[string]any{"thickness": 3.0}); err != nil {
		t.Fatal(err)
	}
	if n := r.fc.count("library_thumbnail"); n != 2 {
		t.Fatalf("vignette paramétrée attendue sans cache (%d)", n)
	}
	o, err := r.svc.Create(ctx(), CreateInput{Template: "washer"})
	if err != nil {
		t.Fatal(err)
	}
	tb, err := r.svc.ModelThumbnail(ctx(), o.ModelID)
	if err != nil || !strings.Contains(string(tb), "thumb.svg") {
		t.Fatalf("vignette de modèle : %q %v", tb, err)
	}
	if _, err := r.svc.ModelThumbnail(ctx(), "mdl_inconnu"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"Collier tube 32": "collier-tube-32", "  Été / Œuf  ": "ete-oeuf", "***": "", "a": "a"} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, attendu %q", in, got, want)
		}
	}
	if got := slug(strings.Repeat("a", 80)); len(got) != 48 {
		t.Errorf("longueur %d", len(got))
	}
}
