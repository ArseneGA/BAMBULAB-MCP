// Package upgrade met à jour automatiquement le serveur Linux : le serveur TIRE les mises à jour (git fetch de
// origin/main), n'installe que les commits dont la CI GitHub est verte, ne redémarre jamais pendant une impression,
// sauvegarde avant, construit dans releases/<sha>, bascule le lien `current`, contrôle la santé et revient en arrière
// (en restaurant la sauvegarde si le schéma de la base a changé) en cas d'échec.
//
// Le choix d'un paquet Go plutôt que d'un script shell : la logique de décision et le scénario de retour arrière
// sont testables sur toutes les plateformes (CI comprise) avec un environnement simulé, la lecture du JSON de GitHub
// est sûre, et les notifications réutilisent le système de bambud. Les effets de bord réels (git, npm, go, systemctl,
// runuser) sont isolés dans sysenv.go derrière l'interface Env.
package upgrade

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Valeurs par défaut de l'installation (deploy/linux/install.sh).
const (
	DefaultRepo      = "https://github.com/ArseneGA/BAMBULAB-MCP"
	DefaultBranch    = "main"
	DefaultRoot      = "/opt/atelier"
	DefaultConfDir   = "/etc/atelier"
	DefaultCacheDir  = "/var/lib/atelier-update"
	DefaultDataDir   = "/var/lib/atelier"
	DefaultService   = "atelier.service"
	DefaultAppUser   = "atelier"
	DefaultBuildUser = "atelier-update"
	DefaultLockFile  = "/run/lock/atelier-update.lock"
	DefaultGitHubAPI = "https://api.github.com"

	DisabledFileName = "auto-update.disabled"
	PinFileName      = "auto-update.pin"
	ConfFileName     = "auto-update.env"
)

// Modes du test de fumée après bascule.
const (
	SmokeRequired = "required" // un échec déclenche le retour arrière (défaut)
	SmokeAdvisory = "advisory" // un échec est journalisé et notifié, sans retour arrière
	SmokeOff      = "off"
)

// Config regroupe les réglages. Aucun champ secret n'est affiché : APIToken et GitHubToken ne sont jamais journalisés.
type Config struct {
	Repo        string
	Branch      string
	Root        string // /opt/atelier : releases/, current, previous, updater/, bin/
	ConfDir     string // /etc/atelier
	CacheDir    string // /var/lib/atelier-update : clone git, constructions, état
	DataDir     string // /var/lib/atelier (BAMBU_DATA_DIR)
	BackupDir   string // dossier des sauvegardes
	CadDir      string // /opt/atelier/cad (worker géométrie), vide si absent
	AppUser     string
	BuildUser   string
	Service     string
	LockFile    string
	GitHubAPI   string
	BaseURL     string // http://127.0.0.1:7878
	EnvFile     string // atelier.env (jeton pour le test de fumée)
	APIToken    string
	GitHubToken string
	BuildPath   string            // PATH de l'utilisateur de construction (go, node, npm)
	UsesCAD     bool              // le worker CAO est configuré (BAMBU_UV_PATH) : le test de fumée l'exige
	PublicURL   string            // BAMBU_PUBLIC_URL : base des liens des notifications
	AppEnv      map[string]string // atelier.env : jamais journalisé (contient le jeton d'API)

	Window         Window
	Pin            string // sha ou préfixe ; vide = suivre la branche
	Disabled       bool
	KeepReleases   int
	KeepBackups    int
	RefreshUpdater bool // ATELIER_UPDATE_REFRESH_UPDATER=1 : remplacer l'outil exécuté en root par celui de la version installée (déconseillé)
	RequiredChecks []string
	SmokeMode      string
	HealthWait     time.Duration
	StableWait     time.Duration
}

// DisabledFile, PinFile : chemins des interrupteurs.
func (c Config) DisabledFile() string { return filepath.Join(c.ConfDir, DisabledFileName) }
func (c Config) PinFile() string      { return filepath.Join(c.ConfDir, PinFileName) }

