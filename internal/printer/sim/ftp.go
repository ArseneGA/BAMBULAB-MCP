package sim

import (
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"net"
	"strconv"

	ftpserver "github.com/fclairamb/ftpserverlib"
	"github.com/spf13/afero"
)

// ftpHandle regroupe le serveur FTPS à TLS implicite (ftpserverlib).
type ftpHandle struct{ srv *ftpserver.FtpServer }

func (h *ftpHandle) stop() { _ = h.srv.Stop() }

type ftpDriver struct {
	s      *Sim
	tlsCfg *tls.Config
	ln     net.Listener
}

func (d *ftpDriver) GetSettings() (*ftpserver.Settings, error) {
	return &ftpserver.Settings{
		Listener:    d.ln, // déjà enveloppé dans le TLS implicite et dans la « gate »
		PublicHost:  "127.0.0.1",
		TLSRequired: ftpserver.ImplicitEncryption,
		Banner:      "Bambu FTPS simulator",
		IdleTimeout: 60,
		DisableMLSD: false,
		DisableSite: true,
	}, nil
}

func (d *ftpDriver) ClientConnected(ftpserver.ClientContext) (string, error) {
	return "sim ready", nil
}
func (d *ftpDriver) ClientDisconnected(ftpserver.ClientContext) {}
func (d *ftpDriver) GetTLSConfig() (*tls.Config, error)         { return d.tlsCfg, nil }

func (d *ftpDriver) AuthUser(_ ftpserver.ClientContext, user, pass string) (ftpserver.ClientDriver, error) {
	if subtle.ConstantTimeCompare([]byte(user), []byte("bblp")) != 1 ||
		subtle.ConstantTimeCompare([]byte(pass), []byte(d.s.cfg.AccessCode)) != 1 {
		return nil, errors.New("login incorrect")
	}
	return d.s.fs, nil
}

func (s *Sim) startFTP(tlsCfg *tls.Config) error {
	raw, err := net.Listen("tcp", net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.FTPPort)))
	if err != nil {
		return err
	}
	s.ftpGate = newGate(raw)
	s.ftpAddr = raw.Addr().String()
	drv := &ftpDriver{s: s, tlsCfg: tlsCfg, ln: tls.NewListener(s.ftpGate, tlsCfg)}
	srv := ftpserver.NewFtpServer(drv)
	if err := srv.Listen(); err != nil {
		return err
	}
	s.ftp = &ftpHandle{srv: srv}
	go func() { _ = srv.Serve() }()
	return nil
}

var _ afero.Fs = (afero.Fs)(nil)
