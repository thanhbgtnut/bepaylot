package document

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/webhook"
)

// Completion callbacks (§4.7). A document uploaded with a callback_url gets
// one delivery per terminal status of each generation (and page-reparse run):
// a JSON POST retried with backoff until a 2xx or max_attempts. Every attempt
// is stored; deliveries can be listed and re-armed through the API.

// updateStatus applies a document update and, when it moves the document to
// a terminal status, schedules its callback. Scheduling errors are logged:
// housekeeping creates missed deliveries later.
func (s *Service) updateStatus(ctx context.Context, id uuid.UUID, gen int, u postgres.DocUpdate) (bool, error) {
	ok, err := s.st.Documents.Update(ctx, id, gen, u)
	if err != nil || !ok || u.Status == nil || types.CallbackEvent(*u.Status) == "" {
		return ok, err
	}
	if err := s.scheduleCallback(context.WithoutCancel(ctx), id); err != nil {
		s.log.Warn("callback: schedule failed", "doc", id, "err", err)
	}
	return ok, nil
}

// ValidateCallbackURL checks a callback URL against the configured policy.
func (s *Service) ValidateCallbackURL(raw string) error {
	if err := webhook.ValidateURL(raw, s.cfg.Callback.AllowPrivateNetworks); err != nil {
		return fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	return nil
}

// scheduleCallback creates the delivery for the document's current state and
// enqueues its first attempt. It is idempotent per (generation, run, URL).
func (s *Service) scheduleCallback(ctx context.Context, docID uuid.UUID) error {
	d, err := s.st.Documents.GetAny(ctx, docID)
	if err != nil {
		return err
	}
	event := types.CallbackEvent(d.Status)
	if d.CallbackURL == "" || event == "" {
		return nil
	}
	id := uuid.New()
	cb, created, err := s.st.Callbacks.Create(ctx, types.DocumentCallback{
		ID: id, DocumentID: d.ID, Gen: d.Gen, Run: d.CallbackRun, Event: event, URL: d.CallbackURL,
		MaxAttempts: s.cfg.Callback.MaxAttempts, Payload: callbackPayload(id, event, d, s.caseCode(ctx, d.CaseID)),
	})
	if err != nil || !created {
		return err
	}
	return s.enqueueCallback(ctx, cb.ID, 1, 0, "")
}

func (s *Service) enqueueCallback(ctx context.Context, id uuid.UUID, attempt int, delay time.Duration, suffix string) error {
	return s.q.Enqueue(ctx, types.TaskDocumentCallback, types.CallbackTaskPayload{CallbackID: id, Attempt: attempt},
		queue.Opts{TaskID: fmt.Sprintf("cb:%s:%d%s", id, attempt, suffix), ProcessIn: delay})
}

// callbackPayload is the JSON body. It is fixed when the delivery is created,
// so every attempt sends the same bytes (and signature input).
func callbackPayload(id uuid.UUID, event string, d types.Document, caseCode string) map[string]any {
	doc := map[string]any{
		"id": d.ID, "kb_id": d.KBID, "case_id": d.CaseID, "case_code": caseCode, "file_name": d.FileName, "mime_type": d.MimeType, "size_bytes": d.SizeBytes,
		"gen": d.Gen, "status": d.Status, "parse_status": d.ParseStatus, "index_status": d.IndexStatus,
		"page_count": d.PageCount, "pages_done": d.PagesDone, "pages_failed": d.PagesFailed,
		"metadata": nonNil(d.Metadata), "created_at": d.CreatedAt, "updated_at": d.UpdatedAt,
	}
	if d.BatchID != nil {
		doc["batch_id"] = *d.BatchID
	}
	for k, v := range map[string]string{"error": d.Error, "title": d.Title, "summary": d.Summary} {
		if v != "" {
			doc[k] = v
		}
	}
	return map[string]any{
		"event": event, "delivery_id": id, "occurred_at": time.Now().UTC().Format(time.RFC3339Nano), "document": doc,
		"links": map[string]string{
			"document": "/v1/documents/" + d.ID.String(),
			"markdown": "/v1/documents/" + d.ID.String() + "/markdown",
			"pages":    "/v1/documents/" + d.ID.String() + "/pages",
		},
	}
}

// caseCode returns the code of a case, "" when it is gone.
func (s *Service) caseCode(ctx context.Context, id uuid.UUID) string {
	if s.cases == nil {
		return ""
	}
	c, err := s.cases.GetCase(ctx, id)
	if err != nil {
		return ""
	}
	return c.Code
}

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// deliverCallback runs one attempt: POST, record the outcome, schedule the
// next attempt or settle the delivery as succeeded/failed. Delivery is
// at-least-once; receivers deduplicate on X-Bepaylot-Delivery.
func (s *Service) deliverCallback(ctx context.Context, raw []byte) error {
	var p types.CallbackTaskPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("%w: bad payload: %v", queue.ErrSkipRetry, err)
	}
	cb, err := s.st.Callbacks.Get(ctx, p.CallbackID)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil // document purged
	}
	if err != nil {
		return err
	}
	attempt := cb.Attempts + 1
	if cb.State != types.CallbackPending || (p.Attempt > 0 && p.Attempt < attempt) {
		return nil // settled, or a stale duplicate task
	}
	body, err := json.Marshal(cb.Payload)
	if err != nil {
		return fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
	}
	res := s.hooks.Send(ctx, webhook.Delivery{URL: cb.URL, Event: cb.Event, ID: cb.ID.String(), Attempt: attempt, Body: body})

	out := postgres.AttemptOutcome{Attempt: attempt, StatusCode: res.StatusCode, Response: res.Response, Duration: res.Duration}
	var delay time.Duration
	switch {
	case res.OK():
		out.State = types.CallbackSucceeded
	case attempt >= cb.MaxAttempts:
		out.State, out.Error = types.CallbackFailed, res.Error()
	default:
		delay = s.callbackBackoff(attempt)
		next := time.Now().Add(delay)
		out.State, out.Error, out.NextAttemptAt = types.CallbackPending, res.Error(), &next
	}
	recorded, err := s.st.Callbacks.RecordAttempt(context.WithoutCancel(ctx), cb.ID, out)
	if err != nil || !recorded {
		return err
	}
	log := s.log.With("callback", cb.ID, "doc", cb.DocumentID, "event", cb.Event, "attempt", attempt, "status_code", res.StatusCode)
	switch out.State {
	case types.CallbackSucceeded:
		log.Info("callback delivered", "ms", res.Duration.Milliseconds())
	case types.CallbackFailed:
		log.Warn("callback failed permanently", "err", out.Error, "attempts", attempt)
	default:
		log.Warn("callback attempt failed, will retry", "err", out.Error, "retry_in", delay)
		return s.enqueueCallback(ctx, cb.ID, attempt+1, delay, "")
	}
	return nil
}

