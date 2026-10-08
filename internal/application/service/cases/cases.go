// Package cases manages cases (§6.2): a set of files identified by one
// business code (e.g. a payment code RT112233). The core only knows "case";
// a new use case needs a new case type (YAML), not new code. The case is the
// hard scope of search and agent sessions.
package cases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/metadata"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// Errors surfaced to the API.
var (
	ErrNotFound   = errors.New("not found")
	ErrBadRequest = errors.New("bad request")
	// ErrConflict: the code already exists (409 with the existing case).
	ErrConflict = errors.New("conflict")
	// ErrClosed: the case does not accept uploads (409).
	ErrClosed = errors.New("case is closed")
)

// Service implements the case module.
type Service struct {
	st    *postgres.Store
	q     queue.Enqueuer
	cfg   *config.Config
	types map[string]types.CaseType
	log   *slog.Logger
}

// New loads the case types and builds the service.
func New(st *postgres.Store, q queue.Enqueuer, cfg *config.Config, log *slog.Logger) (*Service, error) {
	if log == nil {
		log = slog.Default()
	}
	ts, err := LoadTypes(cfg.Cases.TypesDir, cfg.Cases.DefaultType)
	if err != nil {
		return nil, fmt.Errorf("case types: %w", err)
	}
	return &Service{st: st, q: q, cfg: cfg, types: ts, log: log.With("module", "cases")}, nil
}

var _ interfaces.CaseService = (*Service)(nil)

