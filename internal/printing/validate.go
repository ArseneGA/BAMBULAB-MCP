package printing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// ValidateInput est l'entrée de validate_print_artifact.
type ValidateInput struct {
	ArtifactID  string               `json:"artifact_id,omitempty" jsonschema:"empreinte sha256 d'un .gcode.3mf du stockage (ex. artefact d'une variante) ; ou print_job_id"`
	PrintJobID  string               `json:"print_job_id,omitempty"`
	PrinterID   string               `json:"printer_id" jsonschema:"imprimante réelle visée"`
	Plate       int                  `json:"plate,omitempty" jsonschema:"plaque à imprimer (défaut : celle de l'impression, sinon 1)"`
	Assignments []FilamentAssignment `json:"assignments,omitempty" jsonschema:"matières chargées : filament du fichier -> emplacement -> matière"`
}

// ValidateOutput : rapport et impression.
type ValidateOutput struct {
	PrintJobID string               `json:"print_job_id"`
	State      contracts.PrintState `json:"state"`
	Report     *ValidationReport    `json:"report"`
	Job        Job                  `json:"job"`
}

var trayRe = regexp.MustCompile(`^ams([1-4]):([1-4])$`)

// parseTray renvoie l'identifiant global du bac (unité*4 + bac, base 0) ou 254 pour la bobine externe.
func parseTray(t string) (id int, ext bool, unit int, err error) {
	if t == "ext" {
		return 254, true, 0, nil
	}
	m := trayRe.FindStringSubmatch(t)
	if m == nil {
		return 0, false, 0, fmt.Errorf("emplacement %q invalide (ams1:1..ams4:4 ou ext)", t)
	}
	u, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return (u-1)*4 + (b - 1), false, u, nil
}

// buildAMS dérive ams_mapping/use_ams des affectations (positions = numéro de filament - 1).
func buildAMS(as []FilamentAssignment) (mapping []int, useAMS bool) {
	n := 5
	for _, a := range as {
		if a.FilamentSlot > n {
			n = a.FilamentSlot
		}
	}
	mapping = make([]int, n)
	for i := range mapping {
		mapping[i] = -1
	}
	for _, a := range as {
		id, ext, _, err := parseTray(a.Tray)
		if err != nil || a.FilamentSlot < 1 {
			continue
		}
		mapping[a.FilamentSlot-1] = id
		if !ext {
			useAMS = true
		}
	}
	if !useAMS {
		return nil, false
	}
	return mapping, true
}

func normName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return -1
	}, s)
}

// checkAssignments vérifie les matières contre le fichier découpé et la machine réelle.
func (s *Service) checkAssignments(ctx context.Context, rep *ValidationReport, rec PrinterRecord, model contracts.PrinterModel, plate *slicer.PlateResult, as []FilamentAssignment, strict bool) {
	used := map[int]slicer.FilamentUse{}
	if plate != nil {
		for _, f := range plate.Filaments {
			used[f.Slot] = f
		}
	}
	if len(as) == 0 {
		lvl := "warn"
		if strict {
			lvl = "error"
		}
		rep.add(true, "materials_assigned", false, lvl, "affectation des matières (filament -> emplacement -> matière) non fournie ; obligatoire à l'envoi")
		return
	}
	seenSlot, seenTray := map[int]bool{}, map[string]bool{}
	for _, a := range as {
		if a.FilamentSlot < 1 || a.FilamentSlot > 16 {
			rep.add(true, "assignment_slot", false, "error", fmt.Sprintf("numéro de filament %d hors plage 1..16", a.FilamentSlot))
			continue
		}
		if seenSlot[a.FilamentSlot] {
			rep.add(true, "assignment_slot", false, "error", fmt.Sprintf("filament %d affecté deux fois", a.FilamentSlot))
		}
		seenSlot[a.FilamentSlot] = true
		_, ext, unit, err := parseTray(a.Tray)
		if err != nil {
			rep.add(true, "assignment_tray", false, "error", err.Error())
			continue
		}
		if seenTray[a.Tray] {
			rep.add(true, "assignment_tray", false, "error", fmt.Sprintf("emplacement %s affecté deux fois", a.Tray))
		}
		seenTray[a.Tray] = true
		if !ext {
			switch {
			case model.AMSSupported != nil && !*model.AMSSupported:
				rep.add(true, "ams_available", false, "error", model.Name+" ne prend pas en charge l'AMS")
			case unit > rec.AMSUnits:
				rep.add(true, "ams_available", false, "error", fmt.Sprintf("%s demandé mais %d AMS déclaré(s) sur %s", a.Tray, rec.AMSUnits, rec.Name))
			}
		}
		if strings.TrimSpace(a.MaterialID) == "" {
			rep.add(true, "assignment_material", false, "error", fmt.Sprintf("matière du filament %d non précisée", a.FilamentSlot))
			continue
		}
		if plate != nil {
			if _, ok := used[a.FilamentSlot]; !ok {
				rep.add(true, "assignment_unused", false, "warn", fmt.Sprintf("le filament %d n'est pas utilisé par la plaque %d", a.FilamentSlot, plate.Index))
			}
		}
		var mat contracts.Material
		if err := s.d.Store.GetDoc(ctx, "materials", a.MaterialID, &mat); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				rep.add(true, "material_known", false, "warn", fmt.Sprintf("matière %q absente du catalogue : compatibilité non vérifiée", a.MaterialID))
			}
			continue
		}
		if u, ok := used[a.FilamentSlot]; ok && u.Type != "" && !strings.EqualFold(u.Type, mat.Family) {
			rep.add(true, "material_family", false, "error", fmt.Sprintf("filament %d : le fichier est découpé pour %s mais la matière affectée est %s", a.FilamentSlot, u.Type, mat.Family))
		}
		if mat.NozzleMinMM != nil && *mat.NozzleMinMM > rec.NozzleMM+1e-9 {
			rep.add(true, "material_nozzle", false, "error", fmt.Sprintf("%s demande une buse d'au moins %g mm (installée : %g mm)", a.MaterialID, *mat.NozzleMinMM, rec.NozzleMM))
		}
		if mat.HardenedNozzleRequired != nil && *mat.HardenedNozzleRequired {
			rep.add(true, "material_hardened_nozzle", false, "warn", fmt.Sprintf("%s demande une buse durcie : type de buse installée non connu du service, à vérifier", a.MaterialID))
		}
	}
	for slot := range used {
		if !seenSlot[slot] {
			rep.add(true, "assignment_missing", false, "error", fmt.Sprintf("le filament %d est utilisé par la plaque mais n'a pas d'emplacement", slot))
		}
	}
	if len(rep.Blocking) == 0 {
		rep.add(true, "materials_assigned", true, "error", fmt.Sprintf("%d affectation(s) cohérente(s) avec le fichier et la machine", len(as)))
	}
	// Alerte AVANT l'envoi : une bobine risque de manquer (inventaire des bobines, internal/filament). Jamais bloquant
	// ici : l'inventaire peut être faux ; c'est l'humain qui décide sur la page de confirmation.
	for _, w := range s.filamentWarnings(ctx, rec.ID, plate, as) {
		rep.add(true, w.Name, false, "warn", w.Detail)
	}
}

