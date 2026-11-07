package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

func holedCubeSTL(t *testing.T) []byte {
	t.Helper()
	m := mesh.Cube(10)
	m.T = append(append([]mesh.Tri(nil), m.T[:2]...), m.T[4:]...) // retire les triangles 2 et 3 (une face)
	b, err := mesh.STLBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRepairMeshCreatesNewVersionAndKeepsOriginal(t *testing.T) {
	h := newHarness(t, nil)
	rep := h.importAll(ImportInput{Paths: []string{h.put("g/hole.stl", holedCubeSTL(t))}})
	h.idle()
	id := rep.Files[0].ModelID
	ctx := context.Background()
	before := h.details(id)
	if len(before.Versions) != 1 {
		t.Fatalf("versions avant : %d", len(before.Versions))
	}
	origSHA := before.Files[0].SHA256

	// simulation : rien n'est créé
	dry, err := h.svc.RepairMesh(ctx, RepairInput{ModelID: id, DryRun: true})
	if err != nil || dry.Created || dry.Report.Before.Watertight || !dry.Report.After.Watertight {
		t.Fatalf("dry_run : %+v %v", dry, err)
	}
	if got := h.details(id); len(got.Versions) != 1 {
		t.Fatal("dry_run a créé une version")
	}

	out, err := h.svc.RepairMesh(ctx, RepairInput{ModelID: id})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Created || out.VersionID == "" || out.ParentVersionID != before.Versions[0].ID || out.File == nil || out.File.SHA256 == origSHA {
		t.Fatalf("réparation : %+v", out)
	}
	if out.Report.Actions.HolesFilled != 1 || !out.Report.After.Watertight || out.Report.After.Defects != 0 {
		t.Fatalf("rapport : %+v", out.Report)
	}
	after := h.details(id)
	if len(after.Versions) != 2 {
		t.Fatalf("versions après : %d", len(after.Versions))
	}
	// l'original est intact (même empreinte, toujours présent)
	found := false
	for _, f := range after.Files {
		if f.SHA256 == origSHA {
			found = true
		}
	}
	if !found {
		t.Fatal("le fichier d'origine a disparu")
	}
	// la nouvelle version est réellement étanche une fois analysée
	h.idle()
	a, err := h.svc.AnalyzeGeometry(ctx, AnalyzeInput{FileID: out.File.ID, SkipFab: true, Recompute: true})
	if err != nil || a.State != StateOK || !a.Summary.Watertight {
		t.Fatalf("analyse de la version réparée : %+v %v", a, err)
	}
	// rien à réparer ensuite : pas de nouvelle version
	again, err := h.svc.RepairMesh(ctx, RepairInput{ModelID: id})
	if err != nil || again.Created || len(h.details(id).Versions) != 2 {
		t.Fatalf("seconde réparation : %+v %v", again, err)
	}
}

func TestRepairMeshErrors(t *testing.T) {
	h := newHarness(t, nil)
	if _, err := h.svc.RepairMesh(context.Background(), RepairInput{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sans cible : %v", err)
	}
	if _, err := h.svc.RepairMesh(context.Background(), RepairInput{ModelID: "mdl_inconnu"}); err == nil {
		t.Fatal("modèle inconnu accepté")
	}
}
