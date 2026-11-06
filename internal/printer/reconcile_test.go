package printer_test

import (
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
)

func TestMatchesRemoteIsExact(t *testing.T) {
	ref := "benchy-156fa5bbe39e.gcode.3mf"
	cases := []struct {
		st   printer.Status
		want bool
	}{
		{printer.Status{SubtaskName: "benchy-156fa5bbe39e"}, true},
		{printer.Status{GcodeFile: "/sdcard/benchy-156fa5bbe39e.gcode.3mf"}, true},
		{printer.Status{GcodeFile: "benchy-156fa5bbe39e.gcode.3mf"}, true},
		{printer.Status{SubtaskName: "benchy-156fa5bbe39e-copie"}, false},         // sous-chaîne : pas le même fichier
		{printer.Status{GcodeFile: "Metadata/xbenchy-156fa5bbe39e.gcode"}, false}, // contient le radical : refusé
		{printer.Status{SubtaskName: "autre"}, false},
		{printer.Status{}, false},
	}
	for i, c := range cases {
		if got := printer.MatchesRemote(c.st, ref); got != c.want {
			t.Errorf("cas %d : MatchesRemote(%+v) = %v, attendu %v", i, c.st, got, c.want)
		}
	}
	if printer.MatchesRemote(printer.Status{SubtaskName: "x"}, "") {
		t.Error("nom distant vide ne correspond à rien")
	}
}

// Sans repère temporel de la publication (service redémarré, Since inconnu), « idle » ne suffit pas :
// une commande a pu être publiée juste avant et ne pas encore avoir été traitée.
func TestReconcileNotStartedNeedsReferenceTime(t *testing.T) {
	e := newEnv(t, sim.Config{}, func(c *printer.BambuConfig) { c.ReconcileSettle = 200 * time.Millisecond })
	ref := e.upload(newArtefact(2000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	rec, err := e.ad.Reconcile(ctx5(t), printer.StartExpectation{Remote: ref})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != printer.ReconcileUnknown {
		t.Fatalf("sans repère temporel : %s (%s), unknown attendu", rec.Outcome, rec.Reason)
	}
	// Avec un repère (START_REQUESTED enregistré) assez ancien, et un rapport complet postérieur : not_started.
	rec, err = e.ad.Reconcile(ctx5(t), printer.StartExpectation{Remote: ref, Since: time.Now().Add(-time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != printer.ReconcileNotStarted {
		t.Fatalf("avec repère : %s (%s), not_started attendu", rec.Outcome, rec.Reason)
	}
	if n := len(e.sim.StartCommands()); n != 0 {
		t.Fatalf("la réconciliation a envoyé %d commande(s) de lancement", n)
	}
}

// Le délai de stabilisation est respecté : un repère trop récent attend avant de conclure.
func TestReconcileWaitsForSettle(t *testing.T) {
	e := newEnv(t, sim.Config{}, func(c *printer.BambuConfig) { c.ReconcileSettle = 600 * time.Millisecond })
	ref := e.upload(newArtefact(2000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	t0 := time.Now()
	rec, _ := e.ad.Reconcile(ctx5(t), printer.StartExpectation{Remote: ref, Since: t0})
	if rec.Outcome != printer.ReconcileNotStarted {
		t.Fatalf("%s (%s)", rec.Outcome, rec.Reason)
	}
	if d := time.Since(t0); d < 550*time.Millisecond {
		t.Fatalf("conclu après %v, avant la fin du délai de stabilisation", d)
	}
}

func TestFetchCertInfoDescribesTheCertificate(t *testing.T) {
	e := newEnv(t, sim.Config{}, nil)
	ci, err := printer.FetchCertInfo(ctx5(t), e.sim.MQTTAddr())
	if err != nil {
		t.Fatal(err)
	}
	if ci.SHA256 != e.sim.Fingerprint() || ci.SubjectCN == "" || !ci.SelfSigned {
		t.Fatalf("certificat : %+v (empreinte attendue %s)", ci, e.sim.Fingerprint())
	}
}
