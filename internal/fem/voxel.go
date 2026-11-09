// Package fem : calcul par éléments finis linéaire élastique en Go pur, pour estimer la tenue d'une pièce imprimée.
// Voxélisation d'un maillage triangulaire fermé, éléments hexaédriques à 8 nœuds (modes de Wilson), gradient
// conjugué préconditionné (Jacobi) sans assemblage de matrice (produit matrice-vecteur par élément, parallèle),
// contraintes aux nœuds lissées. Unités : mm, N, MPa (N/mm²).
//
// Limites assumées : géométrie en marches d'escalier (voxels), matériau isotrope, petits déplacements, pas de
// contact ni de plasticité, singularités de contrainte aux charges ponctuelles et aux arêtes vives.
package fem

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// Grid est une grille régulière de cubes de côté H ; Solid marque les cubes pleins.
type Grid struct {
	NX, NY, NZ int
	H          float64
	Origin     [3]float64
	Solid      []bool
}

// Cell : index linéaire d'un cube.
func (g *Grid) Cell(ix, iy, iz int) int { return (iz*g.NY+iy)*g.NX + ix }

// NodeIndex : index linéaire d'un nœud de la grille complète ((NX+1)(NY+1)(NZ+1) nœuds).
func (g *Grid) NodeIndex(ix, iy, iz int) int { return (iz*(g.NY+1)+iy)*(g.NX+1) + ix }

// NodeIJK retrouve les indices d'un nœud.
func (g *Grid) NodeIJK(n int) (ix, iy, iz int) {
	ix = n % (g.NX + 1)
	n /= g.NX + 1
	iy = n % (g.NY + 1)
	iz = n / (g.NY + 1)
	return
}

// NodePos : position (mm) d'un nœud.
func (g *Grid) NodePos(n int) [3]float64 {
	ix, iy, iz := g.NodeIJK(n)
	return [3]float64{g.Origin[0] + float64(ix)*g.H, g.Origin[1] + float64(iy)*g.H, g.Origin[2] + float64(iz)*g.H}
}

// NumNodes : nombre de nœuds de la grille complète.
func (g *Grid) NumNodes() int { return (g.NX + 1) * (g.NY + 1) * (g.NZ + 1) }

// SolidCount : nombre de cubes pleins.
func (g *Grid) SolidCount() int {
	n := 0
	for _, s := range g.Solid {
		if s {
			n++
		}
	}
	return n
}

// BoxGrid : parallélépipède plein nx×ny×nz de cubes de côté h (tests, pièces prismatiques).
func BoxGrid(nx, ny, nz int, h float64) *Grid {
	g := &Grid{NX: nx, NY: ny, NZ: nz, H: h, Solid: make([]bool, nx*ny*nz)}
	for i := range g.Solid {
		g.Solid[i] = true
	}
	return g
}

// MeshVolume : volume algébrique (mm³) d'un maillage fermé orienté.
func MeshVolume(m *mesh.Mesh) float64 {
	v := 0.0
	for _, t := range m.T {
		a, b, c := m.V[t[0]], m.V[t[1]], m.V[t[2]]
		v += a.Dot(b.Cross(c))
	}
	return math.Abs(v / 6)
}

// PitchPlan dit comment le pas a été choisi.
type PitchPlan struct {
	H          float64
	CellsTotal int  // cubes de la boîte englobante
	Capped     bool // le pas a dû être agrandi pour respecter un plafond
	CapReason  string
}

// ChoosePitch choisit le côté de cube : viser maxSolid cubes pleins (d'après le volume), sans dépasser maxCells cubes
// dans la boîte englobante (mémoire). Plancher : size_min/3 de la plus petite dimension n'est pas imposé (voir ThinWarning).
func ChoosePitch(m *mesh.Mesh, maxSolid, maxCells int) PitchPlan {
	bb := m.Bounds()
	vol := MeshVolume(m)
	sx, sy, sz := math.Max(bb.Size.X, 1e-6), math.Max(bb.Size.Y, 1e-6), math.Max(bb.Size.Z, 1e-6)
	if vol <= 0 {
		vol = sx * sy * sz * 0.1
	}
	h := math.Cbrt(vol / float64(maxSolid))
	pl := PitchPlan{H: h}
	count := func(h float64) int {
		return int(math.Ceil(sx/h)) * int(math.Ceil(sy/h)) * int(math.Ceil(sz/h))
	}
	for count(h) > maxCells {
		h *= 1.05
		pl.Capped, pl.CapReason = true, fmt.Sprintf("boîte englobante limitée à %d cubes", maxCells)
	}
	pl.H, pl.CellsTotal = h, count(h)
	return pl
}

