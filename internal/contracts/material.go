package contracts

// CompatibilityStatus qualifie la compatibilité matière/machine sans la présumer.
type CompatibilityStatus string

const (
	CompatUnknown  CompatibilityStatus = "inconnu"
	CompatToVerify CompatibilityStatus = "a_verifier"
	CompatDeclared CompatibilityStatus = "compatible_declare"
	CompatMeasured CompatibilityStatus = "compatible_mesure"
)

// Calibration est une calibration datée d'une matière.
type Calibration struct {
	Date string `json:"date" jsonschema:"AAAA-MM-JJ"`
	Note string `json:"note"`
}

// Material est un matériau extensible ; tout champ nil est inconnu.
type Material struct {
	ID                     string              `json:"id"`
	Family                 string              `json:"family" jsonschema:"PLA, PETG, ASA, TPU, PA-CF, ..."`
	Maker                  *string             `json:"maker,omitempty"`
	Reference              *string             `json:"reference,omitempty"`
	Colour                 *string             `json:"colour,omitempty"`
	DiameterMM             *float64            `json:"diameter_mm,omitempty"`
	NozzleMinMM            *float64            `json:"nozzle_min_mm,omitempty"`
	NozzleTempC            *Range              `json:"nozzle_temp_c,omitempty"`
	BedTempC               *Range              `json:"bed_temp_c,omitempty"`
	EnclosureAdvised       *bool               `json:"enclosure_advised,omitempty"`
	HardenedNozzleRequired *bool               `json:"hardened_nozzle_required,omitempty"`
	Compatibility          CompatibilityStatus `json:"compatibility"`
	SpoolState             *string             `json:"spool_state,omitempty"`
	Calibrations           []Calibration       `json:"calibrations,omitempty"`
}
