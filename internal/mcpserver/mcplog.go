package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Journal des requêtes MCP entrantes (diagnostic : « le client lit-il la vue après un tools/call ? »).
// Posé en middleware de réception du SDK : aucun handler n'est touché. Ne journalise JAMAIS d'argument d'outil, de
// contenu de résultat, de jeton, de code de lancement ni d'en-tête : seulement des noms, des tailles, des comptes et des
// types. Les messages d'erreur ne sont pas repris (ils pourraient citer une valeur d'argument) : seul le type d'erreur l'est.
// Désactivable : BAMBU_LOG_MCP=off (défaut : actif).

// polledAppTools : outils réservés aux vues, appelés en boucle (images, états, tranches). DEBUG à chaque appel ;
// une seule ligne INFO agrégée par minute et par outil.
var polledAppTools = map[string]bool{
	"get_live_frame":              true,
	"get_live_state":              true,
	"get_launch_view_state":       true,
	"get_print_confirmation_view": true,
	"get_mesh_chunk":              true,
	"get_layer_paths":             true,
}

// mcpLogAggregateEvery : fenêtre de l'agrégat INFO des outils de vue.
const mcpLogAggregateEvery = time.Minute

// mcpLogEnabled : BAMBU_LOG_MCP=off|0|false|no|non désactive le journal.
func mcpLogEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BAMBU_LOG_MCP"))) {
	case "off", "0", "false", "no", "non":
		return false
	}
	return true
}

type polledAgg struct {
	last  time.Time
	count int
}

type mcpLogger struct {
	log *slog.Logger
	now func() time.Time
	mu  sync.Mutex
	agg map[string]*polledAgg
}

// mcpLogMiddleware construit le middleware ; nil si le journal est désactivé ou sans logger.
func mcpLogMiddleware(l *slog.Logger) mcp.Middleware {
	if l == nil || !mcpLogEnabled() {
		return nil
	}
	m := &mcpLogger{log: l, now: time.Now, agg: map[string]*polledAgg{}}
	return m.middleware
}

func shortSession(s mcp.Session) string {
	id := ""
	if s != nil {
		id = s.ID()
	}
	if id == "" {
		return "-"
	}
	if len(id) > 8 {
		id = id[:8]
	}
	return id
}

func errKind(err error) string { return fmt.Sprintf("%T", err) }

func contentKinds(cs []mcp.Content) string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		switch c.(type) {
		case *mcp.TextContent:
			out = append(out, "text")
		case *mcp.ImageContent:
			out = append(out, "image")
		case *mcp.AudioContent:
			out = append(out, "audio")
		case *mcp.ResourceLink:
			out = append(out, "resource_link")
		case *mcp.EmbeddedResource:
			out = append(out, "resource")
		default:
			out = append(out, "autre")
		}
	}
	return strings.Join(out, ",")
}

func contentBytes(cs []mcp.Content) int {
	n := 0
	for _, c := range cs {
		switch v := c.(type) {
		case *mcp.TextContent:
			n += len(v.Text)
		case *mcp.ImageContent:
			n += len(v.Data)
		case *mcp.AudioContent:
			n += len(v.Data)
		}
	}
	return n
}

