package sim

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// AMS simulé : par défaut un AMS de 4 emplacements, configurable (nombre d'unités, contenu de chaque bac).
// Il publie `ams` (forme décrite par la communauté pour la P1S, voir internal/printer/ams.go) et `tray_now`,
// fait avancer tray_now pendant une impression d'après `ams_mapping`, et retire un pourcentage fixe aux bacs
// utilisés à la fin de l'impression (ConsumePercent). Tout est protégé par Sim.mu comme le reste du modèle.

// AMSTrayConfig décrit le contenu d'un bac. Empty = bac vide (seul son id est publié).
type AMSTrayConfig struct {
	Empty         bool
	Type          string // PLA, PETG...
	Color         string // RRGGBB ou #RRGGBB
	SubBrand      string
	UUID          string // RFID (Bambu) ; vide = bobine sans étiquette
	RemainPercent int    // 0..100 ; -1 = non fourni
	WeightG       float64
}

// AMSConfig configure l'AMS simulé. Units = 0 supprime l'AMS du rapport (bobine externe seule).
type AMSConfig struct {
	Units          int               // 0..4
	Trays          [][]AMSTrayConfig // [unité][bac] ; complétés par des bacs vides
	External       *AMSTrayConfig    // bobine externe (vt_tray) ; nil = absente
	ConsumePercent int               // retiré à chaque bac utilisé en fin d'impression (défaut 3)
	Humidity       int               // 1..5 (défaut 3)
}

// DefaultAMSConfig : un AMS, quatre emplacements remplis.
func DefaultAMSConfig() AMSConfig {
	return AMSConfig{Units: 1, ConsumePercent: 3, Humidity: 3, Trays: [][]AMSTrayConfig{{
		{Type: "PLA", Color: "FFFFFF", SubBrand: "Bambu PLA Basic", UUID: "A1B2C3D4E5F60718293A4B5C6D7E8F01", RemainPercent: 100, WeightG: 1000},
		{Type: "PLA", Color: "000000", SubBrand: "Bambu PLA Basic", UUID: "A1B2C3D4E5F60718293A4B5C6D7E8F02", RemainPercent: 85, WeightG: 1000},
		{Type: "PETG", Color: "D01010", SubBrand: "Generic PETG", RemainPercent: -1},
		{Type: "PLA", Color: "1060D0", SubBrand: "Bambu PLA Basic", UUID: "A1B2C3D4E5F60718293A4B5C6D7E8F04", RemainPercent: 40, WeightG: 1000},
	}}}
}

type amsModel struct {
	cfg     AMSConfig
	trays   [][]AMSTrayConfig
	tray    int   // tray_now global ; 255 = aucun
	mapping []int // ams_mapping de l'impression en cours
	used    map[int]bool
	counted bool // consommation de l'impression courante déjà retirée
}

func newAMSModel(c AMSConfig) *amsModel {
	if c.Units < 0 {
		c.Units = 0
	}
	if c.Units > 4 {
		c.Units = 4
	}
	if c.ConsumePercent == 0 {
		c.ConsumePercent = 3
	}
	if c.Humidity == 0 {
		c.Humidity = 3
	}
	m := &amsModel{cfg: c, tray: 255, used: map[int]bool{}}
	m.trays = make([][]AMSTrayConfig, c.Units)
	for u := range m.trays {
		m.trays[u] = make([]AMSTrayConfig, 4)
		for i := range m.trays[u] {
			m.trays[u][i] = AMSTrayConfig{Empty: true, RemainPercent: -1}
			if u < len(c.Trays) && i < len(c.Trays[u]) {
				m.trays[u][i] = c.Trays[u][i]
			}
		}
	}
	return m
}

// ConfigureAMS remplace la configuration de l'AMS simulé (avant ou pendant l'exécution).
func (s *Sim) ConfigureAMS(c AMSConfig) {
	s.mu.Lock()
	s.p.ams = newAMSModel(c)
	s.mu.Unlock()
	s.publishDelta(false)
}

// SetAMSTray change le contenu d'un bac (unité 1..4, bac 1..4), comme si l'utilisateur rechargeait une bobine.
func (s *Sim) SetAMSTray(unit, tray int, c AMSTrayConfig) error {
	s.mu.Lock()
	if s.p.ams == nil || unit < 1 || unit > len(s.p.ams.trays) || tray < 1 || tray > 4 {
		s.mu.Unlock()
		return fmt.Errorf("sim: bac ams%d:%d inexistant", unit, tray)
	}
	s.p.ams.trays[unit-1][tray-1] = c
	s.mu.Unlock()
	s.publishDelta(false)
	return nil
}

// AMSTrayRemain renvoie le pourcentage restant simulé d'un bac.
func (s *Sim) AMSTrayRemain(unit, tray int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p.ams == nil || unit < 1 || unit > len(s.p.ams.trays) || tray < 1 || tray > 4 {
		return -1
	}
	return s.p.ams.trays[unit-1][tray-1].RemainPercent
}

func normColor(c string) string {
	c = strings.TrimPrefix(strings.TrimSpace(c), "#")
	if len(c) == 6 {
		c += "FF"
	}
	return strings.ToUpper(c)
}

