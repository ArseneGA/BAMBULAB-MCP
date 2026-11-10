package filcatalog

import (
	"strings"
	"testing"
)

func mustCat(t *testing.T) *Catalog {
	t.Helper()
	c, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEmbeddedCatalogIsCoherent(t *testing.T) {
	c := mustCat(t)
	if len(c.Families) < 40 || len(c.Colors) < 300 {
		t.Fatalf("catalogue trop petit : %d gammes, %d couleurs", len(c.Families), len(c.Colors))
	}
	seen := map[string]bool{}
	for _, col := range c.Colors {
		if seen[col.ID] {
			t.Fatalf("identifiant en double %s", col.ID)
		}
		seen[col.ID] = true
		if _, ok := c.Family(col.FamilyID); !ok {
			t.Errorf("%s : gamme %s inconnue", col.ID, col.FamilyID)
		}
		if len(col.Hex) == 0 {
			t.Errorf("%s sans couleur", col.ID)
		}
		for _, h := range col.Hex {
			if NormHex(h) != h {
				t.Errorf("%s : hex %q non normalisé", col.ID, h)
			}
		}
		if col.Name["en"] == "" {
			t.Errorf("%s : nom anglais manquant", col.ID)
		}
		for lang, n := range col.Name {
			if strings.TrimSpace(n) == "" {
				t.Errorf("%s : nom %s vide (doit être omis)", col.ID, lang)
			}
		}
		if col.NameFR() == "" {
			t.Errorf("%s : aucun nom", col.ID)
		}
		if col.Confidence == "" {
			t.Errorf("%s sans niveau de confiance", col.ID)
		}
	}
	for _, f := range c.Families {
		if f.Source.Kind == "" || f.Source.Retrieved == "" || f.Source.Ref == "" || f.Confidence == "" {
			t.Errorf("%s : source ou confiance manquante", f.ID)
		}
		if f.ColorCount == 0 {
			t.Errorf("%s : aucune couleur", f.ID)
		}
	}
}

func TestKnownOfficialValues(t *testing.T) {
	c := mustCat(t)
	col, ok := c.Color("gfa00-10300")
	if !ok || col.PrimaryHex() != "#FF6A13" || col.Name["fr"] != "Orange" {
		t.Fatalf("PLA Basic orange : %+v", col)
	}
	f, _ := c.Family("PLA Silk+")
	if f == nil || f.Finish != "silk" || f.NozzleTempC == nil || *f.NozzleTempC != 230 {
		t.Fatalf("PLA Silk+ : %+v", f)
	}
	cf, _ := c.Family("bambu pa6-cf")
	if cf == nil || !cf.HardenedNozzleRequired || cf.Class != "PA" {
		t.Fatalf("PA6-CF : %+v", cf)
	}
	if b, _ := c.Family("GFA00"); b.HardenedNozzleRequired {
		t.Fatal("PLA Basic ne demande pas d'acier trempé")
	}
	if cf.DryingNote == "" {
		// PA n'a pas de plage wiki : aucune valeur inventée.
	}
	if p, _ := c.Family("PLA Basic"); p.DryingNote == "" || !strings.Contains(p.DryingNote, "wiki") {
		t.Fatalf("séchage PLA : %q", p.DryingNote)
	}
}

func TestNoInventedDryingForUnknownClass(t *testing.T) {
	c := mustCat(t)
	f, _ := c.Family("PA6-CF")
	if f.DryingNote != "" {
		t.Fatalf("séchage PA inventé : %q", f.DryingNote)
	}
}

func TestDistanceAndQuality(t *testing.T) {
	if d := Distance("#FF0000", "#ff0000"); d != 0 {
		t.Fatal(d)
	}
	if Distance("#000000", "zz") != 999 {
		t.Fatal("hex illisible = 999")
	}
	if Quality(5) != "exact" || Quality(20) != "proche" || Quality(40) != "approximatif" || Quality(90) != "aucune" {
		t.Fatal("repères de qualité")
	}
	if NormHex("ff6a13ff") != "#FF6A13" || NormHex("rouge") != "" {
		t.Fatal("NormHex")
	}
}

func TestSearchNearestColor(t *testing.T) {
	c := mustCat(t)
	ms, err := c.Search(Query{Hex: "#FF6A12", Family: "PLA Basic", Limit: 3})
	if err != nil || len(ms) != 3 {
		t.Fatalf("%v %d", err, len(ms))
	}
	if ms[0].Color.ID != "GFA00-10300" || *ms[0].DeltaE > 3 || ms[0].Quality != "exact" {
		t.Fatalf("plus proche : %+v", ms[0])
	}
	if *ms[1].DeltaE < *ms[0].DeltaE {
		t.Fatal("tri par distance")
	}
	if _, err := c.Search(Query{Hex: "nope"}); err == nil {
		t.Fatal("hex invalide accepté")
	}
	n, ok := c.Nearest("#00AE42", "")
	if !ok || n.Color.PrimaryHex() == "" || *n.DeltaE > 5 {
		t.Fatalf("vert Bambu : %+v", n)
	}
}

func TestSearchByNameFinishFamilyAndNozzle(t *testing.T) {
	c := mustCat(t)
	ms, err := c.Search(Query{Text: "Électrique", Limit: 100}) // accent et casse ignorés
	if err != nil {
		t.Fatal(err)
	}
	_ = ms
	ms, _ = c.Search(Query{Finish: "soie", Limit: 100})
	if len(ms) == 0 {
		t.Fatal("aucune couleur soie")
	}
	for _, m := range ms {
		if m.Color.Finish != "silk" {
			t.Fatalf("finition %s", m.Color.Finish)
		}
	}
	if _, err := c.Search(Query{Finish: "holographique"}); err == nil {
		t.Fatal("finition inconnue acceptée")
	}
	// Classe de matière.
	ms, _ = c.Search(Query{Family: "TPU", Limit: 100})
	if len(ms) == 0 {
		t.Fatal("TPU")
	}
	// Buse laiton 0,4 : aucune gamme à fibres.
	ms, _ = c.Search(Query{Family: "PA", Nozzle: &Nozzle{DiameterMM: "0.4"}, Limit: 100})
	for _, m := range ms {
		if f, _ := c.Family(m.Color.FamilyID); f.HardenedNozzleRequired {
			t.Fatalf("%s ne devrait pas passer sans acier trempé", f.Name)
		}
	}
	hard, _ := c.Search(Query{Family: "PA", Nozzle: &Nozzle{DiameterMM: "0.4", Hardened: true}, Limit: 100})
	if len(hard) <= len(ms) {
		t.Fatal("acier trempé : plus de gammes attendues")
	}
}

func TestCompatibleWith(t *testing.T) {
	c := mustCat(t)
	silk, _ := c.Family("PLA Silk")
	if r := silk.CompatibleWith(Nozzle{DiameterMM: "0.6"}); r.OK || len(r.Reasons) == 0 {
		t.Fatalf("Silk sur 0,6 : %+v", r)
	}
	if r := silk.CompatibleWith(Nozzle{DiameterMM: "0.4"}); !r.OK {
		t.Fatalf("Silk sur 0,4 : %+v", r)
	}
	cf, _ := c.Family("PLA-CF")
	if r := cf.CompatibleWith(Nozzle{DiameterMM: "0.4"}); r.OK {
		t.Fatal("PLA-CF sans acier trempé")
	}
	if r := cf.CompatibleWith(Nozzle{DiameterMM: "0.4", Hardened: true}); !r.OK {
		t.Fatalf("PLA-CF acier trempé : %+v", r)
	}
	if r := cf.CompatibleWith(Nozzle{DiameterMM: "0.4", Hardened: true, Kind: "High Flow"}); r.OK {
		t.Fatal("PLA-CF déconseillé sur High Flow 0,4")
	}
}

func TestMulticolorAndStats(t *testing.T) {
	c := mustCat(t)
	g, _ := c.Search(Query{Text: "Arctic Whisper"})
	if len(g) != 1 || g[0].Color.Kind != "gradient" || len(g[0].Color.Hex) != 2 {
		t.Fatalf("dégradé : %+v", g)
	}
	s := c.Stats()
	if s.Colors != len(c.Colors) || s.ColorsByClass["PLA"] == 0 || s.ByConfidence[ConfOfficialProfile] != s.Colors {
		t.Fatalf("%+v", s)
	}
}
