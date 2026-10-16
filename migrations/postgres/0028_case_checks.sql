-- +goose Up
-- U87 (§6.9.9): cross-checks of the documents of a case. The rules live in
-- knowledge_bases.config.checks; a run keeps the sheet it read and its result.
CREATE TABLE case_checks (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id     uuid NOT NULL,                 -- no FK, like documents (U21)
    sheet_id    uuid NOT NULL REFERENCES case_sheets(id) ON DELETE CASCADE,
    status      text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','done','failed')),
    error       text NOT NULL DEFAULT '',
    result      jsonb,                         -- {groups:[{anchor,members,results}],unlinked:[…],counts}
    created_by  uuid NOT NULL REFERENCES users(id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX case_checks_case_idx ON case_checks (case_id, created_at DESC);
CREATE INDEX case_checks_sheet_idx ON case_checks (sheet_id);

-- The contract a user chose for a document; it outlives runs.
CREATE TABLE case_check_links (
    case_id            uuid NOT NULL,
    segment_id         uuid NOT NULL,
    anchor_segment_id  uuid,                   -- NULL = belongs to no contract
    created_by         uuid NOT NULL REFERENCES users(id),
    created_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (case_id, segment_id)
);

-- +goose Down
DROP TABLE IF EXISTS case_check_links;
DROP TABLE IF EXISTS case_checks;
