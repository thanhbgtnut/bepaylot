// Package graph is Module 3's knowledge graph (§7): configurable schemas,
// LLM extraction with schema validation and verbatim evidence, entity
// resolution, and graph queries on Postgres (recursive CTEs).
package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// Errors surfaced to the API.
var (
	ErrNotFound   = errors.New("not found")
	ErrBadRequest = errors.New("bad request")
)

// Deps are the service dependencies.
type Deps struct {
	Store    *postgres.Store
	Docs     interfaces.DocumentStore
	Sections interfaces.SectionReader
	Queue    queue.Enqueuer
	LLM      interfaces.Completer // nil disables extraction
	Config   *config.Config
	Log      *slog.Logger
}

// Service implements the graph half of Module 3.
type Service struct {
	st   *postgres.Store
	docs interfaces.DocumentStore
	secs interfaces.SectionReader
	q    queue.Enqueuer
	llm  interfaces.Completer
	cfg  *config.Config
	log  *slog.Logger
}

// New builds the service.
func New(d Deps) *Service {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{st: d.Store, docs: d.Docs, secs: d.Sections, q: d.Queue, llm: d.LLM, cfg: d.Config, log: log.With("module", "graph")}
}

// EnsureSchemas stores the built-in generic schema and every schema file.
func (s *Service) EnsureSchemas(ctx context.Context) error {
	schemas := []types.GraphSchema{GenericSchema()}
	files, err := LoadSchemaFiles(s.cfg.Graph.SchemaDir)
	if err != nil {
		return err
	}
	schemas = append(schemas, files...)
	for _, sc := range schemas {
		if _, err := s.st.Graph.UpsertSchema(ctx, sc); err != nil {
			return fmt.Errorf("schema %s v%d: %w", sc.Name, sc.Version, err)
		}
	}
	return nil
}

// Handlers returns the graph task handlers.
func (s *Service) Handlers() map[string]queue.Handler {
	return map[string]queue.Handler{
		types.TaskGraphExtract: s.handleExtract,
		types.TaskGraphResolve: s.handleResolve,
	}
}

// schemaFor picks the KB's schema: explicit id, then configured name, then
// the global default, then the built-in generic schema.
func (s *Service) schemaFor(ctx context.Context, kb types.KnowledgeBase) (types.GraphSchema, error) {
	if kb.GraphSchemaID != nil {
		if sc, err := s.st.Graph.SchemaByID(ctx, *kb.GraphSchemaID); err == nil {
			return sc, nil
		}
	}
	for _, name := range []string{kb.Config.GraphSchema, s.cfg.Graph.DefaultSchema, "generic"} {
		if name == "" || name == "none" {
			continue
		}
		if sc, err := s.st.Graph.LatestSchema(ctx, name); err == nil {
			return sc, nil
		}
	}
	g := GenericSchema()
	return s.st.Graph.UpsertSchema(ctx, g)
}

func (s *Service) handleExtract(ctx context.Context, raw []byte) error {
	var p types.DocTaskPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
	}
	d, err := s.docs.GetDocument(ctx, p.DocumentID)
	if err != nil || d.Gen != p.Gen || d.Status == types.DocCancelled || d.Status == types.DocDeleting {
		return nil
	}
	if s.llm == nil {
		return s.docs.SetGraphStatus(ctx, d.ID, d.Gen, types.StageSkipped, nil)
	}
	_ = s.docs.SetGraphStatus(ctx, d.ID, d.Gen, types.StageProcessing, nil)
	n, err := s.extractDocument(ctx, d)
	if err != nil {
		if errors.Is(err, queue.ErrSkipRetry) || queue.IsFinalAttempt(ctx) {
			_ = s.docs.SetGraphStatus(context.WithoutCancel(ctx), d.ID, d.Gen, types.StageFailed, err)
		}
		return err
	}
	s.log.Info("graph extracted", "doc", d.ID, "entities", n)
	return s.docs.SetGraphStatus(ctx, d.ID, d.Gen, types.StageDone, nil)
}

type unit struct {
	id               string
	text             string
	pageFrom, pageTo int
	sectionID        *uuid.UUID
}

