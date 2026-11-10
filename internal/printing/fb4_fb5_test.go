package printing

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// ---- constructeur de G-code factice (plaque 1) ----

type gcodeBuilder struct {
	b strings.Builder
	e float64
}

func newGcodeBuilder() *gcodeBuilder {
	g := &gcodeBuilder{}
	g.b.WriteString("; HEADER\nG90\nM83\n")
	return g
}

func (g *gcodeBuilder) layer(z float64) {
	fmt.Fprintf(&g.b, "; CHANGE_LAYER\n; Z_HEIGHT: %.2f\nG1 Z%.2f F1200\n", z, z)
}

func (g *gcodeBuilder) feature(name string) { fmt.Fprintf(&g.b, "; FEATURE: %s\n", name) }

func (g *gcodeBuilder) move(x, y float64) { fmt.Fprintf(&g.b, "G1 X%.3f Y%.3f F6000\n", x, y) }

func (g *gcodeBuilder) line(x, y float64) { fmt.Fprintf(&g.b, "G1 X%.3f Y%.3f E0.5 F2400\n", x, y) }

// rect : contour fermé d'un rectangle.
func (g *gcodeBuilder) rect(x0, y0, x1, y1 float64) {
	g.move(x0, y0)
	g.line(x1, y0)
	g.line(x1, y1)
	g.line(x0, y1)
	g.line(x0, y0)
}

// fill : remplissage plein (lignes en zigzag espacées de 0,45 mm).
func (g *gcodeBuilder) fill(x0, y0, x1, y1 float64) {
	left := true
	for y := y0; y <= y1; y += 0.45 {
		if left {
			g.move(x0, y)
			g.line(x1, y)
		} else {
			g.move(x1, y)
			g.line(x0, y)
		}
		left = !left
	}
}

func (g *gcodeBuilder) zipBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("Metadata/plate_1.gcode")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(g.b.String())); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withGcode remplace l'artefact factice d'une impression par une archive contenant ce G-code.
func withGcode(t *testing.T, s *Service, j Job, g *gcodeBuilder) Job {
	t.Helper()
	sha, _, err := s.d.Blobs.Put(bytes.NewReader(g.zipBytes(t)), "")
	if err != nil {
		t.Fatal(err)
	}
	j2, err := s.update(context.Background(), j.ID, func(x *Job) error { x.ArtifactSHA256 = sha; return nil })
	if err != nil {
		t.Fatal(err)
	}
	return j2
}

func validate(t *testing.T, s *Service, j Job, rec PrinterRecord, ratio float64) *ValidateOutput {
	t.Helper()
	out, err := s.ValidateArtifact(context.Background(), ValidateInput{PrintJobID: j.ID, PrinterID: rec.ID, Plate: 1, Assignments: guitarAssign, SlenderRatio: ratio})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Report.OK {
		t.Fatalf("les avertissements ne doivent jamais bloquer : %v", out.Report.Blocking)
	}
	return out
}

// ---- FB4 : élancement ----

// troncsDuNeufNovembre : le cas réel du 9/11 en miniature. Une pièce de 30 × 30 mm sur 100 mm (élancement 3,3 : correct),
// deux troncs de supports de 5 × 5 mm qui montent à 100 mm (élancement 20 : à signaler) et un gros support de
// 20 × 20 mm de 100 mm (élancement 5 : correct).
func troncsDuNeufNovembre() *gcodeBuilder {
	g := newGcodeBuilder()
	for i := 1; i <= 500; i++ {
		g.layer(0.2 * float64(i))
		g.feature("Outer wall")
		g.rect(100, 100, 130, 130)
		g.feature("Support")
		g.rect(20, 20, 25, 25)
		g.rect(60, 20, 65, 25)
		g.rect(150, 150, 170, 170)
	}
	return g
}

