package agentlink

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Client est le client du protocole côté agent.
type Client struct {
	BaseURL string // ex. http://bambud.lan:7878 (sans le préfixe /agent/v1)
	Secret  string // "<agent_id>.<secret>" ; vide avant l'appairage
	HTTP    *http.Client
	// ChunkSize : taille des morceaux d'envoi (défaut 8 Mio).
	ChunkSize int64
}

// NewClient crée un client (délai de connexion borné, pas de délai global : le long-polling est long).
func NewClient(baseURL, secret string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Secret: secret, HTTP: &http.Client{}}
}

func (c *Client) chunk() int64 {
	if c.ChunkSize > 0 {
		return c.ChunkSize
	}
	return 8 << 20
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

// decodeErr transforme une réponse d'erreur en *Error.
func decodeErr(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env struct {
		Error APIError `json:"error"`
	}
	if json.Unmarshal(b, &env) == nil && env.Error.Code != "" {
		return &Error{Status: resp.StatusCode, Code: env.Error.Code, Message: env.Error.Message}
	}
	return &Error{Status: resp.StatusCode, Code: "http_" + strconv.Itoa(resp.StatusCode), Message: strings.TrimSpace(string(b))}
}

func (c *Client) json(ctx context.Context, method, p string, in, out any) error {
	var body io.Reader
	hdr := map[string]string{}
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
		hdr["Content-Type"] = "application/json"
	}
	resp, err := c.do(ctx, method, p, body, hdr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return decodeErr(resp)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Pair échange le jeton d'appairage contre l'identité de l'agent.
func (c *Client) Pair(ctx context.Context, req PairRequest) (PairResponse, error) {
	req.Protocol = ProtocolVersion
	var out PairResponse
	err := c.json(ctx, http.MethodPost, "/pair", req, &out)
	return out, err
}

// Hello annonce les capacités et réconcilie.
func (c *Client) Hello(ctx context.Context, req HelloRequest) (HelloResponse, error) {
	req.Protocol = ProtocolVersion
	var out HelloResponse
	err := c.json(ctx, http.MethodPost, "/hello", req, &out)
	return out, err
}

// Heartbeat envoie un signe de vie.
func (c *Client) Heartbeat(ctx context.Context, req HeartbeatRequest) (HeartbeatResponse, error) {
	var out HeartbeatResponse
	err := c.json(ctx, http.MethodPost, "/heartbeat", req, &out)
	return out, err
}

// NextTask est le long-polling ; (nil, nil) si aucun travail après `wait`.
func (c *Client) NextTask(ctx context.Context, wait time.Duration) (*Task, error) {
	resp, err := c.do(ctx, http.MethodGet, "/tasks/next?wait="+strconv.Itoa(int(wait.Seconds())), nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode/100 != 2 {
		return nil, decodeErr(resp)
	}
	var t Task
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// StartTask confirme le démarrage.
func (c *Client) StartTask(ctx context.Context, id string) error {
	return c.json(ctx, http.MethodPost, "/tasks/"+id+"/start", struct{}{}, nil)
}

// Progress rapporte l'avancement.
func (c *Client) Progress(ctx context.Context, id string, p ProgressReport) error {
	return c.json(ctx, http.MethodPost, "/tasks/"+id+"/progress", p, nil)
}

// Result rapporte l'issue finale.
func (c *Client) Result(ctx context.Context, id string, r TaskResult) error {
	return c.json(ctx, http.MethodPost, "/tasks/"+id+"/result", r, nil)
}

// Inventory envoie un lot d'inventaire.
func (c *Client) Inventory(ctx context.Context, req InventoryRequest) (InventoryResponse, error) {
	var out InventoryResponse
	err := c.json(ctx, http.MethodPost, "/inventory", req, &out)
	return out, err
}

// Upload envoie un fichier avec reprise : demande l'offset du serveur, puis
// envoie par morceaux. Le serveur vérifie l'empreinte à la fin.
func (c *Client) Upload(ctx context.Context, taskID, localPath, sha string, size int64, target, name string, progress func(sent, total int64)) error {
	var st UploadStatus
	if err := c.json(ctx, http.MethodPost, "/uploads/begin", UploadBegin{TaskID: taskID, SHA256: sha, Size: size, Target: target, Name: name}, &st); err != nil {
		return err
	}
	if st.Complete {
		if progress != nil {
			progress(size, size)
		}
		return nil
	}
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	off := st.Offset
	for off < size || size == 0 {
		n := size - off
		if n > c.chunk() {
			n = c.chunk()
		}
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return err
		}
		rng := fmt.Sprintf("bytes %d-%d/%d", off, off+n-1, size)
		if n == 0 {
			rng = fmt.Sprintf("bytes */%d", size)
		}
		resp, err := c.do(ctx, http.MethodPut, "/uploads/"+sha+"?task="+url.QueryEscape(taskID), io.LimitReader(f, n), map[string]string{
			"Content-Range": rng, "Content-Type": "application/octet-stream"})
		if err != nil {
			return err
		}
		if resp.StatusCode/100 != 2 {
			e := decodeErr(resp)
			srv := resp.Header.Get("Upload-Offset")
			resp.Body.Close()
			var ae *Error
			if errors.As(e, &ae) && ae.Code == CodeConflict && srv != "" {
				// Décalage : on se recale sur l'offset du serveur et on continue.
				if o, perr := strconv.ParseInt(srv, 10, 64); perr == nil && o != off {
					off = o
					continue
				}
			}
			return e
		}
		var got UploadStatus
		err = json.NewDecoder(resp.Body).Decode(&got)
		resp.Body.Close()
		if err != nil {
			return err
		}
		off = got.Offset
		if progress != nil {
			progress(off, size)
		}
		if got.Complete {
			return nil
		}
	}
	return nil
}

// Download télécharge un artefact d'entrée avec reprise et vérifie son sha256.
// dest est écrit de façon atomique (fichier .part puis renommage).
func (c *Client) Download(ctx context.Context, sha, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	part := dest + ".part"
	var have int64
	if st, err := os.Stat(part); err == nil {
		have = st.Size()
	}
	hdr := map[string]string{}
	if have > 0 {
		hdr["Range"] = fmt.Sprintf("bytes=%d-", have)
	}
	resp, err := c.do(ctx, http.MethodGet, "/blobs/"+sha, nil, hdr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flag := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusOK:
		flag |= os.O_TRUNC // le serveur renvoie tout
	case http.StatusPartialContent:
		flag |= os.O_APPEND
	case http.StatusRequestedRangeNotSatisfiable:
		have, flag = 0, os.O_CREATE|os.O_WRONLY|os.O_TRUNC
		_ = os.Remove(part)
		return c.Download(ctx, sha, dest)
	default:
		return decodeErr(resp)
	}
	f, err := os.OpenFile(part, flag, 0o600)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(f, resp.Body)
	f.Close()
	if cerr != nil {
		return cerr // le .part reste : la reprise repartira de là
	}
	got, err := fileSHA(part)
	if err != nil {
		return err
	}
	if got != sha {
		_ = os.Remove(part)
		return fmt.Errorf("empreinte du téléchargement %s différente de %s", got, sha)
	}
	return os.Rename(part, dest)
}

// SHA256File calcule l'empreinte d'un fichier local.
func SHA256File(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
