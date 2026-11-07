package camera

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/notify"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Target décrit l'imprimante dont on lit la caméra. Secrets fournit le code d'accès (jamais loggé).
type Target struct {
	ID, Name, Host, Serial, ModelID, ModelName string
	PinnedSHA256                               string
	Secrets                                    printer.SecretProvider
}

// JobInfo : une impression, vue par la caméra.
type JobInfo struct {
	ID, PrinterID, Name string
	State               contracts.PrintState
}

// PrinterState : ce que dit l'imprimante pendant l'analyse.
type PrinterState struct {
	Reachable   bool
	Printing    bool
	Layer       int
	TotalLayers int
	HMSCount    int
	PrintError  int
}

// Backend est l'accès (en lecture) à la chaîne d'impression ; adapté sur *printing.Service (printing_backend.go).
type Backend interface {
	Target(ctx context.Context, printerID string) (Target, error)
	PrintingJobs(ctx context.Context) ([]JobInfo, error)
	Job(ctx context.Context, jobID string) (JobInfo, error)
	State(ctx context.Context, printerID string) (PrinterState, error)
	// StopURL crée (ou relit) la DEMANDE d'arrêt de l'impression et renvoie l'adresse de la page /stop : rien n'est arrêté.
	StopURL(ctx context.Context, jobID string) (string, error)
	BaseURL() string
}

// Notifier émet les notifications (notify.Service).
type Notifier interface {
	Emit(ctx context.Context, e notify.Event) []notify.Result
}

// Config du service.
type Config struct {
	Store    *store.Store
	Blobs    *store.BlobStore
	Backend  Backend
	Notifier Notifier // facultatif
	Log      *slog.Logger
	Now      func() time.Time
	// Port remplace le port 6000 de la caméra (simulateur : port éphémère). 0 = 6000.
	Port int
	// MinInterval : plus petit intervalle d'instantanés accepté, en secondes (défaut 5 ; tests : moins).
	MinInterval float64
	// Scan : période de recherche des impressions à surveiller (défaut 5 s).
	Scan time.Duration
	// RateBurst et RateEvery : limite de débit PAR IMPRIMANTE des instantanés et des ouvertures de flux (défaut : 6 d'affilée,
	// puis un toutes les 2 s). MaxViewers : spectateurs simultanés d'un flux (défaut 8).
	RateBurst  int
	RateEvery  time.Duration
	MaxViewers int
}

// Settings : réglages de la détection (non secrets), persistés.
type Settings struct {
	Enabled          bool           `json:"enabled" jsonschema:"active la surveillance périodique pendant les impressions"`
	IntervalSeconds  float64        `json:"interval_seconds" jsonschema:"intervalle entre deux instantanés (5 à 3600 s)"`
	DisabledPrinters []string       `json:"disabled_printers,omitempty" jsonschema:"imprimantes exclues de la surveillance"`
	Detector         DetectorConfig `json:"detector"`
	AlertCooldownMin int            `json:"alert_cooldown_minutes" jsonschema:"minutes minimum entre deux alertes pour la même impression"`
	// AlertPhotoRetentionDays : les photos d'alerte plus anciennes sont supprimées (0 = jamais). Les photos de fin sont conservées.
	AlertPhotoRetentionDays int `json:"alert_photo_retention_days" jsonschema:"jours de conservation des photos d'alerte (0 = illimité, défaut 90) ; les photos de fin d'impression sont conservées"`
}

// DefaultSettings : valeurs par défaut.
func DefaultSettings() Settings {
	return Settings{Enabled: true, IntervalSeconds: 60, Detector: DefaultDetectorConfig(), AlertCooldownMin: 30, AlertPhotoRetentionDays: 90}
}

// Frame : une image reçue.
type Frame struct {
	JPEG []byte
	At   time.Time
}

// Photo : une image enregistrée (blob + ligne camera_photos).
type Photo struct {
	ID        string    `json:"id"`
	PrinterID string    `json:"printer_id"`
	JobID     string    `json:"print_job_id,omitempty"`
	Kind      string    `json:"kind" jsonschema:"snapshot | confirm | final | alert"`
	SHA256    string    `json:"sha256"`
	Size      int       `json:"size_bytes"`
	TakenAt   time.Time `json:"taken_at"`
	URL       string    `json:"url" jsonschema:"route de l'interface qui sert l'image (cookie d'un navigateur enregistré ou jeton d'API)"`
	Width     int       `json:"width,omitempty"`
	Height    int       `json:"height,omitempty"`
	JPEG      []byte    `json:"-"`
}

