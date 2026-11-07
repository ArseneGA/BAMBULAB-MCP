package printer_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
)

// TestSlowAckStillObservedWithoutResend : retour terrain (P1S, fichier de 157 Mo) — l'accusé de transport
// n'arrive pas dans le délai alors que l'impression démarre. Le lancement doit réussir par observation de CE
// fichier, et la commande ne doit jamais être renvoyée.
func TestSlowAckStillObservedWithoutResend(t *testing.T) {
	e := newEnv(t, sim.Config{Faults: []string{sim.FaultSlowAck}, SlowAckDelay: 1200 * time.Millisecond, PrintDuration: 4 * time.Second},
		func(c *printer.BambuConfig) {
			c.PubAckTimeout = time.Nanosecond // l'accusé de transport est « perdu »
			c.StartObserveTimeout = 10 * time.Second
		})
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	if err := e.ad.Start(ctx5(t), e.request(ref, "t")); err != nil {
		t.Fatalf("le départ observé doit réussir malgré l'accusé tardif : %v", err)
	}
	time.Sleep(1500 * time.Millisecond)
	if n := len(e.sim.StartCommands()); n != 1 {
		t.Fatalf("project_file reçu %d fois (jamais de second envoi)", n)
	}
}

// TestSlowAckOtherFileIsNotSuccess : sans accusé, un état « en cours » d'un AUTRE fichier ne prouve rien.
func TestSlowAckIgnoredStartIsUnknown(t *testing.T) {
	e := newEnv(t, sim.Config{Faults: []string{sim.FaultIgnoreStart}}, func(c *printer.BambuConfig) {
		c.PubAckTimeout = time.Nanosecond
		c.StartObserveTimeout = 600 * time.Millisecond
	})
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	err := e.ad.Start(ctx5(t), e.request(ref, "t"))
	if !errors.Is(err, printer.ErrStartUnknown) {
		t.Fatalf("attendu StartUnknown, reçu %v", err)
	}
	if n := len(e.sim.StartCommands()); n != 1 {
		t.Fatalf("project_file reçu %d fois", n)
	}
}
