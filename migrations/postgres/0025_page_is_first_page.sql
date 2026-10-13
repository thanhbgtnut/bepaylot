-- +goose Up
-- U62 (§6.9.4): the Decision API question is_break is now is_first_page
-- (YES / NO); the pages of a file are cut into documents at first pages only.
ALTER TABLE document_pages RENAME COLUMN is_break TO is_first_page;

-- +goose Down
ALTER TABLE document_pages RENAME COLUMN is_first_page TO is_break;
