package printing

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

const secretValue = "SUPERSECRET-ACCESS-12345"

func TestFileSecretStoreEncryptedAndBound(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenFileSecretStore(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if st.HasAccessCode("prn_a") {
		t.Fatal("aucun code attendu")
	}
	if _, err := st.AccessCode(ctx, "prn_a"); err != ErrNoAccessCode {
		t.Fatalf("erreur attendue ErrNoAccessCode, reçu %v", err)
	}
	if err := st.SetAccessCode(ctx, "prn_a", secretValue); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAccessCode(ctx, "prn_b", "AUTRE-CODE-9999"); err != nil {
		t.Fatal(err)
	}
	got, err := st.AccessCode(ctx, "prn_a")
	if err != nil || got != secretValue {
		t.Fatalf("relecture : %q %v", got, err)
	}
	// Rien en clair sur le disque, ni dans le coffre ni dans la clé.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if bytes.Contains(b, []byte(secretValue)) || bytes.Contains(b, []byte("AUTRE-CODE")) {
			t.Fatalf("code d'accès en clair dans %s", e.Name())
		}
	}
	// Une entrée déplacée vers une autre imprimante ne se déchiffre pas (donnée authentifiée = identifiant).
	raw, _ := os.ReadFile(filepath.Join(dir, "printers.secrets"))
	var f secretFile
	_ = json.Unmarshal(raw, &f)
	f.Entries["prn_c"] = f.Entries["prn_a"]
	b, _ := json.Marshal(f)
	_ = os.WriteFile(filepath.Join(dir, "printers.secrets"), b, 0o600)
	if _, err := st.AccessCode(ctx, "prn_c"); err == nil {
		t.Fatal("une entrée copiée sous un autre identifiant ne doit pas se déchiffrer")
	}
	// Réouverture avec la même clé maître.
	st2, err := OpenFileSecretStore(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := st2.AccessCode(ctx, "prn_a"); got != secretValue {
		t.Fatal("clé maître non rechargée")
	}
	if p := st.Protection(); p != "dpapi" && p != "file-0600" {
		t.Fatalf("protection : %s", p)
	}
	// Le fournisseur n'affiche jamais la valeur.
	p := accessCodeProvider{store: st, id: "prn_a"}
	for _, s := range []string{fmt.Sprintf("%v", p), fmt.Sprintf("%+v", p), fmt.Sprintf("%#v", p), fmt.Sprintf("%s", p)} {
		if strings.Contains(s, secretValue) {
			t.Fatalf("fuite par fmt : %s", s)
		}
	}
}

func TestParseTrayAndBuildAMS(t *testing.T) {
	for _, c := range []struct {
		in   string
		id   int
		ext  bool
		unit int
		bad  bool
	}{{"ams1:1", 0, false, 1, false}, {"ams1:4", 3, false, 1, false}, {"ams2:3", 6, false, 2, false}, {"ext", 254, true, 0, false}, {"ams5:1", 0, false, 0, true}, {"AMS1:1", 0, false, 0, true}, {"", 0, false, 0, true}} {
		id, ext, unit, err := parseTray(c.in)
		if (err != nil) != c.bad || (!c.bad && (id != c.id || ext != c.ext || unit != c.unit)) {
			t.Errorf("parseTray(%q) = %d %v %d %v", c.in, id, ext, unit, err)
		}
	}
	m, use := buildAMS([]FilamentAssignment{{1, "ams1:2", "PLA"}, {3, "ams2:1", "PETG"}})
	if !use || len(m) != 5 || m[0] != 1 || m[1] != -1 || m[2] != 4 {
		t.Fatalf("mapping : %v %v", m, use)
	}
	if m, use := buildAMS([]FilamentAssignment{{1, "ext", "PLA"}}); use || m != nil {
		t.Fatalf("externe seule : pas d'AMS, reçu %v %v", m, use)
	}
}

