-- +goose Up
-- U50, U51 (§6.9.4, §6.5): the document type of each page, read from the
-- page image by the Decision API, and the document-type nodes of a file's tree.
ALTER TABLE document_pages
    ADD COLUMN doc_type            text NOT NULL DEFAULT '', -- a doc_types code of the KB; '' = not classified
    ADD COLUMN doc_type_confidence real NOT NULL DEFAULT 0;
ALTER TABLE doc_tree_nodes ADD COLUMN doc_type text NOT NULL DEFAULT ''; -- origin=doctype: the label of the node

-- +goose Down
ALTER TABLE doc_tree_nodes DROP COLUMN IF EXISTS doc_type;
ALTER TABLE document_pages DROP COLUMN IF EXISTS doc_type_confidence, DROP COLUMN IF EXISTS doc_type;
