package printing_test

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

func stlBox(x, y, z float64) ([]byte, error) { return mesh.STLBytes(mesh.Box(0, 0, 0, x, y, z)) }

func exists(r *rig, name string) bool {
	_, err := r.sim.FS().Stat("/" + name)
	if err != nil {
		_, err = r.sim.FS().Stat(name)
	}
	return err == nil
}

// uploadAndAsk dépose et renvoie (print_job_id, question).
func (r *rig) uploadAndAsk(artifact, key string) (string, map[string]any) {
	r.t.Helper()
	o := r.call("upload_print_artifact", map[string]any{"artifact_id": artifact, "printer_id": r.printerID, "idempotency_key": key,
		"plate": 1, "assignments": anyAssign(ams1)})
	if str(o, "state") != "AWAITING_PRINT_CONFIRMATION" {
		r.t.Fatalf("état après dépôt : %s (%v)", str(o, "state"), o["note"])
	}
	return str(o, "print_job_id"), o
}

func TestEndToEndChainWithSimulator(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe") // sans Studio : artefact factice + validateur substitué
	r.sim = startSim(t)
	r.seedPrinterModel()
	r.c.Printing.SetValidator(fakeValidator("0.4"))

	// ---- 1. registre : le code d'accès n'est pas une entrée d'outil ----
	tools, err := r.cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name == "add_printer" {
			raw := fmt.Sprint(tl.InputSchema)
			if strings.Contains(strings.ToLower(raw), "access") || strings.Contains(strings.ToLower(raw), "password") {
				t.Fatalf("add_printer ne doit pas accepter de code d'accès : %s", raw)
			}
		}
	}
	if txt := r.callErr("add_printer", map[string]any{"name": "X", "model_id": "inconnu", "host": "127.0.0.1", "serial": "01P00A000000001"}); !strings.Contains(txt, "modèle") {
		t.Fatalf("modèle inconnu : %s", txt)
	}
	if r.callErr("add_printer", map[string]any{"name": "X", "model_id": "p1s", "host": "127.0.0.1", "serial": "!!"}) == "" {
		t.Fatal("série invalide")
	}
	if r.callErr("add_printer", map[string]any{"name": "X", "model_id": "p1s", "host": "127.0.0.1", "serial": "01P00A000000001", "nozzle_mm": 0.9}) == "" {
		t.Fatal("buse non prévue")
	}
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	setup := str(ap, "setup_url")
	if !strings.HasSuffix(setup, "/setup/printer/"+r.printerID) || ap["printer"].(map[string]any)["access_code_set"] != false {
		t.Fatalf("ajout : %v", ap)
	}

	// Sans code : état hors ligne explicite, pas de panique.
	st := r.call("get_printer_status", map[string]any{"printer_id": r.printerID})
	if str(st, "state") != "offline" || !strings.Contains(str(st, "error"), "setup") {
		t.Fatalf("statut sans code : %v", st)
	}
	// L'humain saisit le code sur la page locale.
	r.humanSetsAccessCode(setup)
	lp := r.call("list_printers", nil)
	if lp["printers"].([]any)[0].(map[string]any)["access_code_set"] != true {
		t.Fatalf("list_printers : %v", lp)
	}
	// Le code n'est dans aucune réponse d'outil ni aucun fichier de données.
	for _, v := range []any{lp, st, ap} {
		if strings.Contains(fmt.Sprint(v), simAccessCode) {
			t.Fatalf("code d'accès dans une réponse : %v", v)
		}
	}
	filepath.Walk(r.dataDir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), simAccessCode) {
				t.Errorf("code d'accès en clair dans %s", p)
			}
		}
		return nil
	})

	// ---- 2. état + fraîcheur ----
	r.waitPrinterState(r.printerID, "idle")
	ps := r.call("get_printer_status", map[string]any{"printer_id": r.printerID})
	if str(ps, "state") != "idle" || ps["age_seconds"] == nil || ps["stale"] != false || str(ps, "plate_empty") != "inconnu" {
		t.Fatalf("statut : %v", ps)
	}

	// ---- 3. validation (artefact factice) + compatibilité réelle ----
	art := r.fakeArtifact()
	v := r.call("validate_print_artifact", map[string]any{"artifact_id": art, "printer_id": r.printerID})
	if v["report"].(map[string]any)["ok"] != true || str(v, "state") != "VALIDATED" {
		t.Fatalf("validation : %v", v)
	}
	// Autre imprimante déclarée avec une buse 0,6 : le même fichier est incompatible.
	p2 := r.addPrinter(map[string]any{"name": "Buse 0.6", "nozzle_mm": 0.6})
	v2 := r.call("validate_print_artifact", map[string]any{"artifact_id": art, "printer_id": str(p2, "printer", "id")})
	if v2["report"].(map[string]any)["ok"] != false || !strings.Contains(fmt.Sprint(v2["report"].(map[string]any)["blocking"]), "nozzle") {
		t.Fatalf("buse 0,6 : incompatibilité attendue : %v", v2["report"])
	}
	if txt := r.callErr("upload_print_artifact", map[string]any{"artifact_id": art, "printer_id": str(p2, "printer", "id"), "idempotency_key": "cle-buse-06-xxxx", "assignments": anyAssign(ams1)}); !strings.Contains(txt, "nozzle") {
		t.Fatalf("dépôt sur une imprimante incompatible : %s", txt)
	}

	// ---- 4. dépôt : idempotency_key et matières obligatoires ----
	if r.callErr("upload_print_artifact", map[string]any{"artifact_id": art, "printer_id": r.printerID, "assignments": anyAssign(ams1)}) == "" {
		t.Fatal("sans idempotency_key")
	}
	if r.callErr("upload_print_artifact", map[string]any{"artifact_id": art, "printer_id": r.printerID, "idempotency_key": "cle-sans-matieres"}) == "" {
		t.Fatal("sans matières")
	}
	bad := []map[string]any{{"filament_slot": 1, "tray": "ams3:1", "material_id": "PLA"}}
	if txt := r.callErr("upload_print_artifact", map[string]any{"artifact_id": art, "printer_id": r.printerID, "idempotency_key": "cle-ams-absent-1", "assignments": anyAssign(bad)}); !strings.Contains(txt, "AMS") {
		t.Fatalf("AMS non déclaré : %s", txt)
	}
	jobID, up := r.uploadAndAsk(art, "cle-depot-premiere-fois")
	name := str(up, "job", "name")
	wantQ := fmt.Sprintf("Le fichier %s est envoyé sur la P1S. Veux-tu lancer cette impression ?", name)
	if str(up, "question") != wantQ {
		t.Fatalf("question :\n%q\nattendue :\n%q", str(up, "question"), wantQ)
	}
	d := up["details"].(map[string]any)
	if d["sha256"] != art || d["plate"].(float64) != 1 || d["printer_name"] != "Atelier" || !strings.Contains(fmt.Sprint(d["reminder"]), "plateau est vide") {
		t.Fatalf("détails : %v", d)
	}
	// L'ENVOI NE DÉCLENCHE RIEN : aucune commande project_file reçue, le fichier est sur la carte.
	if n := len(r.sim.StartCommands()); n != 0 {
		t.Fatalf("%d commande(s) project_file après un simple dépôt", n)
	}
	if g := r.sim.GcodeState(); g != "IDLE" {
		t.Fatalf("l'imprimante a bougé après le dépôt : %s", g)
	}
	remote := str(up, "job", "remote", "name")
	if remote == "" || !exists(r, remote) {
		t.Fatalf("fichier distant absent : %q", remote)
	}
	// Rejeu de la même clé : rien n'est renvoyé.
	up2 := r.call("upload_print_artifact", map[string]any{"artifact_id": art, "printer_id": r.printerID, "idempotency_key": "cle-depot-premiere-fois", "assignments": anyAssign(ams1)})
	if up2["replayed"] != true || str(up2, "print_job_id") != jobID {
		t.Fatalf("rejeu : %v", up2)
	}

	// ---- 5. lancement refusé sans confirmation ----
	if r.callErr("start_print", map[string]any{"print_job_id": jobID, "confirmation_id": "cf_inconnue", "idempotency_key": "cle-start-sans-conf"}) == "" {
		t.Fatal("start_print sans confirmation valide")
	}
	if len(r.sim.StartCommands()) != 0 {
		t.Fatal("commande envoyée sans confirmation")
	}
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": jobID})
	confID, confURL := str(cf, "confirmation_id"), str(cf, "confirm_url")
	if confID == "" || !strings.HasSuffix(confURL, "/confirm/"+confID) || cf["approved"] != false {
		t.Fatalf("confirmation : %v", cf)
	}
	if b := cf["binding"].(map[string]any); b["sha256"] != art || b["plate"].(float64) != 1 || b["file"] != name {
		t.Fatalf("liaison affichée : %v", b)
	}
	if strings.Contains(strings.ToLower(fmt.Sprint(cf)), "token") {
		t.Fatalf("le jeton ne doit pas être rendu au modèle : %v", cf)
	}
	// Pas encore approuvée par l'humain : refus, rien d'envoyé.
	if txt := r.callErr("start_print", map[string]any{"print_job_id": jobID, "confirmation_id": confID, "idempotency_key": "cle-start-non-approuve"}); !strings.Contains(txt, "confirm") {
		t.Fatalf("approbation manquante : %s", txt)
	}
	// Aucun outil MCP ne confirme : l'ancien outil d'application n'existe plus.
	if txt := r.callErr("confirm_print_from_app", map[string]any{"confirmation_id": confID, "plate_clear": true}); txt == "" {
		t.Fatal("confirm_print_from_app ne doit plus exister")
	}
	// Formulaire forgé sans nonce : refusé. Sans case « plateau vide » : refusé.
	if st, _ := r.humanPost(confURL, url.Values{"plate_clear": {"yes"}}); st != 403 {
		t.Fatalf("POST sans nonce : %d", st)
	}
	if st, _ := r.humanConfirm(confURL, false); st != 400 {
		t.Fatalf("sans plateau vide : %d", st)
	}
	if txt := r.callErr("start_print", map[string]any{"print_job_id": jobID, "confirmation_id": confID, "idempotency_key": "cle-start-encore-non"}); txt == "" {
		t.Fatal("start_print avant approbation")
	}
	if len(r.sim.StartCommands()) != 0 {
		t.Fatal("commande envoyée avant approbation")
	}
	// La page affiche ce qui est approuvé.
	if st, body, _ := r.humanGet(confURL); st != 200 || !strings.Contains(body, art) || !strings.Contains(body, name) || !strings.Contains(body, "plateau") {
		t.Fatalf("page de confirmation : %d", st)
	}
	// Humain : coche « plateau vide » et lance.
	if st, body := r.humanConfirm(confURL, true); st != 200 || !strings.Contains(body, "cette impression uniquement") {
		t.Fatalf("approbation : %d %s", st, body)
	}

	// ---- 6. lancement : START_REQUESTED -> PRINTING observé ----
	s1 := r.call("start_print", map[string]any{"print_job_id": jobID, "confirmation_id": confID, "idempotency_key": "cle-start-impression-1"})
	if str(s1, "outcome") != "printing" || str(s1, "state") != "PRINTING" {
		t.Fatalf("lancement : %v", s1)
	}
	if n := len(r.sim.StartCommands()); n != 1 {
		t.Fatalf("%d commande(s) project_file, 1 attendue", n)
	}
	pl := r.sim.StartCommands()[0].Payload
	if pl["command"] != "project_file" || pl["param"] != "Metadata/plate_1.gcode" || pl["use_ams"] != true || fmt.Sprint(pl["ams_mapping"]) != "[1 -1 -1 -1 -1]" {
		t.Fatalf("project_file : %v", pl)
	}
	// Rejeu de la requête : aucune nouvelle commande.
	s1b := r.call("start_print", map[string]any{"print_job_id": jobID, "confirmation_id": confID, "idempotency_key": "cle-start-impression-1"})
	if s1b["replayed"] != true || len(r.sim.StartCommands()) != 1 {
		t.Fatalf("rejeu du lancement : %v (%d)", s1b, len(r.sim.StartCommands()))
	}
	// Nouvelle clé sur la même impression, confirmation consommée : refus.
	if r.callErr("start_print", map[string]any{"print_job_id": jobID, "confirmation_id": confID, "idempotency_key": "cle-start-nouvelle-cle"}) == "" || len(r.sim.StartCommands()) != 1 {
		t.Fatal("relance avec une nouvelle clé")
	}
	// Une autre impression ne peut pas démarrer pendant que celle-ci occupe l'imprimante.
	waitState := r.waitState(jobID, 30*time.Second, "COMPLETED", "FAILED")
	if waitState != "COMPLETED" {
		t.Fatalf("fin d'impression : %s", waitState)
	}
	fin := r.call("get_print_status", map[string]any{"print_job_id": jobID})
	if pg := fin["job"].(map[string]any)["print"].(map[string]any); pg["percent"].(float64) != 100 || pg["matches_file"] != true {
		t.Fatalf("progression finale : %v", pg)
	}

	// ---- 7. deuxième impression : l'ancienne confirmation ne vaut pas ----
	job2, _ := r.uploadAndAsk(art, "cle-depot-deuxieme-imp")
	if job2 == jobID {
		t.Fatal("une nouvelle impression doit avoir son propre identifiant")
	}
	if txt := r.callErr("start_print", map[string]any{"print_job_id": job2, "confirmation_id": confID, "idempotency_key": "cle-start-ancienne-conf"}); txt == "" {
		t.Fatal("l'ancienne confirmation ne doit pas valoir pour la 2e impression")
	}
	cf2 := r.call("request_print_confirmation", map[string]any{"print_job_id": job2})
	conf2 := str(cf2, "confirmation_id")
	if conf2 == confID {
		t.Fatal("confirmation réutilisée")
	}
	if r.callErr("start_print", map[string]any{"print_job_id": job2, "confirmation_id": conf2, "idempotency_key": "cle-start-2-non-approuve"}) == "" {
		t.Fatal("2e impression sans approbation humaine")
	}
	// L'ancienne page ne peut plus être validée (confirmation utilisée).
	if st, body := r.humanPost(confURL, url.Values{"nonce": {"x"}, "plate_clear": {"yes"}}); st == 200 {
		t.Fatalf("ancienne page validée : %s", body)
	}
	if st, _, _ := r.humanGet(confURL); st != 410 {
		t.Fatalf("ancienne confirmation : page %d, 410 attendu", st)
	}
	if n := len(r.sim.StartCommands()); n != 1 {
		t.Fatalf("%d commandes", n)
	}
	if st, _ := r.humanConfirm(str(cf2, "confirm_url"), true); st != 200 {
		t.Fatalf("approbation 2 : %d", st)
	}
	s2 := r.call("start_print", map[string]any{"print_job_id": job2, "confirmation_id": conf2, "idempotency_key": "cle-start-impression-2"})
	if str(s2, "outcome") != "printing" || len(r.sim.StartCommands()) != 2 {
		t.Fatalf("2e lancement : %v", s2)
	}
	if str(r.call("upload_print_artifact", map[string]any{"artifact_id": art, "printer_id": r.printerID, "idempotency_key": "cle-depot-deuxieme-imp", "assignments": anyAssign(ams1)}), "print_job_id") != job2 {
		t.Fatal("rejeu du dépôt")
	}
	r.waitState(job2, 20*time.Second, "COMPLETED")

	// ---- 8a. départ depuis FINISH + liaison coupée : jamais de renvoi, état réel vérifié ----
	job3, _ := r.uploadAndAsk(art, "cle-depot-troisieme-imp")
	cf3 := r.call("request_print_confirmation", map[string]any{"print_job_id": job3})
	if st, _ := r.humanConfirm(str(cf3, "confirm_url"), true); st != 200 {
		t.Fatal("approbation 3")
	}
	if err := r.sim.SetFault("drop_after_start", true); err != nil {
		t.Fatal(err)
	}
	s3 := r.call("start_print", map[string]any{"print_job_id": job3, "confirmation_id": str(cf3, "confirmation_id"), "idempotency_key": "cle-start-impression-3"})
	if n := len(r.sim.StartCommands()); n != 3 {
		t.Fatalf("%d commandes, 3 attendues (une par impression)", n)
	}
	if str(s3, "outcome") == "start_unknown" {
		r.call("reconcile_print", map[string]any{"print_job_id": job3})
	}
	r.waitState(job3, 30*time.Second, "COMPLETED")
	if len(r.sim.StartCommands()) != 3 {
		t.Fatal("renvoi de commande")
	}

	// ---- 8b. perte réseau après le lancement depuis IDLE : START_UNKNOWN -> reconcile, jamais de relance ----
	sim2 := startSim(t, "drop_after_start")
	ap2 := r.addPrinterFor(sim2, map[string]any{"name": "Atelier 2", "allow_start_from_finish": false})
	pid2 := str(ap2, "printer", "id")
	r.humanSetsAccessCode(str(ap2, "setup_url"))
	o8 := r.call("upload_print_artifact", map[string]any{"artifact_id": art, "printer_id": pid2, "idempotency_key": "cle-depot-imprimante-2", "plate": 1, "assignments": anyAssign(ams1)})
	job8 := str(o8, "print_job_id")
	cf8 := r.call("request_print_confirmation", map[string]any{"print_job_id": job8})
	if st, _ := r.humanConfirm(str(cf8, "confirm_url"), true); st != 200 {
		t.Fatal("approbation 8")
	}
	s8 := r.call("start_print", map[string]any{"print_job_id": job8, "confirmation_id": str(cf8, "confirmation_id"), "idempotency_key": "cle-start-imprimante-2"})
	if str(s8, "outcome") != "start_unknown" || str(s8, "state") != "START_UNKNOWN" {
		t.Fatalf("lancement incertain : outcome=%v state=%v note=%v last_error=%v", s8["outcome"], s8["state"], s8["note"], s8["job"].(map[string]any)["last_error"])
	}
	if n := len(sim2.StartCommands()); n != 1 {
		t.Fatalf("%d commande(s) reçue(s) par le simulateur 2, 1 attendue", n)
	}
	// Ni relance avec une autre clé, ni annulation à l'aveugle, ni nouvelle confirmation en attendant.
	if r.callErr("start_print", map[string]any{"print_job_id": job8, "confirmation_id": str(cf8, "confirmation_id"), "idempotency_key": "cle-start-relance-auto"}) == "" || len(sim2.StartCommands()) != 1 {
		t.Fatal("relance après START_UNKNOWN")
	}
	if r.callErr("request_print_confirmation", map[string]any{"print_job_id": job8}) == "" {
		t.Fatal("nouvelle confirmation pendant START_UNKNOWN")
	}
	if c := r.call("cancel_print", map[string]any{"print_job_id": job8}); str(c, "outcome") != "refused" {
		t.Fatalf("annulation d'un lancement incertain : %v", c)
	}
	rc := r.call("reconcile_print", map[string]any{"print_job_id": job8})
	if str(rc, "outcome") != "started" || str(rc, "state") != "PRINTING" {
		t.Fatalf("réconciliation : outcome=%v reason=%v state=%v note=%v", rc["outcome"], rc["reason"], rc["state"], rc["note"])
	}
	if len(sim2.StartCommands()) != 1 {
		t.Fatal("la réconciliation a envoyé une commande")
	}
	r.waitState(job8, 30*time.Second, "COMPLETED")
	if r.callErr("reconcile_print", map[string]any{"print_job_id": job8}) == "" {
		t.Fatal("réconciliation hors START_UNKNOWN")
	}

	// ---- 8c. réconciliation PASSIVE : START_UNKNOWN -> PRINTING par simple lecture, sans reconcile_print ni renvoi ----
	sim3 := startSim(t, "drop_after_start")
	ap3 := r.addPrinterFor(sim3, map[string]any{"name": "Atelier 3"})
	pid3 := str(ap3, "printer", "id")
	r.humanSetsAccessCode(str(ap3, "setup_url"))
	o9 := r.call("upload_print_artifact", map[string]any{"artifact_id": art, "printer_id": pid3, "idempotency_key": "cle-depot-imprimante-3", "plate": 1, "assignments": anyAssign(ams1)})
	job9 := str(o9, "print_job_id")
	cf9 := r.call("request_print_confirmation", map[string]any{"print_job_id": job9})
	if st, _ := r.humanConfirm(str(cf9, "confirm_url"), true); st != 200 {
		t.Fatal("approbation 9")
	}
	s9 := r.call("start_print", map[string]any{"print_job_id": job9, "confirmation_id": str(cf9, "confirmation_id"), "idempotency_key": "cle-start-imprimante-3"})
	if str(s9, "state") != "START_UNKNOWN" {
		t.Fatalf("lancement incertain attendu : %v", s9["state"])
	}
	waitFor(t, 40*time.Second, "réconciliation passive START_UNKNOWN -> PRINTING", func() bool {
		return str(r.call("get_print_status", map[string]any{"print_job_id": job9}), "job", "state") == "PRINTING" ||
			r.printState(job9) == "PRINTING" || r.printState(job9) == "COMPLETED"
	})
	if n := len(sim3.StartCommands()); n != 1 {
		t.Fatalf("la réconciliation passive a envoyé une commande : %d project_file", n)
	}

	// ---- 9. annulations selon la phase ----
	job4, _ := r.uploadAndAsk(art, "cle-depot-quatrieme-imp")
	cf4 := r.call("request_print_confirmation", map[string]any{"print_job_id": job4})
	c4 := r.call("cancel_print", map[string]any{"print_job_id": job4})
	if str(c4, "outcome") != "cancelled" || str(c4, "state") != "CANCELLED" {
		t.Fatalf("annulation avant lancement : %v", c4)
	}
	if st, _, _ := r.humanGet(str(cf4, "confirm_url")); st != 410 {
		t.Fatalf("confirmation d'une impression annulée : %d", st)
	}
	if r.callErr("start_print", map[string]any{"print_job_id": job4, "confirmation_id": str(cf4, "confirmation_id"), "idempotency_key": "cle-start-annulee-xx"}) == "" {
		t.Fatal("lancement d'une impression annulée")
	}
	job5, _ := r.uploadAndAsk(art, "cle-depot-cinquieme-imp")
	cf5 := r.call("request_print_confirmation", map[string]any{"print_job_id": job5})
	if st, _ := r.humanConfirm(str(cf5, "confirm_url"), true); st != 200 {
		t.Fatal("approbation 5")
	}
	if s := r.call("start_print", map[string]any{"print_job_id": job5, "confirmation_id": str(cf5, "confirmation_id"), "idempotency_key": "cle-start-impression-5"}); str(s, "outcome") != "printing" {
		t.Fatalf("lancement 5 : %v", s)
	}
	// Arrêt : cancel_print ne renvoie AUCUN identifiant exécutable ; il crée une demande et renvoie /stop/{id}.
	c5 := r.call("cancel_print", map[string]any{"print_job_id": job5})
	stopURL := str(c5, "stop_url")
	if str(c5, "outcome") != "stop_requested" || !strings.Contains(stopURL, "/stop/stp_") || str(c5, "question") == "" || c5["stop_confirmation_id"] != nil {
		t.Fatalf("arrêt : demande d'arrêt attendue : %v", c5)
	}
	if r.printState(job5) != "PRINTING" {
		t.Fatal("cancel_print a arrêté sans l'humain")
	}
	// L'ancien paramètre forgé ne fait rien (champ inconnu ou ignoré) ; l'impression continue.
	_, _, _ = r.raw("cancel_print", map[string]any{"print_job_id": job5, "stop_confirmation_id": "stop_forge"})
	if r.printState(job5) != "PRINTING" {
		t.Fatal("arrêt avec un identifiant forgé")
	}
	// Une nouvelle demande réutilise la même page (pas d'accumulation).
	if again := r.call("cancel_print", map[string]any{"print_job_id": job5}); str(again, "stop_url") != stopURL {
		t.Fatalf("demande d'arrêt dupliquée : %v", again)
	}
	// Page vue sans navigateur enregistré : redirection vers /enroll, rien n'est arrêté.
	if res, err := http.Get(stopURL); err != nil || res.Request.URL.Path != "/enroll" {
		t.Fatalf("page /stop sans navigateur enregistré : %v %v", err, res)
	}
	// L'humain ouvre /stop, voit l'impression et valide.
	hst, body, nonce := r.humanGet(stopURL)
	if hst != 200 || nonce == "" || !strings.Contains(body, "Arrêter") || !strings.Contains(body, c5["job"].(map[string]any)["name"].(string)) {
		t.Fatalf("page d'arrêt : %d %s", hst, body)
	}
	if st, _ := r.humanPost(stopURL, url.Values{}); st != 403 {
		t.Fatalf("POST /stop sans nonce : %d", st)
	}
	if st, body := r.humanPost(stopURL, url.Values{"nonce": {nonce}}); st != 200 || !strings.Contains(body, "arrêtée") {
		t.Fatalf("validation de l'arrêt : %d %s", st, body)
	}
	if r.printState(job5) != "CANCELLED" {
		t.Fatalf("état après arrêt humain : %s", r.printState(job5))
	}
	// Point C : l'état « failed » de Bambu après un arrêt humain s'affiche « arrêtée (annulée par vous) » et s'explique.
	waitFor(t, 10*time.Second, "affichage de l'arrêt humain", func() bool {
		ps := r.call("get_printer_status", map[string]any{"printer_id": r.printerID})
		return strings.Contains(str(ps, "display"), "arrêtée (annulée par vous)")
	})
	pst := r.call("get_printer_status", map[string]any{"printer_id": r.printerID})
	if !strings.Contains(str(pst, "explanation"), "FAILED") || !strings.Contains(str(pst, "explanation"), "/stop") || str(pst, "last_job", "print_job_id") != job5 {
		t.Fatalf("explication du failed : %v", pst)
	}
	if g := r.sim.GcodeState(); g != "FAILED" {
		t.Fatalf("le simulateur devrait avoir reçu l'arrêt : %s", g)
	}
	// cancel_job (générique) aiguille aussi une impression.
	if c := r.call("cancel_job", map[string]any{"job_id": job8}); !strings.Contains(str(c, "note"), "terminée") {
		t.Fatalf("cancel_job sur impression terminée : %v", c)
	}
}

