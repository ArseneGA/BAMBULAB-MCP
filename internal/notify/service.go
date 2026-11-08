// Package notify envoie des notifications (fin, échec, alerte de détection, lancement incertain, imprimante hors ligne,
// file d'attente) par ntfy, webhook signé HMAC ou e-mail SMTP en TLS.
//
// Règles de sécurité : la destination d'un canal (hôte, port et utilisateur SMTP, destinataires, URL ntfy / webhook) ne se
// règle QUE sur la page humaine /setup/notifications ; l'outil MCP ne peut qu'activer ou désactiver un canal existant et
// choisir ses évènements (sinon le modèle pourrait rediriger le mot de passe SMTP et les photos vers un hôte qu'il choisit).
// Un mot de passe SMTP est lié à (hôte, port, utilisateur) et s'invalide si l'un change. Les destinations non publiques
// (boucle locale, réseaux privés, lien local) et les ports SMTP inhabituels sont refusés, sauf BAMBU_NOTIFY_ALLOW_PRIVATE=1.
// Les SECRETS (URL du sujet ntfy, URL du webhook, clé HMAC, jeton ntfy, mot de passe SMTP) ne se
// saisissent que sur la page humaine /setup/notifications (navigateur enregistré), vivent dans le coffre chiffré ou dans des
// variables d'environnement / fichiers, et ne sont JAMAIS renvoyés (ni au modèle, ni à l'API, ni au journal). Le contenu
// d'une notification ne contient aucun secret : un titre, un message, un lien vers une page de l'interface.
package notify

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Types de canaux.
const (
	KindNtfy    = "ntfy"
	KindWebhook = "webhook"
	KindEmail   = "email"
)

// Évènements notifiables.
const (
	EvPrintCompleted = "print_completed"
	EvPrintFailed    = "print_failed"
	EvDetectionAlert = "detection_alert"
	EvStartUnknown   = "start_unknown"
	EvPrinterOffline = "printer_offline"
	// EvPrintAttention : impression qui attend une action à l'écran, bloquée, injoignable, ou arrêt non appliqué (supervision).
	EvPrintAttention = "print_attention"
	EvQueue          = "queue_event" // file d'attente (chantier E) : prochaine impression à confirmer, file bloquée...
	EvTest           = "test"
)

// AllEvents liste les évènements auxquels un canal peut s'abonner (EvTest est toujours livré par test_notification).
var AllEvents = []string{EvPrintCompleted, EvPrintFailed, EvDetectionAlert, EvStartUnknown, EvPrinterOffline, EvQueue, EvPrintAttention}

// Noms de secrets par type de canal.
var secretFields = map[string][]SecretField{
	KindNtfy:    {{"url", "URL complète du sujet ntfy (le nom du sujet est le secret)", true}, {"token", "jeton d'accès ntfy (sujet protégé), facultatif", false}},
	KindWebhook: {{"url", "URL du webhook", true}, {"hmac_key", "clé HMAC-SHA256 qui signe chaque envoi", true}},
	KindEmail:   {{"password", "mot de passe SMTP (laisser vide si le serveur n'authentifie pas)", false}},
}

// SecretField décrit un secret attendu par un type de canal.
type SecretField struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
}

// Event est une notification à émettre. Son contenu ne doit contenir aucun secret.
type Event struct {
	Kind      string // un des Ev*
	Title     string
	Message   string
	Link      string // adresse d'une page de l'interface
	PrinterID string
	JobID     string
	At        time.Time
	Photo     []byte // JPEG facultatif (joint pour ntfy et e-mail ; le webhook reçoit seulement le lien)
	PhotoURL  string
	DedupKey  string // même clé dans la fenêtre de dédoublonnage : un seul envoi
}

// ChannelConfig : réglages sans secret.
type ChannelConfig struct {
	SMTPHost string   `json:"smtp_host,omitempty"`
	SMTPPort int      `json:"smtp_port,omitempty" jsonschema:"défaut 587 (STARTTLS) ou 465 (TLS implicite)"`
	SMTPUser string   `json:"smtp_user,omitempty"`
	SMTPTLS  string   `json:"smtp_tls,omitempty" jsonschema:"starttls (défaut) ou implicit ; le clair n'existe pas"`
	From     string   `json:"from,omitempty"`
	To       []string `json:"to,omitempty"`
	Priority string   `json:"ntfy_priority,omitempty" jsonschema:"min, low, default, high ou max (ntfy)"`
}

