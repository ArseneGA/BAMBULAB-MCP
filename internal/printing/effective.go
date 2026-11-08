package printing

import (
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// effectiveSlice : réglages d'adhérence et de supports réellement tranchés. Une clé absente de l'archive prend le
// défaut de Bambu Studio (brim_type : auto_brim, raft_layers : 0…) : la valeur effective n'est jamais « inconnue ».
type effectiveSlice struct {
	BrimType, BrimWidth, RaftLayers, SupportType string
	SupportsOn                                   bool
	ThresholdDeg                                 float64 // support_threshold_angle du profil (0 : automatique)
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func orBambuDefault(v, key string) string {
	if strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	d, _ := slicer.BambuDefault(key)
	return d
}

// effectiveOf lit les réglages effectifs de l'archive pour la plaque donnée. Les supports comptent comme actifs
// quand enable_support vaut 1, ou quand le G-code de la plaque en contient (support_used).
func effectiveOf(info *slicer.ArchiveInfo, plate int) effectiveSlice {
	e := effectiveSlice{
		BrimType:    orBambuDefault(info.BrimType, "brim_type"),
		BrimWidth:   orBambuDefault(info.BrimWidth, "brim_width"),
		RaftLayers:  orBambuDefault(info.RaftLayers, "raft_layers"),
		SupportType: orBambuDefault(info.SupportType, "support_type"),
		SupportsOn:  truthy(info.EnableSupport),
	}
	for _, p := range info.Plates {
		if p.Index == plate && p.SupportUsed {
			e.SupportsOn = true
		}
	}
	if v, ok := parseFloat(strings.TrimSpace(info.SupportAngle)); ok {
		e.ThresholdDeg = v
	}
	return e
}

func parseFloat(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil
}

// fillSummary reporte les réglages effectifs dans le résumé de découpe.
func (e effectiveSlice) fillSummary(sum *ArtifactSummary) {
	if sum == nil {
		return
	}
	on := e.SupportsOn
	sum.BrimType, sum.BrimWidth, sum.RaftLayers, sum.EnableSupport, sum.SupportType = e.BrimType, e.BrimWidth, e.RaftLayers, &on, e.SupportType
}

// fillReport reporte les réglages effectifs et le croisement surplombs / supports dans le rapport de validation.
// pm : la pièce (nil si illisible ou plan multi-pièces : aucun croisement possible).
func (e effectiveSlice) fillReport(rep *ValidationReport, pm *mesh.Mesh) {
	on := e.SupportsOn
	rep.BrimType, rep.BrimWidth, rep.RaftLayers, rep.EnableSupport, rep.SupportType = e.BrimType, e.BrimWidth, e.RaftLayers, &on, e.SupportType
	if pm == nil {
		return
	}
	rep.Overhang = mesh.OverhangsForSupport(pm, e.ThresholdDeg)
	if s := rep.Overhang.UnsupportedSummary(on); s != "" {
		rep.Warnings = append(rep.Warnings, "overhang_without_support : "+s)
	}
}

// colorSourceLabel normalise l'origine d'une couleur : « AMS » (lue sur l'imprimante), « bobine déclarée »
// (inventaire ou bobine externe saisie), « profil » (couleur par défaut du preset, jamais confirmée).
func colorSourceLabel(detail string) string {
	d := strings.ToLower(detail)
	switch {
	case strings.Contains(d, "ams"):
		return "AMS"
	case strings.Contains(d, "inventaire"), strings.Contains(d, "bobine"), strings.Contains(d, "déclar"), strings.Contains(d, "saisi"):
		return "bobine déclarée"
	}
	return "profil"
}
