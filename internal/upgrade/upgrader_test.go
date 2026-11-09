package upgrade

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeEnv est un environnement simulé : il enregistre l'ordre exact des opérations et permet de provoquer chaque échec.
type fakeEnv struct {
	now      time.Time
	calls    []string
	notified []Event

	head, pinResolved string
	ancestors         map[[2]string]bool // (ancêtre, descendant) -> vrai ; absent : vrai
	cur               Release
	prepared          map[string]Release
	ci                CIResult
	busy              []BusyState // consommé dans l'ordre ; le dernier est répété
	busyCalls         int
	buildRel          Release
	buildErr          error
	dbSchema          []int // consommé dans l'ordre ; le dernier est répété
	dbErr             error
	backupErr         error
	syncErr           error
	restartErr        error
	healthErrs        map[string]error // clé : "version/schéma"
	smokeErr          error
	restoreErr        error
	startErr          error
	fetchErr          error
	switched          []string
	onSwitch          func(to Release)
}

func newFake() *fakeEnv {
	return &fakeEnv{
		now:      time.Date(2026, 11, 9, 14, 0, 0, 0, time.Local),
		head:     shaNew,
		cur:      Release{SHA: shaOld, Version: "v-old", Schema: 270, Dir: "/opt/releases/" + shaOld},
		ci:       CIResult{CIGreen, "CI verte"},
		busy:     []BusyState{{}},
		buildRel: Release{SHA: shaNew, Version: "v-new", Schema: 270, Dir: "/opt/releases/" + shaNew},
		dbSchema: []int{270},
	}
}

func (f *fakeEnv) rec(s string, a ...any) { f.calls = append(f.calls, fmt.Sprintf(s, a...)) }
func (f *fakeEnv) Now() time.Time         { return f.now }
func (f *fakeEnv) Logf(string, ...any)    {}
func (f *fakeEnv) Fetch(context.Context) (string, error) {
	f.rec("fetch")
	return f.head, f.fetchErr
}
func (f *fakeEnv) ResolveCommit(_ context.Context, rev string) (string, error) {
	f.rec("resolve %s", rev)
	if f.pinResolved == "" {
		return "", errors.New("introuvable")
	}
	return f.pinResolved, nil
}
func (f *fakeEnv) IsAncestor(_ context.Context, a, d string) (bool, error) {
	f.rec("ancestor %s %s", short(a), short(d))
	if v, ok := f.ancestors[[2]string{a, d}]; ok {
		return v, nil
	}
	return true, nil
}
func (f *fakeEnv) Current(context.Context) (Release, error) { return f.cur, nil }
func (f *fakeEnv) Prepared(sha string) (Release, bool) {
	r, ok := f.prepared[sha]
	return r, ok
}
func (f *fakeEnv) CI(_ context.Context, sha string) CIResult { f.rec("ci %s", short(sha)); return f.ci }
func (f *fakeEnv) Busy(context.Context) BusyState {
	f.rec("busy")
	i := f.busyCalls
	if i >= len(f.busy) {
		i = len(f.busy) - 1
	}
	f.busyCalls++
	return f.busy[i]
}
func (f *fakeEnv) DBSchema(context.Context) (int, error) {
	f.rec("dbschema")
	if f.dbErr != nil {
		return 0, f.dbErr
	}
	n := 0
	for _, c := range f.calls {
		if c == "dbschema" {
			n++
		}
	}
	i := n - 1
	if i >= len(f.dbSchema) {
		i = len(f.dbSchema) - 1
	}
	return f.dbSchema[i], nil
}
func (f *fakeEnv) Build(_ context.Context, sha string) (Release, error) {
	f.rec("build %s", short(sha))
	return f.buildRel, f.buildErr
}
func (f *fakeEnv) Backup(_ context.Context, from, target Release) (string, error) {
	f.rec("backup %s", from.Version)
	return "/backups/preupdate-test.tar.gz", f.backupErr
}
func (f *fakeEnv) Switch(_ context.Context, to Release, setPrev bool) error {
	f.rec("switch %s prev=%v", to.Version, setPrev)
	f.switched = append(f.switched, to.Version)
	if f.onSwitch != nil {
		f.onSwitch(to)
	}
	return nil
}
func (f *fakeEnv) SyncGeometry(_ context.Context, to Release) error {
	f.rec("geometry %s", to.Version)
	return f.syncErr
}
func (f *fakeEnv) RevertGeometry(context.Context) error { f.rec("geometry-revert"); return nil }
func (f *fakeEnv) Stop(context.Context) error           { f.rec("stop"); return nil }
func (f *fakeEnv) Start(context.Context) error          { f.rec("start"); return f.startErr }
func (f *fakeEnv) Restart(context.Context) error        { f.rec("restart"); return f.restartErr }
func (f *fakeEnv) Health(_ context.Context, v string, s int) error {
	f.rec("health %s/%d", v, s)
	return f.healthErrs[fmt.Sprintf("%s/%d", v, s)]
}
func (f *fakeEnv) Smoke(context.Context) error { f.rec("smoke"); return f.smokeErr }
func (f *fakeEnv) Restore(_ context.Context, with Release, a string) error {
	f.rec("restore with %s from %s", with.Version, filepath.Base(a))
	return f.restoreErr
}
func (f *fakeEnv) Notify(_ context.Context, e Event) {
	f.notified = append(f.notified, e)
	f.rec("notify %s", e.Kind)
}
func (f *fakeEnv) Finish(_ context.Context, i, p Release) {
	f.rec("finish %s (précédente %s)", i.Version, p.Version)
}

