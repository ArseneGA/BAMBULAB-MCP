package camera

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// FB1 (impression du 9/11) : la référence du plateau vide est prise automatiquement juste après la confirmation humaine
// (passage à START_REQUESTED), sans que la page /confirm ait servi de photo ; la détection visuelle l'utilise pour toute
// l'impression, même si la surveillance ne démarre qu'à la couche 30 (le repli « couche <= 1 » n'aurait rien donné).
func TestReferenceIsCapturedRightAfterConfirmationAndUsedByDetection(t *testing.T) {
	r := newRig(t, "p1s", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ls := &liveScene{be: r.be}
	r.cam.SetScene(ls.fn)
	iv, cons := 0.04, 2
	if _, err := r.svc.UpdateSettings(ctx, SettingsPatch{IntervalSeconds: &iv, ConsecutiveAlert: &cons}); err != nil {
		t.Fatal(err)
	}
	if !r.svc.Settings(ctx).Enabled {
		t.Fatal("la détection visuelle doit être active par défaut")
	}
	// Plateau vide au moment de la confirmation humaine : aucune photo de page /confirm (PlatePhoto) n'est prise.
	var fire func(PrintEvent)
	r.svc.AttachEvents(func(fn func(PrintEvent)) { fire = fn })
	fire(PrintEvent{JobID: "pj_1", PrinterID: "pr_1", To: contracts.PrintStartRequested})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ph, _ := r.svc.PhotosForJob(ctx, "pj_1"); len(ph) == 1 && ph[0].Kind == "reference" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("image de référence jamais prise après la confirmation")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// analyze_print_frame, avant toute analyse périodique : plus de « pas d'image de référence »
	ls.present.Store(true)
	r.be.layer.Store(30)
	a, err := r.svc.AnalyzeLatest(ctx, "pr_1")
	if err != nil || a.Report == nil || !a.Report.ReferenceSet {
		t.Fatalf("analyse avec référence attendue : %+v %v", a, err)
	}
	for _, n := range a.Report.Notes {
		if strings.Contains(n, "pas d'image de référence") {
			t.Fatalf("note de référence manquante alors qu'elle existe : %v", a.Report.Notes)
		}
	}
	// la surveillance démarre à la couche 30 : elle reprend la référence de l'impression et voit le spaghetti
	done := make(chan struct{})
	go func() { defer close(done); r.svc.Run(ctx) }()
	r.nextReport(t)
	if rep, _, ok := r.svc.LastReport("pj_1"); !ok || !rep.ReferenceSet {
		t.Fatalf("la surveillance doit disposer de la référence de l'impression : %+v", rep)
	}
	ls.spaghetti.Store(true)
	for l := int64(31); l <= 60 && len(r.nt.list()) == 0; l++ {
		r.be.layer.Store(l)
		r.nextReport(t)
	}
	if len(r.nt.list()) != 1 {
		rep, _, _ := r.svc.LastReport("pj_1")
		t.Fatalf("spaghetti non détecté avec la référence prise à la confirmation : %+v", rep)
	}
	cancel()
	<-done
}

// La référence survit à un redémarrage du serveur (relue en base) et n'est prise qu'une fois par impression.
func TestReferenceSurvivesRestartAndIsTakenOnce(t *testing.T) {
	r := newRig(t, "p1s", nil)
	ctx := context.Background()
	if err := r.svc.captureReference(ctx, "pr_1", "pj_1"); err != nil {
		t.Fatal(err)
	}
	s2, err := New(Config{Store: r.st, Blobs: r.bl, Backend: r.be, Port: r.svc.c.Port, MinInterval: 0.01})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if ref, ok := s2.jobReference(ctx, "pj_1"); !ok || len(ref.jpeg) == 0 || !ref.job {
		t.Fatal("référence non relue après redémarrage")
	}
	if err := r.svc.captureReference(ctx, "pr_1", "pj_1"); err != nil {
		t.Fatal(err)
	}
	if ph, _ := r.svc.PhotosForJob(ctx, "pj_1"); len(ph) != 1 {
		t.Fatalf("une seule référence attendue : %d", len(ph))
	}
}
