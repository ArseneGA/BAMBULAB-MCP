package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SysEnv est l'environnement réel (Linux, root). Chaque effet de bord passe par Runner : les tests le remplacent par un
// enregistreur, et les opérations qui n'ont besoin que du système de fichiers sont testées sur des dossiers temporaires.
//
// Séparation des privilèges : git, npm, go et les tests s'exécutent sous l'utilisateur dédié `atelier-update`
// (environnement vidé, aucun jeton, aucun accès aux données) ; la sauvegarde, la restauration et les notifications sous
// `atelier` (propriétaire des données) ; seuls la bascule du lien, systemctl et le ménage sont faits en root.
type SysEnv struct {
	Cfg  Config
	Run  Runner
	HTTP *http.Client
	Self string // chemin de ce binaire (sous-commandes upgrade-probe et upgrade-notify)
	Out  io.Writer

	clock       func() time.Time
	Sleep       func(ctx context.Context, d time.Duration) error // injectable (tests) ; défaut : attente réelle
	geomChanged bool
	geomOldTree string
	geomSynced  bool // uv sync du worker réussi dans cette mise à jour (à refaire au retour arrière)
}

// NewSysEnv construit l'environnement réel.
func NewSysEnv(cfg Config, self string) *SysEnv {
	return &SysEnv{Cfg: cfg, Run: ExecRunner{SwitchUser: true}, HTTP: &http.Client{Timeout: 15 * time.Second}, Self: self, Out: os.Stderr, clock: time.Now}
}

func (e *SysEnv) Now() time.Time {
	if e.clock != nil {
		return e.clock()
	}
	return time.Now()
}

func (e *SysEnv) Logf(format string, args ...any) {
	out := e.Out
	if out == nil {
		out = os.Stderr
	}
	fmt.Fprintf(out, format+"\n", args...)
}

// ---- Environnements des commandes -------------------------------------------------------------------------------

const defaultPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

func (e *SysEnv) buildPath() string {
	if e.Cfg.BuildPath != "" {
		return e.Cfg.BuildPath + ":" + defaultPath
	}
	return defaultPath
}

// buildEnv : environnement vidé de l'utilisateur de construction (npm, go, tests, validateur). Pas de BAMBU_*, pas de jeton.
// Il ne porte volontairement PAS GIT_ALLOW_PROTOCOL : les tests de l'outil clonent des dépôts locaux en file:// et
// échoueraient (« git clone ... exit status 128 ») ; cette restriction ne concerne que les commandes git de l'outil (gitEnv).
func (e *SysEnv) buildEnv(extra ...string) []string {
	env := []string{"HOME=" + e.Cfg.CacheDir, "PATH=" + e.buildPath(), "LANG=C.UTF-8", "GIT_TERMINAL_PROMPT=0", "CI=1",
		"npm_config_update_notifier=false", "npm_config_fund=false"}
	return append(env, extra...)
}

// gitEnv : buildEnv + restriction des protocoles git (https seul, file:// seulement si le dépôt suivi est local). Réservé aux
// commandes git lancées par l'outil lui-même.
func (e *SysEnv) gitEnv(extra ...string) []string {
	proto := "GIT_ALLOW_PROTOCOL=https"
	if strings.HasPrefix(e.Cfg.Repo, "file://") {
		proto = "GIT_ALLOW_PROTOCOL=file:https"
	}
	return e.buildEnv(append([]string{proto}, extra...)...)
}

func (e *SysEnv) appEnv(extra ...string) []string {
	home := filepath.Join(e.Cfg.DataDir, "home")
	env := []string{"HOME=" + home, "PATH=" + defaultPath, "BAMBU_DATA_DIR=" + e.Cfg.DataDir}
	if e.Cfg.PublicURL != "" {
		env = append(env, "BAMBU_PUBLIC_URL="+e.Cfg.PublicURL)
	}
	return append(env, extra...)
}

