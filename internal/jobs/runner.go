package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// JobCtx est ce que reçoit un handler en plus du contexte annulable.
type JobCtx struct {
	Job contracts.Job
	q   *Queue
	log *slog.Logger
}

// Progress publie l'avancement (0..1) et un message.
func (c *JobCtx) Progress(p float64, msg string) {
	if err := c.q.SetProgress(context.Background(), c.Job.ID, p, msg); err != nil {
		c.log.Warn("progression non enregistrée", "job", c.Job.ID, "err", err)
	}
}

// Logf ajoute une ligne au journal borné du travail.
func (c *JobCtx) Logf(format string, args ...any) {
	if err := c.q.AppendLog(context.Background(), c.Job.ID, fmt.Sprintf(format, args...)); err != nil {
		c.log.Warn("journal non enregistré", "job", c.Job.ID, "err", err)
	}
}

// Handler exécute un type de travail. Il doit respecter l'annulation de ctx.
// Une erreur *HandlerError fixe le code d'erreur enregistré.
type Handler func(ctx context.Context, jc *JobCtx) (json.RawMessage, error)

// HandlerError porte un code d'erreur stable.
type HandlerError struct {
	Code string
	Err  error
}

func (e *HandlerError) Error() string { return e.Err.Error() }
func (e *HandlerError) Unwrap() error { return e.Err }

type registration struct {
	h          Handler
	replayable bool
}

// Registry associe un type de travail à son handler.
type Registry struct {
	mu sync.RWMutex
	m  map[string]registration
}

// NewRegistry crée un registre vide.
func NewRegistry() *Registry { return &Registry{m: map[string]registration{}} }

// neverReplay : types dont l'exécution a un effet physique ; jamais rejoués
// automatiquement, même si le handler se déclare rejouable (dossier §10, §15.2).
func neverReplay(kind string) bool {
	return kind == "start_print" || strings.HasPrefix(kind, "start_print.") || strings.HasPrefix(kind, "printer.start")
}

// Register enregistre un handler. replayable=true : sûr de relancer après un arrêt brutal.
func (r *Registry) Register(kind string, replayable bool, h Handler) {
	if neverReplay(kind) {
		replayable = false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[kind] = registration{h, replayable}
}

// Replayable dit si un type peut être relancé après interruption. Inconnu -> faux.
func (r *Registry) Replayable(kind string) bool {
	if neverReplay(kind) {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.m[kind].replayable
}

func (r *Registry) get(kind string) (registration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	x, ok := r.m[kind]
	return x, ok
}

// Kinds liste les types enregistrés.
func (r *Registry) Kinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.m))
	for k := range r.m {
		out = append(out, k)
	}
	return out
}

// RunnerConfig règle le runner.
type RunnerConfig struct {
	WorkerID          string
	PollInterval      time.Duration // défaut 1 s
	HeartbeatInterval time.Duration // défaut 2 s (aussi la latence de relais d'annulation)
	Logger            *slog.Logger
}

// Runner exécute les travaux de la file.
type Runner struct {
	q   *Queue
	reg *Registry
	cfg RunnerConfig
	log *slog.Logger
	wg  sync.WaitGroup
}

// NewRunner crée un runner.
func NewRunner(q *Queue, reg *Registry, cfg RunnerConfig) *Runner {
	if cfg.WorkerID == "" {
		cfg.WorkerID = contracts.NewID("wrk")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 2 * time.Second
	}
	lg := cfg.Logger
	if lg == nil {
		lg = slog.Default()
	}
	return &Runner{q: q, reg: reg, cfg: cfg, log: lg.With("component", "jobs")}
}

// Run boucle jusqu'à l'annulation de ctx, puis attend les travaux en cours (drain)
// qui reçoivent un contexte annulé. Les travaux rejouables interrompus par
// l'arrêt sont remis en file ; les autres restent « running » et deviendront
// « unknown » à la reprise (Recover).
func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()
	for {
		r.fill(ctx)
		select {
		case <-ctx.Done():
			r.wg.Wait()
			return nil
		case <-r.q.Wake():
		case <-ticker.C:
		}
	}
}

func (r *Runner) fill(ctx context.Context) {
	for ctx.Err() == nil {
		j, err := r.q.Claim(ctx, r.cfg.WorkerID)
		if err != nil {
			if ctx.Err() == nil {
				r.log.Error("réservation impossible", "err", err)
			}
			return
		}
		if j == nil {
			return
		}
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.execute(ctx, *j)
		}()
	}
}

func (r *Runner) execute(runCtx context.Context, job contracts.Job) {
	log := r.log.With("job", job.ID, "kind", job.Kind)
	bg := context.Background()
	reg, ok := r.reg.get(job.Kind)
	if !ok {
		log.Error("aucun handler pour ce type")
		_ = r.q.Fail(bg, job.ID, "no_handler", "aucun handler enregistré pour "+job.Kind)
		return
	}
	jctx, cancel := context.WithCancel(runCtx)
	defer cancel()

	var mu sync.Mutex
	cancelRequested := false
	hbDone := make(chan struct{})
	hbStopped := make(chan struct{})
	go func() {
		defer close(hbStopped)
		t := time.NewTicker(r.cfg.HeartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-hbDone:
				return
			case <-t.C:
				req, err := r.q.Heartbeat(bg, job.ID)
				if err != nil {
					continue
				}
				if req {
					mu.Lock()
					cancelRequested = true
					mu.Unlock()
					cancel()
					return
				}
			}
		}
	}()

	var result json.RawMessage
	var herr error
	func() {
		defer func() {
			if p := recover(); p != nil {
				herr = &HandlerError{Code: "panic", Err: fmt.Errorf("panique : %v", p)}
				log.Error("panique dans un handler", "panic", p, "stack", string(debug.Stack()))
			}
		}()
		result, herr = reg.h(jctx, &JobCtx{Job: job, q: r.q, log: log})
	}()
	close(hbDone)
	<-hbStopped

	mu.Lock()
	cancelled := cancelRequested
	mu.Unlock()
	if !cancelled { // dernière chance : annulation demandée juste avant la fin
		if cur, err := r.q.Get(bg, job.ID); err == nil && cur.CancelRequested && herr != nil {
			cancelled = true
		}
	}

	var err error
	switch {
	case cancelled && herr != nil:
		err = r.q.MarkCancelled(bg, job.ID)
		log.Info("travail annulé")
	case herr == nil:
		err = r.q.Complete(bg, job.ID, result)
	case runCtx.Err() != nil && errors.Is(herr, context.Canceled):
		// Arrêt du service, pas une annulation voulue.
		if reg.replayable && !neverReplay(job.Kind) {
			err = r.q.Requeue(bg, job.ID)
			log.Info("arrêt du service : travail rejouable remis en file")
		} else {
			log.Warn("arrêt du service : travail non rejouable laissé en cours (deviendra unknown à la reprise)")
		}
	default:
		code := "handler_error"
		var he *HandlerError
		if errors.As(herr, &he) && he.Code != "" {
			code = he.Code
		}
		err = r.q.Fail(bg, job.ID, code, herr.Error())
		log.Warn("travail en échec", "code", code, "err", herr)
	}
	if err != nil {
		log.Error("état final non enregistré", "err", err)
	}
}
