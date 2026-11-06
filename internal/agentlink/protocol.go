// Package agentlink définit le protocole versionné entre le service Linux
// (bambud) et l'agent Windows (bambu-agent), côté serveur (registre, tâches,
// transferts) et côté client (Client). Dossier §3 et §15.
//
// Principes :
//   - HTTP + JSON, préfixe /agent/v1 (ProtocolVersion 1). Une version majeure
//     différente est refusée à l'appairage et au hello.
//   - Appairage : bambud génère un jeton à usage unique et court ; l'agent
//     l'échange une fois contre un secret d'agent (le serveur n'en garde que
//     l'empreinte). Ensuite : Authorization: Bearer <agent_id>.<secret>.
//   - Aucun chemin partagé supposé : les entrées/sorties sont des artefacts
//     identifiés par sha256, transférés par blobs vérifiés (reprise possible).
//   - Un travail lie son exécuteur ; la perte de l'agent le laisse « unknown »
//     jusqu'à réconciliation. Jamais de transfert automatique d'un travail
//     physique d'un exécuteur à l'autre.
package agentlink

import (
	"encoding/json"
	"time"
)

// ProtocolVersion est la version majeure du protocole.
const ProtocolVersion = 1

// Préfixe des routes agent.
const BasePath = "/agent/v1"

// Exécuteurs d'un travail.
const (
	ExecutorLinux        = "linux"
	ExecutorWindowsAgent = "windows-agent"
)

// Types de travaux acceptés par l'agent.
const (
	KindInventory         = "inventory"           // inventaire en lecture seule d'un dossier autorisé
	KindUploadFiles       = "upload_files"        // envoi contrôlé de fichiers inventoriés
	KindSlice             = "slice"               // slicing local (renfort)
	KindGetProjectState   = "get_project_state"   // lecture du projet ouvert dans Studio (copie du .3mf)
	KindOpenInStudio      = "open_in_studio"      // ouvre un artefact dans Studio
	KindModifyOpenProject = "modify_open_project" // non pris en charge (voir docs/jalons/J8.md)
)

// AgentKinds liste les types de travaux que le protocole connaît.
var AgentKinds = []string{KindInventory, KindUploadFiles, KindSlice, KindGetProjectState, KindOpenInStudio, KindModifyOpenProject}

// PhysicalKinds : travaux à effet physique. Ils ne sont jamais assignés à un
// agent par ce protocole et jamais ré-assignés automatiquement.
var PhysicalKinds = map[string]bool{"start_print": true, "send_to_printer": true}

// États d'un travail d'agent.
const (
	StateQueued    = "queued"
	StateAssigned  = "assigned" // remis à l'agent, démarrage non confirmé
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
	StateUnknown   = "unknown" // agent perdu pendant le travail : issue inconnue
)

// IsTerminal : plus de transition automatique (unknown est réconciliable par l'agent).
func IsTerminal(s string) bool {
	switch s {
	case StateSucceeded, StateFailed, StateCancelled:
		return true
	}
	return false
}

// Cibles d'un transfert.
const (
	TargetInbox    = "inbox"    // fichier d'import (inventaire) -> catalogue
	TargetArtifact = "artifact" // sortie d'un travail -> magasin de blobs
)

// Artifact identifie un contenu par son empreinte.
type Artifact struct {
	SHA256 string `json:"sha256"`
	Name   string `json:"name,omitempty"`
	Size   int64  `json:"size_bytes"`
}

// UploadSpec autorise l'envoi d'un fichier précis pour un travail.
type UploadSpec struct {
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size_bytes"`
	Target    string `json:"target"`
	RootLabel string `json:"root_label,omitempty"`
	RelPath   string `json:"rel_path,omitempty"`
}

