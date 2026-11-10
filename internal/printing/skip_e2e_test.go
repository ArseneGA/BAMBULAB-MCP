package printing_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
)

// SK0 : ignorer des objets, de bout en bout avec le simulateur, par les routes locales réservées de la sous-commande
// humaine (CA0.2, CA0.3, CA0.4).

const skipSliceInfo = `<config><plate><metadata key="index" value="1"/>` +
	`<object identify_id="130" name="Cube 1" skipped="false"/><object identify_id="174" name="Cube 2" skipped="false"/><object identify_id="200" name="Cube 3" skipped="false"/></plate></config>`

// skipArtifact dépose un vrai .gcode.3mf minimal (seul slice_info.config compte pour SK0).
func (r *rig) skipArtifact(sliceInfo string) string {
	r.t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("Metadata/slice_info.config")
	_, _ = w.Write([]byte(sliceInfo))
	w, _ = zw.Create("Metadata/plate_1.gcode")
	_, _ = w.Write([]byte("; vide\n"))
	if err := zw.Close(); err != nil {
		r.t.Fatal(err)
	}
	sha, _, err := r.c.Blobs.Put(bytes.NewReader(buf.Bytes()), "")
	if err != nil {
		r.t.Fatal(err)
	}
	return sha
}

func skipRig(t *testing.T, key string, faults ...string) (*rig, string) {
	t.Helper()
	s := recSim(t, 150*time.Millisecond)
	for _, f := range faults {
		if err := s.SetFault(f, true); err != nil {
			t.Fatal(err)
		}
	}
	s.SetObjects([]sim.Object{{ID: 130, Name: "Cube 1", Grams: 10}, {ID: 174, Name: "Cube 2", Grams: 10}, {ID: 200, Name: "Cube 3", Grams: 10}})
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = s
	r.seedPrinterModel()
	r.c.Printing.SetValidator(fakeValidator("0.4"))
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))
	job, _ := r.uploadAndAsk(r.skipArtifact(skipSliceInfo), key)
	return r, job
}

// skipPost appelle une route locale comme le fait la sous-commande (connexion loopback directe, JSON, sans Origin).
func skipPost(t *testing.T, base, path string, body any, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", base+"/api/system/skip-objects/"+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return res.StatusCode, m
}

func auditOps(t *testing.T, r *rig, job string) map[string]string {
	t.Helper()
	es, err := r.c.Store.ListAudit(context.Background(), "print_job", job, 200)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range es {
		out[e.Operation] = e.Actor
	}
	return out
}

func idsOf(v any) []float64 {
	var out []float64
	for _, x := range v.([]any) {
		out = append(out, x.(map[string]any)["id"].(float64))
	}
	return out
}

