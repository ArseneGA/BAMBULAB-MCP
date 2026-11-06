package slicer

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Faux Bambu Studio : le binaire de test se relance lui-même en mode
// FAKE_STUDIO=<mode>. Permet de tester le lancement, le timeout, l'arrêt de
// l'arbre de processus, les codes retour et la validation sans Studio.

func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_STUDIO"); mode != "" {
		os.Exit(fakeStudio(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func fakeStudio(mode string, args []string) int {
	has := false
	for _, a := range args {
		if a == "--slice" {
			has = true
		}
	}
	if !has {
		fmt.Println("BambuStudio-02.08.02.61")
		return 0
	}
	outdir := argAfter(args, "--outputdir")
	_ = os.MkdirAll(outdir, 0o755)
	writeResult := func(code int, msg string) {
		_ = os.WriteFile(filepath.Join(outdir, "result.json"),
			[]byte(fmt.Sprintf(`{"return_code": %d, "error_string": %q, "sliced_plates": [{"id":1,"filaments":[{"id":1,"filament_id":"GFA00","total_used_g":1.0,"main_used_g":1.0}]}]}`, code, msg)), 0o644)
	}
	switch {
	case mode == "ok" || mode == "badmd5" || mode == "wrongmachine":
		opts := archiveOpts{}
		if mode == "badmd5" {
			opts.badMD5 = true
		}
		if mode == "wrongmachine" {
			opts.printerModel = "Bambu Lab A1 mini"
		}
		b := buildArchive(opts)
		_ = os.WriteFile(filepath.Join(outdir, argAfter(args, "--export-3mf")), b, 0o644)
		writeResult(0, "Success.")
		return 0
	case strings.HasPrefix(mode, "err:"):
		var code int
		fmt.Sscanf(mode, "err:%d", &code)
		writeResult(code, "échec simulé")
		return code & 0xff
	case mode == "noresult":
		return 0
	case mode == "garbage":
		_ = os.WriteFile(filepath.Join(outdir, argAfter(args, "--export-3mf")), []byte("pas un zip"), 0o644)
		writeResult(0, "Success.")
		return 0
	case mode == "spam":
		chunk := bytes.Repeat([]byte("x"), 1<<20)
		for i := 0; i < 5; i++ {
			os.Stdout.Write(chunk)
			os.Stderr.Write(chunk)
		}
		return 3
	case mode == "hang":
		// enfant qui survivrait à un kill du seul parent
		child := exec.Command(os.Args[0], "--slice")
		child.Env = append(os.Environ(), "FAKE_STUDIO=hangchild")
		_ = child.Start()
		_ = os.WriteFile(filepath.Join(outdir, "child.pid"), []byte(fmt.Sprint(child.Process.Pid)), 0o644)
		time.Sleep(time.Hour)
		return 0
	case mode == "hangchild":
		time.Sleep(time.Hour)
		return 0
	}
	return 99
}

// ---- construction d'archives .gcode.3mf minimales ----

type archiveOpts struct {
	badMD5       bool
	noGcode      bool
	noMD5        bool
	printerModel string
	machine      string
	process      string
	filaments    []string
	nozzle       string
	gcodeExtra   string // lignes ajoutées au corps
	weightG      string
	layers       int
	skipSlice    bool
}

func buildArchive(o archiveOpts) []byte {
	if o.printerModel == "" {
		o.printerModel = "Test T1"
	}
	if o.machine == "" {
		o.machine = "Test T1 0.4 nozzle"
	}
	if o.process == "" {
		o.process = "0.20mm Std @TST"
	}
	if o.filaments == nil {
		o.filaments = []string{"Test PLA"}
	}
	if o.nozzle == "" {
		o.nozzle = "0.4"
	}
	if o.weightG == "" {
		o.weightG = "1.00"
	}
	if o.layers == 0 {
		o.layers = 2
	}
	var g strings.Builder
	g.WriteString("; HEADER_BLOCK_START\n; BambuStudio 02.08.02.61\n")
	g.WriteString("; model printing time: 1m 0s; total estimated time: 1m 30s\n")
	fmt.Fprintf(&g, "; total layer number: %d\n; total filament weight [g] : %s\n; max_z_height: %.2f\n; HEADER_BLOCK_END\n", o.layers, o.weightG, float64(o.layers)*0.2)
	g.WriteString("; CONFIG_BLOCK_START\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&g, "; some_key_%d = some value to pad the file\n", i)
	}
	g.WriteString("; CONFIG_BLOCK_END\nG1 X5 Y200 Z0.3 E5 ; purge de démarrage hors zone, ignorée\n")
	for l := 1; l <= o.layers; l++ {
		fmt.Fprintf(&g, "; CHANGE_LAYER\n; Z_HEIGHT: %.1f\nG1 Z%.1f\nG1 X%d Y%d E0.5\n", float64(l)*0.2, float64(l)*0.2, 50+l, 50+l)
	}
	g.WriteString(o.gcodeExtra)
	gcode := []byte(g.String())
	sum := md5.Sum(gcode)
	md5hex := hex.EncodeToString(sum[:])
	if o.badMD5 {
		md5hex = strings.Repeat("0", 32)
	}

	si := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<config>
  <header><header_item key="X-BBL-Client-Type" value="slicer"/><header_item key="X-BBL-Client-Version" value="02.08.02.61"/></header>
  <plate>
    <metadata key="index" value="1"/>
    <metadata key="nozzle_diameters" value="%s"/>
    <metadata key="prediction" value="90"/>
    <metadata key="weight" value="%s"/>
    <metadata key="support_used" value="false"/>
    <object identify_id="15" name="cube.stl" skipped="false" />
    <filament id="1" tray_info_idx="GFA00" type="PLA" color="#00AE42" used_m="0.33" used_g="%s" nozzle_diameter="%s"/>
  </plate>
</config>`, o.nozzle, o.weightG, o.weightG, o.nozzle)
	if o.skipSlice {
		si = `<?xml version="1.0"?><config><header/></config>`
	}
	fil := "["
	for i, f := range o.filaments {
		if i > 0 {
			fil += ","
		}
		fil += fmt.Sprintf("%q", f)
	}
	fil += "]"
	ps := fmt.Sprintf(`{"printer_model": %q, "printer_settings_id": %q, "print_settings_id": %q, "filament_settings_id": %s, "nozzle_diameter": [%q], "curr_bed_type": "Smooth Plate"}`,
		o.printerModel, o.machine, o.process, fil, o.nozzle)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	put := func(name string, data []byte) {
		w, _ := zw.Create(name)
		w.Write(data)
	}
	put("[Content_Types].xml", []byte(`<Types/>`))
	put("3D/3dmodel.model", []byte(`<model/>`))
	put("Metadata/slice_info.config", []byte(si))
	put("Metadata/project_settings.config", []byte(ps))
	if !o.noGcode {
		put("Metadata/plate_1.gcode", gcode)
		if !o.noMD5 {
			put("Metadata/plate_1.gcode.md5", []byte(md5hex))
		}
	}
	zw.Close()
	return buf.Bytes()
}

func writeTemp(t *testing.T, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