// Alert : alerte de détection enregistrée.
type Alert struct {
	ID        string    `json:"id"`
	PrinterID string    `json:"printer_id"`
	JobID     string    `json:"print_job_id"`
	At        time.Time `json:"at"`
	Score     float64   `json:"score"`
	Level     string    `json:"level"`
	Report    Report    `json:"report"`
	PhotoURL  string    `json:"photo_url,omitempty"`
}

var (
	// ErrNotFound : élément inconnu.
	ErrNotFound = errors.New("introuvable")
	// ErrUnavailable : caméra non joignable ou non prise en charge.
	ErrUnavailable = errors.New("caméra indisponible")
	// ErrInvalid : entrée invalide.
	ErrInvalid = errors.New("entrée invalide")
	// ErrRateLimited : trop de demandes pour cette imprimante.
	ErrRateLimited = errors.New("trop de demandes de caméra pour cette imprimante : réessaie dans quelques secondes")
)

// MaxJPEGSide : côté maximal accepté d'une image de caméra (au-delà, l'image est refusée avant tout décodage complet).
const MaxJPEGSide = 4096

// checkJPEG lit seulement l'en-tête (jpeg.DecodeConfig) et refuse une image trop grande ou illisible : jamais de décodage
// complet d'une image dont les dimensions ne sont pas bornées (bombe de décompression).
func checkJPEG(b []byte) (image.Config, error) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return cfg, fmt.Errorf("%w : image JPEG illisible", ErrUnavailable)
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxJPEGSide || cfg.Height > MaxJPEGSide {
		return cfg, fmt.Errorf("%w : image de %dx%d refusée (maximum %dx%d)", ErrUnavailable, cfg.Width, cfg.Height, MaxJPEGSide, MaxJPEGSide)
	}
	return cfg, nil
}

type bucket struct {
	tokens float64
	at     time.Time
}

