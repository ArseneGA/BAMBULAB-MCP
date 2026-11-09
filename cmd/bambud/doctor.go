package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

// bambud doctor connector --url https://… : diagnostic EXTERNE d'un connecteur distant (spec G1 F7). Il se comporte
// comme Claude : il ne voit que ce qu'Internet voit. Aucune écriture de données hormis l'enregistrement d'un client
// OAuth de test (purgé par le serveur). Le jeton éventuel n'est envoyé qu'à l'URL donnée, jamais affiché ni journalisé.

const (
	doctorOK   = "OK"
	doctorKO   = "KO"
	doctorWarn = "AVERTISSEMENT"
	doctorSkip = "IGNORÉ"
)

// doctorCheck : une ligne du rapport.
type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type doctorOptions struct {
	URL       string       // URL de base (ou …/mcp)
	Token     string       // Bearer facultatif (jamais affiché)
	AllowHTTP bool         // autorise http:// (boucle locale seulement)
	Client    *http.Client // client HTTP (tests : confiance au certificat du serveur de test)
}

// forbiddenPaths : chemins qui doivent répondre 404 sur une surface publique correctement réglée.
var forbiddenPaths = []string{
	"/confirm/x", "/stop/x", "/action/x", "/setup/launch-code", "/setup/passkeys", "/setup/printer/x", "/enroll",
	"/agents/pair", "/api/capabilities", "/api/system/busy", "/api/session", "/agent/v1/tasks/next", "/agent/v2/x", "/",
}

func doctorMain(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "connector" {
		fmt.Fprintln(stderr, "usage : bambud doctor connector --url https://<nom-public> [--token-file fichier] [--json] [--allow-http]")
		return 2
	}
	fs := flag.NewFlagSet("doctor connector", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rawURL := fs.String("url", "", "URL publique du connecteur (https://nom, avec ou sans /mcp)")
	tokenFile := fs.String("token-file", "", "fichier contenant un jeton Bearer (sinon variable BAMBU_API_TOKEN) pour les contrôles MCP ; jamais affiché")
	asJSON := fs.Bool("json", false, "sortie JSON")
	allowHTTP := fs.Bool("allow-http", false, "autorise une URL http (boucle locale seulement)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	token := strings.TrimSpace(os.Getenv("BAMBU_API_TOKEN"))
	if *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			fmt.Fprintln(stderr, "lecture du fichier de jeton impossible :", err)
			return 2
		}
		token = strings.TrimSpace(string(b))
	}
	if *rawURL == "" {
		fmt.Fprintln(stderr, "--url est obligatoire")
		return 2
	}
	checks, err := runConnectorDoctor(context.Background(), doctorOptions{URL: *rawURL, Token: token, AllowHTTP: *allowHTTP})
	if err != nil {
		fmt.Fprintln(stderr, redactToken(err.Error(), token))
		return 2
	}
	return printDoctor(stdout, checks, *asJSON, token)
}

// redactToken masque toute occurrence du jeton.
func redactToken(s, token string) string {
	if len(token) >= 4 {
		s = strings.ReplaceAll(s, token, "***")
	}
	return s
}

func printDoctor(w io.Writer, checks []doctorCheck, asJSON bool, token string) int {
	code := 0
	for _, c := range checks {
		if c.Status == doctorKO {
			code = 1
		}
	}
	var buf bytes.Buffer
	if asJSON {
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"ok": code == 0, "checks": checks})
	} else {
		for _, c := range checks {
			fmt.Fprintf(&buf, "[%-13s] %s", c.Status, c.Name)
			if c.Detail != "" {
				fmt.Fprintf(&buf, " : %s", c.Detail)
			}
			buf.WriteByte('\n')
		}
		if code == 0 {
			buf.WriteString("\nRésultat : aucun point bloquant.\n")
		} else {
			buf.WriteString("\nRésultat : au moins un point bloquant (KO).\n")
		}
	}
	_, _ = io.WriteString(w, redactToken(buf.String(), token))
	return code
}

