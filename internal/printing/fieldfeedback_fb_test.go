package printing

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// Retours d'usage du 2026-11-09 (impression du 9/11, P1S) : FB2 (fausse alerte « bloquée » pendant la mise à niveau du
// plateau) et FB3 (codes d'erreur périmés présentés comme causes).

func stallRig(t *testing.T) (*Service, *atomic.Int64, func()) {
	t.Helper()
	s, done := newBareService(t)
	var alerts atomic.Int64
	s.OnAlert(func(a Alert) {
		if a.Kind == "stalled" {
			alerts.Add(1)
		}
	})
	return s, &alerts, done
}

// statusAt : relevé « running » figé (couche 0, 0 %, 95 min restantes), plateau chaud et buse en consigne.
func statusAt(stage int, seen bool) printer.Status {
	return printer.Status{State: printer.StatePrinting, Layer: 0, TotalLayers: 400, Progress: 0, RemainingMin: 95, Stage: stage, StageSeen: seen,
		Temps: printer.Temps{BedTarget: 60, NozzleTarget: 220}}
}

func TestNoStalledAlertDuringBedLevelingOrOtherPreparationStages(t *testing.T) {
	s, alerts, done := stallRig(t)
	defer done()
	ctx := context.Background()
	wc := WatchConfig{}.withDefaults()
	t0 := time.Date(2026, 11, 9, 21, 0, 0, 0, time.UTC)
	for _, stage := range []int{printer.StageBedLeveling, printer.StageHeatbedPreheat, printer.StageHeatingHotend, printer.StageCleaningNozzle, printer.StageCalibratingFlow, printer.StageMotorNoiseCali, printer.StageHoming} {
		pg := PrintProgress{}
		now := t0
		st := statusAt(stage, true)
		pg.noteStage(st, now)
		// 15 minutes de mise à niveau figée (bien au-delà des 5 minutes de la règle ordinaire), relevé toutes les 30 s
		for i := 0; i < 30; i++ {
			now = now.Add(30 * time.Second)
			s.checkStall(ctx, Job{ID: "pj_x", PrinterID: "pr_x"}, &pg, st, wc, now)
			if pg.Blocked != "" {
				t.Fatalf("étape %s : fausse alerte « bloquée » après %s : %s", printer.StageName(stage), now.Sub(t0), pg.Blocked)
			}
		}
	}
	// la fin de l'étape ne laisse pas une horloge périmée : l'impression (étape 0, couche 0 encore figée un instant) ne
	// déclenche rien tout de suite
	pg := PrintProgress{}
	now := t0
	prep := statusAt(printer.StageBedLeveling, true)
	pg.noteStage(prep, now)
	for i := 0; i < 30; i++ {
		now = now.Add(30 * time.Second)
		s.checkStall(ctx, Job{ID: "pj_x"}, &pg, prep, wc, now)
	}
	run := statusAt(printer.StagePrinting, true)
	pg.noteStage(run, now)
	s.checkStall(ctx, Job{ID: "pj_x"}, &pg, run, wc, now.Add(30*time.Second))
	if pg.Blocked != "" {
		t.Fatalf("alerte immédiate à la sortie de la mise à niveau : %s", pg.Blocked)
	}
	time.Sleep(100 * time.Millisecond)
	if n := alerts.Load(); n != 0 {
		t.Fatalf("%d alerte(s) « stalled » émise(s) pendant les préparations", n)
	}
}

// Une vraie panne reste détectée : impression (étape 0) figée au-delà du seuil ordinaire, ou étape de préparation qui dure
// anormalement (au-delà du seuil propre aux préparations, puis du seuil ordinaire).
func TestRealStallIsStillFlagged(t *testing.T) {
	s, _, done := stallRig(t)
	defer done()
	ctx := context.Background()
	wc := WatchConfig{}.withDefaults()
	t0 := time.Date(2026, 11, 9, 21, 0, 0, 0, time.UTC)

	pg := PrintProgress{}
	run := statusAt(printer.StagePrinting, true)
	run.Layer, run.Progress = 12, 3
	run.Temps.BedTarget = 60
	pg.noteStage(run, t0)
	now := t0
	for i := 0; i < 14 && pg.Blocked == ""; i++ { // 7 minutes figées
		now = now.Add(30 * time.Second)
		s.checkStall(ctx, Job{ID: "pj_x"}, &pg, run, wc, now)
	}
	if !strings.Contains(pg.Blocked, "bloquée") {
		t.Fatalf("impression figée 7 minutes à l'étape 0 : « bloquée » attendue, obtenu %q", pg.Blocked)
	}

	pg = PrintProgress{}
	prep := statusAt(printer.StageBedLeveling, true)
	pg.noteStage(prep, t0)
	now = t0
	for i := 0; i < 80 && pg.Blocked == ""; i++ { // jusqu'à 40 minutes de « mise à niveau »
		now = now.Add(30 * time.Second)
		s.checkStall(ctx, Job{ID: "pj_x"}, &pg, prep, wc, now)
	}
	if !strings.Contains(pg.Blocked, "bloquée") || now.Sub(t0) < wc.PrepStallAfter {
		t.Fatalf("mise à niveau anormalement longue : « bloquée » attendue après %s, obtenu %q à %s", wc.PrepStallAfter, pg.Blocked, now.Sub(t0))
	}
}

