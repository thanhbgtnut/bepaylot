package wiki

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Lint checks the health of a case wiki (§6.9). It fixes the bookkeeping
// itself — links, the index, stale marks — and only reports content issues
// (contradictions, gaps) for a person to handle.
func (s *Service) Lint(ctx context.Context, c types.Case) error {
	pages, err := s.st.Wiki.Pages(ctx, c.ID)
	if err != nil {
		return err
	}
	docs, err := s.st.Documents.ByCase(ctx, c.ID)
	if err != nil {
		return err
	}
	ids := make([]uuid.UUID, len(pages))
	for i, p := range pages {
		ids[i] = p.ID
	}
	fns, err := s.st.Wiki.Footnotes(ctx, ids)
	if err != nil {
		return err
	}
	links, err := s.st.Wiki.Links(ctx, c.ID, nil, "", "")
	if err != nil {
		return err
	}
	docByID := map[uuid.UUID]types.Document{}
	for _, d := range docs {
		docByID[d.ID] = d
	}
	schema := s.schemaFor(ctx, c)
	var seen []string
	found := map[string]int{}
	fixed := map[string]int{}
	issue := func(kind string, pageIDs []uuid.UUID, detail map[string]any, fp string) {
		seen = append(seen, fp)
		if ok, _ := s.st.Wiki.AddIssue(ctx, types.WikiLintIssue{CaseID: c.ID, Kind: kind, PageIDs: pageIDs, Detail: detail}, fp); ok {
			found[kind]++
		}
	}

	// stale: footnotes whose quote no longer matches the current lines.
	lineCache := map[uuid.UUID]lineTexts{}
	var refresh []uuid.UUID
	for _, p := range pages {
		var stale, valid []int
		fresh := false // a footnote turned stale in this run
		for _, f := range fns[p.ID] {
			d, ok := docByID[f.DocumentID]
			if !ok || f.Gen != d.Gen {
				stale = append(stale, f.N)
				fresh = fresh || f.Status != types.FootnoteStale
				continue
			}
			lt, ok := lineCache[d.ID]
			if !ok {
				pp, err := s.docs.LoadPages(ctx, d.ID, d.Gen, 0, 0)
				if err != nil {
					return err
				}
				lt = linesOf(pp)
				lineCache[d.ID] = lt
			}
			if _, _, ok := matchQuote(lt[f.PageNo], rangeInts(f.LineFrom, f.LineTo), f.Quote, s.cfg.Search.QuoteMinSimilarity); ok {
				if f.Status == types.FootnoteStale {
					valid = append(valid, f.N)
				}
			} else {
				stale = append(stale, f.N)
				fresh = fresh || f.Status != types.FootnoteStale
			}
		}
		if len(valid) > 0 {
			_ = s.st.Wiki.SetFootnoteStatus(ctx, c.ID, p.ID, valid, types.FootnoteValid)
		}
		if len(stale) > 0 {
			if err := s.st.Wiki.SetFootnoteStatus(ctx, c.ID, p.ID, stale, types.FootnoteStale); err != nil {
				return err
			}
			issue(types.LintStale, []uuid.UUID{p.ID}, map[string]any{"slug": p.Slug, "footnotes": stale}, fmt.Sprintf("stale:%s", p.ID))
			// Only newly stale footnotes queue a rewrite: a page already
			// rewritten (or protected by a hand edit) must not loop.
			if fresh {
				refresh = append(refresh, p.ID)
			}
		}
	}

	// contradiction: attributes where two sources disagree.
	for _, p := range pages {
		for name, a := range p.Attributes {
			if !a.Conflict {
				continue
			}
			var values []map[string]any
			for _, h := range a.History {
				values = append(values, map[string]any{"value": h.Value, "footnotes": h.Footnotes, "citation_id": h.CitationID})
			}
			issue(types.LintContradiction, []uuid.UUID{p.ID}, map[string]any{"slug": p.Slug, "attribute": name, "values": values},
				fmt.Sprintf("contradiction:%s:%s", p.ID, name))
		}
	}

	// missing_link (fixed): an entity named in another page without a link.
	linked := map[[2]string]bool{}
	inbound := map[string]int{}
	for _, l := range links {
		linked[[2]string{l.From, l.To}] = true
		inbound[l.To]++
	}
	byID := map[uuid.UUID]types.WikiPage{}
	for _, p := range pages {
		byID[p.ID] = p
	}
	for _, e := range pages {
		if e.Kind != types.WikiKindEntity {
			continue
		}
		names := append([]string{e.Title}, e.Aliases...)
		for _, p := range pages {
			if p.ID == e.ID || linked[[2]string{p.Slug, e.Slug}] || p.Kind == types.WikiKindNote {
				continue
			}
			text := textutil.Normalize(p.Content)
			for _, n := range names {
				if k := textutil.Normalize(n); len([]rune(k)) >= 4 && strings.Contains(text, k) {
					if err := s.st.Wiki.AddLink(ctx, c.ID, p.ID, e.ID, ""); err != nil {
						return err
					}
					linked[[2]string{p.Slug, e.Slug}] = true
					inbound[e.Slug]++
					fixed[types.LintMissingLink]++
					break
				}
			}
		}
	}

	// orphan: entity/topic/note pages nothing links to. A page citing a file
	// gets a link from that file's source page; the rest is reported.
	for _, p := range pages {
		if inbound[p.Slug] > 0 || (p.Kind != types.WikiKindEntity && p.Kind != types.WikiKindTopic && p.Kind != types.WikiKindNote) {
			continue
		}
		done := false
		for _, f := range fns[p.ID] {
			if src, err := s.st.Wiki.SourcePage(ctx, c.ID, f.DocumentID); err == nil {
				if err := s.st.Wiki.AddLink(ctx, c.ID, src.ID, p.ID, ""); err != nil {
					return err
				}
				fixed[types.LintOrphan]++
				done = true
				break
			}
		}
		if !done {
			issue(types.LintOrphan, []uuid.UUID{p.ID}, map[string]any{"slug": p.Slug}, "orphan:"+p.ID.String())
		}
	}

	// gap: files without a source page, missing required attributes,
	// partial ingests.
	hasSource := map[uuid.UUID]bool{}
	for _, p := range pages {
		if p.Kind == types.WikiKindSource && p.DocumentID != nil {
			hasSource[*p.DocumentID] = true
		}
	}
	if s.cases.WikiEnabled(c) {
		for _, d := range docs {
			if !types.Searchable(d.Status) || d.WikiStatus == types.StageSkipped {
				continue
			}
			switch {
			case d.WikiStatus == types.StagePartial:
				issue(types.LintGap, nil, map[string]any{"reason": "ingest partial", "document_id": d.ID, "file_name": d.FileName},
					fmt.Sprintf("gap:partial:%s:%d", d.ID, d.Gen))
			case !hasSource[d.ID] && d.WikiStatus != types.StagePending && d.WikiStatus != types.StageProcessing:
				issue(types.LintGap, nil, map[string]any{"reason": "file has no source page", "document_id": d.ID, "file_name": d.FileName,
					"wiki_status": d.WikiStatus}, fmt.Sprintf("gap:source:%s", d.ID))
				// A file that was ingested but lost its page is queued again;
				// a failed ingest is only reported (retrying would loop).
				if d.WikiStatus == types.StageDone {
					if err := s.queueOp(ctx, c.ID, types.WikiOpIngestDoc, types.WikiOpPayload{DocumentID: d.ID, Gen: d.Gen, FileName: d.FileName}); err != nil {
						return err
					}
				}
			}
		}
	}
	for _, p := range pages {
		if p.Kind != types.WikiKindEntity {
			continue
		}
		def := schema.EntityType(p.EntityType)
		if def == nil {
			continue
		}
		for _, a := range def.Attributes {
			if _, ok := p.Attributes[a.Name]; a.Required && !ok {
				issue(types.LintGap, []uuid.UUID{p.ID}, map[string]any{"reason": "required attribute missing", "slug": p.Slug, "attribute": a.Name},
					fmt.Sprintf("gap:attr:%s:%s", p.ID, a.Name))
			}
		}
	}

	if err := s.st.Wiki.ResolveMissing(ctx, c.ID, []string{types.LintStale, types.LintContradiction, types.LintOrphan, types.LintGap}, seen); err != nil {
		return err
	}

	// index_drift (fixed): the stored index lags the wiki version.
	idx, err := s.st.Wiki.LatestIndex(ctx, c.ID)
	cur, cerr := s.st.Cases.Get(ctx, c.ID)
	if cerr == nil && cur.WikiVersion > 0 && (err != nil || idx.Version != cur.WikiVersion || !s.fresh(ctx, c.ID, idx) ||
		fixed[types.LintMissingLink]+fixed[types.LintOrphan] > 0) {
		if err := s.st.Wiki.InTx(ctx, func(tx *postgres.WikiRepo) error {
			v, err := tx.BumpVersion(ctx, c.ID)
			if err != nil {
				return err
			}
			cur.WikiVersion = v
			return s.storeIndex(ctx, tx, cur)
		}); err != nil {
			return err
		}
		fixed[types.LintIndexDrift]++
	}

	summary := fmt.Sprintf("phát hiện %d vấn đề mới (%s); tự sửa %s", sum(found), kv(found), kv(fixed))
	if _, err := s.st.Wiki.AppendLog(ctx, types.WikiLogEntry{CaseID: c.ID, Op: types.WikiOpLint, Ref: "kiểm tra", Summary: summary}); err != nil {
		return err
	}
	if len(refresh) > 0 {
		return s.queueOp(ctx, c.ID, types.WikiOpRefresh, types.WikiOpPayload{PageIDs: refresh})
	}
	return nil
}

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func kv(m map[string]int) string {
	if len(m) == 0 {
		return "không có"
	}
	var parts []string
	for _, k := range []string{types.LintStale, types.LintContradiction, types.LintOrphan, types.LintMissingLink, types.LintGap, types.LintIndexDrift} {
		if m[k] > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
		}
	}
	return strings.Join(parts, ", ")
}
