package engineering

import (
	"sort"
	"strings"
)

// ScrewKind : mode de fixation dans le plastique.
type ScrewKind string

const (
	ScrewSelfTapping ScrewKind = "selftapping" // vis autotaraudeuse / formeuse directement dans la pièce
	ScrewHeatSet     ScrewKind = "heatset"     // insert fileté laiton à chaud
)

type screwRow struct {
	major     float64 // diamètre nominal de la vis
	pilot     float64 // trou pour vis autotaraudeuse
	insertOD  float64 // trou pour insert à chaud
	insertLen float64 // longueur d'insert typique
}

// Table indicative : M2–M8.
var screwTable = map[string]screwRow{
	"M2":   {2, 1.7, 3.2, 4.0},
	"M2.5": {2.5, 2.1, 3.6, 5.0},
	"M3":   {3, 2.5, 4.0, 5.7},
	"M4":   {4, 3.4, 5.6, 8.1},
	"M5":   {5, 4.3, 6.4, 9.5},
	"M6":   {6, 5.2, 8.0, 12.7},
	"M8":   {8, 7.0, 10.0, 12.7},
}

// ScrewSizes liste les tailles supportées.
func ScrewSizes() []string {
	s := make([]string, 0, len(screwTable))
	for k := range screwTable {
		s = append(s, k)
	}
	sort.Strings(s)
	return s
}

// ScrewResult : Value = diamètre de trou recommandé (mm).
type ScrewResult struct {
	Result
	Kind      ScrewKind `json:"kind"`
	Size      string    `json:"size"`
	BossOD    float64   `json:"boss_outer_diameter_mm"`
	HoleDepth float64   `json:"hole_depth_mm,omitempty"`
	InsertLen float64   `json:"typical_insert_length_mm,omitempty"`
	MinWallMM float64   `json:"min_wall_around_hole_mm"`
}

// ScrewBoss recommande le diamètre de trou et le plot pour une vis autotaraudeuse ou un insert à chaud.
func ScrewBoss(size string, kind ScrewKind, material string) (*ScrewResult, error) {
	key := strings.ToUpper(strings.TrimSpace(size))
	row, ok := screwTable[key]
	if !ok {
		return nil, invalid("taille %q hors M2–M8 (supportées : %s)", size, strings.Join(ScrewSizes(), ", "))
	}
	k := ScrewKind(strings.ToLower(strings.TrimSpace(string(kind))))
	var warns []string
	asm := []string{}
	srcs := []Source{litSource}
	if material != "" {
		m, err := GetMaterial(material)
		if err != nil {
			return nil, err
		}
		if m.Family == "TPU" {
			warns = append(warns, "TPU : ni vis autotaraudeuse ni insert à chaud fiables (matériau souple).")
		}
		if m.Family == "PVA/support" {
			return nil, invalid("PVA/support n'est pas un matériau structurel")
		}
	}
	res := &ScrewResult{Kind: k, Size: key}
	switch k {
	case ScrewSelfTapping:
		res.Value = row.pilot
		res.BossOD = round(2.2*row.major+0.5, 1)
		res.MinWallMM = round((res.BossOD-row.pilot)/2, 2)
		asm = append(asm,
			"Vis métrique à filetage formeur ou vis à plastique, trou ≈ 0,85 × diamètre nominal",
			"Plot cylindrique, diamètre extérieur ≈ 2,2 × d + 0,5 mm, parois ≥ 3 périmètres",
		)
		warns = append(warns, "Visser une seule fois à fond de préférence : un filetage formé dans le plastique se dégrade au remontage ; éviter le serrage fort.")
	case ScrewHeatSet:
		res.Value = row.insertOD
		res.InsertLen = row.insertLen
		res.HoleDepth = round(row.insertLen+1.5, 1)
		res.BossOD = round(row.insertOD+2*1.6, 1)
		res.MinWallMM = 1.6
		asm = append(asm,
			"Insert laiton à chaud standard ; trou = diamètre du trou recommandé pour l'insert (cône d'entrée plus large)",
			"Profondeur = longueur d'insert + 1,5 mm pour le refoulement du plastique ; paroi ≥ 1,6 mm autour du trou",
		)
		warns = append(warns, "Dimensions d'inserts variables selon le fabricant : vérifier la fiche de l'insert réel (diamètre de trou recommandé).")
	default:
		return nil, invalid("type %q inconnu (selftapping|heatset)", kind)
	}
	res.Unit = "mm"
	res.Range = &Range{Min: round(res.Value-0.1, 2), Max: round(res.Value+0.1, 2)}
	res.Result.Assumptions = asm
	res.Result.Validity = "M2–M8, PLA/PETG/ABS/ASA/PC/PA, buse 0,4 mm, trou vertical (axe Z) ; en trou horizontal ajouter la compensation de trou. " + Disclaimer
	res.Result.Level = LevelHeuristic
	res.Result.Sources = srcs
	res.Result.Warnings = warns
	return res, nil
}

// TorqueScrew : couple de serrage T = K·F·d (N·mm), K ≈ 0,2 par défaut.
func TorqueScrew(preload, diameter, k float64) (*Result, error) {
	if err := positive("précharge", preload); err != nil {
		return nil, err
	}
	if err := positive("diamètre", diameter); err != nil {
		return nil, err
	}
	if k == 0 {
		k = 0.2
	}
	if err := positive("K", k); err != nil {
		return nil, err
	}
	return &Result{
		Value: k * preload * diameter, Unit: "N·mm",
		Assumptions: []string{"T = K·F·d avec K = " + ftoa(k) + " (filetage lubrifié ou non à préciser)"},
		Validity:    "Vis métriques ; précharge limite d'un filetage imprimé ≤ 0,5·σ_adm·A_s (non calculée ici). " + Disclaimer,
		Level:       LevelHeuristic, Sources: []Source{litSource},
	}, nil
}
