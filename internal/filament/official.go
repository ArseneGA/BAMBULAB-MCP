package filament

import (
	"strings"

	filcatalog "github.com/ArseneGA/BAMBULAB-MCP/internal/filament/catalog"
)

// OfficialColor rattache une couleur à une entrée du catalogue officiel Bambu (B3, internal/filament/catalog).
// Lecture seule : aucune action sur l'imprimante, aucun appel réseau.
type OfficialColor struct {
	ID         string  `json:"id" jsonschema:"identifiant du catalogue (GFA00-10300)"`
	Name       string  `json:"name" jsonschema:"nom officiel (français, à défaut anglais)"`
	NameEN     string  `json:"name_en,omitempty"`
	Hex        string  `json:"hex" jsonschema:"#RRGGBB officiel (première couleur d'un dégradé)"`
	Family     string  `json:"family" jsonschema:"gamme Bambu (PLA Basic, PETG HF...)"`
	Finish     string  `json:"finish" jsonschema:"finition de la gamme"`
	Linked     bool    `json:"linked" jsonschema:"vrai si la bobine est rattachée à cette entrée (catalog_color_id) ; faux = simple suggestion, la plus proche"`
	DeltaE     float64 `json:"delta_e" jsonschema:"écart ΔE (CIE76) avec la couleur de la bobine ou voulue"`
	Quality    string  `json:"quality" jsonschema:"exact | proche | approximatif | aucune"`
	Confidence string  `json:"confidence" jsonschema:"official_profile | official_site | estimated"`
}

func officialFrom(c filcatalog.Color, delta float64, linked bool) *OfficialColor {
	return &OfficialColor{
		ID: c.ID, Name: c.NameFR(), NameEN: c.Name["en"], Hex: c.PrimaryHex(), Family: c.FamilyName, Finish: c.Finish,
		Linked: linked, DeltaE: delta, Quality: filcatalog.Quality(delta), Confidence: c.Confidence,
	}
}

// nearestOfficial renvoie la couleur officielle la plus proche de hex, dans la classe de matière donnée quand elle
// est connue ; nil si l'écart dépasse maxDelta (rien n'est inventé) ou si le catalogue est indisponible.
func nearestOfficial(hex, material string, maxDelta float64) *OfficialColor {
	cat, err := filcatalog.Default()
	if err != nil || filcatalog.NormHex(hex) == "" {
		return nil
	}
	fam := materialFamily(material)
	ms, err := cat.Search(filcatalog.Query{Hex: hex, Family: fam, MaxDelta: maxDelta, Limit: 1})
	if err != nil || len(ms) == 0 {
		if fam != "" { // classe sans entrée au catalogue : on cherche dans tout le catalogue
			if ms, err = cat.Search(filcatalog.Query{Hex: hex, MaxDelta: maxDelta, Limit: 1}); err != nil || len(ms) == 0 {
				return nil
			}
		} else {
			return nil
		}
	}
	return officialFrom(ms[0].Color, *ms[0].DeltaE, false)
}

// attachOfficial calcule la couleur officielle d'une bobine : l'entrée rattachée, sinon la plus proche (suggestion).
func (sp *Spool) attachOfficial() {
	sp.Official = nil
	cat, err := filcatalog.Default()
	if err != nil {
		return
	}
	if sp.CatalogColorID != "" {
		if c, ok := cat.Color(sp.CatalogColorID); ok {
			d := 0.0
			if sp.ColorHex != "" {
				d = 999
				for _, h := range c.Hex {
					if x := filcatalog.Distance(sp.ColorHex, h); x < d {
						d = x
					}
				}
			}
			sp.Official = officialFrom(*c, d, true)
			return
		}
	}
	if sp.ColorHex != "" {
		sp.Official = nearestOfficial(sp.ColorHex, sp.Material, 30)
	}
}

// applyCatalogColor rattache la bobine à une entrée du catalogue et préremplit les champs encore vides.
func (sp *Spool) applyCatalogColor(id string) error {
	cat, err := filcatalog.Default()
	if err != nil {
		return invalidf("catalogue de filaments indisponible : %v", err)
	}
	if strings.TrimSpace(id) == "-" { // détacher
		sp.CatalogColorID = ""
		return nil
	}
	c, ok := cat.Color(id)
	if !ok {
		return invalidf("catalog_color_id %q inconnu : voir search_filament_catalog", id)
	}
	f, _ := cat.Family(c.FamilyID)
	sp.CatalogColorID = c.ID
	sp.markHuman("catalog_color_id")
	if sp.Material == "" && f != nil {
		sp.Material = f.Class
	}
	if sp.Brand == "" {
		sp.Brand = "Bambu Lab"
	}
	if sp.Name == "" {
		sp.Name = c.NameFR()
	}
	if sp.ColorHex == "" {
		sp.ColorHex = c.PrimaryHex()
	}
	if f != nil && f.Translucent && !sp.isHuman("translucent") {
		sp.Translucent = true
	}
	if f != nil && f.DiameterMM != nil && !sp.isHuman("diameter_mm") {
		sp.DiameterMM = *f.DiameterMM
	}
	return nil
}
