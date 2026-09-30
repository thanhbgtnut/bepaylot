// Package vlm refines OCR with a vision-language model (§5.9) in two steps:
// (1) TurboOCR reads the page (lines, boxes, layout); (2) one VLM call per
// page turns the whole page image into markdown, with the OCR text in the
// prompt so the model checks its answer against it (nothing missing, names
// and numbers right). The markdown is then split and aligned back onto the
// OCR regions (RawRegion.Text); package assemble aligns each region's text
// with its OCR lines, so positions still come from TurboOCR.
//
// The package does not talk to a model itself: every call goes through a
// Transcriber, which production wires to the agent (agent.Extract, a
// streaming call on the agent's provider registry).
package vlm

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// DefaultPrompt is olmOCR's own page prompt (v4, YAML front matter), used
// when there is no OCR text to send (full_page: the layout engine failed).
// Other models work with it too; the front matter is stripped when present.
const DefaultPrompt = "Attached is one page of a document that you must process. Just return the plain text representation of this document as if you were reading it naturally. Convert equations to LateX and tables to HTML.\n" +
	"If there are any figures or charts, label them with the following markdown syntax ![Alt text describing the contents of the figure](page_startx_starty_width_height.png)\n" +
	"Return your output as markdown, with a front matter section on top specifying values for the primary_language, is_rotation_valid, rotation_correction, is_table, and is_diagram parameters."

// OCRPlaceholder marks where PagePrompt receives the OCR text of the page;
// a prompt without it gets the OCR block appended.
const OCRPlaceholder = "{ocr}"

// DefaultPagePrompt reads one page with its OCR text as the check.
const DefaultPagePrompt = "Attached is one page of a document. Return the plain text representation of this page as markdown, " +
	"as if you were reading it naturally: keep the original language, spelling and diacritics; mark headings with #; " +
	"convert tables to HTML and equations to LaTeX. Do not add front matter, comments or code fences, and do not describe images.\n\n" +
	"Below is the text an OCR engine read from the same page, in reading order. Blank lines separate layout regions; " +
	"lines ending in " + LowConfMark + " were read with low confidence. Use it to check your answer: every piece of text in it " +
	"must appear in your answer, and names, numbers, dates and codes must match what the image shows. The OCR text may contain " +
	"mistakes such as missing diacritics or merged words; wherever it disagrees with the image, write what the image shows. " +
	"Never copy the " + LowConfMark + " marks.\n\n<ocr>\n" + OCRPlaceholder + "\n</ocr>"

// LowConfMark ends an OCR context line read with low confidence.
const LowConfMark = "[?]"

// Request is one model call: a prompt and one JPEG image.
type Request struct {
	Prompt string
	JPEG   []byte
	// PageNo is the page the image shows (logging and tests).
	PageNo int
}

// Transcriber answers one Request; the container wires it to the agent.
type Transcriber interface {
	Transcribe(ctx context.Context, req Request) (*Transcription, error)
	Health(ctx context.Context) error
	Model() string
}

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

// PermanentError marks a failure a retry cannot fix (bad request, unknown
// model); everything else is retried up to Config.Retries times.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Retryable reports whether an error is worth another attempt.
func Retryable(err error) bool {
	var pe *PermanentError
	return err != nil && !errors.As(err, &pe) && !errors.Is(err, context.Canceled)
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
