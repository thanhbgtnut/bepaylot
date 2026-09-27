package llm

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/thanhenti/bepaylot/internal/config"
)

// cbModel reports its run to the callbacks in ctx, as the eino-ext models do.
type cbModel struct{ model.ToolCallingChatModel }

func (cbModel) Generate(ctx context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	ctx = callbacks.OnStart(ctx, &model.CallbackInput{Messages: in})
	out := schema.AssistantMessage(`{"ok":true}`, nil)
	callbacks.OnEnd(ctx, &model.CallbackOutput{Message: out})
	return out, nil
}

type cbProvider struct{}

func (cbProvider) Name() string { return "cb" }
func (cbProvider) Kind() string { return "fake" }
func (cbProvider) Model(context.Context, string, Options) (model.ToolCallingChatModel, error) {
	return cbModel{}, nil
}

// A tool calling CompleteJSON inside the agent's graph must not report the
// inner model to the agent's stream handler.
func TestCompleteJSONDoesNotReachCallerCallbacks(t *testing.T) {
	reg, err := NewRegistry(config.LLM{DefaultProvider: "cb"})
	if err != nil {
		t.Fatal(err)
	}
	reg.Register("cb", cbProvider{})
	var seen int
	h := callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, _ *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
			seen++
			return ctx
		}).
		OnEndFn(func(ctx context.Context, _ *callbacks.RunInfo, _ callbacks.CallbackOutput) context.Context {
			seen++
			return ctx
		}).
		Build()
	ctx := callbacks.InitCallbacks(context.Background(), &callbacks.RunInfo{Name: "agent"}, h)

	var out struct{ OK bool }
	c := &JSONCompleter{Reg: reg, Provider: "cb"}
	if err := c.CompleteJSON(ctx, "sys", "user", &out); err != nil || !out.OK {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if seen != 0 {
		t.Fatalf("the caller's handler saw %d inner model callbacks", seen)
	}
	// Control: the same model called with ctx directly does reach it.
	cbModel{}.Generate(ctx, nil)
	if seen == 0 {
		t.Fatal("test model does not report callbacks")
	}
}
