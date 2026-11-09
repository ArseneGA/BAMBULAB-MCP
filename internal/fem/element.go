package fem

import "math"

// Élément hexaédrique à 8 nœuds, cube de côté h, isotrope linéaire élastique, avec modes incompatibles de Wilson
// (9 degrés de liberté internes condensés statiquement) : supprime le verrouillage en cisaillement qui rendrait les
// poutres minces (quelques éléments dans l'épaisseur) trop raides. Sur un maillage de cubes identiques, l'élément
// condensé est le même partout : il est calculé UNE fois (pour E = 1, h = 1) puis mis à l'échelle (K ∝ E·h).
//
// Ordre des nœuds : n = i + 2j + 4k, i, j, k ∈ {0,1} (coordonnée -1 puis +1 selon x, y, z).
// Ordre des composantes : xx, yy, zz, xy, yz, zx (cisaillements en ingénierie).

// ElementMatrices regroupe ce qui dépend seulement du coefficient de Poisson.
type ElementMatrices struct {
	K0 [24 * 24]float64   // rigidité condensée pour E = 1, h = 1
	B  [8][6 * 24]float64 // déformation aux 8 nœuds en fonction des déplacements nodaux (modes de Wilson inclus), h = 1
	D  [36]float64        // matrice d'élasticité pour E = 1
}

var nodeSign = [8][3]float64{
	{-1, -1, -1}, {1, -1, -1}, {-1, 1, -1}, {1, 1, -1},
	{-1, -1, 1}, {1, -1, 1}, {-1, 1, 1}, {1, 1, 1},
}

func elasticity(nu float64) [36]float64 {
	var d [36]float64
	c := 1 / ((1 + nu) * (1 - 2*nu))
	a, b, g := c*(1-nu), c*nu, 1/(2*(1+nu))
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if i == j {
				d[i*6+j] = a
			} else {
				d[i*6+j] = b
			}
		}
	}
	d[3*6+3], d[4*6+4], d[5*6+5] = g, g, g
	return d
}

// bMatrix : matrices 6x24 (déplacements) et 6x9 (modes de Wilson) au point naturel (ξ,η,ζ), h = 1
// (dérivée physique = 2 × dérivée naturelle).
func bMatrix(xi, et, ze float64) (bu [6 * 24]float64, ba [6 * 9]float64) {
	for n := 0; n < 8; n++ {
		s := nodeSign[n]
		dx := 2 * 0.125 * s[0] * (1 + s[1]*et) * (1 + s[2]*ze)
		dy := 2 * 0.125 * s[1] * (1 + s[0]*xi) * (1 + s[2]*ze)
		dz := 2 * 0.125 * s[2] * (1 + s[0]*xi) * (1 + s[1]*et)
		c := n * 3
		bu[0*24+c+0], bu[1*24+c+1], bu[2*24+c+2] = dx, dy, dz
		bu[3*24+c+0], bu[3*24+c+1] = dy, dx
		bu[4*24+c+1], bu[4*24+c+2] = dz, dy
		bu[5*24+c+0], bu[5*24+c+2] = dz, dx
	}
	dm := [3][3]float64{{-2 * xi * 2, 0, 0}, {0, -2 * et * 2, 0}, {0, 0, -2 * ze * 2}}
	for m := 0; m < 3; m++ {
		dx, dy, dz := dm[m][0], dm[m][1], dm[m][2]
		c := m * 3
		ba[0*9+c+0], ba[1*9+c+1], ba[2*9+c+2] = dx, dy, dz
		ba[3*9+c+0], ba[3*9+c+1] = dy, dx
		ba[4*9+c+1], ba[4*9+c+2] = dz, dy
		ba[5*9+c+0], ba[5*9+c+2] = dz, dx
	}
	return
}

