// Package vlm refines OCR with a vision-language model (§5.9): a layout
// engine (TurboOCR) finds the regions of a page; regions of one page whose
// classes share a group are stitched into one numbered image and read in one
// call, titles and tables are read one per call, seals are tagged without a
// call. The transcriptions travel in RawRegion.Text to package assemble,
// which aligns them with the OCR lines per page.
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

// DefaultPrompt is olmOCR's own page prompt (v4, YAML front matter), used for
// a region read on its own. Other models work with it too; the front matter
// is stripped when present.
const DefaultPrompt = "Attached is one page of a document that you must process. Just return the plain text representation of this document as if you were reading it naturally. Convert equations to LateX and tables to HTML.\n" +
	"If there are any figures or charts, label them with the following markdown syntax ![Alt text describing the contents of the figure](page_startx_starty_width_height.png)\n" +
	"Return your output as markdown, with a front matter section on top specifying values for the primary_language, is_rotation_valid, rotation_correction, is_table, and is_diagram parameters."

// DefaultBatchPrompt reads a stitched batch; %d is the number of regions.
const DefaultBatchPrompt = "The image stacks %d regions cut from the same document page, top to bottom. " +
	"A black bar with a number [k] sits above each region.\n" +
	"Transcribe every region exactly as written, in order. Start each region on a new line with its marker <<<k>>> " +
	"(k is the number on its bar), then the region's text as plain markdown: keep the original language and diacritics, " +
	"tables as HTML, equations as LaTeX. Do not transcribe the bars or numbers, do not add front matter, comments or code fences. " +
	"If a region has no text, write its marker alone."

// Request is one model call: a prompt and one JPEG image.
type Request struct {
	Prompt string
	JPEG   []byte
	// Regions are the layout ids the image holds, in order (debugging and
	// tests; the model only sees the image and the prompt).
	Regions []int
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
