package insights

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func (e *env) auditCount(op, id string) int {
	e.t.Helper()
	var n int
	if err := e.st.DB().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE operation=? AND entity_id=?`, op, id).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestSetVerdictWritesColumnsAuditAndIsIdempotent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedPrinter("pr1", "p1s", "P1S")
	e.seedModel("mod1", "Boîtier", "", "")
	e.done("jobA", 1, "COMPLETED")

	o, err := e.svc.SetVerdict(ctx, SetVerdictInput{PrintJobID: "jobA", Verdict: "success", Source: SourceHumanCard, Note: "propre"})
	if err != nil || !o.Changed || o.Verdict != "success" || o.VerdictSource != SourceHumanCard || o.VerdictAt.IsZero() || len(o.FailReasons) != len(FailReasons) {
		t.Fatalf("verdict : %+v %v", o, err)
	}
	var verdict, src string
	if err := e.st.DB().QueryRow(`SELECT verdict, verdict_source FROM print_logs WHERE print_job_id='jobA'`).Scan(&verdict, &src); err != nil || verdict != "success" || src != SourceHumanCard {
		t.Fatalf("colonnes : %q %q %v", verdict, src, err)
	}
	if n := e.auditCount("insights.verdict.set", o.LogID); n != 1 {
		t.Fatalf("audit après le 1er verdict : %d", n)
	}
	// Outcome n'est pas écrasé ; la fiche relit le verdict.
	l, _ := e.svc.GetLog(ctx, "jobA")
	if l.Outcome != "success" || l.Verdict != "success" || !l.Reached100 || l.Origin != "printalk" || l.PartCount != 1 || l.VerdictAt.IsZero() {
		t.Fatalf("fiche : %+v", l)
	}
	if e.svc.VerdictNote(ctx, "jobA") != "propre" {
		t.Error("note du verdict")
	}
	// même appel : idempotent, aucune entrée d'audit en plus.
	o2, err := e.svc.SetVerdict(ctx, SetVerdictInput{PrintJobID: "jobA", Verdict: "success"})
	if err != nil || o2.Changed || e.auditCount("insights.verdict.set", o.LogID) != 1 {
		t.Fatalf("idempotence : %+v %v", o2, err)
	}
	// second appel différent : modifie et ajoute une seconde entrée (CA11.1).
	o3, err := e.svc.SetVerdict(ctx, SetVerdictInput{PrintJobID: "jobA", Verdict: "failed", Reason: "stringing"})
	if err != nil || !o3.Changed || o3.Previous != "success" || o3.FailReason != "stringing" || e.auditCount("insights.verdict.set", o.LogID) != 2 {
		t.Fatalf("modification : %+v %v", o3, err)
	}
	// la note n'apparaît jamais dans l'audit.
	var detail string
	_ = e.st.DB().QueryRow(`SELECT group_concat(detail) FROM audit_log WHERE operation='insights.verdict.set'`).Scan(&detail)
	if strings.Contains(detail, "propre") {
		t.Errorf("note copiée dans l'audit : %s", detail)
	}
}

func TestSetVerdictValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedPrinter("pr1", "p1s", "P1S")
	e.seedModel("mod1", "Boîtier", "", "")
	e.done("jobA", 1, "COMPLETED")
	e.seedJob(jobSpec{ID: "jobRun", State: "PRINTING", Printer: "pr1", Model: "mod1", Seconds: 100, Grams: 1, Start: t0, Percent: 40})

	// failed sans raison : accepté (other) avec avertissement (CA11.2)
	o, err := e.svc.SetVerdict(ctx, SetVerdictInput{PrintJobID: "jobA", Verdict: "failed"})
	if err != nil || o.FailReason != "other" || len(o.Warnings) == 0 {
		t.Fatalf("failed sans raison : %+v %v", o, err)
	}
	// raison hors vocabulaire : refusée avec la liste
	_, err = e.svc.SetVerdict(ctx, SetVerdictInput{PrintJobID: "jobA", Verdict: "failed", Reason: "magie"})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "nozzle_clog") {
		t.Fatalf("raison inconnue : %v", err)
	}
	// verdict inconnu, note trop longue, source inconnue
	for _, in := range []SetVerdictInput{
		{PrintJobID: "jobA", Verdict: "bof"},
		{PrintJobID: "jobA", Verdict: "success", Note: strings.Repeat("x", MaxVerdictNote+1)},
		{PrintJobID: "jobA", Verdict: "success", Source: "robot"},
		{Verdict: "success"},
	} {
		if _, err := e.svc.SetVerdict(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v : erreur attendue, obtenu %v", in, err)
		}
	}
	// impression non terminée / inconnue (CA11.3)
	if _, err := e.svc.SetVerdict(ctx, SetVerdictInput{PrintJobID: "jobRun", Verdict: "success"}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "pas terminée") {
		t.Errorf("non terminée : %v", err)
	}
	if _, err := e.svc.SetVerdict(ctx, SetVerdictInput{PrintJobID: "nope", Verdict: "success"}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "inconnue") {
		t.Errorf("inconnue : %v", err)
	}
	// réussite avec raison : raison ignorée
	o, err = e.svc.SetVerdict(ctx, SetVerdictInput{PrintJobID: "jobA", Verdict: "success", Reason: "warping"})
	if err != nil || o.FailReason != "" || len(o.Warnings) == 0 {
		t.Errorf("réussite avec raison : %+v %v", o, err)
	}
}

func TestPendingVerdicts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedPrinter("pr1", "p1s", "P1S")
	e.seedModel("mod1", "Boîtier", "", "")
	e.done("jobA", 1, "COMPLETED")
	e.done("jobB", 2, "COMPLETED")
	e.done("jobC", 3, "FAILED")
	e.done("jobD", 4, "CANCELLED")
	e.seedJob(jobSpec{ID: "jobRun", State: "PRINTING", Printer: "pr1", Model: "mod1", Seconds: 100, Grams: 1, Start: t0, Percent: 40})
	if _, err := e.svc.SetVerdict(ctx, SetVerdictInput{PrintJobID: "jobB", Verdict: "success"}); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.PendingVerdicts(ctx, 0)
	if err != nil || p.Total != 2 || len(p.Pending) != 2 || p.Pending[0].PrintJobID != "jobC" || p.Pending[1].PrintJobID != "jobA" {
		t.Fatalf("en attente : %+v %v", p, err)
	}
	if p.Pending[0].State != "FAILED" || p.Pending[1].Reached100 != true {
		t.Errorf("détail : %+v", p.Pending)
	}
	one, _ := e.svc.PendingVerdicts(ctx, 1)
	if one.Total != 2 || len(one.Pending) != 1 {
		t.Errorf("borne : %+v", one)
	}
	if big, _ := e.svc.PendingVerdicts(ctx, 9999); len(big.Pending) > 200 {
		t.Error("limite > 200")
	}
}

// CA2.2 / CA5.1 : « imprimée » = 100 % atteint, verdict ou pas ; une annulée à 60 % ne compte pas.
func TestPrintCountsByModel(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedPrinter("pr1", "p1s", "P1S")
	e.seedModel("mod1", "Boîtier", "", "")
	e.seedModel("mod2", "Équerre", "", "")
	for i, id := range []string{"j1", "j2", "j3", "j4", "j5"} {
		e.done(id, i+1, "COMPLETED")
	}
	start := t0.AddDate(0, 0, 9)
	e.seedJob(jobSpec{ID: "jCancel", State: "CANCELLED", Printer: "pr1", Model: "mod1", Seconds: 7200, Grams: 100, Start: start, End: start.Add(time.Hour), Percent: 60})
	e.seedJob(jobSpec{ID: "jRun", State: "PRINTING", Printer: "pr1", Model: "mod1", Seconds: 3600, Grams: 50, Start: t0, Percent: 20})
	for _, v := range []struct{ id, verdict, reason string }{{"j1", "success", ""}, {"j2", "success", ""}, {"j3", "success", ""}, {"j4", "success", ""}, {"j5", "failed", "stringing"}} {
		if _, err := e.svc.SetVerdict(ctx, SetVerdictInput{PrintJobID: v.id, Verdict: v.verdict, Reason: v.reason}); err != nil {
			t.Fatal(err)
		}
	}
	// une sixième impression terminée sans verdict
	e.done("j6", 10, "COMPLETED")
	m, err := e.svc.PrintCountsByModel(ctx, []string{"mod1", "mod2", "mod1", ""})
	if err != nil {
		t.Fatal(err)
	}
	c := m["mod1"]
	if c.Attempts != 7 || c.Printed != 6 || c.PrintedUnits != 6 || c.Success != 4 || c.Failed != 1 || c.Redo != 0 || c.Unjudged != 1 || c.Judged() != 5 {
		t.Fatalf("comptes : %+v", c)
	}
	if c.TopFailReason != "stringing" || c.TopFailReasonN != 1 {
		t.Errorf("raison : %+v", c)
	}
	if c.InProgress != 1 || c.PlannedCost <= 0 {
		t.Errorf("en cours : %+v", c)
	}
	if c.ActualCost <= 0 || c.ActualSeconds < 6*7200 || c.Currency == "" {
		t.Errorf("coût et temps : %+v", c)
	}
	z := m["mod2"]
	if z == nil || z.Attempts != 0 || z.Printed != 0 || z.ActualCost != 0 {
		t.Errorf("pièce jamais imprimée : %+v", z)
	}
	if len(m) != 2 {
		t.Errorf("clés : %d", len(m))
	}
}

func TestReached100IsSetFromTheJobAndOldRowsAreReadByState(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedModel("mod1", "Boîtier", "", "")
	e.done("jobA", 1, "COMPLETED")
	e.seedJob(jobSpec{ID: "jobB", State: "FAILED", Printer: "pr1", Model: "mod1", Seconds: 100, Grams: 1, Start: t0, End: t0.Add(time.Hour), Percent: 40})
	if _, err := e.svc.SyncJournal(ctx); err != nil {
		t.Fatal(err)
	}
	var a, b int
	_ = e.st.DB().QueryRow(`SELECT reached_100 FROM print_logs WHERE print_job_id='jobA'`).Scan(&a)
	_ = e.st.DB().QueryRow(`SELECT reached_100 FROM print_logs WHERE print_job_id='jobB'`).Scan(&b)
	if a != 1 || b != 0 {
		t.Fatalf("reached_100 : %d %d", a, b)
	}
	// ancienne fiche (colonne à 0) d'une impression COMPLETED : lue « imprimée » par calcul, sans écriture.
	if _, err := e.st.DB().Exec(`UPDATE print_logs SET reached_100=0 WHERE print_job_id='jobA'`); err != nil {
		t.Fatal(err)
	}
	m, _ := e.svc.PrintCountsByModel(ctx, []string{"mod1"})
	if m["mod1"].Printed != 1 {
		t.Errorf("ancienne fiche : %+v", m["mod1"])
	}
	l, _ := e.svc.GetLog(ctx, "jobA")
	if !l.Reached100 {
		t.Error("fiche ancienne : reached_100 calculé")
	}
	// le document de la fiche ne porte jamais les champs de suivi
	var doc string
	_ = e.st.DB().QueryRow(`SELECT doc FROM print_logs WHERE print_job_id='jobA'`).Scan(&doc)
	if strings.Contains(doc, "reached_100") || strings.Contains(doc, `"verdict"`) {
		t.Errorf("doc : %s", doc)
	}
	if _, err := e.svc.UpdateLog(ctx, UpdateInput{ID: "jobA", Comment: ptr("x")}); err != nil {
		t.Fatal(err)
	}
	_ = e.st.DB().QueryRow(`SELECT doc FROM print_logs WHERE print_job_id='jobA'`).Scan(&doc)
	if strings.Contains(doc, "reached_100") || strings.Contains(doc, `"verdict"`) {
		t.Errorf("doc après mise à jour : %s", doc)
	}
}