func (f *fakeEnv) has(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (f *fakeEnv) order() string { return strings.Join(f.calls, " | ") }

func newUp(t *testing.T, f *fakeEnv, mod func(*Config)) *Upgrader {
	t.Helper()
	cfg := Config{Branch: "main", SmokeMode: SmokeRequired}
	if mod != nil {
		mod(&cfg)
	}
	return &Upgrader{Cfg: cfg, Env: f, StatePath: filepath.Join(t.TempDir(), "state.json")}
}

func mustRun(t *testing.T, u *Upgrader, o Options) Result {
	t.Helper()
	res, err := u.Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run : %v", err)
	}
	return res
}

func wantOrder(t *testing.T, f *fakeEnv, want ...string) {
	t.Helper()
	// On compare la sous-suite des appels qui comptent (sans fetch/ancestor/ci/busy/dbschema/notify, sauf ceux demandés).
	var got []string
	for _, c := range f.calls {
		for _, w := range want {
			if strings.HasPrefix(c, w) {
				got = append(got, c)
				break
			}
		}
	}
	// chaque préfixe voulu apparaît, dans cet ordre
	i := 0
	for _, g := range got {
		if i < len(want) && strings.HasPrefix(g, want[i]) {
			i++
		}
	}
	if i != len(want) {
		t.Fatalf("ordre attendu %v, obtenu : %s", want, f.order())
	}
}

// ---- Rien à faire ------------------------------------------------------------------------------------------------

func TestUpToDateIsSilentAndCallsNothingExternal(t *testing.T) {
	f := newFake()
	f.head = shaOld
	res := mustRun(t, newUp(t, f, nil), Options{})
	if res.Outcome != OutUpToDate || res.Failed() {
		t.Fatalf("%+v", res)
	}
	if f.has("ci ") || f.has("busy") || f.has("build") || f.has("restart") || len(f.notified) != 0 {
		t.Fatalf("à jour : aucun appel de CI, d'occupation ni de construction : %s", f.order())
	}
}

func TestDisabledDoesNothingNotEvenFetch(t *testing.T) {
	f := newFake()
	res := mustRun(t, newUp(t, f, func(c *Config) { c.Disabled = true }), Options{})
	if res.Outcome != OutDisabled || len(f.calls) != 0 {
		t.Fatalf("%+v %s", res, f.order())
	}
	// --now passe outre l'interrupteur.
	f2 := newFake()
	res = mustRun(t, newUp(t, f2, func(c *Config) { c.Disabled = true }), Options{Manual: true})
	if res.Outcome != OutInstalled {
		t.Fatalf("--now : %+v", res)
	}
}

func TestFetchFailureIsNotAnError(t *testing.T) {
	f := newFake()
	f.fetchErr = errors.New("réseau coupé")
	res := mustRun(t, newUp(t, f, nil), Options{})
	if res.Outcome != OutNoNetwork || res.Failed() || f.has("build") {
		t.Fatalf("%+v", res)
	}
}

