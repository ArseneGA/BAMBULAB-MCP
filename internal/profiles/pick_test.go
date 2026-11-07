package profiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func realStore(t *testing.T) *Store {
	t.Helper()
	dir := os.Getenv("BAMBU_PROFILES_DIR")
	if dir == "" {
		dir = `C:\Program Files\Bambu Studio\resources\profiles\BBL`
	}
	if _, err := os.Stat(filepath.Join(dir, "machine")); err != nil {
		t.Skip("profils Bambu Studio absents")
	}
	s, err := Load(dir)
	if err != nil {
		t.Skip(err)
	}
	return s
}

func TestMachineCode(t *testing.T) {
	for in, want := range map[string]string{"Bambu Lab P1S": "P1S", "Bambu Lab X1 Carbon": "X1C", "Bambu Lab A1 mini": "A1M", "Bambu Lab H2D": "H2D", "Bambu Lab A1": "A1"} {
		if got := MachineCode(in); got != want {
			t.Errorf("MachineCode(%q)=%q, attendu %q", in, got, want)
		}
	}
}

// TestRealPickPerMachine : sur les profils réels, le process et le filament choisis sont compatibles, et le
// preset propre à la machine est préféré quand il existe ; un preset d'un autre modèle est signalé.
func TestRealPickPerMachine(t *testing.T) {
	s := realStore(t)
	for _, machine := range []string{
		"Bambu Lab P1S 0.4 nozzle", "Bambu Lab P1P 0.4 nozzle", "Bambu Lab A1 0.4 nozzle",
		"Bambu Lab A1 mini 0.4 nozzle", "Bambu Lab X1 Carbon 0.4 nozzle", "Bambu Lab H2D 0.4 nozzle",
	} {
		machine := machine
		t.Run(machine, func(t *testing.T) {
			code := MachineCode(strings.TrimSuffix(strings.Split(machine, " 0.")[0], ""))
			p, err := s.PickProcess(machine)
			if err != nil {
				t.Fatal(err)
			}
			f, err := s.PickFilament(machine)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range []struct {
				k Kind
				c Choice
			}{{KindProcess, p}, {KindFilament, f}} {
				ok, err := s.IsCompatible(c.k, c.c.Name, machine)
				if err != nil || !ok {
					t.Fatalf("%s %q incompatible avec %s (%v)", c.k, c.c.Name, machine, err)
				}
				pc := presetCode(c.c.Name)
				if pc != "" && pc != code && !strings.HasPrefix(pc, code) && c.c.Note == "" {
					t.Errorf("%s %q (code %s) pour %s sans réserve affichée", c.k, c.c.Name, pc, code)
				}
				t.Logf("%-32s %-10s %-45s [%s] %s", machine, c.k, c.c.Name, c.c.Match, c.c.Note)
			}
			// la machine a un preset process propre : il doit être choisi
			if own, _ := s.Resolve(KindProcess, "0.20mm Standard @BBL "+code); own != nil {
				if ok, _ := s.IsCompatible(KindProcess, own.Name, machine); ok && presetCode(p.Name) != code {
					t.Errorf("process propre %q existe et est compatible mais %q a été choisi", own.Name, p.Name)
				}
			}
		})
	}
}
