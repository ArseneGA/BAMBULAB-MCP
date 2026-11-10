package printing_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// ---- FB7 : calibrations envoyées ET observées ; étape 54 nommée « non documentée » ----

func stageIDs(rep map[string]any) []int {
	var out []int
	for _, s := range rep["stages"].([]any) {
		out = append(out, int(s.(map[string]any)["id"].(float64)))
	}
	return out
}

func containsInt(l []int, v int) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// Rejoue le scénario du 9/11 avec le simulateur : flow_cali et vibration_cali demandés (défaut). La commande les porte à
// true, le suivi voit la calibration du débit (19) et celle des vibrations (25) : les deux sont « performed », jamais
// « not_observed ». L'étape 54, absente des tables publiées, est nommée « non documentée » et n'est pas prise pour une
// calibration.
func TestFB7CalibrationsSentAndObserved(t *testing.T) {
	s := slowSim(t, 3500*time.Millisecond)
	s.SetExtraPrepareStage(54)
	r, job := rigWithSim(t, "cle-depot-fb7-calibrations", s)
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	if st, b := r.humanConfirm(str(cf, "confirm_url"), true); st != 200 {
		t.Fatalf("approbation : %d %s", st, b)
	}
	p := r.sim.StartCommands()[0].Payload
	if p["flow_cali"] != true || p["vibration_cali"] != true {
		t.Fatalf("les calibrations demandées doivent être envoyées à true : %v", p)
	}
	var rep map[string]any
	waitFor(t, 30*time.Second, "calibrations vues dans les étapes", func() bool {
		m, _, _ := r.raw("get_print_status", map[string]any{"print_job_id": job})
		rep, _ = m["stage_report"].(map[string]any)
		return rep != nil && containsInt(stageIDs(rep), 54)
	})
	if rep["flow_cali"] != "performed" || rep["vibration_cali"] != "performed" {
		t.Fatalf("flow_cali=%v vibration_cali=%v (étapes %v) : les deux auraient dû être observées", rep["flow_cali"], rep["vibration_cali"], stageIDs(rep))
	}
	var name54 string
	for _, x := range rep["stages"].([]any) {
		if m := x.(map[string]any); int(m["id"].(float64)) == 54 {
			name54 = m["name"].(string)
		}
	}
	if !strings.Contains(name54, "54") || !strings.Contains(name54, "non documentée") || strings.Contains(strings.ToLower(name54), "inconnue") {
		t.Fatalf("étape 54 : %q", name54)
	}
}

// Calibrations désactivées : jamais envoyées, jamais jouées, « not_requested ».
func TestFB7CalibrationsOffNotSentNotObserved(t *testing.T) {
	r, job := rigWithSim(t, "cle-depot-fb7-sans-calibrations", slowSim(t, 2800*time.Millisecond))
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job, "flow_cali": false, "vibration_cali": false})
	if st, b := r.humanConfirm(str(cf, "confirm_url"), true); st != 200 {
		t.Fatalf("approbation : %d %s", st, b)
	}
	p := r.sim.StartCommands()[0].Payload
	if p["flow_cali"] != false || p["vibration_cali"] != false {
		t.Fatalf("calibrations désactivées : %v", p)
	}
	var rep map[string]any
	waitFor(t, 30*time.Second, "impression commencée", func() bool {
		m, _, _ := r.raw("get_print_status", map[string]any{"print_job_id": job})
		rep, _ = m["stage_report"].(map[string]any)
		return rep != nil && containsInt(stageIDs(rep), 0)
	})
	if containsInt(stageIDs(rep), 19) || containsInt(stageIDs(rep), 25) || rep["flow_cali"] != "not_requested" || rep["vibration_cali"] != "not_requested" {
		t.Fatalf("étapes %v, flow=%v, vib=%v", stageIDs(rep), rep["flow_cali"], rep["vibration_cali"])
	}
}

func TestFB7StageNames(t *testing.T) {
	if n := printer.StageName(54); n != "étape 54 (non documentée par Bambu)" || printer.StageDocumented(54) {
		t.Fatalf("étape 54 : %q documentée=%v", n, printer.StageDocumented(54))
	}
	if !printer.StageDocumented(19) || !printer.StageDocumented(25) || !printer.StageDocumented(255) {
		t.Fatal("19, 25 et 255 sont dans la table publiée")
	}
}

