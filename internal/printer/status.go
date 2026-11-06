package printer

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// NormalizeGcodeState convertit gcode_state brut en State normalisé.
// Valeurs connues (note de recherche §3) : IDLE, INIT, PREPARE, SLICING,
// RUNNING, PAUSE, FINISH, FAILED. Tout le reste -> unknown.
func NormalizeGcodeState(raw string) State {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "IDLE":
		return StateIdle
	case "INIT", "PREPARE", "SLICING":
		return StatePreparing
	case "RUNNING":
		return StatePrinting
	case "PAUSE":
		return StatePaused
	case "FINISH":
		return StateFinished
	case "FAILED":
		return StateFailed
	default:
		return StateUnknown
	}
}

// reportMerger fusionne les rapports partiels `print` (P1 : seulement les
// changements). La fusion est récursive pour les objets, remplaçante pour
// tout le reste (tableaux compris, ex. hms).
type reportMerger struct {
	data map[string]any
	full bool
}

func newReportMerger() *reportMerger { return &reportMerger{data: map[string]any{}} }

func mergeInto(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergeInto(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}

// merge intègre l'objet "print" d'un push_status. Un rapport « complet » est
// reconnu à la présence de gcode_state ET de nozzle_temper ET de hms, ce qui
// est le cas d'un pushall ; heuristique consignée dans docs/jalons/J7.md.
func (m *reportMerger) merge(print map[string]any) {
	mergeInto(m.data, print)
	_, a := print["gcode_state"]
	_, b := print["nozzle_temper"]
	_, c := print["hms"]
	if a && b && c {
		m.full = true
	}
}

// toStatus projette l'état fusionné.
func (m *reportMerger) toStatus() Status {
	d := m.data
	s := Status{
		GcodeState:     str(d["gcode_state"]),
		Progress:       int(num(d["mc_percent"])),
		Layer:          int(num(d["layer_num"])),
		TotalLayers:    int(num(d["total_layer_num"])),
		RemainingMin:   int(num(d["mc_remaining_time"])),
		PrintError:     int(num(d["print_error"])),
		FailReason:     str(d["fail_reason"]),
		SubtaskName:    str(d["subtask_name"]),
		GcodeFile:      str(d["gcode_file"]),
		TaskID:         str(d["task_id"]),
		FullReportSeen: m.full,
	}
	s.Temps = Temps{
		Nozzle:       num(d["nozzle_temper"]),
		NozzleTarget: num(d["nozzle_target_temper"]),
		Bed:          num(d["bed_temper"]),
		BedTarget:    num(d["bed_target_temper"]),
		Chamber:      num(d["chamber_temper"]),
	}
	if s.FailReason == "0" {
		s.FailReason = ""
	}
	if arr, ok := d["hms"].([]any); ok {
		for _, e := range arr {
			if em, ok := e.(map[string]any); ok {
				s.HMS = append(s.HMS, HMSError{Attr: uint32(num(em["attr"])), Code: uint32(num(em["code"]))})
			}
		}
	}
	if s.GcodeState == "" {
		s.State = StateUnknown
	} else {
		s.State = NormalizeGcodeState(s.GcodeState)
	}
	return s
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	}
	return ""
}

func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	return 0
}

var _ = time.Second
