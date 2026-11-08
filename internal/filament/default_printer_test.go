package filament_test

import "testing"

// Une seule imprimante enregistrée : get_ams_state n'exige pas printer_id (et le résultat nomme l'imprimante).
func TestAMSStateDefaultsToTheOnlyPrinter(t *testing.T) {
	r := newRig(t)
	r.setup(0)
	v := r.call("get_ams_state", map[string]any{})
	if v["printer_id"] != r.printerID {
		t.Fatalf("imprimante par défaut : %v (attendu %s)", v["printer_id"], r.printerID)
	}
	if got := r.call("get_ams_state", map[string]any{"printer_id": r.printerID}); got["printer_id"] != r.printerID {
		t.Fatalf("avec identifiant : %v", got)
	}
}
