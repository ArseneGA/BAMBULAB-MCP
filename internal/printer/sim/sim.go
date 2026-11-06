// Package sim est un simulateur local d'imprimante Bambu (P1S par défaut) qui
// parle le même protocole que le matériel : broker MQTT TLS (user bblp + code
// d'accès, topics device/{serial}/report|request, rapports partiels de type P1)
// et serveur FTPS à TLS implicite. Il n'écoute que sur 127.0.0.1.
//
// Ce que le simulateur NE sait PAS faire, comme le vrai : savoir si le plateau
// est vide (le drapeau plate_occupied est sans effet sur les rapports).
package sim

import (
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
	"github.com/spf13/afero"
)

// Noms de pannes activables (flag, API de contrôle ou SetFault).
const (
	FaultOffline        = "offline"          // plus aucune connexion MQTT/FTPS acceptée
	FaultDropAfterStart = "drop_after_start" // exécute project_file puis coupe sans accuser (une fois)
	FaultIgnoreStart    = "ignore_start"     // reçoit project_file et l'ignore (aucun effet, aucun accusé)
	FaultRejectStart    = "reject_start"     // répond result:fail à project_file
	FaultHMSError       = "hms_error"        // erreur HMS en cours (pause si impression)
	FaultFileMissing    = "file_missing"     // le fichier « disparaît » : l'impression échoue au démarrage
	FaultPlateOccupied  = "plate_occupied"   // sans effet observable : le vrai ne sait pas non plus
)

// Config configure le simulateur.
type Config struct {
	Model      string // "P1S" par défaut
	Serial     string // défaut "01P00A000000001"
	AccessCode string // défaut "12345678"
	Firmware   string // défaut "01.08.02.00"

	Host        string // défaut 127.0.0.1 (toute autre valeur est refusée)
	MQTTPort    int    // 0 = éphémère (le vrai : 8883)
	FTPPort     int    // 0 = éphémère (le vrai : 990)
	ControlPort int    // -1 = pas d'API de contrôle ; 0 = éphémère

	// FilesDir : racine FTP sur disque ; vide = mémoire.
	FilesDir string

	Tick              time.Duration // pas de simulation (défaut 100 ms)
	HeartbeatInterval time.Duration // rapport partiel périodique (défaut 2 s)
	PrepareDuration   time.Duration // PREPARE avant RUNNING (défaut 1 s)
	PrintDuration     time.Duration // durée de RUNNING (défaut 10 s)
	TotalLayers       int           // défaut 100

	Faults []string // pannes actives au démarrage
	Logger *slog.Logger
}

func (c *Config) defaults() error {
	if c.Model == "" {
		c.Model = "P1S"
	}
	if c.Serial == "" {
		c.Serial = "01P00A000000001"
	}
	if c.AccessCode == "" {
		c.AccessCode = "12345678"
	}
	if c.Firmware == "" {
		c.Firmware = "01.08.02.00"
	}
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.Host != "127.0.0.1" {
		return fmt.Errorf("sim: host must be 127.0.0.1 (got %q)", c.Host)
	}
	if c.Tick == 0 {
		c.Tick = 100 * time.Millisecond
	}
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = 2 * time.Second
	}
	if c.PrepareDuration == 0 {
		c.PrepareDuration = time.Second
	}
	if c.PrintDuration == 0 {
		c.PrintDuration = 10 * time.Second
	}
	if c.TotalLayers == 0 {
		c.TotalLayers = 100
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return nil
}

// StartRecord garde une commande project_file reçue (pour les assertions).
type StartRecord struct {
	At      time.Time
	Payload map[string]any
}

// Sim est le simulateur.
type Sim struct {
	cfg Config

	cert        tls.Certificate
	certPEM     []byte
	fingerprint string

	broker   *mqtt.Server
	mqttGate *gate
	ftpGate  *gate
	mqttAddr string
	ftpAddr  string
	ctlAddr  string
	fs       afero.Fs
	ftp      *ftpHandle
	ctl      *controlHandle

	mu        sync.Mutex
	faults    map[string]bool
	p         *model
	lastSent  map[string]string
	lastBeat  time.Time
	starts    []StartRecord
	reportSeq int
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// New construit et démarre le simulateur.
func New(cfg Config) (*Sim, error) {
	if err := cfg.defaults(); err != nil {
		return nil, err
	}
	cert, certPEM, fp, err := newCert(cfg.Serial)
	if err != nil {
		return nil, err
	}
	s := &Sim{cfg: cfg, cert: cert, certPEM: certPEM, fingerprint: fp,
		faults: map[string]bool{}, lastSent: map[string]string{}}
	for _, f := range cfg.Faults {
		s.faults[f] = true
	}
	s.p = newModel(cfg)
	if cfg.FilesDir != "" {
		s.fs = afero.NewBasePathFs(afero.NewOsFs(), cfg.FilesDir)
	} else {
		s.fs = afero.NewMemMapFs()
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}

	// MQTT
	ml, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.MQTTPort)))
	if err != nil {
		return nil, err
	}
	s.mqttGate = newGate(ml)
	s.mqttAddr = ml.Addr().String()
	s.broker = mqtt.New(&mqtt.Options{InlineClient: true, Logger: cfg.Logger})
	if err := s.broker.AddHook(&authHook{user: "bblp", pass: cfg.AccessCode, serial: cfg.Serial}, nil); err != nil {
		return nil, err
	}
	if err := s.broker.AddListener(listeners.NewNet("mqtts", tls.NewListener(s.mqttGate, tlsCfg))); err != nil {
		return nil, err
	}
	if err := s.broker.Subscribe("device/"+cfg.Serial+"/request", 1, s.onRequest); err != nil {
		return nil, err
	}
	go func() { _ = s.broker.Serve() }()

	// FTPS
	if err := s.startFTP(tlsCfg); err != nil {
		return nil, err
	}
	// Contrôle HTTP
	if cfg.ControlPort >= 0 {
		if err := s.startControl(); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.applyOffline()
	s.wg.Add(1)
	go s.loop(ctx)
	return s, nil
}

