package printing_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// Point 4 : l'hôte d'une imprimante est un hôte du réseau local.
func TestCheckPrinterHostPolicy(t *testing.T) {
	for _, ok := range []string{"192.168.1.20", "10.0.0.7", "172.16.0.5", "172.31.255.1", "169.254.3.4", "fd00::1", "fe80::1", "imprimante.local", "P1S-Atelier.LOCAL"} {
		if err := printing.CheckPrinterHost(ok, false); err != nil {
			t.Errorf("%q refusé : %v", ok, err)
		}
	}
	for _, bad := range []string{"8.8.8.8", "127.0.0.1", "::1", "172.32.0.1", "192.169.0.1", "2001:db8::1", "example.com", "localhost", "evil.local.example.com", "a b", ""} {
		if err := printing.CheckPrinterHost(bad, false); err == nil {
			t.Errorf("%q accepté", bad)
		}
	}
	for _, any := range []string{"8.8.8.8", "127.0.0.1", "example.com"} {
		if err := printing.CheckPrinterHost(any, true); err != nil {
			t.Errorf("option serveur : %q refusé : %v", any, err)
		}
	}
}

// Point 4 : add_printer n'accepte plus insecure_reason, allow_start_from_finish ni pinned_sha256.
func TestAddPrinterToolHasNoBypassInputs(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	tools, err := r.cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name != "add_printer" {
			continue
		}
		raw, _ := jsonString(tl.InputSchema)
		for _, f := range []string{"insecure_reason", "allow_start_from_finish", "pinned_sha256", "access_code"} {
			if strings.Contains(raw, f) {
				t.Errorf("add_printer expose %q", f)
			}
		}
	}
	base := map[string]any{"name": "X", "model_id": "p1s", "host": "127.0.0.1", "serial": r.sim.Serial()}
	for _, extra := range []map[string]any{{"insecure_reason": "test"}, {"allow_start_from_finish": true}, {"pinned_sha256": strings.Repeat("a", 64)}} {
		args := map[string]any{}
		for k, v := range base {
			args[k] = v
		}
		for k, v := range extra {
			args[k] = v
		}
		if txt := r.callErr("add_printer", args); txt == "" {
			t.Errorf("champ interdit accepté : %v", extra)
		}
	}
	ps := r.call("list_printers", nil)
	if n := len(ps["printers"].([]any)); n != 0 {
		t.Fatalf("%d imprimante(s) créée(s) malgré des champs interdits", n)
	}
}