func TestCheckAssignments(t *testing.T) {
	s := &Service{}
	_ = s
	yes := true
	model := contracts.PrinterModel{ID: "p1s", Name: "Bambu Lab P1S", AMSSupported: &yes}
	rec := PrinterRecord{ID: "prn_1", NozzleMM: 0.4, AMSUnits: 1}
	plate := &slicer.PlateResult{Index: 1, Filaments: []slicer.FilamentUse{{Slot: 1, Type: "PLA"}, {Slot: 2, Type: "PETG"}}}
	run := func(as []FilamentAssignment, strict bool) *ValidationReport {
		rep := &ValidationReport{}
		svc, cleanup := newBareService(t)
		defer cleanup()
		svc.checkAssignments(context.Background(), rep, rec, model, plate, as, strict)
		return rep
	}
	if r := run(nil, false); len(r.Blocking) != 0 || len(r.Warnings) == 0 {
		t.Fatalf("sans affectation à la validation : avertissement attendu : %+v", r)
	}
	if r := run(nil, true); len(r.Blocking) == 0 {
		t.Fatal("sans affectation à l'envoi : bloquant attendu")
	}
	ok := []FilamentAssignment{{1, "ams1:1", "PLA Basic"}, {2, "ams1:2", "PETG HF"}}
	if r := run(ok, true); len(r.Blocking) != 0 {
		t.Fatalf("cas valide refusé : %+v", r.Blocking)
	}
	for name, as := range map[string][]FilamentAssignment{
		"ams non déclaré":    {{1, "ams2:1", "PLA"}, {2, "ams1:2", "PETG"}},
		"emplacement double": {{1, "ams1:1", "PLA"}, {2, "ams1:1", "PETG"}},
		"filament manquant":  {{1, "ams1:1", "PLA"}},
		"filament double":    {{1, "ams1:1", "PLA"}, {1, "ams1:2", "PLA"}, {2, "ams1:3", "PETG"}},
		"emplacement faux":   {{1, "tiroir", "PLA"}, {2, "ams1:2", "PETG"}},
		"matière vide":       {{1, "ams1:1", " "}, {2, "ams1:2", "PETG"}},
	} {
		if r := run(as, true); len(r.Blocking) == 0 {
			t.Errorf("%s : bloquant attendu", name)
		}
	}
	// Famille de matière connue qui ne correspond pas au fichier découpé.
	svc, cleanup := newBareService(t)
	defer cleanup()
	if err := svc.d.Store.PutDoc(context.Background(), "t", "materials", "mat_abs", contracts.Material{ID: "mat_abs", Family: "ABS", Compatibility: contracts.CompatUnknown}); err != nil {
		t.Fatal(err)
	}
	rep := &ValidationReport{}
	svc.checkAssignments(context.Background(), rep, rec, model, plate, []FilamentAssignment{{1, "ams1:1", "mat_abs"}, {2, "ams1:2", "PETG"}}, true)
	found := false
	for _, b := range rep.Blocking {
		if strings.Contains(b, "material_family") {
			found = true
		}
	}
	if !found {
		t.Fatalf("incompatibilité de famille non détectée : %+v", rep)
	}
	// Pas d'AMS du tout sur le modèle.
	no := false
	rep = &ValidationReport{}
	svc.checkAssignments(context.Background(), rep, rec, contracts.PrinterModel{Name: "X", AMSSupported: &no}, plate, ok, true)
	if len(rep.Blocking) == 0 {
		t.Fatal("AMS non pris en charge : bloquant attendu")
	}
}

// ---- géométrie ----

func cubeMesh(s float64) *mesh.Mesh { return mesh.Cube(s) }

