package printer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// Accessoires de l'imprimante (N4) : éclairage, ventilateurs, vitesse d'impression en cours.
//
// Règle de sécurité : UN SEUL point d'entrée par accessoire (SetLight, SetFan, SetSpeed), et toute commande passe par
// ValidateAccessoryPayload AVANT d'être publiée. La liste blanche est fermée : trois formes de message, rien d'autre.
// Jamais de gcode_line libre, jamais de pause / resume / stop / project_file par ce chemin.
//
// Commandes MQTT Bambu (topic device/{serial}/request), formats publiés par la communauté (OpenBambuAPI, ha-bambulab,
// Bambu Studio) et NON vérifiés sur le matériel dans ce dépôt (docs/ACCESSOIRES.md) :
//
//	lumière     {"system":{"sequence_id":"…","command":"ledctrl","led_node":"chamber_light|work_light","led_mode":"on|off",
//	             "led_on_time":500,"led_off_time":500,"loop_times":0,"interval_time":0}}
//	ventilateur {"print":{"sequence_id":"…","command":"gcode_line","param":"M106 P1|P2|P3 S<0-255>\n"}}
//	vitesse     {"print":{"sequence_id":"…","command":"print_speed","param":"1|2|3|4"}}

// LightNode désigne une lumière.
type LightNode string

const (
	LightChamber LightNode = "chamber_light"
	LightWork    LightNode = "work_light"
)

// FanKind désigne un ventilateur.
type FanKind string

const (
	FanPart    FanKind = "part"    // M106 P1
	FanAux     FanKind = "aux"     // M106 P2
	FanChamber FanKind = "chamber" // M106 P3
)

// SpeedLevel est le niveau de vitesse d'impression (param de print_speed).
type SpeedLevel int

const (
	SpeedSilent    SpeedLevel = 1
	SpeedNormal    SpeedLevel = 2
	SpeedSport     SpeedLevel = 3
	SpeedLudicrous SpeedLevel = 4
)

// Name donne le nom du niveau (silent, normal, sport, ludicrous) ; vide hors 1 à 4.
func (l SpeedLevel) Name() string {
	switch l {
	case SpeedSilent:
		return "silent"
	case SpeedNormal:
		return "normal"
	case SpeedSport:
		return "sport"
	case SpeedLudicrous:
		return "ludicrous"
	}
	return ""
}

// ParseLight lit un nom de lumière (chamber | work, avec ou sans _light ; head / toolhead = work).
func ParseLight(s string) (LightNode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "chamber", "chamber_light":
		return LightChamber, nil
	case "work", "work_light", "head", "toolhead", "tool_head":
		return LightWork, nil
	}
	return "", fmt.Errorf("lumière %q inconnue (chamber | work)", s)
}

// ParseFan lit un nom de ventilateur (part | aux | chamber).
func ParseFan(s string) (FanKind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "part", "piece", "pièce", "cooling":
		return FanPart, nil
	case "aux", "auxiliary", "auxiliaire":
		return FanAux, nil
	case "chamber", "chambre":
		return FanChamber, nil
	}
	return "", fmt.Errorf("ventilateur %q inconnu (part | aux | chamber)", s)
}

// ParseSpeed lit un niveau de vitesse : 1 à 4 ou silent|silencieux, normal|standard, sport, ludicrous.
func ParseSpeed(s string) (SpeedLevel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "silent", "silencieux":
		return SpeedSilent, nil
	case "2", "normal", "standard":
		return SpeedNormal, nil
	case "3", "sport":
		return SpeedSport, nil
	case "4", "ludicrous":
		return SpeedLudicrous, nil
	}
	return 0, fmt.Errorf("niveau de vitesse %q inconnu (1 à 4 : silent, normal, sport, ludicrous)", s)
}