// extractDocument runs extraction over the document's units in batches and
// stores entities, relations and mentions. It is idempotent per document.
func (s *Service) extractDocument(ctx context.Context, d types.Document) (int, error) {
	kb, err := s.docs.GetKB(ctx, d.KBID)
	if err != nil {
		return 0, err
	}
	schema, err := s.schemaFor(ctx, kb)
	if err != nil {
		return 0, err
	}
	secs, err := s.secs.Sections(ctx, d.ID, d.Gen)
	if err != nil {
		return 0, err
	}
	units := buildUnits(secs, schema.Extraction.Unit)
	pages, err := s.docs.LoadPages(ctx, d.ID, d.Gen, 0, 0)
	if err != nil {
		return 0, err
	}
	lines := map[int][]types.ParsedLine{}
	for _, p := range pages {
		lines[p.PageNo] = p.Lines
	}

	system := ExtractPrompt(schema)
	meta, _ := json.Marshal(d.Metadata)
	batch := max(1, s.cfg.Graph.BatchSize)
	var all Extraction
	unitByID := map[string]unit{}
	for i := 0; i < len(units); i += batch {
		chunk := units[i:min(i+batch, len(units))]
		var sb strings.Builder
		fmt.Fprintf(&sb, "Document: %s (%s)\nMetadata: %s\n\n", d.FileName, d.Title, meta)
		texts := map[string]string{}
		for _, u := range chunk {
			fmt.Fprintf(&sb, "<unit id=%q pages=\"%d-%d\">\n%s\n</unit>\n", u.id, u.pageFrom, u.pageTo, u.text)
			texts[u.id] = u.text
			unitByID[u.id] = u
		}
		var ex Extraction
		if err := s.llm.CompleteJSON(ctx, system, sb.String(), &ex); err != nil {
			return 0, fmt.Errorf("extract batch %d: %w", i/batch, err)
		}
		valid, warns := ValidateExtraction(schema, ex, texts)
		for _, w := range warns {
			s.log.Debug("extraction item dropped", "doc", d.ID, "kind", w.Kind, "item", w.Item, "reason", w.Reason)
		}
		all.Entities = append(all.Entities, valid.Entities...)
		all.Relations = append(all.Relations, valid.Relations...)
	}

	if err := s.st.Graph.DeleteDocumentMentions(ctx, d.ID); err != nil {
		return 0, err
	}
	ids := map[string]uuid.UUID{} // type|normname → entity id
	var touched []uuid.UUID
	for _, e := range all.Entities {
		def := schema.EntityType(e.Type)
		id, err := s.st.Graph.UpsertEntity(ctx, postgres.EntityUpsert{
			KBID: d.KBID, SchemaID: schema.ID, Type: e.Type, Name: e.Name,
			NormKey: NormKey(def, e.Name, e.Attributes, d.Metadata), Attributes: e.Attributes, Source: d.ID.String(),
		})
		if err != nil {
			return 0, err
		}
		ids[e.Type+"|"+NormName(e.Name)] = id
		touched = append(touched, id)
		u := unitByID[e.Unit]
		if err := s.st.Graph.InsertMention(ctx, types.Mention{EntityID: &id, DocumentID: d.ID, SectionID: u.sectionID, Evidence: e.Evidence,
			SourceSpans: evidenceSpans(e.Evidence, u, lines)}, d.Gen); err != nil {
			return 0, err
		}
	}
	for _, r := range all.Relations {
		src, ok1 := ids[r.Source.Type+"|"+NormName(r.Source.Name)]
		dst, ok2 := ids[r.Target.Type+"|"+NormName(r.Target.Name)]
		if !ok1 || !ok2 || src == dst {
			continue
		}
		rid, err := s.st.Graph.UpsertRelation(ctx, d.KBID, r.Type, src, dst, r.Attributes)
		if err != nil {
			return 0, err
		}
		u := unitByID[r.Unit]
		if err := s.st.Graph.InsertMention(ctx, types.Mention{RelationID: &rid, DocumentID: d.ID, SectionID: u.sectionID, Evidence: r.Evidence,
			SourceSpans: evidenceSpans(r.Evidence, u, lines)}, d.Gen); err != nil {
			return 0, err
		}
	}
	if err := s.st.Graph.PruneOrphans(ctx); err != nil {
		return 0, err
	}
	if len(touched) > 0 {
		if err := s.scheduleWiki(ctx, d.KBID, touched); err != nil {
			return 0, err
		}
	}
	return len(touched), nil
}

