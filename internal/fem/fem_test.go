package fem

import (
	"math"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// cantilever : poutre pleine nx×ny×nz cubes de côté h, encastrée en x = 0, force P (N) répartie sur l'extrémité libre.
func cantilever(nx, ny, nz int, h, P, E, nu float64) (*Solution, float64, error) {
	g := BoxGrid(nx, ny, nz, h)
	var fixed []BC
	for iz := 0; iz <= nz; iz++ {
		for iy := 0; iy <= ny; iy++ {
			n := g.NodeIndex(0, iy, iz)
			fixed = append(fixed, BC{n, 0}, BC{n, 1}, BC{n, 2})
		}
	}
	var loads []Load
	cnt := (ny + 1) * (nz + 1)
	for iz := 0; iz <= nz; iz++ {
		for iy := 0; iy <= ny; iy++ {
			loads = append(loads, Load{Node: g.NodeIndex(nx, iy, iz), F: [3]float64{0, 0, -P / float64(cnt)}})
		}
	}
	s, err := Solve(Problem{Grid: g, E: E, Nu: nu, Fixed: fixed, Loads: loads})
	if err != nil {
		return nil, 0, err
	}
	tip := 0.0
	for iz := 0; iz <= nz; iz++ {
		for iy := 0; iy <= ny; iy++ {
			tip += s.U[s.Compact(g.NodeIndex(nx, iy, iz))*3+2]
		}
	}
	return s, tip / float64(cnt), nil
}

func maxTopStress(s *Solution, h float64) float64 {
	g := s.Grid
	best := 0.0
	for c, full := range s.Nodes {
		ix, _, iz := g.NodeIJK(int(full))
		if iz == g.NZ && ix >= 1 { // la couche de nœuds de l'encastrement porte l'artefact de la paroi rigide
			if v := math.Abs(s.Stress[c*6]); v > best {
				best = v
			}
		}
	}
	return best
}

// Poutre console : flèche et contrainte de flexion maximale contre la résistance des matériaux (Timoshenko).
func TestCantileverVsBeamTheory(t *testing.T) {
	const L, b, hh, P, E, nu = 100.0, 10.0, 10.0, 100.0, 2000.0, 0.35
	I := b * hh * hh * hh / 12
	G := E / (2 * (1 + nu))
	thDefl := P*L*L*L/(3*E*I) + P*L/((5.0/6)*G*b*hh)
	thSigma := 6 * P * L / (b * hh * hh)
	for _, pitch := range []float64{2.5, 1.25} { // 4 puis 8 éléments dans l'épaisseur
		n := int(hh / pitch)
		s, tip, err := cantilever(int(L/pitch), n, n, pitch, P, E, nu)
		if err != nil {
			t.Fatal(err)
		}
		if !s.Converged {
			t.Fatalf("pas %.2f : non convergé (%d it, résidu %.2e)", pitch, s.Iterations, s.Residual)
		}
		defl := math.Abs(tip)
		sig := maxTopStress(s, pitch)
		t.Logf("pas %.2f mm : flèche %.4f mm (théorie %.4f, écart %+.1f %%), σmax %.2f MPa (théorie %.2f, écart %+.1f %%), %d it",
			pitch, defl, thDefl, 100*(defl/thDefl-1), sig, thSigma, 100*(sig/thSigma-1), s.Iterations)
		if math.Abs(defl/thDefl-1) > 0.15 {
			t.Errorf("flèche hors ±15 %% : %.4f vs %.4f", defl, thDefl)
		}
		if math.Abs(sig/thSigma-1) > 0.15 {
			t.Errorf("contrainte hors ±15 %% : %.2f vs %.2f", sig, thSigma)
		}
	}
}

// Convergence en maillage : la flèche d'une console se rapproche de la solution de Timoshenko quand le pas diminue
// (2, 4 puis 8 cubes dans l'épaisseur), et reste dans ±15 % dès 2 cubes grâce aux modes de Wilson.
func TestCantileverConvergence(t *testing.T) {
	const L, hh, P, E, nu = 80.0, 10.0, 50.0, 2000.0, 0.35
	I := hh * hh * hh * hh / 12
	G := E / (2 * (1 + nu))
	th := P*L*L*L/(3*E*I) + P*L/((5.0/6)*G*hh*hh)
	var errs []float64
	for _, pitch := range []float64{5, 2.5, 1.25} {
		n := int(hh / pitch)
		_, tip, err := cantilever(int(L/pitch), n, n, pitch, P, E, nu)
		if err != nil {
			t.Fatal(err)
		}
		e := math.Abs(-tip/th - 1)
		errs = append(errs, e)
		if e > 0.15 {
			t.Errorf("pas %.2f : écart %.1f %% > 15 %%", pitch, 100*e)
		}
	}
	t.Logf("écarts de flèche par pas 5 / 2,5 / 1,25 mm : %.1f %% / %.1f %% / %.1f %%", 100*errs[0], 100*errs[1], 100*errs[2])
	if errs[2] > 0.03 || errs[2] > errs[0]+1e-3 {
		t.Errorf("pas de convergence : %v", errs)
	}
}

// Traction simple : σ = F/A uniforme, allongement FL/(EA).
func TestSimpleTension(t *testing.T) {
	const L, w, P, E, nu = 32.0, 8.0, 800.0, 3000.0, 0.35
	g := BoxGrid(16, 4, 4, 2.0)
	var fixed []BC
	for iz := 0; iz <= 4; iz++ {
		for iy := 0; iy <= 4; iy++ {
			fixed = append(fixed, BC{g.NodeIndex(0, iy, iz), 0})
		}
	}
	fixed = append(fixed, BC{g.NodeIndex(0, 0, 0), 1}, BC{g.NodeIndex(0, 0, 0), 2}, BC{g.NodeIndex(0, 4, 0), 2}) // modes rigides
	var loads []Load
	for iz := 0; iz <= 4; iz++ {
		for iy := 0; iy <= 4; iy++ {
			wy, wz := 1.0, 1.0 // poids consistants : bord = 1/2, coin = 1/4
			if iy == 0 || iy == 4 {
				wy = 0.5
			}
			if iz == 0 || iz == 4 {
				wz = 0.5
			}
			loads = append(loads, Load{g.NodeIndex(16, iy, iz), [3]float64{P / 16 * wy * wz, 0, 0}})
		}
	}
	s, err := Solve(Problem{Grid: g, E: E, Nu: nu, Fixed: fixed, Loads: loads})
	if err != nil {
		t.Fatal(err)
	}
	sig := 0.0
	for c := range s.Nodes {
		sig += s.Stress[c*6]
	}
	sig /= float64(len(s.Nodes))
	want := P / (w * w)
	if math.Abs(sig/want-1) > 0.02 {
		t.Errorf("σ moyenne %.3f MPa, attendu %.3f", sig, want)
	}
	du := s.U[s.Compact(g.NodeIndex(16, 2, 2))*3]
	if wantU := P * L / (E * w * w); math.Abs(du/wantU-1) > 0.03 {
		t.Errorf("allongement %.5f mm, attendu %.5f", du, wantU)
	}
	// équilibre : somme des réactions = -P
	rx := 0.0
	for c := range s.Nodes {
		rx += s.Reaction[c*3]
	}
	if math.Abs(rx+P) > 1e-3*P {
		t.Errorf("réaction totale %.3f N, attendu %.3f", rx, -P)
	}
}

// Une pièce flottante (sans appui) est écartée, sans fausser le reste.
func TestFloatingPartDropped(t *testing.T) {
	g := BoxGrid(10, 2, 2, 1)
	for iz := 0; iz < 2; iz++ {
		for iy := 0; iy < 2; iy++ {
			g.Solid[g.Cell(5, iy, iz)] = false // coupe la barre en deux
		}
	}
	var fixed []BC
	for iz := 0; iz <= 2; iz++ {
		for iy := 0; iy <= 2; iy++ {
			n := g.NodeIndex(0, iy, iz)
			fixed = append(fixed, BC{n, 0}, BC{n, 1}, BC{n, 2})
		}
	}
	s, err := Solve(Problem{Grid: g, E: 2000, Nu: 0.35, Fixed: fixed, Loads: []Load{{g.NodeIndex(4, 1, 1), [3]float64{0, 0, -10}}}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Dropped != 8*4 { // 4 colonnes x 2 x 2 = 16 éléments (colonnes 6..9)
		if s.Dropped != 16 {
			t.Errorf("éléments écartés = %d", s.Dropped)
		}
	}
	// charge sur la partie flottante : refus
	if _, err := Solve(Problem{Grid: g, E: 2000, Nu: 0.35, Fixed: fixed, Loads: []Load{{g.NodeIndex(9, 1, 1), [3]float64{0, 0, -10}}}}); err == nil {
		t.Error("charge sur une pièce flottante : refus attendu")
	}
}

// Voxélisation d'un cube maillé : volume et nombre de cubes.
func TestVoxelizeBox(t *testing.T) {
	m := boxMesh(20, 10, 6)
	g, leaky, err := Voxelize(m, 2)
	if err != nil || leaky != 0 {
		t.Fatalf("err=%v leaky=%d", err, leaky)
	}
	if g.SolidCount() != 10*5*3 {
		t.Errorf("cubes pleins %d, attendu %d", g.SolidCount(), 150)
	}
	pl := ChoosePitch(m, 150_000, 4_000_000)
	if math.Abs(pl.H-math.Cbrt(20*10*6.0/150000)) > 1e-9 {
		t.Errorf("pas %v", pl.H)
	}
	// plafond de la boîte englobante : on force un très petit plafond
	capped := ChoosePitch(m, 150_000, 1000)
	if !capped.Capped || capped.CellsTotal > 1000 {
		t.Errorf("plafond non respecté : %+v", capped)
	}
}

func boxMesh(x, y, z float64) *mesh.Mesh {
	P := func(a, b, c float64) mesh.Vec3 { return mesh.Vec3{X: a, Y: b, Z: c} }
	v := []mesh.Vec3{P(0, 0, 0), P(x, 0, 0), P(x, y, 0), P(0, y, 0), P(0, 0, z), P(x, 0, z), P(x, y, z), P(0, y, z)}
	t := []mesh.Tri{{0, 2, 1}, {0, 3, 2}, {4, 5, 6}, {4, 6, 7}, {0, 1, 5}, {0, 5, 4}, {1, 2, 6}, {1, 6, 5}, {2, 3, 7}, {2, 7, 6}, {3, 0, 4}, {3, 4, 7}}
	return &mesh.Mesh{V: v, T: t}
}