func jsonString(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// Point 4 : l'épinglage se fait sur /setup : le serveur affiche l'empreinte lue et le numéro de série attendu ;
// l'humain confirme en saisissant le code ; ce qui est épinglé est EXACTEMENT ce qui a été affiché.
func TestSetupPagePinsTheDisplayedFingerprint(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	ap := r.addPrinter(nil)
	id := str(ap, "printer", "id")
	setup := str(ap, "setup_url")
	// Avant la validation humaine : aucune connexion (empreinte non épinglée).
	st := r.call("get_printer_status", map[string]any{"printer_id": id})
	if str(st, "state") != "offline" || !strings.Contains(str(st, "error"), "/setup/printer/") {
		t.Fatalf("avant épinglage : %v", st)
	}
	if rec, _ := r.c.Printing.GetPrinterRecord(t.Context(), id); rec.PinnedSHA256 != "" {
		t.Fatal("empreinte épinglée sans l'humain")
	}
	hst, page, nonce := r.humanGet(setup)
	if hst != 200 || !strings.Contains(page, r.sim.Fingerprint()) || !strings.Contains(page, r.sim.Serial()) || strings.Contains(page, "Attention : le certificat ne porte pas") {
		t.Fatalf("la page doit montrer l'empreinte et le numéro de série attendu : %d", hst)
	}
	// Code trop court : refusé, rien n'est épinglé.
	if s, _ := r.humanPost(setup, url.Values{"nonce": {nonce}, "access_code": {"x"}}); s != 400 {
		t.Fatalf("code invalide : %d", s)
	}
	if rec, _ := r.c.Printing.GetPrinterRecord(t.Context(), id); rec.PinnedSHA256 != "" || r.c.Printing.Secrets().HasAccessCode(id) {
		t.Fatal("épinglage ou code enregistré malgré un refus")
	}
	// Nouveau nonce ; un champ « fingerprint » forgé dans le formulaire est ignoré.
	_, page, nonce = r.humanGet(setup)
	forged := strings.Repeat("e", 64)
	if s, body := r.humanPost(setup, url.Values{"nonce": {nonce}, "access_code": {simAccessCode}, "fingerprint": {forged}, "pinned_sha256": {forged}}); s != 200 || strings.Contains(body, simAccessCode) {
		t.Fatalf("épinglage : %d", s)
	}
	rec, _ := r.c.Printing.GetPrinterRecord(t.Context(), id)
	if rec.PinnedSHA256 != r.sim.Fingerprint() {
		t.Fatalf("empreinte épinglée %s, attendue celle qui a été affichée %s", rec.PinnedSHA256, r.sim.Fingerprint())
	}
	r.waitPrinterState(id, "idle")
	// Le nonce est à usage unique.
	if s, _ := r.humanPost(setup, url.Values{"nonce": {nonce}, "access_code": {simAccessCode}}); s != 403 {
		t.Fatalf("nonce rejoué : %d", s)
	}
}

// Point 4 : changement d'hôte -> ré-épinglage obligatoire (l'ancienne empreinte est effacée, plus aucune connexion).
func TestHostChangeForcesRepin(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	ap := r.addPrinter(nil)
	id := str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))
	r.waitPrinterState(id, "idle")
	// Même hôte : l'épinglage est conservé.
	if _, err := r.c.Printing.UpdatePrinterHost(t.Context(), id, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := r.c.Printing.GetPrinterRecord(t.Context(), id); rec.PinnedSHA256 == "" {
		t.Fatal("hôte inchangé : l'épinglage ne doit pas être perdu")
	}
	if _, err := r.c.Printing.UpdatePrinterHost(t.Context(), id, "localhost"); err != nil { // autre hôte (option serveur du rig)
		t.Fatal(err)
	}
	rec, _ := r.c.Printing.GetPrinterRecord(t.Context(), id)
	if rec.PinnedSHA256 != "" || rec.Host != "localhost" {
		t.Fatalf("après changement d'hôte : %+v", rec)
	}
	st := r.call("get_printer_status", map[string]any{"printer_id": id})
	if str(st, "state") != "offline" || !strings.Contains(str(st, "error"), "épinglé") {
		t.Fatalf("aucune connexion sans nouvel épinglage : %v", st)
	}
	if _, err := r.c.Printing.UpdatePrinterHost(t.Context(), id, "pas un hôte"); err == nil {
		t.Fatal("hôte invalide accepté")
	}
	// L'humain ré-épingle sur la page (le serveur relit le certificat du nouvel hôte).
	r.humanSetsAccessCode(str(ap, "setup_url"))
	if rec, _ := r.c.Printing.GetPrinterRecord(t.Context(), id); rec.PinnedSHA256 != r.sim.Fingerprint() {
		t.Fatal("ré-épinglage")
	}
}

// Point 4 : un certificat qui ne porte pas le numéro de série attendu est signalé sur la page.
func TestSetupPageWarnsOnSerialMismatch(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	ap := r.addPrinter(map[string]any{"serial": "ZZ9999999999"})
	if _, page, _ := r.humanGet(str(ap, "setup_url")); !strings.Contains(page, "ne porte pas le numéro de série attendu") {
		t.Fatalf("avertissement attendu : %s", page)
	}
}

