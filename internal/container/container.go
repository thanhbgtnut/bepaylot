// Package container wires every module into a runnable application: the
// Hertz API, the asynq worker pools, and the services between them (§3.1).
// Modules only meet here, through the contracts in types/interfaces.
package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/agent"
	"github.com/thanhenti/bepaylot/internal/agent/prompt"
	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/document"
	"github.com/thanhenti/bepaylot/internal/application/service/graph"
	"github.com/thanhenti/bepaylot/internal/application/service/index"
	"github.com/thanhenti/bepaylot/internal/application/service/wiki"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/handler"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/llm/fakeprovider"
	appmcp "github.com/thanhenti/bepaylot/internal/mcp"
	"github.com/thanhenti/bepaylot/internal/parser"
	"github.com/thanhenti/bepaylot/internal/parser/pdf"
	"github.com/thanhenti/bepaylot/internal/parser/turboocr"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/retrieval"
	"github.com/thanhenti/bepaylot/internal/router"
	"github.com/thanhenti/bepaylot/internal/skills"
	"github.com/thanhenti/bepaylot/internal/storage"
	"github.com/thanhenti/bepaylot/internal/tools"
	"github.com/thanhenti/bepaylot/internal/types"
)

// App is a wired application.
type App struct {
	Config *config.Config
	Log    *slog.Logger
	Store  *postgres.Store
	Hertz  *server.Hertz

	workers  *queue.Workers
	inline   *queue.Inline
	enqueuer queue.Enqueuer
	docs     *document.Service
	closers  []func()
}

// Build wires the application for cfg.Workers.Role.
func Build(ctx context.Context, cfg *config.Config, log *slog.Logger) (*App, error) {
	app := &App{Config: cfg, Log: log}
	st, err := postgres.Open(ctx, cfg.DB)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	app.Store = st
	app.closers = append(app.closers, st.Close)

	// ---- agent (Module 4) ----
	embedder, err := retrieval.New(cfg.Embd)
	if err != nil {
		return nil, fmt.Errorf("embedder: %w", err)
	}
	skillSvc := skills.NewService(st.Skills, embedder, cfg.Skills.Dir, log)
	if cfg.Skills.SyncOnStartup && cfg.Workers.RunsAPI() {
		if res, err := skillSvc.Sync(ctx); err != nil {
			log.Warn("startup skill sync failed", "err", err)
		} else {
			log.Info("startup skill sync done", "discovered", res.Discovered, "embedded", res.Embedded)
		}
	}
	registry, err := llm.NewRegistry(cfg.LLM)
	if err != nil {
		return nil, fmt.Errorf("llm registry: %w", err)
	}
	for name, pc := range cfg.LLM.Providers {
		if pc.Kind == "fake" {
			registry.Register(name, fakeprovider.New(fakeSkillSlug()))
		}
	}
	if err := registry.Validate(); err != nil {
		return nil, err
	}
	toolReg, err := tools.NewRegistry(skillSvc, cfg.Tools.HTTPAllowlist, st.TaskResults)
	if err != nil {
		return nil, fmt.Errorf("tool registry: %w", err)
	}
	var mcpMgr *appmcp.Manager
	if cfg.MCP.Enabled && cfg.Workers.RunsAPI() {
		mcpMgr = appmcp.New(toolReg, log, cfg.MCP.ToolPrefix, cfg.MCP.InitTimeout)
		app.closers = append(app.closers, mcpMgr.Close)
		bootstrapMCP(ctx, mcpMgr, st, cfg, log)
		if cfg.MCP.File != "" && cfg.MCP.ReloadInterval > 0 {
			go appmcp.Watcher(ctx, mcpMgr, cfg.MCP.File, cfg.MCP.ReloadInterval)
		}
	}
	ag := agent.New(st, registry, toolReg, skillSvc, cfg.Agent, cfg.LLM, log)

	handlers := &handler.Handlers{
		Store: st, Agent: ag, Registry: registry, Skills: skillSvc, MCP: mcpMgr,
		LLM: cfg.LLM, Agentcfg: cfg.Agent, Log: log, Config: cfg,
	}

	// ---- document modules (1–3) ----
	if err := app.buildDocumentModules(ctx, handlers, registry, toolReg, ag); err != nil {
		if cfg.Workers.RunsWorkers() {
			return nil, err
		}
		log.Error("document modules disabled", "err", err)
	}

	if cfg.Workers.RunsAPI() {
		app.Hertz = router.New(cfg.HTTP, handlers, log)
	}
	return app, nil
}

