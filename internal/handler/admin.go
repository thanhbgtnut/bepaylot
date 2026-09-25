package handler

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/handler/dto"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/types"
)

// QueueInspector reports queue depths (asynq inspector or inline queue).
type QueueInspector interface {
	Stats(ctx context.Context) []dto.QueueStat
}

func (h *Handlers) isAdmin(c *app.RequestContext) bool {
	u, ok := h.user(c)
	if !ok {
		return false
	}
	if h.Config != nil {
		for _, e := range h.Config.HTTP.AdminEmails {
			if strings.EqualFold(strings.TrimSpace(e), u.Email) {
				return true
			}
		}
	}
	c.JSON(consts.StatusForbidden, dto.NewError("permission_error", "admin only (http.admin_emails)"))
	return false
}

// QueueStats handles GET /v1/admin/queues.
//
// @Summary   Queue depths per worker pool
// @Tags      Admin
// @Produce   json
// @Success   200  {object}  dto.QueueStats
// @Failure   403  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/admin/queues [get]
func (h *Handlers) QueueStats(ctx context.Context, c *app.RequestContext) {
	if !h.isAdmin(c) {
		return
	}
	out := dto.QueueStats{Data: []dto.QueueStat{}}
	if h.Inspector != nil {
		out.Data = h.Inspector.Stats(ctx)
	}
	c.JSON(consts.StatusOK, out)
}

// ListDeadLetters handles GET /v1/admin/dead-letters.
//
// @Summary   Tasks that exhausted their retries
// @Tags      Admin
// @Produce   json
// @Param     task_type  query     string  false  "Filter by task type"
// @Param     scope_id   query     string  false  "Filter by scope id (document/KB id)"
// @Param     limit      query     int     false  "Max rows"  default(100)
// @Success   200        {object}  dto.DeadLetterList
// @Failure   403        {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/admin/dead-letters [get]
func (h *Handlers) ListDeadLetters(ctx context.Context, c *app.RequestContext) {
	if !h.isAdmin(c) {
		return
	}
	rows, err := h.Store.Tasks.ListDeadLetters(ctx, string(c.Query("task_type")), string(c.Query("scope_id")), intQuery(c, "limit", 100))
	if err != nil {
		h.serverError(c, err)
		return
	}
	out := dto.DeadLetterList{Data: []dto.DeadLetter{}}
	for _, r := range rows {
		out.Data = append(out.Data, dto.DeadLetter{ID: r.ID, TaskType: r.TaskType, Queue: r.Queue, Scope: r.Scope, ScopeID: r.ScopeID,
			Payload: r.Payload, LastError: r.LastError, FailCount: r.FailCount, FailedAt: r.FailedAt.Format(time.RFC3339)})
	}
	c.JSON(consts.StatusOK, out)
}

// RetryDeadLetter handles POST /v1/admin/dead-letters/{id}/retry.
//
// @Summary   Re-enqueue a dead-lettered task
// @Tags      Admin
// @Produce   json
// @Param     id   path      int  true  "Dead letter id"
// @Success   200  {object}  dto.OK
// @Failure   403  {object}  dto.ErrorResponse
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/admin/dead-letters/{id}/retry [post]
func (h *Handlers) RetryDeadLetter(ctx context.Context, c *app.RequestContext) {
	if !h.isAdmin(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		h.badRequest(c, "invalid id")
		return
	}
	dl, err := h.Store.Tasks.TakeDeadLetter(ctx, id)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	var payload any = json.RawMessage(dl.Payload)
	// A retried page task must find its page claimable again.
	if dl.TaskType == types.TaskPageOCR || dl.TaskType == types.TaskPageRender {
		var p types.DocTaskPayload
		if json.Unmarshal(dl.Payload, &p) == nil {
			status := types.PageRendered
			if dl.TaskType == types.TaskPageRender {
				status = types.PagePending
			}
			done, failed, _ := h.Store.Pages.ResetPages(ctx, p.DocumentID, p.Gen, p.Pages, status)
			_ = h.Store.Documents.AdjustCounters(ctx, p.DocumentID, p.Gen, -done, -failed)
			if d, err := h.Store.Documents.Get(ctx, p.DocumentID); err == nil && h.Docs != nil {
				_ = h.Docs.Advance(ctx, d)
				c.JSON(consts.StatusOK, dto.OK{OK: true})
				return
			}
		}
	}
	if err := h.Queue.Enqueue(ctx, dl.TaskType, payload, queue.Opts{TaskID: "retry:" + uuid.NewString()}); err != nil {
		h.serverError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.OK{OK: true})
}

// UploadAttachment handles POST /v1/sessions/{id}/attachments.
//
// @Summary   Attach files to a chat session (parsed on the high-priority lanes)
// @Description Files go into the session's temporary knowledge base, which is added to the session's kb_ids so the agent can search them while parsing continues.
// @Tags      Sessions
// @Accept    mpfd
// @Produce   json
// @Param     id        path      string  true   "Session id"  format(uuid)
// @Param     file      formData  file    true   "File (repeat for several files)"
// @Param     metadata  formData  string  false  "JSON metadata for every file"
// @Success   202       {object}  document.UploadResult
// @Failure   404       {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/sessions/{id}/attachments [post]
func (h *Handlers) UploadAttachment(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	sess, err := h.Store.Sessions.Get(ctx, id)
	if err != nil || sess.UserID != u.ID {
		h.notFound(c, "session not found")
		return
	}
	kb, err := h.Docs.EnsureTempKB(ctx, u.ID, id)
	if err != nil {
		h.serverError(c, err)
		return
	}
	res, err := h.upload(ctx, c, u.ID, kb.ID, true)
	if err != nil {
		if err == errMultipart {
			h.badRequest(c, err.Error())
			return
		}
		h.serviceError(c, err)
		return
	}
	if err := h.attachKB(ctx, sess.ID, sess.Metadata, kb.ID); err != nil {
		h.serverError(c, err)
		return
	}
	c.JSON(consts.StatusAccepted, res)
}
