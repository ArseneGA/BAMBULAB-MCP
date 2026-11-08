package printing

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// En mode agent, le serveur n'a pas accès au réseau de l'imprimante : les fonctions qui s'y appuient refusent avec un
// message clair (jamais une erreur de connexion obscure).
func TestRecoveryFunctionsRefuseCleanlyInAgentMode(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	rec := PrinterRecord{ID: "prn_agent", ModelID: "p1s", Name: "Atelier", Host: "192.168.0.12", Serial: "01P00A000000001", Link: "agent", AgentID: "agt_1"}
	if err := s.d.Store.PutDoc(ctx, actor, "printers", rec.ID, rec); err != nil {
		t.Fatal(err)
	}
	_, err := s.DiagnoseConnection(ctx, rec.ID)
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "agent local") {
		t.Fatalf("diagnostic en mode agent : %v", err)
	}
	if e := s.unsupported(ctx, rec.ID, "le chargement de la bobine externe"); !strings.Contains(e.Error(), "agent local") || !strings.Contains(e.Error(), "chargement") {
		t.Fatalf("message : %v", e)
	}
}
