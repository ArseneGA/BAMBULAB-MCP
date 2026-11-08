package printing_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
)

// rigWithSim : comme rigPrepared, avec un simulateur donné (préparation lente pour voir les étapes).
func rigWithSim(t *testing.T, key string, s *sim.Sim) (*rig, string) {
	t.Helper()
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = s
	r.seedPrinterModel()
	r.c.Printing.SetValidator(fakeValidator("0.4"))
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))
	job, _ := r.uploadAndAsk(r.fakeArtifact(), key)
	return r, job
}

func slowSim(t *testing.T, prepare time.Duration) *sim.Sim {
	t.Helper()
	s, err := sim.New(sim.Config{ControlPort: -1, AccessCode: simAccessCode, Tick: 20 * time.Millisecond, HeartbeatInterval: 150 * time.Millisecond,
		PrepareDuration: prepare, PrintDuration: 8 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// ---- point 6 : FAILED résiduel après une annulation ----

// La P1S reste en FAILED (print_error 50348044) après une annulation : la confirmation est émise, le lancement
// part et l'impression est suivie (« Autorisé, pas démarré » n'arrive plus).
func TestStartFromFailedAfterCancelEndToEnd(t *testing.T) {
	r, job := rigPrepared(t, "cle-depot-failed-residuel")
	r.sim.ForceState("FAILED", 50348044)
	st := r.waitPrinterState(r.printerID, "failed")
	if st["launchable"] != true {
		t.Fatalf("FAILED + reliquat d'annulation : lançable attendu : %v", st)
	}
	det, _ := st["error_details"].([]any)
	if len(det) != 1 || !strings.Contains(det[0].(map[string]any)["text"].(string), "annulée") || det[0].(map[string]any)["short"] != "0300_400C" {
		t.Fatalf("print_error non décodé : %v", st["error_details"])
	}
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	if st, b := r.humanConfirm(str(cf, "confirm_url"), true); st != 200 {
		t.Fatalf("approbation : %d %s", st, b)
	}
	waitFor(t, 20*time.Second, "impression suivie après départ depuis FAILED", func() bool {
		s := r.printState(job)
		return s == "PRINTING" || s == "COMPLETED"
	})
	if n := len(r.sim.StartCommands()); n != 1 {
		t.Fatalf("%d commande(s) de lancement, 1 attendue", n)
	}
}

func TestStartFromFinishEndToEnd(t *testing.T) {
	r, job := rigPrepared(t, "cle-depot-finish-nouveau")
	r.sim.ForceState("FINISH", 0)
	r.waitPrinterState(r.printerID, "finished")
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	if st, b := r.humanConfirm(str(cf, "confirm_url"), true); st != 200 {
		t.Fatalf("approbation : %d %s", st, b)
	}
	waitFor(t, 20*time.Second, "impression suivie après départ depuis FINISH", func() bool {
		s := r.printState(job)
		return s == "PRINTING" || s == "COMPLETED"
	})
}

// AVANT d'émettre une confirmation : HMS actif -> refus précis, aucune confirmation créée, rien d'envoyé.
func TestConfirmationRefusedWhenHMSActive(t *testing.T) {
	r, job := rigPrepared(t, "cle-depot-hms-actif")
	r.sim.ForceState("FAILED", 0)
	if err := r.sim.SetFault(sim.FaultHMSError, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "HMS actif visible", func() bool {
		st := r.call("get_printer_status", map[string]any{"printer_id": r.printerID})
		return st["hms_count"] != nil && st["hms_count"].(float64) > 0
	})
	txt := r.callErr("request_print_confirmation", map[string]any{"print_job_id": job})
	for _, want := range []string{"erreur HMS", "acquittez-la sur l'écran", "aucune confirmation n'a été émise"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("consigne précise attendue (%q) : %s", want, txt)
		}
	}
	if n := len(r.sim.StartCommands()); n != 0 {
		t.Fatalf("rien ne devait être envoyé : %d", n)
	}
	if got := r.printState(job); got != "AWAITING_PRINT_CONFIRMATION" {
		t.Fatalf("l'impression ne doit pas avoir bougé : %s", got)
	}
	// get_printer_status explique pourquoi (launchable false + consigne).
	st := r.call("get_printer_status", map[string]any{"printer_id": r.printerID})
	if st["launchable"] != false || !strings.Contains(str(st, "launch_blocker"), "HMS") {
		t.Fatalf("launchable/launch_blocker : %v", st)
	}
	// Une fois l'alerte acquittée : la confirmation redevient possible.
	if err := r.sim.SetFault(sim.FaultHMSError, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "HMS levé", func() bool {
		return r.call("get_printer_status", map[string]any{"printer_id": r.printerID})["launchable"] == true
	})
	r.call("request_print_confirmation", map[string]any{"print_job_id": job})
}

func TestConfirmationRefusedWhenPrinterBusy(t *testing.T) {
	r, job := rigPrepared(t, "cle-depot-imprimante-occupee")
	r.sim.ForceState("PAUSE", 0)
	r.waitPrinterState(r.printerID, "paused")
	if txt := r.callErr("request_print_confirmation", map[string]any{"print_job_id": job}); !strings.Contains(txt, "occupée") {
		t.Fatalf("imprimante occupée : consigne attendue : %s", txt)
	}
}

// ---- point 7 : options de démarrage dans la liaison ----

func TestFlowVibrationTimelapseAreBoundShownAndSent(t *testing.T) {
	r, job := rigPrepared(t, "cle-depot-options-liaison")
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job, "flow_cali": false, "vibration_cali": false, "timelapse": true})
	b := cf["binding"].(map[string]any)
	if b["flow_cali"] != false || b["vibration_cali"] != false || b["timelapse"] != true || b["bed_leveling"] != true {
		t.Fatalf("liaison : %v", b)
	}
	_, body, _ := r.humanGet(str(cf, "confirm_url"))
	for _, want := range []string{"Calibration du débit", "Calibration des vibrations", "Timelapse", "<strong>oui</strong> (enregistré pendant l'impression)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("la page doit afficher %q : %s", want, body)
		}
	}
	if strings.Count(body, "<strong>non</strong> (désactivée pour cette impression)") != 2 {
		t.Fatalf("flow et vibrations désactivés doivent être annoncés : %s", body)
	}
	// Changer une option exige une NOUVELLE confirmation : l'ancienne ne vaut plus.
	cf2 := r.call("request_print_confirmation", map[string]any{"print_job_id": job, "flow_cali": false, "vibration_cali": false, "timelapse": false})
	if str(cf2, "confirmation_id") == str(cf, "confirmation_id") {
		t.Fatal("nouvelle confirmation attendue")
	}
	if st, _, _ := r.humanGet(str(cf, "confirm_url")); st == 200 {
		t.Fatal("l'ancienne confirmation (autres options) ne doit plus être validable")
	}
	cf3 := r.call("request_print_confirmation", map[string]any{"print_job_id": job, "flow_cali": false, "vibration_cali": false, "timelapse": true})
	if st, b := r.humanConfirm(str(cf3, "confirm_url"), true); st != 200 {
		t.Fatalf("approbation : %d %s", st, b)
	}
	p := r.sim.StartCommands()[0].Payload
	if p["flow_cali"] != false || p["vibration_cali"] != false || p["timelapse"] != true || p["bed_leveling"] != true {
		t.Fatalf("la commande doit porter EXACTEMENT ce que l'humain a approuvé : %v", p)
	}
}

