package notify

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
)

// Garde réseau des notifications : par défaut, ni ntfy, ni webhook, ni SMTP ne peuvent viser la boucle locale, un réseau
// privé ou une adresse de lien local (SSRF, exfiltration vers le réseau interne). La vérification a lieu à la saisie
// (adresses littérales, noms évidents) ET à la connexion, sur l'adresse réellement résolue (anti-rebond DNS).
// BAMBU_NOTIFY_ALLOW_PRIVATE=1 lève la garde pour un ntfy ou un SMTP local choisi par l'humain.

// smtpPorts : les seuls ports SMTP acceptés hors option explicite.
var smtpPorts = map[int]bool{25: true, 465: true, 587: true, 2525: true}

var errPrivateDest = errors.New("destination non publique refusée")

// allowPrivate : option explicite (Deps ou BAMBU_NOTIFY_ALLOW_PRIVATE=1, relue à chaque contrôle).
func (s *Service) allowPrivate() bool {
	return s.d.AllowPrivate || os.Getenv("BAMBU_NOTIFY_ALLOW_PRIVATE") == "1"
}

func privateName(host string) bool {
	h := strings.ToLower(strings.Trim(strings.TrimSuffix(host, "."), "[]"))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") ||
		strings.HasSuffix(h, ".lan") || strings.HasSuffix(h, ".home.arpa") || !strings.Contains(h, ".") && !strings.Contains(h, ":") {
		return true
	}
	if a, err := netip.ParseAddr(h); err == nil {
		return !catalog.PublicIP(net.IP(a.AsSlice()))
	}
	return false
}

// checkSMTPDestination valide hôte et port d'un canal e-mail.
func (s *Service) checkSMTPDestination(host string, port int) error {
	if s.allowPrivate() {
		return nil
	}
	if privateName(host) {
		return invalidf("smtp_host : une adresse locale, privée ou de lien local est refusée (BAMBU_NOTIFY_ALLOW_PRIVATE=1 pour un SMTP local)")
	}
	if !smtpPorts[port] {
		return invalidf("smtp_port : 25, 465, 587 ou 2525 seulement (BAMBU_NOTIFY_ALLOW_PRIVATE=1 pour un autre port)")
	}
	return nil
}

// checkURL valide l'URL d'un ntfy ou d'un webhook.
func (s *Service) checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return invalidf("URL invalide : http(s)://hôte/chemin attendu, sans identifiants intégrés")
	}
	if !s.allowPrivate() && privateName(u.Hostname()) {
		return invalidf("URL : une adresse locale, privée ou de lien local est refusée (BAMBU_NOTIFY_ALLOW_PRIVATE=1 pour un ntfy local)")
	}
	return nil
}

// dialer renvoie un dialer qui refuse, au moment de la connexion, toute adresse résolue non publique.
func (s *Service) dialer(timeout time.Duration) *net.Dialer {
	d := &net.Dialer{Timeout: timeout}
	d.Control = func(_, address string, _ syscall.RawConn) error {
		if s.allowPrivate() {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return errPrivateDest
		}
		ip := net.ParseIP(host)
		if ip == nil || !catalog.PublicIP(ip) {
			return errPrivateDest
		}
		return nil
	}
	return d
}

var addrInText = regexp.MustCompile(`(?i)\b(?:\d{1,3}(?:\.\d{1,3}){3}|\[?[0-9a-f:]*:[0-9a-f:]+\]?)(?::\d{1,5})?\b|\b[a-z0-9-]+(?:\.[a-z0-9-]+)+:\d{1,5}\b`)

// netErrMessage réduit une erreur réseau à une phrase sans adresse, port ni nom d'hôte : le détail ne sort jamais vers le modèle.
func netErrMessage(err error) string {
	var ua x509.UnknownAuthorityError
	var hn x509.HostnameError
	var ci x509.CertificateInvalidError
	var ne net.Error
	switch {
	case errors.Is(err, errPrivateDest):
		return "destination non publique refusée"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return "délai dépassé"
	case errors.As(err, &ua) || errors.As(err, &hn) || errors.As(err, &ci):
		return "certificat non vérifié"
	case errors.As(err, new(*net.OpError)) || errors.As(err, new(*net.DNSError)) || errors.As(err, &ne):
		return "connexion impossible"
	}
	return ""
}