// normalizeConnectorURL : base sans « /mcp » ni « / » final ; https exigé (http : loopback + --allow-http).
func normalizeConnectorURL(raw string, allowHTTP bool) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	raw = strings.TrimSuffix(raw, "/mcp")
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		return "", fmt.Errorf("URL %q invalide : attendu https://nom[:port] (avec ou sans /mcp)", raw)
	}
	if u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if !allowHTTP || !(u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())) {
			return "", errors.New("URL http refusée : un connecteur distant est en https (--allow-http ne vaut que pour la boucle locale)")
		}
	}
	return raw, nil
}

type bearerRT struct {
	token string
	next  http.RoundTripper
}

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

// runConnectorDoctor exécute les contrôles. Une erreur n'est renvoyée que pour une entrée inutilisable.
func runConnectorDoctor(ctx context.Context, o doctorOptions) ([]doctorCheck, error) {
	base, err := normalizeConnectorURL(o.URL, o.AllowHTTP)
	if err != nil {
		return nil, err
	}
	hc := &http.Client{Timeout: 20 * time.Second}
	if o.Client != nil {
		cp := *o.Client
		hc = &cp
		if hc.Timeout == 0 {
			hc.Timeout = 20 * time.Second
		}
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse } // jamais de suivi : le jeton ne quitte pas l'URL donnée
	d := &doctor{ctx: ctx, base: base, hc: hc, token: o.Token}

	d.metadata()
	d.dcr()
	d.authorizeUnknown()
	d.forbidden()
	d.publicPages()
	d.mcpChecks()
	return d.checks, nil
}

type doctor struct {
	ctx    context.Context
	base   string
	hc     *http.Client
	token  string
	checks []doctorCheck
	// skipRest : l'URL n'est pas joignable, les contrôles suivants sont inutiles.
	skipRest bool
}

func (d *doctor) add(status, name, detail string) {
	d.checks = append(d.checks, doctorCheck{Name: name, Status: status, Detail: redactToken(detail, d.token)})
}

func (d *doctor) req(method, path string, hdr map[string]string, body string) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(d.ctx, method, d.base+path, strings.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := d.hc.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res, b, nil
}

func isTLSError(err error) bool {
	var ce *tls.CertificateVerificationError
	var he tls.RecordHeaderError
	return errors.As(err, &ce) || errors.As(err, &he) || strings.Contains(strings.ToLower(err.Error()), "tls") || strings.Contains(strings.ToLower(err.Error()), "x509")
}

