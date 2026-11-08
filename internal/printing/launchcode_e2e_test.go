package printing_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
)

// ---- capture des journaux et des réponses d'outils : le code de lancement ne doit apparaître nulle part ----

var (
	sinkMu  sync.Mutex
	logSink bytes.Buffer // journaux (niveau Debug) du service, du serveur MCP et de l'API
	rspSink bytes.Buffer // toutes les réponses d'outils et erreurs reçues par les tests
)

func captureLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&lockedWriter{&logSink}, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type lockedWriter struct{ b *bytes.Buffer }

func (w *lockedWriter) Write(p []byte) (int, error) {
	sinkMu.Lock()
	defer sinkMu.Unlock()
	return w.b.Write(p)
}

func leakSinkAdd(txt string, m map[string]any) {
	b, _ := json.Marshal(m)
	sinkMu.Lock()
	rspSink.WriteString(txt + "\n" + string(b) + "\n")
	sinkMu.Unlock()
}

// assertNoLeak : le code n'est dans aucune réponse, aucun journal, aucun audit, aucun fichier de données.
func (r *rig) assertNoLeak(codes ...string) {
	r.t.Helper()
	sinkMu.Lock()
	logs, rsp := logSink.String(), rspSink.String()
	sinkMu.Unlock()
	rows, err := r.c.Store.DB().Query(`SELECT operation || ' ' || entity_id || ' ' || COALESCE(detail,'') FROM audit_log`)
	if err != nil {
		r.t.Fatal(err)
	}
	var audit strings.Builder
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		audit.WriteString(s + "\n")
	}
	rows.Close()
	for _, c := range codes {
		for name, hay := range map[string]string{"journaux": logs, "réponses d'outils": rsp, "audit": audit.String()} {
			if strings.Contains(hay, c) {
				i := strings.Index(hay, c)
				r.t.Errorf("le code %q apparaît dans %s : …%s…", c, name, hay[max(i-60, 0):min(i+60, len(hay))])
			}
		}
		_ = filepath.Walk(r.dataDir, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && !strings.HasSuffix(p, ".db") && !strings.HasSuffix(p, "-wal") && !strings.HasSuffix(p, "-shm") {
				if b, _ := os.ReadFile(p); strings.Contains(string(b), c) {
					r.t.Errorf("code en clair dans %s", p)
				}
			}
			return nil
		})
	}
}

// humanSetLaunchCode : l'humain ouvre /setup/launch-code dans son navigateur enregistré et définit son code.
func (r *rig) humanSetLaunchCode(code string) {
	r.t.Helper()
	page := r.web.URL + "/setup/launch-code"
	st, body, nonce := r.humanGet(page)
	if st != 200 || nonce == "" {
		r.t.Fatalf("page du code de lancement : %d %s", st, body)
	}
	if st, body := r.humanPost(page, url.Values{"nonce": {nonce}, "action": {"set"}, "code": {code}, "code2": {code}}); st != 200 || !strings.Contains(body, "Code enregistré") {
		r.t.Fatalf("définition du code : %d %s", st, body)
	}
}

func (r *rig) humanLaunchAction(action string) string {
	r.t.Helper()
	page := r.web.URL + "/setup/launch-code"
	_, _, nonce := r.humanGet(page)
	_, body := r.humanPost(page, url.Values{"nonce": {nonce}, "action": {action}})
	return body
}

// printReady : imprimante simulée, dépôt fait, confirmation demandée. Renvoie (print_job_id, confirmation).
func (r *rig) printReady(key string) (string, map[string]any) {
	r.t.Helper()
	job, _ := r.uploadAndAsk(r.fakeArtifact(), key)
	return job, r.call("request_print_confirmation", map[string]any{"print_job_id": job})
}

func launchRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	r.c.Printing.SetValidator(fakeValidator("0.4"))
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))
	humanauth.LaunchCodesFor(r.c.Store).SetMinDelay(0)
	return r
}

func approveArgs(conf map[string]any, code string, plate bool) map[string]any {
	return map[string]any{"confirmation_id": str(conf, "confirmation_id"), "code": code, "plate_clear": plate}
}

const goodCode = "482916"

