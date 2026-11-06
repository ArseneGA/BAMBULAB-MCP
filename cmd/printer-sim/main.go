// Commande printer-sim : simulateur local d'imprimante Bambu (voir internal/printer/sim).
// N'écoute que sur 127.0.0.1.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
)

func main() {
	var (
		model    = flag.String("model", "P1S", "modèle simulé")
		serial   = flag.String("serial", "01P00A000000001", "numéro de série")
		code     = flag.String("access-code", "12345678", "code d'accès LAN (user bblp)")
		mqttPort = flag.Int("mqtt-port", 8883, "port MQTT TLS (0 = éphémère)")
		ftpPort  = flag.Int("ftp-port", 990, "port FTPS implicite (0 = éphémère)")
		ctlPort  = flag.Int("control-port", 8890, "port de l'API HTTP de contrôle (-1 = désactivée)")
		dir      = flag.String("files-dir", "", "racine FTP sur disque (vide = mémoire)")
		prep     = flag.Duration("prepare", time.Second, "durée de PREPARE")
		dur      = flag.Duration("print-duration", 10*time.Second, "durée de RUNNING")
		layers   = flag.Int("layers", 100, "nombre total de couches")
		faults   = flag.String("faults", "", "pannes initiales séparées par des virgules (offline,drop_after_start,ignore_start,reject_start,hms_error,file_missing,plate_occupied)")
		verbose  = flag.Bool("v", false, "journal détaillé")
	)
	flag.Parse()
	lvl := slog.LevelWarn
	if *verbose {
		lvl = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
	var fl []string
	if *faults != "" {
		fl = strings.Split(*faults, ",")
	}
	s, err := sim.New(sim.Config{Model: *model, Serial: *serial, AccessCode: *code, MQTTPort: *mqttPort,
		FTPPort: *ftpPort, ControlPort: *ctlPort, FilesDir: *dir, PrepareDuration: *prep,
		PrintDuration: *dur, TotalLayers: *layers, Faults: fl, Logger: log})
	if err != nil {
		fmt.Fprintln(os.Stderr, "printer-sim:", err)
		os.Exit(1)
	}
	defer s.Close()
	fmt.Printf("printer-sim %s serial=%s\n  mqtts  %s\n  ftps   %s\n  control %s\n  cert sha256 %s\n",
		s.Model(), s.Serial(), s.MQTTAddr(), s.FTPAddr(), s.ControlAddr(), s.Fingerprint())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
}
