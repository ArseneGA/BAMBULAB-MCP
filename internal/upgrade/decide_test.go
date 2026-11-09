package upgrade

import (
	"strings"
	"testing"
	"time"
)

const (
	shaOld = "1111111111111111111111111111111111111111"
	shaNew = "2222222222222222222222222222222222222222"
)

func green() CIResult { return CIResult{CIGreen, "CI verte (5 contrôle(s))"} }

func at(h, m int) time.Time { return time.Date(2026, 11, 9, h, m, 0, 0, time.Local) }

// La logique de décision : CI verte / rouge / en cours, occupé, désactivé, épinglé, rien de neuf, fenêtre, échec connu.
func TestDecide(t *testing.T) {
	win, _ := ParseWindow("07:00-23:00")
	base := func() Inputs {
		return Inputs{Now: at(14, 0), Current: shaOld, Target: shaNew, CI: green()}
	}
	cases := []struct {
		name string
		mod  func(*Inputs)
		want Action
		in   string // fragment attendu dans la raison
	}{
		{"feu vert", func(*Inputs) {}, ActInstall, "installation"},
		{"rien de neuf", func(i *Inputs) { i.Target = shaOld }, ActUpToDate, "à jour"},
		{"rien de neuf, même sans CI", func(i *Inputs) { i.Target = shaOld; i.CI = CIResult{CIRed, "x"} }, ActUpToDate, ""},
		{"épinglé et déjà installé", func(i *Inputs) { i.Target = shaOld; i.Pinned = true }, ActUpToDate, "épinglée"},
		{"désactivé", func(i *Inputs) { i.Disabled = true }, ActDisabled, "désactivées"},
		{"désactivé mais manuel", func(i *Inputs) { i.Disabled = true; i.Manual = true }, ActInstall, ""},
		{"CI rouge", func(i *Inputs) { i.CI = CIResult{CIRed, "CI rouge : Go (failure)"} }, ActRejectCI, "refusé"},
		{"CI en cours", func(i *Inputs) { i.CI = CIResult{CIPending, "CI en cours : Go"} }, ActWaitCI, "en attente"},
		{"CI inconnue (réseau, limite de débit)", func(i *Inputs) { i.CI = CIResult{CIUnknown, "limite de débit"} }, ActWaitCI, "limite"},
		{"CI rouge prime sur occupé", func(i *Inputs) { i.CI = CIResult{CIRed, "x"}; i.Busy = BusyState{Busy: true} }, ActRejectCI, ""},
		{"occupé : impression en cours", func(i *Inputs) {
			i.Busy = BusyState{Busy: true, Reasons: []string{"print_active: 1 impression(s) PRINTING"}}
		}, ActBusy, "print_active"},
		{"occupé inconnu = occupé", func(i *Inputs) { i.Busy = BusyState{Unknown: true, Reasons: []string{"service injoignable"}} }, ActBusy, "traité comme occupé"},
		{"occupé même en manuel", func(i *Inputs) { i.Manual = true; i.Busy = BusyState{Busy: true} }, ActBusy, ""},
		{"hors fenêtre (nuit)", func(i *Inputs) { i.Window = win; i.Now = at(3, 0) }, ActWindow, "fenêtre"},
		{"dans la fenêtre", func(i *Inputs) { i.Window = win; i.Now = at(10, 0) }, ActInstall, ""},
		{"hors fenêtre mais manuel", func(i *Inputs) { i.Window = win; i.Now = at(3, 0); i.Manual = true }, ActInstall, ""},
		{"commit déjà échoué", func(i *Inputs) {
			i.Failed = map[string]FailedEntry{shaNew: {At: at(1, 0), Reason: "contrôle de santé"}}
		}, ActKnownFail, "déjà échoué"},
		{"commit déjà échoué + retry", func(i *Inputs) {
			i.Failed = map[string]FailedEntry{shaNew: {At: at(1, 0), Reason: "x"}}
			i.Retry = true
		}, ActInstall, ""},
		{"installation d'avant l'automatique (sha inconnu)", func(i *Inputs) { i.Current = "" }, ActInstall, ""},
		{"épinglé sur un autre commit", func(i *Inputs) { i.Pinned = true }, ActInstall, ""},
	}
	for _, c := range cases {
		in := base()
		c.mod(&in)
		d := Decide(in)
		if d.Action != c.want || !strings.Contains(d.Reason, c.in) {
			t.Errorf("%s : %s (%q), attendu %s contenant %q", c.name, d.Action, d.Reason, c.want, c.in)
		}
	}
	// Les sorties silencieuses sont exactement : rien de neuf, désactivé, échec connu.
	if !Decide(Inputs{Current: shaOld, Target: shaOld}).Quiet || Decide(Inputs{Current: shaOld, Target: shaNew, CI: CIResult{CIRed, "x"}}).Quiet {
		t.Error("Quiet incorrect")
	}
}

func TestWindow(t *testing.T) {
	w, err := ParseWindow("07:00-23:00")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		h, m int
		in   bool
	}{{6, 59, false}, {7, 0, true}, {12, 0, true}, {22, 59, true}, {23, 0, false}, {3, 0, false}} {
		if got := w.Contains(at(c.h, c.m)); got != c.in {
			t.Errorf("%02d:%02d : %v, attendu %v", c.h, c.m, got, c.in)
		}
	}
	night, _ := ParseWindow("22:00-06:00") // passe minuit
	for _, c := range []struct {
		h, m int
		in   bool
	}{{21, 59, false}, {22, 0, true}, {2, 0, true}, {5, 59, true}, {6, 0, false}, {12, 0, false}} {
		if got := night.Contains(at(c.h, c.m)); got != c.in {
			t.Errorf("nuit %02d:%02d : %v, attendu %v", c.h, c.m, got, c.in)
		}
	}
	if none, _ := ParseWindow(""); !none.Contains(at(3, 0)) || none.Set() {
		t.Error("fenêtre vide : toujours permis")
	}
	for _, bad := range []string{"07:00", "7-23", "25:00-26:00", "08:00-08:00", "a-b"} {
		if _, err := ParseWindow(bad); err == nil {
			t.Errorf("%q accepté", bad)
		}
	}
}

func TestStateOnceAndPersistence(t *testing.T) {
	path := t.TempDir() + "/state.json"
	st := LoadState(path)
	now := at(10, 0)
	if !st.Once(shaNew, "deferred", now) || st.Once(shaNew, "deferred", now.Add(time.Hour)) {
		t.Fatal("Once : une seule fois par (commit, genre)")
	}
	if !st.Once(shaNew, "ci_red", now) {
		t.Fatal("un autre genre est notifié")
	}
	st.Failed[shaNew] = FailedEntry{At: now, Reason: "x"}
	if err := st.Save(path); err != nil {
		t.Fatal(err)
	}
	again := LoadState(path)
	if _, ok := again.Failed[shaNew]; !ok || again.Once(shaNew, "deferred", now) {
		t.Fatalf("état non relu : %+v", again)
	}
	if bad := LoadState(t.TempDir() + "/absent.json"); bad.Failed == nil || bad.Notified == nil {
		t.Fatal("état absent : tables vides attendues")
	}
}
