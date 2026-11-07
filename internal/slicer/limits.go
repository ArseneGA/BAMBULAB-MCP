package slicer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

// Bornes de repli quand l'installation de Studio ne décrit pas le modèle d'imprimante (profils de test, modèle
// inconnu). Ce sont les valeurs de la P1S/A1 (buse 300 °C, plateau 100 °C), pas des valeurs devinées pour un
// autre modèle : sur un modèle connu de l'installation, la valeur de l'installation prime toujours.
const (
	fallbackNozzleMaxC = 300.0
	fallbackBedMaxC    = 100.0
)

// PrinterLimits : températures maximales d'un modèle d'imprimante, lues dans resources/printers/*.json de
// l'installation de Bambu Studio (nozzle_temp_range, bed_temperature_limit ou bed_temp_range, plage de chambre).
type PrinterLimits struct {
	Model       string  `json:"model"`
	NozzleMaxC  float64 `json:"nozzle_max_c"`
	BedMaxC     float64 `json:"bed_max_c"`
	ChamberMaxC float64 `json:"chamber_max_c"` // 0 : pas de chambre chauffée
	Known       bool    `json:"known"`         // faux : valeurs de repli
	Source      string  `json:"source"`
}

// Trust regroupe ce qui sert de référence de confiance pour juger un fichier : les profils officiels de
// l'installation de Bambu Studio et ses fiches d'imprimantes. Rien de ce que contient l'archive jugée n'y entre.
type Trust struct {
	Store       *profiles.Store
	PrintersDir string

	once     sync.Once
	printers map[string]PrinterLimits
}

// NewTrust construit la référence de confiance d'un catalogue de profils ; le répertoire des fiches
// d'imprimantes se déduit de celui des profils (…/resources/profiles/BBL -> …/resources/printers).
func NewTrust(st *profiles.Store) *Trust {
	if st != nil {
		if v, ok := trustCache.Load(st); ok {
			return v.(*Trust)
		}
	}
	t := &Trust{Store: st}
	if st != nil && st.Dir != "" {
		t.PrintersDir = filepath.Clean(filepath.Join(st.Dir, "..", "..", "printers"))
	}
	if st != nil {
		v, _ := trustCache.LoadOrStore(st, t)
		return v.(*Trust)
	}
	return t
}

var trustCache sync.Map // *profiles.Store -> *Trust (les fiches d'imprimantes ne se relisent qu'une fois)

var (
	defTrustOnce sync.Once
	defTrust     *Trust
)

// DefaultTrust : référence de confiance de l'installation locale de Bambu Studio (nil si absente). Sert quand
// l'appelant de ValidateArchive n'en fournit pas.
func DefaultTrust() *Trust {
	defTrustOnce.Do(func() {
		bin, err := Locate("")
		if err != nil {
			return
		}
		d := bin.ProfilesDir()
		if d == "" {
			return
		}
		st, err := profiles.Load(d)
		if err != nil {
			return
		}
		defTrust = NewTrust(st)
	})
	return defTrust
}

func (t *Trust) load() {
	t.once.Do(func() {
		t.printers = map[string]PrinterLimits{}
		if t.PrintersDir == "" {
			return
		}
		fs, _ := filepath.Glob(filepath.Join(t.PrintersDir, "*.json"))
		for _, f := range fs {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var raw map[string]json.RawMessage
			if json.Unmarshal(b, &raw) != nil {
				continue
			}
			base, ok := raw["00.00.00.00"] // micrologiciel de base : les paliers suivants ne changent pas les maxima
			if !ok {
				continue
			}
			var e struct {
				Display string `json:"display_name"`
				Print   struct {
					NozzleRange   []float64 `json:"nozzle_temp_range"`
					NozzleMax     float64   `json:"nozzle_max_temperature"`
					BedLimit      float64   `json:"bed_temperature_limit"`
					BedRange      []float64 `json:"bed_temp_range"`
					ChamberEdit   bool      `json:"support_chamber_temp_edit"`
					ChamberRange  []float64 `json:"support_chamber_temp_edit_range"`
					ChamberSwitch float64   `json:"support_chamber_temp_switch_heating"`
				} `json:"print"`
			}
			if json.Unmarshal(base, &e) != nil || e.Display == "" {
				continue
			}
			pl := PrinterLimits{Model: e.Display, Known: true, Source: filepath.Base(f)}
			if len(e.Print.NozzleRange) == 2 {
				pl.NozzleMaxC = e.Print.NozzleRange[1]
			}
			if e.Print.NozzleMax > 0 {
				pl.NozzleMaxC = e.Print.NozzleMax
			}
			pl.BedMaxC = e.Print.BedLimit
			if len(e.Print.BedRange) == 2 {
				pl.BedMaxC = e.Print.BedRange[1]
			}
			if e.Print.ChamberEdit && len(e.Print.ChamberRange) == 2 {
				pl.ChamberMaxC = e.Print.ChamberRange[1]
			}
			if pl.NozzleMaxC <= 0 || pl.BedMaxC <= 0 {
				continue // fiche incomplète : on ne devine pas, repli
			}
			t.printers[strings.ToLower(e.Display)] = pl
		}
	})
}

// Limits retourne les maxima du modèle d'imprimante (nom affiché, ex. « Bambu Lab P1S »), ou les valeurs de repli.
func (t *Trust) Limits(model string) PrinterLimits {
	if t != nil {
		t.load()
		if pl, ok := t.printers[strings.ToLower(strings.TrimSpace(model))]; ok {
			return pl
		}
	}
	return PrinterLimits{Model: model, NozzleMaxC: fallbackNozzleMaxC, BedMaxC: fallbackBedMaxC, Source: "repli (modèle absent de l'installation)"}
}

// Trust retourne la référence de confiance de ce Slicer (profils chargés).
func (s *Slicer) Trust() *Trust { return s.trust }
