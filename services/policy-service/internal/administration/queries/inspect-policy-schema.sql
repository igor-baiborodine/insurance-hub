-- Compare business columns and constraints exactly, not a fingerprint of PostgreSQL metadata.
WITH expected_column (tbl, col, typ, not_null, identity_id, has_default) AS (
    VALUES
        ('offer', 'id', 'bigint', true, true, false),
        ('offer', 'creation_date', 'date', false, false, false),
        ('offer', 'number', 'character varying(255)', false, false, false),
        ('offer', 'policy_from', 'date', false, false, false),
        ('offer', 'policy_to', 'date', false, false, false),
        ('offer', 'product_code', 'character varying(255)', false, false, false),
        ('offer', 'status', 'character varying(255)', false, false, false),
        ('offer', 'total_price', 'numeric(19,2)', false, false, false),
        ('offer_answers', 'offer_id', 'bigint', true, false, false),
        ('offer_answers', 'answer', 'character varying(255)', false, false, false),
        ('offer_answers', 'question_code', 'character varying(255)', true, false, false),
        ('offer_cover', 'offer_id', 'bigint', true, false, false),
        ('offer_cover', 'price', 'numeric(19,2)', false, false, false),
        ('offer_cover', 'cover_code', 'character varying(255)', true, false, false),
        ('policy', 'id', 'bigint', true, true, false),
        ('policy', 'agent_login', 'character varying(255)', false, false, false),
        ('policy', 'number', 'character varying(255)', false, false, false),
        ('policy_version', 'id', 'bigint', true, true, false),
        ('policy_version', 'account_number', 'character varying(255)', false, false, false),
        ('policy_version', 'cover_from', 'date', false, false, false),
        ('policy_version', 'cover_to', 'date', false, false, false),
        ('policy_version', 'first_name', 'character varying(255)', false, false, false),
        ('policy_version', 'last_name', 'character varying(255)', false, false, false),
        ('policy_version', 'pesel', 'character varying(255)', false, false, false),
        ('policy_version', 'product_code', 'character varying(255)', false, false, false),
        ('policy_version', 'total_premium_amount', 'numeric(19,2)', false, false, false),
        ('policy_version', 'version_number', 'bigint', false, false, false),
        ('policy_version', 'version_from', 'date', false, false, false),
        ('policy_version', 'version_to', 'date', false, false, false),
        ('policy_version', 'policy_id', 'bigint', false, false, false),
        ('cover', 'id', 'bigint', true, true, false),
        ('cover', 'code', 'character varying(255)', false, false, false),
        ('cover', 'price', 'numeric(19,2)', false, false, false),
        ('cover', 'policy_version_id', 'bigint', false, false, false),
        ('policy_outbox', 'id', 'bigint', true, true, false),
        ('policy_outbox', 'intent_id', 'uuid', true, false, false),
        ('policy_outbox', 'policy_id', 'bigint', true, false, false),
        ('policy_outbox', 'topic', 'character varying(64)', true, false, false),
        ('policy_outbox', 'message_key', 'character varying(255)', true, false, false),
        ('policy_outbox', 'payload', 'jsonb', true, false, false),
        ('policy_outbox', 'trace_parent', 'character varying(55)', false, false, false),
        ('policy_outbox', 'trace_state', 'character varying(512)', false, false, false),
        ('policy_outbox', 'created_at', 'timestamp with time zone', true, false, true),
        ('policy_outbox', 'published_at', 'timestamp with time zone', false, false, false)
), expected_constraint (
    tbl, kind, cols, ref_schema, ref_table, ref_cols, update_action, delete_action, validated
) AS (
    VALUES
        ('offer', 'p', '{id}', NULL, NULL, NULL, NULL, NULL, true),
        ('offer_answers', 'p', '{offer_id,question_code}',
            NULL, NULL, NULL, NULL, NULL, true),
        ('offer_answers', 'f', '{offer_id}',
            'public', 'offer', '{id}', 'a', 'a', true),
        ('offer_cover', 'p', '{offer_id,cover_code}',
            NULL, NULL, NULL, NULL, NULL, true),
        ('offer_cover', 'f', '{offer_id}',
            'public', 'offer', '{id}', 'a', 'a', true),
        ('policy', 'p', '{id}', NULL, NULL, NULL, NULL, NULL, true),
        ('policy_version', 'p', '{id}', NULL, NULL, NULL, NULL, NULL, true),
        ('policy_version', 'f', '{policy_id}',
            'public', 'policy', '{id}', 'a', 'a', true),
        ('cover', 'p', '{id}', NULL, NULL, NULL, NULL, NULL, true),
        ('cover', 'f', '{policy_version_id}',
            'public', 'policy_version', '{id}', 'a', 'a', true),
        ('policy_outbox', 'p', '{id}', NULL, NULL, NULL, NULL, NULL, true),
        ('policy_outbox', 'u', '{intent_id}', NULL, NULL, NULL, NULL, NULL, true),
        ('policy_outbox', 'f', '{policy_id}',
            'public', 'policy', '{id}', 'a', 'a', true),
        ('policy_outbox', 'c', '{topic}', NULL, NULL, NULL, NULL, NULL, true)
), expected_table AS (
    SELECT DISTINCT tbl FROM expected_column
), relation AS (
    SELECT c.oid, c.relname::text AS tbl
    FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'public' AND c.relname IN (SELECT tbl FROM expected_table)
), actual_column AS (
    SELECT r.tbl, a.attname::text, format_type(a.atttypid, a.atttypmod), a.attnotnull,
        a.attidentity = 'a', a.atthasdef
    FROM relation r JOIN pg_attribute a ON a.attrelid = r.oid
    WHERE a.attnum > 0 AND NOT a.attisdropped
), actual_constraint AS (
    SELECT r.tbl, con.contype::text,
        (SELECT array_agg(a.attname::text ORDER BY k.ord)
         FROM unnest(con.conkey) WITH ORDINALITY AS k (attnum, ord)
         JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum)::text,
        referenced_namespace.nspname::text,
        referenced.relname::text,
        (SELECT array_agg(a.attname::text ORDER BY k.ord)
         FROM unnest(con.confkey) WITH ORDINALITY AS k (attnum, ord)
         JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.attnum)::text,
        CASE WHEN con.contype = 'f' THEN con.confupdtype::text END,
        CASE WHEN con.contype = 'f' THEN con.confdeltype::text END,
        con.convalidated
    FROM relation r JOIN pg_constraint con ON con.conrelid = r.oid
    LEFT JOIN pg_class referenced ON referenced.oid = con.confrelid
    LEFT JOIN pg_namespace referenced_namespace ON referenced_namespace.oid = referenced.relnamespace
    WHERE con.contype <> 'n'
)
SELECT (SELECT count(*) FROM relation) = (SELECT count(*) FROM expected_table)
    AND NOT EXISTS (
        SELECT FROM relation r JOIN pg_class c ON c.oid = r.oid
        WHERE c.relkind <> 'r' OR c.relpersistence <> 'p' OR c.relowner <> 'policy'::regrole
           OR c.relrowsecurity OR c.relforcerowsecurity
           OR EXISTS (SELECT FROM pg_trigger WHERE tgrelid = c.oid AND NOT tgisinternal)
           OR EXISTS (SELECT FROM pg_inherits WHERE inhrelid = c.oid OR inhparent = c.oid)
    )
    AND NOT EXISTS (
        (SELECT * FROM expected_column EXCEPT SELECT * FROM actual_column)
        UNION ALL
        (SELECT * FROM actual_column EXCEPT SELECT * FROM expected_column)
    )
    AND NOT EXISTS (
        (SELECT * FROM expected_constraint EXCEPT SELECT * FROM actual_constraint)
        UNION ALL
        (SELECT * FROM actual_constraint EXCEPT SELECT * FROM expected_constraint)
    )