func (e *SysEnv) asBuilder(ctx context.Context, dir string, timeout time.Duration, env []string, name string, args ...string) (string, error) {
	return e.Run.Run(ctx, Cmd{Name: name, Args: args, Dir: dir, Env: env, User: e.Cfg.BuildUser, Timeout: timeout})
}

func (e *SysEnv) asApp(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	return e.Run.Run(ctx, Cmd{Name: name, Args: args, Env: e.appEnv(), User: e.Cfg.AppUser, Timeout: timeout})
}

func (e *SysEnv) asRoot(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	return e.Run.Run(ctx, Cmd{Name: name, Args: args, Timeout: timeout})
}

// ---- Git --------------------------------------------------------------------------------------------------------

func (e *SysEnv) git(ctx context.Context, args ...string) (string, error) {
	all := append([]string{"-C", e.Cfg.RepoDir()}, args...)
	return e.asBuilder(ctx, e.Cfg.CacheDir, 10*time.Minute, e.gitEnv(), "git", all...)
}

// Fetch met à jour le clone local et renvoie le commit de tête de origin/<branche>.
func (e *SysEnv) Fetch(ctx context.Context) (string, error) {
	repo := e.Cfg.RepoDir()
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		if _, err := os.Stat(filepath.Join(repo, "HEAD")); err != nil { // clone absent
			if _, err := e.asBuilder(ctx, e.Cfg.CacheDir, 15*time.Minute, e.gitEnv(), "git", "clone", "--quiet", "--no-checkout", e.Cfg.Repo, repo); err != nil {
				return "", fmt.Errorf("clone de %s : %w", e.Cfg.Repo, err)
			}
		}
	}
	if _, err := e.git(ctx, "remote", "set-url", "origin", e.Cfg.Repo); err != nil {
		return "", err
	}
	ref := "refs/remotes/origin/" + e.Cfg.Branch
	if _, err := e.git(ctx, "fetch", "--quiet", "--prune", "--no-tags", "origin", "+refs/heads/"+e.Cfg.Branch+":"+ref); err != nil {
		return "", fmt.Errorf("fetch de origin/%s : %w", e.Cfg.Branch, err)
	}
	return e.ResolveCommit(ctx, ref)
}

// ResolveCommit renvoie le sha complet (40 hexadécimaux) d'une révision.
func (e *SysEnv) ResolveCommit(ctx context.Context, rev string) (string, error) {
	if strings.HasPrefix(rev, "-") {
		return "", fmt.Errorf("révision %q invalide", rev)
	}
	out, err := e.git(ctx, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("révision %q introuvable", rev)
	}
	sha := strings.TrimSpace(out)
	if len(sha) != 40 || strings.Trim(sha, "0123456789abcdef") != "" {
		return "", fmt.Errorf("révision %q : sha illisible (%q)", rev, truncate(sha, 60))
	}
	return sha, nil
}

// IsAncestor dit si ancestor est un ancêtre de descendant (ou lui-même).
func (e *SysEnv) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	_, err := e.git(ctx, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	// merge-base renvoie 1 si ce n'est pas un ancêtre ; d'autres codes sont de vraies erreurs.
	if strings.Contains(err.Error(), "exit status 1") {
		return false, nil
	}
	return false, err
}

// ---- Versions installées ----------------------------------------------------------------------------------------

func readRevision(dir string) (Release, error) {
	var r Release
	b, err := os.ReadFile(filepath.Join(dir, "REVISION"))
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("REVISION illisible dans %s : %w", dir, err)
	}
	r.Dir = dir
	return r, nil
}

