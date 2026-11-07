package printing

import (
	"context"
	"fmt"

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
// machine). Retourne une erreur claire si la pièce ne peut pas tenir ; les réserves (pièce à la limite) vont
// dans warn. Sans profils Bambu Studio lisibles, ne bloque rien.
func (s *Service) checkFabricable(ctx context.Context, spec *SliceSpec) (warn string, err error) {
	if spec.Plan != nil {
		return "", nil
	}
	sl, serr := s.slicerInst()
	if serr != nil {
		return "", nil
	}
	_ = ctx
	t, serr := s.targetOf(sl, spec)
	if serr != nil {
		return "", nil
	}
	m, perr := s.partMesh(spec)
	if perr != nil || len(m.T) == 0 {
		return "", nil // illisible : la découpe le dira
	}
	c := t.CheckFootprint(m.Bounds().Size)
	switch {
	case !c.Fits || c.TooTall:
		return "", fmt.Errorf("%w : %s", ErrIncompatible, c.Message)
	case c.SafeNeedsRotation:
		return c.Message, nil
	case !c.FitsSafe:
		return c.Message, nil
	}
	return "", nil
}

func (s *Service) targetOf(sl *slicer.Slicer, spec *SliceSpec) (mesh.Target, error) {
	if spec.Machine != "" {
		return sl.Profiles().TargetForMachine(spec.Machine)
	}
	return sl.Profiles().TargetFor(spec.Printer, spec.Nozzle)
}