// ---- CI ----------------------------------------------------------------------------------------------------------

func TestRedCINeverInstalls(t *testing.T) {
	f := newFake()
	f.ci = CIResult{CIRed, "CI rouge : Go (failure)"}
	u := newUp(t, f, nil)
	res := mustRun(t, u, Options{})
	if res.Outcome != OutCIRed || res.Failed() {
		t.Fatalf("%+v", res)
	}
	if f.has("busy") || f.has("build") || f.has("backup") || f.has("switch") || f.has("restart") {
		t.Fatalf("CI rouge : rien ne doit être construit ni redémarré : %s", f.order())
	}
	if len(f.notified) != 1 || f.notified[0].Kind != "ci_red" {
		t.Fatalf("notification : %+v", f.notified)
	}
	// Passage suivant, même commit : pas de nouvelle notification.
	mustRun(t, u, Options{})
	if len(f.notified) != 1 {
		t.Fatalf("notification répétée : %+v", f.notified)
	}
}

func TestPendingOrUnknownCIWaitsAndRetries(t *testing.T) {
	for _, ci := range []CIResult{{CIPending, "CI en cours : Go"}, {CIUnknown, "limite de débit"}} {
		f := newFake()
		f.ci = ci
		u := newUp(t, f, nil)
		res := mustRun(t, u, Options{})
		if res.Outcome != OutWaitCI || res.Failed() || f.has("build") || f.has("busy") || len(f.notified) != 0 {
			t.Fatalf("%s : %+v %s", ci.State, res, f.order())
		}
		// La CI passe au vert au passage suivant : installation.
		f.ci = CIResult{CIGreen, "CI verte"}
		if res := mustRun(t, u, Options{}); res.Outcome != OutInstalled {
			t.Fatalf("après CI verte : %+v", res)
		}
	}
}

// ---- Occupation ---------------------------------------------------------------------------------------------------

func TestBusyDefersThenInstallsWhenFree(t *testing.T) {
	f := newFake()
	f.busy = []BusyState{{Busy: true, Reasons: []string{"print_active: 1 impression(s) PRINTING"}}, {}}
	u := newUp(t, f, nil)
	res := mustRun(t, u, Options{})
	if res.Outcome != OutBusy || res.Failed() {
		t.Fatalf("%+v", res)
	}
	if f.has("build") || f.has("backup") || f.has("stop") || f.has("restart") || f.has("switch") {
		t.Fatalf("impression en cours : rien ne doit être construit ni redémarré : %s", f.order())
	}
	if len(f.notified) != 1 || f.notified[0].Kind != "deferred" || !strings.Contains(f.notified[0].Message, "print_active") {
		t.Fatalf("notification de report : %+v", f.notified)
	}
	// Toujours occupé au passage suivant : pas de seconde notification pour le même commit.
	f.busyCalls = 0
	f.busy = []BusyState{{Busy: true}}
	mustRun(t, u, Options{})
	if len(f.notified) != 1 {
		t.Fatalf("report notifié deux fois : %+v", f.notified)
	}
	// Libre : installé.
	f.busy = []BusyState{{}}
	f.busyCalls = 0
	if res := mustRun(t, u, Options{}); res.Outcome != OutInstalled {
		t.Fatalf("service libre : %+v", res)
	}
}

func TestUnknownBusyIsTreatedAsBusy(t *testing.T) {
	f := newFake()
	f.busy = []BusyState{{Unknown: true, Reasons: []string{"service injoignable", "base illisible"}}}
	res := mustRun(t, newUp(t, f, nil), Options{})
	if res.Outcome != OutBusy || f.has("build") || f.has("restart") {
		t.Fatalf("%+v %s", res, f.order())
	}
}

// Une impression démarre PENDANT la construction : la version construite est gardée, rien n'est redémarré.
func TestPrintStartingDuringBuildDefersAndReusesBuild(t *testing.T) {
	f := newFake()
	f.busy = []BusyState{{}, {Busy: true, Reasons: []string{"print_active: 1"}}} // libre avant la construction, occupé après
	u := newUp(t, f, nil)
	res := mustRun(t, u, Options{})
	if res.Outcome != OutBusy || !f.has("build") {
		t.Fatalf("%+v %s", res, f.order())
	}
	if f.has("backup") || f.has("switch") || f.has("restart") || f.has("stop") {
		t.Fatalf("aucune bascule pendant une impression : %s", f.order())
	}
	// Passage suivant : libre ; la version déjà construite est réutilisée (pas de seconde construction).
	f.calls, f.busyCalls, f.busy = nil, 0, []BusyState{{}}
	f.prepared = map[string]Release{shaNew: f.buildRel}
	if res := mustRun(t, u, Options{}); res.Outcome != OutInstalled || f.has("build") {
		t.Fatalf("%+v %s", res, f.order())
	}
}