func notFound(err error) error {
	if errors.Is(err, postgres.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// Types lists the loaded case types.
func (s *Service) Types() []types.CaseType { return sortedTypes(s.types) }

// CaseType implements interfaces.CaseService.
func (s *Service) CaseType(name string) types.CaseType {
	if t, ok := s.types[name]; ok {
		return t
	}
	return s.types[s.cfg.Cases.DefaultType]
}

// GetCase implements interfaces.CaseService.
func (s *Service) GetCase(ctx context.Context, id uuid.UUID) (types.Case, error) {
	c, err := s.st.Cases.Get(ctx, id)
	return c, notFound(err)
}

// GetCaseOwned implements interfaces.CaseService.
func (s *Service) GetCaseOwned(ctx context.Context, owner, id uuid.UUID) (types.Case, error) {
	c, err := s.st.Cases.GetOwned(ctx, id, owner)
	return c, notFound(err)
}

// Detail returns an owned case with its per-status document counts.
func (s *Service) Detail(ctx context.Context, owner, id uuid.UUID) (types.Case, error) {
	c, err := s.GetCaseOwned(ctx, owner, id)
	if err != nil {
		return c, err
	}
	return s.st.Cases.WithCounts(ctx, c)
}

// CreateRequest is the body of POST /kbs/:id/cases.
type CreateRequest struct {
	Code     string         `json:"code"`
	CaseType string         `json:"case_type,omitempty"`
	Title    string         `json:"title,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Create creates a case in a KB the owner owns. An existing code returns
// the existing case with ErrConflict.
func (s *Service) Create(ctx context.Context, owner, kb uuid.UUID, req CreateRequest) (types.Case, error) {
	if _, err := s.st.KBs.GetOwned(ctx, kb, owner); err != nil {
		return types.Case{}, notFound(err)
	}
	c, created, err := s.create(ctx, owner, kb, req)
	if err != nil {
		return c, err
	}
	if !created {
		return c, ErrConflict
	}
	return c, nil
}

func (s *Service) typeFor(name string) (types.CaseType, error) {
	if name == "" {
		name = s.cfg.Cases.DefaultType
	}
	t, ok := s.types[name]
	if !ok {
		return t, fmt.Errorf("%w: unknown case_type %q", ErrBadRequest, name)
	}
	return t, nil
}

func (s *Service) create(ctx context.Context, owner, kb uuid.UUID, req CreateRequest) (types.Case, bool, error) {
	t, err := s.typeFor(req.CaseType)
	if err != nil {
		return types.Case{}, false, err
	}
	code := NormalizeCode(t.Code, req.Code)
	if err := CheckCode(t.Code, code); err != nil {
		return types.Case{}, false, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	meta, errs := metadata.Validate(t.CaseMetadataSchema, req.Metadata)
	if len(errs) > 0 {
		return types.Case{}, false, fmt.Errorf("%w: case metadata: %v", ErrBadRequest, errs)
	}
	c, created, err := s.st.Cases.Insert(ctx, types.Case{
		KBID: kb, Code: code, CaseType: t.Name, Title: strings.TrimSpace(req.Title), Metadata: meta, CreatedBy: owner,
	})
	if err != nil {
		return c, false, err
	}
	if created {
		s.log.Info("case created", "case", c.ID, "code", c.Code, "type", c.CaseType)
	}
	return c, created, nil
}

// ResolveCase implements interfaces.CaseService: the case of an upload.
// kb may be uuid.Nil when ref.ID is given.
func (s *Service) ResolveCase(ctx context.Context, owner, kb uuid.UUID, ref interfaces.CaseRef) (types.Case, bool, error) {
	var c types.Case
	created := false
	switch {
	case ref.ID != uuid.Nil:
		var err error
		c, err = s.GetCaseOwned(ctx, owner, ref.ID)
		if err != nil {
			return c, false, err
		}
		if kb != uuid.Nil && c.KBID != kb {
			return c, false, ErrNotFound
		}
		if ref.CaseType != "" && ref.CaseType != c.CaseType {
			return c, false, fmt.Errorf("%w: case %s has type %q, not %q", ErrBadRequest, c.Code, c.CaseType, ref.CaseType)
		}
	case strings.TrimSpace(ref.Code) != "":
		if _, err := s.st.KBs.GetOwned(ctx, kb, owner); err != nil {
			return c, false, notFound(err)
		}
		found, err := s.findByCode(ctx, kb, ref.Code, ref.CaseType)
		switch {
		case err == nil:
			c = found
			if ref.CaseType != "" && ref.CaseType != c.CaseType {
				return c, false, fmt.Errorf("%w: case %s has type %q, not %q", ErrBadRequest, c.Code, c.CaseType, ref.CaseType)
			}
		case errors.Is(err, ErrNotFound):
			if !ref.Create || !s.cfg.Cases.AutoCreate() {
				return c, false, fmt.Errorf("%w: case %q does not exist; create it with POST /v1/kbs/%s/cases", ErrNotFound, strings.TrimSpace(ref.Code), kb)
			}
			c, created, err = s.create(ctx, owner, kb, CreateRequest{Code: ref.Code, CaseType: ref.CaseType})
			if err != nil {
				return c, false, err
			}
			if !created && ref.CaseType != "" && ref.CaseType != c.CaseType {
				return c, false, fmt.Errorf("%w: case %s has type %q, not %q", ErrBadRequest, c.Code, c.CaseType, ref.CaseType)
			}
		default:
			return c, false, err
		}
	default:
		return c, false, fmt.Errorf("%w: case_code (or case_id) is required", ErrBadRequest)
	}
	if c.Status == types.CaseClosed {
		return c, false, fmt.Errorf("%w: case %s is closed and does not accept files", ErrClosed, c.Code)
	}
	return c, created, nil
}

// findByCode looks a raw code up. With a type, only that type's
// normalization applies; without, the default type's is tried first, then
// every other type's (a case created as thanh_toan is found from " rt112233").
func (s *Service) findByCode(ctx context.Context, kb uuid.UUID, raw, caseType string) (types.Case, error) {
	t, err := s.typeFor(caseType)
	if err != nil {
		return types.Case{}, err
	}
	c, err := s.st.Cases.ByCode(ctx, kb, NormalizeCode(t.Code, raw))
	if err == nil || caseType != "" || !errors.Is(err, postgres.ErrNotFound) {
		return c, notFound(err)
	}
	for _, other := range s.types {
		if other.Name == t.Name {
			continue
		}
		c, err := s.st.Cases.ByCode(ctx, kb, NormalizeCode(other.Code, raw))
		if err == nil && c.CaseType == other.Name {
			return c, nil
		}
	}
	return types.Case{}, ErrNotFound
}

// ByCode looks a case up by code in a KB the owner owns.
func (s *Service) ByCode(ctx context.Context, owner, kb uuid.UUID, code string) (types.Case, error) {
	if _, err := s.st.KBs.GetOwned(ctx, kb, owner); err != nil {
		return types.Case{}, notFound(err)
	}
	c, err := s.findByCode(ctx, kb, code, "")
	if err != nil {
		return c, err
	}
	return s.st.Cases.WithCounts(ctx, c)
}

// ListRequest filters GET /kbs/:id/cases.
type ListRequest struct {
	Query    string
	CaseType string
	Status   string
	Metadata types.MetadataFilter
	Limit    int
	Before   *time.Time
}

// List lists the cases of a KB the owner owns.
func (s *Service) List(ctx context.Context, owner, kb uuid.UUID, req ListRequest) ([]types.Case, error) {
	if _, err := s.st.KBs.GetOwned(ctx, kb, owner); err != nil {
		return nil, notFound(err)
	}
	var schema *types.MetadataSchema
	if req.CaseType != "" {
		schema = s.CaseType(req.CaseType).CaseMetadataSchema
	}
	out, err := s.st.Cases.List(ctx, postgres.CaseFilter{OwnerID: owner, KBID: kb, Query: req.Query, CaseType: req.CaseType,
		Status: req.Status, Metadata: metadata.NormalizeFilter(schema, req.Metadata), Schema: schema, Limit: req.Limit, Before: req.Before})
	if err != nil && strings.Contains(err.Error(), "metadata filter") {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	return out, err
}

// UpdateRequest is the body of PATCH /cases/:id.
type UpdateRequest struct {
	Title    *string        `json:"title,omitempty"`
	Status   *string        `json:"status,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	// Code, CaseType and KBID are rejected: they never change.
	Code     *string `json:"code,omitempty"`
	CaseType *string `json:"case_type,omitempty"`
	KBID     *string `json:"kb_id,omitempty"`
}

// Update changes the title, status or metadata of a case.
func (s *Service) Update(ctx context.Context, owner, id uuid.UUID, req UpdateRequest) (types.Case, error) {
	c, err := s.GetCaseOwned(ctx, owner, id)
	if err != nil {
		return c, err
	}
	if req.Code != nil || req.CaseType != nil || req.KBID != nil {
		return c, fmt.Errorf("%w: code, case_type and kb_id cannot change; delete the case and create another", ErrBadRequest)
	}
	p := postgres.CasePatch{Title: req.Title}
	if req.Status != nil {
		if *req.Status != types.CaseOpen && *req.Status != types.CaseClosed {
			return c, fmt.Errorf("%w: status must be open or closed", ErrBadRequest)
		}
		p.Status = req.Status
	}
	if req.Metadata != nil {
		meta, errs := metadata.Validate(s.CaseType(c.CaseType).CaseMetadataSchema, req.Metadata)
		if len(errs) > 0 {
			return c, fmt.Errorf("%w: case metadata: %v", ErrBadRequest, errs)
		}
		p.Metadata = meta
	}
	out, err := s.st.Cases.Update(ctx, id, p)
	if err != nil {
		return out, notFound(err)
	}
	return s.st.Cases.WithCounts(ctx, out)
}

// Delete soft-deletes a case and enqueues case:delete, which removes its
// documents.
func (s *Service) Delete(ctx context.Context, owner, id uuid.UUID) error {
	c, err := s.GetCaseOwned(ctx, owner, id)
	if err != nil {
		return err
	}
	if err := notFound(s.st.Cases.SoftDelete(ctx, c.ID)); err != nil {
		return err
	}
	return s.q.Enqueue(ctx, types.TaskCaseDelete, types.CaseTaskPayload{CaseID: c.ID}, queue.Opts{TaskID: "delcase:" + c.ID.String()})
}

// Handlers returns the case task handlers.
func (s *Service) Handlers() map[string]queue.Handler {
	return map[string]queue.Handler{types.TaskCaseDelete: s.handleDelete}
}

// handleDelete purges a soft-deleted case: its documents one by one, then
// the pending ops of the case.
func (s *Service) handleDelete(ctx context.Context, raw []byte) error {
	var p types.CaseTaskPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
	}
	c, err := s.st.Cases.GetAny(ctx, p.CaseID)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.DeletedAt == nil {
		return nil // not deleted
	}
	docs, err := s.st.Documents.SoftDeleteByCase(ctx, c.ID)
	if err != nil {
		return err
	}
	for _, d := range docs {
		if err := s.q.Enqueue(ctx, types.TaskDocumentDelete, types.DocTaskPayload{DocumentID: d.ID, KBID: d.KBID, Gen: -1},
			queue.Opts{TaskID: "del:" + d.ID.String()}); err != nil {
			return err
		}
	}
	if err := s.st.Segments.DeleteBundles(ctx, c.ID); err != nil {
		return err
	}
	if err := s.st.Sheets.DeleteByCase(ctx, c.ID); err != nil {
		return err
	}
	if err := s.st.Tasks.DeleteScopeOps(ctx, types.ScopeCase, c.ID.String()); err != nil {
		return err
	}
	s.log.Info("case purged", "case", c.ID, "code", c.Code, "documents", len(docs))
	return nil
}

// Housekeeping re-drives case:delete for deleted cases that still own rows
// and reports documents whose case does not exist (no FK, §6.2).
func (s *Service) Housekeeping(ctx context.Context) error {
	ids, err := s.st.Cases.Deleted(ctx, 100)
	if err != nil {
		return err
	}
	bucket := time.Now().Unix() / 300
	for _, id := range ids {
		_ = s.q.Enqueue(ctx, types.TaskCaseDelete, types.CaseTaskPayload{CaseID: id}, queue.Opts{TaskID: fmt.Sprintf("delcase:%s:hk%d", id, bucket)})
	}
	orphans, err := s.st.Documents.OrphanCaseIDs(ctx, 100)
	if err != nil {
		return err
	}
	for doc, caseID := range orphans {
		s.log.Error("document points to a missing case", "doc", doc, "case", caseID)
		if seen, _ := s.st.Tasks.HasDeadLetter(ctx, types.ScopeCase, caseID.String(), doc.String()); seen {
			continue
		}
		payload, _ := json.Marshal(map[string]any{"document_id": doc, "case_id": caseID})
		_ = s.st.Tasks.InsertDeadLetter(ctx, postgres.DeadLetter{TaskType: types.TaskHousekeeping, Scope: types.ScopeCase,
			ScopeID: caseID.String(), RelatedID: doc.String(), Payload: payload, LastError: "document.case_id points to no case", FailCount: 1})
	}
	return nil
}
