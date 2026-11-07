package insights

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/filament"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

type fakeSpools struct{}

func (fakeSpools) PriceForSlot(_ context.Context, printer, slot string) (filament.SlotPrice, bool) {
	if printer == "pr1" && slot == "ams1:1" {
		return filament.SlotPrice{SpoolID: "sp1", Label: "PLA noir", PricePerKg: 40}, true
	}
	return filament.SlotPrice{}, false
}

func (fakeSpools) UsageOf(_ context.Context, job string) ([]filament.Usage, error) {
	return []filament.Usage{{PrintJobID: job, FilamentSlot: 1, Grams: 120}}, nil
}

func TestSpoolPriceAndUsageAndCostFunc(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.SetSpools(fakeSpools{})
	st := t0
	e.seedJob(jobSpec{ID: "j1", State: "COMPLETED", Printer: "pr1", Seconds: 3600, Grams: 100, Start: st, End: st.Add(time.Hour), Percent: 100})
	o, err := e.svc.EstimateCost(ctx, EstimateInput{PrintJobID: "j1"})
	if err != nil {
		t.Fatal(err)
	}
	f := o.Estimate.Filament[0]
	if f.PricePerKg != 40 || !strings.Contains(f.PriceSource, "bobine de l'inventaire") || f.Amount != 4 {
		t.Errorf("prix de la bobine : %+v", f)
	}
	if o.Actual == nil || o.Actual.Filament[0].Grams != 120 || o.Actual.Filament[0].Amount != 4.8 {
		t.Errorf("réel : %+v", o.Actual)
	}
	c, ok := e.svc.MaterialCost(ctx, "PETG", 100)
	if !ok || c != 2.2 {
		t.Errorf("MaterialCost : %v %v", c, ok)
	}
}

func TestCameraPhotosAttachedByReadingNotCopying(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.done("jobC", 1, "COMPLETED")
	sha, _, _ := e.bl.Put(bytes.NewReader(append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 30)...)), "")
	ts := store.FormatTime(t0)
	for _, r := range [][]string{{"c1", "final"}, {"c2", "alert"}, {"c3", "snapshot"}} {
		e.exec(`INSERT INTO camera_photos(id,printer_id,print_job_id,kind,sha256,size,taken_at) VALUES(?,?,?,?,?,?,?)`, r[0], "pr1", "jobC", r[1], sha, 34, ts)
	}
	g, err := e.svc.GetLog(ctx, "jobC")
	if err != nil || len(g.CameraPhotos) != 2 || g.CameraPhotos[0].Kind != "final" || g.CameraPhotos[0].SHA256 != sha {
		t.Fatalf("photos de caméra : %v %+v", err, g.CameraPhotos)
	}
	if len(g.Photos) != 0 {
		t.Errorf("rien n'est copié dans les photos de la fiche : %v", g.Photos)
	}
	l, _ := e.svc.ListLogs(ctx, ListQuery{})
	if len(l.Entries[0].CameraPhotos) != 2 {
		t.Errorf("liste : %+v", l.Entries[0].CameraPhotos)
	}
	// lecture autorisée par la fiche
	f, ct, err := e.svc.OpenPhoto(ctx, "jobC", sha)
	if err != nil || ct != "image/jpeg" {
		t.Fatalf("lecture : %v %s", err, ct)
	}
	f.Close()
	// une mise à jour ne fige pas les photos de caméra dans le document
	r := 3
	if _, err := e.svc.UpdateLog(ctx, UpdateInput{ID: "jobC", Rating: &r}); err != nil {
		t.Fatal(err)
	}
	var doc string
	_ = e.st.DB().QueryRow(`SELECT doc FROM print_logs WHERE print_job_id='jobC'`).Scan(&doc)
	if strings.Contains(doc, "camera_photos") {
		t.Error("camera_photos ne doit pas être enregistré dans la fiche")
	}
	// un instantané simple n'est pas rattaché ; photo d'une autre impression non plus
	e.done("jobD", 2, "COMPLETED")
	if d, _ := e.svc.GetLog(ctx, "jobD"); len(d.CameraPhotos) != 0 {
		t.Errorf("jobD : %+v", d.CameraPhotos)
	}
	if _, _, err := e.svc.OpenPhoto(ctx, "jobD", sha); err == nil {
		t.Error("la photo d'une autre impression ne doit pas être servie")
	}
}
