package upgrade

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

type logEnv struct {
	*fakeEnv
	lines []string
}

func (l *logEnv) Logf(f string, a ...any) { l.lines = append(l.lines, fmt.Sprintf(f, a...)) }

// Un passage sans nouveauté est SILENCIEUX ; un report (25 h d'impression = 300 passages) n'écrit qu'une ligne par heure.
func TestJournalNoiseIsThrottled(t *testing.T) {
	f := newFake()
	f.head = shaOld
	l := &logEnv{fakeEnv: f}
	u := &Upgrader{Cfg: Config{Branch: "main", SmokeMode: SmokeRequired}, Env: l, StatePath: t.TempDir() + "/s.json"}
	for i := 0; i < 5; i++ {
		mustRun(t, u, Options{})
	}
	if len(l.lines) != 0 {
		t.Fatalf("à jour : aucune ligne attendue, obtenu %v", l.lines)
	}

	f2 := newFake()
	f2.busy = []BusyState{{Busy: true, Reasons: []string{"print_active: 1 impression(s) PRINTING"}}}
	l2 := &logEnv{fakeEnv: f2}
	u2 := &Upgrader{Cfg: Config{Branch: "main", SmokeMode: SmokeRequired}, Env: l2, StatePath: t.TempDir() + "/s.json"}
	count := func() int {
		n := 0
		for _, s := range l2.lines {
			if strings.Contains(s, "occupé") {
				n++
			}
		}
		return n
	}
	for i := 0; i < 12; i++ { // 12 passages à 5 minutes = 1 heure
		f2.busyCalls = 0
		mustRun(t, u2, Options{})
		f2.now = f2.now.Add(5 * time.Minute)
	}
	if n := count(); n != 1 && n != 2 {
		t.Fatalf("un report pendant une heure doit écrire 1 ou 2 lignes, pas %d : %v", n, l2.lines)
	}
	if len(f2.notified) != 1 {
		t.Fatalf("une seule notification de report : %d", len(f2.notified))
	}
	f2.now = f2.now.Add(2 * time.Hour)
	before := count()
	f2.busyCalls = 0
	mustRun(t, u2, Options{})
	if count() != before+1 {
		t.Fatal("une heure plus tard, le report est de nouveau journalisé")
	}
}
