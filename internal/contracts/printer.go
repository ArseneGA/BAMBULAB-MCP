package contracts

// Range est un intervalle fermé (températures, etc.).
type Range struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// PrinterModel est le profil d'un modèle d'imprimante Bambu Lab (FDM).
// Tout champ inconnu est nil : il n'est jamais remplacé par la valeur d'un autre modèle.
type PrinterModel struct {
	ID          string      `json:"id" jsonschema:"identifiant stable, ex. p1s"`
	Name        string      `json:"name"`
	Vendor      string      `json:"vendor"`
	BuildVolume *Dimensions `json:"build_volume,omitempty"`
	// NozzleDiametersMM : diamètres de buse pris en charge (vide = inconnu).
	NozzleDiametersMM []float64 `json:"nozzle_diameters_mm,omitempty"`
	// Plates : plateaux compatibles (vide = inconnu).
	Plates               []string `json:"plates,omitempty"`
	Enclosed             *bool    `json:"enclosed,omitempty"`
	HeatedBed            *bool    `json:"heated_bed,omitempty"`
	MaxBedTempC          *int     `json:"max_bed_temp_c,omitempty"`
	MaxNozzleTempC       *int     `json:"max_nozzle_temp_c,omitempty"`
	HardenedNozzleOption *bool    `json:"hardened_nozzle_option,omitempty"`
	AMSSupported         *bool    `json:"ams_supported,omitempty"`
	AMSUnitsMax          *int     `json:"ams_units_max,omitempty"`
	Camera               *bool    `json:"camera,omitempty"`
	// Accessories : accessoires pilotables (lumières, ventilateurs) du modèle ; absent si le modèle est inconnu de la
	// table des accessoires. Calculé à la lecture (jamais stocké), voir internal/printer/accessories.go.
	Accessories *AccessoryCaps `json:"accessories,omitempty"`
	// Source dit d'où viennent les valeurs renseignées.
	Source string `json:"source,omitempty"`
}

// AccessoryCaps dit quels accessoires un modèle possède réellement (N4). Un accessoire absent est refusé par
// set_printer_light / set_printer_fan avec « non disponible sur ce modèle ». La vitesse d'impression existe sur tous les
// modèles (pendant une impression seulement).
type AccessoryCaps struct {
	ChamberLight bool   `json:"chamber_light" jsonschema:"lumière de chambre (ledctrl chamber_light)"`
	WorkLight    bool   `json:"work_light" jsonschema:"lumière de la tête / de travail (ledctrl work_light)"`
	PartFan      bool   `json:"part_fan" jsonschema:"ventilateur de refroidissement de la pièce (M106 P1)"`
	AuxFan       bool   `json:"aux_fan" jsonschema:"ventilateur auxiliaire (M106 P2)"`
	ChamberFan   bool   `json:"chamber_fan" jsonschema:"ventilateur de chambre (M106 P3)"`
	SpeedLevels  bool   `json:"speed_levels" jsonschema:"niveaux de vitesse (silent, normal, sport, ludicrous), pendant une impression seulement"`
	Note         string `json:"note,omitempty"`
}

// Printer est une imprimante concrète du parc. Aucun secret (code d'accès LAN)
// ne figure ici : ils restent dans un stockage dédié, jamais exposés par MCP.
type Printer struct {
	ID       string  `json:"id"`
	ModelID  string  `json:"model_id"`
	Name     string  `json:"name"`
	Host     *string `json:"host,omitempty"`
	Serial   *string `json:"serial,omitempty"`
	Firmware *string `json:"firmware,omitempty"`
	// PlateVerifiedEmpty : nil = inconnu. Une imprimante idle ne prouve pas un plateau vide.
	PlateVerifiedEmpty *bool `json:"plate_verified_empty,omitempty"`
	// StatusFreshnessSeconds : âge de la dernière télémétrie, nil = jamais reçue.
	StatusFreshnessSeconds *int `json:"status_freshness_seconds,omitempty"`
}

// SeedPrinterModels renvoie les profils machine livrés d'office : VIDE volontairement.
//
// Le catalogue des modèles Bambu Lab n'est pas figé dans le code : il est semé à
// l'exécution depuis les profils de Bambu Studio par printing.SeedPrinterModels
// (internal/printing, volume d'impression, buses, plateaux ; tout le reste reste nil =
// inconnu). Ce paquet ne peut pas importer internal/profiles : cette fonction reste donc
// vide, ne sème ni la seule P1S ni aucune valeur inventée.
func SeedPrinterModels() []PrinterModel { return nil }
