// Package document is Module 1's orchestration (§4, §5): streaming upload to
// S3, the split → render → ocr → assemble pipeline with per-document fairness
// windows, reparse/cancel/delete, and the read/locate API over parsed pages.
package document

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/parser"
	"github.com/thanhenti/bepaylot/internal/parser/pdf"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/storage"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
	"github.com/thanhenti/bepaylot/internal/webhook"
)

// Renderer is the subset of pdf.Renderer the pipeline uses.
type Renderer interface {
	Probe(ctx context.Context, path string) (*pdf.DocInfo, error)
	RenderBatch(ctx context.Context, path string, pages []int, opt pdf.RenderOptions, emit func(pdf.PageRender) error) error
}

// Errors surfaced to the API.
var (
	ErrNotFound    = errors.New("not found")
	ErrUnsupported = errors.New("unsupported file type")
	ErrTooLarge    = errors.New("file too large")
	ErrBadRequest  = errors.New("bad request")
)

// Deps are the service dependencies.
type Deps struct {
	Store    *postgres.Store
	Objects  storage.ObjectStore
	Files    *storage.FileCache
	Queue    queue.Enqueuer
	Renderer Renderer
	Engines  *parser.Registry
	// Cases resolves the case of an upload and the rules of its type.
	Cases  interfaces.CaseService
	Config *config.Config
	Log    *slog.Logger
	// Webhook sends completion callbacks; nil builds one from Config.Callback.
	Webhook *webhook.Client
}

// Service implements Module 1.
type Service struct {
	st       *postgres.Store
	objects  storage.ObjectStore
	files    *storage.FileCache
	q        queue.Enqueuer
	renderer Renderer
	engines  *parser.Registry
	cases    interfaces.CaseService
	cfg      *config.Config
	keys     storage.Keys
	hooks    *webhook.Client
	log      *slog.Logger
}

// New builds the service.
func New(d Deps) *Service {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	hooks := d.Webhook
	if hooks == nil {
		cb := d.Config.Callback
		hooks = webhook.New(webhook.Config{Timeout: cb.Timeout, Secret: cb.SigningSecret, AllowPrivate: cb.AllowPrivateNetworks})
	}
	return &Service{
		hooks: hooks,
		st:    d.Store, objects: d.Objects, files: d.Files, q: d.Queue, renderer: d.Renderer, engines: d.Engines, cases: d.Cases,
		cfg: d.Config, keys: storage.Keys{Prefix: d.Config.Storage.S3.Prefix}, log: log.With("module", "document"),
	}
}

var _ interfaces.DocumentStore = (*Service)(nil)

// Keys exposes the object key layout.
func (s *Service) Keys() storage.Keys { return s.keys }

// GetDocument implements interfaces.DocumentStore.
func (s *Service) GetDocument(ctx context.Context, id uuid.UUID) (types.Document, error) {
	d, err := s.st.Documents.Get(ctx, id)
	if errors.Is(err, postgres.ErrNotFound) {
		return d, ErrNotFound
	}
	return d, err
}

// GetKB implements interfaces.DocumentStore.
func (s *Service) GetKB(ctx context.Context, id uuid.UUID) (types.KnowledgeBase, error) {
	kb, err := s.st.KBs.Get(ctx, id)
	if errors.Is(err, postgres.ErrNotFound) {
		return kb, ErrNotFound
	}
	return kb, err
}

