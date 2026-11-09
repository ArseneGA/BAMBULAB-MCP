package upgrade

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Release est une version construite : /opt/atelier/releases/<sha>/{bin,geometry,scripts,REVISION}.
type Release struct {
	SHA          string    `json:"sha"`
	Version      string    `json:"version"` // valeur de core.Version compilée (annoncée par /health)
	Schema       int       `json:"schema"`  // version de schéma que ce binaire sait ouvrir (0 : inconnue)
	GeometryTree string    `json:"geometry_tree,omitempty"`
	BuiltAt      time.Time `json:"built_at"`
	Legacy       bool      `json:"legacy,omitempty"` // copie de l'installation d'avant le schéma releases/current
	Dir          string    `json:"dir,omitempty"`
}

// Name : identifiant court pour les journaux.
func (r Release) Name() string {
	switch {
	case r.SHA != "":
		return short(r.SHA)
	case r.Version != "":
		return r.Version
	}
	return "inconnue"
}

// Event est une notification à émettre (sans secret).
type Event struct {
	Kind    string // installed | deferred | ci_red | build_failed | rolled_back | restored | critical | refused | smoke_warning
	Title   string
	Message string
}

// Env regroupe tous les effets de bord (git, construction, systemctl, HTTP...). sysenv.go en donne la version réelle ;
// les tests en fournissent une simulée qui permet de jouer les scénarios d'échec et de retour arrière sur tout poste.
type Env interface {
	Now() time.Time
	Logf(format string, args ...any)

	Fetch(ctx context.Context) (head string, err error)
	ResolveCommit(ctx context.Context, rev string) (string, error)
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)

	Current(ctx context.Context) (Release, error)
	Prepared(sha string) (Release, bool)

	CI(ctx context.Context, sha string) CIResult
	Busy(ctx context.Context) BusyState
	DBSchema(ctx context.Context) (int, error)

	Build(ctx context.Context, sha string) (Release, error)
	Backup(ctx context.Context, from Release, target Release) (archive string, err error) // sauvegarde + vérification
	Switch(ctx context.Context, to Release, setPrevious bool) error
	SyncGeometry(ctx context.Context, to Release) error
	RevertGeometry(ctx context.Context) error

	Stop(ctx context.Context) error
	Start(ctx context.Context) error
	Restart(ctx context.Context) error
	Health(ctx context.Context, expectVersion string, expectSchema int) error
	Smoke(ctx context.Context) error
	Restore(ctx context.Context, with Release, archive string) error

	Notify(ctx context.Context, e Event)
	Finish(ctx context.Context, installed Release, previous Release) // purge des vieilles versions et sauvegardes, rafraîchit le binaire d'administration
}

// Options d'une exécution.
type Options struct {
	Manual    bool // --now : ignore l'interrupteur et la fenêtre horaire (CI et occupation restent contrôlées)
	Retry     bool // --retry : retente un commit déjà échoué
	CheckOnly bool // --check : décide et explique, ne change rien
}

// Résultats (Outcome).
const (
	OutUpToDate     = "a_jour"
	OutDisabled     = "desactive"
	OutKnownFail    = "deja_echoue"
	OutWaitCI       = "attente_ci"
	OutCIRed        = "ci_rouge"
	OutWindow       = "hors_fenetre"
	OutBusy         = "reporte_occupe"
	OutRefused      = "refuse"
	OutWouldInstall = "installerait" // --check
	OutInstalled    = "installe"
	OutBuildFailed  = "echec_construction"
	OutAborted      = "abandon_avant_bascule" // sauvegarde impossible, base illisible... : rien n'a changé
	OutRolledBack   = "retour_arriere"
	OutRestored     = "retour_arriere_avec_restauration"
	OutCritical     = "echec_critique"
	OutNoNetwork    = "reseau_indisponible"
)

// Result est ce que Run rapporte.
type Result struct {
	Outcome string
	Target  string
	Message string
}

// Failed dit si le résultat doit faire échouer l'unité systemd (visible dans `systemctl --failed`).
func (r Result) Failed() bool {
	switch r.Outcome {
	case OutBuildFailed, OutRolledBack, OutRestored, OutCritical, OutAborted:
		return true
	}
	return false
}

