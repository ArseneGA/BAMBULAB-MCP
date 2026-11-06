package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// Version de l'agent.
var Version = "0.1.0-j8"

// Slicing local : abstraction du slicer pour les tests.
type Slicing interface {
	Available() (ok bool, version string)
	Slice(ctx context.Context, req slicer.Request) (*slicer.Result, error)
}

// Options de l'agent.
type Options struct {
	Config  *Config
	Secret  string // secret clair, en mémoire seulement
	Studio  Studio
	Slicing Slicing
	Log     *slog.Logger
	Client  *agentlink.Client // injectable (tests) ; sinon créé depuis la config
}

// Agent est la boucle de travaux.
type Agent struct {
	cfg    *Config
	cl     *agentlink.Client
	studio Studio
	slicer Slicing
	log    *slog.Logger
	jr     *Journal

	mu      sync.Mutex
	running map[string]context.CancelFunc
	hbSec   int
}

// New prépare l'agent.
func New(o Options) (*Agent, error) {
	if o.Config == nil {
		return nil, errors.New("configuration requise")
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Studio == nil {
		o.Studio = &SystemStudio{ConfiguredPath: o.Config.StudioPath}
	}
	if o.Slicing == nil {
		o.Slicing = &localSlicing{studioPath: o.Config.StudioPath, work: filepath.Join(o.Config.workDir(), "slicer")}
	}
	cl := o.Client
	if cl == nil {
		c2, err := agentlink.NewClientWithOptions(o.Config.ServerURL, o.Secret, agentlink.ClientOptions{ServerFingerprint: o.Config.ServerFingerprint})
		if err != nil {
			return nil, err
		}
		cl = c2
		if w := agentlink.InsecureTransportWarning(o.Config.ServerURL); w != "" {
			o.Log.Warn(w)
		}
	}
	jr, err := OpenJournal(o.Config.stateDir())
	if err != nil {
		return nil, err
	}
	return &Agent{cfg: o.Config, cl: cl, studio: o.Studio, slicer: o.Slicing, log: o.Log, jr: jr,
		running: map[string]context.CancelFunc{}, hbSec: 15}, nil
}

// Capabilities construit l'annonce de capacités.
func (a *Agent) Capabilities(ctx context.Context) agentlink.AgentCaps {
	si := a.studio.Info(ctx)
	ok, sv := a.slicer.Available()
	caps := agentlink.AgentCaps{OS: runtime.GOOS, Arch: runtime.GOARCH, AgentVersion: Version,
		Studio:      agentlink.StudioCaps{Installed: si.Installed, Path: si.Path, Version: si.Version, Running: si.Running},
		LocalSlicer: ok, SlicerVersion: sv,
		Tasks: []string{agentlink.KindInventory, agentlink.KindUploadFiles, agentlink.KindGetProjectState, agentlink.KindOpenInStudio}}
	if ok {
		caps.Tasks = append(caps.Tasks, agentlink.KindSlice)
	}
	for _, f := range a.cfg.Folders {
		caps.SharedFolders = append(caps.SharedFolders, agentlink.FolderCap{Label: f.Label, Path: f.Path})
	}
	return caps
}

func (a *Agent) held() []agentlink.HeldTask {
	var h []agentlink.HeldTask
	a.mu.Lock()
	for id := range a.running {
		h = append(h, agentlink.HeldTask{ID: id, State: "running"})
	}
	a.mu.Unlock()
	for _, e := range a.jr.Unacked() {
		if e.Result != nil {
			h = append(h, agentlink.HeldTask{ID: e.ID, State: e.Result.State})
		}
	}
	return h
}

// Hello annonce les capacités, réconcilie, et rapporte les résultats en attente.
func (a *Agent) Hello(ctx context.Context) error {
	// Un travail resté « running » dans le journal sans processus : l'agent a redémarré.
	for _, e := range a.jr.Unacked() {
		a.mu.Lock()
		_, live := a.running[e.ID]
		a.mu.Unlock()
		if e.Result == nil && !live {
			a.jr.Finish(e.ID, &agentlink.TaskResult{State: agentlink.StateFailed,
				Error: &agentlink.TaskError{Code: "agent_restarted", Message: "l'agent a redémarré pendant le travail ; recréer le travail si besoin"}})
		}
	}
	resp, err := a.cl.Hello(ctx, agentlink.HelloRequest{AgentVersion: Version, Capabilities: a.Capabilities(ctx), Held: a.held()})
	if err != nil {
		return err
	}
	if resp.HeartbeatSeconds > 0 {
		a.hbSec = resp.HeartbeatSeconds
	}
	for _, id := range resp.Abandon {
		a.cancelTask(id)
		a.jr.Ack(id) // le serveur ne veut plus de ce travail : on l'oublie
	}
	a.reportPending(ctx)
	return nil
}

// reportPending envoie les résultats détenus et non acquittés.
func (a *Agent) reportPending(ctx context.Context) {
	for _, e := range a.jr.Unacked() {
		if e.Result == nil {
			continue
		}
		err := a.cl.Result(ctx, e.ID, *e.Result)
		var ae *agentlink.Error
		switch {
		case err == nil:
			a.jr.Ack(e.ID)
		case errors.As(err, &ae) && ae.Status >= 400 && ae.Status < 500 && ae.Code != agentlink.CodeOutputMissing:
			a.log.Warn("résultat refusé par le serveur, oublié", "task", e.ID, "err", err)
			a.jr.Ack(e.ID)
		default:
			a.log.Warn("résultat non remis, nouvel essai plus tard", "task", e.ID, "err", err)
		}
	}
}

func (a *Agent) cancelTask(id string) {
	a.mu.Lock()
	if c := a.running[id]; c != nil {
		c()
	}
	a.mu.Unlock()
}

// Heartbeat envoie un signe de vie et applique les annulations demandées.
func (a *Agent) Heartbeat(ctx context.Context) error {
	si := a.studio.Info(ctx)
	hb := agentlink.HeartbeatRequest{StudioRunning: si.Running}
	if si.Project != nil {
		hb.OpenProject = si.Project.Name
	}
	a.mu.Lock()
	for id := range a.running {
		hb.Running = append(hb.Running, id)
	}
	a.mu.Unlock()
	resp, err := a.cl.Heartbeat(ctx, hb)
	if err != nil {
		return err
	}
	for _, id := range resp.Cancel {
		a.cancelTask(id)
	}
	return nil
}

// Run boucle jusqu'à l'annulation de ctx : hello, heartbeat en parallèle,
// long-polling de travaux, reconnexion avec attente croissante.
func (a *Agent) Run(ctx context.Context) error {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := a.Hello(ctx); err != nil {
			var ae *agentlink.Error
			if errors.As(err, &ae) && ae.Status == 401 {
				return fmt.Errorf("le serveur refuse l'agent (secret invalide ou agent révoqué) : %w", err)
			}
			a.log.Warn("serveur injoignable", "err", err, "nouvel essai", backoff)
			if !sleep(ctx, backoff) {
				break
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		a.log.Info("connecté au serveur")
		sess, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := time.NewTicker(time.Duration(a.hbSec) * time.Second)
			defer t.Stop()
			for {
				select {
				case <-sess.Done():
					return
				case <-t.C:
					if err := a.Heartbeat(sess); err != nil && sess.Err() == nil {
						a.log.Warn("heartbeat", "err", err)
					}
				}
			}
		}()
		err := a.pollLoop(sess)
		cancel()
		wg.Wait()
		if ctx.Err() != nil {
			break
		}
		a.log.Warn("session interrompue", "err", err)
		if !sleep(ctx, backoff) {
			break
		}
	}
	return ctx.Err()
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (a *Agent) pollLoop(ctx context.Context) error {
	wait := time.Duration(a.cfg.PollWaitSeconds) * time.Second
	if wait <= 0 {
		wait = 25 * time.Second
	}
	for ctx.Err() == nil {
		t, err := a.cl.NextTask(ctx, wait)
		if err != nil {
			return err
		}
		if t == nil {
			a.reportPending(ctx)
			continue
		}
		a.Execute(ctx, *t)
	}
	return ctx.Err()
}

// Execute exécute un travail jusqu'à son rapport (appel synchrone).
func (a *Agent) Execute(ctx context.Context, t agentlink.Task) {
	tctx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.running[t.ID] = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		delete(a.running, t.ID)
		a.mu.Unlock()
	}()
	a.jr.Begin(t)
	if err := a.cl.StartTask(ctx, t.ID); err != nil {
		a.log.Warn("démarrage non confirmé", "task", t.ID, "err", err)
		var ae *agentlink.Error
		if errors.As(err, &ae) && ae.Status == 409 { // annulé ou remplacé côté serveur
			a.jr.Ack(t.ID)
			return
		}
	}
	env := &taskEnv{a: a, task: t, ctx: tctx}
	res := a.dispatch(env)
	if tctx.Err() != nil && ctx.Err() == nil { // annulation demandée par le serveur
		res = agentlink.TaskResult{State: agentlink.StateCancelled}
	}
	if ctx.Err() != nil { // arrêt de l'agent : on garde le journal « running », hello le déclarera échoué
		return
	}
	a.jr.Finish(t.ID, &res)
	a.reportPending(ctx)
}

func (a *Agent) dispatch(env *taskEnv) (res agentlink.TaskResult) {
	defer func() {
		if p := recover(); p != nil {
			res = failed("panic", fmt.Sprint(p))
		}
	}()
	var out any
	var err error
	switch env.task.Kind {
	case agentlink.KindInventory:
		out, err = a.doInventory(env)
	case agentlink.KindUploadFiles:
		out, err = a.doUploadFiles(env)
	case agentlink.KindSlice:
		return a.doSlice(env)
	case agentlink.KindGetProjectState:
		out, err = a.doProjectState(env)
	case agentlink.KindOpenInStudio:
		out, err = a.doOpenInStudio(env)
	case agentlink.KindModifyOpenProject:
		return failed("not_supported", "modifier en direct le projet ouvert n'est pas pris en charge (aucune passerelle C++ dans Studio) ; modifier une copie puis open_in_studio")
	default:
		return failed("unknown_kind", "type de travail inconnu : "+env.task.Kind)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return agentlink.TaskResult{State: agentlink.StateCancelled}
		}
		var te *taskErr
		if errors.As(err, &te) {
			return failed(te.code, te.msg)
		}
		return failed("task_failed", err.Error())
	}
	raw, _ := json.Marshal(out)
	return agentlink.TaskResult{State: agentlink.StateSucceeded, Result: raw}
}

type taskErr struct{ code, msg string }

func (e *taskErr) Error() string { return e.code + " : " + e.msg }

func terr(code, format string, args ...any) error {
	return &taskErr{code: code, msg: fmt.Sprintf(format, args...)}
}

func failed(code, msg string) agentlink.TaskResult {
	return agentlink.TaskResult{State: agentlink.StateFailed, Error: &agentlink.TaskError{Code: code, Message: msg}}
}

// taskEnv donne aux travaux leur contexte et le rapport d'avancement.
type taskEnv struct {
	a    *Agent
	task agentlink.Task
	ctx  context.Context
	last time.Time
}

func (e *taskEnv) progress(p float64, msg string) {
	if time.Since(e.last) < 700*time.Millisecond && p < 1 {
		return
	}
	e.last = time.Now()
	_ = e.a.cl.Progress(e.ctx, e.task.ID, agentlink.ProgressReport{Progress: p, Message: msg})
}

func (e *taskEnv) workDir() (string, error) {
	d := filepath.Join(e.a.cfg.workDir(), "tasks", e.task.ID)
	return d, os.MkdirAll(d, 0o755)
}