// ---- FB14 : événements faits hors Printalk ----

type extEvent struct {
	Event, Note string
	External    bool
}

func externalEvents(st map[string]any) []extEvent {
	var out []extEvent
	evs, _ := st["job"].(map[string]any)["events"].([]any)
	for _, e := range evs {
		m := e.(map[string]any)
		if ext, _ := m["external"].(bool); ext {
			n, _ := m["note"].(string)
			out = append(out, extEvent{Event: m["event"].(string), Note: n, External: true})
		}
	}
	return out
}

func waitExternal(t *testing.T, r *rig, job, event string) []extEvent {
	t.Helper()
	var evs []extEvent
	waitFor(t, 25*time.Second, "évènement hors Printalk "+event, func() bool {
		evs = externalEvents(r.status(job))
		for _, e := range evs {
			if e.Event == event {
				return true
			}
		}
		return false
	})
	return evs
}

func assertExternal(t *testing.T, evs []extEvent, event string) {
	t.Helper()
	for _, e := range evs {
		if e.Event == event {
			if !strings.HasPrefix(e.Note, "hors Printalk : ") {
				t.Fatalf("la note doit être marquée « hors Printalk » : %q", e.Note)
			}
			return
		}
	}
	t.Fatalf("évènement %s absent : %v", event, evs)
}

// Pause puis reprise faites sur l'écran : deux évènements « hors Printalk » horodatés, et AUCUNE commande n'a été envoyée
// par Printalk (ni pause, ni reprise).
func TestFB14ScreenPauseAndResumeRecorded(t *testing.T) {
	r, job := recRig(t, "cle-depot-fb14-pause", 100*time.Millisecond)
	r.launch(job)
	waitFor(t, 10*time.Second, "impression en cours", func() bool {
		return str(r.call("get_printer_status", map[string]any{"printer_id": r.printerID}), "state") == "printing"
	})
	if err := r.sim.ScreenCommand("pause"); err != nil {
		t.Fatal(err)
	}
	assertExternal(t, waitExternal(t, r, job, "external_pause"), "external_pause")
	time.Sleep(400 * time.Millisecond) // un même état observé plusieurs fois ne duplique pas l'évènement
	if err := r.sim.ScreenCommand("resume"); err != nil {
		t.Fatal(err)
	}
	evs := waitExternal(t, r, job, "external_resume")
	assertExternal(t, evs, "external_resume")
	n := 0
	for _, e := range evs {
		if e.Event == "external_pause" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("la pause doit être inscrite une seule fois : %v", evs)
	}
	for _, c := range []string{"pause", "resume", "stop"} {
		if r.sim.CommandCount(c) != 0 {
			t.Fatalf("Printalk ne doit envoyer aucune commande %s (%d)", c, r.sim.CommandCount(c))
		}
	}
	if got := r.printState(job); got != "PRINTING" {
		t.Fatalf("pause et reprise externes ne changent pas l'état de l'impression : %s", got)
	}
	for _, e := range evs {
		if e.External && !strings.HasPrefix(e.Note, "hors Printalk : ") {
			t.Fatalf("note : %q", e.Note)
		}
	}
	// l'heure est présente
	evsRaw, _ := r.status(job)["job"].(map[string]any)["events"].([]any)
	for _, e := range evsRaw {
		m := e.(map[string]any)
		if ext, _ := m["external"].(bool); ext {
			if _, err := time.Parse(time.RFC3339, m["at"].(string)); err != nil {
				t.Fatalf("horodatage : %v", m)
			}
		}
	}
}

