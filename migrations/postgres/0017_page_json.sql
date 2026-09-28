-- +goose Up
-- Per-page JSON is structured data: the engine's raw answer (layout, VLM
-- calls) and the PDF text layer are stored on the page row instead of as
-- gzip objects in S3 (§9.1). raw_key / text_layer_key stay for pages parsed
-- before this migration; new pages leave them empty.
ALTER TABLE document_pages
    ADD COLUMN IF NOT EXISTS raw jsonb,
    ADD COLUMN IF NOT EXISTS text_layer jsonb;

-- +goose Down
ALTER TABLE document_pages
    DROP COLUMN IF EXISTS raw,
    DROP COLUMN IF EXISTS text_layer;
