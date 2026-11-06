package slicer

import (
	"errors"
	"fmt"
)

// Erreurs de la couche slicer (hors codes retour de Bambu Studio).
var (
	ErrBinaryNotFound = errors.New("slicer: binaire Bambu Studio introuvable")
	ErrInvalidRequest = errors.New("slicer: requête invalide")
	ErrOverride       = errors.New("slicer: surcharge de paramètre invalide")
	ErrIncompatible   = errors.New("slicer: profils incompatibles avec la machine")
	ErrTimeout        = errors.New("slicer: délai dépassé, processus arrêté")
	ErrCanceled       = errors.New("slicer: annulé, processus arrêté")
	ErrNoResult       = errors.New("slicer: result.json absent ou illisible")
	ErrInvalidOutput  = errors.New("slicer: archive de sortie invalide")
)

// Codes retour négatifs de Bambu Studio (src/libslic3r/Utils.hpp, note
// docs/research/bambu-studio-cli.md §4), mappés en erreurs nommées.
var (
	ErrStudioEnvironment      = errors.New("studio -1: environnement")
	ErrStudioInvalidParams    = errors.New("studio -2: paramètres invalides")
	ErrStudioFileNotFound     = errors.New("studio -3: fichier introuvable")
	ErrStudioFileOrder        = errors.New("studio -4: ordre des fichiers")
	ErrStudioInvalidConfig    = errors.New("studio -5: configuration invalide")
	ErrStudioData             = errors.New("studio -6: données invalides")
	ErrStudioPrinterTech      = errors.New("studio -7: technologie d'imprimante")
	ErrStudioExport3mf        = errors.New("studio -13: export 3mf")
	ErrStudioMemory           = errors.New("studio -14: mémoire")
	ErrStudioIncompatibleProc = errors.New("studio -17: process incompatible")
	ErrStudioInvalid3mfValues = errors.New("studio -18: valeurs 3mf invalides")
	ErrStudioArrangeFailed    = errors.New("studio -21: arrangement échoué")
	ErrStudioFileVersion      = errors.New("studio -24: version de fichier non supportée")
	ErrStudioEmptyPlate       = errors.New("studio -50: plaque vide ou aucun objet entièrement dedans")
	ErrStudioValidation       = errors.New("studio -51: validation")
	ErrStudioObjectsOutside   = errors.New("studio -52: objets partiellement hors plateau")
	ErrStudioSliceTimeLimit   = errors.New("studio -58: temps de slicing dépassé")
	ErrStudioTriangleLimit    = errors.New("studio -59: trop de triangles")
	ErrStudioFilamentBed      = errors.New("studio -61: filament incompatible avec le plateau")
	ErrStudioCollision        = errors.New("studio -63/-64: collision")
	ErrStudioSlicing          = errors.New("studio -100: erreur de slicing")
	ErrStudioGcodePath        = errors.New("studio -101..-105: chemin G-code")
	ErrStudioMultiNozzle      = errors.New("studio -108: multi-buses")
	ErrStudioUnknown          = errors.New("studio: code retour inconnu")
)

var studioCodes = map[int]error{
	-1: ErrStudioEnvironment, -2: ErrStudioInvalidParams, -3: ErrStudioFileNotFound,
	-4: ErrStudioFileOrder, -5: ErrStudioInvalidConfig, -6: ErrStudioData,
	-7: ErrStudioPrinterTech, -13: ErrStudioExport3mf, -14: ErrStudioMemory,
	-17: ErrStudioIncompatibleProc, -18: ErrStudioInvalid3mfValues, -21: ErrStudioArrangeFailed,
	-24: ErrStudioFileVersion, -50: ErrStudioEmptyPlate, -51: ErrStudioValidation,
	-52: ErrStudioObjectsOutside, -58: ErrStudioSliceTimeLimit, -59: ErrStudioTriangleLimit,
	-61: ErrStudioFilamentBed, -63: ErrStudioCollision, -64: ErrStudioCollision,
	-100: ErrStudioSlicing, -101: ErrStudioGcodePath, -102: ErrStudioGcodePath,
	-103: ErrStudioGcodePath, -104: ErrStudioGcodePath, -105: ErrStudioGcodePath,
	-108: ErrStudioMultiNozzle,
}

// StudioError est l'échec rapporté par Bambu Studio lui-même (result.json).
// errors.Is(err, ErrStudioXxx) fonctionne.
type StudioError struct {
	Code    int
	Message string // error_string de result.json
	Kind    error  // erreur nommée correspondant au code
}

func (e *StudioError) Error() string {
	return fmt.Sprintf("%v (code %d): %s", e.Kind, e.Code, e.Message)
}

func (e *StudioError) Unwrap() error { return e.Kind }

func newStudioError(code int, msg string) *StudioError {
	k, ok := studioCodes[code]
	if !ok {
		k = ErrStudioUnknown
	}
	return &StudioError{Code: code, Message: msg, Kind: k}
}
