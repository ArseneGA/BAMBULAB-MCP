package printing

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"io"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// Points d'entrée pour internal/strength (test de résistance) : lecture du maillage d'un modèle, réglages de la
// dernière découpe, et texte bitmap des rendus PNG. Aucune logique propre au test de résistance ici.

// ModelMeshRef décrit le maillage lu.
type ModelMeshRef struct {
	ModelID   string
	VersionID string
	Name      string
	SHA256    string
	Tags      []string
}

// ModelMesh lit le maillage (mm) de la version demandée d'un modèle (défaut : la plus récente).
func (s *Service) ModelMesh(ctx context.Context, modelID, versionID string) (*mesh.Mesh, ModelMeshRef, error) {
	ref := ModelMeshRef{ModelID: modelID}
	ver, src, err := s.sourceFor(ctx, modelID, versionID)
	if err != nil {
		return nil, ref, err
	}
	ref.VersionID, ref.SHA256 = ver.ID, src.SHA256
	ref.Name = src.Name
	if m, merr := s.d.Store.GetModel(ctx, modelID); merr == nil {
		ref.Name, ref.Tags = m.Name, m.Tags
	}
	f, err := s.d.Blobs.Open(src.SHA256)
	if err != nil {
		return nil, ref, err
	}
	data, err := io.ReadAll(io.LimitReader(f, 512<<20))
	f.Close()
	if err != nil {
		return nil, ref, err
	}
	md, err := mesh.Parse(src.Name, data)
	if err != nil {
		return nil, ref, invalidf("modèle illisible : %v", err)
	}
	m := md.Combined()
	if len(m.T) == 0 {
		return nil, ref, invalidf("modèle sans géométrie")
	}
	return m, ref, nil
}

// SliceSettings : réglages lus dans la découpe la plus récente d'un modèle (rien n'est inventé : Found=false sinon).
type SliceSettings struct {
	Found     bool
	Filaments []string
	Walls     int     // 0 = inconnu
	InfillPct float64 // 0 = inconnu
	Source    string
}

// LastSliceSettings relit matière, parois et remplissage de la dernière impression préparée pour ce modèle.
func (s *Service) LastSliceSettings(ctx context.Context, modelID string) SliceSettings {
	js, err := s.ListJobs(ctx, "", 200)
	if err != nil {
		return SliceSettings{}
	}
	for _, j := range js {
		if j.ModelID != modelID || j.Slice == nil {
			continue
		}
		out := SliceSettings{Found: true, Filaments: j.Slice.Filaments, Source: "découpe " + j.ID}
		for _, o := range j.Slice.Overrides {
			v := strings.TrimSuffix(strings.TrimSpace(fmt.Sprint(o.Value)), "%")
			switch o.Key {
			case "wall_loops":
				if n, e := strconv.Atoi(v); e == nil {
					out.Walls = n
				}
			case "sparse_infill_density":
				if f, e := strconv.ParseFloat(v, 64); e == nil {
					out.InfillPct = f
				}
			}
		}
		return out
	}
	return SliceSettings{}
}

// DrawText écrit un texte bitmap 5x7 (capitales, sans accents) ; renvoie l'abscisse de fin.
func DrawText(img *image.RGBA, x, y, scale int, c color.Color, s string) int {
	return drawText(img, x, y, scale, c, s)
}

// TextWidth : largeur en pixels d'un texte bitmap.
func TextWidth(s string, scale int) int { return textWidth(s, scale) }
