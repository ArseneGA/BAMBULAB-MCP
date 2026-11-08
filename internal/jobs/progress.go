package jobs

import (
	"context"
	"database/sql"
	"math"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// Detail est l'avancement fin d'un travail en cours (en mémoire : utile seulement tant qu'il s'exécute).
type Detail struct {
	Phase      string  // « découpe par Bambu Studio », « envoi FTPS »…
	Label      string  // libellé lisible de la barre, ex. « Dépôt sur la P1S » (sinon déduit du genre de travail)
	BytesDone  int64   // octets déjà transférés (envois)
	BytesTotal int64   // octets à transférer
	Expected   float64 // durée attendue en secondes (découpe : estimation)
	At         time.Time
}

var details sync.Map // job id -> Detail

// SetDetail publie l'avancement fin du travail (sans accès disque).
func (c *JobCtx) SetDetail(d Detail) {
	d.At = time.Now()
	details.Store(c.Job.ID, d)
}

// GetDetail relit l'avancement fin d'un travail.
func GetDetail(id string) (Detail, bool) {
	v, ok := details.Load(id)
	if !ok {
		return Detail{}, false
	}
	return v.(Detail), true
}

// ForgetDetail oublie l'avancement fin (travail terminé).
func ForgetDetail(id string) { details.Delete(id) }

// StallAfter : un travail « running » sans battement depuis plus longtemps est déclaré bloqué.
const StallAfter = 45 * time.Second

// Progress est la vue d'avancement d'un travail pour les outils : pourcentage, phase, octets, durée restante.
type Progress struct {
	JobID          string   `json:"job_id"`
	State          string   `json:"state"`
	Percent        int      `json:"percent" jsonschema:"avancement 0..100 (octets envoyés / total pour un envoi, temps écoulé / attendu pour une découpe)"`
	Phase          string   `json:"phase" jsonschema:"libellé libre de la phase en cours (toujours présent ; le code stable est phase_code)"`
	Message        string   `json:"message,omitempty"`
	BytesDone      int64    `json:"bytes_done,omitempty"`
	BytesTotal     int64    `json:"bytes_total,omitempty"`
	ElapsedSeconds int      `json:"elapsed_seconds"`
	ETASeconds     *int     `json:"eta_seconds,omitempty"`
	QueuePosition  *int     `json:"queue_position,omitempty" jsonschema:"1 = prochain à démarrer ; renseigné seulement pour un travail en file"`
	RunningAhead   int      `json:"running_ahead,omitempty" jsonschema:"travaux de la même classe en cours d'exécution devant celui-ci"`
	Stalled        bool     `json:"stalled,omitempty" jsonschema:"vrai si le travail est « en cours » mais sans signe de vie depuis longtemps"`
	Estimated      bool     `json:"estimated,omitempty" jsonschema:"vrai si le pourcentage est une estimation par le temps (Bambu Studio ne publie pas de progression)"`
	Hint           string   `json:"hint,omitempty"`
	HeartbeatAge   *int     `json:"heartbeat_age_seconds,omitempty"`
	Terminal       bool     `json:"terminal"`
	Notes          []string `json:"notes,omitempty"`
	// Champs structurés STABLES pour dessiner une barre (documentés dans docs/UTILISATION.md) : percent, done_bytes,
	// total_bytes, eta_seconds, phase_code / phase_label (phase est le libellé libre historique), bar et text.
	PhaseCode  string `json:"phase_code" jsonschema:"phase STABLE : queued | slicing | uploading | starting | importing | analyzing | rendering | converting | exporting | running | done | failed | cancelled | unknown"`
	PhaseLabel string `json:"phase_label" jsonschema:"libellé lisible de la phase, ex. « Dépôt sur la P1S »"`
	DoneBytes  int64  `json:"done_bytes" jsonschema:"octets déjà transférés (0 si le travail n'a pas d'octets)"`
	TotalBytes int64  `json:"total_bytes" jsonschema:"octets à transférer au total (0 si inconnu ou sans objet)"`
	Bar        string `json:"bar" jsonschema:"barre texte de 10 cases, ex. ███████░░░"`
	Text       string `json:"text" jsonschema:"ligne lisible : « Dépôt sur la P1S : ███████░░░ 72 % (21,6 / 30 Mo, ~1 min) »"`
}

// ProgressOf calcule l'avancement lisible d'un travail (et ses champs structurés stables : voir finalize).
func (q *Queue) ProgressOf(ctx context.Context, j contracts.Job) Progress {
	p := q.progressOf(ctx, j)
	label := ""
	if d, ok := GetDetail(j.ID); ok && !j.State.Terminal() && j.State != contracts.JobQueued {
		label = d.Label
	}
	p.finalize(j, label)
	return p
}

func (q *Queue) progressOf(ctx context.Context, j contracts.Job) Progress {
	now := time.Now()
	p := Progress{JobID: j.ID, State: string(j.State), Message: j.Message, Terminal: j.State.Terminal()}
	p.Percent = int(math.Round(j.Progress * 100))
	if j.State.Terminal() {
		if j.State == contracts.JobSucceeded {
			p.Percent = 100
		}
		if j.StartedAt != nil && j.FinishedAt != nil {
			p.ElapsedSeconds = int(j.FinishedAt.Sub(*j.StartedAt).Seconds())
		}
		return p
	}
	if j.State == contracts.JobQueued {
		p.Percent = 0
		p.Phase = "en file d'attente"
		var ahead, running int
		_ = q.s.Tx(ctx, func(tx *sql.Tx) error {
			_ = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE state='queued' AND class=? AND created_at < (SELECT created_at FROM jobs WHERE id=?)`, string(j.Class), j.ID).Scan(&ahead)
			_ = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE state='running' AND class=?`, string(j.Class)).Scan(&running)
			return nil
		})
		pos := ahead + 1
		p.QueuePosition, p.RunningAhead = &pos, running
		p.Hint = "Ce travail attend son tour (une seule découpe à la fois) : rien n'est bloqué, suivre avec get_job_status."
		p.ElapsedSeconds = int(now.Sub(j.CreatedAt).Seconds())
		return p
	}
	if j.StartedAt != nil {
		p.ElapsedSeconds = int(now.Sub(*j.StartedAt).Seconds())
	}
	if j.HeartbeatAt != nil {
		age := int(now.Sub(*j.HeartbeatAt).Seconds())
		p.HeartbeatAge = &age
		if now.Sub(*j.HeartbeatAt) > StallAfter {
			p.Stalled = true
		}
	}
	if d, ok := GetDetail(j.ID); ok {
		p.Phase = d.Phase
		switch {
		case d.BytesTotal > 0:
			p.BytesDone, p.BytesTotal = d.BytesDone, d.BytesTotal
			frac := math.Min(1, float64(d.BytesDone)/float64(d.BytesTotal))
			p.Percent = int(math.Round(frac * 100))
			if d.BytesDone > 0 && p.ElapsedSeconds > 0 {
				rate := float64(d.BytesDone) / float64(max(p.ElapsedSeconds, 1))
				if rate > 0 {
					eta := int(float64(d.BytesTotal-d.BytesDone) / rate)
					p.ETASeconds = &eta
				}
			}
		case d.Expected > 0:
			// estimation asymptotique : n'atteint jamais 100 tant que le travail n'est pas fini
			el := now.Sub(startOr(j, now)).Seconds()
			frac := 0.95 * (1 - math.Exp(-1.6*el/d.Expected))
			p.Percent = int(math.Round(frac * 100))
			p.Estimated = true
			eta := int(math.Max(d.Expected-el, 0))
			p.ETASeconds = &eta
		}
	}
	if p.Stalled {
		p.Hint = "Aucun signe de vie du travail depuis plus de 45 s : il est probablement bloqué ; cancel_job puis relancer."
	}
	return p
}

func startOr(j contracts.Job, def time.Time) time.Time {
	if j.StartedAt != nil {
		return *j.StartedAt
	}
	return def
}
