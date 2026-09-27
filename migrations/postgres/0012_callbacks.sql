-- +goose Up
-- Optional completion callback per document (§4.7). The URL is given at upload
-- (or on reparse); each terminal status of a generation produces one delivery
-- row, and every HTTP attempt is logged.
ALTER TABLE documents ADD COLUMN callback_url text NOT NULL DEFAULT '';
-- Bumped by a page reparse (same generation, new run → new delivery).
ALTER TABLE documents ADD COLUMN callback_run int NOT NULL DEFAULT 0;

CREATE TABLE document_callbacks (
    id               uuid PRIMARY KEY,
    document_id      uuid NOT NULL REFERENCES documents (id) ON DELETE CASCADE,
    gen              int NOT NULL,
    run              int NOT NULL DEFAULT 0,
    event            text NOT NULL,                  -- document.completed | .partial | .failed | .cancelled
    url              text NOT NULL,
    state            text NOT NULL DEFAULT 'pending', -- pending | succeeded | failed
    attempts         int NOT NULL DEFAULT 0,
    max_attempts     int NOT NULL,
    last_status_code int,
    last_error       text NOT NULL DEFAULT '',
    last_attempt_at  timestamptz,
    next_attempt_at  timestamptz,
    delivered_at     timestamptz,
    payload          jsonb NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (document_id, gen, run, url)
);
CREATE INDEX document_callbacks_doc_idx ON document_callbacks (document_id, created_at DESC);
CREATE INDEX document_callbacks_due_idx ON document_callbacks (next_attempt_at) WHERE state = 'pending';

CREATE TABLE document_callback_attempts (
    callback_id  uuid NOT NULL REFERENCES document_callbacks (id) ON DELETE CASCADE,
    attempt      int NOT NULL,
    status_code  int,
    error        text NOT NULL DEFAULT '',
    response     text NOT NULL DEFAULT '', -- first bytes of the response body
    duration_ms  int NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (callback_id, attempt)
);

-- +goose Down
DROP TABLE IF EXISTS document_callback_attempts;
DROP TABLE IF EXISTS document_callbacks;
ALTER TABLE documents DROP COLUMN IF EXISTS callback_run;
ALTER TABLE documents DROP COLUMN IF EXISTS callback_url;
