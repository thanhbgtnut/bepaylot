-- +goose Up
-- Answers of slow model calls on a page image (Decision API classification,
-- VLM transcription), kept by the hash of the request so the same page is
-- never sent twice (reparse, the same file uploaded again; §5.9, §6.9.4).
CREATE TABLE model_cache (
    key        text PRIMARY KEY,                       -- sha256 of kind, model, prompt/labels and image
    kind       text NOT NULL,                          -- decision | vlm
    value      jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS model_cache;
