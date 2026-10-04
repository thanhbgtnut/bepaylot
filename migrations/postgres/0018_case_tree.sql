-- +goose Up
-- One tree per case (§6.5): case → files in the order they were added →
-- the file's own tree (doc_tree_nodes). A file is appended once, when its
-- first tree is stored; a new file, reparse or deletion never rebuilds the
-- other branches.
CREATE TABLE case_tree (
    case_id     uuid NOT NULL,
    document_id uuid PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
    seq         bigint GENERATED ALWAYS AS IDENTITY,
    added_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX case_tree_case_idx ON case_tree (case_id, seq);

-- Existing trees join their case in upload order.
INSERT INTO case_tree (case_id, document_id)
SELECT d.case_id, d.id FROM documents d
WHERE d.case_id IS NOT NULL AND EXISTS (SELECT 1 FROM doc_tree_nodes n WHERE n.document_id = d.id)
ORDER BY d.created_at;

-- +goose Down
DROP TABLE IF EXISTS case_tree;
