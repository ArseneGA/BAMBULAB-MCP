package httpapi_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMaterialsAPI(t *testing.T) {
	e := setup(t)
	var list struct {
		Version   string `json:"version"`
		Materials []struct {
			ID    string `json:"id"`
			Known int    `json:"known_properties"`
		} `json:"materials"`
	}
	if code := e.do("GET", "/api/materials", nil, &list); code != 200 || len(list.Materials) < 8 || list.Version == "" {
		t.Fatalf("liste : %d %+v", code, list)
	}
	for _, m := range list.Materials {
		if m.Known == 0 {
			t.Errorf("%s : aucune propriété connue", m.ID)
		}
	}
	var d struct {
		ID         string                    `json:"id"`
		Properties map[string]map[string]any `json:"properties"`
		Sources    map[string]map[string]any `json:"sources"`
		Disclaimer string                    `json:"disclaimer"`
	}
	if code := e.do("GET", "/api/materials/pla", nil, &d); code != 200 || d.ID != "PLA" || d.Disclaimer == "" {
		t.Fatalf("fiche : %d %+v", code, d)
	}
	den := d.Properties["density"]
	if den["unit"] == nil || den["source"] == nil || den["level"] == nil || d.Sources[den["source"].(string)]["url"] == nil {
		t.Fatalf("une propriété doit porter unité, source, niveau et la source doit être résolue : %v", den)
	}
	var er map[string]any
	if code := e.do("GET", "/api/materials/UNOBTAINIUM", nil, &er); code != 404 || er["code"] != "not_found" {
		t.Fatalf("inconnu : %d %v", code, er)
	}
}

