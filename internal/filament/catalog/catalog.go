// Package filcatalog est le catalogue OFFICIEL des filaments Bambu Lab (B3) : familles (PLA Basic, PETG HF...),
// couleurs exactes (hex, noms FR/EN), finitions, températures recommandées, densité, diamètre, dureté de buse requise.
//
// Les données sont EMBARQUÉES (data/catalog.json, produit par scripts/gen_filament_catalog.py depuis les profils BBL
// de Bambu Studio) : aucun appel réseau, aucune IA. Chaque famille et chaque couleur garde sa source et un niveau de
// confiance ; une valeur absente est « non disponible », jamais inventée. Toutes les fonctions sont pures.
package filcatalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

//go:embed data/catalog.json
var rawCatalog []byte

// Niveaux de confiance.
const (
	ConfOfficialProfile = "official_profile" // lu dans les profils officiels de Bambu Studio
	ConfOfficialSite    = "official_site"    // relevé sur le site / wiki officiel de Bambu Lab
	ConfEstimated       = "estimated"        // déduit, à vérifier
)

// Source dit d'où vient une donnée et quand elle a été relevée.
type Source struct {
	Kind           string `json:"kind" jsonschema:"official_profile | official_site | estimated"`
	Ref            string `json:"ref" jsonschema:"fichier de profil ou URL officielle"`
	ProfileVersion string `json:"profile_version,omitempty"`
	Retrieved      string `json:"retrieved" jsonschema:"date de relevé AAAA-MM-JJ"`
	Note           string `json:"note,omitempty"`
}

// Family est une gamme de filament Bambu (un preset de base de Bambu Studio).
type Family struct {
	ID                     string              `json:"id" jsonschema:"filament_id Bambu (GFA00 = PLA Basic)"`
	Name                   string              `json:"name" jsonschema:"nom de la gamme (PLA Basic, PETG HF...)"`
	PresetLine             string              `json:"preset_line,omitempty" jsonschema:"nom de ligne des presets Bambu Studio (Bambu PLA Basic)"`
	Type                   string              `json:"type,omitempty" jsonschema:"filament_type du profil (PLA, PETG, PA6-CF...)"`
	Class                  string              `json:"class" jsonschema:"PLA | PETG | PET | ABS | ASA | TPU | PA | PC | PPS | PVA | SUPPORT | AUTRE"`
	Vendor                 string              `json:"vendor,omitempty"`
	Finish                 string              `json:"finish" jsonschema:"standard | matte | silk | translucent | metal | sparkle | marble | glow | galaxy | wood | aero | carbon | glass | dynamic"`
	FinishFR               string              `json:"finish_fr" jsonschema:"finition en français"`
	Translucent            bool                `json:"translucent,omitempty"`
	SupportMaterial        bool                `json:"support_material,omitempty"`
	Soluble                bool                `json:"soluble,omitempty"`
	NozzleTempC            *int                `json:"nozzle_temp_c,omitempty" jsonschema:"température de buse recommandée (°C)"`
	NozzleTempFirstLayerC  *int                `json:"nozzle_temp_first_layer_c,omitempty"`
	BedTempC               map[string]int      `json:"bed_temp_c,omitempty" jsonschema:"plateau recommandé par plaque (cool_plate, engineering_plate, high_temp_plate, textured_pei) ; plaque absente = non recommandée"`
	FanMinPct              *int                `json:"fan_min_pct,omitempty"`
	FanMaxPct              *int                `json:"fan_max_pct,omitempty"`
	DensityGCm3            *float64            `json:"density_g_cm3,omitempty"`
	DiameterMM             *float64            `json:"diameter_mm,omitempty"`
	MaxVolumetricSpeed     *float64            `json:"max_volumetric_speed_mm3_s,omitempty"`
	DryingBedTempC         *int                `json:"drying_bed_temp_c,omitempty" jsonschema:"température de plateau pour sécher (profil) ; voir drying_note pour la plage du wiki"`
	DryingNote             string              `json:"drying_note,omitempty" jsonschema:"plage de séchage sur plateau chauffant (wiki Bambu, official_site)"`
	RequiredNozzleHRC      *int                `json:"required_nozzle_hrc,omitempty" jsonschema:"dureté de buse requise (HRC) selon le profil"`
	HardenedNozzleRequired bool                `json:"hardened_nozzle_required" jsonschema:"buse en acier trempé requise (HRC >= 40 : fibres de carbone ou de verre)"`
	IncompatibleNozzles    map[string][]string `json:"incompatible_nozzles,omitempty" jsonschema:"type de buse (Standard, High Flow, E3D High Flow) -> diamètres déconseillés par Bambu Studio"`
	Confidence             string              `json:"confidence"`
	Source                 Source              `json:"source"`
	ColorCount             int                 `json:"color_count" jsonschema:"nombre de couleurs officielles de la gamme"`
}

