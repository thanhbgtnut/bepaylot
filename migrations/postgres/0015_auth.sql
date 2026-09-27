-- +goose Up
-- Login/register and OIDC sign-in (like WeKnora): users get a password hash
-- (empty = no local password, e.g. OIDC-only or seeded accounts) and the
-- issued JWTs are recorded so logout, refresh rotation and password changes
-- can revoke them.
ALTER TABLE users
    ADD COLUMN password_hash  text        NOT NULL DEFAULT '',
    ADD COLUMN auth_provider  text        NOT NULL DEFAULT 'local',
    ADD COLUMN oidc_subject   text,
    ADD COLUMN is_active      boolean     NOT NULL DEFAULT true,
    ADD COLUMN last_login_at  timestamptz,
    ADD COLUMN updated_at     timestamptz NOT NULL DEFAULT now();
CREATE UNIQUE INDEX users_oidc_subject_idx ON users (auth_provider, oidc_subject) WHERE oidc_subject IS NOT NULL;

CREATE TABLE auth_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,          -- sha256 of the JWT; the JWT itself is never stored
    token_type text NOT NULL CHECK (token_type IN ('access', 'refresh')),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX auth_tokens_user_idx ON auth_tokens (user_id) WHERE revoked_at IS NULL;
CREATE INDEX auth_tokens_expires_idx ON auth_tokens (expires_at);

-- Generated secrets that must be stable across restarts and replicas (the JWT
-- signing key when auth.jwt_secret is empty).
CREATE TABLE app_secrets (
    name       text PRIMARY KEY,
    value      text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS app_secrets;
DROP TABLE IF EXISTS auth_tokens;
DROP INDEX IF EXISTS users_oidc_subject_idx;
ALTER TABLE users
    DROP COLUMN IF EXISTS password_hash,
    DROP COLUMN IF EXISTS auth_provider,
    DROP COLUMN IF EXISTS oidc_subject,
    DROP COLUMN IF EXISTS is_active,
    DROP COLUMN IF EXISTS last_login_at,
    DROP COLUMN IF EXISTS updated_at;