func TestExperimentsListMemoryAndFilaments(t *testing.T) {
	e := setup(t)
	var l struct {
		Experiments []any `json:"experiments"`
	}
	if code := e.do("GET", "/api/experiments?model_id=mod_absent", nil, &l); code != 200 || l.Experiments == nil || len(l.Experiments) != 0 {
		t.Fatalf("liste vide attendue : %d %+v", code, l)
	}
	var er map[string]any
	if code := e.do("GET", "/api/print-memory", nil, &er); code != 400 || er["code"] != "invalid" {
		t.Fatalf("mémoire sans conditions : %d %v", code, er)
	}
	if code := e.do("GET", "/api/print-memory?material_family=PLA&machine=P1S&size_mm=1,2", nil, &er); code != 400 {
		t.Fatalf("size_mm invalide : %d %v", code, er)
	}
	var mem struct {
		Hits  []any `json:"hits"`
		Query struct {
			DimsSortedMM [3]float64 `json:"dims_sorted_mm"`
		} `json:"query"`
	}
	if code := e.do("GET", "/api/print-memory?material_family=PLA&machine=P1S&size_mm=10,30,20", nil, &mem); code != 200 || mem.Hits == nil || len(mem.Hits) != 0 || mem.Query.DimsSortedMM != [3]float64{30, 20, 10} {
		t.Fatalf("mémoire : %d %+v", code, mem)
	}
	if code := e.do("GET", "/api/print-memory?experiment_id=exp_x", nil, &er); code != 404 {
		t.Fatalf("expérience inconnue : %d", code)
	}
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func keys(m map[string][]byte) []string {
	var k []string
	for n := range m {
		k = append(k, n)
	}
	return k
}

func TestDownloadExportPartsAndAnalyze(t *testing.T) {
	e := setup(t)
	cube, cyl := data(t, "cube10_ascii.stl"), data(t, "cylinder_binary.stl")
	e.write("lib/cube.stl", cube)
	e.write("lib/cyl.stl", cyl)
	var imp map[string]any
	if code := e.do("POST", "/api/import", map[string]any{"paths": []string{filepath.Join(e.root, "lib")}, "wait_seconds": 30}, &imp); code != 200 || imp["done"] != true {
		t.Fatalf("import : %d %v", code, imp)
	}
	e.c.Catalog.Idle(context.Background(), 30*time.Second)
	var res struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if code := e.do("GET", "/api/models?limit=10", nil, &res); code != 200 || len(res.Models) != 2 {
		t.Fatalf("catalogue : %d %+v", code, res)
	}
	var d2 struct {
		Files []struct {
			ID     string `json:"id"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	e.do("GET", "/api/models/"+res.Models[0].ID, nil, &d2)
	if len(d2.Files) != 1 {
		t.Fatalf("fiche : %+v", d2)
	}
	f := d2.Files[0]

	// Téléchargement : octets d'origine, empreinte annoncée.
	resp, err := http.Get(e.srv.URL + "/api/files/" + f.ID + "/download")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || sum(body) != f.SHA256 || resp.Header.Get("X-Content-SHA256") != f.SHA256 ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("download : %d %v", resp.StatusCode, resp.Header)
	}
	var er map[string]any
	if code := e.do("GET", "/api/files/file_inconnu/download", nil, &er); code != 404 {
		t.Fatalf("fichier inconnu : %d", code)
	}
	if code := e.do("GET", "/api/files/..%2f..%2fetc%2fpasswd/download", nil, &er); code != 404 && code != 400 {
		t.Fatalf("traversée : %d", code)
	}

	// Export groupé : zip valide, originaux identiques, manifeste.
	post := func(body string) (*http.Response, []byte) {
		r, err := http.Post(e.srv.URL+"/api/export", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r, b
	}
	r, zb := post(`{"model_ids":["` + res.Models[0].ID + `","` + res.Models[1].ID + `"]}`)
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export : %d %s", r.StatusCode, zb)
	}
	zr, err := zip.NewReader(bytes.NewReader(zb), int64(len(zb)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string][]byte{}
	for _, zf := range zr.File {
		rc, _ := zf.Open()
		names[zf.Name], _ = io.ReadAll(rc)
		rc.Close()
	}
	if len(names) != 3 || names["manifest.json"] == nil {
		t.Fatalf("contenu de l'archive : %v", keys(names))
	}
	got := map[string]bool{}
	for n, c := range names {
		if n != "manifest.json" {
			got[sum(c)] = true
			if strings.Contains(n, "..") || strings.HasPrefix(n, "/") {
				t.Errorf("nom d'entrée dangereux : %q", n)
			}
		}
	}
	if !got[sum(cube)] || !got[sum(cyl)] {
		t.Fatalf("originaux altérés dans l'archive : %v", keys(names))
	}
	if code := e.do("POST", "/api/export", map[string]any{"model_ids": []string{}}, &er); code != 400 {
		t.Fatalf("export vide : %d", code)
	}
	if code := e.do("POST", "/api/export", map[string]any{"model_ids": []string{"mod_inconnu"}}, &er); code != 404 {
		t.Fatalf("export inconnu : %d", code)
	}

	// Sous-parties : stables d'un appel à l'autre.
	var p1, p2 struct {
		State string `json:"state"`
		Parts []struct {
			ID    string `json:"id"`
			Kind  string `json:"kind"`
			Label string `json:"label"`
		} `json:"parts"`
	}
	if code := e.do("GET", "/api/models/"+res.Models[0].ID+"/parts", nil, &p1); code != 200 || p1.State != "ok" || len(p1.Parts) == 0 {
		t.Fatalf("parts : %d %+v", code, p1)
	}
	e.do("GET", "/api/models/"+res.Models[0].ID+"/parts", nil, &p2)
	if p1.Parts[0].Label == "" || p1.Parts[0].Label == "stl" {
		t.Fatalf("libellé parlant attendu (nom du modèle) : %+v", p1.Parts[0])
	}
	if len(p1.Parts) != len(p2.Parts) || p1.Parts[0].ID != p2.Parts[0].ID {
		t.Fatalf("sous-parties instables : %+v %+v", p1, p2)
	}

	// Analyse : unité confirmée -> nouvelle analyse (dimensions plus grandes pour cm), unité invalide refusée.
	var a0, a1 struct {
		State       string `json:"state"`
		UnitApplied string `json:"unit_applied"`
		Summary     struct {
			SizeMM struct {
				X float64 `json:"x"`
			} `json:"size_mm"`
		} `json:"summary"`
	}
	if code := e.do("POST", "/api/models/"+res.Models[0].ID+"/analyze", map[string]any{"skip_fabricability": true}, &a0); code != 200 || a0.State != "ok" || a0.Summary.SizeMM.X == 0 {
		t.Fatalf("analyse : %d %+v", code, a0)
	}
	if code := e.do("POST", "/api/models/"+res.Models[0].ID+"/analyze", map[string]any{"unit": "cm", "skip_fabricability": true}, &a1); code != 200 || a1.UnitApplied != "cm" || a1.Summary.SizeMM.X < a0.Summary.SizeMM.X*5 {
		t.Fatalf("analyse cm : %d %+v (avant %+v)", code, a1, a0)
	}
	if code := e.do("POST", "/api/models/"+res.Models[0].ID+"/analyze", map[string]any{"unit": "parsec"}, &er); code != 400 {
		t.Fatalf("unité invalide : %d %v", code, er)
	}
	if code := e.do("POST", "/api/models/mod_inconnu/analyze", map[string]any{}, &er); code != 404 {
		t.Fatalf("modèle inconnu : %d", code)
	}
}