func TestGeometryOps(t *testing.T) {
	svc, cleanup := newBareService(t)
	defer cleanup()
	ctx := context.Background()
	m := cubeMesh(20)
	before := stats(m)
	out, applied, _, err := applyOps(ctx, svc, m, []GeometryOp{
		{Op: "scale", Factors: []float64{2, 1, 0.5}},
		{Op: "rotate", Axis: "z", Degrees: 90},
		{Op: "translate", Vector: []float64{10, 0, 5}},
	})
	if err != nil || len(applied) != 3 {
		t.Fatalf("%v %v", err, applied)
	}
	b := out.Bounds()
	// 40 x 20 x 10 puis tourné de 90° autour de Z : 20 x 40 x 10
	if math.Abs(b.Size.X-20) > 1e-6 || math.Abs(b.Size.Y-40) > 1e-6 || math.Abs(b.Size.Z-10) > 1e-6 {
		t.Fatalf("dimensions %+v", b.Size)
	}
	if got := stats(out).VolumeMM3; math.Abs(got-before.VolumeMM3) > 1e-3 {
		t.Fatalf("volume %v ≠ %v", got, before.VolumeMM3)
	}
	// Le maillage d'origine n'est pas modifié (jamais d'écrasement).
	if cubeBefore := stats(m); cubeBefore.SizeMM != before.SizeMM {
		t.Fatal("entrée modifiée")
	}
	// Miroir : volume conservé et normales sortantes (volume positif).
	mir, _, _, err := applyOps(ctx, svc, cubeMesh(10), []GeometryOp{{Op: "mirror", Plane: "yz"}})
	if err != nil || stats(mir).VolumeMM3 < 999 || !stats(mir).Watertight {
		t.Fatalf("miroir : %v %+v", err, stats(mir))
	}
	// Centrer sur un plateau.
	c, _, _, err := applyOps(ctx, svc, cubeMesh(20), []GeometryOp{{Op: "center_on_bed", BedX: 256, BedY: 256}})
	cb := c.Bounds()
	if err != nil || math.Abs(cb.Min.X-118) > 1e-6 || math.Abs(cb.Max.Y-138) > 1e-6 || cb.Min.Z != 0 {
		t.Fatalf("centrage : %v %+v", err, cb)
	}
	// Refus : facteur négatif ou nul, axe inconnu, opération inconnue, plateau absent.
	for _, op := range []GeometryOp{{Op: "scale", Factor: -1}, {Op: "scale"}, {Op: "rotate", Axis: "w"}, {Op: "explode"}, {Op: "center_on_bed"}, {Op: "translate"}, {Op: "mirror", Plane: "ab"}, {Op: "cut_plane", Axis: "z", Keep: "above"}} {
		if _, _, _, err := applyOps(ctx, svc, cubeMesh(5), []GeometryOp{op}); err == nil {
			t.Errorf("%+v : refus attendu", op)
		}
	}
}

func TestLayFlat(t *testing.T) {
	svc, cleanup := newBareService(t)
	defer cleanup()
	// Cube 10 incliné de 30° sur X puis 20° sur Y : la mise à plat doit retrouver une face d'appui à Z=0 sans changer le volume.
	m := cubeMesh(10)
	m = m.Transform(affine(rotAxis(0, 30), mesh.Vec3{X: 5, Y: 5, Z: 5}))
	m = m.Transform(affine(rotAxis(1, 20), mesh.Vec3{X: 5, Y: 5, Z: 5}))
	out, applied, _, err := applyOps(context.Background(), svc, m, []GeometryOp{{Op: "lay_flat"}})
	if err != nil {
		t.Fatal(err)
	}
	b := out.Bounds()
	if math.Abs(b.Min.Z) > 1e-6 || math.Abs(b.Size.Z-10) > 1e-4 {
		t.Fatalf("pose à plat : %+v (%v)", b, applied[0].Detail)
	}
	if math.Abs(stats(out).VolumeMM3-1000) > 1e-2 {
		t.Fatalf("volume %v", stats(out).VolumeMM3)
	}
}

