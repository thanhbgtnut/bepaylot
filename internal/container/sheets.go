package container

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/agent"
	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/docmodel"
	"github.com/thanhenti/bepaylot/internal/application/service/usage"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/types"
)

// extractRunner runs one extraction turn of a sheet build (§6.9.3) in a hidden
// session of the case: the agent gets the text of one document and only the
// arithmetic and JSON tools, never the case tools or MCP tools of a chat. The
// session carries metadata.extract_document_id, which hides it from the chat
// history and bills the turn as kind parse (§8.5).
type extractRunner struct {
	ag  *agent.Agent
	st  *postgres.Store
	cfg config.LLM
}

func (r extractRunner) RunExtract(ctx context.Context, user types.User, caseID, doc uuid.UUID, text string) (string, error) {
	sess, err := r.st.Sessions.Create(ctx, postgres.CreateParams{
		UserID: user.ID, Title: "Trích xuất trường", Provider: r.cfg.DefaultProvider, Model: r.cfg.DefaultModel,
		Metadata: map[string]any{types.SessionExtract: doc.String()}, CaseID: &caseID,
	})
	if err != nil {
		return "", err
	}
	out, err := r.ag.Run(ctx, agent.RunInput{User: user, Session: sess, UserText: text, Provider: r.cfg.DefaultProvider, Model: r.cfg.DefaultModel,
		Tools: docmodel.ExtractTools}, nil)
	var b strings.Builder
	for _, blk := range out.AssistantMessage.Blocks {
		if blk.Type == types.BlockText {
			b.WriteString(blk.Text)
		}
	}
	return b.String(), err
}

// billParse attributes the model calls of a per-document pipeline task (VLM
// pages, the document card) to the document's uploader (§8.5).
func billParse(meter *usage.Service, handlers map[string]queue.Handler) {
	for name, h := range handlers {
		h := h
		handlers[name] = func(ctx context.Context, payload []byte) error {
			var p types.DocTaskPayload
			if json.Unmarshal(payload, &p) == nil && p.DocumentID != uuid.Nil {
				ctx = llm.WithCaller(ctx, meter.ParseCaller(ctx, p.DocumentID))
			}
			return h(ctx, payload)
		}
	}
}
