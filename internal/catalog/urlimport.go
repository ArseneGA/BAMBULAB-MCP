package catalog

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// Import par lien (#11). Anti-SSRF : HTTPS seulement, domaines autorisés (liste configurable), pas d'adresse IP
// littérale, résolution DNS faite par le service et refusée si UNE adresse n'est pas publique (le dial se fait sur
// l'adresse vérifiée : pas de ré-résolution), redirections suivies une par une avec les mêmes contrôles, aucun
// proxy d'environnement, taille et durée bornées.

// DefaultURLAllow : domaines (et leurs sous-domaines) autorisés par défaut. makerworld.com : pages ;
// bblmw.com : CDN de fichiers de MakerWorld (makerworld.bblmw.com, public-cdn.bblmw.com) ; printables.com :
// pages et fichiers (files.printables.com, media.printables.com). À vérifier avec un vrai lien : voir docs.
var DefaultURLAllow = []string{"makerworld.com", "bblmw.com", "printables.com"}

// URLImportOptions règle l'import par lien.
type URLImportOptions struct {
	Disabled     bool
	Allow        []string
	Timeout      time.Duration
	MaxRedirects int

	// Tests uniquement.
	allowPrivate bool
	tlsConfig    *tls.Config
	resolver     func(ctx context.Context, host string) ([]net.IP, error)
}

func (o URLImportOptions) isZero() bool {
	return !o.Disabled && o.Allow == nil && o.Timeout == 0 && o.MaxRedirects == 0 && !o.allowPrivate && o.tlsConfig == nil && o.resolver == nil
}

func (o *URLImportOptions) defaults() {
	if o.Timeout <= 0 {
		o.Timeout = 120 * time.Second
	}
	if o.MaxRedirects <= 0 {
		o.MaxRedirects = 5
	}
	if len(o.Allow) == 0 {
		o.Allow = append([]string(nil), DefaultURLAllow...)
	}
	o.Allow = normalizeAllow(o.Allow)
}

func normalizeAllow(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range in {
		e = strings.ToLower(strings.TrimSpace(e))
		e = strings.TrimPrefix(strings.TrimPrefix(e, "https://"), "http://")
		e = strings.TrimPrefix(strings.TrimPrefix(e, "*."), ".")
		e = strings.TrimRight(strings.SplitN(e, "/", 2)[0], ".")
		if e == "" || !strings.Contains(e, ".") && e != "localhost" || seen[e] {
			continue
		}
		if _, err := netip.ParseAddr(e); err == nil {
			continue // jamais d'IP dans la liste
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

func urlOptionsFromEnv() URLImportOptions {
	var o URLImportOptions
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BAMBU_URL_IMPORT"))) {
	case "off", "0", "false", "no", "non", "disabled":
		o.Disabled = true
	}
	for _, e := range strings.FieldsFunc(os.Getenv("BAMBU_URL_IMPORT_ALLOW"), func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		o.Allow = append(o.Allow, e)
	}
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv("BAMBU_URL_IMPORT_TIMEOUT"))); err == nil && d > 0 {
		o.Timeout = d
	}
	o.defaults()
	return o
}

// URLImportInfo décrit l'import par lien.
func (s *Service) URLImportInfo() URLImportInfo {
	st := s.up()
	o := st.opts.URL
	info := URLImportInfo{Enabled: !o.Disabled, AllowedDomains: append([]string{}, o.Allow...), MaxBytes: st.opts.MaxBytes, TimeoutSeconds: int(o.Timeout / time.Second)}
	if o.Disabled {
		info.Note = "désactivé (BAMBU_URL_IMPORT=off) : télécharger le fichier puis l'envoyer avec upload_file"
	} else {
		info.Note = "MakerWorld et Printables exigent souvent une connexion pour télécharger : si le lien est refusé, télécharger le fichier à la main puis l'envoyer avec upload_file"
	}
	return info
}

// ErrSSRF : adresse ou domaine refusé par la protection anti-SSRF.
var ErrSSRF = fmt.Errorf("%w : lien refusé", ErrNotAllowed)

// ManualDownloadError : le site demande une connexion ou un téléchargement manuel.
type ManualDownloadError struct {
	URL    string
	Reason string
}