// Valeurs par défaut inchangées : ce qui part est ce qui partait déjà.
func TestStartOptionsDefaultsUnchanged(t *testing.T) {
	r, job := rigPrepared(t, "cle-depot-options-defaut")
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	b := cf["binding"].(map[string]any)
	if b["flow_cali"] != true || b["vibration_cali"] != true || b["timelapse"] != false {
		t.Fatalf("défauts de la liaison : %v", b)
	}
	if st, bb := r.humanConfirm(str(cf, "confirm_url"), true); st != 200 {
		t.Fatalf("approbation : %d %s", st, bb)
	}
	p := r.sim.StartCommands()[0].Payload
	if p["flow_cali"] != true || p["vibration_cali"] != true || p["timelapse"] != false || p["bed_leveling"] != true || p["layer_inspect"] != false || p["bed_type"] != "auto" {
		t.Fatalf("options envoyées par défaut : %v", p)
	}
}

// ---- point 7 : étapes réellement exécutées ----

func stageNames(rep map[string]any) []string {
	var out []string
	for _, s := range rep["stages"].([]any) {
		out = append(out, s.(map[string]any)["name"].(string))
	}
	return out
}

func TestStageReportShowsBedLevelingActuallyPerformed(t *testing.T) {
	r, job := rigWithSim(t, "cle-depot-etapes-nivellement", slowSim(t, 2400*time.Millisecond))
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	if st, b := r.humanConfirm(str(cf, "confirm_url"), true); st != 200 {
		t.Fatalf("approbation : %d %s", st, b)
	}
	var rep map[string]any
	waitFor(t, 25*time.Second, "mise à niveau vue dans les étapes", func() bool {
		m, txt, _ := r.raw("get_print_status", map[string]any{"print_job_id": job})
		rep, _ = m["stage_report"].(map[string]any)
		if rep != nil && rep["bed_leveling"] == "performed" {
			if !strings.Contains(txt, "Mise à niveau du plateau : ") || !strings.Contains(txt, "Étapes vues sur l'imprimante") {
				t.Fatalf("le texte doit rapporter l'étape : %s", txt)
			}
			return true
		}
		return false
	})
	names := stageNames(rep)
	if !contains(names, "Mise à niveau du plateau") || !contains(names, "Chauffe du plateau") {
		t.Fatalf("étapes vues : %v", names)
	}
	first := rep["stages"].([]any)[0].(map[string]any)
	if first["first_seen_local"] == "" || !strings.Contains(first["label"].(string), "effectuée à ") && !strings.Contains(first["label"].(string), "en cours depuis ") {
		t.Fatalf("heure locale et libellé attendus : %v", first)
	}
	if rep["flow_cali"] != "performed" && rep["flow_cali"] != "not_observed" {
		t.Fatalf("flow_cali : %v", rep["flow_cali"])
	}
}

