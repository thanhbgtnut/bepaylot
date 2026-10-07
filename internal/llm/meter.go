package llm

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"

	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// Caller says who a model call is made for, so its cost lands on the right
// user (§8.5). Kind is chat, sheet or parse.
type Caller struct {
	UserID *uuid.UUID
	CaseID *uuid.UUID
	Kind   string
}

type callerKey struct{}

// WithCaller attributes the model calls made with ctx.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom returns the caller of ctx; Kind defaults to chat.
func CallerFrom(ctx context.Context) Caller {
	c, _ := ctx.Value(callerKey{}).(Caller)
	if c.Kind == "" {
		c.Kind = "chat"
	}
	return c
}

// ErrBudgetExceeded is returned instead of calling the model when the user
// has spent the monthly limit (§8.5).
var ErrBudgetExceeded = errors.New("budget_exceeded: the monthly cost limit of this account is reached (đã dùng hết giới hạn chi phí tháng này)")

// Meter checks the budget before a model call and records its cost after.
type Meter interface {
	Allow(ctx context.Context, c Caller) error
	Record(ctx context.Context, c Caller, model string, tokensIn, tokensOut int)
}

// SetMeter meters every model built from now on.
func (r *Registry) SetMeter(m Meter) { r.meter = m }

type meteredProvider struct {
	Provider
	meter Meter
}

func (p meteredProvider) Model(ctx context.Context, modelID string, opt Options) (model.ToolCallingChatModel, error) {
	cm, err := p.Provider.Model(ctx, modelID, opt)
	if err != nil {
		return nil, err
	}
	return &meteredModel{inner: cm, model: modelID, meter: p.meter}, nil
}

// meteredModel records the token usage of each call. Its budget check runs
// before the call, so a user over the limit costs nothing more.
type meteredModel struct {
	inner model.ToolCallingChatModel
	model string
	meter Meter
}

func (m *meteredModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &meteredModel{inner: inner, model: m.model, meter: m.meter}, nil
}

func (m *meteredModel) IsCallbacksEnabled() bool { return components.IsCallbacksEnabled(m.inner) }

func (m *meteredModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	c := CallerFrom(ctx)
	if err := m.meter.Allow(ctx, c); err != nil {
		return nil, err
	}
	out, err := m.inner.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	if out != nil && out.ResponseMeta != nil && out.ResponseMeta.Usage != nil {
		u := out.ResponseMeta.Usage
		m.meter.Record(context.WithoutCancel(ctx), c, m.model, u.PromptTokens, u.CompletionTokens)
	} else {
		m.meter.Record(context.WithoutCancel(ctx), c, m.model, estimate(in), estimateText(out))
	}
	return out, nil
}

func (m *meteredModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	c := CallerFrom(ctx)
	if err := m.meter.Allow(ctx, c); err != nil {
		return nil, err
	}
	sr, err := m.inner.Stream(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	copies := sr.Copy(2)
	rctx := context.WithoutCancel(ctx)
	go func() {
		defer copies[0].Close()
		var pin, pout int
		var text strings.Builder
		for {
			chunk, err := copies[0].Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) && pin == 0 && pout == 0 && text.Len() == 0 {
					return // failed before producing anything: nothing billed
				}
				break
			}
			if chunk == nil {
				continue
			}
			text.WriteString(chunk.Content)
			if rm := chunk.ResponseMeta; rm != nil && rm.Usage != nil {
				pin = max(pin, rm.Usage.PromptTokens)
				pout = max(pout, rm.Usage.CompletionTokens)
			}
		}
		if pin == 0 && pout == 0 {
			pin, pout = estimate(in), (text.Len()+3)/4
		}
		m.meter.Record(rctx, c, m.model, pin, pout)
	}()
	return copies[1], nil
}

// estimate approximates tokens when the provider reports no usage.
func estimate(msgs []*schema.Message) int {
	n := 0
	for _, m := range msgs {
		if m != nil {
			n += len(m.Content)
		}
	}
	return (n + 3) / 4
}

func estimateText(m *schema.Message) int {
	if m == nil {
		return 0
	}
	return (len(m.Content) + 3) / 4
}

// keyOverride is the shared LLM key set from Settings (§8.5); it replaces
// the configured key of the providers that used the shared key.
type keyOverride struct{ v atomic.Pointer[string] }

func (k *keyOverride) get(configured string) string {
	if p := k.v.Load(); p != nil && *p != "" {
		return *p
	}
	return configured
}

// KeyInfo describes the shared LLM key for the admin settings page.
type KeyInfo struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	BaseURL  string `json:"base_url"`
	Masked   string `json:"masked_key"`
	Override bool   `json:"overridden"`
}

// SharedKeyInfo returns the default provider's key, masked.
func (r *Registry) SharedKeyInfo() KeyInfo {
	info := KeyInfo{Provider: r.def}
	p, ok := r.providers[r.def]
	if !ok {
		return info
	}
	info.Kind = p.Kind()
	if kp, ok := p.(keyed); ok {
		key, base, over := kp.keyState()
		info.BaseURL, info.Override = base, over
		info.Masked = Mask(key)
	}
	return info
}

// SetSharedKey replaces the key of the default provider and of every
// provider configured with the same key (one key for all models, U46).
func (r *Registry) SetSharedKey(key string) {
	def, ok := r.providers[r.def].(keyed)
	if !ok {
		return
	}
	shared := def.configuredKey()
	for _, p := range r.providers {
		if kp, ok := p.(keyed); ok && (kp == def || kp.configuredKey() == shared) {
			kp.override().v.Store(&key)
		}
	}
}

// keyed is a provider whose API key can be replaced at run time.
type keyed interface {
	configuredKey() string
	override() *keyOverride
	keyState() (key, baseURL string, overridden bool)
}

// Mask shows the last 4 characters of a key.
func Mask(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return "••••"
	}
	return "••••••••" + key[len(key)-4:]
}

var (
	_ model.ToolCallingChatModel = (*meteredModel)(nil)
	_ components.Checker         = (*meteredModel)(nil)
)
