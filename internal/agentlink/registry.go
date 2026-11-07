package agentlink

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
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

// Config du registre.
type Config struct {
	OfflineAfter     time.Duration // sans signe de vie au-delà : hors ligne (défaut 45 s)
	HeartbeatSeconds int           // intervalle conseillé à l'agent (défaut 15)
	PairingTTL       time.Duration // validité d'un jeton d'appairage (défaut 15 min)
	MaxUploadBytes   int64         // taille maximale d'un transfert (défaut 4 Gio)
	MaxChunkBytes    int64         // taille maximale d'un morceau (défaut 64 Mio)
	SweepEvery       time.Duration // période du balayage des agents perdus (défaut 5 s)
	MaxPollWait      time.Duration // attente maximale d'un long-polling (défaut 30 s)
}

func (c *Config) defaults() {
	if c.OfflineAfter <= 0 {
		c.OfflineAfter = 45 * time.Second
	}
	if c.HeartbeatSeconds <= 0 {
		c.HeartbeatSeconds = 15
	}
	if c.PairingTTL <= 0 {
		c.PairingTTL = 15 * time.Minute
	}
	if c.MaxUploadBytes <= 0 {
		c.MaxUploadBytes = 4 << 30
	}
	if c.MaxChunkBytes <= 0 {
		c.MaxChunkBytes = 64 << 20
	}
	if c.SweepEvery <= 0 {
		c.SweepEvery = 5 * time.Second
	}
	if c.MaxPollWait <= 0 {
		c.MaxPollWait = 30 * time.Second
	}
}

// Deps : ce que le registre reçoit du cœur.
type Deps struct {
	Store   *store.Store
	Blobs   *store.BlobStore
	Catalog *catalog.Service // peut être nil (tests du protocole seul)
	Log     *slog.Logger
	DataDir string
	Now     func() time.Time // injectable (tests)
}

// Registry est le registre des agents, leurs travaux et leurs transferts.
type Registry struct {
	d   Deps
	cfg Config

	mu    sync.Mutex
	wake  chan struct{}   // fermé à chaque nouveau travail / changement (diffusion)
	live  map[string]live // état volatil annoncé par le heartbeat
	inbox string
	stage string

	wg       sync.WaitGroup // imports en arrière-plan
	importMu sync.Mutex     // un import de réception à la fois

	// Arrêt : les imports en arrière-plan portent ce contexte, annulé par Shutdown (appelé par Core.Close).
	bgCtx    context.Context
	bgCancel context.CancelFunc
	closed   bool

	// Verrous par (agent, sha) : un seul écrivain du fichier .part à la fois.
	shaMu    sync.Mutex
	shaLocks map[string]*shaLock
}

type shaLock struct {
	mu   sync.Mutex
	refs int
}

type live struct {
	StudioRunning bool
	OpenProject   string
}

// New crée le registre. Les dossiers de réception/transit sont sous DataDir.
func New(d Deps, cfg Config) (*Registry, error) {
	cfg.defaults()
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	r := &Registry{d: d, cfg: cfg, wake: make(chan struct{}), live: map[string]live{}, shaLocks: map[string]*shaLock{},
		inbox: filepath.Join(d.DataDir, "agent-inbox"), stage: filepath.Join(d.DataDir, "agent-staging")}
	r.bgCtx, r.bgCancel = context.WithCancel(context.Background())
	for _, dir := range []string{r.inbox, r.stage} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	if d.Catalog != nil {
		if err := d.Catalog.AddRoot(r.inbox); err != nil {
			return nil, fmt.Errorf("racine de réception : %w", err)
		}
	}
	return r, nil
}

// Config renvoie la configuration effective.
func (r *Registry) Config() Config { return r.cfg }

func (r *Registry) now() time.Time { return r.d.Now().UTC() }
func (r *Registry) db() *sql.DB    { return r.d.Store.DB() }

func (r *Registry) broadcast() {
	r.mu.Lock()
	close(r.wake)
	r.wake = make(chan struct{})
	r.mu.Unlock()
}

func (r *Registry) waitCh() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.wake
}

func newToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func hashHex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ---- appairage ----