// callbackBackoff is the wait before attempt n+1; the last value repeats.
func (s *Service) callbackBackoff(n int) time.Duration {
	b := s.cfg.Callback.Backoff
	if len(b) == 0 {
		return time.Minute
	}
	return b[min(max(n-1, 0), len(b)-1)]
}

// Callbacks lists a document's deliveries with their attempts.
func (s *Service) Callbacks(ctx context.Context, owner, docID uuid.UUID) ([]types.DocumentCallback, error) {
	d, err := s.GetOwned(ctx, owner, docID)
	if err != nil {
		return nil, err
	}
	return s.st.Callbacks.ListByDocument(ctx, d.ID)
}

// RetryCallback re-arms a delivery (the latest one when callbackID is Nil)
// for another max_attempts attempts and sends it now. A finished document
// with a callback URL but no delivery yet gets one.
func (s *Service) RetryCallback(ctx context.Context, owner, docID, callbackID uuid.UUID) (types.DocumentCallback, error) {
	d, err := s.GetOwned(ctx, owner, docID)
	if err != nil {
		return types.DocumentCallback{}, err
	}
	list, err := s.st.Callbacks.ListByDocument(ctx, d.ID)
	if err != nil {
		return types.DocumentCallback{}, err
	}
	var target *types.DocumentCallback
	for i := range list {
		if callbackID == uuid.Nil || list[i].ID == callbackID {
			target = &list[i]
			break
		}
	}
	if target == nil {
		if callbackID != uuid.Nil {
			return types.DocumentCallback{}, ErrNotFound
		}
		if d.CallbackURL == "" || types.CallbackEvent(d.Status) == "" {
			return types.DocumentCallback{}, fmt.Errorf("%w: document has no callback to send (callback_url empty or status %s is not final)", ErrBadRequest, d.Status)
		}
		if err := s.scheduleCallback(ctx, d.ID); err != nil {
			return types.DocumentCallback{}, err
		}
		list, err = s.st.Callbacks.ListByDocument(ctx, d.ID)
		if err != nil || len(list) == 0 {
			return types.DocumentCallback{}, err
		}
		return list[0], nil
	}
	cb, err := s.st.Callbacks.Rearm(ctx, target.ID, s.cfg.Callback.MaxAttempts)
	if err != nil {
		return cb, err
	}
	return cb, s.enqueueCallback(ctx, cb.ID, cb.Attempts+1, 0, fmt.Sprintf(":m%d", time.Now().UnixNano()))
}

// callbackHousekeeping creates deliveries missed by a crash and re-enqueues
// pending ones whose task is overdue.
func (s *Service) callbackHousekeeping(ctx context.Context, bucket int64) {
	missing, err := s.st.Callbacks.MissingForTerminal(ctx, time.Now().Add(-time.Minute), 200)
	if err != nil {
		s.log.Warn("housekeeping: callbacks", "err", err)
		return
	}
	for _, id := range missing {
		if err := s.scheduleCallback(ctx, id); err != nil {
			s.log.Warn("housekeeping: schedule callback", "doc", id, "err", err)
		}
	}
	overdue, err := s.st.Callbacks.Overdue(ctx, time.Now().Add(-5*time.Minute), 200)
	if err != nil {
		s.log.Warn("housekeeping: overdue callbacks", "err", err)
		return
	}
	for _, cb := range overdue {
		_ = s.enqueueCallback(ctx, cb.ID, cb.Attempts+1, 0, fmt.Sprintf(":hk%d", bucket))
	}
}
