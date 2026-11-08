package printing

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// quatrePlotsSousBloc : cas réel (porte-guitare 201 × 215 × 204 mm) : quatre plots de ≈ 3 mm de diamètre (≈ 7 mm²)
// sont les seuls contacts avec le plateau ; le dessous du bloc est un grand surplomb plat.
func quatrePlotsSousBloc() *mesh.Mesh {
	m := mesh.Box(0, 0, 30, 201, 215, 234)
	for _, p := range [][2]float64{{20, 20}, {180, 20}, {20, 190}, {180, 190}} {
		m.Append(mesh.Cylinder(1.5, 30, 24).Transform(mesh.Translation(p[0], p[1], 0)))
	}
	return m
}

// validatedJob prépare une impression VALIDATED (artefact factice + découpe issue de mesh) et un validateur qui
// renvoie l'archive décrite par info.
func validatedJob(t *testing.T, s *Service, m *mesh.Mesh, info func() *slicer.ArchiveInfo) (Job, PrinterRecord) {
	t.Helper()
	ctx := context.Background()
	yes := true
	if err := s.PutPrinterModel(ctx, contracts.PrinterModel{ID: "p1s", Name: "Bambu Lab P1S", Vendor: "Bambu Lab",
		BuildVolume: &contracts.Dimensions{X: 256, Y: 256, Z: 250}, NozzleDiametersMM: []float64{0.4}, Plates: []string{"Textured PEI Plate"}, AMSSupported: &yes, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	rec := PrinterRecord{ID: "prn_t", ModelID: "p1s", Name: "Atelier", Host: "192.168.1.20", Serial: "01P00A000000001", NozzleMM: 0.4, AMSUnits: 1, CreatedAt: time.Now()}
	if err := s.d.Store.PutDoc(ctx, actor, "printers", rec.ID, rec); err != nil {
		t.Fatal(err)
	}
	spec := specFor(t, s, m, "porte-guitare", nil)
	sha, _, err := s.d.Blobs.Put(bytes.NewReader([]byte("artefact factice")), "")
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.createJob(ctx, Job{PrinterID: rec.ID, ModelID: "mdl_t", Name: "porte-guitare.gcode.3mf", State: contracts.PrintValidated,
		ArtifactSHA256: sha, Plate: 1, Slice: spec})
	if err != nil {
		t.Fatal(err)
	}
	s.SetValidator(func(string, slicer.Expect) (*slicer.ArchiveInfo, *slicer.Validation, error) {
		return info(), &slicer.Validation{OK: true, Checks: []slicer.Check{{Name: "fake", OK: true, Level: "error", Detail: "test"}}}, nil
	})
	return j, rec
}

func fakeInfo(set func(*slicer.ArchiveInfo)) func() *slicer.ArchiveInfo {
	return func() *slicer.ArchiveInfo {
		i := &slicer.ArchiveInfo{Entries: 1, StudioVersion: "02.00.00.00", PrinterModel: "Bambu Lab P1S", PrinterProfile: "Bambu Lab P1S 0.4 nozzle",
			ProcessProfile: "0.20mm Standard", FilamentIDs: []string{"Bambu PETG Translucent"}, Nozzles: []string{"0.4"}, BedType: "Textured PEI Plate",
			Plates: []slicer.PlateResult{{Index: 1, Layers: 1000, PredictedSeconds: 3600, WeightGrams: 550, MaxZ: 204,
				Filaments: []slicer.FilamentUse{{Slot: 1, Type: "PETG", Color: "#00AE42", Grams: 550}}}}}
		if set != nil {
			set(i)
		}
		return i
	}
}

var guitarAssign = []FilamentAssignment{{FilamentSlot: 1, Tray: "ams1:1", MaterialID: "Bambu PETG Translucent @BBL X1C"}}

// Points 1, 2, 4 et 14 : à la validation, les surplombs sont croisés avec le réglage de supports (avertissement
// fort jamais bloquant, repris dans la liaison de confirmation), les îlots minuscules lèvent le risque de
// décollement tant qu'il n'y a ni bordure ≥ 3 mm ni radeau, les réglages effectifs sont dans le résumé et la couleur
// est celle de la bobine (AMS), pas celle du profil.
func TestValidateCrossesOverhangsWithSupportsAndRealColor(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	s.SetSpoolSource(func(context.Context, string) []LoadedSpoolSummary {
		return []LoadedSpoolSummary{{Tray: "ams1:1", Material: "PETG", Color: "#bcbcbc", Source: "lecture de l'AMS"}}
	})

	// ---- sans supports, sans bordure : alertes fortes ----
	j, rec := validatedJob(t, s, quatrePlotsSousBloc(), fakeInfo(func(i *slicer.ArchiveInfo) { i.EnableSupport = "0"; i.BrimType = "no_brim"; i.BrimWidth = "0" }))
	out, err := s.ValidateArtifact(ctx, ValidateInput{PrintJobID: j.ID, PrinterID: rec.ID, Plate: 1, Assignments: guitarAssign})
	if err != nil {
		t.Fatal(err)
	}
	rep := out.Report
	if !rep.OK {
		t.Fatalf("les avertissements ne doivent jamais bloquer : %v", rep.Blocking)
	}
	joined := strings.Join(rep.Warnings, "\n")
	if !strings.Contains(joined, "overhang_without_support") || !strings.Contains(joined, "SANS supports") {
		t.Fatalf("avertissement surplombs sans supports absent : %v", rep.Warnings)
	}
	if !strings.Contains(joined, "adhesion_risk") || rep.Adhesion == nil || rep.Adhesion.Level != mesh.AdhesionHigh || rep.Adhesion.TinyIslands != 4 {
		t.Fatalf("4 îlots minuscules sous 204 mm : risque ÉLEVÉ attendu : %+v / %v", rep.Adhesion, rep.Warnings)
	}
	if rep.Overhang == nil || !rep.Overhang.Significant || rep.EnableSupport == nil || *rep.EnableSupport {
		t.Fatalf("rapport : %+v %v", rep.Overhang, rep.EnableSupport)
	}
	sum := out.Job.Summary
	if sum == nil || sum.EnableSupport == nil || *sum.EnableSupport || sum.BrimType != "no_brim" || sum.RaftLayers != "0" || sum.SupportType != "normal(auto)" {
		t.Fatalf("réglages effectifs du résumé (défauts Bambu Studio quand l'archive se tait) : %+v", sum)
	}
	if f := sum.Plates[0].Filaments[0]; f.Color != "#BCBCBC" || f.ColorSource != "AMS" || f.ProfileColor != "#00AE42" || f.WantedColor() != "#00AE42" {
		t.Fatalf("couleur effective : celle de l'AMS, pas celle du profil (#00AE42) : %+v", f)
	}
	// la liaison de confirmation (et donc /confirm) porte les deux avertissements
	cur, _ := s.GetJob(ctx, j.ID)
	_, d := s.promptFor(cur)
	if len(d.Overhang) != 1 || !strings.Contains(d.Overhang[0], "SANS supports") || len(d.Adhesion) != 1 {
		t.Fatalf("liaison : surplombs %v, décollement %v", d.Overhang, d.Adhesion)
	}
	if len(d.Colors) != 1 || d.Colors[0].Color != "#BCBCBC" || d.Colors[0].ColorSource != "AMS" {
		t.Fatalf("couleurs de la liaison : %+v", d.Colors)
	}

	// ---- supports activés + bordure de 6 mm : plus d'alerte ----
	j2, _ := validatedJob2(t, s, quatrePlotsSousBloc(), fakeInfo(func(i *slicer.ArchiveInfo) { i.EnableSupport = "1"; i.BrimType = "outer_only"; i.BrimWidth = "6" }))
	out2, err := s.ValidateArtifact(ctx, ValidateInput{PrintJobID: j2.ID, PrinterID: rec.ID, Plate: 1, Assignments: guitarAssign})
	if err != nil {
		t.Fatal(err)
	}
	if w := strings.Join(out2.Report.Warnings, "\n"); strings.Contains(w, "overhang_without_support") || strings.Contains(w, "adhesion_risk") {
		t.Fatalf("supports + bordure : aucune alerte attendue : %v", out2.Report.Warnings)
	}
	if n := out2.Job.overhangNotes(); len(n) != 0 {
		t.Fatalf("liaison : %v", n)
	}

	// ---- radeau (sans bordure) : couvre aussi le décollement ; sans supports l'alerte de surplombs reste ----
	j3, _ := validatedJob2(t, s, quatrePlotsSousBloc(), fakeInfo(func(i *slicer.ArchiveInfo) { i.EnableSupport = "0"; i.BrimType = "no_brim"; i.RaftLayers = "3" }))
	out3, err := s.ValidateArtifact(ctx, ValidateInput{PrintJobID: j3.ID, PrinterID: rec.ID, Plate: 1, Assignments: guitarAssign})
	if err != nil {
		t.Fatal(err)
	}
	w3 := strings.Join(out3.Report.Warnings, "\n")
	if strings.Contains(w3, "adhesion_risk") || !strings.Contains(w3, "overhang_without_support") {
		t.Fatalf("radeau : décollement couvert, surplombs toujours signalés : %v", out3.Report.Warnings)
	}
	if n := out3.Job.adhesionNotes(); len(n) != 0 {
		t.Fatalf("liaison, radeau : %v", n)
	}
}

// validatedJob2 : second job sur le même service (modèle et imprimante déjà enregistrés).
func validatedJob2(t *testing.T, s *Service, m *mesh.Mesh, info func() *slicer.ArchiveInfo) (Job, PrinterRecord) {
	t.Helper()
	return validatedJob(t, s, m, info)
}

// Point 2 : le critère s'applique à la bordure ≥ 3 mm (ou radeau) ; une bordure de 2 mm ne suffit pas.
func TestAdhesionCoveredNeedsBrimOf3mmOrRaft(t *testing.T) {
	for _, tc := range []struct {
		typ, w, raft string
		want         bool
	}{
		{"no_brim", "0", "0", false}, {"auto_brim", "5", "0", false}, {"outer_only", "2", "0", false},
		{"outer_only", "3", "0", true}, {"outer_and_inner", "6", "", true}, {"no_brim", "0", "1", true}, {"", "", "2", true},
	} {
		if got := adhesionCovered(tc.typ, tc.w, tc.raft); got != tc.want {
			t.Errorf("%+v : %v", tc, got)
		}
	}
}

// Point 11 : « absent du catalogue » était un faux positif pour un préréglage OFFICIEL Bambu. Le nom est normalisé
// (suffixe « @BBL … » retiré) et le préréglage de Bambu Studio compte comme connu.
func TestLookupMaterialNormalisesOfficialNames(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	if _, known, _ := s.lookupMaterial(ctx, "Bambu PETG Translucent @BBL X1C", "Bambu Lab P1S", 0.4); known {
		t.Fatal("rien n'est connu sans catalogue ni profils")
	}
	if err := s.d.Store.PutDoc(ctx, actor, "materials", "bambu petg translucent", contracts.Material{ID: "bambu petg translucent", Family: "PETG", Compatibility: contracts.CompatUnknown}); err != nil {
		t.Fatal(err)
	}
	m, known, err := s.lookupMaterial(ctx, "Bambu PETG Translucent @BBL X1C", "Bambu Lab P1S", 0.4)
	if err != nil || !known || m.Family != "PETG" {
		t.Fatalf("suffixe @BBL normalisé vers le catalogue : %+v %v %v", m, known, err)
	}
	// préréglage officiel de l'installation réelle de Bambu Studio
	bin, lerr := slicer.Locate("")
	if lerr != nil || bin.ProfilesDir() == "" {
		t.Skip("Bambu Studio absent : volet « préréglage officiel » non testé")
	}
	ps, perr := profiles.Load(bin.ProfilesDir())
	if perr != nil {
		t.Skip(perr)
	}
	s2, done2 := newBareService(t)
	defer done2()
	s2.d.Slicer = func() (*slicer.Slicer, error) { return slicer.New(slicer.Config{Profiles: ps}) }
	for _, id := range []string{"Bambu PETG Translucent @BBL X1C", "Bambu PETG Translucent", "Bambu PETG Translucent @BBL P1S"} {
		m, known, err := s2.lookupMaterial(ctx, id, "Bambu Lab P1S", 0.4)
		if err != nil || !known || !strings.EqualFold(m.Family, "PETG") {
			t.Errorf("%q : préréglage officiel connu attendu : %+v %v %v", id, m, known, err)
		}
	}
	if _, known, _ := s2.lookupMaterial(ctx, "Filament imaginaire 9000", "Bambu Lab P1S", 0.4); known {
		t.Error("un filament imaginaire ne doit pas être connu")
	}
}

// Point 14 : la source de couleur est normalisée (AMS / bobine déclarée / profil).
func TestColorSourceLabel(t *testing.T) {
	for in, want := range map[string]string{"lecture de l'AMS": "AMS", "inventaire (saisi par vous)": "bobine déclarée", "": "profil", "preset": "profil"} {
		if got := colorSourceLabel(in); got != want {
			t.Errorf("%q -> %q, attendu %q", in, got, want)
		}
	}
}
