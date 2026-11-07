package printing_test

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

func (r *rig) seedMesh(name string, m *mesh.Mesh) string {
	r.t.Helper()
	stl, err := mesh.STLBytes(m)
	if err != nil {
		r.t.Fatal(err)
	}
	sha, size, err := r.c.Blobs.Put(strings.NewReader(string(stl)), "")
	if err != nil {
		r.t.Fatal(err)
	}
	mo, _, _, err := r.c.Catalog.RegisterGenerated(r.t.Context(), "test", newModel(name), "", "v1", "", []catalogFile{{Name: name + ".stl", Format: "stl", SHA256: sha, Size: size, Unit: "mm"}})
	if err != nil {
		r.t.Fatal(err)
	}
	return mo.ID
}

func (r *rig) preview(args map[string]any) (*mcp.CallToolResult, []byte) {
	r.t.Helper()
	res, err := r.cs.CallTool(r.t.Context(), &mcp.CallToolParams{Name: "capture_preview", Arguments: args})
	if err != nil || res.IsError {
		r.t.Fatalf("capture_preview %v : %v %+v", args, err, res)
	}
	for _, c := range res.Content {
		if ic, ok := c.(*mcp.ImageContent); ok {
			if d := os.Getenv("PLATE_PNG_DIR"); d != "" { // inspection manuelle des rendus
				_ = os.WriteFile(filepath.Join(d, fmt.Sprintf("apercu-%d.png", time.Now().UnixNano())), ic.Data, 0o644)
			}
			return res, ic.Data
		}
	}
	r.t.Fatalf("pas d'image pour %v", args)
	return nil, nil
}

