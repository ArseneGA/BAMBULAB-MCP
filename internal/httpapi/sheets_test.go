package httpapi_test

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/sheets"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Les routes /api/models/{id}/sheet et /api/prints/{id}/sheet renvoient exactement les structures des outils (service partagé).
func TestSheetEndpoints(t *testing.T) {
	srv, c := insightsServer(t)
	ctx := context.Background()
	ts := store.FormatTime(time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC))
	if _, err := c.Store.DB().Exec(`INSERT INTO models(id,name,created_at,updated_at) VALUES('mA','Équerre',?,?),('mZ','Neuve',?,?)`, ts, ts, ts, ts); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	doc := map[string]any{"id": "jobS", "state": "COMPLETED", "printer_id": "pr1", "model_id": "mA", "plate": 1,
		"summary":    map[string]any{"plates": []slicer.PlateResult{{Index: 1, PredictedSeconds: 3600, WeightGrams: 30, Layers: 10, Filaments: []slicer.FilamentUse{{Slot: 1, Type: "PLA", Grams: 30}}}}, "printer_model": "Bambu Lab P1S", "process": "0.20mm Standard"},
		"slice":      map[string]any{"machine": "Bambu Lab P1S 0.4 nozzle", "process": "0.20mm Standard", "nozzle_mm": 0.4},
		"print":      map[string]any{"percent": 100},
		"events":     []map[string]any{{"at": start, "event": "go", "from": "START_REQUESTED", "to": "PRINTING"}, {"at": start.Add(time.Hour), "event": "fin", "from": "PRINTING", "to": "COMPLETED"}},
		"created_at": start, "updated_at": start.Add(time.Hour)}
	if err := c.Store.PutDoc(ctx, "test", "print_jobs", "jobS", doc); err != nil {
		t.Fatal(err)
	}
	if code, _ := do(t, "POST", srv.URL+"/api/prints/jobS/verdict", `{"verdict":"success"}`); code != 200 {
		t.Fatalf("verdict : %d", code)
	}

	code, part := do(t, "GET", srv.URL+"/api/models/mA/sheet", "")
	if code != 200 || part["counts"].(map[string]any)["printed"] != float64(1) || part["counts"].(map[string]any)["judged_success"] != float64(1) {
		t.Fatalf("fiche pièce : %d %v", code, part)
	}
	want, err := c.Sheets.PartSheet(ctx, sheets.PartSheetInput{ModelID: "mA"})
	if err != nil {
		t.Fatal(err)
	}
	wantRaw, _ := json.Marshal(want)
	gotRaw, _ := json.Marshal(part)
	var a, b map[string]any
	_ = json.Unmarshal(wantRaw, &a)
	_ = json.Unmarshal(gotRaw, &b)
	if aj, bj := mustJSON(a), mustJSON(b); aj != bj {
		t.Errorf("la route et le service diffèrent :\n%s\n%s", aj, bj)
	}
	if code, z := do(t, "GET", srv.URL+"/api/models/mZ/sheet?include=versions,profiles", ""); code != 200 || z["counts"].(map[string]any)["never_printed"] != true || len(z["prints"].([]any)) != 0 {
		t.Errorf("jamais imprimée : %d %v", code, z)
	}
	if code, _ := do(t, "GET", srv.URL+"/api/models/mA/sheet?include=zzz", ""); code != 400 {
		t.Errorf("section inconnue : %d", code)
	}
	if code, _ := do(t, "GET", srv.URL+"/api/models/mdl_none/sheet", ""); code != 404 {
		t.Errorf("pièce inconnue : %d", code)
	}

	code, pr := do(t, "GET", srv.URL+"/api/prints/jobS/sheet", "")
	if code != 200 || pr["verdict"].(map[string]any)["value"] != "success" || pr["profile"].(map[string]any)["frozen_note"] == nil {
		t.Fatalf("fiche impression : %d %v", code, pr)
	}
	// CA10.4 : aucune URL signée longue durée ni secret.
	raw := mustJSON(pr)
	if m := regexp.MustCompile(`bblp|C:\\|/home/|/opt/|Bearer|access_code|X-Amz|Signature=|expires=|token=`).FindString(raw); m != "" {
		t.Errorf("motif sensible %q : %s", m, raw)
	}
	if code, _ := do(t, "GET", srv.URL+"/api/prints/inconnue/sheet", ""); code != 404 {
		t.Errorf("impression inconnue : %d", code)
	}
	// Lecture seule : aucune écriture sur la route de lecture.
	if code, _ := do(t, "POST", srv.URL+"/api/prints/jobS/sheet", `{}`); code == 200 {
		t.Errorf("POST sur la fiche : %d", code)
	}
}

func mustJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
