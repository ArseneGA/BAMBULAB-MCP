package profiles

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixture = "testdata/vendor"

func loadFixture(t *testing.T) *Store {
	t.Helper()
	s, err := Load(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLoadFixture(t *testing.T) {
	s := loadFixture(t)
	if s.Vendor != "TST" || s.Version != "01.00.00.00" {
		t.Fatalf("vendor=%q version=%q", s.Vendor, s.Version)
	}
	if len(s.Warnings) != 1 || !strings.Contains(s.Warnings[0], "broken_noname") {
		t.Fatalf("warnings: %v", s.Warnings)
	}
	// les bases ne sont pas listées comme presets
	for _, n := range s.Names(KindMachine) {
		if strings.HasPrefix(n, "fdm_") {
			t.Fatalf("base listée: %s", n)
		}
	}
	if s.Count(KindMachine) != 5 {
		t.Fatalf("count machine = %d", s.Count(KindMachine))
	}
	// chargement par le répertoire fournisseur direct
	if _, err := Load(filepath.Join(fixture, "TST")); err != nil {
		t.Fatal(err)
	}
}

func TestResolveOverrideAndProvenance(t *testing.T) {
	s := loadFixture(t)
	r, err := s.Resolve(KindMachine, "Test T1 0.4 nozzle")
	if err != nil {
		t.Fatal(err)
	}
	wantChain := []string{"Test T1 0.4 nozzle", "fdm_tst_common", "fdm_common"}
	if strings.Join(r.Chain, ",") != strings.Join(wantChain, ",") {
		t.Fatalf("chain %v", r.Chain)
	}
	cases := map[string]string{
		"printable_height":  "Test T1 0.4 nozzle", // surchargé par la feuille
		"printable_area":    "fdm_tst_common",     // surchargé par l'intermédiaire
		"gcode_flavor":      "fdm_common",         // hérité de la racine
		"retraction_length": "fdm_tst_common",     // intermédiaire écrase racine
	}
	for k, src := range cases {
		if r.Source[k] != src {
			t.Errorf("source[%s] = %q, attendu %q", k, r.Source[k], src)
		}
	}
	if h, _ := r.String("printable_height"); h != "230" {
		t.Errorf("height %s", h)
	}
	if rl := r.Strings("retraction_length"); len(rl) != 1 || rl[0] != "1.2" {
		t.Errorf("retraction %v", rl)
	}
	if inh, _ := r.Config["inherits"].(string); inh != "" {
		t.Errorf("inherits non vidé: %q", inh)
	}
	// la config aplatie est complète : plus aucune clé ne dépend d'un parent
	b, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if _, ok := back["gcode_flavor"]; !ok {
		t.Error("clé racine absente du JSON aplati")
	}
	// Clone indépendant
	c := r.Clone()
	c.Config["gcode_flavor"] = "x"
	if v, _ := r.String("gcode_flavor"); v != "marlin" {
		t.Error("Clone partage l'état")
	}
}

func TestResolveErrors(t *testing.T) {
	s := loadFixture(t)
	if _, err := s.Resolve(KindMachine, "inexistant"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, "V", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("machine/a.json", `{"name":"a","inherits":"b"}`)
	write("machine/b.json", `{"name":"b","inherits":"c"}`)
	write("machine/c.json", `{"name":"c","inherits":"a"}`)
	write("machine/orphan.json", `{"name":"orphan","inherits":"ghost"}`)
	write("process/p.json", `{"name":"p"}`)
	write("filament/f.json", `{"name":"f"}`)
	cs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Resolve(KindMachine, "a"); !errors.Is(err, ErrCycle) {
		t.Fatalf("cycle: %v", err)
	}
	if _, err := cs.Resolve(KindMachine, "orphan"); !errors.Is(err, ErrMissingParent) {
		t.Fatalf("parent manquant: %v", err)
	}
	if _, err := Load(t.TempDir()); !errors.Is(err, ErrNoVendor) {
		t.Fatalf("répertoire vide: %v", err)
	}
}

func TestPrinters(t *testing.T) {
	s := loadFixture(t)
	ps, err := s.Printers()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].Model != "Test T1" || ps[1].Model != "Test T2" {
		t.Fatalf("printers: %+v", ps)
	}
	t1 := ps[0]
	if t1.ModelID != "T1" || len(t1.Nozzles) != 2 || t1.Nozzles[1] != 0.6 || t1.DefaultBedType != "Smooth Plate" {
		t.Fatalf("T1: %+v", t1)
	}
	if len(t1.DefaultMaterials) != 2 {
		t.Fatalf("materials %v", t1.DefaultMaterials)
	}
	if len(t1.Variants) != 2 {
		t.Fatalf("variants %+v", t1.Variants)
	}
	v := t1.Variants[0]
	if v.Nozzle != 0.4 || v.BedX != 220 || v.BedY != 180 || v.PrintableZ != 230 || v.Extruders != 1 {
		t.Fatalf("variant: %+v", v)
	}
	if v.DefaultProces != "0.20mm Std @TST" || len(v.DefaultFilmts) != 1 {
		t.Fatalf("defaults: %+v", v)
	}
	t2 := ps[1].Variants[0]
	if t2.Extruders != 2 || t2.BedX != 200 || t2.PrintableZ != 250 {
		t.Fatalf("T2: %+v", t2)
	}
	mp, err := s.MachineProfile("Test T1", 0.6)
	if err != nil || mp != "Test T1 0.6 nozzle" {
		t.Fatalf("MachineProfile = %q, %v", mp, err)
	}
	if _, err := s.MachineProfile("Test T1", 0.8); !errors.Is(err, ErrNotFound) {
		t.Fatalf("buse absente: %v", err)
	}
	if _, err := s.Printer("Nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestCompatibility(t *testing.T) {
	s := loadFixture(t)
	p04, err := s.CompatibleProcesses("Test T1 0.4 nozzle")
	if err != nil {
		t.Fatal(err)
	}
	// liste explicite + condition (modèle T1/T2, buse < 0.5)
	if strings.Join(p04, "|") != "0.16mm Cond @TST|0.20mm Std @TST" {
		t.Fatalf("0.4: %v", p04)
	}
	p06, _ := s.CompatibleProcesses("Test T1 0.6 nozzle")
	if strings.Join(p06, "|") != "0.30mm Wide @TST" {
		t.Fatalf("0.6: %v", p06)
	}
	f04, _ := s.CompatibleFilaments("Test T1 0.4 nozzle")
	if strings.Join(f04, "|") != "Test Generic|Test PLA" {
		t.Fatalf("filaments 0.4: %v", f04)
	}
	f2, _ := s.CompatibleFilaments("Test T2 0.4 nozzle")
	if strings.Join(f2, "|") != "Test Generic" {
		t.Fatalf("filaments T2: %v", f2)
	}
	ok, err := s.IsCompatible(KindProcess, "0.20mm Std @TST", "Test T1 0.6 nozzle")
	if err != nil || ok {
		t.Fatalf("0.20mm avec 0.6: ok=%v err=%v", ok, err)
	}
}

func TestEvalCondition(t *testing.T) {
	cfg := map[string]any{
		"nozzle_diameter": []any{"0.4", "0.6"},
		"printer_model":   "Bambu Lab P1S",
		"printer_notes":   "PRINTER_VENDOR_BAMBU_LAB\nPRINTER_MODEL_P1S",
		"flag":            "1",
	}
	cases := []struct {
		expr string
		want bool
	}{
		{`nozzle_diameter[0]==0.4`, true},
		{`nozzle_diameter[1]==0.4`, false},
		{`nozzle_diameter[0]!=0.2 and nozzle_diameter[1]>=0.6`, true},
		{`printer_model == "Bambu Lab P1S"`, true},
		{`printer_notes=~/.*PRINTER_VENDOR_BAMBU.*/`, true},
		{`printer_notes!~/.*A1M.*/`, true},
		{`!(flag == "1")`, false},
		{`not flag`, false},
		{`flag && nozzle_diameter[0] < 0.2 || printer_model =~ /P1S$/`, true},
		{`missing_key`, false},
		{`true`, true},
	}
	for _, c := range cases {
		got, err := EvalCondition(c.expr, cfg)
		if err != nil {
			t.Errorf("%q: %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q = %v, attendu %v", c.expr, got, c.want)
		}
	}
	for _, bad := range []string{`nozzle_diameter[0] ==`, `a ~ b`, `(a`, `"x`, `a =~ b`, `a b`} {
		if _, err := EvalCondition(bad, cfg); err == nil {
			t.Errorf("%q: erreur attendue", bad)
		}
	}
}