func trayReport(id int, t AMSTrayConfig) map[string]any {
	r := map[string]any{"id": strconv.Itoa(id)}
	if t.Empty {
		return r
	}
	r["tray_type"] = t.Type
	r["tray_color"] = normColor(t.Color)
	r["tray_sub_brands"] = t.SubBrand
	r["tray_info_idx"] = "GFA00"
	r["tray_diameter"] = "1.75"
	r["nozzle_temp_min"] = "190"
	r["nozzle_temp_max"] = "240"
	if t.UUID != "" {
		r["tray_uuid"] = t.UUID
	} else {
		r["tray_uuid"] = "00000000000000000000000000000000"
	}
	if t.WeightG > 0 {
		r["tray_weight"] = strconv.FormatFloat(t.WeightG, 'f', -1, 64)
	}
	if t.RemainPercent >= 0 {
		r["remain"] = t.RemainPercent
	} else {
		r["remain"] = -1
	}
	return r
}

// addAMS ajoute l'AMS au rapport complet (appelé par model.report).
func (m *model) addAMS(r map[string]any) {
	a := m.ams
	if a == nil {
		return
	}
	if len(a.trays) == 0 {
		// Aucun AMS : la clé reste présente (liste vide), sinon une fusion de rapports partiels garderait l'ancien AMS.
		r["ams"] = map[string]any{"ams": []any{}, "ams_exist_bits": "0", "tray_exist_bits": "0", "tray_now": strconv.Itoa(a.tray), "version": 1}
	}
	if len(a.trays) > 0 {
		units := make([]any, 0, len(a.trays))
		bits := 0
		for u, ts := range a.trays {
			trs := make([]any, 0, 4)
			for i, t := range ts {
				trs = append(trs, trayReport(i, t))
				if !t.Empty {
					bits |= 1 << (u*4 + i)
				}
			}
			units = append(units, map[string]any{"id": strconv.Itoa(u), "humidity": strconv.Itoa(a.cfg.Humidity), "temp": "24.5", "tray": trs})
		}
		r["ams"] = map[string]any{"ams": units, "ams_exist_bits": strconv.FormatInt(int64((1<<len(a.trays))-1), 16),
			"tray_exist_bits": strconv.FormatInt(int64(bits), 16), "tray_now": strconv.Itoa(a.tray), "version": 1}
	}
	if a.cfg.External != nil {
		v := trayReport(254, *a.cfg.External)
		r["vt_tray"] = v
	}
	r["tray_now"] = strconv.Itoa(a.tray)
}

// amsBegin retient l'affectation de l'impression qui démarre.
func (m *model) amsBegin(mapping []int) {
	if m.ams == nil {
		return
	}
	m.ams.mapping, m.ams.used, m.ams.counted, m.ams.tray = mapping, map[int]bool{}, false, 255
}

// amsStep fait avancer tray_now et retire la consommation à la fin de l'impression.
func (m *model) amsStep() {
	a := m.ams
	if a == nil {
		return
	}
	switch m.gcodeState {
	case "RUNNING":
		first := 255
		for _, id := range a.mapping {
			if id >= 0 {
				first = id
				break
			}
		}
		if first != 255 {
			a.tray = first
			a.used[first] = true
		}
	case "FINISH":
		if !a.counted {
			a.counted = true
			for id := range a.used {
				u, i := id/4, id%4
				if id < 254 && u < len(a.trays) && !a.trays[u][i].Empty && a.trays[u][i].RemainPercent >= 0 {
					a.trays[u][i].RemainPercent = max(0, a.trays[u][i].RemainPercent-a.cfg.ConsumePercent)
				}
			}
		}
		a.tray = 255
	case "IDLE", "FAILED":
		a.tray = 255
	}
}

// amsOnStart lit use_ams / ams_mapping de la commande project_file.
func (s *Sim) amsOnStart(p map[string]any) {
	var mapping []int
	if arr, ok := p["ams_mapping"].([]any); ok {
		for _, v := range arr {
			if f, ok := v.(float64); ok {
				mapping = append(mapping, int(f))
			}
		}
	}
	s.mu.Lock()
	s.p.amsBegin(mapping)
	s.mu.Unlock()
}

// registerAMSControl : GET /ams (état) et POST /ams/{unit}/{tray} (corps JSON {type,color,remain,empty}).
func (s *Sim) registerAMSControl(mux *http.ServeMux) {
	mux.HandleFunc("GET /ams", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		out := map[string]any{}
		s.p.addAMS(out)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("POST /ams/{unit}/{tray}", func(w http.ResponseWriter, r *http.Request) {
		u, _ := strconv.Atoi(r.PathValue("unit"))
		t, _ := strconv.Atoi(r.PathValue("tray"))
		var b struct {
			Empty  bool    `json:"empty"`
			Type   string  `json:"type"`
			Color  string  `json:"color"`
			Remain *int    `json:"remain"`
			UUID   string  `json:"uuid"`
			Weight float64 `json:"weight_g"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c := AMSTrayConfig{Empty: b.Empty, Type: b.Type, Color: b.Color, UUID: b.UUID, WeightG: b.Weight, RemainPercent: -1}
		if b.Remain != nil {
			c.RemainPercent = *b.Remain
		}
		if err := s.SetAMSTray(u, t, c); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
