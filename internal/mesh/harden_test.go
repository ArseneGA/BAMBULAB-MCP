package mesh

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// explosiveComponents : 6 niveaux, chacun référençant le suivant 60 fois (60^6 feuilles
// si rien ne borne le travail) ; la feuille est un cube.
func explosiveComponents(levels, fanout int) string {
	var b strings.Builder
	b.WriteString(`<model unit="millimeter"><resources>`)
	fmt.Fprintf(&b, `<object id="%d">%s</object>`, levels, cubeXML(1))
	for l := levels - 1; l >= 1; l-- {
		fmt.Fprintf(&b, `<object id="%d"><components>`, l)
		for i := 0; i < fanout; i++ {
			fmt.Fprintf(&b, `<component objectid="%d"/>`, l+1)
		}
		b.WriteString(`</components></object>`)
	}
	b.WriteString(`</resources><build><item objectid="1"/></build></model>`)
	return b.String()
}

func TestThreeMFExplosiveComponentsFailFast(t *testing.T) {
	data := zipOf(t, map[string]string{"3D/3dmodel.model": explosiveComponents(6, 60)})
	done := make(chan error, 1)
	go func() { _, err := ParseThreeMF(data); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrFormat) {
			t.Fatalf("attendu ErrFormat, obtenu %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("l'explosion de composants n'est pas bornée (trop lent)")
	}
}

func TestThreeMFSharedComponentsAreMemoized(t *testing.T) {
	// 40 composants du même cube : légitime, résolu une seule fois.
	var b strings.Builder
	b.WriteString(`<model unit="millimeter"><resources><object id="1">` + cubeXML(1) + `</object><object id="2"><components>`)
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, `<component objectid="1" transform="1 0 0 0 1 0 0 0 1 %d 0 0"/>`, i*2)
	}
	b.WriteString(`</components></object></resources><build><item objectid="2"/></build></model>`)
	md, err := ParseThreeMF(zipOf(t, map[string]string{"3D/3dmodel.model": b.String()}))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(md.Objects[0].Mesh.T); got != 40*12 {
		t.Fatalf("triangles %d", got)
	}
}

