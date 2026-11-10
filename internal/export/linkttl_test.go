package export_test

import (
	"context"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/export"
)

// FB13 : durée du lien configurable, bornée, jamais au-delà de la conservation de l'archive.
func TestLinkTTLDefaultsAndBounds(t *testing.T) {
	if d, err := export.ParseLinkTTL(""); err != nil || d != export.DefaultLinkTTL || d <= 10*time.Minute {
		t.Fatalf("défaut : %v %v", d, err)
	}
	if d, err := export.ParseLinkTTL("90m"); err != nil || d != 90*time.Minute {
		t.Fatalf("90m : %v %v", d, err)
	}
	for _, bad := range []string{"10s", "25h", "-1h", "abc", "0"} {
		if _, err := export.ParseLinkTTL(bad); err == nil {
			t.Errorf("%q accepté", bad)
		}
	}
	e := newEnv(t)
	col := e.collection("Durée", "")
	e.model(col, "Pièce", "", fileSpec{Name: "a.stl", Format: "stl", Content: stl("a", 2)})
	rec := e.run(export.Input{CollectionID: col})
	ctx := context.Background()

	l, err := e.c.Export.Link(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(l.ExpiresAt); d < export.DefaultLinkTTL-time.Minute || d > export.DefaultLinkTTL+time.Second {
		t.Fatalf("durée par défaut : %v", d)
	}
	// demande excessive : l'archive (qui expire dans 30 min) borne le lien
	e.exec(`UPDATE project_exports SET expires_at=? WHERE id=?`, store0(time.Now().Add(30*time.Minute)), rec.ID)
	short, err := e.c.Export.LinkTTL(ctx, rec.ID, 100*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(short.ExpiresAt); d > 31*time.Minute || d < 28*time.Minute {
		t.Fatalf("lien plafonné à la conservation de l'archive : %v", d)
	}
	// archive conservée longtemps : plafond MaxLinkTTL
	e.exec(`UPDATE project_exports SET expires_at=? WHERE id=?`, store0(time.Now().Add(72*time.Hour)), rec.ID)
	long, err := e.c.Export.LinkTTL(ctx, rec.ID, 100*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(long.ExpiresAt); d > export.MaxLinkTTL+time.Second {
		t.Fatalf("plafond MaxLinkTTL dépassé : %v", d)
	}
	// un lien long reste infalsifiable : expiration rallongée refusée
	if e.c.Export.VerifyLink(rec.ID, "9999999999", queryOf(t, long.Path, "sig")) {
		t.Fatal("expiration allongée acceptée")
	}
}
