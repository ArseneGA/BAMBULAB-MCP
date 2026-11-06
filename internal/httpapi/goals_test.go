package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
)

func goalsServer(t *testing.T) *httptest.Server {
	t.Helper()
	c, err := core.Open(context.Background(), core.Config{DataDir: t.TempDir(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), StudioPath: "Z:/absent/bambu-studio.exe"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	srv := httptest.NewServer(httpapi.NewHandler(c))
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, bytes.NewBufferString(body))
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

func TestGoalsEndpoints(t *testing.T) {
	srv := goalsServer(t)
	code, cat := do(t, "GET", srv.URL+"/api/goal-criteria", "")
	if code != 200 || len(cat["criteria"].([]any)) < 10 {
		t.Fatalf("catalogue : %d %v", code, cat)
	}
	code, g := do(t, "POST", srv.URL+"/api/goals", `{"original_text":"solide et rapide","criteria":[{"type":"strength","priority":2},{"type":"speed","priority":2}]}`)
	if code != 201 || g["id"] == nil || len(g["conflicts"].([]any)) != 1 || len(g["ambiguities"].([]any)) < 2 {
		t.Fatalf("objectif : %d %v", code, g)
	}
	code, back := do(t, "GET", srv.URL+"/api/goals/"+g["id"].(string), "")
	if code != 200 || back["original_text"] != "solide et rapide" {
		t.Fatalf("relecture : %d %v", code, back)
	}
	if code, _ := do(t, "GET", srv.URL+"/api/goals/inconnu", ""); code != 404 {
		t.Fatalf("inconnu : %d", code)
	}
	if code, _ := do(t, "POST", srv.URL+"/api/goals", `{"original_text":"","criteria":[]}`); code != 400 {
		t.Fatalf("invalide : %d", code)
	}
	// sans Bambu Studio : outils disponibles, erreur explicite à l'exécution
	code, e := do(t, "POST", srv.URL+"/api/profiles/modify", `{"base":{"machine":"Bambu Lab P1S 0.4 nozzle"},"modifications":[{"key":"wall_loops","value":"3"}]}`)
	if code != 503 || e["code"] != "slicer_missing" || !strings.Contains(e["error"].(string), "slicer_missing") {
		t.Fatalf("slicer_missing attendu : %d %v", code, e)
	}
	if code, _ := do(t, "GET", srv.URL+"/api/experiments/exp_x/compare", ""); code != 404 {
		t.Fatalf("expérience inconnue : %d", code)
	}
	code, e = do(t, "POST", srv.URL+"/api/variants/apply", `{"experiment_id":"exp_x","candidate_id":"c","expected_revision":"ver_x"}`)
	if code != 404 {
		t.Fatalf("apply inconnu : %d %v", code, e)
	}
	if code, _ := do(t, "POST", srv.URL+"/api/print-results", `{"experiment_id":"exp_x","candidate_id":"c","outcome":"bof"}`); code != 400 {
		t.Fatalf("résultat invalide : %d", code)
	}
	// capacités : le slicing est signalé indisponible avec la raison
	_, caps := do(t, "GET", srv.URL+"/api/capabilities", "")
	var found bool
	for _, f := range caps["features"].([]any) {
		m := f.(map[string]any)
		if m["id"] == "slicing" {
			found = m["available"] == false && strings.Contains(m["note"].(string), "slicer_missing")
		}
	}
	if !found {
		t.Fatalf("capacité slicing : %v", caps["features"])
	}
}
