package printer

import (
	"fmt"
	"strconv"
	"strings"
)

// État AMS lu dans le rapport MQTT `print` (docs/research/bambu-lan-protocol.md §3).
//
// Champs lus (forme décrite par la communauté, À VÉRIFIER sur la vraie P1S — voir docs/UTILISATION.md) :
//
//	print.ams.ams[]            unités : id "0".."3", humidity "1".."5", temp "24.5", tray[]
//	print.ams.ams[].tray[]     id "0".."3", tray_type "PLA", tray_color "RRGGBBAA", tray_sub_brands,
//	                           tray_info_idx, tray_uuid, remain (0..100, -1 inconnu), tray_weight (g),
//	                           tray_diameter, nozzle_temp_min/max ; un bac vide n'a que son id
//	print.ams.tray_now         "255" aucun, "254" bobine externe, sinon unité*4 + bac (aussi lu à la racine)
//	print.vt_tray              bobine externe (id "254"), mêmes champs qu'un bac

// AMSTray est un emplacement de filament (bac d'un AMS ou bobine externe).
type AMSTray struct {
	Slot          string  `json:"slot" jsonschema:"ams1:1..ams4:4 ou ext (même notation que les affectations d'impression)"`
	Unit          int     `json:"unit" jsonschema:"numéro d'AMS (1..4) ; 0 pour la bobine externe"`
	Index         int     `json:"index" jsonschema:"numéro du bac dans l'AMS (1..4) ; 0 pour la bobine externe"`
	Empty         bool    `json:"empty" jsonschema:"vrai si l'AMS ne lit aucun filament dans ce bac"`
	Type          string  `json:"type,omitempty" jsonschema:"matière lue (PLA, PETG, ...)"`
	SubBrand      string  `json:"sub_brand,omitempty"`
	Color         string  `json:"color,omitempty" jsonschema:"couleur lue, #RRGGBB"`
	InfoIdx       string  `json:"info_idx,omitempty"`
	UUID          string  `json:"uuid,omitempty" jsonschema:"identifiant RFID de la bobine (bobines Bambu uniquement)"`
	RemainPercent int     `json:"remain_percent" jsonschema:"restant en % si fourni par l'AMS, -1 si inconnu"`
	WeightG       float64 `json:"weight_g,omitempty" jsonschema:"poids net de la bobine déclaré par son étiquette RFID"`
	DiameterMM    float64 `json:"diameter_mm,omitempty"`
	NozzleTempMin int     `json:"nozzle_temp_min,omitempty"`
	NozzleTempMax int     `json:"nozzle_temp_max,omitempty"`
	Active        bool    `json:"active" jsonschema:"vrai pour l'emplacement en cours d'utilisation (tray_now)"`
}

// AMSUnit est un AMS physique.
type AMSUnit struct {
	Unit     int       `json:"unit"`
	Humidity int       `json:"humidity_level" jsonschema:"indice d'humidité 1 (sec) à 5 (humide) ; 0 si inconnu"`
	TempC    float64   `json:"temp_c,omitempty"`
	Trays    []AMSTray `json:"trays"`
}

// AMSState est l'état AMS d'une imprimante au dernier rapport fusionné.
type AMSState struct {
	Present    bool      `json:"present" jsonschema:"vrai si le rapport contient un AMS ou une bobine externe"`
	Units      []AMSUnit `json:"units"`
	External   *AMSTray  `json:"external,omitempty"`
	TrayNow    string    `json:"tray_now,omitempty" jsonschema:"valeur brute de tray_now"`
	ActiveSlot string    `json:"active_slot,omitempty" jsonschema:"emplacement actif (ams1:2, ext) ; vide si aucun"`
}

// Trays aplatit les bacs de tous les AMS puis la bobine externe.
func (a *AMSState) Trays() []AMSTray {
	if a == nil {
		return nil
	}
	var out []AMSTray
	for _, u := range a.Units {
		out = append(out, u.Trays...)
	}
	if a.External != nil {
		out = append(out, *a.External)
	}
	return out
}

// TraySlot renvoie la notation d'emplacement d'un bac (unité 1..4, bac 1..4).
func TraySlot(unit, index int) string { return fmt.Sprintf("ams%d:%d", unit, index) }

func parseAMS(d map[string]any) *AMSState {
	amsObj, _ := d["ams"].(map[string]any)
	vt, _ := d["vt_tray"].(map[string]any)
	if amsObj == nil && vt == nil {
		return nil
	}
	st := &AMSState{Units: []AMSUnit{}}
	now := str(d["tray_now"])
	if v, ok := amsObj["tray_now"]; ok && now == "" {
		now = str(v)
	}
	st.TrayNow = now
	activeGlobal := -1
	if n, err := strconv.Atoi(strings.TrimSpace(now)); err == nil {
		activeGlobal = n
	}
	if arr, ok := amsObj["ams"].([]any); ok {
		for _, e := range arr {
			um, ok := e.(map[string]any)
			if !ok {
				continue
			}
			id, err := strconv.Atoi(str(um["id"]))
			if err != nil || id < 0 || id > 3 {
				continue // AMS HT / identifiants hors 0..3 : non gérés (limite documentée)
			}
			u := AMSUnit{Unit: id + 1, Humidity: int(num(um["humidity"])), TempC: num(um["temp"]), Trays: []AMSTray{}}
			if ta, ok := um["tray"].([]any); ok {
				for _, te := range ta {
					tm, ok := te.(map[string]any)
					if !ok {
						continue
					}
					ti, err := strconv.Atoi(str(tm["id"]))
					if err != nil || ti < 0 || ti > 3 {
						continue
					}
					tr := parseTray(tm)
					tr.Unit, tr.Index, tr.Slot = id+1, ti+1, TraySlot(id+1, ti+1)
					tr.Active = activeGlobal == id*4+ti
					if tr.Active {
						st.ActiveSlot = tr.Slot
					}
					u.Trays = append(u.Trays, tr)
				}
			}
			st.Units = append(st.Units, u)
		}
	}
	if vt != nil {
		tr := parseTray(vt)
		tr.Slot = "ext"
		tr.Active = activeGlobal == 254
		if tr.Active {
			st.ActiveSlot = "ext"
		}
		st.External = &tr
	}
	st.Present = len(st.Units) > 0 || st.External != nil
	return st
}

func parseTray(m map[string]any) AMSTray {
	t := AMSTray{Type: strings.TrimSpace(str(m["tray_type"])), SubBrand: strings.TrimSpace(str(m["tray_sub_brands"])),
		InfoIdx: strings.TrimSpace(str(m["tray_info_idx"])), RemainPercent: -1}
	if r, ok := m["remain"]; ok {
		t.RemainPercent = int(num(r))
		if t.RemainPercent < 0 || t.RemainPercent > 100 {
			t.RemainPercent = -1
		}
	}
	if u := strings.TrimSpace(str(m["tray_uuid"])); u != "" && strings.Trim(u, "0") != "" {
		t.UUID = u
	}
	t.WeightG = num(m["tray_weight"])
	t.DiameterMM = num(m["tray_diameter"])
	t.NozzleTempMin = int(num(m["nozzle_temp_min"]))
	t.NozzleTempMax = int(num(m["nozzle_temp_max"]))
	if c := strings.TrimSpace(str(m["tray_color"])); len(c) >= 6 && strings.Trim(c, "0") != "" {
		t.Color = "#" + strings.ToUpper(c[:6])
	}
	t.Empty = t.Type == "" && t.Color == "" && t.UUID == ""
	return t
}