// DefaultPairingTTL : validité d'un jeton d'appairage.
const DefaultPairingTTL = 15 * time.Minute

// NewPairing génère un jeton d'appairage à usage unique (affiché une seule fois). Il n'est exposé que par
// la CLI humaine (`bambud agent pair`) et la page /agents/pair d'un navigateur enregistré.
func (r *Registry) NewPairing(ctx context.Context) (token string, expires time.Time, err error) {
	return createPairing(ctx, r.d.Store, r.now(), r.cfg.PairingTTL)
}

// CreatePairing crée un jeton d'appairage directement dans la base (CLI `bambud agent pair`, sans registre).
func CreatePairing(ctx context.Context, st *store.Store, ttl time.Duration) (string, time.Time, error) {
	return createPairing(ctx, st, st.Now(), ttl)
}

func createPairing(ctx context.Context, st *store.Store, now time.Time, ttl time.Duration) (token string, expires time.Time, err error) {
	token = "pair-" + newToken(16)
	expires = now.Add(ttl)
	_, err = st.DB().ExecContext(ctx, `INSERT INTO agent_pairings(token_hash,created_at,expires_at) VALUES(?,?,?)`,
		hashHex(token), store.FormatTime(now), store.FormatTime(expires))
	if err == nil {
		_ = st.Audit(ctx, "human", "agent.pairing_created", "agent_pairing", "", map[string]any{"expires": expires})
	}
	return token, expires, err
}

// Pair échange un jeton valide contre un secret d'agent. Le jeton est consommé
// atomiquement : un second usage échoue.
func (r *Registry) Pair(ctx context.Context, req PairRequest) (PairResponse, error) {
	if req.Protocol != ProtocolVersion {
		return PairResponse{}, errf(http.StatusBadRequest, CodeProtocol, "protocole %d non pris en charge (serveur : %d)", req.Protocol, ProtocolVersion)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 80 {
		return PairResponse{}, errf(http.StatusBadRequest, CodeInvalid, "nom d'agent requis (80 caractères max)")
	}
	now := r.now()
	id := contracts.NewID("agt")
	secret := newToken(32)
	err := r.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		var exp string
		var used sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT expires_at, used_at FROM agent_pairings WHERE token_hash=?`, hashHex(req.Token)).Scan(&exp, &used)
		if errors.Is(err, sql.ErrNoRows) {
			return errf(http.StatusForbidden, CodeBadPairing, "jeton d'appairage inconnu")
		}
		if err != nil {
			return err
		}
		e, _ := store.ParseTime(exp)
		if used.Valid || !now.Before(e) {
			return errf(http.StatusForbidden, CodeBadPairing, "jeton d'appairage expiré ou déjà utilisé")
		}
		res, err := tx.ExecContext(ctx, `UPDATE agent_pairings SET used_at=?, agent_id=? WHERE token_hash=? AND used_at IS NULL`,
			store.FormatTime(now), id, hashHex(req.Token))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errf(http.StatusForbidden, CodeBadPairing, "jeton d'appairage déjà utilisé")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO agents(id,name,secret_hash,host,agent_version,protocol,created_at,last_seen_at) VALUES(?,?,?,?,?,?,?,?)`,
			id, name, hashHex(secret), req.Host, req.AgentVersion, req.Protocol, store.FormatTime(now), store.FormatTime(now))
		if err != nil {
			return err
		}
		return r.d.Store.AuditTx(ctx, tx, "agent", "agent.pair", "agent", id, map[string]any{"name": name, "host": req.Host})
	})
	if err != nil {
		return PairResponse{}, err
	}
	return PairResponse{AgentID: id, Secret: id + "." + secret, Protocol: ProtocolVersion, HeartbeatSeconds: r.cfg.HeartbeatSeconds, ServerTime: now}, nil
}

// ---- agents ----

// Agent est un agent appairé.
type Agent struct {
	ID        string
	Name      string
	Host      string
	Version   string
	Protocol  int
	Caps      AgentCaps
	HasCaps   bool
	CreatedAt time.Time
	LastSeen  *time.Time
	Revoked   bool
}

