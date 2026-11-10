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

func waitAcc(t *testing.T, e *env, ok func(*printer.AccessoryState) bool) *printer.AccessoryState {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var a *printer.AccessoryState
	for time.Now().Before(deadline) {
		st, _ := e.ad.Status(context.Background())
		a = st.Accessories
		if a != nil && ok(a) {
			return a
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("état des accessoires attendu non atteint : %+v", a)
	return nil
}

// Cas nominal : lumières, ventilateurs (pourcentage -> M106 S0..255 -> cran 0..15), reflétés dans le rapport ; aucune
// commande de démarrage, d'arrêt, de pause ni de reprise n'est envoyée ; seuls des M106 sont reçus.
func TestSimAccessoriesNominal(t *testing.T) {
	e := newEnv(t, sim.Config{}, nil)
	waitAcc(t, e, func(a *printer.AccessoryState) bool { return a.ChamberLight == "on" && a.SpeedLevel == 2 })
	ctx := ctx5(t)

	if err := e.ad.SetLight(ctx, printer.LightChamber, false); err != nil {
		t.Fatal(err)
	}
	waitAcc(t, e, func(a *printer.AccessoryState) bool { return a.ChamberLight == "off" })
	if err := e.ad.SetLight(ctx, printer.LightWork, true); err != nil {
		t.Fatal(err)
	}
	waitAcc(t, e, func(a *printer.AccessoryState) bool { return a.WorkLight == "on" })

	for fan, pct := range map[printer.FanKind]int{printer.FanPart: 100, printer.FanAux: 50, printer.FanChamber: 0} {
		if err := e.ad.SetFan(ctx, fan, pct); err != nil {
			t.Fatalf("%s %d : %v", fan, pct, err)
		}
	}
	a := waitAcc(t, e, func(a *printer.AccessoryState) bool {
		return a.PartFanPercent != nil && *a.PartFanPercent == 100 && a.AuxFanPercent != nil && *a.AuxFanPercent == 53
	})
	if a.ChamberFanPercent == nil || *a.ChamberFanPercent != 0 {
		t.Fatalf("ventilateur de chambre : %+v", a)
	}
	cl, wl, gears, _ := e.sim.Accessories()
	if cl != "off" || wl != "on" || gears != [3]int{15, 8, 0} {
		t.Fatalf("état simulé : %s %s %v", cl, wl, gears)
	}
	lines := e.sim.GcodeLines()
	if len(lines) != 3 {
		t.Fatalf("gcode reçus : %q", lines)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "M106 P") {
			t.Fatalf("seul M106 est autorisé : %q", l)
		}
	}
	assertOnlyAccessoryCommands(t, e)
}

func assertOnlyAccessoryCommands(t *testing.T, e *env, extra ...string) {
	t.Helper()
	ok := map[string]bool{"ledctrl": true, "gcode_line": true, "print_speed": true}
	for _, x := range extra {
		ok[x] = true
	}
	for _, c := range e.sim.Commands() {
		if !ok[c] {
			t.Fatalf("commande inattendue reçue par l'imprimante : %q (toutes : %v)", c, e.sim.Commands())
		}
	}
	for _, c := range []string{"stop", "pause", "resume"} {
		if n := e.sim.CommandCount(c); n != 0 {
			t.Fatalf("%s reçu %d fois", c, n)
		}
	}
}

// Bornes : valeurs hors plage refusées AVANT toute publication.
func TestSimAccessoriesBoundsSendNothing(t *testing.T) {
	e := newEnv(t, sim.Config{}, nil)
	waitAcc(t, e, func(a *printer.AccessoryState) bool { return a.SpeedLevel == 2 })
	ctx := ctx5(t)
	for _, pct := range []int{-1, 101, 255, 1000} {
		if err := e.ad.SetFan(ctx, printer.FanPart, pct); err == nil {
			t.Fatalf("%d %% aurait dû être refusé", pct)
		}
	}
	if err := e.ad.SetFan(ctx, printer.FanKind("hotend"), 10); !errors.Is(err, printer.ErrAccessoryNotAllowed) {
		t.Fatalf("ventilateur inconnu : %v", err)
	}
	if err := e.ad.SetLight(ctx, printer.LightNode("logo_light"), true); !errors.Is(err, printer.ErrAccessoryNotAllowed) {
		t.Fatalf("lumière inconnue : %v", err)
	}
	for _, lv := range []printer.SpeedLevel{0, 5, -1} {
		if err := e.ad.SetSpeed(ctx, lv); err == nil {
			t.Fatalf("niveau %d aurait dû être refusé", lv)
		}
	}
	if n := len(e.sim.Commands()); n != 0 {
		t.Fatalf("aucune commande ne devait partir : %v", e.sim.Commands())
	}
}

// La vitesse n'est changeable que pendant une impression ; les ventilateurs sont refusés pendant la préparation.
func TestSimSpeedOnlyWhilePrinting(t *testing.T) {
	e := newEnv(t, sim.Config{PrepareDuration: 4 * time.Second, PrintDuration: 8 * time.Second}, nil)
	waitAcc(t, e, func(a *printer.AccessoryState) bool { return a.SpeedLevel == 2 })
	ctx := ctx5(t)

	// Au repos : refus clair, rien n'est envoyé.
	err := e.ad.SetSpeed(ctx, printer.SpeedSport)
	if !errors.Is(err, printer.ErrBadState) {
		t.Fatalf("vitesse au repos : %v", err)
	}
	if e.sim.CommandCount("print_speed") != 0 {
		t.Fatal("print_speed ne devait pas partir au repos")
	}
	// Ventilateur et lumière : autorisés au repos.
	if err := e.ad.SetFan(ctx, printer.FanPart, 40); err != nil {
		t.Fatal(err)
	}

	ref := e.upload(newArtefact(32 * 1024))
	if err := e.ad.Start(ctx, e.request(ref, "tok-acc-1")); err != nil {
		t.Fatal(err)
	}
	// Préparation en cours (4 s) : ventilateur refusé, lumière et vitesse acceptées.
	if err := e.ad.SetFan(ctx, printer.FanPart, 10); !errors.Is(err, printer.ErrBadState) {
		t.Fatalf("ventilateur pendant la préparation : %v", err)
	}
	if err := e.ad.SetLight(ctx, printer.LightChamber, false); err != nil {
		t.Fatal(err)
	}
	waitState(t, e.ad, printer.StatePrinting, 8*time.Second)
	if err := e.ad.SetSpeed(ctx, printer.SpeedLudicrous); err != nil {
		t.Fatal(err)
	}
	waitAcc(t, e, func(a *printer.AccessoryState) bool { return a.SpeedLevel == 4 && a.SpeedName == "ludicrous" })
	if err := e.ad.SetFan(ctx, printer.FanAux, 70); err != nil {
		t.Fatal(err)
	}
	if got := e.sim.GcodeState(); got != "RUNNING" {
		t.Fatalf("l'impression doit continuer : %s", got)
	}
	// Seule commande de démarrage : celle du lancement légitime ; aucun arrêt, pause ni reprise.
	assertOnlyAccessoryCommands(t, e, "project_file")
	if n := e.sim.CommandCount("project_file"); n != 1 {
		t.Fatalf("project_file : %d", n)
	}
}

// Le simulateur lui-même refuse tout gcode_line autre que M106 : preuve que la liste blanche tient aussi en face.
func TestSimRejectsFreeGcode(t *testing.T) {
	e := newEnv(t, sim.Config{}, nil)
	waitAcc(t, e, func(a *printer.AccessoryState) bool { return a.SpeedLevel == 2 })
	// Aucun chemin public de l'adaptateur n'émet de gcode libre ; la liste blanche le garantit.
	err := printer.ValidateAccessoryPayload(map[string]any{"print": map[string]any{"sequence_id": "1", "command": "gcode_line", "param": "M104 S300\n"}})
	if !errors.Is(err, printer.ErrAccessoryNotAllowed) {
		t.Fatalf("gcode libre : %v", err)
	}
}
