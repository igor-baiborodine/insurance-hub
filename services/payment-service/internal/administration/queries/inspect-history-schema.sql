-- Check the history table shape and owner before reading its rows.
SELECT history.relkind = 'r' AND history.relpersistence = 'p'
    AND history.relowner = 'payment'::regrole AND NOT history.relrowsecurity
    AND (
        SELECT count(*) = 2 AND bool_and(
            attnotnull AND (attname, atttypid) IN (
                ('version_id', 'bigint'::regtype), ('applied_at', 'timestamptz'::regtype)
            )
        )
        FROM pg_attribute
        WHERE attrelid = history.oid AND attnum > 0 AND NOT attisdropped
    )
    AND NOT EXISTS (SELECT FROM pg_trigger WHERE tgrelid = history.oid AND NOT tgisinternal)
FROM pg_class AS history
WHERE history.oid = 'public.payment_schema_version'::regclass
