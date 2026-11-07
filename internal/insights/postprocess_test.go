package insights

import (
	"errors"
	"strings"
	"testing"
)

func stepByTech(a *PostProcessAdvice, id string) *AdviceStep {
	for i := range a.Steps {
		if a.Steps[i].Technique == id {
			return &a.Steps[i]
		}
	}
	return nil
}

func rejected(a *PostProcessAdvice, id string) *Rejected {
	for i := range a.NotRecommended {
		if a.NotRecommended[i].Technique == id {
			return &a.NotRecommended[i]
		}
	}
	return nil
}

func TestPostProcessABSSmoothWithDangers(t *testing.T) {
	a, err := PostProcessingAdvice(PostProcessInput{Material: "ABS", Finish: "surface lisse et brillante"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Family != "ABS" || a.Level != "heuristic" || a.DataVersion == "" || len(a.Sources) == 0 {
		t.Fatalf("en-tête : %+v", a)
	}
	for _, g := range []string{"smooth", "gloss"} {
		if !hasGoal(a.Goals, g) {
			t.Errorf("finition %s non reconnue : %v", g, a.Goals)
		}
	}
	sm := stepByTech(a, "chemical_smoothing")
	if sm == nil || sm.Applies != "yes" {
		t.Fatalf("lissage à l'acétone attendu pour l'ABS : %+v", a.Steps)
	}
	h := strings.Join(sm.Hazards, " ")
	for _, want := range []string{"inflammable", "explosif", "flamme"} {
		if !strings.Contains(h, want) {
			t.Errorf("danger %q absent : %s", want, h)
		}
	}
	// ordre : préparation, ponçage, apprêt, lissage chimique, vernis
	order := []string{}
	for _, s := range a.Steps {
		order = append(order, s.Technique)
	}
	want := []string{"cleanup", "sanding", "filler_primer", "chemical_smoothing", "clear_coat"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("ordre : %v", order)
	}
	for i, s := range a.Steps {
		if s.Order != i+1 {
			t.Errorf("numérotation : %+v", s)
		}
	}
	if sand := stepByTech(a, "sanding"); sand == nil || sand.Parameters["grains"] == "" {
		t.Errorf("grains du ponçage : %+v", sand)
	}
}

func TestPostProcessPLASmoothRefusesChemistryHonestly(t *testing.T) {
	a, err := PostProcessingAdvice(PostProcessInput{Material: "Bambu PLA Basic", Finish: "lisse"})
	if err != nil {
		t.Fatal(err)
	}
	r := rejected(a, "chemical_smoothing")
	if r == nil || !strings.Contains(r.Reason, "acétone") || !strings.Contains(r.Reason, "toxiques") {
		t.Fatalf("lissage chimique du PLA à refuser avec raison : %+v", a.NotRecommended)
	}
	if stepByTech(a, "chemical_smoothing") != nil {
		t.Error("aucun lissage chimique conseillé pour le PLA")
	}
	if stepByTech(a, "filler_primer") == nil {
		t.Error("l'apprêt garnissant est la voie conseillée pour le PLA")
	}
	for _, p := range []string{"PETG", "PC", "PA", "TPU"} {
		a, err := PostProcessingAdvice(PostProcessInput{Material: p, Finish: "lisse"})
		if err != nil {
			t.Fatal(err)
		}
		if rejected(a, "chemical_smoothing") == nil || stepByTech(a, "chemical_smoothing") != nil {
			t.Errorf("%s : pas de lissage chimique", p)
		}
	}
	// ASA : possible mais limité
	asa, _ := PostProcessingAdvice(PostProcessInput{Material: "ASA", Finish: "lisse"})
	if s := stepByTech(asa, "chemical_smoothing"); s == nil || s.Applies != "limited" || !strings.Contains(strings.Join(s.Hazards, " "), "inflammable") {
		t.Errorf("ASA : %+v", s)
	}
	// TPU : pas de ponçage
	tpu, _ := PostProcessingAdvice(PostProcessInput{Material: "TPU", Finish: "lisse"})
	if rejected(tpu, "sanding") == nil {
		t.Error("TPU : ponçage déconseillé")
	}
}

func TestPostProcessTransparencyIsHonest(t *testing.T) {
	for _, m := range []string{"PETG", "PC"} {
		a, err := PostProcessingAdvice(PostProcessInput{Material: m, Finish: "transparente", Usage: "lentille"})
		if err != nil {
			t.Fatal(err)
		}
		st := stepByTech(a, "transparent_finish")
		if st == nil || st.Applies != "limited" {
			t.Fatalf("%s : %+v", m, a.Steps)
		}
		if !strings.Contains(strings.Join(a.Honesty, " "), "translucide") || !strings.Contains(strings.Join(a.Honesty, " "), "verre") {
			t.Errorf("%s : honnêteté sur la transparence manquante : %v", m, a.Honesty)
		}
		if !strings.Contains(strings.Join(st.Steps, " "), "2000") {
			t.Errorf("%s : ponçage jusqu'à 2000 attendu", m)
		}
		if stepByTech(a, "paint") != nil {
			t.Errorf("%s : pas de peinture pour une pièce transparente", m)
		}
	}
	abs, _ := PostProcessingAdvice(PostProcessInput{Material: "ABS", Finish: "transparent"})
	if rejected(abs, "transparent_finish") == nil || !strings.Contains(strings.Join(abs.Honesty, " "), "opaque") {
		t.Errorf("ABS transparent : %+v", abs.NotRecommended)
	}
}

func TestPostProcessInsertsUseEngineeringDiameters(t *testing.T) {
	a, err := PostProcessingAdvice(PostProcessInput{Material: "PETG", Finish: "inserts filetés", InsertSizes: []string{"M3", "m5"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Inserts) != 2 {
		t.Fatalf("inserts : %+v", a.Inserts)
	}
	m3 := a.Inserts[0]
	if m3.Size != "M3" || m3.HoleDiameterMM != 4.0 || m3.HoleDepthMM != 7.2 || m3.MinWallMM != 1.6 || m3.TipStartTemp == "" || m3.Level != "heuristic" {
		t.Errorf("M3 : %+v", m3)
	}
	if a.Inserts[1].Size != "M5" || a.Inserts[1].HoleDiameterMM != 6.4 {
		t.Errorf("M5 : %+v", a.Inserts[1])
	}
	st := stepByTech(a, "heatset_inserts")
	if st == nil || len(st.Steps) == 0 {
		t.Fatalf("étapes d'insertion : %+v", st)
	}
	// taille hors plage : avertissement, pas d'échec
	b, err := PostProcessingAdvice(PostProcessInput{Material: "PLA", Finish: "insert", InsertSizes: []string{"M12"}})
	if err != nil || len(b.Inserts) != 0 || !strings.Contains(strings.Join(b.Warnings, "|"), "M12") {
		t.Errorf("M12 : %v %+v", err, b.Warnings)
	}
	// sans taille : exemples M3 et M4
	c, _ := PostProcessingAdvice(PostProcessInput{Material: "ABS", Finish: "inserts"})
	if len(c.Inserts) != 2 {
		t.Errorf("exemples : %+v", c.Inserts)
	}
	// TPU : refus motivé
	d, _ := PostProcessingAdvice(PostProcessInput{Material: "TPU", Finish: "inserts"})
	if rejected(d, "heatset_inserts") == nil || len(d.Inserts) != 0 {
		t.Errorf("TPU : %+v", d.NotRecommended)
	}
}

func TestPostProcessAnnealingBondingAndUsage(t *testing.T) {
	a, err := PostProcessingAdvice(PostProcessInput{Material: "PLA", Finish: "recuit pour la chaleur", Usage: "pièce de voiture en plein soleil, contact alimentaire, tolérances serrées", Geometry: "parois fines et cavités"})
	if err != nil {
		t.Fatal(err)
	}
	an := stepByTech(a, "annealing")
	if an == nil || an.Applies != "limited" || !strings.Contains(strings.Join(an.Notes, " "), "fléchissement") {
		t.Fatalf("recuit PLA avec valeur de fiche : %+v", an)
	}
	w := strings.Join(a.Warnings, "\n")
	for _, want := range []string{"alimentaire", "extérieur", "cotes critiques", "Parois ou détails fins", "Cavités", "55 à 60"} {
		if !strings.Contains(w, want) {
			t.Errorf("avertissement %q absent :\n%s", want, w)
		}
	}
	pet, _ := PostProcessingAdvice(PostProcessInput{Material: "PETG", Finish: "recuit"})
	if r := rejected(pet, "annealing"); r == nil || !strings.Contains(r.Reason, "amorphe") {
		t.Errorf("PETG recuit : %+v", pet.NotRecommended)
	}
	for m, want := range map[string]string{"PLA": "cyanoacrylate", "ABS": "acétone", "TPU": "souple", "PETG": "époxy"} {
		b, err := PostProcessingAdvice(PostProcessInput{Material: m, Finish: "collage"})
		if err != nil {
			t.Fatal(err)
		}
		st := stepByTech(b, "bonding")
		if st == nil || !strings.Contains(strings.ToLower(strings.Join(st.Steps, " ")), want) {
			t.Errorf("collage %s : %+v", m, st)
		}
		if !strings.Contains(strings.Join(st.Hazards, " "), "Cyanoacrylate") {
			t.Errorf("collage %s : dangers communs attendus", m)
		}
	}
}

func TestPostProcessCarbonFibreAndValidation(t *testing.T) {
	a, err := PostProcessingAdvice(PostProcessInput{Material: "PA6-CF", Finish: "mat"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Family != "PA-CF" || !strings.Contains(strings.Join(a.Warnings, " "), "fibres de carbone") || stepByTech(a, "sanding") == nil {
		t.Errorf("PA-CF : %+v", a.Family)
	}
	// finition non précisée : avertissement et conseils par défaut
	d, err := PostProcessingAdvice(PostProcessInput{Material: "PLA"})
	if err != nil || len(d.Steps) == 0 || !strings.Contains(strings.Join(d.Warnings, " "), "non reconnue") {
		t.Errorf("sans finition : %v", err)
	}
	for name, in := range map[string]PostProcessInput{
		"sans matière": {}, "matière inconnue": {Material: "bois"}, "PVA": {Material: "PVA", Finish: "lisse"},
	} {
		if _, err := PostProcessingAdvice(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s : %v", name, err)
		}
	}
}
