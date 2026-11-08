package printer_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
)

// ---- décodage des codes (table partielle, repli « inconnu » + lien) ----

func TestDecodePrintErrorKnownAndUnknown(t *testing.T) {
	// 50348044 = 0x0300400C : le reliquat d'annulation que la P1S garde affiché en état FAILED.
	i := printer.DescribePrintError(50348044)
	if !i.Known || !i.Benign || i.Short != "0300_400C" || !strings.Contains(i.Text, "annulée") {
		t.Fatalf("0300400C : %+v", i)
	}
	u := printer.DescribePrintError(0x03008F01)
	if u.Known || u.Benign || !strings.Contains(u.Text, "code 0300_8F01 (inconnu)") || !strings.Contains(u.Text, "mouvement") || u.Wiki != printer.WikiURL {
		t.Fatalf("inconnu : %+v", u)
	}
	if h := printer.DescribeHMS(printer.HMSError{Attr: 0x03000000, Code: 0x0001000A}); h.Known || !strings.Contains(h.Text, "(inconnu)") {
		t.Fatalf("HMS inconnu : %+v", h)
	}
	if z := printer.DescribePrintError(0); !z.Known || z.Text != "aucune erreur" {
		t.Fatalf("zéro : %+v", z)
	}
}

func TestStageTableAndFallback(t *testing.T) {
	for id, want := range map[int]string{1: "Mise à niveau du plateau", 14: "Nettoyage de la buse", 19: "Calibration du débit (flow)", 25: "Calibration des vibrations (bruit moteur)", 255: "Repos", -1: "Repos"} {
		if got := printer.StageName(id); got != want {
			t.Fatalf("étape %d : %q, attendu %q", id, got, want)
		}
	}
	if got := printer.StageName(99); got != "étape 99 (inconnue)" {
		t.Fatalf("étape inconnue : %q", got)
	}
}

func st(state printer.State, mod func(*printer.Status)) printer.Status {
	s := printer.Status{State: state, GcodeState: strings.ToUpper(string(state)), Connected: true, UpdatedAt: time.Now(), FullReportSeen: true}
	if mod != nil {
		mod(&s)
	}
	return s
}

func TestCheckLaunchableRules(t *testing.T) {
	now := time.Now()
	full := printer.FullStartPolicy
	cases := []struct {
		name    string
		s       printer.Status
		pol     printer.StartPolicy
		wantErr error  // nil = lançable
		reason  string // Reason attendue
		contain string
	}{
		{"idle", st(printer.StateIdle, nil), full, nil, "", ""},
		{"finish", st(printer.StateFinished, nil), full, nil, "", ""},
		{"failed sans erreur", st(printer.StateFailed, nil), full, nil, "", ""},
		{"failed + reliquat d'annulation 50348044", st(printer.StateFailed, func(s *printer.Status) { s.PrintError = 50348044 }), full, nil, "", ""},
		{"failed + HMS actif", st(printer.StateFailed, func(s *printer.Status) { s.HMS = []printer.HMSError{{Attr: 0x03000000, Code: 0x00020003}} }), full, printer.ErrPrinterError, "hms_active", "erreur HMS 0300_0000_0002_0003"},
		{"failed + print_error inconnu", st(printer.StateFailed, func(s *printer.Status) { s.PrintError = 0x03008F01 }), full, printer.ErrPrinterError, "print_error", "0300_8F01"},
		{"idle + reliquat bénin (hors état final)", st(printer.StateIdle, func(s *printer.Status) { s.PrintError = 50348044 }), full, printer.ErrPrinterError, "print_error", ""},
		{"finish + HMS", st(printer.StateFinished, func(s *printer.Status) { s.HMS = []printer.HMSError{{Attr: 1, Code: 2}} }), full, printer.ErrPrinterError, "hms_active", ""},
		{"occupée", st(printer.StatePrinting, nil), full, printer.ErrNotReady, "busy", "occupée"},
		{"hors ligne", st(printer.StateOffline, func(s *printer.Status) { s.Connected = false }), full, printer.ErrNotReady, "offline", "injoignable"},
		{"périmé", st(printer.StateIdle, func(s *printer.Status) { s.UpdatedAt = now.Add(-5 * time.Minute); s.Stale = true }), full, printer.ErrNotReady, "stale", "périmé"},
		{"failed sans autorisation", st(printer.StateFailed, nil), printer.StartPolicy{}, printer.ErrNotReady, "state", "FAILED"},
		{"finish sans autorisation", st(printer.StateFinished, nil), printer.StartPolicy{}, printer.ErrNotReady, "state", "FINISH"},
	}
	for _, c := range cases {
		err := printer.CheckLaunchable(c.s, 30*time.Second, now, c.pol)
		if c.wantErr == nil {
			if err != nil {
				t.Errorf("%s : lançable attendu, erreur %v", c.name, err)
			}
			continue
		}
		var nr *printer.NotReadyError
		if err == nil || !errors.Is(err, c.wantErr) || !errors.As(err, &nr) || nr.Reason != c.reason || !strings.Contains(err.Error(), c.contain) {
			t.Errorf("%s : %v (reason %v), attendu %v / %s / %q", c.name, err, nr, c.wantErr, c.reason, c.contain)
		}
	}
}