func (m *mcpLogger) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		switch method {
		case "tools/call", "resources/read", "resources/list", "resources/templates/list", "prompts/list", "prompts/get":
		default:
			return next(ctx, method, req)
		}
		start := m.now()
		res, err := next(ctx, method, req)
		ms := m.now().Sub(start).Milliseconds()
		sess := shortSession(req.GetSession())

		switch method {
		case "tools/call":
			m.toolCall(req, res, err, sess, ms)
		case "resources/read":
			uri := ""
			if p, ok := req.GetParams().(*mcp.ReadResourceParams); ok && p != nil {
				uri = p.URI
				if i := strings.IndexAny(uri, "?#"); i >= 0 {
					uri = uri[:i]
				}
			}
			attrs := []any{"uri", uri, "session", sess, "duration_ms", ms, "ok", err == nil}
			if r, ok := res.(*mcp.ReadResourceResult); ok && r != nil {
				size := 0
				var mimes []string
				for _, c := range r.Contents {
					if c == nil {
						continue
					}
					size += len(c.Text) + len(c.Blob)
					mimes = append(mimes, c.MIMEType)
				}
				attrs = append(attrs, "mime", strings.Join(mimes, ","), "bytes", size, "contents", len(r.Contents))
			}
			if err != nil {
				attrs = append(attrs, "error_type", errKind(err))
			}
			m.log.Info("MCP resources/read", attrs...)
		case "resources/list":
			n := -1
			if r, ok := res.(*mcp.ListResourcesResult); ok && r != nil {
				n = len(r.Resources)
			}
			m.listLine(method, n, sess, ms, err)
		case "resources/templates/list":
			n := -1
			if r, ok := res.(*mcp.ListResourceTemplatesResult); ok && r != nil {
				n = len(r.ResourceTemplates)
			}
			m.listLine(method, n, sess, ms, err)
		case "prompts/list":
			n := -1
			if r, ok := res.(*mcp.ListPromptsResult); ok && r != nil {
				n = len(r.Prompts)
			}
			m.listLine(method, n, sess, ms, err)
		case "prompts/get":
			name := ""
			if p, ok := req.GetParams().(*mcp.GetPromptParams); ok && p != nil {
				name = p.Name // le nom seulement, jamais les arguments
			}
			attrs := []any{"prompt", name, "session", sess, "duration_ms", ms, "ok", err == nil}
			if err != nil {
				attrs = append(attrs, "error_type", errKind(err))
			}
			m.log.Info("MCP prompts/get", attrs...)
		}
		return res, err
	}
}

func (m *mcpLogger) listLine(method string, n int, sess string, ms int64, err error) {
	attrs := []any{"count", n, "session", sess, "duration_ms", ms, "ok", err == nil}
	if err != nil {
		attrs = append(attrs, "error_type", errKind(err))
	}
	m.log.Info("MCP "+method, attrs...)
}

func (m *mcpLogger) toolCall(req mcp.Request, res mcp.Result, err error, sess string, ms int64) {
	name := ""
	switch p := req.GetParams().(type) {
	case *mcp.CallToolParamsRaw:
		if p != nil {
			name = p.Name // le nom seulement : jamais les arguments
		}
	case *mcp.CallToolParams:
		if p != nil {
			name = p.Name
		}
	}
	ok := err == nil
	metaUI := false
	attrs := []any{"tool", name, "session", sess, "duration_ms", ms}
	var tail []any
	if r, isRes := res.(*mcp.CallToolResult); isRes && r != nil {
		_, metaUI = r.Meta["ui"]
		if !metaUI {
			_, metaUI = r.Meta["ui/resourceUri"]
		}
		ok = ok && !r.IsError
		tail = append(tail, "bytes", contentBytes(r.Content), "blocks", len(r.Content), "block_types", contentKinds(r.Content))
	}
	attrs = append(attrs, "ok", ok, "meta_ui", metaUI)
	attrs = append(attrs, tail...)
	if err != nil {
		attrs = append(attrs, "error_type", errKind(err))
	}
	if !polledAppTools[name] {
		m.log.Info("MCP tools/call", attrs...)
		return
	}
	// Outil de vue appelé en boucle : DEBUG à chaque appel, INFO agrégé au plus une fois par minute.
	m.log.Debug("MCP tools/call", attrs...)
	m.mu.Lock()
	a := m.agg[name]
	if a == nil {
		a = &polledAgg{}
		m.agg[name] = a
	}
	a.count++
	now := m.now()
	flush := a.last.IsZero() || now.Sub(a.last) >= mcpLogAggregateEvery
	n := a.count
	if flush {
		a.last, a.count = now, 0
	}
	m.mu.Unlock()
	if flush {
		m.log.Info("MCP tools/call (outil de vue, agrégé)", "tool", name, "session", sess, "calls", n,
			"window", mcpLogAggregateEvery.String(), "last_duration_ms", ms, "last_ok", ok, "last_meta_ui", metaUI)
	}
}