type crossing struct {
	col int32
	z   float64
}

// Voxelize remplit la grille par lancer de rayons verticaux (parité des croisements) au centre de chaque colonne.
// Le maillage doit être fermé : un croisement impair est ignoré (compté dans leaky).
func Voxelize(m *mesh.Mesh, h float64) (g *Grid, leakyColumns int, err error) {
	if len(m.T) == 0 || h <= 0 {
		return nil, 0, errors.New("maillage vide ou pas invalide")
	}
	bb := m.Bounds()
	nx := int(math.Ceil(bb.Size.X/h - 1e-9))
	ny := int(math.Ceil(bb.Size.Y/h - 1e-9))
	nz := int(math.Ceil(bb.Size.Z/h - 1e-9))
	if nx < 1 {
		nx = 1
	}
	if ny < 1 {
		ny = 1
	}
	if nz < 1 {
		nz = 1
	}
	if int64(nx)*int64(ny)*int64(nz) > 40_000_000 {
		return nil, 0, fmt.Errorf("grille trop grande (%d×%d×%d)", nx, ny, nz)
	}
	g = &Grid{NX: nx, NY: ny, NZ: nz, H: h, Origin: [3]float64{bb.Min.X, bb.Min.Y, bb.Min.Z}, Solid: make([]bool, nx*ny*nz)}
	// décalage infime et irrationnel : évite les rayons exactement sur une arête
	ex, ey := 1.3141592653e-7*h, 2.7182818284e-7*h
	var xs []crossing
	for _, t := range m.T {
		a, b, c := m.V[t[0]], m.V[t[1]], m.V[t[2]]
		den := (b.Y-c.Y)*(a.X-c.X) + (c.X-b.X)*(a.Y-c.Y)
		if math.Abs(den) < 1e-14 {
			continue
		}
		minX, maxX := math.Min(a.X, math.Min(b.X, c.X)), math.Max(a.X, math.Max(b.X, c.X))
		minY, maxY := math.Min(a.Y, math.Min(b.Y, c.Y)), math.Max(a.Y, math.Max(b.Y, c.Y))
		i0 := int(math.Floor((minX-g.Origin[0])/h - 0.5))
		i1 := int(math.Ceil((maxX-g.Origin[0])/h - 0.5))
		j0 := int(math.Floor((minY-g.Origin[1])/h - 0.5))
		j1 := int(math.Ceil((maxY-g.Origin[1])/h - 0.5))
		if i0 < 0 {
			i0 = 0
		}
		if j0 < 0 {
			j0 = 0
		}
		if i1 >= nx {
			i1 = nx - 1
		}
		if j1 >= ny {
			j1 = ny - 1
		}
		for j := j0; j <= j1; j++ {
			py := g.Origin[1] + (float64(j)+0.5)*h + ey
			for i := i0; i <= i1; i++ {
				px := g.Origin[0] + (float64(i)+0.5)*h + ex
				u := ((b.Y-c.Y)*(px-c.X) + (c.X-b.X)*(py-c.Y)) / den
				v := ((c.Y-a.Y)*(px-c.X) + (a.X-c.X)*(py-c.Y)) / den
				w := 1 - u - v
				if u < 0 || v < 0 || w < 0 {
					continue
				}
				xs = append(xs, crossing{int32(j*nx + i), u*a.Z + v*b.Z + w*c.Z})
			}
		}
	}
	sort.Slice(xs, func(p, q int) bool {
		if xs[p].col != xs[q].col {
			return xs[p].col < xs[q].col
		}
		return xs[p].z < xs[q].z
	})
	for s := 0; s < len(xs); {
		e := s
		for e < len(xs) && xs[e].col == xs[s].col {
			e++
		}
		n := e - s
		if n%2 == 1 {
			leakyColumns++
			n--
		}
		col := int(xs[s].col)
		iy, ix := col/nx, col%nx
		for k := 0; k < n; k += 2 {
			z0, z1 := xs[s+k].z, xs[s+k+1].z
			k0 := int(math.Ceil((z0-g.Origin[2])/h - 0.5))
			k1 := int(math.Ceil((z1-g.Origin[2])/h-0.5)) - 1
			if k0 < 0 {
				k0 = 0
			}
			if k1 >= nz {
				k1 = nz - 1
			}
			for iz := k0; iz <= k1; iz++ {
				g.Solid[g.Cell(ix, iy, iz)] = true
			}
		}
		s = e
	}
	return g, leakyColumns, nil
}