const agentCols = `id,name,host,agent_version,protocol,capabilities,created_at,last_seen_at,revoked_at`

func scanAgent(row interface{ Scan(...any) error }) (Agent, error) {
	var a Agent
	var caps, created sql.NullString
	var seen, rev sql.NullString
	if err := row.Scan(&a.ID, &a.Name, &a.Host, &a.Version, &a.Protocol, &caps, &created, &seen, &rev); err != nil {
		return a, err
	}
	a.CreatedAt, _ = store.ParseTime(created.String)
	if caps.Valid && caps.String != "" {
		if json.Unmarshal([]byte(caps.String), &a.Caps) == nil {
			a.HasCaps = true
		}
	}
	if seen.Valid {
		t, _ := store.ParseTime(seen.String)
		a.LastSeen = &t
	}
	a.Revoked = rev.Valid
	return a, nil
}

// Authenticate valide "Authorization: Bearer <agent_id>.<secret>" (comparaison en
// temps constant), refuse un agent révoqué, et note le signe de vie.
func (r *Registry) Authenticate(ctx context.Context, bearer string) (Agent, error) {
	unauth := errf(http.StatusUnauthorized, CodeUnauthorized, "authentification de l'agent refusée")
	token := strings.TrimPrefix(bearer, "Bearer ")
	id, secret, ok := strings.Cut(token, ".")
	if !ok || id == "" || secret == "" || token == bearer {
		return Agent{}, unauth
	}
	var hash string
	if err := r.db().QueryRowContext(ctx, `SELECT secret_hash FROM agents WHERE id=?`, id).Scan(&hash); err != nil {
		// Comparaison factice : pas de différence de timing exploitable pour un id inconnu.
		subtle.ConstantTimeCompare([]byte(hashHex(secret)), []byte(hashHex("x")))
		return Agent{}, unauth
	}
	if subtle.ConstantTimeCompare([]byte(hashHex(secret)), []byte(hash)) != 1 {
		return Agent{}, unauth
	}
	a, err := r.GetAgent(ctx, id)
	if err != nil {
		return Agent{}, err
	}
	if a.Revoked {
		return Agent{}, errf(http.StatusUnauthorized, CodeUnauthorized, "agent révoqué")
	}
	r.touch(ctx, id)
	return a, nil
}

func (r *Registry) touch(ctx context.Context, id string) {
	_, _ = r.db().ExecContext(ctx, `UPDATE agents SET last_seen_at=? WHERE id=?`, store.FormatTime(r.now()), id)
}