func TestValidateFlagsSlenderSupportTrunks(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	j, rec := validatedJob(t, s, mesh.Cube(30), fakeInfo(func(i *slicer.ArchiveInfo) { i.EnableSupport = "1"; i.BrimType = "outer_only"; i.BrimWidth = "6" }))
	j = withGcode(t, s, j, troncsDuNeufNovembre())

	out := validate(t, s, j, rec, 0)
	rep := out.Report
	if rep.Slender == nil || rep.Slender.Limit != DefaultSlenderRatio {
		t.Fatalf("rapport d'élancement attendu avec le seuil par défaut : %+v", rep.Slender)
	}
	if rep.Slender.Supports != 2 || rep.Slender.Parts != 0 {
		t.Fatalf("2 troncs de support élancés, aucun pilier de la pièce : %+v", rep.Slender)
	}
	w := rep.Slender.Worst[0]
	if w.Kind != "support" || w.HeightMM < 99 || w.HeightMM > 100.5 || w.SectionMM < 4.5 || w.SectionMM > 6 || w.Ratio < 15 {
		t.Fatalf("pire colonne : %+v", w)
	}
	joined := strings.Join(rep.Warnings, "\n")
	if !strings.Contains(joined, "slenderness : ") || !strings.Contains(joined, "tronc(s) de support") || !strings.Contains(joined, "tree_support_branch_diameter") {
		t.Fatalf("avertissement d'élancement absent : %v", rep.Warnings)
	}
	// à acquitter AVANT la confirmation : repris dans la liaison de confirmation (carte, page /confirm, texte de Claude)
	cur, _ := s.GetJob(ctx, j.ID)
	if n := cur.slenderNotes(); len(n) != 1 || !strings.Contains(n[0], "Non bloquant") {
		t.Fatalf("notes : %v", n)
	}
	if _, d := s.promptFor(cur); len(d.Slender) != 1 {
		t.Fatalf("liaison de confirmation : %v", d.Slender)
	}

	// seuil configurable : à 25, le même G-code n'est plus signalé ; à 4, le gros support (5) l'est aussi
	if o := validate(t, s, j, rec, 25); o.Report.Slender.Flagged() || strings.Contains(strings.Join(o.Report.Warnings, "\n"), "slenderness") {
		t.Fatalf("seuil 25 : rien à signaler, reçu %+v", o.Report.Slender)
	}
	if o := validate(t, s, j, rec, 4); o.Report.Slender.Supports != 3 {
		t.Fatalf("seuil 4 : 3 supports attendus, reçu %+v", o.Report.Slender)
	}
	// réglage du service et variable d'environnement
	s.d.SlenderRatio = 30
	if got := s.slenderRatio(0); got != 30 {
		t.Fatalf("Deps.SlenderRatio : %v", got)
	}
	s.d.SlenderRatio = 0
	t.Setenv("BAMBU_SLENDER_RATIO", "7.5")
	if got := s.slenderRatio(0); got != 7.5 {
		t.Fatalf("BAMBU_SLENDER_RATIO : %v", got)
	}
	t.Setenv("BAMBU_SLENDER_RATIO", "n'importe quoi")
	if got := s.slenderRatio(0); got != DefaultSlenderRatio {
		t.Fatalf("valeur invalide : %v", got)
	}
	if got := s.slenderRatio(1); got != DefaultSlenderRatio {
		t.Fatalf("seuil sous le plancher ignoré : %v", got)
	}
}

func TestValidateSlendernessQuietWhenSturdyOrUnreadable(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	// cube posé, supports larges : aucune alerte
	g := newGcodeBuilder()
	for i := 1; i <= 300; i++ {
		g.layer(0.2 * float64(i))
		g.feature("Outer wall")
		g.rect(100, 100, 140, 140)
		g.feature("Support")
		g.rect(10, 10, 30, 30)
	}
	j, rec := validatedJob(t, s, mesh.Cube(40), fakeInfo(nil))
	j = withGcode(t, s, j, g)
	out := validate(t, s, j, rec, 0)
	if out.Report.Slender == nil || out.Report.Slender.Flagged() || strings.Contains(strings.Join(out.Report.Warnings, "\n"), "slenderness") {
		t.Fatalf("pièce et supports robustes : %+v / %v", out.Report.Slender, out.Report.Warnings)
	}
	// archive illisible (artefact factice) : silence, pas d'erreur
	j2, rec2 := validatedJob2(t, s, mesh.Cube(40), fakeInfo(nil))
	o2 := validate(t, s, j2, rec2, 0)
	if o2.Report.Slender != nil {
		t.Fatalf("G-code illisible : pas de rapport d'élancement, reçu %+v", o2.Report.Slender)
	}
	// un pilier de la pièce trop fin (8 × 8 mm sur 100 mm) est signalé comme pièce, pas comme support
	g3 := newGcodeBuilder()
	for i := 1; i <= 500; i++ {
		g3.layer(0.2 * float64(i))
		g3.feature("Outer wall")
		g3.rect(50, 50, 58, 58)
	}
	j3, rec3 := validatedJob2(t, s, mesh.Cube(40), fakeInfo(nil))
	j3 = withGcode(t, s, j3, g3)
	o3 := validate(t, s, j3, rec3, 0)
	if o3.Report.Slender.Parts != 1 || o3.Report.Slender.Supports != 0 || o3.Report.Slender.Worst[0].Kind != "part" {
		t.Fatalf("pilier de la pièce : %+v", o3.Report.Slender)
	}
}

// ---- FB5 : accroche sur la couche 1 réelle ----

