package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/goals"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func TestJournalSyncCreatesOneEntryPerFinishedJob(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedPrinter("pr1", "p1s", "Atelier P1S")
	e.seedModel("mod1", "Boîtier", "col1", "Domotique")
	e.done("jobA", 1, "COMPLETED")
	e.done("jobB", 2, "FAILED")
	e.done("jobC", 3, "CANCELLED")
	e.seedJob(jobSpec{ID: "jobD", State: "PRINTING", Printer: "pr1", Seconds: 100, Grams: 1, Start: t0})
	e.seedJob(jobSpec{ID: "jobE", State: "PREPARED", Printer: "pr1", Seconds: 100, Grams: 1})

	out, err := e.svc.ListLogs(ctx, ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 3 || out.Created != 3 || len(out.Entries) != 3 || len(out.Defects) < 10 {
		t.Fatalf("liste : total %d créées %d", out.Total, out.Created)
	}
	again, _ := e.svc.ListLogs(ctx, ListQuery{})
	if again.Created != 0 || again.Total != 3 {
		t.Errorf("idempotence : %+v", again.Created)
	}
	byJob := map[string]LogEntry{}
	for _, en := range out.Entries {
		byJob[en.PrintJobID] = en
	}
	a := byJob["jobA"]
	if a.Outcome != "success" || a.State != "COMPLETED" || a.MaterialFamily != "PLA" || a.CollectionName != "Domotique" || a.ModelName != "Boîtier" ||
		a.PrinterModel != "Bambu Lab P1S" || a.Settings.Process == "" || a.Settings.Layers != 100 || a.PlannedCost == nil || a.ActualCost == nil {
		t.Fatalf("fiche A : %+v", a)
	}
	if a.Estimated.Seconds != 7200 || a.Actual.Seconds != 7200 || a.Estimated.Grams != 100 || a.DurationDelta == nil || *a.DurationDelta != 0 {
		t.Errorf("quantités : %+v %+v %v", a.Estimated, a.Actual, a.DurationDelta)
	}
	if byJob["jobB"].Outcome != "failed" || byJob["jobC"].Outcome != "cancelled" {
		t.Errorf("issues : %s %s", byJob["jobB"].Outcome, byJob["jobC"].Outcome)
	}
	// ordre : la plus récente d'abord
	if out.Entries[0].PrintJobID != "jobC" {
		t.Errorf("ordre : %s", out.Entries[0].PrintJobID)
	}
	// filtres
	f, _ := e.svc.ListLogs(ctx, ListQuery{Outcome: "failed"})
	if f.Total != 1 || f.Entries[0].PrintJobID != "jobB" {
		t.Errorf("filtre issue : %+v", f.Total)
	}
	f, _ = e.svc.ListLogs(ctx, ListQuery{MaterialFamily: "petg"})
	if f.Total != 0 {
		t.Errorf("filtre matière : %d", f.Total)
	}
	if _, err := e.svc.ListLogs(ctx, ListQuery{Outcome: "meh"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("issue inconnue : %v", err)
	}
	// lecture par impression ou par fiche
	g, err := e.svc.GetLog(ctx, "jobA")
	if err != nil || g.ID != a.ID {
		t.Fatalf("get par impression : %v", err)
	}
	if g2, err := e.svc.GetLog(ctx, a.ID); err != nil || g2.PrintJobID != "jobA" {
		t.Fatalf("get par fiche : %v", err)
	}
	if _, err := e.svc.GetLog(ctx, "jobD"); !errors.Is(err, ErrNotFound) {
		t.Errorf("impression en cours : %v", err)
	}
}

func TestUpdateLogFieldsAndValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedPrinter("pr1", "p1s", "P1S")
	e.done("jobA", 1, "COMPLETED")
	rating, comment := 4, "  belle pièce, un peu de fils  "
	en, err := e.svc.UpdateLog(ctx, UpdateInput{ID: "jobA", Rating: &rating, Comment: &comment, Defects: []string{"Stringing", "stringing", "layer_lines"}, Outcome: "partial"})
	if err != nil {
		t.Fatal(err)
	}
	if en.Rating != 4 || en.Comment != "belle pièce, un peu de fils" || len(en.Defects) != 2 || en.Outcome != "partial" {
		t.Fatalf("mise à jour : %+v", en)
	}
	// le tout est persisté et l'index suit l'issue
	var oc string
	if err := e.st.DB().QueryRow(`SELECT outcome FROM print_logs WHERE id=?`, en.ID).Scan(&oc); err != nil || oc != "partial" {
		t.Errorf("colonne outcome : %q %v", oc, err)
	}
	bad0, bad6 := 0, 6
	long := strings.Repeat("x", 4001)
	for name, in := range map[string]UpdateInput{
		"note 0":           {ID: "jobA", Rating: &bad0},
		"note 6":           {ID: "jobA", Rating: &bad6},
		"défaut inconnu":   {ID: "jobA", Defects: []string{"licorne"}},
		"issue inconnue":   {ID: "jobA", Outcome: "super"},
		"commentaire":      {ID: "jobA", Comment: &long},
		"blob absent":      {ID: "jobA", PhotoBlobs: []string{strings.Repeat("a", 64)}},
		"expérience seule": {ID: "jobA", ExperimentID: "exp_1"},
		"masse négative":   {ID: "jobA", ActualGrams: ptr(-1.0)},
		"sans id":          {},
	} {
		if _, err := e.svc.UpdateLog(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s : %v", name, err)
		}
	}
	if _, err := e.svc.UpdateLog(ctx, UpdateInput{ID: "inconnu"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("fiche inconnue : %v", err)
	}
	// liste de défauts vidée
	en, err = e.svc.UpdateLog(ctx, UpdateInput{ID: en.ID, Defects: []string{}})
	if err != nil || len(en.Defects) != 0 {
		t.Errorf("effacement : %v %v", en.Defects, err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestActualsRecomputeCost(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedPrinter("pr1", "p1s", "P1S")
	e.done("jobA", 1, "COMPLETED")
	en, _ := e.svc.GetLog(ctx, "jobA")
	before := en.ActualCost.Total // 2,06 : 100 g, 2 h
	en, err := e.svc.UpdateLog(ctx, UpdateInput{ID: en.ID, ActualGrams: ptr(150.0), ActualMinutes: ptr(240.0)})
	if err != nil {
		t.Fatal(err)
	}
	if en.Actual.Grams != 150 || en.Actual.Seconds != 14400 || en.Actual.Basis != "pesée déclarée" || en.MassDelta == nil || *en.MassDelta != 50 || en.DurationDelta == nil || *en.DurationDelta != 100 {
		t.Fatalf("réels : %+v md=%v dd=%v", en.Actual, en.MassDelta, en.DurationDelta)
	}
	if en.ActualCost.Filament[0].Grams != 150 || en.ActualCost.Total <= before || en.ActualCost.Seconds != 14400 {
		t.Errorf("coût réel : %+v (avant %v)", en.ActualCost, before)
	}
	if en.PlannedCost.Total != before {
		t.Errorf("le coût prévu ne bouge pas : %v", en.PlannedCost.Total)
	}
}

func TestPhotosUploadReadAndRefuse(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.done("jobA", 1, "COMPLETED")
	if _, _, err := e.svc.AddPhoto(ctx, "jobA", strings.NewReader("<svg onload=alert(1)/>")); !errors.Is(err, ErrInvalid) {
		t.Errorf("SVG refusé : %v", err)
	}
	if _, _, err := e.svc.AddPhoto(ctx, "jobA", bytes.NewReader(make([]byte, MaxPhotoBytes+1))); !errors.Is(err, ErrInvalid) {
		t.Errorf("trop gros : %v", err)
	}
	en, sha, err := e.svc.AddPhoto(ctx, "jobA", bytes.NewReader(png1x1))
	if err != nil || len(sha) != 64 || len(en.Photos) != 1 || en.Photos[0] != sha {
		t.Fatalf("ajout : %v %v", err, en)
	}
	// idempotent
	en, _, _ = e.svc.AddPhoto(ctx, "jobA", bytes.NewReader(png1x1))
	if len(en.Photos) != 1 {
		t.Errorf("doublon : %v", en.Photos)
	}
	f, ct, err := e.svc.OpenPhoto(ctx, "jobA", sha)
	if err != nil || ct != "image/png" {
		t.Fatalf("lecture : %v %q", err, ct)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if !bytes.Equal(b, png1x1) {
		t.Error("contenu différent")
	}
	// un blob qui n'est pas une photo de la fiche n'est pas servi
	other, _, _ := e.bl.Put(strings.NewReader("secret"), "")
	if _, _, err := e.svc.OpenPhoto(ctx, "jobA", other); !errors.Is(err, ErrNotFound) {
		t.Errorf("blob étranger : %v", err)
	}
	// référence par blob : doit être une image
	if _, err := e.svc.UpdateLog(ctx, UpdateInput{ID: "jobA", PhotoBlobs: []string{other}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("blob non image : %v", err)
	}
	jpg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 20)...)
	jsha, _, _ := e.bl.Put(bytes.NewReader(jpg), "")
	if en, err = e.svc.UpdateLog(ctx, UpdateInput{ID: "jobA", PhotoBlobs: []string{jsha}}); err != nil || len(en.Photos) != 2 {
		t.Errorf("blob jpeg : %v %v", err, en)
	}
	// plafond
	for i := 0; i < 10; i++ {
		img := append(append([]byte{}, png1x1...), byte(i), byte(i+1))
		_, _, err = e.svc.AddPhoto(ctx, "jobA", bytes.NewReader(img))
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("plafond de photos : %v", err)
	}
}

func TestPushToMemoryAndSyncOfRecordedResults(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.done("jobA", 1, "COMPLETED")
	e.done("jobB", 2, "COMPLETED")
	rating := 5
	// fiche -> mémoire des essais
	en, err := e.svc.UpdateLog(ctx, UpdateInput{ID: "jobA", Rating: &rating, Defects: []string{"stringing"}, ExperimentID: "exp_1", CandidateID: "cand_1", ActualGrams: ptr(98.5)})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.fg.got) != 1 || len(en.ResultIDs) != 1 || en.ResultIDs[0] != "res_fake" {
		t.Fatalf("mémoire : %+v %v", e.fg.got, en.ResultIDs)
	}
	g := e.fg.got[0]
	if g.Outcome != "success" || g.PrintJobID != "jobA" || g.Rating != 5 || g.ActualMassG != 98.5 || !strings.Contains(g.Observations, "stringing") || !strings.Contains(g.Observations, "5/5") {
		t.Errorf("entrée envoyée à la mémoire : %+v", g)
	}
	if _, err := e.svc.UpdateLog(ctx, UpdateInput{ID: "jobA", ExperimentID: "exp_1", CandidateID: "cand_1"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("double versement : %v", err)
	}
	e.fg.err = errors.New("boum")
	if _, err := e.svc.UpdateLog(ctx, UpdateInput{ID: "jobB", ExperimentID: "e", CandidateID: "c"}); err == nil {
		t.Error("l'erreur de la mémoire doit remonter")
	}
	e.fg.err = nil
	// annulée : pas de versement
	e.done("jobC", 3, "CANCELLED")
	if _, err := e.svc.UpdateLog(ctx, UpdateInput{ID: "jobC", ExperimentID: "e", CandidateID: "c"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("annulée : %v", err)
	}

	// retour enregistré par record_print_result (avec print_job_id) : rattaché à la fiche à la synchronisation
	pr := goals.PrintResult{ID: "res_9", ExperimentID: "exp_9", CandidateID: "cand_9", Outcome: "partial", Observations: "coin décollé",
		PrintJobID: "jobB", Rating: 2, Defects: []string{"warping", "inconnu"}, Photos: nil, ActualMassG: 90, ActualMinutes: 130,
		Deviations: []goals.Deviation{{Key: "process.layer_height", Planned: "0.2", Actual: "0.16"}}}
	doc, _ := json.Marshal(pr)
	e.exec(`INSERT INTO print_results(id,experiment_id,candidate_id,model_id,material_family,filament,machine,doc,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"res_9", "exp_9", "cand_9", "m", "PLA", "f", "mach", string(doc), store.FormatTime(t0))
	sr, err := e.svc.SyncJournal(ctx)
	if err != nil || sr.Merged != 1 {
		t.Fatalf("synchro : %+v %v", sr, err)
	}
	b, _ := e.svc.GetLog(ctx, "jobB")
	if b.Outcome != "partial" || b.Rating != 2 || !hasStr(b.Defects, "warping") || !hasStr(b.Defects, "other") || !strings.Contains(b.Comment, "coin décollé") ||
		b.ExperimentID != "exp_9" || len(b.Settings.Deviations) != 1 || b.Actual.Grams != 90 || b.Actual.Seconds != 7800 {
		t.Fatalf("fiche B après rattachement : %+v", b)
	}
	// un second passage ne rattache pas deux fois
	sr, _ = e.svc.SyncJournal(ctx)
	if sr.Merged != 0 {
		t.Errorf("deuxième synchro : %+v", sr)
	}
}

func TestGoalsAcceptsJournalFieldsAndKeepsOldBehaviour(t *testing.T) {
	e := newEnv(t)
	gs, err := goals.New(goals.Config{Store: e.st, Blobs: e.bl})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// l'ancien appel (observations seules) franchit toujours la validation : seule l'expérience manque
	_, err = gs.RecordResult(ctx, goals.ResultInput{ExperimentID: "exp_x", CandidateID: "c", Outcome: "success", Observations: "ok"})
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("ancien appel : %v", err)
	}
	// note et défauts suffisent désormais comme contenu
	_, err = gs.RecordResult(ctx, goals.ResultInput{ExperimentID: "exp_x", CandidateID: "c", Outcome: "success", Rating: 4, Defects: []string{"stringing"}})
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("note seule : %v", err)
	}
	for name, in := range map[string]goals.ResultInput{
		"note 9":      {Rating: 9, Observations: "x"},
		"rien":        {},
		"défaut vide": {Defects: []string{" "}, Observations: "x"},
		"masse":       {ActualMassG: -2, Observations: "x"},
	} {
		in.ExperimentID, in.CandidateID, in.Outcome = "exp_x", "c", "success"
		if _, err := gs.RecordResult(ctx, in); !errors.Is(err, goals.ErrInvalid) {
			t.Errorf("%s : %v", name, err)
		}
	}
}

func TestEntryWithoutObservedTimes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	// pas d'événements : durée réelle inconnue, coût réel absent, mais fiche créée
	e.seedJob(jobSpec{ID: "jobZ", State: "COMPLETED", Printer: "pr1", Seconds: 3600, Grams: 30, End: time.Time{}})
	en, err := e.svc.GetLog(ctx, "jobZ")
	if err != nil {
		t.Fatal(err)
	}
	if en.Actual.Seconds != 0 || en.ActualCost != nil || en.PlannedCost == nil || en.Estimated.Seconds != 3600 {
		t.Errorf("sans événements : %+v", en)
	}
	_ = slicer.Override{}
}