// Une étape inconnue ou absente du relevé garde la règle ordinaire (aucune supposition).
func TestUnknownStageKeepsOrdinaryStallRule(t *testing.T) {
	s, _, done := stallRig(t)
	defer done()
	wc := WatchConfig{}.withDefaults()
	t0 := time.Date(2026, 11, 9, 21, 0, 0, 0, time.UTC)
	pg := PrintProgress{}
	st := statusAt(0, false)
	now := t0
	for i := 0; i < 14 && pg.Blocked == ""; i++ {
		now = now.Add(30 * time.Second)
		s.checkStall(context.Background(), Job{ID: "pj_x"}, &pg, st, wc, now)
	}
	if pg.Blocked == "" {
		t.Fatal("sans étape connue, une impression figée doit rester signalée")
	}
}

var (
	hmsStale = printer.HMSError{Attr: 0x03000D00, Code: 0x00020001} // 0300-0D00-0002-0001 : code périmé du 9/11
	hmsNow   = printer.HMSError{Attr: 0x07FF5000, Code: 0x00020001}
)

func TestNoteErrorsSeparatesActiveFromHistory(t *testing.T) {
	t0 := time.Date(2026, 11, 9, 21, 0, 0, 0, time.UTC)
	pg := PrintProgress{}
	stale := printer.DescribeHMS(hmsStale)
	now := printer.DescribeHMS(hmsNow)

	if !pg.noteErrors([]printer.ErrorInfo{stale}, t0) {
		t.Fatal("un nouveau code doit déclencher l'enregistrement")
	}
	act, hist := pg.causes()
	if len(act) != 1 || len(hist) != 0 || !act[0].Active || act[0].FirstSeenAt != t0 {
		t.Fatalf("code actif attendu : %+v / %+v", act, hist)
	}
	// l'imprimante ne rapporte plus rien (hms_count 0, print_error 0) : le code passe en historique, horodaté
	t1 := t0.Add(10 * time.Minute)
	if !pg.noteErrors(nil, t1) {
		t.Fatal("la disparition d'un code doit être enregistrée")
	}
	act, hist = pg.causes()
	if len(act) != 0 || len(pg.ErrorDetails) != 0 {
		t.Fatalf("plus aucun code actif attendu : %+v %+v", act, pg.ErrorDetails)
	}
	if len(hist) != 1 || hist[0].Active || hist[0].Code != stale.Code || hist[0].FirstSeenAt.IsZero() || hist[0].LastSeenAt.IsZero() {
		t.Fatalf("code périmé attendu en historique horodaté : %+v", hist)
	}
	// un autre code, actif, n'est pas mélangé à l'historique ; le premier reste périmé
	t2 := t1.Add(time.Minute)
	pg.noteErrors([]printer.ErrorInfo{now}, t2)
	act, hist = pg.causes()
	if len(act) != 1 || act[0].Code != now.Code || len(hist) != 1 || hist[0].Code != stale.Code {
		t.Fatalf("actif / historique : %+v / %+v", act, hist)
	}
	// même code revenu : nouvelle occurrence active
	pg.noteErrors([]printer.ErrorInfo{stale, now}, t2.Add(time.Minute))
	if act, _ = pg.causes(); len(act) != 2 {
		t.Fatalf("deux codes actifs attendus : %+v", act)
	}
	// le journal est borné
	for i := 0; i < 40; i++ {
		pg.noteErrors([]printer.ErrorInfo{printer.DescribePrintError(0x0300400C + i)}, t2.Add(time.Duration(i)*time.Minute))
	}
	if len(pg.ErrorLog) > maxErrorLog {
		t.Fatalf("journal non borné : %d", len(pg.ErrorLog))
	}
}
