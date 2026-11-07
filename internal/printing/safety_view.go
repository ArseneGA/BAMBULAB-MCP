package printing

import (
	"fmt"
	"strings"
)

// SafetyView : ce que le G-code de la plaque à imprimer demande vraiment à l'imprimante (températures lues
// dans le G-code lui-même, matières, origine du G-code de démarrage). Affiché sur la page de confirmation
// humaine et dans la vue de confirmation MCP : un fichier manipulé ne peut pas le masquer, car tout vient de
// l'analyse de l'archive (slicer.ValidateArchive), jamais du modèle.
type SafetyView struct {
	Analysed          bool     `json:"analysed" jsonschema:"faux : le G-code n'a pas été analysé (anomalie)"`
	Materials         []string `json:"materials,omitempty" jsonschema:"matières lues dans le G-code (type de filament par emplacement)"`
	NozzleStartMaxC   float64  `json:"nozzle_start_max_c"`
	NozzleFirstLayerC float64  `json:"nozzle_first_layer_c"`
	NozzlePrintMinC   float64  `json:"nozzle_print_min_c"`
	NozzlePrintMaxC   float64  `json:"nozzle_print_max_c"`
	BedFirstLayerC    float64  `json:"bed_first_layer_c"`
	BedMinC           float64  `json:"bed_min_c"`
	BedMaxC           float64  `json:"bed_max_c"`
	ChamberMaxC       float64  `json:"chamber_max_c,omitempty"`
	// GcodeProfileLine : « G-code de démarrage : profil officiel ✓ » ou la raison du doute.
	GcodeProfileLine string `json:"gcode_profile_line"`
	GcodeOfficial    bool   `json:"gcode_official"`
	// Anomalies : tout ce qui doit attirer l'œil (affiché en rouge).
	Anomalies []string `json:"anomalies,omitempty"`
}

// safetyOf construit la vue de sûreté d'une impression à partir de son résumé d'archive et de son rapport de
// validation. Jamais nil : une absence d'analyse est elle-même une anomalie.
func safetyOf(j Job) *SafetyView {
	v := &SafetyView{GcodeProfileLine: "G-code de démarrage : non vérifié"}
	plate := j.Plate
	if plate == 0 {
		plate = 1
	}
	if j.Summary != nil {
		for _, p := range j.Summary.Plates {
			if p.Index != plate {
				continue
			}
			for _, f := range p.Filaments {
				t := strings.TrimSpace(f.Type)
				if t == "" {
					t = "matière inconnue"
				}
				v.Materials = append(v.Materials, fmt.Sprintf("emplacement %d : %s", f.Slot, t))
			}
			if s := p.Safety; s != nil {
				v.Analysed = true
				v.NozzleStartMaxC, v.NozzleFirstLayerC = s.NozzleStartMaxC, s.NozzleFirstLayerC
				v.NozzlePrintMinC, v.NozzlePrintMaxC = s.NozzlePrintMinC, s.NozzlePrintMaxC
				v.BedFirstLayerC, v.BedMinC, v.BedMaxC = s.BedFirstLayerC, s.BedMinC, s.BedMaxC
				v.ChamberMaxC = s.ChamberMaxC
			}
		}
		if gp := j.Summary.GcodeProfile; gp != nil {
			switch {
			case gp.Official:
				v.GcodeOfficial = true
				v.GcodeProfileLine = "G-code de démarrage : profil officiel ✓"
			case len(gp.Mismatch) > 0:
				v.GcodeProfileLine = "G-code de démarrage : DIFFÉRENT du profil officiel ✗ (" + strings.Join(gp.Mismatch, ", ") + ")"
			default:
				v.GcodeProfileLine = "G-code de démarrage : non vérifiable (" + gp.Detail + ")"
			}
		}
	}
	if !v.Analysed {
		v.Anomalies = append(v.Anomalies, "Le G-code n'a pas été analysé : températures inconnues.")
	}
	if j.Summary == nil || j.Summary.GcodeProfile == nil || !v.GcodeOfficial {
		v.Anomalies = append(v.Anomalies, v.GcodeProfileLine)
	}
	if j.Validation != nil {
		for _, c := range j.Validation.Archive {
			if c.OK {
				continue
			}
			n := c.Name
			if strings.HasSuffix(n, "unsafe_gcode") || strings.HasSuffix(n, "gcode_commands") || strings.HasSuffix(n, "filament_range") {
				v.Anomalies = append(v.Anomalies, n+" : "+c.Detail)
			}
		}
	}
	return v
}