// Current lit le lien `current`.
func (e *SysEnv) Current(ctx context.Context) (Release, error) {
	link := e.Cfg.CurrentLink()
	target, err := os.Readlink(link)
	if err != nil {
		return Release{}, fmt.Errorf("%w : %s n'est pas un lien symbolique (%v)", ErrNotInstalled, link, err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(e.Cfg.Root, target)
	}
	r, err := readRevision(target)
	if err != nil {
		return Release{}, fmt.Errorf("%w : %v", ErrNotInstalled, err)
	}
	return r, nil
}

// Prepared renvoie la version déjà construite et testée pour ce sha (marqueur .ready).
func (e *SysEnv) Prepared(sha string) (Release, bool) {
	dir := filepath.Join(e.Cfg.ReleasesDir(), sha)
	if _, err := os.Stat(filepath.Join(dir, ".ready")); err != nil {
		return Release{}, false
	}
	r, err := readRevision(dir)
	if err != nil || r.SHA != sha {
		return Release{}, false
	}
	return r, true
}

// ---- CI, occupation, base -----------------------------------------------------------------------------------------

func (e *SysEnv) CI(ctx context.Context, sha string) CIResult {
	owner, name, err := ParseRepoURL(e.Cfg.Repo)
	if err != nil {
		return CIResult{CIUnknown, err.Error()}
	}
	c := &CIChecker{BaseURL: e.Cfg.GitHubAPI, Owner: owner, Repo: name, Token: e.Cfg.GitHubToken, RequiredChecks: e.Cfg.RequiredChecks}
	return c.Status(ctx, sha)
}

// Busy interroge le service (GET /api/system/busy, boucle locale, jeton). S'il ne répond pas correctement (arrêté, ou
// version trop ancienne pour connaître la route), repli sur la lecture de la base ; si elle échoue aussi : inconnu,
// traité comme occupé.
func (e *SysEnv) Busy(ctx context.Context) BusyState {
	return ApplyBusyPolicy(e.rawBusy(ctx), e.Cfg.AllowDuringPrint)
}

func (e *SysEnv) rawBusy(ctx context.Context) BusyState {
	st, err := e.busyHTTP(ctx)
	if err == nil {
		return st
	}
	e.Logf("occupation : %v ; repli sur la base de données", err)
	out, perr := e.asApp(ctx, 2*time.Minute, e.Self, "upgrade-probe", "--data-dir", e.Cfg.DataDir)
	if perr != nil {
		return BusyState{Unknown: true, Reasons: []string{oneLine(err), oneLine(perr)}}
	}
	p, perr := ParseProbe(out)
	if perr != nil {
		return BusyState{Unknown: true, Reasons: []string{oneLine(err), oneLine(perr)}}
	}
	reasons := append([]string(nil), p.Reasons...)
	if p.Busy {
		reasons = append(reasons, "(service injoignable ou ancien : lecture de la base seule)")
	}
	return BusyState{Busy: p.Busy, Reasons: reasons}
}

func (e *SysEnv) busyHTTP(ctx context.Context) (BusyState, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.Cfg.BaseURL+"/api/system/busy", nil)
	if err != nil {
		return BusyState{}, err
	}
	if e.Cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+e.Cfg.APIToken)
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return BusyState{}, fmt.Errorf("service injoignable (%s)", scrub(err.Error(), e.Cfg.APIToken))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return BusyState{}, fmt.Errorf("/api/system/busy : HTTP %d", resp.StatusCode)
	}
	var rep struct {
		Busy    bool `json:"busy"`
		Reasons []struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		} `json:"reasons"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rep); err != nil {
		return BusyState{}, fmt.Errorf("/api/system/busy illisible : %w", err)
	}
	st := BusyState{Busy: rep.Busy}
	for _, r := range rep.Reasons {
		st.Reasons = append(st.Reasons, r.Code+": "+r.Detail)
	}
	return st, nil
}

// DBSchema lit la version de schéma de la base (lecture seule, sous l'utilisateur du service).
func (e *SysEnv) DBSchema(ctx context.Context) (int, error) {
	out, err := e.asApp(ctx, 2*time.Minute, e.Self, "upgrade-probe", "--data-dir", e.Cfg.DataDir)
	if err != nil {
		return 0, err
	}
	p, err := ParseProbe(out)
	if err != nil {
		return 0, err
	}
	return p.SchemaVersion, nil
}

// ---- Sauvegarde, restauration -----------------------------------------------------------------------------------

// Backup sauvegarde avec le binaire EN SERVICE (celui qui correspond au schéma de la base), vérifie l'archive et son
// empreinte .sha256.
func (e *SysEnv) Backup(ctx context.Context, from Release, target Release) (string, error) {
	out := filepath.Join(e.Cfg.BackupDir, fmt.Sprintf("preupdate-%s-%s.tar.gz", e.Now().UTC().Format("20060102T150405Z"), short(target.SHA)))
	bin := filepath.Join(from.Dir, "bin", "bambud")
	if _, err := e.asApp(ctx, 45*time.Minute, bin, "backup", "--data-dir", e.Cfg.DataDir, "--out", out); err != nil {
		return "", err
	}
	if _, err := e.asApp(ctx, 45*time.Minute, bin, "verify", "--archive", out); err != nil {
		return "", fmt.Errorf("vérification de la sauvegarde : %w", err)
	}
	if _, err := os.Stat(out + ".sha256"); err != nil {
		return "", fmt.Errorf("empreinte %s.sha256 absente", filepath.Base(out))
	}
	return out, nil
}

// Restore restaure la sauvegarde avec l'ANCIEN binaire (le service doit être arrêté), puis contrôle la base.
func (e *SysEnv) Restore(ctx context.Context, with Release, archive string) error {
	bin := filepath.Join(with.Dir, "bin", "bambud")
	if _, err := e.asApp(ctx, 45*time.Minute, bin, "restore", "--from", archive, "--data-dir", e.Cfg.DataDir, "--force"); err != nil {
		return err
	}
	if _, err := e.asApp(ctx, 30*time.Minute, bin, "verify", "--data-dir", e.Cfg.DataDir, "--quick"); err != nil {
		return fmt.Errorf("contrôle après restauration : %w", err)
	}
	return nil
}

// ---- Bascule ---------------------------------------------------------------------------------------------------

func atomicSymlink(link, target string) error {
	tmp := link + ".tmp-" + strconv.Itoa(os.Getpid())
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (e *SysEnv) rel(dir string) string {
	if r, err := filepath.Rel(e.Cfg.Root, dir); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return dir
}

// Switch repointe `current` (rename atomique d'un lien : le service ne voit jamais un état intermédiaire). Avec
// setPrevious, `previous` reçoit l'ancienne cible.
func (e *SysEnv) Switch(ctx context.Context, to Release, setPrevious bool) error {
	if to.Dir == "" {
		return fmt.Errorf("version sans dossier")
	}
	if _, err := os.Stat(filepath.Join(to.Dir, "bin", "bambud")); err != nil {
		return fmt.Errorf("binaire absent de %s : %w", to.Dir, err)
	}
	if setPrevious {
		if old, err := os.Readlink(e.Cfg.CurrentLink()); err == nil && filepath.Join(e.Cfg.Root, old) != to.Dir && old != to.Dir {
			if err := atomicSymlink(e.Cfg.PreviousLink(), old); err != nil {
				return fmt.Errorf("lien previous : %w", err)
			}
		}
	}
	return atomicSymlink(e.Cfg.CurrentLink(), e.rel(to.Dir))
}

// ---- Worker géométrie --------------------------------------------------------------------------------------------

func (e *SysEnv) geomMarker() string {
	return filepath.Join(filepath.Dir(e.Cfg.CadDir), ".geometry-tree")
}

// SyncGeometry remplace le code du worker CAO (hors environnement .venv) seulement si l'arbre workers/geometry a changé.
// L'ancien code est gardé dans geometry.prev jusqu'à la fin de la mise à jour (RevertGeometry le remet).
func (e *SysEnv) SyncGeometry(ctx context.Context, to Release) error {
	e.geomChanged = false
	cad := e.Cfg.CadDir
	if cad == "" || to.GeometryTree == "" {
		return nil
	}
	src := filepath.Join(to.Dir, "geometry")
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	old, _ := os.ReadFile(e.geomMarker())
	oldTree := strings.TrimSpace(string(old))
	if oldTree == to.GeometryTree {
		return nil
	}
	parent := filepath.Dir(cad)
	newDir, prev := filepath.Join(parent, "geometry.new"), filepath.Join(parent, "geometry.prev")
	_ = os.RemoveAll(newDir)
	_ = os.RemoveAll(prev)
	if _, err := e.asRoot(ctx, 5*time.Minute, "cp", "-a", src, newDir); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(cad, ".venv")); err == nil {
		if err := os.Rename(filepath.Join(cad, ".venv"), filepath.Join(newDir, ".venv")); err != nil {
			return fmt.Errorf("déplacement de .venv : %w", err)
		}
	}
	if err := os.Rename(cad, prev); err != nil {
		return err
	}
	if err := os.Rename(newDir, cad); err != nil {
		_ = os.Rename(prev, cad)
		return err
	}
	e.geomChanged, e.geomOldTree = true, oldTree
	_ = os.WriteFile(e.geomMarker(), []byte(to.GeometryTree+"\n"), 0o644)
	if _, err := e.asRoot(ctx, 5*time.Minute, "chown", "-R", e.Cfg.AppUser+":"+e.Cfg.AppUser, cad); err != nil {
		e.Logf("chown du worker géométrie : %v", err)
	}
	if uv := e.Cfg.AppEnv["BAMBU_UV_PATH"]; uv != "" {
		if err := e.syncWorker(ctx, uv, cad); err != nil {
			e.Logf("uv sync du worker géométrie a échoué (non bloquant ici, le test de fumée tranchera) : %v", err)
		} else {
			e.geomSynced = true
		}
	}
	return nil
}

// RevertGeometry remet le code du worker d'avant SyncGeometry.
func (e *SysEnv) RevertGeometry(ctx context.Context) error {
	if !e.geomChanged {
		return nil
	}
	cad := e.Cfg.CadDir
	parent := filepath.Dir(cad)
	prev, failed := filepath.Join(parent, "geometry.prev"), filepath.Join(parent, "geometry.failed")
	if _, err := os.Stat(prev); err != nil {
		return fmt.Errorf("ancien worker géométrie absent : %w", err)
	}
	if _, err := os.Stat(filepath.Join(cad, ".venv")); err == nil {
		_ = os.Rename(filepath.Join(cad, ".venv"), filepath.Join(prev, ".venv"))
	}
	_ = os.RemoveAll(failed)
	if err := os.Rename(cad, failed); err != nil {
		return err
	}
	if err := os.Rename(prev, cad); err != nil {
		return err
	}
	_ = os.RemoveAll(failed)
	_ = os.WriteFile(e.geomMarker(), []byte(e.geomOldTree+"\n"), 0o644)
	e.geomChanged = false
	if uv := e.Cfg.AppEnv["BAMBU_UV_PATH"]; uv != "" && e.geomSynced {
		// Le paquet bambu-geometry est installé NON éditable dans un environnement hors du dossier du worker : il faut le
		// réinstaller depuis l'ancien code, sinon l'ancienne version tournerait avec le nouveau paquet.
		if err := e.syncWorker(ctx, uv, cad); err != nil {
			e.Logf("réinstallation de l'ancien worker géométrie : %v (le prochain démarrage de uv resynchronisera)", err)
		}
		e.geomSynced = false
	}
	return nil
}

// ---- Service -----------------------------------------------------------------------------------------------------

func (e *SysEnv) Stop(ctx context.Context) error {
	_, err := e.asRoot(ctx, 3*time.Minute, "systemctl", "stop", e.Cfg.Service)
	return err
}
func (e *SysEnv) Start(ctx context.Context) error {
	_, err := e.asRoot(ctx, 3*time.Minute, "systemctl", "start", e.Cfg.Service)
	return err
}
func (e *SysEnv) Restart(ctx context.Context) error {
	_, err := e.asRoot(ctx, 3*time.Minute, "systemctl", "restart", e.Cfg.Service)
	return err
}

// Health attend que /health réponde 200 avec status ok, la version et le schéma attendus, puis vérifie encore après un
// délai de stabilité (le service ne doit pas retomber : Restart=on-failure masquerait un démarrage en boucle).
func (e *SysEnv) Health(ctx context.Context, expectVersion string, expectSchema int) error {
	deadline := e.Now().Add(e.Cfg.HealthWait)
	var last error
	for {
		if last = e.healthOnce(ctx, expectVersion, expectSchema); last == nil {
			break
		}
		if e.Now().After(deadline) {
			return fmt.Errorf("pas de /health conforme après %s : %w", e.Cfg.HealthWait, last)
		}
		if err := e.sleep(ctx, 2*time.Second); err != nil {
			return err
		}
	}
	if e.Cfg.StableWait > 0 {
		if err := e.sleep(ctx, e.Cfg.StableWait); err != nil {
			return err
		}
		if _, err := e.asRoot(ctx, time.Minute, "systemctl", "is-active", "--quiet", e.Cfg.Service); err != nil {
			return fmt.Errorf("le service est retombé après son démarrage : %w", err)
		}
		if err := e.healthOnce(ctx, expectVersion, expectSchema); err != nil {
			return fmt.Errorf("/health non conforme après %s de stabilité : %w", e.Cfg.StableWait, err)
		}
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (e *SysEnv) healthOnce(ctx context.Context, expectVersion string, expectSchema int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.Cfg.BaseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("injoignable : %w", err)
	}
	defer resp.Body.Close()
	var h struct {
		Status  string `json:"status"`
		Version string `json:"version"`
		Schema  int    `json:"schema_version"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, &h); err != nil {
		return fmt.Errorf("réponse illisible : %w", err)
	}
	switch {
	case h.Status != "ok":
		return fmt.Errorf("status %q", h.Status)
	case expectVersion != "" && h.Version != expectVersion:
		return fmt.Errorf("version annoncée %q, attendue %q", h.Version, expectVersion)
	case expectSchema > 0 && h.Schema != expectSchema:
		return fmt.Errorf("schema_version %d, attendu %d", h.Schema, expectSchema)
	}
	return nil
}

