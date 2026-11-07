package slicer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

// PlateGap : écart relatif entre deux plaques voisines dans le repère monde de Bambu Studio.
const PlateGap = 0.2

// PlateOrigins donne le décalage monde de chaque plaque quand le projet en compte n : Bambu Studio les range en
// grille de ceil(racine(n)) colonnes (PartPlateList::compute_colum_count), écart de 20 % du plateau, les lignes
// suivantes vers les Y négatifs.
func PlateOrigins(bedX, bedY float64, n int) func(int) (float64, float64) {
	cols := int(math.Ceil(math.Sqrt(float64(max(n, 1)))))
	return func(p int) (float64, float64) {
		i := p - 1
		return float64(i%cols) * bedX * (1 + PlateGap), -float64(i/cols) * bedY * (1 + PlateGap)
	}
}

// projectSettings fusionne machine + process + filaments en un seul JSON à plat : c'est le contenu de
// Metadata/project_settings.config. Bambu Studio plante à la lecture d'un 3MF qui n'en porte pas (constaté
// en 02.08) ; les clés propres aux filaments sont concaténées par emplacement.
func projectSettings(cm, cp *profiles.Resolved, cfs []*profiles.Resolved) ([]byte, error) {
	out := map[string]any{}
	for k, v := range cm.Config {
		out[k] = v
	}
	for k, v := range cp.Config {
		out[k] = v
	}
	for i, cf := range cfs {
		for k, v := range cf.Config {
			if i == 0 {
				out[k] = v
				continue
			}
			a, ok1 := out[k].([]any)
			b, ok2 := v.([]any)
			if ok1 && ok2 {
				out[k] = append(append([]any(nil), a...), b...)
			}
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeProject écrit work/src/project.3mf (3MF multi-objets avec ses réglages par objet).
func writeProject(work string, objs []mesh.ProjectObject, origin func(int) (float64, float64), v profiles.Variant, cm, cp *profiles.Resolved, cfs []*profiles.Resolved) (string, error) {
	if origin == nil {
		n := 1
		for _, o := range objs {
			n = max(n, o.Plate)
		}
		origin = PlateOrigins(v.BedX, v.BedY, n)
	}
	data, err := mesh.WriteBambuProject(objs, origin)
	if err != nil {
		return "", fmt.Errorf("%w: projet: %v", ErrInvalidRequest, err)
	}
	ps, err := projectSettings(cm, cp, cfs)
	if err != nil {
		return "", err
	}
	data, err = mesh.AddZipEntry(data, "Metadata/project_settings.config", ps)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(work, "src")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "project.3mf")
	return p, os.WriteFile(p, data, 0o644)
}
