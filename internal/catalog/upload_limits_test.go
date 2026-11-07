package catalog

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Le total des octets annoncés par les sessions ouvertes est plafonné ; annuler libère la place.
func TestUploadInFlightBytesAreCapped(t *testing.T) {
	h := newH(t)
	h.svc.SetUploadOptions(UploadOptions{MaxInFlightBytes: 1000, MaxSessions: 10})
	ctx := context.Background()
	a, err := h.svc.UploadBegin(ctx, "a.stl", 600, "", UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.UploadBegin(ctx, "b.stl", 500, "", UploadMeta{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("1100 octets annoncés pour un plafond de 1000 : %v", err)
	}
	if _, err := h.svc.UploadBegin(ctx, "b.stl", 400, "", UploadMeta{}); err != nil {
		t.Fatalf("1000 octets pile : %v", err)
	}
	if err := h.svc.UploadCancel(a.UploadID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.UploadBegin(ctx, "c.stl", 600, "", UploadMeta{}); err != nil {
		t.Fatalf("place libérée par l'annulation : %v", err)
	}
}

// Une session active ne vit pas indéfiniment : la durée de vie absolue l'emporte sur le TTL d'inactivité.
func TestUploadSessionHasAnAbsoluteLifetime(t *testing.T) {
	h := newH(t)
	h.svc.SetUploadOptions(UploadOptions{TTL: 5 * time.Second, MaxLifetime: 250 * time.Millisecond, MaxSessions: 2})
	ctx := context.Background()
	st, err := h.svc.UploadBegin(ctx, "a.stl", 10, "", UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := h.svc.UploadWrite(ctx, st.UploadID, 0, []byte("x")); err != nil { // l'activité prolonge le TTL, pas la durée absolue
		t.Fatal(err)
	}
	if got, _ := h.svc.UploadStatus(ctx, st.UploadID); got.ExpiresAt.Sub(time.Now()) > 200*time.Millisecond {
		t.Fatalf("échéance trop lointaine : %v", got.ExpiresAt)
	}
	time.Sleep(450 * time.Millisecond)
	if _, err := h.svc.UploadStatus(ctx, st.UploadID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session active toujours ouverte après sa durée de vie : %v", err)
	}
}
