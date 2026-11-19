ALTER TABLE messages
    ADD COLUMN resource_context JSONB;

ALTER TABLE messages
    ADD CONSTRAINT messages_resource_context_object
    CHECK (resource_context IS NULL OR jsonb_typeof(resource_context) = 'object');

ALTER TABLE messages DROP CONSTRAINT messages_has_content;
ALTER TABLE messages
    ADD CONSTRAINT messages_has_content
    CHECK (NULLIF(BTRIM(body), '') IS NOT NULL OR cardinality(image_urls) > 0 OR resource_context IS NOT NULL);

---- create above / drop below ----

ALTER TABLE messages DROP CONSTRAINT messages_has_content;
ALTER TABLE messages
    ADD CONSTRAINT messages_has_content
    CHECK (NULLIF(BTRIM(body), '') IS NOT NULL OR cardinality(image_urls) > 0);
ALTER TABLE messages DROP CONSTRAINT messages_resource_context_object;
ALTER TABLE messages DROP COLUMN resource_context;
