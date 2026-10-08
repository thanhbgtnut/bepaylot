package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepaylot/internal/types"
)

// SegmentsRepo persists Classification segments and the document bundles of
// a case (§6.9.4, §6.9.7).
type SegmentsRepo struct{ pool *pgxpool.Pool }

const segCols = `s.id, s.case_id, s.document_id, s.page_start, s.page_end, s.label, s.proposed_label, s.confidence,
	s.source, s.needs_review, s.mode, s.bundle_id, COALESCE(b.code, '')`

func scanSegments(rows pgx.Rows) ([]types.Segment, error) {
	defer rows.Close()
	out := []types.Segment{}
	for rows.Next() {
		var g types.Segment
		var conf float32
		if err := rows.Scan(&g.ID, &g.CaseID, &g.DocumentID, &g.PageStart, &g.PageEnd, &g.Label, &g.ProposedLabel, &conf,
			&g.Source, &g.NeedsReview, &g.Mode, &g.BundleID, &g.BundleCode); err != nil {
			return nil, err
		}
		g.Confidence = float64(conf)
		out = append(out, g)
	}
	return out, rows.Err()
}

// ByCase returns the active segments of the live files of a case, in file
// order of the case tree then page.
func (r *SegmentsRepo) ByCase(ctx context.Context, caseID uuid.UUID) ([]types.Segment, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+segCols+` FROM document_segments s
		JOIN documents d ON d.id = s.document_id AND d.case_id = $1 AND d.deleted_at IS NULL AND s.gen = d.gen
		LEFT JOIN case_bundles b ON b.id = s.bundle_id
		LEFT JOIN case_tree t ON t.document_id = d.id
		WHERE s.status = 'active'
		ORDER BY t.seq NULLS LAST, d.created_at, s.page_start`, caseID)
	if err != nil {
		return nil, fmt.Errorf("segments.ByCase: %w", err)
	}
	return scanSegments(rows)
}

// Get returns one active segment.
func (r *SegmentsRepo) Get(ctx context.Context, id uuid.UUID) (types.Segment, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+segCols+` FROM document_segments s
		LEFT JOIN case_bundles b ON b.id = s.bundle_id WHERE s.id = $1 AND s.status = 'active'`, id)
	if err != nil {
		return types.Segment{}, err
	}
	list, err := scanSegments(rows)
	if err != nil {
		return types.Segment{}, err
	}
	if len(list) == 0 {
		return types.Segment{}, ErrNotFound
	}
	return list[0], nil
}

// ByDocument returns the active segments of a document's current gen.
func (r *SegmentsRepo) ByDocument(ctx context.Context, doc uuid.UUID) ([]types.Segment, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+segCols+` FROM document_segments s
		JOIN documents d ON d.id = s.document_id AND s.gen = d.gen
		LEFT JOIN case_bundles b ON b.id = s.bundle_id
		WHERE s.document_id = $1 AND s.status = 'active' ORDER BY s.page_start`, doc)
	if err != nil {
		return nil, fmt.Errorf("segments.ByDocument: %w", err)
	}
	return scanSegments(rows)
}

// SetClassifyStatus records the state of document:classify.
func (r *SegmentsRepo) SetClassifyStatus(ctx context.Context, doc uuid.UUID, status string) error {
	_, err := r.pool.Exec(ctx, `UPDATE documents SET classify_status = $2 WHERE id = $1`, doc, status)
	return err
}

