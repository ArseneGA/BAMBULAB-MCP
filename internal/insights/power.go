package insights

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

//go:embed data/power.json
var powerJSON []byte

type powerModel struct {
	Name     string  `json:"name"`
	PreheatW float64 `json:"preheat_w"`
	PrintW   float64 `json:"print_w"`
}

type powerData struct {
	Version        string                `json:"version"`
	Level          string                `json:"level"`
	Note           string                `json:"note"`
	Aliases        map[string][]string   `json:"model_aliases"`
	Models         map[string]powerModel `json:"models"`
	Default        powerModel            `json:"default_model"`
	PrintFactor    map[string]float64    `json:"family_print_factor"`
	PreheatMinutes map[string]float64    `json:"family_preheat_minutes"`
	DefaultPreheat float64               `json:"default_preheat_minutes"`
	Assumptions    []string              `json:"assumptions"`
}

var (
	powerOnce sync.Once
	powerVal  *powerData
)

func loadPower() *powerData {
	powerOnce.Do(func() {
		var d powerData
		if err := json.Unmarshal(powerJSON, &d); err != nil {
			panic("insights/data/power.json : " + err.Error()) // donnée embarquée : testée
		}
		powerVal = &d
	})
	return powerVal
}

// modelKey trouve la clé de modèle (p1s, x1c, a1mini...) dans un libellé ("Bambu Lab P1S 0.4 nozzle").
func modelKey(hint string) string {
	norm := strings.NewReplacer("@", " ", "-", " ", "_", " ", ".", " ", ",", " ").Replace(strings.ToLower(hint))
	norm = " " + strings.Join(strings.Fields(norm), " ") + " "
	type cand struct {
		key, alias string
	}
	var cs []cand
	for k, as := range loadPower().Aliases {
		for _, a := range as {
			cs = append(cs, cand{k, a})
		}
	}
	// alias les plus longs d'abord : « a1 mini » avant « a1 »
	sort.Slice(cs, func(i, j int) bool {
		if len(cs[i].alias) != len(cs[j].alias) {
			return len(cs[i].alias) > len(cs[j].alias)
		}
		return cs[i].alias < cs[j].alias
	})
	for _, c := range cs {
		if strings.Contains(norm, " "+c.alias+" ") {
			return c.key
		}
	}
	return ""
}

// PowerProfile est la puissance retenue pour une estimation.
type PowerProfile struct {
	ModelKey       string  `json:"model_key"`
	ModelName      string  `json:"model_name"`
	PreheatW       float64 `json:"preheat_w"`
	PrintW         float64 `json:"print_w_pla"`
	PrintFactor    float64 `json:"print_factor"`
	PreheatMinutes float64 `json:"preheat_minutes"`
	Source         string  `json:"source" jsonschema:"indicatif | réglage utilisateur"`
}

// powerFor choisit la puissance d'un modèle d'imprimante pour une famille de matière.
func powerFor(modelHint, family string, set Settings) PowerProfile {
	pd := loadPower()
	key := modelKey(modelHint)
	m, ok := pd.Models[key]
	if !ok {
		m = pd.Default
		key = ""
	}
	pp := PowerProfile{ModelKey: key, ModelName: m.Name, PreheatW: m.PreheatW, PrintW: m.PrintW, Source: "indicatif", PrintFactor: 1, PreheatMinutes: pd.DefaultPreheat}
	if ov, ok := set.PowerOverrides[key]; ok && key != "" {
		used := false
		if ov.PreheatW > 0 {
			pp.PreheatW, used = ov.PreheatW, true
		}
		if ov.PrintW > 0 {
			pp.PrintW, used = ov.PrintW, true
		}
		if used {
			pp.Source = "réglage utilisateur"
		}
	}
	if f, ok := pd.PrintFactor[family]; ok {
		pp.PrintFactor = f
	}
	if m, ok := pd.PreheatMinutes[family]; ok {
		pp.PreheatMinutes = m
	}
	return pp
}
