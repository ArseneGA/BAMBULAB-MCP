package library

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Le chargement des gabarits n'occupe pas le verrou du service et n'appelle le worker qu'une seule fois.
func TestBuiltinsLoadOutsideTheLockWithSingleflight(t *testing.T) {
	r := newRig(t, nil)
	r.fc.listGate = make(chan struct{})
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	start := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := r.svc.loadBuiltins(ctx)
			errs <- err
		}()
	}
	start()
	for i := 0; r.fc.count("library_list") == 0; i++ {
		if i > 400 {
			t.Fatal("le worker n'a pas été appelé")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// le worker est en cours : le verrou du service doit être libre
	if !r.svc.mu.TryLock() {
		t.Fatal("s.mu tenu pendant l'appel au worker")
	}
	r.svc.mu.Unlock()
	for i := 0; i < 5; i++ {
		start()
	}
	time.Sleep(50 * time.Millisecond)
	close(r.fc.listGate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := r.fc.count("library_list"); n != 1 {
		t.Fatalf("%d appels au worker pour 6 chargements simultanés", n)
	}
}