func TestSliceWithoutStudioFailsClearlyAndIsIdempotent(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	r.printerID = str(r.addPrinter(nil), "printer", "id")
	data, _ := stlBox(20, 20, 20)
	sha, size, _ := r.c.Blobs.Put(strings.NewReader(string(data)), "")
	m, ver, _, err := r.c.Catalog.RegisterGenerated(t.Context(), "test", newModel("cube"), "", "v1", "", []catalogFile{{Name: "cube.stl", Format: "stl", SHA256: sha, Size: size, Unit: "mm"}})
	if err != nil {
		t.Fatal(err)
	}
	a := r.call("execute_headless_slice", map[string]any{"model_id": m.ID, "printer_id": r.printerID, "idempotency_key": "cle-decoupe-0001"})
	id := str(a, "job", "id")
	b := r.call("execute_headless_slice", map[string]any{"model_id": m.ID, "printer_id": r.printerID, "idempotency_key": "cle-decoupe-0001"})
	if str(b, "job", "id") != id {
		t.Fatalf("clé d'idempotence : %s ≠ %s", str(b, "job", "id"), id)
	}
	r.waitState(id, 15*time.Second, "FAILED")
	st := r.call("get_print_status", map[string]any{"print_job_id": id})
	if !strings.Contains(str(st, "job", "last_error"), "slicer_missing") || str(st, "job", "version_id") != ver.ID {
		t.Fatalf("échec attendu slicer_missing : %v", st)
	}
	if r.callErr("execute_headless_slice", map[string]any{"model_id": "mdl_inconnu", "printer_id": r.printerID}) == "" {
		t.Fatal("modèle inconnu")
	}
	_ = mcp.LoggingLevel("")
}

