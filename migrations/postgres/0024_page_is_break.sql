-- +goose Up
-- U58 (§6.9.4): whether the Decision API read the page as the first page of
-- a document (question is_break), so consecutive documents of one type are
-- cut apart.
ALTER TABLE document_pages ADD COLUMN is_break boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE document_pages DROP COLUMN IF EXISTS is_break;
