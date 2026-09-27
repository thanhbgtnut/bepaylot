package wiki

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/types"
)

// retract removes a file (generations ≤ gen) from the case wiki (§6.8): its
// source page goes, entity pages left without footnotes go, and the others
// lose what rested on the file and are re-rendered by code. No LLM call.
func (s *Service) retract(ctx context.Context, c types.Case, doc uuid.UUID, gen int, fileName string) error {
	all, err := s.st.Wiki.CaseFootnotes(ctx, c.ID, &doc, 0)
	if err != nil {
		return err
	}
	removed := map[uuid.UUID]map[int]bool{}
	for _, f := range all {
		if gen > 0 && f.Gen > gen {
			continue // a newer generation already ingested
		}
		if removed[f.PageID] == nil {
			removed[f.PageID] = map[int]bool{}
		}
		removed[f.PageID][f.N] = true
	}
	src, srcErr := s.st.Wiki.SourcePage(ctx, c.ID, doc)
	if srcErr != nil && !errors.Is(srcErr, postgres.ErrNotFound) {
		return srcErr
	}
	if len(removed) == 0 && srcErr != nil {
		return nil // nothing of this file is in the wiki
	}
	schema := s.schemaFor(ctx, c)
	var slugs []string
	deleted, stripped := 0, 0
	return s.st.Wiki.InTx(ctx, func(tx *postgres.WikiRepo) error {
		logID, err := tx.AppendLog(ctx, types.WikiLogEntry{CaseID: c.ID, Op: types.WikiOpRetract, Ref: firstNonEmpty(fileName, doc.String()), DocumentID: &doc})
		if err != nil {
			return err
		}
		if srcErr == nil && src.LastEditSource != types.EditUser {
			if err := tx.DeletePage(ctx, c.ID, src.ID); err != nil {
				return err
			}
			slugs = append(slugs, src.Slug)
			deleted++
			delete(removed, src.ID)
		}
		for pageID, ns := range removed {
			p, err := tx.PageByID(ctx, c.ID, pageID)
			if errors.Is(err, postgres.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			slugs = append(slugs, p.Slug)
			gone, err := s.restate(ctx, tx, c, schema, p, ns, &logID)
			if err != nil {
				return err
			}
			if gone {
				deleted++
			} else {
				stripped++
			}
		}
		if err := s.rewriteOverview(ctx, tx, c, schema); err != nil {
			return err
		}
		if err := tx.SetLogResult(ctx, logID, slugs, fmt.Sprintf("xoá %d trang, cập nhật %d trang", deleted, stripped)); err != nil {
			return err
		}
		return s.finishTx(ctx, tx, c)
	})
}

// finishTx bumps the wiki version, stores the index and the coverage.
func (s *Service) finishTx(ctx context.Context, tx *postgres.WikiRepo, c types.Case) error {
	v, err := tx.BumpVersion(ctx, c.ID)
	if err != nil {
		return err
	}
	c.WikiVersion = v
	if err := s.storeIndex(ctx, tx, c); err != nil {
		return err
	}
	covered, err := tx.CoveredDocs(ctx, c.ID)
	if err != nil {
		return err
	}
	return tx.SetCaseWiki(ctx, c.ID, types.WikiBuilding, covered)
}

// restate removes footnotes from a page and saves what is left: an entity
// page is re-rendered from its remaining attributes and links, other pages
// lose the sentences that rested on the footnotes. An entity (or source)
// page left without any footnote is deleted. A hand-edited page gets the
// result as a proposal.
func (s *Service) restate(ctx context.Context, tx *postgres.WikiRepo, c types.Case, schema types.WikiSchema, p types.WikiPage,
	removed map[int]bool, logID *int64) (bool, error) {
	fns, err := tx.Footnotes(ctx, []uuid.UUID{p.ID})
	if err != nil {
		return false, err
	}
	var keep []types.WikiFootnote
	for _, f := range fns[p.ID] {
		if !removed[f.N] {
			keep = append(keep, f)
		}
	}
	if len(keep) == 0 && (p.Kind == types.WikiKindEntity || p.Kind == types.WikiKindTopic || p.Kind == types.WikiKindSource) &&
		p.LastEditSource != types.EditUser {
		return true, tx.DeletePage(ctx, c.ID, p.ID)
	}
	next := stripPage(p, removed)
	var links []linkSpec
	if p.Kind == types.WikiKindEntity {
		out, err := tx.Links(ctx, c.ID, &p.ID, "out", "")
		if err != nil {
			return false, err
		}
		for _, l := range out {
			if l.Relation != "" && (l.FootnoteN == nil || !removed[*l.FootnoteN]) {
				links = append(links, linkSpec{to: l.To, relation: l.Relation, attributes: l.Attributes, footnote: l.FootnoteN})
			}
		}
		pages, err := tx.Pages(ctx, c.ID)
		if err != nil {
			return false, err
		}
		srcOf := sourcesByDoc(pages)
		next.Content, next.Summary = renderEntity(next, schema.EntityType(p.EntityType), keep, links, srcOf)
		links = append(links, sourceLinks(keep, srcOf)...)
	}
	if p.LastEditSource == types.EditUser {
		content := next.Content
		if err := tx.SetProposal(ctx, c.ID, p.ID, &content, next.Attributes); err != nil {
			return false, err
		}
	} else if _, err := tx.SavePage(ctx, next, types.EditSystem, nil, logID); err != nil {
		return false, err
	}
	if err := tx.ReplaceFootnotes(ctx, p.ID, keep); err != nil {
		return false, err
	}
	if p.Kind == types.WikiKindEntity && p.LastEditSource != types.EditUser {
		if err := tx.ReplaceLinks(ctx, c.ID, p.ID, linkRows(ctx, tx, c.ID, nil, links)); err != nil {
			return false, err
		}
	}
	return false, nil
}

// stripPage removes from a page what only rested on the removed footnotes.
func stripPage(p types.WikiPage, removed map[int]bool) types.WikiPage {
	p.Content = dropFootnotes(p.Content, removed)
	attrs := map[string]types.WikiAttribute{}
	for k, a := range p.Attributes {
		if k == roleAttr {
			var hist []types.WikiAttrValue
			for _, h := range a.History {
				var hs []int
				for _, n := range h.Footnotes {
					if !removed[n] {
						hs = append(hs, n)
					}
				}
				if len(hs) > 0 {
					h.Footnotes = hs
					hist = append(hist, h)
				}
			}
			if len(hist) > 0 {
				attrs[k] = rolesValue(hist)
			}
			continue
		}
		var keepFns []int
		for _, n := range a.Footnotes {
			if !removed[n] {
				keepFns = append(keepFns, n)
			}
		}
		var hist []types.WikiAttrValue
		for _, h := range a.History {
			var hs []int
			for _, n := range h.Footnotes {
				if !removed[n] {
					hs = append(hs, n)
				}
			}
			if len(hs) > 0 {
				h.Footnotes = hs
				hist = append(hist, h)
			}
		}
		switch {
		case len(keepFns) > 0:
			a.Footnotes = keepFns
		case len(hist) > 0:
			a.Value, a.Footnotes = hist[0].Value, hist[0].Footnotes
		default:
			continue
		}
		if len(hist) <= 1 {
			a.History, a.Conflict = nil, false
		} else {
			a.History = hist
		}
		attrs[k] = a
	}
	p.Attributes = attrs
	return p
}

// rewriteOverview re-renders the overview after pages changed.
func (s *Service) rewriteOverview(ctx context.Context, tx *postgres.WikiRepo, c types.Case, schema types.WikiSchema) error {
	pages, err := tx.Pages(ctx, c.ID)
	if err != nil {
		return err
	}
	w := overviewWritten(c, schema, pages)
	if w.proposal {
		content := w.page.Content
		return tx.SetProposal(ctx, c.ID, w.page.ID, &content, nil)
	}
	saved, err := tx.SavePage(ctx, w.page, types.EditSystem, nil, nil)
	if err != nil {
		return err
	}
	return tx.ReplaceLinks(ctx, c.ID, saved.ID, linkRows(ctx, tx, c.ID, nil, w.links))
}

// recheck re-verifies the footnotes of a file after a page reparse of the
// same generation (§6.8): footnotes that no longer match turn stale and
// their pages are queued for a rewrite.
func (s *Service) recheck(ctx context.Context, c types.Case, d types.Document, fns []postgres.FootnoteRef) error {
	pages, err := s.docs.LoadPages(ctx, d.ID, d.Gen, 0, 0)
	if err != nil {
		return err
	}
	lines := linesOf(pages)
	staleBy, validBy := map[uuid.UUID][]int{}, map[uuid.UUID][]int{}
	for _, f := range fns {
		from, to, ok := matchQuote(lines[f.PageNo], rangeInts(f.LineFrom, f.LineTo), f.Quote, s.cfg.Search.QuoteMinSimilarity)
		if ok && from == f.LineFrom && to == f.LineTo {
			validBy[f.PageID] = append(validBy[f.PageID], f.N)
		} else {
			staleBy[f.PageID] = append(staleBy[f.PageID], f.N)
		}
	}
	for p, ns := range validBy {
		if err := s.st.Wiki.SetFootnoteStatus(ctx, c.ID, p, ns, types.FootnoteValid); err != nil {
			return err
		}
	}
	var refresh []uuid.UUID
	n := 0
	for p, ns := range staleBy {
		if err := s.st.Wiki.SetFootnoteStatus(ctx, c.ID, p, ns, types.FootnoteStale); err != nil {
			return err
		}
		refresh = append(refresh, p)
		n += len(ns)
	}
	if _, err := s.st.Wiki.AppendLog(ctx, types.WikiLogEntry{CaseID: c.ID, Op: types.WikiOpIngest, Ref: d.FileName, DocumentID: &d.ID,
		Summary: fmt.Sprintf("kiểm lại %d chú thích sau khi parse lại trang, %d không còn khớp", len(fns), n)}); err != nil {
		return err
	}
	if len(refresh) > 0 {
		if err := s.queueOp(ctx, c.ID, types.WikiOpRefresh, types.WikiOpPayload{PageIDs: refresh}); err != nil {
			return err
		}
	}
	return s.docs.SetWikiStatus(ctx, d.ID, d.Gen, types.StageDone, nil)
}

func rangeInts(a, b int) []int {
	var out []int
	for n := a; n <= b; n++ {
		out = append(out, n)
	}
	return out
}

// refreshPages drops the stale footnotes of pages (queued by recheck and
// lint) and re-renders them by code: a statement whose source line changed
// leaves the page until the file is ingested again. No LLM call.
func (s *Service) refreshPages(ctx context.Context, c types.Case, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	fns, err := s.st.Wiki.Footnotes(ctx, ids)
	if err != nil {
		return err
	}
	schema := s.schemaFor(ctx, c)
	return s.st.Wiki.InTx(ctx, func(tx *postgres.WikiRepo) error {
		pages, err := tx.PagesByID(ctx, c.ID, ids)
		if err != nil {
			return err
		}
		var slugs []string
		for _, p := range pages {
			slugs = append(slugs, p.Slug)
		}
		logID, err := tx.AppendLog(ctx, types.WikiLogEntry{CaseID: c.ID, Op: types.WikiOpIngest, Ref: "làm mới trang",
			Pages: slugs, Summary: fmt.Sprintf("bỏ chú thích lỗi thời khỏi %d trang", len(pages))})
		if err != nil {
			return err
		}
		for _, p := range pages {
			stale := map[int]bool{}
			for _, f := range fns[p.ID] {
				if f.Status == types.FootnoteStale {
					stale[f.N] = true
				}
			}
			if len(stale) == 0 {
				continue
			}
			if _, err := s.restate(ctx, tx, c, schema, p, stale, &logID); err != nil {
				return err
			}
		}
		if err := s.rewriteOverview(ctx, tx, c, schema); err != nil {
			return err
		}
		return s.finishTx(ctx, tx, c)
	})
}