func TestCutPlaneCapsAndHoles(t *testing.T) {
	svc, cleanup := newBareService(t)
	defer cleanup()
	ctx := context.Background()
	half := 5.0
	// Cube coupé à mi-hauteur : on garde le haut, capuchon posé, étanche, volume moitié.
	out, applied, warns, err := applyOps(ctx, svc, cubeMesh(10), []GeometryOp{{Op: "cut_plane", Axis: "z", Offset: &half, Keep: "above"}})
	if err != nil || len(warns) != 0 {
		t.Fatalf("%v %v", err, warns)
	}
	st := stats(out)
	if !st.Watertight || math.Abs(st.VolumeMM3-500) > 1e-3 || math.Abs(st.SizeMM[2]-5) > 1e-6 {
		t.Fatalf("coupe : %+v (%s)", st, applied[0].Detail)
	}
	// Garder le bas, autre axe.
	out, _, _, err = applyOps(ctx, svc, cubeMesh(10), []GeometryOp{{Op: "cut_plane", Axis: "x", Offset: &half, Keep: "below"}})
	if st := stats(out); err != nil || !st.Watertight || math.Abs(st.VolumeMM3-500) > 1e-3 {
		t.Fatalf("coupe x : %v %+v", err, st)
	}
	// Plan hors du modèle : refus explicite.
	far := 50.0
	if _, _, _, err := applyOps(ctx, svc, cubeMesh(10), []GeometryOp{{Op: "cut_plane", Axis: "z", Offset: &far, Keep: "above"}}); err == nil {
		t.Fatal("il ne resterait rien : refus attendu")
	}
	if _, _, _, err := applyOps(ctx, svc, cubeMesh(10), []GeometryOp{{Op: "cut_plane", Axis: "z", Offset: &far, Keep: "below"}}); err == nil {
		t.Fatal("tout est conservé : refus attendu")
	}
	// Pièce creuse (cube percé) : section à deux contours imbriqués -> pas de capuchon, avertissement.
	holed := mesh.BoxWithSquareHole(20, 6)
	mid := holed.Bounds().Min.Z + holed.Bounds().Size.Z/2
	_, ap, warns, err := applyOps(ctx, svc, holed, []GeometryOp{{Op: "cut_plane", Axis: "x", Offset: ptr(holed.Bounds().Min.X + holed.Bounds().Size.X/2), Keep: "above"}})
	_ = mid
	if err != nil {
		t.Fatalf("coupe d'une pièce percée : %v", err)
	}
	t.Logf("pièce percée : %s | %v", ap[0].Detail, warns)
	// Cohérence sur les trois axes : sans avertissement le résultat est étanche ; avec, la coupe reste ouverte et le dit.
	nestedSeen := false
	for _, ax := range []string{"x", "y", "z"} {
		bb := holed.Bounds()
		off := map[string]float64{"x": bb.Min.X + bb.Size.X/2, "y": bb.Min.Y + bb.Size.Y/2, "z": bb.Min.Z + bb.Size.Z/2}[ax]
		cut, _, w, err := applyOps(ctx, svc, holed, []GeometryOp{{Op: "cut_plane", Axis: ax, Offset: &off, Keep: "below"}})
		if err != nil {
			t.Fatalf("axe %s : %v", ax, err)
		}
		if len(w) == 0 && !stats(cut).Watertight {
			t.Errorf("axe %s : pas d'avertissement mais maillage ouvert (%d arêtes)", ax, stats(cut).OpenEdges)
		}
		if len(w) > 0 && strings.Contains(w[0], "imbriqués") {
			nestedSeen = true
			if stats(cut).Watertight {
				t.Errorf("axe %s : avertissement de capuchon absent mais maillage étanche ?", ax)
			}
		}
	}
	if !nestedSeen {
		t.Error("aucune coupe de la pièce percée n'a traversé le trou dans l'axe : jeu de test à revoir")
	}
}

func ptr(f float64) *float64 { return &f }

