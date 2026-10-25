-- +goose Up
-- U94, U98 (§6.9.10): test runs and AI optimizations of a document type run
-- in the background; the page polls the job. An optimization keeps its
-- proposal here until the user applies (a new version) or drops it.
CREATE TABLE doc_type_jobs (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    uuid NOT NULL,
    code        text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('run','optimize')),
    status      text NOT NULL DEFAULT 'running' CHECK (status IN ('running','done','failed')),
    version     int  NOT NULL,
    sample_ids  uuid[] NOT NULL DEFAULT '{}',
    result      jsonb,
    error       text NOT NULL DEFAULT '',
    created_by  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    FOREIGN KEY (owner_id, code) REFERENCES doc_types (owner_id, code) ON DELETE CASCADE ON UPDATE CASCADE
);
CREATE INDEX doc_type_jobs_type_idx ON doc_type_jobs (owner_id, code, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS doc_type_jobs;
