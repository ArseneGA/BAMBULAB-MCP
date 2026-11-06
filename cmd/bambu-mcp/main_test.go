package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// safeBuf est un tampon utilisable depuis plusieurs goroutines (stderr).
type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *safeBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// captureOSStdout remplace os.Stdout par un tube : tout ce qui y serait écrit
// (fmt.Println égaré, bibliothèque bavarde…) est récupérable. Le transport MCP,
// lui, passe par les flux injectés, jamais par os.Stdout.
func captureOSStdout(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() { io.Copy(&buf, r); close(done) }()
	return func() string {
		os.Stdout = old
		w.Close()
		<-done
		return buf.String()
	}
}

func TestEmbeddedStdoutIsProtocolOnly(t *testing.T) {
	endCapture := captureOSStdout(t)
	tmp := t.TempDir()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	stderr := &safeBuf{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exit := make(chan int, 1)
	go func() {
		exit <- run(ctx, []string{"--embedded", "--data-dir", filepath.Join(tmp, "data"), "--log-level", "debug"}, inR, outW, stderr)
		outW.Close()
	}()

	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	send := func(s string) { io.WriteString(inW, s+"\n") }
	expect := func(id float64) map[string]any {
		t.Helper()
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("stdout fermé ; stderr=%s", stderr.String())
				}
				var m map[string]any
				if err := json.Unmarshal([]byte(l), &m); err != nil || m["jsonrpc"] != "2.0" {
					t.Fatalf("ligne hors protocole sur stdout : %q", l)
				}
				if m["id"] == id {
					return m
				}
			case <-time.After(20 * time.Second):
				t.Fatalf("délai ; stderr=%s", stderr.String())
			}
		}
	}
	toJSON := func(v any) string { b, _ := json.Marshal(v); return string(b) }

	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	if r := expect(1); r["result"] == nil {
		t.Fatalf("initialize : %v", r)
	}
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if r := expect(2); !strings.Contains(toJSON(r), "get_capabilities") {
		t.Fatalf("tools/list : %v", r)
	}
	send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_capabilities","arguments":{}}}`)
	if r := expect(3); !strings.Contains(toJSON(r), "linux_seul") {
		t.Fatalf("tools/call : %v", r)
	}
	send(`{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"ui://bambu/atelier.html"}}`)
	if r := expect(4); !strings.Contains(toJSON(r), "text/html;profile=mcp-app") {
		t.Fatalf("resources/read : %v", r)
	}

	inW.Close() // fin de session comme le ferait Claude Desktop
	select {
	case code := <-exit:
		if code != 0 {
			t.Fatalf("code de sortie %d ; stderr=%s", code, stderr.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatal("le pont ne s'arrête pas à la fin de stdin")
	}
	if leaked := endCapture(); leaked != "" {
		t.Fatalf("écriture sur os.Stdout : %q", leaked)
	}
	if !strings.Contains(stderr.String(), "level=") {
		t.Fatalf("aucun log structuré sur stderr : %q", stderr.String())
	}
}

func TestBridgeFailsCleanlyWithoutBambud(t *testing.T) {
	endCapture := captureOSStdout(t)
	inR, inW := io.Pipe()
	defer inW.Close()
	outR, outW := io.Pipe()
	go io.Copy(io.Discard, outR)
	stderr := &safeBuf{}
	code := run(context.Background(), []string{"--url", "http://127.0.0.1:1/mcp"}, inR, outW, stderr)
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(stderr.String(), "bambud") {
		t.Fatalf("message d'erreur utile absent : %q", stderr.String())
	}
	if leaked := endCapture(); leaked != "" {
		t.Fatalf("écriture sur os.Stdout : %q", leaked)
	}
}

func TestBadURLAndVersion(t *testing.T) {
	inR, _ := io.Pipe()
	_, outW := io.Pipe()
	stderr := &safeBuf{}
	if code := run(context.Background(), []string{"--url", "ftp://x"}, inR, outW, stderr); code != 1 {
		t.Fatalf("code %d", code)
	}
	stderr = &safeBuf{}
	if code := run(context.Background(), []string{"--version"}, inR, outW, stderr); code != 0 || !strings.Contains(stderr.String(), "bambu-mcp") {
		t.Fatalf("version : %d %q", code, stderr.String())
	}
}
