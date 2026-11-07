package mesh

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Maillages « sales » générés par code : chaque défaut est injecté dans un cube de 10 mm, puis l'analyse
// actuelle est comparée à ce qu'elle devrait voir. Ce qui n'est pas encore détecté est marqué t.Skip(...)
// pour l'agent maillage. Régénérer les fichiers : BAMBU_GEN_TESTDATA=1 go test -run GenerateDirty ./internal/mesh

type dirtyCase struct {
	name string
	mesh func() *Mesh
}

func dirtyCube() *Mesh { return Cube(10) }

func dropTris(m *Mesh, idx ...int) *Mesh {
	skip := map[int]bool{}
	for _, i := range idx {
		skip[i] = true
	}
	out := &Mesh{V: append([]Vec3(nil), m.V...)}
	for i, t := range m.T {
		if !skip[i] {
			out.T = append(out.T, t)
		}
	}
	return out
}

func flipAll(m *Mesh) *Mesh {
	c := m.Clone()
	for i, t := range c.T {
		c.T[i] = Tri{t[0], t[2], t[1]}
	}
	return c
}

func flipOne(m *Mesh, i int) *Mesh {
	c := m.Clone()
	t := c.T[i]
	c.T[i] = Tri{t[0], t[2], t[1]}
	return c
}

// soup déplie le maillage en soupe de triangles (sommets non partagés, comme un STL lu tel quel).
func soup(m *Mesh) *Mesh {
	out := &Mesh{}
	for _, t := range m.T {
		b := len(out.V)
		out.V = append(out.V, m.V[t[0]], m.V[t[1]], m.V[t[2]])
		out.T = append(out.T, Tri{b, b + 1, b + 2})
	}
	return out
}

func withHole() *Mesh { return dropTris(dirtyCube(), 2, 3) } // face +Z retirée

func nonManifold() *Mesh {
	m := dirtyCube()
	// 3e triangle sur l'arête (0,1) : plan vertical hors du cube
	m.V = append(m.V, Vec3{5, -8, -8})
	m.T = append(m.T, Tri{0, 1, len(m.V) - 1})
	return m
}

func duplicatedTriangle() *Mesh {
	m := dirtyCube()
	m.T = append(m.T, m.T[0])
	return m
}

func degenerate() *Mesh {
	m := dirtyCube()
	n := len(m.V)
	m.V = append(m.V, Vec3{1, 1, 1}, Vec3{2, 2, 2}, Vec3{3, 3, 3}) // colinéaires : aire nulle
	m.T = append(m.T, Tri{n, n + 1, n + 2}, Tri{0, 0, 1}) // + sommet répété
	return m
}

func selfIntersecting() *Mesh {
	a := dirtyCube()
	b := Cube(10).Transform(Translation(5, 5, 5)) // deux cubes qui s'interpénètrent, non fusionnés
	a.Append(b)
	return a
}

func floating() *Mesh {
	a := dirtyCube()
	a.Append(Cube(0.5).Transform(Translation(40, 40, 30))) // éclat de 0,5 mm en l'air, loin de la pièce
	return a
}

func dirtyCases() []dirtyCase {
	return []dirtyCase{
		{"cube_hole", withHole},
		{"cube_nonmanifold", nonManifold},
		{"cube_inverted", func() *Mesh { return flipAll(dirtyCube()) }},
		{"cube_one_flipped", func() *Mesh { return flipOne(dirtyCube(), 4) }},
		{"cube_duplicate_tri", duplicatedTriangle},
		{"cube_degenerate", degenerate},
		{"cube_self_intersect", selfIntersecting},
		{"cube_floating", floating},
		{"cube_soup", func() *Mesh { return soup(dirtyCube()) }},
	}
}

