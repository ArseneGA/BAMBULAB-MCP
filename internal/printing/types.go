package printing

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/goals"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// Erreurs nommées (distinguables par errors.Is ; traduites en codes HTTP/MCP).
var (
	ErrInvalid       = errors.New("entrée invalide")
	ErrNotFound      = errors.New("introuvable")
	ErrState         = errors.New("état d'impression incompatible avec l'opération")
	ErrNotConfirmed  = errors.New("confirmation humaine manquante")
	ErrUnavailable   = errors.New("indisponible")
	ErrIncompatible  = errors.New("incompatible avec l'imprimante")
	ErrPrinterBusy   = errors.New("imprimante réservée par une autre impression")
	ErrNeedsApproval = errors.New("approbation humaine requise")
	ErrReplayParams  = errors.New("clé d'idempotence déjà utilisée avec d'autres paramètres")
)

func invalidf(format string, a ...any) error {
	return fmt.Errorf("%w : %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// PrinterRecord est la configuration persistée d'une imprimante. Le code
// d'accès n'y figure JAMAIS (voir SecretStore).
type PrinterRecord struct {
	ID       string  `json:"id"`
	ModelID  string  `json:"model_id"`
	Name     string  `json:"name"`
	Host     string  `json:"host"`
	Serial   string  `json:"serial"`
	Firmware *string `json:"firmware,omitempty"`
	// NozzleMM : buse installée (donnée déclarée par l'utilisateur).
	NozzleMM float64 `json:"nozzle_mm"`
	// AMSUnits : nombre d'AMS raccordés (déclaré ; 0 = bobine externe seule).
	AMSUnits int `json:"ams_units"`
	MQTTPort int `json:"mqtt_port,omitempty" jsonschema:"défaut 8883 ; le simulateur utilise un port éphémère"`
	FTPPort  int `json:"ftp_port,omitempty" jsonschema:"défaut 990"`
	// PinnedSHA256 : empreinte du certificat de l'imprimante (non secrète), épinglée par l'HUMAIN sur
	// /setup/printer/{id} après qu'il l'a comparée au numéro de série ; effacée si l'hôte change.
	// AllowStartFromFinish : accepte un nouveau départ depuis l'état « finished » (à valider sur le
	// vrai matériel, J7 §5 point 8) ; faux par défaut ; non réglable par MCP/API.
	AllowStartFromFinish bool      `json:"allow_start_from_finish,omitempty"`
	PinnedSHA256         string    `json:"pinned_sha256,omitempty"`
	PinnedAt             time.Time `json:"pinned_at,omitzero"`
	CreatedAt            time.Time `json:"created_at"`
}

// ToContract projette vers contracts.Printer.
func (r PrinterRecord) ToContract() contracts.Printer {
	h, s := r.Host, r.Serial
	return contracts.Printer{ID: r.ID, ModelID: r.ModelID, Name: r.Name, Host: &h, Serial: &s, Firmware: r.Firmware}
}

// PrinterView est ce que voit le client : configuration SANS secret, plus l'état du coffre.
type PrinterView struct {
	PrinterRecord
	ModelName     string `json:"model_name"`
	AccessCodeSet bool   `json:"access_code_set" jsonschema:"vrai si un code d'accès est enregistré (sa valeur n'est jamais renvoyée)"`
	SetupURL      string `json:"setup_url,omitempty" jsonschema:"page locale où l'humain saisit le code d'accès"`
}

// FilamentAssignment lie un filament de la découpe à un emplacement physique et une matière.
type FilamentAssignment struct {
	FilamentSlot int    `json:"filament_slot" jsonschema:"numéro (1..n) du filament dans le fichier découpé"`
	Tray         string `json:"tray" jsonschema:"emplacement physique : ams1:1 à ams4:4 (AMS n, bobine m) ou ext (bobine externe)"`
	MaterialID   string `json:"material_id" jsonschema:"identifiant de matière du catalogue, ou libellé libre de la bobine chargée"`
}

// JobEvent est une ligne d'historique d'un PrintJob.
type JobEvent struct {
	At    time.Time            `json:"at"`
	Event string               `json:"event"`
	From  contracts.PrintState `json:"from"`
	To    contracts.PrintState `json:"to"`
	Note  string               `json:"note,omitempty"`
}

// PrintProgress est le dernier état observé de l'impression.
type PrintProgress struct {
	PrinterState  string    `json:"printer_state"`
	Percent       int       `json:"percent"`
	Layer         int       `json:"layer"`
	TotalLayers   int       `json:"total_layers"`
	RemainingMin  int       `json:"remaining_minutes"`
	ObservedAt    time.Time `json:"observed_at"`
	SeenActive    bool      `json:"seen_active" jsonschema:"vrai une fois qu'un état préparation/impression/pause de CE fichier a été observé : seul un FINISH/FAILED vu après peut être attribué à cette impression"`
	MatchesFile   bool      `json:"matches_file" jsonschema:"vrai si le rapport de l'imprimante désigne bien le fichier de cette impression"`
	PrinterReason string    `json:"printer_reason,omitempty"`
	// Stage / Stages : étape courante de l'imprimante (stg_cur) et historique des étapes vues pour CETTE impression.
	Stage     int         `json:"stage,omitempty"`
	StageName string      `json:"stage_name,omitempty"`
	Stages    []StageSeen `json:"stages,omitempty"`

	// Supervision (watch.go) : ce que l'imprimante dit, ce qui cloche, depuis quand.
	Attention    string              `json:"attention,omitempty" jsonschema:"« attend une action à l'écran : <texte HMS> » quand l'imprimante est en pause sur erreur"`
	Blocked      string              `json:"blocked,omitempty" jsonschema:"« bloquée : … » : progression figée ou consignes incohérentes en pleine impression"`
	Unreachable  string              `json:"unreachable,omitempty" jsonschema:"imprimante injoignable depuis un moment : l'état réel est inconnu"`
	ErrorDetails []printer.ErrorInfo `json:"error_details,omitempty" jsonschema:"dernières erreurs (print_error, HMS) décodées vues pour cette impression"`
	WasGone      bool                `json:"was_gone,omitempty" jsonschema:"la liaison avec l'imprimante a été coupée puis rétablie pendant l'impression (redémarrage possible)"`
	GoneSince    time.Time           `json:"gone_since,omitzero"`
	IdleSince    time.Time           `json:"idle_since,omitzero"`
	MismatchAt   time.Time           `json:"mismatch_since,omitzero"`
	InconsistAt  time.Time           `json:"inconsistent_since,omitzero"`
	Sig          string              `json:"sig,omitempty"`
	SigAt        time.Time           `json:"sig_at,omitzero"`
	Alerted      []string            `json:"alerted,omitempty"`
}

// ArtifactSummary résume l'artefact découpé (mesures lues dans l'archive).
type ArtifactSummary struct {
	Plates        []slicer.PlateResult `json:"plates"`
	PrinterModel  string               `json:"printer_model,omitempty"`
	MachinePreset string               `json:"machine_preset,omitempty"`
	Process       string               `json:"process,omitempty"`
	Filaments     []string             `json:"filament_presets,omitempty"`
	Nozzles       []string             `json:"nozzles,omitempty"`
	BedType       string               `json:"bed_type,omitempty"`
	StudioVersion string               `json:"studio_version,omitempty"`
	// Réglages EFFECTIFS de la découpe (project_settings.config de l'archive) : adhérence et supports. Valeur lue dans
	// l'archive, ou défaut de Bambu Studio quand le profil ne la définit pas (jamais null).
	BrimType      string `json:"brim_type,omitempty"`
	BrimWidth     string `json:"brim_width,omitempty"`
	RaftLayers    string `json:"raft_layers,omitempty"`
	EnableSupport *bool  `json:"enable_support,omitempty"`
	SupportType   string `json:"support_type,omitempty"`
	// Objects : matière et couches par pièce de la plaque (lues dans le G-code), pour un plan multi-pièces.
	Objects []slicer.ObjectUse `json:"objects,omitempty"`
	// GcodeProfile : le G-code de démarrage/fin est-il celui du profil machine officiel (empreinte) ?
	GcodeProfile *slicer.GcodeProfile `json:"gcode_profile,omitempty"`
}

// Job est un PrintJob enrichi : état contracts.PrintState + tout ce qui lie l'impression
// à son artefact, son imprimante, sa confirmation et son suivi.
type Job struct {
	ID              string               `json:"id"`
	State           contracts.PrintState `json:"state"`
	PrinterID       string               `json:"printer_id"`
	ModelID         string               `json:"model_id,omitempty"`
	VersionID       string               `json:"version_id,omitempty"`
	ProfileRevision string               `json:"profile_revision,omitempty"`
	Name            string               `json:"name,omitempty" jsonschema:"nom du fichier découpé"`
	ArtifactSHA256  string               `json:"artifact_sha256,omitempty"`
	ArtifactSize    int64                `json:"artifact_size,omitempty"`
	Plate           int                  `json:"plate,omitempty"`
	Assignments     []FilamentAssignment `json:"assignments,omitempty"`
	UseAMS          bool                 `json:"use_ams"`
	AMSMapping      []int                `json:"ams_mapping,omitempty"`
	Remote          *printer.RemoteRef   `json:"remote,omitempty"`
	Summary         *ArtifactSummary     `json:"summary,omitempty"`
	Validation      *ValidationReport    `json:"validation,omitempty"`
	ConfirmationID  string               `json:"confirmation_id,omitempty"`
	// NoBedLeveling : l'humain a approuvé un lancement SANS mise à niveau du plateau (défaut : mise à niveau activée).
	// Fait partie de la liaison approuvée (contracts.ConfirmationBinding.NoBedLeveling).
	NoBedLeveling bool `json:"no_bed_leveling,omitempty" jsonschema:"vrai si le lancement approuvé désactive la mise à niveau du plateau (défaut : elle est activée)"`
	// Options de démarrage approuvées (mêmes conventions que NoBedLeveling : écart au défaut historique, omitempty).
	NoFlowCali      bool `json:"no_flow_cali,omitempty" jsonschema:"vrai si le lancement approuvé désactive la calibration du débit (défaut : activée)"`
	NoVibrationCali bool `json:"no_vibration_cali,omitempty" jsonschema:"vrai si le lancement approuvé désactive la calibration des vibrations (défaut : activée)"`
	Timelapse       bool `json:"timelapse,omitempty" jsonschema:"vrai si le lancement approuvé enregistre un timelapse (défaut : non)"`
	// Adhesion : risque de décollement estimé d'après la géométrie de la pièce (mesh.Adhesion), évalué à la découpe.
	Adhesion      *mesh.AdhesionReport `json:"adhesion,omitempty" jsonschema:"risque de décollement estimé à la découpe (heuristique)"`
	SliceJobID    string               `json:"slice_job_id,omitempty"`
	UploadJobID   string               `json:"upload_job_id,omitempty"`
	StartJobID    string               `json:"start_job_id,omitempty"`
	Print         *PrintProgress       `json:"print,omitempty"`
	LastError     string               `json:"last_error,omitempty"`
	StopConfirmID string               `json:"stop_confirm_id,omitempty"`
	Archived      bool                 `json:"archived,omitempty" jsonschema:"impression terminée masquée de l'historique par défaut"`
	Trashed       bool                 `json:"trashed,omitempty" jsonschema:"impression terminée à la corbeille (récupérable)"`
	StoppedByUser bool                 `json:"stopped_by_user,omitempty" jsonschema:"vrai si l'impression a été arrêtée par l'humain via la page /stop"`
	Events        []JobEvent           `json:"events,omitempty"`
	CreatedAt     time.Time            `json:"created_at"`
	UpdatedAt     time.Time            `json:"updated_at"`
	Slice         *SliceSpec           `json:"slice,omitempty" jsonschema:"paramètres figés de la découpe"`
	Next          string               `json:"next,omitempty" jsonschema:"prochaine étape autorisée de la séquence"`
}

// SliceSpec fige ce qui sert à découper (reproductibilité).
type SliceSpec struct {
	Machine     string                 `json:"machine,omitempty"`
	Printer     string                 `json:"printer_model,omitempty"`
	Nozzle      float64                `json:"nozzle_mm"`
	Process     string                 `json:"process,omitempty"`
	Filaments   []string               `json:"filaments,omitempty"`
	Overrides   []slicer.Override      `json:"overrides,omitempty"`
	Orientation *goals.OrientationSpec `json:"orientation,omitempty"`
	SourceSHA   string                 `json:"source_sha256"`
	SourceName  string                 `json:"source_name"`
	SourceFmt   string                 `json:"source_format"`
	Plate       int                    `json:"plate"`
	// Plan : découpe d'une plaque d'un plan multi-pièces (plan_plate) ; SourceSHA en est alors l'empreinte.
	Plan *PlateSliceSpec `json:"plate_plan,omitempty"`
}

// ToContract projette vers contracts.PrintJob.
func (j Job) ToContract() contracts.PrintJob {
	pj := contracts.PrintJob{ID: j.ID, State: j.State, PrinterID: j.PrinterID, VersionID: j.VersionID,
		ArtifactSHA256: j.ArtifactSHA256, LastError: j.LastError, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt}
	if j.Plate > 0 {
		p := j.Plate
		pj.Plate = &p
	}
	for _, a := range j.Assignments {
		pj.Materials = append(pj.Materials, contracts.SlotAssignment{Slot: bindingSlot(a), MaterialID: a.MaterialID})
	}
	if j.Remote != nil {
		n := j.Remote.Name
		pj.RemoteRef = &n
	}
	return pj
}

// bindingSlot encode « filament>emplacement » : la liaison approuvée couvre donc
// aussi quel filament va dans quel emplacement (l'empreinte de la confirmation en dépend).
func bindingSlot(a FilamentAssignment) string { return fmt.Sprintf("f%d>%s", a.FilamentSlot, a.Tray) }

func (j *Job) event(ev contracts.PrintEvent, from, to contracts.PrintState, note string, now time.Time) {
	j.Events = append(j.Events, JobEvent{At: now, Event: string(ev), From: from, To: to, Note: note})
	if over := len(j.Events) - 60; over > 0 {
		j.Events = j.Events[over:]
	}
}

// ValidationReport est le rapport de validate_print_artifact.
type ValidationReport struct {
	OK         bool           `json:"ok" jsonschema:"vrai seulement si aucun contrôle bloquant n'a échoué"`
	At         time.Time      `json:"at"`
	PrinterID  string         `json:"printer_id"`
	ArtifactID string         `json:"artifact_id"`
	Plate      int            `json:"plate"`
	Archive    []slicer.Check `json:"archive_checks" jsonschema:"validation d'archive (structure, G-code, trajectoires, cohérence avec la demande)"`
	Compat     []slicer.Check `json:"compatibility_checks" jsonschema:"compatibilité avec l'imprimante réelle : modèle, buse, plateau, volume, matières/AMS"`
	Blocking   []string       `json:"blocking,omitempty"`
	Warnings   []string       `json:"warnings,omitempty"`
	Validator  string         `json:"validator" jsonschema:"slicer.ValidateArchive, ou un validateur substitué (tests)"`
	// BrimType / BrimWidth : bordure du procédé réellement tranché (project_settings.config de l'archive) ; vides si illisibles.
	BrimType  string `json:"brim_type,omitempty"`
	BrimWidth string `json:"brim_width,omitempty"`
	// Adhesion : risque de décollement (surface de contact de la première couche), avec la bordure réellement présente.
	Adhesion *mesh.AdhesionReport `json:"adhesion,omitempty"`
	// RaftLayers, EnableSupport, SupportType : réglages réellement tranchés (archive), défaut de Bambu Studio si le profil se tait.
	RaftLayers    string `json:"raft_layers,omitempty"`
	EnableSupport *bool  `json:"enable_support,omitempty"`
	SupportType   string `json:"support_type,omitempty"`
	// Overhang : surplombs de la pièce au seuil d'angle du profil, croisés avec le réglage de supports.
	Overhang *mesh.OverhangSupportCheck `json:"overhang,omitempty"`
}

// brimPresent dit si la bordure du procédé est une vraie bordure extérieure/intérieure d'au moins 3 mm.
// « auto_brim » et « brim_ears » ne comptent pas : leur effet dépend de Studio et n'est pas garanti pour une pièce élancée.
func brimPresent(brimType, width string) bool {
	switch strings.ToLower(strings.TrimSpace(brimType)) {
	case "outer_only", "inner_only", "outer_and_inner", "outer and inner", "outer brim only", "inner brim only", "outer and inner brim":
		w, err := strconv.ParseFloat(strings.TrimSpace(width), 64)
		return err == nil && w >= 3
	}
	return false
}

// brimLabel décrit la bordure pour l'humain.
func brimLabel(brimType, width string) string {
	t := strings.TrimSpace(brimType)
	if t == "" {
		return "bordure du profil inconnue"
	}
	if w := strings.TrimSpace(width); w != "" && t != "no_brim" {
		return "bordure du profil : " + t + " (" + w + " mm)"
	}
	return "bordure du profil : " + t
}

// adhesionNotes : avertissements à montrer à l'humain avant le lancement (jamais bloquants).
func (j Job) adhesionNotes() []string {
	a := j.Adhesion
	if a == nil && j.Validation != nil {
		a = j.Validation.Adhesion
	}
	if !a.Risky() {
		return nil
	}
	var bt, bw, raft string
	if j.Validation != nil {
		bt, bw, raft = j.Validation.BrimType, j.Validation.BrimWidth, j.Validation.RaftLayers
	}
	if adhesionCovered(bt, bw, raft) {
		return nil // une bordure (≥ 3 mm) ou un radeau est déjà prévu : le risque est couvert
	}
	return []string{a.Summary() + " Profil actuel : " + brimLabel(bt, bw) + raftLabel(raft) + ". Non bloquant : à toi de décider de lancer ou de redécouper avec bordure ou radeau."}
}

// raftPresent : le procédé tranché comporte au moins une couche de radeau.
func raftPresent(raftLayers string) bool {
	n, err := strconv.ParseFloat(strings.TrimSpace(raftLayers), 64)
	return err == nil && n > 0
}

// adhesionCovered : une vraie bordure (≥ 3 mm) OU un radeau couvre le risque de décollement.
func adhesionCovered(brimType, width, raftLayers string) bool {
	return brimPresent(brimType, width) || raftPresent(raftLayers)
}

func raftLabel(raftLayers string) string {
	if strings.TrimSpace(raftLayers) == "" {
		return ""
	}
	return ", radeau : " + strings.TrimSpace(raftLayers) + " couche(s)"
}

// OverhangWarning : phrase « surplombs sans supports » du rapport (vide si supports actifs ou surplombs négligeables).
func (v *ValidationReport) OverhangWarning() string {
	if v == nil || v.Overhang == nil {
		return ""
	}
	return v.Overhang.UnsupportedSummary(v.EnableSupport != nil && *v.EnableSupport)
}

// overhangNotes : avertissement FORT « surplombs sans supports », à montrer à l'humain avant le lancement.
// Non bloquant, mais affiché sur la page de confirmation et dans la liaison, comme le risque de décollement.
func (j Job) overhangNotes() []string {
	v := j.Validation
	if v == nil || v.Overhang == nil {
		return nil
	}
	on := v.EnableSupport != nil && *v.EnableSupport
	s := v.Overhang.UnsupportedSummary(on)
	if s == "" {
		return nil
	}
	return []string{s + " Non bloquant : à toi de décider de lancer ou de redécouper avec supports."}
}

func (v *ValidationReport) add(compat bool, name string, ok bool, level, detail string) {
	c := slicer.Check{Name: name, OK: ok, Level: level, Detail: detail}
	if compat {
		v.Compat = append(v.Compat, c)
	} else {
		v.Archive = append(v.Archive, c)
	}
	if !ok {
		line := name + " : " + detail
		if level == "error" {
			v.Blocking = append(v.Blocking, line)
		} else {
			v.Warnings = append(v.Warnings, line)
		}
	}
}
