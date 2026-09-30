package container

import (
	"context"
	"regexp"

	"github.com/thanhenti/bepaylot/internal/agent"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/parser/vlm"
)

// agentVLM is the Transcriber of engine turboocr_vlm: every page (image +
// OCR text) is an extraction request to the agent (agent.Extract), streamed
// on the agent's provider registry, rather than a separate HTTP client (§5.9).
type agentVLM struct {
	ag          *agent.Agent
	reg         *llm.Registry
	provider    string
	model       string
	maxTokens   int
	temperature float32
}

func (v *agentVLM) Model() string { return v.model }

// Health checks that the provider is configured; the model is exercised by
// the first page.
func (v *agentVLM) Health(context.Context) error {
	_, err := v.reg.Get(v.provider)
	return err
}

// reClientError matches an upstream 4xx other than 429 in a provider error.
var reClientError = regexp.MustCompile(`status(?: code)?:? ?4(?:0\d|1\d|2[0-8])\b`)

func (v *agentVLM) Transcribe(ctx context.Context, req vlm.Request) (*vlm.Transcription, error) {
	t := v.temperature
	res, err := v.ag.Extract(ctx, agent.ExtractRequest{
		Provider: v.provider, Model: v.model, Prompt: req.Prompt,
		Images:    []agent.ExtractImage{{MIME: "image/jpeg", Data: req.JPEG}},
		MaxTokens: v.maxTokens, Temperature: &t,
	})
	if err != nil {
		if reClientError.MatchString(err.Error()) {
			return nil, &vlm.PermanentError{Err: err}
		}
		return nil, err
	}
	meta, text := vlm.SplitFrontMatter(res.Text)
	return &vlm.Transcription{Text: text, Meta: meta, PromptTokens: res.PromptTokens, CompletionTokens: res.CompletionTokens,
		Truncated: res.FinishReason == "length" || res.FinishReason == "max_tokens"}, nil
}
