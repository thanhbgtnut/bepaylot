package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepaylot/internal/types"
)

// ChecksRepo persists the cross-check runs of a case and the contracts users
// chose for documents (§6.9.9).
type ChecksRepo struct{ pool *pgxpool.Pool }

const checkCols = `id, case_id, sheet_id, ruleset_id, status, error, result, created_by, created_at, updated_at`

func scanCheck(row pgx.Row) (types.CheckRun, error) {
	var c types.CheckRun
	var result []byte
	err := row.Scan(&c.ID, &c.CaseID, &c.SheetID, &c.RulesetID, &c.Status, &c.Error, &result, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	if len(result) > 0 {
		c.Result = json.RawMessage(result)
	}
	return c, nil
}

// Create inserts a pending run.
func (r *ChecksRepo) Create(ctx context.Context, c types.CheckRun) (types.CheckRun, error) {
	return scanCheck(r.pool.QueryRow(ctx, `INSERT INTO case_checks (case_id, sheet_id, ruleset_id, created_by) VALUES ($1, $2, $3, $4) RETURNING `+checkCols,
		c.CaseID, c.SheetID, c.RulesetID, c.CreatedBy))
}

// Get returns a run.
func (r *ChecksRepo) Get(ctx context.Context, id uuid.UUID) (types.CheckRun, error) {
	return scanCheck(r.pool.QueryRow(ctx, `SELECT `+checkCols+` FROM case_checks WHERE id = $1`, id))
}

// BySheet returns the runs that read a sheet.
func (r *ChecksRepo) BySheet(ctx context.Context, sheetID uuid.UUID) ([]types.CheckRun, error) {
	return r.list(ctx, `SELECT `+checkCols+` FROM case_checks WHERE sheet_id = $1`, sheetID)
}

// ListByCase returns the runs of a case, newest first.
func (r *ChecksRepo) ListByCase(ctx context.Context, caseID uuid.UUID) ([]types.CheckRun, error) {
	return r.list(ctx, `SELECT `+checkCols+` FROM case_checks WHERE case_id = $1 ORDER BY created_at DESC`, caseID)
}

func (r *ChecksRepo) list(ctx context.Context, q string, arg any) ([]types.CheckRun, error) {
	rows, err := r.pool.Query(ctx, q, arg)
	if err != nil {
		return nil, fmt.Errorf("case_checks: %w", err)
	}
	defer rows.Close()
	out := []types.CheckRun{}
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetResult records the status and, when result is not nil, the result.
func (r *ChecksRepo) SetResult(ctx context.Context, id uuid.UUID, status string, result any, errMsg string) error {
	var raw any
	if result != nil {
		b, err := cleanJSON(result)
		if err != nil {
			return err
		}
		raw = b
	}
	_, err := r.pool.Exec(ctx, `UPDATE case_checks SET status = $2, result = COALESCE($3, result), error = $4, updated_at = now() WHERE id = $1`,
		id, status, raw, cleanText(errMsg))
	return err
}

// Delete removes a run.
func (r *ChecksRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM case_checks WHERE id = $1`, id)
	return err
}

// Links returns the contracts users chose for documents of a case.
func (r *ChecksRepo) Links(ctx context.Context, caseID uuid.UUID) (map[uuid.UUID]*uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `SELECT segment_id, anchor_segment_id FROM case_check_links WHERE case_id = $1`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]*uuid.UUID{}
	for rows.Next() {
		var seg uuid.UUID
		var anchor *uuid.UUID
		if err := rows.Scan(&seg, &anchor); err != nil {
			return nil, err
		}
		out[seg] = anchor
	}
	return out, rows.Err()
}

// SetLink records (or, with clear, removes) the user's choice for a document.
func (r *ChecksRepo) SetLink(ctx context.Context, caseID, seg uuid.UUID, anchor *uuid.UUID, clear bool, user uuid.UUID) error {
	if clear {
		_, err := r.pool.Exec(ctx, `DELETE FROM case_check_links WHERE case_id = $1 AND segment_id = $2`, caseID, seg)
		return err
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO case_check_links (case_id, segment_id, anchor_segment_id, created_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (case_id, segment_id) DO UPDATE SET anchor_segment_id = EXCLUDED.anchor_segment_id, created_by = EXCLUDED.created_by, created_at = now()`,
		caseID, seg, anchor, user)
	return err
}

// RulesetsRepo persists the rule sets of the cross-checks, shared by every
// knowledge base of their owner (§6.9.9, U105).
type RulesetsRepo struct{ pool *pgxpool.Pool }

const rulesetCols = `id, name, description, kb_ids, anchor, links, rules, created_by, created_at, updated_at`

func scanRuleset(row pgx.Row) (types.CheckRuleset, error) {
	var r types.CheckRuleset
	var links, rules []byte
	err := row.Scan(&r.ID, &r.Name, &r.Description, &r.KBIDs, &r.Anchor, &links, &rules, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	_ = json.Unmarshal(links, &r.Links)
	_ = json.Unmarshal(rules, &r.Rules)
	if r.KBIDs == nil {
		r.KBIDs = []uuid.UUID{}
	}
	if r.Links == nil {
		r.Links = []types.CheckLink{}
	}
	if r.Rules == nil {
		r.Rules = []types.CheckRule{}
	}
	return r, nil
}

// List returns the rule sets of an owner, by name; kb limits it to the sets
// one knowledge base uses.
func (r *RulesetsRepo) List(ctx context.Context, owner uuid.UUID, kb *uuid.UUID) ([]types.CheckRuleset, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+rulesetCols+` FROM check_rulesets
		WHERE created_by = $1 AND ($2::uuid IS NULL OR $2 = ANY(kb_ids)) ORDER BY name, created_at`, owner, kb)
	if err != nil {
		return nil, fmt.Errorf("check_rulesets: %w", err)
	}
	defer rows.Close()
	out := []types.CheckRuleset{}
	for rows.Next() {
		rs, err := scanRuleset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rs)
	}
	return out, rows.Err()
}

