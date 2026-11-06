package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func newStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(dir, "b.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func waitState(t *testing.T, q *Queue, id string, want contracts.JobState) contracts.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, err := q.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == want {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	j, _ := q.Get(context.Background(), id)
	t.Fatalf("job %s : état %s, attendu %s", id, j.State, want)
	return j
}

func fastRunner(q *Queue, reg *Registry) *Runner {
	return NewRunner(q, reg, RunnerConfig{PollInterval: 20 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond})
}

func TestIdempotency(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, t.TempDir())
	defer s.Close()
	q := NewQueue(s, QueueConfig{})
	a, created, err := q.Enqueue(ctx, EnqueueParams{Kind: "x", IdempotencyKey: "k1"})
	if err != nil || !created {
		t.Fatal(err, created)
	}
	b, created, err := q.Enqueue(ctx, EnqueueParams{Kind: "x", IdempotencyKey: "k1"})
	if err != nil || created || b.ID != a.ID {
		t.Fatalf("doublon : %v %v %s/%s", err, created, a.ID, b.ID)
	}
	if _, _, err := q.Enqueue(ctx, EnqueueParams{Kind: "y", IdempotencyKey: "k1"}); !errors.Is(err, ErrKindMismatch) {
		t.Fatalf("attendu ErrKindMismatch, obtenu %v", err)
	}
	if _, _, err := q.Enqueue(ctx, EnqueueParams{Kind: "x", Params: json.RawMessage("{pas json")}); err == nil {
		t.Fatal("params invalides acceptés")
	}
}

func TestClaimRespectsClassLimits(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, t.TempDir())
	defer s.Close()
	q := NewQueue(s, QueueConfig{})
	for i := 0; i < 3; i++ {
		q.Enqueue(ctx, EnqueueParams{Kind: "h", Class: contracts.ClassHeavy})
	}
	q.Enqueue(ctx, EnqueueParams{Kind: "l", Class: contracts.ClassLight})
	j1, _ := q.Claim(ctx, "w")
	if j1 == nil || j1.Class != contracts.ClassHeavy {
		t.Fatalf("premier : %+v", j1)
	}
	// Un lourd tourne déjà : le suivant doit être le léger, pas un 2e lourd.
	j2, _ := q.Claim(ctx, "w")
	if j2 == nil || j2.Class != contracts.ClassLight {
		t.Fatalf("second devrait être le léger : %+v", j2)
	}
	if j3, _ := q.Claim(ctx, "w"); j3 != nil {
		t.Fatalf("2e lourd lancé malgré la limite : %+v", j3)
	}
	if err := q.Complete(ctx, j1.ID, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if j4, _ := q.Claim(ctx, "w"); j4 == nil {
		t.Fatal("un lourd aurait dû être libéré")
	}
	// Complete sur un job qui n'est plus running : refusé.
	if err := q.Complete(ctx, j1.ID, nil); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("attendu ErrNotRunning, obtenu %v", err)
	}
}

func TestBoundedLogsAndProgress(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, t.TempDir())
	defer s.Close()
	q := NewQueue(s, QueueConfig{MaxLogLines: 5, MaxLogLineBytes: 20})
	j, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "x"})
	cl, _ := q.Claim(ctx, "w")
	for i := 0; i < 12; i++ {
		if err := q.AppendLog(ctx, j.ID, fmt.Sprintf("ligne %d %s", i, strings.Repeat("é", 50))); err != nil {
			t.Fatal(err)
		}
	}
	_ = q.SetProgress(ctx, cl.ID, 7, "trop")
	got, _ := q.Get(ctx, j.ID)
	if len(got.Logs) != 5 || !strings.HasPrefix(got.Logs[0], "ligne 7") || !strings.HasPrefix(got.Logs[4], "ligne 11") {
		t.Fatalf("logs : %v", got.Logs)
	}
	for _, l := range got.Logs {
		if len(l) > 25 {
			t.Fatalf("ligne non bornée : %d", len(l))
		}
	}
	if got.Progress != 1 {
		t.Fatalf("progression non bornée : %v", got.Progress)
	}
}