func (e *ManualDownloadError) Error() string {
	return fmt.Sprintf("téléchargement automatique impossible (%s). Marche à suivre : 1) ouvrir %s dans un navigateur connecté, 2) télécharger le fichier (3MF/STL/STEP, ou le zip), 3) l'envoyer à l'atelier avec upload_file (ou glisser-déposer dans la Bibliothèque), en gardant l'URL en source_url pour la provenance", e.Reason, e.URL)
}
func (e *ManualDownloadError) Is(t error) bool { return t == ErrInvalid }

// URLImportInput : import d'un modèle depuis un lien.
type URLImportInput struct {
	URL string `json:"url" jsonschema:"lien https vers une page de modèle ou un fichier (domaines autorisés : voir get_capabilities, upload.url_import) ; HTTPS uniquement"`
	UploadMeta
}

// URLImportResult : issue d'un import par lien.
type URLImportResult struct {
	URL      string       `json:"url"`
	Source   string       `json:"source"`
	License  string       `json:"license,omitempty"`
	Author   string       `json:"author,omitempty"`
	Files    []FileResult `json:"files"`
	Note     string       `json:"note,omitempty"`
	Fetched  []string     `json:"fetched_urls,omitempty"`
	Redirect []string     `json:"redirects,omitempty"`
}

func hostAllowed(host string, allow []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range allow {
		if host == a || strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}

func checkURL(raw string, o URLImportOptions) (*url.URL, error) {
	if len(raw) > 2048 {
		return nil, invalidf("lien trop long")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, invalidf("lien invalide")
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("%w : HTTPS uniquement (reçu %q)", ErrSSRF, u.Scheme)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w : identifiants dans le lien interdits", ErrSSRF)
	}
	host := strings.ToLower(u.Hostname())
	if _, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return nil, fmt.Errorf("%w : adresse IP interdite, un nom de domaine autorisé est requis", ErrSSRF)
	}
	if p := u.Port(); p != "" && p != "443" && !o.allowPrivate {
		return nil, fmt.Errorf("%w : seul le port 443 est accepté", ErrSSRF)
	}
	if !hostAllowed(host, o.Allow) {
		return nil, fmt.Errorf("%w : le domaine %q n'est pas autorisé (autorisés : %s ; BAMBU_URL_IMPORT_ALLOW pour changer la liste) ; sinon télécharger le fichier puis upload_file", ErrSSRF, host, strings.Join(o.Allow, ", "))
	}
	return u, nil
}

var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "240.0.0.0/4", "64:ff9b::/96", "2001:db8::/32", "fec0::/10", "2002::/16", "100::/64"} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

// PublicIP dit si une adresse est joignable publiquement (ni privée, ni loopback, ni lien local, ni réservée).
func PublicIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsMulticast() ||
		a.IsUnspecified() || a.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

func (o URLImportOptions) client() *http.Client {
	resolve := o.resolver
	if resolve == nil {
		resolve = func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", host)
		}
	}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := resolve(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("%w : %q ne se résout pas", ErrSSRF, host)
		}
		if !o.allowPrivate {
			for _, ip := range ips {
				if !PublicIP(ip) {
					return nil, fmt.Errorf("%w : %q se résout vers une adresse non publique (%s)", ErrSSRF, host, ip)
				}
			}
		}
		var d net.Dialer
		var last error
		for _, ip := range ips {
			c, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return c, nil
			}
			last = err
		}
		return nil, last
	}
	tr := &http.Transport{
		Proxy: nil, DialContext: dial, TLSClientConfig: o.tlsConfig, ForceAttemptHTTP2: true,
		DisableKeepAlives: true, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
	}
	return &http.Client{Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > o.MaxRedirects {
			return fmt.Errorf("%w : trop de redirections", ErrSSRF)
		}
		if _, err := checkURL(req.URL.String(), o); err != nil {
			return fmt.Errorf("redirection vers %s refusée : %w", req.URL.Host, err)
		}
		return nil
	}}
}

func siteLabel(host string) string {
	switch {
	case hostAllowed(host, []string{"makerworld.com", "bblmw.com"}):
		return "MakerWorld"
	case hostAllowed(host, []string{"printables.com"}):
		return "Printables"
	}
	return host
}

