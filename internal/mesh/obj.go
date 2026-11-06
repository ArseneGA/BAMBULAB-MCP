package mesh

import (
	"bufio"
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParseOBJ lit un Wavefront OBJ (v, f ; o/g pour les noms). Les polygones sont
// triangulés en éventail (convexes) ; pour un polygone concave, l'éventail peut
// être incorrect (signalé). Indices négatifs relatifs supportés. vt/vn ignorés.
// Plusieurs objets/groupes (o) donnent plusieurs Objects.
func ParseOBJ(data []byte) (*Model, error) {
	md := &Model{Format: "obj", Unit: "unknown", UnitAssumed: true, ScaleToMM: 1}
	md.Warnings = append(md.Warnings, "OBJ sans unité : valeurs interprétées comme des millimètres (hypothèse à confirmer)")
	var verts []Vec3
	type group struct {
		name string
		tris []Tri
	}
	groups := []*group{{name: "obj"}}
	cur := groups[0]
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	line := 0
	totalTris := 0
	concave := false
	for sc.Scan() {
		line++
		s := strings.TrimSpace(sc.Text())
		if s == "" || s[0] == '#' {
			continue
		}
		f := strings.Fields(s)
		switch f[0] {
		case "v":
			if len(f) < 4 {
				return nil, fmt.Errorf("%w : ligne %d : sommet incomplet", ErrFormat, line)
			}
			var c [3]float64
			for k := 0; k < 3; k++ {
				v, err := strconv.ParseFloat(f[1+k], 64)
				if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
					return nil, fmt.Errorf("%w : ligne %d : nombre invalide %q", ErrFormat, line, f[1+k])
				}
				c[k] = v
			}
			if len(verts) >= MaxVertices {
				return nil, fmt.Errorf("%w : trop de sommets (limite %d)", ErrFormat, MaxVertices)
			}
			verts = append(verts, Vec3{c[0], c[1], c[2]})
		case "o", "g":
			name := "obj"
			if len(f) > 1 {
				name = strings.Join(f[1:], " ")
			}
			if len(cur.tris) == 0 {
				cur.name = name
			} else {
				cur = &group{name: name}
				groups = append(groups, cur)
			}
		case "f":
			if len(f) < 4 {
				return nil, fmt.Errorf("%w : ligne %d : face de moins de 3 sommets", ErrFormat, line)
			}
			ids := make([]int, 0, len(f)-1)
			for _, tok := range f[1:] {
				p := tok
				if i := strings.IndexByte(p, '/'); i >= 0 {
					p = p[:i]
				}
				k, err := strconv.Atoi(p)
				if err != nil || k == 0 {
					return nil, fmt.Errorf("%w : ligne %d : index de sommet invalide %q", ErrFormat, line, tok)
				}
				if k < 0 {
					k = len(verts) + k + 1
				}
				if k < 1 || k > len(verts) {
					return nil, fmt.Errorf("%w : ligne %d : index %d hors limites (%d sommets lus)", ErrFormat, line, k, len(verts))
				}
				ids = append(ids, k-1)
			}
			if len(ids) > 3 {
				concave = true // détecté ci-dessous seulement si besoin ; on signale la triangulation
			}
			totalTris += len(ids) - 2
			if totalTris > MaxTriangles { // total tous groupes confondus
				return nil, fmt.Errorf("%w : trop de triangles (limite %d)", ErrFormat, MaxTriangles)
			}
			for i := 1; i+1 < len(ids); i++ {
				cur.tris = append(cur.tris, Tri{ids[0], ids[i], ids[i+1]})
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w : %v", ErrFormat, err)
	}
	if concave {
		md.Warnings = append(md.Warnings, "OBJ : polygones triangulés en éventail (exact pour les polygones convexes)")
	}
	n := 0
	for gi, g := range groups {
		if len(g.tris) == 0 {
			continue
		}
		m := compact(&Mesh{V: verts, T: g.tris})
		n++
		md.Objects = append(md.Objects, Object{ID: strconv.Itoa(gi + 1), Name: g.name, Mesh: m})
	}
	if n == 0 {
		return nil, ErrEmpty
	}
	if err := validateModel(md); err != nil {
		return nil, err
	}
	return md, nil
}

// compact retire les sommets inutilisés et renumérote.
func compact(m *Mesh) *Mesh {
	remap := map[int]int{}
	out := &Mesh{T: make([]Tri, 0, len(m.T))}
	for _, t := range m.T {
		var nt Tri
		for k, i := range t {
			j, ok := remap[i]
			if !ok {
				j = len(out.V)
				remap[i] = j
				out.V = append(out.V, m.V[i])
			}
			nt[k] = j
		}
		out.T = append(out.T, nt)
	}
	return out
}
