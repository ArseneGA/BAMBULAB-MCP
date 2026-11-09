package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/notify"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/upgrade"
)

// Mise à jour automatique du serveur (docs/DEPLOY-LINUX.md, « Mises à jour automatiques »). `bambud upgrade` est lancé par
// atelier-update.timer (via /opt/atelier/updater/bambud) ou à la main (sudo atelier-update). Elle TIRE les mises à jour :
// aucune connexion entrante, aucune clé de déploiement. Les sous-commandes upgrade-probe, upgrade-notify et schema-version
// sont des outils internes de la procédure.

// upgradeMain : `bambud upgrade [--auto] [--check] [--now] [--retry] [--status] [--pin SHA|current] [--unpin]`.
func upgradeMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("bambud upgrade", flag.ContinueOnError)
	fs.SetOutput(errw)
	confDir := fs.String("conf-dir", envOr("ATELIER_CONF_DIR", upgrade.DefaultConfDir), "dossier de configuration (défaut /etc/atelier)")
	auto := fs.Bool("auto", false, "passage du timer : silencieux quand il n'y a rien à faire")
	check := fs.Bool("check", false, "décide et explique, ne change rien (ni construction ni redémarrage)")
	now := fs.Bool("now", false, "passage manuel : ignore l'interrupteur et la fenêtre horaire (CI verte et service libre restent exigés)")
	retry := fs.Bool("retry", false, "retente un commit qui a déjà échoué sur ce serveur")
	status := fs.Bool("status", false, "affiche la version installée, l'épingle, l'interrupteur et le dernier résultat")
	pin := fs.String("pin", "", "épingle une version : sha (ou préfixe), ou « current » pour figer la version installée")
	unpin := fs.Bool("unpin", false, "retire l'épingle : suivre de nouveau la branche")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := upgrade.LoadConfig(*confDir, nil)
	if err != nil {
		fmt.Fprintln(errw, "erreur de configuration :", err)
		return 2
	}
	ctx, stop := cliContext()
	defer stop()
	self, _ := os.Executable()
	env := upgrade.NewSysEnv(cfg, self)
	env.Out = errw

	switch {
	case *status:
		return upgradeStatus(ctx, cfg, env, out)
	case *unpin:
		if err := os.Remove(cfg.PinFile()); err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(errw, "erreur :", err)
			return 1
		}
		fmt.Fprintln(out, "Épingle retirée : les mises à jour suivent de nouveau", cfg.Branch+".")
		return 0
	case *pin != "":
		return upgradePin(ctx, cfg, env, *pin, out, errw)
	}

	if os.Geteuid() != 0 {
		fmt.Fprintln(errw, "erreur : à lancer en root (sudo atelier-update) : la mise à jour redémarre le service et change de version")
		return 1
	}
	// Prérequis de droits : le compte de construction doit traverser /opt/atelier (0750 root:atelier sans ACL : « mkdir ...
	// Permission denied » en pleine construction). Code de sortie dédié, avant toute action.
	if perr := upgrade.CheckBuilderAccess(ctx, env.Run, cfg); perr != nil {
		fmt.Fprintln(errw, "erreur de prérequis :", perr)
		return upgrade.ExitPrereq
	}
	release, err := core.LockPath(cfg.LockFile)
	if err != nil {
		if errors.Is(err, core.ErrLocked) {
			fmt.Fprintln(out, "Une mise à jour est déjà en cours : rien à faire ici.")
			return 0
		}
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	defer release()

	up := &upgrade.Upgrader{Cfg: cfg, Env: env, StatePath: cfg.StateFile()}
	res, err := up.Run(ctx, upgrade.Options{Manual: *now, Retry: *retry, CheckOnly: *check})
	if err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	// Passage du timer : seuls les évènements qui comptent écrivent dans le journal. Les reports (CI en cours, hors
	// fenêtre, serveur occupé) sont déjà journalisés par le moteur, au plus une fois par heure.
	quiet := false
	if *auto {
		switch res.Outcome {
		case upgrade.OutUpToDate, upgrade.OutDisabled, upgrade.OutKnownFail, upgrade.OutWaitCI, upgrade.OutWindow, upgrade.OutBusy,
			upgrade.OutNoNetwork, upgrade.OutCIRed, upgrade.OutRefused:
			quiet = true
		}
	}
	if !quiet {
		fmt.Fprintf(out, "[%s] %s\n", res.Outcome, res.Message)
	}
	if res.Failed() {
		return 1
	}
	return 0
}

