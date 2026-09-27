-- +goose Up
-- Cases (§6.2): a file set identified by a business code (e.g. RT112233).
-- Every document belongs to exactly one case; the case is the hard search
-- scope of agent sessions. documents.case_id and sessions.case_id are NOT
-- foreign keys on purpose (U21): the service layer keeps them consistent.
CREATE TABLE cases (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kb_id      uuid NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    code       text NOT NULL,                      -- normalized per case type
    case_type  text NOT NULL DEFAULT 'default',    -- configs/case_types/<name>.yaml
    title      text NOT NULL DEFAULT '',
    status     text NOT NULL DEFAULT 'open',       -- open | closed
    metadata   jsonb NOT NULL DEFAULT '{}',        -- validated by case_metadata_schema
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE UNIQUE INDEX cases_kb_code_uq ON cases (kb_id, code) WHERE deleted_at IS NULL;
CREATE INDEX cases_kb_created_idx ON cases (kb_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX cases_code_trgm_idx ON cases USING gin (code gin_trgm_ops);
CREATE INDEX cases_metadata_gin ON cases USING gin (metadata jsonb_path_ops);

ALTER TABLE documents ADD COLUMN case_id uuid;

-- Existing data: each metadata->>'ma_ho_so' value (trimmed, upper-cased) of a
-- KB becomes a case; the remaining documents of a KB go to '_UNASSIGNED'.
INSERT INTO cases (kb_id, code, created_by)
SELECT DISTINCT d.kb_id, upper(btrim(d.metadata->>'ma_ho_so')), kb.owner_id
FROM documents d JOIN knowledge_bases kb ON kb.id = d.kb_id
WHERE coalesce(btrim(d.metadata->>'ma_ho_so'), '') <> ''
ON CONFLICT (kb_id, code) WHERE deleted_at IS NULL DO NOTHING;

UPDATE documents d SET case_id = c.id
FROM cases c
WHERE c.kb_id = d.kb_id AND c.deleted_at IS NULL
  AND c.code = upper(btrim(d.metadata->>'ma_ho_so'));

INSERT INTO cases (kb_id, code, created_by)
SELECT DISTINCT d.kb_id, '_UNASSIGNED', kb.owner_id
FROM documents d JOIN knowledge_bases kb ON kb.id = d.kb_id
WHERE d.case_id IS NULL
ON CONFLICT (kb_id, code) WHERE deleted_at IS NULL DO NOTHING;

UPDATE documents d SET case_id = c.id
FROM cases c
WHERE d.case_id IS NULL AND c.kb_id = d.kb_id AND c.code = '_UNASSIGNED' AND c.deleted_at IS NULL;

-- The case code is no longer metadata.
UPDATE documents SET metadata = metadata - 'ma_ho_so' WHERE metadata ? 'ma_ho_so';
UPDATE knowledge_bases
SET metadata_schema = jsonb_set(metadata_schema, '{fields}', coalesce(
        (SELECT jsonb_agg(f) FROM jsonb_array_elements(metadata_schema->'fields') f WHERE f->>'key' <> 'ma_ho_so'),
        '[]'::jsonb))
WHERE jsonb_typeof(metadata_schema->'fields') = 'array';

ALTER TABLE documents ALTER COLUMN case_id SET NOT NULL;
DROP INDEX IF EXISTS documents_kb_sha_uq;
CREATE UNIQUE INDEX documents_case_sha_uq ON documents (case_id, sha256) WHERE deleted_at IS NULL;
CREATE INDEX documents_case_created_idx ON documents (case_id, created_at DESC) WHERE deleted_at IS NULL;

-- Sessions are bound to at most one case, for their whole life (§8.1).
ALTER TABLE sessions ADD COLUMN case_id uuid;
CREATE INDEX sessions_case_idx ON sessions (case_id, updated_at DESC) WHERE case_id IS NOT NULL AND deleted_at IS NULL;
UPDATE sessions SET metadata = metadata - 'kb_ids' - 'kb_filter' WHERE metadata ?| array['kb_ids', 'kb_filter'];

-- +goose Down
DROP INDEX IF EXISTS sessions_case_idx;
ALTER TABLE sessions DROP COLUMN IF EXISTS case_id;
DROP INDEX IF EXISTS documents_case_created_idx;
DROP INDEX IF EXISTS documents_case_sha_uq;
UPDATE documents d SET metadata = d.metadata || jsonb_build_object('ma_ho_so', c.code)
FROM cases c WHERE c.id = d.case_id AND c.code <> '_UNASSIGNED';
ALTER TABLE documents DROP COLUMN IF EXISTS case_id;
CREATE UNIQUE INDEX IF NOT EXISTS documents_kb_sha_uq ON documents (kb_id, sha256) WHERE deleted_at IS NULL;
DROP TABLE IF EXISTS cases;
