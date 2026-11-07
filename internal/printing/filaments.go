package printing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

// machinePreset : preset machine (modèle + buse) de l'imprimante, depuis les profils de Bambu Studio.
func (s *Service) machinePreset(printerModel string, nozzle float64) (*profiles.Store, string, error) {
	sl, err := s.slicerInst()
	if err != nil {
		return nil, "", err
	}
	if nozzle <= 0 {
		nozzle = 0.4
	}
	m, err := sl.Profiles().MachineProfile(printerModel, nozzle)
	if err != nil {
		return nil, "", err
	}
	return sl.Profiles(), m, nil
}

// resolveFilamentNames remplace chaque nom de matière libre par un preset installé et compatible. Un preset
// exact est conservé tel quel. Un nom inconnu donne une erreur avec les 5 noms les plus proches.
func (s *Service) resolveFilamentNames(printerModel string, nozzle float64, names []string) ([]string, []string, error) {
	ps, machine, err := s.machinePreset(printerModel, nozzle)
	if err != nil {
		return names, nil, nil // profils indisponibles : la découpe signalera elle-même le problème
	}
	var out, notes []string
	for _, n := range names {
		if ok, cerr := ps.IsCompatible(profiles.KindFilament, n, machine); cerr == nil && ok {
			out = append(out, n)
			continue
		}
		r, rerr := ps.ResolveFilament(machine, n)
		if rerr != nil {
			var nf *profiles.FilamentNotFound
			if errors.As(rerr, &nf) {
				return nil, nil, invalidf("%v", rerr)
			}
			return nil, nil, invalidf("filament %q : %v", n, rerr)
		}
		out = append(out, r.Entry.Name)
		note := fmt.Sprintf("« %s » → %s", n, r.Entry.Name)
		if r.Note != "" {
			note += " (" + r.Note + ")"
		}
		notes = append(notes, note)
	}
	return out, notes, nil
}

// FilamentList est la réponse de list_filaments.
type FilamentList struct {
	Printer  string               `json:"printer"`
	Machine  string               `json:"machine_preset"`
	Families []FilamentFamily     `json:"families"`
	Total    int                  `json:"total"`
	Hint     string               `json:"hint"`
	Loaded   []LoadedSpoolSummary `json:"loaded_spools,omitempty"`
	Notes    []string             `json:"notes,omitempty"`
}

// FilamentFamily regroupe les filaments d'une famille de matière.
type FilamentFamily struct {
	Family    string                   `json:"family"`
	Filaments []profiles.FilamentEntry `json:"filaments"`
}

// LoadedSpoolSummary : bobine déclarée ou lue (AMS / externe) pour rapprocher couleur et matière.
type LoadedSpoolSummary struct {
	Tray     string `json:"tray"`
	Material string `json:"material,omitempty"`
	Color    string `json:"color,omitempty"`
	Source   string `json:"source"`
}

// ListFilaments liste les presets filament compatibles avec une imprimante, groupés par famille. La couleur
// n'est donnée que lorsqu'une bobine chargée (AMS, bobine externe déclarée ou lue) la fournit.
func (s *Service) ListFilaments(printerID, family string) (*FilamentList, error) {
	if printerID == "" {
		return nil, invalidf("printer_id requis")
	}
	rec, err := s.GetPrinterRecord(context.Background(), printerID)
	if err != nil {
		return nil, err
	}
	model, err := s.GetPrinterModel(context.Background(), rec.ModelID)
	if err != nil {
		return nil, err
	}
	ps, machine, err := s.machinePreset(model.Name, rec.NozzleMM)
	if err != nil {
		return nil, fmt.Errorf("%w : profils Bambu Studio indisponibles : %v", ErrUnavailable, err)
	}
	list, err := ps.ListFilaments(machine)
	if err != nil {
		return nil, err
	}
	out := &FilamentList{Printer: printerID, Machine: machine}
	by := map[string]*FilamentFamily{}
	var order []string
	for _, e := range list {
		if family != "" && !strings.EqualFold(e.Family, family) {
			continue
		}
		f, ok := by[e.Family]
		if !ok {
			f = &FilamentFamily{Family: e.Family}
			by[e.Family] = f
			order = append(order, e.Family)
		}
		f.Filaments = append(f.Filaments, e)
		out.Total++
	}
	for _, k := range order {
		out.Families = append(out.Families, *by[k])
	}
	out.Hint = "Passer à execute_headless_slice le champ `name` d'un preset, ou simplement un nom libre (« PETG transparent », « PLA mat ») : il est résolu vers un preset de cette liste. La couleur réelle vient des bobines chargées (AMS / bobine externe déclarée), jamais du profil."
	out.Loaded = s.loadedSpools(printerID)
	return out, nil
}

// SpoolSource fournit les bobines chargées (AMS lu, inventaire, bobine externe déclarée) d'une imprimante.
// La couleur et la matière d'une bobine ne viennent QUE d'ici, jamais du profil de découpe.
type SpoolSource func(ctx context.Context, printerID string) []LoadedSpoolSummary

// SetSpoolSource branche la source des bobines chargées (nil la retire).
func (s *Service) SetSpoolSource(f SpoolSource) {
	s.hookMu.Lock()
	s.spoolSource = f
	s.hookMu.Unlock()
}

func (s *Service) loadedSpools(printerID string) []LoadedSpoolSummary {
	s.hookMu.Lock()
	f := s.spoolSource
	s.hookMu.Unlock()
	if f == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return f(ctx, printerID)
}

// colorForTray : couleur de la bobine chargée à cet emplacement (inventaire, AMS ou bobine externe déclarée).
func trayColor(loaded []LoadedSpoolSummary, tray string) (LoadedSpoolSummary, bool) {
	for _, l := range loaded {
		if l.Tray == tray && l.Color != "" {
			return l, true
		}
	}
	return LoadedSpoolSummary{}, false
}
