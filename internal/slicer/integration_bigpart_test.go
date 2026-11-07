package slicer

import (
	"context"
	"fmt"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// TestRealBigPartsP1S : reproduction du retour terrain « erreur -50, plaque vide » pour les pièces larges
// (202×214 passait, 216×236 et 239×141 étaient refusées) sur le plateau 256×256 de la P1S.
func TestRealBigPartsP1S(t *testing.T) {
	s := realSlicer(t)
	cases := []struct {
		name    string
		x, y, z float64
	}{{"202x214", 202, 214, 20}, {"239x141", 239, 141, 50}, {"216x236", 216, 236, 20}}
	for _, c := range cases {
		tr := true
		for _, arrange := range []*bool{nil, &tr} {
			c := c
			t.Run(fmt.Sprintf("%s-%v", c.name, arrange != nil), func(t *testing.T) {
				m := mesh.Box(0, 0, 0, c.x, c.y, c.z)
				path := writeSTL(t, c.name+".stl", m)
				res, err := s.Slice(context.Background(), Request{ID: "big", Model: path, Machine: p1sMachine, Process: p1sProcess,
					Filaments: []string{p1sFilament}, Arrange: arrange})
				if res != nil {
					defer res.Cleanup()
					t.Logf("result.json: %s", res.StudioJSON)
					t.Logf("stderr: %s", tail(res.Stderr, 600))
				}
				if err != nil {
					t.Fatalf("%v", err)
				}
			})
		}
	}
}
