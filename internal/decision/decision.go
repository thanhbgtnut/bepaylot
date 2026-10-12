// Package decision is the client of the Decision API, Ollama's System One
// endpoint (POST /v1/systemone, https://docs.ollama.com/api/systemone): a
// typed question about an image, answered with a choice and its
// probabilities. bepaylot uses it to read the document type of a page
// (§6.9.4).
package decision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // page images are JPEG; PNG is accepted too
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/image/draw"

	"github.com/thanhenti/bepaylot/internal/types"
)

// Config is the classify.decision block of the configuration.
type Config struct {
	BaseURL      string
	Model        string
	Instructions string
	Timeout      time.Duration
	MaxSide      int
}

// Client calls the Decision API.
type Client struct {
	cfg  Config
	http *http.Client
}

// New builds a client.
func New(cfg Config) *Client {
	cfg.BaseURL = strings.TrimSuffix(strings.TrimRight(cfg.BaseURL, "/"), "/v1/systemone")
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
}

const question = "document_type"

// breakQuestion asks whether the page starts a document or continues the one
// on the page before it: consecutive pages of one type are cut into separate
// documents at every page answered breakFirst (§6.9.4).
const (
	breakQuestion     = "is_break"
	breakFirst        = "FIRST"
	breakContinuation = "CONTINUATION"
	breakInstructions = "Trang này là trang đầu tiên của một tài liệu mới, hay là trang tiếp theo của tài liệu ở trang trước?"
)

var breakCriteria = map[string]string{
	breakFirst:        "Trang đầu tiên của một tài liệu: có tiêu đề, số hiệu, ngày tháng hoặc phần mở đầu của tài liệu",
	breakContinuation: "Trang tiếp theo của một tài liệu: nội dung nối tiếp trang trước, không có tiêu đề mở đầu",
}

type request struct {
	Model     string                    `json:"model"`
	State     string                    `json:"state"`
	Images    []string                  `json:"images"`
	Questions map[string]choiceQuestion `json:"questions"`
}

type choiceQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type response struct {
	Answers map[string]struct {
		Choice     string  `json:"choice"`
		Confidence float64 `json:"confidence"`
	} `json:"answers"`
}

// ClassifyPage implements interfaces.PageClassifier: it asks which of labels
// the page image is, and in the same call whether the page starts a document
// (is_break). The image goes inline as raw base64: the endpoint takes
// neither URLs nor data URLs, and a "data:" prefix is refused as illegal
// base64. An answer without is_break is a page that does not break.
func (c *Client) ClassifyPage(ctx context.Context, labels []types.ClassLabel, img []byte) (string, float64, bool, error) {
	criteria := make(map[string]string, len(labels))
	for _, l := range labels {
		criteria[l.Name] = l.Title
		if l.Description != "" {
			criteria[l.Name] = l.Title + " — " + l.Description
		}
	}
	body, err := json.Marshal(request{
		Model: c.cfg.Model, State: "Ảnh chứng từ:",
		Images: []string{base64.StdEncoding.EncodeToString(downscale(img, c.cfg.MaxSide))},
		Questions: map[string]choiceQuestion{
			question:      {Type: "choice", Instructions: c.cfg.Instructions, Criteria: criteria},
			breakQuestion: {Type: "choice", Instructions: breakInstructions, Criteria: breakCriteria},
		},
	})
	if err != nil {
		return "", 0, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return "", 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", 0, false, fmt.Errorf("decision api: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", 0, false, fmt.Errorf("decision api: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), 300)])))
	}
	var out response
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", 0, false, fmt.Errorf("decision api: %w", err)
	}
	a, ok := out.Answers[question]
	if _, known := criteria[a.Choice]; !ok || !known {
		return "", 0, false, fmt.Errorf("decision api: no valid choice in the answer (%q)", a.Choice)
	}
	return a.Choice, a.Confidence, out.Answers[breakQuestion].Choice == breakFirst, nil
}

// downscale re-encodes an image whose long side is above maxSide; any other
// image (or one that does not decode) is sent as it is.
func downscale(b []byte, maxSide int) []byte {
	if maxSide <= 0 {
		return b
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || max(cfg.Width, cfg.Height) <= maxSide {
		return b
	}
	src, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return b
	}
	f := float64(maxSide) / float64(max(cfg.Width, cfg.Height))
	dst := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(cfg.Width)*f+0.5)), max(1, int(float64(cfg.Height)*f+0.5))))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 90}); err != nil {
		return b
	}
	return buf.Bytes()
}
