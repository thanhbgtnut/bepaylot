// Package testkit wires every module against a real Postgres
// (TEST_DATABASE_URL) with in-memory object storage, the inline queue, the
// WebAssembly renderer, an OCR engine that echoes nothing (the PDF text layer
// fills pages in) and a scriptable LLM. Only tests import it.
package testkit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/document"
	"github.com/thanhenti/bepaylot/internal/application/service/graph"
	"github.com/thanhenti/bepaylot/internal/application/service/index"
	"github.com/thanhenti/bepaylot/internal/application/service/wiki"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/parser"
	"github.com/thanhenti/bepaylot/internal/parser/pdf"
	"github.com/thanhenti/bepaylot/internal/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/storage"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Harness bundles wired services for a test.
type Harness struct {
	T       *testing.T
	Ctx     context.Context
	Store   *postgres.Store
	Config  *config.Config
	Queue   *queue.Inline
	Objects *storage.Memory
	Docs    *document.Service
	Index   *index.Service
	Graph   *graph.Service
	Wiki    *wiki.Service
	LLM     *ScriptLLM
	Owner   types.User
}

var (
	rendererOnce sync.Once
	renderer     *pdf.Renderer
	rendererErr  error
)

// New builds a harness or skips the test when TEST_DATABASE_URL is unset.
func New(t *testing.T, tweak ...func(*config.Config)) *Harness {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" || testing.Short() {
		t.Skip("set TEST_DATABASE_URL to run integration tests")
	}
	ctx := context.Background()
	st, err := postgres.Open(ctx, config.DB{DSN: dsn, MaxConns: 8, MinConns: 1, AutoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	user, err := st.Users.Create(ctx, fmt.Sprintf("kit-%s@bepaylot.local", uuid.NewString()), "kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user.ID) })

	cfg := config.Defaults()
	cfg.Parser.DefaultEngine = "echo"
	cfg.Parser.Render.DPI = 72
	cfg.Graph.SchemaDir = findSchemaDir()
	cfg.Wiki.MinMentions = 1
	cfg.Wiki.IngestDelay = time.Millisecond
	for _, f := range tweak {
		f(cfg)
	}
	rendererOnce.Do(func() {
		renderer, rendererErr = pdf.New(pdf.Config{Mode: "webassembly", Workers: 1, PageTimeout: time.Minute})
	})
	if rendererErr != nil {
		t.Fatal(rendererErr)
	}
	objects := storage.NewMemory()
	files, err := storage.NewFileCache(t.TempDir(), 1<<30, objects)
	if err != nil {
		t.Fatal(err)
	}
	engines := parser.NewRegistry("echo")
	engines.Register(echoOCR{})
	q := queue.NewInline()
	ai := NewScriptLLM()

	docs := document.New(document.Deps{Store: st, Objects: objects, Files: files, Queue: q, Renderer: renderer, Engines: engines, Config: cfg})
	idx := index.New(index.Deps{Store: st, Docs: docs, Queue: q, TreeLLM: ai, SearchLLM: ai, Config: cfg})
	gr := graph.New(graph.Deps{Store: st, Docs: docs, Sections: idx, Queue: q, LLM: ai, Config: cfg})
	wk := wiki.New(st, q, nil, cfg, nil)
	if err := gr.EnsureSchemas(ctx); err != nil {
		t.Fatal(err)
	}
	handlers := map[string]queue.Handler{}
	for _, m := range []map[string]queue.Handler{docs.Handlers(), idx.Handlers(), gr.Handlers(), wk.Handlers()} {
		for k, v := range m {
			handlers[k] = v
		}
	}
	q.Register(handlers, func(ctx context.Context, taskType, queueName string, payload []byte, err error, attempts int) {
		_ = st.Tasks.InsertDeadLetter(ctx, postgres.DeadLetter{TaskType: taskType, Queue: queueName, Scope: "test", ScopeID: "test", Payload: payload, LastError: err.Error(), FailCount: attempts})
	})
	return &Harness{T: t, Ctx: ctx, Store: st, Config: cfg, Queue: q, Objects: objects, Docs: docs, Index: idx, Graph: gr, Wiki: wk, LLM: ai, Owner: user}
}

func findSchemaDir() string {
	for _, p := range []string{"configs/graph_schemas", "../configs/graph_schemas", "../../configs/graph_schemas", "../../../configs/graph_schemas", "../../../../configs/graph_schemas"} {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return ""
}

// KB creates a knowledge base.
func (h *Harness) KB(cfg types.KBConfig, schema *types.MetadataSchema) types.KnowledgeBase {
	h.T.Helper()
	kb, err := h.Docs.CreateKB(h.Ctx, h.Owner.ID, "kb-"+uuid.NewString()[:8], "", cfg, schema, false)
	if err != nil {
		h.T.Fatal(err)
	}
	return kb
}

// Upload uploads a generated PDF and returns its document id.
func (h *Harness) Upload(kb uuid.UUID, name string, meta map[string]any, pages []pdftest.Page) uuid.UUID {
	h.T.Helper()
	up, err := h.Docs.BeginUpload(h.Ctx, h.Owner.ID, kb, false)
	if err != nil {
		h.T.Fatal(err)
	}
	up.SetSharedMetadata(meta)
	if err := up.AddFile(h.Ctx, name, "", bytes.NewReader(pdftest.Build(pages, pdftest.Options{Outline: true}))); err != nil {
		h.T.Fatal(err)
	}
	res, err := up.Finish(h.Ctx)
	if err != nil || len(res.Documents) != 1 {
		h.T.Fatalf("upload %s: %+v %v", name, res, err)
	}
	return res.Documents[0].DocumentID
}

// Drain runs queued tasks and fails the test on task failures.
func (h *Harness) Drain() {
	h.T.Helper()
	if err := h.Queue.Drain(h.Ctx, 2000); err != nil {
		h.T.Fatal(err)
	}
	if len(h.Queue.Failed) > 0 {
		h.T.Fatalf("failed tasks: %v", h.Queue.Failed)
	}
}

// echoOCR returns no OCR lines, so pages are built from the text layer.
type echoOCR struct{}

func (echoOCR) Name() string                 { return "echo" }
func (echoOCR) Health(context.Context) error { return nil }
func (echoOCR) ParsePage(_ context.Context, in parser.PageImage, _ parser.PageOptions) (*parser.RawPage, error) {
	return &parser.RawPage{Width: in.Width, Height: in.Height, Raw: []byte("{}")}, nil
}

// ScriptLLM answers the index/search prompts deterministically; tests set
// Extract to script graph extraction. It counts calls per prompt kind.
type ScriptLLM struct {
	mu      sync.Mutex
	Calls   map[string]int
	Extract func(user string) any
}

// NewScriptLLM returns a script with default behaviour.
func NewScriptLLM() *ScriptLLM { return &ScriptLLM{Calls: map[string]int{}} }

var (
	lineRe     = regexp.MustCompile(`\[L(\d+)\] (.*)`)
	pageRe     = regexp.MustCompile(`<page n="(\d+)" doc="(d\d+)"`)
	nodeRe     = regexp.MustCompile(`\[(n\d+)\] `)
	partRe     = regexp.MustCompile(`<part id="(n\d+)"`)
	questionRe = regexp.MustCompile(`Question: (.*)`)
)

// CompleteJSON implements interfaces.Completer.
func (s *ScriptLLM) CompleteJSON(_ context.Context, system, user string, out any) error {
	s.mu.Lock()
	kind := "other"
	var reply any
	switch {
	case strings.Contains(system, "summarize parts"):
		kind = "summarize"
		m := map[string]string{}
		for _, x := range partRe.FindAllStringSubmatch(user, -1) {
			m[x[1]] = "Tóm tắt " + x[1]
		}
		reply = map[string]any{"summaries": m}
	case strings.Contains(system, "catalogue card"):
		kind = "card"
		reply = map[string]any{"title": "Tài liệu kiểm thử", "doc_type": "Kiểm thử", "summary": "Tài liệu dùng cho kiểm thử"}
	case strings.Contains(system, "table of contents for a document"):
		kind = "toc"
		reply = map[string]any{"toc": []any{}}
	case strings.Contains(system, "pick which documents"):
		kind = "select_docs"
		reply = map[string]any{"select": []map[string]string{{"doc": "d1"}}}
	case strings.Contains(system, "navigate"):
		kind = "select_nodes"
		var sel []map[string]string
		for _, m := range nodeRe.FindAllStringSubmatch(user, -1) {
			sel = append(sel, map[string]string{"node_id": m[1]})
		}
		reply = map[string]any{"select": sel, "answerable": true}
	case strings.Contains(system, "exact lines"):
		kind = "locate"
		q := strings.Fields(textutil.Normalize(questionRe.FindStringSubmatch(user)[1]))
		var hits []map[string]any
		page, doc := 0, ""
		for _, ln := range strings.Split(user, "\n") {
			if m := pageRe.FindStringSubmatch(ln); m != nil {
				fmt.Sscan(m[1], &page)
				doc = m[2]
				continue
			}
			if m := lineRe.FindStringSubmatch(ln); m != nil && len(q) > 0 && strings.Contains(textutil.Normalize(m[2]), q[0]) {
				var n int
				fmt.Sscan(m[1], &n)
				hits = append(hits, map[string]any{"doc": doc, "page": page, "lines": []int{n}, "quote": m[2], "relevance": 0.9})
			}
		}
		reply = map[string]any{"hits": hits}
	case strings.Contains(system, "extract a knowledge graph"):
		kind = "extract"
		if s.Extract != nil {
			reply = s.Extract(user)
		} else {
			reply = map[string]any{"entities": []any{}, "relations": []any{}}
		}
	default:
		s.mu.Unlock()
		return fmt.Errorf("script llm: unexpected prompt %q", strings.SplitN(system, "\n", 2)[0])
	}
	s.Calls[kind]++
	s.mu.Unlock()
	b, _ := json.Marshal(reply)
	return llm.DecodeJSON(string(b), out)
}
