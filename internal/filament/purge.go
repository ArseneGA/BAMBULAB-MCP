package filament

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Purge d'un G-code produit par Bambu Studio (testé avec 02.08.02.61, deux filaments).
//
// Ce que Studio écrit et que l'on lit :
//   - `M83` : extrusion relative, donc `G1 ... E<mm de filament>` ; un E négatif est une rétraction, le E positif
//     qui la suit n'est pas de la matière déposée (on le compense) ;
//   - `; FEATURE: Prime tower` : tout ce qui est extrudé jusqu'au prochain `; FEATURE:` sert à la tour de purge
//     (parois de la tour ET purge de la buse) ; `; FLUSH_START` / `; FLUSH_END` isolent la purge de buse
//     (changement de couleur) à l'intérieur de ces sections ;
//   - `T0`, `T1`... : outil (filament) actif ; `T255`/`T1000` etc. (déchargement) sont ignorés ;
//   - en-tête : `; total filament length [mm] : a,b`, `; filament_density: a,b`, `; filament_diameter: a,b`,
//     `; flush_volumes_matrix = ...`, `; flush_multiplier = ...`, `; enable_prime_tower = 1`.
//
// Le résultat est une MESURE sur le G-code réel (masse = longueur × section × densité), recoupée avec l'en-tête :
// l'écart entre la somme mesurée et `total filament length` est consigné dans Check.

// PurgeFilament est la matière jetée pour un filament de la plaque.
type PurgeFilament struct {
	Slot   int     `json:"filament_slot" jsonschema:"numéro (1..n) du filament dans le fichier découpé"`
	FlushG float64 `json:"flush_g" jsonschema:"purge de buse (changement de couleur)"`
	TowerG float64 `json:"tower_g" jsonschema:"parois et remplissage de la tour de purge, hors purge de buse"`
	WasteG float64 `json:"waste_g" jsonschema:"total jeté pour ce filament"`
	UsedG  float64 `json:"used_g" jsonschema:"total extrudé pour ce filament (pièce + purge), selon l'en-tête du G-code"`
}

// PurgeReport est la purge d'une plaque.
type PurgeReport struct {
	Plate            int             `json:"plate"`
	Source           string          `json:"source" jsonschema:"gcode : mesuré sur le G-code ; absent : aucun G-code lisible"`
	TowerEnabled     bool            `json:"prime_tower_enabled"`
	Changes          int             `json:"color_changes" jsonschema:"nombre de changements de filament dans le G-code"`
	FlushG           float64         `json:"flush_g"`
	TowerG           float64         `json:"tower_g"`
	WasteG           float64         `json:"waste_g" jsonschema:"purge de buse + tour : matière jetée"`
	TotalG           float64         `json:"total_g" jsonschema:"matière totale extrudée sur la plaque (en-tête du G-code)"`
	WastePercent     float64         `json:"waste_percent"`
	PerFilament      []PurgeFilament `json:"per_filament"`
	MatrixEstimateG  float64         `json:"matrix_estimate_g,omitempty" jsonschema:"recoupement : purge de buse calculée avec flush_volumes_matrix pour les changements observés"`
	DeviationPercent float64         `json:"deviation_percent" jsonschema:"écart (%) entre la longueur mesurée et l'en-tête : proche de 0 si la lecture est fiable"`
	Check            string          `json:"check,omitempty" jsonschema:"écart entre la longueur mesurée et celle de l'en-tête (contrôle de la lecture)"`
}

var (
	reTotalLen = regexp.MustCompile(`^; total filament length \[mm\]\s*:\s*([\d.,]+)`)
	reDensity  = regexp.MustCompile(`^; filament_density\s*[:=]\s*([\d.,]+)`)
	reDiameter = regexp.MustCompile(`^; filament_diameter\s*[:=]\s*([\d.,]+)`)
	reMatrix   = regexp.MustCompile(`^; flush_volumes_matrix\s*=\s*([\d.,]+)`)
	reMult     = regexp.MustCompile(`^; flush_multiplier\s*=\s*([\d.]+)`)
	reTower    = regexp.MustCompile(`^; enable_prime_tower\s*=\s*(\d)`)
	reTool     = regexp.MustCompile(`^T(\d+)\b`)
	reE        = regexp.MustCompile(`\bE(-?[\d.]+)`)
)

func floats(s string) []float64 {
	var out []float64
	for _, p := range strings.Split(s, ",") {
		if f, err := strconv.ParseFloat(strings.TrimSpace(p), 64); err == nil {
			out = append(out, f)
		}
	}
	return out
}

// ReadPurge mesure la purge de la plaque `plate` d'un .gcode.3mf.
func ReadPurge(archivePath string, plate int) (*PurgeReport, error) {
	if plate < 1 {
		plate = 1
	}
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("archive illisible : %w", err)
	}
	defer zr.Close()
	want := fmt.Sprintf("Metadata/plate_%d.gcode", plate)
	for _, f := range zr.File {
		if f.Name != want {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return scanPurge(rc, plate)
	}
	return nil, fmt.Errorf("%s absent de l'archive", want)
}

