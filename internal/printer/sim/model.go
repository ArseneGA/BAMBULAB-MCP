package sim

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// model est l'état interne de l'imprimante simulée (protégé par Sim.mu).
type model struct {
	gcodeState string // IDLE, PREPARE, RUNNING, PAUSE, FINISH, FAILED
	subtask    string
	gcodeFile  string
	taskID     string
	file       string

	prepareUntil time.Time
	runStarted   time.Time
	pausedAt     time.Time
	pausedTotal  time.Duration
	prepareDur   time.Duration
	printDur     time.Duration
	totalLayers  int

	percent   int
	layer     int
	remaining int

	nozzle, nozzleT, bed, bedT, chamber float64

	printError int
	hms        []map[string]any
	failReason string
	hmsFault   bool
	wifi       int
	taskSeq    int
}

func newModel(c Config) *model {
	return &model{gcodeState: "IDLE", prepareDur: c.PrepareDuration, printDur: c.PrintDuration,
		totalLayers: c.TotalLayers, nozzle: 25, bed: 25, chamber: 25, wifi: -45}
}

func (m *model) setHMS(on bool) {
	m.hmsFault = on
	if on {
		// Entrée HMS fictive : attr/code entiers (forme décrite par la note de recherche).
		m.hms = []map[string]any{{"attr": 0x0C00 << 16, "code": 0x00010001}}
		m.printError = 0x0500C011
		if m.gcodeState == "RUNNING" {
			m.gcodeState = "PAUSE"
			m.pausedAt = time.Now()
		}
	} else {
		m.hms = nil
		m.printError = 0
	}
}

// begin traite project_file ; erreur = refus avec result:fail.
func (m *model) begin(now time.Time, file, param, subtask string) error {
	switch m.gcodeState {
	case "IDLE", "FINISH", "FAILED":
	default:
		return errors.New("printer busy")
	}
	if m.hmsFault {
		return errors.New("printer reports an HMS error")
	}
	m.file = file
	m.gcodeFile = param
	m.subtask = subtask
	if m.subtask == "" || m.subtask == "<nil>" {
		m.subtask = strings.TrimSuffix(strings.TrimSuffix(file, ".3mf"), ".gcode")
	}
	m.taskSeq++
	m.taskID = fmt.Sprint(1000 + m.taskSeq)
	m.gcodeState = "PREPARE"
	m.prepareUntil = now.Add(m.prepareDur)
	m.percent, m.layer, m.remaining = 0, 0, int(math.Ceil(m.printDur.Minutes()))
	m.pausedTotal, m.pausedAt = 0, time.Time{}
	m.failReason = ""
	m.nozzleT, m.bedT = 220, 60
	return nil
}

func (m *model) control(cmd string, now time.Time) error {
	switch cmd {
	case "pause":
		if m.gcodeState != "RUNNING" {
			return errors.New("not running")
		}
		m.gcodeState, m.pausedAt = "PAUSE", now
	case "resume":
		if m.gcodeState != "PAUSE" {
			return errors.New("not paused")
		}
		if m.hmsFault {
			return errors.New("error not cleared")
		}
		m.pausedTotal += now.Sub(m.pausedAt)
		m.gcodeState = "RUNNING"
	case "stop":
		switch m.gcodeState {
		case "PREPARE", "RUNNING", "PAUSE":
			m.gcodeState = "FAILED"
			m.failReason = "50348044" // « annulé par l'utilisateur » (valeur fictive)
			m.nozzleT, m.bedT = 0, 0
		default:
			return errors.New("nothing to stop")
		}
	}
	return nil
}

func approach(cur, target, step float64) float64 {
	if cur < target {
		return math.Min(cur+step, target)
	}
	return math.Max(cur-step, target)
}

// step avance la simulation.
func (m *model) step(now time.Time, fileMissingFault bool, exists func(string) bool) {
	switch m.gcodeState {
	case "PREPARE":
		if now.After(m.prepareUntil) {
			if fileMissingFault || !exists(m.file) {
				m.gcodeState = "FAILED"
				m.failReason = "07008011" // fichier introuvable (valeur fictive)
				m.printError = 0
				m.nozzleT, m.bedT = 0, 0
				break
			}
			m.gcodeState = "RUNNING"
			m.runStarted = now
		}
	case "RUNNING":
		el := now.Sub(m.runStarted) - m.pausedTotal
		p := float64(el) / float64(m.printDur)
		if p >= 1 {
			m.gcodeState, m.percent, m.layer, m.remaining = "FINISH", 100, m.totalLayers, 0
			m.nozzleT, m.bedT = 0, 0
		} else {
			m.percent = int(p * 100)
			m.layer = int(math.Ceil(p * float64(m.totalLayers)))
			m.remaining = int(math.Ceil((1 - p) * m.printDur.Minutes()))
		}
	}
	m.nozzle = approach(m.nozzle, m.nozzleT, 20)
	m.bed = approach(m.bed, m.bedT, 10)
	ct := 25.0
	if m.bedT > 0 {
		ct = 38
	}
	m.chamber = approach(m.chamber, ct, 3)
	m.wifi = -45 - int(now.Unix()%3)
}

// report produit le rapport complet ; Sim.publishDelta n'en diffuse que les écarts.
func (m *model) report(c Config) map[string]any {
	r := map[string]any{
		"gcode_state":          m.gcodeState,
		"mc_percent":           m.percent,
		"mc_remaining_time":    m.remaining,
		"layer_num":            m.layer,
		"total_layer_num":      m.totalLayers,
		"nozzle_temper":        math.Round(m.nozzle*10) / 10,
		"nozzle_target_temper": m.nozzleT,
		"bed_temper":           math.Round(m.bed*10) / 10,
		"bed_target_temper":    m.bedT,
		"chamber_temper":       math.Round(m.chamber*10) / 10,
		"print_error":          m.printError,
		"mc_print_error_code":  fmt.Sprint(m.printError),
		"fail_reason":          orZero(m.failReason),
		"hms":                  nonNilHMS(m.hms),
		"subtask_name":         m.subtask,
		"gcode_file":           m.gcodeFile,
		"task_id":              m.taskID,
		"wifi_signal":          fmt.Sprintf("%ddBm", m.wifi),
		"nozzle_diameter":      "0.4",
		"spd_lvl":              2,
		"stg_cur":              stage(m.gcodeState),
		"print_type":           "local",
		"hw_switch_state":      0,
		"sdcard":               true,
		"online":               map[string]any{"ahb": false, "rfid": false, "version": 1},
	}
	if m.taskID == "" {
		r["task_id"] = "0"
	}
	return r
}

func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

func nonNilHMS(h []map[string]any) []map[string]any {
	if h == nil {
		return []map[string]any{}
	}
	return h
}

func stage(st string) int {
	switch st {
	case "PREPARE":
		return 2
	case "RUNNING":
		return 0
	case "PAUSE":
		return 16
	}
	return -1
}
