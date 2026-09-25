-- +goose Up
CREATE TABLE sections (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id  uuid NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    kb_id        uuid NOT NULL,
    gen          int NOT NULL,
    seq          int NOT NULL,
    kind         text NOT NULL,
    content      text NOT NULL,
    heading_path text[] NOT NULL DEFAULT '{}',
    heading_text text NOT NULL DEFAULT '',
    page_start   int NOT NULL,
    page_end     int NOT NULL,
    line_from    int NOT NULL,
    line_to      int NOT NULL,
    doc_md_start int NOT NULL DEFAULT -1,
    doc_md_end   int NOT NULL DEFAULT -1,
    source_spans jsonb NOT NULL DEFAULT '[]',
    token_count  int NOT NULL DEFAULT 0,
    tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple', unaccent_vi(heading_text || ' ' || content))) STORED,
    tsv_exact tsvector GENERATED ALWAYS AS (to_tsvector('simple', lower(content))) STORED
);
CREATE INDEX sections_doc_idx ON sections (document_id, gen, seq);
CREATE INDEX sections_kb_idx ON sections (kb_id);
CREATE INDEX sections_tsv_idx ON sections USING gin (tsv);
CREATE INDEX sections_tsv_exact_idx ON sections USING gin (tsv_exact);
CREATE INDEX sections_trgm_idx ON sections USING gin (content gin_trgm_ops);

CREATE TABLE doc_tree_nodes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id uuid NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    gen         int NOT NULL,
    parent_id   uuid REFERENCES doc_tree_nodes(id) ON DELETE CASCADE,
    short_id    text NOT NULL,
    ord         int NOT NULL,
    level       int NOT NULL,
    title       text NOT NULL,
    origin      text NOT NULL,
    page_start  int NOT NULL,
    page_end    int NOT NULL,
    summary     text NOT NULL DEFAULT '',
    section_ids uuid[] NOT NULL DEFAULT '{}',
    token_count int NOT NULL DEFAULT 0,
    UNIQUE (document_id, gen, short_id)
);
CREATE INDEX doc_tree_nodes_doc_idx ON doc_tree_nodes (document_id, gen, parent_id, ord);

-- +goose Down
DROP TABLE IF EXISTS doc_tree_nodes;
DROP TABLE IF EXISTS sections;