// ChannelInput crée ou met à jour un canal (clé : name).
type ChannelInput struct {
	Name    string        `json:"name" jsonschema:"nom du canal, 1 à 40 caractères (lettres, chiffres, espace, - _)"`
	Kind    string        `json:"kind" jsonschema:"ntfy | webhook | email"`
	Enabled *bool         `json:"enabled,omitempty"`
	Events  []string      `json:"events,omitempty" jsonschema:"évènements abonnés ; vide = tous"`
	Config  ChannelConfig `json:"config,omitzero"`
}

// SecretStatus dit si un secret est présent et d'où il vient (jamais sa valeur).
type SecretStatus struct {
	SecretField
	Set    bool   `json:"set"`
	Source string `json:"source,omitempty" jsonschema:"vault | env | file"`
}

// ChannelView est ce que voit le client : configuration et état des secrets, jamais les valeurs.
type ChannelView struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Kind      string         `json:"kind"`
	Enabled   bool           `json:"enabled"`
	Events    []string       `json:"events"`
	Config    ChannelConfig  `json:"config"`
	Secrets   []SecretStatus `json:"secrets"`
	Ready     bool           `json:"ready" jsonschema:"vrai si tous les secrets requis et la configuration sont présents"`
	Missing   []string       `json:"missing,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// Vault est le coffre de secrets (printing.SecretStore le satisfait).
type Vault interface {
	SetAccessCode(ctx context.Context, key, value string) error
	AccessCode(ctx context.Context, key string) (string, error)
	HasAccessCode(key string) bool
	DeleteAccessCode(key string) error
}

// Deps : ce que le service utilise.
type Deps struct {
	Store   *store.Store
	Vault   Vault
	Log     *slog.Logger
	BaseURL func() string // base des liens (page de l'interface)
	Now     func() time.Time
	// HTTP : client des envois ntfy / webhook (défaut : délai de 15 s, redirections refusées).
	HTTP *http.Client
	// SMTPTLS : configuration TLS des envois SMTP (tests : certificat de confiance).
	SMTPTLS *tls.Config
	// Dedup : fenêtre de dédoublonnage des évènements portant la même DedupKey (défaut 15 min).
	Dedup time.Duration
	// Retries : nombre d'essais par envoi (défaut 3) ; RetryDelay : pause de base (défaut 1 s).
	Retries    int
	RetryDelay time.Duration
	// AllowPrivate autorise les destinations non publiques (ntfy ou SMTP local) et les ports SMTP libres. Défaut : lu dans
	// BAMBU_NOTIFY_ALLOW_PRIVATE=1 ; sinon refusé.
	AllowPrivate bool
}

// Service gère les canaux et les envois.
type Service struct {
	d     Deps
	log   *slog.Logger
	mu    sync.Mutex
	seen  map[string]time.Time
	pages map[string]time.Time // nonces de la page de saisie
}

// New construit le service.
func New(d Deps) (*Service, error) {
	if d.Store == nil || d.Vault == nil {
		return nil, errors.New("notify : store et coffre requis")
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Now == nil {
		d.Now = func() time.Time { return d.Store.Now() }
	}
	if d.Dedup == 0 {
		d.Dedup = 15 * time.Minute
	}
	if d.Retries == 0 {
		d.Retries = 3
	}
	if d.RetryDelay == 0 {
		d.RetryDelay = time.Second
	}
	if os.Getenv("BAMBU_NOTIFY_ALLOW_PRIVATE") == "1" {
		d.AllowPrivate = true
	}
	s := &Service{d: d, log: d.Log, seen: map[string]time.Time{}, pages: map[string]time.Time{}}
	if d.HTTP == nil {
		d.HTTP = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{DialContext: s.dialer(15 * time.Second).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second}}
		s.d = d
	}
	return s, nil
}

func (s *Service) now() time.Time { return s.d.Now().UTC() }

// BaseURL : base des liens.
func (s *Service) BaseURL() string {
	if s.d.BaseURL != nil {
		return strings.TrimRight(s.d.BaseURL(), "/")
	}
	return "http://127.0.0.1:7878"
}

// SetupURL : page humaine de saisie des secrets.
func (s *Service) SetupURL() string { return s.BaseURL() + "/setup/notifications" }

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{0,39}$`)

