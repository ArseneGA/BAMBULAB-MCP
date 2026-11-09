package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// localDirectRequest : la connexion TCP vient du loopback ET la requête ne porte aucun en-tête de proxy
// (Forwarded, X-Forwarded-*, X-Real-Ip, Via, Tailscale-*). Un client distant passant par un proxy local
// (tailscale serve, Caddy...) arrive en loopback mais avec ces en-têtes : il est refusé.
func localDirectRequest(r *http.Request) bool {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	a, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil || !a.Unmap().IsLoopback() {
		return false
	}
	for k := range r.Header {
		lk := strings.ToLower(k)
		if lk == "forwarded" || lk == "x-real-ip" || lk == "via" || strings.HasPrefix(lk, "x-forwarded-") || strings.HasPrefix(lk, "tailscale-") {
			return false
		}
	}
	return true
}
