package printer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// TLSOptions décrit la politique de confiance envers le certificat
// auto-signé de l'imprimante. Exactement une des trois voies est requise.
type TLSOptions struct {
	// PinnedSHA256 : empreinte SHA-256 (hex) du certificat feuille DER. Voie recommandée.
	PinnedSHA256 string
	// RootCAPEM : autorité à utiliser pour une vérification classique.
	RootCAPEM []byte
	// InsecureSkipVerify désactive toute vérification. Exige InsecureReason,
	// consigné au WARN à chaque connexion.
	InsecureSkipVerify bool
	InsecureReason     string
	// ServerName pour SNI / vérification (défaut : l'hôte).
	ServerName string
}

// ErrTLSPolicy : politique TLS absente ou incohérente.
var ErrTLSPolicy = errors.New("printer: TLS policy must be exactly one of pinned fingerprint, root CA, or explicit insecure option with a reason")

// BambuConfig configure l'adaptateur Bambu LAN.
type BambuConfig struct {
	PrinterID string // identifiant logique, lié aux preuves de confirmation
	Host      string
	Serial    string
	MQTTPort  int // défaut 8883
	FTPPort   int // défaut 990
	Secrets   SecretProvider
	TLS       TLSOptions
	Verifier  ProofVerifier
	Logger    *slog.Logger
	Now       func() time.Time

	// StartURLTemplate : valeur du champ url de project_file ; {name} est
	// remplacé par le nom distant. Défaut "ftp:///{name}" (hypothèse, §J7).
	StartURLTemplate string

	MaxStatusAge         time.Duration // fraîcheur affichée (défaut 2 min)
	MaxStartStatusAge    time.Duration // fraîcheur exigée au lancement (défaut 30 s)
	PushAllMinInterval   time.Duration // défaut 5 min (note de recherche §2)
	PushAllWait          time.Duration // attente d'un rapport après pushall (défaut 5 s)
	PubAckTimeout        time.Duration // accusé de transport de la commande de lancement ; défaut 30 s (une P1S occupée à lire un gros fichier met plus de 5 s)
	CommandTimeout       time.Duration // pause/resume/stop (défaut 10 s)
	StartObserveTimeout  time.Duration // attente de PREPARE/RUNNING après start (défaut 90 s)
	ReconnectDelay       time.Duration // défaut 2 s
	AllowStartFromFinish bool          // autorise le départ depuis l'état finished (défaut non)
	// StartFromTerminal : accepte le départ depuis FINISH et FAILED (comme Bambu Studio). Une P1S reste en FAILED après
	// une annulation jusqu'à son redémarrage. Exige toujours un état frais, sans HMS actif ni print_error en cours
	// (hors reliquat bénin d'annulation) ; l'ancien état final ne compte jamais comme preuve de départ. Défaut : non.
	StartFromTerminal bool
	ReconcileSettle   time.Duration // délai de stabilisation avant de conclure « non démarré » (défaut 10 s)
}

