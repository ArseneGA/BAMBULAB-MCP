package slicer

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

// Limites de lecture d'archive.
const (
	maxEntries      = 10000
	maxGcodeBytes   = 4 << 30
	maxMetaBytes    = 64 << 20
	maxGcodeLineLen = 16 << 20
)

// Check est un contrôle individuel de la validation de sortie.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Level  string `json:"level"` // "error" (bloquant) ou "warn"
	Detail string `json:"detail,omitempty"`
}

// Validation est le rapport de validation d'un .gcode.3mf.
type Validation struct {
	OK     bool    `json:"ok"` // aucun contrôle bloquant en échec
	Checks []Check `json:"checks"`
}

func (v *Validation) add(name string, ok bool, level, format string, a ...any) {
	v.Checks = append(v.Checks, Check{Name: name, OK: ok, Level: level, Detail: fmt.Sprintf(format, a...)})
}

// Failed retourne les contrôles bloquants en échec.
func (v *Validation) Failed() []Check {
	var out []Check
	for _, c := range v.Checks {
		if !c.OK && c.Level == "error" {
			out = append(out, c)
		}
	}
	return out
}

// Expect décrit ce que la sortie doit refléter de la demande.
type Expect struct {
	PrinterModel string // valeur de printer_model du profil machine
	MachineName  string // nom du preset machine
	Nozzle       float64
	ProcessName  string
	Filaments    []string // noms de presets, un par emplacement
	Plate        int      // 0 = toutes les plaques
	BedArea      []profiles.Point
	PrintableZ   float64
	// LayerHeight et FirstLayerHeight (mm) : tolérance du contrôle de hauteur de trajectoire
	// (max(hauteur de couche, première couche, 0,25 mm)).
	LayerHeight, FirstLayerHeight float64
	// Volume du modèle d'entrée (mm³) et nature de l'entrée, pour comparer à
	// la géométrie du plateau ; 0 = pas de comparaison.
	InputVolume float64
	InputIsMesh bool // STL/OBJ : écart bloquant ; 3MF : avertissement
	// BinaryVersion : version du binaire, comparée à celle écrite dans l'archive.
	BinaryVersion string
	Studio        *studioResult
	// Trust : référence de confiance (profils officiels, fiches d'imprimantes) ; défaut DefaultTrust().
	Trust *Trust
}

// FilamentUse est la consommation d'un emplacement filament sur une plaque.
type FilamentUse struct {
	Slot  int    `json:"slot"`
	Type  string `json:"type"`
	Color string `json:"color,omitempty"`
	// ColorSource : d'où vient Color. Vide = couleur du profil de découpe (valeur par défaut du preset, par exemple
	// le vert Bambu #00AE42), qui n'est PAS la couleur de la bobine chargée.
	ColorSource string  `json:"color_source,omitempty"`
	TrayInfo    string  `json:"tray_info_idx,omitempty"`
	Meters      float64 `json:"used_m"`
	Grams       float64 `json:"used_g"`
	Nozzle      float64 `json:"nozzle_diameter,omitempty"`
}

// PlateResult regroupe ce qui est lisible d'une plaque tranchée.
type PlateResult struct {
	Index            int           `json:"index"`
	GcodeEntry       string        `json:"gcode_entry"`
	GcodeBytes       int64         `json:"gcode_bytes"`
	GcodeMD5         string        `json:"gcode_md5"`
	PredictedSeconds float64       `json:"predicted_seconds"`
	EstimatedTime    string        `json:"estimated_time,omitempty"` // « 9m 2s » tel qu'écrit par Studio
	WeightGrams      float64       `json:"weight_grams"`
	Filaments        []FilamentUse `json:"filaments"`
	Layers           int           `json:"layers"`
	MaxZ             float64       `json:"max_z_mm"`
	NozzleDiameter   float64       `json:"nozzle_diameter"`
	Objects          []string      `json:"objects,omitempty"`
	SupportUsed      bool          `json:"support_used"`
	ExtrudeMoves     int           `json:"extrude_moves"`
	ExtentX          [2]float64    `json:"extent_x"` // min, max des déplacements extrudés
	ExtentY          [2]float64    `json:"extent_y"`
	// Safety : températures et commandes lues dans le G-code de la plaque.
	Safety *GcodeSafety `json:"safety,omitempty"`
}

