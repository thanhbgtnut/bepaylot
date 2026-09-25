package handler

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/types"
)

// setSessionKnowledge stores kb_ids / kb_filter on the session after checking
// the caller owns every knowledge base (§8.1).
func (h *Handlers) setSessionKnowledge(ctx context.Context, owner uuid.UUID, sess types.Session, kbIDs []string, filter map[string]any) (types.Session, error) {
	meta := map[string]any{}
	for k, v := range sess.Metadata {
		meta[k] = v
	}
	if len(kbIDs) > 0 {
		ids := make([]any, 0, len(kbIDs))
		for _, raw := range kbIDs {
			id, err := uuid.Parse(raw)
			if err != nil {
				return sess, fmt.Errorf("invalid kb id %q", raw)
			}
			if _, err := h.Store.KBs.GetOwned(ctx, id, owner); err != nil {
				return sess, fmt.Errorf("knowledge base %s not found", raw)
			}
			ids = append(ids, id.String())
		}
		meta["kb_ids"] = ids
	}
	if filter != nil {
		if len(filter) == 0 {
			delete(meta, "kb_filter")
		} else {
			meta["kb_filter"] = filter
		}
	}
	return h.Store.Sessions.Update(ctx, sess.ID, postgres.UpdateParams{Metadata: meta})
}

// attachKB appends a knowledge base to the session's kb_ids.
func (h *Handlers) attachKB(ctx context.Context, sessID uuid.UUID, current map[string]any, kb uuid.UUID) error {
	meta := map[string]any{}
	for k, v := range current {
		meta[k] = v
	}
	var ids []any
	if list, ok := meta["kb_ids"].([]any); ok {
		for _, x := range list {
			if fmt.Sprint(x) == kb.String() {
				return nil
			}
			ids = append(ids, x)
		}
	}
	meta["kb_ids"] = append(ids, kb.String())
	_, err := h.Store.Sessions.Update(ctx, sessID, postgres.UpdateParams{Metadata: meta})
	return err
}
