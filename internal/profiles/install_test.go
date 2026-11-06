package profiles

import (
	"os"
	"strings"
	"testing"
)

// Tests sur l'installation locale de Bambu Studio ; ignorés si absente.
func loadInstall(t *testing.T) *Store {
	t.Helper()
	dir := os.Getenv("BAMBU_PROFILES_DIR")
	if dir == "" {
		dir = `C:\Program Files\Bambu Studio\resources\profiles\BBL`
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("profils Bambu Studio absents: %v", err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestInstallPrinters(t *testing.T) {
	s := loadInstall(t)
	ps, err := s.Printers()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) < 10 {
		t.Fatalf("seulement %d modèles", len(ps))
	}
	by := map[string]Printer{}
	for _, p := range ps {
		by[p.Model] = p
		if len(p.Variants) == 0 {
			t.Errorf("%s sans variante", p.Model)
		}
	}
	p1s := by["Bambu Lab P1S"]
	if len(p1s.Variants) == 0 {
		t.Fatal("P1S absente")
	}
	var v *Variant
	for i := range p1s.Variants {
		if p1s.Variants[i].Nozzle == 0.4 {
			v = &p1s.Variants[i]
		}
	}
	if v == nil || v.BedX != 256 || v.BedY != 256 || v.PrintableZ < 250 {
		t.Fatalf("P1S 0.4: %+v", v)
	}
	a1m := by["Bambu Lab A1 mini"]
	for _, x := range a1m.Variants {
		if x.Nozzle == 0.4 && (x.BedX != 180 || x.BedY != 180 || x.PrintableZ != 180) {
			t.Fatalf("A1 mini 0.4: %+v", x)
		}
	}
	t.Logf("%d modèles, %d machines, %d process, %d filaments (bases comprises: %d/%d/%d)",
		len(ps), len(s.Names(KindMachine)), len(s.Names(KindProcess)), len(s.Names(KindFilament)),
		s.Count(KindMachine), s.Count(KindProcess), s.Count(KindFilament))
}

func TestInstallResolveAll(t *testing.T) {
	s := loadInstall(t)
	for _, k := range []Kind{KindMachine, KindProcess, KindFilament} {
		for _, n := range s.Names(k) {
			r, err := s.Resolve(k, n)
			if err != nil {
				t.Errorf("%s %q: %v", k, n, err)
				continue
			}
			if len(r.Chain) == 0 || r.Chain[0] != n {
				t.Errorf("chaîne %q: %v", n, r.Chain)
			}
		}
	}
}

func TestInstallP1SChain(t *testing.T) {
	s := loadInstall(t)
	r, err := s.Resolve(KindMachine, "Bambu Lab P1S 0.4 nozzle")
	if err != nil {
		t.Fatal(err)
	}
	if r.Chain[len(r.Chain)-1] != "fdm_machine_common" {
		t.Errorf("racine: %v", r.Chain)
	}
	if len(r.Config) < 50 {
		t.Errorf("config trop courte: %d clés", len(r.Config))
	}
	if r.Source["printable_area"] != "fdm_bbl_3dp_001_common" {
		t.Errorf("printable_area vient de %q", r.Source["printable_area"])
	}
	procs, err := s.CompatibleProcesses("Bambu Lab P1S 0.4 nozzle")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range procs {
		if p == "0.20mm Standard @BBL X1C" {
			found = true
		}
		if strings.HasPrefix(p, "fdm_") {
			t.Errorf("base listée %s", p)
		}
	}
	if !found {
		t.Errorf("0.20mm Standard @BBL X1C absent: %v", procs)
	}
	fils, err := s.CompatibleFilaments("Bambu Lab P1S 0.4 nozzle")
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, f := range fils {
		if f == "Bambu PLA Basic @BBL P1S 0.4 nozzle" {
			found = true
		}
	}
	if !found {
		t.Errorf("PLA Basic P1S absent (%d filaments)", len(fils))
	}
	// un filament A1 mini ne convient pas à la P1S
	ok, err := s.IsCompatible(KindFilament, "Bambu PLA Basic @BBL A1M", "Bambu Lab P1S 0.4 nozzle")
	if err == nil && ok {
		t.Error("PLA Basic A1M ne devrait pas être compatible P1S")
	}
}
