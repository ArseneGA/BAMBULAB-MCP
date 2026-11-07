package filament_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func (r *rig) httpDo(t *testing.T, method, path string, body map[string]any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader = http.NoBody
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, r.web.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return res.StatusCode, m
}

// TestHTTPRoutes : l'interface web et le calcul de coût passent par l'API JSON ; aucune route ne lance ni n'approuve.
func TestHTTPRoutes(t *testing.T) {
	r := newRig(t)
	r.setup(0)
	r.c.Printing.SetValidator(fakeValidator(fil{"PLA", "#FFFFFF", 12}))

	// Bobines : création, lecture, modification, prix (point de lecture du chantier coût).
	st, sp := r.httpDo(t, "POST", "/api/spools", map[string]any{"material": "PLA", "name": "Blanc", "color_hex": "#FFFFFF", "price_per_kg": 18.5,
		"printer_id": r.printerID, "slot": "ams1:1", "remaining_g": 500})
	if st != 201 || sp["id"] == nil || sp["remaining_source"] != "human" {
		t.Fatalf("création : %d %v", st, sp)
	}
	id := sp["id"].(string)
	if st, m := r.httpDo(t, "GET", "/api/spools?printer_id="+r.printerID, nil); st != 200 || len(m["spools"].([]any)) != 1 || m["alert_margin"] == nil {
		t.Fatalf("liste : %d %v", st, m)
	}
	if st, m := r.httpDo(t, "PATCH", "/api/spools/"+id, map[string]any{"price_per_kg": 25}); st != 200 || m["price_per_kg"].(float64) != 25 {
		t.Fatalf("modification : %d %v", st, m)
	}
	if st, m := r.httpDo(t, "GET", "/api/spools/"+id+"/price", nil); st != 200 || m["price_per_kg"].(float64) != 25 || m["spool_id"] != id {
		t.Fatalf("prix par bobine : %d %v", st, m)
	}
	if st, m := r.httpDo(t, "GET", "/api/filament/price?printer_id="+r.printerID+"&slot=ams1:1", nil); st != 200 || m["price_per_kg"].(float64) != 25 {
		t.Fatalf("prix par emplacement : %d %v", st, m)
	}
	if st, _ := r.httpDo(t, "GET", "/api/filament/price?printer_id="+r.printerID+"&slot=ams1:2", nil); st != 404 {
		t.Fatalf("emplacement sans bobine : %d", st)
	}
	if st, _ := r.httpDo(t, "GET", "/api/spools/sp_inconnue", nil); st != 404 {
		t.Fatalf("bobine inconnue : %d", st)
	}
	if st, m := r.httpDo(t, "POST", "/api/spools", map[string]any{"brand": "x"}); st != 400 || m["code"] != "invalid" {
		t.Fatalf("création invalide : %d %v", st, m)
	}
	if st, m := r.httpDo(t, "POST", "/api/spools", map[string]any{"material": "PLA", "printer_id": r.printerID, "slot": "ams1:1"}); st != 409 || m["code"] != "conflict" {
		t.Fatalf("emplacement pris : %d %v", st, m)
	}
	if st, _ := r.httpDo(t, "POST", "/api/spools", map[string]any{"material": "PLA", "inconnu": 1}); st != 400 {
		t.Fatalf("champ inconnu : %d", st)
	}

	// AMS, marge, vérification, plan.
	if st, m := r.httpDo(t, "GET", "/api/printers/"+r.printerID+"/ams", nil); st != 200 || m["ams_present"] != true || len(m["trays"].([]any)) != 4 {
		t.Fatalf("AMS : %d %v", st, m)
	}
	if st, _ := r.httpDo(t, "GET", "/api/printers/prn_inconnue/ams", nil); st != 404 {
		t.Fatalf("imprimante inconnue : %d", st)
	}
	if st, m := r.httpDo(t, "PUT", "/api/filament/settings", map[string]any{"margin_percent": 15, "margin_grams": 3}); st != 200 || m["margin_percent"].(float64) != 15 {
		t.Fatalf("réglages : %d %v", st, m)
	}
	if st, _ := r.httpDo(t, "PUT", "/api/filament/settings", map[string]any{"margin_percent": -5, "margin_grams": 3}); st != 400 {
		t.Fatalf("marge invalide : %d", st)
	}
	job := r.validated()
	if st, m := r.httpDo(t, "POST", "/api/filament/check", map[string]any{"print_job_id": job, "assignments": []any{assign(1, "ams1:1", "PLA Blanc")}}); st != 200 || m["verdict"] != "ok" {
		t.Fatalf("check : %d %v", st, m)
	}
	if st, m := r.httpDo(t, "POST", "/api/filament/plan-colors", map[string]any{"print_job_id": job}); st != 200 || m["complete"] != true {
		t.Fatalf("plan : %d %v", st, m)
	}

	// File d'attente par l'API : mise en file, liste, ordre, retrait.
	st, e := r.httpDo(t, "POST", "/api/print-queue", map[string]any{"print_job_id": job, "assignments": []any{assign(1, "ams1:1", "PLA Blanc")}})
	if st != 201 || e["id"] == nil {
		t.Fatalf("queue : %d %v", st, e)
	}
	qid := e["id"].(string)
	waitFor(t, 30*time.Second, "élément prêt", func() bool {
		_, m := r.httpDo(t, "GET", "/api/print-queue", nil)
		q := m["queue"].([]any)
		return len(q) == 1 && q[0].(map[string]any)["state"] == "ready"
	})
	if st, m := r.httpDo(t, "POST", "/api/print-queue/reorder", map[string]any{"queue_ids": []any{qid}}); st != 200 || len(m["queue"].([]any)) != 1 {
		t.Fatalf("reorder : %d %v", st, m)
	}
	if st, m := r.httpDo(t, "POST", "/api/print-queue/"+qid+"/confirmation", nil); st != 200 || !strings.Contains(fmt.Sprint(m["confirm_url"]), "/confirm/") {
		t.Fatalf("confirmation : %d %v", st, m)
	}
	if st, _ := r.httpDo(t, "DELETE", "/api/print-queue/"+qid, nil); st != 200 {
		t.Fatalf("retrait : %d", st)
	}
	if st, _ := r.httpDo(t, "DELETE", "/api/print-queue/"+qid, nil); st != 404 {
		t.Fatalf("retrait répété : %d", st)
	}
	if st, m := r.httpDo(t, "GET", "/api/spools/usage?print_job_id="+job, nil); st != 200 || len(m["usage"].([]any)) != 0 {
		t.Fatalf("usage : %d %v", st, m)
	}
	// Rien n'a été lancé par toutes ces routes.
	if n := len(r.sim.StartCommands()); n != 0 {
		t.Fatalf("%d commande(s) de lancement", n)
	}
	// Aucune route d'approbation ou de lancement sous /api/print-queue ni /api/spools.
	for _, p := range []string{"/api/print-queue/start", "/api/print-queue/" + qid + "/start", "/api/print-queue/" + qid + "/approve", "/api/spools/start"} {
		if st, _ := r.httpDo(t, "POST", p, map[string]any{"plate_clear": true}); st < 400 || st == 500 {
			t.Fatalf("%s ne doit pas exister : %d", p, st)
		}
	}
}
