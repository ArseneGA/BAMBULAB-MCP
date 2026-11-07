// Package printer définit l'interface d'accès à une imprimante et son
// implémentation Bambu LAN (sous-package bambulan en fichiers de ce paquet).
//
// Règles structurantes (docs/DOSSIER.md §10) :
//   - un lancement exige une ConfirmationProof vérifiée par une fonction injectée ;
//   - on distingue accusé de transport, acceptation et état PRINTING observé ;
//   - après perte réseau suivant un Start, le résultat est ErrStartUnknown :
//     jamais de renvoi automatique, seulement une réconciliation par lecture d'état ;
//   - une imprimante « idle » ne prouve pas que le plateau est vide.
package printer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// State est l'état normalisé d'une imprimante.
type State string

const (
	StateIdle      State = "idle"
	StatePreparing State = "preparing"
	StatePrinting  State = "printing"
	StatePaused    State = "paused"
	StateFinished  State = "finished"
	StateFailed    State = "failed"
	StateOffline   State = "offline"
	StateUnknown   State = "unknown"
)

// HMSError est une entrée HMS brute du rapport (attr/code entiers).
type HMSError struct {
	Attr uint32 `json:"attr"`
	Code uint32 `json:"code"`
}

// Code renvoie le code HMS à 16 chiffres hexadécimaux (attr puis code).
func (h HMSError) Code16() string { return fmt.Sprintf("%08X%08X", h.Attr, h.Code) }

// Temps regroupe les températures en degrés Celsius.
type Temps struct {
	Nozzle       float64 `json:"nozzle"`
	NozzleTarget float64 `json:"nozzle_target"`
	Bed          float64 `json:"bed"`
	BedTarget    float64 `json:"bed_target"`
	Chamber      float64 `json:"chamber"`
}

// Status est l'état normalisé, fusionné à partir des rapports partiels.
type Status struct {
	State      State  `json:"state"`
	GcodeState string `json:"gcode_state"` // valeur brute (IDLE, RUNNING...), vide si inconnue

	Progress       int `json:"progress_percent"`
	Layer          int `json:"layer"`
	TotalLayers    int `json:"total_layers"`
	RemainingMin   int `json:"remaining_minutes"`
	Temps          Temps
	PrintError     int        `json:"print_error"`
	HMS            []HMSError `json:"hms,omitempty"`
	FailReason     string     `json:"fail_reason,omitempty"`
	SubtaskName    string     `json:"subtask_name,omitempty"`
	GcodeFile      string     `json:"gcode_file,omitempty"`
	TaskID         string     `json:"task_id,omitempty"`
	Firmware       string     `json:"firmware,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at"`       // horodatage de la dernière info reçue
	Stale          bool       `json:"stale"`            // UpdatedAt plus ancien que le seuil de fraîcheur
	Connected      bool       `json:"connected"`        // session MQTT active
	FullReportSeen bool       `json:"full_report_seen"` // au moins un pushall complet fusionné
	AMS            *AMSState  `json:"ams,omitempty"`    // état AMS du dernier rapport (nil si aucun)
}

// HasError indique une erreur d'imprimante (print_error ou HMS non vide).
func (s Status) HasError() bool { return s.PrintError != 0 || len(s.HMS) > 0 }

// RemoteRef désigne un artefact présent sur l'imprimante.
type RemoteRef struct {
	Name   string `json:"name"` // nom de fichier sur l'imprimante (contient l'empreinte)
	Path   string `json:"path"` // chemin FTP
	URL    string `json:"url"`  // valeur du champ url de project_file
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"` // empreinte hexadécimale minuscule de l'artefact
	Reused bool   `json:"reused"` // vrai si l'upload a été évité (idempotence)
}

