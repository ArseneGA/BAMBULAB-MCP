package filcatalog

import (
	"fmt"
	"strings"
)

// SearchResult est la réponse de search_filament_catalog (outil MCP et API web).
type SearchResult struct {
	Colors   []Match           `json:"colors" jsonschema:"couleurs officielles trouvées (par distance croissante si hex est donné)"`
	Families []Family          `json:"families" jsonschema:"gammes concernées, avec températures, finition et compatibilité de buse"`
	Compat   map[string]Compat `json:"nozzle_compat,omitempty" jsonschema:"gamme (filament_id) -> verdict de compatibilité avec la buse demandée"`
	Total    int               `json:"total_colors" jsonschema:"nombre de couleurs du catalogue qui répondent (avant limite)"`
	Catalog  Stats             `json:"catalog"`
	Sources  []Source          `json:"sources"`
	Note     string            `json:"note"`
}

// RunSearch exécute une recherche et regroupe les gammes concernées. Sans critère de couleur (hex, texte) ni de
// gamme, la réponse est l'aperçu du catalogue : toutes les gammes, aucune couleur.
func (c *Catalog) RunSearch(q Query) (*SearchResult, error) {
	full := q
	full.Limit = 100000
	all, err := c.Search(full)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	res := &SearchResult{Total: len(all), Catalog: c.Stats(), Sources: c.Sources}
	overview := q.Hex == "" && q.Text == "" && q.Family == "" && q.Finish == ""
	if !overview {
		res.Colors = all
		if len(res.Colors) > limit {
			res.Colors = res.Colors[:limit]
		}
	} else {
		res.Colors = []Match{}
	}
	seen := map[string]bool{}
	for _, m := range all {
		if !seen[m.Color.FamilyID] {
			seen[m.Color.FamilyID] = true
		}
	}
	for _, f := range c.Families {
		if overview {
			if q.Nozzle != nil && !f.CompatibleWith(*q.Nozzle).OK {
				continue
			}
			res.Families = append(res.Families, f)
		} else if seen[f.ID] {
			res.Families = append(res.Families, f)
		}
	}
	if res.Families == nil {
		res.Families = []Family{}
	}
	if q.Nozzle != nil {
		res.Compat = map[string]Compat{}
		for _, f := range res.Families {
			res.Compat[f.ID] = f.CompatibleWith(*q.Nozzle)
		}
	}
	switch {
	case overview:
		res.Note = fmt.Sprintf("Aperçu : %d gammes, %d couleurs officielles. Préciser family, finish, text ou hex pour obtenir des couleurs.", len(res.Families), res.Catalog.Colors)
	case len(all) == 0:
		res.Note = "Aucune couleur officielle ne répond : valeur non disponible, rien n'est inventé. Élargir la recherche (autre gamme, max_delta_e plus grand)."
	case q.Hex != "":
		res.Note = fmt.Sprintf("%d couleur(s) officielle(s) classée(s) par écart ΔE (CIE76) avec %s : exact <= 12, proche <= 30, approximatif <= 55.", len(all), NormHex(q.Hex))
	default:
		res.Note = fmt.Sprintf("%d couleur(s) officielle(s).", len(all))
	}
	return res, nil
}

// EntryResult est la réponse de get_filament_catalog_entry.
type EntryResult struct {
	Family  Family   `json:"family"`
	Color   *Color   `json:"color,omitempty" jsonschema:"présent quand id désigne une couleur (GFA00-10300)"`
	Colors  []Color  `json:"colors,omitempty" jsonschema:"toutes les couleurs de la gamme quand id désigne une gamme"`
	Compat  *Compat  `json:"nozzle_compat,omitempty"`
	Sources []Source `json:"sources"`
	Missing []string `json:"not_available,omitempty" jsonschema:"champs que le catalogue ne connaît pas (jamais inventés)"`
	Note    string   `json:"note"`
}

// Entry renvoie une couleur (id GFA00-10300) ou une gamme (GFA00, « PLA Basic ») avec sa fiche complète.
func (c *Catalog) Entry(id string, n *Nozzle) (*EntryResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("id requis : identifiant de couleur (GFA00-10300), de gamme (GFA00) ou nom de gamme (PLA Basic)")
	}
	r := &EntryResult{Sources: c.Sources}
	if col, ok := c.Color(id); ok {
		f := c.famByID[col.FamilyID]
		r.Color, r.Family = col, *f
		r.Note = fmt.Sprintf("%s (%s) : %s, finition %s.", col.NameFR(), col.PrimaryHex(), f.Name, f.FinishFR)
	} else if f, ok := c.Family(id); ok {
		r.Family = *f
		for _, col := range c.Colors {
			if col.FamilyID == f.ID {
				r.Colors = append(r.Colors, col)
			}
		}
		r.Note = fmt.Sprintf("%s : %d couleur(s) officielle(s), finition %s.", f.Name, len(r.Colors), f.FinishFR)
	} else {
		return nil, fmt.Errorf("%q : entrée inconnue du catalogue (voir search_filament_catalog)", id)
	}
	if n != nil {
		cp := r.Family.CompatibleWith(*n)
		r.Compat = &cp
	}
	f := r.Family
	if f.NozzleTempC == nil {
		r.Missing = append(r.Missing, "nozzle_temp_c")
	}
	if f.DryingNote == "" && f.DryingBedTempC == nil {
		r.Missing = append(r.Missing, "drying")
	}
	r.Missing = append(r.Missing, "net_weight_g")
	return r, nil
}
