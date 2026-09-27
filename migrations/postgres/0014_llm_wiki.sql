-- +goose Up
-- LLM Wiki per case (§6.6–6.9). The KB-wide graph and entity wiki of spec 0.4
-- are dropped without conversion: every file is ingested again into the wiki
-- of its case (housekeeping queues the 'pending' ones).

-- 1) Drop the old graph/wiki data.
DROP TABLE IF EXISTS kg_mentions, kg_relations, kg_entities, graph_schemas, wiki_page_revisions, wiki_pages CASCADE;
DELETE FROM task_pending_ops WHERE task_type LIKE 'graph:%' OR task_type LIKE 'wiki:%';
DELETE FROM task_dead_letters WHERE task_type LIKE 'graph:%' OR task_type LIKE 'wiki:%';
ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS graph_schema_id;
UPDATE knowledge_bases SET config = config - 'graph_enabled' - 'graph_schema';

-- Documents are no longer classified at index time: doc_type goes, and the
-- generated meta_tsv that referenced it is rebuilt without it.
ALTER TABLE documents DROP COLUMN meta_tsv;
ALTER TABLE documents DROP COLUMN doc_type;
ALTER TABLE documents ADD COLUMN meta_tsv tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', unaccent_vi(
        file_name || ' ' || title || ' ' || summary || ' ' ||
        jsonb_values_text(metadata) || ' ' || coalesce(pdf_info->>'Title', '') || ' ' ||
        coalesce(pdf_info->>'Subject', '') || ' ' || coalesce(pdf_info->>'Keywords', '')))
) STORED;
CREATE INDEX documents_meta_tsv_idx ON documents USING gin (meta_tsv);

ALTER TABLE documents RENAME COLUMN graph_status TO wiki_status;  -- pending|processing|done|partial|failed|skipped
UPDATE documents SET wiki_status = 'pending' WHERE deleted_at IS NULL AND status IN ('completed', 'partial', 'enriching');
UPDATE documents SET status = CASE WHEN parse_status = 'partial' THEN 'partial' ELSE 'completed' END
WHERE status = 'enriching';

-- 2) Wiki state of a case.
ALTER TABLE cases
    ADD COLUMN wiki_schema       text NOT NULL DEFAULT 'generic',
    ADD COLUMN wiki_status       text NOT NULL DEFAULT 'none',   -- none|building|ready|stale|failed
    ADD COLUMN wiki_version      int NOT NULL DEFAULT 0,
    ADD COLUMN wiki_built_at     timestamptz,
    ADD COLUMN wiki_docs_covered int NOT NULL DEFAULT 0;

-- 3) Schemas (§6.7).
CREATE TABLE wiki_schemas (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    version    int NOT NULL,
    spec       jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (name, version)
);

-- 4) Pages. No FK to cases, like documents.case_id.
CREATE TABLE wiki_pages (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id             uuid NOT NULL,
    slug                text NOT NULL,
    kind                text NOT NULL,                  -- overview|source|entity|topic|note
    entity_type         text,
    identity_key        text,
    document_id         uuid,                           -- kind = source
    title               text NOT NULL,
    aliases             text[] NOT NULL DEFAULT '{}',
    summary             text NOT NULL DEFAULT '',
    content             text NOT NULL DEFAULT '',
    attributes          jsonb NOT NULL DEFAULT '{}',
    ord                 int NOT NULL DEFAULT 0,
    version             int NOT NULL DEFAULT 1,
    last_edit_source    text NOT NULL DEFAULT 'system', -- system|user
    proposed_content    text,
    proposed_attributes jsonb,
    tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple', unaccent_vi(title || ' ' || summary || ' ' || content))) STORED,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (case_id, slug)
);
CREATE UNIQUE INDEX wiki_pages_identity_uq ON wiki_pages (case_id, entity_type, identity_key) WHERE identity_key IS NOT NULL;
CREATE UNIQUE INDEX wiki_pages_source_uq ON wiki_pages (case_id, document_id) WHERE kind = 'source';
CREATE UNIQUE INDEX wiki_pages_overview_uq ON wiki_pages (case_id) WHERE kind = 'overview';
CREATE INDEX wiki_pages_case_kind_idx ON wiki_pages (case_id, kind, entity_type, ord);
CREATE INDEX wiki_pages_tsv_idx ON wiki_pages USING gin (tsv);
CREATE INDEX wiki_pages_title_trgm ON wiki_pages USING gin (unaccent_vi(title) gin_trgm_ops);

