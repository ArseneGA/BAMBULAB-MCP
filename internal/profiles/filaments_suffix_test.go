package profiles

import (
	"strings"
	"testing"
)

// « Bambu PETG Translucent » (sans suffixe) ou avec le suffixe d'une AUTRE machine (« @BBL X1C ») est résolu vers le
// préréglage compatible avec la machine choisie (P1S), jamais refusé.
func TestRealResolveFilamentWithoutOrWithOtherMachineSuffix(t *testing.T) {
	s := realStore(t)
	const p1s = "Bambu Lab P1S 0.4 nozzle"
	for _, q := range []string{"Bambu PETG Translucent", "Bambu PETG Translucent @BBL X1C", "bambu petg translucent @bbl x1c"} {
		r, err := s.ResolveFilament(p1s, q)
		if err != nil {
			t.Fatalf("%q : %v", q, err)
		}
		if r.Entry.Line != "Bambu PETG Translucent" {
			t.Errorf("%q -> %q", q, r.Entry.Line)
		}
		if ok, cerr := s.IsCompatible(KindFilament, r.Entry.Name, p1s); cerr != nil || !ok {
			t.Errorf("%q : preset %q incompatible avec la P1S (%v)", q, r.Entry.Name, cerr)
		}
	}
	if PresetBase("Bambu PETG Translucent @BBL X1C") != "Bambu PETG Translucent" {
		t.Error("PresetBase doit retirer le suffixe machine")
	}
	// un nom trop vague (plusieurs lignes également plausibles) se résout, mais en le disant
	for _, q := range []string{"PETG", "PLA", "ABS", "TPU"} {
		r, err := s.ResolveFilament(p1s, q)
		if err != nil {
			t.Fatalf("%q : %v", q, err)
		}
		t.Logf("%q -> %s | %s", q, r.Entry.Line, r.Note)
		if strings.Contains(r.Note, "ambigu") && !strings.Contains(r.Note, r.Entry.Line) {
			t.Errorf("%q : la note d'ambiguïté doit nommer le choix : %s", q, r.Note)
		}
	}
}
