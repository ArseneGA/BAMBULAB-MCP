package contracts

import (
	"encoding/json"
	"time"
)

// JobState est l'état d'un travail de la file.
type JobState string

const (
	JobQueued    JobState = "queued"
	JobRunning   JobState = "running"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
	JobCancelled JobState = "cancelled"
	// JobUnknown : travail interrompu non rejouable ; l'issue réelle est inconnue.
	JobUnknown JobState = "unknown"
)

// Terminal indique qu'aucune transition automatique ne suivra.
func (s JobState) Terminal() bool {
	switch s {
	case JobSucceeded, JobFailed, JobCancelled, JobUnknown:
		return true
	}
	return false
}

// JobClass borne la concurrence : un worker lourd par défaut (dossier §3).
type JobClass string

const (
	ClassHeavy JobClass = "heavy"
	ClassLight JobClass = "light"
)

// JobError est l'erreur structurée d'un travail.
type JobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Job est un travail persistant.
type Job struct {
	ID              string          `json:"id"`
	Kind            string          `json:"kind"`
	Class           JobClass        `json:"class"`
	State           JobState        `json:"state"`
	Progress        float64         `json:"progress" jsonschema:"avancement de 0 à 1"`
	Message         string          `json:"message,omitempty"`
	Logs            []string        `json:"logs,omitempty"`
	Params          json.RawMessage `json:"params,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
	Error           *JobError       `json:"error,omitempty"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	Attempts        int             `json:"attempts"`
	CancelRequested bool            `json:"cancel_requested,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	HeartbeatAt     *time.Time      `json:"heartbeat_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
}
