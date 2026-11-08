package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agent"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentv2"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

const printerLinkUsage = `bambu-agent printer-link — agent local d'imprimante (protocole /agent/v2, voir docs/AGENT-V2.md)

L'agent ouvre une connexion SORTANTE vers le serveur, parle seul à l'imprimante (MQTT TLS 8883, FTPS 990, caméra
6000) et garde seul son code d'accès LAN, qui n'est jamais envoyé au serveur. Il ne lance une impression que sur une
autorisation signée par le serveur (clé épinglée à l'appairage), valable 2 minutes, à usage unique, qu'il revérifie.

Premier démarrage (appairage avec un jeton de « bambud agent pair ») :
  bambu-agent printer-link --server https://bambu.example --token pair-… --printer-id prn_… \
      --printer-host 192.168.1.50 --printer-serial 01P00A… --access-code-file /etc/bambu/access-code \
      --pin-sha256 <empreinte du certificat de l'imprimante>

Démarrages suivants : les mêmes options sans --token. Options :
  --server URL                    URL de bambud (https recommandé)
  --server-fingerprint SHA256     épingle le certificat TLS du serveur (auto-signé)
  --token JETON                   jeton d'appairage à usage unique (premier démarrage seulement)
  --server-key-id ID              identifiant de la clé de signature attendue (comparé à bambud : GET /api/agent-v2/server-key)
  --name NOM                      nom de l'agent
  --printer-id ID                 identifiant de l'imprimante dans bambud (prn_…)
  --printer-host HÔTE             adresse de l'imprimante sur ce réseau   --printer-serial SERIE
  --mqtt-port N  --ftp-port N  --camera-port N
  --access-code-file CHEMIN       fichier contenant le code d'accès LAN (droits 0600 conseillés)
  --pin-sha256 SHA256             empreinte du certificat de l'imprimante ; --discover-pin l'affiche pour comparaison
  --read-only                     KILL-SWITCH : refuse tout lancement, dépôt et reprise
  --read-only-file CHEMIN         idem tant que ce fichier existe (touch / rm, sans redémarrage)
  --skip-remote-hash              n'effectue pas la relecture FTPS du fichier avant lancement (déconseillé)
  --state-dir DOSSIER             état de l'agent (défaut : <config>/printer-link)
`

