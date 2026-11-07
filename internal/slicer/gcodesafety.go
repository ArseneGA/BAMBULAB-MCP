package slicer

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Analyse de sûreté du G-code : températures demandées (M104/M109 buse, M140/M190 plateau, M141/M191
// chambre) et commandes présentes. Ce que Bambu Studio produit pour la P1S et l'A1 mini est connu et fini ;
// tout autre commande est signalée, et les commandes capables de modifier le micrologiciel sont refusées.

// ExpectedCommands : commandes observées dans les archives réellement produites par Bambu Studio 02.08 pour la
// P1S (PLA, PETG, supports, bordure, repassage) et l'A1 mini, complétées par celles des gabarits de G-code
// des profils machine BBL de la famille P1/A1 (changement de filament AMS, vidéo accélérée, détection
// d'enroulement). Voir TestExpectedCommandsCoverRealArchives.
var ExpectedCommands = map[string]bool{
	// déplacements et modes
	"G0": true, "G1": true, "G2": true, "G3": true, "G4": true, "G17": true, "G21": true, "G28": true,
	"G29": true, "G29.1": true, "G29.2": true, "G29.4": true, "G39": true, "G39.3": true, "G39.4": true,
	"G90": true, "G91": true, "G92": true, "G380": true, "G392": true, "G2814": true,
	// températures, ventilateurs, mouvement (limites d'accélération/vitesse/jerk du moment)
	"M104": true, "M106": true, "M109": true, "M140": true, "M142": true, "M190": true,
	"M201": true, "M201.2": true, "M203": true, "M204": true, "M205": true, "M220": true, "M221": true,
	// extrusion et état de la machine
	"M17": true, "M18": true, "M73": true, "M73.2": true, "M83": true, "M211": true, "M290": true, "M400": true,
	"M412": true, "M500": true, "M710": true, "M900": true,
	// AMS et changement de filament
	"M620": true, "M620.1": true, "M620.3": true, "M620.10": true, "M620.11": true, "M621": true,
	"M622": true, "M622.1": true, "M623": true, "M628": true, "M629": true, "M630": true,
	// supervision propre à Bambu (étalonnage, capteurs, caméra, lampe)
	"M960": true, "M970": true, "M970.2": true, "M970.3": true, "M971": true, "M974": true, "M975": true,
	"M981": true, "M982.2": true, "M983": true, "M9833": true, "M9833.2": true, "M984": true, "M991": true,
	"M1002": true, "M1003": true, "M1004": true, "M1006": true, "M1007": true, "M1031": true,
}

// DangerousCommands : commandes qui modifient le micrologiciel, les limites de sécurité ou le stockage de
// l'imprimante. Leur présence dans un G-code à imprimer est un refus, pas un avertissement.
var DangerousCommands = map[string]string{
	"M92": "modifie les pas par mm", "M112": "arrêt d'urgence", "M206": "décalage d'origine persistant",
	"M207": "paramètres de rétraction du micrologiciel", "M208": "limites de course", "M301": "réglage PID de la buse",
	"M302": "autorise l'extrusion à froid", "M303": "auto-réglage PID (chauffe jusqu'à la consigne)",
	"M304": "réglage PID du plateau", "M306": "modèle de chauffe", "M502": "restaure les réglages d'usine",
	"M906": "courant des moteurs", "M907": "courant des moteurs", "M908": "courant des moteurs",
	"M913": "sensibilité de détection de blocage", "M914": "sensibilité de détection de blocage",
	"M997": "mise à jour du micrologiciel", "M999": "redémarrage du micrologiciel",
	"M28": "écriture sur la carte SD", "M29": "écriture sur la carte SD", "M30": "suppression d'un fichier de la carte SD",
	"M32": "lancement d'un fichier de la carte SD",
}

// UnexpectedCommand : une commande hors ensemble attendu.
type UnexpectedCommand struct {
	Command string `json:"command"`
	Count   int    `json:"count"`
	Line    int    `json:"first_line"`
	Example string `json:"example"`
	Reason  string `json:"reason,omitempty"`
}

// GcodeSafety : ce qui a été lu dans le G-code d'une plaque.
type GcodeSafety struct {
	// Buse (°C) : consigne maximale avant la 1re couche (démarrage), consigne en vigueur à la 1re couche,
	// minimum et maximum des consignes pendant l'impression (hors extinction à 0), maximum global.
	NozzleStartMaxC   float64 `json:"nozzle_start_max_c"`
	NozzleFirstLayerC float64 `json:"nozzle_first_layer_c"`
	NozzlePrintMinC   float64 `json:"nozzle_print_min_c"`
	NozzlePrintMaxC   float64 `json:"nozzle_print_max_c"`
	NozzleMaxC        float64 `json:"nozzle_max_c"`
	// Plateau (°C).
	BedFirstLayerC float64 `json:"bed_first_layer_c"`
	BedMinC        float64 `json:"bed_min_c"`
	BedMaxC        float64 `json:"bed_max_c"`
	// Chambre (°C), 0 si jamais demandée.
	ChamberMaxC float64 `json:"chamber_max_c"`

	Unexpected []UnexpectedCommand `json:"unexpected_commands,omitempty"`
	Dangerous  []UnexpectedCommand `json:"dangerous_commands,omitempty"`
	BadTemps   []string            `json:"unreadable_temperatures,omitempty"`
}

type gcodeSafetyScan struct {
	GcodeSafety
	nozzleNow, bedNow float64
	seenPrintNozzle   bool
	seenBed           bool
	unexp             map[string]*UnexpectedCommand
	danger            map[string]*UnexpectedCommand
	lineNo            int
}

func (g *gcodeSafetyScan) layerOne() {
	g.NozzleFirstLayerC, g.BedFirstLayerC = g.nozzleNow, g.bedNow
	g.notePrintNozzle(g.nozzleNow)
}

func (g *gcodeSafetyScan) notePrintNozzle(v float64) {
	if v <= 0 {
		return
	}
	if !g.seenPrintNozzle || v < g.NozzlePrintMinC {
		g.NozzlePrintMinC = v
	}
	if v > g.NozzlePrintMaxC {
		g.NozzlePrintMaxC = v
	}
	g.seenPrintNozzle = true
}

// command analyse une ligne de commande (sans commentaire de tête). inLayers : la 1re couche est commencée.
func (g *gcodeSafetyScan) command(l []byte, inLayers bool) {
	// espaces de tête, numéro de ligne « N123 » et somme de contrôle « *45 » (acceptés par Marlin)
	for len(l) > 0 && (l[0] == ' ' || l[0] == '\t') {
		l = l[1:]
	}
	if len(l) == 0 || l[0] == ';' {
		return
	}
	if (l[0] == 'N' || l[0] == 'n') && len(l) > 1 && l[1] >= '0' && l[1] <= '9' {
		i := 1
		for i < len(l) && l[i] >= '0' && l[i] <= '9' {
			i++
		}
		for i < len(l) && (l[i] == ' ' || l[i] == '\t') {
			i++
		}
		l = l[i:]
	}
	if i := indexByte(l, ';'); i >= 0 {
		l = l[:i]
	}
	if i := indexByte(l, '*'); i >= 0 {
		l = l[:i]
	}
	if len(l) == 0 {
		return
	}
	if l[0] == '{' || l[0] == '}' { // balises de gabarit non évaluées : jamais dans un G-code final, mais sans effet
		g.note(string(l[:min(len(l), 40)]), "gabarit non évalué", l, false)
		return
	}
	c := l[0]
	if c >= 'a' && c <= 'z' {
		c -= 'a' - 'A'
	}
	if c < 'A' || c > 'Z' {
		g.note("?", "ligne qui n'est pas une commande", l, false)
		return
	}
	i := 1
	for i < len(l) && ((l[i] >= '0' && l[i] <= '9') || l[i] == '.') {
		i++
	}
	if i == 1 {
		g.note(string(c), "commande sans numéro", l, false)
		return
	}
	tok := string(c) + string(l[1:i])
	rest := l[i:]
	// G1 domine largement : chemin court
	if tok == "G1" || tok == "G0" {
		return
	}
	switch tok {
	case "M104", "M109":
		g.temp(tok, rest, l, &g.nozzleNow, inLayers, 'n')
		return
	case "M140", "M190":
		g.temp(tok, rest, l, &g.bedNow, inLayers, 'b')
		return
	case "M141", "M191":
		var ch float64
		g.temp(tok, rest, l, &ch, inLayers, 'c')
		g.note(tok, "chambre chauffée absente des familles P1/A1", l, false)
		return
	}
	if why, bad := DangerousCommands[tok]; bad {
		g.note(tok, why, l, true)
		return
	}
	if ExpectedCommands[tok] {
		return
	}
	if c == 'T' {
		if n, err := strconv.Atoi(tok[1:]); err == nil && (n <= 15 || n == 255 || n == 1000) { // changement d'outil/AMS
			return
		}
	}
	g.note(tok, "commande hors de l'ensemble produit par Bambu Studio pour P1/A1", l, false)
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func (g *gcodeSafetyScan) note(tok, reason string, l []byte, dangerous bool) {
	m := &g.unexp
	if dangerous {
		m = &g.danger
	}
	if *m == nil {
		*m = map[string]*UnexpectedCommand{}
	}
	u := (*m)[tok]
	if u == nil {
		ex := string(l)
		if len(ex) > 120 {
			ex = ex[:120]
		}
		u = &UnexpectedCommand{Command: tok, Line: g.lineNo, Example: strings.TrimSpace(ex), Reason: reason}
		(*m)[tok] = u
	}
	u.Count++
}

// temp lit la consigne d'une commande de chauffe (paramètres S et R) et met à jour les extrêmes.
func (g *gcodeSafetyScan) temp(tok string, rest, line []byte, now *float64, inLayers bool, what byte) {
	got := false
	for _, w := range splitFields(rest) {
		if len(w) < 2 {
			continue
		}
		p := w[0]
		if p >= 'a' && p <= 'z' {
			p -= 'a' - 'A'
		}
		if p != 'S' && p != 'R' {
			continue
		}
		v, err := strconv.ParseFloat(string(w[1:]), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			// Marlin lit aussi des formes que Go refuse (hexadécimal…) : une consigne illisible est un refus
			g.BadTemps = append(g.BadTemps, strings.TrimSpace(string(line)))
			continue
		}
		got = true
		switch what {
		case 'n':
			*now = v
			g.NozzleMaxC = math.Max(g.NozzleMaxC, v)
			if inLayers {
				g.notePrintNozzle(v)
			} else {
				g.NozzleStartMaxC = math.Max(g.NozzleStartMaxC, v)
			}
		case 'b':
			*now = v
			g.BedMaxC = math.Max(g.BedMaxC, v)
			if v > 0 {
				if !g.seenBed || v < g.BedMinC {
					g.BedMinC = v
				}
				g.seenBed = true
			}
		case 'c':
			g.ChamberMaxC = math.Max(g.ChamberMaxC, v)
		}
	}
	_ = got
}

func splitFields(b []byte) [][]byte {
	var out [][]byte
	i := 0
	for i < len(b) {
		for i < len(b) && (b[i] == ' ' || b[i] == '\t') {
			i++
		}
		j := i
		for j < len(b) && b[j] != ' ' && b[j] != '\t' {
			j++
		}
		if j > i {
			out = append(out, b[i:j])
		}
		i = j
	}
	return out
}

func (g *gcodeSafetyScan) finish() {
	flat := func(m map[string]*UnexpectedCommand) []UnexpectedCommand {
		var out []UnexpectedCommand
		for _, u := range m {
			out = append(out, *u)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Line < out[j].Line })
		if len(out) > 30 {
			out = out[:30]
		}
		return out
	}
	g.Unexpected, g.Dangerous = flat(g.unexp), flat(g.danger)
	if len(g.BadTemps) > 10 {
		g.BadTemps = g.BadTemps[:10]
	}
}

// ---- jugement ----

// Verdict compare une analyse à des bornes de confiance. nozzleHi : plafond de la buse pendant l'impression
// (plage du filament) ; nozzleMachine : maximum du modèle d'imprimante ; bedMax, chamberMax idem. Retourne les
// motifs de refus et les avertissements.
func (g *GcodeSafety) Verdict(nozzleHi, nozzleMachine, bedMax, chamberMax float64) (refus, avert []string) {
	if g.NozzleMaxC > nozzleMachine {
		refus = append(refus, fmt.Sprintf("consigne de buse %.0f °C au-delà du maximum du modèle (%.0f °C)", g.NozzleMaxC, nozzleMachine))
	}
	if nozzleHi > 0 && g.NozzlePrintMaxC > nozzleHi && g.NozzleMaxC <= nozzleMachine {
		refus = append(refus, fmt.Sprintf("consigne de buse %.0f °C pendant l'impression au-delà de la plage du filament (%.0f °C)", g.NozzlePrintMaxC, nozzleHi))
	}
	if g.BedMaxC > bedMax {
		refus = append(refus, fmt.Sprintf("consigne de plateau %.0f °C au-delà du maximum du modèle (%.0f °C)", g.BedMaxC, bedMax))
	}
	if g.ChamberMaxC > chamberMax {
		refus = append(refus, fmt.Sprintf("consigne de chambre %.0f °C alors que le modèle plafonne à %.0f °C", g.ChamberMaxC, chamberMax))
	}
	for _, d := range g.Dangerous {
		refus = append(refus, fmt.Sprintf("%s (%s) ligne %d : %s", d.Command, d.Reason, d.Line, d.Example))
	}
	for _, b := range g.BadTemps {
		refus = append(refus, "consigne de température illisible : "+b)
	}
	for _, u := range g.Unexpected {
		avert = append(avert, fmt.Sprintf("%s ×%d (1re : ligne %d, « %s »)", u.Command, u.Count, u.Line, u.Example))
	}
	return refus, avert
}