// Upgrader exécute un passage de mise à jour.
type Upgrader struct {
	Cfg       Config
	Env       Env
	StatePath string
}

// Run fait un passage complet. L'appelant tient le verrou (une seule mise à jour à la fois).
func (u *Upgrader) Run(ctx context.Context, o Options) (res Result, err error) {
	env := u.Env
	st := LoadState(u.StatePath)
	st.LastCheck = env.Now()
	defer func() {
		st.LastResult = res.Outcome + " : " + res.Message
		if serr := st.Save(u.StatePath); serr != nil {
			env.Logf("état non enregistré : %v", serr)
		}
	}()

	// Une mise à jour interrompue (reboot, arrêt de l'unité) est TOUJOURS menée à son terme, même si l'automatique a été
	// désactivé depuis : on ne laisse jamais une version non vérifiée en service.
	if st.Pending != nil {
		return u.resume(ctx, st)
	}
	if u.Cfg.Disabled && !o.Manual {
		return Result{Outcome: OutDisabled, Message: "mises à jour automatiques désactivées"}, nil
	}
	head, err := env.Fetch(ctx)
	if err != nil {
		env.Logf("git fetch impossible (réseau ?) : %v", err)
		return Result{Outcome: OutNoNetwork, Message: err.Error()}, nil
	}
	target, pinned := head, u.Cfg.Pin != ""
	if pinned {
		t, rerr := env.ResolveCommit(ctx, u.Cfg.Pin)
		if rerr != nil {
			return u.refuse(ctx, st, u.Cfg.Pin, "version épinglée introuvable : "+rerr.Error()), nil
		}
		if ok, aerr := env.IsAncestor(ctx, t, head); aerr != nil || !ok {
			return u.refuse(ctx, st, t, fmt.Sprintf("la version épinglée %s n'appartient pas à origin/%s : refusée", short(t), u.Cfg.Branch)), nil
		}
		target = t
	}
	cur, cerr := env.Current(ctx)
	if cerr != nil {
		return Result{}, fmt.Errorf("version installée illisible (installation migrée par enable-auto-update.sh ?) : %w", cerr)
	}

	in := Inputs{Now: env.Now(), Disabled: u.Cfg.Disabled, Manual: o.Manual, Retry: o.Retry, Window: u.Cfg.Window,
		Current: cur.SHA, Target: target, Pinned: pinned, Failed: st.Failed}

	// 1. Sorties qui n'exigent aucun appel externe.
	if d := Decide(withCI(in, CIResult{State: CIGreen})); d.Action == ActUpToDate || d.Action == ActDisabled || d.Action == ActKnownFail {
		return Result{Outcome: outcomeOf(d.Action), Target: target, Message: d.Reason}, nil // silencieux : rien à journaliser
	}
	// Un commit qui ne prolonge pas l'installé (réécriture de l'historique de main) n'est jamais installé tout seul.
	if cur.SHA != "" && !pinned {
		if ok, aerr := env.IsAncestor(ctx, cur.SHA, target); aerr == nil && !ok {
			return u.refuse(ctx, st, target, fmt.Sprintf("origin/%s (%s) ne prolonge pas la version installée (%s) : historique réécrit ? "+
				"Rien n'est installé ; épingler une version ou installer à la main si c'est voulu", u.Cfg.Branch, short(target), short(cur.SHA))), nil
		}
	}

	// 2. CI, fenêtre, occupation.
	ci := env.CI(ctx, target)
	in.CI = ci
	d := Decide(in)
	if d.Action == ActInstall {
		in.Busy = env.Busy(ctx)
		d = Decide(in)
	}
	// Un report (CI en cours, hors fenêtre, serveur occupé pendant 25 h d'impression) se répète toutes les 5 minutes :
	// il n'est journalisé qu'une fois par heure et par commit. L'installation, elle, est toujours journalisée.
	if d.Action == ActInstall || o.CheckOnly || st.LogEvery(string(d.Action)+":"+target, time.Hour, env.Now()) {
		env.Logf("%s", d.Reason)
	}
	res = Result{Outcome: outcomeOf(d.Action), Target: target, Message: d.Reason}
	switch d.Action {
	case ActRejectCI:
		if st.Once(target, "ci_red", env.Now()) {
			env.Notify(ctx, Event{Kind: "ci_red", Title: "Mise à jour refusée : CI rouge",
				Message: fmt.Sprintf("Le commit %s de %s a une CI en échec : il n'est pas installé. %s", short(target), u.Cfg.Branch, ci.Detail)})
		}
		return res, nil
	case ActBusy:
		if st.Once(target, "deferred", env.Now()) {
			env.Notify(ctx, Event{Kind: "deferred", Title: "Mise à jour reportée : le serveur est occupé",
				Message: fmt.Sprintf("La version %s est prête mais n'est pas installée : %s. Nouvelle tentative automatique plus tard.", short(target), strings.Join(in.Busy.Reasons, " ; "))})
		}
		return res, nil
	case ActInstall:
	default:
		return res, nil
	}
	if o.CheckOnly {
		return Result{Outcome: OutWouldInstall, Target: target, Message: d.Reason}, nil
	}
	return u.install(ctx, st, cur, target, o)
}

