// Package wiki is the LLM Wiki of each case (§6.6–6.9), after Karpathy's
// "LLM Wiki": immutable sources (parsed pages) → a wiki the LLM compiles and
// maintains → a schema of conventions. Ingest folds each file into the wiki,
// lint keeps it healthy, and search reads its index first. Unlike the gist,
// every page, footnote, link, the index and the log live in Postgres.
package wiki

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// Errors surfaced to the API.
var (
	ErrNotFound   = errors.New("not found")
	ErrBadRequest = errors.New("bad request")
	// ErrBusy means another worker holds the case's wiki lock; the task
	// retries later (one ingest stream per case, §6.8).
	ErrBusy = errors.New("wiki ingest already running for this case")
)

// Deps are the service dependencies.
type Deps struct {
	Store    *postgres.Store
	Docs     interfaces.DocumentStore
	Sections interfaces.SectionReader
	Cases    interfaces.CaseService
	Queue    queue.Enqueuer
	// LLM writes pages; nil keeps template source pages only (no entities).
	LLM    interfaces.Completer
	Config *config.Config
	Log    *slog.Logger
}

// Service implements the case wiki.
type Service struct {
	st    *postgres.Store
	docs  interfaces.DocumentStore
	secs  interfaces.SectionReader
	cases interfaces.CaseService
	q     queue.Enqueuer
	llm   interfaces.Completer
	cfg   *config.Config
	log   *slog.Logger
}

// New builds the service.
func New(d Deps) *Service {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{st: d.Store, docs: d.Docs, secs: d.Sections, cases: d.Cases, q: d.Queue, llm: d.LLM, cfg: d.Config, log: log.With("module", "wiki")}
}

var _ interfaces.WikiReader = (*Service)(nil)

// EnsureSchemas stores the built-in generic schema and every schema file.
func (s *Service) EnsureSchemas(ctx context.Context) error {
	schemas := []types.WikiSchema{GenericSchema()}
	files, err := LoadSchemaFiles(s.cfg.Wiki.SchemasDir)
	if err != nil {
		return err
	}
	schemas = append(schemas, files...)
	for _, sc := range schemas {
		if _, err := s.st.Wiki.UpsertSchema(ctx, sc); err != nil {
			return fmt.Errorf("wiki schema %s v%d: %w", sc.Name, sc.Version, err)
		}
	}
	return nil
}

// schemaFor returns the schema a case was created with (latest version),
// falling back to the default and then the built-in generic schema.
func (s *Service) schemaFor(ctx context.Context, c types.Case) types.WikiSchema {
	for _, name := range []string{c.WikiSchema, s.cfg.Wiki.DefaultSchema, "generic"} {
		if name == "" {
			continue
		}
		if sc, err := s.st.Wiki.LatestSchema(ctx, name); err == nil {
			return sc
		}
	}
	return GenericSchema()
}

// Handlers returns the wiki task handlers.
func (s *Service) Handlers() map[string]queue.Handler {
	return map[string]queue.Handler{
		types.TaskWikiIngest: s.caseHandler(s.drain),
		types.TaskWikiLint:   s.caseHandler(s.Lint),
		types.TaskWikiIndex:  s.caseHandler(s.Reindex),
	}
}

func (s *Service) caseHandler(fn func(ctx context.Context, c types.Case) error) queue.Handler {
	return func(ctx context.Context, raw []byte) error {
		var p types.CaseTaskPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
		}
		c, err := s.cases.GetCase(ctx, p.CaseID)
		if err != nil {
			return nil // case deleted: case:delete removes its wiki
		}
		return fn(ctx, c)
	}
}

// maxOpFailures is how often one op may fail before it is dropped.
const maxOpFailures = 5

