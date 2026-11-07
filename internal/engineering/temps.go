package engineering

import (
	"math"
	"strings"
)

// bblTempSource : plages lues dans les profils de base filament de Bambu Studio 02.08 (resources/profiles/BBL/filament/fdm_filament_*.json,
// clés nozzle_temperature_range_low / nozzle_temperature_range_high). Le profil du filament réellement utilisé
// (par exemple « Bambu PETG HF @BBL P1S ») prime toujours quand il est connu : voir internal/calibration.
var bblTempSource = Source{ID: "bbl_filament_profiles", Title: "Profils de base filament Bambu Studio 02.08 (nozzle_temperature_range_low/high)", Date: "2026-11-07"}

// nozzleRanges : famille -> [bas, haut] en °C (valeurs des profils fdm_filament_* ; défaut commun 190-240 pour PLA et dérivés).
var nozzleRanges = map[string][2]float64{
	"PLA":  {190, 240},
	"PETG": {220, 260},
	"PET":  {220, 260},
	"ABS":  {240, 280},
	"ASA":  {240, 280},
	"TPU":  {200, 250},
	"PC":   {260, 290},
	"PA":   {260, 300},
	"PVA":  {190, 240},
}

// NozzleTempRange donne la plage de température de buse recommandée pour un matériau (id de la base matériaux, famille ou
// nom : « PETG-HF », « PA-CF », « pla »...). ok=false si la famille est inconnue. Niveau : heuristique (profils par défaut
// de Bambu Studio, pas une mesure sur la bobine de l'utilisateur).
func NozzleTempRange(material string) (lo, hi float64, src Source, ok bool) {
	n := strings.ToUpper(strings.TrimSpace(material))
	if m, err := GetMaterial(material); err == nil && m != nil {
		n = strings.ToUpper(m.Family + "-" + m.ID)
	}
	// du plus spécifique au plus général : PETG avant PET, PA/PC/PLA : préfixe de la famille
	for _, fam := range []string{"PETG", "PLA", "ABS", "ASA", "TPU", "PVA", "PET", "PC", "PA"} {
		if strings.HasPrefix(n, fam) || strings.Contains(n, "-"+fam) || strings.Contains(n, " "+fam) {
			r := nozzleRanges[fam]
			return r[0], r[1], bblTempSource, true
		}
	}
	return 0, 0, Source{}, false
}

// TemperatureSeries répartit n températures (n >= 2) régulièrement entre lo et hi, arrondies à 5 °C, sans doublon.
func TemperatureSeries(lo, hi float64, n int) []float64 {
	if n < 2 || hi <= lo {
		return []float64{math.Round(lo/5) * 5}
	}
	var out []float64
	for i := 0; i < n; i++ {
		v := math.Round((lo+(hi-lo)*float64(i)/float64(n-1))/5) * 5
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	return out
}
