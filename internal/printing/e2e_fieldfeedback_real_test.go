package printing_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// TestRealStudioBigPartsAndMaterialNames : retours terrain sur la P1S, rejoués avec le vrai Bambu Studio.
//   - 300×100 mm : refusé AVANT la découpe, avec dimensions et zone utile ;
//   - 216×236 mm : tourné de 90° par le service et découpé (la zone utile est 238×228) ;
//   - 239×141 mm : Studio refuse (-50) -> message honnête, pas le texte anglais brut ;
//   - « PETG transparent » : résolu en PETG Translucent compatible P1S ; nom inconnu : 5 propositions ;
//   - list_filaments : groupes par famille ; process/filament par défaut propres à la machine.
func TestRealStudioBigPartsAndMaterialNames(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	r := newRig(t, "")
	if r.c.StudioErr != nil {
		t.Skipf("Bambu Studio absent : %v", r.c.StudioErr)
	}
	r.sim = startSim(t)
	r.seedPrinterModel()
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	seed := func(name string, x, y, z float64) string {
		stl, err := mesh.STLBytes(mesh.Box(0, 0, 0, x, y, z))
		if err != nil {
			t.Fatal(err)
		}
		sha, size, err := r.c.Blobs.Put(strings.NewReader(string(stl)), "")
		if err != nil {
			t.Fatal(err)
		}
		m, _, _, err := r.c.Catalog.RegisterGenerated(t.Context(), "test", newModel(name), "", "v1", "", []catalogFile{{Name: name + ".stl", Format: "stl", SHA256: sha, Size: size, Unit: "mm"}})
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}

	// 1. trop grand : refus immédiat
	big := seed("trop-grand", 300, 100, 20)
	msg := r.callErr("execute_headless_slice", map[string]any{"model_id": big, "printer_id": r.printerID})
	if !strings.Contains(msg, "pièce hors zone imprimable") || !strings.Contains(msg, "300 × 100 × 20 mm") || !strings.Contains(msg, "zone utile 238 × 228 mm") || !strings.Contains(msg, "zones exclues") {
		t.Fatalf("refus avant découpe : %s", msg)
	}

	// 2. 216×236 : tournée, puis découpée par Studio
	rot := seed("a-tourner", 216, 236, 20)
	sl := r.call("execute_headless_slice", map[string]any{"model_id": rot, "printer_id": r.printerID, "idempotency_key": "cle-rot-216-236"})
	if str(sl, "job_id") == "" {
		t.Fatalf("job_id attendu : %v", sl)
	}
	st := r.waitState(str(sl, "job", "id"), 120*time.Second, "VALIDATED", "FAILED")
	if st != "VALIDATED" {
		g := r.call("get_print_status", map[string]any{"print_job_id": str(sl, "job", "id")})
		t.Fatalf("216×236 : %s %v", st, g["job"].(map[string]any)["last_error"])
	}

	// 3. 239×141 : à la limite. Studio 02.09 (serveur) la refuse (-50) ; Studio 02.08 (ce poste) l'accepte en la centrant.
	// Dans les deux cas l'utilisateur est prévenu honnêtement : réserve à la préparation, ou message du -50 reformulé.
	lim := seed("limite", 239, 141, 50)
	sl3 := r.call("execute_headless_slice", map[string]any{"model_id": lim, "printer_id": r.printerID, "idempotency_key": "cle-lim-239-141"})
	st3 := r.waitState(str(sl3, "job", "id"), 120*time.Second, "FAILED", "VALIDATED")
	g3 := r.call("get_print_status", map[string]any{"print_job_id": str(sl3, "job", "id")})
	if st3 == "FAILED" {
		le := str(g3, "job", "last_error")
		if !strings.Contains(le, "239 × 141 × 50 mm") || !strings.Contains(le, "zone utile 238 × 228 mm") || strings.Contains(le, "One of the plate is empty") {
			t.Fatalf("message du -50 : %s", le)
		}
	} else {
		warned := false
		for _, e := range g3["job"].(map[string]any)["events"].([]any) {
			if n, _ := e.(map[string]any)["note"].(string); strings.Contains(n, "pièce à la limite du plateau") && strings.Contains(n, "zone utile 238 × 228 mm") {
				warned = true
			}
		}
		if !warned {
			t.Fatalf("239×141 acceptée sans réserve à la préparation : %v", g3["job"])
		}
	}

	// 4. matières
	fl := r.call("list_filaments", map[string]any{"printer_id": r.printerID})
	fams := map[string]bool{}
	for _, f := range fl["families"].([]any) {
		fams[f.(map[string]any)["family"].(string)] = true
	}
	if !fams["PLA"] || !fams["PETG"] || fl["total"].(float64) < 10 {
		t.Fatalf("list_filaments : %v", fams)
	}
	ok := seed("pour-petg", 30, 30, 10)
	sl4 := r.call("execute_headless_slice", map[string]any{"model_id": ok, "printer_id": r.printerID, "idempotency_key": "cle-petg-transparent", "filaments": []any{"PETG transparent"}})
	jid := str(sl4, "job", "id")
	if st := r.waitState(jid, 120*time.Second, "VALIDATED", "FAILED"); st != "VALIDATED" {
		t.Fatalf("PETG transparent : %s", st)
	}
	g4 := r.call("get_print_status", map[string]any{"print_job_id": jid, "detail": true})
	fp := g4["job"].(map[string]any)["summary"].(map[string]any)["filament_presets"].([]any)
	if len(fp) != 1 || !strings.Contains(fp[0].(string), "PETG Translucent") {
		t.Fatalf("préset choisi : %v", fp)
	}
	// la couleur du profil n'est pas présentée comme réelle
	for _, p := range g4["job"].(map[string]any)["summary"].(map[string]any)["plates"].([]any) {
		for _, f := range p.(map[string]any)["filaments"].([]any) {
			if c, _ := f.(map[string]any)["color"].(string); c != "" {
				t.Fatalf("couleur du profil présentée comme réelle : %v", f)
			}
		}
	}
	bad := r.callErr("execute_headless_slice", map[string]any{"model_id": ok, "printer_id": r.printerID, "idempotency_key": "cle-inconnue-xx", "filaments": []any{"unobtainium"}})
	if !strings.Contains(bad, "plus proches") {
		t.Fatalf("erreur sans propositions : %s", bad)
	}
}
