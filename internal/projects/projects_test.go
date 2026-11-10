package projects

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

var t0 = time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)

type env struct {
	t   *testing.T
	st  *store.Store
	ins *insights.Service
	svc *Service
	n   int
}

func newEnv(t *testing.T) *env {
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
	svc, err := New(Config{Store: st, Counts: ins, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, st: st, ins: ins, svc: svc}
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.st.DB().Exec(q, args...); err != nil {
		e.t.Fatalf("%s : %v", q, err)
	}
}

func (e *env) model(id, name, collection string) {
	e.t.Helper()
	ts := store.FormatTime(t0)
	var cid any
	if collection != "" {
		e.exec(`INSERT OR IGNORE INTO collections(id,name,created_at) VALUES(?,?,?)`, collection, collection, ts)
		cid = collection
	}
	e.exec(`INSERT INTO models(id,collection_id,name,created_at,updated_at) VALUES(?,?,?,?,?)`, id, cid, name, ts, ts)
}

// print enregistre une impression de 2 h pour 100 g de PLA ; percent < 100 avec state CANCELLED : annulée avant la fin.
func (e *env) print(model, state string, percent int) string {
	e.t.Helper()
	e.n++
	id := model + "_job" + string(rune('a'+e.n))
	start := t0.AddDate(0, 0, e.n)
	end := start.Add(2 * time.Hour)
	events := []map[string]any{{"at": start, "event": "go", "from": "START_REQUESTED", "to": "PRINTING"}}
	if state != "PRINTING" {
		events = append(events, map[string]any{"at": end, "event": "fin", "from": "PRINTING", "to": state})
	}
	doc := map[string]any{
		"id": id, "state": state, "printer_id": "pr1", "model_id": model, "name": id + ".gcode.3mf", "plate": 1,
		"assignments": []map[string]any{{"filament_slot": 1, "tray": "ams1:1", "material_id": "Bambu PLA Basic noir"}},
		"summary": map[string]any{
			"plates":        []slicer.PlateResult{{Index: 1, PredictedSeconds: 7200, WeightGrams: 100, Layers: 100, Filaments: []slicer.FilamentUse{{Slot: 1, Type: "PLA", Grams: 100}}}},
			"printer_model": "Bambu Lab P1S", "machine_preset": "Bambu Lab P1S 0.4 nozzle", "process": "0.20mm Standard @BBL X1C",
			"filament_presets": []string{"Bambu PLA Basic @BBL P1S 0.4 nozzle"},
		},
		"print": map[string]any{"percent": percent}, "events": events,
		"slice":      map[string]any{"machine": "Bambu Lab P1S 0.4 nozzle", "process": "0.20mm Standard @BBL X1C", "filaments": []string{"Bambu PLA Basic @BBL P1S 0.4 nozzle"}, "nozzle_mm": 0.4},
		"created_at": t0, "updated_at": end,
	}
	if err := e.st.PutDoc(context.Background(), "test", "print_jobs", id, doc); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) done(model string) string { return e.print(model, "COMPLETED", 100) }

func (e *env) verdict(job, v, reason string) {
	e.t.Helper()
	if _, err := e.ins.SetVerdict(context.Background(), insights.SetVerdictInput{PrintJobID: job, Verdict: v, Reason: reason}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) project(name, parent string) *Project {
	e.t.Helper()
	p, err := e.svc.Create(context.Background(), CreateInput{Name: name, ParentID: parent})
	if err != nil {
		e.t.Fatalf("créer %s : %v", name, err)
	}
	return p
}

func (e *env) add(project, model string, qty int) {
	e.t.Helper()
	if _, err := e.svc.AddPart(context.Background(), PartInput{ProjectID: project, ModelID: model, Quantity: qty}); err != nil {
		e.t.Fatal(err)
	}
}

// CA2.1 : 3 pièces (quantités 2, 1, 3), impressions à 100 % (2, 1, 1) -> 2/3 pièces, 4/6 unités, coût et temps = sommes.
func TestProgressCA21(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, m := range []string{"mA", "mB", "mC"} {
		e.model(m, "Pièce "+m, "")
	}
	p := e.project("Étagère", "")
	e.add(p.ID, "mA", 2)
	e.add(p.ID, "mB", 1)
	e.add(p.ID, "mC", 3)
	for _, m := range []string{"mA", "mA", "mB", "mC"} {
		e.done(m)
	}
	d, err := e.svc.Get(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	g := d.Progress
	if g.PartsDone != 2 || g.PartsTotal != 3 || g.UnitsPrinted != 4 || g.UnitsTotal != 6 || g.Complete {
		t.Fatalf("avancement : %+v", g)
	}
	if !strings.Contains(g.Text, "2/3 pièces imprimées") || !strings.Contains(g.Text, "4/6 unités") || !strings.Contains(g.Text, "4 non jugées") {
		t.Errorf("texte : %s", g.Text)
	}
	logs, _ := e.ins.ListLogs(ctx, insights.ListQuery{})
	var cost, secs float64
	for _, l := range logs.Entries {
		cost += l.ActualCost.Total
		secs += l.Actual.Seconds
	}
	if !d.Totals.Available || d.Totals.ActualCost != r2(cost) || d.Totals.ActualSeconds != r2(secs) || secs != 4*7200 || d.Totals.ActualCost <= 0 {
		t.Errorf("totaux : %+v (coût attendu %v, temps %v)", d.Totals, cost, secs)
	}
	if d.SuggestedStatus != StatusPrinting || d.Project.Status != StatusIdea {
		t.Errorf("suggestion %q, statut %q", d.SuggestedStatus, d.Project.Status)
	}
	if !strings.Contains(d.NextAction, "Pièce mC") || !strings.Contains(d.NextAction, "1/3") {
		t.Errorf("prochaine action : %s", d.NextAction)
	}
	if d.Levels["verdict"] != "déclaré" || d.Levels["temps"] != "mesuré" {
		t.Errorf("niveaux : %v", d.Levels)
	}
}

// CA2.2 : imprimée = 100 % atteint, verdict ou pas ; une ratée compte imprimée mais pas réussie ; une annulée à 60 % ne compte pas.
func TestProgressCA22VerdictIsSeparateFromPrinted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.model("mA", "Équerre", "")
	e.model("mB", "Support", "")
	e.model("mC", "Capot", "")
	p := e.project("Boîte", "")
	e.add(p.ID, "mA", 1)
	e.add(p.ID, "mB", 1)
	e.add(p.ID, "mC", 1)
	e.verdict(e.done("mA"), "failed", "warping")
	e.done("mB")
	e.print("mC", "CANCELLED", 60)
	d, _ := e.svc.Get(ctx, p.ID)
	by := map[string]PartProgress{}
	for _, x := range d.Parts {
		by[x.ModelID] = x
	}
	a, b, c := by["mA"], by["mB"], by["mC"]
	if !a.Satisfied || a.JudgedFailed != 1 || a.JudgedSuccess != 0 || !strings.Contains(a.Text, "jugée ratée") || strings.Contains(a.Text, "jugée réussie") {
		t.Errorf("ratée : %+v", a)
	}
	if !b.Satisfied || b.Unjudged != 1 || !strings.Contains(b.Text, "non jugée") {
		t.Errorf("non jugée : %+v", b)
	}
	if c.Satisfied || c.Printed != 0 || c.Attempts != 1 || !strings.Contains(c.Text, "aucune impression n'a atteint 100") {
		t.Errorf("annulée à 60 %% : %+v", c)
	}
	if d.Progress.PartsDone != 2 || d.Progress.UnitsSuccess != 0 || d.Progress.UnitsFailed != 1 || d.Progress.UnitsUnjudged != 1 {
		t.Errorf("avancement : %+v", d.Progress)
	}
	// un verdict de réussite s'ajoute aux « jugées réussies » sans changer « imprimée »
	e.verdict(e.done("mC"), "success", "")
	d, _ = e.svc.Get(ctx, p.ID)
	if d.Progress.PartsDone != 3 || d.Progress.UnitsSuccess != 1 || !d.Progress.Complete || d.SuggestedStatus != StatusDone {
		t.Errorf("après réussite : %+v suggestion %q", d.Progress, d.SuggestedStatus)
	}
	if d.Project.Status != StatusIdea { // CA2.4 : la suggestion n'est jamais appliquée seule
		t.Errorf("statut modifié : %s", d.Project.Status)
	}
	if after, _ := e.svc.Get(ctx, p.ID); after.Project.Status != StatusIdea || after.Project.Rev != d.Project.Rev {
		t.Error("lire un projet le modifie")
	}
}

// CA2.7 : A = P1 x2 + sous-projet B (P2 x1, P3 x1).
func TestSubProjectsAggregateCA27(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, m := range []string{"P1", "P2", "P3"} {
		e.model(m, m, "")
	}
	a := e.project("Assemblage A", "")
	b := e.project("Sous-assemblage B", a.ID)
	e.add(a.ID, "P1", 2)
	e.add(b.ID, "P2", 1)
	e.add(b.ID, "P3", 1)
	e.done("P1")
	e.done("P1")
	e.done("P2")
	da, err := e.svc.Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if da.Progress.UnitsTotal != 4 || da.Progress.PartsTotal != 3 || da.Progress.UnitsPrinted != 3 || da.Own.UnitsTotal != 2 || da.Own.UnitsPrinted != 2 {
		t.Errorf("A : %+v / propre %+v", da.Progress, da.Own)
	}
	if len(da.SubProjects) != 1 || da.SubProjects[0].Progress.UnitsTotal != 2 || da.SubProjects[0].Progress.UnitsPrinted != 1 || da.SubProjects[0].PartsCount != 2 {
		t.Fatalf("sous-projets : %+v", da.SubProjects)
	}
	db, _ := e.svc.Get(ctx, b.ID)
	if db.Progress.UnitsTotal != 2 || db.Level != 2 || len(db.Path) != 1 || db.Path[0].ID != a.ID {
		t.Errorf("B : %+v niveau %d chemin %v", db.Progress, db.Level, db.Path)
	}
	// la même pièce dans A et B est comptée dans chacun (par chemin d'appartenance)
	e.add(b.ID, "P1", 1)
	da, _ = e.svc.Get(ctx, a.ID)
	db, _ = e.svc.Get(ctx, b.ID)
	if da.Progress.UnitsTotal != 5 || db.Progress.UnitsTotal != 3 || da.Progress.PartsTotal != 4 {
		t.Errorf("pièce partagée : A %+v B %+v", da.Progress, db.Progress)
	}
	if da.Totals.Attempts != 3 { // une pièce distincte n'est pas comptée deux fois dans les totaux
		t.Errorf("totaux : %+v", da.Totals)
	}
}

// CA2.8 : cycle, profondeur, parent unique.
func TestHierarchyGuardsCA28(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.project("A", "")
	b := e.project("B", a.ID)
	c := e.project("C", b.ID)
	// A sous son descendant C : refusé, rien n'est modifié
	before, _ := e.svc.Get(ctx, a.ID)
	_, err := e.svc.Update(ctx, UpdateInput{ID: a.ID, ParentID: ptr(c.ID)})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle : %v", err)
	}
	after, _ := e.svc.Get(ctx, a.ID)
	if after.Project.ParentID != "" || after.Project.Rev != before.Project.Rev {
		t.Errorf("rien ne doit changer : %+v", after.Project)
	}
	if _, err := e.svc.Update(ctx, UpdateInput{ID: a.ID, ParentID: ptr(a.ID)}); !errors.Is(err, ErrInvalid) {
		t.Errorf("son propre parent : %v", err)
	}
	// profondeur : A > B > C > D (4 niveaux) accepté, un 5e refusé
	d := e.project("D", c.ID)
	if _, err := e.svc.Create(ctx, CreateInput{Name: "E", ParentID: d.ID}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "profondeur") {
		t.Fatalf("profondeur à la création : %v", err)
	}
	// rattacher un sous-arbre qui ferait dépasser la borne : refusé
	x := e.project("X", "")
	y := e.project("Y", x.ID)
	if _, err := e.svc.Update(ctx, UpdateInput{ID: x.ID, ParentID: ptr(d.ID)}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "profondeur") {
		t.Fatalf("profondeur du sous-arbre : %v (y=%s)", err, y.ID)
	}
	// parent inconnu
	if _, err := e.svc.Create(ctx, CreateInput{Name: "Z", ParentID: "prj_nope"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("parent inconnu : %v", err)
	}
	// un sous-projet n'a qu'un parent : changer de parent remplace, ne duplique pas
	other := e.project("Autre", "")
	if _, err := e.svc.Update(ctx, UpdateInput{ID: y.ID, ParentID: ptr(other.ID)}); err != nil {
		t.Fatal(err)
	}
	oy, _ := e.svc.Get(ctx, other.ID)
	ox, _ := e.svc.Get(ctx, x.ID)
	if len(oy.SubProjects) != 1 || len(ox.SubProjects) != 0 {
		t.Errorf("parent unique : %v %v", oy.SubProjects, ox.SubProjects)
	}
	// détacher : redevient racine
	if p, err := e.svc.Update(ctx, UpdateInput{ID: y.ID, ParentID: ptr("")}); err != nil || p.ParentID != "" {
		t.Errorf("détacher : %v %v", p, err)
	}
}

func TestValidationStatusAndArchive(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, in := range []CreateInput{{Name: ""}, {Name: strings.Repeat("x", 121)}, {Name: "ok", Status: "fini"}, {Name: "ok", DueDate: "demain"}, {Name: "ok", Description: strings.Repeat("d", 2001)}, {Name: "ok", CollectionID: "col_nope"}} {
		if _, err := e.svc.Create(ctx, in); err == nil {
			t.Errorf("%+v : erreur attendue", in)
		}
	}
	p, err := e.svc.Create(ctx, CreateInput{Name: "  Lampe  ", DueDate: "2026-12-24"})
	if err != nil || p.Name != "Lampe" || p.Status != StatusIdea || p.DueDate != "2026-12-24" || p.Rev != 1 {
		t.Fatalf("création : %+v %v", p, err)
	}
	// abandoned et in_use posent closed_at ; un autre statut l'efface
	u, _ := e.svc.Update(ctx, UpdateInput{ID: p.ID, Status: StatusInUse})
	if u.ClosedAt.IsZero() || u.Rev != 2 {
		t.Errorf("in_use : %+v", u)
	}
	u, _ = e.svc.Update(ctx, UpdateInput{ID: p.ID, Status: StatusDesign})
	if !u.ClosedAt.IsZero() {
		t.Errorf("closed_at non effacé : %+v", u)
	}
	if _, err := e.svc.Update(ctx, UpdateInput{ID: p.ID, Status: "nope"}); !errors.Is(err, ErrInvalid) {
		t.Error("statut inconnu")
	}
	if _, err := e.svc.Update(ctx, UpdateInput{ID: "prj_nope", Name: ptr("x")}); !errors.Is(err, ErrNotFound) {
		t.Error("projet inconnu")
	}
	// aucun changement : pas de nouvelle révision
	same, _ := e.svc.Update(ctx, UpdateInput{ID: p.ID, Name: ptr("Lampe")})
	if same.Rev != u.Rev {
		t.Errorf("rev %d -> %d", u.Rev, same.Rev)
	}
	// CA2.6 : archiver masque de list_projects par défaut ; aucune suppression
	child := e.project("Enfant", p.ID)
	if _, err := e.svc.Update(ctx, UpdateInput{ID: p.ID, Archived: ptrB(true)}); err != nil {
		t.Fatal(err)
	}
	l, _ := e.svc.List(ctx, ListQuery{})
	if l.Total != 1 || l.Projects[0].Project.ID != child.ID { // le sous-projet reste consultable, au premier niveau
		t.Errorf("liste : %+v", l.Projects)
	}
	l, _ = e.svc.List(ctx, ListQuery{IncludeArchived: true})
	if l.Total != 1 || l.Projects[0].Project.ID != p.ID || len(l.Projects) != 2 || l.Projects[1].Depth != 1 || l.Projects[1].Project.ParentID != p.ID {
		t.Errorf("liste avec archivés : %+v", l)
	}
	if _, err := e.svc.Get(ctx, p.ID); err != nil {
		t.Errorf("un projet archivé reste consultable : %v", err)
	}
	var n int
	e.st.DB().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE operation LIKE 'project.%'`).Scan(&n)
	if n < 4 {
		t.Errorf("audit : %d", n)
	}
}

func TestPartsAndListTree(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.model("mA", "Équerre", "")
	e.model("mB", "Vis", "")
	p1 := e.project("P1", "")
	p2 := e.project("P2", "")
	e.project("Sous-P1", p1.ID)
	// la même pièce dans deux projets compte dans les deux (CA2.3)
	e.add(p1.ID, "mA", 2)
	e.add(p2.ID, "mA", 1)
	e.done("mA")
	d1, _ := e.svc.Get(ctx, p1.ID)
	d2, _ := e.svc.Get(ctx, p2.ID)
	if d1.Progress.UnitsPrinted != 1 || d2.Progress.UnitsPrinted != 1 || !d2.Progress.Complete {
		t.Errorf("pièce partagée : %+v %+v", d1.Progress, d2.Progress)
	}
	refs, _ := e.svc.ProjectsOfModel(ctx, "mA")
	if len(refs) != 2 {
		t.Errorf("projets de la pièce : %v", refs)
	}
	// mise à jour de la quantité (rôle conservé), bornes, modèle inconnu
	if _, err := e.svc.AddPart(ctx, PartInput{ProjectID: p1.ID, ModelID: "mA", Role: "support"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddPart(ctx, PartInput{ProjectID: p1.ID, ModelID: "mA", Quantity: 3}); err != nil {
		t.Fatal(err)
	}
	d1, _ = e.svc.Get(ctx, p1.ID)
	if len(d1.Parts) != 1 || d1.Parts[0].Quantity != 3 || d1.Parts[0].Role != "support" {
		t.Errorf("mise à jour : %+v", d1.Parts)
	}
	for _, in := range []PartInput{{ProjectID: p1.ID, ModelID: "mB", Quantity: 1001}, {ProjectID: p1.ID, ModelID: "mB", Quantity: -1}, {ProjectID: p1.ID, ModelID: "m_nope"}, {ProjectID: "prj_nope", ModelID: "mB"}, {ProjectID: p1.ID}} {
		if _, err := e.svc.AddPart(ctx, in); err == nil {
			t.Errorf("%+v : erreur attendue", in)
		}
	}
	// pièce dans la corbeille : refusée
	e.exec(`UPDATE models SET deleted_at=? WHERE id='mB'`, store.FormatTime(t0))
	if _, err := e.svc.AddPart(ctx, PartInput{ProjectID: p1.ID, ModelID: "mB"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("corbeille : %v", err)
	}
	if _, err := e.svc.RemovePart(ctx, p1.ID, "mB"); !errors.Is(err, ErrNotFound) {
		t.Errorf("retrait d'une pièce absente : %v", err)
	}
	if _, err := e.svc.RemovePart(ctx, p1.ID, "mA"); err != nil {
		t.Fatal(err)
	}
	d1, _ = e.svc.Get(ctx, p1.ID)
	if len(d1.Parts) != 0 || d1.Progress.Text != "aucune pièce" || !strings.Contains(d1.NextAction, "Ajouter des pièces") && len(d1.SubProjects) == 0 {
		t.Errorf("après retrait : %+v", d1)
	}
	// liste : arbre par défaut, descendre avec parent_id, filtre de statut = liste plate
	l, _ := e.svc.List(ctx, ListQuery{})
	if l.Total != 2 || len(l.Applied) == 0 || l.Counts[StatusIdea] != 3 {
		t.Fatalf("liste : %+v", l)
	}
	var nested int
	for _, it := range l.Projects {
		if it.Depth == 1 {
			nested++
		}
	}
	if nested != 1 || len(l.Projects) != 3 {
		t.Errorf("arbre : %d sous-projets imbriqués", nested)
	}
	sub, _ := e.svc.List(ctx, ListQuery{ParentID: p1.ID})
	if sub.Total != 1 || sub.Projects[0].Project.Name != "Sous-P1" {
		t.Errorf("descendre : %+v", sub)
	}
	flat, _ := e.svc.List(ctx, ListQuery{Status: StatusIdea})
	if flat.Total != 3 {
		t.Errorf("liste plate : %d", flat.Total)
	}
	if _, err := e.svc.List(ctx, ListQuery{Status: "nope"}); !errors.Is(err, ErrInvalid) {
		t.Error("statut de filtre inconnu")
	}
	if _, err := e.svc.List(ctx, ListQuery{ParentID: "prj_nope"}); !errors.Is(err, ErrNotFound) {
		t.Error("parent de filtre inconnu")
	}
	empty := newEnv(t)
	if l, err := empty.svc.List(ctx, ListQuery{}); err != nil || l.Total != 0 || len(l.Projects) != 0 || !strings.Contains(l.Note, "create_project") {
		t.Errorf("liste vide : %+v %v", l, err)
	}
}

func TestAdoptedCollectionAndTotalsUnavailable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.model("mA", "Équerre", "col1")
	e.model("mB", "Vis", "col1")
	p, err := e.svc.Create(ctx, CreateInput{Name: "Atelier", CollectionID: "col1"})
	if err != nil {
		t.Fatal(err)
	}
	e.add(p.ID, "mA", 2) // cité explicitement : quantité 2 ; mB adoptée : quantité 1
	d, _ := e.svc.Get(ctx, p.ID)
	if len(d.Parts) != 2 || d.Progress.UnitsTotal != 3 {
		t.Fatalf("pièces : %+v", d.Parts)
	}
	var implicit int
	for _, x := range d.Parts {
		if x.Implicit {
			implicit++
			if x.ModelID != "mB" || x.Quantity != 1 {
				t.Errorf("adoptée : %+v", x)
			}
		}
	}
	if implicit != 1 {
		t.Errorf("pièces adoptées : %d", implicit)
	}
	// jamais imprimé : coût et temps « non disponibles » avec la raison, pas de zéro présenté comme une mesure
	if d.Totals.Available || d.Totals.Reason == "" || !strings.Contains(d.Note, "non disponibles") {
		t.Errorf("totaux : %+v / %s", d.Totals, d.Note)
	}
	if d.SuggestedStatus != "" {
		t.Errorf("suggestion sans impression : %q", d.SuggestedStatus)
	}
	if _, err := e.svc.RemovePart(ctx, p.ID, "mB"); !errors.Is(err, ErrNotFound) {
		t.Errorf("retrait d'une pièce adoptée : %v", err)
	}
	// en cours : tentative comptée, coût estimé à part
	e.print("mA", "PRINTING", 30)
	d, _ = e.svc.Get(ctx, p.ID)
	if d.Totals.InProgress != 1 || d.Totals.PlannedCost <= 0 || d.Totals.Available || !strings.Contains(d.NextAction, "en cours") {
		t.Errorf("en cours : %+v / %s", d.Totals, d.NextAction)
	}
	if d.SuggestedStatus != StatusPrinting {
		t.Errorf("suggestion : %q", d.SuggestedStatus)
	}
}

func TestSuggestStatusAndDescribeCounts(t *testing.T) {
	done := Progress{PartsDone: 2, PartsTotal: 2, Complete: true}
	part := Progress{PartsDone: 1, PartsTotal: 2}
	cases := []struct {
		cur      string
		arch     bool
		p        Progress
		att, inp int
		want     string
	}{
		{StatusIdea, false, done, 2, 0, StatusDone},
		{StatusDone, false, done, 2, 0, ""},
		{StatusInUse, false, done, 2, 0, ""},
		{StatusPrinting, false, done, 2, 0, StatusDone},
		{StatusIdea, false, part, 1, 0, StatusPrinting},
		{StatusDesign, false, part, 0, 1, StatusPrinting},
		{StatusPrinting, false, part, 1, 0, ""},
		{StatusIdea, false, part, 0, 0, ""},
		{StatusAbandoned, false, done, 2, 0, ""},
		{StatusIdea, true, done, 2, 0, ""},
		{StatusIdea, false, Progress{}, 3, 0, ""},
	}
	for _, c := range cases {
		if got, _ := SuggestStatus(c.cur, c.arch, c.p, c.att, c.inp); got != c.want {
			t.Errorf("%+v : %q, attendu %q", c, got, c.want)
		}
	}
	if s := DescribeCounts(insights.PrintCounts{}); s != "jamais imprimée" {
		t.Errorf("jamais : %s", s)
	}
	s := DescribeCounts(insights.PrintCounts{Printed: 5, Success: 4, Failed: 1, TopFailReason: "stringing", TopFailReasonN: 1})
	if s != "imprimée 5 fois (100 % atteint), 4 jugées réussies, 1 jugée ratée (raison la plus fréquente : stringing, n = 1)" {
		t.Errorf("décrit : %s", s)
	}
}

// Garde-fou : aucun appel de lancement, d'arrêt, de reprise, d'approbation ou d'annulation dans le paquet.
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

func ptr[T any](v T) *T { return &v }
func ptrB(v bool) *bool { return &v }