// Le cas du 9/11 : le maillage ne touche le plateau que par quatre plots de 3 mm (risque ÉLEVÉ « ajouter une bordure »),
// mais le G-code pose une bordure et des bases de supports : des dizaines de milliers de mm² réellement au contact.
func TestValidateAdhesionUsesRealFirstLayer(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	info := fakeInfo(func(i *slicer.ArchiveInfo) { i.EnableSupport = "1"; i.BrimType = "auto_brim"; i.BrimWidth = "0" })

	// 1. G-code : bases de supports pleines + bordure autour : l'avertissement du maillage disparaît
	g := newGcodeBuilder()
	g.layer(0.2)
	g.feature("Brim")
	g.rect(8, 8, 192, 207)
	g.rect(9, 9, 191, 206)
	g.feature("Support")
	g.fill(20, 20, 180, 190)
	g.feature("Outer wall")
	g.rect(20, 20, 180, 190)
	g.layer(0.4)
	g.feature("Outer wall")
	g.rect(20, 20, 180, 190)
	j, rec := validatedJob(t, s, quatrePlotsSousBloc(), info)
	j = withGcode(t, s, j, g)
	out := validate(t, s, j, rec, 0)
	a := out.Report.Adhesion
	if a == nil || a.Source != "gcode" || !a.BrimInGcode {
		t.Fatalf("risque mesuré sur la couche 1 du G-code, bordure détectée : %+v", a)
	}
	if a.ContactAreaMM2 < 20000 || a.Risky() {
		t.Fatalf("surface réelle ≈ 27 000 mm², aucun risque : %+v", a)
	}
	if strings.Contains(strings.Join(out.Report.Warnings, "\n"), "adhesion_risk") {
		t.Fatalf("plus d'avertissement d'accroche : %v", out.Report.Warnings)
	}
	cur, _ := s.GetJob(ctx(), j.ID)
	if n := cur.adhesionNotes(); len(n) != 0 {
		t.Fatalf("liaison de confirmation : aucune note d'accroche attendue, reçu %v", n)
	}
	// le maillage seul, lui, restait alarmiste : c'est bien le G-code qui l'a levé
	if m := mesh.Adhesion(quatrePlotsSousBloc()); m.Level != mesh.AdhesionHigh || m.Source != "mesh" {
		t.Fatalf("estimation du maillage : %+v", m)
	}

	// 2. G-code sans bordure, quatre plots de 3 × 3 mm seulement : le risque reste, et propose d'ajouter une bordure
	g2 := newGcodeBuilder()
	g2.layer(0.2)
	g2.feature("Outer wall")
	for _, p := range [][2]float64{{20, 20}, {180, 20}, {20, 190}, {180, 190}} {
		g2.fill(p[0], p[1], p[0]+3, p[1]+3)
	}
	j2, _ := validatedJob2(t, s, quatrePlotsSousBloc(), info)
	j2 = withGcode(t, s, j2, g2)
	o2 := validate(t, s, j2, rec, 0)
	a2 := o2.Report.Adhesion
	if a2 == nil || a2.Source != "gcode" || a2.Level != mesh.AdhesionHigh || a2.BrimInGcode || a2.Islands != 4 {
		t.Fatalf("4 plots réels sans bordure : %+v", a2)
	}
	if !strings.Contains(a2.Recommendation, "Ajouter une bordure") || !strings.Contains(strings.Join(o2.Report.Warnings, "\n"), "adhesion_risk") {
		t.Fatalf("la recommandation d'ajouter une bordure doit rester : %q / %v", a2.Recommendation, o2.Report.Warnings)
	}

	// 3. mêmes plots AVEC une bordure dans le G-code : si le risque persiste, plus de « ajouter une bordure »
	g3 := newGcodeBuilder()
	g3.layer(0.2)
	g3.feature("Brim")
	for _, p := range [][2]float64{{20, 20}, {180, 20}, {20, 190}, {180, 190}} {
		g3.rect(p[0]-1, p[1]-1, p[0]+4, p[1]+4)
	}
	g3.feature("Outer wall")
	for _, p := range [][2]float64{{20, 20}, {180, 20}, {20, 190}, {180, 190}} {
		g3.fill(p[0], p[1], p[0]+3, p[1]+3)
	}
	j3, _ := validatedJob2(t, s, quatrePlotsSousBloc(), info)
	j3 = withGcode(t, s, j3, g3)
	o3 := validate(t, s, j3, rec, 0)
	a3 := o3.Report.Adhesion
	if a3 == nil || !a3.BrimInGcode || !a3.Risky() {
		t.Fatalf("bordure dans le G-code mais plots toujours trop petits : %+v", a3)
	}
	if strings.Contains(a3.Recommendation, "Ajouter une bordure") || !strings.Contains(a3.Recommendation, "déjà dans le G-code") {
		t.Fatalf("plus de « ajouter une bordure » quand elle est déjà dans le G-code : %q", a3.Recommendation)
	}
	cur3, _ := s.GetJob(ctx(), j3.ID)
	if n := cur3.adhesionNotes(); len(n) != 1 || strings.Contains(n[0], "Ajouter une bordure") || !strings.Contains(n[0], "couche 1 réelle du G-code") {
		t.Fatalf("liaison de confirmation : %v", n)
	}
}

func ctx() context.Context { return context.Background() }
