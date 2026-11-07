package mesh

import (
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// sphereOf construit une sphère étanche d'environ n triangles.
func sphereOf(n int) *Mesh {
	// triangles ≈ 2·rings·segs avec segs = 2·rings
	rings := 1
	for 4*rings*rings < n {
		rings++
	}
	return UVSphere(50, rings, 2*rings)
}

// soup dédouble les sommets (comme un STL : 3 sommets par triangle).
func soup(m *Mesh) *Mesh {
	out := &Mesh{V: make([]Vec3, 0, 3*len(m.T)), T: make([]Tri, 0, len(m.T))}
	for _, t := range m.T {
		k := len(out.V)
		out.V = append(out.V, m.V[t[0]], m.V[t[1]], m.V[t[2]])
		out.T = append(out.T, Tri{k, k + 1, k + 2})
	}
	return out
}

func benchAnalyze(b *testing.B, m *Mesh) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := Analyze(m, AnalyzeOptions{})
		if !r.Watertight {
			b.Fatal("sphère non étanche")
		}
	}
}

func BenchmarkAnalyze1M(b *testing.B)     { benchAnalyze(b, sphereOf(1_000_000)) }
func BenchmarkAnalyze1MSoup(b *testing.B) { benchAnalyze(b, soup(sphereOf(1_000_000))) }
func BenchmarkAnalyze5M(b *testing.B)     { benchAnalyze(b, sphereOf(5_000_000)) }

// peakHeap mesure le pic de tas en service pendant f (échantillonnage) et le total alloué.
func peakHeap(f func()) (peak uint64, total uint64) {
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var stop atomic.Bool
	done := make(chan uint64)
	go func() {
		var mx uint64
		var ms runtime.MemStats
		for !stop.Load() {
			runtime.ReadMemStats(&ms)
			if ms.HeapInuse > mx {
				mx = ms.HeapInuse
			}
			time.Sleep(2 * time.Millisecond)
		}
		done <- mx
	}()
	f()
	stop.Store(true)
	peak = <-done
	var end runtime.MemStats
	runtime.ReadMemStats(&end)
	return peak, end.TotalAlloc - base.TotalAlloc
}

// TestAnalyzeMemory5M vérifie l'objectif de volumétrie : 5 M de triangles analysés sous 2 Gio de tas.
// Long (génération + analyse) : ignoré en -short.
func TestAnalyzeMemory5M(t *testing.T) {
	if testing.Short() && os.Getenv("BAMBU_BIGMESH") == "" {
		t.Skip("volumétrie : ignoré en -short (BAMBU_BIGMESH=1 pour forcer)")
	}
	for _, n := range []int{1_000_000, 5_000_000} {
		m := sphereOf(n)
		var rep *Report
		peak, total := peakHeap(func() { rep = Analyze(m, AnalyzeOptions{}) })
		if !rep.Watertight || rep.Components != 1 {
			t.Fatalf("n=%d : sphère non étanche (%+v)", n, rep.Warnings)
		}
		t.Logf("n=%d triangles=%d : pic de tas %.0f Mio (maillage inclus), alloué au total %.0f Mio", n, len(m.T), float64(peak)/(1<<20), float64(total)/(1<<20))
		if n == 5_000_000 && peak > 2<<30 {
			t.Errorf("pic de tas %.2f Gio > 2 Gio", float64(peak)/(1<<30))
		}
	}
}
