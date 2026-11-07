package httpapi_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
)

var pngHead = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 1, 2, 3, 4}

func insightsServer(t *testing.T) (*httptest.Server, *core.Core) {
	t.Helper()
	c, err := core.Open(context.Background(), core.Config{DataDir: t.TempDir(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), StudioPath: "Z:/absent/bambu-studio.exe"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	srv := httptest.NewServer(httpapi.NewHandler(c))
	t.Cleanup(srv.Close)
	return srv, c
}

func seedFinishedJob(t *testing.T, c *core.Core, id string) {
	t.Helper()
	start := time.Now().UTC().Add(-3 * time.Hour)
	doc := map[string]any{
		"id": id, "state": "COMPLETED", "printer_id": "pr1", "name": id + ".gcode.3mf", "plate": 1,
		"summary": map[string]any{"plates": []map[string]any{{"index": 1, "predicted_seconds": 3600, "weight_grams": 40, "layers": 50,
			"filaments": []map[string]any{{"slot": 1, "type": "PLA", "used_g": 40}}}}, "printer_model": "Bambu Lab P1S", "filament_presets": []string{"Bambu PLA Basic"}},
		"events":     []map[string]any{{"at": start, "event": "go", "from": "START_REQUESTED", "to": "PRINTING"}, {"at": start.Add(time.Hour), "event": "end", "from": "PRINTING", "to": "COMPLETED"}},
		"created_at": start, "updated_at": start.Add(time.Hour),
	}
	if err := c.Store.PutDoc(context.Background(), "test", "print_jobs", id, doc); err != nil {
		t.Fatal(err)
	}
}

func raw(t *testing.T, method, url, ctype string, body []byte) (int, http.Header, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, url, bytes.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, b
}

func TestInsightsCostAndSettingsEndpoints(t *testing.T) {
	srv, _ := insightsServer(t)
	code, st := do(t, "GET", srv.URL+"/api/cost/settings", "")
	if code != 200 || st["using_defaults"] != true || st["currency"] != "EUR" || !strings.Contains(st["note"].(string), "INDICATIFS") {
		t.Fatalf("réglages par défaut : %d %v", code, st)
	}
	code, st = do(t, "PUT", srv.URL+"/api/cost/settings", `{"electricity_per_kwh":0.3,"filament_per_kg":{"PLA":18},"machine_wear_per_hour":0.2}`)
	if code != 200 || st["electricity_per_kwh"] != 0.3 || st["using_defaults"] != false {
		t.Fatalf("PUT : %d %v", code, st)
	}
	if code, _ := do(t, "PUT", srv.URL+"/api/cost/settings", `{"electricity_per_kwh":-1}`); code != 400 {
		t.Errorf("kWh négatif : %d", code)
	}
	if code, _ := do(t, "PUT", srv.URL+"/api/cost/settings", `{"champ_inconnu":1}`); code != 400 {
		t.Errorf("champ inconnu : %d", code)
	}
	code, est := do(t, "POST", srv.URL+"/api/cost/estimate", `{"printer_model":"p1s","duration_minutes":120,"filaments":[{"material":"PLA","grams":100}]}`)
	if code != 200 {
		t.Fatalf("estimation : %d %v", code, est)
	}
	e := est["estimate"].(map[string]any)
	if !strings.HasPrefix(e["label"].(string), "ESTIMATION") || e["filament_total"] != 1.8 || len(e["assumptions"].([]any)) < 4 {
		t.Errorf("estimation : %v", e)
	}
	if code, _ := do(t, "POST", srv.URL+"/api/cost/estimate", `{"duration_minutes":120}`); code != 400 {
		t.Errorf("sans matière : %d", code)
	}
	if code, _ := do(t, "POST", srv.URL+"/api/cost/estimate", `{"print_job_id":"nope"}`); code != 404 {
		t.Errorf("impression inconnue : %d", code)
	}
}

func TestInsightsJournalPhotoDashboardEndpoints(t *testing.T) {
	srv, c := insightsServer(t)
	seedFinishedJob(t, c, "jobH")
	code, list := do(t, "GET", srv.URL+"/api/print-log", "")
	if code != 200 || list["total"] != float64(1) || list["created_from_finished_prints"] != float64(1) {
		t.Fatalf("liste : %d %v", code, list)
	}
	entry := list["entries"].([]any)[0].(map[string]any)
	id := entry["id"].(string)
	if entry["print_job_id"] != "jobH" || entry["outcome"] != "success" || entry["actual_cost"] == nil {
		t.Errorf("fiche : %v", entry)
	}
	code, upd := do(t, "PATCH", srv.URL+"/api/print-log/"+id, `{"rating":4,"comment":"propre","defects":["stringing"]}`)
	if code != 200 || upd["rating"] != float64(4) {
		t.Fatalf("PATCH : %d %v", code, upd)
	}
	if code, _ := do(t, "PATCH", srv.URL+"/api/print-log/"+id, `{"rating":12}`); code != 400 {
		t.Errorf("note 12 : %d", code)
	}
	if code, _ := do(t, "PATCH", srv.URL+"/api/print-log/"+id, `{"id":"autre","rating":3}`); code != 400 {
		t.Errorf("id différent : %d", code)
	}
	if code, _ := do(t, "PATCH", srv.URL+"/api/print-log/inconnu", `{"rating":3}`); code != 404 {
		t.Errorf("fiche inconnue : %d", code)
	}
	// photo : corps brut
	code, _, body := raw(t, "POST", srv.URL+"/api/print-log/"+id+"/photo", "image/png", pngHead)
	if code != 201 || !strings.Contains(string(body), `"photo"`) {
		t.Fatalf("photo : %d %s", code, body)
	}
	sha := strings.Split(strings.Split(string(body), `"photo":"`)[1], `"`)[0]
	code, hdr, got := raw(t, "GET", srv.URL+"/api/print-log/"+id+"/photos/"+sha, "", nil)
	if code != 200 || hdr.Get("Content-Type") != "image/png" || hdr.Get("X-Content-Type-Options") != "nosniff" || !bytes.Equal(got, pngHead) {
		t.Errorf("lecture photo : %d %v", code, hdr)
	}
	if code, _, _ := raw(t, "POST", srv.URL+"/api/print-log/"+id+"/photo", "image/svg+xml", []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")); code != 400 {
		t.Errorf("SVG : %d", code)
	}
	if code, _, _ := raw(t, "POST", srv.URL+"/api/print-log/inconnu/photo", "image/png", pngHead); code != 404 {
		t.Errorf("fiche inconnue : %d", code)
	}
	if code, _, _ := raw(t, "GET", srv.URL+"/api/print-log/"+id+"/photos/"+strings.Repeat("0", 64), "", nil); code != 404 {
		t.Errorf("photo étrangère : %d", code)
	}
	// tableau de bord + graphe
	code, d := do(t, "GET", srv.URL+"/api/usage/dashboard", "")
	if code != 200 || d["totals"].(map[string]any)["prints"] != float64(1) || !strings.Contains(d["label"].(string), "ESTIMATIONS") {
		t.Fatalf("tableau de bord : %d %v", code, d)
	}
	if code, _ := do(t, "GET", srv.URL+"/api/usage/dashboard?period=year", ""); code != 400 {
		t.Errorf("période : %d", code)
	}
	code, hdr, png := raw(t, "GET", srv.URL+"/api/usage/chart.png", "", nil)
	if code != 200 || hdr.Get("Content-Type") != "image/png" || !bytes.HasPrefix(png, pngHead[:8]) {
		t.Errorf("graphe : %d %v", code, hdr)
	}
	// ajustements et post-traitement
	code, adj := do(t, "POST", srv.URL+"/api/print-log/adjustments", `{"log_id":"`+id+`"}`)
	if code != 200 || adj["automatic"] != false || len(adj["proposals"].([]any)) != 1 {
		t.Errorf("ajustements : %d %v", code, adj)
	}
	if code, _ := do(t, "POST", srv.URL+"/api/print-log/adjustments", `{}`); code != 400 {
		t.Errorf("sans contexte : %d", code)
	}
	code, pp := do(t, "POST", srv.URL+"/api/post-processing", `{"material":"ABS","finish":"lisse"}`)
	if code != 200 || pp["family"] != "ABS" || len(pp["steps"].([]any)) < 3 {
		t.Errorf("post-traitement : %d %v", code, pp)
	}
	if code, _ := do(t, "POST", srv.URL+"/api/post-processing", `{"material":"bois"}`); code != 400 {
		t.Errorf("matière inconnue : %d", code)
	}
}