func (c *BambuConfig) defaults() {
	if c.MQTTPort == 0 {
		c.MQTTPort = 8883
	}
	if c.FTPPort == 0 {
		c.FTPPort = 990
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.StartURLTemplate == "" {
		c.StartURLTemplate = "ftp:///{name}"
	}
	d := func(p *time.Duration, v time.Duration) {
		if *p == 0 {
			*p = v
		}
	}
	d(&c.MaxStatusAge, 2*time.Minute)
	d(&c.MaxStartStatusAge, 30*time.Second)
	d(&c.PushAllMinInterval, 5*time.Minute)
	d(&c.PushAllWait, 5*time.Second)
	d(&c.PubAckTimeout, 30*time.Second)
	d(&c.CommandTimeout, 10*time.Second)
	d(&c.StartObserveTimeout, 90*time.Second)
	d(&c.ReconnectDelay, 2*time.Second)
	d(&c.ReconcileSettle, 10*time.Second)
}

// DefaultStartOptions renvoie les options usuelles (nivellement, calibrations).
func DefaultStartOptions() StartOptions {
	return StartOptions{BedLeveling: true, FlowCali: true, VibrationCali: true, BedType: "auto"}
}

type conn struct {
	client   mqtt.Client
	lost     chan struct{}
	lostOnce sync.Once
	at       time.Time
}

func (c *conn) markLost() { c.lostOnce.Do(func() { close(c.lost) }) }

// Bambu est l'adaptateur MQTT/FTPS d'une imprimante Bambu en mode LAN.
type Bambu struct {
	cfg BambuConfig
	tls *tls.Config

	mu          sync.Mutex
	cur         *conn
	merger      *reportMerger
	updatedAt   time.Time
	firmware    string
	lastPushAll time.Time
	// lastPublishAt / acceptedAt : dernière publication de project_file et acceptation éventuelle (Reconcile).
	lastPublishAt time.Time
	acceptedAt    time.Time
	changed       chan struct{}
	subs          map[*subscriber]struct{}
	waiters       map[string]chan map[string]any
	rc            reconnectState // liaison : dernière connexion, erreurs, délai (reconnect.go)

	seq      atomic.Int64
	startMu  sync.Mutex // réservation : un seul Start à la fois
	uploadMu sync.Mutex
	accMu    sync.Mutex // une commande d'accessoire à la fois (accessories.go)
	skipMu   sync.Mutex // une commande skip_objects à la fois (skip.go)

	skipReply string // dernière réponse explicite à skip_objects, sous mu (skip.go)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type subscriber struct{ ch chan Status }

// NewBambu valide la configuration et construit l'adaptateur (aucune connexion).
func NewBambu(cfg BambuConfig) (*Bambu, error) {
	cfg.defaults()
	if cfg.Host == "" || cfg.Serial == "" || cfg.PrinterID == "" {
		return nil, errors.New("printer: Host, Serial and PrinterID are required")
	}
	if cfg.Secrets == nil {
		return nil, errors.New("printer: Secrets provider is required")
	}
	tc, err := buildTLS(cfg.Host, cfg.TLS, cfg.Logger)
	if err != nil {
		return nil, err
	}
	return &Bambu{
		cfg:     cfg,
		tls:     tc,
		merger:  newReportMerger(),
		changed: make(chan struct{}),
		subs:    map[*subscriber]struct{}{},
		waiters: map[string]chan map[string]any{},
	}, nil
}

func buildTLS(host string, o TLSOptions, log *slog.Logger) (*tls.Config, error) {
	n := 0
	if o.PinnedSHA256 != "" {
		n++
	}
	if len(o.RootCAPEM) > 0 {
		n++
	}
	if o.InsecureSkipVerify {
		n++
	}
	if n != 1 {
		return nil, ErrTLSPolicy
	}
	sn := o.ServerName
	if sn == "" {
		sn = host
	}
	// La session cache est nécessaire : le FTPS des Bambu exige (probablement)
	// la réutilisation de session TLS sur le canal de données.
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: sn, ClientSessionCache: tls.NewLRUClientSessionCache(16)}
	switch {
	case o.PinnedSHA256 != "":
		want := strings.ToLower(strings.ReplaceAll(o.PinnedSHA256, ":", ""))
		cfg.InsecureSkipVerify = true // la vérification est remplacée par l'épinglage ci-dessous
		cfg.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("printer: no peer certificate")
			}
			sum := sha256.Sum256(raw[0])
			if hex.EncodeToString(sum[:]) != want {
				return errors.New("printer: certificate fingerprint does not match pinned value")
			}
			return nil
		}
	case len(o.RootCAPEM) > 0:
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(o.RootCAPEM) {
			return nil, errors.New("printer: invalid RootCAPEM")
		}
		cfg.RootCAs = pool
	default:
		if strings.TrimSpace(o.InsecureReason) == "" {
			return nil, fmt.Errorf("%w: InsecureReason is mandatory", ErrTLSPolicy)
		}
		cfg.InsecureSkipVerify = true
		log.Warn("TLS certificate verification explicitly disabled", "reason", o.InsecureReason, "host", host)
	}
	return cfg, nil
}

// CertInfo décrit le certificat présenté par une imprimante (affiché à l'humain avant épinglage).
type CertInfo struct {
	SHA256     string    `json:"sha256"`
	SubjectCN  string    `json:"subject_cn"`
	DNSNames   []string  `json:"dns_names,omitempty"`
	NotAfter   time.Time `json:"not_after"`
	SelfSigned bool      `json:"self_signed"`
}

