package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

type echoIn struct{}
type echoOut struct {
	Announced bool   `json:"announced"`
	Client    string `json:"client"`
}

// Le serveur lit, dans l'`initialize` REÇU, l'annonce des vues MCP Apps (io.modelcontextprotocol/ui + mimeTypes).
func TestClientViewSupportReadsInitialize(t *testing.T) {
	for name, tc := range map[string]struct {
		caps *mcp.ClientCapabilities
		want bool
	}{
		"aucune extension": {&mcp.ClientCapabilities{}, false},
		"extension sans mimeTypes": {func() *mcp.ClientCapabilities {
			c := &mcp.ClientCapabilities{}
			c.AddExtension(uiExtensionID, nil)
			return c
		}(), false},
		"extension avec le MIME mcp-app": {func() *mcp.ClientCapabilities {
			c := &mcp.ClientCapabilities{}
			c.AddExtension(uiExtensionID, map[string]any{"mimeTypes": []any{AppMIMEType}})
			return c
		}(), true},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
			mcp.AddTool(srv, &mcp.Tool{Name: "echo", Description: "d"}, func(_ context.Context, req *mcp.CallToolRequest, _ echoIn) (*mcp.CallToolResult, echoOut, error) {
				a, c := clientViewSupport(req.Session)
				return nil, echoOut{Announced: a, Client: c}, nil
			})
			ct, st := mcp.NewInMemoryTransports()
			ss, err := srv.Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "claude-ai", Version: "9"}, &mcp.ClientOptions{Capabilities: tc.caps}).Connect(ctx, ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{}})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(res.StructuredContent)
			var out echoOut
			_ = json.Unmarshal(raw, &out)
			if out.Announced != tc.want || out.Client != "claude-ai 9" {
				t.Fatalf("annonce = %+v, attendu %v", out, tc.want)
			}
		})
	}
}

// Texte du résultat : court, avec le lien et le résumé ; le mode dégradé met le lien en avant ; jamais de jeton.
func TestConfirmationTextModes(t *testing.T) {
	v := &printing.ConfirmationView{ConfirmationID: "cf_x", ConfirmURL: "https://atelier.ts.net/confirm/cf_x", ExpiresAtLocal: "21:03 (08/11/2026, Europe/Paris)",
		Binding: printing.PromptDetails{File: "piece.gcode.3mf", PrinterName: "Atelier", Plate: 1, SHA256: strings.Repeat("a", 64), BedLeveling: true,
			Adhesion: []string{"Risque de décollement ÉLEVÉ : pièce de 204 mm"}}}
	deg := confirmationText(v, "https://atelier.ts.net", false)
	for _, want := range []string{"Ce client n'a pas annoncé", "https://atelier.ts.net/confirm/cf_x", "21:03 (08/11/2026, Europe/Paris)", "mise à niveau du plateau : oui", "AVERTISSEMENT", "n'appelle PAS start_print", "JAMAIS son code"} {
		if !strings.Contains(deg, want) {
			t.Errorf("texte dégradé sans %q : %s", want, deg)
		}
	}
	ok := confirmationText(v, "https://atelier.ts.net", true)
	if !strings.Contains(ok, "s'affiche dans la conversation") || strings.Contains(ok, "Ce client n'a pas annoncé") {
		t.Errorf("texte avec vues : %s", ok)
	}
	v.Binding.BedLeveling = false
	if !strings.Contains(confirmationText(v, "", true), "mise à niveau du plateau : NON") {
		t.Error("la désactivation de la mise à niveau doit être dite")
	}
}

// Le contenu structuré de la confirmation reste court : pas de consignes longues dedans (elles sont dans le texte).
func TestConfirmationStructuredContentHasNoInstructions(t *testing.T) {
	raw, _ := json.Marshal(printing.ConfirmationView{ConfirmationID: "cf_x"})
	if strings.Contains(string(raw), "instructions") || strings.Contains(string(raw), "start_print") {
		t.Fatalf("contenu structuré trop bavard : %s", raw)
	}
}
