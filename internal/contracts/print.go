package contracts

import (
	"errors"
	"fmt"
	"time"
)

// PrintState est l'état métier d'une impression (dossier §10).
type PrintState string

const (
	PrintDraft                PrintState = "DRAFT"
	PrintPrepared             PrintState = "PREPARED"
	PrintSlicing              PrintState = "SLICING"
	PrintValidated            PrintState = "VALIDATED"
	PrintUploading            PrintState = "UPLOADING"
	PrintUploaded             PrintState = "UPLOADED"
	PrintAwaitingConfirmation PrintState = "AWAITING_PRINT_CONFIRMATION"
	PrintStartRequested       PrintState = "START_REQUESTED"
	PrintPrinting             PrintState = "PRINTING"
	PrintCompleted            PrintState = "COMPLETED"
	PrintFailed               PrintState = "FAILED"
	PrintCancelled            PrintState = "CANCELLED"
	PrintStartUnknown         PrintState = "START_UNKNOWN"
)

// Terminal : plus aucune transition possible.
func (s PrintState) Terminal() bool {
	return s == PrintCompleted || s == PrintFailed || s == PrintCancelled
}

// PrintEvent est un événement déclencheur de transition.
type PrintEvent string

const (
	EvPrepare             PrintEvent = "prepare"
	EvModify              PrintEvent = "modify" // changement de modèle/profil : retour en PREPARED
	EvStartSlice          PrintEvent = "start_slice"
	EvSliceOK             PrintEvent = "slice_ok"
	EvStartUpload         PrintEvent = "start_upload"
	EvUploadOK            PrintEvent = "upload_ok"
	EvRequestConfirmation PrintEvent = "request_confirmation"
	EvInvalidateConfirm   PrintEvent = "invalidate_confirmation" // élément lié modifié ou jeton expiré
	EvConfirm             PrintEvent = "confirm"                 // jeton valide consommé
	EvStartRejected       PrintEvent = "start_rejected"          // commande refusée par l'imprimante
	EvObservedPrinting    PrintEvent = "observed_printing"       // état PRINTING observé en télémétrie
	EvStartLost           PrintEvent = "start_lost"              // perte de lien après l'envoi de la commande
	EvReconcilePrinting   PrintEvent = "reconcile_printing"      // réconciliation : l'impression tourne
	EvReconcileNotStarted PrintEvent = "reconcile_not_started"   // réconciliation : rien n'a démarré
	EvPrintDone           PrintEvent = "print_done"
	EvFail                PrintEvent = "fail"
	EvCancel              PrintEvent = "cancel"
)

// ErrIllegalTransition est renvoyée (enveloppée) pour toute transition interdite.
var ErrIllegalTransition = errors.New("transition d'état d'impression interdite")

type transitionKey struct {
	s PrintState
	e PrintEvent
}

// printTransitions est la table unique de la machine d'états.
// Choix de sûreté : START_UNKNOWN n'est jamais annulable ni rejouable sans
// réconciliation explicite ; "reconcile_not_started" renvoie en UPLOADED, ce qui
// impose une NOUVELLE confirmation avant tout nouveau départ.
var printTransitions = map[transitionKey]PrintState{
	{PrintDraft, EvPrepare}: PrintPrepared,
	{PrintDraft, EvCancel}:  PrintCancelled,

	{PrintPrepared, EvStartSlice}: PrintSlicing,
	{PrintPrepared, EvCancel}:     PrintCancelled,

	{PrintSlicing, EvSliceOK}: PrintValidated,
	{PrintSlicing, EvFail}:    PrintFailed,
	{PrintSlicing, EvCancel}:  PrintCancelled,

	{PrintValidated, EvStartUpload}: PrintUploading,
	{PrintValidated, EvModify}:      PrintPrepared,
	{PrintValidated, EvCancel}:      PrintCancelled,

	{PrintUploading, EvUploadOK}: PrintUploaded,
	{PrintUploading, EvFail}:     PrintFailed,
	{PrintUploading, EvCancel}:   PrintCancelled,

	{PrintUploaded, EvRequestConfirmation}: PrintAwaitingConfirmation,
	{PrintUploaded, EvModify}:              PrintPrepared,
	{PrintUploaded, EvCancel}:              PrintCancelled,

	{PrintAwaitingConfirmation, EvConfirm}:           PrintStartRequested,
	{PrintAwaitingConfirmation, EvInvalidateConfirm}: PrintUploaded,
	{PrintAwaitingConfirmation, EvCancel}:            PrintCancelled,

	{PrintStartRequested, EvObservedPrinting}: PrintPrinting,
	{PrintStartRequested, EvStartRejected}:    PrintFailed,
	{PrintStartRequested, EvStartLost}:        PrintStartUnknown,

	{PrintStartUnknown, EvReconcilePrinting}:   PrintPrinting,
	{PrintStartUnknown, EvReconcileNotStarted}: PrintUploaded,
	{PrintStartUnknown, EvFail}:                PrintFailed,

	{PrintPrinting, EvPrintDone}: PrintCompleted,
	{PrintPrinting, EvFail}:      PrintFailed,
	{PrintPrinting, EvCancel}:    PrintCancelled,
}

// Transition applique l'événement à l'état. Pure, sans effet de bord ; refuse l'illégal.
func Transition(state PrintState, event PrintEvent) (PrintState, error) {
	next, ok := printTransitions[transitionKey{state, event}]
	if !ok {
		return state, fmt.Errorf("%w : %s --%s-->", ErrIllegalTransition, state, event)
	}
	return next, nil
}

// AllowedEvents liste les événements légaux depuis un état (ordre non garanti).
func AllowedEvents(state PrintState) []PrintEvent {
	var out []PrintEvent
	for k := range printTransitions {
		if k.s == state {
			out = append(out, k.e)
		}
	}
	return out
}

// SlotAssignment lie un emplacement matière (AMS ou bobine externe) à une matière.
type SlotAssignment struct {
	Slot       string `json:"slot" jsonschema:"ex. ams1:2 ou ext"`
	MaterialID string `json:"material_id"`
}

// PrintJob est une impression, de la préparation au résultat physique.
type PrintJob struct {
	ID        string     `json:"id"`
	State     PrintState `json:"state"`
	PrinterID string     `json:"printer_id"`
	// VersionID : version de modèle imprimée.
	VersionID string `json:"version_id"`
	// ArtifactSHA256 : empreinte de l'artefact slicé (.gcode.3mf) ; vide avant slicing.
	ArtifactSHA256 string           `json:"artifact_sha256,omitempty"`
	Plate          *int             `json:"plate,omitempty"`
	Materials      []SlotAssignment `json:"materials,omitempty"`
	// RemoteRef : référence du fichier déposé sur l'imprimante.
	RemoteRef *string   `json:"remote_ref,omitempty"`
	LastError string    `json:"last_error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Apply fait avancer le PrintJob selon la machine d'états.
func (p *PrintJob) Apply(event PrintEvent, now time.Time) error {
	next, err := Transition(p.State, event)
	if err != nil {
		return err
	}
	p.State = next
	p.UpdatedAt = now
	return nil
}
