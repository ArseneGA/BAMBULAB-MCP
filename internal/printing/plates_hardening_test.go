package printing_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

func platesRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, "")
	r.seedPrinterModel()
	r.sim = startSim(t)
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	return r
}

// Un modèle de plus de 64 Mio est refusé avant toute lecture ou analyse ; les triangles sont comptés au chargement.
func TestPlanRefusesHugeBlobsAndCountsTrianglesWhileLoading(t *testing.T) {
	r := platesRig(t)
	// 65 Mio de zéros : jamais lus, jamais analysés
	sha, size, err := r.c.Blobs.Put(bytes.NewReader(make([]byte, 65<<20)), "")
	if err != nil {
		t.Fatal(err)
	}
	mo, _, _, err := r.c.Catalog.RegisterGenerated(t.Context(), "test", newModel("gros"), "", "v1", "", []catalogFile{{Name: "gros.stl", Format: "stl", SHA256: sha, Size: size, Unit: "mm"}})
	if err != nil {
		t.Fatal(err)
	}
	e := r.callErr("plan_plate", map[string]any{"printer_id": r.printerID, "arrangement": "estimate", "items": []any{map[string]any{"model_id": mo.ID}}})
	if !strings.Contains(e, "trop volumineux") {
		t.Fatalf("blob de 65 Mio : %q", e)
	}

	// cumul : plafond abaissé à 30 triangles, trois cubes DISTINCTS de 12 triangles chacun
	defer printing.SetMaxPlanTriangles(30)()
	var items []any
	for _, n := range []string{"cube-a", "cube-b", "cube-c"} {
		items = append(items, map[string]any{"model_id": r.seedMesh(n, mesh.Cube(10))})
	}
	if e := r.callErr("plan_plate", map[string]any{"printer_id": r.printerID, "arrangement": "estimate", "items": items}); !strings.Contains(e, "plan trop lourd") {
		t.Fatalf("cumul de triangles : %q", e)
	}
	// deux seulement : accepté
	r.call("plan_plate", map[string]any{"printer_id": r.printerID, "arrangement": "estimate", "items": items[:2]})
}

// Deux execute_plate_slice concurrents sur le même plan renvoient les MÊMES impressions : aucune plaque n'est découpée deux fois.
func TestConcurrentPlateSliceYieldsTheSameJobs(t *testing.T) {
	r := platesRig(t)
	// Le test porte sur la déduplication, pas sur la découpe : sans Bambu Studio (CI Linux) la vraie découpe échoue aussitôt,
	// et une impression en échec est légitimement recréée. On garde donc la découpe « en cours » jusqu'à la fin du test.
	r.c.Registry.Register(printing.JobSlice, true, func(ctx context.Context, _ *jobs.JobCtx) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	cube := r.seedMesh("cube", mesh.Cube(40))
	plan := r.call("plan_plate", map[string]any{"printer_id": r.printerID, "arrangement": "estimate", "items": []any{map[string]any{"model_id": cube, "quantity": 2}}})["plan"].(map[string]any)
	planID := plan["id"].(string)
	const n = 6
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := r.c.Printing.ExecutePlateSlice(t.Context(), printing.PlateSliceInput{PlanID: planID})
			errs[i] = err
			if err == nil {
				for _, j := range out.Jobs {
					ids[i] += j.ID + ","
				}
			}
		}()
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("appel %d : %v", i, errs[i])
		}
		if ids[i] == "" || ids[i] != ids[0] {
			t.Fatalf("appel %d : impressions %q différentes de %q", i, ids[i], ids[0])
		}
	}
	got, err := r.c.Printing.GetPlatePlan(t.Context(), planID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Jobs) != len(got.Plates) || len(got.Jobs) != strings.Count(ids[0], ",") {
		t.Fatalf("plan.Jobs = %+v pour %d plaque(s), impressions %q", got.Jobs, len(got.Plates), ids[0])
	}
	// une plaque n'a qu'une impression en base
	var cnt int
	if err := r.c.Store.DB().QueryRow(`SELECT COUNT(*) FROM print_jobs`).Scan(&cnt); err == nil && cnt != len(got.Plates) {
		t.Fatalf("%d impressions créées pour %d plaque(s)", cnt, len(got.Plates))
	}
}
