package llm

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// dummyModel answers with fixed usage, as a provider would.
type dummyModel struct{ calls int }

func (d *dummyModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return d, nil }
func (d *dummyModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	d.calls++
	return &schema.Message{Content: "ok", ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 100, CompletionTokens: 20}}}, nil
}
func (d *dummyModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	d.calls++
	return schema.StreamReaderFromArray([]*schema.Message{
		{Content: "a"},
		{Content: "b", ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 300, CompletionTokens: 40}}},
	}), nil
}

type recMeter struct {
	mu    sync.Mutex
	block bool
	recs  []string
	in    []int
	kinds []string
}

func (m *recMeter) Allow(context.Context, Caller) error {
	if m.block {
		return ErrBudgetExceeded
	}
	return nil
}
func (m *recMeter) Record(_ context.Context, c Caller, model string, in, out int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs, m.in, m.kinds = append(m.recs, model), append(m.in, in+out), append(m.kinds, c.Kind)
}

func TestMeteredModel(t *testing.T) {
	inner, meter := &dummyModel{}, &recMeter{}
	m := &meteredModel{inner: inner, model: "m", meter: meter}
	uid := uuid.New()
	ctx := WithCaller(context.Background(), Caller{UserID: &uid, Kind: "sheet"})

	if _, err := m.Generate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	sr, err := m.Stream(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := sr.Recv(); errors.Is(err, io.EOF) {
			break
		}
	}
	for i := 0; i < 50; i++ { // the stream copy is drained in the background
		meter.mu.Lock()
		n := len(meter.recs)
		meter.mu.Unlock()
		if n == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(meter.in) != 2 || meter.in[0] != 120 || meter.in[1] != 340 || meter.kinds[0] != "sheet" {
		t.Fatalf("records = %v %v %v", meter.recs, meter.in, meter.kinds)
	}

	// Over the limit: refused before the provider is called.
	meter.block = true
	if _, err := m.Generate(ctx, nil); !errors.Is(err, ErrBudgetExceeded) || inner.calls != 2 {
		t.Fatalf("blocked generate: %v, provider calls %d", err, inner.calls)
	}
	if _, err := m.Stream(ctx, nil); !errors.Is(err, ErrBudgetExceeded) || inner.calls != 2 {
		t.Fatalf("blocked stream: %v, provider calls %d", err, inner.calls)
	}
}