// ValidateArtifact valide l'archive et sa compatibilité avec l'imprimante réelle.
func (s *Service) ValidateArtifact(ctx context.Context, in ValidateInput) (*ValidateOutput, error) {
	if in.PrinterID == "" {
		return nil, invalidf("printer_id requis")
	}
	rec, err := s.GetPrinterRecord(ctx, in.PrinterID)
	if err != nil {
		return nil, err
	}
	model, err := s.GetPrinterModel(ctx, rec.ModelID)
	if err != nil {
		return nil, err
	}
	var j Job
	switch {
	case in.PrintJobID != "":
		if j, err = s.GetJob(ctx, in.PrintJobID); err != nil {
			return nil, err
		}
		if in.ArtifactID != "" && j.ArtifactSHA256 != "" && in.ArtifactID != j.ArtifactSHA256 {
			return nil, invalidf("artifact_id ne correspond pas à l'artefact de l'impression %s", j.ID)
		}
	case in.ArtifactID != "":
		if !s.d.Blobs.Has(in.ArtifactID) {
			return nil, fmt.Errorf("%w : artefact %q absent du stockage", ErrNotFound, in.ArtifactID)
		}
		if j, err = s.adoptArtifact(ctx, in.ArtifactID, in.PrinterID); err != nil {
			return nil, err
		}
	default:
		return nil, invalidf("artifact_id ou print_job_id requis")
	}
	if j.State != contracts.PrintValidated {
		return nil, fmt.Errorf("%w : l'impression %s est en %s ; la validation s'applique à une impression VALIDATED", ErrState, j.ID, j.State)
	}
	plate := in.Plate
	if plate == 0 {
		plate = j.Plate
	}
	if plate == 0 {
		plate = 1
	}
	assign := in.Assignments
	if len(assign) == 0 {
		assign = j.Assignments
	}
	rep := &ValidationReport{At: s.now(), PrinterID: in.PrinterID, ArtifactID: j.ArtifactSHA256, Plate: plate, Validator: "slicer.ValidateArchive"}
	path, err := s.d.Blobs.Path(j.ArtifactSHA256)
	if err != nil {
		return nil, err
	}
	if err := s.d.Blobs.Verify(j.ArtifactSHA256); err != nil {
		rep.add(false, "artifact_integrity", false, "error", err.Error())
	}
	exp := slicer.Expect{Nozzle: rec.NozzleMM, Plate: plate}
	if bv := model.BuildVolume; bv != nil && bv.X > 0 && bv.Y > 0 {
		exp.BedArea = []profiles.Point{{X: 0, Y: 0}, {X: bv.X, Y: 0}, {X: bv.X, Y: bv.Y}, {X: 0, Y: bv.Y}}
		exp.PrintableZ = bv.Z
	}
	info, val, verr := s.d.Validator(path, exp)
	if val != nil {
		for _, c := range val.Checks {
			rep.Archive = append(rep.Archive, c)
			if !c.OK {
				line := c.Name + " : " + c.Detail
				if c.Level == "error" {
					rep.Blocking = append(rep.Blocking, line)
				} else {
					rep.Warnings = append(rep.Warnings, line)
				}
			}
		}
	} else if verr != nil {
		rep.add(false, "archive_validation", false, "error", verr.Error())
	}
	if info != nil {
		s.compatChecks(ctx, rep, rec, model, info, plate, assign)
		sum := &ArtifactSummary{Plates: info.Plates, PrinterModel: info.PrinterModel, MachinePreset: info.PrinterProfile, Process: info.ProcessProfile,
			Filaments: info.FilamentIDs, Nozzles: info.Nozzles, BedType: info.BedType, StudioVersion: info.StudioVersion, GcodeProfile: info.GcodeProfile}
		if j.Summary != nil {
			sum.StudioVersion = firstNonEmpty(sum.StudioVersion, j.Summary.StudioVersion)
		}
		j.Summary = sum
	}
	rep.OK = len(rep.Blocking) == 0
	j, err = s.update(ctx, j.ID, func(x *Job) error {
		if x.State != contracts.PrintValidated {
			return fmt.Errorf("%w : %s", ErrState, x.State)
		}
		x.PrinterID, x.Validation, x.Summary, x.Plate = in.PrinterID, rep, j.Summary, plate
		if len(in.Assignments) > 0 {
			x.Assignments = in.Assignments
		}
		x.event("validate", x.State, x.State, fmt.Sprintf("validation : ok=%v bloquants=%d avertissements=%d", rep.OK, len(rep.Blocking), len(rep.Warnings)), s.now())
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &ValidateOutput{PrintJobID: j.ID, State: j.State, Report: rep, Job: j}, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *Service) compatChecks(ctx context.Context, rep *ValidationReport, rec PrinterRecord, model contracts.PrinterModel, info *slicer.ArchiveInfo, plate int, as []FilamentAssignment) {
	if info.PrinterModel != "" {
		ok := normName(info.PrinterModel) == normName(model.Name)
		rep.add(true, "printer_model", ok, "error", fmt.Sprintf("archive découpée pour %q ; imprimante %s est un %q", info.PrinterModel, rec.Name, model.Name))
	} else {
		rep.add(true, "printer_model", false, "warn", "modèle d'imprimante absent de l'archive : non vérifié")
	}
	if len(info.Nozzles) > 0 {
		n, _ := strconv.ParseFloat(strings.TrimSpace(info.Nozzles[0]), 64)
		rep.add(true, "nozzle", math.Abs(n-rec.NozzleMM) < 1e-6, "error", fmt.Sprintf("archive : buse %s mm ; imprimante : %g mm", info.Nozzles[0], rec.NozzleMM))
	}
	var pr *slicer.PlateResult
	for i := range info.Plates {
		if info.Plates[i].Index == plate {
			pr = &info.Plates[i]
		}
	}
	rep.add(true, "plate_present", pr != nil, "error", fmt.Sprintf("plaque %d demandée ; plaques tranchées : %d", plate, len(info.Plates)))
	if pr != nil {
		if bv := model.BuildVolume; bv != nil && bv.Z > 0 {
			rep.add(true, "build_height", pr.MaxZ <= bv.Z+0.01, "error", fmt.Sprintf("hauteur de la plaque %.2f mm ; volume utile %.0f mm", pr.MaxZ, bv.Z))
		}
	}
	if info.BedType != "" && len(model.Plates) > 0 {
		ok := false
		for _, p := range model.Plates {
			if normName(p) == normName(info.BedType) {
				ok = true
			}
		}
		rep.add(true, "bed_type", ok, "warn", fmt.Sprintf("plateau %q de l'archive ; plateaux connus pour %s : %v", info.BedType, model.Name, model.Plates))
	}
	s.checkAssignments(ctx, rep, rec, model, pr, as, false)
}

// adoptArtifact crée (ou retrouve) une impression VALIDATED autour d'un artefact déjà présent
// sur le stockage (ex. variante d'une expérience). L'historique le dit : « artefact existant ».
func (s *Service) adoptArtifact(ctx context.Context, sha, printerID string) (Job, error) {
	if existing, err := s.ListJobs(ctx, contracts.PrintValidated, 200); err == nil {
		for _, e := range existing {
			if e.ArtifactSHA256 == sha && e.PrinterID == printerID {
				return e, nil
			}
		}
	}
	size, err := s.d.Blobs.Size(sha)
	if err != nil {
		return Job{}, err
	}
	j, err := s.createJob(ctx, Job{PrinterID: printerID, ArtifactSHA256: sha, ArtifactSize: size, Name: sha[:12] + ".gcode.3mf"})
	if err != nil {
		return j, err
	}
	for _, step := range []struct {
		ev   contracts.PrintEvent
		note string
	}{{contracts.EvPrepare, "artefact existant adopté (découpé hors de cette impression)"}, {contracts.EvStartSlice, "découpe non rejouée"}, {contracts.EvSliceOK, "archive à valider"}} {
		if j, err = s.transition(ctx, j.ID, step.ev, step.note, nil); err != nil {
			return j, err
		}
	}
	return j, nil
}
