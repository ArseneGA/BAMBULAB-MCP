package printer

import (
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeGcodeState(t *testing.T) {
	cases := map[string]State{
		"IDLE": StateIdle, "INIT": StatePreparing, "PREPARE": StatePreparing, "SLICING": StatePreparing,
		"RUNNING": StatePrinting, "PAUSE": StatePaused, "FINISH": StateFinished, "FAILED": StateFailed,
		"running": StatePrinting, "": StateUnknown, "WHATEVER": StateUnknown,
	}
	for in, want := range cases {
		if got := NormalizeGcodeState(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestPartialReportsMerge(t *testing.T) {
	m := newReportMerger()
	m.merge(map[string]any{"gcode_state": "IDLE", "nozzle_temper": 25.0, "hms": []any{}, "mc_percent": 0.0,
		"ams": map[string]any{"tray_now": "255", "humidity": "4"}})
	if !m.full {
		t.Fatal("pushall-like report should be full")
	}
	// Rapport partiel : seulement quelques clés.
	m.merge(map[string]any{"mc_percent": 42.0, "layer_num": 7.0, "ams": map[string]any{"tray_now": "0"},
		"hms": []any{map[string]any{"attr": 3.0, "code": 4.0}}})
	s := m.toStatus()
	if s.Progress != 42 || s.Layer != 7 || s.GcodeState != "IDLE" || s.Temps.Nozzle != 25 {
		t.Fatalf("merge lost data: %+v", s)
	}
	if got := m.data["ams"].(map[string]any); got["tray_now"] != "0" || got["humidity"] != "4" {
		t.Fatalf("nested merge wrong: %v", got)
	}
	if len(s.HMS) != 1 || s.HMS[0].Attr != 3 || !s.HasError() {
		t.Fatalf("hms: %+v", s.HMS)
	}
	// Partiel seul : jamais « full ».
	p := newReportMerger()
	p.merge(map[string]any{"mc_percent": 1.0})
	if p.full || p.toStatus().State != StateUnknown {
		t.Fatal("partial-only report must stay unknown/not full")
	}
}

func TestSecretNeverPrinted(t *testing.T) {
	s := StaticSecret("super-secret-code")
	for _, f := range []string{"%v", "%s", "%+v", "%#v"} {
		if out := fmt.Sprintf(f, s); strings.Contains(out, "super-secret") {
			t.Fatalf("secret leaked with %s: %s", f, out)
		}
	}
}

func TestRemoteNameSafe(t *testing.T) {
	sha := strings.Repeat("ab", 32)
	cases := map[string]string{
		"benchy.gcode.3mf":         "benchy-abababababab.gcode.3mf",
		"../../etc/passwd":         "passwd-abababababab.3mf",
		`C:\x\my model!.gcode.3mf`: "my_model-abababababab.gcode.3mf",
		"":                         "job-abababababab.3mf",
	}
	for in, want := range cases {
		if got := remoteName(in, sha); got != want {
			t.Errorf("remoteName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStartUnknownErrorIs(t *testing.T) {
	var e error = &StartUnknownError{Stage: StageAcked, Cause: errTimeout}
	if !isErr(e, ErrStartUnknown) || !isErr(e, errTimeout) {
		t.Fatal("StartUnknownError must unwrap to both")
	}
}

func isErr(e, target error) bool {
	type unwrapper interface{ Unwrap() []error }
	if e == target {
		return true
	}
	if u, ok := e.(unwrapper); ok {
		for _, x := range u.Unwrap() {
			if isErr(x, target) {
				return true
			}
		}
	}
	return false
}
