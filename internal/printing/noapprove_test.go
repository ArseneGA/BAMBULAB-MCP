package printing_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// Modèle de menace : le modèle n'a que les outils MCP ; aucun ne doit pouvoir produire l'accord de
// lancement. On énumère TOUS les outils, on vérifie leur schéma d'entrée (aucun champ d'approbation,
// de jeton ou de nonce), puis on les appelle tous avec des arguments d'approbation forgés : la
// confirmation reste « non approuvée » et start_print refuse.
func TestNoMCPToolCanApproveAConfirmation(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	r.c.Printing.SetValidator(fakeValidator("0.4"))
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))
	art := r.fakeArtifact()
	job, _ := r.uploadAndAsk(art, "cle-depot-noapprove")
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	confID := str(cf, "confirmation_id")

	tools, err := r.cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"plate_clear", "approved", "approve", "nonce", "token", "confirm_token", "human"}
	called := 0
	for _, tl := range tools.Tools {
		low := strings.ToLower(tl.Name)
		if strings.Contains(low, "approve") || strings.Contains(low, "from_app") || low == "confirm_print" {
			t.Fatalf("outil d'approbation interdit : %s", tl.Name)
		}
		schema, _ := json.Marshal(tl.InputSchema)
		for _, f := range forbidden {
			if f == "token" && tl.Name == "apply_file_operations" {
				continue // jeton d'aperçu d'une opération sur FICHIERS (catalogue) : sans rapport avec le lancement d'une impression
			}
			if strings.Contains(string(schema), `"`+f+`"`) {
				t.Fatalf("l'outil %s expose un champ d'approbation %q dans son schéma d'entrée", tl.Name, f)
			}
		}
		if tl.Name == "start_print" || tl.Name == "cancel_print" || tl.Name == "execute_headless_slice" || tl.Name == "create_parametric_model" ||
			tl.Name == "upload_print_artifact" || strings.HasPrefix(tl.Name, "import") {
			continue // appels à effet : couverts par leurs propres tests ; ils n'acceptent de toute façon aucun champ d'approbation
		}
		// Appel avec des arguments d'approbation forgés : l'outil échoue ou ignore ; peu importe, rien ne doit être approuvé.
		_, _, _ = r.raw(tl.Name, map[string]any{"confirmation_id": confID, "print_job_id": job, "plate_clear": true,
			"approved": true, "approve": true, "nonce": "x", "token": "x"})
		called++
	}
	if called < 20 {
		t.Fatalf("seulement %d outils exercés", called)
	}
	v := r.call("get_print_confirmation_view", map[string]any{"confirmation_id": confID})
	if v["approved"] != false {
		t.Fatalf("un outil MCP a approuvé la confirmation : %v", v)
	}
	if r.callErr("start_print", map[string]any{"print_job_id": job, "confirmation_id": confID, "idempotency_key": "cle-start-noapprove"}) == "" || len(r.sim.StartCommands()) != 0 {
		t.Fatal("start_print a abouti sans approbation humaine")
	}
}
