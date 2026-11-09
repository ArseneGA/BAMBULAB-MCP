package upgrade

import (
	"fmt"
	"runtime"
	"strings"
	"time"
)

func runtimeEnforcesModes() bool { return runtime.GOOS != "windows" }

// Window est une plage horaire locale pendant laquelle une mise à jour peut être INSTALLÉE (le redémarrage).
// Plage vide : toujours permis. « 07:00-23:00 » : pas la nuit. « 22:00-06:00 » : plage qui passe minuit.
type Window struct {
	set        bool
	start, end int // minutes depuis minuit
}

// ParseWindow lit « HH:MM-HH:MM » (vide : pas de restriction).
func ParseWindow(s string) (Window, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Window{}, nil
	}
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return Window{}, fmt.Errorf("%q : attendu HH:MM-HH:MM", s)
	}
	st, err := parseHM(a)
	if err != nil {
		return Window{}, err
	}
	en, err := parseHM(b)
	if err != nil {
		return Window{}, err
	}
	if st == en {
		return Window{}, fmt.Errorf("%q : début et fin identiques", s)
	}
	return Window{set: true, start: st, end: en}, nil
}

func parseHM(s string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("heure %q invalide (attendu HH:MM)", s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Set dit si une restriction est configurée.
func (w Window) Set() bool { return w.set }

// Contains dit si l'instant t (dans son fuseau, donc l'heure locale du serveur) est dans la plage. La fin est exclue.
func (w Window) Contains(t time.Time) bool {
	if !w.set {
		return true
	}
	m := t.Hour()*60 + t.Minute()
	if w.start < w.end {
		return m >= w.start && m < w.end
	}
	return m >= w.start || m < w.end // passe minuit
}

// String : « 07:00-23:00 » ou « toute la journée ».
func (w Window) String() string {
	if !w.set {
		return "toute la journée"
	}
	return fmt.Sprintf("%02d:%02d-%02d:%02d", w.start/60, w.start%60, w.end/60, w.end%60)
}
