-- +goose Up
CREATE TABLE graph_schemas (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    version    int NOT NULL,
    spec       jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (name, version)
);

CREATE TABLE kg_entities (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kb_id              uuid NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    schema_id          uuid NOT NULL REFERENCES graph_schemas(id),
    type               text NOT NULL,
    name               text NOT NULL,
    norm_key           text NOT NULL,
    aliases            text[] NOT NULL DEFAULT '{}',
    attributes         jsonb NOT NULL DEFAULT '{}',
    attributes_history jsonb NOT NULL DEFAULT '[]',
    conflict           boolean NOT NULL DEFAULT false,
    summary            text NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kb_id, type, norm_key)
);
CREATE INDEX kg_entities_name_trgm ON kg_entities USING gin (unaccent_vi(name) gin_trgm_ops);

CREATE TABLE kg_relations (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kb_id      uuid NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    type       text NOT NULL,
    source_id  uuid NOT NULL REFERENCES kg_entities(id) ON DELETE CASCADE,
    target_id  uuid NOT NULL REFERENCES kg_entities(id) ON DELETE CASCADE,
    attributes jsonb NOT NULL DEFAULT '{}',
    UNIQUE (kb_id, type, source_id, target_id)
);
CREATE INDEX kg_relations_src ON kg_relations (source_id);
CREATE INDEX kg_relations_tgt ON kg_relations (target_id);

CREATE TABLE kg_mentions (
    id           bigserial PRIMARY KEY,
    entity_id    uuid REFERENCES kg_entities(id) ON DELETE CASCADE,
    relation_id  uuid REFERENCES kg_relations(id) ON DELETE CASCADE,
    document_id  uuid NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    gen          int NOT NULL,
    section_id   uuid,
    evidence     text NOT NULL,
    source_spans jsonb NOT NULL DEFAULT '[]',
    CHECK (entity_id IS NOT NULL OR relation_id IS NOT NULL)
);
CREATE INDEX kg_mentions_entity ON kg_mentions (entity_id);
CREATE INDEX kg_mentions_relation ON kg_mentions (relation_id);
CREATE INDEX kg_mentions_doc ON kg_mentions (document_id, gen);

CREATE TABLE wiki_pages (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kb_id            uuid NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    entity_id        uuid REFERENCES kg_entities(id) ON DELETE SET NULL,
    slug             text NOT NULL,
    title            text NOT NULL,
    page_type        text NOT NULL,
    summary          text NOT NULL DEFAULT '',
    content          text NOT NULL,
    aliases          text[] NOT NULL DEFAULT '{}',
    source_refs      text[] NOT NULL DEFAULT '{}',
    in_links         text[] NOT NULL DEFAULT '{}',
    out_links        text[] NOT NULL DEFAULT '{}',
    version          int NOT NULL DEFAULT 1,
    last_edit_source text NOT NULL DEFAULT 'system',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kb_id, slug)
);

CREATE TABLE wiki_page_revisions (
    page_id     uuid NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    version     int NOT NULL,
    content     text NOT NULL,
    summary     text NOT NULL DEFAULT '',
    edit_source text NOT NULL,
    editor_id   uuid,
    edited_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, version)
);

-- +goose Down
DROP TABLE IF EXISTS wiki_page_revisions;
DROP TABLE IF EXISTS wiki_pages;
DROP TABLE IF EXISTS kg_mentions;
DROP TABLE IF EXISTS kg_relations;
DROP TABLE IF EXISTS kg_entities;
DROP TABLE IF EXISTS graph_schemas;