// Point 2 : sans navigateur enregistré, les pages ne montrent rien et n'agissent pas.
func TestPagesRequireEnrolledBrowser(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	r.c.Printing.SetValidator(fakeValidator("0.4"))
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))
	job, _ := r.uploadAndAsk(r.fakeArtifact(), "cle-depot-anon")
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	// Un client sans cookie (le modèle ou un autre processus) : redirigé vers /enroll, jamais la page.
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := anon.Get(str(cf, "confirm_url"))
	if err != nil || res.StatusCode != 303 || !strings.HasPrefix(res.Header.Get("Location"), "/enroll") {
		t.Fatalf("GET sans cookie : %v %v", res, err)
	}
	res.Body.Close()
	// POST sans cookie, même avec de bons en-têtes et un nonce valide obtenu par ailleurs : refus.
	_, _, nonce := r.humanGet(str(cf, "confirm_url"))
	req, _ := http.NewRequest("POST", str(cf, "confirm_url"), strings.NewReader(url.Values{"nonce": {nonce}, "plate_clear": {"yes"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("POST sans cookie : %d", res.StatusCode)
	}
	if v := r.call("get_print_confirmation_view", map[string]any{"confirmation_id": str(cf, "confirmation_id")}); v["approved"] != false {
		t.Fatal("approuvée sans navigateur enregistré")
	}
}

// Point 10 : /api/previews/{sha} ne sert que des blobs enregistrés comme aperçus PNG.
func TestPreviewRouteServesOnlyRegisteredPreviews(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	data, _ := stlBox(30, 20, 10)
	secret, _, err := r.c.Blobs.Put(strings.NewReader(string(data)), "")
	if err != nil {
		t.Fatal(err)
	}
	if st, _, _ := r.httpJSON("GET", "/api/previews/"+secret, ""); st != 404 {
		t.Fatalf("un blob quelconque est servi : %d", st)
	}
	m, _, _, err := r.c.Catalog.RegisterGenerated(t.Context(), "test", newModel("boite"), "", "v1", "", []catalogFile{{Name: "boite.stl", Format: "stl", SHA256: secret, Size: int64(len(data)), Unit: "mm"}})
	if err != nil {
		t.Fatal(err)
	}
	out := r.call("capture_preview", map[string]any{"model_id": m.ID, "width": 320, "height": 240})
	png := str(out, "png_sha256")
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(png) {
		t.Fatalf("aperçu : %v", out)
	}
	res, err := http.Get(r.web.URL + "/api/previews/" + png)
	if err != nil || res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("aperçu enregistré : %v %v", res, err)
	}
	res.Body.Close()
	if st, _, _ := r.httpJSON("GET", "/api/previews/"+secret, ""); st != 404 {
		t.Fatalf("le blob du modèle est servi : %d", st)
	}
	if st, _, _ := r.httpJSON("GET", "/api/previews/pas-un-sha", ""); st != 404 {
		t.Fatalf("sha invalide : %d", st)
	}
}

// Point 6 : dépôt rejoué avec la même clé mais d'autres paramètres -> refusé ; mêmes paramètres -> rejeu sans renvoi.
func TestUploadReplayWithDifferentParamsRefused(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	r.c.Printing.SetValidator(fakeValidator("0.4"))
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))
	art := r.fakeArtifact()
	job, _ := r.uploadAndAsk(art, "cle-depot-rejeu-1")
	again := r.call("upload_print_artifact", map[string]any{"print_job_id": job, "printer_id": r.printerID, "idempotency_key": "cle-depot-rejeu-1", "plate": 1, "assignments": anyAssign(ams1)})
	if again["replayed"] != true {
		t.Fatalf("rejeu identique : %v", again["replayed"])
	}
	other := r.callErr("upload_print_artifact", map[string]any{"print_job_id": job, "printer_id": r.printerID, "idempotency_key": "cle-depot-rejeu-1", "plate": 1, "assignments": anyAssign(ext1)})
	if !strings.Contains(other, "d'autres paramètres") {
		t.Fatalf("rejeu avec d'autres matières : %s", other)
	}
	var key string
	if err := r.c.Store.DB().QueryRow(`SELECT idempotency_key FROM jobs WHERE kind='upload_print_artifact'`).Scan(&key); err != nil || key != job+":upload:cle-depot-rejeu-1" {
		t.Fatalf("clé composée attendue, trouvée %q (%v)", key, err)
	}
}
