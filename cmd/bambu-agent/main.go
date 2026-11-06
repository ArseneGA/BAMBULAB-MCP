// bambu-agent est l'agent Windows de l'Atelier Bambu : il s'appaire à bambud,
// annonce ses capacités, exécute les travaux qu'on lui confie (inventaire et
// envoi de dossiers autorisés, slicing local, lecture du projet ouvert dans
// Bambu Studio, ouverture d'un résultat dans Studio) et ne modifie jamais ni les
// originaux ni les projets/profils de Studio.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agent"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
)

const usage = `bambu-agent — agent Windows de l'Atelier Bambu (version %s)

Commandes :
  pair      --server URL --token JETON [--name NOM] [--dpapi-scope user|machine]
  folder    add --label L --path P | list | remove --label L
  run       boucle de travaux au premier plan
  inventory --label L          inventaire lecture seule d'un dossier autorisé (envoyé au serveur)
  status    configuration et état de Bambu Studio (sans secret)
  studio    info | project | open FICHIER | probe --file FICHIER [--close]
  service   install | remove | run     (service Windows, voir docs/jalons/J8.md)

Options communes : --config CHEMIN (défaut %%LOCALAPPDATA%%\BambuAtelier\agent.json)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, agent.Version)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	var err error
	switch cmd {
	case "pair":
		err = cmdPair(args)
	case "folder":
		err = cmdFolder(args)
	case "run":
		err = cmdRun(args, log)
	case "inventory":
		err = cmdInventory(args, log)
	case "status":
		err = cmdStatus(args)
	case "studio":
		err = cmdStudio(args)
	case "service":
		err = cmdService(args)
	case "version", "--version":
		fmt.Println("bambu-agent", agent.Version)
	default:
		fmt.Fprintf(os.Stderr, usage, agent.Version)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		os.Exit(1)
	}
}

func parse(name string, args []string, setup func(*flag.FlagSet)) (*flag.FlagSet, *agent.Config, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	cfgPath := fs.String("config", agent.DefaultConfigPath(), "fichier de configuration")
	if setup != nil {
		setup(fs)
	}
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	cfg, err := agent.LoadConfig(*cfgPath)
	return fs, cfg, err
}

func cmdPair(args []string) error {
	var server, token, name, scope string
	_, cfg, err := parse("pair", args, func(fs *flag.FlagSet) {
		fs.StringVar(&server, "server", "", "URL de bambud, ex. http://192.168.1.20:7878")
		fs.StringVar(&token, "token", "", "jeton d'appairage (POST /api/agents/pairing sur le serveur)")
		fs.StringVar(&name, "name", "", "nom de cet agent (défaut : nom du poste)")
		fs.StringVar(&scope, "dpapi-scope", "user", "user | machine")
	})
	if err != nil {
		return err
	}
	if server == "" || token == "" {
		return errors.New("--server et --token requis")
	}
	if scope != "user" && scope != "machine" {
		return errors.New("--dpapi-scope : user | machine")
	}
	if name == "" {
		name, _ = os.Hostname()
	}
	host, _ := os.Hostname()
	cfg.ServerURL, cfg.Name, cfg.DPAPIScope = strings.TrimRight(server, "/"), name, scope
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := agentlink.NewClient(cfg.ServerURL, "").Pair(ctx, agentlink.PairRequest{Token: token, Name: name, Host: host, AgentVersion: agent.Version})
	if err != nil {
		return err
	}
	if err := cfg.SetSecret(agent.NewProtector(), resp.AgentID, resp.Secret); err != nil {
		return err
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("appairé : agent %s (%s). Secret protégé par DPAPI dans %s\n", resp.AgentID, name, cfg.Path())
	return nil
}

func cmdFolder(args []string) error {
	if len(args) == 0 {
		return errors.New("folder add|list|remove")
	}
	sub, rest := args[0], args[1:]
	var label, path string
	_, cfg, err := parse("folder", rest, func(fs *flag.FlagSet) {
		fs.StringVar(&label, "label", "", "étiquette du dossier (devient la collection racine)")
		fs.StringVar(&path, "path", "", "chemin absolu du dossier")
	})
	if err != nil {
		return err
	}
	switch sub {
	case "list":
		for _, f := range cfg.Folders {
			fmt.Printf("%s\t%s\n", f.Label, f.Path)
		}
		return nil
	case "add":
		abs, err := filepath.Abs(path)
		if err != nil || label == "" || path == "" {
			return errors.New("--label et --path requis")
		}
		if _, ok := cfg.Folder(label); ok {
			return fmt.Errorf("étiquette %q déjà utilisée", label)
		}
		cfg.Folders = append(cfg.Folders, agent.Folder{Label: label, Path: abs})
		if err := cfg.Validate(); err != nil {
			return err
		}
	case "remove":
		var keep []agent.Folder
		for _, f := range cfg.Folders {
			if f.Label != label {
				keep = append(keep, f)
			}
		}
		cfg.Folders = keep
	default:
		return errors.New("folder add|list|remove")
	}
	return cfg.Save()
}

func loadPaired(cfg *agent.Config) (string, error) {
	if cfg.ServerURL == "" {
		return "", agent.ErrNotPaired
	}
	return cfg.Secret(agent.NewProtector())
}

func newAgent(cfg *agent.Config, log *slog.Logger) (*agent.Agent, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	secret, err := loadPaired(cfg)
	if err != nil {
		return nil, err
	}
	return agent.New(agent.Options{Config: cfg, Secret: secret, Log: log})
}

func cmdRun(args []string, log *slog.Logger) error {
	_, cfg, err := parse("run", args, nil)
	if err != nil {
		return err
	}
	a, err := newAgent(cfg, log)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	log.Info("agent démarré", "serveur", cfg.ServerURL, "version", agent.Version)
	if err := a.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func cmdInventory(args []string, log *slog.Logger) error {
	var label string
	_, cfg, err := parse("inventory", args, func(fs *flag.FlagSet) { fs.StringVar(&label, "label", "", "étiquette du dossier autorisé") })
	if err != nil {
		return err
	}
	f, ok := cfg.Folder(label)
	if !ok {
		return fmt.Errorf("dossier %q non autorisé (bambu-agent folder add)", label)
	}
	a, err := newAgent(cfg, log)
	if err != nil {
		return err
	}
	rep, err := a.Inventory(context.Background(), f, nil)
	if rep != nil {
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(b))
	}
	return err
}

func cmdStatus(args []string) error {
	_, cfg, err := parse("status", args, nil)
	if err != nil {
		return err
	}
	st := &agent.SystemStudio{ConfiguredPath: cfg.StudioPath}
	out := map[string]any{"config": cfg.Path(), "server": cfg.ServerURL, "name": cfg.Name, "agent_id": cfg.AgentID,
		"paired": cfg.SecretProtected != "", "secret_storage": "DPAPI (" + firstNonEmpty(cfg.DPAPIScope, "user") + ")",
		"folders": cfg.Folders, "studio": st.Info(context.Background()), "version": agent.Version}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func cmdStudio(args []string) error {
	if len(args) == 0 {
		return errors.New("studio info|project|open FICHIER|probe --file FICHIER [--close]")
	}
	sub, rest := args[0], args[1:]
	var file string
	var closeAfter bool
	var extra string
	var settle, pid int
	_, cfg, err := parse("studio", rest, func(fs *flag.FlagSet) {
		fs.StringVar(&file, "file", "", "fichier à ouvrir")
		fs.IntVar(&pid, "pid", 0, "close : PID du processus Studio à fermer (seulement celui-là)")
		fs.IntVar(&settle, "settle", 3, "secondes d'attente avant d'observer Studio après le lancement")
		fs.StringVar(&extra, "extra-args", "", "options Studio avant le fichier, séparées par « ; » (ex. --datadir;D:/profil-sonde)")
		fs.BoolVar(&closeAfter, "close", false, "fermer proprement l'instance lancée par la sonde (seulement celle-là)")
	})
	if err != nil {
		return err
	}
	st := &agent.SystemStudio{ConfiguredPath: cfg.StudioPath}
	st.Settle = time.Duration(settle) * time.Second
	if extra != "" {
		st.ExtraArgs = strings.Split(extra, ";")
	}
	ctx := context.Background()
	print := func(v any) { b, _ := json.MarshalIndent(v, "", "  "); fmt.Println(string(b)) }
	switch sub {
	case "info":
		print(st.Info(ctx))
	case "project":
		a, err := agent.New(agent.Options{Config: cfg, Studio: st})
		if err != nil {
			return err
		}
		rep, err := a.ProjectState(ctx, func() (string, error) { return os.MkdirTemp("", "bambu-agent-project-") })
		if err != nil {
			return err
		}
		print(rep)
	case "close":
		// Fermeture propre (WM_CLOSE) d'UN processus Studio désigné, jamais des autres.
		found := false
		for _, p := range st.Info(ctx).Processes {
			found = found || p.PID == pid
		}
		if pid == 0 || !found {
			return errors.New("--pid doit désigner un processus bambu-studio.exe en cours")
		}
		fmt.Printf("WM_CLOSE envoyé à %d fenêtre(s) du processus %d\n", agent.CloseWindowsOfPID(pid), pid)
	case "open", "probe":
		if file == "" && len(os.Args) > 3 {
			file = os.Args[len(os.Args)-1]
		}
		if file == "" {
			return errors.New("fichier requis")
		}
		abs, err := filepath.Abs(file)
		if err != nil {
			return err
		}
		before := st.Info(ctx)
		res, err := st.Open(ctx, abs)
		if err != nil {
			return err
		}
		after := st.Info(ctx)
		print(map[string]any{"before_running": before.Running, "open": res, "after": after})
		if sub == "probe" && closeAfter {
			if res.Instance != "new_process" || len(res.RunningBefore) > 0 {
				fmt.Println("sonde : une autre instance existait ou le fichier a été transmis ; RIEN n'est fermé automatiquement")
				return nil
			}
			n := agent.CloseWindowsOfPID(res.LaunchedPID)
			fmt.Printf("sonde : WM_CLOSE envoyé à %d fenêtre(s) du processus %d\n", n, res.LaunchedPID)
			for i := 0; i < 30; i++ {
				time.Sleep(500 * time.Millisecond)
				alive := false
				for _, p := range st.Info(ctx).Processes {
					if p.PID == res.LaunchedPID {
						alive = true
					}
				}
				if !alive {
					fmt.Println("sonde : instance fermée proprement")
					return nil
				}
			}
			fmt.Println("sonde : l'instance est toujours ouverte (boîte de dialogue ?) — à fermer à la main")
		}
	default:
		return errors.New("studio info|project|open|probe")
	}
	return nil
}
