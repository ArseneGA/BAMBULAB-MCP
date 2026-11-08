package printing

import (
	"context"
	"fmt"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// partMesh lit la pièce de la découpe (orientation de la variante appliquée comprise), posée sur Z=0.
func (s *Service) partMesh(spec *SliceSpec) (*mesh.Mesh, error) {
	f, err := s.d.Blobs.Open(spec.SourceSHA)
	if err != nil {
		return nil, err
	}
	data, err := readAll(f, 512<<20)
	f.Close()
	if err != nil {
		return nil, err
	}
	md, err := mesh.Parse(spec.SourceName, data)
	if err != nil {
		return nil, err
	}
	m := md.Combined()
	if spec.Orientation != nil {
		m = m.Transform(mesh.Matrix(spec.Orientation.Rotation))
	}
	return m, nil
}

// checkFabricable juge la pièce contre la zone utile de la machine AVANT la découpe (zones exclues du profil
// machine). Retourne une erreur claire si la pièce ne peut pas tenir ; les réserves (pièce à la limite, risque de
// décollement sans bordure demandée) vont dans warn. Sans profils Bambu Studio lisibles, ne bloque rien.
// adh : risque de décollement estimé d'après la surface de contact de la première couche (nil si maillage illisible).
func (s *Service) checkFabricable(ctx context.Context, spec *SliceSpec) (warn string, adh *mesh.AdhesionReport, err error) {
	_ = ctx
	if spec.Plan != nil {
		return "", nil, nil
	}
	m, perr := s.partMesh(spec)
	if perr != nil || len(m.T) == 0 {
		return "", nil, nil // illisible : la découpe le dira
	}
	adh = mesh.Adhesion(m)
	var notes []string
	if sl, serr := s.slicerInst(); serr == nil {
		if t, terr := s.targetOf(sl, spec); terr == nil {
			c := t.CheckFootprint(m.Bounds().Size)
			switch {
			case !c.Fits || c.TooTall:
				return "", adh, fmt.Errorf("%w : %s", ErrIncompatible, c.Message)
			case c.SafeNeedsRotation, !c.FitsSafe:
				notes = append(notes, c.Message)
			}
		}
	}
	if adh.Risky() && !overridesHaveBrim(spec.Overrides) {
		notes = append(notes, adh.Summary()+" (aucune bordure demandée dans les surcharges ; le profil de procédé peut en avoir une, vérifiée à la validation)")
	}
	return strings.Join(notes, " — "), adh, nil
}

// overridesHaveBrim : les surcharges de découpe demandent-elles une vraie bordure (≥ 3 mm) ?
func overridesHaveBrim(ov []slicer.Override) bool {
	var typ, width string
	for _, o := range ov {
		switch o.Key {
		case "brim_type":
			typ = fmt.Sprint(o.Value)
		case "brim_width":
			width = fmt.Sprint(o.Value)
		}
	}
	return brimPresent(typ, width)
}

func (s *Service) targetOf(sl *slicer.Slicer, spec *SliceSpec) (mesh.Target, error) {
	if spec.Machine != "" {
		return sl.Profiles().TargetForMachine(spec.Machine)
	}
	return sl.Profiles().TargetFor(spec.Printer, spec.Nozzle)
}