// Une reprise commandée par Printalk (validée par l'humain) n'est PAS inscrite comme externe.
func TestFB14OwnResumeNotMarkedExternal(t *testing.T) {
	r, job := recRig(t, "cle-depot-fb14-reprise-propre", 100*time.Millisecond)
	r.launch(job)
	waitFor(t, 10*time.Second, "impression en cours", func() bool {
		return str(r.call("get_printer_status", map[string]any{"printer_id": r.printerID}), "state") == "printing"
	})
	if err := r.sim.ScreenCommand("pause"); err != nil {
		t.Fatal(err)
	}
	waitExternal(t, r, job, "external_pause")
	rq := r.call("resume_print", map[string]any{"print_job_id": job})
	if st, b := r.humanAction(str(rq, "action_url")); st != 200 {
		t.Fatalf("reprise : %d %s", st, b)
	}
	waitFor(t, 20*time.Second, "reprise appliquée", func() bool { return strings.Contains(strings.Join(eventNames(r.status(job)), ","), "resume_applied") })
	time.Sleep(600 * time.Millisecond)
	for _, e := range externalEvents(r.status(job)) {
		if e.Event == "external_resume" {
			t.Fatalf("la reprise de Printalk ne doit pas être « hors Printalk » : %v", e)
		}
	}
}

// Arrêt fait sur l'écran : inscrit « hors Printalk », l'impression est fermée, aucune commande envoyée.
func TestFB14ScreenStopRecorded(t *testing.T) {
	r, job := recRig(t, "cle-depot-fb14-arret", 100*time.Millisecond)
	r.launch(job)
	waitFor(t, 10*time.Second, "impression en cours", func() bool {
		return str(r.call("get_printer_status", map[string]any{"printer_id": r.printerID}), "state") == "printing"
	})
	if err := r.sim.ScreenCommand("stop"); err != nil {
		t.Fatal(err)
	}
	assertExternal(t, waitExternal(t, r, job, "external_stop"), "external_stop")
	r.waitState(job, 20*time.Second, "FAILED")
	if r.sim.CommandCount("stop") != 0 {
		t.Fatal("Printalk ne doit envoyer aucun arrêt")
	}
}

// Arrêt demandé PAR Printalk (validé par l'humain) : jamais inscrit comme externe.
func TestFB14OwnStopNotMarkedExternal(t *testing.T) {
	r, job := recRig(t, "cle-depot-fb14-arret-propre", 100*time.Millisecond)
	r.launch(job)
	waitFor(t, 10*time.Second, "impression en cours", func() bool {
		return str(r.call("get_printer_status", map[string]any{"printer_id": r.printerID}), "state") == "printing"
	})
	c := r.call("cancel_print", map[string]any{"print_job_id": job})
	if u := str(c, "stop_url"); u != "" {
		if st, b := r.humanAction2(u); st != 200 {
			t.Fatalf("arrêt : %d %s", st, b)
		}
	} else {
		t.Skipf("cancel_print sans lien d'arrêt : %v", c)
	}
	r.waitState(job, 20*time.Second, "CANCELLED")
	if evs := externalEvents(r.status(job)); len(evs) != 0 {
		t.Fatalf("un arrêt de Printalk n'est pas « hors Printalk » : %v", evs)
	}
}

// Redémarrage de l'imprimante : inscrit « hors Printalk » (external_restart).
func TestFB14RebootRecorded(t *testing.T) {
	r, job := recRig(t, "cle-depot-fb14-redemarrage", 100*time.Millisecond)
	r.launch(job)
	waitFor(t, 10*time.Second, "impression en cours", func() bool {
		return str(r.call("get_printer_status", map[string]any{"printer_id": r.printerID}), "state") == "printing"
	})
	r.sim.Reboot(2 * time.Second)
	assertExternal(t, waitExternal(t, r, job, "external_restart"), "external_restart")
}

// Relance depuis l'écran : nouveau numéro de tâche pour le même fichier -> « external_relaunch ».
func TestFB14ScreenRelaunchRecorded(t *testing.T) {
	r, job := recRig(t, "cle-depot-fb14-relance", 100*time.Millisecond)
	r.launch(job)
	waitFor(t, 10*time.Second, "impression en cours", func() bool {
		return str(r.call("get_printer_status", map[string]any{"printer_id": r.printerID}), "state") == "printing"
	})
	time.Sleep(500 * time.Millisecond) // le suivi a vu le numéro de tâche de départ
	if err := r.sim.ScreenRelaunch(); err != nil {
		t.Fatal(err)
	}
	assertExternal(t, waitExternal(t, r, job, "external_relaunch"), "external_relaunch")
	if r.sim.CommandCount("project_file") != 0 && len(r.sim.StartCommands()) != 1 {
		t.Fatal("Printalk ne doit pas relancer")
	}
}
