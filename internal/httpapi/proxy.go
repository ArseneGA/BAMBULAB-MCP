package httpapi

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// ParseTrustedProxies lit une liste séparée par des virgules d'adresses IP ou de réseaux CIDR
// (« 127.0.0.1, 10.0.0.0/8 »). Les mots-clés « loopback » (127.0.0.0/8 et ::1) et « none » (aucun) sont
// acceptés. Liste vide : nil, ce qui signifie « loopback seulement » (voir trustedProxy).
func ParseTrustedProxies(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		switch strings.ToLower(part) {
		case "":
			continue
		case "none":
			return []netip.Prefix{}, nil
		case "loopback":
			out = append(out, netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128"))
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("proxy de confiance %q invalide : attendu une adresse IP ou un réseau CIDR", part)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

// trustedProxy dit si l'adresse distante de la connexion TCP est un proxy de confiance. Sans configuration
// (TrustedProxies nil), seul le loopback l'est ; une liste vide non nil (« none ») n'en admet aucun.
func (o Options) trustedProxy(remoteAddr string) bool {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	a, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	a = a.Unmap()
	if o.TrustedProxies == nil {
		return a.IsLoopback()
	}
	for _, p := range o.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// clientAddr donne l'adresse du client : l'adresse TCP, ou, si elle est celle d'un proxy de confiance,
// l'entrée X-Forwarded-For la plus à droite qui n'est pas elle-même un proxy de confiance (les entrées de
// gauche sont forgeables par le client). Aucun en-tête n'est lu sur une connexion qui ne vient pas d'un proxy de confiance.
func (o Options) clientAddr(r *http.Request) string {
	remote := r.RemoteAddr
	if h, _, err := net.SplitHostPort(remote); err == nil {
		remote = h
	}
	if !o.trustedProxy(r.RemoteAddr) {
		return remote
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				hops = append(hops, p)
			}
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		if _, err := netip.ParseAddr(strings.Trim(hops[i], "[]")); err != nil {
			return remote // entrée illisible : on ne devine pas
		}
		if !o.trustedProxy(hops[i]) {
			return strings.Trim(hops[i], "[]")
		}
	}
	return remote
}

// publicURLs renvoie les URL publiques configurées (PublicURLs, sinon PublicURL).
func (o Options) publicURLs() []string {
	if len(o.PublicURLs) > 0 {
		return o.PublicURLs
	}
	if o.PublicURL != "" {
		return []string{o.PublicURL}
	}
	return nil
}

// requestHTTPS dit si la requête arrive en https du point de vue du navigateur : TLS direct, ou
// X-Forwarded-Proto d'un proxy de confiance (jamais cru ailleurs), sinon le schéma de l'URL publique dont
// l'hôte correspond au Host de la requête, sinon celui de la première URL publique.
func (o Options) requestHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if o.trustedProxy(r.RemoteAddr) {
		if v := r.Header.Get("X-Forwarded-Proto"); v != "" {
			return strings.EqualFold(strings.TrimSpace(strings.Split(v, ",")[0]), "https")
		}
	}
	urls := o.publicURLs()
	if len(urls) == 0 {
		return false
	}
	host := strings.ToLower(r.Host)
	for _, p := range urls {
		if u, err := url.Parse(p); err == nil && (strings.EqualFold(u.Host, host) || (u.Port() == "" && strings.EqualFold(u.Hostname(), hostOnly(host)))) {
			return u.Scheme == "https"
		}
	}
	return strings.HasPrefix(strings.ToLower(urls[0]), "https://")
}

func hostOnly(hp string) string {
	if h, _, err := net.SplitHostPort(hp); err == nil {
		return strings.Trim(h, "[]")
	}
	return strings.Trim(hp, "[]")
}
