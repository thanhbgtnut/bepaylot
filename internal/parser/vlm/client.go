// Package vlm refines OCR with a vision-language model (§5.9): a layout
// engine (TurboOCR) finds the regions of a page, each region is cropped and
// transcribed concurrently by an OpenAI-compatible chat endpoint (e.g.
// allenai/olmocr-2-7b served by vLLM or LM Studio), and the transcriptions
// travel in RawRegion.Text to package assemble, which aligns them with the
// OCR lines per page.
package vlm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// DefaultPrompt is olmOCR's own page prompt (v4, YAML front matter). Other
// models work with it too; the front matter is stripped when present.
const DefaultPrompt = "Attached is one page of a document that you must process. Just return the plain text representation of this document as if you were reading it naturally. Convert equations to LateX and tables to HTML.\n" +
	"If there are any figures or charts, label them with the following markdown syntax ![Alt text describing the contents of the figure](page_startx_starty_width_height.png)\n" +
	"Return your output as markdown, with a front matter section on top specifying values for the primary_language, is_rotation_valid, rotation_correction, is_table, and is_diagram parameters."

// ClientConfig configures the chat client.
type ClientConfig struct {
	BaseURL     string // up to and including /v1
	APIKey      string
	Model       string
	Prompt      string
	MaxTokens   int
	Temperature float64
	Timeout     time.Duration
	HTTPClient  *http.Client
}

// Client calls POST {base}/chat/completions with one image per request.
type Client struct {
	cfg ClientConfig
	hc  *http.Client
}

// NewClient returns a client with defaults filled in.
func NewClient(cfg ClientConfig) *Client {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Prompt == "" {
		cfg.Prompt = DefaultPrompt
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 4096
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 180 * time.Second
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: cfg.Timeout}
	}
	return &Client{cfg: cfg, hc: hc}
}

// Model returns the configured model name.
func (c *Client) Model() string { return c.cfg.Model }

// Transcription is one parsed model answer.
type Transcription struct {
	Text string `json:"text"`
	// Meta is the YAML front matter (olmOCR: primary_language, is_table…).
	Meta             map[string]string `json:"meta,omitempty"`
	PromptTokens     int               `json:"prompt_tokens,omitempty"`
	CompletionTokens int               `json:"completion_tokens,omitempty"`
	// Truncated is set when the model stopped at max_tokens.
	Truncated bool `json:"truncated,omitempty"`
}

// StatusError is a non-2xx answer; 5xx and 429 are retryable.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string { return fmt.Sprintf("vlm: status %d: %s", e.Code, e.Body) }

// Retryable reports whether an error is worth another attempt.
func Retryable(err error) bool {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code >= 500 || se.Code == http.StatusTooManyRequests
	}
	return err != nil && !errors.Is(err, context.Canceled)
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
}

type chatMessage struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Transcribe sends one JPEG image and returns the cleaned transcription.
func (c *Client) Transcribe(ctx context.Context, jpeg []byte) (*Transcription, error) {
	if c.cfg.BaseURL == "" {
		return nil, errors.New("vlm: base_url is not configured")
	}
	body, err := json.Marshal(chatRequest{
		Model: c.cfg.Model, MaxTokens: c.cfg.MaxTokens, Temperature: c.cfg.Temperature,
		Messages: []chatMessage{{Role: "user", Content: []contentPart{
			{Type: "text", Text: c.cfg.Prompt},
			{Type: "image_url", ImageURL: &imageURL{URL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg)}},
		}}},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vlm: request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("vlm: read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, &StatusError{Code: resp.StatusCode, Body: truncate(string(raw), 300)}
	}
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, fmt.Errorf("vlm: decode response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return nil, errors.New("vlm: empty choices")
	}
	meta, text := SplitFrontMatter(cr.Choices[0].Message.Content)
	return &Transcription{
		Text: text, Meta: meta,
		PromptTokens: cr.Usage.PromptTokens, CompletionTokens: cr.Usage.CompletionTokens,
		Truncated: cr.Choices[0].FinishReason == "length",
	}, nil
}

// Health lists the models and checks that the configured one is served.
func (c *Client) Health(ctx context.Context) error {
	if c.cfg.BaseURL == "" {
		return errors.New("vlm: base_url is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+"/models", nil)
	if err != nil {
		return err
	}
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("vlm: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return &StatusError{Code: resp.StatusCode}
	}
	var ml struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&ml); err != nil || c.cfg.Model == "" {
		return nil // not every server lists models
	}
	for _, m := range ml.Data {
		if m.ID == c.cfg.Model {
			return nil
		}
	}
	return fmt.Errorf("vlm: model %q is not served at %s", c.cfg.Model, c.cfg.BaseURL)
}

var (
	reFrontMatter = regexp.MustCompile(`(?s)^\s*---\s*\n(.*?)\n---\s*(?:\n|$)`)
	reFence       = regexp.MustCompile("(?s)^\\s*```[a-zA-Z]*\\s*\n(.*?)\n```\\s*$")
)

// SplitFrontMatter separates a leading YAML front matter block (flat
// key: value pairs) from the text, and unwraps a whole-answer code fence.
func SplitFrontMatter(s string) (map[string]string, string) {
	s = strings.TrimPrefix(s, "\uFEFF")
	var meta map[string]string
	if m := reFrontMatter.FindStringSubmatchIndex(s); m != nil {
		meta = map[string]string{}
		for _, line := range strings.Split(s[m[2]:m[3]], "\n") {
			k, v, ok := strings.Cut(line, ":")
			if ok {
				meta[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
			}
		}
		s = s[m[1]:]
	}
	if m := reFence.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	return meta, strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