var (
	reLoginPath = regexp.MustCompile(`(?i)/(log-?in|sign-?in|signin|sso|account/login|oauth|auth)(/|$|\?)`)
	reFileLink  = regexp.MustCompile(`https:(?:\\?/){2}[A-Za-z0-9._~%\-]+(?::[0-9]+)?(?:\\?/[^"'\s<>\\]*|)\.(?:3mf|stl|obj|step|stp|zip)(?:\?[^"'\s<>\\]*)?`)
	reMeta      = regexp.MustCompile(`(?is)<meta\s+[^>]*>`)
	reAttr      = regexp.MustCompile(`(?is)([a-z\-:]+)\s*=\s*("([^"]*)"|'([^']*)')`)
	reLicenseLD = regexp.MustCompile(`(?i)"license"\s*:\s*"([^"]{2,200})"`)
	reLicenseA  = regexp.MustCompile(`(?is)<a\s+[^>]*rel=["']license["'][^>]*>(.*?)</a>`)
	reAuthorLD  = regexp.MustCompile(`(?is)"author"\s*:\s*(?:\{[^}]*?"name"\s*:\s*"([^"]{1,120})"|"([^"]{1,120})")`)
	reTag       = regexp.MustCompile(`<[^>]+>`)
)

type pageInfo struct{ Title, Author, License string }

func metaValue(page, key string) string {
	for _, m := range reMeta.FindAllString(page, -1) {
		attrs := map[string]string{}
		for _, a := range reAttr.FindAllStringSubmatch(m, -1) {
			v := a[3]
			if v == "" {
				v = a[4]
			}
			attrs[strings.ToLower(a[1])] = v
		}
		if strings.EqualFold(attrs["property"], key) || strings.EqualFold(attrs["name"], key) {
			return strings.TrimSpace(html.UnescapeString(attrs["content"]))
		}
	}
	return ""
}

func parsePage(page string) pageInfo {
	pi := pageInfo{Title: firstNonEmpty(metaValue(page, "og:title"), metaValue(page, "twitter:title"))}
	pi.Author = firstNonEmpty(metaValue(page, "author"), metaValue(page, "article:author"))
	if pi.Author == "" {
		if m := reAuthorLD.FindStringSubmatch(page); m != nil {
			pi.Author = html.UnescapeString(firstNonEmpty(m[1], m[2]))
		}
	}
	pi.License = metaValue(page, "license")
	if pi.License == "" {
		if m := reLicenseLD.FindStringSubmatch(page); m != nil {
			pi.License = html.UnescapeString(m[1])
		}
	}
	if pi.License == "" {
		if m := reLicenseA.FindStringSubmatch(page); m != nil {
			pi.License = strings.TrimSpace(html.UnescapeString(reTag.ReplaceAllString(m[1], "")))
		}
	}
	if len(pi.License) > 120 {
		pi.License = pi.License[:120]
	}
	return pi
}