// scheduleWiki records touched entities as a durable pending op and
// schedules a debounced wiki ingest for the KB (§7.5).
func (s *Service) scheduleWiki(ctx context.Context, kb uuid.UUID, entities []uuid.UUID) error {
	payload, _ := json.Marshal(map[string]any{"entities": entities})
	if err := s.st.Tasks.EnqueueOp(ctx, postgres.PendingOp{TaskType: types.TaskWikiIngest, Scope: types.ScopeKnowledgeBase, ScopeID: kb.String(), Op: "ingest", Payload: payload}); err != nil {
		return err
	}
	delay := s.cfg.Wiki.IngestDelay
	bucket := time.Now().UnixNano() / int64(max(delay, time.Second))
	if s.cfg.Graph.Resolve.LLMConfirm {
		_ = s.q.Enqueue(ctx, types.TaskGraphResolve, types.KBTaskPayload{KBID: kb}, queue.Opts{TaskID: fmt.Sprintf("resolve:%s:%d", kb, bucket), ProcessIn: delay / 2})
	}
	return s.q.Enqueue(ctx, types.TaskWikiIngest, types.KBTaskPayload{KBID: kb}, queue.Opts{TaskID: fmt.Sprintf("wiki:%s:%d", kb, bucket), ProcessIn: delay})
}

func buildUnits(secs []types.Section, mode string) []unit {
	var out []unit
	clean := func(s string) string {
		var keep []string
		for _, l := range strings.Split(s, "\n") {
			// In-figure text (seal/stamp OCR) is quoted with "> " and skipped (§7.3 step 5).
			if strings.HasPrefix(l, "> ") || strings.HasPrefix(l, "![") {
				continue
			}
			keep = append(keep, l)
		}
		return strings.TrimSpace(strings.Join(keep, "\n"))
	}
	if mode == "page" {
		byPage := map[int][]string{}
		var pages []int
		for _, s := range secs {
			if _, ok := byPage[s.PageStart]; !ok {
				pages = append(pages, s.PageStart)
			}
			byPage[s.PageStart] = append(byPage[s.PageStart], s.Content)
		}
		sort.Ints(pages)
		for i, p := range pages {
			if t := clean(strings.Join(byPage[p], "\n\n")); t != "" {
				out = append(out, unit{id: fmt.Sprintf("u%d", i+1), text: t, pageFrom: p, pageTo: p})
			}
		}
		return out
	}
	for i, s := range secs {
		t := clean(s.Content)
		if t == "" {
			continue
		}
		if len(s.HeadingPath) > 0 && !strings.HasPrefix(t, "#") {
			t = "(" + strings.Join(s.HeadingPath, " › ") + ")\n" + t
		}
		id := s.ID
		out = append(out, unit{id: fmt.Sprintf("u%d", i+1), text: t, pageFrom: s.PageStart, pageTo: s.PageEnd, sectionID: &id})
	}
	return out
}

// evidenceSpans maps a verbatim quote to the lines that contain it.
func evidenceSpans(evidence string, u unit, lines map[int][]types.ParsedLine) []types.SourceSpan {
	ev := textutil.Normalize(evidence)
	var out []types.SourceSpan
	for p := u.pageFrom; p <= u.pageTo; p++ {
		span := types.SourceSpan{Page: p, LineFrom: -1, LineTo: -1}
		for _, l := range lines[p] {
			t := textutil.Normalize(l.Text)
			if len([]rune(t)) < 3 {
				continue
			}
			if strings.Contains(ev, t) || strings.Contains(t, ev) {
				if span.LineFrom < 0 {
					span.LineFrom = l.LineNo
				}
				span.LineTo = l.LineNo
				span.BBox = span.BBox.Union(l.BBox)
			}
		}
		if span.LineFrom >= 0 {
			out = append(out, span)
		}
	}
	return out
}

