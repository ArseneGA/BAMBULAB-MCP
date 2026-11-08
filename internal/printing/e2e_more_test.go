package printing_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConcurrentStartsSendOneCommand(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	r.c.Printing.SetValidator(fakeValidator("0.4"))
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))
	art := r.fakeArtifact()
	job, _ := r.uploadAndAsk(art, "cle-depot-concurrence")
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	if st, _ := r.humanConfirm(str(cf, "confirm_url"), true); st != 200 {
		t.Fatal("approbation")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	printing := 0
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m, _, isErr := r.raw("start_print", map[string]any{"print_job_id": job, "confirmation_id": str(cf, "confirmation_id"), "idempotency_key": fmt.Sprintf("cle-concurrente-%d", i)})
			if !isErr && str(m, "outcome") == "printing" && m["replayed"] != true { // aucun ne doit être l'exécutant : l'approbation a déjà lancé
				mu.Lock()
				printing++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if n := len(r.sim.StartCommands()); n != 1 {
		t.Fatalf("%d commande(s) project_file pour 5 lancements concurrents, 1 attendue", n)
	}
	if printing > 0 {
		t.Fatalf("%d réponses « printing »", printing)
	}
	// La confirmation est consommée une seule fois, en base.
	var used *string
	var doc string
	if err := r.c.Store.DB().QueryRow(`SELECT doc, used_at FROM print_confirmations WHERE id=?`, str(cf, "confirmation_id")).Scan(&doc, &used); err != nil || used == nil {
		t.Fatalf("confirmation non marquée utilisée : %v %v", used, err)
	}
	r.waitState(job, 30e9, "COMPLETED")
}

func (r *rig) httpJSON(method, path, body string) (int, map[string]any, http.Header) {
	r.t.Helper()
	req, _ := http.NewRequest(method, r.web.URL+path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return res.StatusCode, m, res.Header
}

func TestHTTPRoutesAndPageSecurity(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	r.c.Printing.SetValidator(fakeValidator("0.4"))
	_, mp := splitAddr(r.sim.MQTTAddr())
	_, fp := splitAddr(r.sim.FTPAddr())
	body := fmt.Sprintf(`{"name":"HTTP","model_id":"p1s","host":"127.0.0.1","serial":%q,"mqtt_port":%d,"ftp_port":%d,"ams_units":1}`, r.sim.Serial(), mp, fp)
	st, pr, _ := r.httpJSON("POST", "/api/printers", body)
	if st != 201 {
		t.Fatalf("POST /api/printers : %d %v", st, pr)
	}
	id := pr["id"].(string)
	if st, _, _ := r.httpJSON("POST", "/api/printers", `{"name":"x","model_id":"p1s","host":"127.0.0.1","serial":"01P00A000000001","access_code":"12345678"}`); st != 400 {
		t.Fatalf("un code d'accès dans le corps JSON doit être refusé (champ inconnu) : %d", st)
	}
	if st, m, _ := r.httpJSON("GET", "/api/printers", ""); st != 200 || len(m["printers"].([]any)) != 1 || strings.Contains(fmt.Sprint(m), "access_code\":") {
		t.Fatalf("liste : %d %v", st, m)
	}
	if st, _, _ := r.httpJSON("GET", "/api/printers/prn_inconnue", ""); st != 404 {
		t.Fatalf("imprimante inconnue : %d", st)
	}
	r.humanSetsAccessCode(r.web.URL + "/setup/printer/" + id)
	// La connexion MQTT et le premier rapport complet sont asynchrones : attente bornée de l'état « idle ».
	var (
		stHTTP int
		stBody map[string]any
	)
	for deadline := time.Now().Add(20 * time.Second); ; {
		stHTTP, stBody, _ = r.httpJSON("GET", "/api/printers/"+id+"/status", "")
		if (stHTTP == 200 && stBody["state"] == "idle") || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if stHTTP != 200 || stBody["state"] != "idle" {
		t.Fatalf("statut : %d %v", stHTTP, stBody)
	}
	art := r.fakeArtifact()
	st, v, _ := r.httpJSON("POST", "/api/print-jobs/validate", fmt.Sprintf(`{"artifact_id":%q,"printer_id":%q}`, art, id))
	if st != 200 || v["report"].(map[string]any)["ok"] != true {
		t.Fatalf("validate : %d %v", st, v)
	}
	job := v["print_job_id"].(string)
	if st, m, _ := r.httpJSON("POST", "/api/print-jobs/upload", fmt.Sprintf(`{"print_job_id":%q,"printer_id":%q,"assignments":[{"filament_slot":1,"tray":"ext","material_id":"PLA"}]}`, job, id)); st != 400 {
		t.Fatalf("upload sans clé d'idempotence : %d %v", st, m)
	}
	st, up, _ := r.httpJSON("POST", "/api/print-jobs/upload", fmt.Sprintf(`{"print_job_id":%q,"printer_id":%q,"idempotency_key":"cle-http-depot-1","assignments":[{"filament_slot":1,"tray":"ext","material_id":"PLA"}]}`, job, id))
	if st != 200 || up["state"] != "AWAITING_PRINT_CONFIRMATION" {
		t.Fatalf("upload : %d %v", st, up)
	}
	st, cf, _ := r.httpJSON("POST", "/api/print-jobs/"+job+"/confirmation", "")
	if st != 201 {
		t.Fatalf("confirmation : %d %v", st, cf)
	}
	cid := cf["confirmation_id"].(string)
	// Aucune route JSON ne permet d'approuver ou de lancer sans l'humain.
	for _, p := range []string{"/api/confirmations/" + cid + "/approve", "/api/confirmations/" + cid + "/confirm"} {
		if st, _, _ := r.httpJSON("POST", p, `{"plate_clear":true}`); st != 404 && st != 405 {
			t.Fatalf("%s existe ? %d", p, st)
		}
	}
	if st, m, _ := r.httpJSON("POST", "/api/print-jobs/start", fmt.Sprintf(`{"print_job_id":%q,"confirmation_id":%q,"idempotency_key":"cle-http-start-1"}`, job, cid)); st != 403 || m["code"] != "needs_human_approval" {
		t.Fatalf("start sans approbation : %d %v", st, m)
	}
	if len(r.sim.StartCommands()) != 0 {
		t.Fatal("commande envoyée")
	}
	// En-têtes de la page de confirmation : anti-clickjacking, pas de cache.
	res, err := http.Get(cf["confirm_url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	h := res.Header
	if h.Get("X-Frame-Options") != "DENY" || !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("en-têtes : %v", h)
	}
	// Origine étrangère : le garde HTTP (anti-CSRF navigateur) refuse la soumission.
	req, _ := http.NewRequest("POST", cf["confirm_url"].(string), strings.NewReader("plate_clear=yes&nonce=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 403 {
		t.Fatalf("POST cross-origin : %v %v", res, err)
	}
	if st, _ := r.humanConfirm(cf["confirm_url"].(string), true); st != 200 {
		t.Fatal("approbation humaine")
	}
	st, s, _ := r.httpJSON("POST", "/api/print-jobs/start", fmt.Sprintf(`{"print_job_id":%q,"confirmation_id":%q,"idempotency_key":"cle-http-start-2"}`, job, cid))
	if st != 200 || s["outcome"] != "printing" {
		t.Fatalf("start : %d %v", st, s)
	}
	r.waitState(job, 30e9, "COMPLETED")
	if st, m, _ := r.httpJSON("GET", "/api/print-jobs/"+job, ""); st != 200 || m["job"].(map[string]any)["state"] != "COMPLETED" {
		t.Fatalf("suivi : %d", st)
	}
	if st, m, _ := r.httpJSON("POST", "/api/previews", fmt.Sprintf(`{"model_id":"mdl_inconnu"}`)); st != 404 {
		t.Fatalf("aperçu d'un modèle inconnu : %d %v", st, m)
	}
	if st, _, _ := r.httpJSON("GET", "/api/previews/"+strings.Repeat("0", 64), ""); st != 404 {
		t.Fatalf("aperçu absent : %d", st)
	}
}

func TestParametricAndModifyGeometryTools(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	r.printerID = str(r.addPrinter(nil), "printer", "id")
	// worker fictif (le vrai exige uv + CadQuery) : un cube de inner_x mm
	if txt := r.callErr("create_parametric_model", map[string]any{"template": "box", "params": map[string]any{}}); !strings.Contains(txt, "inner_x") {
		t.Fatalf("paramètres invalides : %s", txt)
	}
	if r.callErr("create_parametric_model", map[string]any{"template": "box", "params": map[string]any{"inner_x": 30}, "name": "../evil"}) == "" {
		t.Fatal("nom de modèle dangereux")
	}
	pm := r.call("create_parametric_model", map[string]any{"template": "box", "params": map[string]any{"inner_x": 30}, "name": "boitier_test"})
	mid, v1 := str(pm, "model_id"), str(pm, "version_id")
	if mid == "" || v1 == "" || len(pm["assumptions"].([]any)) == 0 || len(pm["files"].([]any)) != 1 {
		t.Fatalf("modèle paramétrique : %v", pm)
	}
	det := r.call("get_model_details", map[string]any{"model_id": mid})
	if len(det["versions"].([]any)) != 1 {
		t.Fatalf("versions : %v", det["versions"])
	}
	origSHA := pm["files"].([]any)[0].(map[string]any)["sha256"].(string)

	md := r.call("modify_geometry", map[string]any{"model_id": mid, "base_version_id": v1, "label": "mis à l'échelle", "operations": []any{
		map[string]any{"op": "scale", "factors": []any{2, 1, 1}},
		map[string]any{"op": "lay_flat"},
		map[string]any{"op": "center_on_bed", "printer_id": r.printerID},
	}})
	v2 := str(md, "version_id")
	if v2 == "" || v2 == v1 || str(md, "parent_version_id") != v1 {
		t.Fatalf("nouvelle version : %v", md)
	}
	diff := md["diff"].(map[string]any)
	before, after := diff["before"].(map[string]any), diff["after"].(map[string]any)
	if before["size_mm"].([]any)[0].(float64) != 30 || after["size_mm"].([]any)[0].(float64) < 59 || diff["volume_ratio"].(float64) < 1.99 || diff["volume_ratio"].(float64) > 2.01 {
		t.Fatalf("différences : %v", diff)
	}
	if len(md["applied"].([]any)) != 3 || md["applied"].([]any)[0].(map[string]any)["matrix"] == nil {
		t.Fatalf("opérations appliquées : %v", md["applied"])
	}
	// jamais d'écrasement : deux versions, le fichier d'origine intact
	det = r.call("get_model_details", map[string]any{"model_id": mid})
	if len(det["versions"].([]any)) != 2 {
		t.Fatalf("versions : %v", det["versions"])
	}
	files := det["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("fichiers : %v", files)
	}
	found := false
	for _, f := range files {
		if f.(map[string]any)["sha256"] == origSHA {
			found = true
		}
	}
	if !found {
		t.Fatal("fichier d'origine absent")
	}
	// révision périmée refusée ; opération invalide refusée sans créer de version
	if txt := r.callErr("modify_geometry", map[string]any{"model_id": mid, "base_version_id": v1, "operations": []any{map[string]any{"op": "mirror", "plane": "xy"}}}); !strings.Contains(txt, "périmée") {
		t.Fatalf("base périmée : %s", txt)
	}
	if r.callErr("modify_geometry", map[string]any{"model_id": mid, "base_version_id": v2, "operations": []any{map[string]any{"op": "scale", "factor": -2}}}) == "" {
		t.Fatal("échelle négative")
	}
	cut := 10.0
	mc := r.call("modify_geometry", map[string]any{"model_id": mid, "base_version_id": v2, "operations": []any{map[string]any{"op": "cut_plane", "axis": "z", "offset": cut, "keep": "below"}}})
	if mc["diff"].(map[string]any)["after"].(map[string]any)["watertight"] != true {
		t.Fatalf("coupe : %v", mc)
	}
	det = r.call("get_model_details", map[string]any{"model_id": mid})
	if len(det["versions"].([]any)) != 3 {
		t.Fatalf("versions après refus et coupe : %d", len(det["versions"].([]any)))
	}
}
