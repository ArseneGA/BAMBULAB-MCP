package engineering

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed data/materials.json
var materialsJSON []byte

// Property est une propriété matériau avec sa provenance. Value nil = inconnue.
type Property struct {
	Value  *float64 `json:"value"`
	Unit   string   `json:"unit"`
	Tol    *float64 `json:"tol,omitempty"`
	Source string   `json:"source"`
	Date   string   `json:"date"`
	Level  Level    `json:"level"`
	Domain string   `json:"domain"`
	Note   string   `json:"note,omitempty"`
}

// Material est une fiche matériau.
type Material struct {
	ID         string              `json:"-"`
	Name       string              `json:"name"`
	Family     string              `json:"family"`
	Vendor     string              `json:"vendor"`
	TDSSource  string              `json:"tds_source"`
	Properties map[string]Property `json:"properties"`
}

// MaterialDB est la base embarquée.
type MaterialDB struct {
	Version   string                `json:"version"`
	Note      string                `json:"note"`
	Sources   map[string]DataSource `json:"sources"`
	Materials map[string]*Material  `json:"materials"`
}

// DataSource décrit une source de la base matériaux.
type DataSource struct {
	Title    string `json:"title"`
	Version  string `json:"version"`
	URL      string `json:"url"`
	Accessed string `json:"accessed"`
	// SHA256 : empreinte du PDF vérifié ; Extract : chemin (depuis la racine du dépôt) du
	// fichier d'extraits exacts (pdftotext) dans docs/research/materials-tds/.
	SHA256  string `json:"sha256,omitempty"`
	Extract string `json:"extract,omitempty"`
}

var (
	dbOnce sync.Once
	db     *MaterialDB
	dbErr  error
)

// Materials retourne la base embarquée (chargée une fois).
func Materials() (*MaterialDB, error) {
	dbOnce.Do(func() {
		var d MaterialDB
		if err := json.Unmarshal(materialsJSON, &d); err != nil {
			dbErr = fmt.Errorf("materials.json: %w", err)
			return
		}
		for id, m := range d.Materials {
			m.ID = id
		}
		db = &d
	})
	return db, dbErr
}

// MaterialIDs liste les identifiants triés.
func MaterialIDs() []string {
	d, err := Materials()
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(d.Materials))
	for id := range d.Materials {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// GetMaterial cherche un matériau (insensible à la casse ; espaces/underscores ≈ tirets).
func GetMaterial(id string) (*Material, error) {
	d, err := Materials()
	if err != nil {
		return nil, err
	}
	norm := func(s string) string {
		return strings.NewReplacer(" ", "-", "_", "-").Replace(strings.ToUpper(strings.TrimSpace(s)))
	}
	n := norm(id)
	for k, m := range d.Materials {
		if norm(k) == n {
			return m, nil
		}
	}
	return nil, invalid("matériau inconnu %q (connus : %s)", id, strings.Join(MaterialIDs(), ", "))
}

// Prop retourne une propriété et un booléen "connue" (valeur non nulle).
func (m *Material) Prop(key string) (Property, bool) {
	p, ok := m.Properties[key]
	if !ok || p.Value == nil {
		return p, false
	}
	return p, true
}

// sourceFor construit la Source d'une propriété.
func (m *Material) sourceFor(p Property) Source {
	d, _ := Materials()
	if d != nil {
		if s, ok := d.Sources[p.Source]; ok {
			return Source{ID: p.Source, Title: s.Title + " " + s.Version, URL: s.URL, Date: p.Date, Note: p.Domain}
		}
	}
	return Source{ID: p.Source, Title: p.Source, Date: p.Date}
}

// propUsed accumule les propriétés effectivement utilisées d'un calcul : sources,
// niveau le moins fiable et avertissements.
type propUsed struct {
	level   Level
	sources []Source
	seen    map[string]bool
}

func newUsed() *propUsed { return &propUsed{level: LevelCalculated, seen: map[string]bool{}} }

func (u *propUsed) add(m *Material, p Property) {
	lvl := p.Level
	if lvl == LevelMeasured {
		lvl = LevelCalculated // formule appliquée à une donnée mesurée
	}
	u.level = worst(u.level, lvl)
	s := m.sourceFor(p)
	key := s.ID
	if !u.seen[key] {
		u.seen[key] = true
		u.sources = append(u.sources, s)
	}
}

// ErrUnknownProperty : propriété inconnue (null) pour ce matériau.
func errUnknown(m *Material, key string) error {
	return invalid("propriété %q inconnue (null) pour le matériau %s : calcul impossible sans hypothèse explicite", key, m.ID)
}

// need retourne la valeur d'une propriété connue ou une erreur.
func (m *Material) need(key string, u *propUsed) (float64, error) {
	p, ok := m.Prop(key)
	if !ok {
		return 0, errUnknown(m, key)
	}
	u.add(m, p)
	return *p.Value, nil
}