// Task est un travail tel que l'agent le reçoit.
type Task struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Params    json.RawMessage `json:"params,omitempty"`
	Inputs    []Artifact      `json:"inputs,omitempty"`
	Uploads   []UploadSpec    `json:"uploads,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// TaskInfo est la vue complète d'un travail (API d'administration).
type TaskInfo struct {
	ID              string          `json:"id"`
	AgentID         string          `json:"agent_id"`
	Executor        string          `json:"executor"`
	Kind            string          `json:"kind"`
	State           string          `json:"state"`
	Physical        bool            `json:"physical"`
	CancelRequested bool            `json:"cancel_requested,omitempty"`
	Progress        float64         `json:"progress"`
	Message         string          `json:"message,omitempty"`
	Params          json.RawMessage `json:"params,omitempty"`
	Inputs          []Artifact      `json:"inputs,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
	Outputs         []Artifact      `json:"outputs,omitempty"`
	Error           *TaskError      `json:"error,omitempty"`
	Note            string          `json:"note,omitempty"`
	Replaces        string          `json:"replaces,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
}

// TaskError : erreur structurée d'un travail.
type TaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// TaskResult est le rapport final d'un travail.
type TaskResult struct {
	State   string          `json:"state"` // succeeded | failed | cancelled
	Result  json.RawMessage `json:"result,omitempty"`
	Outputs []Artifact      `json:"outputs,omitempty"` // déjà transférés (uploads artifact)
	Error   *TaskError      `json:"error,omitempty"`
}

// ProgressReport : avancement d'un travail en cours.
type ProgressReport struct {
	Progress float64 `json:"progress"`
	Message  string  `json:"message,omitempty"`
}

// PairRequest : échange du jeton d'appairage contre un secret d'agent.
type PairRequest struct {
	Token        string `json:"token"`
	Name         string `json:"name"`
	Host         string `json:"host,omitempty"`
	AgentVersion string `json:"agent_version,omitempty"`
	Protocol     int    `json:"protocol"`
}

// PairResponse : le secret n'est renvoyé qu'une fois.
type PairResponse struct {
	AgentID          string    `json:"agent_id"`
	Secret           string    `json:"secret"`
	Protocol         int       `json:"protocol"`
	HeartbeatSeconds int       `json:"heartbeat_seconds"`
	ServerTime       time.Time `json:"server_time"`
}

// StudioCaps : Bambu Studio sur le poste.
type StudioCaps struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Running   bool   `json:"running"`
}

// FolderCap : dossier autorisé à l'import (étiquette + chemin local).
type FolderCap struct {
	Label string `json:"label"`
	Path  string `json:"path,omitempty"`
}

// AgentCaps est l'annonce de capacités de l'agent.
type AgentCaps struct {
	OS            string      `json:"os"`
	Arch          string      `json:"arch"`
	AgentVersion  string      `json:"agent_version"`
	Studio        StudioCaps  `json:"studio"`
	LocalSlicer   bool        `json:"local_slicer"`
	SlicerVersion string      `json:"slicer_version,omitempty"`
	SharedFolders []FolderCap `json:"shared_folders,omitempty"`
	CAD           []string    `json:"cad,omitempty"`
	Tasks         []string    `json:"tasks"` // types de travaux acceptés
}

// HeldTask : travail que l'agent détient encore (en cours ou terminé non acquitté).
type HeldTask struct {
	ID    string `json:"id"`
	State string `json:"state"` // running | succeeded | failed | cancelled
}

// HelloRequest ouvre (ou rouvre) une session et déclenche la réconciliation.
type HelloRequest struct {
	Protocol     int        `json:"protocol"`
	AgentVersion string     `json:"agent_version,omitempty"`
	Capabilities AgentCaps  `json:"capabilities"`
	Held         []HeldTask `json:"held,omitempty"`
}

// HelloResponse : Abandon liste les travaux que le serveur ne considère plus
// comme siens (à interrompre/oublier).
type HelloResponse struct {
	Protocol         int       `json:"protocol"`
	ServerTime       time.Time `json:"server_time"`
	HeartbeatSeconds int       `json:"heartbeat_seconds"`
	Abandon          []string  `json:"abandon,omitempty"`
	Reconciled       []string  `json:"reconciled,omitempty"`
}

// HeartbeatRequest : signe de vie léger.
type HeartbeatRequest struct {
	Running       []string `json:"running,omitempty"`
	StudioRunning bool     `json:"studio_running"`
	OpenProject   string   `json:"open_project,omitempty"`
}

// HeartbeatResponse : Cancel liste les travaux dont l'annulation est demandée.
type HeartbeatResponse struct {
	ServerTime time.Time `json:"server_time"`
	Cancel     []string  `json:"cancel,omitempty"`
}

// InventoryEntry : un fichier trouvé (lecture seule).
type InventoryEntry struct {
	RelPath    string `json:"rel_path"`
	Size       int64  `json:"size_bytes"`
	MTimeNS    int64  `json:"mtime_ns"`
	SHA256     string `json:"sha256"`
	OriginPath string `json:"origin_path,omitempty"`
}

// InventoryRequest : lot d'inventaire d'un dossier autorisé.
type InventoryRequest struct {
	RootLabel string           `json:"root_label"`
	Entries   []InventoryEntry `json:"entries"`
}

// InventoryResponse résume l'enregistrement.
type InventoryResponse struct {
	Accepted int            `json:"accepted"`
	Rejected int            `json:"rejected"`
	ByStatus map[string]int `json:"by_status"`
}

// UploadBegin ouvre (ou reprend) l'envoi d'un artefact.
type UploadBegin struct {
	TaskID string `json:"task_id"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size_bytes"`
	// Target/Name : pour un artefact de sortie déclaré par l'agent (target "artifact").
	// Un envoi d'inventaire (inbox) est pré-autorisé par le serveur et n'en a pas besoin.
	Target string `json:"target,omitempty"`
	Name   string `json:"name,omitempty"`
}

// UploadStatus : offset à partir duquel reprendre.
type UploadStatus struct {
	Offset   int64 `json:"offset"`
	Size     int64 `json:"size_bytes"`
	Complete bool  `json:"complete"`
}

// APIError est la forme des erreurs HTTP.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Codes d'erreur stables.
const (
	CodeUnauthorized    = "unauthorized"
	CodeProtocol        = "protocol_mismatch"
	CodeBadPairing      = "pairing_invalid"
	CodeNotFound        = "not_found"
	CodeConflict        = "conflict"
	CodeInvalid         = "invalid"
	CodeNotAllowed      = "not_allowed"
	CodeHashMismatch    = "hash_mismatch"
	CodeOutputMissing   = "output_not_uploaded"
	CodeAgentOffline    = "agent_offline"
	CodeNotSupported    = "not_supported"
	CodePhysicalRefused = "physical_task_refused"
)
