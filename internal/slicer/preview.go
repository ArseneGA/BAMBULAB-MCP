package slicer

import (
	"fmt"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

// ResolveWithOverrides résout les profils (copies privées) et applique les
// surcharges typées exactement comme Slice, sans lancer Bambu Studio. Sert à
// valider des modifications et à calculer des différences.
func ResolveWithOverrides(st *profiles.Store, machine, process string, filaments []string, ovs []Override) (m, p *profiles.Resolved, fs []*profiles.Resolved, applied []AppliedOverride, err error) {
	rm, err := st.Resolve(profiles.KindMachine, machine)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	rp, err := st.Resolve(profiles.KindProcess, process)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if len(filaments) == 0 {
		return nil, nil, nil, nil, fmt.Errorf("%w: au moins un filament requis", ErrInvalidRequest)
	}
	m, p = rm.Clone(), rp.Clone()
	for _, fn := range filaments {
		rf, err := st.Resolve(profiles.KindFilament, fn)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		fs = append(fs, rf.Clone())
	}
	applied, err = applyOverrides(st, m, p, fs, ovs)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return m, p, fs, applied, nil
}