// allow applique la limite de débit d'une imprimante pour une catégorie (« snapshot », « stream »).
func (s *Service) allow(printerID, cat string) bool {
	burst, every := s.c.RateBurst, s.c.RateEvery
	if burst <= 0 {
		burst = 6
	}
	if every <= 0 {
		every = 2 * time.Second
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rate == nil {
		s.rate = map[string]*bucket{}
	}
	k := cat + "|" + printerID
	b := s.rate[k]
	if b == nil {
		b = &bucket{tokens: float64(burst), at: now}
		s.rate[k] = b
	}
	b.tokens += float64(now.Sub(b.at)) / float64(every)
	if b.tokens > float64(burst) {
		b.tokens = float64(burst)
	}
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Snapshot : instantané demandé de l'extérieur (API HTTP, outils MCP). Limité en débit par imprimante ; l'image n'est
// JAMAIS conservée (seuls les photos de fin, d'alerte et de confirmation le sont).
func (s *Service) Snapshot(ctx context.Context, printerID string) (*Photo, error) {
	if !s.allow(printerID, "snapshot") {
		return nil, ErrRateLimited
	}
	return s.Capture(ctx, printerID, "snapshot", "", 2*time.Second)
}

// Stream ouvre un abonnement demandé de l'extérieur (relais MJPEG) : limite de débit des ouvertures et nombre maximal de
// spectateurs simultanés par imprimante.
func (s *Service) Stream(ctx context.Context, printerID string) (*Stream, error) {
	if !s.allow(printerID, "stream") {
		return nil, ErrRateLimited
	}
	max := s.c.MaxViewers
	if max <= 0 {
		max = 8
	}
	s.mu.Lock()
	h := s.hubs[printerID]
	s.mu.Unlock()
	if h != nil {
		h.mu.Lock()
		n := len(h.subs)
		h.mu.Unlock()
		if n >= max {
			return nil, ErrRateLimited
		}
	}
	return s.Subscribe(ctx, printerID)
}

// Service gère les instantanés, le relais MJPEG et la surveillance.
type Service struct {
	c   Config
	log *slog.Logger

	mu        sync.Mutex
	hubs      map[string]*hub
	snap      map[string]*sync.Mutex // un instantané à la fois par imprimante
	refs      map[string]refImage    // dernière photo de plateau (confirmation) par imprimante
	mons      map[string]*monitor
	last      map[string]lastAnalysis // par impression, conservé après la fin
	final     map[string]*sync.Mutex
	nonce     map[string]nonceEntry
	lastFrame map[string]Frame
	rate      map[string]*bucket
	wg        sync.WaitGroup

	setMu    sync.Mutex
	settings *Settings
}

type refImage struct {
	at   time.Time
	jpeg []byte
}

type lastAnalysis struct {
	Report Report
	JPEG   []byte
	At     time.Time
}

// New construit le service (aucune connexion).
func New(c Config) (*Service, error) {
	if c.Store == nil || c.Blobs == nil || c.Backend == nil {
		return nil, errors.New("camera : store, blobs et backend requis")
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}
	if c.Now == nil {
		c.Now = func() time.Time { return c.Store.Now() }
	}
	if c.Scan == 0 {
		c.Scan = 5 * time.Second
	}
	return &Service{c: c, log: c.Log, hubs: map[string]*hub{}, snap: map[string]*sync.Mutex{}, refs: map[string]refImage{},
		mons: map[string]*monitor{}, last: map[string]lastAnalysis{}, final: map[string]*sync.Mutex{}, nonce: map[string]nonceEntry{}, lastFrame: map[string]Frame{}}, nil
}

func (s *Service) now() time.Time { return s.c.Now().UTC() }

// ---- réglages ----

// Settings renvoie les réglages courants.
func (s *Service) Settings(ctx context.Context) Settings {
	s.setMu.Lock()
	defer s.setMu.Unlock()
	if s.settings != nil {
		return *s.settings
	}
	st := DefaultSettings()
	var doc string
	if err := s.c.Store.DB().QueryRowContext(ctx, `SELECT doc FROM camera_settings WHERE key='detection'`).Scan(&doc); err == nil {
		_ = json.Unmarshal([]byte(doc), &st)
	}
	st.normalize(s.minInterval())
	s.settings = &st
	return st
}

func (st *Settings) normalize(minSec float64) {
	if st.IntervalSeconds < minSec {
		st.IntervalSeconds = minSec
	}
	if st.IntervalSeconds > 3600 {
		st.IntervalSeconds = 3600
	}
	if st.AlertCooldownMin < 1 {
		st.AlertCooldownMin = 30
	}
	st.Detector.normalize()
	sort.Strings(st.DisabledPrinters)
}

// SettingsPatch : modification partielle des réglages (champs absents = inchangés).
type SettingsPatch struct {
	Enabled          *bool    `json:"enabled,omitempty"`
	IntervalSeconds  *float64 `json:"interval_seconds,omitempty" jsonschema:"5 à 3600"`
	Threshold        *float64 `json:"threshold,omitempty" jsonschema:"score d'alerte, 0.1 à 1"`
	DiffThreshold    *float64 `json:"diff_threshold,omitempty" jsonschema:"écart de luminance qui fait une cellule « changée », 4 à 100"`
	PersistFrames    *int     `json:"persist_frames,omitempty" jsonschema:"1 à 10"`
	StallMinutes     *float64 `json:"stall_minutes,omitempty"`
	ConsecutiveAlert *int     `json:"consecutive_alerts,omitempty" jsonschema:"1 à 20"`
	CooldownMinutes  *int     `json:"alert_cooldown_minutes,omitempty"`
	PhotoRetention   *int     `json:"alert_photo_retention_days,omitempty" jsonschema:"0 (illimité) à 3650 ; défaut 90"`
	DisablePrinters  []string `json:"disable_printers,omitempty"`
	EnablePrinters   []string `json:"enable_printers,omitempty"`
}

// UpdateSettings applique et persiste une modification (validée).
func (s *Service) UpdateSettings(ctx context.Context, p SettingsPatch) (Settings, error) {
	st := s.Settings(ctx)
	if p.Enabled != nil {
		st.Enabled = *p.Enabled
	}
	if p.IntervalSeconds != nil {
		if *p.IntervalSeconds < s.minInterval() || *p.IntervalSeconds > 3600 {
			return st, fmt.Errorf("%w : interval_seconds entre %g et 3600", ErrInvalid, s.minInterval())
		}
		st.IntervalSeconds = *p.IntervalSeconds
	}
	if p.Threshold != nil {
		if *p.Threshold < 0.1 || *p.Threshold > 1 {
			return st, fmt.Errorf("%w : threshold entre 0.1 et 1", ErrInvalid)
		}
		st.Detector.Threshold = *p.Threshold
	}
	if p.DiffThreshold != nil {
		if *p.DiffThreshold < 4 || *p.DiffThreshold > 100 {
			return st, fmt.Errorf("%w : diff_threshold entre 4 et 100", ErrInvalid)
		}
		st.Detector.DiffThreshold = *p.DiffThreshold
	}
	if p.PersistFrames != nil {
		if *p.PersistFrames < 1 || *p.PersistFrames > 10 {
			return st, fmt.Errorf("%w : persist_frames entre 1 et 10", ErrInvalid)
		}
		st.Detector.Persist = *p.PersistFrames
	}
	if p.StallMinutes != nil {
		if *p.StallMinutes < 1 || *p.StallMinutes > 600 {
			return st, fmt.Errorf("%w : stall_minutes entre 1 et 600", ErrInvalid)
		}
		st.Detector.StallMinutes = *p.StallMinutes
	}
	if p.ConsecutiveAlert != nil {
		if *p.ConsecutiveAlert < 1 || *p.ConsecutiveAlert > 20 {
			return st, fmt.Errorf("%w : consecutive_alerts entre 1 et 20", ErrInvalid)
		}
		st.Detector.Consecutive = *p.ConsecutiveAlert
	}
	if p.CooldownMinutes != nil {
		if *p.CooldownMinutes < 1 || *p.CooldownMinutes > 1440 {
			return st, fmt.Errorf("%w : alert_cooldown_minutes entre 1 et 1440", ErrInvalid)
		}
		st.AlertCooldownMin = *p.CooldownMinutes
	}
	if p.PhotoRetention != nil {
		if *p.PhotoRetention < 0 || *p.PhotoRetention > 3650 {
			return st, fmt.Errorf("%w : alert_photo_retention_days entre 0 et 3650", ErrInvalid)
		}
		st.AlertPhotoRetentionDays = *p.PhotoRetention
	}
	dis := map[string]bool{}
	for _, id := range st.DisabledPrinters {
		dis[id] = true
	}
	for _, id := range p.DisablePrinters {
		dis[id] = true
	}
	for _, id := range p.EnablePrinters {
		delete(dis, id)
	}
	st.DisabledPrinters = nil
	for id := range dis {
		st.DisabledPrinters = append(st.DisabledPrinters, id)
	}
	st.normalize(s.minInterval())
	doc, _ := json.Marshal(st)
	if _, err := s.c.Store.DB().ExecContext(ctx, `INSERT INTO camera_settings(key,doc,updated_at) VALUES('detection',?,?)
		ON CONFLICT(key) DO UPDATE SET doc=excluded.doc, updated_at=excluded.updated_at`, string(doc), store.FormatTime(s.now())); err != nil {
		return st, err
	}
	_ = s.c.Store.Audit(ctx, "camera", "camera.settings_updated", "camera_settings", "detection", st)
	s.setMu.Lock()
	s.settings = &st
	s.setMu.Unlock()
	return st, nil
}

// ---- client caméra ----

// Supported dit si le modèle de l'imprimante a une caméra que ce service sait lire.
func Supported(t Target) (printer.CameraKind, error) {
	k := printer.CameraKindFor(t.ModelID)
	if k == printer.CameraNone {
		k = printer.CameraKindFor(t.ModelName)
	}
	switch k {
	case printer.CameraJPEG:
		return k, nil
	case printer.CameraRTSP:
		return k, fmt.Errorf("%w : le modèle %s expose sa caméra en RTSP (rtsps://IP:322/streaming/live/1), non pris en charge ici (seuls les P1 et A1 le sont)", ErrUnavailable, firstNonEmpty(t.ModelName, t.ModelID))
	}
	return k, fmt.Errorf("%w : modèle %s : aucune caméra connue", ErrUnavailable, firstNonEmpty(t.ModelName, t.ModelID))
}

func firstNonEmpty(v ...string) string {
	for _, x := range v {
		if x != "" {
			return x
		}
	}
	return ""
}

func (s *Service) port() int {
	if s.c.Port > 0 {
		return s.c.Port
	}
	return printer.CameraDefaultPort
}

func (s *Service) cameraPin(ctx context.Context, t Target) string {
	var doc string
	if err := s.c.Store.DB().QueryRowContext(ctx, `SELECT doc FROM camera_settings WHERE key=?`, "pin:"+t.ID).Scan(&doc); err == nil {
		var p struct {
			SHA256 string `json:"sha256"`
		}
		if json.Unmarshal([]byte(doc), &p) == nil && p.SHA256 != "" {
			return p.SHA256
		}
	}
	return t.PinnedSHA256
}

func (s *Service) client(ctx context.Context, t Target) (*printer.CameraClient, error) {
	if _, err := Supported(t); err != nil {
		return nil, err
	}
	pin := s.cameraPin(ctx, t)
	if pin == "" {
		return nil, fmt.Errorf("%w : le certificat de l'imprimante n'est pas épinglé — l'humain doit valider son empreinte sur %s/setup/printer/%s", ErrUnavailable, s.c.Backend.BaseURL(), t.ID)
	}
	if t.Secrets == nil {
		return nil, fmt.Errorf("%w : code d'accès non enregistré", ErrUnavailable)
	}
	return printer.NewCameraClient(printer.CameraConfig{Host: t.Host, Port: s.port(), Secrets: t.Secrets, TLS: printer.TLSOptions{PinnedSHA256: pin}, Logger: s.log})
}

// friendly traduit une erreur du client caméra en message utile (sans secret).
func (s *Service) friendly(t Target, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrUnavailable):
		return err
	case errors.Is(err, printer.ErrCameraAuth):
		return fmt.Errorf("%w : %v. Sur la P1S : activer le « LAN liveview » dans les réglages réseau de l'écran, vérifier le code d'accès, fermer les autres clients vidéo", ErrUnavailable, err)
	case strings.Contains(err.Error(), "fingerprint"):
		return fmt.Errorf("%w : le certificat présenté sur le port caméra diffère de l'empreinte épinglée — l'humain peut valider l'empreinte de la caméra sur %s/setup/camera/%s", ErrUnavailable, s.c.Backend.BaseURL(), t.ID)
	}
	return fmt.Errorf("%w : %v", ErrUnavailable, err)
}

