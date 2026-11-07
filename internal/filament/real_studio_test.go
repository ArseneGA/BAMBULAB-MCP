package filament_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/filament"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// Tests avec un VRAI Bambu Studio (ignorés avec -short ou sans Studio) : deux pièces, deux couleurs.

func sliceTwoColors(t *testing.T) (*slicer.Slicer, *slicer.Result) {
	t.Helper()
	s := realStudio(t)
	res, err := s.Slice(context.Background(), slicer.Request{Model: twoColor3MF(t, s, 20), Machine: p1sMachine, Process: p1sProcess,
		Filaments: []string{p1sPLABasic, p1sPLAMatte}, Label: "deux-couleurs"})
	if err != nil {
		t.Fatalf("découpe à deux couleurs : %v", err)
	}
	return s, res
}

// TestRealStudioPurgeTwoColors : la purge est MESURÉE dans le G-code réel et recoupée avec l'en-tête et la matrice.
func TestRealStudioPurgeTwoColors(t *testing.T) {
	s, res := sliceTwoColors(t)
	if len(res.Plates) != 1 || len(res.Plates[0].Filaments) != 2 {
		t.Fatalf("deux filaments attendus : %+v", res.Plates)
	}
	pr, err := filament.ReadPurge(res.ArtifactPath, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("purge réelle : %+v", pr)
	if !pr.TowerEnabled || pr.Changes < 2 {
		t.Fatalf("changements de couleur attendus : %+v", pr)
	}
	if pr.FlushG <= 0 || pr.TowerG <= 0 || len(pr.PerFilament) != 2 {
		t.Fatalf("purge nulle : %+v", pr)
	}
	for _, f := range pr.PerFilament {
		if f.WasteG <= 0 || f.WasteG >= f.UsedG {
			t.Fatalf("filament %d : jeté %v sur %v", f.Slot, f.WasteG, f.UsedG)
		}
	}
	// Total = poids de la plaque annoncé par Studio (slice_info) ; lecture fidèle à l'en-tête (< 8 % d'écart).
	if math.Abs(pr.TotalG-res.Plates[0].WeightGrams) > 0.1 {
		t.Fatalf("total %v g vs slice_info %v g", pr.TotalG, res.Plates[0].WeightGrams)
	}
	if math.Abs(pr.DeviationPercent) > 8 {
		t.Fatalf("lecture du G-code trop éloignée de l'en-tête : %v %%", pr.DeviationPercent)
	}
	// Recoupement indépendant : la matrice de volumes de purge donne la purge de buse à 3 % près.
	if pr.MatrixEstimateG <= 0 || math.Abs(pr.MatrixEstimateG-pr.FlushG)/pr.FlushG > 0.03 {
		t.Fatalf("purge de buse mesurée %v g, estimée par la matrice %v g", pr.FlushG, pr.MatrixEstimateG)
	}
	if pr.WastePercent < 10 {
		t.Fatalf("deux petites pièces de deux couleurs sur la même plaque jettent beaucoup de matière : %v %%", pr.WastePercent)
	}

	// Témoin : une seule couleur, aucun changement, aucune purge.
	b, _ := mesh.STLBytes(mesh.Cube(20))
	stl := filepath.Join(t.TempDir(), "cube.stl")
	if err := os.WriteFile(stl, b, 0o644); err != nil {
		t.Fatal(err)
	}
	one, err := s.Slice(context.Background(), slicer.Request{Model: stl, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sPLABasic}})
	if err != nil {
		t.Fatal(err)
	}
	p1, err := filament.ReadPurge(one.ArtifactPath, 1)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Changes != 0 || p1.FlushG != 0 {
		t.Fatalf("une couleur ne purge pas : %+v", p1)
	}
}

// TestRealStudioPlanColorsWithPurgeCost : plan_colors sur une vraie archive à deux couleurs ; coût de purge au prix/kg des bobines.
func TestRealStudioPlanColorsWithPurgeCost(t *testing.T) {
	_, res := sliceTwoColors(t)
	r := newRig(t)
	r.setup(0)
	sha, _, err := r.c.Blobs.Put(mustOpen(t, res.ArtifactPath), "")
	if err != nil {
		t.Fatal(err)
	}
	v := r.call("validate_print_artifact", map[string]any{"artifact_id": sha, "printer_id": r.printerID})
	if str(v, "state") != "VALIDATED" {
		t.Fatalf("validation de l'archive réelle : %v", v["report"])
	}
	job := str(v, "print_job_id")
	// Bobines inscrites avec leur prix/kg : blanc en ams1:1 (20 €/kg), bleu en ams1:4 (30 €/kg).
	r.createSpool(map[string]any{"material": "PLA", "color_hex": "#FFFFFF", "price_per_kg": 20, "printer_id": r.printerID, "slot": "ams1:1"})
	r.createSpool(map[string]any{"material": "PLA", "color_hex": "#1060D0", "price_per_kg": 30, "printer_id": r.printerID, "slot": "ams1:4"})

	p := r.call("plan_colors", map[string]any{"print_job_id": job, "colors": []any{
		map[string]any{"filament_slot": 1, "color_hex": "#FFFFFF", "part": "pièce A"},
		map[string]any{"filament_slot": 2, "color_hex": "#1060D0", "part": "pièce B"}}})
	if p["complete"] != true || fmt.Sprint(p["ams_mapping"]) != "[0 3 -1 -1 -1]" {
		t.Fatalf("plan : %v", p)
	}
	purge := p["purge"].(map[string]any)
	if purge["source"] != "gcode" || purge["waste_g"].(float64) <= 5 || purge["color_changes"].(float64) < 2 {
		t.Fatalf("purge : %v", purge)
	}
	per := purge["per_filament"].([]any)
	want := per[0].(map[string]any)["waste_g"].(float64)/1000*20 + per[1].(map[string]any)["waste_g"].(float64)/1000*30
	pc := p["purge_cost"].(map[string]any)
	if pc["basis"] != "spool_price_per_kg" || pc["complete"] != true || math.Abs(pc["cost"].(float64)-want) > 0.011 {
		t.Fatalf("coût de purge : %v (attendu %.3f)", pc, want)
	}
	t.Logf("plan multicolore réel : %.1f g jetés (%.0f %%), coût %.2f", purge["waste_g"], purge["waste_percent"], pc["cost"])
	// Le fichier réel est exploitable pour la suite : les affectations du plan passent au dépôt et à la liaison humaine.
	if len(p["assignments"].([]any)) != 2 {
		t.Fatalf("assignments : %v", p["assignments"])
	}
	if n := len(r.sim.StartCommands()); n != 0 {
		t.Fatal("plan_colors ne lance rien")
	}
}

func mustOpen(t *testing.T, p string) *os.File {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
