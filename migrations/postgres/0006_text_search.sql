-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS unaccent;

-- unaccent_vi folds Vietnamese diacritics (đ → d) and lower-cases. It is
-- declared IMMUTABLE so it can back generated columns and expression indexes.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION unaccent_vi(text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $$
    SELECT lower(public.unaccent('public.unaccent'::regdictionary,
                                 replace(replace($1, 'đ', 'd'), 'Đ', 'D')))
$$;
-- +goose StatementEnd

-- jsonb_values_text flattens the scalar values of a JSON object into one
-- string so metadata values are full-text searchable.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION jsonb_values_text(jsonb) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT coalesce(string_agg(v, ' '), '')
    FROM jsonb_each_text(CASE WHEN jsonb_typeof($1) = 'object' THEN $1 ELSE '{}'::jsonb END) AS e(k, v)
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS jsonb_values_text(jsonb);
DROP FUNCTION IF EXISTS unaccent_vi(text);