// GetAgent lit un agent.
func (r *Registry) GetAgent(ctx context.Context, id string) (Agent, error) {
	a, err := scanAgent(r.db().QueryRowContext(ctx, `SELECT `+agentCols+` FROM agents WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, errf(http.StatusNotFound, CodeNotFound, "agent introuvable")
	}
	return a, err
}

// ListAgents renvoie les agents non révoqués.
func (r *Registry) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := r.db().QueryContext(ctx, `SELECT `+agentCols+` FROM agents WHERE revoked_at IS NULL ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Online : l'agent a donné signe de vie dans la fenêtre OfflineAfter.
func (r *Registry) Online(a Agent) bool {
	return !a.Revoked && a.LastSeen != nil && r.now().Sub(*a.LastSeen) <= r.cfg.OfflineAfter
}

// Revoke révoque un agent (son secret ne fonctionne plus ; ses travaux en cours deviennent inconnus).
func (r *Registry) Revoke(ctx context.Context, id string) error {
	res, err := r.db().ExecContext(ctx, `UPDATE agents SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, store.FormatTime(r.now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errf(http.StatusNotFound, CodeNotFound, "agent introuvable ou déjà révoqué")
	}
	_ = r.Sweep(ctx)
	return nil
}

// Hello enregistre les capacités annoncées et réconcilie les travaux détenus.
func (r *Registry) Hello(ctx context.Context, a Agent, req HelloRequest) (HelloResponse, error) {
	if req.Protocol != ProtocolVersion {
		return HelloResponse{}, errf(http.StatusBadRequest, CodeProtocol, "protocole %d non pris en charge (serveur : %d)", req.Protocol, ProtocolVersion)
	}
	caps, _ := json.Marshal(req.Capabilities)
	if _, err := r.db().ExecContext(ctx, `UPDATE agents SET capabilities=?, agent_version=?, protocol=?, last_seen_at=? WHERE id=?`,
		string(caps), req.AgentVersion, req.Protocol, store.FormatTime(r.now()), a.ID); err != nil {
		return HelloResponse{}, err
	}
	r.mu.Lock()
	r.live[a.ID] = live{StudioRunning: req.Capabilities.Studio.Running, OpenProject: r.live[a.ID].OpenProject}
	r.mu.Unlock()

	resp := HelloResponse{Protocol: ProtocolVersion, ServerTime: r.now(), HeartbeatSeconds: r.cfg.HeartbeatSeconds}
	held := map[string]string{}
	for _, h := range req.Held {
		held[h.ID] = h.State
	}
	// Réconciliation : le serveur confronte sa vision à celle de l'agent.
	tasks, err := r.listTasksRaw(ctx, `WHERE agent_id=? AND state IN ('assigned','running','unknown')`, a.ID)
	if err != nil {
		return resp, err
	}
	for _, t := range tasks {
		hs, isHeld := held[t.ID]
		switch {
		case isHeld && hs == "running":
			if t.State != StateRunning {
				_ = r.setState(ctx, t.ID, StateRunning, "réconcilié : l'agent confirme l'exécution après reconnexion", StateAssigned, StateUnknown)
				resp.Reconciled = append(resp.Reconciled, t.ID)
			}
		case isHeld: // terminé côté agent, résultat à rapporter
		case t.State == StateAssigned || t.State == StateRunning:
			_ = r.setState(ctx, t.ID, StateUnknown, "l'agent s'est reconnecté sans détenir ce travail : issue inconnue", StateAssigned, StateRunning)
		}
	}
	// Travaux détenus que le serveur a annulés/terminés ou ne connaît pas : à abandonner.
	for id := range held {
		t, err := r.GetTask(ctx, id)
		if err != nil || t.AgentID != a.ID || t.State == StateCancelled || (IsTerminal(t.State) && held[id] == "running") {
			resp.Abandon = append(resp.Abandon, id)
		}
	}
	sort.Strings(resp.Abandon)
	sort.Strings(resp.Reconciled)
	r.broadcast()
	return resp, nil
}

// Heartbeat note le signe de vie et renvoie les annulations demandées.
func (r *Registry) Heartbeat(ctx context.Context, a Agent, req HeartbeatRequest) (HeartbeatResponse, error) {
	r.mu.Lock()
	r.live[a.ID] = live{StudioRunning: req.StudioRunning, OpenProject: req.OpenProject}
	r.mu.Unlock()
	resp := HeartbeatResponse{ServerTime: r.now()}
	rows, err := r.db().QueryContext(ctx, `SELECT id FROM agent_tasks WHERE agent_id=? AND cancel_requested=1 AND state IN ('assigned','running')`, a.ID)
	if err != nil {
		return resp, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return resp, err
		}
		resp.Cancel = append(resp.Cancel, id)
	}
	return resp, rows.Err()
}

// Status décrit les agents pour /api/capabilities et get_capabilities.
func (r *Registry) Status(ctx context.Context) []contracts.AgentStatus {
	_ = r.Sweep(ctx)
	agents, err := r.ListAgents(ctx)
	if err != nil {
		return nil
	}
	out := make([]contracts.AgentStatus, 0, len(agents))
	for _, a := range agents {
		s := contracts.AgentStatus{ID: a.ID, Name: a.Name, Online: r.Online(a), Version: a.Version}
		if a.LastSeen != nil {
			s.LastSeen = store.FormatTime(*a.LastSeen)
		}
		if a.HasCaps {
			s.StudioInstalled, s.StudioVersion = a.Caps.Studio.Installed, a.Caps.Studio.Version
			s.LocalSlicer, s.CAD, s.Tasks = a.Caps.LocalSlicer, a.Caps.CAD, a.Caps.Tasks
			for _, f := range a.Caps.SharedFolders {
				s.SharedFolders = append(s.SharedFolders, f.Label)
			}
			r.mu.Lock()
			s.StudioRunning = r.live[a.ID].StudioRunning
			r.mu.Unlock()
		}
		out = append(out, s)
	}
	return out
}

// ApplyCapabilities complète la réponse de get_capabilities (hook du cœur).
func (r *Registry) ApplyCapabilities(c *contracts.Capabilities) {
	ctx := context.Background()
	st := r.Status(ctx)
	c.Agents = st
	online := 0
	var names []string
	var studio, slicer bool
	for _, a := range st {
		if a.Online {
			online++
			names = append(names, a.Name)
			studio = studio || a.StudioInstalled
			slicer = slicer || a.LocalSlicer
		}
	}
	set := func(id string, avail bool, note string) {
		for i := range c.Features {
			if c.Features[i].ID == id {
				c.Features[i].Available, c.Features[i].Note = avail, note
				return
			}
		}
		c.Features = append(c.Features, contracts.Feature{ID: id, Available: avail, Note: note})
	}
	note := "Windows : non utilisé (aucun agent appairé ; le mode Linux seul est normal)"
	switch {
	case online > 0:
		c.Mode = "linux_renfort_windows"
		note = fmt.Sprintf("%d agent(s) en ligne : %s", online, strings.Join(names, ", "))
	case len(st) > 0:
		note = fmt.Sprintf("Windows : hors ligne (%d agent(s) appairé(s), aucun ne répond) : le contrôle de la session Studio et les workers Windows attendent leur retour", len(st))
	}
	set("windows_agent", online > 0, note)
	set("studio_session", online > 0 && studio, "lecture du projet ouvert (copie du 3MF) et ouverture d'un résultat dans Studio ; la modification en direct n'est pas prise en charge (voir docs/jalons/J8.md)")
	set("windows_slicing", online > 0 && slicer, "slicing local en renfort sur le poste Windows ; le résultat est rapatrié et vérifié (sha256) avant tout usage")
	set("windows_import", online > 0, "inventaire et copie contrôlée de dossiers Windows autorisés vers le catalogue")
}

// ---- balayage : agent perdu => travaux inconnus ----

// Sweep marque « unknown » les travaux assignés/en cours d'un agent hors ligne ou
// révoqué. Les travaux jamais remis (queued) restent en file : rien n'a démarré.
func (r *Registry) Sweep(ctx context.Context) error {
	rows, err := r.db().QueryContext(ctx, `SELECT `+agentCols+` FROM agents`)
	if err != nil {
		return err
	}
	var lost []string
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			rows.Close()
			return err
		}
		if !r.Online(a) {
			lost = append(lost, a.ID)
		}
	}
	rows.Close()
	changed := false
	for _, id := range lost {
		res, err := r.db().ExecContext(ctx, `UPDATE agent_tasks SET state='unknown', updated_at=?,
			note='agent perdu pendant le travail : issue inconnue (réconciliation au retour de l''agent ; jamais relancé automatiquement)'
			WHERE agent_id=? AND state IN ('assigned','running')`, store.FormatTime(r.now()), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			changed = true
			r.d.Log.Warn("agent perdu : travaux marqués inconnus", "agent", id, "travaux", n)
		}
	}
	if changed {
		r.broadcast()
	}
	return nil
}

// Run balaie périodiquement jusqu'à l'annulation de ctx (démarré par Core.Run via AddRunHook), et purge
// les fichiers .part abandonnés.
func (r *Registry) Run(ctx context.Context) {
	t := time.NewTicker(r.cfg.SweepEvery)
	defer t.Stop()
	purge := time.NewTicker(time.Hour)
	defer purge.Stop()
	r.PurgeStaging(PartMaxAge)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = r.Sweep(ctx)
		case <-purge.C:
			r.PurgeStaging(PartMaxAge)
		}
	}
}

// Shutdown annule les imports en arrière-plan et attend leur fin. À appeler (Core.Close) AVANT la
// fermeture de la base ; plus aucun import ne démarre ensuite.
func (r *Registry) Shutdown() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.bgCancel()
	r.wg.Wait()
}
