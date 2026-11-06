package printing_test

import (
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// TestRealStudioFullChain : seed d'un modèle -> objectifs -> expérience -> variante appliquée -> découpe
// RÉELLE par Bambu Studio -> validation (archive réelle + imprimante) -> dépôt sur le simulateur ->
// confirmation humaine -> lancement -> COMPLETED. Ignoré sans Bambu Studio ou avec -short.
func TestRealStudioFullChain(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	r := newRig(t, "")
	if r.c.StudioErr != nil {
		t.Skipf("Bambu Studio absent : %v", r.c.StudioErr)
	}
	r.sim = startSim(t)
	models := r.call("list_printer_models", nil)
	found := false
	for _, m := range models["models"].([]any) {
		if mm := m.(map[string]any); mm["id"] == "p1s" {
			found = true
			if mm["build_volume"] == nil || mm["nozzle_diameters_mm"] == nil || mm["ams_supported"] != nil {
				t.Fatalf("modèle P1S semé : volume et buses attendus, AMS inconnu (nil) : %v", mm)
			}
		}
	}
	if !found {
		t.Skip("profils Bambu Studio sans P1S")
	}
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))

	// seed du modèle (cube 20 mm)
	stl, err := mesh.STLBytes(mesh.Cube(20))
	if err != nil {
		t.Fatal(err)
	}
	sha, size, err := r.c.Blobs.Put(strings.NewReader(string(stl)), "")
	if err != nil {
		t.Fatal(err)
	}
	m, ver, _, err := r.c.Catalog.RegisterGenerated(t.Context(), "test", newModel("cube20"), "", "v1", "", []catalogFile{{Name: "cube20.stl", Format: "stl", SHA256: sha, Size: size, Unit: "mm"}})
	if err != nil {
		t.Fatal(err)
	}

	// objectifs -> expérience -> variante appliquée
	g := r.call("set_manufacturing_goals", map[string]any{"original_text": "pièce rapide", "material": "PLA", "criteria": []any{map[string]any{"type": "speed", "priority": 2}}})
	exp := r.call("create_experiment", map[string]any{"model_id": m.ID, "goal_id": str(g, "id"), "candidates": 2,
		"base": map[string]any{"machine": "Bambu Lab P1S 0.4 nozzle", "process": "0.20mm Standard @BBL X1C", "filaments": []any{"Bambu PLA Basic @BBL P1S 0.4 nozzle"}}})
	var cmp map[string]any
	waitFor(t, 120*time.Second, "découpes de l'expérience", func() bool {
		cmp = r.call("compare_variants", map[string]any{"experiment_id": str(exp, "id")})
		return cmp["complete"] == true
	})
	var candID string
	for _, c := range cmp["candidates"].([]any) {
		if cc := c.(map[string]any); cc["state"] == "succeeded" && candID == "" {
			candID = cc["id"].(string)
		}
	}
	if candID == "" {
		t.Fatalf("aucune variante découpée : %v", cmp)
	}
	r.call("apply_variant", map[string]any{"experiment_id": str(exp, "id"), "candidate_id": candID, "expected_revision": ver.ID})

	// découpe réelle pour l'imprimante (la variante appliquée est reprise)
	sl := r.call("execute_headless_slice", map[string]any{"model_id": m.ID, "printer_id": r.printerID, "idempotency_key": "cle-decoupe-reelle"})
	jobID := str(sl, "job", "id")
	if str(sl, "job", "profile_revision") == "" {
		t.Fatalf("la variante appliquée n'est pas reprise : %v", sl)
	}
	r.waitState(jobID, 180*time.Second, "VALIDATED", "FAILED")
	st := r.call("get_print_status", map[string]any{"print_job_id": jobID})
	if str(st, "job", "state") != "VALIDATED" {
		t.Fatalf("découpe : %v", str(st, "job", "last_error"))
	}
	art := str(st, "job", "artifact_sha256")

	// validation avec la vraie archive
	v := r.call("validate_print_artifact", map[string]any{"print_job_id": jobID, "printer_id": r.printerID, "assignments": anyAssign(ams1)})
	rep := v["report"].(map[string]any)
	if rep["ok"] != true || rep["validator"] != "slicer.ValidateArchive" || len(rep["archive_checks"].([]any)) < 10 || len(rep["compatibility_checks"].([]any)) < 5 {
		t.Fatalf("rapport : %v", rep)
	}

	// aperçus : modèle (iso) et couche du G-code réel
	for _, args := range []map[string]any{{"model_id": m.ID}, {"print_job_id": jobID, "layer": 20}} {
		res, err := r.cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "capture_preview", Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("capture_preview %v : %v %+v", args, err, res)
		}
		ok := false
		for _, c := range res.Content {
			if ic, isImg := c.(*mcp.ImageContent); isImg && len(ic.Data) > 2000 {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("pas d'image pour %v", args)
		}
	}

	// dépôt -> question -> confirmation humaine -> lancement -> fin
	up := r.call("upload_print_artifact", map[string]any{"print_job_id": jobID, "printer_id": r.printerID, "idempotency_key": "cle-depot-reel", "plate": 1, "assignments": anyAssign(ams1)})
	if !strings.HasPrefix(str(up, "question"), "Le fichier ") || !strings.HasSuffix(str(up, "question"), " est envoyé sur la P1S. Veux-tu lancer cette impression ?") {
		t.Fatalf("question : %q", str(up, "question"))
	}
	if len(r.sim.StartCommands()) != 0 {
		t.Fatal("commande reçue après un simple dépôt")
	}
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": jobID})
	if st, _ := r.humanConfirm(str(cf, "confirm_url"), true); st != 200 {
		t.Fatal("confirmation humaine")
	}
	s := r.call("start_print", map[string]any{"print_job_id": jobID, "confirmation_id": str(cf, "confirmation_id"), "idempotency_key": "cle-start-reel"})
	if str(s, "outcome") != "printing" {
		t.Fatalf("lancement : %v", s)
	}
	r.waitState(jobID, 30*time.Second, "COMPLETED")
	if a := str(r.call("get_print_status", map[string]any{"print_job_id": jobID}), "job", "artifact_sha256"); a != art {
		t.Fatalf("empreinte changée : %s ≠ %s", a, art)
	}
}