func TestOutsideWindowIsDeferredWithoutBuilding(t *testing.T) {
	f := newFake()
	f.now = time.Date(2026, 11, 9, 3, 0, 0, 0, time.Local)
	w, _ := ParseWindow("07:00-23:00")
	res := mustRun(t, newUp(t, f, func(c *Config) { c.Window = w }), Options{})
	if res.Outcome != OutWindow || f.has("build") || f.has("busy") || f.has("restart") {
		t.Fatalf("%+v %s", res, f.order())
	}
	f.now = time.Date(2026, 11, 9, 10, 0, 0, 0, time.Local)
	if res := mustRun(t, newUp(t, f, func(c *Config) { c.Window = w }), Options{}); res.Outcome != OutInstalled {
		t.Fatalf("dans la fenêtre : %+v", res)
	}
}

// ---- Épingle, historique réécrit, échec connu ----------------------------------------------------------------------

func TestPinnedVersion(t *testing.T) {
	// épinglé sur la version déjà installée : rien à faire, même si main a avancé.
	f := newFake()
	f.pinResolved = shaOld
	res := mustRun(t, newUp(t, f, func(c *Config) { c.Pin = "1111111" }), Options{})
	if res.Outcome != OutUpToDate || f.has("ci ") || f.has("build") {
		t.Fatalf("%+v %s", res, f.order())
	}
	// épinglé sur un autre commit de main : installé (CI contrôlée sur CE commit).
	f = newFake()
	f.pinResolved = shaNew
	f.head = "3333333333333333333333333333333333333333"
	res = mustRun(t, newUp(t, f, func(c *Config) { c.Pin = "2222222" }), Options{})
	if res.Outcome != OutInstalled || !f.has("ci 22222222") {
		t.Fatalf("%+v %s", res, f.order())
	}
	// épinglé sur un commit hors de la branche : refusé.
	f = newFake()
	f.pinResolved = shaNew
	f.ancestors = map[[2]string]bool{{shaNew, shaNew}: false}
	res = mustRun(t, newUp(t, f, func(c *Config) { c.Pin = "2222222" }), Options{})
	if res.Outcome != OutRefused || f.has("build") {
		t.Fatalf("%+v", res)
	}
	// épingle introuvable : refusé.
	f = newFake()
	res = mustRun(t, newUp(t, f, func(c *Config) { c.Pin = "deadbee" }), Options{})
	if res.Outcome != OutRefused || f.has("build") {
		t.Fatalf("%+v", res)
	}
}

func TestRewrittenHistoryIsRefused(t *testing.T) {
	f := newFake()
	f.ancestors = map[[2]string]bool{{shaOld, shaNew}: false}
	u := newUp(t, f, nil)
	res := mustRun(t, u, Options{})
	if res.Outcome != OutRefused || f.has("build") || f.has("ci ") {
		t.Fatalf("%+v %s", res, f.order())
	}
	if len(f.notified) != 1 || f.notified[0].Kind != "refused" {
		t.Fatalf("%+v", f.notified)
	}
}

func TestFailedCommitIsNotRetriedUntilNewCommitOrRetry(t *testing.T) {
	f := newFake()
	f.buildErr = errors.New("go test : FAIL")
	u := newUp(t, f, nil)
	if res := mustRun(t, u, Options{}); res.Outcome != OutBuildFailed || !res.Failed() {
		t.Fatalf("%+v", res)
	}
	f.calls = nil
	if res := mustRun(t, u, Options{}); res.Outcome != OutKnownFail || f.has("build") || f.has("ci ") {
		t.Fatalf("même commit : %+v %s", res, f.order())
	}
	// --retry
	f.buildErr = nil
	if res := mustRun(t, u, Options{Retry: true}); res.Outcome != OutInstalled {
		t.Fatalf("--retry : %+v", res)
	}
	// Un nouveau commit est essayé normalement.
	f2 := newFake()
	f2.buildErr = errors.New("x")
	u2 := newUp(t, f2, nil)
	mustRun(t, u2, Options{})
	f2.buildErr, f2.head = nil, "4444444444444444444444444444444444444444"
	f2.buildRel.SHA = f2.head
	if res := mustRun(t, u2, Options{}); res.Outcome != OutInstalled {
		t.Fatalf("nouveau commit : %+v", res)
	}
}