func TestLaunchCodeApprovesPrintFromAppTool(t *testing.T) {
	r := launchRig(t)
	job, cf := r.printReady("cle-depot-code-approve")
	confID := str(cf, "confirmation_id")

	// Sans code défini : l'outil ne peut rien, la vue le sait (lien navigateur).
	if m := r.call("approve_print_with_code", approveArgs(cf, goodCode, true)); m["approved"] != false || str(m, "problem") != "not_set" {
		t.Fatalf("sans code défini : %v", m)
	}
	vs := r.call("get_launch_view_state", map[string]any{"confirmation_id": confID})
	if vs["code_set"] != false || vs["approved"] != false || !strings.HasSuffix(str(vs, "setup_url"), "/setup/launch-code") {
		t.Fatalf("état sans code : %v", vs)
	}

	r.humanSetLaunchCode(goodCode)
	vs = r.call("get_launch_view_state", map[string]any{"confirmation_id": confID, "with_photo": true})
	if vs["code_set"] != true || vs["locked"] != false || vs["attempts_left"].(float64) != 3 || vs["photo"] != nil || str(vs, "photo_note") == "" {
		t.Fatalf("état avec code : %v", vs)
	}

	// L'attestation « plateau vide » reste obligatoire, même avec le bon code (qui n'est alors pas consommé).
	if txt := r.callErr("approve_print_with_code", approveArgs(cf, goodCode, false)); !strings.Contains(txt, "plateau") {
		t.Fatalf("message : %s", txt)
	}
	// Une confirmation inconnue est refusée.
	if r.callErr("approve_print_with_code", map[string]any{"confirmation_id": "cf_inconnue", "code": goodCode, "plate_clear": true}) == "" {
		t.Fatal("confirmation inconnue approuvée")
	}
	// Tant que rien n'est approuvé, start_print refuse.
	if r.callErr("start_print", map[string]any{"print_job_id": job, "confirmation_id": confID, "idempotency_key": "cle-start-code-00"}) == "" || len(r.sim.StartCommands()) != 0 {
		t.Fatal("start_print sans accord")
	}

	// Deux mauvais codes, puis le bon : approuvé.
	if m := r.call("approve_print_with_code", approveArgs(cf, "135791", true)); m["approved"] != false || str(m, "problem") != "bad_code" || m["attempts_left"].(float64) != 2 {
		t.Fatalf("mauvais code 1 : %v", m)
	}
	m := r.call("approve_print_with_code", approveArgs(cf, goodCode, true))
	if m["approved"] != true || m["started"] != true || str(m, "outcome") != "printing" || len(r.sim.StartCommands()) != 1 { // le code approuve ET lance (sans start_print)
		t.Fatalf("bon code : %v", m)
	}
	if v := r.call("get_print_confirmation_view", map[string]any{"confirmation_id": confID}); v["approved"] != true {
		t.Fatalf("la confirmation n'est pas approuvée : %v", v)
	}
	// Le compteur d'échecs est remis à zéro par la réussite.
	if vs := r.call("get_launch_view_state", map[string]any{"confirmation_id": confID}); vs["attempts_left"].(float64) != 3 || vs["approved"] != true {
		t.Fatalf("état après réussite : %v", vs)
	}
	// start_print du modèle ensuite : constat « déjà lancé », aucune seconde commande ; la confirmation est consommée.
	s := r.call("start_print", map[string]any{"print_job_id": job, "confirmation_id": confID, "idempotency_key": "cle-start-code-01"})
	if str(s, "outcome") != "printing" || s["replayed"] != true || len(r.sim.StartCommands()) != 1 {
		t.Fatalf("lancement : %v", s)
	}
	// La confirmation consommée ne se ré-approuve pas.
	if r.callErr("approve_print_with_code", approveArgs(cf, goodCode, true)) == "" {
		t.Fatal("confirmation consommée ré-approuvée")
	}
	r.assertNoLeak(goodCode, "135791")
}

