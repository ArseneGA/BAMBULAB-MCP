package sim

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// Accessoires simulés (N4) : lumières (ledctrl), ventilateurs (gcode_line M106 P1/P2/P3 S0..255) et vitesse
// (print_speed 1..4), reflétés dans lights_report, cooling_fan_speed / big_fan1_speed / big_fan2_speed (crans 0 à 15 sous
// forme de texte, comme la P1) et spd_lvl / spd_mag. Le simulateur n'accepte QUE ces M106 : tout autre gcode_line est
// refusé (result:fail), et chaque commande reçue est consignée pour les tests (Commands, GcodeLines).

type accModelState struct {
	chamberLight, workLight string // on | off
	gear                    [4]int // crans 0 à 15 ; index 1 pièce, 2 auxiliaire, 3 chambre
	speed                   int    // 1 à 4
}

func newAccState() accModelState {
	return accModelState{chamberLight: "on", workLight: "off", speed: 2}
}

var speedMag = map[int]int{1: 50, 2: 100, 3: 124, 4: 166}

func (m *model) addAccessories(r map[string]any) {
	r["lights_report"] = []any{
		map[string]any{"node": "chamber_light", "mode": m.acc.chamberLight},
		map[string]any{"node": "work_light", "mode": m.acc.workLight},
	}
	r["cooling_fan_speed"] = strconv.Itoa(m.acc.gear[1])
	r["big_fan1_speed"] = strconv.Itoa(m.acc.gear[2])
	r["big_fan2_speed"] = strconv.Itoa(m.acc.gear[3])
	r["spd_mag"] = speedMag[m.acc.speed]
}

var m106Re = regexp.MustCompile(`^M106 P([123]) S([0-9]{1,3})\n$`)

// setLight applique ledctrl.
func (m *model) setLight(node, mode string) error {
	if mode != "on" && mode != "off" {
		return fmt.Errorf("led_mode %q", mode)
	}
	switch node {
	case "chamber_light":
		m.acc.chamberLight = mode
	case "work_light":
		m.acc.workLight = mode
	default:
		return fmt.Errorf("led_node %q", node)
	}
	return nil
}

// gcodeLine n'accepte que M106 P1|P2|P3 S0..255 ; tout le reste est refusé.
func (m *model) gcodeLine(param string) error {
	mm := m106Re.FindStringSubmatch(param)
	if mm == nil {
		return errors.New("unsupported gcode_line (only M106 P1/P2/P3 S0-255 is simulated)")
	}
	p, _ := strconv.Atoi(mm[1])
	v, _ := strconv.Atoi(mm[2])
	if v > 255 {
		return errors.New("M106 S out of range")
	}
	m.acc.gear[p] = int(math.Round(float64(v) * 15 / 255))
	return nil
}

// setSpeed : seulement pendant une impression.
func (m *model) setSpeed(param string) error {
	switch m.gcodeState {
	case "PREPARE", "RUNNING", "PAUSE":
	default:
		return errors.New("print_speed only valid while printing")
	}
	l, err := strconv.Atoi(param)
	if err != nil || l < 1 || l > 4 {
		return fmt.Errorf("print_speed %q", param)
	}
	m.acc.speed = l
	return nil
}

// onSystem traite les messages {"system": …} (ledctrl).
func (s *Sim) onSystem(p map[string]any) {
	cmd := fmt.Sprint(p["command"])
	seq := fmt.Sprint(p["sequence_id"])
	s.noteCommand(cmd)
	var err error
	if cmd != "ledctrl" {
		err = errors.New("unsupported system command")
	} else {
		s.mu.Lock()
		err = s.p.setLight(fmt.Sprint(p["led_node"]), fmt.Sprint(p["led_mode"]))
		s.mu.Unlock()
	}
	r := map[string]any{"command": cmd, "sequence_id": seq, "result": "success", "reason": ""}
	if err != nil {
		r["result"], r["reason"] = "fail", err.Error()
	}
	s.publishReport(map[string]any{"system": r})
	s.publishDelta(false)
}

// onAccessoryPrint traite gcode_line et print_speed ; renvoie faux si la commande n'est pas un accessoire.
func (s *Sim) onAccessoryPrint(cmd, seq string, p map[string]any) bool {
	switch cmd {
	case "gcode_line":
		param := fmt.Sprint(p["param"])
		s.mu.Lock()
		s.gcodes = append(s.gcodes, param)
		err := s.p.gcodeLine(param)
		s.mu.Unlock()
		s.respond(cmd, seq, err)
	case "print_speed":
		s.mu.Lock()
		err := s.p.setSpeed(fmt.Sprint(p["param"]))
		s.mu.Unlock()
		s.respond(cmd, seq, err)
	default:
		return false
	}
	s.publishDelta(false)
	return true
}

// GcodeLines renvoie les paramètres des gcode_line reçus.
func (s *Sim) GcodeLines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.gcodes...)
}

// Accessories renvoie l'état simulé : lumières, crans de ventilateur (pièce, auxiliaire, chambre), niveau de vitesse.
func (s *Sim) Accessories() (chamberLight, workLight string, gears [3]int, speed int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.p.acc
	return a.chamberLight, a.workLight, [3]int{a.gear[1], a.gear[2], a.gear[3]}, a.speed
}