// ArchiveInfo résume l'archive validée.
type ArchiveInfo struct {
	Entries        int               `json:"entries"`
	StudioVersion  string            `json:"studio_version"` // X-BBL-Client-Version
	PrinterModel   string            `json:"printer_model"`
	PrinterProfile string            `json:"printer_settings_id"`
	ProcessProfile string            `json:"print_settings_id"`
	FilamentIDs    []string          `json:"filament_settings_id"`
	Nozzles        []string          `json:"nozzle_diameter"`
	BedType        string            `json:"bed_type,omitempty"`
	BrimType       string            `json:"brim_type,omitempty"`               // bordure du procédé réellement tranché (project_settings.config)
	BrimWidth      string            `json:"brim_width,omitempty"`              // largeur de bordure (mm), telle qu'écrite par Studio
	RaftLayers     string            `json:"raft_layers,omitempty"`             // couches de radeau du procédé tranché
	EnableSupport  string            `json:"enable_support,omitempty"`          // « 1 » / « 0 » : supports activés dans le procédé tranché
	SupportType    string            `json:"support_type,omitempty"`            // normal(auto), tree(auto), normal(manual), tree(manual)
	SupportAngle   string            `json:"support_threshold_angle,omitempty"` // seuil de surplomb (°, pente depuis l'horizontale) ; « 0 » = automatique
	Plates         []PlateResult     `json:"plates"`
	Settings       map[string]string `json:"-"`
	// GcodeProfile : le G-code de démarrage/fin est-il celui du profil machine officiel ?
	GcodeProfile *GcodeProfile `json:"gcode_profile,omitempty"`
}

// ---- lecture des fichiers de l'archive ----

type kv struct {
	Key   string `xml:"key,attr"`
	Value string `xml:"value,attr"`
}

type siFilament struct {
	ID       string `xml:"id,attr"`
	Tray     string `xml:"tray_info_idx,attr"`
	Type     string `xml:"type,attr"`
	Color    string `xml:"color,attr"`
	UsedM    string `xml:"used_m,attr"`
	UsedG    string `xml:"used_g,attr"`
	Nozzle   string `xml:"nozzle_diameter,attr"`
	ForObjs  string `xml:"used_for_object,attr"`
	ForSupps string `xml:"used_for_support,attr"`
}

type siPlate struct {
	Meta    []kv `xml:"metadata"`
	Objects []struct {
		Name    string `xml:"name,attr"`
		Skipped string `xml:"skipped,attr"`
	} `xml:"object"`
	Filaments []siFilament `xml:"filament"`
}

type siConfig struct {
	Header struct {
		Items []kv `xml:"header_item"`
	} `xml:"header"`
	Plates []siPlate `xml:"plate"`
}

func (p siPlate) meta(k string) string {
	for _, m := range p.Meta {
		if m.Key == k {
			return m.Value
		}
	}
	return ""
}

func readEntry(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("entrée %s trop grande", f.Name)
	}
	return b, nil
}

var gcodeEntryRe = regexp.MustCompile(`^Metadata/plate_(\d+)\.gcode$`)