// ---- resolve ----

const promptSame = `Decide which pairs refer to the same real-world entity. Reply with JSON only: {"same": [<pair index>, ...]}`

func (s *Service) handleResolve(ctx context.Context, raw []byte) error {
	var p types.KBTaskPayload
	if err := json.Unmarshal(raw, &p); err != nil || s.llm == nil {
		return nil
	}
	pairs, err := s.st.Graph.SimilarPairs(ctx, p.KBID, s.cfg.Graph.Resolve.NameSimilarity, 50)
	if err != nil || len(pairs) == 0 {
		return err
	}
	var sb strings.Builder
	for i, pr := range pairs {
		a, _ := json.Marshal(pr[0].Attributes)
		b, _ := json.Marshal(pr[1].Attributes)
		fmt.Fprintf(&sb, "%d. [%s] %q %s  vs  %q %s\n", i, pr[0].Type, pr[0].Name, a, pr[1].Name, b)
	}
	var out struct {
		Same []int `json:"same"`
	}
	if err := s.llm.CompleteJSON(ctx, promptSame, sb.String(), &out); err != nil {
		return err
	}
	for _, i := range out.Same {
		if i >= 0 && i < len(pairs) {
			a, b := pairs[i][0], pairs[i][1]
			into, from := a, b
			if b.Mentions > a.Mentions {
				into, from = b, a
			}
			if err := s.st.Graph.MergeEntities(ctx, into.ID, from.ID); err != nil {
				s.log.Warn("merge entities failed", "err", err)
			}
		}
	}
	return nil
}

// ---- queries ----

var _ interfaces.GraphQuerier = (*Service)(nil)

func (s *Service) ownedKB(ctx context.Context, owner, kb uuid.UUID) error {
	if _, err := s.st.KBs.GetOwned(ctx, kb, owner); err != nil {
		return ErrNotFound
	}
	return nil
}

// SearchEntities implements interfaces.GraphQuerier.
func (s *Service) SearchEntities(ctx context.Context, owner, kb uuid.UUID, query, typ string, limit int) ([]types.Entity, error) {
	if err := s.ownedKB(ctx, owner, kb); err != nil {
		return nil, err
	}
	return s.st.Graph.SearchEntities(ctx, kb, strings.TrimSpace(query), typ, limit)
}

// EntityDetail is an entity with its relations and evidence.
type EntityDetail struct {
	types.Entity
	Relations []RelationView  `json:"relations"`
	Mentions  []types.Mention `json:"mentions"`
}

// RelationView is a relation with the other endpoint resolved.
type RelationView struct {
	types.Relation
	Direction string `json:"direction"` // out | in
	Other     string `json:"other_name"`
	OtherType string `json:"other_type"`
}

// Entity returns one entity with relations and mentions.
func (s *Service) Entity(ctx context.Context, owner, id uuid.UUID) (*EntityDetail, error) {
	e, own, err := s.st.Graph.Entity(ctx, id)
	if err != nil || own != owner {
		return nil, ErrNotFound
	}
	rels, err := s.st.Graph.Relations(ctx, id)
	if err != nil {
		return nil, err
	}
	var otherIDs []uuid.UUID
	for _, r := range rels {
		if r.SourceID == id {
			otherIDs = append(otherIDs, r.TargetID)
		} else {
			otherIDs = append(otherIDs, r.SourceID)
		}
	}
	others, _ := s.st.Graph.EntitiesByID(ctx, otherIDs)
	byID := map[uuid.UUID]types.Entity{}
	for _, o := range others {
		byID[o.ID] = o
	}
	out := &EntityDetail{Entity: e}
	for _, r := range rels {
		v := RelationView{Relation: r, Direction: "out"}
		oid := r.TargetID
		if r.TargetID == id {
			v.Direction, oid = "in", r.SourceID
		}
		v.Other, v.OtherType = byID[oid].Name, byID[oid].Type
		out.Relations = append(out.Relations, v)
	}
	out.Mentions, err = s.st.Graph.Mentions(ctx, &id, nil, 100)
	return out, err
}