func withCI(in Inputs, ci CIResult) Inputs { in.CI = ci; return in }

func outcomeOf(a Action) string {
	switch a {
	case ActUpToDate:
		return OutUpToDate
	case ActDisabled:
		return OutDisabled
	case ActKnownFail:
		return OutKnownFail
	case ActWaitCI:
		return OutWaitCI
	case ActRejectCI:
		return OutCIRed
	case ActWindow:
		return OutWindow
	case ActBusy:
		return OutBusy
	}
	return OutWouldInstall
}

func (u *Upgrader) refuse(ctx context.Context, st *State, sha, msg string) Result {
	if st.LogEvery("refused:"+sha, time.Hour, u.Env.Now()) {
		u.Env.Logf("REFUSÉ : %s", msg)
	}
	if st.Once(sha, "refused", u.Env.Now()) {
		u.Env.Notify(ctx, Event{Kind: "refused", Title: "Mise à jour refusée", Message: msg})
	}
	return Result{Outcome: OutRefused, Target: sha, Message: msg}
}

// fail enregistre l'échec du commit (il ne sera pas retenté tant qu'un nouveau commit n'arrive pas).
func (u *Upgrader) fail(st *State, sha, reason string) {
	st.Failed[sha] = FailedEntry{At: u.Env.Now(), Reason: reason}
}

