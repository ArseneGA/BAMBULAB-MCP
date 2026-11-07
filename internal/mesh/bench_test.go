package mesh

import (
	"fmt"
	"math"
	"testing"
)

// Benchmarks d'analyse et de fabricabilité sur des maillages générés (sphère UV) de 100 k, 1 M et 5 M triangles.
//
//	go test -run '^$' -bench . -benchtime 1x ./internal/mesh
//
// Les chiffres de référence sont consignés dans docs/jalons/QUALITE.md.

// uvSphere génère une sphère fermée de rayon r (≈ 2·nlon·(nlat−1) triangles), normales sortantes, sommets partagés.
func uvSphere(r float64, nlat, nlon int) *Mesh {
	m := &Mesh{}
	m.V = append(m.V, Vec3{0, 0, -r}) // pôle sud
	for i := 1; i < nlat; i++ {
		phi := -math.Pi/2 + math.Pi*float64(i)/float64(nlat)
		for j := 0; j < nlon; j++ {
			th := 2 * math.Pi * float64(j) / float64(nlon)
			m.V = append(m.V, Vec3{r * math.Cos(phi) * math.Cos(th), r * math.Cos(phi) * math.Sin(th), r * math.Sin(phi)})
		}
	}
	north := len(m.V)
	m.V = append(m.V, Vec3{0, 0, r})
	ring := func(i, j int) int { return 1 + (i-1)*nlon + (j % nlon) }
	for j := 0; j < nlon; j++ {
		m.T = append(m.T, Tri{0, ring(1, j+1), ring(1, j)})
	}
	for i := 1; i < nlat-1; i++ {
		for j := 0; j < nlon; j++ {
			a, b, c, d := ring(i, j), ring(i, j+1), ring(i+1, j+1), ring(i+1, j)
			m.T = append(m.T, Tri{a, b, c}, Tri{a, c, d})
		}
	}
	for j := 0; j < nlon; j++ {
		m.T = append(m.T, Tri{ring(nlat-1, j), ring(nlat-1, j+1), north})
	}
	return m
}

// sphereWithTris renvoie une sphère de rayon 50 mm d'environ n triangles.
func sphereWithTris(n int) *Mesh {
	nlon := int(math.Sqrt(float64(n)))
	return uvSphere(50, nlon/2+1, nlon)
}

var benchSizes = []struct {
	name string
	n    int
}{{"100k", 100_000}, {"1M", 1_000_000}, {"5M", 5_000_000}}

func TestUVSphereIsClosed(t *testing.T) {
	m := uvSphere(10, 20, 40)
	r := Analyze(m, AnalyzeOptions{})
	if !r.Watertight || r.InconsistentEdges != 0 || r.Components != 1 || r.Volume <= 0 {
		t.Fatalf("sphère de bench invalide : %+v", r)
	}
	if want := 4.0 / 3 * math.Pi * 1000; math.Abs(r.Volume-want)/want > 0.03 {
		t.Fatalf("volume %v loin de %v", r.Volume, want)
	}
}

func BenchmarkAnalyze(b *testing.B) {
	for _, s := range benchSizes {
		b.Run(s.name, func(b *testing.B) {
			m := sphereWithTris(s.n)
			b.ReportAllocs()
			b.ReportMetric(float64(len(m.T)), "tris")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if r := Analyze(m, AnalyzeOptions{}); !r.Watertight {
					b.Fatal("sphère non étanche")
				}
			}
		})
	}
}

func BenchmarkFabricability(b *testing.B) {
	for _, s := range benchSizes {
		b.Run(s.name, func(b *testing.B) {
			m := sphereWithTris(s.n)
			b.ReportAllocs()
			b.ReportMetric(float64(len(m.T)), "tris")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := Fabricability(m, FabOptions{SkipOrientation: true}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Avec la proposition d'orientation (plus coûteuse) : 100 k et 1 M seulement.
func BenchmarkFabricabilityWithOrientation(b *testing.B) {
	for _, s := range benchSizes[:2] {
		b.Run(s.name, func(b *testing.B) {
			m := sphereWithTris(s.n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := Fabricability(m, FabOptions{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkWeldSoup(b *testing.B) {
	for _, s := range benchSizes[:2] {
		b.Run(fmt.Sprint(s.name), func(b *testing.B) {
			src := sphereWithTris(s.n)
			m := soup(src)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				Weld(m, 1e-6)
			}
		})
	}
}