// Smoke joue scripts/smoke-linux.sh de la version installée. Ce script vient du dépôt : il s'exécute sous l'utilisateur du
// SERVICE (jamais en root). Le jeton d'API lui est remis par un fichier temporaire 0600 appartenant à cet utilisateur,
// dans le dossier de données, supprimé aussitôt après.
func (e *SysEnv) Smoke(ctx context.Context) (err error) {
	script := filepath.Join(e.Cfg.CurrentLink(), "scripts", "smoke-linux.sh")
	if _, serr := os.Stat(script); serr != nil {
		e.Logf("scripts/smoke-linux.sh absent de la version installée : test de fumée ignoré")
		return nil
	}
	args := []string{script, "--url", e.Cfg.BaseURL}
	if e.Cfg.APIToken != "" {
		tf := filepath.Join(e.Cfg.DataDir, ".update-smoke-token")
		if werr := os.WriteFile(tf, []byte("BAMBU_API_TOKEN="+e.Cfg.APIToken+"\n"), 0o600); werr != nil {
			return fmt.Errorf("fichier de jeton du test de fumée : %w", werr)
		}
		defer os.Remove(tf)
		if _, cerr := e.asRoot(ctx, time.Minute, "chown", e.Cfg.AppUser+":"+e.Cfg.AppUser, tf); cerr != nil {
			return cerr
		}
		args = append(args, "--token-file", tf)
	}
	if !e.Cfg.UsesCAD {
		args = append(args, "--no-cad")
	}
	if _, err = e.asApp(ctx, 8*time.Minute, "bash", args...); err != nil {
		return err
	}
	if e.Cfg.UsesCAD {
		return e.checkWorkerVersion(ctx)
	}
	return nil
}