// Neighbors implements interfaces.GraphQuerier.
func (s *Service) Neighbors(ctx context.Context, owner, entity uuid.UUID, relTypes []string, depth int) (*types.Subgraph, error) {
	if _, own, err := s.st.Graph.Entity(ctx, entity); err != nil || own != owner {
		return nil, ErrNotFound
	}
	if relTypes == nil {
		relTypes = []string{}
	}
	return s.st.Graph.Neighborhood(ctx, entity, relTypes, depth, 200)
}

// Path finds a relation path between two entities.
func (s *Service) Path(ctx context.Context, owner, from, to uuid.UUID, maxDepth int) (*types.Subgraph, error) {
	for _, id := range []uuid.UUID{from, to} {
		if _, own, err := s.st.Graph.Entity(ctx, id); err != nil || own != owner {
			return nil, ErrNotFound
		}
	}
	return s.st.Graph.Path(ctx, from, to, maxDepth)
}

// WikiPage implements interfaces.GraphQuerier.
func (s *Service) WikiPage(ctx context.Context, owner, kb uuid.UUID, slug string) (*types.WikiPage, error) {
	if err := s.ownedKB(ctx, owner, kb); err != nil {
		return nil, err
	}
	w, err := s.st.Graph.WikiPage(ctx, kb, slug)
	if err != nil {
		return nil, ErrNotFound
	}
	return w, nil
}

// Rebuild re-runs extraction for every searchable document of a KB (after a
// schema change).
func (s *Service) Rebuild(ctx context.Context, owner, kb uuid.UUID) (int, error) {
	if err := s.ownedKB(ctx, owner, kb); err != nil {
		return 0, err
	}
	docs, err := s.st.Documents.List(ctx, postgres.DocumentFilter{OwnerID: owner, KBIDs: []uuid.UUID{kb},
		Statuses: []string{types.DocCompleted, types.DocPartial, types.DocEnriching}, Limit: 500})
	if err != nil {
		return 0, err
	}
	stamp := time.Now().Unix()
	for _, d := range docs {
		if err := s.q.Enqueue(ctx, types.TaskGraphExtract, types.DocTaskPayload{DocumentID: d.ID, KBID: d.KBID, Gen: d.Gen},
			queue.Opts{TaskID: fmt.Sprintf("gx:%s:%d:r%d", d.ID, d.Gen, stamp)}); err != nil {
			return 0, err
		}
	}
	return len(docs), nil
}

// ---- schemas API ----

// Schemas lists schemas (latest per name, or every version of one name).
func (s *Service) Schemas(ctx context.Context, name string) ([]types.GraphSchema, error) {
	return s.st.Graph.Schemas(ctx, name)
}

// CreateSchema validates and stores a new schema version.
func (s *Service) CreateSchema(ctx context.Context, sc types.GraphSchema) (types.GraphSchema, error) {
	if sc.Version == 0 {
		if cur, err := s.st.Graph.LatestSchema(ctx, sc.Name); err == nil {
			sc.Version = cur.Version + 1
		} else {
			sc.Version = 1
		}
	}
	if err := ValidateSchema(&sc); err != nil {
		return sc, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	return s.st.Graph.UpsertSchema(ctx, sc)
}

// TestResult is a dry-run extraction.
type TestResult struct {
	Extraction Extraction `json:"extraction"`
	Warnings   []Warning  `json:"warnings"`
}

// TestSchema extracts from text with a schema without storing anything.
func (s *Service) TestSchema(ctx context.Context, name, text string) (*TestResult, error) {
	if s.llm == nil {
		return nil, fmt.Errorf("%w: no LLM configured for graph extraction", ErrBadRequest)
	}
	sc, err := s.st.Graph.LatestSchema(ctx, name)
	if err != nil {
		return nil, ErrNotFound
	}
	var ex Extraction
	user := fmt.Sprintf("<unit id=\"u1\" pages=\"1-1\">\n%s\n</unit>", text)
	if err := s.llm.CompleteJSON(ctx, ExtractPrompt(sc), user, &ex); err != nil {
		return nil, err
	}
	valid, warns := ValidateExtraction(sc, ex, map[string]string{"u1": text})
	return &TestResult{Extraction: valid, Warnings: warns}, nil
}