func TestGenerateDirtyTestdata(t *testing.T) {
	if os.Getenv("BAMBU_GEN_TESTDATA") == "" {
		t.Skip("génération désactivée")
	}
	_ = os.MkdirAll("testdata", 0o755)
	for _, c := range dirtyCases() {
		b, err := STLBytes(c.mesh())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join("testdata", "dirty_"+c.name+".stl"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func hasWarning(r *Report, sub string) bool {
	for _, w := range r.Warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestDirtyHole(t *testing.T) {
	r := Analyze(withHole(), AnalyzeOptions{})
	if r.Watertight || r.OpenEdges != 4 {
		t.Fatalf("trou : watertight=%v open=%d (attendu 4 arêtes ouvertes)", r.Watertight, r.OpenEdges)
	}
	if !hasWarning(r, "non étanche") {
		t.Fatalf("avertissement de trou manquant : %v", r.Warnings)
	}
	fr, err := Fabricability(withHole(), FabOptions{SkipOrientation: true})
	if err != nil || !strings.Contains(strings.Join(fr.Warnings, "|"), "non étanche") {
		t.Fatalf("fabricabilité doit signaler le maillage non étanche : %v %v", fr, err)
	}
}

func TestDirtyNonManifold(t *testing.T) {
	r := Analyze(nonManifold(), AnalyzeOptions{})
	if r.NonManifoldEdges != 1 || r.Watertight {
		t.Fatalf("non-manifold : %d arêtes, watertight=%v", r.NonManifoldEdges, r.Watertight)
	}
	if !hasWarning(r, "non manifold") {
		t.Fatalf("avertissement manquant : %v", r.Warnings)
	}
}

func TestDirtyInvertedNormals(t *testing.T) {
	r := Analyze(flipAll(dirtyCube()), AnalyzeOptions{})
	if !r.InvertedNormals || r.Volume > -999 || r.Volume < -1001 {
		t.Fatalf("normales inversées non détectées : inverted=%v vol=%v", r.InvertedNormals, r.Volume)
	}
	if !r.Watertight || r.InconsistentEdges != 0 {
		t.Fatalf("un cube entièrement retourné reste fermé et cohérent : %+v", r)
	}
}

func TestDirtyOneFlippedFace(t *testing.T) {
	r := Analyze(flipOne(dirtyCube(), 4), AnalyzeOptions{})
	if r.InconsistentEdges != 3 {
		t.Fatalf("une face retournée doit produire 3 arêtes incohérentes, obtenu %d", r.InconsistentEdges)
	}
	if !hasWarning(r, "incohérente") {
		t.Fatalf("avertissement manquant : %v", r.Warnings)
	}
}

func TestDirtyDegenerate(t *testing.T) {
	r := Analyze(degenerate(), AnalyzeOptions{})
	if r.DegenerateTriangles != 2 {
		t.Fatalf("triangles dégénérés : %d (attendu 2)", r.DegenerateTriangles)
	}
	near(t, "volume inchangé", r.Volume, 1000, 1e-6)
	if !r.Watertight {
		t.Fatal("les triangles dégénérés sont ignorés : le cube reste étanche")
	}
}

func TestDirtyDuplicatedTriangleIsFlaggedNonManifold(t *testing.T) {
	r := Analyze(duplicatedTriangle(), AnalyzeOptions{})
	if r.Watertight || r.NonManifoldEdges != 3 {
		t.Fatalf("un triangle dupliqué crée 3 arêtes à 4 faces : non-manifold=%d watertight=%v", r.NonManifoldEdges, r.Watertight)
	}
}

func TestDirtyDuplicatedTriangleDedicatedReport(t *testing.T) {
	t.Skip("à implémenter : compter et signaler les triangles dupliqués (champ DuplicateTriangles + avertissement dédié) au lieu de les confondre avec du non-manifold")
}

func TestDirtyUnweldedSoup(t *testing.T) {
	m := soup(dirtyCube())
	if len(m.V) != 36 {
		t.Fatalf("soupe : %d sommets", len(m.V))
	}
	r := Analyze(m, AnalyzeOptions{})
	if r.RawVertices != 36 || r.WeldedVertices != 8 || !r.Watertight || r.Components != 1 {
		t.Fatalf("la soudure doit retrouver le cube : %+v", r)
	}
}

func TestDirtyFloatingComponent(t *testing.T) {
	r := Analyze(floating(), AnalyzeOptions{})
	if r.Components != 2 || len(r.ComponentList) != 2 || !hasWarning(r, "composantes connexes") {
		t.Fatalf("composant flottant non détecté : %d composantes %v", r.Components, r.Warnings)
	}
	small := r.ComponentList[1]
	near(t, "volume de l'éclat", small.Volume, 0.125, 1e-9)
}

func TestDirtyFloatingFragmentSpecificWarning(t *testing.T) {
	t.Skip("à implémenter : distinguer un éclat flottant (volume négligeable devant la pièce principale, ou non posé sur le plateau) d'une pièce disjointe légitime, avec un avertissement dédié dans Analyze/Fabricability")
}

func TestDirtySelfIntersectionIsOnlySeenAsTwoComponents(t *testing.T) {
	r := Analyze(selfIntersecting(), AnalyzeOptions{})
	if r.Components != 2 || !r.Watertight {
		t.Fatalf("deux cubes interpénétrés : composantes=%d watertight=%v", r.Components, r.Watertight)
	}
}

func TestDirtySelfIntersectionDetected(t *testing.T) {
	t.Skip("à implémenter : détection d'auto-intersections (paires de triangles qui se coupent, via le BVH de bvh.go) avec compteur SelfIntersections et avertissement ; le volume de deux cubes interpénétrés est aujourd'hui compté deux fois (2000 au lieu de 1875)")
}

func TestDirtyNaNRejected(t *testing.T) {
	m := dirtyCube()
	m.V[3].X = math.NaN()
	if err := m.Validate(); err == nil {
		t.Fatal("un sommet NaN doit être refusé par Validate")
	}
	if _, err := Fabricability(m, FabOptions{}); err == nil {
		t.Fatal("Fabricability doit refuser un sommet NaN")
	}
}

func TestDirtyTestdataFilesMatchGenerators(t *testing.T) {
	for _, c := range dirtyCases() {
		md, err := ReadFile(filepath.Join("testdata", "dirty_"+c.name+".stl"))
		if err != nil {
			t.Fatalf("%s : %v (régénérer avec BAMBU_GEN_TESTDATA=1)", c.name, err)
		}
		got := Analyze(md.Combined(), AnalyzeOptions{})
		want := Analyze(c.mesh(), AnalyzeOptions{})
		if got.Watertight != want.Watertight || got.OpenEdges != want.OpenEdges || got.NonManifoldEdges != want.NonManifoldEdges ||
			got.InconsistentEdges != want.InconsistentEdges || got.DegenerateTriangles != want.DegenerateTriangles ||
			got.Components != want.Components {
			t.Errorf("%s : fichier et générateur divergent\nfichier=%+v\ncode=%+v", c.name, got, want)
		}
	}
}