func (s *Sim) MQTTAddr() string    { return s.mqttAddr }
func (s *Sim) FTPAddr() string     { return s.ftpAddr }
func (s *Sim) ControlAddr() string { return s.ctlAddr }
func (s *Sim) Fingerprint() string { return s.fingerprint }
func (s *Sim) CertPEM() []byte     { return s.certPEM }
func (s *Sim) Serial() string      { return s.cfg.Serial }
func (s *Sim) AccessCode() string  { return s.cfg.AccessCode }
func (s *Sim) FS() afero.Fs        { return s.fs }
func (s *Sim) Model() string       { return s.cfg.Model }

// Close arrête tout.
func (s *Sim) Close() error {
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.wg.Wait()
		_ = s.broker.Close()
		s.mqttGate.DropAll()
		if s.ftp != nil {
			s.ftp.stop()
		}
		if s.ctl != nil {
			s.ctl.stop()
		}
	})
	return nil
}

// SetFault active ou désactive une panne.
func (s *Sim) SetFault(name string, on bool) error {
	switch name {
	case FaultOffline, FaultDropAfterStart, FaultIgnoreStart, FaultRejectStart, FaultHMSError, FaultFileMissing, FaultPlateOccupied:
	default:
		return fmt.Errorf("sim: unknown fault %q", name)
	}
	s.mu.Lock()
	if on {
		s.faults[name] = true
	} else {
		delete(s.faults, name)
	}
	s.mu.Unlock()
	if name == FaultOffline {
		s.applyOffline()
	}
	if name == FaultHMSError {
		s.mu.Lock()
		s.p.setHMS(on)
		s.mu.Unlock()
	}
	return nil
}

func (s *Sim) fault(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.faults[name]
}

func (s *Sim) applyOffline() {
	off := s.fault(FaultOffline)
	s.mqttGate.setOffline(off)
	if s.ftpGate != nil {
		s.ftpGate.setOffline(off)
	}
}

// Faults liste les pannes actives.
func (s *Sim) Faults() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k := range s.faults {
		out = append(out, k)
	}
	return out
}

// StartCommands renvoie les project_file reçus.
func (s *Sim) StartCommands() []StartRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]StartRecord(nil), s.starts...)
}

// GcodeState renvoie le gcode_state courant du modèle.
func (s *Sim) GcodeState() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.p.gcodeState
}

// ConnectedClients donne le nombre de clients MQTT externes connectés.
func (s *Sim) ConnectedClients() int {
	n := 0
	for id := range s.broker.Clients.GetAll() {
		if id != mqtt.InlineClientId {
			n++
		}
	}
	return n
}

// --- authentification MQTT -------------------------------------------------

type authHook struct {
	mqtt.HookBase
	user, pass, serial string
}

func (h *authHook) ID() string { return "bambu-auth" }
func (h *authHook) Provides(b byte) bool {
	return b == mqtt.OnConnectAuthenticate || b == mqtt.OnACLCheck
}
func (h *authHook) OnConnectAuthenticate(cl *mqtt.Client, pk packets.Packet) bool {
	u := string(pk.Connect.Username)
	p := string(pk.Connect.Password)
	return subtle.ConstantTimeCompare([]byte(u), []byte(h.user)) == 1 &&
		subtle.ConstantTimeCompare([]byte(p), []byte(h.pass)) == 1
}
func (h *authHook) OnACLCheck(cl *mqtt.Client, topic string, write bool) bool {
	return strings.HasPrefix(topic, "device/"+h.serial+"/")
}

// --- boucle de simulation --------------------------------------------------

func (s *Sim) loop(ctx context.Context) {
	defer s.wg.Done()
	t := time.NewTicker(s.cfg.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			s.p.step(time.Now(), s.faults[FaultFileMissing], s.fileExists)
			s.mu.Unlock()
			s.publishDelta(false)
		}
	}
}