// NewElementMatrices calcule l'élément condensé pour un coefficient de Poisson donné.
func NewElementMatrices(nu float64) *ElementMatrices {
	em := &ElementMatrices{D: elasticity(nu)}
	var kuu [24 * 24]float64
	var kua [24 * 9]float64
	var kaa [9 * 9]float64
	g := 1 / math.Sqrt(3)
	w := 0.125 // détJ = (h/2)^3 avec h = 1, poids de Gauss 1
	for _, a := range [2]float64{-g, g} {
		for _, b := range [2]float64{-g, g} {
			for _, c := range [2]float64{-g, g} {
				bu, ba := bMatrix(a, b, c)
				var dbu [6 * 24]float64
				var dba [6 * 9]float64
				for i := 0; i < 6; i++ {
					for j := 0; j < 24; j++ {
						s := 0.0
						for k := 0; k < 6; k++ {
							s += em.D[i*6+k] * bu[k*24+j]
						}
						dbu[i*24+j] = s
					}
					for j := 0; j < 9; j++ {
						s := 0.0
						for k := 0; k < 6; k++ {
							s += em.D[i*6+k] * ba[k*9+j]
						}
						dba[i*9+j] = s
					}
				}
				for i := 0; i < 24; i++ {
					for j := 0; j < 24; j++ {
						s := 0.0
						for k := 0; k < 6; k++ {
							s += bu[k*24+i] * dbu[k*24+j]
						}
						kuu[i*24+j] += w * s
					}
					for j := 0; j < 9; j++ {
						s := 0.0
						for k := 0; k < 6; k++ {
							s += bu[k*24+i] * dba[k*9+j]
						}
						kua[i*9+j] += w * s
					}
				}
				for i := 0; i < 9; i++ {
					for j := 0; j < 9; j++ {
						s := 0.0
						for k := 0; k < 6; k++ {
							s += ba[k*9+i] * dba[k*9+j]
						}
						kaa[i*9+j] += w * s
					}
				}
			}
		}
	}
	// G = -Kaa^-1 Kau (9x24) ; K = Kuu + Kua G
	kaaInv := invert(kaa[:], 9)
	var G [9 * 24]float64
	for i := 0; i < 9; i++ {
		for j := 0; j < 24; j++ {
			s := 0.0
			for k := 0; k < 9; k++ {
				s += kaaInv[i*9+k] * kua[j*9+k] // Kau[k][j] = Kua[j][k]
			}
			G[i*24+j] = -s
		}
	}
	for i := 0; i < 24; i++ {
		for j := 0; j < 24; j++ {
			s := kuu[i*24+j]
			for k := 0; k < 9; k++ {
				s += kua[i*9+k] * G[k*24+j]
			}
			em.K0[i*24+j] = s
		}
	}
	for i := 0; i < 24; i++ { // symétrisation (bruit d'arrondi)
		for j := i + 1; j < 24; j++ {
			v := 0.5 * (em.K0[i*24+j] + em.K0[j*24+i])
			em.K0[i*24+j], em.K0[j*24+i] = v, v
		}
	}
	for n := 0; n < 8; n++ { // déformation aux nœuds : (Bu + Ba G) u
		s := nodeSign[n]
		bu, ba := bMatrix(s[0], s[1], s[2])
		for i := 0; i < 6; i++ {
			for j := 0; j < 24; j++ {
				v := bu[i*24+j]
				for k := 0; k < 9; k++ {
					v += ba[i*9+k] * G[k*24+j]
				}
				em.B[n][i*24+j] = v
			}
		}
	}
	return em
}

// invert inverse une matrice n×n dense (Gauss-Jordan avec pivot partiel).
func invert(a []float64, n int) []float64 {
	m := make([]float64, n*2*n)
	for i := 0; i < n; i++ {
		copy(m[i*2*n:], a[i*n:(i+1)*n])
		m[i*2*n+n+i] = 1
	}
	for c := 0; c < n; c++ {
		p := c
		for r := c + 1; r < n; r++ {
			if math.Abs(m[r*2*n+c]) > math.Abs(m[p*2*n+c]) {
				p = r
			}
		}
		if p != c {
			for k := 0; k < 2*n; k++ {
				m[c*2*n+k], m[p*2*n+k] = m[p*2*n+k], m[c*2*n+k]
			}
		}
		d := m[c*2*n+c]
		for k := 0; k < 2*n; k++ {
			m[c*2*n+k] /= d
		}
		for r := 0; r < n; r++ {
			if r == c {
				continue
			}
			f := m[r*2*n+c]
			if f == 0 {
				continue
			}
			for k := 0; k < 2*n; k++ {
				m[r*2*n+k] -= f * m[c*2*n+k]
			}
		}
	}
	out := make([]float64, n*n)
	for i := 0; i < n; i++ {
		copy(out[i*n:], m[i*2*n+n:(i+1)*2*n])
	}
	return out
}