// Color est une couleur officielle d'une gamme.
type Color struct {
	ID            string            `json:"id" jsonschema:"identifiant stable : <filament_id>-<code couleur> (ex. GFA00-10300)"`
	FamilyID      string            `json:"family_id"`
	FamilyName    string            `json:"family_name,omitempty"`
	ColorCode     string            `json:"color_code" jsonschema:"code couleur Bambu (fila_color_code)"`
	TrayColorCode string            `json:"tray_color_code,omitempty" jsonschema:"code court de la puce de bobine (ex. A0)"`
	Name          map[string]string `json:"name" jsonschema:"nom par langue (fr, en, de, es, it)"`
	Hex           []string          `json:"hex" jsonschema:"#RRGGBB ; 2 couleurs ou plus pour un dégradé ou une bobine multicolore"`
	Kind          string            `json:"kind" jsonschema:"single | gradient | multicolor"`
	Finish        string            `json:"finish,omitempty" jsonschema:"finition de la gamme"`
	Confidence    string            `json:"confidence"`
}

// NameFR renvoie le nom français (à défaut l'anglais).
func (c Color) NameFR() string {
	if n := c.Name["fr"]; n != "" {
		return n
	}
	return c.Name["en"]
}

// PrimaryHex est la couleur de référence (la première) ; "" si la couleur n'est pas disponible.
func (c Color) PrimaryHex() string {
	if len(c.Hex) == 0 {
		return ""
	}
	return c.Hex[0]
}

// Catalog est le catalogue chargé.
type Catalog struct {
	Schema    int      `json:"schema"`
	Generated string   `json:"generated"`
	Sources   []Source `json:"sources"`
	Drying    drying   `json:"drying_heatbed"`
	Families  []Family `json:"families"`
	Colors    []Color  `json:"colors"`
	famByID   map[string]*Family
	colByID   map[string]*Color
}

type drying struct {
	SourceKind string           `json:"source_kind"`
	Source     string           `json:"source"`
	Retrieved  string           `json:"retrieved"`
	Hours      int              `json:"hours"`
	Ranges     map[string][]int `json:"ranges_c"`
}

var (
	once    sync.Once
	cat     *Catalog
	loadErr error
)

// Default renvoie le catalogue embarqué (chargé une fois).
func Default() (*Catalog, error) {
	once.Do(func() { cat, loadErr = Parse(rawCatalog) })
	return cat, loadErr
}

// Parse lit un catalogue JSON (tests, régénération).
func Parse(b []byte) (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("catalogue de filaments illisible : %w", err)
	}
	c.famByID = map[string]*Family{}
	c.colByID = map[string]*Color{}
	for i := range c.Families {
		f := &c.Families[i]
		if r, ok := c.Drying.Ranges[f.Class]; ok && len(r) == 2 {
			f.DryingNote = fmt.Sprintf("%d à %d °C sur le plateau chauffant, %d h (wiki Bambu Lab, %s)", r[0], r[1], c.Drying.Hours, c.Drying.Retrieved)
		}
		c.famByID[f.ID] = f
	}
	for i := range c.Colors {
		col := &c.Colors[i]
		if f := c.famByID[col.FamilyID]; f != nil {
			col.FamilyName = f.Name
			col.Finish = f.Finish
			f.ColorCount++
		}
		c.colByID[col.ID] = col
	}
	return &c, nil
}

