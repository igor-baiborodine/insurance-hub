-- $1 selects the managed shape: identity IDs and the outbox. Otherwise the Java shape applies.
-- Compare business columns and constraints exactly, not a fingerprint of PostgreSQL metadata.
WITH expected_column (tbl, col, typ, not_null, identity_id, has_default, managed_only) AS (
    VALUES
        ('offer', 'id', 'bigint', true, true, false, false),
        ('offer', 'creation_date', 'date', false, false, false, false),
        ('offer', 'number', 'character varying(255)', false, false, false, false),
        ('offer', 'policy_from', 'date', false, false, false, false),
        ('offer', 'policy_to', 'date', false, false, false, false),
        ('offer', 'product_code', 'character varying(255)', false, false, false, false),
        ('offer', 'status', 'character varying(255)', false, false, false, false),
        ('offer', 'total_price', 'numeric(19,2)', false, false, false, false),
        ('offer_answers', 'offer_id', 'bigint', true, false, false, false),
        ('offer_answers', 'answer', 'character varying(255)', false, false, false, false),
        ('offer_answers', 'question_code', 'character varying(255)', true, false, false, false),
        ('offer_cover', 'offer_id', 'bigint', true, false, false, false),
        ('offer_cover', 'price', 'numeric(19,2)', false, false, false, false),
        ('offer_cover', 'cover_code', 'character varying(255)', true, false, false, false),
        ('policy', 'id', 'bigint', true, true, false, false),
        ('policy', 'agent_login', 'character varying(255)', false, false, false, false),
        ('policy', 'number', 'character varying(255)', false, false, false, false),
        ('policy_version', 'id', 'bigint', true, true, false, false),
        ('policy_version', 'account_number', 'character varying(255)', false, false, false, false),
        ('policy_version', 'cover_from', 'date', false, false, false, false),
        ('policy_version', 'cover_to', 'date', false, false, false, false),
        ('policy_version', 'first_name', 'character varying(255)', false, false, false, false),
        ('policy_version', 'last_name', 'character varying(255)', false, false, false, false),
        ('policy_version', 'pesel', 'character varying(255)', false, false, false, false),
        ('policy_version', 'product_code', 'character varying(255)', false, false, false, false),
        ('policy_version', 'total_premium_amount', 'numeric(19,2)', false, false, false, false),
        ('policy_version', 'version_number', 'bigint', false, false, false, false),
        ('policy_version', 'version_from', 'date', false, false, false, false),
        ('policy_version', 'version_to', 'date', false, false, false, false),
        ('policy_version', 'policy_id', 'bigint', false, false, false, false),
        ('cover', 'id', 'bigint', true, true, false, false),
        ('cover', 'code', 'character varying(255)', false, false, false, false),
        ('cover', 'price', 'numeric(19,2)', false, false, false, false),
        ('cover', 'policy_version_id', 'bigint', false, false, false, false),
        ('policy_outbox', 'id', 'bigint', true, true, false, true),
        ('policy_outbox', 'intent_id', 'uuid', true, false, false, true),
        ('policy_outbox', 'policy_id', 'bigint', true, false, false, true),
        ('policy_outbox', 'topic', 'character varying(64)', true, false, false, true),
        ('policy_outbox', 'message_key', 'character varying(255)', true, false, false, true),
        ('policy_outbox', 'payload', 'jsonb', true, false, false, true),
        ('policy_outbox', 'created_at', 'timestamp with time zone', true, false, true, true),
        ('policy_outbox', 'published_at', 'timestamp with time zone', false, false, false, true)
), expected_constraint (tbl, kind, cols, ref, managed_only) AS (
    VALUES
        ('offer', 'p', '{id}', NULL, false),
        ('offer_answers', 'p', '{offer_id,question_code}', NULL, false),
        ('offer_answers', 'f', '{offer_id}', 'offer', false),
        ('offer_cover', 'p', '{offer_id,cover_code}', NULL, false),
        ('offer_cover', 'f', '{offer_id}', 'offer', false),
        ('policy', 'p', '{id}', NULL, false),
        ('policy_version', 'p', '{id}', NULL, false),
        ('policy_version', 'f', '{policy_id}', 'policy', false),
        ('cover', 'p', '{id}', NULL, false),
        ('cover', 'f', '{policy_version_id}', 'policy_version', false),
        ('policy_outbox', 'p', '{id}', NULL, true),
        ('policy_outbox', 'u', '{intent_id}', NULL, true),
        ('policy_outbox', 'f', '{policy_id}', 'policy', true),
        ('policy_outbox', 'c', '{topic}', NULL, true)
), managed_table AS (
    SELECT DISTINCT tbl FROM expected_column WHERE NOT managed_only OR $1
), relation AS (
    SELECT c.oid, c.relname::text AS tbl
    FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'public' AND c.relname IN (SELECT tbl FROM managed_table)
), actual_column AS (
    SELECT r.tbl, a.attname::text, format_type(a.atttypid, a.atttypmod), a.attnotnull,
        a.attidentity = 'a', a.atthasdef
    FROM relation r JOIN pg_attribute a ON a.attrelid = r.oid
    WHERE a.attnum > 0 AND NOT a.attisdropped
), wanted_column AS (
    SELECT tbl, col, typ, not_null, identity_id AND $1, has_default
    FROM expected_column WHERE NOT managed_only OR $1
), actual_constraint AS (
    SELECT r.tbl, con.contype::text,
        (SELECT array_agg(a.attname::text ORDER BY k.ord)
         FROM unnest(con.conkey) WITH ORDINALITY AS k (attnum, ord)
         JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum)::text,
        referenced.relname::text
    FROM relation r JOIN pg_constraint con ON con.conrelid = r.oid
    LEFT JOIN pg_class referenced ON referenced.oid = con.confrelid
    WHERE con.contype <> 'n'
), wanted_constraint AS (
    SELECT tbl, kind, cols, ref FROM expected_constraint WHERE NOT managed_only OR $1
)
SELECT (SELECT count(*) FROM relation) = (SELECT count(*) FROM managed_table)
    AND NOT EXISTS (
        SELECT FROM relation r JOIN pg_class c ON c.oid = r.oid
        WHERE c.relkind <> 'r' OR c.relpersistence <> 'p' OR c.relowner <> 'policy'::regrole
           OR c.relrowsecurity OR c.relforcerowsecurity
           OR EXISTS (SELECT FROM pg_trigger WHERE tgrelid = c.oid AND NOT tgisinternal)
           OR EXISTS (SELECT FROM pg_inherits WHERE inhrelid = c.oid OR inhparent = c.oid)
    )
    AND NOT EXISTS (
        (SELECT * FROM wanted_column EXCEPT SELECT * FROM actual_column)
        UNION ALL
        (SELECT * FROM actual_column EXCEPT SELECT * FROM wanted_column)
    )
    AND NOT EXISTS (
        (SELECT * FROM wanted_constraint EXCEPT SELECT * FROM actual_constraint)
        UNION ALL
        (SELECT * FROM actual_constraint EXCEPT SELECT * FROM wanted_constraint)
    )
