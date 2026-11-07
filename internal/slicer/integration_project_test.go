package slicer

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// Tests RÉELS (Bambu Studio) des projets multi-objets : réglages par objet relus par le découpeur,
// répartition sur plusieurs plaques, vérification dans le G-code. Ignorés sans Studio ou avec -short.

func sliceProject(t *testing.T, s *Slicer, objs []mesh.ProjectObject, plate int) *Result {
	t.Helper()
	return sliceOK(t, s, Request{Objects: objs, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}, Plate: plate, Label: "proj"})
}

func objByName(rep []ObjectUse, name string) ObjectUse {
	for _, o := range rep {
		if o.Name == name {
			return o
		}
	}
	return ObjectUse{Name: "?"}
}

// Deux cubes identiques, l'un à 100 % de remplissage, l'autre à 15 % : la masse DÉPOSÉE par chacun, lue dans
// le G-code par objet, doit différer fortement — c'est la preuve que Studio relit sparse_infill_density par objet.
func TestRealPerObjectInfill(t *testing.T) {
	s := realSlicer(t)
	res := sliceProject(t, s, []mesh.ProjectObject{
		{Name: "dense", Mesh: mesh.Cube(20), Plate: 1, X: 20, Y: 20, Settings: map[string]string{"sparse_infill_density": "100%"}},
		{Name: "light", Mesh: mesh.Cube(20), Plate: 1, X: 100, Y: 20, Settings: map[string]string{"sparse_infill_density": "15%"}},
	}, 1)
	rep, err := ObjectReport(res.ArtifactPath, 1)
	if err != nil {
		t.Fatal(err)
	}
	d, l := objByName(rep, "dense"), objByName(rep, "light")
	t.Logf("100 %% : %.2f g (%d couches) ; 15 %% : %.2f g (%d couches) ; masse totale du plateau %.2f g", d.Grams, d.Layers, l.Grams, l.Layers, res.Plates[0].WeightGrams)
	if d.Grams < 2*l.Grams || d.Grams < 8 || l.Grams > 6 {
		t.Errorf("le réglage par objet n'a pas été relu : dense %.2f g, léger %.2f g", d.Grams, l.Grams)
	}
	if d.Layers != l.Layers || d.Layers < 99 {
		t.Errorf("couches %d / %d", d.Layers, l.Layers)
	}
}

// Quelles clés par objet Studio relit-il ? Chaque cube porte UNE clé ; on mesure l'écart avec le témoin.
func TestRealPerObjectKeys(t *testing.T) {
	s := realSlicer(t)
	type probe struct {
		name string
		set  map[string]string
		// effet attendu par rapport au témoin
		check func(base, o ObjectUse) bool
	}
	more := func(b, o ObjectUse) bool { return o.Grams > b.Grams*1.08 }
	probes := []probe{
		{"infill40", map[string]string{"sparse_infill_density": "40%"}, more},
		{"walls6", map[string]string{"wall_loops": "6"}, more},
		{"layer01", map[string]string{"layer_height": "0.1"}, func(b, o ObjectUse) bool { return o.Layers > b.Layers*3/2 }},
		{"layer03", map[string]string{"layer_height": "0.3"}, func(b, o ObjectUse) bool { return o.Layers < b.Layers*3/4 }},
		{"top10", map[string]string{"top_shell_layers": "10", "bottom_shell_layers": "10"}, more},
		{"brim", map[string]string{"brim_type": "outer_only", "brim_width": "5"}, func(b, o ObjectUse) bool {
			return (o.ExtentX[1]-o.ExtentX[0])-(b.ExtentX[1]-b.ExtentX[0]) > 6 // la bordure élargit l'empreinte déposée
		}},
		{"pattern", map[string]string{"sparse_infill_pattern": "cubic"}, func(b, o ObjectUse) bool { return math.Abs(o.Grams-b.Grams) > 0.02 }},
	}
	// un témoin et UNE sonde par découpe : isole la clé testée (et ses éventuelles collisions)
	var lines []string
	for _, p := range probes {
		res := sliceProject(t, s, []mesh.ProjectObject{
			{Name: "base", Mesh: mesh.Cube(20), Plate: 1, X: 20, Y: 20},
			{Name: p.name, Mesh: mesh.Cube(20), Plate: 1, X: 100, Y: 20, Settings: p.set},
		}, 1)
		rep, err := ObjectReport(res.ArtifactPath, 1)
		if err != nil {
			t.Fatal(err)
		}
		base, o := objByName(rep, "base"), objByName(rep, p.name)
		ok := p.check(base, o)
		lines = append(lines, fmt.Sprintf("%-10s %v -> %.2f g / %d couches (témoin %.2f g / %d)", p.name, p.set, o.Grams, o.Layers, base.Grams, base.Layers))
		if !ok {
			t.Errorf("clé par objet non relue : %s (%+v contre témoin %+v)", p.name, o, base)
		}
	}
	// plage de couches : 0-8 mm à 0,1 mm
	res := sliceProject(t, s, []mesh.ProjectObject{
		{Name: "base", Mesh: mesh.Cube(20), Plate: 1, X: 20, Y: 20},
		{Name: "range", Mesh: mesh.Cube(20), Plate: 1, X: 100, Y: 20, Ranges: []mesh.LayerRange{{MinZ: 0, MaxZ: 8, Settings: map[string]string{"layer_height": "0.1"}}}},
	}, 1)
	rep, err := ObjectReport(res.ArtifactPath, 1)
	if err != nil {
		t.Fatal(err)
	}
	base, r := objByName(rep, "base"), objByName(rep, "range")
	lines = append(lines, fmt.Sprintf("range 0-8mm@0.1 -> %.2f g, %d couches (témoin %d)", r.Grams, r.Layers, base.Layers))
	t.Log("\n" + strings.Join(lines, "\n"))
	if r.Layers <= base.Layers+20 {
		t.Errorf("plage de couches non relue : %d couches contre %d", r.Layers, base.Layers)
	}
}

// Trois plaques : positions monde calculées comme Studio, plaque par plaque ; chaque plaque tranchée seule
// ne contient que ses objets et son numéro.
func TestRealThreePlates(t *testing.T) {
	s := realSlicer(t)
	objs := []mesh.ProjectObject{
		{Name: "a", Mesh: mesh.Cube(20), Plate: 1, X: 20, Y: 20},
		{Name: "b", Mesh: mesh.Cube(20), Plate: 2, X: 40, Y: 40},
		{Name: "c", Mesh: mesh.Cube(20), Plate: 3, X: 60, Y: 60},
	}
	n := 3
	origin := PlateOrigins(256, 256, n)
	res := sliceOK(t, s, Request{Objects: objs, PlateOrigin: origin, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}, Plate: SliceAll, Label: "tri"})
	if len(res.Plates) != 3 {
		t.Fatalf("%d plaques tranchées", len(res.Plates))
	}
	for i, want := range []string{"a", "b", "c"} {
		if got := res.Plates[i].Objects; len(got) != 1 || got[0] != want {
			t.Errorf("plaque %d : objets %v, attendu %s", i+1, got, want)
		}
	}
	one := sliceOK(t, s, Request{Objects: objs, PlateOrigin: origin, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}, Plate: 2, Label: "tri2"})
	if len(one.Plates) != 1 || one.Plates[0].Index != 2 || one.Plates[0].Objects[0] != "b" {
		t.Errorf("plaque 2 seule : %+v", one.Plates)
	}
}
