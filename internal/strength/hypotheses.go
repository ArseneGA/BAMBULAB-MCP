package strength

import (
	"fmt"
	"math"
	"strings"
)

// Hypothèses par défaut proposées quand l'humain ne connaît pas la charge. Rien n'est jamais appliqué en silence :
// chaque valeur supposée est annoncée à l'humain et marquée source « assumed » dans les entrées et le rapport.

// ObjectExample : objet courant et masse typique.
type ObjectExample struct {
	Key           string   `json:"key"`
	Label         string   `json:"label"`
	Keywords      []string `json:"-"`
	MassLowKg     float64  `json:"mass_low_kg"`
	MassHighKg    float64  `json:"mass_high_kg"`
	DynamicFactor float64  `json:"dynamic_factor" jsonschema:"majoration proposée pour les chocs et manipulations (1 = charge calme)"`
	Note          string   `json:"note"`
	Source        string   `json:"source"`
}

const exampleSource = "estimation : masses courantes du commerce (fourchettes arrondies), à remplacer par la pesée de l'objet réel"

// Examples : table documentée d'objets courants.
var Examples = []ObjectExample{
	{Key: "guitare", Label: "guitare (électrique, basse ou acoustique)", Keywords: []string{"guitar", "basse", "ukulele", "banjo", "mandoline"},
		MassLowKg: 1.8, MassHighKg: 4.5, DynamicFactor: 1.5, Note: "électrique 3,2 à 4,5 kg, basse 3,5 à 5 kg, acoustique 1,8 à 2,8 kg ; 1,5 pour les chocs quand on la repose"},
	{Key: "velo", Label: "vélo", Keywords: []string{"velo", "vélo", "bike", "bicycle", "vtt"},
		MassLowKg: 9, MassHighKg: 16, DynamicFactor: 2, Note: "vélo électrique 20 à 25 kg : donner la valeur réelle"},
	{Key: "livres", Label: "livres, étagère", Keywords: []string{"livre", "book", "etagere", "étagère", "shelf", "rayon", "tablette"},
		MassLowKg: 3, MassHighKg: 15, DynamicFactor: 1.2, Note: "un livre de poche 0,3 à 0,5 kg ; une rangée de 30 cm 3 à 8 kg ; ce total est partagé entre les appuis"},
	{Key: "ecran", Label: "écran d'ordinateur ou de télévision", Keywords: []string{"ecran", "écran", "monitor", "screen", "tv", "television", "télévision", "vesa"},
		MassLowKg: 3, MassHighKg: 8, DynamicFactor: 1.3, Note: "moniteur 24-27 pouces 3 à 8 kg, téléviseur 40 pouces 8 à 12 kg"},
	{Key: "outil", Label: "outil à main ou électroportatif", Keywords: []string{"outil", "tool", "perceuse", "drill", "tournevis", "pince", "marteau", "visseuse"},
		MassLowKg: 0.3, MassHighKg: 2.5, DynamicFactor: 1.5, Note: "tournevis 0,1 à 0,3 kg, perceuse-visseuse 1 à 2,5 kg"},
	{Key: "casque", Label: "casque (vélo, moto, audio)", Keywords: []string{"casque", "helmet", "headphone", "headset"},
		MassLowKg: 0.25, MassHighKg: 1.5, DynamicFactor: 1.5, Note: "casque audio 0,25 à 0,5 kg, casque de vélo 0,3 kg, casque de moto 1 à 1,5 kg"},
	{Key: "vetement", Label: "manteau, veste, sac", Keywords: []string{"manteau", "veste", "coat", "jacket", "patere", "patère", "cintre", "sac", "bag", "backpack"},
		MassLowKg: 0.5, MassHighKg: 5, DynamicFactor: 1.5, Note: "manteau 1 à 3 kg, sac à dos chargé 3 à 8 kg"},
	{Key: "plante", Label: "plante en pot", Keywords: []string{"plante", "pot", "plant", "jardiniere", "jardinière"},
		MassLowKg: 1, MassHighKg: 10, DynamicFactor: 1.2, Note: "pot avec terre humide : compter lourd"},
	{Key: "ustensile", Label: "tasse, ustensile, petit objet de cuisine", Keywords: []string{"tasse", "mug", "cup", "ustensile", "cuisine"},
		MassLowKg: 0.1, MassHighKg: 0.6, DynamicFactor: 1.2, Note: "tasse pleine 0,4 à 0,6 kg"},
	{Key: "telephone", Label: "téléphone, tablette", Keywords: []string{"telephone", "téléphone", "phone", "smartphone", "tablet", "tablette"},
		MassLowKg: 0.15, MassHighKg: 0.8, DynamicFactor: 1.5, Note: "téléphone 0,15 à 0,25 kg, tablette 0,4 à 0,8 kg"},
}

func init() {
	for i := range Examples {
		Examples[i].Source = exampleSource
	}
}

// MatchExample cherche un objet courant dans un texte libre (usage, nom, étiquettes).
func MatchExample(text string) (ObjectExample, bool) {
	t := strings.ToLower(text)
	for _, ex := range Examples {
		if strings.Contains(t, ex.Key) {
			return ex, true
		}
		for _, k := range ex.Keywords {
			if strings.Contains(t, k) {
				return ex, true
			}
		}
	}
	return ObjectExample{}, false
}

// Gravity : accélération de la pesanteur (m/s²).
const Gravity = 9.81

// SuggestedForceN : force proposée = masse haute × g × facteur dynamique (N).
func (e ObjectExample) SuggestedForceN() float64 {
	return math.Round(e.MassHighKg*Gravity*e.DynamicFactor*10) / 10
}

// Statement : phrase d'hypothèse à annoncer à l'humain.
func (e ObjectExample) Statement() string {
	return fmt.Sprintf("%s : en général %.3g à %.3g kg ; je pars sur %.3g kg × %.2g (chocs) = %.0f N, d'accord ?",
		e.Label, e.MassLowKg, e.MassHighKg, e.MassHighKg, e.DynamicFactor, e.SuggestedForceN())
}