// Family renvoie une gamme par filament_id ou par nom (insensible à la casse, aux accents et à la ponctuation).
func (c *Catalog) Family(idOrName string) (*Family, bool) {
	if f, ok := c.famByID[strings.ToUpper(strings.TrimSpace(idOrName))]; ok {
		return f, true
	}
	k := fold(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(idOrName)), "bambu "))
	for i := range c.Families {
		if fold(c.Families[i].Name) == k {
			return &c.Families[i], true
		}
	}
	return nil, false
}

// Color renvoie une couleur par identifiant (GFA00-10300).
func (c *Catalog) Color(id string) (*Color, bool) {
	col, ok := c.colByID[strings.ToUpper(strings.TrimSpace(id))]
	return col, ok
}

// fold met en minuscules, retire accents et ponctuation : « Bleu Électrique » -> « bleuelectrique ».
func fold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if unicode.IsLetter(r) && !unicode.Is(unicode.Mn, r) || unicode.IsDigit(r) || r == '+' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---- couleurs -----------------------------------------------------------------------------------------------

var hexRe = regexp.MustCompile(`^#?([0-9a-fA-F]{6})([0-9a-fA-F]{2})?$`)

// NormHex renvoie « #RRGGBB » en majuscules, "" si la valeur n'est pas une couleur hexadécimale.
func NormHex(s string) string {
	m := hexRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return ""
	}
	return "#" + strings.ToUpper(m[1])
}

