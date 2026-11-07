package slicer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// TestRealBigPartsProjectP1S : même reproduction par le chemin « projet 3MF » (positions explicites).
func TestRealBigPartsProjectP1S(t *testing.T) {
	s := realSlicer(t)
	cases := []struct {
		name       string
		x, y, z    float64
		px, py     float64
		wantFailed bool
	}{
		{"239x141-centre", 239, 141, 50, 8.5, 57.5, false},
		{"216x236-centre", 216, 236, 20, 20, 10, false},
		{"239x141-coin", 239, 141, 50, 0, 0, true},
		{"239x141-hors", 239, 141, 50, 30, 0, true}, // dépasse la bordure droite (30+239 > 256)
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			res, err := s.Slice(context.Background(), Request{ID: "bigp", Machine: p1sMachine, Process: p1sProcess,
				Filaments: []string{p1sFilament},
				Objects:   []mesh.ProjectObject{{Name: c.name, Mesh: mesh.Box(0, 0, 0, c.x, c.y, c.z), Plate: 1, X: c.px, Y: c.py}}})
			if res != nil {
				defer res.Cleanup()
				t.Logf("code=%d err=%v", res.ReturnCode, err)
			}
			if c.wantFailed != (err != nil) {
				t.Fatalf("erreur=%v, attendu échec=%v", err, c.wantFailed)
			}
			if err != nil {
				t.Logf("message: %v", err)
				var pe *PlacementError
				if !errors.As(err, &pe) || !strings.Contains(pe.Message, "zone utile 238 × 228 mm") {
					t.Fatalf("message non reformulé : %v", err)
				}
				if c.name == "239x141-hors" && !errors.Is(err, ErrStudioEmptyPlate) {
					t.Fatalf("le code -50 doit rester détectable : %v", err)
				}
			}
		})
	}
}