// FetchCertInfo ouvre une connexion TLS (sans rien envoyer) et décrit le certificat feuille présenté.
// Aucune confiance automatique : l'empreinte doit être validée par l'humain.
func FetchCertInfo(ctx context.Context, hostport string) (CertInfo, error) {
	d := tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, NetDialer: &net.Dialer{Timeout: 6 * time.Second}}
	c, err := d.DialContext(ctx, "tcp", hostport)
	if err != nil {
		return CertInfo{}, err
	}
	defer c.Close()
	certs := c.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return CertInfo{}, errors.New("printer: no certificate presented")
	}
	sum := sha256.Sum256(certs[0].Raw)
	return CertInfo{SHA256: hex.EncodeToString(sum[:]), SubjectCN: certs[0].Subject.CommonName, DNSNames: certs[0].DNSNames,
		NotAfter: certs[0].NotAfter, SelfSigned: bytes.Equal(certs[0].RawIssuer, certs[0].RawSubject)}, nil
}

// FetchCertFingerprint ouvre une connexion TLS et renvoie l'empreinte SHA-256
// du certificat présenté, pour que l'utilisateur la valide et l'épingle.
// Aucune donnée n'est envoyée ; ne pas utiliser pour une confiance automatique.
func FetchCertFingerprint(ctx context.Context, hostport string) (string, error) {
	d := tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}}
	c, err := d.DialContext(ctx, "tcp", hostport)
	if err != nil {
		return "", err
	}
	defer c.Close()
	certs := c.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("printer: no certificate presented")
	}
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:]), nil
}

// Connect établit la première session MQTT (erreur remontée telle quelle, ex.
// code d'accès refusé) puis lance la reconnexion en arrière-plan.
func (b *Bambu) Connect(ctx context.Context) error {
	b.mu.Lock()
	if b.ctx != nil {
		b.mu.Unlock()
		return errors.New("printer: already connected")
	}
	b.ctx, b.cancel = context.WithCancel(context.Background())
	b.mu.Unlock()
	if err := b.connectOnce(ctx); err != nil {
		b.cancel()
		b.mu.Lock()
		b.ctx = nil
		b.mu.Unlock()
		return err
	}
	b.wg.Add(1)
	go b.supervise()
	return nil
}

// Close coupe la session et arrête la reconnexion.
func (b *Bambu) Close() error {
	b.mu.Lock()
	cancel := b.cancel
	c := b.cur
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if c != nil {
		c.client.Disconnect(100)
	}
	b.wg.Wait()
	return nil
}

func (b *Bambu) mqttAddr() string {
	return "ssl://" + net.JoinHostPort(b.cfg.Host, strconv.Itoa(b.cfg.MQTTPort))
}
func (b *Bambu) ftpAddr() string      { return net.JoinHostPort(b.cfg.Host, strconv.Itoa(b.cfg.FTPPort)) }
func (b *Bambu) reportTopic() string  { return "device/" + b.cfg.Serial + "/report" }
func (b *Bambu) requestTopic() string { return "device/" + b.cfg.Serial + "/request" }

func (b *Bambu) connectOnce(ctx context.Context) error {
	code, err := b.cfg.Secrets.AccessCode(ctx)
	if err != nil {
		return fmt.Errorf("printer: access code unavailable: %w", err)
	}
	c := &conn{lost: make(chan struct{}), at: b.cfg.Now()}
	rnd := make([]byte, 4)
	_, _ = rand.Read(rnd)
	opts := mqtt.NewClientOptions().
		AddBroker(b.mqttAddr()).
		SetClientID("bambu-mcp-" + hex.EncodeToString(rnd)).
		SetUsername("bblp").
		SetPassword(code).
		SetTLSConfig(b.tls).
		SetCleanSession(true).
		SetAutoReconnect(false). // jamais de renvoi implicite d'un Start : on reconstruit un client neuf
		SetConnectRetry(false).
		SetKeepAlive(30 * time.Second).
		SetConnectTimeout(10 * time.Second).
		SetOrderMatters(true).
		SetConnectionLostHandler(func(_ mqtt.Client, err error) {
			b.cfg.Logger.Warn("mqtt connection lost", "printer", b.cfg.PrinterID, "err", err)
			b.onLost(c)
		})
	client := mqtt.NewClient(opts)
	c.client = client
	tok := client.Connect()
	if !waitToken(ctx, tok, 15*time.Second) {
		client.Disconnect(0)
		return errors.New("printer: mqtt connect timeout")
	}
	if err := tok.Error(); err != nil {
		return fmt.Errorf("printer: mqtt connect: %w", err)
	}
	st := client.Subscribe(b.reportTopic(), 0, func(_ mqtt.Client, m mqtt.Message) { b.onMessage(m.Payload()) })
	if !waitToken(ctx, st, 10*time.Second) || st.Error() != nil {
		client.Disconnect(0)
		return errors.New("printer: mqtt subscribe failed")
	}
	b.mu.Lock()
	b.cur = c
	b.merger = newReportMerger() // nouvelle session : on repart d'un pushall complet
	b.mu.Unlock()
	b.rcNoteOK()
	b.notify()
	// pushall à la connexion : seul cas où la limite des 5 minutes ne s'applique pas.
	b.sendPushAll(c, true)
	b.sendRaw(c, map[string]any{"info": map[string]any{"sequence_id": b.nextSeq(), "command": "get_version"}})
	return nil
}

