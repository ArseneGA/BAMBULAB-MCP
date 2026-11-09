package upgrade

import (
	"errors"
	"testing"
)

// ---- Reprise d'une mise à jour interrompue ---------------------------------------------------------------------------

// Le journal d'intention est écrit sur disque AVANT la bascule du lien.
func TestIntentJournalWrittenBeforeSwitch(t *testing.T) {
	f := newFake()
	u := newUp(t, f, nil)
	var seen *PendingUpdate
	f.onSwitch = func(to Release) {
		if to.Version == "v-new" {
			seen = LoadState(u.StatePath).Pending
		}
	}
	mustRun(t, u, Options{})
	if seen == nil || seen.Target.SHA != shaNew || seen.Previous.Version != "v-old" || seen.PreSchema != 270 || seen.Archive == "" {
		t.Fatalf("journal d'intention absent ou incomplet au moment de la bascule : %+v", seen)
	}
	if LoadState(u.StatePath).Pending != nil {
		t.Fatal("le journal doit être effacé après le succès")
	}
}

func interrupted(f *fakeEnv, u *Upgrader, current Release) {
	st := LoadState(u.StatePath)
	st.Pending = &PendingUpdate{Target: f.buildRel, Previous: Release{SHA: shaOld, Version: "v-old", Schema: 270, Dir: "/opt/releases/" + shaOld},
		Archive: "/backups/preupdate-test.tar.gz", PreSchema: 270, Since: f.now}
	_ = st.Save(u.StatePath)
	f.cur = current
}

// Reboot juste après la bascule : la nouvelle version tourne bien → finalisée (ménage, notification), pas de retour arrière.
func TestResumeAfterSwitchHealthy(t *testing.T) {
	f := newFake()
	u := newUp(t, f, nil)
	interrupted(f, u, f.buildRel)
	res := mustRun(t, u, Options{})
	if res.Outcome != OutInstalled || f.has("fetch") || f.has("stop") || !f.has("finish v-new") {
		t.Fatalf("%+v %s", res, f.order())
	}
	if LoadState(u.StatePath).Pending != nil {
		t.Fatal("journal non effacé")
	}
}

func TestResumeAfterSwitchUnhealthyRollsBack(t *testing.T) {
	f := newFake()
	f.healthErrs = map[string]error{"v-new/270": errors.New("HTTP 503")}
	u := newUp(t, f, nil)
	interrupted(f, u, f.buildRel)
	res := mustRun(t, u, Options{})
	if res.Outcome != OutRolledBack || f.switched[len(f.switched)-1] != "v-old" || !f.has("start") {
		t.Fatalf("%+v %s", res, f.order())
	}
	if _, bad := LoadState(u.StatePath).Failed[shaNew]; !bad || LoadState(u.StatePath).Pending != nil {
		t.Fatalf("état : %+v", LoadState(u.StatePath))
	}
}

// Interruption avant la bascule : l'ancienne version tourne, rien à défaire.
func TestResumeBeforeSwitchLeavesOldVersion(t *testing.T) {
	f := newFake()
	u := newUp(t, f, nil)
	interrupted(f, u, f.cur) // current = ancienne version
	res := mustRun(t, u, Options{})
	if res.Outcome != OutUpToDate || f.has("stop") || f.has("switch") || f.has("restore") {
		t.Fatalf("%+v %s", res, f.order())
	}
	if LoadState(u.StatePath).Pending != nil {
		t.Fatal("journal non effacé")
	}
}

// Même désactivée, une mise à jour interrompue est menée à son terme : jamais de version non vérifiée en service.
func TestResumeEvenWhenDisabled(t *testing.T) {
	f := newFake()
	f.healthErrs = map[string]error{"v-new/270": errors.New("HTTP 503")}
	u := newUp(t, f, func(c *Config) { c.Disabled = true })
	interrupted(f, u, f.buildRel)
	if res := mustRun(t, u, Options{}); res.Outcome != OutRolledBack {
		t.Fatalf("%+v", res)
	}
}

// Retour arrière impossible : l'état reste « en cours » (retenté au passage suivant), une seule alerte critique.
func TestCriticalNotifiedOnceAndRetried(t *testing.T) {
	f := newFake()
	f.buildRel.Schema = 280
	f.dbSchema = []int{270, 280}
	f.healthErrs = map[string]error{"v-new/280": errors.New("503")}
	f.restoreErr = errors.New("archive illisible")
	u := newUp(t, f, nil)
	if res := mustRun(t, u, Options{}); res.Outcome != OutCritical {
		t.Fatalf("%+v", res)
	}
	if LoadState(u.StatePath).Pending == nil {
		t.Fatal("l'état doit rester « en cours » pour retenter")
	}
	f.cur = f.buildRel // le lien n'a pas été remis
	f.restoreErr = nil // l'humain a réparé la cause ; le passage suivant réussit
	f.dbSchema = []int{280}
	res := mustRun(t, u, Options{})
	if res.Outcome != OutRestored {
		t.Fatalf("reprise : %+v %s", res, f.order())
	}
	n := 0
	for _, e := range f.notified {
		if e.Kind == "critical" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d alertes critiques", n)
	}
}
