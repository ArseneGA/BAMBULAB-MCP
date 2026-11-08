// Package agentv2 est le protocole /agent/v2 : le transport « via agent » d'une imprimante.
//
// Dans la cible SaaS, le serveur (bambud) ne parle plus à l'imprimante. Un agent local, installé sur le réseau de
// l'imprimante (Raspberry Pi, Linux, Windows), ouvre une connexion SORTANTE vers le serveur, parle seul à
// l'imprimante (MQTT TLS 8883, FTPS 990, caméra 6000) et garde seul le code d'accès LAN, qui n'est jamais transmis.
//
// Ce paquet contient les trois moitiés du protocole :
//   - le Hub (côté serveur) : file d'ordres par agent, long-poll, télémétrie, fichiers à déposer, autorisations
//     signées, et HubLink, l'implémentation de printer.Link qui relaie les opérations vers l'agent ;
//   - le Runner (côté agent) : exécute les ordres, REVÉRIFIE chaque autorisation de lancement, pousse la
//     télémétrie ;
//   - la cryptographie : autorisation de lancement signée Ed25519, nonce à usage unique.
//
// Le protocole /agent/v1 (agent Windows de l'atelier : inventaire, slicing, Studio) est inchangé ; v2 réutilise
// seulement son appairage (jeton à usage unique -> secret d'agent, Authorization: Bearer <agent_id>.<secret>).
// Ce paquet n'importe ni agentlink ni core : l'authentification est injectée (Authenticator).
package agentv2

import (
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// ProtocolVersion est la version majeure de ce protocole.
const ProtocolVersion = 2

// BasePath est le préfixe des routes.
const BasePath = "/agent/v2"

// Types d'ordres envoyés à l'agent.
const (
	OrderPutFile   = "put_file"  // dépôt d'un fichier sur l'imprimante (l'agent le tire par morceaux)
	OrderStart     = "start"     // lancement : UNIQUEMENT sur autorisation signée
	OrderControl   = "control"   // pause | resume | stop
	OrderSnapshot  = "snapshot"  // image caméra
	OrderReconcile = "reconcile" // lecture d'état pour lever un START_UNKNOWN ; n'envoie jamais de lancement
	OrderStatus    = "status"    // rapport d'état frais (pushall) renvoyé en télémétrie
)

// Commandes de contrôle.
const (
	CmdPause  = "pause"
	CmdResume = "resume"
	CmdStop   = "stop"
)

// Issues d'un ordre (OrderResult.State).
const (
	ResultOK       = "ok"       // exécuté
	ResultStarted  = "started"  // lancement : PREPARE/RUNNING observé
	ResultRejected = "rejected" // lancement : l'imprimante a refusé la commande (rien ne tourne)
	ResultRefused  = "refused"  // refus de l'agent AVANT d'envoyer quoi que ce soit (autorisation, mode lecture seule...)
	ResultUnknown  = "unknown"  // lancement : commande peut-être partie, issue indéterminée -> jamais de renvoi
	ResultFailed   = "failed"   // échec d'une opération sans effet physique indéterminé
)

// Codes de refus (OrderResult.Code).
const (
	CodeReadOnly      = "read_only"      // kill-switch local : l'agent est en lecture seule
	CodeBadSignature  = "bad_signature"  // signature absente, invalide, ou clé de serveur différente de celle épinglée
	CodeExpired       = "expired"        // autorisation périmée (ou valable trop longtemps)
	CodeNotYetValid   = "not_yet_valid"  // émise dans le futur au-delà de la tolérance d'horloge
	CodeReplay        = "replay"         // nonce déjà utilisé
	CodeWrongTarget   = "wrong_target"   // autre agent, autre imprimante
	CodeFileUnknown   = "file_unknown"   // l'agent n'a jamais déposé ce fichier (empreinte inconnue)
	CodeHashMismatch  = "hash_mismatch"  // le fichier présent sur l'imprimante n'a pas l'empreinte autorisée
	CodeBusy          = "busy"           // une autre opération physique est en cours
	CodeNotReady      = "not_ready"      // l'imprimante n'est pas lançable
	CodeBadRequest    = "bad_request"    // ordre mal formé
	CodeUnsupported   = "unsupported"    // ordre inconnu
	CodePrinterFailed = "printer_failed" // erreur de l'imprimante / du transport local
)

// Order est un ordre long-poll remis à l'agent (au plus UNE fois).
type Order struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	PrinterID string    `json:"printer_id"`
	CreatedAt time.Time `json:"created_at"`

	File      *FileSpec      `json:"file,omitempty"`      // put_file
	Auth      *SignedAuth    `json:"auth,omitempty"`      // start
	Cmd       string         `json:"cmd,omitempty"`       // control
	Reconcile *ReconcileSpec `json:"reconcile,omitempty"` // reconcile
}

// FileSpec décrit un fichier que l'agent doit tirer (GET /agent/v2/files/{id}, Range) puis déposer.
type FileSpec struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size_bytes"`
	SHA256 string `json:"sha256"`
}

// ReconcileSpec décrit l'impression attendue.
type ReconcileSpec struct {
	Name   string    `json:"name"`
	SHA256 string    `json:"sha256,omitempty"`
	Since  time.Time `json:"since,omitzero"`
}

// OrderResult est le rapport de l'agent sur un ordre.
type OrderResult struct {
	State   string `json:"state"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	// Stage : pour un lancement « unknown », jusqu'où la commande est allée (printer.StartStage).
	Stage string `json:"stage,omitempty"`

	Remote *printer.RemoteRef `json:"remote,omitempty"` // put_file
	// Outcome / Reason : reconcile.
	Outcome string          `json:"outcome,omitempty"`
	Reason  string          `json:"reason,omitempty"`
	Status  *printer.Status `json:"status,omitempty"`
	// JPEGBase64 : snapshot.
	JPEGBase64 string `json:"jpeg_base64,omitempty"`
}

// Telemetry est l'état poussé par l'agent.
type Telemetry struct {
	PrinterID string         `json:"printer_id"`
	Status    printer.Status `json:"status"`
	// AgeMS : âge de l'information de l'imprimante à l'émission, mesuré sur l'horloge de l'agent. Le serveur en
	// déduit UpdatedAt sur SA horloge : aucune dérive entre les deux machines ne fausse la fraîcheur.
	AgeMS      int64  `json:"age_ms"`
	ReadOnly   bool   `json:"read_only"`
	AgentVer   string `json:"agent_version,omitempty"`
	CameraOK   bool   `json:"camera"`
	RemoteHash bool   `json:"remote_hash_check"`
}

// PairRequest : appairage (le jeton est celui du mécanisme existant : `bambud agent pair` ou /agents/pair).
type PairRequest struct {
	Token        string `json:"token"`
	Name         string `json:"name"`
	Host         string `json:"host,omitempty"`
	AgentVersion string `json:"agent_version,omitempty"`
	Protocol     int    `json:"protocol"`
	PrinterID    string `json:"printer_id,omitempty"`
}

// PairResponse : le secret n'est renvoyé qu'une fois ; ServerKey est la clé publique Ed25519 (base64) que l'agent
// ÉPINGLE : seule une autorisation signée par elle pourra jamais déclencher un lancement.
type PairResponse struct {
	AgentID     string    `json:"agent_id"`
	Secret      string    `json:"secret"`
	Protocol    int       `json:"protocol"`
	ServerKey   string    `json:"server_key"`
	ServerKeyID string    `json:"server_key_id"`
	PollSeconds int       `json:"poll_seconds"`
	ServerTime  time.Time `json:"server_time"`
}

// APIError est la forme des erreurs HTTP.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
