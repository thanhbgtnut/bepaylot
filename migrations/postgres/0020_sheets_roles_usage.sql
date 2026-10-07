-- +goose Up
-- U43–U46: roles and prompt templates (§8.4), case sheets and the corrections
-- users make to AI values (§6.9.6), and cost per model call (§8.5).
ALTER TABLE users
    ADD COLUMN role text NOT NULL DEFAULT 'user' CHECK (role IN ('admin','prompt_editor','user')),
    ADD COLUMN monthly_limit numeric(16,0);            -- usage.currency; NULL = no limit
ALTER TABLE api_keys ADD COLUMN issued_by uuid REFERENCES users(id) ON DELETE SET NULL;

CREATE TABLE prompt_templates (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind            text NOT NULL CHECK (kind IN ('chat','sheet')),
    slug            text NOT NULL UNIQUE,
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    case_type       text,                              -- NULL = every case type
    status          text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','archived')),
    current_version int,                               -- the published version
    created_by      uuid NOT NULL REFERENCES users(id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE prompt_template_versions (               -- immutable: never UPDATEd
    template_id uuid NOT NULL REFERENCES prompt_templates(id) ON DELETE CASCADE,
    version     int NOT NULL,
    body        text NOT NULL,
    fields      jsonb NOT NULL DEFAULT '[]',          -- kind=sheet: [{key,label,value_type}] in order
    created_by  uuid NOT NULL REFERENCES users(id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (template_id, version)
);
ALTER TABLE sessions ADD COLUMN template_id uuid REFERENCES prompt_templates(id) ON DELETE SET NULL;

CREATE TABLE case_sheets (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id          uuid NOT NULL,                    -- no FK, like documents (U21)
    template_id      uuid NOT NULL REFERENCES prompt_templates(id),
    template_version int NOT NULL,
    name             text NOT NULL,
    status           text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','done','failed')),
    filled           int NOT NULL DEFAULT 0,
    total            int NOT NULL DEFAULT 0,
    rows             jsonb NOT NULL DEFAULT '[]',      -- [{key,label,value_type,field_id,ai_field_id,ai_value_text,note}]
    error            text NOT NULL DEFAULT '',
    created_by       uuid NOT NULL REFERENCES users(id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX case_sheets_case_idx ON case_sheets (case_id, created_at DESC);

CREATE TABLE field_corrections (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    sheet_id         uuid NOT NULL REFERENCES case_sheets(id) ON DELETE CASCADE,
    template_id      uuid NOT NULL,
    template_version int NOT NULL,
    key              text NOT NULL,
    ai_field_id      uuid REFERENCES extracted_fields(id) ON DELETE SET NULL,
    user_field_id    uuid REFERENCES extracted_fields(id) ON DELETE SET NULL,
    ai_value_text    text NOT NULL,
    user_value_text  text NOT NULL,
    origin           text NOT NULL CHECK (origin IN ('page','xlsx')),
    reverted         boolean NOT NULL DEFAULT false,
    user_id          uuid NOT NULL REFERENCES users(id),
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX field_corrections_tpl_idx ON field_corrections (template_id, template_version, key);

CREATE TABLE usage_events (
    id         bigserial PRIMARY KEY,
    user_id    uuid REFERENCES users(id) ON DELETE CASCADE, -- NULL = not attributed (system)
    api_key_id uuid REFERENCES api_keys(id) ON DELETE SET NULL,
    case_id    uuid,
    kind       text NOT NULL CHECK (kind IN ('chat','sheet','parse')),
    model      text NOT NULL,
    tokens_in  int NOT NULL,
    tokens_out int NOT NULL,
    cost       numeric(16,2) NOT NULL,                 -- usage.currency
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX usage_events_user_idx ON usage_events (user_id, created_at);
CREATE INDEX usage_events_created_idx ON usage_events (created_at);

-- +goose Down
DROP TABLE IF EXISTS usage_events;
DROP TABLE IF EXISTS field_corrections;
DROP TABLE IF EXISTS case_sheets;
ALTER TABLE sessions DROP COLUMN IF EXISTS template_id;
DROP TABLE IF EXISTS prompt_template_versions;
DROP TABLE IF EXISTS prompt_templates;
ALTER TABLE api_keys DROP COLUMN IF EXISTS issued_by;
ALTER TABLE users DROP COLUMN IF EXISTS monthly_limit, DROP COLUMN IF EXISTS role;
