package printing_test

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// Lecture réelle d'une plaque : début, recherche (G29 / M622 / « ; leveling »), bornes, aucun secret.
func TestReadGcodeExcerptReadsPlateWithGrepAndLimits(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	f, _ := zw.Create("Metadata/plate_1.gcode")
	f.Write([]byte("; HEADER_BLOCK_START\n; BambuStudio 02.00.00.00\nM73 P0 R10\nG28\n; leveling\nG29 A1 X10 Y10\nM622 J1\nG1 X10 Y10\naccess_code = 12345678\nG29.2 S1\nG1 X20\n" + strings.Repeat("G1 X1 Y1 E0.1\n", 50) + "M622.1 S1\n"))
	other, _ := zw.Create("Metadata/project_settings.config")
	other.Write([]byte("access_code = JAMAIS-EXPOSE\n"))
	zw.Close()
	sha, _, err := r.c.Blobs.Put(bytes.NewReader(zb.Bytes()), "")
	if err != nil {
		t.Fatal(err)
	}
	o := r.call("read_gcode_excerpt", map[string]any{"artifact_id": sha, "head_lines": 3, "grep": `G29|M622|(?i); leveling`, "max_lines": 10})
	head := o["head"].([]any)
	if len(head) != 3 || head[0].(map[string]any)["text"] != "; HEADER_BLOCK_START" || head[0].(map[string]any)["n"].(float64) != 1 {
		t.Fatalf("début : %v", head)
	}
	if o["match_count"].(float64) != 5 { // leveling, G29, M622 J1, G29.2, M622.1
		t.Fatalf("correspondances : %v", o["match_count"])
	}
	var got []string
	for _, m := range o["matches"].([]any) {
		got = append(got, m.(map[string]any)["text"].(string))
	}
	if !contains(got, "G29 A1 X10 Y10") || !contains(got, "M622 J1") || !contains(got, "; leveling") {
		t.Fatalf("lignes trouvées : %v", got)
	}
	if o["entry"] != "Metadata/plate_1.gcode" {
		t.Fatalf("entrée lue : %v", o["entry"])
	}
	// Borne max_lines : 3 lignes en tout, le reste est annoncé tronqué.
	o2 := r.call("read_gcode_excerpt", map[string]any{"artifact_id": sha, "grep": "G1 ", "max_lines": 3})
	if len(o2["matches"].([]any)) != 3 || o2["truncated"] != true {
		t.Fatalf("borne : %v", o2)
	}
	// Un secret éventuel du G-code est masqué ; le reste de l'archive n'est jamais lu.
	o3 := r.call("read_gcode_excerpt", map[string]any{"artifact_id": sha, "grep": "access_code"})
	txt := o3["matches"].([]any)[0].(map[string]any)["text"].(string)
	if strings.Contains(txt, "12345678") || !strings.Contains(txt, "masquée") {
		t.Fatalf("ligne secrète non masquée : %q", txt)
	}
	if _, txtAll, _ := r.raw("read_gcode_excerpt", map[string]any{"artifact_id": sha, "grep": "JAMAIS"}); strings.Contains(txtAll, "JAMAIS-EXPOSE") {
		t.Fatal("une autre entrée de l'archive a été lue")
	}
	// Plaque absente.
	if txt := r.callErr("read_gcode_excerpt", map[string]any{"artifact_id": sha, "plate": 2}); !strings.Contains(txt, "plate_2.gcode") {
		t.Fatalf("plaque absente : %s", txt)
	}
}
