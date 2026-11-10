package issues

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func newSvc(t *testing.T) (*Service, *time.Time) {
	t.Helper()
	clock := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), store.Options{Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	return s, &clock
}

func TestFingerprintNormalizes(t *testing.T) {
	a := Fingerprint("get_print_status", "Le job pj_3f9a1c2b4d5e reste à 42 % : état périmé !")
	b := Fingerprint(" Get_Print_Status ", "le job pj_77b0aa11cc22 reste a 87 %  etat perime")
	if a != b {
		t.Errorf("même symptôme, empreintes différentes :\n%q\n%q", a, b)
	}
	if Fingerprint("list_printers", "x") == Fingerprint("get_printer_status", "x") {
		t.Error("outils différents : empreintes identiques")
	}
	if Fingerprint("t", "progression bloquée") == Fingerprint("t", "progression absente") {
		t.Error("symptômes différents : empreintes identiques")
	}
}

func TestReportCreatesThenDeduplicates(t *testing.T) {
	ctx := context.Background()
	s, clock := newSvc(t)
	o, err := s.Report(ctx, ReportInput{Title: "Progression figée à 42 %", Tool: "get_print_status", Severity: "P2", Observed: "toujours 42 %", PrintJobID: "pj_aaaaaa111111"})
	if err != nil || !o.Created || o.Issue.Occurrences != 1 || o.Issue.State != StateNew || !strings.HasPrefix(o.Issue.ID, "iss_") {
		t.Fatalf("création : %+v %v", o, err)
	}
	*clock = clock.Add(time.Hour)
	o2, err := s.Report(ctx, ReportInput{Title: "progression figée à 87 %", Tool: "get_print_status", Severity: "P1", PrinterID: "pr_1", Expected: "elle avance"})
	if err != nil || o2.Created || o2.Issue.ID != o.Issue.ID || o2.Issue.Occurrences != 2 {
		t.Fatalf("doublon : %+v %v", o2, err)
	}
	if o2.Issue.Severity != "P1" || o2.Issue.PrinterID != "pr_1" || o2.Issue.PrintJobID != "pj_aaaaaa111111" || o2.Issue.Expected != "elle avance" || o2.Issue.Observed != "toujours 42 %" {
		t.Errorf("fusion des champs : %+v", o2.Issue)
	}
	if !o2.Issue.LastSeenAt.After(o2.Issue.CreatedAt) || !strings.Contains(o2.Note, "2 occurrence") {
		t.Errorf("dates ou note : %+v / %s", o2.Issue, o2.Note)
	}
	// une gravité moindre ne rétrograde pas
	o3, _ := s.Report(ctx, ReportInput{Title: "Progression figée", Tool: "get_print_status", Severity: "P3"})
	if o3.Created {
		t.Log("titre différent : nouvelle ligne attendue")
	} else {
		t.Error("« Progression figée » (sans le nombre) ne doit pas être confondu avec « Progression figée à # % »")
	}
	l, err := s.List(ctx, ListQuery{})
	if err != nil || l.Total != 2 || l.Issues[0].Severity != "P1" {
		t.Fatalf("liste : %+v %v", l, err)
	}
	// même titre sur un autre outil = autre constat
	if o4, _ := s.Report(ctx, ReportInput{Title: "Progression figée à 10 %", Tool: "get_printer_status"}); !o4.Created {
		t.Error("autre outil : nouvelle ligne attendue")
	}
}

func TestReportValidation(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)
	for _, in := range []ReportInput{{}, {Title: "   "}, {Title: "x", Severity: "P9"}} {
		if _, err := s.Report(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v : %v", in, err)
		}
	}
	o, err := s.Report(ctx, ReportInput{Title: strings.Repeat("é", 500), Observed: strings.Repeat("x", 5000)})
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(o.Issue.Title)); n > maxTitle || len([]rune(o.Issue.Observed)) > maxText || o.Issue.Severity != "P2" {
		t.Errorf("bornes : %d %d %s", n, len([]rune(o.Issue.Observed)), o.Issue.Severity)
	}
	if o, _ := s.Report(ctx, ReportInput{Title: "minuscule", Severity: "p0"}); o.Issue.Severity != "P0" {
		t.Errorf("gravité en minuscules : %s", o.Issue.Severity)
	}
}