func scanPurge(r io.Reader, plate int) (*PurgeReport, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	var (
		feature    string
		inFlush    bool
		tool       = -1
		prevTool   = -1
		pool       float64 // rétraction à compenser
		totalLen   []float64
		density    []float64
		diameter   []float64
		matrix     []float64
		mult       = 1.0
		tower      bool
		changes    int
		matrixMM3  float64
		allLen     = map[int]float64{}
		flushLen   = map[int]float64{}
		towerLen   = map[int]float64{}
		seenChange [][2]int
	)
	for sc.Scan() {
		l := sc.Text()
		if l == "" {
			continue
		}
		if l[0] == ';' {
			switch {
			case strings.HasPrefix(l, ";VT") && tool < 0: // filament initial (« ;VT0 H-1 » après le G-code de démarrage)
				if n, err := strconv.Atoi(strings.Fields(l[3:])[0]); err == nil && n < 64 {
					tool, prevTool = n, n
				}
			case strings.HasPrefix(l, "; FEATURE:"):
				feature = strings.TrimSpace(strings.TrimPrefix(l, "; FEATURE:"))
				inFlush = false
			case l == "; FLUSH_START":
				inFlush = true
			case l == "; FLUSH_END":
				inFlush = false
			default:
				if m := reTotalLen.FindStringSubmatch(l); m != nil {
					totalLen = floats(m[1])
				} else if m := reDensity.FindStringSubmatch(l); m != nil {
					density = floats(m[1])
				} else if m := reDiameter.FindStringSubmatch(l); m != nil {
					diameter = floats(m[1])
				} else if m := reMatrix.FindStringSubmatch(l); m != nil {
					matrix = floats(m[1])
				} else if m := reMult.FindStringSubmatch(l); m != nil {
					mult, _ = strconv.ParseFloat(m[1], 64)
				} else if m := reTower.FindStringSubmatch(l); m != nil {
					tower = m[1] == "1"
				}
			}
			continue
		}
		if m := reTool.FindStringSubmatch(l); m != nil {
			n, _ := strconv.Atoi(m[1])
			if n < 64 { // T255 / T1000 : déchargement, pas un filament
				tool = n
				if prevTool >= 0 && prevTool != n {
					changes++
					seenChange = append(seenChange, [2]int{prevTool, n})
				}
				prevTool = n
			}
			continue
		}
		if l[0] != 'G' || (!strings.HasPrefix(l, "G1 ") && !strings.HasPrefix(l, "G0 ")) {
			continue
		}
		i := strings.Index(l, ";")
		body := l
		if i >= 0 {
			body = l[:i]
		}
		m := reE.FindStringSubmatch(body)
		if m == nil {
			continue
		}
		e, _ := strconv.ParseFloat(m[1], 64)
		if e < 0 {
			pool += -e
			continue
		}
		pay := math.Min(pool, e)
		pool -= pay
		net := e - pay
		if net <= 0 || tool < 0 {
			continue
		}
		allLen[tool] += net
		switch {
		case inFlush:
			flushLen[tool] += net
		case feature == "Prime tower":
			towerLen[tool] += net
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(diameter) == 0 || len(density) == 0 {
		return nil, fmt.Errorf("diamètre ou densité de filament absents de l'en-tête du G-code")
	}
	grams := func(tool int, mm float64) float64 {
		d := diameter[min(tool, len(diameter)-1)]
		rho := density[min(tool, len(density)-1)]
		return mm * math.Pi * d * d / 4 * rho / 1000
	}
	rep := &PurgeReport{Plate: plate, Source: "gcode", TowerEnabled: tower, Changes: changes}
	n := len(totalLen)
	for t := 0; t < max(n, 1); t++ {
		pf := PurgeFilament{Slot: t + 1, FlushG: round2(grams(t, flushLen[t])), TowerG: round2(grams(t, towerLen[t]))}
		if t < n {
			pf.UsedG = round2(grams(t, totalLen[t]))
		}
		pf.WasteG = round2(pf.FlushG + pf.TowerG)
		rep.FlushG += pf.FlushG
		rep.TowerG += pf.TowerG
		rep.TotalG += pf.UsedG
		rep.PerFilament = append(rep.PerFilament, pf)
	}
	rep.FlushG, rep.TowerG, rep.TotalG = round2(rep.FlushG), round2(rep.TowerG), round2(rep.TotalG)
	rep.WasteG = round2(rep.FlushG + rep.TowerG)
	if rep.TotalG > 0 {
		rep.WastePercent = round2(100 * rep.WasteG / rep.TotalG)
	}
	// Recoupement par la matrice de volumes de purge (mm³ par changement) × multiplicateur.
	if nf := int(math.Round(math.Sqrt(float64(len(matrix))))); nf > 0 && nf*nf == len(matrix) {
		for _, c := range seenChange {
			if c[0] < nf && c[1] < nf {
				matrixMM3 += matrix[c[0]*nf+c[1]] * mult
				rep.MatrixEstimateG += matrix[c[0]*nf+c[1]] * mult * density[min(c[1], len(density)-1)] / 1000
			}
		}
		rep.MatrixEstimateG = round2(rep.MatrixEstimateG)
	}
	_ = matrixMM3
	var mLen, hLen float64
	for t := 0; t < n; t++ {
		mLen += allLen[t]
		hLen += totalLen[t]
	}
	dev := 0.0
	if hLen > 0 {
		dev = 100 * (mLen - hLen) / hLen
	}
	rep.DeviationPercent = round2(dev)
	rep.Check = fmt.Sprintf("mesuré sur le G-code : %.0f mm de filament extrudé contre %.0f mm annoncés par l'en-tête (écart %.2f %%) ; %d changement(s) de filament, tour de purge %s",
		mLen, hLen, dev, changes, map[bool]string{true: "active", false: "désactivée"}[tower])
	return rep, nil
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }
