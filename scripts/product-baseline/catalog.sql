BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;

WITH table_metadata AS (
    SELECT
        pg_class.oid AS table_oid,
        pg_get_userbyid(pg_class.relowner) AS table_owner
    FROM pg_class
    JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace
    WHERE pg_namespace.nspname = 'public'
      AND pg_class.relname = 'product'
      AND pg_class.relkind IN ('r', 'p')
),
schema_parts AS (
    SELECT format(
        'column|%s|%s|%s|%s|%s|%s',
        ordinal_position,
        column_name,
        udt_name,
        COALESCE(character_maximum_length::text, ''),
        is_nullable,
        COALESCE(column_default, '')
    ) AS value
    FROM information_schema.columns
    WHERE table_schema = 'public'
      AND table_name = 'product'

    UNION ALL

    SELECT format(
        'constraint|%s|%s|%s',
        pg_constraint.conname,
        pg_constraint.contype,
        pg_get_constraintdef(pg_constraint.oid, true)
    )
    FROM pg_constraint
    JOIN table_metadata ON table_metadata.table_oid = pg_constraint.conrelid
),
catalog_rows AS (
    SELECT
        code,
        definition::text AS raw_definition,
        md5(definition::text) AS row_checksum
    FROM public.product
),
questions AS (
    SELECT catalog_rows.code, question.value AS question
    FROM catalog_rows
    CROSS JOIN LATERAL jsonb_array_elements(
        CASE
            WHEN jsonb_typeof(catalog_rows.raw_definition::jsonb -> 'questions') = 'array'
                THEN catalog_rows.raw_definition::jsonb -> 'questions'
            ELSE '[]'::jsonb
        END
    ) AS question(value)
),
covers AS (
    SELECT catalog_rows.code, cover.value AS cover
    FROM catalog_rows
    CROSS JOIN LATERAL jsonb_array_elements(
        CASE
            WHEN jsonb_typeof(catalog_rows.raw_definition::jsonb -> 'covers') = 'array'
                THEN catalog_rows.raw_definition::jsonb -> 'covers'
            ELSE '[]'::jsonb
        END
    ) AS cover(value)
)
SELECT jsonb_build_object(
    'observedAt', to_char(
        statement_timestamp() AT TIME ZONE 'UTC',
        'YYYY-MM-DD"T"HH24:MI:SS"Z"'
    ),
    'transactionIsolation', current_setting('transaction_isolation'),
    'transactionReadOnly', current_setting('transaction_read_only')::boolean,
    'transactionSnapshot', txid_current_snapshot()::text,
    'database', current_database(),
    'schema', current_schema(),
    'table', 'public.product',
    'tableOwner', (SELECT table_owner FROM table_metadata),
    'schemaIdentity', jsonb_build_object(
        'algorithm', 'MD5',
        'value', (SELECT md5(string_agg(value, E'\n' ORDER BY value)) FROM schema_parts)
    ),
    'dataIdentity', jsonb_build_object(
        'algorithm', 'MD5',
        'value', (
            SELECT md5(string_agg(code || ':' || row_checksum, ',' ORDER BY code))
            FROM catalog_rows
        ),
        'rowCount', (SELECT count(*) FROM catalog_rows),
        'codes', COALESCE((
            SELECT jsonb_agg(code ORDER BY code)
            FROM catalog_rows
        ), '[]'::jsonb)
    ),
    'rows', COALESCE((
        SELECT jsonb_agg(
            jsonb_build_object(
                'code', code,
                'rawLosslessDefinitionJson', raw_definition,
                'checksum', jsonb_build_object(
                    'algorithm', 'MD5',
                    'value', row_checksum
                )
            )
            ORDER BY code
        )
        FROM catalog_rows
    ), '[]'::jsonb),
    'coverage', jsonb_build_object(
        'questionTypes', COALESCE((
            SELECT jsonb_agg(DISTINCT question ->> 'type')
            FILTER (WHERE question ? 'type' AND question -> 'type' <> 'null'::jsonb)
            FROM questions
        ), '[]'::jsonb),
        'sumInsuredTokens', COALESCE((
            SELECT jsonb_agg(cover ->> 'sumInsured' ORDER BY code, cover ->> 'code')
            FILTER (WHERE cover ? 'sumInsured')
            FROM covers
        ), '[]'::jsonb),
        'products', COALESCE((
            SELECT jsonb_agg(
                jsonb_build_object(
                    'code', code,
                    'topLevelKeys', (
                        SELECT jsonb_agg(key ORDER BY key)
                        FROM jsonb_object_keys(raw_definition::jsonb) AS key
                    ),
                    'coversType', jsonb_typeof(raw_definition::jsonb -> 'covers'),
                    'coversCount', CASE
                        WHEN jsonb_typeof(raw_definition::jsonb -> 'covers') = 'array'
                            THEN jsonb_array_length(raw_definition::jsonb -> 'covers')
                        ELSE null
                    END,
                    'questionsType', jsonb_typeof(raw_definition::jsonb -> 'questions'),
                    'questionsCount', CASE
                        WHEN jsonb_typeof(raw_definition::jsonb -> 'questions') = 'array'
                            THEN jsonb_array_length(raw_definition::jsonb -> 'questions')
                        ELSE null
                    END
                )
                ORDER BY code
            )
            FROM catalog_rows
        ), '[]'::jsonb),
        'choiceCollections', COALESCE((
            SELECT jsonb_agg(
                jsonb_build_object(
                    'productCode', code,
                    'questionCode', question ->> 'code',
                    'choicesPresence', CASE
                        WHEN NOT (question ? 'choices') THEN 'absent'
                        WHEN question -> 'choices' = 'null'::jsonb THEN 'null'
                        ELSE jsonb_typeof(question -> 'choices')
                    END,
                    'choicesCount', CASE
                        WHEN jsonb_typeof(question -> 'choices') = 'array'
                            THEN jsonb_array_length(question -> 'choices')
                        ELSE null
                    END
                )
                ORDER BY code, question ->> 'code'
            )
            FROM questions
            WHERE question ->> 'type' = 'choice'
        ), '[]'::jsonb)
    )
)::text;

COMMIT;