// metadata : TLS, 401 de /mcp, métadonnées RFC 9728 et RFC 8414.
func (d *doctor) metadata() {
	// (2) POST /mcp sans jeton, et (1) TLS déduit de cette première connexion.
	res, _, err := d.req("POST", "/mcp", map[string]string{"Content-Type": "application/json", "Accept": "application/json, text/event-stream"},
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"bambud-doctor","version":"1"}}}`)
	if err != nil {
		if strings.HasPrefix(d.base, "https://") && isTLSError(err) {
			d.add(doctorKO, "TLS", "certificat ou connexion TLS invalide : "+err.Error())
		} else {
			d.add(doctorKO, "Joignabilité", "connexion impossible : "+err.Error())
		}
		d.add(doctorSkip, "Contrôles suivants", "interrompus : l'URL n'est pas joignable")
		d.skipRest = true
		return
	}
	if res.TLS != nil && len(res.TLS.PeerCertificates) > 0 {
		cert := res.TLS.PeerCertificates[0]
		left := time.Until(cert.NotAfter)
		switch {
		case left < 0:
			d.add(doctorKO, "TLS", "certificat expiré")
		case left < 14*24*time.Hour:
			d.add(doctorWarn, "TLS", fmt.Sprintf("valide, mais expire dans %d jours", int(left.Hours()/24)))
		default:
			d.add(doctorOK, "TLS", fmt.Sprintf("chaîne et nom valides, expire dans %d jours", int(left.Hours()/24)))
		}
	} else if strings.HasPrefix(d.base, "https://") {
		d.add(doctorWarn, "TLS", "aucune information de certificat")
	} else {
		d.add(doctorWarn, "TLS", "connexion http (boucle locale)")
	}
	wa := res.Header.Get("WWW-Authenticate")
	if res.StatusCode == 401 && strings.Contains(wa, "resource_metadata=") {
		d.add(doctorOK, "POST /mcp sans jeton", "401 avec WWW-Authenticate et resource_metadata")
	} else {
		d.add(doctorKO, "POST /mcp sans jeton", fmt.Sprintf("attendu 401 avec resource_metadata, reçu %d (WWW-Authenticate : %q)", res.StatusCode, wa))
	}

	// (3) métadonnées de la ressource protégée
	var prm struct {
		Resource string   `json:"resource"`
		AS       []string `json:"authorization_servers"`
	}
	res, body, err := d.req("GET", "/.well-known/oauth-protected-resource", nil, "")
	switch {
	case err != nil:
		d.add(doctorKO, "Métadonnées de la ressource", err.Error())
	case res.StatusCode != 200 || json.Unmarshal(body, &prm) != nil:
		d.add(doctorKO, "Métadonnées de la ressource", fmt.Sprintf("GET /.well-known/oauth-protected-resource : %d, JSON attendu", res.StatusCode))
	case prm.Resource != d.base+"/mcp":
		d.add(doctorKO, "Métadonnées de la ressource", fmt.Sprintf("resource = %q, attendu %q (BAMBU_PUBLIC_LISTEN_URL ne correspond pas à l'adresse testée)", prm.Resource, d.base+"/mcp"))
	case len(prm.AS) == 0:
		d.add(doctorKO, "Métadonnées de la ressource", "authorization_servers vide")
	default:
		d.add(doctorOK, "Métadonnées de la ressource", "resource = "+prm.Resource)
	}

	// (4) métadonnées du serveur d'autorisation
	var as struct {
		Issuer string   `json:"issuer"`
		Auth   string   `json:"authorization_endpoint"`
		Token  string   `json:"token_endpoint"`
		Reg    string   `json:"registration_endpoint"`
		PKCE   []string `json:"code_challenge_methods_supported"`
	}
	res, body, err = d.req("GET", "/.well-known/oauth-authorization-server", nil, "")
	switch {
	case err != nil:
		d.add(doctorKO, "Métadonnées du serveur d'autorisation", err.Error())
	case res.StatusCode != 200 || json.Unmarshal(body, &as) != nil:
		d.add(doctorKO, "Métadonnées du serveur d'autorisation", fmt.Sprintf("GET /.well-known/oauth-authorization-server : %d, JSON attendu", res.StatusCode))
	case as.Auth == "" || as.Token == "" || as.Reg == "":
		d.add(doctorKO, "Métadonnées du serveur d'autorisation", "authorization_endpoint, token_endpoint ou registration_endpoint manquant")
	case !containsStr(as.PKCE, "S256"):
		d.add(doctorKO, "Métadonnées du serveur d'autorisation", "S256 absent de code_challenge_methods_supported")
	case !strings.HasPrefix(as.Auth, d.base+"/") || !strings.HasPrefix(as.Token, d.base+"/") || !strings.HasPrefix(as.Reg, d.base+"/"):
		d.add(doctorKO, "Métadonnées du serveur d'autorisation", "les adresses annoncées ne sont pas sous l'URL testée (émetteur mal réglé : BAMBU_PUBLIC_LISTEN_URL)")
	default:
		d.add(doctorOK, "Métadonnées du serveur d'autorisation", "endpoints, S256 et émetteur corrects")
	}
}

func containsStr(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}

// dcr : enregistrement dynamique d'un client public de test (purgé par le serveur : clients sans autorisation).
func (d *doctor) dcr() {
	if d.skipRest {
		return
	}
	res, body, err := d.req("POST", "/oauth/register", map[string]string{"Content-Type": "application/json"},
		`{"client_name":"bambud doctor (test)","redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"]}`)
	var out struct {
		ClientID string `json:"client_id"`
	}
	switch {
	case err != nil:
		d.add(doctorKO, "Enregistrement dynamique (DCR)", err.Error())
	case res.StatusCode == 429:
		d.add(doctorWarn, "Enregistrement dynamique (DCR)", "429 : limite de débit atteinte (réessayer plus tard)")
	case res.StatusCode != 201 || json.Unmarshal(body, &out) != nil || out.ClientID == "":
		d.add(doctorKO, "Enregistrement dynamique (DCR)", fmt.Sprintf("POST /oauth/register : %d, client_id attendu", res.StatusCode))
	default:
		d.add(doctorOK, "Enregistrement dynamique (DCR)", "client public de test enregistré (purgé automatiquement par le serveur)")
	}
}

// authorizeUnknown : un client inconnu reçoit une page d'erreur, jamais une redirection.
func (d *doctor) authorizeUnknown() {
	if d.skipRest {
		return
	}
	q := url.Values{"response_type": {"code"}, "client_id": {"bc_doctor_inconnu"}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"},
		"state": {"x"}, "code_challenge": {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"}, "code_challenge_method": {"S256"}, "resource": {d.base + "/mcp"}}
	res, _, err := d.req("GET", "/oauth/authorize?"+q.Encode(), nil, "")
	switch {
	case err != nil:
		d.add(doctorKO, "Autorisation d'un client inconnu", err.Error())
	case res.StatusCode >= 300 && res.StatusCode < 400 || res.Header.Get("Location") != "":
		d.add(doctorKO, "Autorisation d'un client inconnu", fmt.Sprintf("redirection %d : un client inconnu ne doit jamais être redirigé", res.StatusCode))
	case res.StatusCode == 429:
		d.add(doctorWarn, "Autorisation d'un client inconnu", "429 : limite de débit atteinte")
	case res.StatusCode >= 400 && res.StatusCode < 500:
		d.add(doctorOK, "Autorisation d'un client inconnu", fmt.Sprintf("page d'erreur %d, sans redirection", res.StatusCode))
	default:
		d.add(doctorKO, "Autorisation d'un client inconnu", fmt.Sprintf("réponse inattendue %d", res.StatusCode))
	}
}

// forbidden : les chemins qui ne doivent jamais sortir sur Internet répondent 404.
func (d *doctor) forbidden() {
	if d.skipRest {
		return
	}
	var exposed, soft []string
	for _, p := range forbiddenPaths {
		res, _, err := d.req("GET", p, nil, "")
		if err != nil {
			exposed = append(exposed, fmt.Sprintf("%s (erreur : %v)", p, err))
			continue
		}
		switch {
		case res.StatusCode == 404:
		case res.StatusCode == 401 || res.StatusCode == 403 || res.StatusCode == 405:
			soft = append(soft, fmt.Sprintf("%s -> %d", p, res.StatusCode))
		default:
			exposed = append(exposed, fmt.Sprintf("%s -> %d", p, res.StatusCode))
		}
	}
	sort.Strings(exposed)
	switch {
	case len(exposed) > 0:
		d.add(doctorKO, "Chemins interdits", "chemin interdit exposé : "+strings.Join(exposed, ", ")+" (le Funnel ou le proxy doit pointer SEULEMENT sur l'écouteur public, --public-listen)")
	case len(soft) > 0:
		d.add(doctorWarn, "Chemins interdits", "refusés mais pas en 404 (existence révélée) : "+strings.Join(soft, ", "))
	default:
		d.add(doctorOK, "Chemins interdits", fmt.Sprintf("%d chemins privés répondent 404", len(forbiddenPaths)))
	}
}

// publicPages : pages publiques et /health réduit.
func (d *doctor) publicPages() {
	if d.skipRest {
		return
	}
	for _, p := range []string{"/about", "/privacy", "/icon.svg"} {
		res, _, err := d.req("GET", p, nil, "")
		if err != nil || res.StatusCode != 200 {
			d.add(doctorWarn, "Page publique "+p, "200 attendu")
		} else {
			d.add(doctorOK, "Page publique "+p, "200")
		}
	}
	res, body, err := d.req("GET", "/health", nil, "")
	var m map[string]any
	switch {
	case err != nil || res.StatusCode != 200 || json.Unmarshal(body, &m) != nil:
		d.add(doctorWarn, "/health", "200 JSON attendu")
	case len(m) > 1:
		d.add(doctorWarn, "/health", "expose plus que {\"status\":\"ok\"} (version, schéma…) : n'est pas la surface publique restreinte")
	default:
		d.add(doctorOK, "/health", "réduit à {\"status\":\"ok\"}")
	}
}

// mcpChecks : avec un jeton, initialize, tools/list, liens _meta.ui et lecture des vues.
func (d *doctor) mcpChecks() {
	if d.token == "" {
		d.add(doctorSkip, "Contrôles MCP avec jeton", "aucun jeton (--token-file ou BAMBU_API_TOKEN) : initialize, tools/list, vues non vérifiés")
		return
	}
	if d.skipRest {
		return
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "bambud-doctor", Version: "1"}, nil)
	tr := &mcp.StreamableClientTransport{Endpoint: d.base + "/mcp", HTTPClient: &http.Client{Transport: bearerRT{token: d.token, next: d.hc.Transport}, Timeout: 0, CheckRedirect: d.hc.CheckRedirect}}
	if d.hc.Transport == nil {
		tr.HTTPClient.Transport = bearerRT{token: d.token, next: http.DefaultTransport}
	}
	cctx, cancel := context.WithTimeout(d.ctx, 60*time.Second)
	defer cancel()
	cs, err := client.Connect(cctx, tr, nil)
	if err != nil {
		d.add(doctorKO, "MCP initialize", "échec avec le jeton fourni : "+err.Error())
		return
	}
	defer cs.Close()
	init := cs.InitializeResult()
	name := ""
	if init != nil && init.ServerInfo != nil {
		name = init.ServerInfo.Name
	}
	d.add(doctorOK, "MCP initialize", "serveur « "+name+" »")

	var tools []*mcp.Tool
	cursor := ""
	for i := 0; i < 20; i++ {
		lr, err := cs.ListTools(cctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			d.add(doctorKO, "MCP tools/list", err.Error())
			return
		}
		tools = append(tools, lr.Tools...)
		if lr.NextCursor == "" {
			break
		}
		cursor = lr.NextCursor
	}
	if len(tools) == 0 {
		d.add(doctorKO, "MCP tools/list", "aucun outil")
		return
	}
	d.add(doctorOK, "MCP tools/list", fmt.Sprintf("%d outils", len(tools)))

	byName := map[string]*mcp.Tool{}
	for _, t := range tools {
		byName[t.Name] = t
	}
	var uris []string
	for _, n := range []string{"request_print_confirmation", "get_print_status"} {
		t := byName[n]
		if t == nil {
			d.add(doctorKO, "Carte de "+n, "outil absent de tools/list")
			continue
		}
		uri := toolViewURI(t)
		if uri == "" {
			d.add(doctorKO, "Carte de "+n, "_meta.ui.resourceUri absent : Claude n'affichera pas de carte")
			continue
		}
		d.add(doctorOK, "Carte de "+n, "_meta.ui.resourceUri = "+uri)
		uris = append(uris, uri)
	}
	uris = append(uris, mcpserver.LiveViewURI)
	seen := map[string]bool{}
	for _, uri := range uris {
		if seen[uri] {
			continue
		}
		seen[uri] = true
		rr, err := cs.ReadResource(cctx, &mcp.ReadResourceParams{URI: uri})
		switch {
		case err != nil:
			d.add(doctorKO, "resources/read "+uri, err.Error())
		case len(rr.Contents) == 0 || strings.TrimSpace(rr.Contents[0].Text) == "":
			d.add(doctorKO, "resources/read "+uri, "contenu vide")
		case rr.Contents[0].MIMEType != mcpserver.AppMIMEType:
			d.add(doctorKO, "resources/read "+uri, fmt.Sprintf("type %q, attendu %q", rr.Contents[0].MIMEType, mcpserver.AppMIMEType))
		case len(rr.Contents[0].Text) < 2000:
			d.add(doctorWarn, "resources/read "+uri, fmt.Sprintf("%d caractères seulement : vue peut-être non construite (npm run build dans web/ avant go build)", len(rr.Contents[0].Text)))
		default:
			d.add(doctorOK, "resources/read "+uri, fmt.Sprintf("%d caractères, %s", len(rr.Contents[0].Text), rr.Contents[0].MIMEType))
		}
	}
}

// toolViewURI lit _meta.ui.resourceUri (ou la clé plate historique) d'un outil.
func toolViewURI(t *mcp.Tool) string {
	if ui, ok := t.Meta["ui"].(map[string]any); ok {
		if s, ok := ui["resourceUri"].(string); ok && s != "" {
			return s
		}
	}
	if s, ok := t.Meta["ui/resourceUri"].(string); ok {
		return s
	}
	return ""
}
