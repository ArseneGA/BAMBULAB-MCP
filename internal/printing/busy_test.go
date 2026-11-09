package printing

import (
	"context"
	"testing"
	"time"
)

func busyCodes(rs []BusyReason) map[string]bool {
	m := map[string]bool{}
	for _, r := range rs {
		m[r.Code] = true
	}
	return m
}

// Les demandes humaines en attente (confirmation, arrêt, action) rendent le service « occupé » tant qu'elles sont
// valables, et plus après leur expiration.
func TestBusyPendingHumanRequests(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	if r := s.BusyReasons(ctx); len(r) != 0 {
		t.Fatalf("service neuf : %+v", r)
	}
	future, past := s.now().Add(10*time.Minute), s.now().Add(-time.Minute)

	s.mu.Lock()
	s.approv["c1"] = &approval{jobID: "pj", expires: future}
	s.stops["s1"] = stopReq{jobID: "pj", expires: future}
	s.actions["a1"] = &actionReq{kind: ActionResume, expires: future}
	s.mu.Unlock()
	got := busyCodes(s.BusyReasons(ctx))
	for _, want := range []string{BusyConfirmPending, BusyStopPending, BusyActionPending} {
		if !got[want] {
			t.Errorf("raison %s absente : %v", want, got)
		}
	}

	// Expirées : plus rien (la confirmation périmée ne peut plus servir).
	s.mu.Lock()
	s.approv["c1"].expires = past
	s.stops["s1"] = stopReq{jobID: "pj", expires: past}
	s.actions["a1"].expires = past
	s.mu.Unlock()
	if r := s.BusyReasons(ctx); len(r) != 0 {
		t.Fatalf("demandes expirées : %+v", r)
	}

	// Arrêt en cours d'application : occupé même sans demande valable.
	s.mu.Lock()
	s.stopping["pj"] = true
	s.mu.Unlock()
	if !busyCodes(s.BusyReasons(ctx))[BusyStopPending] {
		t.Fatal("arrêt en cours non détecté")
	}
}
