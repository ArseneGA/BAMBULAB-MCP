package mesh

import (
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

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
			time.Sleep(5 * time.Millisecond)
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
// Long : ignoré en -short (BAMBU_BIGMESH=1 pour forcer).
func TestAnalyzeMemory5M(t *testing.T) {
	if testing.Short() && os.Getenv("BAMBU_BIGMESH") == "" {
		t.Skip("volumétrie : ignoré en -short (BAMBU_BIGMESH=1 pour forcer)")
	}
	for _, n := range []int{1_000_000, 5_000_000} {
		m := sphereWithTris(n)
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