// ValidateArchive ouvre un .gcode.3mf et contrôle sa structure, ses métadonnées,
// ses trajectoires et leur cohérence avec la demande. Le rapport est rempli
// même en cas d'échec ; l'erreur (ErrInvalidOutput) n'est retournée que si un
// contrôle bloquant échoue.
func ValidateArchive(archivePath string, exp Expect) (*ArchiveInfo, *Validation, error) {
	val := &Validation{}
	info := &ArchiveInfo{}
	finish := func() (*ArchiveInfo, *Validation, error) {
		failed := val.Failed()
		val.OK = len(failed) == 0
		if !val.OK {
			names := make([]string, len(failed))
			for i, c := range failed {
				names[i] = c.Name + ": " + c.Detail
			}
			return info, val, fmt.Errorf("%w: %s", ErrInvalidOutput, strings.Join(names, "; "))
		}
		return info, val, nil
	}

	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		val.add("archive_open", false, "error", "%v", err)
		return finish()
	}
	defer zr.Close()
	info.Entries = len(zr.File)
	entries := map[string]*zip.File{}
	badName := ""
	for _, f := range zr.File {
		n := f.Name
		if strings.Contains(n, "..") || strings.HasPrefix(n, "/") || strings.Contains(n, `\`) {
			badName = n
		}
		entries[n] = f
	}
	if len(zr.File) > maxEntries || badName != "" {
		val.add("archive_open", false, "error", "archive suspecte: %d entrées, nom %q", len(zr.File), badName)
		return finish()
	}
	val.add("archive_open", true, "error", "%d entrées", len(zr.File))

	required := []string{"[Content_Types].xml", "3D/3dmodel.model", "Metadata/slice_info.config", "Metadata/project_settings.config"}
	var missing []string
	for _, r := range required {
		if entries[r] == nil {
			missing = append(missing, r)
		}
	}
	val.add("required_entries", len(missing) == 0, "error", "manquants: %v", missing)
	if len(missing) > 0 {
		return finish()
	}

	// project_settings.config (JSON)
	var settings map[string]any
	if b, err := readEntry(entries["Metadata/project_settings.config"], maxMetaBytes); err != nil {
		val.add("project_settings", false, "error", "%v", err)
		return finish()
	} else if err := json.Unmarshal(b, &settings); err != nil {
		val.add("project_settings", false, "error", "JSON invalide: %v", err)
		return finish()
	}
	val.add("project_settings", true, "error", "%d clés", len(settings))
	info.PrinterModel = firstString(settings["printer_model"])
	info.PrinterProfile = firstString(settings["printer_settings_id"])
	info.ProcessProfile = firstString(settings["print_settings_id"])
	info.FilamentIDs = allStrings(settings["filament_settings_id"])
	info.Nozzles = allStrings(settings["nozzle_diameter"])
	info.BedType = firstString(settings["curr_bed_type"])
	info.BrimType = firstString(settings["brim_type"])
	info.BrimWidth = firstString(settings["brim_width"])
	info.RaftLayers = firstString(settings["raft_layers"])
	info.EnableSupport = firstString(settings["enable_support"])
	info.SupportType = firstString(settings["support_type"])
	info.SupportAngle = firstString(settings["support_threshold_angle"])

	// slice_info.config (XML)
	var si siConfig
	if b, err := readEntry(entries["Metadata/slice_info.config"], maxMetaBytes); err != nil {
		val.add("slice_info", false, "error", "%v", err)
		return finish()
	} else if err := xml.Unmarshal(b, &si); err != nil {
		val.add("slice_info", false, "error", "XML invalide: %v", err)
		return finish()
	}
	for _, h := range si.Header.Items {
		if h.Key == "X-BBL-Client-Version" {
			info.StudioVersion = h.Value
		}
	}
	val.add("slice_info", len(si.Plates) > 0, "error", "%d plaque(s) tranchée(s)", len(si.Plates))
	if len(si.Plates) == 0 {
		return finish()
	}

	// cohérence avec la demande
	if exp.PrinterModel != "" {
		val.add("printer_model", info.PrinterModel == exp.PrinterModel, "error", "archive=%q attendu=%q", info.PrinterModel, exp.PrinterModel)
	}
	if exp.MachineName != "" {
		val.add("machine_profile", info.PrinterProfile == exp.MachineName, "error", "archive=%q attendu=%q", info.PrinterProfile, exp.MachineName)
	}
	if exp.ProcessName != "" {
		val.add("process_profile", info.ProcessProfile == exp.ProcessName, "error", "archive=%q attendu=%q", info.ProcessProfile, exp.ProcessName)
	}
	if exp.Nozzle > 0 {
		ok := len(info.Nozzles) > 0 && math.Abs(parseF(info.Nozzles[0])-exp.Nozzle) < 1e-6
		val.add("nozzle", ok, "error", "archive=%v attendu=%g", info.Nozzles, exp.Nozzle)
	}
	if len(exp.Filaments) > 0 {
		ok := len(info.FilamentIDs) >= len(exp.Filaments)
		for i := 0; ok && i < len(exp.Filaments); i++ {
			ok = info.FilamentIDs[i] == exp.Filaments[i]
		}
		val.add("filament_profiles", ok, "error", "archive=%v attendu=%v", info.FilamentIDs, exp.Filaments)
	}
	if exp.BinaryVersion != "" && info.StudioVersion != "" {
		val.add("studio_version", info.StudioVersion == exp.BinaryVersion, "warn", "archive=%s binaire=%s", info.StudioVersion, exp.BinaryVersion)
	}

	// référence de confiance : profils officiels et fiches d'imprimantes de l'installation, jamais l'archive
	trust := exp.Trust
	if trust == nil {
		trust = DefaultTrust()
	}
	lim := trust.Limits(info.PrinterModel)
	gchk := newGcodeChecker(trust, settings, info)
	var scanBad []string
	// plafond de température de buse pendant l'impression : plage haute des filaments de l'archive (profils officiels)
	nozzleHi, filKnown := 0.0, true
	for _, n := range info.FilamentIDs {
		var r *profiles.Resolved
		if trust != nil && trust.Store != nil {
			r, _ = trust.Store.Resolve(profiles.KindFilament, n)
		}
		if v, ok := firstFloat(r, "nozzle_temperature_range_high"); ok && v > 0 {
			nozzleHi = math.Max(nozzleHi, v)
		} else {
			filKnown = false
		}
	}
	if len(info.FilamentIDs) == 0 {
		filKnown = false
	}
	if !filKnown {
		nozzleHi = 0 // plafond filament non vérifiable : seul le maximum du modèle s'applique
	}

	// plaques
	gcodeIdx := map[int]string{}
	for n := range entries {
		if m := gcodeEntryRe.FindStringSubmatch(n); m != nil {
			i, _ := strconv.Atoi(m[1])
			gcodeIdx[i] = n
		}
	}
	for _, sp := range si.Plates {
		pr := PlateResult{Index: atoi(sp.meta("index"))}
		pr.PredictedSeconds = parseF(sp.meta("prediction"))
		pr.WeightGrams = parseF(sp.meta("weight"))
		pr.SupportUsed = sp.meta("support_used") == "true"
		for _, o := range sp.Objects {
			if o.Skipped != "true" {
				pr.Objects = append(pr.Objects, o.Name)
			}
		}
		for _, f := range sp.Filaments {
			pr.Filaments = append(pr.Filaments, FilamentUse{
				Slot: atoi(f.ID), Type: f.Type, Color: f.Color, TrayInfo: f.Tray,
				Meters: parseF(f.UsedM), Grams: parseF(f.UsedG), Nozzle: parseF(f.Nozzle),
			})
		}
		if nd := strings.Split(sp.meta("nozzle_diameters"), ","); len(nd) > 0 {
			pr.NozzleDiameter = parseF(nd[0])
		}
		pre := fmt.Sprintf("plate_%d.", pr.Index)

		entry, ok := gcodeIdx[pr.Index]
		val.add(pre+"gcode_present", ok, "error", "Metadata/plate_%d.gcode", pr.Index)
		if !ok {
			info.Plates = append(info.Plates, pr)
			continue
		}
		pr.GcodeEntry = entry
		gf := entries[entry]
		pr.GcodeBytes = int64(gf.UncompressedSize64)
		val.add(pre+"gcode_size", pr.GcodeBytes > 1024, "error", "%d octets", pr.GcodeBytes)

		scan, err := scanGcode(gf)
		if err != nil {
			val.add(pre+"gcode_read", false, "error", "%v", err)
			info.Plates = append(info.Plates, pr)
			continue
		}
		pr.GcodeMD5 = scan.md5
		pr.EstimatedTime = scan.totalTime
		pr.Layers = scan.headerLayers
		pr.MaxZ = scan.headerMaxZ
		pr.ExtrudeMoves = scan.extrudes
		pr.ExtentX = [2]float64{scan.minX, scan.maxX}
		pr.ExtentY = [2]float64{scan.minY, scan.maxY}
		sf := scan.safe.GcodeSafety
		pr.Safety = &sf
		{
			refus, avert := sf.Verdict(nozzleHi, lim.NozzleMaxC, lim.BedMaxC, lim.ChamberMaxC)
			src := lim.Source
			if !lim.Known {
				src = "bornes de repli (modèle absent de l'installation)"
			}
			summary := fmt.Sprintf("buse démarrage ≤%.0f °C, 1re couche %.0f °C, impression %.0f–%.0f °C ; plateau 1re couche %.0f °C (max %.0f) ; plafonds %s : buse %.0f, filament %.0f, plateau %.0f",
				sf.NozzleStartMaxC, sf.NozzleFirstLayerC, sf.NozzlePrintMinC, sf.NozzlePrintMaxC, sf.BedFirstLayerC, sf.BedMaxC, src, lim.NozzleMaxC, nozzleHi, lim.BedMaxC)
			if len(refus) > 0 {
				summary = strings.Join(refus, " ; ")
			}
			val.add(pre+"unsafe_gcode", len(refus) == 0, "error", "%s", summary)
			if len(avert) > 0 {
				val.add(pre+"gcode_commands", false, "warn", "commandes hors de l'ensemble attendu pour Bambu Studio : %s", strings.Join(avert, " ; "))
			}
			if !filKnown && len(info.FilamentIDs) > 0 {
				val.add(pre+"filament_range", false, "warn", "plage de température du filament non vérifiable (profil absent de l'installation) : seul le maximum du modèle %.0f °C est appliqué", lim.NozzleMaxC)
			}
		}
		scanBad = append(scanBad, gchk.checkScan(scan.cfgGcode)...)

		if mf := entries[entry+".md5"]; mf != nil {
			b, _ := readEntry(mf, 1024)
			want := strings.ToLower(strings.TrimSpace(string(b)))
			val.add(pre+"gcode_md5", want == scan.md5, "error", "fichier=%s calculé=%s", want, scan.md5)
		} else {
			val.add(pre+"gcode_md5", false, "warn", "pas de fichier md5")
		}
		val.add(pre+"gcode_header", scan.headerLayers > 0 && scan.headerSeconds > 0, "error",
			"couches=%d durée=%s (%.0fs)", scan.headerLayers, scan.totalTime, scan.headerSeconds)
		val.add(pre+"toolpath_extrusions", scan.extrudes > 0, "error", "%d déplacements extrudés", scan.extrudes)
		val.add(pre+"toolpath_layers", scan.layerChanges == scan.headerLayers, "error",
			"%d marqueurs CHANGE_LAYER pour %d couches annoncées", scan.layerChanges, scan.headerLayers)
		if pr.PredictedSeconds > 0 {
			val.add(pre+"time_consistency", math.Abs(pr.PredictedSeconds-scan.headerSeconds) <= math.Max(2, 0.02*scan.headerSeconds), "error",
				"slice_info=%.0fs gcode=%.0fs", pr.PredictedSeconds, scan.headerSeconds)
		} else {
			val.add(pre+"time_consistency", false, "error", "durée absente de slice_info")
		}
		wsum := 0.0
		for _, f := range pr.Filaments {
			wsum += f.Grams
		}
		val.add(pre+"weight", pr.WeightGrams > 0 && math.Abs(wsum-pr.WeightGrams) <= math.Max(0.05, 0.01*pr.WeightGrams), "error",
			"plaque=%.2f g, somme filaments=%.2f g", pr.WeightGrams, wsum)
		val.add(pre+"weight_gcode", math.Abs(scan.headerWeight-pr.WeightGrams) <= math.Max(0.05, 0.01*pr.WeightGrams), "error",
			"gcode=%.2f g slice_info=%.2f g", scan.headerWeight, pr.WeightGrams)
		if exp.Nozzle > 0 {
			val.add(pre+"plate_nozzle", math.Abs(pr.NozzleDiameter-exp.Nozzle) < 1e-6, "error", "plaque=%g attendu=%g", pr.NozzleDiameter, exp.Nozzle)
		}
		if len(exp.Filaments) > 0 {
			bad := 0
			for _, f := range pr.Filaments {
				if f.Slot < 1 || f.Slot > len(exp.Filaments) {
					bad++
				}
			}
			val.add(pre+"filament_slots", bad == 0 && len(pr.Filaments) > 0, "error", "%d emplacement(s) utilisé(s) pour %d demandé(s)", len(pr.Filaments), len(exp.Filaments))
		}
		if len(exp.BedArea) > 0 {
			minX, minY, maxX, maxY := bounds(exp.BedArea)
			const tol = 1.0
			in := scan.minX >= minX-tol && scan.maxX <= maxX+tol && scan.minY >= minY-tol && scan.maxY <= maxY+tol
			val.add(pre+"toolpath_in_bed", in, "error", "X[%.1f,%.1f] Y[%.1f,%.1f] dans plateau X[%.0f,%.0f] Y[%.0f,%.0f]",
				scan.minX, scan.maxX, scan.minY, scan.maxY, minX, maxX, minY, maxY)
		}
		if exp.PrintableZ > 0 {
			tol := ZTolerance(exp.LayerHeight, exp.FirstLayerHeight)
			val.add(pre+"toolpath_height", scan.maxZ <= exp.PrintableZ+tol && math.Abs(scan.maxZ-scan.headerMaxZ) <= tol, "error",
				"Z max trajectoire=%.2f en-tête=%.2f volume utile=%.0f (tolérance %.2f mm)", scan.maxZ, scan.headerMaxZ, exp.PrintableZ, tol)
		}
		if exp.Studio != nil {
			if sp2 := exp.Studio.plate(pr.Index); sp2 != nil {
				rg := 0.0
				for _, f := range sp2.Filaments {
					rg += f.TotalUsedG
				}
				val.add(pre+"result_json_weight", math.Abs(rg-pr.WeightGrams) <= math.Max(0.05, 0.01*pr.WeightGrams), "error",
					"result.json=%.2f g slice_info=%.2f g", rg, pr.WeightGrams)
			} else {
				val.add(pre+"result_json_weight", false, "warn", "plaque absente de result.json")
			}
		}
		info.Plates = append(info.Plates, pr)
	}
	sort.Slice(info.Plates, func(i, j int) bool { return info.Plates[i].Index < info.Plates[j].Index })

	// G-code de démarrage/fin et tous les *_gcode : identiques au profil machine officiel résolu, sinon refus
	info.GcodeProfile = gchk.finish(scanBad)
	switch {
	case len(info.GcodeProfile.Mismatch) > 0:
		val.add("unsafe_gcode_profile", false, "error", "%s (empreinte %s)", info.GcodeProfile.Detail, info.GcodeProfile.Fingerprint)
	case !info.GcodeProfile.Verifiable:
		val.add("unsafe_gcode_profile", false, "warn", "%s", info.GcodeProfile.Detail)
	default:
		val.add("unsafe_gcode_profile", true, "error", "%s (empreinte %s)", info.GcodeProfile.Detail, info.GcodeProfile.Fingerprint)
	}

	if exp.Plate > 0 {
		found := false
		for _, p := range info.Plates {
			if p.Index == exp.Plate {
				found = true
			}
		}
		val.add("requested_plate", found, "error", "plaque %d demandée", exp.Plate)
	}

	// géométrie du plateau : on relit le modèle de l'archive avec le paquet mesh
	if exp.InputVolume > 0 {
		var m *mesh.Model
		data, rerr := os.ReadFile(archivePath) // ParseThreeMF attend l'archive complète
		if rerr == nil {
			m, rerr = mesh.ParseThreeMF(data)
		}
		if rerr == nil {
			cm := m.Combined()
			vol := math.Abs(mesh.Analyze(cm, mesh.AnalyzeOptions{}).Volume)
			level := "warn"
			if exp.InputIsMesh {
				level = "error"
			}
			ratio := vol / exp.InputVolume
			val.add("model_volume", math.Abs(ratio-1) <= 0.03, level, "volume archive=%.1f mm³ entrée=%.1f mm³ (rapport %.3f)", vol, exp.InputVolume, ratio)
			if exp.PrintableZ > 0 {
				bb := cm.Bounds()
				val.add("model_height", bb.Size.Z <= exp.PrintableZ+0.01, "error", "hauteur du modèle %.1f mm", bb.Size.Z)
			}
		} else {
			val.add("model_volume", false, "warn", "modèle de l'archive illisible par mesh: %v", rerr)
		}
	}
	return finish()
}

// ---- G-code ----

type gcodeScan struct {
	md5           string
	headerLayers  int
	headerSeconds float64
	totalTime     string
	headerWeight  float64
	headerMaxZ    float64
	layerChanges  int
	extrudes      int
	minX, maxX    float64
	minY, maxY    float64
	maxZ          float64

	safe     gcodeSafetyScan
	nline    int
	cfgGcode map[string]string // « ; machine_start_gcode = … » du bloc de configuration
}

var (
	reTime   = regexp.MustCompile(`total estimated time:\s*(.+)$`)
	reLayers = regexp.MustCompile(`total layer number:\s*(\d+)`)
	// Une valeur par filament séparées par des virgules (« 12.98,13.22 ») : le poids de la plaque est leur somme.
	reWeight = regexp.MustCompile(`total filament weight \[g\]\s*:\s*([\d.,]+)`)
	reMaxZ   = regexp.MustCompile(`max_z_height:\s*([\d.]+)`)
	reDur    = regexp.MustCompile(`(\d+)\s*([dhms])`)
)

func parseDuration(s string) float64 {
	t := 0.0
	for _, m := range reDur.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.ParseFloat(m[1], 64)
		switch m[2] {
		case "d":
			t += n * 86400
		case "h":
			t += n * 3600
		case "m":
			t += n * 60
		case "s":
			t += n
		}
	}
	return t
}

func scanGcode(f *zip.File) (*gcodeScan, error) {
	if f.UncompressedSize64 > maxGcodeBytes {
		return nil, fmt.Errorf("G-code trop grand: %d octets", f.UncompressedSize64)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	h := md5.New()
	r := bufio.NewReaderSize(io.TeeReader(io.LimitReader(rc, maxGcodeBytes), h), 1<<20)
	s := &gcodeScan{minX: math.Inf(1), minY: math.Inf(1), maxX: math.Inf(-1), maxY: math.Inf(-1)}
	var curZ float64
	for {
		line, err := readLine(r)
		s.nline++
		if len(line) > 0 {
			s.line(line, &curZ)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	s.safe.finish()
	s.md5 = hex.EncodeToString(h.Sum(nil))
	if s.extrudes == 0 {
		s.minX, s.maxX, s.minY, s.maxY = 0, 0, 0, 0
	}
	return s, nil
}

// readLine lit une ligne en ignorant ce qui dépasse maxGcodeLineLen (les blocs
// de configuration peuvent être très longs).
func readLine(r *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if len(out) < maxGcodeLineLen {
			out = append(out, chunk...)
		}
		if err != nil {
			return out, err
		}
		if !isPrefix {
			return out, nil
		}
	}
}

func (s *gcodeScan) line(l []byte, curZ *float64) {
	if len(l) == 0 {
		return
	}
	s.safe.lineNo = s.nline
	if l[0] == ';' {
		// paramètres de G-code du bloc de configuration (« ; machine_start_gcode = … ») : conservés pour l'empreinte
		if len(l) > 6 && l[1] == ' ' {
			head := l
			if len(head) > 80 {
				head = head[:80]
			}
			if i := bytes.Index(head, []byte(" = ")); i > 2 {
				if key := string(l[2:i]); strings.HasSuffix(key, "_gcode") && !strings.ContainsAny(key, " \t") {
					if s.cfgGcode == nil {
						s.cfgGcode = map[string]string{}
					}
					s.cfgGcode[key] = string(l[i+3:])
				}
			}
		}
		// commentaires : en-tête et marqueurs, mais jamais les lignes très longues du bloc de config
		if len(l) > 4096 {
			return
		}
		str := string(l)
		switch {
		case strings.HasPrefix(str, "; CHANGE_LAYER"):
			s.layerChanges++
			if s.layerChanges == 1 {
				s.safe.layerOne()
			}
		case strings.Contains(str, "total estimated time:"):
			if m := reTime.FindStringSubmatch(str); m != nil {
				s.totalTime = strings.TrimSpace(m[1])
				s.headerSeconds = parseDuration(s.totalTime)
			}
		case strings.Contains(str, "total layer number:"):
			if m := reLayers.FindStringSubmatch(str); m != nil {
				s.headerLayers, _ = strconv.Atoi(m[1])
			}
		case strings.Contains(str, "total filament weight [g]"):
			if m := reWeight.FindStringSubmatch(str); m != nil {
				s.headerWeight = 0
				for _, w := range strings.Split(m[1], ",") {
					s.headerWeight += parseF(w)
				}
			}
		case strings.Contains(str, "max_z_height:"):
			if m := reMaxZ.FindStringSubmatch(str); m != nil {
				s.headerMaxZ = parseF(m[1])
			}
		}
		return
	}
	s.safe.command(l, s.layerChanges > 0)
	if l[0] != 'G' || len(l) < 3 {
		return
	}
	cmd := l[:2]
	if !(bytes.Equal(cmd, []byte("G0")) || bytes.Equal(cmd, []byte("G1")) || bytes.Equal(cmd, []byte("G2")) || bytes.Equal(cmd, []byte("G3"))) || (l[2] != ' ' && l[2] != '\t') {
		return
	}
	if i := bytes.IndexByte(l, ';'); i >= 0 {
		l = l[:i]
	}
	var x, y, e float64
	var hx, hy, he bool
	for _, w := range bytes.Fields(l[2:]) {
		if len(w) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(string(w[1:]), 64)
		if err != nil {
			continue
		}
		switch w[0] {
		case 'X':
			x, hx = v, true
		case 'Y':
			y, hy = v, true
		case 'Z':
			*curZ = v
		case 'E':
			e, he = v, true
		}
	}
	// M83 (extrusion relative) est utilisé par Bambu : E>0 = dépôt de matière
	if he && e > 0 && (hx || hy) && s.layerChanges > 0 { // le gcode de démarrage (purge hors zone) précède le premier CHANGE_LAYER
		s.extrudes++
		if hx {
			s.minX, s.maxX = math.Min(s.minX, x), math.Max(s.maxX, x)
		}
		if hy {
			s.minY, s.maxY = math.Min(s.minY, y), math.Max(s.maxY, y)
		}
		s.maxZ = math.Max(s.maxZ, *curZ)
	}
}

// ---- utilitaires ----

func bounds(poly []profiles.Point) (minX, minY, maxX, maxY float64) {
	minX, minY, maxX, maxY = math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range poly {
		minX, maxX = math.Min(minX, p.X), math.Max(maxX, p.X)
		minY, maxY = math.Min(minY, p.Y), math.Max(maxY, p.Y)
	}
	return
}

func parseF(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func firstString(v any) string {
	if l := allStrings(v); len(l) > 0 {
		return l[0]
	}
	return ""
}

func allStrings(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// ZTolerance : tolérance (mm) entre la hauteur de trajectoire et celle annoncée : une couche (ou la première
// couche) d'écart est normale ; jamais moins de 0,25 mm.
func ZTolerance(layer, first float64) float64 { return math.Max(0.25, math.Max(layer, first)) }
