package printing_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

var accessoryTools = []string{"set_printer_light", "set_printer_fan", "set_print_speed_level"}

// forbiddenToolName : un outil dont le nom évoque un lancement, un arrêt, une reprise ou une approbation est interdit.
var forbiddenToolName = regexp.MustCompile(`(?i)approve|start|launch|stop|resume|cancel`)

func accessoryRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	r.c.Printing.SetValidator(fakeValidator("0.4"))
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))
	r.waitPrinterState(r.printerID, "idle")
	return r
}

// commandsAreAccessoriesOnly : la seule chose que l'imprimante a reçue ce sont des commandes d'accessoires (et, si
// allowed le dit, le lancement humain) ; jamais pause, reprise ni arrêt, et tout gcode_line est un M106.
func (r *rig) commandsAreAccessoriesOnly(allowed ...string) {
	r.t.Helper()
	ok := map[string]bool{"ledctrl": true, "gcode_line": true, "print_speed": true}
	for _, a := range allowed {
		ok[a] = true
	}
	for _, c := range r.sim.Commands() {
		if !ok[c] {
			r.t.Fatalf("commande inattendue reçue par l'imprimante : %q (toutes : %v)", c, r.sim.Commands())
		}
	}
	for _, c := range []string{"stop", "pause", "resume"} {
		if r.sim.CommandCount(c) != 0 {
			r.t.Fatalf("%s a été envoyé", c)
		}
	}
	for _, l := range r.sim.GcodeLines() {
		if !regexp.MustCompile(`^M106 P[123] S[0-9]{1,3}\n$`).MatchString(l) {
			r.t.Fatalf("gcode hors liste blanche reçu : %q", l)
		}
	}
}

func TestAccessoryToolsSchemasAndNames(t *testing.T) {
	r := accessoryRig(t)
	tools, err := r.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, tl := range tools.Tools {
		for _, n := range accessoryTools {
			if tl.Name != n {
				continue
			}
			found[n] = true
			if forbiddenToolName.MatchString(n) {
				t.Fatalf("%s : le nom ne doit pas évoquer lancement, arrêt, reprise ou approbation", n)
			}
			if tl.Title == "" || tl.OutputSchema == nil || tl.Description == "" {
				t.Fatalf("%s : Title, outputSchema et description requis", n)
			}
			a := tl.Annotations
			if a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint || !a.IdempotentHint || a.OpenWorldHint == nil || !*a.OpenWorldHint {
				t.Fatalf("%s : annotations incorrectes (écriture, non destructif, idempotent, monde ouvert) : %+v", n, a)
			}
			schema, _ := json.Marshal(tl.InputSchema)
			for _, f := range []string{"confirmation_id", "plate_clear", "approved", "approve", "nonce", "token", "human", "code", "param", "gcode"} {
				if strings.Contains(string(schema), `"`+f+`"`) {
					t.Fatalf("%s : champ interdit %q dans le schéma d'entrée : %s", n, f, schema)
				}
			}
		}
	}
	for _, n := range accessoryTools {
		if !found[n] {
			t.Fatalf("outil %s absent", n)
		}
	}
}