// LoadPages implements interfaces.DocumentStore.
func (s *Service) LoadPages(ctx context.Context, doc uuid.UUID, gen, from, to int) ([]*types.ParsedPage, error) {
	rows, err := s.st.Pages.List(ctx, doc, gen, from, to)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	first, last := rows[0].PageNo, rows[len(rows)-1].PageNo
	blocks, err := s.st.Pages.Blocks(ctx, doc, first, last)
	if err != nil {
		return nil, err
	}
	lines, err := s.st.Pages.Lines(ctx, doc, first, last)
	if err != nil {
		return nil, err
	}
	byPage := make(map[int]*types.ParsedPage, len(rows))
	out := make([]*types.ParsedPage, 0, len(rows))
	for _, r := range rows {
		p := &types.ParsedPage{PageNo: r.PageNo, Width: r.Width, Height: r.Height, DPI: r.DPI, Rotation: r.Rotation, Engine: r.Engine,
			TextSource: r.TextSource, TextQuality: r.TextQuality, IsBlank: r.IsBlank, Markdown: r.Markdown, DocMdOffset: r.DocMdOffset}
		byPage[r.PageNo] = p
		out = append(out, p)
	}
	for _, b := range blocks {
		if p := byPage[b.PageNo]; p != nil {
			p.Blocks = append(p.Blocks, b.ParsedBlock)
		}
	}
	for _, l := range lines {
		if p := byPage[l.PageNo]; p != nil {
			p.Lines = append(p.Lines, l.ParsedLine)
		}
	}
	return out, nil
}

// SetIndexResult implements interfaces.DocumentStore. A document whose case
// has a wiki moves to enriching and is queued for ingest (§6.8); search works
// from here on either way.
func (s *Service) SetIndexResult(ctx context.Context, doc uuid.UUID, gen int, card interfaces.DocumentCard, failed error) error {
	d, err := s.st.Documents.Get(ctx, doc)
	if err != nil {
		return err
	}
	u := postgres.DocUpdate{}
	if failed != nil {
		st, msg := types.StageFailed, "index: "+failed.Error()
		u.IndexStatus, u.Error = &st, &msg
		status := types.DocFailed
		u.Status = &status
		_, err := s.updateStatus(ctx, doc, gen, u)
		return err
	}
	done := types.StageDone
	u.IndexStatus = &done
	if card.Title != "" {
		u.Title = &card.Title
	}
	if card.Summary != "" {
		u.Summary = &card.Summary
	}
	final := types.DocCompleted
	if d.ParseStatus == types.StagePartial {
		final = types.DocPartial
	}
	wiki := types.StageSkipped
	if c, err := s.cases.GetCase(ctx, d.CaseID); err == nil && s.cases.WikiEnabled(c) {
		final, wiki = types.DocEnriching, types.StagePending
	}
	u.WikiStatus = &wiki
	u.Status = &final
	ok, err := s.updateStatus(ctx, doc, gen, u)
	if err != nil || !ok || wiki != types.StagePending {
		return err
	}
	return s.queueWikiOp(ctx, d, types.WikiOpIngestDoc, gen)
}

// SetWikiStatus implements interfaces.DocumentStore. A finished ingest
// (done, partial, failed, skipped) settles the document status.
func (s *Service) SetWikiStatus(ctx context.Context, doc uuid.UUID, gen int, status string, failed error) error {
	d, err := s.st.Documents.Get(ctx, doc)
	if err != nil {
		return err
	}
	u := postgres.DocUpdate{WikiStatus: &status}
	if failed != nil {
		msg := "wiki: " + failed.Error()
		u.Error = &msg
	}
	if d.Status == types.DocEnriching && status != types.StagePending && status != types.StageProcessing {
		final := types.DocCompleted
		if d.ParseStatus == types.StagePartial {
			final = types.DocPartial
		}
		u.Status = &final
	}
	_, err = s.updateStatus(ctx, doc, gen, u)
	return err
}

// queueWikiOp records a wiki op of the document's case and wakes the case's
// ingest stream (§6.8). The op survives restarts in task_pending_ops.
func (s *Service) queueWikiOp(ctx context.Context, d types.Document, op string, gen int) error {
	if _, err := s.st.Tasks.EnqueueWikiOp(ctx, d.CaseID, op, types.WikiOpPayload{DocumentID: d.ID, Gen: gen, FileName: d.FileName}); err != nil {
		return err
	}
	return s.q.Enqueue(ctx, types.TaskWikiIngest, types.CaseTaskPayload{CaseID: d.CaseID},
		queue.Opts{TaskID: fmt.Sprintf("wi:%s:%s:%d:%s", d.CaseID, d.ID, gen, op)})
}
