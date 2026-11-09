package printing

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// Les NOUVEAUX dépôts portent un nom parlant (modèle, réglages clés, numéro de version) ; l'adaptateur y ajoute
// l'empreinte courte. Le nom reste sûr pour la carte SD et stable pour une même impression.
func TestDepositNameIsMeaningfulAndStable(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	// horloge simulée qui avance à chaque lecture : le numéro de version dépend de l'ordre de création, que l'horloge
	// réelle (résolution ~1 à 15 ms sous Windows) ne garantit pas pour des dépôts créés à la suite.
	clock := time.Now()
	s.d.Now = func() time.Time { clock = clock.Add(time.Second); return clock }
	m, err := s.d.Store.CreateModel(ctx, "test", contracts.Model{Name: "Porte guitare (Éric)"})
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	mk := func(sha string, support bool) Job {
		j, err := s.createJob(ctx, Job{ModelID: m.ID, Name: "x.gcode.3mf", ArtifactSHA256: sha, Summary: &ArtifactSummary{EnableSupport: &support, BrimType: "outer_only", BrimWidth: "5"}})
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	_ = yes
	j1 := mk(strings.Repeat("a", 64), false)
	j2 := mk(strings.Repeat("b", 64), true)
	j3 := mk(strings.Repeat("c", 64), true)
	n1, n2, n3 := s.depositName(ctx, j1), s.depositName(ctx, j2), s.depositName(ctx, j3)
	if n1 != "porte_guitare_eric_bordure_v1.gcode.3mf" || n2 != "porte_guitare_eric_supports_bordure_v2.gcode.3mf" || n3 != "porte_guitare_eric_supports_bordure_v3.gcode.3mf" {
		t.Fatalf("noms : %q %q %q", n1, n2, n3)
	}
	if again := s.depositName(ctx, j2); again != n2 {
		t.Fatalf("le nom d'une même impression doit rester stable : %q puis %q", n2, again)
	}
	// sans modèle : repli sur le nom de fichier de la découpe (comportement d'avant)
	j4, _ := s.createJob(ctx, Job{Name: "boite.gcode.3mf", ArtifactSHA256: strings.Repeat("d", 64)})
	if got := s.depositName(ctx, j4); got != "boite.gcode.3mf" {
		t.Fatalf("repli : %q", got)
	}
}
