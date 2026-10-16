-- +goose Up
-- U85 (§6.9.8): Excel forms of a KB. The uploaded file stays in S3; the
-- regions the code found in it and how its cells are filled are structured
-- data, kept here.
CREATE TABLE export_templates (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kb_id       uuid NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    name        text NOT NULL,
    file_name   text NOT NULL,
    object_key  text NOT NULL,
    layout      jsonb NOT NULL, -- {sheet,title,header_top,header_bottom,data_start,data_rows,total_row,columns,placeholders}
    mapping     jsonb NOT NULL, -- {doc_type,columns:[{col,kind,key,formula,total}],placeholders:[{cell,doc_type,key,text}]}
    created_by  uuid NOT NULL REFERENCES users(id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX export_templates_kb_idx ON export_templates (kb_id, created_at);

-- The form a sheet was built for: its Studio row downloads that form.
ALTER TABLE case_sheets ADD COLUMN export_template_id uuid REFERENCES export_templates(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE case_sheets DROP COLUMN IF EXISTS export_template_id;
DROP TABLE IF EXISTS export_templates;
