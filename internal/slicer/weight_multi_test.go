package slicer

import (
	"archive/zip"
	"bytes"
	"math"
	"testing"
)

// Plusieurs filaments : l'en-tête du G-code liste un poids par filament (« 12.98,13.22 ») ; le poids de la plaque
// de slice_info.config est leur somme. Lire seulement la première valeur faisait rejeter toute archive multicolore.
func TestScanGcodeSumsPerFilamentWeights(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("Metadata/plate_1.gcode")
	_, _ = w.Write([]byte("; HEADER_BLOCK_START\n; total layer number: 3\n; total filament weight [g] : 12.98,13.22\n; HEADER_BLOCK_END\nG1 X1 Y1 E1\n"))
	_ = zw.Close()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	sc, err := scanGcode(zr.File[0])
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(sc.headerWeight-26.2) > 1e-9 {
		t.Fatalf("poids de l'en-tête : %v (26.2 attendu)", sc.headerWeight)
	}
}
