package slicer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

// GcodeProfile : résultat de la comparaison du G-code de démarrage/fin (et de tous les paramètres *_gcode)
// d'une archive avec ceux du profil machine officiel de l'installation de Bambu Studio.
type GcodeProfile struct {
	Official    bool     `json:"official"`   // vrai : tout correspond au profil officiel
	Verifiable  bool     `json:"verifiable"` // faux : aucun profil officiel de référence n'a pu être résolu
	Source      string   `json:"source,omitempty"`
	Checked     []string `json:"checked_keys,omitempty"`
	Mismatch    []string `json:"mismatch_keys,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"` // SHA-256 (12 premiers caractères) des textes *_gcode de l'archive
	Detail      string   `json:"detail,omitempty"`
}

// canonGcode met un texte de G-code sous une forme comparable : fins de ligne normalisées et retours à la
// ligne écrits « \n » comme dans le bloc de configuration du G-code.
func canonGcode(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, `\"`, `"`)
	// les fins de ligne et espaces de queue sont sans effet : Studio en ajoute ou en retire selon la clé
	for {
		t := strings.TrimRight(s, " \t")
		t = strings.TrimSuffix(t, `\n`)
		if t == s {
			return s
		}
		s = t
	}
}

func gcodeText(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			} else {
				out = append(out, fmt.Sprint(e))
			}
		}
		return out
	case nil:
		return nil
	}
	return []string{fmt.Sprint(v)}
}

type gcodeChecker struct {
	gp        GcodeProfile
	machines  []*profiles.Resolved // profil nommé, ou à défaut tous les profils officiels du même modèle
	filaments []*profiles.Resolved // un par emplacement (nil si introuvable)
	byName    bool
}

// newGcodeChecker compare les paramètres *_gcode et post_process du project_settings.config d'une archive aux
// profils officiels. Seule la référence de confiance (profils de l'installation) fait foi.
func newGcodeChecker(t *Trust, settings map[string]any, info *ArchiveInfo) *gcodeChecker {
	c := &gcodeChecker{}
	var keys []string
	var fp strings.Builder
	for k := range settings {
		if strings.HasSuffix(k, "_gcode") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		fp.WriteString(k + "\x00")
		for _, s := range gcodeText(settings[k]) {
			fp.WriteString(canonGcode(s) + "\x01")
		}
	}
	sum := sha256.Sum256([]byte(fp.String()))
	c.gp.Fingerprint = hex.EncodeToString(sum[:6])

	if pp := strings.TrimSpace(strings.Join(gcodeText(settings["post_process"]), "")); pp != "" {
		c.gp.Mismatch = append(c.gp.Mismatch, "post_process (script exécuté sur le G-code)")
	}
	if t == nil || t.Store == nil {
		c.gp.Detail = "aucune installation de Bambu Studio : profil officiel introuvable"
		return c
	}
	if r, err := t.Store.Resolve(profiles.KindMachine, info.PrinterProfile); err == nil {
		c.machines, c.byName = []*profiles.Resolved{r}, true
		c.gp.Source = r.Name
	} else if info.PrinterModel != "" {
		for _, n := range t.Store.Names(profiles.KindMachine) {
			r, err := t.Store.Resolve(profiles.KindMachine, n)
			if err != nil {
				continue
			}
			if m, _ := r.String("printer_model"); m == info.PrinterModel {
				c.machines = append(c.machines, r)
			}
		}
		if len(c.machines) > 0 {
			c.gp.Source = fmt.Sprintf("profils officiels du modèle %s (%d)", info.PrinterModel, len(c.machines))
		}
	}
	for _, n := range info.FilamentIDs {
		r, _ := t.Store.Resolve(profiles.KindFilament, n)
		c.filaments = append(c.filaments, r)
	}
	if len(c.machines) == 0 {
		c.gp.Detail = fmt.Sprintf("profil machine %q / modèle %q absents de l'installation : G-code non comparable", info.PrinterProfile, info.PrinterModel)
		return c
	}
	c.gp.Verifiable = true

	for _, k := range keys {
		arch := gcodeText(settings[k])
		c.gp.Checked = append(c.gp.Checked, k)
		if !c.matchProject(k, arch) {
			c.gp.Mismatch = append(c.gp.Mismatch, k)
		}
	}
	return c
}

// machineValues : valeurs officielles d'une clé *_gcode de machine (une par profil de référence).
func (c *gcodeChecker) machineValues(k string) ([]string, bool) {
	var out []string
	for _, m := range c.machines {
		if v, ok := m.Config[k]; ok {
			out = append(out, strings.Join(gcodeText(v), ""))
		}
	}
	return out, len(out) > 0
}

func (c *gcodeChecker) matchProject(k string, arch []string) bool {
	if want, ok := c.machineValues(k); ok {
		got := canonGcode(strings.Join(arch, ""))
		for _, w := range want {
			if canonGcode(w) == got {
				return true
			}
		}
		return false
	}
	// paramètre de filament : une valeur par emplacement
	if len(c.filaments) > 0 {
		for i, f := range c.filaments {
			if f == nil {
				continue
			}
			v, ok := f.Config[k]
			if !ok {
				continue
			}
			want := gcodeText(v)
			if len(want) == 0 || i >= len(arch) {
				continue
			}
			if canonGcode(arch[i]) != canonGcode(want[0]) && (len(want) <= i || canonGcode(arch[i]) != canonGcode(want[i])) {
				return false
			}
		}
		for _, f := range c.filaments {
			if f != nil {
				if _, ok := f.Config[k]; ok {
					return true
				}
			}
		}
	}
	// connu d'aucun profil officiel : doit être vide
	return strings.TrimSpace(strings.Join(arch, "")) == ""
}

// checkScan compare les paramètres *_gcode du bloc de configuration du G-code lui-même au profil officiel.
func (c *gcodeChecker) checkScan(cfg map[string]string) (bad []string) {
	if !c.gp.Verifiable {
		return nil
	}
	for k, v := range cfg {
		want, ok := c.machineValues(k)
		if !ok {
			continue // paramètre de filament (format de liste) : couvert par project_settings
		}
		got := canonGcode(v)
		match := false
		for _, w := range want {
			if canonGcode(w) == got {
				match = true
			}
		}
		if !match {
			bad = append(bad, k)
		}
	}
	sort.Strings(bad)
	return bad
}

func (c *gcodeChecker) finish(scanBad []string) *GcodeProfile {
	for _, b := range scanBad {
		c.gp.Mismatch = append(c.gp.Mismatch, "bloc de configuration du G-code : "+b)
	}
	c.gp.Official = c.gp.Verifiable && len(c.gp.Mismatch) == 0
	if c.gp.Detail == "" {
		switch {
		case len(c.gp.Mismatch) > 0:
			c.gp.Detail = "différences avec le profil officiel : " + strings.Join(c.gp.Mismatch, ", ")
		case c.gp.Official:
			c.gp.Detail = fmt.Sprintf("%d paramètres *_gcode identiques au profil officiel (%s)", len(c.gp.Checked), c.gp.Source)
		}
	}
	gp := c.gp
	return &gp
}