func (u *Upgrader) install(ctx context.Context, st *State, cur Release, target string, o Options) (Result, error) {
	env := u.Env

	// a. Construction dans releases/<sha> (déjà prête si un passage précédent l'a faite puis reporté).
	rel, ready := env.Prepared(target)
	if ready {
		env.Logf("version %s déjà construite et testée : réutilisée", short(target))
	} else {
		env.Logf("construction de %s...", short(target))
		var err error
		if rel, err = env.Build(ctx, target); err != nil {
			u.fail(st, target, "construction : "+oneLine(err))
			env.Notify(ctx, Event{Kind: "build_failed", Title: "Mise à jour abandonnée : la construction a échoué",
				Message: fmt.Sprintf("La version %s n'a pas pu être construite ou testée (%s). Le service en marche n'a pas été touché.", short(target), oneLine(err))})
			return Result{Outcome: OutBuildFailed, Target: target, Message: oneLine(err)}, nil
		}
	}

	// b. Schéma de la base : jamais de retour à un binaire plus ancien que la base.
	preSchema, err := env.DBSchema(ctx)
	if err != nil {
		msg := "version de schéma de la base illisible : " + oneLine(err)
		if st.Once(target, "aborted", env.Now()) {
			env.Notify(ctx, Event{Kind: "build_failed", Title: "Mise à jour abandonnée", Message: msg + ". Rien n'a changé."})
		}
		return Result{Outcome: OutAborted, Target: target, Message: msg}, nil
	}
	if rel.Schema > 0 && rel.Schema < preSchema {
		msg := fmt.Sprintf("la version %s ne connaît que le schéma %d alors que la base est en %d : retour en arrière impossible sans restaurer une sauvegarde", short(target), rel.Schema, preSchema)
		u.fail(st, target, msg)
		env.Notify(ctx, Event{Kind: "refused", Title: "Mise à jour refusée", Message: msg})
		return Result{Outcome: OutRefused, Target: target, Message: msg}, nil
	}

	// c. Nouvelle vérification de l'occupation juste avant de toucher au service : une impression a pu démarrer
	// pendant la construction (plusieurs minutes). La version construite est conservée pour le prochain passage.
	b := env.Busy(ctx)
	if len(b.Tolerated) > 0 && !b.Busy && !b.Unknown {
		env.Logf("impression en cours : la mise à jour continue (ATELIER_UPDATE_ALLOW_DURING_PRINT) ; l'impression n'est pas interrompue, le suivi reprend après le redémarrage (%s)", strings.Join(b.Tolerated, " ; "))
	}
	if b.Busy || b.Unknown {
		msg := "service occupé après la construction : " + strings.Join(b.Reasons, " ; ")
		env.Logf("%s", msg)
		if st.Once(target, "deferred", env.Now()) {
			env.Notify(ctx, Event{Kind: "deferred", Title: "Mise à jour reportée : le serveur est occupé",
				Message: fmt.Sprintf("La version %s est construite et testée mais n'est pas installée : %s.", short(target), strings.Join(b.Reasons, " ; "))})
		}
		return Result{Outcome: OutBusy, Target: target, Message: msg}, nil
	}

	// d. Sauvegarde vérifiée (avec empreinte .sha256) : filet pour tout retour arrière qui toucherait au schéma.
	archive, err := env.Backup(ctx, cur, rel)
	if err != nil {
		msg := "sauvegarde avant mise à jour impossible : " + oneLine(err)
		if st.Once(target, "aborted", env.Now()) {
			env.Notify(ctx, Event{Kind: "build_failed", Title: "Mise à jour abandonnée : sauvegarde impossible", Message: msg + ". Rien n'a changé ; nouvelle tentative au prochain passage."})
		}
		return Result{Outcome: OutAborted, Target: target, Message: msg}, nil
	}
	env.Logf("sauvegarde vérifiée : %s (schéma de la base %d, version visée : schéma %d)", archive, preSchema, rel.Schema)

	// e. Journal d'intention PUIS bascule : si la machine redémarre ou si l'unité est tuée entre-temps, le passage suivant
	// sait quoi vérifier et vers quoi revenir.
	st.Pending = &PendingUpdate{Target: rel, Previous: cur, Archive: archive, PreSchema: preSchema, Since: env.Now()}
	if serr := st.Save(u.StatePath); serr != nil {
		st.Pending = nil
		msg := "journal d'intention non écrit (" + oneLine(serr) + ") : mise à jour abandonnée avant la bascule"
		return Result{Outcome: OutAborted, Target: target, Message: msg}, nil
	}
	cause := u.activate(ctx, cur, rel, preSchema)
	return u.conclude(ctx, st, cur, rel, archive, preSchema, cause), nil
}