func lab(hex string) (L, a, b float64, ok bool) {
	h := NormHex(hex)
	if h == "" {
		return 0, 0, 0, false
	}
	v, _ := strconv.ParseUint(h[1:], 16, 32)
	lin := func(c float64) float64 {
		c /= 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	R, G, B := lin(float64(v>>16&0xff)), lin(float64(v>>8&0xff)), lin(float64(v&0xff))
	x := (0.4124*R + 0.3576*G + 0.1805*B) / 0.95047
	y := 0.2126*R + 0.7152*G + 0.0722*B
	z := (0.0193*R + 0.1192*G + 0.9505*B) / 1.08883
	f := func(t float64) float64 {
		if t > 0.008856 {
			return math.Cbrt(t)
		}
		return 7.787*t + 16.0/116
	}
	fx, fy, fz := f(x), f(y), f(z)
	return 116*fy - 16, 500 * (fx - fy), 200 * (fy - fz), true
}

// Distance est l'écart ΔE (CIE76) entre deux couleurs hexadécimales, 999 si l'une est illisible.
// Mêmes repères que plan_colors : <= 12 exacte, <= 30 proche, <= 55 approximative.
func Distance(a, b string) float64 {
	l1, a1, b1, ok1 := lab(a)
	l2, a2, b2, ok2 := lab(b)
	if !ok1 || !ok2 {
		return 999
	}
	return math.Sqrt((l1-l2)*(l1-l2) + (a1-a2)*(a1-a2) + (b1-b2)*(b1-b2))
}

// Quality traduit un écart en verdict : exact | proche | approximatif | aucune.
func Quality(d float64) string {
	switch {
	case d <= 12:
		return "exact"
	case d <= 30:
		return "proche"
	case d <= 55:
		return "approximatif"
	}
	return "aucune"
}

// ---- compatibilité buse -------------------------------------------------------------------------------------

// Nozzle décrit la buse de l'imprimante.
type Nozzle struct {
	DiameterMM string `json:"diameter_mm,omitempty" jsonschema:"0.2 | 0.4 | 0.6 | 0.8"`
	Hardened   bool   `json:"hardened,omitempty" jsonschema:"buse en acier trempé (ou carbure) installée"`
	Kind       string `json:"kind,omitempty" jsonschema:"Standard | High Flow | E3D High Flow ; vide = Standard"`
}

// Compat est le verdict de compatibilité d'une gamme avec une buse.
type Compat struct {
	OK      bool     `json:"ok"`
	Reasons []string `json:"reasons,omitempty"`
}

// CompatibleWith vérifie une gamme contre une buse : acier trempé requis, diamètre ou type déconseillé par Bambu Studio.
func (f Family) CompatibleWith(n Nozzle) Compat {
	out := Compat{OK: true}
	if f.HardenedNozzleRequired && !n.Hardened {
		out.OK = false
		out.Reasons = append(out.Reasons, f.Name+" est chargé de fibres abrasives : buse en acier trempé requise (HRC >= 40)")
	}
	kind := n.Kind
	if kind == "" {
		kind = "Standard"
	}
	if n.DiameterMM != "" {
		for _, d := range f.IncompatibleNozzles[kind] {
			if d == n.DiameterMM {
				out.OK = false
				out.Reasons = append(out.Reasons, fmt.Sprintf("Bambu Studio déconseille %s sur une buse %s de %s mm", f.Name, kind, d))
			}
		}
	}
	return out
}

// ---- recherche ----------------------------------------------------------------------------------------------

// Query est une recherche dans le catalogue ; tous les critères renseignés se cumulent.
type Query struct {
	Family   string  `json:"family,omitempty" jsonschema:"gamme (nom ou filament_id) ou classe de matière (PLA, PETG, ABS, TPU, PA...)"`
	Finish   string  `json:"finish,omitempty" jsonschema:"finition : matte|mat, silk|soie, translucent|translucide, metal|métal, sparkle, marble|marbre, glow, galaxy, wood|bois, aero, carbon, glass, standard"`
	Text     string  `json:"text,omitempty" jsonschema:"texte dans le nom de la couleur (FR, EN, DE, ES ou IT)"`
	Hex      string  `json:"hex,omitempty" jsonschema:"#RRGGBB : renvoie les couleurs officielles les plus proches (distance croissante)"`
	MaxDelta float64 `json:"max_delta_e,omitempty" jsonschema:"avec hex : écart ΔE maximal (défaut : pas de limite)"`
	Nozzle   *Nozzle `json:"nozzle,omitempty" jsonschema:"ne garder que les gammes utilisables avec cette buse"`
	Limit    int     `json:"limit,omitempty" jsonschema:"nombre maximal de résultats (défaut 20, maximum 100)"`
}

// Match est une couleur trouvée.
type Match struct {
	Color   Color    `json:"color"`
	DeltaE  *float64 `json:"delta_e,omitempty" jsonschema:"écart de couleur ΔE (CIE76) avec le hex demandé"`
	Quality string   `json:"quality,omitempty" jsonschema:"exact | proche | approximatif | aucune"`
}

var finishAlias = map[string]string{
	"mat": "matte", "matte": "matte", "soie": "silk", "silk": "silk", "satin": "silk", "translucide": "translucent", "transparent": "translucent",
	"translucent": "translucent", "metal": "metal", "métal": "metal", "metallique": "metal", "paillettes": "sparkle", "sparkle": "sparkle",
	"marbre": "marble", "marble": "marble", "glow": "glow", "phosphorescent": "glow", "galaxy": "galaxy", "galaxie": "galaxy",
	"bois": "wood", "wood": "wood", "aero": "aero", "carbone": "carbon", "carbon": "carbon", "cf": "carbon", "verre": "glass", "glass": "glass", "gf": "glass",
	"standard": "standard", "basic": "standard", "dynamic": "dynamic",
}

// NormFinish ramène un mot de finition (FR ou EN) au code du catalogue ; "" si inconnu.
func NormFinish(s string) string {
	return finishAlias[strings.ToLower(strings.TrimSpace(s))]
}

// familyMatches : un critère « family » vaut pour un nom de gamme, un filament_id ou une classe de matière.
func familyMatches(f Family, crit string) bool {
	if crit == "" {
		return true
	}
	if strings.EqualFold(f.ID, crit) || strings.EqualFold(f.Class, crit) || strings.EqualFold(f.Type, crit) {
		return true
	}
	k := fold(strings.TrimPrefix(strings.ToLower(crit), "bambu "))
	return k != "" && fold(f.Name) == k
}

// Search applique la requête. Sans hex : ordre du catalogue (gamme puis code) ; avec hex : par distance croissante.
func (c *Catalog) Search(q Query) ([]Match, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	wantHex := ""
	if q.Hex != "" {
		if wantHex = NormHex(q.Hex); wantHex == "" {
			return nil, fmt.Errorf("couleur %q invalide : attendu #RRGGBB", q.Hex)
		}
	}
	finish := ""
	if q.Finish != "" {
		if finish = NormFinish(q.Finish); finish == "" {
			return nil, fmt.Errorf("finition %q inconnue", q.Finish)
		}
	}
	text := fold(q.Text)
	var out []Match
	for _, col := range c.Colors {
		f := c.famByID[col.FamilyID]
		if f == nil || !familyMatches(*f, q.Family) {
			continue
		}
		if finish != "" && f.Finish != finish {
			continue
		}
		if q.Nozzle != nil && !f.CompatibleWith(*q.Nozzle).OK {
			continue
		}
		if text != "" {
			found := false
			for _, n := range col.Name {
				if strings.Contains(fold(n), text) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		m := Match{Color: col}
		if wantHex != "" {
			d := 999.0
			for _, h := range col.Hex {
				if x := Distance(wantHex, h); x < d {
					d = x
				}
			}
			d = math.Round(d*10) / 10
			if q.MaxDelta > 0 && d > q.MaxDelta {
				continue
			}
			m.DeltaE, m.Quality = &d, Quality(d)
		}
		out = append(out, m)
	}
	if wantHex != "" {
		sort.SliceStable(out, func(i, j int) bool { return *out[i].DeltaE < *out[j].DeltaE })
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Nearest renvoie la couleur officielle la plus proche de hex (dans une gamme ou une classe si family est renseigné).
func (c *Catalog) Nearest(hex, family string) (*Match, bool) {
	ms, err := c.Search(Query{Hex: hex, Family: family, Limit: 1})
	if err != nil || len(ms) == 0 {
		return nil, false
	}
	return &ms[0], true
}

// FamilySearch liste les gammes (filtre classe/finition/texte de nom).
func (c *Catalog) FamilySearch(family, finishWord string, nozzle *Nozzle) ([]Family, error) {
	finish := ""
	if finishWord != "" {
		if finish = NormFinish(finishWord); finish == "" {
			return nil, fmt.Errorf("finition %q inconnue", finishWord)
		}
	}
	var out []Family
	for _, f := range c.Families {
		if !familyMatches(f, family) || (finish != "" && f.Finish != finish) {
			continue
		}
		if nozzle != nil && !f.CompatibleWith(*nozzle).OK {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// Stats compte les gammes et couleurs par classe et par niveau de confiance.
type Stats struct {
	Families      int            `json:"families"`
	Colors        int            `json:"colors"`
	ColorsByClass map[string]int `json:"colors_by_class"`
	ByConfidence  map[string]int `json:"by_confidence"`
}

func (c *Catalog) Stats() Stats {
	s := Stats{Families: len(c.Families), Colors: len(c.Colors), ColorsByClass: map[string]int{}, ByConfidence: map[string]int{}}
	for _, col := range c.Colors {
		if f := c.famByID[col.FamilyID]; f != nil {
			s.ColorsByClass[f.Class]++
		}
		s.ByConfidence[col.Confidence]++
	}
	for _, f := range c.Families {
		s.ByConfidence["family:"+f.Confidence]++
	}
	return s
}
