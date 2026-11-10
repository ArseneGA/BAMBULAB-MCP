package insights

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// SettingsSignature : empreinte courte (« sig_ » + 12 hexadécimaux) des réglages EFFECTIFS d'une impression : machine,
// procédé, filaments, buse, surcharges (triées par clé), supports et type de plateau. Deux impressions aux mêmes réglages
// ont la même signature : c'est la clé d'un « profil testé » (fiche pièce PJ5, comparateur PJ7). Fonction pure et stable.
func SettingsSignature(s EffectiveSettings) string {
	ov := make([]string, 0, len(s.Overrides))
	for _, o := range s.Overrides {
		ov = append(ov, fmt.Sprintf("%s=%v@%s", o.Key, o.Value, o.Target))
	}
	sort.Strings(ov)
	canon := strings.Join([]string{
		s.Machine, s.Process, strings.Join(s.Filaments, ","), fmt.Sprintf("%.3f", s.NozzleMM), strings.Join(ov, ";"),
		fmt.Sprintf("supports=%t", s.Supports), s.BedType,
	}, "|")
	sum := sha256.Sum256([]byte(canon))
	return "sig_" + hex.EncodeToString(sum[:])[:12]
}

// SettingsLabel : libellé lisible d'un profil (procédé, premier filament, buse, nombre de surcharges).
func SettingsLabel(s EffectiveSettings) string {
	var parts []string
	if s.Process != "" {
		parts = append(parts, s.Process)
	}
	if len(s.Filaments) > 0 {
		parts = append(parts, strings.Join(s.Filaments, " + "))
	}
	if s.NozzleMM > 0 {
		parts = append(parts, fmt.Sprintf("buse %s", strings.Replace(fmt.Sprintf("%g", s.NozzleMM), ".", ",", 1)))
	}
	if n := len(s.Overrides); n > 0 {
		parts = append(parts, fmt.Sprintf("%d réglage%s modifié%s", n, plural(n), plural(n)))
	}
	if len(parts) == 0 {
		return "réglages non relevés"
	}
	return strings.Join(parts, " · ")
}

func plural(n int) string {
	if n > 1 {
		return "s"
	}
	return ""
}
