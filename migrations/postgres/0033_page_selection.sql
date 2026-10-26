-- +goose Up
-- U111 (§4.2, §4.8): only the pages the user picks go through the pipeline.
-- hold: uploaded and counted (split only probes), every page 'skipped' until
-- the user picks pages; pages_skipped: pages of the generation not picked.
ALTER TABLE documents
    ADD COLUMN hold          boolean NOT NULL DEFAULT false,
    ADD COLUMN pages_skipped int     NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE documents
    DROP COLUMN IF EXISTS pages_skipped,
    DROP COLUMN IF EXISTS hold;
