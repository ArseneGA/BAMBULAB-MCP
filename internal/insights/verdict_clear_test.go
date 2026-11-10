package insights

import (
	"context"
	"errors"
	"testing"
)

func TestClearVerdictRestoresUnjudgedAndAudits(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedPrinter("pr1", "p1s", "P1S")
	e.seedModel("mod1", "Boîtier", "", "")
	e.done("jobA", 1, "COMPLETED")

	// rien à retirer : idempotent, aucune entrée d'audit
	o, err := e.svc.ClearVerdict(ctx, "jobA")
	if err != nil || o.Changed {
		t.Fatalf("sans verdict : %+v %v", o, err)
	}
	v, err := e.svc.SetVerdict(ctx, SetVerdictInput{PrintJobID: "jobA", Verdict: "failed", Reason: "warping", Source: SourceHumanWeb})
	if err != nil {
		t.Fatal(err)
	}
	o, err = e.svc.ClearVerdict(ctx, "jobA")
	if err != nil || !o.Changed || e.auditCount("insights.verdict.cleared", v.LogID) != 1 {
		t.Fatalf("retrait : %+v %v", o, err)
	}
	l, _ := e.svc.GetLog(ctx, "jobA")
	if l.Verdict != "" || l.FailReason != "" || !l.Reached100 {
		t.Fatalf("la fiche doit être non jugée mais toujours imprimée : %+v", l)
	}
	if o2, err := e.svc.ClearVerdict(ctx, "jobA"); err != nil || o2.Changed || e.auditCount("insights.verdict.cleared", v.LogID) != 1 {
		t.Fatalf("2e retrait : %+v %v", o2, err)
	}
	if _, err := e.svc.ClearVerdict(ctx, "nope"); !errors.Is(err, ErrInvalid) {
		t.Errorf("inconnue : %v", err)
	}
	if _, err := e.svc.ClearVerdict(ctx, " "); !errors.Is(err, ErrInvalid) {
		t.Errorf("vide : %v", err)
	}
}
