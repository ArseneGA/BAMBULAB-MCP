package slicer

import (
	"fmt"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// Studio dispose 14 cubes de 60 mm (plus qu'une plaque ne peut en porter) sur plusieurs plaques ; la
// disposition relue est réinjectée telle quelle et la découpe retrouve exactement les mêmes objets par plaque.
func TestRealArrangeThenSlice(t *testing.T) {
	s := realSlicer(t)
	var objs []mesh.ProjectObject
	for i := 0; i < 14; i++ {
		objs = append(objs, mesh.ProjectObject{Name: fmt.Sprintf("n%02d", i), Mesh: mesh.Cube(60), Plate: 1})
	}
	ar, err := s.Arrange(t.Context(), Request{Objects: objs, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}, Label: "arr"})
	if err != nil {
		t.Fatal(err)
	}
	if ar.Plates < 2 || len(ar.Placements) != 14 {
		t.Fatalf("disposition : %d plaques, %d placements", ar.Plates, len(ar.Placements))
	}
	want := map[int][]string{}
	for _, p := range ar.Placements {
		objs[p.Index].Plate, objs[p.Index].X, objs[p.Index].Y = p.Plate, p.X, p.Y
		want[p.Plate] = append(want[p.Plate], p.Name)
	}
	t.Logf("Studio : %d plaques en %v", ar.Plates, ar.Duration)
	res := sliceProject(t, s, objs, SliceAll)
	if len(res.Plates) != ar.Plates {
		t.Fatalf("%d plaques tranchées pour %d disposées", len(res.Plates), ar.Plates)
	}
	for _, pr := range res.Plates {
		if len(pr.Objects) != len(want[pr.Index]) {
			t.Errorf("plaque %d : %v, disposition %v", pr.Index, pr.Objects, want[pr.Index])
		}
	}
}
