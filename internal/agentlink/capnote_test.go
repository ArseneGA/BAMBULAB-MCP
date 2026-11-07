package agentlink_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink/agentlinktest"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

func winNote(t *testing.T, reg *agentlink.Registry) (string, bool) {
	t.Helper()
	c := &contracts.Capabilities{Mode: "linux_seul"}
	reg.ApplyCapabilities(c)
	for _, f := range c.Features {
		if f.ID == "windows_agent" {
			return f.Note, f.Available
		}
	}
	t.Fatal("feature windows_agent absente")
	return "", false
}

// Sans agent jamais appairé : « non utilisé » (pas d'alerte) ; avec un agent appairé muet : « hors ligne ».
func TestCapabilitiesWindowsUnusedVsOffline(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{})
	if note, ok := winNote(t, e.Reg); ok || !strings.Contains(note, "non utilisé") || strings.Contains(note, "hors ligne") {
		t.Fatalf("sans agent : %q %v", note, ok)
	}
	tok, _, err := e.Reg.NewPairing(bg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentlink.NewClient(e.Server.URL, "").Pair(bg, agentlink.PairRequest{Token: tok, Name: "PC", AgentVersion: "t"}); err != nil {
		t.Fatal(err)
	}
	e.Clock.Advance(10 * time.Minute)
	if note, ok := winNote(t, e.Reg); ok || !strings.Contains(note, "hors ligne") || strings.Contains(note, "non utilisé") {
		t.Fatalf("agent appairé muet : %q %v", note, ok)
	}
}