// drain runs the case's pending ops one at a time under the case lock
// (§6.8): files are folded in the order they finished indexing, each one
// on top of the wiki the previous ones left. A drained queue triggers lint.
func (s *Service) drain(ctx context.Context, c types.Case) error {
	ran, err := s.st.Wiki.TryCaseLock(ctx, c.ID, func(ctx context.Context) error {
		did := 0
		for {
			ops, err := s.st.Tasks.PeekOps(ctx, types.TaskWikiIngest, types.ScopeCase, c.ID.String(), 1)
			if err != nil {
				return err
			}
			if len(ops) == 0 {
				break
			}
			op := ops[0]
			if did == 0 {
				_ = s.st.Cases.SetWikiStatus(ctx, c.ID, types.WikiBuilding, -1)
			}
			if err := s.runOp(ctx, c, op); err != nil {
				s.log.Warn("wiki op failed", "case", c.ID, "op", op.Op, "fails", op.FailCount+1, "err", err)
				if op.FailCount+1 < maxOpFailures && !errors.Is(err, queue.ErrSkipRetry) {
					_ = s.st.Tasks.IncrOpFail(ctx, []int64{op.ID})
					return err
				}
				s.opGaveUp(context.WithoutCancel(ctx), c, op, err)
			}
			if err := s.st.Tasks.DeleteOps(ctx, []int64{op.ID}); err != nil {
				return err
			}
			did++
		}
		if did > 0 {
			s.settleCaseStatus(ctx, c.ID)
			return s.q.Enqueue(ctx, types.TaskWikiLint, types.CaseTaskPayload{CaseID: c.ID},
				queue.Opts{TaskID: fmt.Sprintf("wl:%s:%d", c.ID, time.Now().UnixNano())})
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !ran {
		return ErrBusy
	}
	return nil
}

// opGaveUp records a dropped op: the file is marked failed for the wiki and a
// gap issue tells the user.
func (s *Service) opGaveUp(ctx context.Context, c types.Case, op postgres.PendingOp, cause error) {
	var p types.WikiOpPayload
	_ = json.Unmarshal(op.Payload, &p)
	if op.Op == types.WikiOpIngestDoc && p.DocumentID != uuid.Nil {
		// Keep the file in the wiki with a template source page (card and
		// table of contents) rather than leaving it out.
		if err := s.ingestTemplate(ctx, c, p.DocumentID, p.Gen); err != nil {
			_ = s.docs.SetWikiStatus(ctx, p.DocumentID, p.Gen, types.StageFailed, cause)
		} else {
			_ = s.docs.SetWikiStatus(ctx, p.DocumentID, p.Gen, types.StagePartial, cause)
		}
	}
	_, _ = s.st.Wiki.AddIssue(ctx, types.WikiLintIssue{CaseID: c.ID, Kind: types.LintGap,
		Detail: map[string]any{"reason": "wiki op failed", "op": op.Op, "document_id": p.DocumentID, "file_name": p.FileName, "error": cause.Error()}},
		fmt.Sprintf("gap:op:%s:%s:%d", op.Op, p.DocumentID, p.Gen))
	_ = s.st.Cases.SetWikiStatus(ctx, c.ID, types.WikiFailed, -1)
}

// settleCaseStatus sets ready (or stale when files still wait) after a drain.
func (s *Service) settleCaseStatus(ctx context.Context, caseID uuid.UUID) {
	covered, _ := s.st.Wiki.CoveredDocs(ctx, caseID)
	cur, err := s.st.Cases.Get(ctx, caseID)
	if err != nil {
		return
	}
	status := types.WikiReady
	if cur.WikiStatus == types.WikiFailed {
		status = types.WikiStale
	}
	if cur.WikiVersion == 0 {
		status = types.WikiNone
	}
	_ = s.st.Cases.SetWikiStatus(ctx, caseID, status, covered)
}

func (s *Service) runOp(ctx context.Context, c types.Case, op postgres.PendingOp) error {
	var p types.WikiOpPayload
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("%w: bad op payload: %v", queue.ErrSkipRetry, err)
	}
	switch op.Op {
	case types.WikiOpIngestDoc:
		return s.ingestOp(ctx, c, p)
	case types.WikiOpRetractDoc:
		return s.retract(ctx, c, p.DocumentID, p.Gen, p.FileName)
	case types.WikiOpRefresh:
		return s.refreshPages(ctx, c, p.PageIDs)
	}
	return fmt.Errorf("%w: unknown wiki op %q", queue.ErrSkipRetry, op.Op)
}

// queueOp records an op for the case and wakes its ingest stream.
func (s *Service) queueOp(ctx context.Context, caseID uuid.UUID, op string, p types.WikiOpPayload) error {
	added, err := s.st.Tasks.EnqueueWikiOp(ctx, caseID, op, p)
	if err != nil || !added {
		return err
	}
	return s.q.Enqueue(ctx, types.TaskWikiIngest, types.CaseTaskPayload{CaseID: caseID},
		queue.Opts{TaskID: fmt.Sprintf("wi:%s:%d", caseID, time.Now().UnixNano())})
}

// Reindex rebuilds the stored index of a case (task wiki:index).
func (s *Service) Reindex(ctx context.Context, c types.Case) error {
	return s.st.Wiki.InTx(ctx, func(w *postgres.WikiRepo) error {
		v, err := w.BumpVersion(ctx, c.ID)
		if err != nil {
			return err
		}
		c.WikiVersion = v
		return s.storeIndex(ctx, w, c)
	})
}

// Housekeeping re-drives stalled wiki work (§4.3): pending ops of every case
// (restart recovery), searchable files never queued for ingest (e.g. after
// migration 0014) and the periodic lint.
func (s *Service) Housekeeping(ctx context.Context) error {
	bucket := time.Now().Unix() / 300
	scopes, err := s.st.Tasks.OpScopes(ctx, types.TaskWikiIngest)
	if err != nil {
		return err
	}
	for _, id := range scopes {
		_ = s.q.Enqueue(ctx, types.TaskWikiIngest, types.CaseTaskPayload{CaseID: uuid.MustParse(id)},
			queue.Opts{TaskID: fmt.Sprintf("wi:%s:hk%d", id, bucket)})
	}
	pending, err := s.st.Documents.WikiPending(ctx, time.Now().Add(-10*time.Minute), 200)
	if err != nil {
		return err
	}
	for _, d := range pending {
		c, err := s.cases.GetCase(ctx, d.CaseID)
		if err != nil {
			continue
		}
		if !s.cases.WikiEnabled(c) {
			_ = s.docs.SetWikiStatus(ctx, d.ID, d.Gen, types.StageSkipped, nil)
			continue
		}
		if err := s.queueOp(ctx, c.ID, types.WikiOpIngestDoc, types.WikiOpPayload{DocumentID: d.ID, Gen: d.Gen, FileName: d.FileName}); err != nil {
			s.log.Warn("housekeeping: queue ingest", "doc", d.ID, "err", err)
		}
	}
	if s.cfg.Wiki.Lint.Interval > 0 {
		lintBucket := time.Now().Unix() / int64(max(1, int(s.cfg.Wiki.Lint.Interval.Seconds())))
		cs, err := s.st.Cases.WithWiki(ctx, 500)
		if err != nil {
			return err
		}
		for _, c := range cs {
			_ = s.q.Enqueue(ctx, types.TaskWikiLint, types.CaseTaskPayload{CaseID: c.ID},
				queue.Opts{TaskID: fmt.Sprintf("wl:%s:p%d", c.ID, lintBucket)})
		}
	}
	return nil
}