// ErrInvalid : entrée invalide.
var ErrInvalid = errors.New("entrée invalide")

func invalidf(f string, a ...any) error {
	return fmt.Errorf("%w : %s", ErrInvalid, fmt.Sprintf(f, a...))
}

func (s *Service) validateInput(in *ChannelInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if !nameRe.MatchString(in.Name) {
		return invalidf("nom de canal : 1 à 40 caractères (lettres, chiffres, espace, - _)")
	}
	switch in.Kind {
	case KindNtfy, KindWebhook, KindEmail:
	default:
		return invalidf("type de canal %q : ntfy, webhook ou email", in.Kind)
	}
	known := map[string]bool{}
	for _, e := range AllEvents {
		known[e] = true
	}
	seen := map[string]bool{}
	var evs []string
	for _, e := range in.Events {
		if !known[e] {
			return invalidf("évènement inconnu %q (connus : %s)", e, strings.Join(AllEvents, ", "))
		}
		if !seen[e] {
			seen[e] = true
			evs = append(evs, e)
		}
	}
	sort.Strings(evs)
	in.Events = evs
	c := &in.Config
	if in.Kind != KindEmail {
		if c.SMTPHost != "" || c.SMTPUser != "" || c.From != "" || len(c.To) > 0 {
			return invalidf("les réglages SMTP ne concernent que les canaux email")
		}
	}
	if in.Kind == KindNtfy {
		switch c.Priority {
		case "", "min", "low", "default", "high", "max":
		default:
			return invalidf("ntfy_priority %q", c.Priority)
		}
	}
	if in.Kind == KindEmail {
		c.SMTPHost = strings.TrimSpace(c.SMTPHost)
		if c.SMTPHost == "" || strings.ContainsAny(c.SMTPHost, " /\\@") {
			return invalidf("smtp_host requis (nom d'hôte seul)")
		}
		if c.SMTPTLS == "" {
			c.SMTPTLS = "starttls"
			if c.SMTPPort == 465 {
				c.SMTPTLS = "implicit"
			}
		}
		if c.SMTPTLS != "starttls" && c.SMTPTLS != "implicit" {
			return invalidf("smtp_tls : starttls ou implicit (le SMTP en clair n'est pas pris en charge)")
		}
		if c.SMTPPort == 0 {
			c.SMTPPort = 587
			if c.SMTPTLS == "implicit" {
				c.SMTPPort = 465
			}
		}
		if c.SMTPPort < 1 || c.SMTPPort > 65535 {
			return invalidf("smtp_port hors plage")
		}
		if err := s.checkSMTPDestination(c.SMTPHost, c.SMTPPort); err != nil {
			return err
		}
		if _, err := mail.ParseAddress(c.From); err != nil {
			return invalidf("from : adresse invalide")
		}
		if len(c.To) == 0 || len(c.To) > 10 {
			return invalidf("to : 1 à 10 destinataires")
		}
		for _, a := range c.To {
			if _, err := mail.ParseAddress(a); err != nil {
				return invalidf("to : adresse invalide %q", a)
			}
		}
		if strings.ContainsAny(c.SMTPUser, "\r\n") {
			return invalidf("smtp_user invalide")
		}
	}
	return nil
}

func channelID(name string) string {
	return "nc_" + strings.ToLower(strings.NewReplacer(" ", "-", "_", "-").Replace(name))
}

