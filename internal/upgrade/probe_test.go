package upgrade

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// La sonde de repli (base seule) et le service doivent compter les mêmes états d'impression comme « occupé ».
func TestProbeStatesMatchServiceStates(t *testing.T) {
	a := append([]string(nil), ProbeBusyPrintStates...)
	b := printing.ActiveJobStates()
	sort.Strings(a)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("sonde %v, service %v : à resynchroniser", a, b)
	}
}

func TestProbeDB(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "bambu.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p, err := ProbeDB(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.SchemaVersion != store.LatestSchemaVersion() || p.Busy || len(p.Reasons) != 0 {
		t.Fatalf("base neuve : %+v", p)
	}

	// États de repos : jamais occupé.
	for i, s := range []contracts.PrintState{contracts.PrintDraft, contracts.PrintUploaded, contracts.PrintCompleted, contracts.PrintFailed, contracts.PrintCancelled} {
		id := "idle" + string(rune('a'+i))
		if err := st.PutDoc(ctx, "test", "print_jobs", id, printing.Job{ID: id, State: s}); err != nil {
			t.Fatal(err)
		}
	}
	if p, _ = ProbeDB(ctx, dir); p.Busy {
		t.Fatalf("états de repos : %+v", p)
	}

	// Chaque état actif est vu.
	for _, s := range ProbeBusyPrintStates {
		id := "act-" + strings.ToLower(s)
		if err := st.PutDoc(ctx, "test", "print_jobs", id, printing.Job{ID: id, State: contracts.PrintState(s)}); err != nil {
			t.Fatal(err)
		}
		p, err = ProbeDB(ctx, dir)
		if err != nil || !p.Busy {
			t.Fatalf("%s : %+v %v", s, p, err)
		}
		_ = st.DB().QueryRow(`DELETE FROM print_jobs WHERE id=? RETURNING id`, id).Scan(new(string))
	}
	if p, _ = ProbeDB(ctx, dir); p.Busy {
		t.Fatalf("après nettoyage : %+v", p)
	}

	// Travail en cours.
	q := jobs.NewQueue(st, jobs.QueueConfig{})
	if _, _, err := q.Enqueue(ctx, jobs.EnqueueParams{Kind: "system.selftest", Class: contracts.ClassHeavy}); err != nil {
		t.Fatal(err)
	}
	if p, _ = ProbeDB(ctx, dir); p.Busy {
		t.Fatalf("travail en file seulement : %+v", p)
	}
	if j, err := q.Claim(ctx, "w"); err != nil || j == nil {
		t.Fatal(err)
	}
	if p, _ = ProbeDB(ctx, dir); !p.Busy || !strings.Contains(p.Reasons[0], "job_running") {
		t.Fatalf("travail en cours : %+v", p)
	}
}

// La sonde ne modifie rien et ne migre pas : une base plus récente que ce binaire reste lisible.
func TestProbeDBIsReadOnly(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "bambu.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO schema_version(version,name,applied_at) VALUES(9999,'futur','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	st.Close()
	before, _ := os.ReadFile(filepath.Join(dir, "bambu.db"))
	p, err := ProbeDB(ctx, dir)
	if err != nil || p.SchemaVersion != 9999 {
		t.Fatalf("base plus récente que le binaire : %+v %v", p, err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "bambu.db"))
	if string(before) != string(after) {
		t.Fatal("la sonde a modifié la base")
	}
	if _, err := ProbeDB(ctx, t.TempDir()); err == nil {
		t.Fatal("base absente : erreur attendue")
	}
}

func TestParseProbe(t *testing.T) {
	p, err := ParseProbe("bruit\n{\"schema_version\":270,\"busy\":true,\"reasons\":[\"x\"]}\n")
	if err != nil || p.SchemaVersion != 270 || !p.Busy {
		t.Fatalf("%+v %v", p, err)
	}
	for _, bad := range []string{"", "pas de json", "{"} {
		if _, err := ParseProbe(bad); err == nil {
			t.Errorf("%q accepté", bad)
		}
	}
}
