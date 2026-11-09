package printing_test

import (
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// Scénario de la soirée de terrain sur P1S, rejoué avec le simulateur (temps accéléré) : abandon en préparation, arrêt
// validé, redémarrage, pause sur erreur de filament, reprise, impression figée. À chaque étape l'état côté serveur doit
// suivre celui de l'imprimante en moins de 30 s, les codes HMS doivent être traduits, et une confirmation doit pouvoir
// être regénérée pour le fichier déjà déposé.

var (
	hmsHoming = sim.HMSEntry{Attr: 0x03000D00, Code: 0x00020005} // 0300-0D00-0002-0005 : mise à l'origine du plateau
	hmsExt1   = sim.HMSEntry{Attr: 0x07FF5000, Code: 0x00020001} // 07FF-5000-0002-0001 : bobine externe
	hmsExt2   = sim.HMSEntry{Attr: 0x07FF7000, Code: 0x00020008} // 07FF-7000-0002-0008 : bobine externe
)

const (
	printErrAbort = 50348032   // 0300-4000
	printErrExt   = 0x07FF8012 // 07FF-8012
)

func recSim(t *testing.T, prepare time.Duration) *sim.Sim {
	t.Helper()
	s, err := sim.New(sim.Config{ControlPort: -1, AccessCode: simAccessCode, Tick: 20 * time.Millisecond, HeartbeatInterval: 150 * time.Millisecond,
		PrepareDuration: prepare, PrintDuration: 120 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// recRig : imprimante simulée, fichier déposé, supervision en temps accéléré.
func recRig(t *testing.T, key string, prepare time.Duration) (*rig, string) {
	t.Helper()
	r, job := rigWithSim(t, key, recSim(t, prepare))
	r.c.Printing.SetWatchConfig(printing.WatchConfig{StallAfter: time.Second, IdleGrace: 300 * time.Millisecond, MismatchGrace: 500 * time.Millisecond,
		InconsistAfter: 500 * time.Millisecond, GoneAfter: time.Second, TrackTick: 100 * time.Millisecond, StopVerifyWait: 6 * time.Second, CommandWait: time.Second})
	return r, job
}

// launch : confirmation, accord humain, l'impression est suivie (PRINTING). On attend d'abord le premier relevé de
// l'imprimante (état « unknown » tant que la liaison MQTT n'a rien rapporté, long sous charge CPU) : sans lui,
// request_print_confirmation refuse « imprimante non lançable ».
func (r *rig) launch(job string) {
	r.t.Helper()
	waitFor(r.t, 30*time.Second, "premier relevé de l'imprimante (lançable)", func() bool {
		return r.call("get_printer_status", map[string]any{"printer_id": r.printerID})["launchable"] == true
	})
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	if st, b := r.humanConfirm(str(cf, "confirm_url"), true); st != 200 {
		r.t.Fatalf("approbation : %d %s", st, b)
	}
	r.waitState(job, 20*time.Second, "PRINTING")
}

func (r *rig) status(job string) map[string]any {
	return r.call("get_print_status", map[string]any{"print_job_id": job})
}

func eventNames(st map[string]any) []string {
	var out []string
	evs, _ := st["job"].(map[string]any)["events"].([]any)
	for _, e := range evs {
		out = append(out, e.(map[string]any)["event"].(string))
	}
	return out
}

func hasEvents(t *testing.T, st map[string]any, want ...string) {
	t.Helper()
	got := strings.Join(eventNames(st), ",")
	pos := 0
	for _, w := range want {
		i := strings.Index(got[pos:], w)
		if i < 0 {
			t.Fatalf("évènement %q absent (ou dans le désordre) : %s", w, got)
		}
		pos += i + len(w)
	}
}

func (r *rig) waitAlert(job, contains string, d time.Duration) map[string]any {
	r.t.Helper()
	var st map[string]any
	waitFor(r.t, d, "alerte « "+contains+" »", func() bool {
		st = r.status(job)
		return strings.Contains(str(st, "alert"), contains)
	})
	return st
}

// humanAction : l'humain ouvre /action/{id} et clique.
func (r *rig) humanAction(actionURL string) (int, string) {
	r.t.Helper()
	st, body, nonce := r.humanGet(actionURL)
	if st != 200 || nonce == "" {
		r.t.Fatalf("page d'action : %d %s", st, body)
	}
	return r.humanPost(actionURL, url.Values{"nonce": {nonce}})
}

// ---- P0-1 : abandon en préparation, puis redémarrage, puis relance du fichier déjà déposé ----

func TestAbandonInPreparationThenRebootThenReprint(t *testing.T) {
	r, job := recRig(t, "cle-depot-abandon-prepa", 60*time.Second) // la préparation ne finit jamais d'elle-même
	r.launch(job)
	r.sim.AbortPreparation([]sim.HMSEntry{hmsHoming}, printErrAbort)

	r.waitState(job, 25*time.Second, "FAILED")
	st := r.status(job)
	le := str(st, "job", "last_error")
	for _, w := range []string{"IDLE", "0300_0D00_0002_0005", "grave", "mise à l'origine", "0300_4000"} {
		if !strings.Contains(le, w) {
			t.Fatalf("cause décodée attendue (%q) : %s", w, le)
		}
	}
	if causes, _ := st["causes"].([]any); len(causes) < 2 {
		t.Fatalf("causes décodées (HMS + print_error) attendues : %v", st["causes"])
	}
	hasEvents(t, st, "request_confirmation", "confirm", "observed_printing", "fail")
	// tant que l'imprimante affiche l'alerte HMS : refus précis de relancer
	if txt := r.callErr("reprint_uploaded", map[string]any{"print_job_id": job}); !strings.Contains(txt, "HMS") {
		t.Fatalf("relance refusée avec la consigne HMS attendue : %s", txt)
	}
	if n := len(r.sim.StartCommands()); n != 1 {
		t.Fatalf("%d lancement(s) envoyé(s), 1 attendu", n)
	}
	// l'imprimante est redémarrée (alerte effacée) : une confirmation est regénérée pour le fichier DÉJÀ déposé
	r.sim.Reboot(300 * time.Millisecond)
	var out map[string]any
	waitFor(t, 25*time.Second, "imprimante au repos et relance possible", func() bool {
		o, txt, isErr := r.raw("reprint_uploaded", map[string]any{"print_job_id": job})
		if isErr {
			_ = txt
			return false
		}
		out = o
		return true
	})
	chk, _ := out["file_check"].(map[string]any)
	if chk["exists"] != true || chk["size_ok"] != true || chk["sha256_ok"] != true || chk["hashed"] != true {
		t.Fatalf("le fichier déposé doit être revérifié (taille et sha256) : %v", chk)
	}
	newJob := str(out, "print_job_id")
	if newJob == "" || newJob == job {
		t.Fatalf("une NOUVELLE impression est attendue : %v", out)
	}
	if got := r.printState(job); got != "FAILED" {
		t.Fatalf("l'historique de l'échec doit rester : %s", got)
	}
	if got := r.printState(newJob); got != "AWAITING_PRINT_CONFIRMATION" {
		t.Fatalf("nouvelle impression : %s", got)
	}
	st = r.status(newJob)
	hasEvents(t, st, "reprint_from", "confirmation_issued")
	// rien n'est lancé sans l'humain
	if n := len(r.sim.StartCommands()); n != 1 {
		t.Fatalf("la relance ne doit rien envoyer sans accord humain : %d", n)
	}
	if st, b := r.humanConfirm(str(out, "confirmation", "confirm_url"), true); st != 200 {
		t.Fatalf("approbation : %d %s", st, b)
	}
	r.waitState(newJob, 20*time.Second, "PRINTING")
	if n := len(r.sim.StartCommands()); n != 2 {
		t.Fatalf("%d lancements, 2 attendus", n)
	}
}

// Redémarrage = coupure puis reconnexion : l'impression est perdue, constatée sans attendre un état FAILED qui ne viendra pas.
func TestRebootDuringPrintDetected(t *testing.T) {
	r, job := recRig(t, "cle-depot-reboot", 60*time.Second)
	r.launch(job)
	r.sim.Reboot(1500 * time.Millisecond)
	r.waitState(job, 30*time.Second, "FAILED")
	st := r.status(job)
	if le := str(st, "job", "last_error"); !strings.Contains(le, "redémarré") {
		t.Fatalf("redémarrage attendu dans la cause : %s", le)
	}
	hasEvents(t, st, "printer_reconnected", "fail")
	// l'historique de l'échec est une vue « failed », et une confirmation peut être regénérée pour le fichier déjà déposé
	waitFor(t, 25*time.Second, "relance possible après redémarrage", func() bool {
		_, _, isErr := r.raw("reprint_uploaded", map[string]any{"print_job_id": job, "quick": true})
		return !isErr
	})
}

// Le fichier déposé a disparu de la carte : la relance le dit et ne crée rien.
func TestReprintRefusedWhenFileGone(t *testing.T) {
	r, job := recRig(t, "cle-depot-fichier-disparu", 60*time.Second)
	r.launch(job)
	r.sim.AbortPreparation(nil, 0)
	r.waitState(job, 25*time.Second, "FAILED")
	st := r.status(job)
	name := str(st, "job", "remote", "name")
	if err := r.sim.FS().Remove("/" + name); err != nil {
		t.Fatal(err)
	}
	if txt := r.callErr("reprint_uploaded", map[string]any{"print_job_id": job}); !strings.Contains(txt, "plus sur la carte") {
		t.Fatalf("disparition du fichier attendue : %s", txt)
	}
	h := r.call("list_print_history", map[string]any{"view": "active"})
	if jobs, _ := h["jobs"].([]any); len(jobs) != 0 {
		t.Fatalf("aucune impression ne doit avoir été créée : %v", jobs)
	}
}

// ---- P0-2 : arrêt validé, journalisé et vérifié ----

func TestStopIsJournaled(t *testing.T) {
	r, job := recRig(t, "cle-depot-arret-journal", 100*time.Millisecond)
	r.launch(job)
	c := r.call("cancel_print", map[string]any{"print_job_id": job})
	if st, b := r.humanAction2(str(c, "stop_url")); st != 200 {
		t.Fatalf("arrêt : %d %s", st, b)
	}
	r.waitState(job, 15*time.Second, "CANCELLED")
	hasEvents(t, r.status(job), "stop_requested", "stop_approved", "stop_sent", "stop_acked", "cancel")
}

// humanAction2 : page /stop (même forme de formulaire que /action).
func (r *rig) humanAction2(u string) (int, string) { return r.humanAction(u) }

// L'imprimante coupe les chauffes mais garde gcode_state RUNNING : l'arrêt est constaté, journalisé, l'impression passe en
// CANCELLED et le redémarrage est proposé (au lieu d'« arrêt en échec » sans trace).
func TestStopThatLeavesPrinterRunningIsVerified(t *testing.T) {
	r, job := recRig(t, "cle-depot-arret-residuel", 100*time.Millisecond)
	r.launch(job)
	if err := r.sim.SetFault(sim.FaultStopStaysRunning, true); err != nil {
		t.Fatal(err)
	}
	c := r.call("cancel_print", map[string]any{"print_job_id": job})
	st, body := r.humanAction(str(c, "stop_url"))
	if st != 200 || !strings.Contains(body, "RUNNING") || !strings.Contains(body, "Redémarrez") {
		t.Fatalf("la page doit dire que l'imprimante affiche encore RUNNING et proposer de la redémarrer : %d %s", st, body)
	}
	if got := r.printState(job); got != "CANCELLED" {
		t.Fatalf("état : %s", got)
	}
	s := r.status(job)
	hasEvents(t, s, "stop_requested", "stop_approved", "stop_sent", "stop_acked", "stop_verified", "cancel")
	if str(s, "job", "stopped_by_user") != "" && s["job"].(map[string]any)["stopped_by_user"] != true {
		t.Fatalf("stopped_by_user : %v", s["job"])
	}
	if cnt := r.sim.CommandCount("stop"); cnt != 1 {
		t.Fatalf("une seule commande d'arrêt attendue, %d envoyée(s)", cnt)
	}
	ps := r.call("get_printer_status", map[string]any{"printer_id": r.printerID})
	if !strings.Contains(str(ps, "display"), "résiduel") {
		t.Fatalf("l'état de l'imprimante doit expliquer le RUNNING résiduel : %v", ps["display"])
	}
}

// ---- P0-3 / P1-5 : pause sur erreur de filament, chargement, reprise ; reprise bancale ; impression figée ----

func TestPauseOnFilamentErrorLoadAndResume(t *testing.T) {
	r, job := recRig(t, "cle-depot-pause-filament", 100*time.Millisecond)
	var mu sync.Mutex
	var kinds []string
	r.c.Printing.OnAlert(func(a printing.Alert) { mu.Lock(); kinds = append(kinds, a.Kind); mu.Unlock() })
	r.launch(job)
	waitFor(t, 10*time.Second, "impression en cours", func() bool {
		return str(r.call("get_printer_status", map[string]any{"printer_id": r.printerID}), "state") == "printing"
	})
	r.sim.PauseWithHMS([]sim.HMSEntry{hmsExt1, hmsExt2}, printErrExt)

	st := r.waitAlert(job, "attend une action à l'écran", 25*time.Second)
	al := str(st, "alert")
	if !strings.Contains(al, "07FF_5000_0002_0001") || !strings.Contains(al, "bobine externe") {
		t.Fatalf("texte HMS décodé attendu dans l'alerte : %s", al)
	}
	if str(st, "job", "state") != "PRINTING" {
		t.Fatalf("une pause sur erreur ne termine pas l'impression : %v", st["job"])
	}
	waitFor(t, 5*time.Second, "notification d'attention", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(strings.Join(kinds, ","), "attention")
	})
	hasEvents(t, st, "attention")

	// resume_print crée seulement la demande : rien n'est envoyé sans l'humain
	rq := r.call("resume_print", map[string]any{"print_job_id": job})
	if r.sim.CommandCount("resume") != 0 {
		t.Fatal("resume_print ne doit rien envoyer")
	}
	// l'humain valide alors que l'erreur n'est pas levée : l'imprimante refuse ; une demande ne vaut qu'une fois
	if st, b := r.humanAction(str(rq, "action_url")); st != 409 || !strings.Contains(b, "refus") {
		t.Fatalf("reprise refusée par l'imprimante attendue : %d %s", st, b)
	}
	if st, _, _ := r.humanGet(str(rq, "action_url")); st != 410 {
		t.Fatalf("une demande déjà traitée ne se rejoue pas : %d", st)
	}
	if cnt := r.sim.CommandCount("resume"); cnt != 1 {
		t.Fatalf("%d commande(s) de reprise, 1 attendue", cnt)
	}

	// chargement de la bobine externe validé par le CODE de lancement de l'humain (vue de l'application)
	const code = "482915"
	r.humanSetLaunchCode(code)
	humanauth.LaunchCodesFor(r.c.Store).SetMinDelay(0)
	ld := r.call("load_filament", map[string]any{"printer_id": r.printerID, "print_job_id": job, "temperature_c": 230})
	if r.sim.CommandCount("ams_change_filament") != 0 {
		t.Fatal("load_filament ne doit rien envoyer")
	}
	if m := r.call("validate_action_with_code", map[string]any{"action_id": str(ld, "action_id"), "code": "135790"}); m["approved"] != false || str(m, "problem") != "bad_code" {
		t.Fatalf("mauvais code : %v", m)
	}
	if r.sim.CommandCount("ams_change_filament") != 0 {
		t.Fatal("un mauvais code ne doit rien exécuter")
	}
	if m := r.call("validate_action_with_code", map[string]any{"action_id": str(ld, "action_id"), "code": code}); m["approved"] != true {
		t.Fatalf("bon code : %v", m)
	}
	if cnt := r.sim.CommandCount("ams_change_filament"); cnt != 1 {
		t.Fatalf("une seule commande de chargement attendue : %d", cnt)
	}
	// reprise validée sur la page : l'impression repart
	rq = r.call("resume_print", map[string]any{"print_job_id": job})
	if st, b := r.humanAction(str(rq, "action_url")); st != 200 {
		t.Fatalf("reprise : %d %s", st, b)
	}
	waitFor(t, 20*time.Second, "imprimante reprise", func() bool {
		return str(r.call("get_printer_status", map[string]any{"printer_id": r.printerID}), "state") == "printing"
	})
	waitFor(t, 20*time.Second, "alerte levée", func() bool { return str(r.status(job), "alert") == "" })
	hasEvents(t, r.status(job), "resume_requested", "resume_approved", "resume_sent", "resume_applied")
	if got := r.printState(job); got != "PRINTING" {
		t.Fatalf("état : %s", got)
	}
}

func TestBrokenResumeIsFlagged(t *testing.T) {
	r, job := recRig(t, "cle-depot-reprise-bancale", 100*time.Millisecond)
	r.launch(job)
	waitFor(t, 10*time.Second, "impression en cours", func() bool {
		return str(r.call("get_printer_status", map[string]any{"printer_id": r.printerID}), "state") == "printing"
	})
	_ = r.sim.SetFault(sim.FaultResumeBroken, true)
	r.sim.PauseWithHMS([]sim.HMSEntry{hmsExt1}, printErrExt)
	r.waitAlert(job, "attend une action", 25*time.Second)
	r.sim.ClearErrors() // filament rechargé à l'écran
	rq := r.call("resume_print", map[string]any{"print_job_id": job})
	if st, b := r.humanAction(str(rq, "action_url")); st != 200 {
		t.Fatalf("reprise : %d %s", st, b)
	}
	// RUNNING mais figé, buse chaude, consigne plateau à 0 °C
	st := r.waitAlert(job, "bloquée", 25*time.Second)
	if !strings.Contains(str(st, "alert"), "plateau à 0") {
		t.Fatalf("consigne plateau à 0 °C attendue dans l'alerte : %s", str(st, "alert"))
	}
	if str(st, "job", "state") != "PRINTING" {
		t.Fatalf("alerte seulement, pas de changement d'état : %v", st["job"])
	}
	hasEvents(t, st, "stalled")
}

func TestFrozenPrintIsFlaggedAndClears(t *testing.T) {
	r, job := recRig(t, "cle-depot-fige", 100*time.Millisecond)
	r.launch(job)
	waitFor(t, 10*time.Second, "impression en cours", func() bool {
		return str(r.call("get_printer_status", map[string]any{"printer_id": r.printerID}), "state") == "printing"
	})
	_ = r.sim.SetFault(sim.FaultFreeze, true)
	st := r.waitAlert(job, "bloquée : couche", 25*time.Second)
	if !strings.Contains(str(st, "alert"), "figés") {
		t.Fatalf("progression figée attendue : %s", str(st, "alert"))
	}
	_ = r.sim.SetFault(sim.FaultFreeze, false)
	waitFor(t, 20*time.Second, "alerte levée quand ça avance", func() bool { return str(r.status(job), "alert") == "" })
}

// ---- P2-7 : fichiers de la carte SD ----

func TestListAndCleanupPrinterFiles(t *testing.T) {
	r, job := recRig(t, "cle-depot-fichiers", 100*time.Millisecond)
	fs := r.sim.FS()
	old := time.Now().Add(-72 * time.Hour)
	for _, n := range []string{"vieux_modele_v1-aaaaaaaaaaaa.gcode.3mf", "autre_v2-bbbbbbbbbbbb.gcode.3mf", "notes.txt"} {
		if err := afero.WriteFile(fs, "/"+n, []byte("0123456789"), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = fs.Chtimes("/"+n, old, old)
	}
	l := r.call("list_printer_files", map[string]any{"printer_id": r.printerID})
	files, _ := l["files"].([]any)
	byName := map[string]map[string]any{}
	for _, f := range files {
		m := f.(map[string]any)
		byName[m["name"].(string)] = m
	}
	dep := str(r.status(job), "job", "remote", "name")
	if byName[dep] == nil || byName[dep]["in_use"] != true || byName[dep]["obsolete"] == true {
		t.Fatalf("le fichier de l'impression en attente doit être « en usage » : %v", byName[dep])
	}
	if ids, _ := byName[dep]["print_job_ids"].([]any); len(ids) != 1 || ids[0] != job {
		t.Fatalf("lien vers l'impression attendu : %v", byName[dep])
	}
	if byName["notes.txt"]["managed"] == true || byName["notes.txt"]["obsolete"] == true {
		t.Fatalf("un fichier que bambud n'a pas déposé n'est jamais candidat : %v", byName["notes.txt"])
	}
	if byName["vieux_modele_v1-aaaaaaaaaaaa.gcode.3mf"]["obsolete"] != true {
		t.Fatalf("fichier ancien sans impression connue : obsolète attendu : %v", byName["vieux_modele_v1-aaaaaaaaaaaa.gcode.3mf"])
	}
	// nettoyage : liste exacte, validation humaine
	if txt := r.callErr("cleanup_printer_files", map[string]any{"printer_id": r.printerID, "names": []string{"notes.txt"}}); !strings.Contains(txt, "candidat") {
		t.Fatalf("un non-candidat est refusé : %s", txt)
	}
	cq := r.call("cleanup_printer_files", map[string]any{"printer_id": r.printerID})
	if !exists(r, "vieux_modele_v1-aaaaaaaaaaaa.gcode.3mf") {
		t.Fatal("rien ne doit être supprimé avant la validation humaine")
	}
	st, body, _ := r.humanGet(str(cq, "action_url"))
	if st != 200 || !strings.Contains(body, "vieux_modele_v1-aaaaaaaaaaaa.gcode.3mf") || !strings.Contains(body, "autre_v2-bbbbbbbbbbbb.gcode.3mf") || strings.Contains(body, "notes.txt") {
		t.Fatalf("la page doit lister EXACTEMENT les fichiers à supprimer : %d %s", st, body)
	}
	if st, b := r.humanAction(str(cq, "action_url")); st != 200 || !strings.Contains(b, "vieux_modele_v1-aaaaaaaaaaaa.gcode.3mf") {
		t.Fatalf("nettoyage : %d %s", st, b)
	}
	if exists(r, "vieux_modele_v1-aaaaaaaaaaaa.gcode.3mf") || exists(r, "autre_v2-bbbbbbbbbbbb.gcode.3mf") {
		t.Fatal("les fichiers obsolètes devaient être supprimés")
	}
	if !exists(r, "notes.txt") || !exists(r, dep) {
		t.Fatal("notes.txt et le fichier en usage ne doivent jamais être supprimés")
	}
}

// ---- P1-6 : diagnostic ----

func TestDiagnoseConnectionOnSimulator(t *testing.T) {
	r, _ := recRig(t, "cle-depot-diag", 100*time.Millisecond)
	d := r.call("diagnose_connection", map[string]any{"printer_id": r.printerID})
	rep, _ := d["report"].(map[string]any)
	if rep["dns"] == nil || rep["diagnosis"] == nil || len(rep["ports"].([]any)) != 3 {
		t.Fatalf("rapport incomplet : %v", d)
	}
	ports := rep["ports"].([]any)
	if ports[0].(map[string]any)["open"] != true || ports[1].(map[string]any)["open"] != true {
		t.Fatalf("MQTT et FTPS du simulateur doivent répondre : %v", ports)
	}
	if str(d, "last_connected") == "" {
		t.Fatalf("dernière connexion réussie attendue : %v", d)
	}
}