// ---- flux partagé (MJPEG) ----

type hub struct {
	s      *Service
	t      Target
	mu     sync.Mutex
	subs   map[int]chan Frame
	next   int
	latest *Frame
	cancel context.CancelFunc
	err    error
	closed bool
	idle   *time.Timer
}

// Stream est un abonnement au flux partagé d'une imprimante.
type Stream struct {
	C    <-chan Frame // se ferme à la fin du flux (erreur de la caméra ou arrêt)
	h    *hub
	stop func()
}

// Err donne la raison de la fin du flux (nil si arrêt normal).
func (st *Stream) Err() error {
	st.h.mu.Lock()
	defer st.h.mu.Unlock()
	return st.h.err
}

// Stop libère l'abonnement.
func (st *Stream) Stop() { st.stop() }

// Subscribe ouvre (ou rejoint) le flux partagé de l'imprimante : UNE seule connexion à la caméra quel que soit le nombre de
// spectateurs (la caméra d'une P1 en accepte peu). Le flux se ferme quand le dernier spectateur part.
func (s *Service) Subscribe(ctx context.Context, printerID string) (*Stream, error) {
	t, err := s.c.Backend.Target(ctx, printerID)
	if err != nil {
		return nil, err
	}
	cl, err := s.client(ctx, t)
	if err != nil {
		return nil, err
	}
	for {
		s.mu.Lock()
		h := s.hubs[printerID]
		if h == nil {
			hctx, cancel := context.WithCancel(context.Background())
			h = &hub{s: s, t: t, subs: map[int]chan Frame{}, cancel: cancel}
			s.hubs[printerID] = h
			s.wg.Add(1)
			go h.run(hctx, cl)
		}
		s.mu.Unlock()
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			continue
		}
		if h.idle != nil {
			h.idle.Stop()
			h.idle = nil
		}
		id := h.next
		h.next++
		ch := make(chan Frame, 2)
		h.subs[id] = ch
		if h.latest != nil && s.now().Sub(h.latest.At) < 2*time.Second {
			ch <- *h.latest
		}
		h.mu.Unlock()
		stop := func() {
			h.mu.Lock()
			if c, ok := h.subs[id]; ok {
				delete(h.subs, id)
				close(c)
			}
			if len(h.subs) == 0 && h.idle == nil && !h.closed {
				h.idle = time.AfterFunc(3*time.Second, func() { h.cancel() })
			}
			h.mu.Unlock()
		}
		return &Stream{C: ch, h: h, stop: stop}, nil
	}
}

