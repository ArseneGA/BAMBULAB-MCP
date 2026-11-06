package sim

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"sync"
	"time"
)

// newCert génère un certificat auto-signé ECDSA pour 127.0.0.1 / localhost.
func newCert(cn string) (tls.Certificate, []byte, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, "", err
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost", cn},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, "", err
	}
	sum := sha256.Sum256(der)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pemBytes, hex.EncodeToString(sum[:]), nil
}

// gate enveloppe un net.Listener pour simuler « hors ligne » et coupures :
// suivi des connexions, refus pendant offline, DropAll.
type gate struct {
	net.Listener
	mu      sync.Mutex
	offline bool
	conns   map[net.Conn]struct{}
}

func newGate(l net.Listener) *gate { return &gate{Listener: l, conns: map[net.Conn]struct{}{}} }

func (g *gate) Accept() (net.Conn, error) {
	for {
		c, err := g.Listener.Accept()
		if err != nil {
			return nil, err
		}
		g.mu.Lock()
		if g.offline {
			g.mu.Unlock()
			_ = c.Close()
			continue
		}
		tc := &trackedConn{Conn: c, g: g}
		g.conns[tc] = struct{}{}
		g.mu.Unlock()
		return tc, nil
	}
}

func (g *gate) setOffline(v bool) {
	g.mu.Lock()
	g.offline = v
	g.mu.Unlock()
	if v {
		g.DropAll()
	}
}

// DropAll ferme brutalement toutes les connexions actives.
func (g *gate) DropAll() {
	g.mu.Lock()
	cs := make([]net.Conn, 0, len(g.conns))
	for c := range g.conns {
		cs = append(cs, c)
	}
	g.mu.Unlock()
	for _, c := range cs {
		_ = c.Close()
	}
}

type trackedConn struct {
	net.Conn
	g *gate
}

func (t *trackedConn) Close() error {
	t.g.mu.Lock()
	delete(t.g.conns, t)
	t.g.mu.Unlock()
	return t.Conn.Close()
}
