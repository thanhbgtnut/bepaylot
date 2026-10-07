-- +goose Up
-- Extracted Field and its evidence (§6.9.2, §6.9.3): the part of the
-- document model the case sheets (U43, U44) need. Tables, cells and
-- classification (rest of §6.9) come with P8.
CREATE TABLE extracted_fields (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id       uuid NOT NULL,                       -- copied from documents.case_id (no FK, U21)
    document_id   uuid NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    gen           int NOT NULL,
    key           text NOT NULL,                       -- snake_case
    ord           int NOT NULL DEFAULT 0,
    value         jsonb NOT NULL,
    value_type    text NOT NULL DEFAULT 'string',      -- string|number|money|date|bool|json
    value_text    text NOT NULL DEFAULT '',
    value_matched boolean NOT NULL DEFAULT false,
    confidence    real NOT NULL DEFAULT 0,
    status        text NOT NULL DEFAULT 'proposed',    -- proposed|confirmed|rejected|superseded|stale
    source        text NOT NULL,                       -- agent|user
    supersedes    uuid REFERENCES extracted_fields(id) ON DELETE SET NULL,
    session_id    uuid,
    created_by    uuid,
    reviewed_by   uuid,
    reviewed_at   timestamptz,
    needs_review  boolean NOT NULL DEFAULT false,
    note          text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX extracted_fields_doc_key_idx ON extracted_fields (document_id, key, ord, created_at DESC);
CREATE UNIQUE INDEX extracted_fields_one_proposed ON extracted_fields (document_id, key, ord) WHERE status = 'proposed';
CREATE UNIQUE INDEX extracted_fields_one_confirmed ON extracted_fields (document_id, key, ord) WHERE status = 'confirmed';
CREATE INDEX extracted_fields_case_key_idx ON extracted_fields (case_id, key) WHERE status IN ('proposed','confirmed');

CREATE TABLE evidence_spans (
    id          bigserial PRIMARY KEY,
    owner_type  text NOT NULL CHECK (owner_type IN ('field','segment')),
    owner_id    uuid NOT NULL,
    n           int NOT NULL,
    document_id uuid NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    gen         int NOT NULL,
    page_no     int NOT NULL,
    anchor      text NOT NULL,                         -- lines|element|cell|page
    line_from   int,
    line_to     int,
    block_no    int,
    row_no      int,
    col_no      int,
    bbox        real[4],
    quote       text NOT NULL DEFAULT '',
    citation_id text NOT NULL,
    status      text NOT NULL DEFAULT 'valid',         -- valid|stale
    checked_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_type, owner_id, n)
);
CREATE INDEX evidence_spans_doc_idx ON evidence_spans (document_id, gen, page_no);
CREATE INDEX evidence_spans_owner_idx ON evidence_spans (owner_type, owner_id);

-- +goose Down
DROP TABLE IF EXISTS evidence_spans;
DROP TABLE IF EXISTS extracted_fields;
