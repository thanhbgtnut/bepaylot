-- +goose Up
-- The VLM's page markdown, verbatim (turboocr_vlm, §5.9). markdown stays the
-- one rebuilt from the blocks: it carries the line and block offsets.
ALTER TABLE document_pages ADD COLUMN markdown_vlm text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE document_pages DROP COLUMN markdown_vlm;