func (h *hub) run(ctx context.Context, cl *printer.CameraClient) {
	defer h.s.wg.Done()
	defer func() {
		h.s.mu.Lock()
		if h.s.hubs[h.t.ID] == h {
			delete(h.s.hubs, h.t.ID)
		}
		h.s.mu.Unlock()
		h.mu.Lock()
		h.closed = true
		for id, c := range h.subs {
			close(c)
			delete(h.subs, id)
		}
		h.mu.Unlock()
		h.cancel()
	}()
	// La caméra libère un client à retardement : une connexion refusée juste après une autre est retentée une fois.
	var st *printer.CameraStream
	var firstJPG []byte
	for attempt := 0; ; attempt++ {
		var err error
		if st, err = cl.Open(ctx); err == nil {
			if firstJPG, err = st.Next(); err == nil {
				break
			}
			st.Close()
		}
		if attempt == 0 && errors.Is(err, printer.ErrCameraAuth) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
			continue
		}
		h.mu.Lock()
		h.err = h.s.friendly(h.t, err)
		h.mu.Unlock()
		return
	}
	defer st.Close()
	for ctx.Err() == nil {
		jpg, err := firstJPG, error(nil)
		if firstJPG != nil {
			firstJPG = nil
		} else {
			jpg, err = st.Next()
		}
		if err != nil {
			if ctx.Err() == nil {
				h.mu.Lock()
				h.err = h.s.friendly(h.t, err)
				h.mu.Unlock()
			}
			return
		}
		if _, err := checkJPEG(jpg); err != nil {
			continue // image hors limites : ignorée, jamais relayée ni décodée
		}
		f := Frame{JPEG: jpg, At: h.s.now()}
		h.mu.Lock()
		h.latest = &f
		for _, c := range h.subs {
			select {
			case c <- f:
			default: // abonné lent : on saute l'image
			}
		}
		h.mu.Unlock()
		h.s.remember(h.t.ID, f)
	}
}