func waitToken(ctx context.Context, t mqtt.Token, d time.Duration) bool {
	done := make(chan struct{})
	go func() { t.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	case <-ctx.Done():
		return false
	}
}

func (b *Bambu) onLost(c *conn) {
	b.mu.Lock()
	if b.cur == c {
		b.cur = nil
	}
	b.mu.Unlock()
	b.rcNoteLost()
	c.markLost()
	b.notify()
}

func (b *Bambu) supervise() {
	defer b.wg.Done()
	tick := time.NewTicker(b.cfg.PushAllMinInterval)
	defer tick.Stop()
	for {
		b.mu.Lock()
		c := b.cur
		b.mu.Unlock()
		if c == nil {
			if !b.waitReconnect() { // délai croissant plafonné, coupé dès qu'un port répond (reconnect.go)
				return
			}
			cctx, cancel := context.WithTimeout(b.ctx, 20*time.Second)
			err := b.connectOnce(cctx)
			cancel()
			if err != nil {
				b.rcNoteFail(err)
				b.cfg.Logger.Warn("reconnect failed", "printer", b.cfg.PrinterID, "err", err)
			}
			continue
		}
		select {
		case <-b.ctx.Done():
			return
		case <-c.lost:
		case <-tick.C:
			b.sendPushAll(c, false)
		}
	}
}

// notify réveille tous les observateurs et diffuse l'état aux abonnés.
func (b *Bambu) notify() {
	b.mu.Lock()
	old := b.changed
	b.changed = make(chan struct{})
	st := b.snapshotLocked()
	subs := make([]*subscriber, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.Unlock()
	close(old)
	for _, s := range subs {
		push(s.ch, st)
	}
}

func push(ch chan Status, st Status) {
	for {
		select {
		case ch <- st:
			return
		default:
			select {
			case <-ch: // abandonne la plus ancienne
			default:
			}
		}
	}
}

func (b *Bambu) snapshotLocked() Status {
	s := b.merger.toStatus()
	s.UpdatedAt = b.updatedAt
	s.Firmware = b.firmware
	s.Connected = b.cur != nil
	s.Stale = b.updatedAt.IsZero() || b.cfg.Now().Sub(b.updatedAt) > b.cfg.MaxStatusAge
	if !s.Connected {
		s.State = StateOffline
	}
	return s
}

func (b *Bambu) snapshot() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotLocked()
}

func (b *Bambu) onMessage(payload []byte) {
	var top map[string]any
	if err := json.Unmarshal(payload, &top); err != nil {
		return
	}
	if info, ok := top["info"].(map[string]any); ok {
		b.handleInfo(info)
	}
	if sm, ok := top["system"].(map[string]any); ok { // réponses de ledctrl (accessories.go) : même routage par sequence_id
		b.deliver(sm)
		return
	}
	pm, ok := top["print"].(map[string]any)
	if !ok {
		return
	}
	cmd := str(pm["command"])
	if cmd == "skip_objects" {
		b.noteSkipReply(pm)
	}
	if cmd == "push_status" || (cmd == "" && pm["gcode_state"] != nil) {
		b.mu.Lock()
		b.merger.merge(pm)
		b.updatedAt = b.cfg.Now()
		b.mu.Unlock()
		b.notify()
		return
	}
	b.deliver(pm)
}

