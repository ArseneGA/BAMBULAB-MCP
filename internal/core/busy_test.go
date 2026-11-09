package core

import (
	"context"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

func hasReason(r BusyReport, code string) bool {
	for _, x := range r.Reasons {
		if x.Code == code {
			return true
		}
	}
	return false
}

func TestBusyFreeWhenNothingRuns(t *testing.T) {
	ctx := context.Background()
	c, err := Open(ctx, Config{DataDir: t.TempDir(), Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	rep := c.Busy(ctx)
	if rep.Busy || len(rep.Reasons) != 0 || rep.Reasons == nil {
		t.Fatalf("service au repos : %+v", rep)
	}
}

func TestBusyPrintStates(t *testing.T) {
	ctx := context.Background()
	for state, want := range map[contracts.PrintState]string{
		contracts.PrintPrinting:       printing.BusyPrintRunning,
		contracts.PrintStartRequested: printing.BusyPrintActive,
		contracts.PrintStartUnknown:   printing.BusyPrintActive,
		contracts.PrintUploading:      printing.BusyDepositProgress,
		contracts.PrintSlicing:        printing.BusyDepositProgress,
	} {
		c, err := Open(ctx, Config{DataDir: t.TempDir(), Logger: quiet()})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Store.PutDoc(ctx, "test", "print_jobs", "pj-1", printing.Job{ID: "pj-1", State: state}); err != nil {
			t.Fatal(err)
		}
		rep := c.Busy(ctx)
		if !rep.Busy || !hasReason(rep, want) {
			t.Errorf("état %s : %+v (raison %s attendue)", state, rep, want)
		}
		c.Close()
	}
	// États de repos : jamais occupé.
	c, err := Open(ctx, Config{DataDir: t.TempDir(), Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for i, state := range []contracts.PrintState{contracts.PrintDraft, contracts.PrintPrepared, contracts.PrintValidated, contracts.PrintUploaded,
		contracts.PrintCompleted, contracts.PrintFailed, contracts.PrintCancelled} {
		id := "idle-" + string(rune('a'+i))
		if err := c.Store.PutDoc(ctx, "test", "print_jobs", id, printing.Job{ID: id, State: state}); err != nil {
			t.Fatal(err)
		}
	}
	if rep := c.Busy(ctx); rep.Busy {
		t.Fatalf("états de repos : %+v", rep)
	}
}

func TestBusyRunningJob(t *testing.T) {
	ctx := context.Background()
	c, err := Open(ctx, Config{DataDir: t.TempDir(), Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, _, err := c.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: "system.selftest", Class: contracts.ClassHeavy}); err != nil {
		t.Fatal(err)
	}
	if rep := c.Busy(ctx); rep.Busy { // en file mais pas en cours : un redémarrage le rejouera
		t.Fatalf("travail seulement en file : %+v", rep)
	}
	if j, err := c.Queue.Claim(ctx, "w"); err != nil || j == nil {
		t.Fatalf("claim : %v %v", j, err)
	}
	rep := c.Busy(ctx)
	if !rep.Busy || !hasReason(rep, BusyJobRunning) {
		t.Fatalf("travail lourd en cours : %+v", rep)
	}
}
