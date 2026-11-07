package printer_test

import (
	"context"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
)

func waitAMS(t *testing.T, e *env, ok func(*printer.AMSState) bool) *printer.AMSState {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var a *printer.AMSState
	for time.Now().Before(deadline) {
		st, _ := e.ad.Status(context.Background())
		a = st.AMS
		if ok(a) {
			return a
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("état AMS attendu non atteint : %+v", a)
	return nil
}

// L'AMS simulé a quatre emplacements par défaut, reconfigurables à chaud.
func TestSimAMSDefaultAndConfigurable(t *testing.T) {
	e := newEnv(t, sim.Config{}, nil)
	a := waitAMS(t, e, func(a *printer.AMSState) bool { return a != nil && a.Present && len(a.Trays()) == 4 })
	t1 := a.Units[0].Trays[0]
	if t1.Slot != "ams1:1" || t1.Type != "PLA" || t1.Color != "#FFFFFF" || t1.RemainPercent != 100 || t1.UUID == "" {
		t.Fatalf("ams1:1 : %+v", t1)
	}
	if a.Units[0].Trays[2].RemainPercent != -1 || a.Units[0].Trays[2].UUID != "" {
		t.Fatalf("PETG sans étiquette ni restant : %+v", a.Units[0].Trays[2])
	}
	if a.ActiveSlot != "" || a.TrayNow != "255" {
		t.Fatalf("aucun emplacement actif au repos : %+v", a)
	}

	// Rechargement d'un bac : visible au rapport suivant.
	if err := e.sim.SetAMSTray(1, 3, sim.AMSTrayConfig{Type: "TPU", Color: "#00FF00", RemainPercent: 55}); err != nil {
		t.Fatal(err)
	}
	waitAMS(t, e, func(a *printer.AMSState) bool {
		return a != nil && a.Units[0].Trays[2].Type == "TPU" && a.Units[0].Trays[2].Color == "#00FF00"
	})
	if e.sim.SetAMSTray(2, 1, sim.AMSTrayConfig{}) == nil || e.sim.SetAMSTray(1, 9, sim.AMSTrayConfig{}) == nil {
		t.Fatal("bac inexistant")
	}

	// Deux AMS + bobine externe, puis plus d'AMS (bobine externe seule).
	e.sim.ConfigureAMS(sim.AMSConfig{Units: 2, External: &sim.AMSTrayConfig{Type: "ABS", Color: "FF8800", RemainPercent: -1},
		Trays: [][]sim.AMSTrayConfig{{{Type: "PLA", Color: "123456", RemainPercent: 10}}}})
	a = waitAMS(t, e, func(a *printer.AMSState) bool { return a != nil && len(a.Units) == 2 })
	if len(a.Trays()) != 9 || a.Units[1].Trays[0].Slot != "ams2:1" || !a.Units[1].Trays[0].Empty || a.External == nil || a.External.Type != "ABS" {
		t.Fatalf("2 AMS : %+v", a)
	}
	if a.Units[0].Trays[0].Color != "#123456" || a.Units[0].Trays[1].Empty != true {
		t.Fatalf("complétés par des bacs vides : %+v", a.Units[0].Trays)
	}
	e.sim.ConfigureAMS(sim.AMSConfig{Units: 0, External: &sim.AMSTrayConfig{Type: "PETG", Color: "0000FF"}})
	a = waitAMS(t, e, func(a *printer.AMSState) bool { return a != nil && a.External != nil && a.External.Type == "PETG" })
	if len(a.Units) != 0 || a.External.Slot != "ext" {
		t.Fatalf("bobine externe seule : %+v", a)
	}
}

// Pendant l'impression, tray_now suit l'ams_mapping ; à la fin le bac utilisé perd un pourcentage fixe.
func TestSimAMSActiveTrayAndConsumption(t *testing.T) {
	e := newEnv(t, sim.Config{PrintDuration: 1200 * time.Millisecond}, nil)
	waitAMS(t, e, func(a *printer.AMSState) bool { return a != nil && a.Present })
	a := newArtefact(64 * 1024)
	ref := e.upload(a)
	if err := e.ad.Start(ctx5(t), e.request(ref, "tok-ams-1")); err != nil {
		t.Fatal(err)
	}
	act := waitAMS(t, e, func(a *printer.AMSState) bool { return a != nil && a.ActiveSlot == "ams1:1" })
	if act.TrayNow != "0" {
		t.Fatalf("tray_now : %q", act.TrayNow)
	}
	waitState(t, e.ad, printer.StateFinished, 6*time.Second)
	waitAMS(t, e, func(a *printer.AMSState) bool {
		return a != nil && a.ActiveSlot == "" && a.Units[0].Trays[0].RemainPercent == 97
	})
	if got := e.sim.AMSTrayRemain(1, 1); got != 97 {
		t.Fatalf("restant simulé : %d", got)
	}
	if got := e.sim.AMSTrayRemain(1, 2); got != 85 {
		t.Fatalf("un bac non utilisé ne change pas : %d", got)
	}
}
