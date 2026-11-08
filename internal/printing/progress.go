package printing

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"sync/atomic"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

type progressSinkKey struct{}

// WithProgressSink fait remonter l'avancement des travaux attendus par un outil (dépôt, lancement) à `sink`, appelé
// au plus une fois par seconde : la couche MCP en fait des notifications/progress quand le client a fourni un
// progressToken.
func WithProgressSink(ctx context.Context, sink func(jobs.Progress)) context.Context {
	return context.WithValue(ctx, progressSinkKey{}, sink)
}

func progressSinkOf(ctx context.Context) func(jobs.Progress) {
	f, _ := ctx.Value(progressSinkKey{}).(func(jobs.Progress))
	return f
}

// expectedSliceSeconds estime la durée de découpe d'après la taille de la source : Bambu Studio ne publie aucune
// progression exploitable (sortie bloc à la fin), l'avancement affiché est donc une ESTIMATION par le temps écoulé.
// Retour terrain : ~5 min pour un modèle de 150 Mo.
func expectedSliceSeconds(spec *SliceSpec, req slicer.Request) float64 {
	if spec.Plan != nil {
		return 90
	}
	size := float64(0)
	if req.Model != "" {
		size = float64(fileSize(req.Model))
	}
	mb := size / (1 << 20)
	return math.Min(math.Max(25+2*mb, 25), 900)
}

// sliceProgress publie l'avancement estimé de la découpe (temps écoulé / durée attendue) toutes les 3 s dans la
// file ; retourne la fonction qui l'arrête.
func (s *Service) sliceProgress(jc *jobs.JobCtx, spec *SliceSpec, req slicer.Request) func() {
	exp := expectedSliceSeconds(spec, req)
	start := time.Now()
	jc.SetDetail(jobs.Detail{Phase: "découpe par Bambu Studio", Expected: exp})
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				el := time.Since(start).Seconds()
				frac := 0.95 * (1 - math.Exp(-1.6*el/exp))
				jc.Progress(0.2+0.6*frac, fmt.Sprintf("découpe par Bambu Studio — estimation : %.0f %% (écoulé %s, attendu ~%s)", frac*100, round(el), round(exp)))
			}
		}
	}()
	return func() { close(done); <-stopped }
}

func round(sec float64) string {
	if sec < 90 {
		return fmt.Sprintf("%.0f s", sec)
	}
	return fmt.Sprintf("%.0f min", sec/60)
}

// countingReader compte les octets lus (envoi FTPS) et publie la progression.
type countingReader struct {
	r     io.Reader
	total int64
	read  atomic.Int64
	jc    *jobs.JobCtx
	label string       // libellé de la barre (« Dépôt sur la P1S »)
	last  atomic.Int64 // dernière publication en base (ns)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		done := c.read.Add(int64(n))
		c.jc.SetDetail(jobs.Detail{Phase: "envoi FTPS vers l'imprimante", Label: c.label, BytesDone: done, BytesTotal: c.total})
		now := time.Now().UnixNano()
		if now-c.last.Load() > int64(2*time.Second) {
			c.last.Store(now)
			frac := float64(done) / float64(max(c.total, 1))
			c.jc.Progress(0.05+0.9*frac, fmt.Sprintf("envoi FTPS : %.1f / %.1f Mo (%.0f %%)", float64(done)/(1<<20), float64(c.total)/(1<<20), frac*100))
		}
	}
	return n, err
}

func fileSize(p string) int64 {
	if st, err := os.Stat(p); err == nil {
		return st.Size()
	}
	return 0
}
