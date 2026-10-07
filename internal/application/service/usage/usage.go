// Package usage meters model calls (§8.5): it prices each call from its
// tokens, records it for the user it was made for, refuses calls once a user
// has spent the monthly limit, and sums the spend for the settings page.
package usage

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/types"
)

// cacheTTL is how long a user's spend and limit are trusted before the
// budget check reads them again.
const cacheTTL = 15 * time.Second

// Service implements llm.Meter.
type Service struct {
	st  *postgres.Store
	cfg config.Usage
	loc *time.Location
	log *slog.Logger

	mu      sync.Mutex
	budget  map[uuid.UUID]budgetEntry
	unknown map[string]bool // models without a price, logged once
}

type budgetEntry struct {
	limit, spent float64
	at           time.Time
}

// New builds the service.
func New(st *postgres.Store, cfg config.Usage, log *slog.Logger) *Service {
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		loc = time.UTC
	}
	return &Service{st: st, cfg: cfg, loc: loc, log: log, budget: map[uuid.UUID]budgetEntry{}, unknown: map[string]bool{}}
}

// Currency of the amounts.
func (s *Service) Currency() string { return s.cfg.Currency }

// MonthStart is the start of the current month in the usage timezone.
func (s *Service) MonthStart(now time.Time) time.Time {
	t := now.In(s.loc)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, s.loc)
}

func (s *Service) dayStart(now time.Time) time.Time {
	t := now.In(s.loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, s.loc)
}

// Allow refuses a call when the caller has reached the monthly limit.
// Parsing is never refused: an uploaded file must finish (§8.5).
func (s *Service) Allow(ctx context.Context, c llm.Caller) error {
	if c.UserID == nil || c.Kind == types.UsageParse {
		return nil
	}
	return s.Check(ctx, *c.UserID)
}

// Check is Allow for a request that is about to start a turn.
func (s *Service) Check(ctx context.Context, user uuid.UUID) error {
	e, err := s.entry(ctx, user)
	if err != nil {
		s.log.Warn("usage: budget check failed", "user", user, "err", err)
		return nil // never block on a metering failure
	}
	if e.limit > 0 && e.spent >= e.limit {
		return llm.ErrBudgetExceeded
	}
	return nil
}

func (s *Service) entry(ctx context.Context, user uuid.UUID) (budgetEntry, error) {
	s.mu.Lock()
	e, ok := s.budget[user]
	s.mu.Unlock()
	if ok && time.Since(e.at) < cacheTTL {
		return e, nil
	}
	u, err := s.st.Users.Get(ctx, user)
	if err != nil {
		return e, err
	}
	e = budgetEntry{at: time.Now()}
	if u.MonthlyLimit != nil {
		e.limit = *u.MonthlyLimit
		if e.spent, err = s.st.Usage.Spent(ctx, &user, s.MonthStart(time.Now())); err != nil {
			return e, err
		}
	}
	s.mu.Lock()
	s.budget[user] = e
	s.mu.Unlock()
	return e, nil
}

// Forget drops a cached budget (after an admin changes the limit).
func (s *Service) Forget(user uuid.UUID) {
	s.mu.Lock()
	delete(s.budget, user)
	s.mu.Unlock()
}

// Cost prices tokens of a model.
func (s *Service) Cost(model string, in, out int) float64 {
	p, ok := s.cfg.Prices[model]
	if !ok {
		s.mu.Lock()
		first := !s.unknown[model]
		s.unknown[model] = true
		s.mu.Unlock()
		if first {
			s.log.Warn("usage: model has no price in usage.prices, its calls cost 0", "model", model)
		}
		return 0
	}
	return float64(in)/1e6*p.Input + float64(out)/1e6*p.Output
}

// Record stores the cost of one call.
func (s *Service) Record(ctx context.Context, c llm.Caller, model string, in, out int) {
	cost := s.Cost(model, in, out)
	ev := types.UsageEvent{UserID: c.UserID, CaseID: c.CaseID, Kind: c.Kind, Model: model, TokensIn: in, TokensOut: out, Cost: cost}
	if err := s.st.Usage.Insert(ctx, ev); err != nil {
		s.log.Warn("usage: record failed", "err", err)
		return
	}
	if c.UserID != nil {
		s.mu.Lock()
		if e, ok := s.budget[*c.UserID]; ok {
			e.spent += cost
			s.budget[*c.UserID] = e
		}
		s.mu.Unlock()
	}
}

// Summary is the usage page of one user, or of everyone (nil).
func (s *Service) Summary(ctx context.Context, user *uuid.UUID) (types.UsageSummary, error) {
	now := time.Now()
	month, day := s.MonthStart(now), s.dayStart(now)
	out := types.UsageSummary{Currency: s.cfg.Currency, UpdatedAt: now}
	var err error
	if out.Month.Spent, err = s.st.Usage.Spent(ctx, user, month); err != nil {
		return out, err
	}
	if out.Today.Spent, err = s.st.Usage.Spent(ctx, user, day); err != nil {
		return out, err
	}
	if out.ByKind, err = s.st.Usage.ByKind(ctx, user, month); err != nil {
		return out, err
	}
	next := month.AddDate(0, 1, 0)
	out.Month.ResetsAt = &next
	tomorrow := day.AddDate(0, 0, 1)
	out.Today.ResetsAt = &tomorrow
	if user != nil {
		if u, err := s.st.Users.Get(ctx, *user); err == nil && u.MonthlyLimit != nil {
			out.Month.Limit = *u.MonthlyLimit
		}
	}
	return out, nil
}

// ParseCaller attributes the model calls of a document's pipeline task to
// whoever uploaded it (§8.5).
func (s *Service) ParseCaller(ctx context.Context, doc uuid.UUID) llm.Caller {
	user, caseID := s.st.Usage.DocumentPayer(ctx, doc)
	return llm.Caller{UserID: user, CaseID: caseID, Kind: types.UsageParse}
}

var _ llm.Meter = (*Service)(nil)
