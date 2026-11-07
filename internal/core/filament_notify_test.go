package core

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/filament"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/notify"
)

// Un évènement de la file d'attente devient une notification queue_event avec le lien de confirmation humaine.
func TestFilamentEventsAreNotified(t *testing.T) {
	t.Setenv("BAMBU_NOTIFY_ALLOW_PRIVATE", "1") // ntfy de test sur 127.0.0.1
	c, err := Open(context.Background(), Config{DataDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got := make(chan *http.Request, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got <- r }))
	defer srv.Close()
	ch, err := c.Notify.UpsertChannel(context.Background(), notify.ChannelInput{Name: "tel", Kind: notify.KindNtfy, Events: []string{notify.EvQueue}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Notify.SetSecret(context.Background(), ch.ID, "url", srv.URL+"/t"); err != nil {
		t.Fatal(err)
	}
	// l'abonnement fait par wireCamera : on publie via l'API publique du service filament
	c.Filament.Publish(filament.Event{Kind: "queue_next_ready", QueueID: "q1", ConfirmURL: "http://127.0.0.1:7878/confirm/cf_1", Message: "prête"})
	select {
	case r := <-got:
		if r.Header.Get("Click") != "http://127.0.0.1:7878/confirm/cf_1" {
			t.Fatalf("lien : %v", r.Header)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("aucune notification")
	}
}