// AccessoryController est l'interface OPTIONNELLE des adaptateurs qui savent piloter les accessoires (le service
// type-asserte ; un adaptateur qui ne l'a pas répond « indisponible »). Aucune de ces méthodes ne lance, n'arrête ni ne
// reprend une impression.
type AccessoryController interface {
	SetLight(ctx context.Context, node LightNode, on bool) error
	SetFan(ctx context.Context, fan FanKind, percent int) error
	SetSpeed(ctx context.Context, level SpeedLevel) error
}

var (
	// ErrAccessoryNotAllowed : la commande n'est pas dans la liste blanche des accessoires ; rien n'est publié.
	ErrAccessoryNotAllowed = errors.New("printer: commande hors liste blanche des accessoires")
	// ErrAccessoryUnavailable : l'accessoire n'existe pas sur ce modèle.
	ErrAccessoryUnavailable = errors.New("printer: accessoire non disponible sur ce modèle")
)

// AccessoryState est l'état courant des accessoires lu dans le dernier rapport MQTT ; chaque champ est absent tant que
// l'imprimante ne l'a pas transmis (jamais deviné).
type AccessoryState struct {
	ChamberLight       string `json:"chamber_light,omitempty" jsonschema:"on | off | flashing, si l'imprimante l'a rapporté"`
	WorkLight          string `json:"work_light,omitempty" jsonschema:"on | off | flashing, si l'imprimante l'a rapporté"`
	PartFanPercent     *int   `json:"part_fan_percent,omitempty" jsonschema:"vitesse du ventilateur pièce, 0 à 100 (arrondie : l'imprimante rapporte 16 crans)"`
	AuxFanPercent      *int   `json:"aux_fan_percent,omitempty"`
	ChamberFanPercent  *int   `json:"chamber_fan_percent,omitempty"`
	SpeedLevel         int    `json:"speed_level,omitempty" jsonschema:"niveau de vitesse 1 à 4"`
	SpeedName          string `json:"speed_name,omitempty" jsonschema:"silent | normal | sport | ludicrous"`
	SpeedPercentOfBase int    `json:"speed_percent,omitempty" jsonschema:"pourcentage de vitesse rapporté (spd_mag)"`
}

// parseAccessories projette les champs d'accessoires du rapport fusionné ; nil si aucun n'a été vu.
func parseAccessories(d map[string]any) *AccessoryState {
	a := &AccessoryState{}
	seen := false
	if arr, ok := d["lights_report"].([]any); ok {
		for _, e := range arr {
			m, _ := e.(map[string]any)
			if m == nil {
				continue
			}
			mode := strings.ToLower(str(m["mode"]))
			switch LightNode(str(m["node"])) {
			case LightChamber:
				a.ChamberLight, seen = mode, true
			case LightWork:
				a.WorkLight, seen = mode, true
			}
		}
	}
	fan := func(key string) *int {
		v, ok := d[key]
		if !ok {
			return nil
		}
		s := strings.TrimSpace(str(v))
		if s == "" {
			return nil
		}
		g, err := strconv.ParseFloat(s, 64)
		if err != nil || g < 0 {
			return nil
		}
		p := int(math.Round(math.Min(g, 15) * 100 / 15)) // l'imprimante rapporte un cran de 0 à 15
		seen = true
		return &p
	}
	a.PartFanPercent = fan("cooling_fan_speed")
	a.AuxFanPercent = fan("big_fan1_speed")
	a.ChamberFanPercent = fan("big_fan2_speed")
	if v, ok := d["spd_lvl"]; ok {
		if l := SpeedLevel(int(num(v))); l.Name() != "" {
			a.SpeedLevel, a.SpeedName, seen = int(l), l.Name(), true
		}
	}
	if v, ok := d["spd_mag"]; ok {
		a.SpeedPercentOfBase, seen = int(num(v)), true
	}
	if !seen {
		return nil
	}
	return a
}

// ---- capacités par modèle ----

type accModel struct {
	chamberLight, workLight, partFan, auxFan, chamberFan bool
	note                                                 string
}