// UpsertChannel crée ou met à jour un canal (par nom) avec sa destination complète. RÉSERVÉ AUX PAGES HUMAINES : l'outil MCP
// passe par PatchChannel, qui ne touche ni l'hôte, ni le port, ni l'utilisateur, ni les destinataires. Aucun secret n'est accepté ici.
func (s *Service) UpsertChannel(ctx context.Context, in ChannelInput) (*ChannelView, error) {
	if err := s.validateInput(&in); err != nil {
		return nil, err
	}
	id := channelID(in.Name)
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	evs, _ := json.Marshal(in.Events)
	if in.Events == nil {
		evs = []byte("[]")
	}
	cfg, _ := json.Marshal(in.Config)
	now := store.FormatTime(s.now())
	var existing, oldCfg string
	err := s.d.Store.DB().QueryRowContext(ctx, `SELECT kind,config FROM notify_channels WHERE id=?`, id).Scan(&existing, &oldCfg)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = s.d.Store.DB().ExecContext(ctx, `INSERT INTO notify_channels(id,name,kind,enabled,events,config,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			id, in.Name, in.Kind, b2i(enabled), string(evs), string(cfg), now, now)
	case err == nil:
		if existing != in.Kind {
			return nil, invalidf("le canal %q existe avec le type %s : le supprimer d'abord pour changer de type", in.Name, existing)
		}
		_, err = s.d.Store.DB().ExecContext(ctx, `UPDATE notify_channels SET enabled=?, events=?, config=?, updated_at=? WHERE id=?`, b2i(enabled), string(evs), string(cfg), now, id)
		var prev ChannelConfig
		_ = json.Unmarshal([]byte(oldCfg), &prev)
		if err == nil && in.Kind == KindEmail && bindingOf(prev) != bindingOf(in.Config) {
			// la destination change : le mot de passe enregistré ne doit pas la suivre
			s.dropBoundSecret(id)
			_ = s.d.Store.Audit(ctx, "human:web", "notify.secret_invalidated", "notify_channel", id, map[string]any{"field": "password"})
		}
	}
	if err != nil {
		return nil, err
	}
	_ = s.d.Store.Audit(ctx, "notify", "notify.channel_saved", "notify_channel", id, map[string]any{"kind": in.Kind, "enabled": enabled, "events": in.Events})
	return s.GetChannel(ctx, id)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ChannelPatch : ce que le modèle peut changer sur un canal EXISTANT (jamais sa destination).
type ChannelPatch struct {
	Name    string   `json:"name" jsonschema:"nom d'un canal existant"`
	Enabled *bool    `json:"enabled,omitempty" jsonschema:"activer ou désactiver le canal"`
	Events  []string `json:"events,omitempty" jsonschema:"évènements abonnés ; [] = tous ; omis = inchangé"`
}

// PatchChannel active ou désactive un canal existant et choisit ses évènements. Hôte, port, utilisateur, destinataires,
// URL et secrets sont intouchables ici (page humaine uniquement). Un canal inconnu est refusé : le modèle ne crée rien.
func (s *Service) PatchChannel(ctx context.Context, p ChannelPatch) (*ChannelView, error) {
	ch, err := s.find(ctx, strings.TrimSpace(p.Name))
	if err != nil {
		return nil, err
	}
	enabled := ch.Enabled
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	evs := ch.Events
	if p.Events != nil {
		known := map[string]bool{}
		for _, e := range AllEvents {
			known[e] = true
		}
		seen := map[string]bool{}
		evs = []string{}
		for _, e := range p.Events {
			if !known[e] {
				return nil, invalidf("évènement inconnu %q (connus : %s)", e, strings.Join(AllEvents, ", "))
			}
			if !seen[e] {
				seen[e] = true
				evs = append(evs, e)
			}
		}
		sort.Strings(evs)
	}
	b, _ := json.Marshal(evs)
	if _, err := s.d.Store.DB().ExecContext(ctx, `UPDATE notify_channels SET enabled=?, events=?, updated_at=? WHERE id=?`, b2i(enabled), string(b), store.FormatTime(s.now()), ch.ID); err != nil {
		return nil, err
	}
	_ = s.d.Store.Audit(ctx, "notify", "notify.channel_patched", "notify_channel", ch.ID, map[string]any{"enabled": enabled, "events": evs})
	return s.GetChannel(ctx, ch.ID)
}

// bindingOf : empreinte de la destination (hôte, port, utilisateur) à laquelle un mot de passe SMTP est lié.
func bindingOf(c ChannelConfig) string {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(c.SMTPHost)) + "|" + strconv.Itoa(c.SMTPPort) + "|" + c.SMTPUser))
	return hex.EncodeToString(h[:])
}

func bindKey(channelID string) string { return vaultKey(channelID, "password") + ":bind" }

func (s *Service) dropBoundSecret(channelID string) {
	_ = s.d.Vault.DeleteAccessCode(vaultKey(channelID, "password"))
	_ = s.d.Vault.DeleteAccessCode(bindKey(channelID))
}

// DeleteChannel supprime un canal (par nom ou identifiant) et ses secrets du coffre.
func (s *Service) DeleteChannel(ctx context.Context, nameOrID string) error {
	ch, err := s.find(ctx, nameOrID)
	if err != nil {
		return err
	}
	if _, err := s.d.Store.DB().ExecContext(ctx, `DELETE FROM notify_channels WHERE id=?`, ch.ID); err != nil {
		return err
	}
	for _, f := range secretFields[ch.Kind] {
		_ = s.d.Vault.DeleteAccessCode(vaultKey(ch.ID, f.Name))
	}
	_ = s.d.Vault.DeleteAccessCode(bindKey(ch.ID))
	_ = s.d.Store.Audit(ctx, "notify", "notify.channel_deleted", "notify_channel", ch.ID, nil)
	return nil
}

// ErrNotFound : canal inconnu.
var ErrNotFound = errors.New("introuvable")

func (s *Service) find(ctx context.Context, nameOrID string) (*ChannelView, error) {
	var id string
	err := s.d.Store.DB().QueryRowContext(ctx, `SELECT id FROM notify_channels WHERE id=? OR name=?`, nameOrID, nameOrID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w : canal %q", ErrNotFound, nameOrID)
	}
	if err != nil {
		return nil, err
	}
	return s.GetChannel(ctx, id)
}

// GetChannel renvoie un canal (sans secret).
func (s *Service) GetChannel(ctx context.Context, id string) (*ChannelView, error) {
	row := s.d.Store.DB().QueryRowContext(ctx, `SELECT id,name,kind,enabled,events,config,created_at,updated_at FROM notify_channels WHERE id=?`, id)
	v, err := s.scan(ctx, row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w : canal %q", ErrNotFound, id)
	}
	return v, err
}

type scanner interface{ Scan(...any) error }

func (s *Service) scan(ctx context.Context, r scanner) (*ChannelView, error) {
	var v ChannelView
	var en int
	var evs, cfg, ca, ua string
	if err := r.Scan(&v.ID, &v.Name, &v.Kind, &en, &evs, &cfg, &ca, &ua); err != nil {
		return nil, err
	}
	v.Enabled = en == 1
	_ = json.Unmarshal([]byte(evs), &v.Events)
	_ = json.Unmarshal([]byte(cfg), &v.Config)
	if v.Events == nil {
		v.Events = []string{}
	}
	v.CreatedAt, _ = store.ParseTime(ca)
	v.UpdatedAt, _ = store.ParseTime(ua)
	v.Ready = true
	for _, f := range secretFields[v.Kind] {
		_, src, _ := s.secret(ctx, v, f.Name)
		st := SecretStatus{SecretField: f, Set: src != "", Source: src}
		v.Secrets = append(v.Secrets, st)
		if f.Required && !st.Set {
			v.Ready = false
			v.Missing = append(v.Missing, "secret "+f.Name)
		}
	}
	if v.Kind == KindEmail && (v.Config.SMTPHost == "" || len(v.Config.To) == 0) {
		v.Ready = false
		v.Missing = append(v.Missing, "configuration SMTP")
	}
	return &v, nil
}

// ListChannels liste les canaux (sans secret).
func (s *Service) ListChannels(ctx context.Context) ([]ChannelView, error) {
	rows, err := s.d.Store.DB().QueryContext(ctx, `SELECT id,name,kind,enabled,events,config,created_at,updated_at FROM notify_channels ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChannelView{}
	for rows.Next() {
		v, err := s.scan(ctx, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func vaultKey(channelID, field string) string { return "notify:" + channelID + ":" + field }

var envNameRe = regexp.MustCompile(`[^A-Z0-9]+`)

// EnvName renvoie le nom de la variable d'environnement d'un secret : BAMBU_NOTIFY_<CANAL>_<CHAMP>
// (et la même suffixée _FILE pour désigner un fichier qui contient la valeur).
func EnvName(channelName, field string) string {
	return "BAMBU_NOTIFY_" + strings.Trim(envNameRe.ReplaceAllString(strings.ToUpper(channelName), "_"), "_") + "_" + strings.ToUpper(field)
}

// secret résout un secret : variable d'environnement, puis fichier (_FILE), puis coffre. Renvoie (valeur, source).
func (s *Service) secret(ctx context.Context, ch ChannelView, field string) (string, string, error) {
	env := EnvName(ch.Name, field)
	if v := strings.TrimSpace(os.Getenv(env)); v != "" {
		return v, "env", nil
	}
	if p := strings.TrimSpace(os.Getenv(env + "_FILE")); p != "" {
		if b, err := readSmallFile(p); err == nil && strings.TrimSpace(string(b)) != "" {
			return strings.TrimSpace(string(b)), "file", nil
		}
	}
	k := vaultKey(ch.ID, field)
	if s.d.Vault.HasAccessCode(k) {
		if ch.Kind == KindEmail && field == "password" {
			// lié à (hôte, port, utilisateur) : un secret sans liaison valide est ignoré
			b, err := s.d.Vault.AccessCode(ctx, bindKey(ch.ID))
			if err != nil || b != bindingOf(ch.Config) {
				return "", "", nil
			}
		}
		v, err := s.d.Vault.AccessCode(ctx, k)
		if err != nil {
			return "", "", err
		}
		return v, "vault", nil
	}
	return "", "", nil
}

func readSmallFile(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, 8192)
	n, _ := f.Read(b)
	return b[:n], nil
}

// SetSecret enregistre un secret dans le coffre (appelé par la page humaine UNIQUEMENT).
func (s *Service) SetSecret(ctx context.Context, channelID, field, value string) error {
	ch, err := s.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}
	ok := false
	for _, f := range secretFields[ch.Kind] {
		ok = ok || f.Name == field
	}
	if !ok {
		return invalidf("secret %q inconnu pour un canal %s", field, ch.Kind)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return invalidf("valeur vide")
	}
	if len(value) > 2048 || strings.ContainsAny(value, "\r\n") {
		return invalidf("valeur trop longue ou multi-lignes")
	}
	switch field {
	case "url":
		if err := s.checkURL(value); err != nil {
			return err
		}
	}
	if ch.Kind == KindEmail && field == "password" {
		if err := s.d.Vault.SetAccessCode(ctx, bindKey(ch.ID), bindingOf(ch.Config)); err != nil {
			return err
		}
	}
	if err := s.d.Vault.SetAccessCode(ctx, vaultKey(ch.ID, field), value); err != nil {
		return err
	}
	_ = s.d.Store.Audit(ctx, "human:web", "notify.secret_set", "notify_channel", ch.ID, map[string]any{"field": field}) // jamais la valeur
	return nil
}

// ClearSecret retire un secret du coffre.
func (s *Service) ClearSecret(ctx context.Context, channelID, field string) error {
	_ = s.d.Vault.DeleteAccessCode(vaultKey(channelID, field))
	if field == "password" {
		_ = s.d.Vault.DeleteAccessCode(bindKey(channelID))
	}
	_ = s.d.Store.Audit(ctx, "human:web", "notify.secret_cleared", "notify_channel", channelID, map[string]any{"field": field})
	return nil
}

// Result : issue d'un envoi vers un canal.
type Result struct {
	Channel string `json:"channel"`
	Kind    string `json:"kind"`
	OK      bool   `json:"ok"`
	Skipped string `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Emit envoie l'évènement à tous les canaux actifs abonnés (synchrone, avec essais). Les secrets ne sortent pas du service.
func (s *Service) Emit(ctx context.Context, e Event) []Result {
	if e.At.IsZero() {
		e.At = s.now()
	}
	if e.DedupKey != "" {
		s.mu.Lock()
		for k, t := range s.seen {
			if s.now().Sub(t) > s.d.Dedup {
				delete(s.seen, k)
			}
		}
		if _, dup := s.seen[e.DedupKey]; dup {
			s.mu.Unlock()
			return []Result{{Skipped: "dédoublonné (même évènement récemment notifié)"}}
		}
		s.seen[e.DedupKey] = s.now()
		s.mu.Unlock()
	}
	chs, err := s.ListChannels(ctx)
	if err != nil {
		s.log.Warn("notifications : canaux illisibles", "err", err)
		return nil
	}
	var res []Result
	for _, ch := range chs {
		if !ch.Enabled || (e.Kind != EvTest && len(ch.Events) > 0 && !contains(ch.Events, e.Kind)) {
			continue
		}
		res = append(res, s.sendTo(ctx, ch, e))
	}
	return res
}

// EmitAsync lance Emit sans bloquer l'appelant.
func (s *Service) EmitAsync(e Event) {
	go func() {
		defer func() { _ = recover() }()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		s.Emit(ctx, e)
	}()
}

// Test envoie une notification d'essai à un canal (par nom ou id) ou à tous les canaux actifs si nameOrID est vide.
func (s *Service) Test(ctx context.Context, nameOrID string) ([]Result, error) {
	e := Event{Kind: EvTest, Title: "Test de notification — Atelier Bambu", Message: "Si tu lis ceci, le canal fonctionne. Aucun secret n'est contenu dans ce message.", Link: s.BaseURL() + "/impression"}
	if nameOrID == "" {
		return s.Emit(ctx, e), nil
	}
	ch, err := s.find(ctx, nameOrID)
	if err != nil {
		return nil, err
	}
	return []Result{s.sendTo(ctx, *ch, e)}, nil
}

func (s *Service) sendTo(ctx context.Context, ch ChannelView, e Event) Result {
	r := Result{Channel: ch.Name, Kind: ch.Kind}
	if !ch.Ready {
		r.Error = "canal incomplet : " + strings.Join(ch.Missing, ", ") + " — l'humain les saisit sur " + s.SetupURL()
		s.logSend(ctx, ch, e, r)
		return r
	}
	var err error
	for try := 0; try < s.d.Retries; try++ {
		if try > 0 {
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-time.After(time.Duration(try) * s.d.RetryDelay):
			}
			if ctx.Err() != nil {
				break
			}
		}
		switch ch.Kind {
		case KindNtfy:
			err = s.sendNtfy(ctx, ch, e)
		case KindWebhook:
			err = s.sendWebhook(ctx, ch, e)
		case KindEmail:
			err = s.sendEmail(ctx, ch, e)
		}
		if err == nil {
			break
		}
		var perm *permanentError
		if errors.As(err, &perm) {
			break
		}
	}
	if err != nil {
		r.Error = scrubErr(err)
	} else {
		r.OK = true
	}
	s.logSend(ctx, ch, e, r)
	return r
}

func (s *Service) logSend(ctx context.Context, ch ChannelView, e Event, r Result) {
	_, _ = s.d.Store.DB().ExecContext(ctx, `INSERT INTO notify_log(at,channel_id,event,ok,error) VALUES(?,?,?,?,?)`,
		store.FormatTime(s.now()), ch.ID, e.Kind, b2i(r.OK), r.Error)
	if !r.OK {
		s.log.Warn("notification non envoyée", "channel", ch.Name, "event", e.Kind, "err", r.Error)
	}
}

// LogEntry : une ligne du journal d'envoi.
type LogEntry struct {
	At      time.Time `json:"at"`
	Channel string    `json:"channel_id"`
	Event   string    `json:"event"`
	OK      bool      `json:"ok"`
	Error   string    `json:"error,omitempty"`
}

// Log renvoie les derniers envois.
func (s *Service) Log(ctx context.Context, limit int) ([]LogEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	rows, err := s.d.Store.DB().QueryContext(ctx, `SELECT at,channel_id,event,ok,COALESCE(error,'') FROM notify_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogEntry{}
	for rows.Next() {
		var l LogEntry
		var at string
		var ok int
		if err := rows.Scan(&at, &l.Channel, &l.Event, &ok, &l.Error); err != nil {
			return nil, err
		}
		l.At, _ = store.ParseTime(at)
		l.OK = ok == 1
		out = append(out, l)
	}
	return out, rows.Err()
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}
