// Package mesh lit (STL, OBJ, 3MF), analyse et exporte (GLB, STL) des maillages
// triangulaires, en Go pur. Les unités internes sont des millimètres.
package mesh

import (
	"errors"
	"fmt"
	"math"
)

// Vec3 est un vecteur 3D en float64.
type Vec3 struct{ X, Y, Z float64 }

func (a Vec3) Add(b Vec3) Vec3      { return Vec3{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }
func (a Vec3) Sub(b Vec3) Vec3      { return Vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func (a Vec3) Scale(s float64) Vec3 { return Vec3{a.X * s, a.Y * s, a.Z * s} }
func (a Vec3) Dot(b Vec3) float64   { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }
func (a Vec3) Cross(b Vec3) Vec3 {
	return Vec3{a.Y*b.Z - a.Z*b.Y, a.Z*b.X - a.X*b.Z, a.X*b.Y - a.Y*b.X}
}
func (a Vec3) Len() float64 { return math.Sqrt(a.Dot(a)) }

// Norm retourne le vecteur unitaire (zéro si nul).
func (a Vec3) Norm() Vec3 {
	l := a.Len()
	if l == 0 {
		return Vec3{}
	}
	return a.Scale(1 / l)
}

// Tri référence trois sommets par index.
type Tri [3]int

// Mesh est un maillage triangulaire indexé. Les sommets peuvent être dupliqués
// (STL) ; voir Weld.
type Mesh struct {
	V []Vec3
	T []Tri
}

// Normal retourne la normale unitaire du triangle i (zéro si dégénéré) et son aire.
func (m *Mesh) Normal(i int) (Vec3, float64) {
	t := m.T[i]
	a, b, c := m.V[t[0]], m.V[t[1]], m.V[t[2]]
	n := b.Sub(a).Cross(c.Sub(a))
	l := n.Len()
	if l == 0 {
		return Vec3{}, 0
	}
	return n.Scale(1 / l), l / 2
}

// BBox est une boîte englobante alignée aux axes.
type BBox struct {
	Min  Vec3 `json:"min"`
	Max  Vec3 `json:"max"`
	Size Vec3 `json:"size"`
}

// Bounds calcule la boîte englobante (zéro si vide).
func (m *Mesh) Bounds() BBox {
	if len(m.V) == 0 {
		return BBox{}
	}
	lo, hi := m.V[0], m.V[0]
	for _, v := range m.V {
		lo = Vec3{math.Min(lo.X, v.X), math.Min(lo.Y, v.Y), math.Min(lo.Z, v.Z)}
		hi = Vec3{math.Max(hi.X, v.X), math.Max(hi.Y, v.Y), math.Max(hi.Z, v.Z)}
	}
	return BBox{Min: lo, Max: hi, Size: hi.Sub(lo)}
}

// Validate vérifie les index et les valeurs finies.
func (m *Mesh) Validate() error {
	for i, v := range m.V {
		if math.IsNaN(v.X+v.Y+v.Z) || math.IsInf(v.X+v.Y+v.Z, 0) {
			return fmt.Errorf("sommet %d non fini", i)
		}
	}
	for i, t := range m.T {
		for _, k := range t {
			if k < 0 || k >= len(m.V) {
				return fmt.Errorf("triangle %d : index de sommet %d hors limites", i, k)
			}
		}
	}
	return nil
}

// Clone copie le maillage.
func (m *Mesh) Clone() *Mesh {
	return &Mesh{V: append([]Vec3(nil), m.V...), T: append([]Tri(nil), m.T...)}
}

// Transform applique une matrice affine 3x4 (convention 3MF : ligne-vecteur,
// x' = x·m00 + y·m10 + z·m20 + m30) et retourne un nouveau maillage. Si le
// déterminant est négatif, l'orientation des triangles est inversée pour que
// les normales restent sortantes.
func (m *Mesh) Transform(t Matrix) *Mesh {
	out := &Mesh{V: make([]Vec3, len(m.V)), T: append([]Tri(nil), m.T...)}
	for i, v := range m.V {
		out.V[i] = t.Apply(v)
	}
	if t.Det() < 0 {
		for i := range out.T {
			out.T[i][1], out.T[i][2] = out.T[i][2], out.T[i][1]
		}
	}
	return out
}

// Append ajoute un autre maillage.
func (m *Mesh) Append(o *Mesh) {
	off := len(m.V)
	m.V = append(m.V, o.V...)
	for _, t := range o.T {
		m.T = append(m.T, Tri{t[0] + off, t[1] + off, t[2] + off})
	}
}

// Matrix est une transformation affine 3x4 en convention 3MF (12 nombres,
// ligne-vecteur) : M[0..2] = ligne x, M[3..5] = ligne y, M[6..8] = ligne z, M[9..11] = translation.
type Matrix [12]float64

// Identity est la transformation neutre.
var Identity = Matrix{1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0}

// Apply transforme un point.
func (m Matrix) Apply(v Vec3) Vec3 {
	return Vec3{
		v.X*m[0] + v.Y*m[3] + v.Z*m[6] + m[9],
		v.X*m[1] + v.Y*m[4] + v.Z*m[7] + m[10],
		v.X*m[2] + v.Y*m[5] + v.Z*m[8] + m[11],
	}
}

// Mul retourne la composition "a puis b" : v·a·b.
func (a Matrix) Mul(b Matrix) Matrix {
	var r Matrix
	// Partie linéaire 3x3 : (ligne i de a) · b
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			r[i*3+j] = a[i*3+0]*b[0*3+j] + a[i*3+1]*b[1*3+j] + a[i*3+2]*b[2*3+j]
		}
	}
	// Translation : t_a · B + t_b
	for j := 0; j < 3; j++ {
		r[9+j] = a[9]*b[0*3+j] + a[10]*b[1*3+j] + a[11]*b[2*3+j] + b[9+j]
	}
	return r
}

