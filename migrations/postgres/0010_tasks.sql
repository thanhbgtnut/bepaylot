-- +goose Up
CREATE TABLE task_dead_letters (
    id         bigserial PRIMARY KEY,
    task_type  text NOT NULL,
    queue      text NOT NULL DEFAULT '',
    scope      text NOT NULL,
    scope_id   text NOT NULL,
    related_id text NOT NULL DEFAULT '',
    payload    jsonb NOT NULL DEFAULT '{}',
    last_error text NOT NULL DEFAULT '',
    fail_count int NOT NULL DEFAULT 0,
    failed_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX task_dead_letters_scope ON task_dead_letters (scope, scope_id);
CREATE INDEX task_dead_letters_type ON task_dead_letters (task_type, failed_at DESC);

CREATE TABLE task_pending_ops (
    id          bigserial PRIMARY KEY,
    task_type   text NOT NULL,
    scope       text NOT NULL,
    scope_id    text NOT NULL,
    op          text NOT NULL,
    dedup_key   text NOT NULL DEFAULT '',
    payload     jsonb NOT NULL DEFAULT '{}',
    fail_count  int NOT NULL DEFAULT 0,
    enqueued_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX task_pending_ops_scope ON task_pending_ops (task_type, scope, scope_id, id);

CREATE TABLE processing_spans (
    id          bigserial PRIMARY KEY,
    document_id uuid NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    gen         int NOT NULL,
    stage       text NOT NULL,
    ref         text NOT NULL DEFAULT '',
    status      text NOT NULL,
    error       text NOT NULL DEFAULT '',
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX processing_spans_doc ON processing_spans (document_id, gen, started_at);

-- +goose Down
DROP TABLE IF EXISTS processing_spans;
DROP TABLE IF EXISTS task_pending_ops;
DROP TABLE IF EXISTS task_dead_letters;
