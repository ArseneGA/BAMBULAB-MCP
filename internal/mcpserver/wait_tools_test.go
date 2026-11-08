package mcpserver_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

// wait_for_job rend dès que l'état change (travail court : jusqu'au succès), avec la barre et les champs structurés.
func TestWaitForJobReturnsOnStateChange(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	j, _, err := c.Queue.Enqueue(ctx, jobsParams("system.selftest", `{"steps":5,"step_ms":80}`))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	res := call(t, cs, "wait_for_job", map[string]any{"job_id": j.ID, "timeout_seconds": 20})
	st := structured(t, res)
	// wait_for_job rend dès le PREMIER changement d'état : si le worker n'a pas encore pris le travail au moment de
	// l'appel (queued), il rend à « running ». On rappelle alors, comme le ferait un client, jusqu'à l'état terminal.
	for i := 0; i < 5 && st["state"] != "succeeded" && st["state"] != "failed"; i++ {
		if st["state"] != "queued" && st["state"] != "running" {
			break
		}
		res = call(t, cs, "wait_for_job", map[string]any{"job_id": j.ID, "timeout_seconds": 20})
		st = structured(t, res)
	}
	if st["state"] != "succeeded" {
		t.Fatalf("état : %v", st["state"])
	}
	if time.Since(t0) > 15*time.Second {
		t.Fatalf("l'attente n'a pas rendu dès le changement : %s", time.Since(t0))
	}
	w := st["wait"].(map[string]any)
	if w["changed"] != true || w["timed_out"] != false {
		t.Fatalf("wait : %v", w)
	}
	d := st["progress_detail"].(map[string]any)
	if d["percent"].(float64) != 100 || d["bar"] != "██████████" || d["phase_code"] != "done" || d["phase_label"] == "" {
		t.Fatalf("progression finale : %v", d)
	}
	txt := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(txt, "██████████ 100 %") {
		t.Fatalf("texte sans barre : %s", txt)
	}
	// Déjà terminé : rend immédiatement, sans attendre.
	t1 := time.Now()
	again := structured(t, call(t, cs, "wait_for_job", map[string]any{"job_id": j.ID, "timeout_seconds": 30}))
	if time.Since(t1) > 3*time.Second || again["wait"].(map[string]any)["timed_out"] != false {
		t.Fatalf("travail terminé : retour immédiat attendu (%s) %v", time.Since(t1), again["wait"])
	}
}

// À l'échéance : timed_out, le travail continue, la progression est rendue (percent, done_bytes... stables).
func TestWaitForJobTimeoutKeepsJobRunning(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	long, _, _ := c.Queue.Enqueue(ctx, jobsParams("system.selftest", `{"steps":1000,"step_ms":50}`))
	// wait_for_job rend dès le premier changement d'état : on attend d'abord que le worker ait pris le travail
	// (queued -> running), sinon l'appel rendrait à cette transition, bien avant l'échéance.
	for dl := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if cur, err := c.Queue.Get(ctx, long.ID); err == nil && cur.State != "queued" {
			break
		}
		if time.Now().After(dl) {
			t.Fatal("le travail n'a pas démarré")
		}
	}
	t0 := time.Now()
	st := structured(t, call(t, cs, "wait_for_job", map[string]any{"job_id": long.ID, "timeout_seconds": 1}))
	if el := time.Since(t0); el < 900*time.Millisecond || el > 6*time.Second {
		t.Fatalf("durée d'attente : %s", el)
	}
	w := st["wait"].(map[string]any)
	if w["timed_out"] != true || w["changed"] != false {
		t.Fatalf("wait : %v", w)
	}
	if st["state"] == "succeeded" || st["state"] == "failed" {
		t.Fatalf("état : %v", st["state"])
	}
	d := st["progress_detail"].(map[string]any)
	for _, k := range []string{"percent", "done_bytes", "total_bytes", "phase", "phase_code", "phase_label", "bar", "text", "elapsed_seconds"} {
		if _, ok := d[k]; !ok {
			t.Fatalf("champ stable %q absent : %v", k, d)
		}
	}
	// plafond : timeout_seconds > 60 est ramené à 60 (validation de schéma tolérante) ; 0 -> 25 s par défaut (non attendu ici).
	_, _ = c.Queue.RequestCancel(ctx, long.ID)
}

// Avec un progressToken, wait_for_job émet des notifications/progress strictement croissantes, message = barre texte.
func TestWaitForJobEmitsProgressNotifications(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	srv := mcpserver.New(c)
	var mu sync.Mutex
	var got []mcp.ProgressNotificationParams
	ct, stt := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, stt, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cl := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, r *mcp.ProgressNotificationClientRequest) {
			mu.Lock()
			got = append(got, *r.Params)
			mu.Unlock()
		}})
	cs, err := cl.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })

	j, _, _ := c.Queue.Enqueue(ctx, jobsParams("system.selftest", `{"steps":40,"step_ms":100}`)) // ~4 s
	params := &mcp.CallToolParams{Name: "wait_for_job", Arguments: map[string]any{"job_id": j.ID, "timeout_seconds": 20}}
	params.SetProgressToken("tok-barre-1")
	res, err := cs.CallTool(ctx, params)
	if err != nil || res.IsError {
		t.Fatalf("wait_for_job : %v %+v", err, res)
	}
	time.Sleep(200 * time.Millisecond) // les notifications sont asynchrones
	mu.Lock()
	defer mu.Unlock()
	if len(got) < 2 {
		t.Fatalf("%d notification(s) de progression, au moins 2 attendues", len(got))
	}
	last := -1.0
	for _, n := range got {
		// Tant que le travail attend son tour (la course avec le worker est légitime), la ligne est « En file d'attente
		// (position N) » à 0 % : sans pourcentage par construction. Dès qu'il tourne, la barre texte porte « NN % ».
		enFile := n.Progress == 0 && strings.HasPrefix(n.Message, "En file d'attente")
		if n.ProgressToken != "tok-barre-1" || n.Total != 100 || n.Progress <= last || !(enFile || strings.Contains(n.Message, "%")) {
			t.Fatalf("notification invalide : %+v (précédent %v)", n, last)
		}
		last = n.Progress
	}

	// Sans progressToken : aucune notification.
	before := len(got)
	mu.Unlock()
	j2, _, _ := c.Queue.Enqueue(ctx, jobsParams("system.selftest", `{"steps":3,"step_ms":50}`))
	call(t, cs, "wait_for_job", map[string]any{"job_id": j2.ID, "timeout_seconds": 10})
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	if len(got) != before {
		t.Fatalf("notifications émises sans progressToken : %d -> %d", before, len(got))
	}
}