func fileLinks(page string, allow []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range reFileLink.FindAllString(page, 50) {
		l = html.UnescapeString(strings.ReplaceAll(l, `\/`, "/"))
		u, err := url.Parse(l)
		if err != nil || !hostAllowed(strings.ToLower(u.Hostname()), allow) || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}

// ImportFromURL télécharge (domaines autorisés seulement) puis importe au catalogue.
func (s *Service) ImportFromURL(ctx context.Context, in URLImportInput) (*URLImportResult, error) {
	st := s.up()
	o := st.opts.URL
	if o.Disabled {
		return nil, fmt.Errorf("%w : l'import par lien est désactivé sur ce service (BAMBU_URL_IMPORT=off) ; télécharger le fichier puis l'envoyer avec upload_file", ErrNotAllowed)
	}
	if in.OnDuplicate != "" && in.OnDuplicate != "skip" && in.OnDuplicate != "keep" {
		return nil, invalidf("on_duplicate : skip | keep")
	}
	if _, err := normalizeUnit(in.Unit); err != nil {
		return nil, err
	}
	first, err := checkURL(in.URL, o)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	cl := o.client()
	res := &URLImportResult{URL: first.String(), Source: siteLabel(first.Hostname())}

	body, name, ctype, err := s.fetch(ctx, cl, first, st, res)
	if err != nil {
		return nil, err
	}
	meta := in.UploadMeta
	var page pageInfo
	if strings.HasPrefix(ctype, "text/html") || strings.HasPrefix(ctype, "application/xhtml") {
		defer os.Remove(body)
		raw, _ := os.ReadFile(body)
		page = parsePage(string(raw))
		links := fileLinks(string(raw), o.Allow)
		if len(links) == 0 {
			return nil, &ManualDownloadError{URL: first.String(), Reason: "la page ne propose aucun lien de fichier téléchargeable sans connexion"}
		}
		var lastErr error
		got := false
		for _, l := range links[:min(len(links), 3)] {
			lu, err := checkURL(l, o)
			if err != nil {
				lastErr = err
				continue
			}
			b2, n2, ct2, err := s.fetch(ctx, cl, lu, st, res)
			if err != nil {
				lastErr = err
				continue
			}
			if strings.HasPrefix(ct2, "text/html") {
				os.Remove(b2)
				lastErr = &ManualDownloadError{URL: first.String(), Reason: "le lien de fichier renvoie une page (connexion requise)"}
				continue
			}
			body, name, ctype, got = b2, n2, ct2, true
			break
		}
		if !got {
			if lastErr == nil {
				lastErr = &ManualDownloadError{URL: first.String(), Reason: "aucun fichier récupérable"}
			}
			return nil, lastErr
		}
	}
	defer os.Remove(body)
	if meta.SourceURL == "" {
		meta.SourceURL = first.String()
	}
	meta.Source = firstNonEmpty(meta.Source, res.Source)
	meta.Author = firstNonEmpty(meta.Author, page.Author)
	meta.License = firstNonEmpty(meta.License, page.License)
	if meta.Name == "" && page.Title != "" {
		meta.Name = clipName(page.Title)
	}
	res.License, res.Author = meta.License, meta.Author
	_ = ctype

	stt, err := os.Stat(body)
	if err != nil {
		return nil, err
	}
	n, format, nerr := ValidUploadName(name)
	if strings.HasSuffix(strings.ToLower(name), ".zip") {
		return s.importZip(ctx, body, meta, res)
	}
	if nerr != nil {
		// Nom sans extension reconnue : 3MF si le contenu en est un.
		if check3MF(body, stt.Size()) == nil {
			n, format = "modele.3mf", contracts.Format3MF
		} else {
			return nil, fmt.Errorf("fichier téléchargé non reconnu (%s) : %w", name, nerr)
		}
	}
	r, err := s.finalize(ctx, body, n, format, stt.Size(), "", meta)
	if err != nil {
		return nil, err
	}
	res.Files = []FileResult{r.File}
	res.Note = r.Note
	return res, nil
}

func clipName(t string) string {
	t = strings.TrimSpace(t)
	if i := strings.Index(t, " | "); i > 0 {
		t = t[:i]
	}
	if len(t) > 120 {
		t = t[:120]
	}
	return t
}

// fetch télécharge u vers un fichier temporaire du service. Renvoie le chemin temporaire, le nom proposé et le type.
func (s *Service) fetch(ctx context.Context, cl *http.Client, u *url.URL, st *uploadState, res *URLImportResult) (string, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("User-Agent", "bambud-url-import/1 (+atelier Bambu Lab)")
	req.Header.Set("Accept", "model/3mf, model/stl, application/zip, application/octet-stream, text/html;q=0.5")
	resp, err := cl.Do(req)
	if err != nil {
		if errors.Is(err, ErrNotAllowed) {
			return "", "", "", err
		}
		var ue *url.Error
		if errors.As(err, &ue) && errors.Is(ue.Err, ErrNotAllowed) {
			return "", "", "", ue.Err
		}
		return "", "", "", fmt.Errorf("téléchargement impossible : %w", err)
	}
	defer resp.Body.Close()
	res.Fetched = append(res.Fetched, u.String())
	fin := resp.Request.URL
	if fin.String() != u.String() {
		res.Redirect = append(res.Redirect, fin.String())
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "", "", "", &ManualDownloadError{URL: u.String(), Reason: fmt.Sprintf("le site répond %d : connexion ou jeton requis", resp.StatusCode)}
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return "", "", "", fmt.Errorf("%w : le lien n'existe plus (%d)", ErrNotFound, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return "", "", "", fmt.Errorf("le site répond %d", resp.StatusCode)
	}
	ctype := strings.ToLower(strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0]))
	if reLoginPath.MatchString(fin.Path) && strings.HasPrefix(ctype, "text/html") {
		return "", "", "", &ManualDownloadError{URL: u.String(), Reason: "le site redirige vers une page de connexion"}
	}
	limit := st.opts.MaxBytes
	if strings.HasPrefix(ctype, "text/html") {
		limit = 4 << 20
	}
	if resp.ContentLength > limit {
		return "", "", "", invalidf("fichier trop gros (%d octets > limite %d)", resp.ContentLength, limit)
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	tmp := filepath.Join(st.dir, "dl-"+hex.EncodeToString(b)+".part")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", "", "", err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	f.Close()
	if err == nil && n > limit {
		err = invalidf("fichier trop gros (plus de %d octets)", limit)
	}
	if err != nil {
		os.Remove(tmp)
		if ctx.Err() != nil {
			return "", "", "", fmt.Errorf("délai dépassé pendant le téléchargement : %w", ctx.Err())
		}
		return "", "", "", err
	}
	name := ""
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, p, err := mime.ParseMediaType(cd); err == nil {
			name = p["filename"]
		}
	}
	if name == "" {
		name, _ = url.PathUnescape(path.Base(fin.Path))
	}
	name = path.Base(strings.ReplaceAll(name, `\`, "/")) // jamais un chemin : seulement le dernier segment
	return tmp, name, ctype, nil
}

// importZip valide une archive zip (téléchargée) et importe ses fichiers 3D (noms aplatis, jamais de chemin).
func (s *Service) importZip(ctx context.Context, zpath string, meta UploadMeta, res *URLImportResult) (*URLImportResult, error) {
	meta.Name = ""
	st := s.up()
	zr, err := zip.OpenReader(zpath)
	if err != nil {
		return nil, invalidf("l'archive téléchargée n'est pas un zip valide : %v", err)
	}
	defer zr.Close()
	if len(zr.File) > max3MFEntries {
		return nil, invalidf("archive zip refusée : trop d'entrées")
	}
	type pick struct {
		f      *zip.File
		name   string
		format contracts.FileFormat
	}
	var picks []pick
	var total uint64
	for _, e := range zr.File {
		if e.FileInfo().IsDir() {
			continue
		}
		n := strings.ReplaceAll(e.Name, `\`, "/")
		if strings.ContainsRune(n, 0) || strings.HasPrefix(n, "/") {
			continue // entrée dangereuse : ignorée (on n'extrait jamais vers un chemin)
		}
		base := path.Base(n)
		name, format, err := ValidUploadName(base)
		if err != nil {
			continue
		}
		if int64(e.UncompressedSize64) > s.maxFor(format) {
			res.Note += fmt.Sprintf(" %s ignoré (trop gros).", base)
			continue
		}
		total += e.UncompressedSize64
		if total > uint64(st.opts.MaxBytes)*2 {
			return nil, invalidf("archive zip refusée : contenu décompressé trop gros")
		}
		picks = append(picks, pick{e, name, format})
		if len(picks) >= 25 {
			break
		}
	}
	if len(picks) == 0 {
		return nil, invalidf("l'archive ne contient aucun fichier 3D (.stl .obj .3mf .step)")
	}
	for _, p := range picks {
		fr, err := s.importZipEntry(ctx, st, p.f, p.name, p.format, meta)
		if err != nil {
			fr = FileResult{Path: p.name, Status: StatusRejected, Format: string(p.format), Note: err.Error()}
		}
		res.Files = append(res.Files, fr)
	}
	res.Note = strings.TrimSpace(fmt.Sprintf("Archive zip : %d fichier(s) 3D traité(s).%s", len(res.Files), res.Note))
	return res, nil
}

func (s *Service) importZipEntry(ctx context.Context, st *uploadState, e *zip.File, name string, format contracts.FileFormat, meta UploadMeta) (FileResult, error) {
	rc, err := e.Open()
	if err != nil {
		return FileResult{}, err
	}
	defer rc.Close()
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	tmp := filepath.Join(st.dir, "zip-"+hex.EncodeToString(b)+".part")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return FileResult{}, err
	}
	defer os.Remove(tmp)
	max := s.maxFor(format)
	n, err := io.Copy(f, io.LimitReader(rc, max+1))
	f.Close()
	if err != nil {
		return FileResult{}, err
	}
	if n > max {
		return FileResult{}, invalidf("%s : décompressé plus gros que la limite", name)
	}
	r, err := s.finalize(ctx, tmp, name, format, n, "", meta)
	if err != nil {
		return FileResult{}, err
	}
	return r.File, nil
}
