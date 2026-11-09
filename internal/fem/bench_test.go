package fem

import (
	"testing"
	"time"
)

// Vitesse du solveur sur une console de 150 000 cubes (hors -short) : journalise temps et itérations.
func TestSolveSpeed150k(t *testing.T) {
	if testing.Short() {
		t.Skip("banc d'essai : hors -short")
	}
	t0 := time.Now()
	s, tip, err := cantilever(100, 38, 38, 1.0, 100, 1800, 0.38)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d cubes, %d nœuds : %d itérations en %.1f s (%.1f ms/itération), résidu %.1e, flèche %.3f mm, convergé=%v",
		s.Elements, len(s.Nodes), s.Iterations, time.Since(t0).Seconds(), 1000*time.Since(t0).Seconds()/float64(s.Iterations), s.Residual, -tip, s.Converged)
}
