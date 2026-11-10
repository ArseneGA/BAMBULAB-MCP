package filament_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// FB12 : une bobine non déclarée ne laisse plus le contrôle « inconnu » sans explication ; la confirmation demande le restant (grammes),
// le compare à la masse prévue + marge, et rien de tout cela ne bloque ni ne lance.
func TestFB12UndeclaredSpoolAsksRemaining(t *testing.T) {
	r := newRig(t)
	r.setup(0)
	r.c.Printing.SetValidator(fakeValidator(fil{"PLA", "#000000", 40}))
	asg := []any{assign(1, "ams1:3", "PLA Noir")} // aucune bobine inscrite à cet emplacement
	job := r.validated()

	// 1. Le contrôle filament dit POURQUOI il ne conclut pas, et quoi faire ; il ne bloque pas.
	chk := r.call("check_filament_for_print", map[string]any{"print_job_id": job, "assignments": asg})
	if chk["verdict"] != "unknown" || chk["ok"] != true {
		t.Fatalf("inconnu non bloquant attendu : %v", chk)
	}
	w := str(chk, "warning")
	for _, want := range []string{"Restant inconnu", "grammes", "remaining_g", "ne bloque pas", "filament 1"} {
		if !strings.Contains(w, want) {
			t.Fatalf("avertissement sans %q : %q", want, w)
		}
	}

	// 2. Déclaré trop bas (30 g pour 40 g) : manque ; entre le besoin et la marge : marginal ; au-dessus : ok.
	for _, c := range []struct {
		g       float64
		verdict string
	}{{30, "insufficient"}, {45, "marginal"}, {200, "ok"}} {
		o := r.call("check_filament_for_print", map[string]any{"print_job_id": job, "assignments": asg, "remaining_g": []any{map[string]any{"filament_slot": 1, "grams": c.g}}})
		sl := o["slots"].([]any)[0].(map[string]any)
		if o["verdict"] != c.verdict || sl["remaining_source"] != "declared" || sl["remaining_g"].(float64) != c.g {
			t.Fatalf("restant déclaré %v g : verdict %v attendu : %v", c.g, c.verdict, o)
		}
	}

	// 3. La confirmation : sans restant, avertissement à acquitter (alerte + ligne), avec la consigne de demander les grammes.
	up := r.call("upload_print_artifact", map[string]any{"print_job_id": job, "printer_id": r.printerID, "idempotency_key": "cle-fb12-undeclared", "plate": 1, "assignments": asg})
	if up["state"] != "AWAITING_PRINT_CONFIRMATION" && up["state"] != "UPLOADED" {
		t.Fatalf("dépôt : %v", up)
	}
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	b := cf["binding"].(map[string]any)
	if !strings.Contains(fmt.Sprint(b["alerts"]), "filament_unknown_1") {
		t.Fatalf("avertissement matière absent des alertes de confirmation : %v", b)
	}
	lines := b["filament"].([]any)
	l0 := lines[0].(map[string]any)
	if len(lines) != 1 || l0["verdict"] != "unknown" || l0["needs_remaining"] != true || l0["needed_g"].(float64) != 40 {
		t.Fatalf("ligne filament : %v", lines)
	}
	if cf["approved"] != false {
		t.Fatalf("l'avertissement ne doit rien approuver : %v", cf)
	}
	if _, body, _ := r.humanGet(str(cf, "confirm_url")); !strings.Contains(body, "filament_unknown_1") {
		t.Fatalf("avertissement absent de la page /confirm")
	}

	// 4. L'utilisateur donne le restant : même impression, nouvelle confirmation, comparaison à besoin + marge, plus d'alerte « inconnu ».
	cf2 := r.call("request_print_confirmation", map[string]any{"print_job_id": job, "remaining_g": []any{map[string]any{"tray": "ams1:3", "grams": 45}}})
	b2 := cf2["binding"].(map[string]any)
	l2 := b2["filament"].([]any)[0].(map[string]any)
	if l2["remaining_source"] != "declared" || l2["verdict"] != "marginal" || l2["needs_remaining"] == true || strings.Contains(fmt.Sprint(b2["alerts"]), "filament_unknown") {
		t.Fatalf("restant déclaré : %v", b2)
	}
	if cf2["confirmation_id"] == cf["confirmation_id"] || cf2["approved"] != false {
		t.Fatalf("une nouvelle confirmation (non approuvée) est émise : %v", cf2)
	}
	// La déclaration est enregistrée sur l'impression (information), pas dans la liaison approuvée.
	j, _ := r.c.Printing.GetJob(context.Background(), job)
	if len(j.DeclaredRemaining) != 1 || j.DeclaredRemaining[0].Grams != 45 || j.DeclaredRemaining[0].FilamentSlot != 1 {
		t.Fatalf("déclaration non enregistrée : %+v", j.DeclaredRemaining)
	}

	// 5. Une déclaration absurde est refusée ; le lancement reste un geste humain.
	for _, bad := range []map[string]any{{"filament_slot": 1, "grams": -3}, {"filament_slot": 1, "grams": 20000}, {"filament_slot": 7, "grams": 50}} {
		r.callErr("request_print_confirmation", map[string]any{"print_job_id": job, "remaining_g": []any{bad}})
	}
}