func TestLaunchCodeThreeWrongCodesLockEverything(t *testing.T) {
	r := launchRig(t)
	r.humanSetLaunchCode(goodCode)
	job, cf := r.printReady("cle-depot-code-lock")
	confID := str(cf, "confirmation_id")

	var last map[string]any
	for i, c := range []string{"135791", "246801", "975310"} {
		last = r.call("approve_print_with_code", approveArgs(cf, c, true))
		if last["approved"] != false || str(last, "problem") != "bad_code" || last["attempts_left"].(float64) != float64(2-i) {
			t.Fatalf("essai %d : %v", i+1, last)
		}
	}
	if last["locked"] != true {
		t.Fatalf("pas verrouillé après 3 échecs : %v", last)
	}
	// Verrouillé : le BON code est refusé, pour cette confirmation comme pour une nouvelle.
	if m := r.call("approve_print_with_code", approveArgs(cf, goodCode, true)); m["approved"] != false || str(m, "problem") != "locked" {
		t.Fatalf("bon code sous verrou : %v", m)
	}
	if v := r.call("get_print_confirmation_view", map[string]any{"confirmation_id": confID}); v["approved"] != false {
		t.Fatalf("approuvé sous verrou : %v", v)
	}
	if vs := r.call("get_launch_view_state", map[string]any{"confirmation_id": confID}); vs["locked"] != true || vs["code_set"] != true {
		t.Fatalf("état verrouillé : %v", vs)
	}
	// Le verrou résiste à une nouvelle confirmation et à un redémarrage du compteur mémoire.
	cf2 := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	if m := r.call("approve_print_with_code", approveArgs(cf2, goodCode, true)); m["approved"] != false || str(m, "problem") != "locked" {
		t.Fatalf("nouvelle confirmation sous verrou : %v", m)
	}
	// Le lien navigateur reste disponible sous verrou (l'humain peut toujours approuver sur la page : son clic lance
	// l'impression, ce qui est vérifié dans TestEndToEndChainWithSimulator ; ici on garde la confirmation pour la suite).
	if st, body, nonce := r.humanGet(str(cf2, "confirm_url")); st != 200 || nonce == "" {
		t.Fatalf("lien navigateur sous verrou : %d %s", st, body)
	}
	// Seuls la page humaine ou la CLI déverrouillent : un outil MCP n'en a pas.
	cf3 := cf2
	if body := r.humanLaunchAction("unlock"); !strings.Contains(body, "Verrou levé") {
		t.Fatalf("déverrouillage : %s", body)
	}
	if m := r.call("approve_print_with_code", approveArgs(cf3, goodCode, true)); m["approved"] != true {
		t.Fatalf("bon code après déverrouillage humain : %v", m)
	}
	r.assertNoLeak(goodCode, "135791", "246801", "975310")
}

func TestLaunchCodeAttemptsAreBoundToTheConfirmation(t *testing.T) {
	r := launchRig(t)
	r.humanSetLaunchCode(goodCode)
	job, cf := r.printReady("cle-depot-code-bound")
	// Nouvelle confirmation : l'ancienne ne vaut plus rien, même avec le bon code.
	cf2 := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	if txt := r.callErr("approve_print_with_code", approveArgs(cf, goodCode, true)); txt == "" {
		t.Fatal("ancienne confirmation approuvée")
	}
	// Une confirmation refusée n'a pas consommé d'essai de code.
	if vs := r.call("get_launch_view_state", map[string]any{"confirmation_id": str(cf2, "confirmation_id")}); vs["attempts_left"].(float64) != 3 {
		t.Fatalf("essai consommé par une confirmation périmée : %v", vs)
	}
	// La liaison (matières) change après la demande : refus, même avec le bon code.
	if r.callErr("get_launch_view_state", map[string]any{"confirmation_id": "cf_x"}) == "" {
		t.Fatal("état d'une confirmation inconnue")
	}
	if m := r.call("approve_print_with_code", approveArgs(cf2, goodCode, true)); m["approved"] != true {
		t.Fatalf("confirmation courante : %v", m)
	}
}

func TestLaunchCodeToolsAreAppOnlyAndWarnTheModel(t *testing.T) {
	r := launchRig(t)
	tools, err := r.cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, tl := range tools.Tools {
		switch tl.Name {
		case "approve_print_with_code", "approve_stop_with_code", "get_launch_view_state", "get_stop_view_state":
			seen++
			meta, _ := json.Marshal(tl.Meta)
			if !strings.Contains(string(meta), `"visibility":["app"]`) {
				t.Errorf("%s n'est pas réservé à l'application : %s", tl.Name, meta)
			}
		}
		if strings.HasPrefix(tl.Name, "approve_") {
			for _, w := range []string{"JAMAIS demander", "dans la conversation", "verrouill"} {
				if !strings.Contains(tl.Description, w) {
					t.Errorf("%s : la description ne dit pas %q : %s", tl.Name, w, tl.Description)
				}
			}
		}
		if tl.Name == "request_print_confirmation" && !strings.Contains(tl.Description, "JAMAIS") {
			t.Errorf("request_print_confirmation ne met pas en garde : %s", tl.Description)
		}
	}
	if seen != 4 {
		t.Fatalf("%d outils du code de lancement vus sur 4", seen)
	}
}

