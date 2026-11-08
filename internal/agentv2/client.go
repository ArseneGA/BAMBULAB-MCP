package agentv2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client est le client HTTP de l'agent : TOUTES ses connexions sont sortantes vers le serveur.
type Client struct {
	BaseURL string
	Secret  string // « <agent_id>.<secret> » ; vide avant l'appairage
	HTTP    *http.Client
	// ChunkSize : taille des morceaux de téléchargement d'un fichier (défaut 8 Mio).
	ChunkSize int64
}

// NormalizeFingerprint met une empreinte en minuscules sans « : » et vérifie 64 chiffres hexadécimaux.
func NormalizeFingerprint(fp string) (string, error) {
	n := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(fp), ":", ""), " ", ""))
	if len(n) != 64 {
		return "", errors.New("empreinte de certificat invalide : 64 chiffres hexadécimaux (SHA-256) attendus")
	}
	if _, err := hex.DecodeString(n); err != nil {
		return "", fmt.Errorf("empreinte de certificat invalide : %w", err)
	}
	return n, nil
}

// InsecureTransportWarning renvoie un avertissement quand le serveur est joint en HTTP clair hors de cette machine.
func InsecureTransportWarning(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "http" {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return ""
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return ""
	}
	return "ATTENTION : " + baseURL + " est joint en HTTP clair hors de cette machine : le secret de l'agent transite sans chiffrement. Utiliser https:// (avec --server-fingerprint si le certificat est auto-signé)."
}

// NewClient crée un client. fingerprint (facultatif) épingle le certificat TLS du serveur (SHA-256 du certificat
// feuille) ; la chaîne n'est alors pas vérifiée.
func NewClient(baseURL, secret, fingerprint string) (*Client, error) {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          4,
	}
	if fingerprint != "" {
		want, err := NormalizeFingerprint(fingerprint)
		if err != nil {
			return nil, err
		}
		tr.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, // remplacé par l'épinglage ci-dessous
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				if len(raw) == 0 {
					return errors.New("agentv2 : le serveur n'a présenté aucun certificat")
				}
				sum := sha256.Sum256(raw[0])
				if hex.EncodeToString(sum[:]) != want {
					return errors.New("agentv2 : l'empreinte du certificat du serveur ne correspond pas à celle épinglée")
				}
				return nil
			},
		}
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Secret: secret, HTTP: &http.Client{Transport: tr}}, nil
}

func (c *Client) do(ctx context.Context, method, p string, body io.Reader, hdr map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+BasePath+p, body)
	if err != nil {
		return nil, err
	}
	if c.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return c.HTTP.Do(req)
}

// HTTPError : réponse d'erreur du serveur.
type HTTPError struct {
	Status  int
	Code    string
	Message string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("serveur : %d %s : %s", e.Status, e.Code, e.Message)
}

func decodeErr(resp *http.Response) error {
	var env struct {
		Error APIError `json:"error"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	_ = json.Unmarshal(b, &env)
	return &HTTPError{Status: resp.StatusCode, Code: env.Error.Code, Message: env.Error.Message}
}

func (c *Client) postJSON(ctx context.Context, p string, in, out any, timeout time.Duration) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := c.do(ctx, http.MethodPost, p, bytes.NewReader(b), map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return decodeErr(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Pair échange le jeton d'appairage (mécanisme existant) contre un secret d'agent et la clé du serveur.
func (c *Client) Pair(ctx context.Context, req PairRequest) (PairResponse, error) {
	req.Protocol = ProtocolVersion
	var out PairResponse
	err := c.postJSON(ctx, "/pair", req, &out, 30*time.Second)
	return out, err
}

// NextOrder attend (long-poll) le prochain ordre ; nil si aucun dans le délai.
func (c *Client) NextOrder(ctx context.Context, printerID string, waitSeconds int) (*Order, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(waitSeconds+20)*time.Second)
	defer cancel()
	q := url.Values{"printer_id": {printerID}, "wait": {strconv.Itoa(waitSeconds)}}
	resp, err := c.do(ctx, http.MethodGet, "/orders/next?"+q.Encode(), nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNoContent:
		return nil, nil
	case resp.StatusCode/100 != 2:
		return nil, decodeErr(resp)
	}
	var o Order
	if err := json.NewDecoder(resp.Body).Decode(&o); err != nil {
		return nil, err
	}
	return &o, nil
}

// PostResult remet le résultat d'un ordre.
func (c *Client) PostResult(ctx context.Context, orderID string, res OrderResult) error {
	return c.postJSON(ctx, "/orders/"+url.PathEscape(orderID)+"/result", res, nil, 2*time.Minute)
}

// PostTelemetry envoie l'état de l'imprimante.
func (c *Client) PostTelemetry(ctx context.Context, t Telemetry) error {
	return c.postJSON(ctx, "/telemetry", t, nil, 20*time.Second)
}

// Download tire le fichier par morceaux (Range) et les écrit dans w (reprise : chaque morceau est réessayé).
func (c *Client) Download(ctx context.Context, fileID string, size int64, w io.Writer) error {
	chunk := c.ChunkSize
	if chunk <= 0 {
		chunk = 8 << 20
	}
	var off int64
	for off < size {
		end := off + chunk - 1
		if end >= size {
			end = size - 1
		}
		var lastErr error
		for try := 0; try < 4; try++ {
			if try > 0 {
				select {
				case <-time.After(time.Duration(try) * time.Second):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			n, err := c.downloadRange(ctx, fileID, off, end, w)
			off += n // un morceau partiel déjà écrit n'est pas retéléchargé : on reprend là où on s'est arrêté
			if err == nil && off > end {
				lastErr = nil
				break
			}
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			lastErr = err
			var he *HTTPError
			if errors.As(err, &he) && he.Status/100 == 4 {
				return err
			}
		}
		if lastErr != nil {
			return lastErr
		}
	}
	return nil
}

func (c *Client) downloadRange(ctx context.Context, fileID string, start, end int64, w io.Writer) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	resp, err := c.do(ctx, http.MethodGet, "/files/"+url.PathEscape(fileID), nil, map[string]string{"Range": fmt.Sprintf("bytes=%d-%d", start, end)})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return 0, decodeErr(resp)
	}
	if resp.StatusCode == http.StatusOK && start != 0 {
		return 0, errors.New("agentv2 : le serveur ignore Range")
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, end-start+1))
	return n, err
}
