package httpapi_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
)

// Routes /api/gcode/* de la visionneuse de l'interface web : résumé par couche et tranches, derrière le jeton d'API ;
// absentes de la surface publique.
func TestGcodeRoutes(t *testing.T) {
	e := newPubEnv(t, httpapi.Options{})
	var g strings.Builder
	g.WriteString("; total estimated time: 1m 30s\nG90\nM83\n")
	for l := 1; l <= 6; l++ {
		fmt.Fprintf(&g, "; CHANGE_LAYER\n; Z_HEIGHT: %.2f\n; FEATURE: Outer wall\nG1 X20 Y20 F3000\nG1 X%d Y20 E1\nG1 X%d Y%d E1\n; FEATURE: Sparse infill\nG1 X30 Y30 E1\n", float64(l)*0.2, 20+l*5, 20+l*5, 20+l*5)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("Metadata/plate_1.gcode")
	_, _ = w.Write([]byte(g.String()))
	_ = zw.Close()
	sha, _, err := e.c.Blobs.Put(&buf, "")
	if err != nil {
		t.Fatal(err)
	}
	auth := map[string]string{"Authorization": "Bearer " + oauthStaticToken}

	res, body := e.do(e.main.URL, "GET", "/api/gcode/layers?artifact_id="+sha, nil, "")
	if res.StatusCode != 401 {
		t.Fatalf("sans jeton : %d", res.StatusCode)
	}
	res, body = e.do(e.main.URL, "GET", "/api/gcode/layers?artifact_id="+sha, auth, "")
	var idx struct {
		LayersTotal int       `json:"layers_total"`
		Z           []float64 `json:"z"`
		TimeSource  string    `json:"time_source"`
		TotalTimeS  float64   `json:"total_time_s"`
		Features    []string  `json:"features"`
	}
	if res.StatusCode != 200 || json.Unmarshal([]byte(body), &idx) != nil || idx.LayersTotal != 6 || len(idx.Z) != 6 || idx.TimeSource != "slicer" || idx.TotalTimeS != 90 || len(idx.Features) != 3 { // paroi, remplissage et les déplacements à vide
		t.Fatalf("layers : %d %s", res.StatusCode, body)
	}
	seen, next := 0, 1
	for i := 0; next != 0 && i < 10; i++ {
		res, body = e.do(e.main.URL, "GET", fmt.Sprintf("/api/gcode/slice?artifact_id=%s&from_layer=%d&with_travel=1", sha, next), auth, "")
		var sl struct {
			Next   int               `json:"next"`
			Done   bool              `json:"done"`
			Layers []json.RawMessage `json:"layers"`
		}
		if res.StatusCode != 200 || json.Unmarshal([]byte(body), &sl) != nil {
			t.Fatalf("slice : %d %s", res.StatusCode, body)
		}
		seen += len(sl.Layers)
		next = sl.Next
	}
	if seen != 6 {
		t.Fatalf("couches servies : %d", seen)
	}
	if res, _ := e.do(e.main.URL, "GET", "/api/gcode/slice?artifact_id="+sha+"&from_layer=99", auth, ""); res.StatusCode != 400 {
		t.Errorf("couche hors plage : %d", res.StatusCode)
	}
	if res, _ := e.do(e.main.URL, "GET", "/api/gcode/layers", auth, ""); res.StatusCode != 400 {
		t.Errorf("sans référence : %d", res.StatusCode)
	}
	if res, _ := e.do(e.main.URL, "GET", "/api/gcode/layers?artifact_id="+strings.Repeat("e", 64), auth, ""); res.StatusCode != 404 {
		t.Errorf("artefact inconnu : %d", res.StatusCode)
	}
	// jamais sur l'écouteur public
	if res, _ := e.do(e.pub.URL, "GET", "/api/gcode/layers?artifact_id="+sha, auth, ""); res.StatusCode != 404 {
		t.Errorf("surface publique : %d (attendu 404)", res.StatusCode)
	}
}
