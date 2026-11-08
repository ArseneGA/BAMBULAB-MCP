package printer

import "strconv"

// Étapes de l'imprimante (champ stg_cur du rapport MQTT). Table des étapes Bambu telle que publiée par les projets
// open source (ha-bambulab : CURRENT_STAGE_IDS, OpenBambuAPI) : elle est stable d'un firmware à l'autre, mais une
// étape absente de la table est rendue « étape N (inconnue) », jamais devinée.
const (
	StageIdle255 = 255 // repos (aussi -1)

	StagePrinting          = 0
	StageBedLeveling       = 1  // mise à niveau automatique du plateau
	StageHeatbedPreheat    = 2  // chauffe du plateau
	StageSweepXY           = 3  // balayage mécanique XY
	StageChangingFilament  = 4  // changement de filament
	StageHeatingHotend     = 7  // chauffe de la buse
	StageCalibratingExtr   = 8  // calibration de l'extrusion
	StageScanningBed       = 9  // scan de la surface du plateau
	StageInspectFirstLayer = 10 // inspection de la première couche
	StageIdentifyPlate     = 11 // identification du type de plateau
	StageCalibratingLidar  = 12 // calibration du micro-lidar
	StageHoming            = 13 // mise à l'origine
	StageCleaningNozzle    = 14 // nettoyage de la buse
	StageCheckExtruderTemp = 15 // contrôle de la température de l'extrudeur
	StagePausedUser        = 16 // pause par l'utilisateur
	StageCalibratingFlow   = 19 // calibration du débit (flow)
	StageMotorNoiseCali    = 25 // calibration des vibrations / bruit moteur
)

var stageNames = map[int]string{
	0:  "Impression",
	1:  "Mise à niveau du plateau",
	2:  "Chauffe du plateau",
	3:  "Balayage mécanique XY",
	4:  "Changement de filament",
	5:  "Pause (M400)",
	6:  "Pause : fin de filament",
	7:  "Chauffe de la buse",
	8:  "Calibration de l'extrusion",
	9:  "Scan de la surface du plateau",
	10: "Inspection de la première couche",
	11: "Identification du plateau",
	12: "Calibration du micro-lidar",
	13: "Mise à l'origine",
	14: "Nettoyage de la buse",
	15: "Contrôle de la température de l'extrudeur",
	16: "Pause demandée par l'utilisateur",
	17: "Pause : capot ouvert",
	18: "Calibration du lidar",
	19: "Calibration du débit (flow)",
	20: "Pause : anomalie de température de la buse",
	21: "Pause : anomalie de température du plateau",
	22: "Déchargement du filament",
	23: "Pause : étape ignorée",
	24: "Chargement du filament",
	25: "Calibration des vibrations (bruit moteur)",
	26: "Pause : AMS perdu",
	27: "Pause : ventilateur de la buse trop lent",
	28: "Pause : régulation de la chambre",
	29: "Refroidissement de la chambre",
	30: "Pause : G-code utilisateur",
	31: "Démonstration du bruit moteur",
	32: "Pause : filament détecté sur la buse",
	33: "Pause : erreur du coupe-filament",
	34: "Pause : erreur de première couche",
	35: "Pause : buse bouchée",
}

// StageName donne le libellé français d'une étape ; « étape N (inconnue) » si absente de la table.
func StageName(id int) string {
	if StageIdle(id) {
		return "Repos"
	}
	if n, ok := stageNames[id]; ok {
		return n
	}
	return "étape " + strconv.Itoa(id) + " (inconnue)"
}

// StageIdle dit si l'étape est « repos » (à ne pas historiser).
func StageIdle(id int) bool { return id < 0 || id == StageIdle255 }