func (app *App) buildDocumentModules(ctx context.Context, h *handler.Handlers, registry *llm.Registry, toolReg *tools.Registry, ag *agent.Agent) error {
	cfg, log, st := app.Config, app.Log, app.Store

	// Object storage: S3, or in-memory only in development.
	var objects storage.ObjectStore
	if cfg.Storage.S3.Bucket != "" {
		s3, err := storage.NewS3(ctx, cfg.Storage.S3)
		if err != nil {
			return err
		}
		objects = s3
	} else if cfg.Env == "development" {
		log.Warn("storage.s3.bucket is empty: using IN-MEMORY object storage (development only, data is lost on restart)")
		objects = storage.NewMemory()
	} else {
		return errors.New("storage.s3.bucket is required")
	}
	files, err := storage.NewFileCache(cfg.Parser.Render.CacheDir, cfg.Parser.Render.CacheMaxBytes, objects)
	if err != nil {
		return err
	}

	// Queue: asynq on Redis, or in-process when Redis is not configured.
	if cfg.Redis.Addr != "" {
		qc, err := queue.NewClient(cfg.Redis)
		if err != nil {
			return err
		}
		app.enqueuer = qc
		app.closers = append(app.closers, func() { qc.Close() })
		h.Inspector = newAsynqInspector(cfg)
	} else {
		if !cfg.Workers.RunsWorkers() {
			return errors.New("redis.addr is required when workers run in another process (role=api)")
		}
		log.Warn("redis.addr is empty: tasks run in-process (single instance only, queued work is lost on restart)")
		app.inline = queue.NewInline()
		app.enqueuer = app.inline
		h.Inspector = inlineInspector{app.inline}
	}

	// OCR engines.
	engines := parser.NewRegistry(cfg.Parser.DefaultEngine)
	oc := cfg.Parser.Engines.TurboOCR
	engines.Register(turboocr.New(turboocr.Config{BaseURL: oc.BaseURL, Timeout: oc.Timeout, BreakerFailures: oc.Breaker.Failures, BreakerOpenFor: oc.Breaker.OpenFor}))

	// PDF renderer: workers only.
	var renderer document.Renderer
	if cfg.Workers.RunsWorkers() {
		rc := cfg.Parser.Render
		r, err := pdf.New(pdf.Config{Mode: rc.Mode, WorkerBin: rc.WorkerBin, Workers: rc.Workers, RecycleAfterPages: rc.RecycleAfterPages,
			MaxWorkerRSSMB: rc.MaxWorkerRSSMB, PageTimeout: rc.PageTimeout, Log: log})
		if err != nil {
			return fmt.Errorf("pdf renderer: %w", err)
		}
		renderer = r
		app.closers = append(app.closers, func() { r.Close() })
		log.Info("pdf renderer ready", "mode", rc.Mode, "workers", rc.Workers)
	}

	completer := func(provider, model string, maxTokens int) *llm.JSONCompleter {
		if model == "" {
			model = cfg.LLM.DefaultModel
		}
		return &llm.JSONCompleter{Reg: registry, Provider: provider, Model: model, MaxTokens: maxTokens}
	}
	treeLLM := completer(cfg.Index.Tree.Provider, cfg.Index.Tree.Model, 4096)
	searchLLM := completer(cfg.Search.Provider, cfg.Search.Model, 4096)
	graphLLM := completer(cfg.Graph.Provider, cfg.Graph.Model, 8192)

	docs := document.New(document.Deps{Store: st, Objects: objects, Files: files, Queue: app.enqueuer, Renderer: renderer, Engines: engines, Config: cfg, Log: log})
	idx := index.New(index.Deps{Store: st, Docs: docs, Queue: app.enqueuer, TreeLLM: treeLLM, SearchLLM: searchLLM, Config: cfg, Log: log})
	gr := graph.New(graph.Deps{Store: st, Docs: docs, Sections: idx, Queue: app.enqueuer, LLM: graphLLM, Config: cfg, Log: log})
	wk := wiki.New(st, app.enqueuer, graphLLM, cfg, log)
	if err := gr.EnsureSchemas(ctx); err != nil {
		log.Warn("graph schemas not loaded", "err", err)
	}
	app.docs = docs

	h.Docs, h.Searcher, h.Graph, h.Wiki, h.Engines, h.Queue = docs, idx, gr, wk, engines, app.enqueuer
	if err := toolReg.SetKnowledgeTools(idx, gr); err != nil {
		return err
	}
	ag.SetKnowledge(describer{st: st, docs: docs})

	if cfg.Workers.RunsWorkers() {
		handlers := map[string]queue.Handler{}
		for _, m := range []map[string]queue.Handler{docs.Handlers(), idx.Handlers(), gr.Handlers(), wk.Handlers()} {
			for k, v := range m {
				handlers[k] = v
			}
		}
		sink := deadLetterSink(st, log)
		if app.inline != nil {
			app.inline.Register(handlers, sink)
		} else {
			conc := map[string]int{}
			for k, v := range cfg.Workers.Concurrency {
				conc[k] = v
			}
			conc[types.PoolRender] = cfg.Parser.Render.Workers
			app.workers = queue.NewWorkers(cfg.Redis, conc, log, handlers, sink)
		}
	}
	return nil
}