// ReleasesDir, CurrentLink, PreviousLink, UpdaterBin.
func (c Config) ReleasesDir() string  { return filepath.Join(c.Root, "releases") }
func (c Config) CurrentLink() string  { return filepath.Join(c.Root, "current") }
func (c Config) PreviousLink() string { return filepath.Join(c.Root, "previous") }
func (c Config) UpdaterBin() string   { return filepath.Join(c.Root, "updater", "bambud") }
func (c Config) RepoDir() string      { return filepath.Join(c.CacheDir, "repo") }
func (c Config) BuildDir() string     { return filepath.Join(c.CacheDir, "build") }
func (c Config) StateFile() string    { return filepath.Join(c.CacheDir, "state.json") }

// ReadEnvFile lit un fichier KEY=VALUE (valeurs éventuellement entre guillemets, commentaires #). Un fichier absent
// donne une table vide sans erreur.
func ReadEnvFile(path string) (map[string]string, error) {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		out[k] = v
	}
	return out, sc.Err()
}

// LoadConfig lit la configuration : variables d'environnement ATELIER_UPDATE_* (prioritaires), puis
// <confDir>/auto-update.env, puis atelier.env et backup.env pour l'adresse, le dossier de données et le jeton du service.
func LoadConfig(confDir string, getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if confDir == "" {
		confDir = DefaultConfDir
	}
	own, err := ReadEnvFile(filepath.Join(confDir, ConfFileName))
	if err != nil {
		return Config{}, fmt.Errorf("%s : %w", ConfFileName, err)
	}
	app, err := ReadEnvFile(filepath.Join(confDir, "atelier.env"))
	if err != nil {
		return Config{}, fmt.Errorf("atelier.env : %w", err)
	}
	bk, err := ReadEnvFile(filepath.Join(confDir, "backup.env"))
	if err != nil {
		return Config{}, fmt.Errorf("backup.env : %w", err)
	}
	get := func(k, def string) string {
		if v := strings.TrimSpace(getenv("ATELIER_UPDATE_" + k)); v != "" {
			return v
		}
		if v := strings.TrimSpace(own["ATELIER_UPDATE_"+k]); v != "" {
			return v
		}
		return def
	}
	c := Config{
		Repo: strings.TrimRight(get("REPO", DefaultRepo), "/"), Branch: get("BRANCH", DefaultBranch),
		Root: get("ROOT", DefaultRoot), ConfDir: confDir, CacheDir: get("CACHE_DIR", DefaultCacheDir),
		DataDir: firstNonEmpty(app["BAMBU_DATA_DIR"], DefaultDataDir), BackupDir: firstNonEmpty(get("BACKUP_DIR", ""), bk["BACKUP_DIR"], "/var/backups/atelier"),
		AppUser: get("APP_USER", DefaultAppUser), BuildUser: get("BUILD_USER", DefaultBuildUser), Service: get("SERVICE", DefaultService),
		LockFile: get("LOCK_FILE", DefaultLockFile), GitHubAPI: strings.TrimRight(get("GITHUB_API", DefaultGitHubAPI), "/"),
		EnvFile: filepath.Join(confDir, "atelier.env"), APIToken: app["BAMBU_API_TOKEN"], BuildPath: get("PATH", ""),
		UsesCAD: app["BAMBU_UV_PATH"] != "", PublicURL: app["BAMBU_PUBLIC_URL"], AppEnv: app, SmokeMode: strings.ToLower(get("SMOKE", SmokeRequired)),
		HealthWait: 180 * time.Second, StableWait: 20 * time.Second,
	}
	if d := app["BAMBU_GEOMETRY_DIR"]; d != "" {
		c.CadDir = d
	}
	if c.BaseURL = get("BASE_URL", ""); c.BaseURL == "" {
		c.BaseURL = "http://127.0.0.1:" + portOf(firstNonEmpty(app["BAMBU_ADDR"], "127.0.0.1:7878"))
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	if c.KeepReleases, err = intOf(get("KEEP_RELEASES", "3"), 2); err != nil {
		return c, fmt.Errorf("ATELIER_UPDATE_KEEP_RELEASES : %w", err)
	}
	if c.KeepBackups, err = intOf(get("KEEP_BACKUPS", "5"), 1); err != nil {
		return c, fmt.Errorf("ATELIER_UPDATE_KEEP_BACKUPS : %w", err)
	}
	if c.Window, err = ParseWindow(get("WINDOW", "")); err != nil {
		return c, fmt.Errorf("ATELIER_UPDATE_WINDOW : %w", err)
	}
	c.RefreshUpdater = get("REFRESH_UPDATER", "") == "1"
	for _, n := range strings.Split(get("REQUIRED_CHECKS", ""), ";") {
		if n = strings.TrimSpace(n); n != "" {
			c.RequiredChecks = append(c.RequiredChecks, n)
		}
	}
	switch c.SmokeMode {
	case SmokeRequired, SmokeAdvisory, SmokeOff:
	default:
		return c, fmt.Errorf("ATELIER_UPDATE_SMOKE %q : required, advisory ou off", c.SmokeMode)
	}
	if v := get("HEALTH_WAIT", ""); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Second {
			return c, fmt.Errorf("ATELIER_UPDATE_HEALTH_WAIT %q invalide (ex. 180s)", v)
		}
		c.HealthWait = d
	}
	if _, err := os.Stat(c.DisabledFile()); err == nil {
		c.Disabled = true
	}
	if v := strings.TrimSpace(getenv("ATELIER_UPDATE_DISABLED")); v == "1" || strings.EqualFold(v, "true") {
		c.Disabled = true
	}
	if b, err := os.ReadFile(c.PinFile()); err == nil {
		c.Pin = firstLine(string(b))
	}
	if v := strings.TrimSpace(getenv("ATELIER_UPDATE_PIN")); v != "" {
		c.Pin = v
	}
	if tf := get("TOKEN_FILE", filepath.Join(confDir, "github-token")); tf != "" {
		tok, terr := ReadTokenFile(tf)
		if terr == nil {
			c.GitHubToken = tok
		} else if !os.IsNotExist(terr) {
			return c, fmt.Errorf("jeton GitHub %s : %w", tf, terr)
		}
	}
	return c, c.Validate()
}