// Get returns one rule set of an owner.
func (r *RulesetsRepo) Get(ctx context.Context, owner, id uuid.UUID) (types.CheckRuleset, error) {
	return scanRuleset(r.pool.QueryRow(ctx, `SELECT `+rulesetCols+` FROM check_rulesets WHERE id = $1 AND created_by = $2`, id, owner))
}

// Create inserts a rule set.
func (r *RulesetsRepo) Create(ctx context.Context, rs types.CheckRuleset) (types.CheckRuleset, error) {
	links, rules, err := rulesetJSON(rs)
	if err != nil {
		return rs, err
	}
	return scanRuleset(r.pool.QueryRow(ctx, `INSERT INTO check_rulesets (name, description, kb_ids, anchor, links, rules, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+rulesetCols,
		cleanText(rs.Name), cleanText(rs.Description), kbIDs(rs.KBIDs), rs.Anchor, links, rules, rs.CreatedBy))
}

// Update replaces the content of a rule set.
func (r *RulesetsRepo) Update(ctx context.Context, rs types.CheckRuleset) (types.CheckRuleset, error) {
	links, rules, err := rulesetJSON(rs)
	if err != nil {
		return rs, err
	}
	return scanRuleset(r.pool.QueryRow(ctx, `UPDATE check_rulesets SET name = $2, description = $3, kb_ids = $4, anchor = $5,
		links = $6, rules = $7, updated_at = now() WHERE id = $1 RETURNING `+rulesetCols,
		rs.ID, cleanText(rs.Name), cleanText(rs.Description), kbIDs(rs.KBIDs), rs.Anchor, links, rules))
}

// Delete removes a rule set; runs keep their result.
func (r *RulesetsRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM check_rulesets WHERE id = $1`, id)
	return err
}

func rulesetJSON(rs types.CheckRuleset) ([]byte, []byte, error) {
	if rs.Links == nil {
		rs.Links = []types.CheckLink{}
	}
	if rs.Rules == nil {
		rs.Rules = []types.CheckRule{}
	}
	links, err := cleanJSON(rs.Links)
	if err != nil {
		return nil, nil, err
	}
	rules, err := cleanJSON(rs.Rules)
	return links, rules, err
}

func kbIDs(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}