// ConfirmationProof est la preuve produite par le mécanisme de confirmation
// humaine. Elle lie imprimante, fichier, plateau et affectation matière.
type ConfirmationProof struct {
	PrinterID  string    `json:"printer_id"`
	SHA256     string    `json:"sha256"`
	Plate      int       `json:"plate"`
	AMSMapping []int     `json:"ams_mapping"`
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// ProofVerifier vérifie le jeton d'une preuve (signature, usage unique...).
// Elle est injectée ; l'adaptateur ne fabrique jamais de preuve.
type ProofVerifier func(ctx context.Context, proof ConfirmationProof) error

// StartOptions regroupe les options de calibration et de suivi.
type StartOptions struct {
	Timelapse     bool
	BedLeveling   bool
	FlowCali      bool
	VibrationCali bool
	LayerInspect  bool
	BedType       string // "auto" par défaut en LAN
}

// StartRequest demande le lancement d'une impression.
type StartRequest struct {
	Remote     RemoteRef
	Plate      int // numéro de plateau (1 = Metadata/plate_1.gcode)
	UseAMS     bool
	AMSMapping []int // voir note de recherche §2 (5 entiers, -1 = inutilisé)
	// PlateClear est l'attestation humaine « plateau vide » portée par le flux
	// de confirmation. L'imprimante ne peut pas la vérifier : refus si faux.
	PlateClear bool
	Options    StartOptions
	Proof      ConfirmationProof
}

// StartExpectation décrit l'impression attendue, pour la réconciliation.
type StartExpectation struct {
	Remote RemoteRef
	// Since : instant (approximatif) où la commande de lancement a été demandée ; repère de repli
	// quand l'adaptateur n'a pas lui-même publié la commande (service redémarré).
	Since time.Time
}

// ReconcileOutcome est le verdict d'une réconciliation.
type ReconcileOutcome string

const (
	// ReconcileStarted : l'impression attendue est en cours ou terminée.
	ReconcileStarted ReconcileOutcome = "started"
	// ReconcileNotStarted : état frais, inactif, sans trace de l'impression.
	ReconcileNotStarted ReconcileOutcome = "not_started"
	// ReconcileUnknown : impossible de conclure (hors ligne, état périmé, autre fichier...).
	ReconcileUnknown ReconcileOutcome = "unknown"
)

// Reconciliation est le résultat de Reconcile.
type Reconciliation struct {
	Outcome ReconcileOutcome
	Status  Status
	Reason  string
}

// Adapter est l'interface commune à toutes les imprimantes.
type Adapter interface {
	Status(ctx context.Context) (Status, error)
	// Upload est idempotent par sha256+nom : un second appel identique ne
	// retransfère pas le fichier (RemoteRef.Reused vaut true).
	Upload(ctx context.Context, artefact io.Reader, name string, size int64, sha256 string) (RemoteRef, error)
	// Start renvoie nil seulement quand l'état PRINTING/PREPARING a été observé.
	// ErrStartUnknown signale un résultat indéterminé : ne jamais réessayer sans Reconcile.
	Start(ctx context.Context, req StartRequest) error
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	Stop(ctx context.Context) error
	Subscribe(ctx context.Context) (<-chan Status, error)
}

// Reconciler est implémenté par les adaptateurs capables de lever un StartUnknown.
type Reconciler interface {
	Reconcile(ctx context.Context, exp StartExpectation) (Reconciliation, error)
}

// SecretProvider fournit le code d'accès. La valeur ne doit jamais être loggée.
type SecretProvider interface {
	AccessCode(ctx context.Context) (string, error)
}

// StaticSecret est un SecretProvider constant (tests, configuration locale).
type StaticSecret string

func (s StaticSecret) AccessCode(context.Context) (string, error) { return string(s), nil }

// String masque la valeur pour éviter toute fuite par fmt/log.
func (s StaticSecret) String() string { return "StaticSecret(***)" }

// GoString masque aussi la forme %#v.
func (s StaticSecret) GoString() string { return "StaticSecret(***)" }

var (
	ErrNotConnected   = errors.New("printer: not connected")
	ErrStaleStatus    = errors.New("printer: status is stale")
	ErrNotReady       = errors.New("printer: not ready to start")
	ErrPrinterError   = errors.New("printer: printer reports an error")
	ErrBusy           = errors.New("printer: another start is in progress")
	ErrNoProof        = errors.New("printer: confirmation proof missing or invalid")
	ErrProofExpired   = errors.New("printer: confirmation proof expired")
	ErrProofMismatch  = errors.New("printer: confirmation proof does not match request")
	ErrPlateNotClear  = errors.New("printer: plate emptiness not attested")
	ErrStartRejected  = errors.New("printer: start rejected by printer")
	ErrStartUnknown   = errors.New("printer: start outcome unknown")
	ErrUploadMismatch = errors.New("printer: upload verification failed")
	ErrBadState       = errors.New("printer: command not valid in current state")
)

// StartStage dit jusqu'où la commande est allée avant que le résultat devienne inconnu.
type StartStage string

const (
	StageTransport StartStage = "transport_unacknowledged" // publiée, PUBACK non reçu
	StageAcked     StartStage = "transport_acknowledged"   // PUBACK reçu, pas de réponse de l'imprimante
	StageAccepted  StartStage = "accepted"                 // l'imprimante a répondu success, état non observé
)

// StartUnknownError enveloppe ErrStartUnknown avec l'étape atteinte.
type StartUnknownError struct {
	Stage StartStage
	Cause error
}

func (e *StartUnknownError) Error() string {
	return fmt.Sprintf("printer: start outcome unknown (stage %s): %v", e.Stage, e.Cause)
}
func (e *StartUnknownError) Unwrap() []error { return []error{ErrStartUnknown, e.Cause} }
