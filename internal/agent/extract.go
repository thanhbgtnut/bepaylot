package agent

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/schema"

	"github.com/thanhenti/bepaylot/internal/llm"
)

// ExtractImage is one image attached to an extraction request.
type ExtractImage struct {
	MIME string // image/jpeg, image/png
	Data []byte
}

// ExtractRequest asks the agent's model to read images (§5.9): the parser's
// VLM engine sends every page-region extraction through here, so it uses the
// same provider registry, retries and streaming as a chat turn instead of a
// separate HTTP client.
type ExtractRequest struct {
	Provider    string // "" = the agent's default provider
	Model       string // "" = the agent's default model
	Prompt      string
	Images      []ExtractImage
	MaxTokens   int
	Temperature *float32
	// OnDelta, when set, receives the answer as it streams.
	OnDelta func(chunk string)
}

// ExtractResult is the streamed answer, joined.
type ExtractResult struct {
	Text             string
	Model            string
	FinishReason     string
	PromptTokens     int
	CompletionTokens int
}

var reThink = regexp.MustCompile(`(?s)<think>.*?</think>`)

// Extract runs one extraction request as a streaming model call. It has no
// session, history, skills or tools: the prompt and images are the whole
// input, which keeps an extraction's token cost to what the page needs.
func (a *Agent) Extract(ctx context.Context, req ExtractRequest) (ExtractResult, error) {
	// Keep the inner stream away from the callbacks of a surrounding turn
	// (same reason as llm.JSONCompleter).
	ctx = callbacks.InitCallbacks(ctx, &callbacks.RunInfo{Name: "Extract"})
	p, err := a.registry.Get(req.Provider)
	if err != nil {
		return ExtractResult{}, err
	}
	modelID := req.Model
	if modelID == "" {
		modelID = a.lcfg.DefaultModel
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	cm, err := p.Model(ctx, modelID, llm.Options{MaxTokens: maxTokens, Temperature: req.Temperature})
	if err != nil {
		return ExtractResult{}, fmt.Errorf("extract: build chat model: %w", err)
	}

	parts := []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeText, Text: req.Prompt}}
	for _, img := range req.Images {
		data := base64.StdEncoding.EncodeToString(img.Data)
		mime := img.MIME
		if mime == "" {
			mime = "image/jpeg"
		}
		parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{
			MessagePartCommon: schema.MessagePartCommon{Base64Data: &data, MIMEType: mime},
			Detail:            schema.ImageURLDetailHigh,
		}})
	}
	msgs := []*schema.Message{{Role: schema.User, UserInputMultiContent: parts}}

	stream, err := cm.Stream(ctx, msgs)
	if err != nil {
		return ExtractResult{}, err
	}
	defer stream.Close()
	res := ExtractResult{Model: modelID}
	var sb strings.Builder
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ExtractResult{}, err
		}
		if chunk == nil {
			continue
		}
		if chunk.Content != "" {
			sb.WriteString(chunk.Content)
			if req.OnDelta != nil {
				req.OnDelta(chunk.Content)
			}
		}
		if rm := chunk.ResponseMeta; rm != nil {
			if rm.FinishReason != "" {
				res.FinishReason = rm.FinishReason
			}
			if rm.Usage != nil {
				res.PromptTokens, res.CompletionTokens = rm.Usage.PromptTokens, rm.Usage.CompletionTokens
			}
		}
	}
	res.Text = strings.TrimSpace(reThink.ReplaceAllString(sb.String(), ""))
	return res, nil
}