func TestCheckOnlyChangesNothing(t *testing.T) {
	f := newFake()
	res := mustRun(t, newUp(t, f, nil), Options{CheckOnly: true})
	if res.Outcome != OutWouldInstall || f.has("build") || f.has("backup") || f.has("switch") || f.has("restart") {
		t.Fatalf("%+v %s", res, f.order())
	}
}

// ---- Succès ------------------------------------------------------------------------------------------------------

func TestSuccessfulInstallOrder(t *testing.T) {
	f := newFake()
	u := newUp(t, f, nil)
	res := mustRun(t, u, Options{})
	if res.Outcome != OutInstalled || res.Failed() {
		t.Fatalf("%+v", res)
	}
	wantOrder(t, f, "build", "busy", "backup v-old", "switch v-new prev=true", "geometry v-new", "restart", "health v-new/270", "smoke", "finish v-new (précédente v-old)", "notify installed")
	if f.has("stop") || f.has("restore") {
		t.Fatalf("succès : ni arrêt ni restauration : %s", f.order())
	}
	// La sauvegarde est faite AVANT la bascule.
	if idx(f, "backup") > idx(f, "switch") {
		t.Fatalf("sauvegarde après bascule : %s", f.order())
	}
	st := LoadState(u.StatePath)
	if st.LastOK != shaNew {
		t.Fatalf("état : %+v", st)
	}
}

