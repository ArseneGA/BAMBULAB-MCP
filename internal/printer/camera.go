package printer

// Client du flux caméra des Bambu P1 / A1 (docs/research/bambu-lan-protocol.md §5, OpenBambuAPI video.md) :
// serveur TCP + TLS sur le port 6000 de l'imprimante, qui diffuse des images JPEG (1280x720).
//
//	client -> imprimante, une fois : paquet d'authentification de 80 octets
//	    0x40 0 0 0 | 0x00 0x30 0 0 | 0 0 0 0 | 0 0 0 0 | utilisateur (32 o) | mot de passe (32 o)   (entiers en LE, texte ASCII complété de zéros)
//	imprimante -> client, en boucle : en-tête de 16 octets (taille du JPEG en uint32 LE, itrack, flags=1, 0) + JPEG (FFD8 ... FFD9)
//
// Ce que ce client NE fait PAS : RTSP (X1, P2S, H2 : rtsps://IP:322/streaming/live/1), voir CameraKindFor.
// Validé contre le simulateur seulement (internal/printer/sim/camera.go) : aucune imprimante réelle n'a été contactée.
// À valider sur la P1S : le certificat présenté sur le port 6000 (même empreinte que MQTT ?), le comportement quand
// le « LAN liveview » est désactivé et le nombre de clients simultanés acceptés.

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"
)

// Constantes du protocole caméra JPEG.
const (
	CameraDefaultPort     = 6000
	CameraAuthPacketSize  = 80
	CameraAuthPayloadSize = 0x40
	CameraAuthType        = 0x3000
	CameraFrameHeaderSize = 16
	// CameraMaxFrame borne la taille d'une image acceptée (un JPEG 1280x720 fait ~50-300 Kio).
	CameraMaxFrame = 4 << 20
)

// CameraKind dit comment un modèle expose sa caméra.
type CameraKind string

const (
	CameraJPEG CameraKind = "jpeg_tls_6000" // A1, P1 : pris en charge
	CameraRTSP CameraKind = "rtsps_322"     // X1, P2S, H2S/H2D : non pris en charge
	CameraNone CameraKind = "none"
)

// CameraKindFor déduit le mode d'accès caméra depuis l'identifiant ou le nom du modèle (« p1s », « Bambu Lab A1 mini »...).
func CameraKindFor(model string) CameraKind {
	m := strings.ToLower(strings.TrimSpace(model))
	m = strings.TrimPrefix(m, "bambu lab ")
	m = strings.TrimPrefix(m, "bambulab ")
	m = strings.TrimPrefix(m, "bambu-lab-")
	m = strings.TrimSpace(m)
	switch {
	case strings.HasPrefix(m, "p1"), strings.HasPrefix(m, "a1"):
		return CameraJPEG
	case strings.HasPrefix(m, "x1"), strings.HasPrefix(m, "p2"), strings.HasPrefix(m, "h2"):
		return CameraRTSP
	}
	return CameraNone
}

var (
	// ErrCameraUnsupported : le modèle expose RTSP (ou rien) ; ce client ne parle que le flux JPEG des P1/A1.
	ErrCameraUnsupported = errors.New("printer: camera protocol not supported for this model (RTSP not implemented)")
	// ErrCameraAuth : connexion fermée avant la première image (code refusé, liveview LAN désactivé, caméra occupée).
	ErrCameraAuth = errors.New("printer: camera closed the connection before the first frame (access code refused, LAN liveview disabled or camera busy)")
	// ErrCameraFrame : image mal formée.
	ErrCameraFrame = errors.New("printer: invalid camera frame")
)

// CameraConfig configure le client caméra. La politique TLS est celle de l'adaptateur (empreinte épinglée).
type CameraConfig struct {
	Host         string
	Port         int    // défaut 6000
	Username     string // défaut bblp
	Secrets      SecretProvider
	TLS          TLSOptions
	Logger       *slog.Logger
	DialTimeout  time.Duration // défaut 8 s
	FrameTimeout time.Duration // défaut 10 s entre deux images
	MaxFrame     int           // défaut CameraMaxFrame
}

// CameraClient ouvre des flux d'images.
type CameraClient struct {
	cfg CameraConfig
	tls *tls.Config
}

// NewCameraClient valide la configuration (aucune connexion).
func NewCameraClient(cfg CameraConfig) (*CameraClient, error) {
	if cfg.Host == "" || cfg.Secrets == nil {
		return nil, errors.New("printer: camera Host and Secrets are required")
	}
	if cfg.Port == 0 {
		cfg.Port = CameraDefaultPort
	}
	if cfg.Username == "" {
		cfg.Username = "bblp"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 8 * time.Second
	}
	if cfg.FrameTimeout == 0 {
		cfg.FrameTimeout = 10 * time.Second
	}
	if cfg.MaxFrame == 0 {
		cfg.MaxFrame = CameraMaxFrame
	}
	tc, err := buildTLS(cfg.Host, cfg.TLS, cfg.Logger)
	if err != nil {
		return nil, err
	}
	return &CameraClient{cfg: cfg, tls: tc}, nil
}