// Det retourne le déterminant de la partie linéaire.
func (m Matrix) Det() float64 {
	return m[0]*(m[4]*m[8]-m[5]*m[7]) - m[1]*(m[3]*m[8]-m[5]*m[6]) + m[2]*(m[3]*m[7]-m[4]*m[6])
}

// Scaling retourne une homothétie uniforme.
func Scaling(s float64) Matrix { return Matrix{s, 0, 0, 0, s, 0, 0, 0, s, 0, 0, 0} }

// Translation retourne une translation.
func Translation(x, y, z float64) Matrix { return Matrix{1, 0, 0, 0, 1, 0, 0, 0, 1, x, y, z} }

// Object est un objet nommé d'un modèle, déjà exprimé en millimètres et en
// coordonnées monde (transformations de composants et d'items de build appliquées).
type Object struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type,omitempty"` // model|support|solidsupport|other (3MF)
	Mesh     *Mesh             `json:"-"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Model est le résultat de la lecture d'un fichier.
type Model struct {
	Format string `json:"format"` // stl|obj|3mf
	// Unit : unité déclarée par le fichier ("millimeter" pour 3MF par défaut ; "unknown" pour STL/OBJ).
	Unit string `json:"unit"`
	// UnitAssumed vrai quand le fichier ne porte pas d'unité : les valeurs sont interprétées comme des mm.
	UnitAssumed bool `json:"unit_assumed"`
	// ScaleToMM : facteur déjà appliqué aux coordonnées pour obtenir des mm.
	ScaleToMM float64           `json:"scale_to_mm"`
	Objects   []Object          `json:"objects"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	// Extras liste les fichiers du conteneur non interprétés mais conservés dans l'original
	// (ex. Metadata/*.config Bambu, miniatures, plaques).
	Extras   []string `json:"extras,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// Combined fusionne tous les objets en un seul maillage.
func (md *Model) Combined() *Mesh {
	out := &Mesh{}
	for _, o := range md.Objects {
		if o.Mesh != nil {
			out.Append(o.Mesh)
		}
	}
	return out
}

// Erreurs de lecture.
var (
	ErrFormat    = errors.New("format de fichier non reconnu")
	ErrTruncated = errors.New("fichier tronqué")
	ErrEmpty     = errors.New("aucune géométrie")
)

// Limites de sécurité.
const (
	MaxTriangles     = 20_000_000
	maxZipEntryBytes = 1 << 30 // 1 GiB décompressé par entrée
	maxZipTotalBytes = 2 << 30
	maxZipEntries    = 10000
	maxXMLTokenDepth = 64
)
