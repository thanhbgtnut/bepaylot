package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/cloudwego/eino/schema"
)

// JSONCompleter asks a model for a JSON answer (used by the index, search and
// graph modules). It extracts the first JSON value from the reply and, if it
// does not decode, asks the model once more to repair it.
type JSONCompleter struct {
	Reg         *Registry
	Provider    string
	Model       string
	MaxTokens   int
	Temperature float32

	calls atomic.Int64
}

// Calls reports how many model calls were made.
func (c *JSONCompleter) Calls() int64 { return c.calls.Load() }

// CompleteJSON implements interfaces.Completer.
func (c *JSONCompleter) CompleteJSON(ctx context.Context, system, user string, out any) error {
	p, err := c.Reg.Get(c.Provider)
	if err != nil {
		return err
	}
	maxTok := c.MaxTokens
	if maxTok <= 0 {
		maxTok = 4096
	}
	temp := c.Temperature
	m, err := p.Model(ctx, c.Model, Options{MaxTokens: maxTok, Temperature: &temp})
	if err != nil {
		return err
	}
	msgs := []*schema.Message{schema.SystemMessage(system), schema.UserMessage(user)}
	c.calls.Add(1)
	resp, err := m.Generate(ctx, msgs)
	if err != nil {
		return err
	}
	derr := DecodeJSON(resp.Content, out)
	if derr == nil {
		return nil
	}
	msgs = append(msgs, resp, schema.UserMessage(fmt.Sprintf(
		"Your reply was not valid JSON (%v). Reply again with only the corrected JSON value, no prose or code fences.", derr)))
	c.calls.Add(1)
	resp, err = m.Generate(ctx, msgs)
	if err != nil {
		return err
	}
	return DecodeJSON(resp.Content, out)
}

// DecodeJSON extracts the first JSON object or array from text (tolerating
// code fences and surrounding prose) and unmarshals it into out.
func DecodeJSON(text string, out any) error {
	s := strings.TrimSpace(text)
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if j := strings.Index(rest, "```"); j >= 0 {
			s = strings.TrimSpace(rest[:j])
		}
	}
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return errors.New("no JSON value in reply")
	}
	s = s[start:]
	end := matchingEnd(s)
	if end < 0 {
		return errors.New("unterminated JSON value")
	}
	return json.Unmarshal([]byte(s[:end+1]), out)
}

func matchingEnd(s string) int {
	depth := 0
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case ch == '\\':
				esc = true
			case ch == '"':
				inStr = false
			}
			continue
		}
		switch ch {
		case '"':
			inStr = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
