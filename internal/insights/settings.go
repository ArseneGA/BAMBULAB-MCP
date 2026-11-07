package insights

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// PowerOverride remplace la puissance moyenne d'un modèle d'imprimante (watts mesurés avec une prise mesurante).
type PowerOverride struct {
	PreheatW float64 `json:"preheat_w,omitempty" jsonschema:"puissance moyenne pendant la chauffe, en watts (0 = valeur indicative)"`
	PrintW   float64 `json:"print_w,omitempty" jsonschema:"puissance moyenne en impression de PLA, en watts (0 = valeur indicative)"`
}

// Settings sont les réglages de prix. Aucun secret : des prix et des watts.
type Settings struct {
	Currency          string                   `json:"currency" jsonschema:"code de devise à 3 lettres, ex. EUR"`
	ElectricityPerKWh float64                  `json:"electricity_per_kwh" jsonschema:"prix du kWh, dans la devise"`
	FilamentPerKg     map[string]float64       `json:"filament_per_kg" jsonschema:"prix du kg par famille de matière (PLA, PETG, ABS, ASA, TPU, PC, PA, PA-CF, PLA-CF, PETG-CF, PVA, other)"`
	SpoolPerKg        map[string]float64       `json:"spool_per_kg,omitempty" jsonschema:"prix du kg par bobine, clé = libellé de la bobine en minuscules (ex. « bambu pla basic noir »), prioritaire sur la famille"`
	MachineWearPerHr  float64                  `json:"machine_wear_per_hour" jsonschema:"amortissement de la machine par heure d'impression (défaut 0)"`
	NozzleWearPerHr   float64                  `json:"nozzle_wear_per_hour" jsonschema:"usure de la buse par heure d'impression (défaut 0)"`
	PowerOverrides    map[string]PowerOverride `json:"power_overrides,omitempty" jsonschema:"puissances mesurées par modèle d'imprimante (p1s, x1c, a1, a1mini, h2d...), prioritaires sur les valeurs indicatives"`
	UpdatedAt         time.Time                `json:"updated_at,omitzero"`
	UsingDefaults     bool                     `json:"using_defaults" jsonschema:"vrai tant qu'aucun réglage n'a été enregistré"`
	DefaultedPrices   []string                 `json:"defaulted_prices,omitempty" jsonschema:"prix encore à leur valeur par défaut INDICATIVE (« electricity » et familles de matière)"`
	Note              string                   `json:"note"`
}

const settingsNote = "Les prix par défaut sont INDICATIFS (ordre de grandeur, pas des prix constatés chez un fournisseur) : à remplacer par vos prix réels. " +
	"Aucune usure n'est comptée tant que machine_wear_per_hour et nozzle_wear_per_hour valent 0."

// DefaultSettings renvoie les réglages initiaux.
func DefaultSettings() Settings {
	return Settings{
		Currency:          "EUR",
		ElectricityPerKWh: 0.25,
		FilamentPerKg: map[string]float64{
			"PLA": 20, "PLA-CF": 40, "PETG": 22, "PETG-CF": 40, "ABS": 22, "ASA": 25, "TPU": 32, "PC": 45,
			"PA": 50, "PA-CF": 70, "PVA": 45, "PET": 45, "other": 25,
		},
		UsingDefaults: true,
		Note:          settingsNote,
	}
}

// SettingsPatch modifie une partie des réglages. Dans les dictionnaires, une valeur 0 supprime l'entrée.
type SettingsPatch struct {
	Currency          *string                  `json:"currency,omitempty"`
	ElectricityPerKWh *float64                 `json:"electricity_per_kwh,omitempty"`
	FilamentPerKg     map[string]float64       `json:"filament_per_kg,omitempty" jsonschema:"fusionné avec l'existant ; 0 rétablit la valeur par défaut"`
	SpoolPerKg        map[string]float64       `json:"spool_per_kg,omitempty" jsonschema:"fusionné avec l'existant ; 0 supprime l'entrée"`
	MachineWearPerHr  *float64                 `json:"machine_wear_per_hour,omitempty"`
	NozzleWearPerHr   *float64                 `json:"nozzle_wear_per_hour,omitempty"`
	PowerOverrides    map[string]PowerOverride `json:"power_overrides,omitempty" jsonschema:"fusionné avec l'existant ; {preheat_w:0,print_w:0} supprime l'entrée"`
	ApplyToHistory    bool                     `json:"apply_to_history,omitempty" jsonschema:"recalcule aussi le coût réel de toutes les fiches du journal avec ces prix"`
}

