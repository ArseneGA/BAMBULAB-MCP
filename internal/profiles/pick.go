package profiles

import (
	"fmt"
	"strings"
)

// Match dit à quel point un preset process/filament est fait pour la machine.
type Match int

const (
	MatchOwn       Match = iota // conçu pour ce modèle (suffixe « @BBL P1S »)
	MatchExact                  // compatible_printers ne cite que des variantes de ce modèle
	MatchListed                 // compatible_printers cite la machine, partagé avec d'autres modèles
	MatchCondition              // compatible par condition (pas de liste)
)

func (m Match) String() string {
	return [...]string{"propre", "exact", "partagé", "par condition"}[m]
}

// Choice est un preset choisi pour une machine, avec la raison et une réserve éventuelle.
type Choice struct {
	Name  string `json:"name"`
	Match string `json:"match"`
	// Note : à afficher quand le preset a été dessiné pour un autre modèle (ex. « @BBL X1C » pour une P1S).
	Note string `json:"note,omitempty"`
}

// MachineCode : code court du modèle dans les noms de presets Bambu (« Bambu Lab X1 Carbon » → « X1C »,
// « A1 mini » → « A1M », « P1S » → « P1S »).
func MachineCode(printerModel string) string {
	m := strings.TrimSpace(printerModel)
	m = strings.TrimPrefix(m, "Bambu Lab ")
	switch strings.ToLower(m) {
	case "x1 carbon":
		return "X1C"
	case "a1 mini":
		return "A1M"
	case "h2d pro":
		return "H2DP"
	}
	return strings.ToUpper(strings.ReplaceAll(m, " ", ""))
}

// presetCode extrait le code après « @BBL » (« Bambu PETG Basic @BBL X1C 0.2 nozzle » → « X1C »).
func presetCode(name string) string {
	_, after, ok := strings.Cut(name, "@BBL ")
	if !ok {
		return ""
	}
	f := strings.Fields(after)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// materialWord : première matière connue dans un nom de preset (« Bambu PETG Basic » → « PETG »).
func materialWord(name string) string {
	for _, w := range strings.Fields(strings.ToUpper(name)) {
		switch w {
		case "PLA", "PETG", "ABS", "ASA", "TPU", "PC", "PA", "PVA", "PET", "PPS", "HIPS", "PP":
			return w
		}
	}
	return ""
}

func presetBase(name string) string {
	b, _, _ := strings.Cut(name, " @")
	return strings.TrimSpace(b)
}

func (s *Store) matchFor(k Kind, name string, m *Resolved, ownCode, ownModel string) (Match, bool, error) {
	r, err := s.Resolve(k, name)
	if err != nil {
		return 0, false, err
	}
	ok, err := compatibleWith(r, m)
	if err != nil || !ok {
		return 0, false, err
	}
	if c := presetCode(name); c != "" && c == ownCode {
		return MatchOwn, true, nil
	}
	list := r.Strings("compatible_printers")
	if len(list) == 0 {
		return MatchCondition, true, nil
	}
	exact := true
	for _, n := range list {
		if !strings.Contains(n, ownModel) {
			exact = false
			break
		}
	}
	if exact {
		return MatchExact, true, nil
	}
	return MatchListed, true, nil
}

// PickProcess choisit le preset process par défaut d'une machine : on part du process déclaré par la machine
// (default_print_profile, souvent hérité d'un X1C) et on lui préfère, à qualité égale (même préfixe, ex.
// « 0.20mm Standard »), un preset compatible dessiné pour la machine elle-même, puis pour son modèle, puis
// partagé, puis par condition. Un preset d'un autre modèle n'est gardé qu'en dernier recours et signalé.
func (s *Store) PickProcess(machine string) (Choice, error) {
	def, _ := s.DefaultProcessOf(machine)
	return s.pick(KindProcess, machine, def)
}

// PickFilament fait de même pour un preset filament (par défaut default_filament_profile[0]).
func (s *Store) PickFilament(machine string) (Choice, error) {
	m, err := s.Resolve(KindMachine, machine)
	if err != nil {
		return Choice{}, err
	}
	def := ""
	if l := m.Strings("default_filament_profile"); len(l) > 0 {
		def = l[0]
	}
	return s.pick(KindFilament, machine, def)
}

// PickSimilar cherche, pour un preset filament/process donné (souvent le nom d'un autre modèle), le meilleur
// preset de même base compatible avec la machine.
func (s *Store) PickSimilar(k Kind, machine, name string) (Choice, error) {
	return s.pick(k, machine, name)
}

func (s *Store) pick(k Kind, machine, def string) (Choice, error) {
	m, err := s.Resolve(KindMachine, machine)
	if err != nil {
		return Choice{}, err
	}
	model, _ := m.String("printer_model")
	code := MachineCode(model)
	if def == "" {
		return Choice{}, fmt.Errorf("%w: aucun preset par défaut pour %q", ErrNotFound, machine)
	}
	best, bestScore, found := def, Match(99), false
	if _, err := s.Resolve(k, def); err == nil {
		if sc, ok, _ := s.matchFor(k, def, m, code, model); ok {
			best, bestScore, found = def, sc, true
		}
	}
	base := presetBase(def)
	for _, n := range s.Names(k) {
		if n == def || presetBase(n) != base {
			continue
		}
		sc, ok, err := s.matchFor(k, n, m, code, model)
		if err != nil || !ok {
			continue
		}
		if !found || sc < bestScore {
			best, bestScore, found = n, sc, true
		}
	}
	fallback := ""
	if !found && k == KindFilament {
		// le défaut déclaré n'existe pas ou n'est pas compatible (P1P : « Bambu PLA Basic @BBL P1P » absent des
		// profils) : repli sur la même matière, de préférence « Generic <matière> »
		mat := materialWord(base)
		for _, n := range s.Names(k) {
			if mat == "" || !strings.HasPrefix(n, "Generic "+mat) {
				continue
			}
			sc, ok, err := s.matchFor(k, n, m, code, model)
			if err != nil || !ok {
				continue
			}
			if !found || sc < bestScore || (sc == bestScore && len(n) < len(best)) {
				best, bestScore, found = n, sc, true
			}
		}
		if found {
			fallback = fmt.Sprintf("le filament par défaut « %s » n'est pas disponible pour %s dans les profils installés : repli sur la matière générique", def, code)
		}
	}
	if !found {
		return Choice{}, fmt.Errorf("%w: aucun preset %q compatible avec %q", ErrNotFound, base, machine)
	}
	c := Choice{Name: best, Match: bestScore.String(), Note: fallback}
	if pc := presetCode(best); pc != "" && pc != code && bestScore != MatchOwn {
		c.Note = strings.TrimSpace(c.Note + " ")
		c.Note += fmt.Sprintf("preset dessiné pour %s, déclaré compatible avec %s par Bambu Studio (aucun preset propre à ce modèle)", pc, code)
	}
	return c, nil
}