// ---- Notification ------------------------------------------------------------------------------------------------

// Notify journalise l'évènement puis l'envoie par le système de notifications de bambud (canaux ntfy / webhook / e-mail
// de l'humain), sous l'utilisateur du service. Un échec d'envoi n'empêche jamais la mise à jour.
func (e *SysEnv) Notify(ctx context.Context, ev Event) {
	e.Logf("NOTIFICATION [%s] %s — %s", ev.Kind, ev.Title, ev.Message)
	nctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if _, err := e.asApp(nctx, 3*time.Minute, e.Self, "upgrade-notify", "--data-dir", e.Cfg.DataDir, "--kind", ev.Kind, "--title", ev.Title, "--message", ev.Message); err != nil {
		e.Logf("notification non envoyée : %v", err)
	}
}

// ---- Ménage ------------------------------------------------------------------------------------------------------

// Finish : copie le binaire du nouvel outil d'administration, purge les anciennes versions et sauvegardes.
func (e *SysEnv) Finish(ctx context.Context, installed Release, previous Release) {
	// L'outil de mise à jour est exécuté en ROOT par le timer : par défaut il n'est jamais remplacé par du code qui vient
	// d'être téléchargé (seul l'administrateur le rafraîchit : enable-auto-update.sh / install.sh). Option explicite.
	if e.Cfg.RefreshUpdater {
		if err := e.refreshUpdater(installed); err != nil {
			e.Logf("outil de mise à jour non rafraîchi : %v", err)
		}
	}
	if err := e.pruneReleases(installed, previous); err != nil {
		e.Logf("purge des anciennes versions : %v", err)
	}
	if err := e.pruneBackups(); err != nil {
		e.Logf("purge des anciennes sauvegardes : %v", err)
	}
}