func TestStageReportBedLevelingOffNotPerformed(t *testing.T) {
	r, job := rigWithSim(t, "cle-depot-etapes-sans-nivellement", slowSim(t, 2400*time.Millisecond))
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job, "bed_leveling": false})
	if st, b := r.humanConfirm(str(cf, "confirm_url"), true); st != 200 {
		t.Fatalf("approbation : %d %s", st, b)
	}
	var rep map[string]any
	waitFor(t, 25*time.Second, "étapes vues", func() bool {
		m, _, _ := r.raw("get_print_status", map[string]any{"print_job_id": job})
		rep, _ = m["stage_report"].(map[string]any)
		return rep != nil && len(rep["stages"].([]any)) >= 3 && str(m, "job", "state") == "PRINTING" && contains(stageNames(rep), "Impression")
	})
	if contains(stageNames(rep), "Mise à niveau du plateau") || rep["bed_leveling"] != "not_requested" {
		t.Fatalf("mise à niveau désactivée : jamais exécutée (%v, %v)", stageNames(rep), rep["bed_leveling"])
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// ---- point 13 : attente d'une impression ----

func TestWaitForPrintChangeReturnsOnStageOrStateChange(t *testing.T) {
	r, job := rigWithSim(t, "cle-depot-attente-impression", slowSim(t, 2400*time.Millisecond))
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	if st, b := r.humanConfirm(str(cf, "confirm_url"), true); st != 200 {
		t.Fatalf("approbation : %d %s", st, b)
	}
	t0 := time.Now()
	m := r.call("wait_for_print_change", map[string]any{"print_job_id": job, "timeout_seconds": 20})
	w := m["wait"].(map[string]any)
	if w["changed"] != true || time.Since(t0) > 15*time.Second || str(w, "reason") == "" {
		t.Fatalf("attente : %v (%s)", w, time.Since(t0))
	}
	// wait_seconds sur get_print_status : même comportement, plafonné.
	m2 := r.call("get_print_status", map[string]any{"print_job_id": job, "wait_seconds": 2})
	if m2["wait"] == nil {
		t.Fatalf("wait_seconds : bloc wait attendu : %v", m2)
	}
	// État final atteint : retour immédiat.
	r.sim.ForceState("IDLE", 0)
	r.waitState(job, 30*time.Second, "COMPLETED", "FAILED", "PRINTING")
}

// ---- point 7 : read_gcode_excerpt ----

func TestReadGcodeExcerptBoundedReadOnly(t *testing.T) {
	r, job := rigPrepared(t, "cle-depot-extrait-gcode")
	// Artefact factice (octets quelconques, pas une archive) : lecture refusée proprement, jamais de panique.
	if txt := r.callErr("read_gcode_excerpt", map[string]any{"print_job_id": job}); txt == "" {
		t.Fatal("erreur attendue sur un artefact non lisible")
	}
	if txt := r.callErr("read_gcode_excerpt", map[string]any{}); !strings.Contains(txt, "artifact_id ou print_job_id") {
		t.Fatalf("entrée vide : %s", txt)
	}
	if txt := r.callErr("read_gcode_excerpt", map[string]any{"print_job_id": job, "grep": "(["}); txt == "" {
		t.Fatal("motif invalide accepté")
	}
	if txt := r.callErr("read_gcode_excerpt", map[string]any{"print_job_id": job, "head_lines": 5000}); txt == "" {
		t.Fatal("head_lines hors borne accepté")
	}
	if txt := r.callErr("read_gcode_excerpt", map[string]any{"print_job_id": job, "grep": strings.Repeat("a", 500)}); txt == "" {
		t.Fatal("motif trop long accepté")
	}
}