func idx(f *fakeEnv, prefix string) int {
	for i, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

// ---- Échecs avant toute bascule ----------------------------------------------------------------------------------

func TestBuildFailureLeavesRunningServiceUntouched(t *testing.T) {
	f := newFake()
	f.buildErr = errors.New("npm run build : échec")
	res := mustRun(t, newUp(t, f, nil), Options{})
	if res.Outcome != OutBuildFailed {
		t.Fatalf("%+v", res)
	}
	for _, p := range []string{"backup", "switch", "stop", "start", "restart", "restore"} {
		if f.has(p) {
			t.Fatalf("échec de construction : %s ne doit pas être appelé : %s", p, f.order())
		}
	}
	if len(f.notified) != 1 || f.notified[0].Kind != "build_failed" {
		t.Fatalf("%+v", f.notified)
	}
}

func TestBackupFailureAbortsBeforeSwitchAndIsRetried(t *testing.T) {
	f := newFake()
	f.backupErr = errors.New("disque plein")
	u := newUp(t, f, nil)
	res := mustRun(t, u, Options{})
	if res.Outcome != OutAborted || f.has("switch") || f.has("restart") || f.has("stop") {
		t.Fatalf("%+v %s", res, f.order())
	}
	// Transitoire : le commit n'est pas marqué en échec, le passage suivant réessaie.
	f.backupErr = nil
	f.prepared = map[string]Release{shaNew: f.buildRel}
	if res := mustRun(t, u, Options{}); res.Outcome != OutInstalled {
		t.Fatalf("réessai : %+v", res)
	}
}

func TestDowngradeBelowDatabaseSchemaIsRefused(t *testing.T) {
	f := newFake()
	f.dbSchema = []int{300}
	f.buildRel.Schema = 270
	res := mustRun(t, newUp(t, f, nil), Options{})
	if res.Outcome != OutRefused || f.has("backup") || f.has("switch") || f.has("restart") {
		t.Fatalf("%+v %s", res, f.order())
	}
}

func TestUnreadableDBSchemaAborts(t *testing.T) {
	f := newFake()
	f.dbErr = errors.New("base verrouillée")
	res := mustRun(t, newUp(t, f, nil), Options{})
	if res.Outcome != OutAborted || f.has("switch") || f.has("restart") {
		t.Fatalf("%+v", res)
	}
}

// ---- Retour arrière -----------------------------------------------------------------------------------------------

// Scénario demandé : /health en échec, schéma inchangé → relien + redémarrage, base intacte (aucune restauration).
func TestRollbackWhenHealthFailsSameSchema(t *testing.T) {
	f := newFake()
	f.healthErrs = map[string]error{"v-new/270": errors.New("HTTP 503")}
	u := newUp(t, f, nil)
	res := mustRun(t, u, Options{})
	if res.Outcome != OutRolledBack || !res.Failed() {
		t.Fatalf("%+v", res)
	}
	wantOrder(t, f, "backup v-old", "switch v-new prev=true", "restart", "health v-new/270", "stop", "dbschema", "switch v-old prev=false", "geometry-revert", "start", "health v-old/270", "notify rolled_back")
	if f.has("restore") {
		t.Fatalf("schéma inchangé : aucune restauration : %s", f.order())
	}
	if f.has("smoke") || f.has("finish") {
		t.Fatalf("pas de smoke ni de ménage après un échec : %s", f.order())
	}
	if f.switched[len(f.switched)-1] != "v-old" {
		t.Fatalf("le lien doit revenir à l'ancienne version : %v", f.switched)
	}
	if _, bad := LoadState(u.StatePath).Failed[shaNew]; !bad {
		t.Fatal("le commit doit être marqué en échec (pas de redémarrage en boucle)")
	}
}

// Scénario demandé : la nouvelle version a MIGRÉ la base (schéma 270 → 280) puis échoue : restaurer la sauvegarde
// AVANT de relancer l'ancienne version.
func TestRollbackRestoresBackupWhenSchemaMigrated(t *testing.T) {
	f := newFake()
	f.buildRel.Schema = 280
	f.dbSchema = []int{270, 280} // avant la mise à jour, puis après l'échec : la base a été migrée
	f.healthErrs = map[string]error{"v-new/280": errors.New("le service est retombé")}
	res := mustRun(t, newUp(t, f, nil), Options{})
	if res.Outcome != OutRestored || !res.Failed() {
		t.Fatalf("%+v", res)
	}
	wantOrder(t, f, "backup v-old", "switch v-new prev=true", "health v-new/280", "stop", "restore with v-old from preupdate-test.tar.gz", "switch v-old prev=false", "start", "health v-old/270", "notify restored")
	if idx(f, "restore") > idx(f, "start") || idx(f, "restore") < idx(f, "stop") {
		t.Fatalf("la restauration doit avoir lieu service arrêté et avant le redémarrage de l'ancienne version : %s", f.order())
	}
	if !strings.Contains(f.notified[len(f.notified)-1].Message, "restaurée") {
		t.Fatalf("notification : %+v", f.notified)
	}
}

// Nouvelle version qui promet un schéma plus récent mais échoue AVANT d'avoir migré : pas de restauration inutile
// (les données écrites entre-temps sont conservées).
func TestRollbackSkipsRestoreWhenMigrationNeverHappened(t *testing.T) {
	f := newFake()
	f.buildRel.Schema = 280
	f.dbSchema = []int{270} // inchangé après l'échec
	f.restartErr = errors.New("systemctl restart : échec")
	res := mustRun(t, newUp(t, f, nil), Options{})
	if res.Outcome != OutRolledBack || f.has("restore") {
		t.Fatalf("%+v %s", res, f.order())
	}
}

func TestRollbackAssumesMigrationWhenSchemaUnreadable(t *testing.T) {
	f := newFake()
	f.buildRel.Schema = 280
	// 1er appel (avant) OK ; les suivants échouent : simulé par un dbSchema à une seule valeur puis erreur.
	calls := 0
	g := &flakyDB{fakeEnv: f, okCalls: 1, calls: &calls}
	f.healthErrs = map[string]error{"v-new/280": errors.New("HTTP 500")}
	u := &Upgrader{Cfg: Config{Branch: "main", SmokeMode: SmokeRequired}, Env: g, StatePath: filepath.Join(t.TempDir(), "s.json")}
	res := mustRun(t, u, Options{})
	if res.Outcome != OutRestored || !f.has("restore") {
		t.Fatalf("par prudence, base illisible + schéma plus récent = restauration : %+v %s", res, f.order())
	}
}

type flakyDB struct {
	*fakeEnv
	okCalls int
	calls   *int
}

func (g *flakyDB) DBSchema(ctx context.Context) (int, error) {
	*g.calls++
	if *g.calls > g.okCalls {
		g.fakeEnv.rec("dbschema")
		return 0, errors.New("base illisible")
	}
	return g.fakeEnv.DBSchema(ctx)
}

func TestRollbackOnEachActivationFailure(t *testing.T) {
	for name, mod := range map[string]func(*fakeEnv){
		"redémarrage":      func(f *fakeEnv) { f.restartErr = errors.New("restart") },
		"santé":            func(f *fakeEnv) { f.healthErrs = map[string]error{"v-new/270": errors.New("503")} },
		"test de fumée":    func(f *fakeEnv) { f.smokeErr = errors.New("smoke : Bambu Studio indisponible") },
		"worker géométrie": func(f *fakeEnv) { f.syncErr = errors.New("cp") },
	} {
		f := newFake()
		mod(f)
		res := mustRun(t, newUp(t, f, nil), Options{})
		if res.Outcome != OutRolledBack {
			t.Errorf("%s : %+v %s", name, res, f.order())
			continue
		}
		if f.switched[len(f.switched)-1] != "v-old" || !f.has("start") || !f.has("health v-old/270") {
			t.Errorf("%s : retour incomplet : %s", name, f.order())
		}
	}
}

func TestAdvisorySmokeDoesNotRollBack(t *testing.T) {
	f := newFake()
	f.smokeErr = errors.New("CadQuery indisponible")
	res := mustRun(t, newUp(t, f, func(c *Config) { c.SmokeMode = SmokeAdvisory }), Options{})
	if res.Outcome != OutInstalled || f.has("stop") {
		t.Fatalf("%+v %s", res, f.order())
	}
	kinds := []string{}
	for _, n := range f.notified {
		kinds = append(kinds, n.Kind)
	}
	if strings.Join(kinds, ",") != "smoke_warning,installed" {
		t.Fatalf("notifications : %v", kinds)
	}
}

func TestSmokeOffSkipsSmoke(t *testing.T) {
	f := newFake()
	res := mustRun(t, newUp(t, f, func(c *Config) { c.SmokeMode = SmokeOff }), Options{})
	if res.Outcome != OutInstalled || f.has("smoke") {
		t.Fatalf("%+v %s", res, f.order())
	}
}

func TestCriticalWhenRestoreFails(t *testing.T) {
	f := newFake()
	f.buildRel.Schema = 280
	f.dbSchema = []int{270, 280}
	f.healthErrs = map[string]error{"v-new/280": errors.New("503")}
	f.restoreErr = errors.New("archive illisible")
	res := mustRun(t, newUp(t, f, nil), Options{})
	if res.Outcome != OutCritical || !res.Failed() {
		t.Fatalf("%+v", res)
	}
	if f.has("switch v-old") || f.has("start") {
		t.Fatalf("restauration échouée : on ne relance PAS l'ancienne version sur une base incohérente : %s", f.order())
	}
	last := f.notified[len(f.notified)-1]
	if last.Kind != "critical" || !strings.Contains(last.Message, "preupdate-test.tar.gz") {
		t.Fatalf("notification critique : %+v", last)
	}
}

func TestCriticalWhenOldVersionDoesNotComeBack(t *testing.T) {
	f := newFake()
	f.healthErrs = map[string]error{"v-new/270": errors.New("503"), "v-old/270": errors.New("503")}
	res := mustRun(t, newUp(t, f, nil), Options{})
	if res.Outcome != OutCritical {
		t.Fatalf("%+v", res)
	}
}

// Le journal de la notification ne doit contenir ni jeton ni chemin de secret.
func TestNotificationsCarryNoSecrets(t *testing.T) {
	f := newFake()
	f.healthErrs = map[string]error{"v-new/270": errors.New("HTTP 503")}
	mustRun(t, newUp(t, f, nil), Options{})
	for _, n := range f.notified {
		for _, bad := range []string{"BAMBU_API_TOKEN", "Bearer", "ghp_", "master.key"} {
			if strings.Contains(n.Title+n.Message, bad) {
				t.Errorf("secret possible dans la notification : %q", n.Message)
			}
		}
	}
}
