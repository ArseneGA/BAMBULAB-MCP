package httpapi_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/projects"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func TestProjectsEndpoints(t *testing.T) {
	srv, c := insightsServer(t)
	ctx := context.Background()
	ts := store.FormatTime(time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC))
	if _, err := c.Store.DB().Exec(`INSERT INTO models(id,name,created_at,updated_at) VALUES('mA','Équerre',?,?)`, ts, ts); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	doc := map[string]any{"id": "jobP", "state": "COMPLETED", "printer_id": "pr1", "model_id": "mA", "plate": 1,
		"summary":    map[string]any{"plates": []slicer.PlateResult{{Index: 1, PredictedSeconds: 3600, WeightGrams: 30, Layers: 10, Filaments: []slicer.FilamentUse{{Slot: 1, Type: "PLA", Grams: 30}}}}, "printer_model": "Bambu Lab P1S"},
		"print":      map[string]any{"percent": 100},
		"events":     []map[string]any{{"at": start, "event": "go", "from": "START_REQUESTED", "to": "PRINTING"}, {"at": start.Add(time.Hour), "event": "fin", "from": "PRINTING", "to": "COMPLETED"}},
		"created_at": start, "updated_at": start.Add(time.Hour)}
	if err := c.Store.PutDoc(ctx, "test", "print_jobs", "jobP", doc); err != nil {
		t.Fatal(err)
	}
	p, err := c.Projects.Create(ctx, projects.CreateInput{Name: "Boîte"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Projects.AddPart(ctx, projects.PartInput{ProjectID: p.ID, ModelID: "mA", Quantity: 2}); err != nil {
		t.Fatal(err)
	}

	code, l := do(t, "GET", srv.URL+"/api/projects", "")
	if code != 200 || l["total"] != float64(1) || len(l["projects"].([]any)) != 1 {
		t.Fatalf("liste : %d %v", code, l)
	}
	if code, _ := do(t, "GET", srv.URL+"/api/projects?status=nope", ""); code != 400 {
		t.Errorf("statut inconnu : %d", code)
	}
	code, g := do(t, "GET", srv.URL+"/api/projects/"+p.ID, "")
	if code != 200 || g["progress"].(map[string]any)["units_printed"] != float64(1) || g["progress"].(map[string]any)["units_total"] != float64(2) {
		t.Fatalf("fiche : %d %v", code, g)
	}
	if code, _ := do(t, "GET", srv.URL+"/api/projects/prj_none", ""); code != 404 {
		t.Errorf("inconnu : %d", code)
	}

	code, pend := do(t, "GET", srv.URL+"/api/verdicts/pending", "")
	if code != 200 || pend["total"] != float64(1) {
		t.Fatalf("en attente : %d %v", code, pend)
	}
	code, v := do(t, "POST", srv.URL+"/api/prints/jobP/verdict", `{"verdict":"failed","reason":"warping","note":"coins"}`)
	if code != 200 || v["verdict_source"] != "human_web" || v["fail_reason"] != "warping" {
		t.Fatalf("verdict : %d %v", code, v)
	}
	for _, bad := range []string{`{"verdict":"bof"}`, `{"verdict":"failed","reason":"magie"}`, `{"verdict":"success","inconnu":1}`} {
		if code, _ := do(t, "POST", srv.URL+"/api/prints/jobP/verdict", bad); code != 400 {
			t.Errorf("%s : %d", bad, code)
		}
	}
	if code, _ := do(t, "POST", srv.URL+"/api/prints/jInconnue/verdict", `{"verdict":"success"}`); code != 400 {
		t.Errorf("impression inconnue : %d", code)
	}
	code, g = do(t, "GET", srv.URL+"/api/projects/"+p.ID, "")
	part := g["parts"].([]any)[0].(map[string]any)
	if code != 200 || part["judged_failed"] != float64(1) || !strings.Contains(part["text"].(string), "jugée ratée") {
		t.Errorf("fiche après verdict : %v", part)
	}
	if code, pend := do(t, "GET", srv.URL+"/api/verdicts/pending", ""); code != 200 || pend["total"] != float64(0) {
		t.Errorf("plus rien en attente : %d %v", code, pend)
	}
}
