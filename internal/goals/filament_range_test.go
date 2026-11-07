package goals

import "testing"

// Plages lues dans les VRAIS profils Bambu Studio installés (le test est ignoré si Studio est absent).
func TestNozzleTempRangeFromRealFilamentProfiles(t *testing.T) {
	st := installProfiles(t)
	e := newEnv(t, st, nil, false)
	for _, c := range []struct {
		name   string
		lo, hi float64
	}{
		{p1sFilament, 190, 240}, // PLA Basic : hérite de fdm_filament_common (190-240)
		{"Bambu PETG Basic @BBL P1S 0.4 nozzle", 220, 260},
		{"Bambu ABS @BBL P1S 0.4 nozzle", 240, 280},
	} {
		lo, hi, ok := e.svc.NozzleTempRange(c.name)
		if !ok {
			t.Logf("profil %q absent de cette installation", c.name)
			continue
		}
		if lo != c.lo || hi != c.hi {
			t.Errorf("%s : %v-%v, attendu %v-%v", c.name, lo, hi, c.lo, c.hi)
		}
	}
	if _, _, ok := e.svc.NozzleTempRange(p1sFilament); !ok {
		t.Fatal("le filament de référence doit avoir une plage")
	}
	if _, _, ok := e.svc.NozzleTempRange("filament inexistant"); ok {
		t.Error("profil inconnu accepté")
	}
	if _, _, ok := e.svc.NozzleTempRange(""); ok {
		t.Error("nom vide accepté")
	}
}

func TestFirstNumber(t *testing.T) {
	for _, c := range []struct {
		in any
		v  float64
		ok bool
	}{{[]any{"220"}, 220, true}, {"190", 190, true}, {[]any{}, 0, false}, {[]any{"nil"}, 0, false}, {nil, 0, false}, {[]any{3.0}, 0, false}} {
		if v, ok := firstNumber(c.in); v != c.v || ok != c.ok {
			t.Errorf("%v : %v %v", c.in, v, ok)
		}
	}
}
