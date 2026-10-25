-- +goose Up
-- U94, U97, U98 (§6.9.10): document types become a catalogue of their owner,
-- shared by every knowledge base of that owner; a KB keeps only the codes it
-- uses (config.doc_types = [{name}]) and reads title, description and fields
-- from the catalogue. Each type has versioned AI instructions, sample
-- documents read by docpipe and the outputs of each version on them.
CREATE TABLE doc_types (
    owner_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code           text NOT NULL,
    title          text NOT NULL,
    description    text NOT NULL DEFAULT '',
    fields         jsonb NOT NULL DEFAULT '[]',   -- [{key, label, value_type, description}]
    prompt_version int  NOT NULL DEFAULT 1,       -- instructions in use
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_id, code)
);

CREATE TABLE doc_type_prompts (
    owner_id    uuid NOT NULL,
    code        text NOT NULL,
    version     int  NOT NULL,
    instruction text NOT NULL DEFAULT '',
    fields      jsonb NOT NULL DEFAULT '[]',      -- fields of this version (the sheet columns, U103)
    rules       jsonb NOT NULL DEFAULT '[]',      -- per-field rules, plain sentences
    shots       jsonb NOT NULL DEFAULT '[]',      -- [{sample_id, why}]: reviewed samples used as examples
    note        text NOT NULL DEFAULT '',
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_id, code, version),
    FOREIGN KEY (owner_id, code) REFERENCES doc_types (owner_id, code) ON DELETE CASCADE ON UPDATE CASCADE
);

CREATE TABLE doc_type_samples (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    uuid NOT NULL,
    code        text NOT NULL,
    file_name   text NOT NULL,
    storage_key text NOT NULL,
    mime_type   text NOT NULL DEFAULT '',
    status      text NOT NULL DEFAULT 'processing' CHECK (status IN ('processing','ready','failed')),
    error       text NOT NULL DEFAULT '',
    pages       jsonb NOT NULL DEFAULT '[]',      -- [{n, width, height, markdown, lines:[{n, text, conf, bbox}]}]
    truth       jsonb,                            -- reviewed values; NULL = not reviewed
    reviewed_by uuid REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at timestamptz,
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (owner_id, code) REFERENCES doc_types (owner_id, code) ON DELETE CASCADE ON UPDATE CASCADE
);
CREATE INDEX doc_type_samples_type_idx ON doc_type_samples (owner_id, code, created_at);

CREATE TABLE doc_type_runs (
    sample_id uuid NOT NULL REFERENCES doc_type_samples(id) ON DELETE CASCADE,
    version   int  NOT NULL,
    output    jsonb NOT NULL,                     -- {key: {value, page, line}}
    ran_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (sample_id, version)
);

-- Every type declared in a KB joins its owner's catalogue: the KB created
-- first gives title, description and fields; fields other KBs add under new
-- keys are appended (Q31).
INSERT INTO doc_types (owner_id, code, title, description, fields)
SELECT DISTINCT ON (k.owner_id, t->>'name') k.owner_id, t->>'name', COALESCE(NULLIF(t->>'title', ''), t->>'name'),
       COALESCE(t->>'description', ''), COALESCE(t->'fields', '[]')
FROM knowledge_bases k, jsonb_array_elements(CASE WHEN jsonb_typeof(k.config->'doc_types') = 'array' THEN k.config->'doc_types' ELSE '[]' END) t
WHERE COALESCE(t->>'name', '') <> ''
ORDER BY k.owner_id, t->>'name', k.created_at;

UPDATE doc_types d SET fields = d.fields || extra.f
FROM (
    SELECT owner_id, code, jsonb_agg(f) AS f FROM (
        SELECT DISTINCT ON (k.owner_id, t->>'name', f->>'key') k.owner_id, t->>'name' AS code, f
        FROM knowledge_bases k,
             jsonb_array_elements(CASE WHEN jsonb_typeof(k.config->'doc_types') = 'array' THEN k.config->'doc_types' ELSE '[]' END) t,
             jsonb_array_elements(COALESCE(t->'fields', '[]')) f
        ORDER BY k.owner_id, t->>'name', f->>'key', k.created_at
    ) x
    WHERE NOT EXISTS (SELECT 1 FROM doc_types d2, jsonb_array_elements(d2.fields) g
                      WHERE d2.owner_id = x.owner_id AND d2.code = x.code AND g->>'key' = x.f->>'key')
    GROUP BY owner_id, code
) extra
WHERE extra.owner_id = d.owner_id AND extra.code = d.code;

INSERT INTO doc_type_prompts (owner_id, code, version, instruction, fields, note)
SELECT owner_id, code, 1,
       'Bạn đọc một ' || lower(title) || ' và lấy ra các thông tin bên dưới.' || E'\n' ||
       'Dựa vào phần Nội dung; chữ số nào không rõ thì kiểm lại ở phần Các dòng.' || E'\n' ||
       'Chỉ ghi đúng giá trị có trên tài liệu. Thông tin nào không thấy thì để trống, không đoán.',
       fields, 'Bản đầu tiên'
FROM doc_types;

UPDATE knowledge_bases SET config = jsonb_set(config, '{doc_types}',
    COALESCE((SELECT jsonb_agg(jsonb_build_object('name', t->>'name')) FROM jsonb_array_elements(config->'doc_types') t), '[]'))
WHERE jsonb_typeof(config->'doc_types') = 'array';

-- +goose Down
UPDATE knowledge_bases k SET config = jsonb_set(k.config, '{doc_types}',
    COALESCE((SELECT jsonb_agg(jsonb_build_object('name', d.code, 'title', d.title, 'description', d.description, 'fields', d.fields))
              FROM jsonb_array_elements(k.config->'doc_types') t JOIN doc_types d ON d.owner_id = k.owner_id AND d.code = t->>'name'), '[]'))
WHERE jsonb_typeof(k.config->'doc_types') = 'array';
DROP TABLE IF EXISTS doc_type_runs;
DROP TABLE IF EXISTS doc_type_samples;
DROP TABLE IF EXISTS doc_type_prompts;
DROP TABLE IF EXISTS doc_types;