// SaveProposals replaces the pipeline segments of a document's gen (§6.9.4).
// Segments of the user are kept; proposals overlapping them are dropped.
func (r *SegmentsRepo) SaveProposals(ctx context.Context, caseID, doc uuid.UUID, gen int, segs []types.Segment, mode, model string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT id FROM documents WHERE id = $1 FOR UPDATE`, doc); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE document_segments SET status = 'stale'
		WHERE document_id = $1 AND source = 'pipeline' AND status = 'active'`, doc); err != nil {
		return fmt.Errorf("segments.SaveProposals stale: %w", err)
	}
	for _, g := range segs {
		if _, err := tx.Exec(ctx, `INSERT INTO document_segments (case_id, document_id, gen, page_start, page_end, label, proposed_label,
			confidence, source, mode, model)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8, 'pipeline', $9, $10
			WHERE NOT EXISTS (SELECT 1 FROM document_segments u WHERE u.document_id = $2 AND u.source = 'user' AND u.status = 'active'
			  AND u.page_start <= $5 AND u.page_end >= $4)`,
			caseID, doc, gen, g.PageStart, g.PageEnd, g.Label, g.ProposedLabel, g.Confidence, mode, model); err != nil {
			return fmt.Errorf("segments.SaveProposals insert: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE documents SET classify_status = 'done' WHERE id = $1`, doc); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Bundles returns the bundles of a case in order.
func (r *SegmentsRepo) Bundles(ctx context.Context, caseID uuid.UUID) ([]types.Bundle, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, seq, code FROM case_bundles WHERE case_id = $1 ORDER BY seq`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []types.Bundle{}
	for rows.Next() {
		var b types.Bundle
		if err := rows.Scan(&b.ID, &b.Seq, &b.Code); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SplitDoc is the review state of a file (§6.9.7).
type SplitDoc struct {
	ID             uuid.UUID
	FileName       string
	PageCount      int
	Status         string
	ClassifyStatus string
	ReviewedAt     *time.Time
	Seq            int // position in the case tree; 0 = not indexed yet
}

// SplitDocs lists the live files of a case with their split state, in case
// tree order.
func (r *SegmentsRepo) SplitDocs(ctx context.Context, caseID uuid.UUID) ([]SplitDoc, error) {
	rows, err := r.pool.Query(ctx, `SELECT d.id, d.file_name, COALESCE(d.page_count, 0), d.status, d.classify_status, d.split_reviewed_at,
		COALESCE(t.seq, 0) FROM documents d LEFT JOIN case_tree t ON t.document_id = d.id
		WHERE d.case_id = $1 AND d.deleted_at IS NULL ORDER BY t.seq NULLS LAST, d.created_at`, caseID)
	if err != nil {
		return nil, fmt.Errorf("segments.SplitDocs: %w", err)
	}
	defer rows.Close()
	var out []SplitDoc
	for rows.Next() {
		var d SplitDoc
		if err := rows.Scan(&d.ID, &d.FileName, &d.PageCount, &d.Status, &d.ClassifyStatus, &d.ReviewedAt, &d.Seq); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SplitSegment is one reviewed document of PUT /cases/{id}/split.
type SplitSegment struct {
	DocumentID uuid.UUID
	PageStart  int
	PageEnd    int
	Label      string
}

// SaveSplit stores a reviewed split of a case (§6.9.7) in one transaction:
// the segments of every document in docs become the user's (a segment with
// the same pages and label keeps its id, the others go stale), the bundles of
// the case are replaced (bundles[i] is B0i+1) and the documents are marked
// reviewed. Reviewed files left out of the request keep their bundle by code.
func (r *SegmentsRepo) SaveSplit(ctx context.Context, caseID, user uuid.UUID, docs []uuid.UUID, bundles [][]SplitSegment) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('split:' || $1::text))`, caseID); err != nil {
		return err
	}
	// Remember the bundle codes of segments outside the request.
	keep := map[uuid.UUID]string{}
	rows, err := tx.Query(ctx, `SELECT s.id, b.code FROM document_segments s JOIN case_bundles b ON b.id = s.bundle_id
		WHERE s.case_id = $1 AND s.status = 'active' AND NOT (s.document_id = ANY($2))`, caseID, docs)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id uuid.UUID
		var code string
		if err := rows.Scan(&id, &code); err != nil {
			rows.Close()
			return err
		}
		keep[id] = code
	}
	rows.Close()
	if _, err := tx.Exec(ctx, `DELETE FROM case_bundles WHERE case_id = $1`, caseID); err != nil {
		return fmt.Errorf("segments.SaveSplit bundles: %w", err)
	}
	ids := map[string]uuid.UUID{}
	bundleID := func(code string) (uuid.UUID, error) {
		if id, ok := ids[code]; ok {
			return id, nil
		}
		var id uuid.UUID
		err := tx.QueryRow(ctx, `INSERT INTO case_bundles (case_id, seq, code, created_by)
			VALUES ($1, (SELECT COALESCE(max(seq), 0) + 1 FROM case_bundles WHERE case_id = $1), $2, $3) RETURNING id`, caseID, code, user).Scan(&id)
		ids[code] = id
		return id, err
	}
	used := map[uuid.UUID]bool{}
	for i, segs := range bundles {
		bid, err := bundleID(BundleCode(i + 1))
		if err != nil {
			return fmt.Errorf("segments.SaveSplit bundle: %w", err)
		}
		for _, g := range segs {
			var id uuid.UUID
			err := tx.QueryRow(ctx, `UPDATE document_segments s SET bundle_id = $5
				FROM documents d WHERE d.id = s.document_id AND s.gen = d.gen AND s.document_id = $1 AND s.source = 'user' AND s.status = 'active'
				  AND s.page_start = $2 AND s.page_end = $3 AND s.label = $4 AND NOT (s.id = ANY($6))
				RETURNING s.id`, g.DocumentID, g.PageStart, g.PageEnd, g.Label, bid, keys(used)).Scan(&id)
			if err == pgx.ErrNoRows {
				err = tx.QueryRow(ctx, `INSERT INTO document_segments (case_id, document_id, gen, page_start, page_end, label, confidence, source, bundle_id, created_by)
					VALUES ($1, $2, (SELECT gen FROM documents WHERE id = $2), $3, $4, $5, 1, 'user', $6, $7) RETURNING id`,
					caseID, g.DocumentID, g.PageStart, g.PageEnd, g.Label, bid, user).Scan(&id)
			}
			if err != nil {
				return fmt.Errorf("segments.SaveSplit segment: %w", err)
			}
			used[id] = true
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE document_segments SET status = 'stale', bundle_id = NULL
		WHERE document_id = ANY($1) AND status = 'active' AND NOT (id = ANY($2))`, docs, keys(used)); err != nil {
		return fmt.Errorf("segments.SaveSplit stale: %w", err)
	}
	for id, code := range keep {
		bid, err := bundleID(code)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE document_segments SET bundle_id = $2 WHERE id = $1`, id, bid); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE documents SET split_reviewed_at = now(), split_reviewed_by = $2 WHERE id = ANY($1)`, docs, user); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteBundles removes the bundles of a deleted case (segments go with
// their documents).
func (r *SegmentsRepo) DeleteBundles(ctx context.Context, caseID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM case_bundles WHERE case_id = $1`, caseID)
	return err
}

// BundleCode is the display code of the n-th bundle: B01, B02…
func BundleCode(n int) string { return fmt.Sprintf("B%02d", n) }

func keys(m map[uuid.UUID]bool) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
