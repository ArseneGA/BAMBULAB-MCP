package printer

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
)

// Reconnexion MQTT à délai croissant plafonné, avec reprise immédiate dès qu'un port répond.
//
// Avant : une tentative toutes les 2 s, sans rien savoir de la panne ; après le redémarrage d'une P1S (« no route to
// host » pendant plus de 20 min) le journal ne disait rien d'exploitable. Maintenant : le délai double à chaque échec
// (ReconnectDelay, 2 s par défaut, jusqu'à ReconnectMaxDefault = 60 s) MAIS un sondage TCP du port MQTT toutes les
// ReconnectProbeDefault (1 s) coupe l'attente dès que l'imprimante répond. La dernière connexion réussie, la dernière
// erreur et le prochain essai sont lisibles (ConnInfo) pour diagnose_connection. Aucune commande n'est jamais rejouée.

const (
	// ReconnectMaxDefault : plafond du délai entre deux tentatives.
	ReconnectMaxDefault = 60 * time.Second
	// ReconnectProbeDefault : période du sondage du port pendant l'attente.
	ReconnectProbeDefault = time.Second
)

// ConnInfo : ce que l'adaptateur sait de sa liaison MQTT (aucun secret).
type ConnInfo struct {
	Connected     bool      `json:"connected"`
	LastConnected time.Time `json:"last_connected,omitzero" jsonschema:"dernière connexion MQTT réussie"`
	DownSince     time.Time `json:"down_since,omitzero" jsonschema:"depuis quand la liaison est coupée"`
	Attempts      int       `json:"failed_attempts" jsonschema:"échecs de reconnexion consécutifs"`
	LastError     string    `json:"last_error,omitempty"`
	LastErrorAt   time.Time `json:"last_error_at,omitzero"`
	NextTryAt     time.Time `json:"next_try_at,omitzero"`
}

// ConnectionReporter est implémenté par les adaptateurs qui savent décrire leur liaison.
type ConnectionReporter interface{ ConnInfo() ConnInfo }

type reconnectState struct {
	mu   sync.Mutex
	info ConnInfo
}

// ErrStateNotObserved : la commande a été acquittée par l'imprimante (PUBACK, éventuellement result:success) mais l'état
// attendu n'a pas été observé dans le délai. Ce n'est PAS un refus : l'effet réel doit être vérifié (arrêt qui laisse
// gcode_state à RUNNING, etc.). Jamais de renvoi automatique.
var ErrStateNotObserved = errors.New("printer: command acknowledged but expected state not observed")

// ConnInfo décrit la liaison (Bambu implémente ConnectionReporter).
func (b *Bambu) ConnInfo() ConnInfo {
	b.rc.mu.Lock()
	i := b.rc.info
	b.rc.mu.Unlock()
	i.Connected = b.current() != nil
	return i
}

func (b *Bambu) rcNoteOK() {
	b.rc.mu.Lock()
	b.rc.info.LastConnected = b.cfg.Now()
	b.rc.info.DownSince, b.rc.info.Attempts, b.rc.info.NextTryAt = time.Time{}, 0, time.Time{}
	b.rc.info.LastError = ""
	b.rc.mu.Unlock()
}

func (b *Bambu) rcNoteLost() {
	b.rc.mu.Lock()
	if b.rc.info.DownSince.IsZero() {
		b.rc.info.DownSince = b.cfg.Now()
	}
	b.rc.mu.Unlock()
}

func (b *Bambu) rcNoteFail(err error) {
	b.rc.mu.Lock()
	b.rc.info.Attempts++
	if b.rc.info.DownSince.IsZero() {
		b.rc.info.DownSince = b.cfg.Now()
	}
	b.rc.info.LastError, b.rc.info.LastErrorAt = err.Error(), b.cfg.Now()
	b.rc.mu.Unlock()
}

// reconnectBackoff : base, 2×base, 4×base… plafonné à max. attempt = échecs déjà subis.
func reconnectBackoff(base, max time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = 2 * time.Second
	}
	if max < base {
		max = base
	}
	d := base
	for i := 0; i < attempt && d < max; i++ {
		d *= 2
	}
	return min(d, max)
}

// probePort : le port TCP répond-il (poignée de main TCP seulement, rien n'est envoyé) ?
func probePort(ctx context.Context, addr string, timeout time.Duration) bool {
	d := net.Dialer{Timeout: timeout}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// waitReconnect attend le délai croissant, en sondant le port MQTT : retourne dès qu'il répond (reprise immédiate).
// false si le contexte de l'adaptateur est terminé.
func (b *Bambu) waitReconnect() bool {
	b.rc.mu.Lock()
	attempt := b.rc.info.Attempts
	b.rc.mu.Unlock()
	wait := reconnectBackoff(b.cfg.ReconnectDelay, ReconnectMaxDefault, attempt)
	b.rc.mu.Lock()
	b.rc.info.NextTryAt = b.cfg.Now().Add(wait)
	b.rc.mu.Unlock()
	end := time.NewTimer(wait)
	defer end.Stop()
	probe := time.NewTicker(ReconnectProbeDefault)
	defer probe.Stop()
	addr := net.JoinHostPort(b.cfg.Host, strconv.Itoa(b.cfg.MQTTPort))
	for {
		select {
		case <-b.ctx.Done():
			return false
		case <-end.C:
			return true
		case <-probe.C:
			if attempt > 0 && probePort(b.ctx, addr, 800*time.Millisecond) {
				return true // le port répond : inutile d'attendre la fin du délai
			}
		}
	}
}