// publishDelta publie uniquement les clés changées (comportement P1), ou tout
// si full. Un heartbeat (wifi_signal) garantit un rapport périodique.
func (s *Sim) publishDelta(full bool) {
	if s.fault(FaultOffline) {
		return
	}
	s.mu.Lock()
	all := s.p.report(s.cfg)
	now := time.Now()
	out := map[string]any{}
	for k, v := range all {
		b, _ := json.Marshal(v)
		if full || s.lastSent[k] != string(b) {
			out[k] = v
			s.lastSent[k] = string(b)
		}
	}
	if !full && len(out) == 0 {
		if now.Sub(s.lastBeat) < s.cfg.HeartbeatInterval {
			s.mu.Unlock()
			return
		}
		out["wifi_signal"] = all["wifi_signal"]
	}
	s.lastBeat = now
	s.reportSeq++
	out["command"] = "push_status"
	out["msg"] = map[bool]int{true: 0, false: 1}[full]
	out["sequence_id"] = strconv.Itoa(s.reportSeq + 2000)
	s.mu.Unlock()
	s.publishReport(map[string]any{"print": out})
}

func (s *Sim) publishReport(v any) {
	b, _ := json.Marshal(v)
	_ = s.broker.Publish("device/"+s.cfg.Serial+"/report", b, false, 0)
}

func (s *Sim) fileExists(name string) bool {
	ok, _ := afero.Exists(s.fs, "/"+name)
	return ok
}

// --- commandes -------------------------------------------------------------

func (s *Sim) onRequest(_ *mqtt.Client, _ packets.Subscription, pk packets.Packet) {
	payload := append([]byte(nil), pk.Payload...)
	go s.handleRequest(payload) // hors de la goroutine de lecture du broker
}

func (s *Sim) handleRequest(payload []byte) {
	if s.fault(FaultOffline) {
		return
	}
	var top map[string]map[string]any
	dec := json.NewDecoder(bytes.NewReader(payload))
	if err := dec.Decode(&top); err != nil {
		return
	}
	if p, ok := top["pushing"]; ok && fmt.Sprint(p["command"]) == "pushall" {
		s.mu.Lock()
		s.lastSent = map[string]string{}
		s.mu.Unlock()
		s.publishDelta(true)
		return
	}
	if p, ok := top["info"]; ok && fmt.Sprint(p["command"]) == "get_version" {
		s.publishReport(map[string]any{"info": map[string]any{
			"command": "get_version", "sequence_id": fmt.Sprint(p["sequence_id"]), "result": "success",
			"module": []any{
				map[string]any{"name": "ota", "sw_ver": s.cfg.Firmware, "hw_ver": "OTA", "sn": s.cfg.Serial},
				map[string]any{"name": "esp32", "sw_ver": "01.07.23.00", "hw_ver": "AP05", "sn": "SIM"},
			}}})
		return
	}
	p, ok := top["print"]
	if !ok {
		return
	}
	cmd := fmt.Sprint(p["command"])
	seq := fmt.Sprint(p["sequence_id"])
	switch cmd {
	case "project_file":
		s.onProjectFile(seq, p)
	case "pause", "resume", "stop":
		s.mu.Lock()
		err := s.p.control(cmd, time.Now())
		s.mu.Unlock()
		s.respond(cmd, seq, err)
		s.publishDelta(false)
	default:
		s.respond(cmd, seq, errors.New("unsupported command"))
	}
}

func (s *Sim) respond(cmd, seq string, err error) {
	r := map[string]any{"command": cmd, "sequence_id": seq, "result": "success", "reason": "success"}
	if err != nil {
		r["result"], r["reason"] = "fail", err.Error()
	}
	s.publishReport(map[string]any{"print": r})
}

// fileFromURL extrait le nom de fichier de l'URL de project_file
// (ftp:///x.3mf, ftp://x.3mf, file:///mnt/sdcard/x.3mf, file:///sdcard/x.3mf).
func fileFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && (u.Scheme == "ftp" || u.Scheme == "file") {
		if u.Host != "" && u.Path == "" {
			return path.Base(u.Host)
		}
		return path.Base(u.Path)
	}
	return path.Base(raw)
}

func (s *Sim) onProjectFile(seq string, p map[string]any) {
	s.mu.Lock()
	s.starts = append(s.starts, StartRecord{At: time.Now(), Payload: p})
	faults := map[string]bool{}
	for k, v := range s.faults {
		faults[k] = v
	}
	s.mu.Unlock()

	if faults[FaultIgnoreStart] {
		return
	}
	if faults[FaultRejectStart] {
		s.respond("project_file", seq, errors.New("developer mode required (simulated)"))
		return
	}
	name := fileFromURL(fmt.Sprint(p["url"]))
	s.mu.Lock()
	err := s.p.begin(time.Now(), name, fmt.Sprint(p["param"]), fmt.Sprint(p["subtask_name"]))
	if err == nil && faults[FaultDropAfterStart] {
		// L'impression démarre mais la liaison tombe avant tout accusé ni rapport :
		// l'appelant ne peut pas savoir. Sous le verrou, pour qu'aucun rapport ne
		// parte entre le démarrage et la coupure.
		delete(s.faults, FaultDropAfterStart)
		s.mqttGate.DropAll()
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	if err != nil {
		s.respond("project_file", seq, err)
		return
	}
	s.respond("project_file", seq, nil)
	s.publishDelta(false)
}
