package sheets

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/camera"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/projects"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

var t0 = time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)

// fakeCam : caméra simulée (paliers et alertes par impression ; réglages modifiables).
type fakeCam struct {
	steps  map[string][]camera.ProgressPhoto
	alerts map[string][]camera.Alert
	set    camera.Settings
}

func (f *fakeCam) ProgressPhotos(_ context.Context, id string) ([]camera.ProgressPhoto, error) {
	return f.steps[id], nil
}
func (f *fakeCam) Alerts(_ context.Context, id string, _ int) ([]camera.Alert, error) {
	return f.alerts[id], nil
}
func (f *fakeCam) Settings(context.Context) camera.Settings { return f.set }

type env struct {
	t   *testing.T
	st  *store.Store
	ins *insights.Service
	prj *projects.Service
	svc *Service
	cam *fakeCam
	n   int
}

func newEnv(t *testing.T, withCamera bool) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bambu.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := func() time.Time { return t0.Add(30 * 24 * time.Hour) }
	ins, err := insights.New(insights.Config{Store: st, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	prj, err := projects.New(projects.Config{Store: st, Counts: ins, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, st: st, ins: ins, prj: prj, cam: &fakeCam{steps: map[string][]camera.ProgressPhoto{}, alerts: map[string][]camera.Alert{}, set: camera.DefaultSettings()}}
	cfg := Config{Store: st, Insights: ins, Projects: prj}
	if withCamera {
		cfg.Camera = e.cam
	}
	if e.svc, err = New(cfg); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.st.DB().Exec(q, args...); err != nil {
		e.t.Fatalf("%s : %v", q, err)
	}
}

func (e *env) model(id, name string) {
	e.t.Helper()
	ts := store.FormatTime(t0)
	e.exec(`INSERT INTO models(id,name,created_at,updated_at) VALUES(?,?,?,?)`, id, name, ts, ts)
}

type jobOpt struct {
	state   string
	percent int
	hours   float64 // durée réelle ; 0 = aucun évènement (durée réelle non relevée)
	process string
}

// job enregistre une impression prévue 2 h / 100 g de PLA ; la durée réelle vaut opt.hours.
func (e *env) job(model string, o jobOpt) string {
	e.t.Helper()
	e.n++
	if o.state == "" {
		o.state = "COMPLETED"
	}
	if o.percent == 0 {
		o.percent = 100
	}
	if o.process == "" {
		o.process = "0.20mm Standard @BBL X1C"
	}
	id := model + "_job" + string(rune('a'+e.n))
	start := t0.AddDate(0, 0, e.n)
	end := start.Add(time.Duration(o.hours * float64(time.Hour)))
	var events []map[string]any
	if o.hours > 0 {
		events = []map[string]any{{"at": start, "event": "go", "from": "START_REQUESTED", "to": "PRINTING"}, {"at": end, "event": "fin", "from": "PRINTING", "to": o.state}}
	}
	doc := map[string]any{
		"id": id, "state": o.state, "printer_id": "pr1", "model_id": model, "name": id + ".gcode.3mf", "plate": 1,
		"assignments": []map[string]any{{"filament_slot": 1, "tray": "ams1:1", "material_id": "Bambu PLA Basic noir"}},
		"summary": map[string]any{
			"plates":        []slicer.PlateResult{{Index: 1, PredictedSeconds: 7200, WeightGrams: 100, Layers: 100, Filaments: []slicer.FilamentUse{{Slot: 1, Type: "PLA", Grams: 100}}}},
			"printer_model": "Bambu Lab P1S", "machine_preset": "Bambu Lab P1S 0.4 nozzle", "process": o.process,
			"filament_presets": []string{"Bambu PLA Basic @BBL P1S 0.4 nozzle"},
		},
		"print": map[string]any{"percent": o.percent}, "events": events,
		"slice":      map[string]any{"machine": "Bambu Lab P1S 0.4 nozzle", "process": o.process, "filaments": []string{"Bambu PLA Basic @BBL P1S 0.4 nozzle"}, "nozzle_mm": 0.4},
		"created_at": start, "updated_at": end,
	}
	if err := e.st.PutDoc(context.Background(), "test", "print_jobs", id, doc); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) verdict(job, v, reason string) {
	e.t.Helper()
	if _, err := e.ins.SetVerdict(context.Background(), insights.SetVerdictInput{PrintJobID: job, Verdict: v, Reason: reason}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) part(t *testing.T, id string, in ...string) *PartSheet {
	t.Helper()
	sh, err := e.svc.PartSheet(context.Background(), PartSheetInput{ModelID: id, Include: in})
	if err != nil {
		t.Fatal(err)
	}
	return sh
}

var secretPatterns = regexp.MustCompile(`bblp|C:\\|/home/|/opt/|Bearer|access_code|X-Amz|Signature=|token=`)

func noSecrets(t *testing.T, v any) {
	t.Helper()
	raw, _ := json.Marshal(v)
	if m := secretPatterns.FindString(string(raw)); m != "" {
		t.Errorf("motif de secret %q dans la sortie : %s", m, raw)
	}
}

// CA5.1 : 5 impressions à 100 % (4 jugées réussies, 1 ratée stringing) ; une pièce jamais imprimée : zéros, sans erreur.
func TestPartSheetCounts(t *testing.T) {
	e := newEnv(t, true)
	e.model("mA", "Équerre")
	e.model("mZ", "Neuve")
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, e.job("mA", jobOpt{hours: 2}))
	}
	for _, j := range ids[:4] {
		e.verdict(j, "success", "")
	}
	e.verdict(ids[4], "failed", "stringing")
	e.job("mA", jobOpt{state: "CANCELLED", percent: 60, hours: 1}) // annulée avant 100 % : pas imprimée
	sh := e.part(t, "mA")
	c := sh.Counts
	if c.Printed != 5 || c.Success != 4 || c.Failed != 1 || c.Attempts != 6 || c.NeverPrinted {
		t.Fatalf("compteurs : %+v", c)
	}
	if !strings.Contains(c.Text, "imprimée 5 fois") || !strings.Contains(c.Text, "4 jugées réussies") || !strings.Contains(c.Text, "stringing") {
		t.Errorf("phrase : %s", c.Text)
	}
	if !sh.Totals.Available || sh.Totals.ActualCost <= 0 || sh.Totals.ActualSeconds != 6*2*3600-3600 {
		t.Errorf("totaux : %+v", sh.Totals)
	}
	if sh.Totals.ActualCost != sh.Counts.ActualCost {
		t.Errorf("coût cumulé %v != compteurs %v", sh.Totals.ActualCost, sh.Counts.ActualCost)
	}
	if sh.Totals.CostPerSuccess == nil || *sh.Totals.CostPerSuccess <= 0 {
		t.Errorf("coût par réussite : %+v", sh.Totals.CostPerSuccess)
	}
	if sh.PrintsTotal != 6 || len(sh.Prints) != 6 || sh.Truncated {
		t.Errorf("impressions : %d/%d", len(sh.Prints), sh.PrintsTotal)
	}
	if len(sh.Profiles) != 1 || sh.Profiles[0].Printed != 5 || sh.Profiles[0].Success != 4 || sh.Profiles[0].MedianSeconds == nil || *sh.Profiles[0].MedianSeconds != 7200 {
		t.Errorf("profils : %+v", sh.Profiles)
	}
	if sh.LastSuccess == nil || sh.LastSuccess.ProfileSig != sh.Profiles[0].Signature {
		t.Errorf("dernier profil réussi : %+v", sh.LastSuccess)
	}
	noSecrets(t, sh)

	z := e.part(t, "mZ")
	if !z.Counts.NeverPrinted || z.Counts.Printed != 0 || z.Counts.Text != "jamais imprimée" || z.Totals.Available || len(z.Prints) != 0 || z.LastSuccess != nil {
		t.Errorf("jamais imprimée : %+v / %+v", z.Counts, z.Totals)
	}
	if z.Unavailable["cost"] == "" || z.Unavailable["last_success"] == "" || z.Versions == nil || z.Profiles == nil || z.Projects == nil {
		t.Errorf("raisons et listes non nulles : %+v", z.Unavailable)
	}
	noSecrets(t, z)
	if _, err := e.svc.PartSheet(context.Background(), PartSheetInput{ModelID: "mdl_nope"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("pièce inconnue : %v", err)
	}
	if _, err := e.svc.PartSheet(context.Background(), PartSheetInput{ModelID: "mA", Include: []string{"nope"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("section inconnue : %v", err)
	}
}

// Profils : des réglages différents donnent des profils distincts ; limit tronque la liste, pas les compteurs.
func TestPartSheetProfilesAndLimit(t *testing.T) {
	e := newEnv(t, true)
	e.model("mA", "Équerre")
	for i := 0; i < 3; i++ {
		e.job("mA", jobOpt{hours: 2})
	}
	e.job("mA", jobOpt{hours: 1, process: "0.12mm Fine @BBL X1C"})
	sh, err := e.svc.PartSheet(context.Background(), PartSheetInput{ModelID: "mA", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(sh.Profiles) != 2 || sh.Profiles[0].Attempts != 3 || sh.Profiles[1].Attempts != 1 || sh.Profiles[0].Signature == sh.Profiles[1].Signature {
		t.Fatalf("profils : %+v", sh.Profiles)
	}
	if len(sh.Prints) != 2 || sh.PrintsTotal != 4 || !sh.Truncated || sh.Counts.Printed != 4 {
		t.Errorf("limite : %d/%d tronqué=%v imprimées=%d", len(sh.Prints), sh.PrintsTotal, sh.Truncated, sh.Counts.Printed)
	}
	only := e.part(t, "mA", "profiles")
	if len(only.Prints) != 0 || len(only.Versions) != 0 || len(only.Profiles) != 2 {
		t.Errorf("include=profiles : %d impressions, %d versions", len(only.Prints), len(only.Versions))
	}
}

// CA5.2 : les versions suivent parent_id ; chaque test de résistance est rattaché à SA version ; assumed relevé.
func TestPartSheetVersionsAndStrength(t *testing.T) {
	e := newEnv(t, true)
	e.model("mA", "Équerre")
	ts := func(d int) string { return store.FormatTime(t0.AddDate(0, 0, d)) }
	e.exec(`INSERT INTO model_versions(id,model_id,parent_id,label,created_at) VALUES('v1','mA',NULL,'v1',?)`, ts(1))
	e.exec(`INSERT INTO model_versions(id,model_id,parent_id,label,created_at) VALUES('v2','mA','v1','v2',?)`, ts(2))
	e.exec(`INSERT INTO model_versions(id,model_id,parent_id,label,created_at) VALUES('v3','mA','v2','v3',?)`, ts(3))
	ins := `INSERT INTO strength_tests(id,model_id,version_id,created_at,material,verdict,safety_factor,assumed,input_json,result_json) VALUES(?,?,?,?,?,?,?,?,'{}','{}')`
	e.exec(ins, "t1", "mA", "v1", ts(4), "PLA", "fail", 0.8, 0)
	e.exec(ins, "t2", "mA", "v3", ts(5), "PETG", "ok", 2.4, 1)
	e.exec(ins, "t3", "mA", "v3", ts(6), "PETG", "ok", 3.1, 0) // plus récent pour v3
	e.exec(ins, "t4", "mA", "", ts(7), "PLA", "ok", 1.5, 1)    // sans version identifiable
	sh := e.part(t, "mA")
	if len(sh.Versions) != 3 || sh.Versions[0].ID != "v1" || sh.Versions[1].ID != "v2" || sh.Versions[2].ID != "v3" {
		t.Fatalf("versions : %+v", sh.Versions)
	}
	if sh.Versions[0].Depth != 0 || sh.Versions[1].Depth != 1 || sh.Versions[2].Depth != 2 || sh.Versions[1].ParentID != "v1" || !sh.Versions[2].IsLatest || sh.Versions[0].IsLatest {
		t.Errorf("profondeurs / parents : %+v", sh.Versions)
	}
	v1, v2, v3 := sh.Versions[0], sh.Versions[1], sh.Versions[2]
	if v1.Strength == nil || v1.Strength.TestID != "t1" || v1.StrengthStatus != "tested" || v1.Strength.Verdict != "fail" {
		t.Errorf("v1 : %+v", v1.Strength)
	}
	if v2.Strength != nil || v2.StrengthStatus != "never_tested" {
		t.Errorf("v2 jamais testée : %+v", v2)
	}
	if v3.Strength == nil || v3.Strength.TestID != "t3" || v3.Strength.Assumed {
		t.Errorf("v3 : dernier test attendu t3 : %+v", v3.Strength)
	}
	if len(sh.StrengthUnassigned) != 1 || sh.StrengthUnassigned[0].TestID != "t4" || !sh.StrengthUnassigned[0].Assumed {
		t.Errorf("sans version : %+v", sh.StrengthUnassigned)
	}
	pr, _ := e.prj.Create(context.Background(), projects.CreateInput{Name: "Étagère"})
	if _, err := e.prj.AddPart(context.Background(), projects.PartInput{ProjectID: pr.ID, ModelID: "mA", Quantity: 2}); err != nil {
		t.Fatal(err)
	}
	if sh = e.part(t, "mA", "projects"); len(sh.Projects) != 1 || sh.Projects[0].Name != "Étagère" {
		t.Errorf("projets : %+v", sh.Projects)
	}
}

// CA5.3 : une impression déclarée hors Printalk est listée « déclarée » et ne compte pas dans le coût réel mesuré.
func TestPartSheetExternalExcludedFromMeasured(t *testing.T) {
	e := newEnv(t, true)
	e.model("mA", "Équerre")
	j1 := e.job("mA", jobOpt{hours: 2})
	j2 := e.job("mA", jobOpt{hours: 2})
	if _, err := e.ins.SyncJournal(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := e.part(t, "mA")
	e.exec(`UPDATE print_logs SET origin='external' WHERE print_job_id=?`, j2)
	sh := e.part(t, "mA")
	if sh.Totals.DeclaredCount != 1 || sh.Totals.ActualCost >= before.Totals.ActualCost || sh.Totals.ActualCost <= 0 {
		t.Errorf("coût mesuré %v (avant %v), déclarées %d", sh.Totals.ActualCost, before.Totals.ActualCost, sh.Totals.DeclaredCount)
	}
	if sh.Totals.ActualSeconds != 7200 || sh.Totals.DeclaredSeconds != 7200 {
		t.Errorf("temps mesuré %v, déclaré %v", sh.Totals.ActualSeconds, sh.Totals.DeclaredSeconds)
	}
	var ext, own *PartPrint
	for i := range sh.Prints {
		switch sh.Prints[i].PrintJobID {
		case j2:
			ext = &sh.Prints[i]
		case j1:
			own = &sh.Prints[i]
		}
	}
	if ext == nil || !ext.Declared || ext.Origin != "external" || ext.Level != LevelDeclared || ext.Cost != nil || ext.Photos.Note == "" {
		t.Errorf("impression déclarée : %+v", ext)
	}
	if own == nil || own.Declared || own.Cost == nil || own.Level != LevelMeasured {
		t.Errorf("impression Printalk : %+v", own)
	}
	if sh.Counts.External != 1 {
		t.Errorf("compteur déclarées : %d", sh.Counts.External)
	}
}

// Bandeau de paliers (CA10.5, CA10.6 côté fiche) : 10 paliers, un indisponible avec sa raison, les autres avec photo.
func TestPhotoStripPaliers(t *testing.T) {
	e := newEnv(t, true)
	e.model("mA", "Équerre")
	j := e.job("mA", jobOpt{hours: 2})
	for _, p := range photoPercents {
		pp := camera.ProgressPhoto{JobID: j, Percent: p, Status: camera.ProgressOK, SHA256: strings.Repeat("a", 63) + string(rune('0'+p/10%10)), Kind: "progress", URL: "/api/camera/photos/x"}
		if p == 40 {
			pp = camera.ProgressPhoto{JobID: j, Percent: 40, Status: camera.ProgressUnavailable, Reason: "caméra injoignable"}
		}
		if p == 100 {
			pp.Kind = "final"
		}
		e.cam.steps[j] = append(e.cam.steps[j], pp)
	}
	sh, err := e.svc.PrintSheet(context.Background(), PrintSheetInput{PrintJobID: j})
	if err != nil {
		t.Fatal(err)
	}
	if len(sh.Photos.Steps) != 10 || sh.Photos.Available != 9 || sh.Photos.Thumbnail == nil || sh.Photos.Thumbnail.Percent != 100 {
		t.Fatalf("bandeau : %+v", sh.Photos)
	}
	if s := sh.Photos.Steps[3]; s.Percent != 40 || s.Status != StepUnavailable || s.Reason != "caméra injoignable" || s.SHA256 != "" {
		t.Errorf("palier 40 : %+v", s)
	}
	if !strings.Contains(sh.Text, "palier 40 % : photo non disponible (caméra injoignable)") {
		t.Errorf("texte : %s", sh.Text)
	}
	// Palier non relevé : « absent » (sauté ou non atteint), distinct de « unavailable ».
	e.cam.steps[j] = e.cam.steps[j][:2]
	sh, _ = e.svc.PrintSheet(context.Background(), PrintSheetInput{PrintJobID: j})
	if sh.Photos.Steps[5].Status != StepAbsent || sh.Photos.Available != 2 {
		t.Errorf("palier non relevé : %+v", sh.Photos.Steps[5])
	}
	// Aucun relevé : liste vide + explication, jamais d'erreur.
	delete(e.cam.steps, j)
	sh, _ = e.svc.PrintSheet(context.Background(), PrintSheetInput{PrintJobID: j})
	if len(sh.Photos.Steps) != 0 || !strings.Contains(sh.Photos.Note, "photo non disponible") {
		t.Errorf("sans relevé : %+v", sh.Photos)
	}
	noSecrets(t, sh)
}

// CA10.1 : le profil affiché est celui relevé à la découpe ; il ne change pas si la source change ensuite.
func TestPrintSheetProfileFrozen(t *testing.T) {
	e := newEnv(t, true)
	e.model("mA", "Équerre")
	j := e.job("mA", jobOpt{hours: 2})
	a, err := e.svc.PrintSheet(context.Background(), PrintSheetInput{PrintJobID: j})
	if err != nil {
		t.Fatal(err)
	}
	if a.Profile.Settings.Process != "0.20mm Standard @BBL X1C" || a.Profile.Settings.NozzleMM != 0.4 || !strings.Contains(a.Profile.Frozen, "ne changent pas") || a.Profile.Level != LevelMeasured {
		t.Fatalf("profil : %+v", a.Profile)
	}
	// Le profil (et la définition du job) changent ensuite : la fiche de journal, déjà créée, reste figée.
	e.exec(`UPDATE print_jobs SET doc=json_set(doc,'$.slice.process','0.08mm Extra Fine','$.summary.process','0.08mm Extra Fine','$.slice.nozzle_mm',0.6) WHERE id=?`, j)
	b, err := e.svc.PrintSheet(context.Background(), PrintSheetInput{PrintJobID: j})
	if err != nil {
		t.Fatal(err)
	}
	if b.Profile.Signature != a.Profile.Signature || b.Profile.Settings.Process != "0.20mm Standard @BBL X1C" || b.Profile.Settings.NozzleMM != 0.4 {
		t.Errorf("profil modifié : %+v", b.Profile)
	}
	// Accessible aussi par l'identifiant de la fiche de journal.
	c, err := e.svc.PrintSheet(context.Background(), PrintSheetInput{PrintJobID: a.LogID})
	if err != nil || c.PrintJobID != j {
		t.Errorf("par fiche : %v %+v", err, c)
	}
}

// CA10.2 : écart = (réel − prévu) / prévu en % ; sans durée réelle : « non disponible ».
func TestPrintSheetDurationDelta(t *testing.T) {
	e := newEnv(t, true)
	e.model("mA", "Équerre")
	long := e.job("mA", jobOpt{hours: 3}) // prévu 2 h, réel 3 h : +50 %
	none := e.job("mA", jobOpt{hours: 0}) // aucun évènement : durée réelle non relevée
	a, _ := e.svc.PrintSheet(context.Background(), PrintSheetInput{PrintJobID: long})
	if a.Duration.DeltaPct == nil || *a.Duration.DeltaPct != 50 || !strings.Contains(a.Duration.Text, "+50,0 %") || a.Duration.PlannedSeconds == nil || *a.Duration.PlannedSeconds != 7200 {
		t.Errorf("écart : %+v", a.Duration)
	}
	b, err := e.svc.PrintSheet(context.Background(), PrintSheetInput{PrintJobID: none})
	if err != nil {
		t.Fatal(err)
	}
	if b.Duration.ActualSeconds != nil || b.Duration.DeltaPct != nil || !strings.Contains(b.Duration.Text, "non disponible") || b.Unavailable["actual_seconds"] == "" || b.Unavailable["duration_delta"] == "" {
		t.Errorf("sans durée réelle : %+v / %v", b.Duration, b.Unavailable)
	}
}

// CA10.3 : alertes avec instant et score ; « caméra non disponible » distinct de « aucune alerte ».
func TestPrintSheetCamera(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, true)
	e.model("mA", "Équerre")
	j := e.job("mA", jobOpt{hours: 2})
	sh, _ := e.svc.PrintSheet(ctx, PrintSheetInput{PrintJobID: j})
	if !sh.Camera.Available || sh.Camera.AlertsStatus != AlertsNone || len(sh.Camera.Alerts) != 0 || !strings.Contains(sh.Camera.Text, "aucune alerte") {
		t.Errorf("aucune alerte : %+v", sh.Camera)
	}
	at := t0.Add(90 * time.Minute)
	e.cam.alerts[j] = []camera.Alert{{ID: "a2", JobID: j, At: at.Add(10 * time.Minute), Score: 0.81, Level: "alert", PhotoURL: "/api/camera/photos/" + strings.Repeat("b", 64)}, {ID: "a1", JobID: j, At: at, Score: 0.55, Level: "warning"}}
	sh, _ = e.svc.PrintSheet(ctx, PrintSheetInput{PrintJobID: j})
	if sh.Camera.AlertsStatus != AlertsListed || len(sh.Camera.Alerts) != 2 || !sh.Camera.Alerts[0].At.Equal(at) || sh.Camera.Alerts[1].Score != 0.81 || sh.Camera.Alerts[1].Level != "alert" {
		t.Errorf("alertes : %+v", sh.Camera)
	}
	// Détection désactivée et aucune trace : caméra non disponible.
	e2 := newEnv(t, true)
	e2.model("mA", "Équerre")
	j2 := e2.job("mA", jobOpt{hours: 2})
	e2.cam.set.Enabled = false
	sh, _ = e2.svc.PrintSheet(ctx, PrintSheetInput{PrintJobID: j2})
	if sh.Camera.Available || sh.Camera.AlertsStatus != AlertsUnavailable || sh.Camera.Reason == "" || strings.Contains(sh.Camera.Text, "aucune alerte levée pendant") {
		t.Errorf("détection désactivée : %+v", sh.Camera)
	}
	// Pas de service caméra du tout.
	e3 := newEnv(t, false)
	e3.model("mA", "Équerre")
	j3 := e3.job("mA", jobOpt{hours: 2})
	sh, _ = e3.svc.PrintSheet(ctx, PrintSheetInput{PrintJobID: j3})
	if sh.Camera.AlertsStatus != AlertsUnavailable || sh.Camera.Alerts == nil || !strings.Contains(sh.Camera.Text, "caméra non disponible") {
		t.Errorf("sans caméra : %+v", sh.Camera)
	}
	// Imprimante exclue de la surveillance.
	e4 := newEnv(t, true)
	e4.model("mA", "Équerre")
	j4 := e4.job("mA", jobOpt{hours: 2})
	e4.cam.set.DisabledPrinters = []string{"pr1"}
	sh, _ = e4.svc.PrintSheet(ctx, PrintSheetInput{PrintJobID: j4})
	if sh.Camera.AlertsStatus != AlertsUnavailable || !strings.Contains(sh.Camera.Reason, "exclue") {
		t.Errorf("imprimante exclue : %+v", sh.Camera)
	}
}

// Accord humain lu dans l'audit : canal et date, jamais de code ni de jeton ; impression déclarée : sans objet.
func TestPrintSheetApproval(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, true)
	e.model("mA", "Équerre")
	j := e.job("mA", jobOpt{hours: 2})
	sh, _ := e.svc.PrintSheet(ctx, PrintSheetInput{PrintJobID: j})
	if sh.Approval.Status != ApprovalNone {
		t.Errorf("sans audit : %+v", sh.Approval)
	}
	_ = e.st.Audit(ctx, "model", "print.confirmation_requested", "print_job", j, map[string]any{"confirmation": "cf_1"})
	if sh, _ = e.svc.PrintSheet(ctx, PrintSheetInput{PrintJobID: j}); sh.Approval.Status != ApprovalRequested || sh.Approval.RequestedAt.IsZero() {
		t.Errorf("demande seule : %+v", sh.Approval)
	}
	_ = e.st.Audit(ctx, "human:web", "print.confirmation_approved", "print_job", j, map[string]any{"confirmation": "cf_1", "plate_clear": true})
	_ = e.st.Audit(ctx, "human:app-code", "print.confirmation_code_accepted", "print_job", j, map[string]any{"code": "123456"})
	sh, _ = e.svc.PrintSheet(ctx, PrintSheetInput{PrintJobID: j})
	if sh.Approval.Status != ApprovalApproved || sh.Approval.ApprovedAt.IsZero() || sh.Approval.Via == "" || !strings.Contains(sh.Approval.Text, "approuvé par l'humain") {
		t.Errorf("accord : %+v", sh.Approval)
	}
	raw, _ := json.Marshal(sh.Approval)
	if strings.Contains(string(raw), "123456") || strings.Contains(string(raw), "cf_1") {
		t.Errorf("détail d'audit dans la sortie : %s", raw)
	}
	// Déclarée hors Printalk : pas d'accord à relever, pas de caméra, pas de coût mesuré.
	e.exec(`UPDATE print_logs SET origin='external' WHERE print_job_id=?`, j)
	sh, _ = e.svc.PrintSheet(ctx, PrintSheetInput{PrintJobID: j})
	if sh.Approval.Status != ApprovalNotApplicable || !sh.Header.Declared || sh.Header.Origin != "external" || sh.Cost.Actual != nil || sh.Camera.AlertsStatus != AlertsUnavailable ||
		sh.Profile.Level != LevelDeclared || sh.Levels["profile"] != LevelDeclared || !strings.Contains(sh.Text, "déclarée hors Printalk") {
		t.Errorf("déclarée : %+v / %+v", sh.Header, sh.Approval)
	}
}

// Verdict, matière (grammes estimés signalés), coût, niveaux ; impression inconnue ou non terminée : erreur claire.
func TestPrintSheetVerdictMaterialAndErrors(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, true)
	e.model("mA", "Équerre")
	j := e.job("mA", jobOpt{hours: 2})
	if _, err := e.ins.SetVerdict(ctx, insights.SetVerdictInput{PrintJobID: j, Verdict: "failed", Reason: "stringing", Note: "fils partout", Source: insights.SourceHumanCard}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ins.GetLog(ctx, j); err != nil {
		t.Fatal(err)
	}
	e.exec(`INSERT INTO spool_usage(print_job_id,spool_id,filament_slot,grams,estimated,price_per_kg,cost,note,recorded_at) VALUES(?,?,?,?,?,?,?,?,?)`, j, "sp_1", 1, 98.5, 1, 20.0, 1.97, "", store.FormatTime(t0))
	sh, err := e.svc.PrintSheet(ctx, PrintSheetInput{PrintJobID: j})
	if err != nil {
		t.Fatal(err)
	}
	if sh.Verdict.Value != "failed" || sh.Verdict.FailReason != "stringing" || sh.Verdict.Note != "fils partout" || sh.Verdict.Source != insights.SourceHumanCard || sh.Verdict.Level != LevelDeclared || len(sh.Verdict.FailReasons) == 0 {
		t.Errorf("verdict : %+v", sh.Verdict)
	}
	if len(sh.Material.Spools) != 1 || sh.Material.Spools[0].Grams != 98.5 || !sh.Material.Spools[0].Estimated || !sh.Material.Estimated || sh.Material.ActualGrams == nil {
		t.Errorf("matière : %+v", sh.Material)
	}
	if sh.Cost.Actual == nil || sh.Cost.Planned == nil || sh.Cost.Level != LevelComputed || sh.Levels["cost"] != LevelComputed || sh.Levels["duration"] != LevelMeasured {
		t.Errorf("coût / niveaux : %+v %v", sh.Cost, sh.Levels)
	}
	noSecrets(t, sh)
	// Impression en cours : fiche pas encore créée.
	run := e.job("mA", jobOpt{state: "PRINTING", percent: 40, hours: 0})
	if _, err := e.svc.PrintSheet(ctx, PrintSheetInput{PrintJobID: run}); !errors.Is(err, store.ErrNotFound) || !strings.Contains(err.Error(), "pas terminée") {
		t.Errorf("non terminée : %v", err)
	}
	if _, err := e.svc.PrintSheet(ctx, PrintSheetInput{PrintJobID: "inconnue"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("inconnue : %v", err)
	}
	if _, err := e.svc.PrintSheet(ctx, PrintSheetInput{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("vide : %v", err)
	}
}

// La signature de réglages est stable et sensible aux réglages.
func TestSettingsSignature(t *testing.T) {
	a := insights.EffectiveSettings{Machine: "m", Process: "p", Filaments: []string{"f"}, NozzleMM: 0.4, Overrides: []slicer.Override{{Key: "a", Value: 1}, {Key: "b", Value: 2}}}
	b := a
	b.Overrides = []slicer.Override{{Key: "b", Value: 2}, {Key: "a", Value: 1}}
	if insights.SettingsSignature(a) != insights.SettingsSignature(b) {
		t.Error("l'ordre des surcharges ne doit pas compter")
	}
	c := a
	c.NozzleMM = 0.6
	if insights.SettingsSignature(a) == insights.SettingsSignature(c) || !strings.HasPrefix(insights.SettingsSignature(a), "sig_") {
		t.Error("la buse doit changer la signature")
	}
}

// Garde-fou : aucun lancement, arrêt, reprise, approbation ni annulation dans le paquet ; aucune dépendance à la chaîne d'impression.
func TestPackageNeverStartsStopsResumesOrApproves(t *testing.T) {
	re := regexp.MustCompile(`\.(Approve|Start|Stop|Resume|Cancel|Launch)[A-Za-z]*\(`)
	files, _ := filepath.Glob("*.go")
	if len(files) < 4 {
		t.Fatal("sources introuvables")
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		if m := re.FindString(string(b)); m != "" {
			t.Errorf("%s : appel interdit %q", f, m)
		}
		for _, imp := range []string{"internal/printing\"", "internal/printer\"", "internal/filament\""} {
			if strings.Contains(string(b), imp) {
				t.Errorf("%s : import interdit %s", f, imp)
			}
		}
	}
}