// ---- aperçus ----

func TestRenderIsoPNG(t *testing.T) {
	o := renderIso(cubeMesh(20), "cube.stl", strings.Repeat("a", 64), 400, 300)
	img, err := png.Decode(bytes.NewReader(o.PNG))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 400 || img.Bounds().Dy() != 300+legendH {
		t.Fatalf("taille %v", img.Bounds())
	}
	// Le fond vaut (0xf3,0xf1,0xed) ; le modèle (bleu) doit couvrir une part notable et le coin rester du fond.
	bg, blue := 0, 0
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			switch {
			case r>>8 == 0xf3 && g>>8 == 0xf1 && b>>8 == 0xed:
				bg++
			case b > r:
				blue++
			}
		}
	}
	if blue < 5000 || bg < 20000 {
		t.Fatalf("rendu douteux : fond=%d modèle=%d", bg, blue)
	}
	if len(o.Legend) < 4 || !strings.Contains(strings.Join(o.Legend, " "), "isometrique") || len(o.NotRepresented) == 0 || o.Render["projection"] == nil {
		t.Fatalf("légende : %+v", o)
	}
	// Maillage vide : pas de panique.
	if e := renderIso(&mesh.Mesh{}, "vide", "", 200, 200); len(e.PNG) == 0 {
		t.Fatal("PNG vide attendu valide")
	}
}

func fakeGcode3MF(t *testing.T) string {
	t.Helper()
	g := "; HEADER\nG90\nM83\n; CHANGE_LAYER\n; Z_HEIGHT: 0.2\n; FEATURE: Outer wall\nG1 X10 Y10 F3000\nG1 X50 Y10 E1.2\nG1 X50 Y50 E1.2\n; FEATURE: Sparse infill\nG1 X10 Y50 E1.2\nG1 X10 Y10 E1.2\n" +
		"; CHANGE_LAYER\n; Z_HEIGHT: 0.4\n; FEATURE: Outer wall\nG1 X20 Y20\nG1 X30 Y20 E1\nG1 X30 Y30 E1\n"
	p := filepath.Join(t.TempDir(), "a.gcode.3mf")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("Metadata/plate_1.gcode")
	w.Write([]byte(g))
	zw.Close()
	f.Close()
	return p
}

func TestRenderLayer(t *testing.T) {
	p := fakeGcode3MF(t)
	bed := &contracts.Dimensions{X: 256, Y: 256, Z: 250}
	o, err := renderLayer(p, strings.Repeat("b", 64), 1, 1, 500, 400, bed)
	if err != nil {
		t.Fatal(err)
	}
	if o.Kind != "gcode_layer" || o.Render["segments"].(int) != 4 || o.Render["layers_total"].(int) != 2 {
		t.Fatalf("%+v", o.Render)
	}
	if _, err := png.Decode(bytes.NewReader(o.PNG)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(o.Legend, " "), "Outer wall") {
		t.Fatalf("légende sans types : %v", o.Legend)
	}
	o2, err := renderLayer(p, "x", 1, 2, 300, 300, nil)
	if err != nil || o2.Render["segments"].(int) != 2 {
		t.Fatalf("couche 2 : %v %v", err, o2)
	}
	if _, err := renderLayer(p, "x", 1, 9, 300, 300, nil); err == nil {
		t.Fatal("couche hors plage : erreur attendue")
	}
	if _, err := renderLayer(p, "x", 4, 1, 300, 300, nil); err == nil {
		t.Fatal("plaque absente : erreur attendue")
	}
}

func TestDrawTextAndLegendWrap(t *testing.T) {
	o := renderIso(cubeMesh(5), "un nom de modèle très très long avec des accents éèà et plus encore pour forcer le retour à la ligne de la légende", "", 300, 200)
	if _, err := png.Decode(bytes.NewReader(o.PNG)); err != nil {
		t.Fatal(err)
	}
}