func cmdPrinterLink(args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("printer-link", flag.ContinueOnError)
	var (
		server, fp, token, keyID, name, printerID, host, serial, codeFile, pin, roFile, stateDir string
		mqttPort, ftpPort, camPort                                                               int
		readOnly, skipHash, discover                                                             bool
	)
	fs.StringVar(&server, "server", "", "")
	fs.StringVar(&fp, "server-fingerprint", "", "")
	fs.StringVar(&token, "token", "", "")
	fs.StringVar(&keyID, "server-key-id", "", "")
	fs.StringVar(&name, "name", "", "")
	fs.StringVar(&printerID, "printer-id", "", "")
	fs.StringVar(&host, "printer-host", "", "")
	fs.StringVar(&serial, "printer-serial", "", "")
	fs.IntVar(&mqttPort, "mqtt-port", 0, "")
	fs.IntVar(&ftpPort, "ftp-port", 0, "")
	fs.IntVar(&camPort, "camera-port", 0, "")
	fs.StringVar(&codeFile, "access-code-file", "", "")
	fs.StringVar(&pin, "pin-sha256", "", "")
	fs.BoolVar(&discover, "discover-pin", false, "")
	fs.BoolVar(&readOnly, "read-only", false, "")
	fs.StringVar(&roFile, "read-only-file", "", "")
	fs.BoolVar(&skipHash, "skip-remote-hash", false, "")
	fs.StringVar(&stateDir, "state-dir", filepath.Join(agent.DefaultDir(), "printer-link"), "")
	fs.Usage = func() { fmt.Fprint(os.Stderr, printerLinkUsage) }
	if err := fs.Parse(args); err != nil {
		return err
	}

	// --discover-pin : lit le certificat présenté par l'imprimante (rien n'est envoyé) pour que l'humain le compare.
	if discover {
		if host == "" {
			return errors.New("--printer-host requis")
		}
		mp := mqttPort
		if mp == 0 {
			mp = 8883
		}
		ci, err := printer.FetchCertInfo(context.Background(), net.JoinHostPort(host, strconv.Itoa(mp)))
		if err != nil {
			return err
		}
		fmt.Printf("certificat de l'imprimante %s : SHA-256 %s (CN %q, auto-signé : %v)\nComparez avec celui de l'imprimante avant de l'utiliser comme --pin-sha256.\n", host, ci.SHA256, ci.SubjectCN, ci.SelfSigned)
		return nil
	}

	cfgPath := filepath.Join(stateDir, "link.json")
	cfg, err := agentv2.LoadLinkConfig(cfgPath)
	switch {
	case errors.Is(err, agentv2.ErrNotPaired):
		if server == "" || token == "" || printerID == "" {
			return errors.New("premier démarrage : --server, --token et --printer-id requis")
		}
		if fp != "" {
			if fp, err = agentv2.NormalizeFingerprint(fp); err != nil {
				return err
			}
		}
		if w := agentv2.InsecureTransportWarning(server); w != "" {
			fmt.Fprintln(os.Stderr, w)
		}
		cl, cerr := agentv2.NewClient(server, "", fp)
		if cerr != nil {
			return cerr
		}
		if name == "" {
			name, _ = os.Hostname()
		}
		h, _ := os.Hostname()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cfg, err = agentv2.PairAgent(ctx, cl, server, fp, token, name, h, printerID, agent.Version, keyID)
		cancel()
		if err != nil {
			return err
		}
		if err := cfg.Save(cfgPath); err != nil {
			return err
		}
		fmt.Printf("appairé : agent %s pour l'imprimante %s. Clé de signature du serveur épinglée : %s (à comparer avec GET /api/agent-v2/server-key).\n", cfg.AgentID, cfg.PrinterID, cfg.ServerKeyID)
	case err != nil:
		return err
	}
	if printerID != "" && printerID != cfg.PrinterID {
		return fmt.Errorf("cet agent est appairé pour l'imprimante %s, pas %s : supprimer %s pour réappairer", cfg.PrinterID, printerID, cfgPath)
	}
	if host == "" || serial == "" || codeFile == "" || pin == "" {
		return errors.New("--printer-host, --printer-serial, --access-code-file et --pin-sha256 requis")
	}
	pubKey, err := cfg.ServerPublicKey()
	if err != nil {
		return err
	}
	code, err := agentv2.ReadAccessCode(codeFile)
	if err != nil {
		return err
	}
	if st, serr := os.Stat(codeFile); serr == nil && st.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		fmt.Fprintf(os.Stderr, "ATTENTION : %s est lisible par d'autres utilisateurs (droits %v) : chmod 600 conseillé.\n", codeFile, st.Mode().Perm())
	}
	if pin, err = agentv2.NormalizeFingerprint(pin); err != nil {
		return err
	}
	cl, err := agentv2.NewClient(cfg.ServerURL, cfg.Secret, cfg.ServerFingerprint)
	if err != nil {
		return err
	}
	newDevice, snapshot, err := agentv2.BambuDevice{PrinterID: cfg.PrinterID, Host: host, Serial: serial, MQTTPort: mqttPort, FTPPort: ftpPort,
		CameraPort: camPort, AccessCode: code, PinSHA256: pin, Logger: log}.Factory()
	if err != nil {
		return err
	}
	if roFile == "" {
		roFile = filepath.Join(stateDir, "READ_ONLY") // kill-switch par défaut : `touch <state-dir>/READ_ONLY`
	}
	r, err := agentv2.NewRunner(agentv2.RunnerConfig{
		Client: cl, AgentID: cfg.AgentID, PrinterID: cfg.PrinterID, ServerKey: pubKey,
		NewDevice: newDevice, Snapshot: snapshot, StateDir: stateDir,
		ReadOnly: agentv2.ReadOnlyFlag(readOnly, roFile), SkipRemoteHash: skipHash,
		Version: agent.Version, Log: log,
	})
	if err != nil {
		return err
	}
	defer r.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	log.Info("agent d'imprimante démarré", "serveur", cfg.ServerURL, "imprimante", cfg.PrinterID, "agent", cfg.AgentID,
		"lecture_seule", r.ReadOnly(), "fichier_kill_switch", strings.TrimSpace(roFile))
	if err := r.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
