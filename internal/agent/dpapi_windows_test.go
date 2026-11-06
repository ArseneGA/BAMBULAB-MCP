//go:build windows

package agent_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agent"
)

func TestDPAPIRoundTrip(t *testing.T) {
	p := agent.NewProtector()
	for _, machine := range []bool{false, true} {
		plain := []byte("agt_ab12.s3cr3t-très-secret-0123456789abcdef")
		blob, err := p.Protect(plain, machine)
		if err != nil {
			t.Fatalf("Protect(machine=%v) : %v", machine, err)
		}
		if bytes.Contains(blob, []byte("s3cr3t")) || bytes.Contains(blob, plain) {
			t.Fatal("le blob DPAPI contient le secret en clair")
		}
		back, err := p.Unprotect(blob, machine)
		if err != nil || !bytes.Equal(back, plain) {
			t.Fatalf("Unprotect(machine=%v) : %q %v", machine, back, err)
		}
		// Blob altéré : refusé.
		bad := append([]byte(nil), blob...)
		bad[len(bad)/2] ^= 0xff
		if _, err := p.Unprotect(bad, machine); err == nil {
			t.Fatal("blob altéré accepté")
		}
	}
	if _, err := p.Unprotect(nil, false); err == nil {
		t.Fatal("blob vide accepté")
	}
	if _, err := p.Unprotect([]byte("pas un blob dpapi"), false); err == nil {
		t.Fatal("blob arbitraire accepté")
	}
}

func TestDPAPIWithConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")
	cfg, _ := agent.LoadConfig(path)
	cfg.ServerURL = "http://srv:7878"
	secret := "agt_9.TOPSECRETvalue1234567890"
	if err := cfg.SetSecret(agent.NewProtector(), "agt_9", secret); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "TOPSECRET") {
		t.Fatalf("secret en clair dans agent.json : %s", raw)
	}
	back, _ := agent.LoadConfig(path)
	got, err := back.Secret(agent.NewProtector())
	if err != nil || got != secret {
		t.Fatalf("relecture DPAPI : %q %v", got, err)
	}
}