// Rapport « tient / ne tient pas » AVANT découpe, réglages par pièce validés, aperçu du plateau (rangement estimé,
// sans Bambu Studio : valable partout).
func TestPlanPlateReportAndPreview(t *testing.T) {
	r := newRig(t, "")
	r.seedPrinterModel()
	r.sim = startSim(t)
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	cube := r.seedMesh("cube40", mesh.Cube(40))
	bar := r.seedMesh("barre", mesh.Box(0, 0, 0, 120, 30, 15))
	huge := r.seedMesh("enorme", mesh.Box(0, 0, 0, 400, 20, 10))
	tall := r.seedMesh("tour", mesh.Box(0, 0, 0, 20, 20, 400))

	// tout tient : 4 cubes + 2 barres sur le moins de plaques possible
	p := r.call("plan_plate", map[string]any{"printer_id": r.printerID, "arrangement": "estimate", "name": "presse", "items": []any{
		map[string]any{"model_id": cube, "quantity": 4, "settings": map[string]any{"sparse_infill_density": 100, "wall_loops": 4}},
		map[string]any{"model_id": bar, "quantity": 2, "label": "matrice", "settings": map[string]any{"sparse_infill_density": "15%", "layer_height": 0.12},
			"layer_ranges": []any{map[string]any{"from_mm": 0, "to_mm": 5, "settings": map[string]any{"layer_height": 0.1}}}},
	}})
	plan := p["plan"].(map[string]any)
	if plan["fits"] != true || plan["arrangement"] != "estimate" || len(plan["objects"].([]any)) != 6 {
		t.Fatalf("plan : %v", plan)
	}
	rep := fmt.Sprint(plan["report"])
	if !strings.Contains(rep, "TIENT : 6 pièce(s)") || !strings.Contains(rep, "Plaque 1") {
		t.Fatalf("rapport : %s", rep)
	}
	for _, o := range plan["objects"].([]any) {
		om := o.(map[string]any)
		if om["fits"] != true || om["plate"].(float64) < 1 || om["x_mm"].(float64) < 0 || om["x_mm"].(float64)+om["width_mm"].(float64) > 256 {
			t.Fatalf("objet mal placé : %v", om)
		}
	}
	first := plan["objects"].([]any)[0].(map[string]any)
	if first["settings"].(map[string]any)["sparse_infill_density"] != "100%" {
		t.Fatalf("réglage normalisé : %v", first["settings"])
	}
	// relecture
	g := r.call("get_plate_plan", map[string]any{"plan_id": plan["id"]})
	if len(g["plans"].([]any)) != 1 {
		t.Fatalf("get_plate_plan : %v", g)
	}

	// ne tient pas : une pièce trop longue, une trop haute
	bad := r.call("plan_plate", map[string]any{"printer_id": r.printerID, "arrangement": "estimate", "items": []any{
		map[string]any{"model_id": cube}, map[string]any{"model_id": huge}, map[string]any{"model_id": tall}}})["plan"].(map[string]any)
	if bad["fits"] != false || !strings.Contains(fmt.Sprint(bad["report"]), "NE TIENT PAS : enorme") || !strings.Contains(fmt.Sprint(bad["report"]), "NE TIENT PAS : tour") {
		t.Fatalf("plan impossible : %v", bad["report"])
	}
	if e := r.callErr("execute_plate_slice", map[string]any{"plan_id": bad["id"]}); !strings.Contains(e, "ne tient pas") {
		t.Fatalf("découpe d'un plan qui ne tient pas : %q", e)
	}

	// réglages invalides refusés avec la liste des clés
	if e := r.callErr("plan_plate", map[string]any{"printer_id": r.printerID, "items": []any{map[string]any{"model_id": cube, "settings": map[string]any{"vitesse": 3}}}}); !strings.Contains(e, "sparse_infill_density") {
		t.Fatalf("clé inconnue : %q", e)
	}
	if e := r.callErr("plan_plate", map[string]any{"printer_id": r.printerID, "items": []any{map[string]any{"model_id": cube, "settings": map[string]any{"sparse_infill_density": 140}}}}); !strings.Contains(e, "0 à 100") {
		t.Fatalf("densité hors plage : %q", e)
	}

	// aperçu du plateau : PNG lisible, taille raisonnable
	res, img := r.preview(map[string]any{"mode": "plate", "plan_id": plan["id"], "plate": 1})
	cfg, err := png.DecodeConfig(bytes.NewReader(img))
	if err != nil || cfg.Width < 400 || cfg.Width > 1100 || cfg.Height > 2000 || len(img) > 600_000 {
		t.Fatalf("aperçu : %v %+v %d o", err, cfg, len(img))
	}
	txt := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(txt, "matrice") || !strings.Contains(txt, "cube40") || !strings.Contains(txt, "sparse_infill_density=100%") {
		t.Fatalf("légende : %s", txt)
	}
	// mode inconnu / entrées manquantes
	if e := r.callErr("capture_preview", map[string]any{"mode": "plate"}); !strings.Contains(e, "plan_id") {
		t.Fatalf("mode plate sans source : %q", e)
	}
	if e := r.callErr("capture_preview", map[string]any{"mode": "cube"}); !strings.Contains(e, "mode") {
		t.Fatalf("mode inconnu : %q", e)
	}
}

