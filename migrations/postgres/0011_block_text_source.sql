-- +goose Up
-- Blocks transcribed by a VLM keep their text verbatim in markdown (§5.9).
ALTER TABLE page_blocks ADD COLUMN text_source text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE page_blocks DROP COLUMN IF EXISTS text_source;