func (s *Service) remember(printerID string, f Frame) {
	s.mu.Lock()
	s.lastFrame[printerID] = f
	s.mu.Unlock()
}

// frame renvoie une image récente : celle du flux partagé s'il tourne, sinon un instantané à connexion courte.
func (s *Service) frame(ctx context.Context, t Target, maxAge time.Duration) (Frame, error) {
	s.mu.Lock()
	h := s.hubs[t.ID]
	s.mu.Unlock()
	if h != nil {
		h.mu.Lock()
		l := h.latest
		h.mu.Unlock()
		if l != nil && s.now().Sub(l.At) <= maxAge {
			return *l, nil
		}
		// un flux est ouvert : la caméra n'accepte que peu de clients, on attend la prochaine image plutôt que d'en ouvrir un autre
		if st, err := s.Subscribe(ctx, t.ID); err == nil {
			defer st.Stop()
			select {
			case f, ok := <-st.C:
				if ok {
					return f, nil
				}
			case <-time.After(6 * time.Second):
			case <-ctx.Done():
				return Frame{}, ctx.Err()
			}
		}
	}
	cl, err := s.client(ctx, t)
	if err != nil {
		return Frame{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	jpg, err := cl.Snapshot(cctx)
	if errors.Is(err, printer.ErrCameraAuth) { // client précédent pas encore libéré côté caméra : un nouvel essai
		time.Sleep(500 * time.Millisecond)
		jpg, err = cl.Snapshot(cctx)
	}
	if err != nil {
		return Frame{}, s.friendly(t, err)
	}
	if _, err := checkJPEG(jpg); err != nil {
		return Frame{}, err
	}
	f := Frame{JPEG: jpg, At: s.now()}
	s.remember(t.ID, f)
	return f, nil
}

// ---- photos ----

// Capture prend une photo, l'enregistre (blob + ligne) et la renvoie. kind : snapshot | confirm | final | alert.
func (s *Service) Capture(ctx context.Context, printerID, kind, jobID string, maxAge time.Duration) (*Photo, error) {
	t, err := s.c.Backend.Target(ctx, printerID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	mu := s.snap[printerID]
	if mu == nil {
		mu = &sync.Mutex{}
		s.snap[printerID] = mu
	}
	s.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	f, err := s.frame(ctx, t, maxAge)
	if err != nil {
		return nil, err
	}
	cfg, err := checkJPEG(f.JPEG)
	if err != nil {
		return nil, err
	}
	if kind == "snapshot" { // instantané à la demande : renvoyé, jamais enregistré
		sum := sha256.Sum256(f.JPEG)
		return &Photo{PrinterID: printerID, JobID: jobID, Kind: kind, SHA256: hex.EncodeToString(sum[:]), Size: len(f.JPEG), TakenAt: f.At, Width: cfg.Width, Height: cfg.Height, JPEG: f.JPEG}, nil
	}
	p, err := s.store(ctx, printerID, kind, jobID, f)
	if p != nil {
		p.Width, p.Height = cfg.Width, cfg.Height
	}
	return p, err
}

func (s *Service) store(ctx context.Context, printerID, kind, jobID string, f Frame) (*Photo, error) {
	sum := sha256.Sum256(f.JPEG)
	sha := hex.EncodeToString(sum[:])
	if _, _, err := s.c.Blobs.Put(bytes.NewReader(f.JPEG), sha); err != nil {
		return nil, err
	}
	p := &Photo{ID: contracts.NewID("ph"), PrinterID: printerID, JobID: jobID, Kind: kind, SHA256: sha, Size: len(f.JPEG), TakenAt: f.At, JPEG: f.JPEG}
	p.URL = "/api/camera/photos/" + sha
	var jid any
	if jobID != "" {
		jid = jobID
	}
	if _, err := s.c.Store.DB().ExecContext(ctx, `INSERT INTO camera_photos(id,printer_id,print_job_id,kind,sha256,size,taken_at) VALUES(?,?,?,?,?,?,?)`,
		p.ID, printerID, jid, kind, sha, p.Size, store.FormatTime(p.TakenAt)); err != nil {
		return nil, err
	}
	return p, nil
}

// OpenPhoto renvoie le contenu d'une photo enregistrée (jamais un autre blob du stockage).
func (s *Service) OpenPhoto(ctx context.Context, sha string) ([]byte, error) {
	var n int
	if s.c.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM camera_photos WHERE sha256=?`, sha).Scan(&n) != nil || n == 0 {
		return nil, fmt.Errorf("%w : photo", ErrNotFound)
	}
	f, err := s.c.Blobs.Open(sha)
	if err != nil {
		return nil, fmt.Errorf("%w : photo", ErrNotFound)
	}
	defer f.Close()
	var b bytes.Buffer
	if _, err := b.ReadFrom(f); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// PhotosForJob liste les photos rattachées à une impression (photo de fin, alertes), plus anciennes d'abord.
func (s *Service) PhotosForJob(ctx context.Context, jobID string) ([]Photo, error) {
	rows, err := s.c.Store.DB().QueryContext(ctx, `SELECT id,printer_id,kind,sha256,size,taken_at FROM camera_photos WHERE print_job_id=? ORDER BY taken_at`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Photo{}
	for rows.Next() {
		var p Photo
		var at string
		if err := rows.Scan(&p.ID, &p.PrinterID, &p.Kind, &p.SHA256, &p.Size, &at); err != nil {
			return nil, err
		}
		p.JobID, p.URL = jobID, "/api/camera/photos/"+p.SHA256
		p.TakenAt, _ = store.ParseTime(at)
		out = append(out, p)
	}
	return out, rows.Err()
}

// FinalPhoto prend (ou relit) la photo de fin d'impression et la rattache à l'impression. Idempotent. Implémente notify.PhotoSource.
func (s *Service) FinalPhoto(ctx context.Context, printerID, jobID string) ([]byte, string, error) {
	s.mu.Lock()
	mu := s.final[jobID]
	if mu == nil {
		mu = &sync.Mutex{}
		s.final[jobID] = mu
	}
	s.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	var sha string
	err := s.c.Store.DB().QueryRowContext(ctx, `SELECT sha256 FROM camera_photos WHERE print_job_id=? AND kind='final' ORDER BY taken_at DESC LIMIT 1`, jobID).Scan(&sha)
	if err == nil {
		if b, e := s.OpenPhoto(ctx, sha); e == nil {
			return b, "/api/camera/photos/" + sha, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}
	p, err := s.Capture(ctx, printerID, "final", jobID, 3*time.Second)
	if err != nil {
		return nil, "", err
	}
	return p.JPEG, p.URL, nil
}

// PlatePhoto prend la photo du plateau montrée sur /confirm/{id} (printing.SetPlatePhotoProvider). Elle sert aussi de
// référence « plateau vide » à la détection d'échec de l'impression qui suit.
func (s *Service) PlatePhoto(ctx context.Context, printerID string) (url string, takenAt time.Time, err error) {
	cctx, cancel := context.WithTimeout(ctx, 14*time.Second)
	defer cancel()
	p, err := s.Capture(cctx, printerID, "confirm", "", time.Second)
	if err != nil {
		return "", time.Time{}, err
	}
	s.mu.Lock()
	s.refs[printerID] = refImage{at: p.TakenAt, jpeg: p.JPEG}
	s.mu.Unlock()
	return p.URL, p.TakenAt, nil
}

// ---- alertes ----

// Alerts liste les alertes d'une impression (la plus récente d'abord).
func (s *Service) Alerts(ctx context.Context, jobID string, limit int) ([]Alert, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q := `SELECT id,printer_id,print_job_id,at,score,level,report,COALESCE(photo_sha256,'') FROM camera_alerts`
	args := []any{}
	if jobID != "" {
		q += ` WHERE print_job_id=?`
		args = append(args, jobID)
	}
	q += ` ORDER BY at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.c.Store.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var a Alert
		var at, rep, sha string
		if err := rows.Scan(&a.ID, &a.PrinterID, &a.JobID, &at, &a.Score, &a.Level, &rep, &sha); err != nil {
			return nil, err
		}
		a.At, _ = store.ParseTime(at)
		_ = json.Unmarshal([]byte(rep), &a.Report)
		if sha != "" {
			a.PhotoURL = "/api/camera/photos/" + sha
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Close attend la fin des flux et des surveillances.
func (s *Service) Close() {
	s.mu.Lock()
	for _, h := range s.hubs {
		h.cancel()
	}
	for _, m := range s.mons {
		m.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func decodeJPEG(b []byte) (image.Image, error) {
	if _, err := checkJPEG(b); err != nil {
		return nil, err
	}
	return jpeg.Decode(bytes.NewReader(b))
}

// PurgeOldPhotos supprime les photos d'alerte (et d'anciens instantanés) plus vieilles que la rétention réglée, avec leur
// contenu quand plus rien ne le référence. Les photos de fin d'impression et de confirmation sont conservées.
func (s *Service) PurgeOldPhotos(ctx context.Context) (int, error) {
	days := s.Settings(ctx).AlertPhotoRetentionDays
	if days <= 0 {
		return 0, nil
	}
	cutoff := store.FormatTime(s.now().AddDate(0, 0, -days))
	rows, err := s.c.Store.DB().QueryContext(ctx, `SELECT id,sha256 FROM camera_photos WHERE kind IN ('alert','snapshot') AND taken_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	type row struct{ id, sha string }
	var old []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.sha); err != nil {
			rows.Close()
			return 0, err
		}
		old = append(old, r)
	}
	rows.Close()
	n := 0
	for _, r := range old {
		if _, err := s.c.Store.DB().ExecContext(ctx, `DELETE FROM camera_photos WHERE id=?`, r.id); err != nil {
			return n, err
		}
		n++
		_, _ = s.c.Store.DB().ExecContext(ctx, `UPDATE camera_alerts SET photo_sha256=NULL WHERE photo_sha256=? AND at < ?`, r.sha, cutoff)
		var left int
		if s.c.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM camera_photos WHERE sha256=?`, r.sha).Scan(&left) == nil && left == 0 {
			_, _ = s.c.Store.DB().ExecContext(ctx, `UPDATE camera_alerts SET photo_sha256=NULL WHERE photo_sha256=?`, r.sha)
			if err := s.c.Blobs.Delete(r.sha); err != nil {
				s.log.Warn("purge des photos : contenu non supprimé", "err", err)
			}
		}
	}
	if n > 0 {
		_ = s.c.Store.Audit(ctx, "camera", "camera.photos_purged", "camera_photos", "alert", map[string]any{"count": n, "older_than_days": days})
	}
	return n, nil
}

func (s *Service) minInterval() float64 {
	if s.c.MinInterval > 0 {
		return s.c.MinInterval
	}
	return 5
}