// Addr est l'adresse host:port jointe.
func (c *CameraClient) Addr() string {
	return net.JoinHostPort(c.cfg.Host, strconv.Itoa(c.cfg.Port))
}

// BuildCameraAuthPacket construit le paquet d'authentification de 80 octets.
func BuildCameraAuthPacket(user, pass string) ([]byte, error) {
	if len(user) > 32 || len(pass) > 32 {
		return nil, errors.New("printer: camera credentials longer than 32 bytes")
	}
	b := make([]byte, CameraAuthPacketSize)
	binary.LittleEndian.PutUint32(b[0:], CameraAuthPayloadSize)
	binary.LittleEndian.PutUint32(b[4:], CameraAuthType)
	copy(b[16:48], user)
	copy(b[48:80], pass)
	return b, nil
}

// ParseCameraAuthPacket décode un paquet d'authentification (simulateur, tests).
func ParseCameraAuthPacket(b []byte) (user, pass string, err error) {
	if len(b) != CameraAuthPacketSize || binary.LittleEndian.Uint32(b[0:]) != CameraAuthPayloadSize || binary.LittleEndian.Uint32(b[4:]) != CameraAuthType {
		return "", "", errors.New("printer: bad camera auth packet")
	}
	trim := func(x []byte) string { return string(bytes.TrimRight(x, "\x00")) }
	return trim(b[16:48]), trim(b[48:80]), nil
}

// BuildCameraFrame emballe un JPEG dans l'en-tête de 16 octets (simulateur, tests).
func BuildCameraFrame(jpeg []byte) []byte {
	b := make([]byte, CameraFrameHeaderSize+len(jpeg))
	binary.LittleEndian.PutUint32(b[0:], uint32(len(jpeg)))
	binary.LittleEndian.PutUint32(b[8:], 1)
	copy(b[CameraFrameHeaderSize:], jpeg)
	return b
}

// CameraStream est un flux ouvert d'images JPEG.
type CameraStream struct {
	conn    net.Conn
	cfg     *CameraConfig
	gotOne  bool
	stopCtx func() bool
}

// Open se connecte (TLS, empreinte épinglée), s'authentifie et renvoie le flux. Le contexte coupe la connexion.
func (c *CameraClient) Open(ctx context.Context) (*CameraStream, error) {
	code, err := c.cfg.Secrets.AccessCode(ctx)
	if err != nil {
		return nil, fmt.Errorf("printer: access code unavailable: %w", err)
	}
	auth, err := BuildCameraAuthPacket(c.cfg.Username, code)
	if err != nil {
		return nil, err
	}
	d := tls.Dialer{Config: c.tls, NetDialer: &net.Dialer{Timeout: c.cfg.DialTimeout}}
	dctx, cancel := context.WithTimeout(ctx, c.cfg.DialTimeout)
	defer cancel()
	conn, err := d.DialContext(dctx, "tcp", c.Addr())
	if err != nil {
		return nil, fmt.Errorf("printer: camera connection: %w", err)
	}
	_ = conn.SetWriteDeadline(time.Now().Add(c.cfg.DialTimeout))
	if _, err := conn.Write(auth); err != nil {
		conn.Close()
		return nil, fmt.Errorf("printer: camera auth write: %w", err)
	}
	_ = conn.SetWriteDeadline(time.Time{})
	s := &CameraStream{conn: conn, cfg: &c.cfg}
	s.stopCtx = context.AfterFunc(ctx, func() { conn.Close() })
	return s, nil
}

// Next lit l'image suivante (JPEG complet, vérifié).
func (s *CameraStream) Next() ([]byte, error) {
	_ = s.conn.SetReadDeadline(time.Now().Add(s.cfg.FrameTimeout))
	var hdr [CameraFrameHeaderSize]byte
	if _, err := io.ReadFull(s.conn, hdr[:]); err != nil {
		if !s.gotOne && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || isReset(err)) {
			return nil, ErrCameraAuth
		}
		return nil, fmt.Errorf("printer: camera read: %w", err)
	}
	n := int(binary.LittleEndian.Uint32(hdr[0:]))
	if n < 4 || n > s.cfg.MaxFrame {
		return nil, fmt.Errorf("%w: size %d", ErrCameraFrame, n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(s.conn, buf); err != nil {
		return nil, fmt.Errorf("printer: camera read: %w", err)
	}
	if buf[0] != 0xFF || buf[1] != 0xD8 || buf[n-2] != 0xFF || buf[n-1] != 0xD9 {
		return nil, fmt.Errorf("%w: not a JPEG", ErrCameraFrame)
	}
	s.gotOne = true
	return buf, nil
}

// Close ferme le flux.
func (s *CameraStream) Close() error {
	if s.stopCtx != nil {
		s.stopCtx()
	}
	return s.conn.Close()
}

func isReset(err error) bool {
	if err == nil {
		return false
	}
	m := err.Error()
	return strings.Contains(m, "reset by peer") || strings.Contains(m, "forcibly closed") || strings.Contains(m, "broken pipe")
}

// Snapshot ouvre le flux, lit une image et ferme.
func (c *CameraClient) Snapshot(ctx context.Context) ([]byte, error) {
	s, err := c.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return s.Next()
}