// deliver remet une réponse à la commande qui attend ce sequence_id.
func (b *Bambu) deliver(pm map[string]any) {
	seq := str(pm["sequence_id"])
	b.mu.Lock()
	ch := b.waiters[seq]
	b.mu.Unlock()
	if ch != nil {
		select {
		case ch <- pm:
		default:
		}
	}
}

func (b *Bambu) handleInfo(info map[string]any) {
	mods, _ := info["module"].([]any)
	fw := ""
	for _, m := range mods {
		mm, _ := m.(map[string]any)
		if mm == nil {
			continue
		}
		v := str(mm["sw_ver"])
		if v == "" {
			continue
		}
		if str(mm["name"]) == "ota" {
			fw = v
			break
		}
		if fw == "" {
			fw = v
		}
	}
	if fw != "" {
		b.mu.Lock()
		b.firmware = fw
		b.mu.Unlock()
		b.notify()
	}
}

func (b *Bambu) nextSeq() string { return strconv.FormatInt(b.seq.Add(1)+1000, 10) }

func (b *Bambu) sendRaw(c *conn, payload any) mqtt.Token {
	data, _ := json.Marshal(payload)
	return c.client.Publish(b.requestTopic(), 1, false, data)
}

// sendPushAll applique la limite d'intervalle sauf force.
func (b *Bambu) sendPushAll(c *conn, force bool) bool {
	b.mu.Lock()
	now := b.cfg.Now()
	if !force && !b.lastPushAll.IsZero() && now.Sub(b.lastPushAll) < b.cfg.PushAllMinInterval {
		b.mu.Unlock()
		return false
	}
	b.lastPushAll = now
	b.mu.Unlock()
	b.sendRaw(c, map[string]any{"pushing": map[string]any{"sequence_id": b.nextSeq(), "command": "pushall", "version": 1, "push_target": 1}})
	return true
}

func (b *Bambu) current() *conn {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cur
}

func (b *Bambu) changedCh() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.changed
}

// Status renvoie l'état fusionné ; si aucun rapport n'est encore arrivé, attend
// le premier (borné par ctx et 5 s).
func (b *Bambu) Status(ctx context.Context) (Status, error) {
	deadline := time.After(5 * time.Second)
	for {
		ch := b.changedCh()
		s := b.snapshot()
		if !s.Connected || !s.UpdatedAt.IsZero() {
			return s, nil
		}
		select {
		case <-ch:
		case <-deadline:
			return s, nil
		case <-ctx.Done():
			return s, ctx.Err()
		}
	}
}