// conclude clôt une mise à jour après activate (ou après une reprise) : succès, ou retour arrière.
func (u *Upgrader) conclude(ctx context.Context, st *State, cur, rel Release, archive string, preSchema int, cause error) Result {
	env := u.Env
	target := rel.SHA
	if cause == nil {
		st.Pending = nil
		st.LastOK, st.LastOKAt = target, env.Now()
		delete(st.Failed, target)
		env.Finish(ctx, rel, cur)
		env.Notify(ctx, Event{Kind: "installed", Title: "Atelier mis à jour",
			Message: fmt.Sprintf("Version %s installée (avant : %s), santé et test de fumée OK. Sauvegarde d'avant mise à jour : %s.", rel.Name(), cur.Name(), archive)})
		return Result{Outcome: OutInstalled, Target: target, Message: "installé " + rel.Name()}
	}

	// f. Retour arrière.
	env.Logf("ÉCHEC de la mise à jour : %v", cause)
	u.fail(st, target, oneLine(cause))
	restored, rerr := u.rollback(ctx, cur, rel, archive, preSchema)
	if rerr == nil {
		st.Pending = nil
	} // en cas d'échec du retour arrière, Pending reste : le passage suivant retentera
	switch {
	case rerr != nil:
		if st.Once(target, "critical", env.Now()) { // une seule alerte : le passage suivant retente le retour arrière
			env.Notify(ctx, Event{Kind: "critical", Title: "ÉCHEC CRITIQUE : retour arrière impossible, intervention requise",
				Message: fmt.Sprintf("La mise à jour vers %s a échoué (%s) puis le retour arrière a échoué aussi (%s). Voir docs/DEPLOY-LINUX.md, « Que faire si ça casse ». Sauvegarde : %s.",
					rel.Name(), oneLine(cause), oneLine(rerr), archive)})
		}
		return Result{Outcome: OutCritical, Target: target, Message: oneLine(cause) + " ; retour arrière : " + oneLine(rerr)}
	case restored:
		env.Notify(ctx, Event{Kind: "restored", Title: "Mise à jour échouée : ancienne version et données restaurées",
			Message: fmt.Sprintf("La version %s a échoué (%s). Comme elle avait changé le schéma de la base, la sauvegarde d'avant mise à jour a été restaurée et %s relancée. "+
				"Les écritures faites entre la sauvegarde et le retour arrière sont conservées à part dans .pre-restore-*.", rel.Name(), oneLine(cause), cur.Name())})
		return Result{Outcome: OutRestored, Target: target, Message: oneLine(cause)}
	}
	env.Notify(ctx, Event{Kind: "rolled_back", Title: "Mise à jour échouée : retour à l'ancienne version",
		Message: fmt.Sprintf("La version %s a échoué (%s). %s a été relancée ; les données n'ont pas été touchées.", rel.Name(), oneLine(cause), cur.Name())})
	return Result{Outcome: OutRolledBack, Target: target, Message: oneLine(cause)}
}

// resume reprend une mise à jour interrompue (voir State.Pending).
func (u *Upgrader) resume(ctx context.Context, st *State) (Result, error) {
	env := u.Env
	p := st.Pending
	env.Logf("mise à jour interrompue détectée (%s -> %s, commencée le %s) : reprise", p.Previous.Name(), p.Target.Name(), p.Since.Local().Format("2006-01-02 15:04"))
	cur, err := env.Current(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("reprise impossible : %w", err)
	}
	expect := p.PreSchema
	if p.Target.Schema > expect {
		expect = p.Target.Schema
	}
	if sameRelease(cur, p.Target) {
		// Le lien a été basculé : la nouvelle version tourne-t-elle correctement ?
		cause := env.Health(ctx, p.Target.Version, expect)
		if cause == nil && u.Cfg.SmokeMode == SmokeRequired {
			if serr := env.Smoke(ctx); serr != nil {
				cause = fmt.Errorf("test de fumée : %w", serr)
			}
		}
		if cause != nil {
			cause = fmt.Errorf("après interruption : %w", cause)
		}
		return u.conclude(ctx, st, p.Previous, p.Target, p.Archive, p.PreSchema, cause), nil
	}
	// Le lien n'a pas été basculé (ou le retour arrière a eu lieu) : l'ancienne version doit tourner.
	if herr := env.Health(ctx, p.Previous.Version, p.PreSchema); herr != nil {
		res := u.conclude(ctx, st, p.Previous, p.Target, p.Archive, p.PreSchema, fmt.Errorf("l'ancienne version ne répond pas après l'interruption : %w", herr))
		return res, nil
	}
	st.Pending = nil
	msg := "mise à jour interrompue avant la bascule : l'ancienne version " + p.Previous.Name() + " tourne normalement"
	env.Logf("%s", msg)
	return Result{Outcome: OutUpToDate, Target: p.Target.SHA, Message: msg}, nil
}

func sameRelease(a, b Release) bool {
	if a.SHA != "" && a.SHA == b.SHA {
		return true
	}
	return a.Dir != "" && a.Dir == b.Dir
}