func TestLaunchCodeNeverLeaks(t *testing.T) {
	r := launchRig(t)
	r.humanSetLaunchCode(goodCode)
	job, cf := r.printReady("cle-depot-code-leak")
	// Entrées de toutes formes, y compris invalides (type erroné, espaces, trop long, injection) : jamais d'écho du code.
	secret := "907351"
	for _, args := range []map[string]any{
		{"confirmation_id": str(cf, "confirmation_id"), "code": 907351, "plate_clear": true},
		{"confirmation_id": str(cf, "confirmation_id"), "code": []any{secret}, "plate_clear": true},
		{"confirmation_id": str(cf, "confirmation_id"), "code": " " + secret + " ", "plate_clear": true},
		{"confirmation_id": str(cf, "confirmation_id"), "code": secret + secret, "plate_clear": true},
		{"confirmation_id": str(cf, "confirmation_id"), "code": secret, "plate_clear": "oui"},
		{"confirmation_id": secret, "code": secret, "plate_clear": true},
		{"confirmation_id": str(cf, "confirmation_id"), "code": secret, "plate_clear": true, "extra": secret},
		{"stop_id": secret, "code": secret},
	} {
		_, _, _ = r.raw("approve_print_with_code", args)
		_, _, _ = r.raw("approve_stop_with_code", args)
		_, _, _ = r.raw("get_launch_view_state", args)
	}
	// Un code mal formé ne compte pas ; un code bien formé et faux compte (et n'est pas écrit).
	vs := r.call("get_launch_view_state", map[string]any{"confirmation_id": str(cf, "confirmation_id")})
	if vs["attempts_left"].(float64) < 2 {
		t.Fatalf("les entrées mal formées ont consommé des essais : %v", vs)
	}
	_, _, _ = r.raw("approve_print_with_code", approveArgs(cf, secret, true))
	_ = job
	r.assertNoLeak(secret, goodCode)
	// Rien dans la base ne contient le code en clair (seule l'empreinte argon2id).
	var h string
	if err := r.c.Store.DB().QueryRow(`SELECT code_hash FROM launch_code`).Scan(&h); err != nil || strings.Contains(h, goodCode) || !strings.HasPrefix(h, "argon2id$") {
		t.Fatalf("empreinte : %q %v", h, err)
	}
}

func TestLaunchCodeStopFromApp(t *testing.T) {
	r := launchRig(t)
	r.humanSetLaunchCode(goodCode)
	job, cf := r.printReady("cle-depot-code-stop")
	if m := r.call("approve_print_with_code", approveArgs(cf, goodCode, true)); m["approved"] != true {
		t.Fatalf("approbation : %v", m)
	}
	if s := r.call("start_print", map[string]any{"print_job_id": job, "confirmation_id": str(cf, "confirmation_id"), "idempotency_key": "cle-start-code-stop"}); str(s, "outcome") != "printing" {
		t.Fatalf("lancement : %v", s)
	}
	c := r.call("cancel_print", map[string]any{"print_job_id": job})
	stopURL := str(c, "stop_url")
	stopID := stopURL[strings.Index(stopURL, "stp_"):]
	if str(c, "outcome") != "stop_requested" {
		t.Fatalf("demande d'arrêt : %v", c)
	}
	// La vue de l'arrêt lit l'état (sans secret).
	ss := r.call("get_stop_view_state", map[string]any{"stop_id": stopID})
	if ss["code_set"] != true || str(ss, "stop", "print_job_id") != job {
		t.Fatalf("état de l'arrêt : %v", ss)
	}
	// Mauvais code : l'impression continue.
	if m := r.call("approve_stop_with_code", map[string]any{"stop_id": stopID, "code": "135791"}); m["approved"] != false || str(m, "problem") != "bad_code" {
		t.Fatalf("mauvais code : %v", m)
	}
	if r.printState(job) != "PRINTING" {
		t.Fatal("arrêt avec un mauvais code")
	}
	// Demande d'arrêt forgée : refus.
	if r.callErr("approve_stop_with_code", map[string]any{"stop_id": "stp_forge", "code": goodCode}) == "" {
		t.Fatal("arrêt forgé")
	}
	// Bon code : arrêtée.
	m := r.call("approve_stop_with_code", map[string]any{"stop_id": stopID, "code": goodCode})
	if m["approved"] != true {
		t.Fatalf("bon code : %v", m)
	}
	if r.printState(job) != "CANCELLED" || r.sim.GcodeState() != "FAILED" {
		t.Fatalf("état après arrêt : %s / %s", r.printState(job), r.sim.GcodeState())
	}
	// Usage unique : la demande est consommée.
	if r.callErr("approve_stop_with_code", map[string]any{"stop_id": stopID, "code": goodCode}) == "" {
		t.Fatal("demande d'arrêt réutilisée")
	}
	r.assertNoLeak(goodCode, "135791")
}