func TestRunnerSuccessFailPanic(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	s := newStore(t, t.TempDir())
	defer s.Close()
	q := NewQueue(s, QueueConfig{})
	reg := NewRegistry()
	reg.Register("ok", true, func(ctx context.Context, jc *JobCtx) (json.RawMessage, error) {
		jc.Progress(0.5, "mi-chemin")
		jc.Logf("bonjour %s", "monde")
		return json.RawMessage(`{"v":42}`), nil
	})
	reg.Register("ko", true, func(ctx context.Context, jc *JobCtx) (json.RawMessage, error) {
		return nil, &HandlerError{Code: "boom", Err: errors.New("raté")}
	})
	reg.Register("pan", true, func(ctx context.Context, jc *JobCtx) (json.RawMessage, error) {
		panic("oups")
	})
	done := make(chan struct{})
	go func() { fastRunner(q, reg).Run(ctx); close(done) }()
	ok, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "ok"})
	ko, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "ko"})
	pan, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "pan"})
	nh, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "inconnu"})
	j := waitState(t, q, ok.ID, contracts.JobSucceeded)
	if string(j.Result) != `{"v":42}` || len(j.Logs) != 1 || j.Progress != 1 {
		t.Fatalf("%+v", j)
	}
	if j := waitState(t, q, ko.ID, contracts.JobFailed); j.Error == nil || j.Error.Code != "boom" {
		t.Fatalf("%+v", j.Error)
	}
	if j := waitState(t, q, pan.ID, contracts.JobFailed); j.Error == nil || j.Error.Code != "panic" {
		t.Fatalf("%+v", j.Error)
	}
	if j := waitState(t, q, nh.ID, contracts.JobFailed); j.Error == nil || j.Error.Code != "no_handler" {
		t.Fatalf("%+v", j.Error)
	}
	stop()
	<-done
}

func TestCancelQueuedAndRunning(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	s := newStore(t, t.TempDir())
	defer s.Close()
	q := NewQueue(s, QueueConfig{})

	// Annulation en file : immédiate.
	qd, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "x"})
	out, err := q.RequestCancel(ctx, qd.ID)
	if err != nil || out != CancelledNow {
		t.Fatal(out, err)
	}
	waitState(t, q, qd.ID, contracts.JobCancelled)
	if out, _ := q.RequestCancel(ctx, qd.ID); out != AlreadyFinished {
		t.Fatalf("attendu already_finished, obtenu %s", out)
	}
	if _, err := q.RequestCancel(ctx, "job_inexistant"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("attendu ErrNotFound : %v", err)
	}

	// Annulation en cours : le contexte du handler est annulé.
	reg := NewRegistry()
	started := make(chan struct{})
	var sawCancel atomic.Bool
	reg.Register("long", true, func(ctx context.Context, jc *JobCtx) (json.RawMessage, error) {
		close(started)
		select {
		case <-ctx.Done():
			sawCancel.Store(true)
			return nil, ctx.Err()
		case <-time.After(30 * time.Second):
			return nil, errors.New("jamais annulé")
		}
	})
	done := make(chan struct{})
	go func() { fastRunner(q, reg).Run(ctx); close(done) }()
	j, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "long"})
	<-started
	out, err = q.RequestCancel(ctx, j.ID)
	if err != nil || out != CancelRequested {
		t.Fatal(out, err)
	}
	waitState(t, q, j.ID, contracts.JobCancelled)
	if !sawCancel.Load() {
		t.Fatal("le handler n'a pas vu l'annulation de son contexte")
	}
	stop()
	<-done
}

