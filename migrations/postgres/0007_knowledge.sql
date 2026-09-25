-- +goose Up
CREATE TABLE knowledge_bases (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    config          jsonb NOT NULL DEFAULT '{}',
    metadata_schema jsonb,
    graph_schema_id uuid,
    is_temporary    boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);
CREATE INDEX knowledge_bases_owner_idx ON knowledge_bases (owner_id, created_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE upload_batches (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kb_id      uuid NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    created_by uuid NOT NULL REFERENCES users(id),
    metadata   jsonb NOT NULL DEFAULT '{}',
    file_count int NOT NULL,
    accepted   int NOT NULL DEFAULT 0,
    rejected   int NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE documents (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kb_id            uuid NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    batch_id         uuid REFERENCES upload_batches(id) ON DELETE SET NULL,
    created_by       uuid REFERENCES users(id) ON DELETE SET NULL,
    file_name        text NOT NULL,
    mime_type        text NOT NULL,
    size_bytes       bigint NOT NULL,
    sha256           text NOT NULL,
    storage_key      text NOT NULL,
    page_count       int NOT NULL DEFAULT 0,
    gen              int NOT NULL DEFAULT 1,
    status           text NOT NULL,
    parse_status     text NOT NULL DEFAULT 'pending',
    index_status     text NOT NULL DEFAULT 'pending',
    graph_status     text NOT NULL DEFAULT 'skipped',
    pages_done       int NOT NULL DEFAULT 0,
    pages_failed     int NOT NULL DEFAULT 0,
    pages_text_layer int NOT NULL DEFAULT 0,
    pdfa_part        int,
    pdfa_conformance text NOT NULL DEFAULT '',
    pdf_info         jsonb NOT NULL DEFAULT '{}',
    engine           text NOT NULL DEFAULT '',
    markdown_key     text NOT NULL DEFAULT '',
    error            text NOT NULL DEFAULT '',
    metadata         jsonb NOT NULL DEFAULT '{}',
    title            text NOT NULL DEFAULT '',
    doc_type         text NOT NULL DEFAULT '',
    summary          text NOT NULL DEFAULT '',
    interactive      boolean NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    meta_tsv tsvector GENERATED ALWAYS AS (
        to_tsvector('simple', unaccent_vi(
            file_name || ' ' || title || ' ' || doc_type || ' ' || summary || ' ' ||
            jsonb_values_text(metadata) || ' ' || coalesce(pdf_info->>'Title', '') || ' ' ||
            coalesce(pdf_info->>'Subject', '') || ' ' || coalesce(pdf_info->>'Keywords', '')))
    ) STORED
);
CREATE UNIQUE INDEX documents_kb_sha_uq ON documents (kb_id, sha256) WHERE deleted_at IS NULL;
CREATE INDEX documents_kb_created_idx ON documents (kb_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX documents_metadata_gin ON documents USING gin (metadata jsonb_path_ops);
CREATE INDEX documents_metadata_keys_gin ON documents USING gin (metadata);
CREATE INDEX documents_meta_tsv_idx ON documents USING gin (meta_tsv);
CREATE INDEX documents_batch_idx ON documents (batch_id);
CREATE INDEX documents_status_idx ON documents (status) WHERE deleted_at IS NULL;

CREATE TABLE document_pages (
    document_id    uuid NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    page_no        int NOT NULL,
    gen            int NOT NULL,
    status         text NOT NULL,
    attempts       int NOT NULL DEFAULT 0,
    width          int NOT NULL DEFAULT 0,
    height         int NOT NULL DEFAULT 0,
    dpi            real NOT NULL DEFAULT 0,
    width_pt       real NOT NULL DEFAULT 0,
    height_pt      real NOT NULL DEFAULT 0,
    rotation       int NOT NULL DEFAULT 0,
    image_key      text NOT NULL DEFAULT '',
    raw_key        text NOT NULL DEFAULT '',
    text_layer_key text NOT NULL DEFAULT '',
    text_source    text NOT NULL DEFAULT '',
    text_quality   real NOT NULL DEFAULT 0,
    render_ms      int NOT NULL DEFAULT 0,
    ocr_ms         int NOT NULL DEFAULT 0,
    engine         text NOT NULL DEFAULT '',
    markdown       text NOT NULL DEFAULT '',
    text_plain     text NOT NULL DEFAULT '',
    doc_md_offset  int NOT NULL DEFAULT 0,
    is_blank       boolean NOT NULL DEFAULT false,
    error          text NOT NULL DEFAULT '',
    started_at     timestamptz,
    finished_at    timestamptz,
    tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple', unaccent_vi(text_plain))) STORED,
    PRIMARY KEY (document_id, page_no)
);
CREATE INDEX document_pages_status_idx ON document_pages (document_id, status, page_no);
CREATE INDEX document_pages_tsv_idx ON document_pages USING gin (tsv);

CREATE TABLE page_blocks (
    document_id  uuid NOT NULL,
    page_no      int NOT NULL,
    block_no     int NOT NULL,
    source_id    int NOT NULL DEFAULT -1,
    type         text NOT NULL,
    raw_class    text NOT NULL DEFAULT '',
    confidence   real NOT NULL DEFAULT 0,
    bbox         real[] NOT NULL,
    text         text NOT NULL DEFAULT '',
    html         text NOT NULL DEFAULT '',
    latex        text NOT NULL DEFAULT '',
    asset_key    text NOT NULL DEFAULT '',
    is_furniture boolean NOT NULL DEFAULT false,
    md_start     int NOT NULL DEFAULT -1,
    md_end       int NOT NULL DEFAULT -1,
    PRIMARY KEY (document_id, page_no, block_no),
    FOREIGN KEY (document_id, page_no) REFERENCES document_pages (document_id, page_no) ON DELETE CASCADE
);

CREATE TABLE page_lines (
    document_id    uuid NOT NULL,
    page_no        int NOT NULL,
    line_no        int NOT NULL,
    source_id      int NOT NULL DEFAULT -1,
    block_no       int NOT NULL DEFAULT -1,
    text           text NOT NULL,
    text_ocr       text NOT NULL DEFAULT '',
    text_layer     text NOT NULL DEFAULT '',
    text_source    text NOT NULL DEFAULT 'ocr',
    confidence     real NOT NULL DEFAULT 0,
    quad           real[] NOT NULL,
    bbox           real[] NOT NULL,
    in_figure      boolean NOT NULL DEFAULT false,
    low_confidence boolean NOT NULL DEFAULT false,
    md_start       int NOT NULL DEFAULT -1,
    md_end         int NOT NULL DEFAULT -1,
    PRIMARY KEY (document_id, page_no, line_no),
    FOREIGN KEY (document_id, page_no) REFERENCES document_pages (document_id, page_no) ON DELETE CASCADE
);
CREATE INDEX page_lines_trgm_idx ON page_lines USING gin (unaccent_vi(text) gin_trgm_ops);

-- +goose Down
DROP TABLE IF EXISTS page_lines;
DROP TABLE IF EXISTS page_blocks;
DROP TABLE IF EXISTS document_pages;
DROP TABLE IF EXISTS documents;
DROP TABLE IF EXISTS upload_batches;
DROP TABLE IF EXISTS knowledge_bases;
