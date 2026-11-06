package mesh

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// vertexIndexer déduplique exactement les sommets (clé float32 : précision STL).
type vertexIndexer struct {
	m    *Mesh
	seen map[[3]float32]int
}

func newIndexer() *vertexIndexer {
	return &vertexIndexer{m: &Mesh{}, seen: map[[3]float32]int{}}
}

func (x *vertexIndexer) add(v Vec3) int {
	k := [3]float32{float32(v.X), float32(v.Y), float32(v.Z)}
	if i, ok := x.seen[k]; ok {
		return i
	}
	i := len(x.m.V)
	x.m.V = append(x.m.V, v)
	x.seen[k] = i
	return i
}

// IsBinarySTL détecte un STL binaire : la taille doit correspondre exactement
// à 84 + 50·n octets (méthode robuste ; l'en-tête "solid" ne suffit pas, certains
// exporteurs l'écrivent dans des fichiers binaires).
func isBinarySTL(data []byte) (binary_ bool, n uint32) {
	if len(data) < 84 {
		return false, 0
	}
	n = binary.LittleEndian.Uint32(data[80:84])
	return uint64(len(data)) == 84+50*uint64(n), n
}

// ParseSTL lit un STL ASCII ou binaire.
func ParseSTL(data []byte) (*Model, error) {
	md := &Model{Format: "stl", Unit: "unknown", UnitAssumed: true, ScaleToMM: 1}
	md.Warnings = append(md.Warnings, "STL sans unité : valeurs interprétées comme des millimètres (hypothèse à confirmer)")
	var m *Mesh
	var err error
	if bin, n := isBinarySTL(data); bin {
		m, err = parseSTLBinary(data, n)
	} else if looksASCII(data) {
		m, err = parseSTLASCII(data, md)
	} else if len(data) >= 84 {
		n := binary.LittleEndian.Uint32(data[80:84])
		if printable(data[80:84]) {
			return nil, fmt.Errorf("%w : ni STL ASCII ni binaire (le compteur de triangles est du texte)", ErrFormat)
		}
		if uint64(len(data)) < 84+50*uint64(n) {
			return nil, fmt.Errorf("%w : STL binaire annonce %d triangles mais ne contient que %d octets (attendu %d)", ErrTruncated, n, len(data), 84+50*uint64(n))
		}
		// Octets en trop après les triangles : on tolère en ignorant la fin.
		md.Warnings = append(md.Warnings, "STL binaire : octets en trop après les triangles, ignorés")
		m, err = parseSTLBinary(data[:84+50*uint64(n)], n)
	} else {
		return nil, fmt.Errorf("%w : ni STL ASCII ni binaire", ErrFormat)
	}
	if err != nil {
		return nil, err
	}
	if len(m.T) == 0 {
		return nil, ErrEmpty
	}
	md.Objects = []Object{{ID: "1", Name: "stl", Mesh: m}}
	if err := validateModel(md); err != nil {
		return nil, err
	}
	return md, nil
}

func looksASCII(data []byte) bool {
	head := data
	if len(head) > 4096 {
		head = head[:4096]
	}
	t := bytes.TrimLeft(head, "\xef\xbb\xbf \t\r\n")
	if !bytes.HasPrefix(bytes.ToLower(t[:min(len(t), 5)]), []byte("solid")) {
		return false
	}
	// Le mot "facet" ou "endsolid" doit apparaître dans le texte ; les octets non ASCII dans la tête indiquent du binaire.
	// Recherche insensible à la casse SANS copier le fichier entier.
	if !containsFold(data, "facet") && !containsFold(data, "endsolid") {
		return false
	}
	for _, b := range head {
		if b == 0 {
			return false
		}
	}
	return true
}

// containsFold cherche pat (en minuscules ASCII) dans data sans tenir compte de la casse,
// sans allocation.
func containsFold(data []byte, pat string) bool {
	n := len(pat)
	for i := 0; i+n <= len(data); i++ {
		if data[i]|0x20 != pat[0] {
			continue
		}
		ok := true
		for j := 1; j < n; j++ {
			if data[i+j]|0x20 != pat[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func parseSTLBinary(data []byte, n uint32) (*Mesh, error) {
	if n > MaxTriangles {
		return nil, fmt.Errorf("%w : %d triangles dépasse la limite %d", ErrFormat, n, MaxTriangles)
	}
	idx := newIndexer()
	idx.m.T = make([]Tri, 0, n)
	p := data[84:]
	f := func(b []byte) float64 { return float64(math.Float32frombits(binary.LittleEndian.Uint32(b))) }
	for i := uint32(0); i < n; i++ {
		rec := p[i*50 : i*50+50]
		var t Tri
		for k := 0; k < 3; k++ {
			o := 12 + 12*k
			v := Vec3{f(rec[o:]), f(rec[o+4:]), f(rec[o+8:])}
			if math.IsNaN(v.X+v.Y+v.Z) || math.IsInf(v.X+v.Y+v.Z, 0) {
				return nil, fmt.Errorf("%w : coordonnée non finie au triangle %d", ErrFormat, i)
			}
			t[k] = idx.add(v)
		}
		idx.m.T = append(idx.m.T, t)
	}
	return idx.m, nil
}

func parseSTLASCII(data []byte, md *Model) (*Mesh, error) {
	idx := newIndexer()
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var cur []int
	sawEnd := false
	line := 0
	for sc.Scan() {
		line++
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "vertex":
			if len(fields) < 4 {
				return nil, fmt.Errorf("%w : ligne %d : sommet incomplet", ErrFormat, line)
			}
			var c [3]float64
			for k := 0; k < 3; k++ {
				v, err := strconv.ParseFloat(fields[1+k], 64)
				if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
					return nil, fmt.Errorf("%w : ligne %d : nombre invalide %q", ErrFormat, line, fields[1+k])
				}
				c[k] = v
			}
			cur = append(cur, idx.add(Vec3{c[0], c[1], c[2]}))
		case "endfacet":
			if len(cur) != 3 {
				return nil, fmt.Errorf("%w : ligne %d : facette avec %d sommets", ErrTruncated, line, len(cur))
			}
			idx.m.T = append(idx.m.T, Tri{cur[0], cur[1], cur[2]})
			cur = cur[:0]
			if len(idx.m.T) > MaxTriangles {
				return nil, fmt.Errorf("%w : trop de triangles", ErrFormat)
			}
		case "endsolid":
			sawEnd = true
		}
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		return nil, fmt.Errorf("%w : %v", ErrFormat, err)
	}
	if len(cur) != 0 {
		return nil, fmt.Errorf("%w : facette incomplète en fin de fichier", ErrTruncated)
	}
	if !sawEnd {
		md.Warnings = append(md.Warnings, "STL ASCII sans 'endsolid' : fichier possiblement tronqué")
	}
	return idx.m, nil
}

// printable vrai si tous les octets sont des caractères ASCII imprimables ou des espaces.
func printable(b []byte) bool {
	for _, c := range b {
		if !(c == 9 || c == 10 || c == 13 || (c >= 0x20 && c < 0x7f)) {
			return false
		}
	}
	return true
}
