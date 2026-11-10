package filament_test

import (
	"encoding/json"
	"testing"
)

// FB11 : get_print_status est compact par défaut (la validation complète n'est plus répétée) ; detail=true rend tout.
// Les champs nécessaires à la sécurité et à la carte de suivi restent présents en compact.
func TestFB11PrintStatusCompactByDefault(t *testing.T) {
	r := newRig(t)
	r.setup(0)
	r.c.Printing.SetValidator(fakeValidator(fil{"PLA", "#000000", 40}))
	asg := []any{assign(1, "ams1:2", "PLA Noir")}
	job := r.validated()
	r.call("upload_print_artifact", map[string]any{"print_job_id": job, "printer_id": r.printerID, "idempotency_key": "cle-fb11-compact", "plate": 1, "assignments": asg})
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})

	compact := r.call("get_print_status", map[string]any{"print_job_id": job})
	full := r.call("get_print_status", map[string]any{"print_job_id": job, "detail": true})

	cj := compact["job"].(map[string]any)
	fj := full["job"].(map[string]any)
	if compact["compact"] != true || full["compact"] == true {
		t.Fatalf("drapeau compact : %v / %v", compact["compact"], full["compact"])
	}
	// Retiré en compact, présent en complet.
	for _, k := range []string{"validation", "summary", "slice"} {
		if _, ok := cj[k]; ok {
			t.Errorf("compact : %q ne devrait plus être répété", k)
		}
	}
	if _, ok := fj["validation"]; !ok {
		t.Fatalf("detail=true doit rendre la validation complète")
	}
	if _, ok := fj["summary"]; !ok {
		t.Fatalf("detail=true doit rendre le résumé de découpe")
	}
	vb, ok := compact["validation_brief"].(map[string]any)
	if !ok || vb["ok"] != true || vb["checks"].(float64) < 1 {
		t.Fatalf("verdict de validation en bref : %v", compact["validation_brief"])
	}
	// Sécurité et carte : état, empreinte, confirmation liée, imprimante, matières, options approuvées toujours là.
	for _, k := range []string{"id", "state", "printer_id", "artifact_sha256", "confirmation_id", "plate", "assignments", "use_ams", "next"} {
		if _, ok := cj[k]; !ok {
			t.Errorf("compact : champ %q manquant", k)
		}
	}
	if cj["confirmation_id"] != cf["confirmation_id"] || cj["artifact_sha256"] != fj["artifact_sha256"] || cj["state"] != "AWAITING_PRINT_CONFIRMATION" {
		t.Fatalf("liaison de sécurité altérée en compact : %v", cj)
	}
	// Réduction : sur ce banc (validateur factice à un seul contrôle) le gain est modeste ; sur une vraie découpe la validation, le
	// résumé de plaques et les réglages figés pèsent plusieurs dizaines de Ko.
	cb, _ := json.Marshal(compact)
	fb, _ := json.Marshal(full)
	if len(cb) >= len(fb) {
		t.Fatalf("le compact devrait être plus court : %d o contre %d o", len(cb), len(fb))
	}
}