func TestReportRedactsSecretsBeforeStorage(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)
	tok := "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcde"
	o, err := s.Report(ctx, ReportInput{
		Title:    "create_upload_link renvoie un lien refusé",
		Tool:     "create_upload_link",
		Expected: "PUT http://nas:7878/upload/" + tok + " accepté",
		Observed: "code de lancement 123456 saisi ; access_code=87654321 ; Authorization: Bearer abcdef123456",
	})
	if err != nil || !o.Redacted || !strings.Contains(o.Note, "masqués") {
		t.Fatalf("%+v %v", o, err)
	}
	// ce qui est stocké est déjà propre : on relit directement la base
	var exp, obs string
	if err := s.st.DB().QueryRow(`SELECT expected, observed FROM issues WHERE id=?`, o.Issue.ID).Scan(&exp, &obs); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{tok, "123456", "87654321", "abcdef123456"} {
		if strings.Contains(exp+obs, bad) {
			t.Errorf("secret %q stocké : %s | %s", bad, exp, obs)
		}
	}
	// un titre ne doit pas non plus conserver de secret
	o2, _ := s.Report(ctx, ReportInput{Title: "jeton token=zzzz9999 refusé"})
	if strings.Contains(o2.Issue.Title, "zzzz9999") {
		t.Errorf("titre : %s", o2.Issue.Title)
	}
	// ni l'export
	md, _, _ := s.ExportMarkdown(ctx, ListQuery{State: "all"})
	for _, bad := range []string{tok, "123456", "87654321", "zzzz9999"} {
		if strings.Contains(md, bad) {
			t.Errorf("secret %q dans l'export", bad)
		}
	}
	// une pièce propre ne signale rien
	if o, _ := s.Report(ctx, ReportInput{Title: "ras", Observed: "progression 42 %, buse 220 °C"}); o.Redacted {
		t.Error("faux positif de masquage")
	}
}

