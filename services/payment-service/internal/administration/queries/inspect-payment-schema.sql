-- Compare business columns and constraints exactly, not a fingerprint of PostgreSQL metadata.
WITH expected_column (tbl, col, typ, not_null, identity_id, has_default) AS (
    VALUES
        ('policy_account', 'id', 'bigint', true, true, false),
        ('policy_account', 'policy_number', 'character varying(255)', true, false, false),
        ('policy_account', 'policy_account_number', 'character varying(255)', true, false, false),
        ('policy_account', 'registered_premium', 'numeric(19,2)', true, false, false),
        ('policy_account', 'registered_cover_from', 'date', true, false, false),
        ('policy_account', 'created', 'timestamp without time zone', true, false, false),
        ('policy_account', 'updated', 'timestamp without time zone', true, false, false),
        ('statement_receipt', 'id', 'bigint', true, true, false),
        ('statement_receipt', 'source_name', 'character varying(255)', true, false, false),
        ('statement_receipt', 'sha256', 'character varying(64)', true, false, false),
        ('statement_receipt', 'state', 'character varying(16)', true, false, false),
        ('statement_receipt', 'created_at', 'timestamp with time zone', true, false, true),
        ('statement_receipt', 'updated_at', 'timestamp with time zone', true, false, true),
        ('accounting_entry', 'id', 'bigint', true, true, false),
        ('accounting_entry', 'policy_account_id', 'bigint', true, false, false),
        ('accounting_entry', 'kind', 'character varying(16)', true, false, false),
        ('accounting_entry', 'creation_date', 'date', true, false, false),
        ('accounting_entry', 'effective_date', 'date', true, false, false),
        ('accounting_entry', 'amount', 'numeric(19,2)', true, false, false),
        ('accounting_entry', 'statement_receipt_id', 'bigint', false, false, false)
), expected_constraint (
    tbl, kind, cols, ref_schema, ref_table, ref_cols, update_action, delete_action, validated
) AS (
    VALUES
        ('policy_account', 'p', '{id}', NULL, NULL, NULL, NULL, NULL, true),
        ('policy_account', 'u', '{policy_number}', NULL, NULL, NULL, NULL, NULL, true),
        ('policy_account', 'u', '{policy_account_number}', NULL, NULL, NULL, NULL, NULL, true),
        ('statement_receipt', 'p', '{id}', NULL, NULL, NULL, NULL, NULL, true),
        ('statement_receipt', 'u', '{source_name,sha256}', NULL, NULL, NULL, NULL, NULL, true),
        ('statement_receipt', 'c', '{sha256}', NULL, NULL, NULL, NULL, NULL, true),
        ('statement_receipt', 'c', '{state}', NULL, NULL, NULL, NULL, NULL, true),
        ('accounting_entry', 'p', '{id}', NULL, NULL, NULL, NULL, NULL, true),
        ('accounting_entry', 'f', '{policy_account_id}',
            'public', 'policy_account', '{id}', 'a', 'a', true),
        ('accounting_entry', 'f', '{statement_receipt_id}',
            'public', 'statement_receipt', '{id}', 'a', 'a', true),
        ('accounting_entry', 'c', '{kind}', NULL, NULL, NULL, NULL, NULL, true)
), expected_table AS (
    SELECT DISTINCT tbl FROM expected_column
), expected_check (tbl, cols, definition) AS (
    VALUES
        ('statement_receipt', '{sha256}',
            'CHECK (sha256::text ~ ''^[0-9a-f]{64}$''::text)'),
        ('statement_receipt', '{state}',
            'CHECK (state::text = ANY (ARRAY[''review''::character varying, '
            || '''posted''::character varying, ''archived''::character varying]::text[]))'),
        ('accounting_entry', '{kind}',
            'CHECK (kind::text = ANY (ARRAY[''expected_payment''::character varying, '
            || '''in_payment''::character varying, ''out_payment''::character varying]::text[]))')
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
), actual_check AS (
    SELECT r.tbl,
        (SELECT array_agg(a.attname::text ORDER BY k.ord)
         FROM unnest(con.conkey) WITH ORDINALITY AS k (attnum, ord)
         JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum)::text,
        pg_get_constraintdef(con.oid, true)
    FROM relation r JOIN pg_constraint con ON con.conrelid = r.oid
    WHERE con.contype = 'c'
)
SELECT (SELECT count(*) FROM relation) = (SELECT count(*) FROM expected_table)
    AND NOT EXISTS (
        SELECT FROM relation r JOIN pg_class c ON c.oid = r.oid
        WHERE c.relkind <> 'r' OR c.relpersistence <> 'p' OR c.relowner <> 'payment'::regrole
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
    AND NOT EXISTS (
        (SELECT * FROM expected_check EXCEPT SELECT * FROM actual_check)
        UNION ALL
        (SELECT * FROM actual_check EXCEPT SELECT * FROM expected_check)
    )