func TestPrintToolsGuardsAndPreview(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	r.printerID = str(r.addPrinter(nil), "printer", "id")
	data, _ := stlBox(30, 20, 10)
	sha, size, _ := r.c.Blobs.Put(strings.NewReader(string(data)), "")
	m, _, _, err := r.c.Catalog.RegisterGenerated(t.Context(), "test", newModel("boite"), "", "v1", "", []catalogFile{{Name: "boite.stl", Format: "stl", SHA256: sha, Size: size, Unit: "mm"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "capture_preview", Arguments: map[string]any{"model_id": m.ID, "width": 400, "height": 300}})
	if err != nil || res.IsError {
		t.Fatalf("capture_preview : %v %+v", err, res)
	}
	var img *mcp.ImageContent
	for _, c := range res.Content {
		if ic, ok := c.(*mcp.ImageContent); ok {
			img = ic
		}
	}
	if img == nil || img.MIMEType != "image/png" || len(img.Data) < 1000 || string(img.Data[1:4]) != "PNG" {
		t.Fatalf("image : %+v", img)
	}
	// L'outil de vue est en lecture seule, invisible pour le modèle ; aucun outil d'approbation n'existe.
	tools, _ := r.cs.ListTools(t.Context(), nil)
	seen := 0
	for _, tl := range tools.Tools {
		if tl.Name == "confirm_print_from_app" {
			t.Fatal("confirm_print_from_app ne doit plus exister")
		}
		if tl.Name == "get_print_confirmation_view" {
			seen++
			if !strings.Contains(fmt.Sprint(tl.Meta), "app") {
				t.Fatalf("%s : visibility app attendue : %v", tl.Name, tl.Meta)
			}
		}
	}
	if seen != 1 {
		t.Fatal("outil de vue absent")
	}
	// start_print sur une impression inexistante / mauvaise entrée.
	if r.callErr("start_print", map[string]any{"print_job_id": "pj_x", "confirmation_id": "cf_x", "idempotency_key": "court"}) == "" {
		t.Fatal("clé trop courte")
	}
	if r.callErr("get_print_status", map[string]any{"print_job_id": "pj_inconnu"}) == "" {
		t.Fatal("impression inconnue")
	}
	if r.callErr("request_print_confirmation", map[string]any{"print_job_id": "pj_inconnu"}) == "" {
		t.Fatal("confirmation d'une impression inconnue")
	}
}
