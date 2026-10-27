-- Computes the accepted Product column and constraint identity.
WITH schema_parts AS (
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
        conname,
        contype,
        pg_get_constraintdef(oid, true)
    )
    FROM pg_constraint
    WHERE conrelid = to_regclass('public.product')
)
SELECT md5(string_agg(value, E'\n' ORDER BY value))
FROM schema_parts