// Validate refuse une configuration incohérente (chemins relatifs, utilisateurs vides...).
func (c Config) Validate() error {
	for name, p := range map[string]string{"Root": c.Root, "CacheDir": c.CacheDir, "DataDir": c.DataDir, "BackupDir": c.BackupDir, "ConfDir": c.ConfDir} {
		if !filepath.IsAbs(p) && !strings.HasPrefix(p, "/") {
			return fmt.Errorf("%s doit être un chemin absolu (%q)", name, p)
		}
	}
	if c.Repo == "" || c.Branch == "" || c.Service == "" {
		return fmt.Errorf("dépôt, branche et service sont requis")
	}
	if c.BuildUser == "" || c.AppUser == "" {
		return fmt.Errorf("utilisateurs de construction et de service requis")
	}
	if c.BuildUser == c.AppUser {
		return fmt.Errorf("l'utilisateur de construction doit différer de celui du service (la construction exécute du code téléchargé)")
	}
	return nil
}

// ReadTokenFile lit un jeton GitHub facultatif. Le fichier doit être en 0600 (ou plus strict) : sinon il est refusé,
// car un jeton lisible par tous est un secret divulgué. Un fichier absent renvoie os.ErrNotExist.
func ReadTokenFile(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", fmt.Errorf("c'est un dossier")
	}
	if runtimeEnforcesModes() && st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("droits %04o trop larges : chmod 600 (le jeton n'est pas lu)", st.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := firstLine(string(b))
	if strings.HasPrefix(tok, "GITHUB_TOKEN=") || strings.HasPrefix(tok, "TOKEN=") {
		_, tok, _ = strings.Cut(tok, "=")
	}
	tok = strings.Trim(strings.TrimSpace(tok), `"'`)
	if strings.ContainsAny(tok, " \t\r\n") {
		return "", fmt.Errorf("le fichier doit contenir le jeton seul")
	}
	return tok, nil
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			return l
		}
	}
	return ""
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 && i+1 < len(addr) {
		return addr[i+1:]
	}
	return "7878"
}

func intOf(s string, min int) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < min {
		return 0, fmt.Errorf("entier >= %d attendu (%q)", min, s)
	}
	return n, nil
}