// activate bascule vers rel et vérifie. Une erreur déclenche le retour arrière (le lien `current` a pu changer).
func (u *Upgrader) activate(ctx context.Context, cur, rel Release, preSchema int) error {
	env := u.Env
	expectSchema := preSchema
	if rel.Schema > expectSchema {
		expectSchema = rel.Schema
	}
	if err := env.Switch(ctx, rel, true); err != nil {
		return fmt.Errorf("bascule du lien current : %w", err)
	}
	if err := env.SyncGeometry(ctx, rel); err != nil {
		return fmt.Errorf("worker géométrie : %w", err)
	}
	env.Logf("redémarrage du service sur %s...", rel.Name())
	if err := env.Restart(ctx); err != nil {
		return fmt.Errorf("redémarrage : %w", err)
	}
	if err := env.Health(ctx, rel.Version, expectSchema); err != nil {
		return fmt.Errorf("contrôle de santé : %w", err)
	}
	switch u.Cfg.SmokeMode {
	case SmokeOff:
	case SmokeAdvisory:
		if err := env.Smoke(ctx); err != nil {
			env.Logf("test de fumée en échec (mode advisory, pas de retour arrière) : %v", err)
			env.Notify(ctx, Event{Kind: "smoke_warning", Title: "Mise à jour installée, mais le test de fumée échoue",
				Message: fmt.Sprintf("La version %s répond mais scripts/smoke-linux.sh signale : %s", rel.Name(), oneLine(err))})
		}
	default:
		if err := env.Smoke(ctx); err != nil {
			return fmt.Errorf("test de fumée : %w", err)
		}
	}
	return nil
}

// rollback remet l'ancienne version. Si la base a été migrée par la nouvelle version, l'ancien binaire refuserait de
// démarrer (il refuse une base plus récente que lui) : la sauvegarde d'avant mise à jour est alors restaurée AVANT de
// relancer l'ancienne version. restored dit si une restauration a eu lieu.
func (u *Upgrader) rollback(ctx context.Context, cur, rel Release, archive string, preSchema int) (restored bool, err error) {
	env := u.Env
	env.Logf("RETOUR ARRIÈRE vers %s", cur.Name())
	if serr := env.Stop(ctx); serr != nil {
		env.Logf("arrêt du service : %v (on continue)", serr)
	}
	dbSchema, derr := env.DBSchema(ctx)
	migrated := dbSchema > preSchema
	if derr != nil {
		migrated = rel.Schema > preSchema // illisible : par prudence, supposer la migration faite
		env.Logf("schéma de la base illisible après l'échec (%v) : migration supposée = %v", derr, migrated)
	}
	if migrated {
		env.Logf("la base est en schéma %d (avant : %d) : restauration de %s", dbSchema, preSchema, archive)
		if rerr := env.Restore(ctx, cur, archive); rerr != nil {
			return false, fmt.Errorf("restauration de la sauvegarde : %w", rerr)
		}
		restored = true
	}
	if serr := env.Switch(ctx, cur, false); serr != nil {
		return restored, fmt.Errorf("retour du lien current : %w", serr)
	}
	if gerr := env.RevertGeometry(ctx); gerr != nil {
		env.Logf("retour du worker géométrie : %v (non bloquant : uv resynchronise l'environnement)", gerr)
	}
	if serr := env.Start(ctx); serr != nil {
		return restored, fmt.Errorf("redémarrage de l'ancienne version : %w", serr)
	}
	expect := preSchema
	if herr := env.Health(ctx, cur.Version, expect); herr != nil {
		return restored, fmt.Errorf("santé de l'ancienne version : %w", herr)
	}
	env.Logf("ancienne version %s de nouveau en service", cur.Name())
	return restored, nil
}

func oneLine(err error) string {
	if err == nil {
		return ""
	}
	s := strings.Join(strings.Fields(err.Error()), " ")
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	return s
}

// ErrNotInstalled : le schéma releases/current n'est pas en place.
var ErrNotInstalled = errors.New("schéma releases/current absent")