func upgradeStatus(ctx context.Context, cfg upgrade.Config, env *upgrade.SysEnv, out io.Writer) int {
	cur, err := env.Current(ctx)
	if err != nil {
		fmt.Fprintln(out, "Version installée : schéma releases/current absent (lancer deploy/linux/enable-auto-update.sh)")
	} else {
		fmt.Fprintf(out, "Version installée : %s (version %s, schéma %d)%s\n", cur.Name(), cur.Version, cur.Schema, map[bool]string{true: " [installation d'avant l'automatique]", false: ""}[cur.Legacy])
	}
	if t, err := os.Readlink(cfg.PreviousLink()); err == nil {
		fmt.Fprintln(out, "Version précédente :", filepath.Base(t))
	}
	state := "ACTIVÉ"
	if cfg.Disabled {
		state = "DÉSACTIVÉ (" + cfg.DisabledFile() + ")"
	}
	fmt.Fprintln(out, "Mises à jour automatiques :", state)
	if cfg.Pin != "" {
		fmt.Fprintln(out, "Version épinglée :", cfg.Pin)
	} else {
		fmt.Fprintf(out, "Suit : %s (%s)\n", cfg.Branch, cfg.Repo)
	}
	fmt.Fprintln(out, "Fenêtre horaire :", cfg.Window.String())
	fmt.Fprintln(out, "Test de fumée :", cfg.SmokeMode)
	st := upgrade.LoadState(cfg.StateFile())
	if !st.LastCheck.IsZero() {
		fmt.Fprintf(out, "Dernier passage : %s — %s\n", st.LastCheck.Local().Format("2006-01-02 15:04:05"), st.LastResult)
	}
	if st.LastOK != "" {
		fmt.Fprintf(out, "Dernière installation réussie : %s le %s\n", st.LastOK[:min(8, len(st.LastOK))], st.LastOKAt.Local().Format("2006-01-02 15:04"))
	}
	for sha, f := range st.Failed {
		fmt.Fprintf(out, "Commit en échec (non retenté) : %s le %s — %s\n", sha[:min(8, len(sha))], f.At.Local().Format("2006-01-02 15:04"), f.Reason)
	}
	return 0
}

func upgradePin(ctx context.Context, cfg upgrade.Config, env *upgrade.SysEnv, spec string, out, errw io.Writer) int {
	sha := spec
	if spec == "current" {
		cur, err := env.Current(ctx)
		if err != nil || cur.SHA == "" {
			fmt.Fprintln(errw, "erreur : version installée sans commit connu : indiquer un sha explicite")
			return 1
		}
		sha = cur.SHA
	}
	if sha == "" || strings.ContainsAny(sha, " \t\r\n/:") || strings.HasPrefix(sha, "-") || len(sha) < 7 || len(sha) > 40 || strings.Trim(sha, "0123456789abcdef") != "" {
		fmt.Fprintln(errw, "erreur : un sha git (7 à 40 caractères hexadécimaux) ou « current » est attendu")
		return 2
	}
	if err := os.WriteFile(cfg.PinFile(), []byte(sha+"\n"), 0o644); err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	fmt.Fprintf(out, "Version épinglée sur %s : plus aucune mise à jour automatique au-delà. Retirer : sudo atelier-update --unpin\n", sha)
	return 0
}

// upgradeProbeMain : `bambud upgrade-probe --data-dir D` — lecture seule de la base : schéma et occupation (JSON).
func upgradeProbeMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("bambud upgrade-probe", flag.ContinueOnError)
	fs.SetOutput(errw)
	dataDir := fs.String("data-dir", envOr("BAMBU_DATA_DIR", "./data"), "dossier de données (env BAMBU_DATA_DIR)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx, stop := cliContext()
	defer stop()
	p, err := upgrade.ProbeDB(ctx, *dataDir)
	if err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	b, _ := json.Marshal(p)
	fmt.Fprintln(out, string(b))
	return 0
}

// schemaVersionMain : `bambud schema-version` — la version de schéma que ce binaire sait ouvrir.
func schemaVersionMain(out io.Writer) int {
	fmt.Fprintln(out, store.LatestSchemaVersion())
	return 0
}

// upgradeNotifyMain : `bambud upgrade-notify --data-dir D --kind K --title T --message M` — émet une notification par les
// canaux de l'humain (évènement system_update). La base est ouverte SANS migration : ce binaire peut avoir un schéma
// différent de celui de la base (après un retour arrière, par exemple) et ne doit jamais le changer.
func upgradeNotifyMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("bambud upgrade-notify", flag.ContinueOnError)
	fs.SetOutput(errw)
	dataDir := fs.String("data-dir", envOr("BAMBU_DATA_DIR", "./data"), "dossier de données (env BAMBU_DATA_DIR)")
	kind := fs.String("kind", "info", "genre : installed, deferred, ci_red, build_failed, rolled_back, restored, critical, refused, smoke_warning, info")
	title := fs.String("title", "", "titre (obligatoire)")
	msg := fs.String("message", "", "message (sans secret)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*title) == "" {
		fmt.Fprintln(errw, "erreur : --title est requis")
		return 2
	}
	if _, err := os.Stat(*dataDir); err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	st, err := store.Open(filepath.Join(*dataDir, "bambu.db"), store.Options{NoMigrate: true})
	if err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(errw, &slog.HandlerOptions{Level: slog.LevelWarn}))
	vault, err := printing.OpenFileSecretStore(filepath.Join(*dataDir, "secrets"), log)
	if err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	base := publicBase(os.Getenv("BAMBU_PUBLIC_URL"), envOr("BAMBU_ADDR", "127.0.0.1:7878"))
	nv, err := notify.New(notify.Deps{Store: st, Vault: vault, Log: log, BaseURL: func() string { return base }})
	if err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res := nv.Emit(ctx, notify.Event{Kind: notify.EvSystemUpdate, Title: *title, Message: strings.TrimSpace(*msg + " [" + *kind + "]"), Link: base + "/"})
	sent := 0
	for _, r := range res {
		if r.OK {
			sent++
		}
	}
	fmt.Fprintf(out, "Notification émise : %d canal(aux) sur %d.\n", sent, len(res))
	return 0
}
