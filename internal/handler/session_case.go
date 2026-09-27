package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/handler/dto"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Errors of binding a case to a session (§8.1).
var (
	errLegacyScope  = errors.New("metadata.kb_ids and metadata.kb_filter were removed: bind the session to one case with metadata.case_id (or metadata.case {kb_id, code})")
	errCaseMismatch = errors.New("the session is already bound to another case; open a new session to work on a different case")
	errCaseUnknown  = errors.New("case not found")
)

// resolveCaseRef finds the case named by metadata: case_id, or {kb_id, code}
// with the code normalized by the case type. Only cases the caller can read.
func (h *Handlers) resolveCaseRef(ctx context.Context, owner uuid.UUID, caseID string, ref *dto.CaseRef) (types.Case, error) {
	if h.Cases == nil {
		return types.Case{}, errors.New("cases are not configured on this server")
	}
	if id := strings.TrimSpace(caseID); id != "" {
		cid, err := uuid.Parse(id)
		if err != nil {
			return types.Case{}, fmt.Errorf("invalid case_id %q", id)
		}
		c, err := h.Cases.GetCaseOwned(ctx, owner, cid)
		if err != nil {
			return c, errCaseUnknown
		}
		return c, nil
	}
	kb, err := uuid.Parse(strings.TrimSpace(ref.KBID))
	if err != nil || strings.TrimSpace(ref.Code) == "" {
		return types.Case{}, errors.New("metadata.case needs kb_id and code")
	}
	c, err := h.Cases.ByCode(ctx, owner, kb, ref.Code)
	if err != nil {
		return c, errCaseUnknown
	}
	return c, nil
}

// bindSessionCase applies metadata.case_id / metadata.case to a session: an
// unbound session is bound (for good), the same case is a no-op, another
// case is errCaseMismatch. Legacy kb_ids / kb_filter are errLegacyScope.
func (h *Handlers) bindSessionCase(ctx context.Context, owner uuid.UUID, sess types.Session, md *dto.Metadata) (types.Session, error) {
	if md == nil {
		return sess, nil
	}
	if md.HasLegacyScope() {
		return sess, errLegacyScope
	}
	if strings.TrimSpace(md.CaseID) == "" && md.Case == nil {
		return sess, nil
	}
	c, err := h.resolveCaseRef(ctx, owner, md.CaseID, md.Case)
	if err != nil {
		return sess, err
	}
	if sess.CaseID != nil && *sess.CaseID != c.ID {
		return sess, errCaseMismatch
	}
	out, err := h.Store.Sessions.BindCase(ctx, sess.ID, c.ID)
	if errors.Is(err, postgres.ErrCaseBound) {
		return sess, errCaseMismatch
	}
	return out, err
}

// caseBindError writes the HTTP answer for a bindSessionCase error.
func (h *Handlers) caseBindError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, errCaseMismatch):
		c.JSON(consts.StatusConflict, dto.NewError("conflict_error", err.Error()))
	case errors.Is(err, errCaseUnknown):
		h.notFound(c, err.Error())
	default:
		c.JSON(consts.StatusUnprocessableEntity, dto.NewError("invalid_request_error", err.Error()))
	}
}

// aguiMetadata reads the case binding of an AG-UI run from `metadata`, or
// from `forwardedProps` (where CopilotKit clients put custom fields).
func aguiMetadata(req dto.AGUIRunAgentInput) *dto.Metadata {
	if req.Metadata != nil {
		return req.Metadata
	}
	if len(req.ForwardedProps) == 0 {
		return nil
	}
	var fp struct {
		dto.Metadata
		Nested *dto.Metadata `json:"metadata"`
	}
	if json.Unmarshal(req.ForwardedProps, &fp) != nil {
		return nil
	}
	if fp.Nested != nil {
		return fp.Nested
	}
	return &fp.Metadata
}
