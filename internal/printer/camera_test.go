package printer_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image/jpeg"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
)

func startCamSim(t *testing.T, cfg sim.CameraConfig) (*sim.Sim, *sim.Camera) {
	t.Helper()
	s, err := sim.New(sim.Config{ControlPort: -1, AccessCode: "12345678"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if cfg.FrameEvery == 0 {
		cfg.FrameEvery = 30 * time.Millisecond
	}
	c, err := s.StartCamera(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return s, c
}

func camClient(t *testing.T, s *sim.Sim, c *sim.Camera, code, pin string) *printer.CameraClient {
	t.Helper()
	host, _ := splitHostPort(c.Addr())
	cl, err := printer.NewCameraClient(printer.CameraConfig{Host: host, Port: c.Port(), Secrets: printer.StaticSecret(code),
		TLS: printer.TLSOptions{PinnedSHA256: pin}, FrameTimeout: 3 * time.Second, DialTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return cl
}

func splitHostPort(a string) (string, string) {
	for i := len(a) - 1; i >= 0; i-- {
		if a[i] == ':' {
			return a[:i], a[i+1:]
		}
	}
	return a, ""
}

func TestCameraSnapshotAgainstSimulator(t *testing.T) {
	s, c := startCamSim(t, sim.CameraConfig{})
	cl := camClient(t, s, c, "12345678", s.Fingerprint())
	jpg, err := cl.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(jpg))
	if err != nil || cfg.Width != 1280 || cfg.Height != 720 {
		t.Fatalf("JPEG 1280x720 attendu : %v %+v", err, cfg)
	}
	if c.Frames() == 0 || c.Connections() != 1 {
		t.Fatalf("frames=%d connexions=%d", c.Frames(), c.Connections())
	}
}

func TestCameraStreamDeliversSeveralFrames(t *testing.T) {
	s, c := startCamSim(t, sim.CameraConfig{})
	cl := camClient(t, s, c, "12345678", s.Fingerprint())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := cl.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := 0; i < 4; i++ {
		if _, err := st.Next(); err != nil {
			t.Fatalf("image %d : %v", i, err)
		}
	}
}

func TestCameraWrongAccessCodeIsRefusedWithExplicitError(t *testing.T) {
	s, c := startCamSim(t, sim.CameraConfig{})
	cl := camClient(t, s, c, "00000000", s.Fingerprint())
	_, err := cl.Snapshot(context.Background())
	if !errors.Is(err, printer.ErrCameraAuth) {
		t.Fatalf("ErrCameraAuth attendu, reçu %v", err)
	}
	if c.AuthFailures() != 1 {
		t.Fatalf("échecs d'authentification : %d", c.AuthFailures())
	}
}

func TestCameraRefusesAnUnpinnedCertificate(t *testing.T) {
	s, c := startCamSim(t, sim.CameraConfig{})
	cl := camClient(t, s, c, "12345678", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	_, err := cl.Snapshot(context.Background())
	if err == nil || errors.Is(err, printer.ErrCameraAuth) {
		t.Fatalf("empreinte différente : erreur de certificat attendue, reçu %v", err)
	}
	if c.Frames() != 0 {
		t.Fatal("aucune image ne doit être envoyée à un client dont l'empreinte ne correspond pas")
	}
}

func TestCameraTooManyClientsAndFaultDown(t *testing.T) {
	s, c := startCamSim(t, sim.CameraConfig{MaxClients: 1})
	cl := camClient(t, s, c, "12345678", s.Fingerprint())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, err := cl.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := first.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Snapshot(ctx); !errors.Is(err, printer.ErrCameraAuth) {
		t.Fatalf("caméra occupée : %v", err)
	}
	if c.BusyRefused() != 1 {
		t.Fatalf("refus pour saturation : %d", c.BusyRefused())
	}
	first.Close()
	time.Sleep(100 * time.Millisecond)
	if err := c.SetFault(sim.CameraFaultDown, true); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Snapshot(ctx); err == nil {
		t.Fatal("caméra hors service : erreur attendue")
	}
	if c.SetFault("nimporte", true) == nil {
		t.Fatal("panne inconnue acceptée")
	}
}

func TestCameraProtocolPackets(t *testing.T) {
	p, err := printer.BuildCameraAuthPacket("bblp", "secret12")
	if err != nil || len(p) != 80 {
		t.Fatalf("%v %d", err, len(p))
	}
	if p[0] != 0x40 || p[4] != 0x00 || p[5] != 0x30 {
		t.Fatalf("en-tête du paquet d'authentification : % x", p[:8])
	}
	u, pw, err := printer.ParseCameraAuthPacket(p)
	if err != nil || u != "bblp" || pw != "secret12" {
		t.Fatalf("%q %q %v", u, pw, err)
	}
	if _, err := printer.BuildCameraAuthPacket("bblp", string(make([]byte, 33))); err == nil {
		t.Fatal("mot de passe trop long accepté")
	}
	f := printer.BuildCameraFrame([]byte{0xFF, 0xD8, 1, 2, 0xFF, 0xD9})
	if len(f) != 16+6 || f[0] != 6 || f[8] != 1 {
		t.Fatalf("trame : % x", f)
	}
}

func TestCameraKindForModels(t *testing.T) {
	for m, want := range map[string]printer.CameraKind{"p1s": printer.CameraJPEG, "Bambu Lab P1P": printer.CameraJPEG, "a1-mini": printer.CameraJPEG, "Bambu Lab A1": printer.CameraJPEG,
		"x1c": printer.CameraRTSP, "Bambu Lab X1 Carbon": printer.CameraRTSP, "h2d": printer.CameraRTSP, "p2s": printer.CameraRTSP, "foo": printer.CameraNone} {
		if got := printer.CameraKindFor(m); got != want {
			t.Errorf("%s : %s, attendu %s", m, got, want)
		}
	}
}

// badServer : serveur TLS qui répond au paquet d'authentification par les octets fournis.
func badServer(t *testing.T, reply []byte) (host string, port int, pin string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "bad"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 80)
				_, _ = io.ReadFull(c, buf)
				_, _ = c.Write(reply)
				time.Sleep(200 * time.Millisecond)
			}()
		}
	}()
	sum := sha256.Sum256(der)
	return "127.0.0.1", ln.Addr().(*net.TCPAddr).Port, hex.EncodeToString(sum[:])
}

func TestCameraRejectsMalformedFrames(t *testing.T) {
	huge := make([]byte, 16)
	binary.LittleEndian.PutUint32(huge, 64<<20) // 64 Mio annoncés : refusé avant toute allocation
	notJPEG := append(printer.BuildCameraFrame([]byte("ceci n'est pas un JPEG")), 0)
	for name, reply := range map[string][]byte{"taille démesurée": huge, "contenu non JPEG": notJPEG} {
		host, port, pin := badServer(t, reply)
		cl, err := printer.NewCameraClient(printer.CameraConfig{Host: host, Port: port, Secrets: printer.StaticSecret("12345678"), TLS: printer.TLSOptions{PinnedSHA256: pin}, FrameTimeout: 2 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cl.Snapshot(context.Background()); !errors.Is(err, printer.ErrCameraFrame) {
			t.Errorf("%s : ErrCameraFrame attendu, reçu %v", name, err)
		}
	}
}
