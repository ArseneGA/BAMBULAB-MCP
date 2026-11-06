package agentlinktest

import (
	"archive/zip"
	"bytes"
	"math/rand"
)

// FakeGcode3MF construit un .gcode.3mf minimal VALIDE pour le contrôle de format du serveur (archive ZIP avec
// [Content_Types].xml et un fichier .gcode) dont le contenu G-code fait environ pad octets (déterministe selon seed).
func FakeGcode3MF(pad int, seed int64) []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, _ := zw.Create("[Content_Types].xml")
	_, _ = w.Write([]byte(`<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"></Types>`))
	g, _ := zw.CreateHeader(&zip.FileHeader{Name: "Metadata/plate_1.gcode", Method: zip.Store})
	rnd := rand.New(rand.NewSource(seed))
	buf := make([]byte, pad)
	for i := range buf {
		buf[i] = byte('a' + rnd.Intn(26))
	}
	_, _ = g.Write([]byte("; G-code de test\n"))
	_, _ = g.Write(buf)
	_ = zw.Close()
	return b.Bytes()
}

// FakeModel3MF construit un .3mf minimal valide (modèle 3D vide).
func FakeModel3MF() []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, _ := zw.Create("[Content_Types].xml")
	_, _ = w.Write([]byte(`<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"></Types>`))
	m, _ := zw.Create("3D/3dmodel.model")
	_, _ = m.Write([]byte(`<model unit="millimeter"/>`))
	_ = zw.Close()
	return b.Bytes()
}