// ---- départ réel contre le simulateur depuis FAILED / FINISH ----

func termEnv(t *testing.T, mod func(*printer.BambuConfig)) *env {
	return newEnv(t, sim.Config{PrintDuration: 3 * time.Second, PrepareDuration: 600 * time.Millisecond}, func(c *printer.BambuConfig) {
		c.StartFromTerminal = true
		if mod != nil {
			mod(c)
		}
	})
}

func TestStartFromFailedResidualAfterCancel(t *testing.T) {
	e := termEnv(t, nil)
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	// La P1S réelle reste en FAILED, print_error 50348044, jusqu'au redémarrage : Bambu Studio lance quand même.
	e.sim.ForceState("FAILED", 50348044)
	waitState(t, e.ad, printer.StateFailed, 3*time.Second)
	if err := e.ad.Start(ctx5(t), e.request(ref, "t1")); err != nil {
		t.Fatalf("départ depuis FAILED (reliquat d'annulation) : %v", err)
	}
	if n := len(e.sim.StartCommands()); n != 1 {
		t.Fatalf("%d commande(s)", n)
	}
	if g := e.sim.GcodeState(); g != "PREPARE" && g != "RUNNING" {
		t.Fatalf("le simulateur n'a pas démarré : %s", g)
	}
}

func TestStartFromFinish(t *testing.T) {
	e := termEnv(t, nil)
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	e.sim.ForceState("FINISH", 0)
	waitState(t, e.ad, printer.StateFinished, 3*time.Second)
	if err := e.ad.Start(ctx5(t), e.request(ref, "t1")); err != nil {
		t.Fatalf("départ depuis FINISH : %v", err)
	}
}

func TestStartRefusedFromFailedWithActiveHMS(t *testing.T) {
	e := termEnv(t, nil)
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	e.sim.ForceState("FAILED", 0)
	waitState(t, e.ad, printer.StateFailed, 3*time.Second)
	if err := e.sim.SetFault(sim.FaultHMSError, true); err != nil { // alerte HMS active + print_error
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s, _ := e.ad.Status(context.Background()); len(s.HMS) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	err := e.ad.Start(ctx5(t), e.request(ref, "t1"))
	var nr *printer.NotReadyError
	if !errors.Is(err, printer.ErrPrinterError) || !errors.As(err, &nr) || nr.Reason != "hms_active" || !strings.Contains(err.Error(), "acquittez-la sur l'écran") {
		t.Fatalf("HMS actif : refus précis attendu, obtenu %v", err)
	}
	if n := len(e.sim.StartCommands()); n != 0 {
		t.Fatalf("rien ne devait être envoyé : %d commande(s)", n)
	}
}

// Sans StartFromTerminal (défaut inchangé) : FAILED reste refusé.
func TestStartFromFailedRefusedByDefault(t *testing.T) {
	e := newEnv(t, sim.Config{PrintDuration: 3 * time.Second}, nil)
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	e.sim.ForceState("FAILED", 0)
	waitState(t, e.ad, printer.StateFailed, 3*time.Second)
	if err := e.ad.Start(ctx5(t), e.request(ref, "t1")); !errors.Is(err, printer.ErrNotReady) {
		t.Fatalf("défaut : FAILED refusé attendu, obtenu %v", err)
	}
	if len(e.sim.StartCommands()) != 0 {
		t.Fatal("commande envoyée")
	}
}

// stg_cur : le simulateur joue les étapes selon les options reçues ; l'adaptateur les remonte dans Status.
func TestStageReportedAndFollowsOptions(t *testing.T) {
	e := termEnv(t, nil)
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	req := e.request(ref, "t1")
	req.Options = printer.DefaultStartOptions()
	req.Options.BedLeveling = false
	if err := e.ad.Start(ctx5(t), req); err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := e.ad.Status(context.Background())
		if s.StageSeen {
			seen[s.Stage] = true
		}
		if s.State == printer.StatePrinting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if seen[printer.StageBedLeveling] {
		t.Fatalf("la mise à niveau est désactivée : l'étape 1 ne doit pas être jouée (vues : %v)", seen)
	}
	if !seen[printer.StageHeatbedPreheat] && !seen[printer.StageHoming] {
		t.Fatalf("aucune étape de préparation vue : %v", seen)
	}
}
