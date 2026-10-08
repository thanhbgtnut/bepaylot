-- +goose Up
-- U47–U49 (§6.9.4, §6.9.6, §6.9.7): Classification by page range (segments),
-- document bundles (bộ chứng từ) the user reviews, fields that belong to one
-- segment, and sheet templates with one sub-table per document type.
CREATE TABLE case_bundles (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id    uuid NOT NULL,                          -- no FK, like documents (U21)
    seq        int NOT NULL,
    code       text NOT NULL,                          -- B01, B02… by seq
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (case_id, seq)
);

CREATE TABLE document_segments (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id        uuid NOT NULL,
    document_id    uuid NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    gen            int NOT NULL,
    page_start     int NOT NULL,
    page_end       int NOT NULL,
    label          text NOT NULL,                      -- a classification.labels name | unknown | other
    proposed_label text NOT NULL DEFAULT '',           -- the LLM's label when lowered to unknown
    confidence     real NOT NULL DEFAULT 0,
    source         text NOT NULL,                      -- pipeline|user
    status         text NOT NULL DEFAULT 'active',     -- active|stale
    needs_review   boolean NOT NULL DEFAULT false,
    mode           text NOT NULL DEFAULT '',           -- titles|pages (source=pipeline)
    model          text NOT NULL DEFAULT '',
    bundle_id      uuid REFERENCES case_bundles(id) ON DELETE SET NULL, -- source=user only
    created_by     uuid,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CHECK (page_start >= 1 AND page_start <= page_end)
);
CREATE INDEX document_segments_doc_idx ON document_segments (document_id, gen, page_start) WHERE status = 'active';
CREATE INDEX document_segments_case_idx ON document_segments (case_id) WHERE status = 'active';

ALTER TABLE documents
    ADD COLUMN classify_status   text NOT NULL DEFAULT 'none', -- none|running|done|failed|skipped
    ADD COLUMN split_reviewed_at timestamptz,
    ADD COLUMN split_reviewed_by uuid;

ALTER TABLE extracted_fields ADD COLUMN segment_id uuid REFERENCES document_segments(id) ON DELETE SET NULL;
DROP INDEX extracted_fields_one_proposed;
DROP INDEX extracted_fields_one_confirmed;
DROP INDEX extracted_fields_doc_key_idx;
CREATE INDEX extracted_fields_doc_key_idx ON extracted_fields (document_id, segment_id, key, ord, created_at DESC);
CREATE UNIQUE INDEX extracted_fields_one_proposed ON extracted_fields
    (document_id, COALESCE(segment_id, '00000000-0000-0000-0000-000000000000'::uuid), key, ord) WHERE status = 'proposed';
CREATE UNIQUE INDEX extracted_fields_one_confirmed ON extracted_fields
    (document_id, COALESCE(segment_id, '00000000-0000-0000-0000-000000000000'::uuid), key, ord) WHERE status = 'confirmed';

ALTER TABLE prompt_template_versions ADD COLUMN tables jsonb NOT NULL DEFAULT '[]'; -- [{label,title,fields:[{key,label,value_type}]}]
UPDATE prompt_template_versions SET tables = jsonb_build_array(jsonb_build_object('label', '', 'title', 'Tổng hợp', 'fields', fields))
    WHERE fields <> '[]'::jsonb;
ALTER TABLE case_sheets ADD COLUMN tables text[] NOT NULL DEFAULT '{}';   -- labels of the chosen sub-tables

ALTER TABLE field_corrections
    ADD COLUMN label      text NOT NULL DEFAULT '',
    ADD COLUMN segment_id uuid;                        -- no FK: stats outlive a replaced segment
DROP INDEX field_corrections_tpl_idx;
CREATE INDEX field_corrections_tpl_idx ON field_corrections (template_id, template_version, label, key);

-- +goose Down
DROP INDEX IF EXISTS field_corrections_tpl_idx;
CREATE INDEX field_corrections_tpl_idx ON field_corrections (template_id, template_version, key);
ALTER TABLE field_corrections DROP COLUMN IF EXISTS segment_id, DROP COLUMN IF EXISTS label;
ALTER TABLE case_sheets DROP COLUMN IF EXISTS tables;
ALTER TABLE prompt_template_versions DROP COLUMN IF EXISTS tables;
DROP INDEX IF EXISTS extracted_fields_one_proposed;
DROP INDEX IF EXISTS extracted_fields_one_confirmed;
DROP INDEX IF EXISTS extracted_fields_doc_key_idx;
ALTER TABLE extracted_fields DROP COLUMN IF EXISTS segment_id;
CREATE INDEX extracted_fields_doc_key_idx ON extracted_fields (document_id, key, ord, created_at DESC);
CREATE UNIQUE INDEX extracted_fields_one_proposed ON extracted_fields (document_id, key, ord) WHERE status = 'proposed';
CREATE UNIQUE INDEX extracted_fields_one_confirmed ON extracted_fields (document_id, key, ord) WHERE status = 'confirmed';
ALTER TABLE documents DROP COLUMN IF EXISTS split_reviewed_by, DROP COLUMN IF EXISTS split_reviewed_at, DROP COLUMN IF EXISTS classify_status;
DROP TABLE IF EXISTS document_segments;
DROP TABLE IF EXISTS case_bundles;