func TestThreeMFZipBombEntry(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("3D/3dmodel.model")
	chunk := make([]byte, 1<<20)
	for i := 0; i < 130; i++ { // 130 Mio de zéros : se compresse très bien
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	zw.Close()
	start := time.Now()
	if _, err := ParseThreeMF(buf.Bytes()); !errors.Is(err, ErrFormat) {
		t.Fatalf("attendu ErrFormat : %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("rejet trop lent : l'entrée a été décompressée")
	}
}

func TestThreeMFRejectsDuplicateEntries(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i < 2; i++ {
		w, _ := zw.Create("3D/3dmodel.model")
		w.Write([]byte(`<model/>`))
	}
	zw.Close()
	if _, err := ParseThreeMF(buf.Bytes()); !errors.Is(err, ErrFormat) || !strings.Contains(err.Error(), "dupliqu") {
		t.Fatalf("entrées dupliquées : %v", err)
	}
}

func TestThreeMFRejectsNonFinite(t *testing.T) {
	res := func(vx, transform string) string {
		return `<model><resources><object id="1"><mesh><vertices><vertex x="` + vx + `" y="0" z="0"/><vertex x="1" y="0" z="0"/><vertex x="0" y="1" z="0"/></vertices><triangles><triangle v1="0" v2="1" v3="2"/></triangles></mesh></object></resources><build><item objectid="1" transform="` + transform + `"/></build></model>`
	}
	for name, model := range map[string]string{
		"sommet NaN":         res("NaN", ""),
		"sommet Inf":         res("Inf", ""),
		"transformation NaN": res("0", "1 0 0 0 1 0 0 0 1 NaN 0 0"),
		"transformation Inf": res("0", "1 0 0 0 1 0 0 0 1 0 +Inf 0"),
		"débordement":        res("1e300", "1e300 0 0 0 1 0 0 0 1 0 0 0"),
	} {
		if _, err := ParseThreeMF(zipOf(t, map[string]string{"3D/3dmodel.model": model})); !errors.Is(err, ErrFormat) {
			t.Errorf("%s : %v", name, err)
		}
	}
}

func TestEntryNameNormalization(t *testing.T) {
	for _, bad := range []string{"../evil.model", "a/../../b", `..\x`, "/abs/x.model", "C:/x.model", "a/..", "x\x00y"} {
		if _, err := safeEntryName(bad); err == nil {
			t.Errorf("%q accepté", bad)
		}
	}
	for good, want := range map[string]string{"3D/a..b.model": "3D/a..b.model", `3D\3dmodel.model`: "3D/3dmodel.model", "./3D/x.model": "3D/x.model"} {
		got, err := safeEntryName(good)
		if err != nil || got != want {
			t.Errorf("%q -> %q, %v", good, got, err)
		}
	}
	// Un nom légitime contenant « .. » dans un segment est lu.
	data := zipOf(t, map[string]string{"3D/mod..el.model": `<model unit="millimeter"><resources><object id="1">` + cubeXML(1) + `</object></resources><build><item objectid="1"/></build></model>`})
	if _, err := ParseThreeMF(data); err != nil {
		t.Errorf("nom légitime refusé : %v", err)
	}
}

func TestParsersValidateMeshes(t *testing.T) {
	// Un STL ASCII avec un sommet non fini est refusé dès la lecture.
	if _, err := ParseSTL([]byte("solid x\nfacet normal 0 0 1\nouter loop\nvertex NaN 0 0\nvertex 1 0 0\nvertex 0 1 0\nendloop\nendfacet\nendsolid")); err == nil {
		t.Error("STL NaN accepté")
	}
	if _, err := ParseOBJ([]byte("v 1e999 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 3\n")); err == nil {
		t.Error("OBJ Inf accepté")
	}
}

func TestOBJTotalTriangleCap(t *testing.T) {
	// Plusieurs groupes : chacun sous le plafond, mais le total le dépasse.
	var b strings.Builder
	b.WriteString("v 0 0 0\nv 1 0 0\nv 1 1 0\n")
	per := MaxTriangles/3 + 10
	for g := 0; g < 3; g++ {
		fmt.Fprintf(&b, "o g%d\n", g)
		b.WriteString(strings.Repeat("f 1 2 3\n", per))
	}
	if _, err := ParseOBJ([]byte(b.String())); !errors.Is(err, ErrFormat) {
		t.Fatalf("plafond global non appliqué : %v", err)
	}
}

func TestLooksASCIIWithoutCopy(t *testing.T) {
	data := append([]byte("solid x\n"), bytes.Repeat([]byte("  \n"), 1<<20)...)
	data = append(data, []byte("FaCeT normal 0 0 0")...)
	allocs := testing.AllocsPerRun(3, func() { _ = looksASCII(data) })
	if allocs > 2 {
		t.Fatalf("looksASCII alloue %v fois", allocs)
	}
	if !looksASCII(data) {
		t.Fatal("STL ASCII non reconnu")
	}
	if looksASCII([]byte("solid x\n" + strings.Repeat("a", 100))) {
		t.Fatal("faux positif")
	}
}

func TestReadFileTooLarge(t *testing.T) {
	// Un fichier sparse/gros n'est pas lu : refus explicite par taille.
	p := filepath.Join(t.TempDir(), "big.stl")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxFileBytes + 1); err != nil {
		t.Skip("fichier creux non supporté : ", err)
	}
	f.Close()
	if _, err := ReadFile(p); !errors.Is(err, ErrFormat) {
		t.Fatalf("attendu ErrFormat : %v", err)
	}
}

func TestBVHBoundedLeavesWithIdenticalCentroids(t *testing.T) {
	m := &Mesh{V: []Vec3{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}}
	for i := 0; i < 1000; i++ {
		m.T = append(m.T, Tri{0, 1, 2})
	}
	b := buildBVH(m)
	for i, n := range b.nodes {
		if n.left < 0 && n.n > bvhLeaf {
			t.Fatalf("feuille %d de %d triangles", i, n.n)
		}
	}
}