// CA0.2 + CA0.3 : refus (absent, déjà ignoré, tous les objets restants, pas en cours), puis envoi unique et vérifié.
func TestSkipObjectsVerifiedEndToEnd(t *testing.T) {
	r, job := skipRig(t, "cle-skip-e2e")

	// Pas encore en cours : la liste se lit, l'envoi est refusé (409), rien ne part.
	code, ls := skipPost(t, r.web.URL, "list", map[string]any{"job": job}, nil)
	if code != 200 || ls["can_skip"] != false || !strings.Contains(str(ls, "refusal"), "en cours") || len(ls["objects"].([]any)) != 3 {
		t.Fatalf("liste avant lancement : %d %v", code, ls)
	}
	if code, m := skipPost(t, r.web.URL, "prepare", map[string]any{"job": job, "ids": []int{174}}, nil); code != 409 {
		t.Fatalf("préparation avant lancement : %d %v", code, m)
	}

	r.launch(job)
	time.Sleep(300 * time.Millisecond)

	code, ls = skipPost(t, r.web.URL, "list", map[string]any{"job": job}, nil)
	if code != 200 || ls["can_skip"] != true || len(idsOf(ls["objects"])) != 3 {
		t.Fatalf("liste en cours : %d %v", code, ls)
	}

	// CA0.2 : identifiant absent du fichier, doublon, aucun identifiant, tous les objets : refus clairs.
	if code, m := skipPost(t, r.web.URL, "prepare", map[string]any{"job": job, "ids": []int{999}}, nil); code != 400 || !strings.Contains(str(m, "error"), "absent du fichier") {
		t.Fatalf("id absent : %d %v", code, m)
	}
	if code, m := skipPost(t, r.web.URL, "prepare", map[string]any{"job": job, "ids": []int{130, 130}}, nil); code != 400 || !strings.Contains(str(m, "error"), "double") {
		t.Fatalf("doublon : %d %v", code, m)
	}
	if code, _ := skipPost(t, r.web.URL, "prepare", map[string]any{"job": job, "ids": []int{}}, nil); code != 400 {
		t.Fatalf("liste vide : %d", code)
	}
	if code, m := skipPost(t, r.web.URL, "prepare", map[string]any{"job": job, "ids": []int{130, 174, 200}}, nil); code != 400 || !strings.Contains(str(m, "error"), "utilisez l'arrêt") {
		t.Fatalf("tous les objets : %d %v", code, m)
	}
	if r.sim.CommandCount("skip_objects") != 0 {
		t.Fatal("rien ne devait partir pendant les refus")
	}

	// Préparation puis envoi : une fois, vérifié par s_obj.
	code, pr := skipPost(t, r.web.URL, "prepare", map[string]any{"job": job, "ids": []int{174}}, nil)
	if code != 200 || str(pr, "confirm_token") == "" || len(idsOf(pr["targets"])) != 1 || len(idsOf(pr["remaining"])) != 2 {
		t.Fatalf("préparation : %d %v", code, pr)
	}
	if tg := pr["targets"].([]any)[0].(map[string]any); tg["name"] != "Cube 2" || tg["id"] != float64(174) {
		t.Fatalf("cible : %v", tg)
	}
	if r.sim.CommandCount("skip_objects") != 0 {
		t.Fatal("la préparation n'envoie rien")
	}
	tok := str(pr, "confirm_token")
	t0 := time.Now()
	code, res := skipPost(t, r.web.URL, "execute", map[string]any{"confirm_token": tok, "wait_s": 20}, nil)
	if code != 200 || str(res, "status") != "verified" {
		t.Fatalf("exécution : %d %v", code, res)
	}
	if time.Since(t0) > 20*time.Second {
		t.Fatal("vérifié trop tard")
	}
	if n := r.sim.CommandCount("skip_objects"); n != 1 {
		t.Fatalf("skip_objects reçu %d fois", n)
	}
	// jeton consommé : une seconde exécution est refusée et ne renvoie rien
	if code, m := skipPost(t, r.web.URL, "execute", map[string]any{"confirm_token": tok}, nil); code != 404 {
		t.Fatalf("jeton réutilisé : %d %v", code, m)
	}
	if n := r.sim.CommandCount("skip_objects"); n != 1 {
		t.Fatalf("renvoyé : %d", n)
	}
	// seule la commande skip_objects (et le lancement humain) est sortie : jamais pause, reprise, arrêt ni G-code
	for _, c := range r.sim.Commands() {
		if c != "project_file" && c != "skip_objects" {
			t.Fatalf("commande inattendue : %q (%v)", c, r.sim.Commands())
		}
	}
	if sk := r.sim.SkippedObjects(); len(sk) != 1 || sk[0] != 174 {
		t.Fatalf("s_obj simulé : %v", sk)
	}
	if !strings.Contains(r.sim.SkipPayloads()[0], `"obj_list":[174]`) || !strings.Contains(r.sim.SkipPayloads()[0], `"command":"skip_objects"`) {
		t.Fatalf("message reçu : %v", r.sim.SkipPayloads())
	}

	// visible dans get_print_status et get_printer_status ; événements du journal ; audit
	waitFor(t, 15*time.Second, "objets ignorés visibles dans get_print_status", func() bool {
		pg, _ := r.status(job)["job"].(map[string]any)["print"].(map[string]any)
		l, _ := pg["skipped_objects"].([]any)
		return len(l) == 1 && l[0] == float64(174)
	})
	waitFor(t, 5*time.Second, "objets ignorés visibles dans get_printer_status", func() bool {
		l, _ := r.call("get_printer_status", map[string]any{"printer_id": r.printerID})["skipped_objects"].([]any)
		return len(l) == 1 && l[0] == float64(174)
	})
	hasEvents(t, r.status(job), "skip_requested", "skip_approved", "skip_sent", "skip_verified")
	ops := auditOps(t, r, job)
	for _, op := range []string{"print.skip_requested", "print.skip_approved", "print.skip_sent", "print.skip_verified"} {
		if ops[op] != "human:cli" {
			t.Fatalf("audit %s : acteur %q (tout : %v)", op, ops[op], ops)
		}
	}
	// l'objet ignoré ne consomme plus
	c1 := r.sim.ObjectConsumed(174)
	time.Sleep(500 * time.Millisecond)
	if r.sim.ObjectConsumed(174) != c1 {
		t.Fatal("l'objet ignoré consomme encore")
	}

	// CA0.2 : objet déjà dans s_obj, puis dernier objet restant
	if code, m := skipPost(t, r.web.URL, "prepare", map[string]any{"job": job, "ids": []int{174}}, nil); code != 400 || !strings.Contains(str(m, "error"), "déjà ignoré") {
		t.Fatalf("déjà ignoré : %d %v", code, m)
	}
	if code, m := skipPost(t, r.web.URL, "prepare", map[string]any{"job": job, "ids": []int{130, 200}}, nil); code != 400 || !strings.Contains(str(m, "error"), "utilisez l'arrêt") {
		t.Fatalf("tous les objets restants : %d %v", code, m)
	}
	// la liste marque l'objet ignoré
	_, ls = skipPost(t, r.web.URL, "list", map[string]any{"job": job}, nil)
	for _, o := range ls["objects"].([]any) {
		m := o.(map[string]any)
		if (m["id"] == float64(174)) != (m["skipped"] == true) {
			t.Fatalf("marque « ignoré » : %v", ls["objects"])
		}
	}
}

