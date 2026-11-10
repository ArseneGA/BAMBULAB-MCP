package strength

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/fem"
)

// maxContactPasses : nombre maximal de calculs successifs pour trouver les points d'appui qui portent réellement.
const maxContactPasses = 10

// restTension : part (0 à 1) de la réaction des appuis posés qui tirerait sur la pièce (« traction » : impossible pour
// une pièce simplement posée), et bilan par nœud.
func restTension(sol *fem.Solution, rests []restInfo) (frac float64, vn map[int]float64) {
	vn = map[int]float64{}
	neg, pos := 0.0, 0.0
	for _, r := range rests {
		sgn := math.Copysign(1, r.z.Normal[r.axis])
		for _, n := range r.nodes {
			c := sol.Compact(n)
			if c < 0 {
				continue
			}
			// réaction sur la pièce ; pousser = dans le sens -normale sortante
			v := -sol.Reaction[c*3+r.axis] * sgn
			vn[n] = v
			if v >= 0 {
				pos += v
			} else {
				neg += -v
			}
		}
	}
	if pos+neg > 0 {
		frac = neg / (pos + neg)
	}
	return
}

// solveWithContact résout le problème avec un contact UNILATÉRAL pour les appuis posés : un appui posé bloque la
// pièce vers le support mais ne la retient pas vers le haut. Calcul d'abord avec tous les points bloqués, puis les
// points qui tireraient sur la pièce sont relâchés et ceux qui s'enfonceraient dans le support sont re-bloqués, par
// reprises (démarrage à chaud) jusqu'à stabilité. Sans appui posé : un seul calcul.
func solveWithContact(ctx context.Context, grid *fem.Grid, eff Effective, fixed []fem.BC, loads []fem.Load, rests []restInfo,
	soft map[[2]int]bool, totalApplied float64, dl time.Time, prog func(int, float64)) (*fem.Solution, *ContactInfo, int, error) {
	cur := append([]fem.BC(nil), fixed...)
	released := map[int]bool{}
	var warm *fem.Solution
	ci := &ContactInfo{}
	var sol *fem.Solution
	passes := 0
	nRest := 0
	for _, r := range rests {
		nRest += len(r.nodes)
	}
	for pass := 1; ; pass++ {
		hold := make([]int, 0, len(released))
		for n := range released {
			hold = append(hold, n)
		}
		sort.Ints(hold)
		var err error
		sol, err = fem.Solve(fem.Problem{Grid: grid, E: eff.EMPa, Nu: eff.Nu, Fixed: cur, Hold: hold, Warm: warm, Loads: loads,
			Ctx: ctx, Tol: 1e-6, Deadline: dl, Progress: prog})
		if err != nil {
			return nil, nil, passes, err
		}
		passes = pass
		if len(rests) == 0 {
			return sol, nil, passes, nil
		}
		frac, vn := restTension(sol, rests)
		if pass == 1 {
			ci.TensionBeforePct = 100 * frac
		}
		ci.TensionAfterPct = 100 * frac
		if !sol.Converged || pass >= maxContactPasses || frac < 0.03 || (!sol.TimedOut && time.Until(dl) <= 0 && !dl.IsZero()) {
			break
		}
		// décisions : relâcher ce qui tire, re-bloquer ce qui s'enfonce
		maxU := 0.0
		for i := range sol.U {
			maxU = math.Max(maxU, math.Abs(sol.U[i]))
		}
		tolGap := 1e-3*maxU + 1e-9
		// seuil de traction : au-dessous (bruit numérique, ≈ 5 % d'une réaction moyenne) un point reste bloqué
		pushSum, pushN := 0.0, 0
		for n, v := range vn {
			if v > 0 && !released[n] {
				pushSum += v
				pushN++
			}
		}
		tolF := 1e-5 * totalApplied
		if pushN > 0 {
			tolF = math.Max(tolF, 0.05*pushSum/float64(pushN))
		}
		var release, engage [][2]int // (nœud, axe)
		for _, r := range rests {
			sgn := math.Copysign(1, r.z.Normal[r.axis])
			for _, n := range r.nodes {
				c := sol.Compact(n)
				if c < 0 {
					continue
				}
				if released[n] {
					if sol.U[c*3+r.axis]*sgn > tolGap { // s'enfonce dans le support : le contact se referme
						engage = append(engage, [2]int{n, r.axis})
					}
				} else if vn[n] < -tolF && soft[[2]int{n, r.axis}] {
					release = append(release, [2]int{n, r.axis})
				}
			}
		}
		if len(release) == 0 && len(engage) == 0 {
			break
		}
		if nRest-len(released)-len(release)+len(engage) < 3 { // trop peu de points porteurs : la pièce serait un mécanisme
			break
		}
		drop := map[[2]int]bool{}
		for _, k := range release {
			drop[k] = true
			released[k[0]] = true
		}
		for _, k := range engage {
			delete(released, k[0])
			cur = append(cur, fem.BC{Node: k[0], Dof: k[1]})
		}
		if len(drop) > 0 {
			kept := cur[:0]
			for _, b := range cur {
				if !drop[[2]int{b.Node, b.Dof}] {
					kept = append(kept, b)
				}
			}
			cur = kept
		}
		warm = sol
	}
	ci.Passes = passes
	// surface d'appui qui porte encore (poids des nœuds non relâchés)
	area, tot := 0.0, 0.0
	for _, r := range rests {
		for _, n := range r.nodes {
			tot += r.w[n]
			if !released[n] {
				area += r.w[n]
			}
		}
	}
	ci.ReleasedNodes = len(released)
	if tot > 0 {
		ci.ContactAreaPct = 100 * area / tot
	}
	ci.TippingSuspected = passes > 1 && ci.ContactAreaPct < 25
	return sol, ci, passes, nil
}