// Débit réel : 6 pièces, 2 jeux de réglages, Bambu Studio dispose (--arrange 1) puis découpe plaque par plaque ;
// chaque impression est liée à SA plaque, l'archive ne porte que ses pièces et la matière par pièce reflète les
// réglages (100 % contre 15 %). Ignoré sans Bambu Studio ou avec -short.
func TestRealStudioPlatePlan(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	r := newRig(t, "")
	if r.c.StudioErr != nil {
		t.Skipf("Bambu Studio absent : %v", r.c.StudioErr)
	}
	r.sim = startSim(t)
	if _, err := r.c.Printing.EnsureModels(t.Context()); err != nil {
		t.Skipf("profils absents : %v", err)
	}
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))
	matrice := r.seedMesh("matrice", mesh.Box(0, 0, 0, 90, 60, 20))
	poussoir := r.seedMesh("poussoir", mesh.Cube(30))

	p := r.call("plan_plate", map[string]any{"printer_id": r.printerID, "name": "presse", "items": []any{
		map[string]any{"model_id": matrice, "quantity": 4, "label": "matrice", "settings": map[string]any{"sparse_infill_density": 100}},
		map[string]any{"model_id": poussoir, "quantity": 2, "label": "poussoir", "settings": map[string]any{"sparse_infill_density": 15}},
	}})
	plan := p["plan"].(map[string]any)
	if plan["arrangement"] != "studio" || plan["fits"] != true {
		t.Fatalf("plan : %v", plan["report"])
	}
	t.Logf("rapport :\n%s", strings.Join(strs(plan["report"]), "\n"))
	nPlates := len(plan["plates"].([]any))
	_, imgPlan := r.preview(map[string]any{"mode": "plate", "plan_id": plan["id"], "plate": 1, "width": 900})
	t.Logf("aperçu du plan : %d o", len(imgPlan))

	sl := r.call("execute_plate_slice", map[string]any{"plan_id": plan["id"], "idempotency_key": "presse-1"})
	jobs := sl["jobs"].([]any)
	if len(jobs) != nPlates {
		t.Fatalf("%d impressions pour %d plaques", len(jobs), nPlates)
	}
	// idempotence : même clé, mêmes impressions
	again := r.call("execute_plate_slice", map[string]any{"plan_id": plan["id"], "idempotency_key": "presse-1"})
	for i, j := range again["jobs"].([]any) {
		if str(j.(map[string]any), "id") != str(jobs[i].(map[string]any), "id") {
			t.Fatalf("rejeu non idempotent : %v / %v", j, jobs[i])
		}
	}
	dense, light := 0.0, 0.0
	seen := map[string]bool{}
	for i, j := range jobs {
		id := str(j.(map[string]any), "id")
		if st := r.waitState(id, 240*time.Second, "VALIDATED", "FAILED"); st != "VALIDATED" {
			t.Fatalf("plaque %d : %s %s", i+1, st, str(r.call("get_print_status", map[string]any{"print_job_id": id}), "job", "last_error"))
		}
		job := r.call("get_print_status", map[string]any{"print_job_id": id})["job"].(map[string]any)
		if int(job["plate"].(float64)) != i+1 {
			t.Fatalf("numéro de plaque : %v", job["plate"])
		}
		sum := job["summary"].(map[string]any)
		if len(sum["plates"].([]any)) != 1 || int(sum["plates"].([]any)[0].(map[string]any)["index"].(float64)) != i+1 {
			t.Fatalf("l'archive devrait porter la seule plaque %d : %v", i+1, sum["plates"])
		}
		for _, o := range sum["objects"].([]any) {
			om := o.(map[string]any)
			seen[om["name"].(string)] = true
			if strings.HasPrefix(om["name"].(string), "matrice") {
				dense += om["grams"].(float64) / 1
			}
			if strings.HasPrefix(om["name"].(string), "poussoir") {
				light += om["grams"].(float64)
			}
		}
		// validation pour l'imprimante réelle (simulateur) : plaque liée à l'impression
		v := r.call("validate_print_artifact", map[string]any{"print_job_id": id, "printer_id": r.printerID})
		if v["report"].(map[string]any)["plate"].(float64) != float64(i+1) {
			t.Fatalf("validation : plaque %v", v["report"].(map[string]any)["plate"])
		}
		// aperçu de la plaque DÉCOUPÉE : lu dans le 3MF
		res, img := r.preview(map[string]any{"mode": "plate", "print_job_id": id})
		if !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "pièces lues dans le 3MF") || len(img) < 3000 {
			t.Fatalf("aperçu archive : %s", res.Content[0].(*mcp.TextContent).Text)
		}
		// une couche du G-code (mode layers)
		r.preview(map[string]any{"mode": "layers", "print_job_id": id, "layer": 10})
	}
	if len(seen) != 6 {
		t.Fatalf("pièces retrouvées dans les archives : %v", seen)
	}
	// 4 matrices à 100 % (90x60x20) contre 2 poussoirs à 15 % (30 mm) : la matière par pièce le prouve
	perDense, perLight := dense/4, light/2
	t.Logf("matière par pièce : matrice (100 %%) %.1f g, poussoir (15 %%) %.1f g", perDense, perLight)
	if perDense < 5*perLight {
		t.Fatalf("les réglages par pièce ne se retrouvent pas dans la matière : %.2f / %.2f", perDense, perLight)
	}
}

func strs(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, fmt.Sprint(x))
	}
	return out
}
