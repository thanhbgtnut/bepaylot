-- +goose Up
-- U68–U70 (§6.9.6): a sheet is built from the document types and fields of
-- the KB, not from a sheet template. It keeps its own columns (the tables of
-- the template version it was made from, for the sheets that had one).
ALTER TABLE case_sheets ADD COLUMN columns jsonb NOT NULL DEFAULT '[]'; -- [{label,title,description,fields:[{key,label,value_type}]}]
UPDATE case_sheets s SET columns = v.tables FROM prompt_template_versions v
    WHERE v.template_id = s.template_id AND v.version = s.template_version;
ALTER TABLE case_sheets ALTER COLUMN template_id DROP NOT NULL, ALTER COLUMN template_version DROP NOT NULL;
ALTER TABLE field_corrections ALTER COLUMN template_id DROP NOT NULL, ALTER COLUMN template_version DROP NOT NULL;

-- +goose Down
ALTER TABLE case_sheets DROP COLUMN IF EXISTS columns;