func TestRecoverAfterCrash(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := newStore(t, dir)
	q := NewQueue(s, QueueConfig{Limits: map[contracts.JobClass]int{contracts.ClassHeavy: 5, contracts.ClassLight: 5}})
	replay, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "slice", Class: contracts.ClassHeavy})
	physical, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "start_print", Class: contracts.ClassLight, IdempotencyKey: "print-1"})
	unregistered, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "mystere"})
	cancelling, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "slice"})
	waiting, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "slice"})
	// « crash » : on réserve 4 travaux (ils passent running) puis le processus meurt.
	for i := 0; i < 4; i++ {
		if j, _ := q.Claim(ctx, "ancien-worker"); j == nil {
			t.Fatal("claim")
		}
	}
	if _, err := q.RequestCancel(ctx, cancelling.ID); err != nil {
		t.Fatal(err)
	}
	s.Close() // pas d'arrêt propre : l'état reste 'running' en base

	// Redémarrage.
	s2 := newStore(t, dir)
	defer s2.Close()
	q2 := NewQueue(s2, QueueConfig{})
	reg := NewRegistry()
	var ran atomic.Int32
	reg.Register("slice", true, func(ctx context.Context, jc *JobCtx) (json.RawMessage, error) {
		ran.Add(1)
		return json.RawMessage(`"fait"`), nil
	})
	var startPrintRan atomic.Bool
	// Même déclaré rejouable, start_print ne doit jamais l'être.
	reg.Register("start_print", true, func(ctx context.Context, jc *JobCtx) (json.RawMessage, error) {
		startPrintRan.Store(true)
		return nil, nil
	})
	rep, err := q2.Recover(ctx, reg.Replayable)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Requeued) != 1 || rep.Requeued[0] != replay.ID {
		t.Fatalf("requeued : %+v", rep)
	}
	if len(rep.Unknown) != 2 || len(rep.Cancelled) != 1 {
		t.Fatalf("rapport : %+v", rep)
	}
	for _, id := range []string{physical.ID, unregistered.ID} {
		j, _ := q2.Get(ctx, id)
		if j.State != contracts.JobUnknown || j.Error == nil || j.Error.Code != "interrupted_unknown" {
			t.Fatalf("%s devrait être unknown : %+v", id, j)
		}
	}
	if j, _ := q2.Get(ctx, cancelling.ID); j.State != contracts.JobCancelled {
		t.Fatalf("annulation demandée avant crash : %s", j.State)
	}

	// Le runner reprend : le travail rejouable et celui en attente tournent, pas start_print.
	rctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { fastRunner(q2, reg).Run(rctx); close(done) }()
	waitState(t, q2, replay.ID, contracts.JobSucceeded)
	waitState(t, q2, waiting.ID, contracts.JobSucceeded) // jamais réservé avant le crash
	stop()
	<-done
	if startPrintRan.Load() {
		t.Fatal("start_print a été rejoué")
	}
	if j, _ := q2.Get(ctx, physical.ID); j.State != contracts.JobUnknown {
		t.Fatalf("start_print : %s", j.State)
	}
	// Le travail repris garde sa trace de reprise dans ses journaux.
	if j, _ := q2.Get(ctx, replay.ID); j.Attempts != 2 || len(j.Logs) == 0 {
		t.Fatalf("tentatives/journaux : %+v", j)
	}
}

func TestRecoverAttemptsExhausted(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, t.TempDir())
	defer s.Close()
	q := NewQueue(s, QueueConfig{MaxAttempts: 2})
	j, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "slice"})
	for i := 0; i < 2; i++ {
		if c, _ := q.Claim(ctx, "w"); c == nil {
			t.Fatal("claim")
		}
		rep, err := q.Recover(ctx, func(string) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && len(rep.Requeued) != 1 {
			t.Fatalf("1re reprise : %+v", rep)
		}
		if i == 1 && len(rep.Failed) != 1 {
			t.Fatalf("2e reprise devrait échouer : %+v", rep)
		}
	}
	if got, _ := q.Get(ctx, j.ID); got.State != contracts.JobFailed {
		t.Fatalf("état %s", got.State)
	}
}

func TestGracefulShutdownRequeuesReplayableOnly(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, t.TempDir())
	defer s.Close()
	q := NewQueue(s, QueueConfig{})
	reg := NewRegistry()
	var started atomic.Int32
	block := func(ctx context.Context, jc *JobCtx) (json.RawMessage, error) {
		started.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	reg.Register("rejouable", true, block)
	reg.Register("physique", false, block)
	a, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "rejouable"})
	b, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "physique"})
	rctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { fastRunner(q, reg).Run(rctx); close(done) }()
	for started.Load() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("le drain n'a pas terminé")
	}
	if j, _ := q.Get(ctx, a.ID); j.State != contracts.JobQueued {
		t.Fatalf("rejouable : %s", j.State)
	}
	if j, _ := q.Get(ctx, b.ID); j.State != contracts.JobRunning {
		t.Fatalf("physique doit rester running jusqu'à la reprise : %s", j.State)
	}
	rep, _ := q.Recover(ctx, reg.Replayable)
	if len(rep.Unknown) != 1 || rep.Unknown[0] != b.ID {
		t.Fatalf("%+v", rep)
	}
}
