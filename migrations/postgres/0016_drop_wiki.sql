-- +goose Up
-- Search walks document trees only (§6.6, U35): the LLM Wiki of each case is
-- removed. Its tables, the wiki columns of cases and documents and the
-- "enriching" status go; documents that were being ingested are completed.
DROP TABLE IF EXISTS wiki_lint_issues, wiki_log, wiki_index, wiki_page_revisions,
    wiki_links, wiki_footnotes, wiki_pages, wiki_schemas CASCADE;
ALTER TABLE cases
    DROP COLUMN IF EXISTS wiki_schema,
    DROP COLUMN IF EXISTS wiki_status,
    DROP COLUMN IF EXISTS wiki_version,
    DROP COLUMN IF EXISTS wiki_built_at,
    DROP COLUMN IF EXISTS wiki_docs_covered;
UPDATE documents SET status = CASE WHEN parse_status = 'partial' THEN 'partial' ELSE 'completed' END, updated_at = now()
    WHERE status = 'enriching';
ALTER TABLE documents DROP COLUMN IF EXISTS wiki_status;
DELETE FROM task_pending_ops WHERE task_type LIKE 'wiki:%';
DELETE FROM task_dead_letters WHERE task_type LIKE 'wiki:%';

-- Tokens of a node's subtree rendered as a table of contents (§6.5 step 7):
-- tells search whether the whole tree fits the prompt. NULL on trees built
-- before this migration; it is then computed when read.
ALTER TABLE doc_tree_nodes ADD COLUMN tree_tokens int;

-- +goose Down
ALTER TABLE doc_tree_nodes DROP COLUMN IF EXISTS tree_tokens;
ALTER TABLE documents ADD COLUMN wiki_status text NOT NULL DEFAULT 'skipped';
ALTER TABLE cases
    ADD COLUMN wiki_schema       text NOT NULL DEFAULT 'generic',
    ADD COLUMN wiki_status       text NOT NULL DEFAULT 'none',
    ADD COLUMN wiki_version      int NOT NULL DEFAULT 0,
    ADD COLUMN wiki_built_at     timestamptz,
    ADD COLUMN wiki_docs_covered int NOT NULL DEFAULT 0;
-- The wiki tables are not recreated: roll back to 0015 and re-run 0014 to
-- rebuild them empty.
