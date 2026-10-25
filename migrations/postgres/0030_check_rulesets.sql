-- +goose Up
-- U104, U105 (§6.9.9): cross-check rules become rule sets shared by every
-- knowledge base; a KB uses the sets that list it, and a run says which set
-- it ran. Each rule compares a field of document A with a field of document
-- B ({a, op, b}) or is a sentence the AI judges ({text, ai}).
CREATE TABLE check_rulesets (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    kb_ids      uuid[] NOT NULL DEFAULT '{}',   -- knowledge bases that use the set
    anchor      text NOT NULL DEFAULT '',       -- code of the anchor document type
    links       jsonb NOT NULL DEFAULT '[]',    -- [{doc_type, steps:[{id, text, formula|ai, explanation}]}]
    rules       jsonb NOT NULL DEFAULT '[]',    -- [{id, a:{type,key}, op, b:{type,key}, off} | {id, text, ai, off}]
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX check_rulesets_kbs_idx ON check_rulesets USING gin (kb_ids);

ALTER TABLE case_checks ADD COLUMN ruleset_id uuid REFERENCES check_rulesets(id) ON DELETE SET NULL;

-- The rules of each KB become one set used by that KB; earlier runs point to it.
INSERT INTO check_rulesets (name, kb_ids, anchor, links, rules, created_by)
SELECT 'Quy tắc của ' || k.name, ARRAY[k.id], k.config->'checks'->>'anchor',
       COALESCE(k.config->'checks'->'links', '[]'), COALESCE(k.config->'checks'->'rules', '[]'), k.owner_id
FROM knowledge_bases k
WHERE COALESCE(k.config->'checks'->>'anchor', '') <> '';

UPDATE case_checks cc SET ruleset_id = r.id
FROM cases c, check_rulesets r
WHERE c.id = cc.case_id AND r.kb_ids = ARRAY[c.kb_id] AND r.name LIKE 'Quy tắc của %';

UPDATE knowledge_bases SET config = config - 'checks' WHERE config ? 'checks';

-- +goose Down
UPDATE knowledge_bases k SET config = jsonb_set(k.config, '{checks}',
    jsonb_build_object('anchor', r.anchor, 'links', r.links, 'rules', r.rules))
FROM check_rulesets r WHERE r.kb_ids = ARRAY[k.id] AND r.name LIKE 'Quy tắc của %';
ALTER TABLE case_checks DROP COLUMN IF EXISTS ruleset_id;
DROP TABLE IF EXISTS check_rulesets;
