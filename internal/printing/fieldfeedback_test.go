package printing

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// ---- point 4 : liens /stop sans ponctuation finale, ancien format accepté, erreurs explicites ----

func TestStopIDIsAlphanumericAfterPrefix(t *testing.T) {
	re := regexp.MustCompile(`^stp_[0-9a-f]{24}$`)
	for i := 0; i < 200; i++ {
		if id := newStopID(); !re.MatchString(id) {
			t.Fatalf("identifiant %q : alphanumérique attendu (jamais de _ ou - final)", id)
		}
	}
}

func TestStopLookupAcceptsLegacyIDWithoutTrailingUnderscore(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	legacy := "stp_AbCdEfGhIjKlMnOpQr_" // ancien format : base64url, se terminait par « _ »
	s.mu.Lock()
	s.stops[legacy] = stopReq{jobID: "pj_x", expires: s.now().Add(time.Minute)}
	s.mu.Unlock()
	id, r, why := s.lookupStop(strings.TrimRight(legacy, "_")) // l'affichage du chat a retiré le « _ »
	if why != "" || id != legacy || r.jobID != "pj_x" {
		t.Fatalf("ancien format refusé : %q %v %q", id, r, why)
	}
	if _, _, why := s.lookupStop(legacy); why != "" {
		t.Fatalf("identifiant exact refusé : %q", why)
	}
}

func TestStopLookupDistinguishesReasons(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	clock := time.Now()
	s.d.Now = func() time.Time { return clock }
	id, _ := s.requestStop("pj_1")
	if _, _, why := s.lookupStop("stp_inconnu0123"); !strings.Contains(why, "inconnu") {
		t.Fatalf("inconnu : %q", why)
	}
	clock = clock.Add(stopTTL + time.Second)
	if _, _, why := s.lookupStop(id); !strings.Contains(why, "expiré") {
		t.Fatalf("expiré : %q", why)
	}
	s.purge()
	if _, _, why := s.lookupStop(id); !strings.Contains(why, "expiré") { // trace gardée 24 h après la purge
		t.Fatalf("expiré après purge : %q", why)
	}
	s.mu.Lock()
	s.stopGone["stp_used"] = goneStop{reason: "used", at: clock}
	s.mu.Unlock()
	if _, _, why := s.lookupStop("stp_used"); !strings.Contains(why, "déjà été utilisé") {
		t.Fatalf("utilisé : %q", why)
	}
	if m := stopGoneMessage("finished", contracts.PrintCompleted); !strings.Contains(m, "n'est plus en cours") || !strings.Contains(m, "COMPLETED") {
		t.Fatalf("terminée : %q", m)
	}
}

// ---- point A : travaux orphelins ----

