package notify

import (
	"context"
	"mime"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

type fakePrinters struct{ connected bool }

func (f *fakePrinters) ListPrinters(context.Context) ([]printing.PrinterView, error) {
	return []printing.PrinterView{
		{PrinterRecord: printing.PrinterRecord{ID: "pr_1", Name: "Atelier", PinnedSHA256: "aa"}, AccessCodeSet: true},
		{PrinterRecord: printing.PrinterRecord{ID: "pr_2", Name: "Pas configurée"}, AccessCodeSet: false}, // ignorée
	}, nil
}
func (f *fakePrinters) GetPrinterStatus(_ context.Context, id string) (*printing.PrinterStatus, error) {
	if id != "pr_1" {
		panic("imprimante non configurée interrogée")
	}
	return &printing.PrinterStatus{PrinterID: id, Connected: f.connected, State: map[bool]string{true: "idle", false: "offline"}[f.connected]}, nil
}
func (f *fakePrinters) GetPrinterRecord(_ context.Context, id string) (printing.PrinterRecord, error) {
	return printing.PrinterRecord{ID: id, Name: "Atelier"}, nil
}

type fakePhotos struct{}

func (fakePhotos) FinalPhoto(context.Context, string, string) ([]byte, string, error) {
	return jpg, "/api/camera/photos/abc", nil
}

func waitReqs(t *testing.T, got func() []captured, n int) []captured {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if c := got(); len(c) >= n {
			return c
		}
	}
	t.Fatalf("%d requête(s) attendue(s), %d reçue(s)", n, len(got()))
	return nil
}

func TestPrintEventsBecomeNotificationsWithFinalPhoto(t *testing.T) {
	srv, got := recorder(200)
	defer srv.Close()
	s := newSvc(t, nil)
	ctx := context.Background()
	ch, _ := s.UpsertChannel(ctx, ChannelInput{Name: "tel", Kind: KindNtfy})
	_ = s.SetSecret(ctx, ch.ID, "url", srv.URL+"/t")
	w := NewWatcher(s, &fakePrinters{connected: true}, fakePhotos{})

	w.OnPrintEvent(printing.Event{JobID: "pj_1", PrinterID: "pr_1", JobName: "boite.gcode.3mf", From: contracts.PrintPrinting, To: contracts.PrintCompleted})
	c := waitReqs(t, got, 1)
	if c[0].method != http.MethodPut || string(c[0].body) != string(jpg) || !strings.Contains(c[0].hdr.Get("Click"), "/impression") {
		t.Fatalf("fin d'impression : %+v", c[0])
	}
	w.OnPrintEvent(printing.Event{JobID: "pj_2", PrinterID: "pr_1", JobName: "x", To: contracts.PrintFailed, Note: "l'imprimante rapporte un échec"})
	w.OnPrintEvent(printing.Event{JobID: "pj_3", PrinterID: "pr_1", JobName: "y", To: contracts.PrintStartUnknown})
	w.OnPrintEvent(printing.Event{JobID: "pj_4", PrinterID: "pr_1", To: contracts.PrintCancelled}) // annulation humaine : silence
	w.OnPrintEvent(printing.Event{JobID: "pj_1", PrinterID: "pr_1", To: contracts.PrintCompleted}) // doublon : dédoublonné
	c = waitReqs(t, got, 3)
	time.Sleep(150 * time.Millisecond)
	if n := len(got()); n != 3 {
		t.Fatalf("%d notifications au lieu de 3 (annulation et doublon ne notifient pas)", n)
	}
	titles := ""
	for _, r := range got() {
		d, _ := new(mime.WordDecoder).DecodeHeader(r.hdr.Get("Title"))
		titles += d + "|"
	}
	if !strings.Contains(titles, "Impression terminée") || !strings.Contains(titles, "Échec d'impression") || !strings.Contains(titles, "Lancement incertain") {
		t.Fatalf("titres : %s", titles)
	}
}

func TestOfflinePrinterIsAnnouncedOnceAfterSeveralMisses(t *testing.T) {
	srv, got := recorder(200)
	defer srv.Close()
	s := newSvc(t, nil)
	ctx := context.Background()
	ch, _ := s.UpsertChannel(ctx, ChannelInput{Name: "tel", Kind: KindNtfy, Events: []string{EvPrinterOffline}})
	_ = s.SetSecret(ctx, ch.ID, "url", srv.URL+"/t")
	fp := &fakePrinters{connected: false}
	w := NewWatcher(s, fp, nil)
	w.Check(ctx)
	w.Check(ctx)
	time.Sleep(50 * time.Millisecond)
	if len(got()) != 0 {
		t.Fatal("annoncée trop tôt")
	}
	w.Check(ctx)
	waitReqs(t, got, 1)
	w.Check(ctx)
	w.Check(ctx)
	time.Sleep(100 * time.Millisecond)
	if len(got()) != 1 {
		t.Fatalf("annonce répétée : %d", len(got()))
	}
	// retour en ligne puis nouvelle panne : nouvelle annonce (la clé de dédoublonnage expire, on la purge)
	fp.connected = true
	w.Check(ctx)
	if w.offline["pr_1"] || w.misses["pr_1"] != 0 {
		t.Fatal("retour en ligne non pris en compte")
	}
}
