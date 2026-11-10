package fem

import (
	"testing"
	"time"
)

func cantileverProblem(nx, ny, nz int) Problem {
	g := BoxGrid(nx, ny, nz, 1)
	var fixed []BC
	for iz := 0; iz <= nz; iz++ {
		for iy := 0; iy <= ny; iy++ {
			n := g.NodeIndex(0, iy, iz)
			fixed = append(fixed, BC{n, 0}, BC{n, 1}, BC{n, 2})
		}
	}
	cnt := (ny + 1) * (nz + 1)
	var loads []Load
	for iz := 0; iz <= nz; iz++ {
		for iy := 0; iy <= ny; iy++ {
			loads = append(loads, Load{Node: g.NodeIndex(nx, iy, iz), F: [3]float64{0, 0, -50 / float64(cnt)}})
		}
	}
	return Problem{Grid: g, E: 2000, Nu: 0.38, Fixed: fixed, Loads: loads, Tol: 1e-8}
}

// Échéance dépassée : arrêt sans erreur, solution marquée non convergée (jamais présentée comme fiable).
func TestDeadlineGivesNonConvergedSolution(t *testing.T) {
	p := cantileverProblem(60, 8, 8)
	p.Deadline = time.Now().Add(-time.Second)
	s, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Converged || !s.TimedOut || s.Iterations == 0 || s.Iterations > 30 {
		t.Errorf("convergé=%v, échéance=%v, %d itérations", s.Converged, s.TimedOut, s.Iterations)
	}
	if s.Precond == "" {
		t.Error("préconditionneur non renseigné")
	}
}

// Démarrage à chaud : repartir de la solution précédente converge en bien moins d'itérations.
func TestWarmStartConvergesFaster(t *testing.T) {
	p := cantileverProblem(60, 8, 8)
	cold, err := Solve(p)
	if err != nil || !cold.Converged {
		t.Fatalf("calcul à froid : %v", err)
	}
	p.Warm = cold
	warm, err := Solve(p)
	if err != nil || !warm.Converged {
		t.Fatalf("calcul à chaud : %v", err)
	}
	t.Logf("à froid %d itérations, à chaud %d", cold.Iterations, warm.Iterations)
	if warm.Iterations*4 > cold.Iterations {
		t.Errorf("le démarrage à chaud n'aide pas : %d / %d", warm.Iterations, cold.Iterations)
	}
}

func TestInvert3(t *testing.T) {
	m := []float64{4, 1, 0, 1, 3, 1, 0, 1, 2}
	o := append([]float64(nil), m...)
	if !invert3(m) {
		t.Fatal("singulière ?")
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			s := 0.0
			for k := 0; k < 3; k++ {
				s += o[i*3+k] * m[k*3+j]
			}
			want := 0.0
			if i == j {
				want = 1
			}
			if d := s - want; d > 1e-12 || d < -1e-12 {
				t.Errorf("(A·A⁻¹)[%d][%d] = %v", i, j, s)
			}
		}
	}
}