func newSlicingJob(t *testing.T, s *Service, enqueue bool) (Job, string) {
	t.Helper()
	ctx := context.Background()
	j, err := s.createJob(ctx, Job{PrinterID: "prn_t", Name: "x.gcode.3mf"})
	if err != nil {
		t.Fatal(err)
	}
	if j, err = s.transition(ctx, j.ID, contracts.EvPrepare, "prépa", nil); err != nil {
		t.Fatal(err)
	}
	qid := ""
	if enqueue {
		params, _ := json.Marshal(map[string]string{"print_job_id": j.ID})
		qj, _, err := s.d.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: JobSlice, Class: contracts.ClassHeavy, Params: params})
		if err != nil {
			t.Fatal(err)
		}
		qid = qj.ID
		if j, err = s.update(ctx, j.ID, func(x *Job) error { x.SliceJobID = qj.ID; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	return j, qid
}

func TestOrphanSliceJobCancelledInQueueClosesPrintJob(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	j, qid := newSlicingJob(t, s, true)
	if _, err := s.d.Queue.RequestCancel(ctx, qid); err != nil { // cancel_job côté file : l'impression restait « découpe en cours »
		t.Fatal(err)
	}
	cur := s.CheckConsistency(ctx, j)
	if cur.State != contracts.PrintCancelled || !strings.Contains(cur.LastError, "annulé") {
		t.Fatalf("état %s, erreur %q", cur.State, cur.LastError)
	}
}

func TestOrphanSliceJobFailedInQueueFailsPrintJob(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	j, qid := newSlicingJob(t, s, true)
	if _, err := s.transition(ctx, j.ID, contracts.EvStartSlice, "x", nil); err != nil {
		t.Fatal(err)
	}
	cl, err := s.d.Queue.Claim(ctx, "w1")
	if err != nil || cl == nil || cl.ID != qid {
		t.Fatalf("réservation : %v %v", cl, err)
	}
	if err := s.d.Queue.Fail(ctx, qid, "slice_failed", "boum"); err != nil {
		t.Fatal(err)
	}
	cur := s.CheckConsistency(ctx, mustGet(t, s, j.ID))
	if cur.State != contracts.PrintFailed || !strings.Contains(cur.LastError, "boum") {
		t.Fatalf("état %s, erreur %q", cur.State, cur.LastError)
	}
}

func TestOrphanStalledRunningSliceIsClosed(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	j, qid := newSlicingJob(t, s, true)
	if _, err := s.transition(ctx, j.ID, contracts.EvStartSlice, "x", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.d.Queue.Claim(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	// aucun battement : on avance l'horloge du service au-delà du seuil
	s.d.Now = func() time.Time { return time.Now().Add(10 * time.Minute) }
	cur := s.CheckConsistency(ctx, mustGet(t, s, j.ID))
	if cur.State != contracts.PrintFailed || !strings.Contains(cur.LastError, "sans signe de vie") {
		t.Fatalf("état %s, erreur %q (travail %s)", cur.State, cur.LastError, qid)
	}
}

func TestQueuedSliceReportsPositionNotRunning(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	j1, q1 := newSlicingJob(t, s, true)
	j2, q2 := newSlicingJob(t, s, true)
	if _, err := s.d.Queue.Claim(ctx, "w1"); err != nil { // la première prend l'unique place « lourde »
		t.Fatal(err)
	}
	p1, p2 := s.ProgressFor(ctx, mustGet(t, s, j1.ID)), s.ProgressFor(ctx, mustGet(t, s, j2.ID))
	if p1 == nil || p1.QueuePosition != nil || p1.State != "running" {
		t.Fatalf("p1 : %+v (%s)", p1, q1)
	}
	if p2 == nil || p2.QueuePosition == nil || *p2.QueuePosition != 1 || p2.RunningAhead != 1 || !strings.Contains(p2.Phase, "file") {
		t.Fatalf("p2 : %+v (%s)", p2, q2)
	}
	if cur := s.CheckConsistency(ctx, mustGet(t, s, j2.ID)); cur.State != contracts.PrintPrepared {
		t.Fatalf("une découpe en file n'est pas orpheline : %s", cur.State)
	}
}

func mustGet(t *testing.T, s *Service, id string) Job {
	t.Helper()
	j, err := s.GetJob(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// ---- point 5 : progression ----

func TestJobProgressFromBytesAndEstimate(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	qj, _, err := s.d.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: "test.x", Class: contracts.ClassLight, Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	cl, err := s.d.Queue.Claim(ctx, "w1")
	if err != nil || cl == nil {
		t.Fatal(err)
	}
	jobs.SetDetailForTest(qj.ID, jobs.Detail{Phase: "envoi FTPS", BytesDone: 30 << 20, BytesTotal: 59 << 20})
	time.Sleep(1100 * time.Millisecond)
	cur, _ := s.d.Queue.Get(ctx, qj.ID)
	p := s.d.Queue.ProgressOf(ctx, cur)
	if p.Percent != 51 || p.BytesTotal != 59<<20 || p.ETASeconds == nil || p.Phase != "envoi FTPS" {
		t.Fatalf("envoi : %+v", p)
	}
	jobs.SetDetailForTest(qj.ID, jobs.Detail{Phase: "découpe", Expected: 3600})
	p = s.d.Queue.ProgressOf(ctx, cur)
	if !p.Estimated || p.Percent < 0 || p.Percent >= 95 {
		t.Fatalf("découpe estimée : %+v", p)
	}
}

// ---- point 8 : couleur réelle, jamais celle du profil ----

func TestRealColorNeverTheProfileDefault(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	sum := &ArtifactSummary{Plates: []slicer.PlateResult{{Index: 1, Filaments: []slicer.FilamentUse{{Slot: 1, Type: "PETG", Color: "#00AE42"}}}}}
	clearProfileColors(sum)
	if f := sum.Plates[0].Filaments[0]; f.Color != "" || !strings.Contains(f.ColorSource, "inconnue") {
		t.Fatalf("la couleur du profil doit disparaître : %+v", f)
	}
	j := Job{PrinterID: "prn_t", Plate: 1, Summary: sum, Assignments: []FilamentAssignment{{FilamentSlot: 1, Tray: "ext", MaterialID: "PETG"}}}
	if c := s.colorsFor(j); len(c) != 1 || c[0].Color != "inconnue" {
		t.Fatalf("sans bobine déclarée : %+v", c)
	}
	// bobine externe déclarée (PETG translucide teinté)
	s.SetSpoolSource(func(_ context.Context, printerID string) []LoadedSpoolSummary {
		return []LoadedSpoolSummary{{Tray: "ext", Material: "PETG translucide", Color: "#cfe8ff", Source: "inventaire (saisi par vous)"}}
	})
	c := s.colorsFor(j)
	if len(c) != 1 || c[0].Color != "#CFE8FF" || c[0].Material != "PETG translucide" || !strings.Contains(c[0].Source, "inventaire") {
		t.Fatalf("bobine externe déclarée : %+v", c)
	}
	applyRealColors(&j, s.loadedSpools("prn_t"))
	if f := j.Summary.Plates[0].Filaments[0]; f.Color != "#CFE8FF" {
		t.Fatalf("couleur appliquée : %+v", f)
	}
	// l'AMS (autre emplacement) ne se mélange pas avec l'externe
	j.Assignments[0].Tray = "ams1:1"
	if c := s.colorsFor(j); c[0].Color != "inconnue" {
		t.Fatalf("emplacement sans bobine : %+v", c)
	}
}

// ---- point B : historique (vues, regroupement par modèle, archive / corbeille récupérable) ----

func TestHistoryViewsArchiveTrashRestoreAndGrouping(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	mk := func(model string, state contracts.PrintState) Job {
		j, err := s.createJob(ctx, Job{PrinterID: "prn_t", ModelID: model, Name: "x.gcode.3mf", State: state})
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	ok1 := mk("mdl_a", contracts.PrintCompleted)
	f1, f2 := mk("mdl_a", contracts.PrintFailed), mk("mdl_b", contracts.PrintCancelled)
	act := mk("mdl_b", contracts.PrintAwaitingConfirmation)
	h, err := s.ListHistory(ctx, HistoryFilter{View: ViewFailed}, false)
	if err != nil || len(h.Jobs) != 2 || h.Counts["failed"] != 2 || h.Counts["finished"] != 1 || h.Counts["active"] != 1 {
		t.Fatalf("vue failed : %+v %v", h, err)
	}
	if h, _ = s.ListHistory(ctx, HistoryFilter{View: ViewActive}, false); len(h.Jobs) != 1 || h.Jobs[0].ID != act.ID {
		t.Fatalf("vue active : %+v", h.Jobs)
	}
	g, _ := s.ListHistory(ctx, HistoryFilter{View: ViewAll}, true)
	if len(g.Groups) != 2 || g.Groups[0].Count != 2 {
		t.Fatalf("groupes : %+v", g.Groups)
	}
	// une impression active ne s'archive pas ; les terminées oui
	r, err := s.ArchiveJobs(ctx, "archive", []string{act.ID, f1.ID, f2.ID})
	if err != nil || len(r.Done) != 2 || len(r.Skipped) != 1 || !strings.Contains(r.Skipped[0], act.ID) {
		t.Fatalf("archive : %+v %v", r, err)
	}
	if h, _ = s.ListHistory(ctx, HistoryFilter{View: ViewAll}, false); len(h.Jobs) != 2 {
		t.Fatalf("la vue par défaut doit masquer les archivées : %d", len(h.Jobs))
	}
	if h, _ = s.ListHistory(ctx, HistoryFilter{View: ViewArchived}, false); len(h.Jobs) != 2 {
		t.Fatalf("archivées : %d", len(h.Jobs))
	}
	if _, err = s.ArchiveJobs(ctx, "trash", []string{f1.ID}); err != nil {
		t.Fatal(err)
	}
	if h, _ = s.ListHistory(ctx, HistoryFilter{View: ViewTrash}, false); len(h.Jobs) != 1 || h.Jobs[0].ID != f1.ID {
		t.Fatalf("corbeille : %+v", h.Jobs)
	}
	// récupérable : le document existe toujours, restore le remet dans la vue par défaut
	if _, err = s.GetJob(ctx, f1.ID); err != nil {
		t.Fatalf("la corbeille ne supprime rien : %v", err)
	}
	if _, err = s.ArchiveJobs(ctx, "restore", []string{f1.ID, ok1.ID}); err != nil {
		t.Fatal(err)
	}
	if h, _ = s.ListHistory(ctx, HistoryFilter{View: ViewFailed}, false); len(h.Jobs) != 1 || h.Jobs[0].ID != f1.ID {
		t.Fatalf("restauration : %+v", h.Jobs)
	}
	if _, err = s.ArchiveJobs(ctx, "détruire", []string{f1.ID}); err == nil {
		t.Fatal("action inconnue acceptée")
	}
	if _, err = s.ListHistory(ctx, HistoryFilter{View: "tout"}, false); err == nil {
		t.Fatal("vue inconnue acceptée")
	}
}
