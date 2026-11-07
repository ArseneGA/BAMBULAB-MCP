package slicer

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// Placement est la position d'un objet après disposition : plaque (1..n) et coin minimal de sa boîte
// englobante dans le repère de CETTE plaque (mm).
type Placement struct {
	Index int     `json:"index"` // rang de l'objet dans la demande (0..n-1)
	Name  string  `json:"name"`
	Plate int     `json:"plate"`
	X     float64 `json:"x_mm"`
	Y     float64 `json:"y_mm"`
	W     float64 `json:"width_mm"`
	D     float64 `json:"depth_mm"`
}

// ArrangeResult est la disposition calculée par Bambu Studio (--arrange 1).
type ArrangeResult struct {
	Plates     int           `json:"plates"`
	Placements []Placement   `json:"placements"`
	Duration   time.Duration `json:"duration"`
	Command    []string      `json:"command"`
}

type msConfig struct {
	Objects []struct {
		ID   string `xml:"id,attr"`
		Meta []kv   `xml:"metadata"`
	} `xml:"object"`
	Plates []struct {
		Meta      []kv `xml:"metadata"`
		Instances []struct {
			Meta []kv `xml:"metadata"`
		} `xml:"model_instance"`
	} `xml:"plate"`
}

// Arrange laisse Bambu Studio disposer les objets (--arrange 1) sans les trancher : il les répartit
// lui-même sur autant de plaques que nécessaire. Les noms des objets doivent être uniques. La position
// retournée est celle du projet exporté, lue à nouveau (aucune hypothèse sur l'algorithme de Studio).
func (s *Slicer) Arrange(ctx context.Context, req Request) (*ArrangeResult, error) {
	if len(req.Objects) == 0 {
		return nil, fmt.Errorf("%w: aucun objet à disposer", ErrInvalidRequest)
	}
	seen := map[string]bool{}
	for _, o := range req.Objects {
		if seen[o.Name] {
			return nil, fmt.Errorf("%w: nom d'objet en double %q", ErrInvalidRequest, o.Name)
		}
		seen[o.Name] = true
	}
	objs := append([]mesh.ProjectObject(nil), req.Objects...)
	for i := range objs { // toutes les pièces partent de la plaque 1 : Studio répartit
		objs[i].Plate = 1
		objs[i].X, objs[i].Y = 0, 0
	}
	req.Objects, req.ArrangeOnly, req.Plate = objs, true, 1
	res, err := s.Slice(ctx, req)
	if res != nil {
		defer res.Cleanup()
	}
	if err != nil {
		return nil, err
	}
	pl, err := ReadArrangement(res.ArtifactPath, objs, res.BedX, res.BedY)
	if err != nil {
		return nil, err
	}
	pl.Duration, pl.Command = res.Duration, res.Command
	return pl, nil
}

// ReadArrangement lit un projet 3MF disposé par Studio : plaque et position de chaque objet. Le décalage de
// plaque est celui de PlateOrigins ; une position qui sort du plateau trahirait une disposition mal comprise.
func ReadArrangement(path string, objs []mesh.ProjectObject, bedX, bedY float64) (*ArrangeResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	md, err := mesh.ParseThreeMF(data)
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var cfg msConfig
	for _, f := range zr.File {
		if f.Name == "Metadata/model_settings.config" {
			b, err := readEntry(f, maxMetaBytes)
			if err != nil {
				return nil, err
			}
			if err := xml.Unmarshal(b, &cfg); err != nil {
				return nil, err
			}
		}
	}
	nameOf := map[string]string{}
	for _, o := range cfg.Objects {
		for _, m := range o.Meta {
			if m.Key == "name" {
				nameOf[o.ID] = m.Value
			}
		}
	}
	plateOf := map[string]int{}
	for _, p := range cfg.Plates {
		idx := 0
		for _, m := range p.Meta {
			if m.Key == "plater_id" {
				idx, _ = strconv.Atoi(m.Value)
			}
		}
		for _, in := range p.Instances {
			for _, m := range in.Meta {
				if m.Key == "object_id" {
					plateOf[m.Value] = idx
				}
			}
		}
	}
	idxOf := map[string]int{}
	for i, o := range objs {
		idxOf[o.Name] = i
	}
	out := &ArrangeResult{}
	world := map[string]mesh.BBox{}
	for _, o := range md.Objects {
		world[o.ID] = o.Mesh.Bounds()
	}
	for id, nm := range nameOf {
		i, ok := idxOf[nm]
		if !ok {
			continue
		}
		pl := plateOf[id]
		if pl < 1 {
			return nil, fmt.Errorf("%w: objet %q sans plaque dans la disposition", ErrInvalidOutput, nm)
		}
		b, ok := world[id]
		if !ok {
			return nil, fmt.Errorf("%w: objet %q absent du projet disposé", ErrInvalidOutput, nm)
		}
		out.Placements = append(out.Placements, Placement{Index: i, Name: nm, Plate: pl, X: b.Min.X, Y: b.Min.Y, W: b.Size.X, D: b.Size.Y})
		out.Plates = max(out.Plates, pl)
	}
	if len(out.Placements) != len(objs) {
		return nil, fmt.Errorf("%w: %d objets disposés sur %d demandés", ErrInvalidOutput, len(out.Placements), len(objs))
	}
	// repère monde -> repère de plaque
	origin := PlateOrigins(bedX, bedY, out.Plates)
	for i := range out.Placements {
		p := &out.Placements[i]
		ox, oy := origin(p.Plate)
		p.X, p.Y = round3(p.X-ox), round3(p.Y-oy)
		if p.X < -0.5 || p.Y < -0.5 || p.X+p.W > bedX+0.5 || p.Y+p.D > bedY+0.5 {
			return nil, fmt.Errorf("%w: objet %q hors plateau après disposition (x=%.1f y=%.1f sur %.0fx%.0f) : décalage de plaque mal interprété", ErrInvalidOutput, p.Name, p.X, p.Y, bedX, bedY)
		}
	}
	sort.Slice(out.Placements, func(a, b int) bool { return out.Placements[a].Index < out.Placements[b].Index })
	return out, nil
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