// Table des accessoires par modèle. Source : documentation publique Bambu Lab (fiches techniques, wiki), OpenBambuAPI et
// ha-bambulab (fonctions CHAMBER_LIGHT / AUX_FAN / CHAMBER_FAN par modèle). NON vérifiée sur du matériel réel : un modèle absent
// de la table est refusé (« non disponible ») plutôt que deviné ; à corriger ici si une machine contredit la table.
var accTable = map[string]accModel{
	"p1p":     {partFan: true, auxFan: true, note: "P1P : pas de lumière de chambre ni de ventilateur de chambre."},
	"p1s":     {chamberLight: true, partFan: true, auxFan: true, chamberFan: true},
	"x1":      {chamberLight: true, partFan: true, auxFan: true, chamberFan: true},
	"x1c":     {chamberLight: true, partFan: true, auxFan: true, chamberFan: true},
	"x1e":     {chamberLight: true, partFan: true, auxFan: true, chamberFan: true},
	"a1":      {workLight: true, partFan: true, auxFan: true, note: "A1 : lumière de la tête seulement, pas de chambre fermée."},
	"a1_mini": {workLight: true, partFan: true, auxFan: true, note: "A1 mini : lumière de la tête seulement, pas de chambre fermée."},
	"p2s":     {chamberLight: true, partFan: true, auxFan: true, chamberFan: true},
	"h2d":     {chamberLight: true, workLight: true, partFan: true, auxFan: true, chamberFan: true},
	"h2d_pro": {chamberLight: true, workLight: true, partFan: true, auxFan: true, chamberFan: true},
	"h2s":     {chamberLight: true, workLight: true, partFan: true, auxFan: true, chamberFan: true},
	"h2c":     {chamberLight: true, workLight: true, partFan: true, auxFan: true, chamberFan: true},
}

var accAlias = map[string]string{"x1_carbon": "x1c", "x1_carbon_combo": "x1c", "x1c_combo": "x1c", "x1e_combo": "x1e", "p1s_combo": "p1s", "a1_combo": "a1",
	"a1_mini_combo": "a1_mini", "a1mini": "a1_mini", "h2d_combo": "h2d", "p2s_combo": "p2s"}

var accSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// accKey ramène un identifiant ou un nom de modèle (« p1s », « Bambu Lab A1 mini ») à la clé de la table.
func accKey(model string) string {
	n := strings.ToLower(strings.TrimSpace(model))
	n = strings.TrimPrefix(n, "bambu lab ")
	n = strings.Trim(accSlugRe.ReplaceAllString(n, "_"), "_")
	if a, ok := accAlias[n]; ok {
		return a
	}
	return n
}

// AccessoriesFor renvoie les accessoires du modèle ; known est faux si le modèle n'est pas dans la table (tout est alors
// refusé, rien n'est deviné).
func AccessoriesFor(model string) (caps contracts.AccessoryCaps, known bool) {
	m, ok := accTable[accKey(model)]
	if !ok {
		return contracts.AccessoryCaps{Note: "modèle absent de la table des accessoires : aucun accessoire n'est proposé"}, false
	}
	return contracts.AccessoryCaps{ChamberLight: m.chamberLight, WorkLight: m.workLight, PartFan: m.partFan, AuxFan: m.auxFan,
		ChamberFan: m.chamberFan, SpeedLevels: true, Note: m.note}, true
}

// HasLight, HasFan : l'accessoire existe-t-il d'après les capacités ?
func HasLight(c contracts.AccessoryCaps, n LightNode) bool {
	return (n == LightChamber && c.ChamberLight) || (n == LightWork && c.WorkLight)
}
func HasFan(c contracts.AccessoryCaps, f FanKind) bool {
	switch f {
	case FanPart:
		return c.PartFan
	case FanAux:
		return c.AuxFan
	case FanChamber:
		return c.ChamberFan
	}
	return false
}

// ---- construction des commandes et liste blanche ----

var fanIndex = map[FanKind]int{FanPart: 1, FanAux: 2, FanChamber: 3}

// FanPWM convertit un pourcentage (0 à 100) en valeur S de M106 (0 à 255), arrondie.
func FanPWM(percent int) (int, error) {
	if percent < 0 || percent > 100 {
		return 0, fmt.Errorf("vitesse de ventilateur %d %% hors bornes (0 à 100)", percent)
	}
	return (percent*255 + 50) / 100, nil
}

func lightPayload(seq string, node LightNode, on bool) (map[string]any, error) {
	if node != LightChamber && node != LightWork {
		return nil, fmt.Errorf("%w : lumière %q", ErrAccessoryNotAllowed, node)
	}
	mode := "off"
	if on {
		mode = "on"
	}
	return map[string]any{"system": map[string]any{"sequence_id": seq, "command": "ledctrl", "led_node": string(node), "led_mode": mode,
		"led_on_time": 500, "led_off_time": 500, "loop_times": 0, "interval_time": 0}}, nil
}

func fanPayload(seq string, fan FanKind, percent int) (map[string]any, error) {
	idx, ok := fanIndex[fan]
	if !ok {
		return nil, fmt.Errorf("%w : ventilateur %q", ErrAccessoryNotAllowed, fan)
	}
	s, err := FanPWM(percent)
	if err != nil {
		return nil, err
	}
	return map[string]any{"print": map[string]any{"sequence_id": seq, "command": "gcode_line", "param": fmt.Sprintf("M106 P%d S%d\n", idx, s)}}, nil
}

func speedPayload(seq string, level SpeedLevel) (map[string]any, error) {
	if level.Name() == "" {
		return nil, fmt.Errorf("niveau de vitesse %d hors bornes (1 à 4)", int(level))
	}
	return map[string]any{"print": map[string]any{"sequence_id": seq, "command": "print_speed", "param": strconv.Itoa(int(level))}}, nil
}

var m106Re = regexp.MustCompile(`^M106 P([123]) S([0-9]{1,3})\n$`)

func onlyKeys(m map[string]any, keys ...string) bool {
	if len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

// ValidateAccessoryPayload est la LISTE BLANCHE : seule une commande d'accessoire bien formée passe. Elle refuse toute
// autre commande (pause, resume, stop, project_file, gcode_line libre, M104/M140/G28…), toute clé supplémentaire et
// toute valeur hors bornes. Appelée avant chaque publication.
func ValidateAccessoryPayload(payload map[string]any) error {
	if len(payload) != 1 {
		return fmt.Errorf("%w : un seul objet attendu", ErrAccessoryNotAllowed)
	}
	if sys, ok := payload["system"].(map[string]any); ok {
		if !onlyKeys(sys, "sequence_id", "command", "led_node", "led_mode", "led_on_time", "led_off_time", "loop_times", "interval_time") {
			return fmt.Errorf("%w : champs de ledctrl", ErrAccessoryNotAllowed)
		}
		node, _ := sys["led_node"].(string)
		mode, _ := sys["led_mode"].(string)
		if sys["command"] != "ledctrl" || (node != string(LightChamber) && node != string(LightWork)) || (mode != "on" && mode != "off") {
			return fmt.Errorf("%w : ledctrl %v %v", ErrAccessoryNotAllowed, node, mode)
		}
		return nil
	}
	pr, ok := payload["print"].(map[string]any)
	if !ok {
		return fmt.Errorf("%w : objet %v", ErrAccessoryNotAllowed, keysOf(payload))
	}
	if !onlyKeys(pr, "sequence_id", "command", "param") {
		return fmt.Errorf("%w : champs de la commande", ErrAccessoryNotAllowed)
	}
	param, _ := pr["param"].(string)
	switch pr["command"] {
	case "gcode_line":
		m := m106Re.FindStringSubmatch(param)
		if m == nil {
			return fmt.Errorf("%w : gcode_line libre refusé", ErrAccessoryNotAllowed)
		}
		if v, _ := strconv.Atoi(m[2]); v > 255 {
			return fmt.Errorf("%w : M106 S%d hors bornes (0 à 255)", ErrAccessoryNotAllowed, v)
		}
		return nil
	case "print_speed":
		if param != "1" && param != "2" && param != "3" && param != "4" {
			return fmt.Errorf("%w : print_speed %q", ErrAccessoryNotAllowed, param)
		}
		return nil
	}
	return fmt.Errorf("%w : commande %v", ErrAccessoryNotAllowed, pr["command"])
}

func keysOf(m map[string]any) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	return k
}