func (e *SysEnv) refreshUpdater(r Release) error {
	src := filepath.Join(r.Dir, "bin", "bambud")
	dst := e.Cfg.UpdaterBin()
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if old, err := os.ReadFile(dst); err == nil && string(old) == string(b) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	if err := os.WriteFile(tmp, b, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func (e *SysEnv) pruneReleases(installed, previous Release) error {
	ents, err := os.ReadDir(e.Cfg.ReleasesDir())
	if err != nil {
		return err
	}
	type item struct {
		dir string
		at  time.Time
	}
	var all []item
	for _, en := range ents {
		if !en.IsDir() || strings.HasPrefix(en.Name(), ".") {
			continue
		}
		dir := filepath.Join(e.Cfg.ReleasesDir(), en.Name())
		at := time.Time{}
		if r, err := readRevision(dir); err == nil {
			at = r.BuiltAt
		}
		if at.IsZero() {
			if fi, err := en.Info(); err == nil {
				at = fi.ModTime()
			}
		}
		all = append(all, item{dir, at})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.After(all[j].at) })
	keep := map[string]bool{installed.Dir: true, previous.Dir: true}
	for i, it := range all {
		if i < e.Cfg.KeepReleases {
			keep[it.dir] = true
		}
		if _, err := os.Stat(filepath.Join(it.dir, ".keep")); err == nil {
			keep[it.dir] = true
		}
	}
	for _, link := range []string{e.Cfg.CurrentLink(), e.Cfg.PreviousLink()} {
		if t, err := os.Readlink(link); err == nil {
			if !filepath.IsAbs(t) {
				t = filepath.Join(e.Cfg.Root, t)
			}
			keep[t] = true
		}
	}
	for _, it := range all {
		if keep[it.dir] {
			continue
		}
		e.Logf("ancienne version supprimée : %s", filepath.Base(it.dir))
		if err := os.RemoveAll(it.dir); err != nil {
			return err
		}
	}
	return nil
}

func (e *SysEnv) pruneBackups() error {
	files, err := filepath.Glob(filepath.Join(e.Cfg.BackupDir, "preupdate-*.tar.gz"))
	if err != nil {
		return err
	}
	// Les noms portent l'horodatage UTC : l'ordre lexicographique est l'ordre chronologique.
	sort.Strings(files)
	if len(files) <= e.Cfg.KeepBackups {
		return nil
	}
	for _, f := range files[:len(files)-e.Cfg.KeepBackups] {
		e.Logf("ancienne sauvegarde d'avant mise à jour supprimée : %s", filepath.Base(f))
		_ = os.Remove(f)
		_ = os.Remove(f + ".sha256")
	}
	return nil
}
