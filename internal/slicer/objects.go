package slicer

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// ObjectUse est la consommation d'UN objet d'une plaque tranchée, lue dans son G-code (marqueurs
// « start printing object, unique label id » de Bambu Studio). C'est la preuve que les réglages par objet
// (taux de remplissage, parois, hauteur de couche…) ont bien été relus par le découpeur.
type ObjectUse struct {
	Name       string     `json:"name"`
	IdentifyID string     `json:"identify_id"`
	ExtrudedMM float64    `json:"extruded_mm"` // longueur de filament déposée par l'objet
	Grams      float64    `json:"grams"`       // masse correspondante (diamètre et densité du filament de l'archive)
	Layers     int        `json:"layers"`      // nombre de couches distinctes où l'objet est imprimé
	MaxZ       float64    `json:"max_z_mm"`
	ExtentX    [2]float64 `json:"extent_x"`
	ExtentY    [2]float64 `json:"extent_y"`
}

type siObjects struct {
	Plates []struct {
		Meta []struct {
			Key   string `xml:"key,attr"`
			Value string `xml:"value,attr"`
		} `xml:"metadata"`
		Objects []struct {
			IdentifyID string `xml:"identify_id,attr"`
			Name       string `xml:"name,attr"`
		} `xml:"object"`
	} `xml:"plate"`
}

// ObjectReport ventile la matière et les couches d'une plaque par objet. Les marqueurs d'objets doivent être
// présents dans le G-code (exclude_object / label_objects, activé par défaut dans les profils Bambu) ; sinon
// la liste est vide et l'erreur le dit.
func ObjectReport(archivePath string, plate int) ([]ObjectUse, error) {
	if plate <= 0 {
		plate = 1
	}
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	byName := map[string]*zip.File{}
	for _, f := range zr.File {
		byName[f.Name] = f
	}
	names := map[string]string{}
	if f := byName["Metadata/slice_info.config"]; f != nil {
		b, err := readEntry(f, maxMetaBytes)
		if err != nil {
			return nil, err
		}
		var si siObjects
		if err := xml.Unmarshal(b, &si); err == nil {
			for _, p := range si.Plates {
				idx := 0
				for _, m := range p.Meta {
					if m.Key == "index" {
						idx, _ = strconv.Atoi(m.Value)
					}
				}
				if idx == plate {
					for _, o := range p.Objects {
						names[o.IdentifyID] = o.Name
					}
				}
			}
		}
	}
	dia, dens := 1.75, 1.24
	if f := byName["Metadata/project_settings.config"]; f != nil {
		if b, err := readEntry(f, maxMetaBytes); err == nil {
			var st map[string]any
			if json.Unmarshal(b, &st) == nil {
				if v := parseF(firstString(st["filament_diameter"])); v > 0 {
					dia = v
				}
				if v := parseF(firstString(st["filament_density"])); v > 0 {
					dens = v
				}
			}
		}
	}
	g := byName[fmt.Sprintf("Metadata/plate_%d.gcode", plate)]
	if g == nil {
		return nil, fmt.Errorf("plaque %d absente de l'archive", plate)
	}
	rc, err := g.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	br := bufio.NewReaderSize(io.LimitReader(rc, maxGcodeBytes), 1<<20)

	type acc struct {
		e      float64
		layers map[int]bool
		maxZ   float64
		x, y   [2]float64
		n      int
	}
	accs := map[string]*acc{}
	var order []string
	cur := ""
	layer := 0
	var z, x, y float64
	for {
		line, err := readLine(br)
		if len(line) > 0 {
			l := bytes.TrimSpace(line)
			switch {
			case len(l) == 0:
			case l[0] == ';':
				if len(l) > 512 {
					break
				}
				s := string(l)
				switch {
				case strings.HasPrefix(s, "; CHANGE_LAYER"):
					layer++
				case strings.HasPrefix(s, "; Z_HEIGHT:"):
					z = parseF(strings.TrimSpace(strings.TrimPrefix(s, "; Z_HEIGHT:")))
				case strings.HasPrefix(s, "; start printing object, unique label id:"):
					cur = strings.TrimSpace(strings.TrimPrefix(s, "; start printing object, unique label id:"))
					if accs[cur] == nil {
						accs[cur] = &acc{layers: map[int]bool{}, x: [2]float64{math.Inf(1), math.Inf(-1)}, y: [2]float64{math.Inf(1), math.Inf(-1)}}
						order = append(order, cur)
					}
				case strings.HasPrefix(s, "; stop printing object"):
					cur = ""
				}
			case l[0] == 'G' && len(l) > 3 && (l[1] == '0' || l[1] == '1' || l[1] == '2' || l[1] == '3') && l[2] == ' ':
				if i := bytes.IndexByte(l, ';'); i >= 0 {
					l = l[:i]
				}
				var e float64
				var hx, hy, he bool
				for _, w := range bytes.Fields(l[2:]) {
					if len(w) < 2 {
						continue
					}
					v, perr := strconv.ParseFloat(string(w[1:]), 64)
					if perr != nil {
						continue
					}
					switch w[0] {
					case 'X':
						x, hx = v, true
					case 'Y':
						y, hy = v, true
					case 'E':
						e, he = v, true
					}
				}
				if cur != "" && he && e > 0 && (hx || hy) {
					a := accs[cur]
					a.e += e
					a.layers[layer] = true
					a.maxZ = math.Max(a.maxZ, z)
					a.x[0], a.x[1] = math.Min(a.x[0], x), math.Max(a.x[1], x)
					a.y[0], a.y[1] = math.Min(a.y[0], y), math.Max(a.y[1], y)
					a.n++
				}
			}
		}
		if err != nil {
			break
		}
	}
	if len(order) == 0 {
		return nil, fmt.Errorf("aucun marqueur d'objet dans le G-code de la plaque %d", plate)
	}
	area := math.Pi * dia * dia / 4 // mm²
	var out []ObjectUse
	for _, id := range order {
		a := accs[id]
		if a.n == 0 {
			continue
		}
		name := names[id]
		if name == "" {
			name = "objet " + id
		}
		out = append(out, ObjectUse{Name: name, IdentifyID: id, ExtrudedMM: a.e, Grams: a.e * area * dens / 1000,
			Layers: len(a.layers), MaxZ: a.maxZ, ExtentX: a.x, ExtentY: a.y})
	}
	return out, nil
}
