package agentv2

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Error est une erreur du protocole avec son statut HTTP.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + " : " + e.Message }

func errf(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

// Authenticator est l'authentification et l'appairage des agents, fournis par le registre existant (agentlink) :
// ce paquet ne les réimplémente pas. Les erreurs renvoyées peuvent être des *Error.
type Authenticator interface {
	// Authenticate valide « Authorization: Bearer <agent_id>.<secret> » et renvoie l'identifiant de l'agent.
	Authenticate(ctx context.Context, bearer string) (agentID string, err error)
	// Pair échange un jeton d'appairage à usage unique contre un agent et son secret (« <agent_id>.<secret> »).
	Pair(ctx context.Context, req PairRequest) (agentID, secret string, err error)
}

// HubConfig configure le Hub. Tous les champs sont facultatifs.
type HubConfig struct {
	// Store : journal d'audit des autorisations (table agent_v2_authz). nil = pas de journal en base.
	Store *store.Store
	// DataDir : racine des données ; la clé de signature est <DataDir>/secrets/agent-v2-ed25519.key, les
	// fichiers en transit <DataDir>/agent-v2/stage. Créés à la première utilisation seulement.
	DataDir string
	// Signer : substitue la clé (tests).
	Signer *Signer
	Log    *slog.Logger
	Now    func() time.Time
	// AuthClock : horloge des autorisations signées (tests d'expiration). Défaut : Now.
	AuthClock func() time.Time
	// OnlineAfter : un agent sans signe de vie depuis plus longtemps est hors ligne (défaut 45 s).
	OnlineAfter time.Duration
	// MaxPollWait : attente maximale d'un long-poll (défaut 30 s).
	MaxPollWait time.Duration
	// MaxStatusAge : au-delà, la télémétrie est marquée périmée (défaut 2 min).
	MaxStatusAge time.Duration
	// StartWait : attente maximale du résultat d'un lancement (défaut 3 min + 1 s par Mio).
	StartWait time.Duration
	// TapOrder : appelé avec chaque ordre remis à un agent (tests uniquement).
	TapOrder func(Order)
}

func (c *HubConfig) defaults() {
	if c.Log == nil {
		c.Log = slog.Default()
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.AuthClock == nil {
		c.AuthClock = func() time.Time { return c.Now() }
	}
	if c.OnlineAfter <= 0 {
		c.OnlineAfter = 45 * time.Second
	}
	if c.MaxPollWait <= 0 {
		c.MaxPollWait = 30 * time.Second
	}
	if c.MaxStatusAge <= 0 {
		c.MaxStatusAge = 2 * time.Minute
	}
}

type pending struct {
	id        string
	agentID   string
	kind      string
	delivered bool
	ch        chan OrderResult
}

type staged struct {
	agentID string
	path    string
	size    int64
}

type teleEntry struct {
	t  Telemetry
	at time.Time
}

type sub struct{ ch chan printer.Status }

// Hub est le côté serveur du protocole.
type Hub struct {
	cfg HubConfig

	signerMu  sync.Mutex
	signer    *Signer
	signerErr error

	mu      sync.Mutex
	wake    chan struct{}
	queues  map[string][]*Order
	pend    map[string]*pending
	files   map[string]*staged
	seen    map[string]time.Time
	tele    map[string]teleEntry
	subs    map[string]map[*sub]struct{}
	stageOK bool
}

// NewHub crée le hub. Aucun effet de bord sur disque avant la première utilisation.
func NewHub(cfg HubConfig) *Hub {
	cfg.defaults()
	return &Hub{cfg: cfg, signer: cfg.Signer, wake: make(chan struct{}), queues: map[string][]*Order{}, pend: map[string]*pending{},
		files: map[string]*staged{}, seen: map[string]time.Time{}, tele: map[string]teleEntry{}, subs: map[string]map[*sub]struct{}{}}
}

func (h *Hub) now() time.Time { return h.cfg.Now() }

// Signer renvoie la clé de signature du serveur (créée à la première utilisation).
func (h *Hub) Signer() (*Signer, error) {
	h.signerMu.Lock()
	defer h.signerMu.Unlock()
	if h.signer != nil {
		return h.signer, nil
	}
	if h.signerErr != nil {
		return nil, h.signerErr
	}
	if h.cfg.DataDir == "" {
		s, err := NewSigner()
		h.signer, h.signerErr = s, err
		return s, err
	}
	s, err := LoadOrCreateSigner(filepath.Join(h.cfg.DataDir, "secrets", "agent-v2-ed25519.key"))
	h.signer, h.signerErr = s, err
	return s, err
}

// ServerKey renvoie la clé publique du serveur (base64) et son identifiant court.
func (h *Hub) ServerKey() (keyB64, keyID string, err error) {
	s, err := h.Signer()
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(s.Public()), s.KeyID(), nil
}

// SignAuthorization signe une autorisation de lancement (utilisée par HubLink.Start ; exportée pour les tests).
func (h *Hub) SignAuthorization(p AuthPayload) (SignedAuth, error) {
	s, err := h.Signer()
	if err != nil {
		return SignedAuth{}, err
	}
	return s.Sign(p)
}

func (h *Hub) broadcast() {
	h.mu.Lock()
	close(h.wake)
	h.wake = make(chan struct{})
	h.mu.Unlock()
}

func (h *Hub) touch(agentID string) {
	h.mu.Lock()
	h.seen[agentID] = h.now()
	h.mu.Unlock()
}

// Online dit si l'agent a donné signe de vie récemment.
func (h *Hub) Online(agentID string) bool {
	h.mu.Lock()
	t, ok := h.seen[agentID]
	h.mu.Unlock()
	return ok && h.now().Sub(t) <= h.cfg.OnlineAfter
}

// ---- ordres ----

func (h *Hub) enqueue(agentID string, o Order) *pending {
	if o.ID == "" {
		o.ID = contracts.NewID("ord")
	}
	o.CreatedAt = h.now().UTC()
	p := &pending{id: o.ID, agentID: agentID, kind: o.Kind, ch: make(chan OrderResult, 1)}
	h.mu.Lock()
	h.pend[o.ID] = p
	h.queues[agentID] = append(h.queues[agentID], &o)
	h.mu.Unlock()
	h.broadcast()
	return p
}

// withdraw retire un ordre encore en file (jamais remis). Vrai = l'agent ne l'a pas vu : rien n'a pu partir.
func (h *Hub) withdraw(agentID, orderID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	q := h.queues[agentID]
	for i, o := range q {
		if o.ID == orderID {
			h.queues[agentID] = append(q[:i:i], q[i+1:]...)
			delete(h.pend, orderID)
			return true
		}
	}
	return false
}

func (h *Hub) forget(orderID string) {
	h.mu.Lock()
	delete(h.pend, orderID)
	h.mu.Unlock()
}

// next remet à l'agent son prochain ordre (au plus une fois), en attendant jusqu'à wait.
func (h *Hub) next(ctx context.Context, agentID string, wait time.Duration) *Order {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		h.mu.Lock()
		if q := h.queues[agentID]; len(q) > 0 {
			o := q[0]
			h.queues[agentID] = q[1:]
			if p := h.pend[o.ID]; p != nil {
				p.delivered = true
			}
			h.mu.Unlock()
			if h.cfg.TapOrder != nil {
				h.cfg.TapOrder(*o)
			}
			return o
		}
		ch := h.wake
		h.mu.Unlock()
		select {
		case <-ch:
		case <-deadline.C:
			return nil
		case <-ctx.Done():
			return nil
		}
	}
}

func (h *Hub) submit(agentID, orderID string, res OrderResult) error {
	h.mu.Lock()
	p := h.pend[orderID]
	h.mu.Unlock()
	if p == nil || p.agentID != agentID {
		return errf(http.StatusNotFound, "not_found", "ordre inconnu, déjà clos ou adressé à un autre agent")
	}
	select {
	case p.ch <- res:
	default:
		return errf(http.StatusConflict, "conflict", "résultat déjà reçu")
	}
	return nil
}

// await attend le résultat d'un ordre.
func (h *Hub) await(ctx context.Context, p *pending, d time.Duration) (OrderResult, bool) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case r := <-p.ch:
		return r, true
	case <-t.C:
	case <-ctx.Done():
	}
	return OrderResult{}, false
}

// ---- télémétrie ----

func teleKey(agentID, printerID string) string { return agentID + "|" + printerID }

func (h *Hub) putTelemetry(agentID string, t Telemetry) {
	k := teleKey(agentID, t.PrinterID)
	h.mu.Lock()
	h.tele[k] = teleEntry{t: t, at: h.now()}
	subs := make([]*sub, 0, len(h.subs[k]))
	for s := range h.subs[k] {
		subs = append(subs, s)
	}
	h.mu.Unlock()
	if len(subs) > 0 {
		st := h.statusOf(agentID, t.PrinterID)
		for _, s := range subs {
			for {
				select {
				case s.ch <- st:
				default:
					select {
					case <-s.ch:
						continue
					default:
					}
				}
				break
			}
		}
	}
}

// statusOf traduit la télémétrie reçue en printer.Status sur l'horloge du serveur.
func (h *Hub) statusOf(agentID, printerID string) printer.Status {
	h.mu.Lock()
	e, ok := h.tele[teleKey(agentID, printerID)]
	h.mu.Unlock()
	now := h.now()
	if !ok || !h.Online(agentID) {
		st := printer.Status{State: printer.StateOffline, Stale: true}
		if ok {
			st = e.t.Status
			st.State, st.Connected, st.Stale = printer.StateOffline, false, true
			st.UpdatedAt = time.Time{}
			if !e.t.Status.UpdatedAt.IsZero() {
				st.UpdatedAt = e.at.Add(-time.Duration(e.t.AgeMS) * time.Millisecond)
			}
		}
		return st
	}
	st := e.t.Status
	st.UpdatedAt = time.Time{}
	if !e.t.Status.UpdatedAt.IsZero() {
		st.UpdatedAt = e.at.Add(-time.Duration(e.t.AgeMS) * time.Millisecond)
	}
	st.Stale = st.UpdatedAt.IsZero() || now.Sub(st.UpdatedAt) > h.cfg.MaxStatusAge
	if now.Sub(e.at) > h.cfg.OnlineAfter {
		st.Connected = false
	}
	if !st.Connected {
		st.State = printer.StateOffline
	}
	return st
}

// AgentReadOnly dit si la dernière télémétrie de l'agent annonce le mode lecture seule.
func (h *Hub) AgentReadOnly(agentID, printerID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tele[teleKey(agentID, printerID)].t.ReadOnly
}

// ---- fichiers en transit ----

func (h *Hub) stageDir() (string, error) {
	if h.cfg.DataDir == "" {
		return os.MkdirTemp("", "bambu-agentv2-stage-")
	}
	d := filepath.Join(h.cfg.DataDir, "agent-v2", "stage")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

// stage copie le flux dans un fichier temporaire en le hachant, et ne le publie que s'il a la taille et l'empreinte
// annoncées.
func (h *Hub) stage(agentID string, r io.Reader, size int64, sha string) (string, string, error) {
	dir, err := h.stageDir()
	if err != nil {
		return "", "", err
	}
	f, err := os.CreateTemp(dir, "up-*.part")
	if err != nil {
		return "", "", err
	}
	hh := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hh), io.LimitReader(r, size+1))
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", "", err
	}
	if n != size || hex.EncodeToString(hh.Sum(nil)) != sha {
		os.Remove(f.Name())
		return "", "", fmt.Errorf("%w: read %d bytes (expected %d) or sha256 differs", printer.ErrUploadMismatch, n, size)
	}
	id := contracts.NewID("fil")
	h.mu.Lock()
	h.files[id] = &staged{agentID: agentID, path: f.Name(), size: size}
	h.mu.Unlock()
	return id, f.Name(), nil
}

func (h *Hub) unstage(id string) {
	h.mu.Lock()
	s := h.files[id]
	delete(h.files, id)
	h.mu.Unlock()
	if s != nil {
		os.Remove(s.path)
	}
}

// ---- audit des autorisations ----

func (h *Hub) auditIssued(p AuthPayload) {
	if h.cfg.Store == nil {
		return
	}
	ctx := context.Background()
	_, err := h.cfg.Store.DB().ExecContext(ctx, `INSERT INTO agent_v2_authz(id,agent_id,printer_id,sha256,plate,issued_at,expires_at) VALUES(?,?,?,?,?,?,?)`,
		p.ID, p.AgentID, p.PrinterID, p.SHA256, p.Plate, store.FormatTime(p.IssuedAt), store.FormatTime(p.ExpiresAt))
	if err != nil {
		h.cfg.Log.Warn("agent v2 : autorisation non journalisée", "err", err)
		return
	}
	_ = h.cfg.Store.Audit(ctx, "agent-v2", "agent_v2.authorize_start", "printer", p.PrinterID, map[string]any{"authorization": p.ID, "agent": p.AgentID, "sha256": p.SHA256, "plate": p.Plate, "expires_at": p.ExpiresAt})
}

