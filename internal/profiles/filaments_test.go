package profiles

import (
	"errors"
	"strings"
	"testing"
)

func TestCanonTokensSynonyms(t *testing.T) {
	cases := map[string]struct {
		mat  string
		mods string
	}{
		"PETG transparent":      {"petg", "translucent"},
		"pla mat":               {"pla", "matte"},
		"PLA soie":              {"pla", "silk"},
		"PETG clear":            {"petg", "translucent"},
		"ABS":                   {"abs", ""},
		"PETG fibre de carbone": {"petg", "cf"},
		"pla haute vitesse":     {"pla", "hf"},
	}
	for in, want := range cases {
		m, mods := canonTokens(in)
		if m != want.mat || strings.Join(mods, ",") != want.mods {
			t.Errorf("%q -> %q %v, attendu %q %q", in, m, mods, want.mat, want.mods)
		}
	}
}

func TestLevenshtein(t *testing.T) {
	if levenshtein("petg", "petg") != 0 || levenshtein("pla", "plb") != 1 || levenshtein("", "abc") != 3 {
		t.Fatal("distance de Levenshtein fausse")
	}
}

// TestRealResolveFilament : sur les profils réels, « PETG transparent » donne PETG Translucent pour la P1S,
// les FR/EN équivalents aussi, une variante absente se replie avec une note, un nom absurde propose 5 noms.
func TestRealResolveFilament(t *testing.T) {
	s := realStore(t)
	const p1s = "Bambu Lab P1S 0.4 nozzle"
	for _, q := range []string{"PETG transparent", "petg translucide", "PETG clear", "Bambu PETG Translucent"} {
		r, err := s.ResolveFilament(p1s, q)
		if err != nil {
			t.Fatalf("%q : %v", q, err)
		}
		if r.Entry.Line != "Bambu PETG Translucent" {
			t.Errorf("%q -> %q (%s)", q, r.Entry.Line, r.Entry.Name)
		}
		if ok, _ := s.IsCompatible(KindFilament, r.Entry.Name, p1s); !ok {
			t.Errorf("%q : preset %q incompatible", q, r.Entry.Name)
		}
	}
	if r, err := s.ResolveFilament(p1s, "PLA mat"); err != nil || r.Entry.Line != "Bambu PLA Matte" {
		t.Errorf("PLA mat -> %+v %v", r.Entry, err)
	}
	if r, err := s.ResolveFilament(p1s, "PLA"); err != nil || !strings.Contains(r.Entry.Line, "PLA") {
		t.Errorf("PLA -> %+v %v", r.Entry, err)
	}
	// variante absente : repli signalé
	r, err := s.ResolveFilament(p1s, "PLA galaxy xyz")
	if err == nil && r.Note == "" {
		t.Errorf("repli sans note : %+v", r)
	}
	// inconnu : 5 propositions
	_, err = s.ResolveFilament(p1s, "unobtainium")
	var nf *FilamentNotFound
	if !errors.As(err, &nf) || len(nf.Closest) != 5 {
		t.Fatalf("erreur attendue avec 5 propositions : %v", err)
	}
	// liste groupable
	l, err := s.ListFilaments(p1s)
	if err != nil || len(l) < 10 {
		t.Fatalf("liste : %d %v", len(l), err)
	}
	fam := map[string]int{}
	for _, e := range l {
		fam[e.Family]++
	}
	if fam["PLA"] == 0 || fam["PETG"] == 0 {
		t.Errorf("familles : %v", fam)
	}
}