func TestUpdateLifecycleAndRegression(t *testing.T) {
	ctx := context.Background()
	s, clock := newSvc(t)
	o, _ := s.Report(ctx, ReportInput{Title: "Fausse alerte de bobine vide", Tool: "list_spools", Severity: "P1"})
	id := o.Issue.ID

	if _, err := s.Update(ctx, UpdateInput{ID: id}); !errors.Is(err, ErrInvalid) {
		t.Errorf("sans changement : %v", err)
	}
	if _, err := s.Update(ctx, UpdateInput{ID: id, State: "fixed"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("fixed sans commit : %v", err)
	}
	if _, err := s.Update(ctx, UpdateInput{ID: id, State: "seen", FixCommit: "abc1234"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("commit hors fixed : %v", err)
	}
	if _, err := s.Update(ctx, UpdateInput{ID: id, State: "fixed", FixCommit: "pas de commit ici"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("commit invalide : %v", err)
	}
	if _, err := s.Update(ctx, UpdateInput{ID: id, State: "fixed", FixCommit: "token=abcdef12"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("commit portant un secret : %v", err)
	}
	if _, err := s.Update(ctx, UpdateInput{ID: id, State: "bof"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("état inconnu : %v", err)
	}
	if _, err := s.Update(ctx, UpdateInput{ID: "iss_absent", State: "seen"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("introuvable : %v", err)
	}

	i, err := s.Update(ctx, UpdateInput{ID: id, State: "seen"})
	if err != nil || i.State != StateSeen {
		t.Fatalf("seen : %+v %v", i, err)
	}
	i, err = s.Update(ctx, UpdateInput{ID: id, State: "fixed", FixCommit: "3f9a1c2"})
	if err != nil || i.State != StateFixed || i.FixCommit != "3f9a1c2" {
		t.Fatalf("fixed : %+v %v", i, err)
	}
	if l, _ := s.List(ctx, ListQuery{}); l.Total != 0 || l.Counts[StateFixed] != 1 {
		t.Errorf("un constat corrigé n'est plus ouvert : %+v", l)
	}
	// le symptôme revient : régression, rouvert
	*clock = clock.Add(24 * time.Hour)
	r, err := s.Report(ctx, ReportInput{Title: "fausse alerte de bobine vide", Tool: "list_spools"})
	if err != nil || r.Created || !r.Reopened || r.Issue.State != StateNew || r.Issue.Regressions != 1 || r.Issue.Occurrences != 2 {
		t.Fatalf("régression : %+v %v", r, err)
	}
	// écarté : le compteur monte mais l'état reste
	if _, err := s.Update(ctx, UpdateInput{ID: id, State: "dismissed"}); err != nil {
		t.Fatal(err)
	}
	r, _ = s.Report(ctx, ReportInput{Title: "fausse alerte de bobine vide", Tool: "list_spools"})
	if r.Issue.State != StateDismissed || r.Issue.Occurrences != 3 || r.Reopened {
		t.Errorf("écarté : %+v", r.Issue)
	}
	// gravité seule
	if i, err := s.Update(ctx, UpdateInput{ID: id, Severity: "P3"}); err != nil || i.Severity != "P3" || i.State != StateDismissed {
		t.Errorf("gravité : %+v %v", i, err)
	}
}

func TestListFiltersAndOrder(t *testing.T) {
	ctx := context.Background()
	s, clock := newSvc(t)
	add := func(title, tool, sev string, times int) string {
		var id string
		for i := 0; i < times; i++ {
			*clock = clock.Add(time.Minute)
			o, err := s.Report(ctx, ReportInput{Title: title, Tool: tool, Severity: sev})
			if err != nil {
				t.Fatal(err)
			}
			id = o.Issue.ID
		}
		return id
	}
	add("alpha", "a", "P2", 1)
	add("bravo", "b", "P2", 3)
	p0 := add("charlie", "a", "P0", 1)
	done := add("delta", "c", "P3", 1)
	if _, err := s.Update(ctx, UpdateInput{ID: done, State: "fixed", FixCommit: "abc1234"}); err != nil {
		t.Fatal(err)
	}
	l, _ := s.List(ctx, ListQuery{})
	if l.Total != 3 || l.Issues[0].ID != p0 || l.Issues[1].Title != "bravo" || l.Issues[2].Title != "alpha" {
		t.Errorf("ordre grave puis fréquent : %+v", l.Issues)
	}
	if l.Counts[StateNew] != 3 || l.Counts[StateFixed] != 1 || l.Counts[StateDismissed] != 0 {
		t.Errorf("compteurs : %v", l.Counts)
	}
	if l, _ := s.List(ctx, ListQuery{State: "all"}); l.Total != 4 {
		t.Errorf("all : %d", l.Total)
	}
	if l, _ := s.List(ctx, ListQuery{State: "fixed"}); l.Total != 1 || l.Issues[0].Title != "delta" {
		t.Errorf("fixed : %+v", l)
	}
	if l, _ := s.List(ctx, ListQuery{Severity: "p2"}); l.Total != 2 {
		t.Errorf("gravité : %d", l.Total)
	}
	if l, _ := s.List(ctx, ListQuery{Tool: "a"}); l.Total != 2 {
		t.Errorf("outil : %d", l.Total)
	}
	if l, _ := s.List(ctx, ListQuery{Limit: 1, Offset: 1}); len(l.Issues) != 1 || l.Total != 3 || l.Issues[0].Title != "bravo" {
		t.Errorf("pagination : %+v", l)
	}
	if _, err := s.List(ctx, ListQuery{State: "nope"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("état inconnu : %v", err)
	}
	if _, err := s.List(ctx, ListQuery{Severity: "P7"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("gravité inconnue : %v", err)
	}
	if open, urgent, err := s.OpenCounts(ctx); err != nil || open != 3 || urgent != 1 {
		t.Errorf("OpenCounts : %d %d %v", open, urgent, err)
	}
	if g, err := s.Get(ctx, p0); err != nil || g.Title != "charlie" {
		t.Errorf("Get : %+v %v", g, err)
	}
	if _, err := s.Get(ctx, "iss_none"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get absent : %v", err)
	}
}

func TestExportMarkdown(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)
	md, n, err := s.ExportMarkdown(ctx, ListQuery{})
	if err != nil || n != 0 || !strings.Contains(md, "Aucun constat") {
		t.Fatalf("vide : %q %v", md, err)
	}
	o, _ := s.Report(ctx, ReportInput{Title: "Réponse trop lourde", Tool: "list_print_log", Severity: "P1", Expected: "10 lignes", Observed: "ligne 1\nligne 2", PrintJobID: "pj_1", PrinterID: "pr_1"})
	_, _ = s.Report(ctx, ReportInput{Title: "Détail d'affichage", Tool: "show_atelier", Severity: "P3"})
	_, _ = s.Report(ctx, ReportInput{Title: "Réponse trop lourde", Tool: "list_print_log"})
	md, n, err = s.ExportMarkdown(ctx, ListQuery{})
	if err != nil || n != 2 {
		t.Fatalf("export : %d %v", n, err)
	}
	for _, want := range []string{"# Dysfonctionnements de Printalk", "update_issue", "## P1", "## P3", "### Réponse trop lourde `" + o.Issue.ID + "`", "Outil : `list_print_log`",
		"Occurrences : 2", "Impression : `pj_1`", "Imprimante : `pr_1`", "Attendu : 10 lignes", "ligne 1\n  ligne 2", "État : nouveau"} {
		if !strings.Contains(md, want) {
			t.Errorf("export sans %q :\n%s", want, md)
		}
	}
	if strings.Index(md, "## P1") > strings.Index(md, "## P3") || strings.Contains(md, "## P0") {
		t.Error("ordre ou sections inutiles")
	}
	if md, n, _ := s.ExportMarkdown(ctx, ListQuery{Severity: "P3"}); n != 1 || !strings.Contains(md, "gravité P3") || strings.Contains(md, "Réponse trop lourde") {
		t.Errorf("filtre : %d\n%s", n, md)
	}
}

func TestMigrationIsAdditive(t *testing.T) {
	s, _ := newSvc(t)
	v, err := s.st.SchemaVersion(context.Background())
	if err != nil || v < 290 {
		t.Fatalf("version de schéma %d : %v", v, err)
	}
	if store.LatestSchemaVersion() != v {
		t.Errorf("base %d, binaire %d", v, store.LatestSchemaVersion())
	}
}