func (h *Hub) auditResult(id, state, code string) {
	if h.cfg.Store == nil {
		return
	}
	_, err := h.cfg.Store.DB().ExecContext(context.Background(), `UPDATE agent_v2_authz SET result=?, result_code=?, result_at=? WHERE id=?`,
		state, code, store.FormatTime(h.now().UTC()), id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		h.cfg.Log.Warn("agent v2 : résultat non journalisé", "err", err)
	}
}

// ---- HTTP ----

const (
	maxTelemetry = 1 << 20
	maxResult    = 12 << 20
)

// Handler renvoie le routeur /agent/v2/* (préfixe inclus). L'authentification (Bearer) est celle du registre
// existant ; /pair se passe de Bearer et exige un jeton d'appairage.
func (h *Hub) Handler(auth Authenticator) http.Handler {
	mux := http.NewServeMux()
	guarded := func(fn func(w http.ResponseWriter, r *http.Request, agentID string)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			id, err := auth.Authenticate(r.Context(), r.Header.Get("Authorization"))
			if err != nil {
				writeErr(w, err)
				return
			}
			h.touch(id)
			fn(w, r, id)
		}
	}
	mux.HandleFunc("POST "+BasePath+"/pair", func(w http.ResponseWriter, r *http.Request) {
		var in PairRequest
		if !readJSON(w, r, &in, 1<<16) {
			return
		}
		if in.Protocol != ProtocolVersion {
			writeErr(w, errf(http.StatusBadRequest, "protocol_mismatch", "protocole %d non pris en charge (ce point d'accès : %d)", in.Protocol, ProtocolVersion))
			return
		}
		id, secret, err := auth.Pair(r.Context(), in)
		if err != nil {
			writeErr(w, err)
			return
		}
		keyB64, keyID, err := h.ServerKey()
		if err != nil {
			writeErr(w, err)
			return
		}
		h.touch(id)
		writeJSON(w, http.StatusOK, PairResponse{AgentID: id, Secret: secret, Protocol: ProtocolVersion, ServerKey: keyB64, ServerKeyID: keyID,
			PollSeconds: int(h.cfg.MaxPollWait / time.Second), ServerTime: h.now().UTC()})
	})
	mux.HandleFunc("GET "+BasePath+"/orders/next", guarded(func(w http.ResponseWriter, r *http.Request, agentID string) {
		wait := 25 * time.Second
		if s := r.URL.Query().Get("wait"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n >= 0 {
				wait = time.Duration(n) * time.Second
			}
		}
		if wait > h.cfg.MaxPollWait {
			wait = h.cfg.MaxPollWait
		}
		o := h.next(r.Context(), agentID, wait)
		if o == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, o)
	}))
	mux.HandleFunc("POST "+BasePath+"/orders/{id}/result", guarded(func(w http.ResponseWriter, r *http.Request, agentID string) {
		var in OrderResult
		if !readJSON(w, r, &in, maxResult) {
			return
		}
		if err := h.submit(agentID, r.PathValue("id"), in); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	}))
	mux.HandleFunc("POST "+BasePath+"/telemetry", guarded(func(w http.ResponseWriter, r *http.Request, agentID string) {
		var in Telemetry
		if !readJSON(w, r, &in, maxTelemetry) {
			return
		}
		if in.PrinterID == "" {
			writeErr(w, errf(http.StatusBadRequest, "invalid", "printer_id requis"))
			return
		}
		h.putTelemetry(agentID, in)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "server_time": h.now().UTC()})
	}))
	mux.HandleFunc("GET "+BasePath+"/files/{id}", guarded(func(w http.ResponseWriter, r *http.Request, agentID string) {
		h.mu.Lock()
		s := h.files[r.PathValue("id")]
		h.mu.Unlock()
		if s == nil || s.agentID != agentID {
			writeErr(w, errf(http.StatusNotFound, "not_found", "fichier inconnu ou adressé à un autre agent"))
			return
		}
		f, err := os.Open(s.path)
		if err != nil {
			writeErr(w, errf(http.StatusNotFound, "not_found", "fichier indisponible"))
			return
		}
		defer f.Close()
		st, _ := f.Stat()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, "", st.ModTime(), f) // gère Range : reprise par morceaux
	}))
	return mux
}

func readJSON(w http.ResponseWriter, r *http.Request, v any, max int64) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, max)).Decode(v); err != nil {
		writeErr(w, errf(http.StatusBadRequest, "invalid", "corps JSON invalide : %v", err))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var e *Error
	if errors.As(err, &e) {
		writeJSON(w, e.Status, map[string]APIError{"error": {Code: e.Code, Message: e.Message}})
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]APIError{"error": {Code: "internal", Message: "erreur interne"}})
}