func TestAccessoryNominalBoundsAndAudit(t *testing.T) {
	r := accessoryRig(t)
	id := r.printerID

	// get_printer_status expose l'état courant et les accessoires du modèle.
	st := r.call("get_printer_status", map[string]any{"printer_id": id})
	if m, _ := st["available_accessories"].(map[string]any); m == nil || m["chamber_light"] != true || m["chamber_fan"] != true || m["work_light"] != false {
		t.Fatalf("available_accessories (P1S) : %v", st["available_accessories"])
	}
	waitFor(t, 15*time.Second, "accessoires rapportés", func() bool {
		a, _ := r.call("get_printer_status", map[string]any{"printer_id": id})["accessories"].(map[string]any)
		return a != nil && a["chamber_light"] == "on" && a["speed_level"] == float64(2)
	})

	// list_printer_models : accessoires du modèle.
	ms := r.call("list_printer_models", map[string]any{})
	foundP1S := false
	for _, x := range ms["models"].([]any) {
		m := x.(map[string]any)
		if m["id"] == "p1s" {
			a, _ := m["accessories"].(map[string]any)
			if a == nil || a["chamber_light"] != true || a["speed_levels"] != true {
				t.Fatalf("list_printer_models p1s : %v", m)
			}
			foundP1S = true
		}
	}
	if !foundP1S {
		t.Fatal("modèle p1s absent")
	}

	// Lumière : éteindre, puis relire l'état.
	out := r.call("set_printer_light", map[string]any{"light": "chamber", "on": false})
	if out["applied"] != true || str(out, "accessory") != "light:chamber" || str(out, "value") != "off" {
		t.Fatalf("set_printer_light : %v", out)
	}
	waitFor(t, 15*time.Second, "lumière éteinte rapportée", func() bool {
		a, _ := r.call("get_printer_status", map[string]any{"printer_id": id})["accessories"].(map[string]any)
		return a != nil && a["chamber_light"] == "off"
	})
	// Ventilateur auxiliaire à 60 % : M106 P2 S153 -> cran 9.
	out = r.call("set_printer_fan", map[string]any{"printer_id": id, "fan": "aux", "percent": 60})
	if out["applied"] != true {
		t.Fatalf("set_printer_fan : %v", out)
	}
	if lines := r.sim.GcodeLines(); len(lines) != 1 || lines[0] != "M106 P2 S153\n" {
		t.Fatalf("gcode reçu : %q", lines)
	}

	// Bornes et entrées invalides : refus, rien n'est envoyé.
	before := len(r.sim.Commands())
	for _, args := range []map[string]any{
		{"fan": "part", "percent": 101}, {"fan": "part", "percent": -1}, {"fan": "hotend", "percent": 10}, {"fan": "part"},
	} {
		if txt := r.callErr("set_printer_fan", args); txt == "" {
			t.Fatalf("%v aurait dû être refusé", args)
		}
	}
	if txt := r.callErr("set_printer_light", map[string]any{"light": "logo", "on": true}); !strings.Contains(txt, "inconnue") {
		t.Fatalf("lumière inconnue : %s", txt)
	}
	// Vitesse hors impression : refus clair, rien n'est envoyé.
	if txt := r.callErr("set_print_speed_level", map[string]any{"level": "sport"}); !strings.Contains(txt, "impression") {
		t.Fatalf("vitesse au repos : %s", txt)
	}
	if txt := r.callErr("set_print_speed_level", map[string]any{"level": "turbo"}); txt == "" {
		t.Fatal("niveau inconnu")
	}
	if len(r.sim.Commands()) != before {
		t.Fatalf("des commandes sont parties malgré les refus : %v", r.sim.Commands())
	}

	// HTTP (page web) : mêmes règles.
	st2, body, _ := r.httpJSON("POST", "/api/printers/"+id+"/accessories/light", `{"light":"work","on":true}`)
	if st2 != 409 { // le P1S n'a pas de lumière de tête
		t.Fatalf("lumière de tête sur P1S : %d %v", st2, body)
	}
	st2, body, _ = r.httpJSON("POST", "/api/printers/"+id+"/accessories/fan", `{"fan":"part","percent":30}`)
	if st2 != 200 || body["applied"] != true {
		t.Fatalf("HTTP fan : %d %v", st2, body)
	}
	if st2, _, _ = r.httpJSON("POST", "/api/printers/"+id+"/accessories/fan", `{"fan":"part","percent":300}`); st2 != 400 {
		t.Fatalf("HTTP fan hors bornes : %d", st2)
	}
	if st2, _, _ = r.httpJSON("POST", "/api/printers/"+id+"/accessories/speed", `{"level":"sport"}`); st2 != 409 {
		t.Fatalf("HTTP vitesse au repos : %d", st2)
	}

	// Audit : qui / quoi / valeur, sans secret.
	es, err := r.c.Store.ListAudit(context.Background(), "printer", id, 50)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range es {
		if e.Operation != "printer.accessory_set" {
			continue
		}
		var d map[string]any
		_ = json.Unmarshal(e.Detail, &d)
		seen[e.Actor+"|"+d["accessory"].(string)+"|"+d["value"].(string)+"|"+d["outcome"].(string)] = true
	}
	for _, want := range []string{"mcp|light:chamber|off|ok", "mcp|fan:aux|60|ok", "web|fan:part|30|ok"} {
		if !seen[want] {
			t.Fatalf("entrée d'audit %q absente : %v", want, seen)
		}
	}
	r.assertNoLeak(simAccessCode)
	r.commandsAreAccessoriesOnly()
}