const settingsKey = "settings"

// stored est le document enregistré : seulement ce que l'utilisateur a réglé (les valeurs par défaut ne sont jamais figées).
type stored struct {
	Currency          string                   `json:"currency,omitempty"`
	ElectricityPerKWh *float64                 `json:"electricity_per_kwh,omitempty"`
	FilamentPerKg     map[string]float64       `json:"filament_per_kg,omitempty"`
	SpoolPerKg        map[string]float64       `json:"spool_per_kg,omitempty"`
	MachineWearPerHr  float64                  `json:"machine_wear_per_hour,omitempty"`
	NozzleWearPerHr   float64                  `json:"nozzle_wear_per_hour,omitempty"`
	PowerOverrides    map[string]PowerOverride `json:"power_overrides,omitempty"`
	UpdatedAt         time.Time                `json:"updated_at,omitzero"`
}

func (s *Service) loadStored(ctx context.Context) (stored, bool, error) {
	var raw string
	err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT doc FROM insight_settings WHERE key=?`, settingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return stored{}, false, nil
	}
	if err != nil {
		return stored{}, false, err
	}
	var st stored
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return stored{}, false, err
	}
	return st, true, nil
}

// view complète les réglages enregistrés avec les valeurs par défaut.
func (st stored) view(exists bool) Settings {
	def := DefaultSettings()
	out := def
	out.UsingDefaults = !exists
	out.FilamentPerKg = map[string]float64{}
	var defaulted []string
	for k, v := range def.FilamentPerKg {
		if u, ok := st.FilamentPerKg[k]; ok {
			out.FilamentPerKg[k] = u
		} else {
			out.FilamentPerKg[k] = v
			defaulted = append(defaulted, k)
		}
	}
	for k, v := range st.FilamentPerKg {
		out.FilamentPerKg[k] = v
	}
	if st.ElectricityPerKWh != nil {
		out.ElectricityPerKWh = *st.ElectricityPerKWh
	} else {
		defaulted = append(defaulted, "electricity")
	}
	sort.Strings(defaulted)
	out.DefaultedPrices = defaulted
	if st.Currency != "" {
		out.Currency = st.Currency
	}
	out.SpoolPerKg = st.SpoolPerKg
	out.MachineWearPerHr, out.NozzleWearPerHr = st.MachineWearPerHr, st.NozzleWearPerHr
	out.PowerOverrides = st.PowerOverrides
	out.UpdatedAt = st.UpdatedAt
	return out
}

// GetSettings renvoie les réglages courants (valeurs par défaut indicatives pour ce qui n'a pas été réglé).
func (s *Service) GetSettings(ctx context.Context) (Settings, error) {
	st, exists, err := s.loadStored(ctx)
	if err != nil {
		return DefaultSettings(), err
	}
	return st.view(exists), nil
}

func okMoney(v, max float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= max }

func mergePrices(dst, src map[string]float64, what string, norm func(string) string) (map[string]float64, error) {
	if dst == nil {
		dst = map[string]float64{}
	}
	for k, v := range src {
		k = norm(k)
		if k == "" || len(k) > 120 {
			return nil, invalid("%s : clé vide ou trop longue", what)
		}
		if !okMoney(v, 10000) {
			return nil, invalid("%s[%s] : entre 0 et 10000 par kg", what, k)
		}
		if v == 0 {
			delete(dst, k)
		} else {
			dst[k] = v
		}
	}
	return dst, nil
}

// SetSettings applique un patch, l'enregistre et renvoie le résultat.
func (s *Service) SetSettings(ctx context.Context, p SettingsPatch) (Settings, error) {
	st, _, err := s.loadStored(ctx)
	if err != nil {
		return Settings{}, err
	}
	if p.Currency != nil {
		c := strings.ToUpper(strings.TrimSpace(*p.Currency))
		if len(c) != 3 || strings.Trim(c, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
			return Settings{}, invalid("currency : code à 3 lettres attendu (ex. EUR)")
		}
		st.Currency = c
	}
	if p.ElectricityPerKWh != nil {
		if !okMoney(*p.ElectricityPerKWh, 10) {
			return Settings{}, invalid("electricity_per_kwh : entre 0 et 10 par kWh")
		}
		v := *p.ElectricityPerKWh
		st.ElectricityPerKWh = &v
	}
	if p.MachineWearPerHr != nil {
		if !okMoney(*p.MachineWearPerHr, 1000) {
			return Settings{}, invalid("machine_wear_per_hour : entre 0 et 1000")
		}
		st.MachineWearPerHr = *p.MachineWearPerHr
	}
	if p.NozzleWearPerHr != nil {
		if !okMoney(*p.NozzleWearPerHr, 1000) {
			return Settings{}, invalid("nozzle_wear_per_hour : entre 0 et 1000")
		}
		st.NozzleWearPerHr = *p.NozzleWearPerHr
	}
	if st.FilamentPerKg, err = mergePrices(st.FilamentPerKg, p.FilamentPerKg, "filament_per_kg", func(k string) string {
		k = strings.TrimSpace(k)
		if strings.EqualFold(k, "other") {
			return "other"
		}
		if f := MaterialFamily(k); f != "" {
			return f
		}
		return strings.ToUpper(k)
	}); err != nil {
		return Settings{}, err
	}
	if st.SpoolPerKg, err = mergePrices(st.SpoolPerKg, p.SpoolPerKg, "spool_per_kg", func(k string) string { return strings.ToLower(strings.TrimSpace(k)) }); err != nil {
		return Settings{}, err
	}
	if len(p.PowerOverrides) > 0 {
		if st.PowerOverrides == nil {
			st.PowerOverrides = map[string]PowerOverride{}
		}
		for k, v := range p.PowerOverrides {
			key := modelKey(k)
			if key == "" {
				return Settings{}, invalid("power_overrides : modèle %q inconnu (%s)", k, strings.Join(knownModelKeys(), ", "))
			}
			if !okMoney(v.PreheatW, 5000) || !okMoney(v.PrintW, 5000) {
				return Settings{}, invalid("power_overrides[%s] : entre 0 et 5000 W", key)
			}
			if v.PreheatW == 0 && v.PrintW == 0 {
				delete(st.PowerOverrides, key)
			} else {
				st.PowerOverrides[key] = v
			}
		}
	}
	st.UpdatedAt = s.now()
	doc, _ := json.Marshal(st)
	err = s.cfg.Store.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO insight_settings(key,doc,updated_at) VALUES(?,?,?)
			ON CONFLICT(key) DO UPDATE SET doc=excluded.doc, updated_at=excluded.updated_at`, settingsKey, string(doc), store.FormatTime(st.UpdatedAt)); err != nil {
			return err
		}
		return s.cfg.Store.AuditTx(ctx, tx, actor, "insights.settings", "insight_settings", settingsKey, nil)
	})
	if err != nil {
		return Settings{}, err
	}
	v := st.view(true)
	if p.ApplyToHistory {
		if err := s.recomputeHistory(ctx, v); err != nil {
			return v, err
		}
	}
	return v, nil
}

func knownModelKeys() []string {
	pw := loadPower()
	ks := make([]string, 0, len(pw.Models))
	for k := range pw.Models {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