-- 5) Footnotes: the bridge from the wiki back to source lines.
CREATE TABLE wiki_footnotes (
    page_id     uuid NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    n           int NOT NULL,
    document_id uuid NOT NULL,
    gen         int NOT NULL,
    page_no     int NOT NULL,
    line_from   int NOT NULL,
    line_to     int NOT NULL,
    quote       text NOT NULL,
    citation_id text NOT NULL,                          -- doc:{id}:p{n}:l{a}-{b}
    status      text NOT NULL DEFAULT 'valid',          -- valid|stale
    checked_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, n)
);
CREATE INDEX wiki_footnotes_doc_idx ON wiki_footnotes (document_id, gen);

-- 6) Links, plain ([[slug]]) or typed by the schema.
CREATE TABLE wiki_links (
    case_id    uuid NOT NULL,
    from_page  uuid NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    to_page    uuid NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    relation   text NOT NULL DEFAULT '',
    attributes jsonb NOT NULL DEFAULT '{}',
    footnote_n int,
    PRIMARY KEY (from_page, to_page, relation)
);
CREATE INDEX wiki_links_to_idx ON wiki_links (to_page);
CREATE INDEX wiki_links_case_idx ON wiki_links (case_id);

-- 7) Page history.
CREATE TABLE wiki_page_revisions (
    page_id     uuid NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    version     int NOT NULL,
    title       text NOT NULL,
    content     text NOT NULL,
    attributes  jsonb NOT NULL DEFAULT '{}',
    edit_source text NOT NULL,
    editor_id   uuid,
    log_id      bigint,
    edited_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, version)
);

-- 8) Index (index.md): the latest build and the one before.
CREATE TABLE wiki_index (
    case_id     uuid NOT NULL,
    version     int NOT NULL,
    content     text NOT NULL,
    refs        jsonb NOT NULL,
    token_count int NOT NULL,
    doc_gens    jsonb NOT NULL,
    built_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (case_id, version)
);

-- 9) Log (log.md), append-only.
CREATE TABLE wiki_log (
    id          bigserial PRIMARY KEY,
    case_id     uuid NOT NULL,
    at          timestamptz NOT NULL DEFAULT now(),
    op          text NOT NULL,                          -- ingest|retract|lint|edit|note|rebuild
    ref         text NOT NULL DEFAULT '',
    document_id uuid,
    pages       text[] NOT NULL DEFAULT '{}',
    summary     text NOT NULL DEFAULT '',
    actor       text NOT NULL DEFAULT 'system',
    llm_calls   int NOT NULL DEFAULT 0,
    tokens_in   int NOT NULL DEFAULT 0,
    tokens_out  int NOT NULL DEFAULT 0
);
CREATE INDEX wiki_log_case_idx ON wiki_log (case_id, at DESC);

-- 10) Lint issues.
CREATE TABLE wiki_lint_issues (
    id          bigserial PRIMARY KEY,
    case_id     uuid NOT NULL,
    kind        text NOT NULL,                          -- stale|contradiction|orphan|missing_link|gap|index_drift
    page_ids    uuid[] NOT NULL DEFAULT '{}',
    detail      jsonb NOT NULL DEFAULT '{}',
    fingerprint text NOT NULL DEFAULT '',               -- dedups repeated findings
    status      text NOT NULL DEFAULT 'open',           -- open|fixed|dismissed
    found_at    timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    resolved_by uuid
);
CREATE INDEX wiki_lint_open_idx ON wiki_lint_issues (case_id, status, kind);
CREATE UNIQUE INDEX wiki_lint_fp_uq ON wiki_lint_issues (case_id, fingerprint) WHERE status = 'open' AND fingerprint <> '';

-- +goose Down
DROP TABLE IF EXISTS wiki_lint_issues, wiki_log, wiki_index, wiki_page_revisions, wiki_links, wiki_footnotes, wiki_pages, wiki_schemas;
ALTER TABLE cases DROP COLUMN IF EXISTS wiki_docs_covered, DROP COLUMN IF EXISTS wiki_built_at,
    DROP COLUMN IF EXISTS wiki_version, DROP COLUMN IF EXISTS wiki_status, DROP COLUMN IF EXISTS wiki_schema;
ALTER TABLE documents RENAME COLUMN wiki_status TO graph_status;
ALTER TABLE documents ADD COLUMN doc_type text NOT NULL DEFAULT '';
ALTER TABLE knowledge_bases ADD COLUMN graph_schema_id uuid;
