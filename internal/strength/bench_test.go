package strength

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Banc d'essai (hors -short) : porte-guitare mural en J, maillage très fin (décimé pour les zones, voxélisé à pas
// adaptatif). Images écrites dans $STRENGTH_BENCH_DIR si défini.
func TestBenchGuitarHolderBigMesh(t *testing.T) {
	if testing.Short() {
		t.Skip("banc d'essai : hors -short")
	}
	prof := [][2]float64{{0, 0}, {90, 0}, {90, 40}, {80, 40}, {80, 10}, {8, 10}, {8, 140}, {0, 140}}
	for _, subs := range []int{7, 8} {
		m := extrude(prof, 40)
		for i := 0; i < subs; i++ {
			m = subdivide(m)
		}
		t0 := time.Now()
		zs := DetectZones(m)
		tZones := time.Since(t0)
		in := AssessInput{ModelID: "m", Material: "PETG", Infill: 25, Walls: 3, LayerOrientation: "z", Duration: "permanent", LoadKind: "impact",
			Loads:    []LoadIn{{Point: []float64{85, 20, 40}, RadiusMM: 8, MassKg: 4.5, Source: SourceAssumed}},
			Supports: []SupportIn{{Zone: zoneOfKind(zs, KindParoiXm), Type: "fixed", Source: SourceAssumed}}}
		t1 := time.Now()
		an, err := Run(context.Background(), m, in, nil)
		if err != nil {
			t.Fatal(err)
		}
		tRun := time.Since(t1)
		t2 := time.Now()
		pngBytes, err := StressImage(an, "porte-guitare")
		if err != nil {
			t.Fatal(err)
		}
		plan, _ := PlanImage(zs, "porte-guitare")
		t.Logf("%d triangles : zones %.1f s (analyse décimée=%v, %d tri), calcul %.1f s (voxel %.2f mm, %d cubes, %d it), rendus %.1f s ; marge ×%.2f, σmax %.1f MPa, flèche %.2f mm",
			len(m.T), tZones.Seconds(), zs.Decimated, len(zs.Mesh.T), tRun.Seconds(), an.Result.Mesh.VoxelMM, an.Result.Mesh.Elements, an.Result.Solver.Iterations,
			time.Since(t2).Seconds(), an.Result.SafetyAchieved, an.Result.MaxVonMisesMPa, an.Result.MaxDisplacementMM)
		if dir := os.Getenv("STRENGTH_BENCH_DIR"); dir != "" && subs == 7 {
			_ = os.WriteFile(filepath.Join(dir, "stress.png"), pngBytes, 0o644)
			_ = os.WriteFile(filepath.Join(dir, "plan.png"), plan, 0o644)
		}
	}
}