// Subscribe renvoie un canal d'états (le dernier état est livré d'emblée ;
// si le lecteur est lent, les plus anciens sont écartés).
func (b *Bambu) Subscribe(ctx context.Context) (<-chan Status, error) {
	s := &subscriber{ch: make(chan Status, 16)}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	st := b.snapshotLocked()
	b.mu.Unlock()
	push(s.ch, st)
	out := make(chan Status, 16)
	go func() {
		defer func() {
			b.mu.Lock()
			delete(b.subs, s)
			b.mu.Unlock()
			close(out)
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case <-b.ctxDone():
				return
			case v := <-s.ch:
				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func (b *Bambu) ctxDone() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ctx == nil {
		return nil
	}
	return b.ctx.Done()
}

func (b *Bambu) registerWaiter(seq string) chan map[string]any {
	ch := make(chan map[string]any, 4)
	b.mu.Lock()
	b.waiters[seq] = ch
	b.mu.Unlock()
	return ch
}

func (b *Bambu) dropWaiter(seq string) {
	b.mu.Lock()
	delete(b.waiters, seq)
	b.mu.Unlock()
}

var (
	errLost    = errors.New("connection lost")
	errTimeout = errors.New("timeout")
)

// waitEval boucle jusqu'à ce que eval conclue, que la connexion tombe, que le
// délai expire ou que ctx soit annulé. resp vaut nil tant qu'aucune réponse
// portant le sequence_id n'est arrivée.
func (b *Bambu) waitEval(ctx context.Context, c *conn, respCh <-chan map[string]any, timeout time.Duration,
	eval func(resp map[string]any, st Status) (bool, error)) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var resp map[string]any
	for {
		changed := b.changedCh()
		select {
		case <-c.lost:
			return errLost
		default:
		}
		if done, err := eval(resp, b.snapshot()); done || err != nil {
			return err
		}
		select {
		case r := <-respCh:
			resp = r
			if done, err := eval(resp, b.snapshot()); done || err != nil {
				return err
			}
		case <-changed:
		case <-c.lost:
			return errLost
		case <-timer.C:
			return errTimeout
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// publishAcked publie en QoS 1 et attend le PUBACK.
func (b *Bambu) publishAcked(ctx context.Context, c *conn, payload any) error {
	tok := b.sendRaw(c, payload)
	if !waitToken(ctx, tok, b.cfg.PubAckTimeout) {
		return errTimeout
	}
	return tok.Error()
}

// ensureFresh garantit un rapport récent, avec pushall si la limite le permet.
func (b *Bambu) ensureFresh(ctx context.Context, c *conn, maxAge time.Duration) (Status, error) {
	s := b.snapshot()
	if s.FullReportSeen && !s.UpdatedAt.IsZero() && b.cfg.Now().Sub(s.UpdatedAt) <= maxAge {
		return s, nil
	}
	before := b.snapshot().UpdatedAt
	b.sendPushAll(c, false)
	timer := time.NewTimer(b.cfg.PushAllWait)
	defer timer.Stop()
	for {
		ch := b.changedCh()
		s = b.snapshot()
		if s.FullReportSeen && s.UpdatedAt.After(before) && b.cfg.Now().Sub(s.UpdatedAt) <= maxAge {
			return s, nil
		}
		select {
		case <-ch:
		case <-timer.C:
			return s, ErrStaleStatus
		case <-c.lost:
			return s, ErrNotConnected
		case <-ctx.Done():
			return s, ctx.Err()
		}
	}
}

// Pause met l'impression en pause (état printing requis).
func (b *Bambu) Pause(ctx context.Context) error {
	return b.control(ctx, "pause", func(s State) bool { return s == StatePrinting },
		func(s Status) bool { return s.State == StatePaused })
}

// Resume reprend une impression en pause.
func (b *Bambu) Resume(ctx context.Context) error {
	return b.control(ctx, "resume", func(s State) bool { return s == StatePaused },
		func(s Status) bool { return s.State == StatePrinting || s.State == StatePreparing })
}

// Stop arrête l'impression (preparing, printing ou paused).
func (b *Bambu) Stop(ctx context.Context) error {
	return b.control(ctx, "stop", func(s State) bool {
		return s == StatePreparing || s == StatePrinting || s == StatePaused
	}, func(s Status) bool { return s.State == StateIdle || s.State == StateFailed || s.State == StateFinished })
}

func (b *Bambu) control(ctx context.Context, cmd string, allowed func(State) bool, reached func(Status) bool) error {
	c := b.current()
	if c == nil {
		return ErrNotConnected
	}
	// Pause/stop sont des gestes de sécurité : on ne les bloque pas sur la fraîcheur.
	s := b.snapshot()
	if !allowed(s.State) {
		return fmt.Errorf("%w: %s while %s", ErrBadState, cmd, s.State)
	}
	seq := b.nextSeq()
	ch := b.registerWaiter(seq)
	defer b.dropWaiter(seq)
	if err := b.publishAcked(ctx, c, map[string]any{"print": map[string]any{"sequence_id": seq, "command": cmd, "param": ""}}); err != nil {
		return fmt.Errorf("printer: %s not acknowledged: %w", cmd, err)
	}
	err := b.waitEval(ctx, c, ch, b.cfg.CommandTimeout, func(resp map[string]any, st Status) (bool, error) {
		if resp != nil {
			if r := str(resp["result"]); r != "" && !strings.EqualFold(r, "success") {
				return false, fmt.Errorf("printer: %s refused: %s %s", cmd, r, str(resp["reason"]))
			}
		}
		return reached(st), nil
	})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errTimeout):
		return fmt.Errorf("printer: %s: %w in time", cmd, ErrStateNotObserved)
	case errors.Is(err, errLost):
		return fmt.Errorf("printer: %s: %w", cmd, ErrNotConnected)
	}
	return err
}