// Run serves HTTP and/or runs workers until ctx is cancelled.
func (app *App) Run(ctx context.Context) error {
	defer app.Close()
	if app.workers != nil {
		if err := app.workers.Start(); err != nil {
			return err
		}
		defer app.workers.Shutdown()
		app.Log.Info("workers started", "pools", types.Pools())
	}
	if app.inline != nil {
		go app.inline.RunLoop(ctx, 500*time.Millisecond)
	}
	if app.Config.Workers.RunsWorkers() && app.docs != nil {
		go app.housekeeping(ctx)
	}
	if app.Hertz != nil {
		app.Log.Info("bepaylot listening", "addr", app.Config.HTTP.Addr, "role", app.Config.Workers.Role)
		router.Run(ctx, app.Hertz)
		return nil
	}
	app.Log.Info("worker running", "role", app.Config.Workers.Role)
	<-ctx.Done()
	return nil
}

// Close releases resources in reverse order.
func (app *App) Close() {
	for i := len(app.closers) - 1; i >= 0; i-- {
		app.closers[i]()
	}
	app.closers = nil
}

// housekeeping enqueues the sweep on every interval; the TaskID bucket makes
// it run once per interval across replicas (§4.3).
func (app *App) housekeeping(ctx context.Context) {
	every := app.Config.Workers.HousekeepingInterval
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			id := fmt.Sprintf("hk:%d", now.Unix()/int64(every.Seconds()))
			if err := app.enqueuer.Enqueue(ctx, types.TaskHousekeeping, map[string]any{}, queue.Opts{TaskID: id}); err != nil {
				app.Log.Warn("enqueue housekeeping failed", "err", err)
			}
		}
	}
}

// deadLetterSink archives exhausted tasks with their scope (§4.5).
func deadLetterSink(st *postgres.Store, log *slog.Logger) queue.DeadLetterSink {
	return func(ctx context.Context, taskType, queueName string, payload []byte, err error, attempts int) {
		dl := postgres.DeadLetter{TaskType: taskType, Queue: queueName, Scope: types.ScopeUnknown, Payload: payload, LastError: err.Error(), FailCount: attempts}
		var p struct {
			DocumentID uuid.UUID `json:"document_id"`
			KBID       uuid.UUID `json:"kb_id"`
			Pages      []int     `json:"pages"`
		}
		if json.Unmarshal(payload, &p) == nil {
			switch {
			case p.DocumentID != uuid.Nil:
				dl.Scope, dl.ScopeID = types.ScopeDocument, p.DocumentID.String()
				if len(p.Pages) > 0 {
					dl.RelatedID = fmt.Sprint(p.Pages)
				}
			case p.KBID != uuid.Nil:
				dl.Scope, dl.ScopeID = types.ScopeKnowledgeBase, p.KBID.String()
			}
		}
		if err := st.Tasks.InsertDeadLetter(ctx, dl); err != nil {
			log.Error("store dead letter failed", "err", err)
		}
	}
}

// describer implements agent.KnowledgeDescriber.
type describer struct {
	st   *postgres.Store
	docs *document.Service
}

func (d describer) DescribeKnowledgeBases(ctx context.Context, owner uuid.UUID, ids []uuid.UUID) []prompt.KnowledgeBase {
	var out []prompt.KnowledgeBase
	for _, id := range ids {
		kb, err := d.st.KBs.GetOwned(ctx, id, owner)
		if err != nil {
			continue
		}
		n, _ := d.st.Documents.CountByKB(ctx, id)
		ref := prompt.KnowledgeBase{ID: kb.ID.String(), Name: kb.Name, Description: kb.Description, Documents: n}
		for _, f := range d.docs.MetadataKeys(ctx, kb) {
			ref.Fields = append(ref.Fields, prompt.MetadataField{Key: f.Key, Type: f.Type, Description: f.Description, Values: f.Values})
		}
		out = append(out, ref)
	}
	return out
}

func fakeSkillSlug() string {
	if v := os.Getenv("BEPAYLOT_FAKE_SKILL_SLUG"); v != "" {
		return v
	}
	return "pdf-forms"
}

var _ agent.KnowledgeDescriber = describer{}
