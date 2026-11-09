package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Limitation de débit de la surface publique (spec G1 F2). Par adresse client (opts.clientAddr : X-Forwarded-For
// n'est cru que d'un proxy de confiance ; sans en-tête, toutes les requêtes d'un même proxy partagent une adresse,
// ce qui ne peut que resserrer la limite). Dépassement : 429 + Retry-After.
//
// Ce qui existait déjà et n'est PAS doublé : internal/oauth limite POST /oauth/register (30 puis 1 toutes les 3 s)
// et GET /oauth/authorize (30 puis 1 toutes les 2 s) par adresse, ainsi que 30 clients récents par adresse ; les
// échecs d'enregistrement de navigateur (/enroll) et de code de lancement ont leurs verrous. Ici s'ajoutent les
// plafonds qui manquaient : un plafond HORAIRE d'enregistrements (le seau d'oauth se vide en 90 s, ce qui laisse
// 1 200 enregistrements/heure), POST /oauth/token (aucune limite) et /mcp SANS jeton valide (401 en boucle). Les
// appels /mcp portant un jeton valide ne sont jamais comptés.

// PublicRateLimits : plafonds par adresse. Zéro : valeur par défaut.
type PublicRateLimits struct {
	RegisterPerHour int // POST /oauth/register (défaut 10)
	TokenPerMinute  int // POST /oauth/token (défaut 60)
	MCPPerMinute    int // /mcp sans jeton valide (défaut 60)
}

// Bornes : une limite configurable reste une limite.
const maxPublicRate = 100000

// DefaultPublicRateLimits : valeurs par défaut.
func DefaultPublicRateLimits() PublicRateLimits {
	return PublicRateLimits{RegisterPerHour: 10, TokenPerMinute: 60, MCPPerMinute: 60}
}

func (l PublicRateLimits) withDefaults() PublicRateLimits {
	d := DefaultPublicRateLimits()
	if l.RegisterPerHour <= 0 {
		l.RegisterPerHour = d.RegisterPerHour
	}
	if l.TokenPerMinute <= 0 {
		l.TokenPerMinute = d.TokenPerMinute
	}
	if l.MCPPerMinute <= 0 {
		l.MCPPerMinute = d.MCPPerMinute
	}
	return l
}

// ParsePublicRateLimits lit « register=10,token=60,mcp=60 » (register : par heure ; token et mcp : par minute).
// Chaque clé est facultative (défaut) ; chaque valeur est un entier de 1 à 100000. Vide : les défauts.
func ParsePublicRateLimits(s string) (PublicRateLimits, error) {
	out := DefaultPublicRateLimits()
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if !ok || err != nil || n < 1 || n > maxPublicRate {
			return out, fmt.Errorf("limite %q invalide : attendu clé=entier entre 1 et %d (clés register, token, mcp)", part, maxPublicRate)
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "register":
			out.RegisterPerHour = n
		case "token":
			out.TokenPerMinute = n
		case "mcp":
			out.MCPPerMinute = n
		default:
			return out, fmt.Errorf("limite %q inconnue : clés register, token, mcp", part)
		}
	}
	return out, nil
}

type rlBucket struct {
	tokens float64
	at     time.Time
}

type publicLimiter struct {
	mu      sync.Mutex
	b       map[string]*rlBucket
	lim     PublicRateLimits
	now     func() time.Time
	log     *slog.Logger
	lastLog map[string]time.Time
}

func newPublicLimiter(o Options) *publicLimiter {
	return &publicLimiter{b: map[string]*rlBucket{}, lastLog: map[string]time.Time{}, lim: o.PublicRate.withDefaults(), now: time.Now, log: slog.Default()}
}

// allow consomme un jeton du seau (catégorie, adresse). Refus : durée avant le prochain jeton.
func (l *publicLimiter) allow(cat, ip string) (bool, time.Duration) {
	var burst int
	var per time.Duration
	switch cat {
	case "register":
		burst, per = l.lim.RegisterPerHour, time.Hour
	case "token":
		burst, per = l.lim.TokenPerMinute, time.Minute
	default:
		burst, per = l.lim.MCPPerMinute, time.Minute
	}
	every := per / time.Duration(burst)
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.b) > 10000 { // table bornée : on oublie les adresses inactives
		for k, b := range l.b {
			if now.Sub(b.at) > per {
				delete(l.b, k)
			}
		}
		if len(l.b) > 10000 {
			l.b = map[string]*rlBucket{}
		}
	}
	k := cat + "|" + ip
	b := l.b[k]
	if b == nil {
		b = &rlBucket{tokens: float64(burst), at: now}
		l.b[k] = b
	}
	b.tokens = math.Min(float64(burst), b.tokens+float64(now.Sub(b.at))/float64(every))
	b.at = now
	if b.tokens < 1 {
		return false, time.Duration((1 - b.tokens) * float64(every))
	}
	b.tokens--
	return true, 0
}

// noteLimited journalise un refus : catégorie seulement (jamais l'adresse), au plus une ligne par minute et par catégorie.
func (l *publicLimiter) noteLimited(cat string) {
	now := l.now()
	l.mu.Lock()
	last := l.lastLog[cat]
	ok := now.Sub(last) >= time.Minute
	if ok {
		l.lastLog[cat] = now
	}
	l.mu.Unlock()
	if ok {
		l.log.Warn("surface publique : limite de débit atteinte (429)", "categorie", cat)
	}
}

// wrap applique les limites devant next (le garde). La requête /mcp est comptée seulement si elle ne porte pas de
// jeton valide (Bearer statique ou jeton OAuth) : un client authentifié n'est jamais limité par ceux qui frappent à vide.
func (l *publicLimiter) wrap(o Options, next http.Handler) http.Handler {
	var tokenSum [32]byte
	if o.APIToken != "" {
		tokenSum = sha256.Sum256([]byte(o.APIToken))
	}
	authed := func(r *http.Request) bool {
		t := bearerOf(r)
		if t == "" {
			return false
		}
		if o.APIToken != "" {
			sum := sha256.Sum256([]byte(t))
			if subtle.ConstantTimeCompare(sum[:], tokenSum[:]) == 1 {
				return true
			}
		}
		return o.extraBearer != nil && o.extraBearer(r)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cat := ""
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/oauth/register":
			cat = "register"
		case r.Method == http.MethodPost && r.URL.Path == "/oauth/token":
			cat = "token"
		case r.URL.Path == "/mcp" && !authed(r):
			cat = "mcp"
		}
		if cat != "" {
			if ok, wait := l.allow(cat, o.clientAddr(r)); !ok {
				secs := int(math.Ceil(wait.Seconds()))
				if secs < 1 {
					secs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				l.noteLimited(cat)
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "trop de requêtes", "error_description": "réessaie plus tard"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
