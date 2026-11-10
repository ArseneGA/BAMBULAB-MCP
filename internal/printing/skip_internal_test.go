package printing

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

func skipZip(t *testing.T, s *Service, sliceInfo string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("Metadata/slice_info.config")
	_, _ = w.Write([]byte(sliceInfo))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sha, _, err := s.d.Blobs.Put(bytes.NewReader(buf.Bytes()), "")
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

// Le transport « agent » (et tout adaptateur sans ObjectSkipper) ne sait pas relayer skip_objects : refus clair, rien d'envoyé.
func TestSkipObjectsRefusedOnTransportWithoutSupport(t *testing.T) {
	s, fa := newRunService(t)
	fa.mu.Lock()
	fa.state = printer.StatePrinting
	fa.mu.Unlock()
	sha := skipZip(t, s, `<config><plate><metadata key="index" value="1"/><object identify_id="1" name="A"/><object identify_id="2" name="B"/></plate></config>`)
	j, err := s.createJob(context.Background(), Job{State: contracts.PrintPrinting, PrinterID: "prn_t", ArtifactSHA256: sha, ArtifactSize: 10, Name: "x.gcode.3mf", Plate: 1,
		Remote: &printer.RemoteRef{Name: "x-cccccccccccc.gcode.3mf", Size: 10}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	l, err := s.SkipObjectsList(ctx, j.ID)
	if err != nil || l.CanSkip || !strings.Contains(l.Refusal, "non pris en charge par ce transport") || len(l.Objects) != 2 {
		t.Fatalf("liste : %+v %v", l, err)
	}
	if _, err := s.SkipObjectsPrepare(ctx, j.ID, []int{1}); err == nil || !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "non pris en charge par ce transport") {
		t.Fatalf("préparation : %v", err)
	}
	if len(s.skipReqs) != 0 {
		t.Fatal("aucune demande ne doit être créée")
	}
}

// Fichier sans objets étiquetés (plaque d'une pièce, étiquetage désactivé) : rien à ignorer, erreur claire.
func TestSkipObjectsNoLabelledObjects(t *testing.T) {
	s, fa := newRunService(t)
	fa.mu.Lock()
	fa.state = printer.StatePrinting
	fa.mu.Unlock()
	sha := skipZip(t, s, `<config><plate><metadata key="index" value="1"/></plate></config>`)
	j, err := s.createJob(context.Background(), Job{State: contracts.PrintPrinting, PrinterID: "prn_t", ArtifactSHA256: sha, ArtifactSize: 10, Name: "x.gcode.3mf", Plate: 1,
		Remote: &printer.RemoteRef{Name: "x-cccccccccccc.gcode.3mf", Size: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SkipObjectsList(context.Background(), j.ID); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "aucun objet étiqueté") {
		t.Fatalf("%v", err)
	}
}

func TestCleanObjectName(t *testing.T) {
	if got := cleanObjectName("Pied\x1b[31m #3\n\r"); strings.ContainsAny(got, "\x1b\n\r") || !strings.HasPrefix(got, "Pied") {
		t.Fatalf("%q", got)
	}
	if got := cleanObjectName(strings.Repeat("é", 200)); len([]rune(got)) != 81 {
		t.Fatalf("%d", len([]rune(got)))
	}
}
