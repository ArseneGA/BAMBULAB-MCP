package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

func TestCancelRunningNonReplayableBecomesUnknown(t *testing.T) {
	for _, kind := range []string{"physique", "start_print"} {
		t.Run(kind, func(t *testing.T) {
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			s := newStore(t, t.TempDir())
			defer s.Close()
			q := NewQueue(s, QueueConfig{})
			reg := NewRegistry()
			started := make(chan struct{})
			// Même déclaré rejouable, start_print ne l'est jamais.
			reg.Register(kind, kind == "start_print", func(ctx context.Context, jc *JobCtx) (json.RawMessage, error) {
				close(started)
				<-ctx.Done()
				return nil, ctx.Err()
			})
			done := make(chan struct{})
			go func() { fastRunner(q, reg).Run(ctx); close(done) }()
			j, _, err := q.Enqueue(ctx, EnqueueParams{Kind: kind, IdempotencyKey: "k-" + kind})
			if err != nil {
				t.Fatal(err)
			}
			<-started
			if _, err := q.RequestCancel(ctx, j.ID); err != nil {
				t.Fatal(err)
			}
			got := waitState(t, q, j.ID, contracts.JobUnknown)
			if got.Error == nil || got.Error.Code != "interrupted_unknown" {
				t.Fatalf("erreur : %+v", got.Error)
			}
			stop()
			<-done
		})
	}
}

func TestCancelQueuedNonReplayableStaysCancelled(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, t.TempDir())
	defer s.Close()
	q := NewQueue(s, QueueConfig{})
	j, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "start_print", IdempotencyKey: "k"})
	if out, err := q.RequestCancel(ctx, j.ID); err != nil || out != CancelledNow {
		t.Fatal(out, err)
	}
	if got, _ := q.Get(ctx, j.ID); got.State != contracts.JobCancelled {
		t.Fatalf("jamais démarré : attendu cancelled, obtenu %s", got.State)
	}
}

func TestRecoverCancelRequestedNonReplayableIsUnknown(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, t.TempDir())
	defer s.Close()
	q := NewQueue(s, QueueConfig{})
	phys, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "start_print", IdempotencyKey: "k"})
	rep, _, _ := q.Enqueue(ctx, EnqueueParams{Kind: "slice"})
	for i := 0; i < 2; i++ {
		if c, _ := q.Claim(ctx, "w"); c == nil {
			t.Fatal("claim")
		}
	}
	for _, id := range []string{phys.ID, rep.ID} {
		if _, err := q.RequestCancel(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	report, err := q.Recover(ctx, func(string) bool { return true }) // même « rejouable »
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Unknown) != 1 || report.Unknown[0] != phys.ID || len(report.Cancelled) != 1 || report.Cancelled[0] != rep.ID {
		t.Fatalf("rapport : %+v", report)
	}
	if got, _ := q.Get(ctx, phys.ID); got.State != contracts.JobUnknown {
		t.Fatalf("état %s", got.State)
	}
}

func TestRequeueRespectsPolicies(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, t.TempDir())
	defer s.Close()
	q := NewQueue(s, QueueConfig{MaxAttempts: 2, Limits: map[contracts.JobClass]int{contracts.ClassHeavy: 9, contracts.ClassLight: 9}})
	claim := func(kind, key string) contracts.Job {
		j, _, err := q.Enqueue(ctx, EnqueueParams{Kind: kind, IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		c, err := q.Claim(ctx, "w")
		if err != nil || c == nil || c.ID != j.ID {
			t.Fatalf("claim %v %v", c, err)
		}
		return *c
	}
	// neverReplay : refus, état inchangé.
	p := claim("start_print", "k1")
	if _, err := q.Requeue(ctx, p.ID); !errors.Is(err, ErrNotReplayable) {
		t.Fatalf("attendu ErrNotReplayable : %v", err)
	}
	if got, _ := q.Get(ctx, p.ID); got.State != contracts.JobRunning {
		t.Fatalf("état %s", got.State)
	}
	// cancel_requested : cancelled, pas de remise en file.
	c := claim("slice", "k2")
	if _, err := q.RequestCancel(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if st, err := q.Requeue(ctx, c.ID); err != nil || st != contracts.JobCancelled {
		t.Fatalf("%s %v", st, err)
	}
	// MaxAttempts : 1re tentative -> remis en file ; 2e -> failed.
	a := claim("slice", "k3")
	if st, err := q.Requeue(ctx, a.ID); err != nil || st != contracts.JobQueued {
		t.Fatalf("%s %v", st, err)
	}
	if c2, _ := q.Claim(ctx, "w"); c2 == nil || c2.ID != a.ID {
		t.Fatal("reclaim")
	}
	if st, err := q.Requeue(ctx, a.ID); err != nil || st != contracts.JobFailed {
		t.Fatalf("%s %v", st, err)
	}
}

func TestEnqueuePhysicalKindsRequireIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, t.TempDir())
	defer s.Close()
	q := NewQueue(s, QueueConfig{})
	for _, kind := range []string{"start_print", "printer.start", "printer.start_job", "upload_print_artifact"} {
		if _, _, err := q.Enqueue(ctx, EnqueueParams{Kind: kind}); !errors.Is(err, ErrIdempotencyRequired) {
			t.Errorf("%s sans clé : %v", kind, err)
		}
		if _, created, err := q.Enqueue(ctx, EnqueueParams{Kind: kind, IdempotencyKey: "cle-" + kind}); err != nil || !created {
			t.Errorf("%s avec clé : %v %v", kind, created, err)
		}
	}
	if _, _, err := q.Enqueue(ctx, EnqueueParams{Kind: "slice"}); err != nil {
		t.Fatalf("un kind ordinaire n'exige pas de clé : %v", err)
	}
	_ = time.Second
}
