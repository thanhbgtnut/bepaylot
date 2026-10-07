package usage_test

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/usage"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/testkit"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Each call is priced from its tokens; once a user's month reaches the
// limit, chat calls are refused before the provider is called, parsing is
// not (§8.5).
func TestUsageLimit(t *testing.T) {
	h := testkit.New(t)
	svc := usage.New(h.Store, config.Usage{Currency: "VND", Timezone: "Asia/Ho_Chi_Minh",
		Prices: map[string]config.ModelPrice{"m": {Input: 1000, Output: 4000}}}, slog.Default())
	uid := h.Owner.ID
	chat := llm.Caller{UserID: &uid, Kind: types.UsageChat}

	if c := svc.Cost("m", 1_000_000, 500_000); c != 3000 {
		t.Fatalf("cost = %v", c)
	}
	limit := 5000.0
	if _, err := h.Store.Users.Update(h.Ctx, uid, postgres.UserUpdate{MonthlyLimit: &limit}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Allow(h.Ctx, chat); err != nil {
		t.Fatalf("under the limit: %v", err)
	}
	svc.Record(h.Ctx, chat, "m", 1_000_000, 500_000) // 3000
	svc.Record(h.Ctx, llm.Caller{UserID: &uid, Kind: types.UsageSheet}, "m", 1_000_000, 500_000)
	if err := svc.Allow(h.Ctx, chat); !errors.Is(err, llm.ErrBudgetExceeded) {
		t.Fatalf("over the limit: %v", err)
	}
	if err := svc.Allow(h.Ctx, llm.Caller{UserID: &uid, Kind: types.UsageParse}); err != nil {
		t.Fatalf("parsing must not be refused: %v", err)
	}
	sum, err := svc.Summary(h.Ctx, &uid)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Month.Spent != 6000 || sum.Month.Limit != 5000 || sum.ByKind[types.UsageChat] != 3000 || sum.ByKind[types.UsageSheet] != 3000 {
		t.Fatalf("summary = %+v", sum)
	}
	// Removing the limit lets the user call again.
	clear := -1.0
	if _, err := h.Store.Users.Update(h.Ctx, uid, postgres.UserUpdate{MonthlyLimit: &clear}); err != nil {
		t.Fatal(err)
	}
	svc.Forget(uid)
	if err := svc.Allow(h.Ctx, chat); err != nil {
		t.Fatalf("no limit: %v", err)
	}
}
