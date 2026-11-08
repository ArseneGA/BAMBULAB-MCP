package filament_test

import (
	"strings"
	"testing"
)

// TestListSpoolsTextListsSpools : le contenu texte de list_spools liste les bobines (libellé + identifiant), pas
// seulement un compteur (seul contenu lu par le modèle quand une vue est affichée).
func TestListSpoolsTextListsSpools(t *testing.T) {
	r := newRig(t)
	r.setup(0)
	sp := r.createSpool(map[string]any{"material": "PETG", "brand": "Bambu", "name": "Translucent"})
	_, txt, isErr := r.raw("list_spools", map[string]any{})
	if isErr || !strings.Contains(txt, "Translucent") || !strings.Contains(txt, sp["id"].(string)) || !strings.Contains(txt, "\n- ") {
		t.Fatalf("le texte de list_spools ne liste pas les bobines : %q", txt)
	}
}