// Simulateur qui ignore la commande : « envoyée, non confirmée », une seule publication, aucune nouvelle tentative.
func TestSkipObjectsSentUnconfirmed(t *testing.T) {
	r, job := skipRig(t, "cle-skip-unconfirmed", sim.FaultIgnoreSkip)
	r.launch(job)
	time.Sleep(300 * time.Millisecond)
	_, pr := skipPost(t, r.web.URL, "prepare", map[string]any{"job": job, "ids": []int{130}}, nil)
	code, res := skipPost(t, r.web.URL, "execute", map[string]any{"confirm_token": str(pr, "confirm_token"), "wait_s": 1}, nil)
	if code != 200 || str(res, "status") != "sent_unconfirmed" || !strings.Contains(str(res, "message"), "PAS renvoyée") {
		t.Fatalf("exécution : %d %v", code, res)
	}
	time.Sleep(1500 * time.Millisecond)
	if n := r.sim.CommandCount("skip_objects"); n != 1 {
		t.Fatalf("skip_objects reçu %d fois (jamais renvoyé)", n)
	}
	if len(r.sim.SkippedObjects()) != 0 {
		t.Fatal("rien ne devait être ignoré")
	}
	hasEvents(t, r.status(job), "skip_requested", "skip_approved", "skip_sent", "skip_unconfirmed")
	ops := auditOps(t, r, job)
	if ops["print.skip_sent"] != "human:cli" || ops["print.skip_unconfirmed"] != "human:cli" || ops["print.skip_verified"] != "" {
		t.Fatalf("audit : %v", ops)
	}
}

// Les routes sont réservées : pas de navigateur (Origin, Sec-Fetch), pas de proxy, pas de cookie de navigateur enregistré
// à la place du jeton d'API.
func TestSkipObjectsRoutesAreReserved(t *testing.T) {
	r, job := skipRig(t, "cle-skip-reserved")
	body := map[string]any{"job": job}
	for name, hdr := range map[string]map[string]string{
		"Origin":         {"Origin": "http://evil.example"},
		"Sec-Fetch-Site": {"Sec-Fetch-Site": "same-origin"},
		"proxy":          {"X-Forwarded-For": "203.0.113.5"},
		"Tailscale":      {"Tailscale-User-Login": "x@example.org"},
	} {
		if code, m := skipPost(t, r.web.URL, "list", body, hdr); code != 403 {
			t.Fatalf("%s : %d %v", name, code, m)
		}
	}
	// corps non JSON
	req, _ := http.NewRequest("POST", r.web.URL+"/api/system/skip-objects/list", strings.NewReader("job=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 415 {
		t.Fatalf("form : %d", res.StatusCode)
	}
	// avec un jeton d'API configuré : le Bearer est exigé, le cookie d'un navigateur enregistré ne suffit pas
	web2 := httptest.NewServer(httpapi.NewHandlerWithOptions(r.c, httpapi.Options{APIToken: "jeton-admin-de-test"}))
	t.Cleanup(web2.Close)
	if code, _ := skipPost(t, web2.URL, "list", body, nil); code != 401 {
		t.Fatalf("sans jeton : %d", code)
	}
	if code, _ := skipPost(t, web2.URL, "list", body, map[string]string{"Authorization": "Bearer faux"}); code != 401 {
		t.Fatalf("mauvais jeton : %d", code)
	}
	if code, m := skipPost(t, web2.URL, "list", body, map[string]string{"Authorization": "Bearer jeton-admin-de-test"}); code != 200 {
		t.Fatalf("bon jeton : %d %v", code, m)
	}
	b, _ := json.Marshal(body)
	req2, _ := http.NewRequest("POST", web2.URL+"/api/system/skip-objects/list", bytes.NewReader(b))
	req2.Header.Set("Content-Type", "application/json")
	res2, err := r.human.Do(req2) // cookie de navigateur enregistré, pas de Bearer
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode == 200 {
		t.Fatal("un navigateur enregistré ne doit pas pouvoir atteindre la route")
	}
}