// ---- implémentation Bambu LAN ----

var _ AccessoryController = (*Bambu)(nil)

// SetLight allume ou éteint une lumière. Autorisé à tout moment (connexion active requise).
func (b *Bambu) SetLight(ctx context.Context, node LightNode, on bool) error {
	return b.accessory(ctx, "light", func(seq string) (map[string]any, error) { return lightPayload(seq, node, on) }, nil,
		func(st Status) bool {
			if st.Accessories == nil {
				return false
			}
			cur := st.Accessories.ChamberLight
			if node == LightWork {
				cur = st.Accessories.WorkLight
			}
			return (on && cur == "on") || (!on && cur == "off")
		})
}

// SetFan règle un ventilateur (pourcentage 0 à 100 converti en M106 S0 à 255). Refusé pendant la préparation (mise à
// niveau, calibration) : la machine y exécute son propre G-code.
func (b *Bambu) SetFan(ctx context.Context, fan FanKind, percent int) error {
	return b.accessory(ctx, "fan", func(seq string) (map[string]any, error) { return fanPayload(seq, fan, percent) },
		func(s State) error {
			if s == StatePreparing {
				return fmt.Errorf("%w : ventilateur pendant la préparation (mise à niveau, calibration) : réessayer quand l'impression a démarré", ErrBadState)
			}
			return nil
		}, nil)
}

// SetSpeed règle le niveau de vitesse (1 à 4). Seulement pendant une impression (préparation, impression, pause).
func (b *Bambu) SetSpeed(ctx context.Context, level SpeedLevel) error {
	return b.accessory(ctx, "speed", func(seq string) (map[string]any, error) { return speedPayload(seq, level) },
		func(s State) error {
			if s != StatePrinting && s != StatePaused && s != StatePreparing {
				return fmt.Errorf("%w : la vitesse ne se change que pendant une impression (état : %s)", ErrBadState, s)
			}
			return nil
		}, func(st Status) bool { return st.Accessories != nil && st.Accessories.SpeedLevel == int(level) })
}

// accessory est le seul chemin de publication des accessoires : construit la commande, la valide (liste blanche), la
// publie UNE fois (jamais de renvoi), attend la réponse de l'imprimante ou l'état rapporté.
func (b *Bambu) accessory(ctx context.Context, what string, build func(seq string) (map[string]any, error), gate func(State) error, reached func(Status) bool) error {
	b.accMu.Lock()
	defer b.accMu.Unlock()
	c := b.current()
	if c == nil {
		return ErrNotConnected
	}
	if gate != nil {
		if err := gate(b.snapshot().State); err != nil {
			return err
		}
	}
	seq := b.nextSeq()
	payload, err := build(seq)
	if err != nil {
		return err
	}
	if err := ValidateAccessoryPayload(payload); err != nil {
		return err
	}
	ch := b.registerWaiter(seq)
	defer b.dropWaiter(seq)
	if err := b.publishAcked(ctx, c, payload); err != nil {
		return fmt.Errorf("printer: %s not acknowledged: %w", what, err)
	}
	err = b.waitEval(ctx, c, ch, b.cfg.CommandTimeout, func(resp map[string]any, st Status) (bool, error) {
		if resp != nil {
			if r := str(resp["result"]); r != "" && !strings.EqualFold(r, "success") {
				return false, fmt.Errorf("printer: %s refused: %s %s", what, r, str(resp["reason"]))
			}
			return true, nil
		}
		return reached != nil && reached(st), nil
	})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errTimeout):
		return fmt.Errorf("printer: %s: %w in time", what, ErrStateNotObserved)
	case errors.Is(err, errLost):
		return fmt.Errorf("printer: %s: %w", what, ErrNotConnected)
	}
	return err
}