// Un modèle sans lumière de chambre : refus clair, aucune commande envoyée.
func TestAccessoryAbsentOnModelRefused(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	if err := r.c.Printing.PutPrinterModel(context.Background(), contracts.PrinterModel{ID: "p1p", Name: "Bambu Lab P1P", Vendor: "Bambu Lab",
		BuildVolume: &contracts.Dimensions{X: 256, Y: 256, Z: 256}, NozzleDiametersMM: []float64{0.4}, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	ap := r.addPrinter(map[string]any{"model_id": "p1p", "name": "P1P de test"})
	r.printerID = str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))
	r.waitPrinterState(r.printerID, "idle")

	for _, args := range []map[string]any{{"light": "chamber", "on": true}, {"light": "work", "on": true}} {
		if txt := r.callErr("set_printer_light", args); !strings.Contains(txt, "non disponible sur ce modèle") {
			t.Fatalf("%v : %s", args, txt)
		}
	}
	if txt := r.callErr("set_printer_fan", map[string]any{"fan": "chamber", "percent": 50}); !strings.Contains(txt, "non disponible sur ce modèle") {
		t.Fatalf("ventilateur de chambre P1P : %s", txt)
	}
	// Le ventilateur auxiliaire existe sur la P1P.
	if out := r.call("set_printer_fan", map[string]any{"fan": "aux", "percent": 100}); out["applied"] != true {
		t.Fatalf("aux P1P : %v", out)
	}
	if n := r.sim.CommandCount("ledctrl"); n != 0 {
		t.Fatalf("ledctrl envoyé %d fois malgré le refus", n)
	}
	if l := r.sim.GcodeLines(); len(l) != 1 || l[0] != "M106 P2 S255\n" {
		t.Fatalf("gcode : %q", l)
	}
	st := r.call("get_printer_status", map[string]any{"printer_id": r.printerID})
	if m, _ := st["available_accessories"].(map[string]any); m == nil || m["chamber_light"] != false || m["aux_fan"] != true {
		t.Fatalf("available_accessories P1P : %v", st["available_accessories"])
	}
	r.commandsAreAccessoriesOnly()
}

// Pendant une impression : la vitesse se change, l'impression continue, et aucune commande de démarrage, d'arrêt, de
// pause ou de reprise n'est envoyée par les outils d'accessoires.
func TestAccessoryToolsNeverStartStopPauseOrResume(t *testing.T) {
	r, job := rigWithSim(t, "cle-accessoires", slowSim(t, 300*time.Millisecond))
	r.launch(job) // lancement par le chemin humain habituel : la seule commande de démarrage attendue
	if n := r.sim.CommandCount("project_file"); n != 1 {
		t.Fatalf("project_file : %d", n)
	}
	for _, lv := range []string{"silent", "sport", "ludicrous", "normal", "3"} {
		if out := r.call("set_print_speed_level", map[string]any{"level": lv}); out["applied"] != true {
			t.Fatalf("vitesse %s : %v", lv, out)
		}
	}
	waitFor(t, 15*time.Second, "vitesse rapportée", func() bool {
		a, _ := r.call("get_printer_status", map[string]any{"printer_id": r.printerID})["accessories"].(map[string]any)
		return a != nil && a["speed_level"] == float64(3) && a["speed_name"] == "sport"
	})
	r.waitPrinterState(r.printerID, "printing") // la préparation (mise à niveau) est finie : les ventilateurs sont acceptés
	r.call("set_printer_light", map[string]any{"light": "chamber", "on": true})
	r.call("set_printer_fan", map[string]any{"fan": "part", "percent": 80})
	if r.sim.GcodeState() != "RUNNING" && r.sim.GcodeState() != "PREPARE" {
		t.Fatalf("l'impression a changé d'état : %s", r.sim.GcodeState())
	}
	if r.printState(job) != "PRINTING" {
		t.Fatalf("impression : %s", r.printState(job))
	}
	r.commandsAreAccessoriesOnly("project_file")
	if n := r.sim.CommandCount("project_file"); n != 1 {
		t.Fatalf("project_file : %d", n)
	}
}
