package printing

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// grandSurTroisPieds : 201 x 215 x 204 mm sur trois petits appuis de 15 x 15 mm (cas réel décollé en PETG).
func grandSurTroisPieds() *mesh.Mesh {
	m := mesh.Box(0, 0, 30, 201, 215, 204)
	for _, p := range [][2]float64{{10, 10}, {176, 10}, {93, 190}} {
		m.Append(mesh.Box(p[0], p[1], 0, p[0]+15, p[1]+15, 30))
	}
	return m
}

func specFor(t *testing.T, s *Service, m *mesh.Mesh, name string, ov []slicer.Override) *SliceSpec {
	t.Helper()
	data, err := mesh.STLBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	sha, _, err := s.d.Blobs.Put(bytes.NewReader(data), "")
	if err != nil {
		t.Fatal(err)
	}
	return &SliceSpec{SourceSHA: sha, SourceName: name + ".stl", SourceFmt: "stl", Plate: 1, Overrides: ov}
}

func TestCheckFabricableWarnsAboutAdhesion(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()

	warn, adh, err := s.checkFabricable(ctx, specFor(t, s, grandSurTroisPieds(), "grand", nil))
	if err != nil || adh == nil || !adh.Risky() {
		t.Fatalf("risque attendu : %v %+v", err, adh)
	}
	if !strings.Contains(warn, "Risque de décollement") || !strings.Contains(warn, "outer_only") {
		t.Fatalf("avertissement de découpe : %q", warn)
	}

	// avec une vraie bordure demandée : plus d'avertissement de découpe (le risque reste mesuré)
	ov := []slicer.Override{{Key: "brim_type", Value: "outer_only"}, {Key: "brim_width", Value: 6}}
	warn, adh, err = s.checkFabricable(ctx, specFor(t, s, grandSurTroisPieds(), "grand2", ov))
	if err != nil || adh == nil || strings.Contains(warn, "Risque de décollement") {
		t.Fatalf("bordure demandée : %v %q %+v", err, warn, adh)
	}

	// cube posé : aucun risque, aucun avertissement
	warn, adh, err = s.checkFabricable(ctx, specFor(t, s, mesh.Cube(40), "cube", nil))
	if err != nil || adh == nil || adh.Risky() || warn != "" {
		t.Fatalf("cube : %v %q %+v", err, warn, adh)
	}
}

func TestAdhesionNotesOnConfirmationPrompt(t *testing.T) {
	adh := mesh.Adhesion(grandSurTroisPieds())
	j := Job{Adhesion: adh}
	if n := j.adhesionNotes(); len(n) != 1 || !strings.Contains(n[0], "bordure du profil inconnue") || !strings.Contains(n[0], "Non bloquant") {
		t.Fatalf("sans validation : %v", n)
	}
	j.Validation = &ValidationReport{BrimType: "no_brim", BrimWidth: "0"}
	if n := j.adhesionNotes(); len(n) != 1 || !strings.Contains(n[0], "no_brim") {
		t.Fatalf("no_brim : %v", n)
	}
	j.Validation = &ValidationReport{BrimType: "auto_brim", BrimWidth: "5"}
	if n := j.adhesionNotes(); len(n) != 1 {
		t.Fatalf("auto_brim ne couvre pas le risque : %v", n)
	}
	j.Validation = &ValidationReport{BrimType: "outer_only", BrimWidth: "6"}
	if n := j.adhesionNotes(); len(n) != 0 {
		t.Fatalf("bordure extérieure de 6 mm : aucun avertissement attendu, reçu %v", n)
	}
	j.Validation = &ValidationReport{BrimType: "outer_only", BrimWidth: "1"}
	if n := j.adhesionNotes(); len(n) != 1 {
		t.Fatalf("bordure de 1 mm trop fine : %v", n)
	}
	if n := (Job{Adhesion: mesh.Adhesion(mesh.Cube(40))}).adhesionNotes(); len(n) != 0 {
		t.Fatalf("cube : %v", n)
	}
}
