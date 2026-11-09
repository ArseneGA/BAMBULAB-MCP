package upgrade

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	reFailTest = regexp.MustCompile(`^\s*--- FAIL: (\S+)`)
	reFailPkg  = regexp.MustCompile(`^FAIL\s+(\S+)`)
	rePanic    = regexp.MustCompile(`^(panic: .*|--- FAIL: .*)`)
)

// FailureDigest résume la sortie COMPLÈTE d'un `go test` en échec : les lignes « --- FAIL: NomDuTest » et « FAIL <paquet> »,
// bornées (au plus 12 tests, 6 paquets, 120 caractères par nom). La fin de la sortie ne les contient pas forcément (elle est
// tronquée, et les paquets suivants la noient) : sans ce résumé, le journal et la notification ne disent pas QUEL test a échoué.
// Chaîne vide si rien d'identifiable.
func FailureDigest(out string) string {
	const maxTests, maxPkgs = 12, 6
	var tests, pkgs []string
	seenT, seenP := map[string]bool{}, map[string]bool{}
	moreT, moreP := 0, 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := reFailTest.FindStringSubmatch(line); m != nil && !seenT[m[1]] {
			seenT[m[1]] = true
			if len(tests) < maxTests {
				tests = append(tests, truncate(m[1], 120))
			} else {
				moreT++
			}
		} else if m := reFailPkg.FindStringSubmatch(line); m != nil && !seenP[m[1]] {
			seenP[m[1]] = true
			if len(pkgs) < maxPkgs {
				pkgs = append(pkgs, truncate(m[1], 120))
			} else {
				moreP++
			}
		}
	}
	var parts []string
	if len(tests) > 0 {
		s := "--- FAIL: " + strings.Join(tests, ", ")
		if moreT > 0 {
			s += fmt.Sprintf(" (+%d autres)", moreT)
		}
		parts = append(parts, s)
	}
	if len(pkgs) > 0 {
		s := "FAIL " + strings.Join(pkgs, ", ")
		if moreP > 0 {
			s += fmt.Sprintf(" (+%d autres)", moreP)
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		for _, line := range strings.Split(out, "\n") {
			if m := rePanic.FindString(strings.TrimRight(line, "\r")); m != "" {
				return truncate(m, 200)
			}
		}
	}
	return strings.Join(parts, " ; ")
}

// withDigest met le résumé de l'échec EN TÊTE du message d'erreur (les journaux et notifications coupent la fin).
func withDigest(err error, out string) error {
	d := FailureDigest(out)
	if d == "" {
		return err
	}
	return fmt.Errorf("[%s] %w", d, err)
}