func TestLaunchCodeStopLockAndFallbackToBrowserPage(t *testing.T) {
	r := launchRig(t)
	r.humanSetLaunchCode(goodCode)
	job, cf := r.printReady("cle-depot-code-stop2")
	_ = r.call("approve_print_with_code", approveArgs(cf, goodCode, true))
	_ = r.call("start_print", map[string]any{"print_job_id": job, "confirmation_id": str(cf, "confirmation_id"), "idempotency_key": "cle-start-code-st2"})
	c := r.call("cancel_print", map[string]any{"print_job_id": job})
	stopURL := str(c, "stop_url")
	stopID := stopURL[strings.Index(stopURL, "stp_"):]
	for _, w := range []string{"135791", "246801", "975310"} {
		_ = r.call("approve_stop_with_code", map[string]any{"stop_id": stopID, "code": w})
	}
	if m := r.call("approve_stop_with_code", map[string]any{"stop_id": stopID, "code": goodCode}); m["approved"] != false || str(m, "problem") != "locked" {
		t.Fatalf("verrou global : %v", m)
	}
	if r.printState(job) != "PRINTING" {
		t.Fatal("arrêt sous verrou")
	}
	// La page /stop du navigateur enregistré fonctionne toujours.
	_, _, nonce := r.humanGet(stopURL)
	if st, body := r.humanPost(stopURL, url.Values{"nonce": {nonce}}); st != 200 {
		t.Fatalf("page /stop : %d %s", st, body)
	}
	if r.printState(job) != "CANCELLED" {
		t.Fatal("la page /stop n'a pas arrêté")
	}
}

func TestLaunchCodeSurvivesRestartAndCLIReset(t *testing.T) {
	r := launchRig(t)
	r.humanSetLaunchCode(goodCode)
	_, cf := r.printReady("cle-depot-code-restart")
	for _, w := range []string{"135791", "246801", "975310"} {
		_ = r.call("approve_print_with_code", approveArgs(cf, w, true))
	}
	// Le verrou est dans la base : une autre instance (processus de la CLI) le voit et le lève.
	lc := humanauth.LaunchCodesFor(r.c.Store)
	st, _ := lc.Status(context.Background())
	if !st.Locked {
		t.Fatalf("verrou non durable : %+v", st)
	}
	if err := lc.Revoke(context.Background(), "human:cli"); err != nil {
		t.Fatal(err)
	}
	if m := r.call("approve_print_with_code", approveArgs(cf, goodCode, true)); str(m, "problem") != "not_set" {
		t.Fatalf("après --reset : %v", m)
	}
	// Le lien navigateur marche toujours (comportement actuel).
	if st, body := r.humanConfirm(str(cf, "confirm_url"), true); st != 200 {
		t.Fatalf("lien navigateur : %d %s", st, body)
	}
}

func TestLaunchCodeMinimumDelayThroughTool(t *testing.T) {
	r := launchRig(t)
	r.humanSetLaunchCode(goodCode)
	humanauth.LaunchCodesFor(r.c.Store).SetMinDelay(time.Hour)
	_, cf := r.printReady("cle-depot-code-delay")
	_ = r.call("approve_print_with_code", approveArgs(cf, "135791", true))
	if m := r.call("approve_print_with_code", approveArgs(cf, goodCode, true)); str(m, "problem") != "too_fast" || m["approved"] != false {
		t.Fatalf("délai minimal : %v", m)
	}
}
