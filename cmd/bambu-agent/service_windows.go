//go:build windows

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agent"
)

const serviceName = "BambuAtelierAgent"

// cmdService : install | remove | run.
//
// DPAPI : le secret est lié au compte qui a exécuté « pair ». Un service qui
// tourne sous LocalSystem ne peut relire un secret « user » : appairer avec
// --dpapi-scope machine, ou installer le service avec --user/--password du même compte.
func cmdService(args []string) error {
	if len(args) == 0 {
		return errors.New("service install|remove|run")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	cfgPath := fs.String("config", agent.DefaultConfigPath(), "fichier de configuration")
	user := fs.String("user", "", "compte du service (défaut LocalSystem)")
	pass := fs.String("password", "", "mot de passe du compte")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	switch sub {
	case "install":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		m, err := mgr.Connect()
		if err != nil {
			return fmt.Errorf("gestionnaire de services (droits administrateur requis) : %w", err)
		}
		defer m.Disconnect()
		s, err := m.CreateService(serviceName, exe, mgr.Config{DisplayName: "Atelier Bambu - agent Windows",
			StartType: mgr.StartAutomatic, ServiceStartName: *user, Password: *pass,
			Description: "Agent de l'Atelier Bambu : inventaire, slicing local, session Bambu Studio"},
			"service", "run", "--config", *cfgPath)
		if err != nil {
			return err
		}
		defer s.Close()
		fmt.Println("service installé :", serviceName)
		return nil
	case "remove":
		m, err := mgr.Connect()
		if err != nil {
			return err
		}
		defer m.Disconnect()
		s, err := m.OpenService(serviceName)
		if err != nil {
			return err
		}
		defer s.Close()
		return s.Delete()
	case "run":
		return svc.Run(serviceName, &handler{cfgPath: *cfgPath})
	}
	return errors.New("service install|remove|run")
}

type handler struct{ cfgPath string }

func (h *handler) Execute(_ []string, req <-chan svc.ChangeRequest, st chan<- svc.Status) (bool, uint32) {
	st <- svc.Status{State: svc.StartPending}
	logf, _ := os.OpenFile(filepath.Join(filepath.Dir(h.cfgPath), "agent.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	var log *slog.Logger
	if logf != nil {
		defer logf.Close()
		log = slog.New(slog.NewTextHandler(logf, nil))
	} else {
		log = slog.Default()
	}
	cfg, err := agent.LoadConfig(h.cfgPath)
	var a *agent.Agent
	if err == nil {
		a, err = newAgent(cfg, log)
	}
	if err != nil {
		log.Error("démarrage du service impossible", "err", err)
		return false, 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = a.Run(ctx) }()
	st <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for c := range req {
		switch c.Cmd {
		case svc.Interrogate:
			st <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			st <- svc.Status{State: svc.StopPending}
			cancel()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
			}
			return false, 0
		}
	}
	cancel()
	return false, 0
}
